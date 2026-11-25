package supervise

// The tracker's server, as the supervisor runs it.
//
// The design is docs/designs/tracker-store-size.md, "Running the server", and
// what it may listen on is docs/decisions/developer-runs-and-local-services.md.
// The server is `dolt sql-server` over the store's own database directory. It is
// started detached, so it survives the supervisor as every part does, and a
// supervisor that comes back finds it through its record
// (internal/beads, trackerserver.go). It is a resident rather than one of the
// parts the services section declares: there is nothing about it to configure.
//
// dolt opens a TCP port whatever else it listens on, so the server listens on
// 127.0.0.1 at a port picked when it starts, and refuses every connection
// without its password. Everything that would let a process in is in one folder
// under the services folder, made again at each start:
//
//   - credentials, the password as bd reads it;
//   - privileges.db, the one account, made before the server starts by a dolt
//     given its statement on standard input, so the password is never in an
//     argument or an environment; the server makes no account of its own;
//   - local, where dolt writes the secret of the local superuser it makes for
//     itself. dolt writes that inside the directory it serves, at .dolt, so
//     .dolt is a link to here, put there before the server starts and taken out
//     when it stops. Without it the secret would be readable beside the store.
//
// The server is started and stopped only with the store lock held exclusive, so
// no harness call is in progress either way.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

const (
	// TrackerServerLockWait bounds waiting for the tracker calls in progress to
	// finish before the server is started or stopped.
	TrackerServerLockWait = 30 * time.Second
	trackerServerReady    = 30 * time.Second
	trackerServerStop     = 15 * time.Second
	trackerServerCommand  = 30 * time.Second
	// The wait after a start that failed doubles from the first to the last.
	trackerServerFirstWait = 5 * time.Second
	trackerServerLastWait  = 5 * time.Minute

	trackerServerLog  = "server.log"
	trackerServerInfo = "local/sql-server.info"
)

// LaunchDetached starts a process of its own that outlives this one, writing to
// log within logRoot, and returns its number.
type LaunchDetached func(program string, args []string, dir string, environ []string, logRoot, log string) (int, error)

// TrackerServer runs the tracker's server.
type TrackerServer struct {
	// Tracker is the client whose store is served, and whose lock is held.
	Tracker beads.Client
	// Repository is the checkout the store is in, asked whether bd's local
	// setting is kept out of Git.
	Repository string
	StateRoot  string
	Product    domain.ProductID
	// Program is the dolt to run, or why there is none to run.
	Program func() (string, error)
	// Hold is why no server is run, and empty where one may be.
	Hold string
	// Build is the build this supervisor runs; a server another build started is
	// started again.
	Build  string
	Runner execution.ProcessRunner
	Launch LaunchDetached
	Now    func() time.Time
	Log    func(format string, args ...any)

	failures int
	nextTry  time.Time
	said     string
}

// Name is how the resident is named in what the supervisor says.
func (t *TrackerServer) Name() string { return "tracker-server" }

// Look starts the server where none is running, and again where it has died or
// another build started it; where it is held, it stops one it finds.
func (t *TrackerServer) Look(ctx context.Context, now time.Time) {
	if now.Before(t.nextTry) {
		return
	}
	said, err := t.look(ctx)
	if err != nil {
		t.failures++
		wait := trackerServerFirstWait
		for i := 1; i < t.failures && wait < trackerServerLastWait; i++ {
			wait = min(wait*2, trackerServerLastWait)
		}
		t.nextTry = now.Add(wait)
		said = fmt.Sprintf("the tracker's server could not be brought to where it should be: %v", err)
	} else {
		t.failures, t.nextTry = 0, time.Time{}
	}
	if said != "" && said != t.said && t.Log != nil {
		t.Log("%s", said)
	}
	t.said = said
}

func (t *TrackerServer) look(ctx context.Context) (string, error) {
	store, database := t.Tracker.LiveStore()
	if store == "" || filepath.Base(database) != beads.EmbeddedDatabase {
		// No store, or one bd already reaches through a server of its own.
		return "", nil
	}
	record, found, err := beads.ReadServerRecord(store)
	if err == nil && t.Hold != "" && (!found || record.State == beads.ServerStopped) {
		return "", nil
	}
	if err == nil && t.Hold == "" && record.State == beads.ServerRunning && record.Build == t.Build && beads.ServerAnswers(ctx, record.Port) {
		return "", nil
	}
	if t.Hold != "" {
		return t.Stop(ctx, t.Hold)
	}
	if _, err := t.Program(); err != nil {
		// Recorded, so the surfaces say why the store is opened directly.
		if found && record.State == beads.ServerStopped && record.Reason == err.Error() {
			return "", nil
		}
		return t.Stop(ctx, err.Error())
	}
	unlock, err := t.Tracker.HoldStore(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	if _, err := t.StopHeld(ctx, "the server is being started again"); err != nil {
		return "", err
	}
	if err := t.StartHeld(ctx); err != nil {
		return "", err
	}
	record, _, err = beads.ReadServerRecord(store)
	return fmt.Sprintf("the tracker's server is running as process %d on port %d", record.PID, record.Port), err
}

// Stop stops the server and returns the store to direct opening, recording why.
// A store that has never had a server is left as it is, but for the reason.
func (t *TrackerServer) Stop(ctx context.Context, why string) (string, error) {
	unlock, err := t.Tracker.HoldStore(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	running, err := t.StopHeld(ctx, why)
	if err != nil || !running {
		return "", err
	}
	return "the tracker's server was stopped: " + why, nil
}

// StopIfRunning is Stop where the record says anything but stopped, and nothing
// where there is no record.
func (t *TrackerServer) StopIfRunning(ctx context.Context, why string) (string, error) {
	store, _ := t.Tracker.LiveStore()
	if store == "" {
		return "", nil
	}
	record, found, err := beads.ReadServerRecord(store)
	if err == nil && (!found || record.State == beads.ServerStopped) {
		return "", nil
	}
	return t.Stop(ctx, why)
}

// StopHeld is Stop with the store lock already held. It reports whether the
// record said a server was running, and a server whose process cannot be ended
// is not recorded as stopped.
func (t *TrackerServer) StopHeld(ctx context.Context, why string) (bool, error) {
	store, database := t.Tracker.LiveStore()
	if store == "" {
		return false, nil
	}
	// A record this cannot read is replaced once no server process is left.
	record, found, readErr := beads.ReadServerRecord(store)
	running := readErr != nil || (found && record.State != beads.ServerStopped)
	pids := []int{record.PID}
	// A server started and not recorded, by a supervisor that died between the
	// two, is named by the file dolt keeps while it runs.
	if directory, err := t.directory(); err == nil {
		if left := infoPID(filepath.Join(directory, trackerServerInfo)); left != record.PID {
			pids = append(pids, left)
		}
	}
	for _, pid := range pids {
		if err := t.end(ctx, pid, database); err != nil {
			return running, err
		}
	}
	err := errors.Join(beads.RemoveLocalSetting(store), unlinkLocal(database))
	if err != nil {
		return running, err
	}
	return running, beads.WriteServerRecord(store, beads.ServerRecord{State: beads.ServerStopped, StoppedAt: t.now().UTC(), Reason: why})
}

// end stops process pid where it is a server holding database, and waits for it
// to be gone.
func (t *TrackerServer) end(ctx context.Context, pid int, database string) error {
	runs, err := t.Tracker.ServerProcessRuns(ctx, pid, database)
	if err != nil || !runs {
		return err
	}
	for _, ask := range []func(int) error{terminate, killTree} {
		if err := ask(pid); err != nil && !strings.Contains(err.Error(), "not running") {
			return fmt.Errorf("stop the tracker's server, process %d: %w", pid, err)
		}
		deadline := t.now().Add(trackerServerStop)
		for t.now().Before(deadline) {
			collectExited(pid)
			if runs, err := t.Tracker.ServerProcessRuns(ctx, pid, database); err == nil && !runs {
				return nil
			}
			time.Sleep(stopInterval)
		}
	}
	return fmt.Errorf("the tracker's server, process %d, did not stop", pid)
}

// StartHeld starts the server with the store lock already held, and returns
// once it takes connections and is recorded. A start that fails leaves the
// store opened directly, with why recorded.
func (t *TrackerServer) StartHeld(ctx context.Context) (err error) {
	if t.Hold != "" {
		return errors.New("the tracker's server is not switched on: " + t.Hold)
	}
	program, err := t.Program()
	if err != nil {
		return err
	}
	store, database := t.Tracker.LiveStore()
	if filepath.Base(database) != beads.EmbeddedDatabase {
		// The live rebuild keeps the directory's name, so only a store changed by
		// hand comes here.
		return fmt.Errorf("the tracker's store has no %s directory to serve", beads.EmbeddedDatabase)
	}
	defer func() {
		if err != nil {
			_, stopErr := t.StopHeld(context.WithoutCancel(ctx), "the server could not be started: "+err.Error())
			err = errors.Join(err, stopErr)
		}
	}()
	if err := t.settingKeptOutOfGit(ctx, store); err != nil {
		return err
	}
	if err := beads.WriteServerRecord(store, beads.ServerRecord{State: beads.ServerStarting, Build: t.Build}); err != nil {
		return err
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	directory, credentials, err := t.prepare(ctx, program, port)
	if err != nil {
		return err
	}
	if err := linkLocal(database, filepath.Join(directory, "local")); err != nil {
		return err
	}
	pid, err := t.Launch(program, []string{"sql-server",
		"--data-dir", database, "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"--doltcfg-dir", filepath.Join(directory, "cfg"),
		"--privilege-file", filepath.Join(directory, "privileges.db"),
		"--branch-control-file", filepath.Join(directory, "branch_control.db"),
		"--skip-root-user-initialization",
	}, directory, doltEnvironment(directory), directory, trackerServerLog)
	if err != nil {
		return err
	}
	record := beads.ServerRecord{State: beads.ServerRunning, PID: pid, Port: port, StartedAt: t.now().UTC(), Credentials: credentials, Build: t.Build}
	if err := t.ready(ctx, pid, port, database); err != nil {
		return err
	}
	if err := beads.WriteLocalSetting(store, record); err != nil {
		return err
	}
	return beads.WriteServerRecord(store, record)
}

// ready waits for the server to take connections while holding the database.
func (t *TrackerServer) ready(ctx context.Context, pid, port int, database string) error {
	deadline := t.now().Add(trackerServerReady)
	for {
		if beads.ServerAnswers(ctx, port) {
			runs, err := t.Tracker.ServerProcessRuns(ctx, pid, database)
			switch {
			case err != nil:
				return err
			case runs:
				return nil
			}
			// Another program took the port first, or the server has just died.
		}
		if collectExited(pid) || ctx.Err() != nil || !t.now().Before(deadline) {
			return fmt.Errorf("process %d ended, or did not take connections on port %d within %s; what it said is in %s in its folder under the services folder", pid, port, trackerServerReady, trackerServerLog)
		}
		time.Sleep(stopInterval)
	}
}

// directory is the server's own folder under the services folder.
func (t *TrackerServer) directory() (string, error) {
	services, err := execution.PrepareServicesDirectory(t.StateRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(services, t.directoryName()), nil
}

func (t *TrackerServer) directoryName() string { return "tracker-server-" + string(t.Product) }

// prepare makes the server's folder afresh, with a new password and the one
// account, and returns it and the credentials file in it.
func (t *TrackerServer) prepare(ctx context.Context, program string, port int) (directory, credentials string, err error) {
	if directory, err = t.directory(); err != nil {
		return "", "", err
	}
	services, err := repowrite.OpenPinnedRoot(filepath.Dir(directory))
	if err != nil {
		return "", "", err
	}
	defer services.Close()
	if err := services.RemoveAll(t.directoryName()); err != nil {
		return "", "", fmt.Errorf("clear the tracker server's folder: %w", err)
	}
	folder, err := services.CreateDirectory(t.directoryName())
	if err != nil {
		return "", "", fmt.Errorf("make the tracker server's folder: %w", err)
	}
	defer folder.Close()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", err
	}
	password := hex.EncodeToString(secret)
	for _, name := range []string{"local", "empty", "tmp"} {
		if err := folder.MakeDirectory(name, 0o700); err != nil {
			return "", "", err
		}
	}
	content := fmt.Sprintf("[127.0.0.1:%d]\npassword=%s\n", port, password)
	if err := folder.WriteFile("credentials", []byte(content), 0o600, true); err != nil {
		return "", "", err
	}
	account := fmt.Sprintf("CREATE USER '%[1]s'@'127.0.0.1' IDENTIFIED BY '%[2]s';\nGRANT ALL ON *.* TO '%[1]s'@'127.0.0.1' WITH GRANT OPTION;\n", beads.ServerUser, password)
	result, err := t.Runner.Run(ctx, execution.Command{
		Name: program,
		Args: []string{
			"--data-dir", filepath.Join(directory, "empty"),
			"--doltcfg-dir", filepath.Join(directory, "cfg"),
			"--privilege-file", filepath.Join(directory, "privileges.db"), "sql"},
		Dir:     filepath.Join(directory, "empty"),
		Env:     doltEnvironment(directory),
		Stdin:   strings.NewReader(account),
		Timeout: trackerServerCommand,
	}, nil)
	if err != nil || result.Status != execution.ProcessSucceeded {
		return "", "", fmt.Errorf("make the tracker server's account: %s", strings.ReplaceAll(describeFailure(result, err), password, "(password)"))
	}
	return directory, filepath.Join(directory, "credentials"), nil
}

// doltEnvironment keeps dolt's own settings and temporary files in the server's folder and stops it
// sending usage to its maker. It is nothing this process was given but a path.
func doltEnvironment(directory string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + directory, "DOLT_ROOT_PATH=" + directory, "TMPDIR=" + filepath.Join(directory, "tmp"), "DOLT_DISABLE_EVENT_FLUSH=1"}
}

// settingKeptOutOfGit refuses a checkout that would commit bd's local setting.
func (t *TrackerServer) settingKeptOutOfGit(ctx context.Context, store string) error {
	result, err := t.Runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    []string{"-C", t.Repository, "check-ignore", "--quiet", beads.LocalSettingPath(store)},
		Timeout: rebuildGitTimeout,
	}, nil)
	if err != nil || result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("git does not ignore %s, which bd's setting for reaching the server goes in, so it could be committed: %s", beads.LocalSettingPath(store), describeFailure(result, err))
	}
	return nil
}

// linkLocal puts the link dolt writes its own secret through in database.
func linkLocal(database, target string) error {
	if err := unlinkLocal(database); err != nil {
		return err
	}
	root, err := repowrite.OpenPinnedRoot(database)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Symlink(target, beads.ServerLink); err != nil {
		return fmt.Errorf("keep dolt's own secret out of %s: %w", database, err)
	}
	return nil
}

// unlinkLocal takes that link out, and refuses anything else in its place: dolt
// would write its secret there.
func unlinkLocal(database string) error {
	root, err := repowrite.OpenPinnedRoot(database)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(beads.ServerLink)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return fmt.Errorf("%s is not the link the harness puts there, so dolt would keep its own secret beside the store; take it out by hand", filepath.Join(database, beads.ServerLink))
	}
	return root.Remove(beads.ServerLink)
}

// infoPID is the process the file dolt keeps while it serves names, and zero
// where there is none.
func infoPID(path string) int {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	first, _, _ := strings.Cut(string(content), ":")
	pid, _ := strconv.Atoi(strings.TrimSpace(first))
	return pid
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("pick a port for the tracker's server: %w", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func (t *TrackerServer) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}
