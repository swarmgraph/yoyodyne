package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// Reaching the tracker's store through its server.
//
// bd opens its store afresh for every call, and pays for the store's journal
// each time. The server the supervisor runs (internal/supervise,
// trackerserver.go) opens it once, and every bd call is answered through it.
// The design is docs/designs/tracker-store-size.md, "Running the server".
//
// The supervisor keeps one record of the server inside the store's own
// directory, beside the harness's locks, so every harness process that reaches
// the store reads the same one whichever state directory it was given. It
// holds no password. A call reads it once it holds the store lock shared and
// has its turn:
//
//   - no record: no server has ever been run here, and bd is run as it always
//     was;
//   - stopped, and the server's process is gone: bd opens the store directly,
//     with server mode turned off whatever the machine's local setting says;
//   - running, and the server's port takes a connection: bd goes through it;
//   - anything else cannot tell whether a server holds the store, so the store
//     is never opened directly: the call lets the lock and its turn go, so the
//     supervisor can take the lock to start the server, and asks again up to
//     its own bound, after which it reads as unanswered.
//
// The supervisor starts and stops the server only while it holds the store lock
// exclusive (HoldStore), so what a call read stays true for as long as it runs,
// but for a server that dies.

// ServerHold is why the tracker's server is not run, and empty when it may be.
// The supervisor starts no server while it is set, and stops one it finds.
//
// dolt always opens a TCP port, so the server cannot listen on a socket file
// alone and has to be reached with a password
// (docs/decisions/developer-runs-and-local-services.md); until every role on
// both providers is refused the folder the password is kept in, a role could read and repeat it.
const ServerHold = "a role other than a developer can still read the folder its password would be kept in, until sessions on both Claude Code and Codex are refused it (yoyodyne-ifd.435.98 is the Codex half), and no real install of dolt 2.7.6 by the harness's setup has been recorded as checked"

// The states a server's record is in. Starting is written before the server's
// process is started, so a supervisor that died between the two leaves a record
// that does not say the server is stopped.
const (
	ServerStarting = "starting"
	ServerRunning  = "running"
	ServerStopped  = "stopped"
)

// ServerUser is the one account the server has.
const ServerUser = "yoyodyne"

// serverRecordName is the record, relative to the store.
const serverRecordName = writeLockDirectory + "/tracker-server.json"

const (
	serverDialLimit = 500 * time.Millisecond
	serverWaitPoll  = 250 * time.Millisecond
)

// ServerRecord is the supervisor's record of the tracker's server.
type ServerRecord struct {
	State string `json:"state"`
	// PID, Port and StartedAt are the server's process, the port it listens on
	// at 127.0.0.1, and when it was started. A record that says stopped, or being
	// started, keeps the number of the last process launched, so whether that
	// process is gone can be asked.
	PID       int       `json:"pid,omitempty"`
	Port      int       `json:"port,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
	// ProcessStarted is when the operating system says process PID started, which
	// tells it from a later process of the same number; zero where unknown.
	ProcessStarted time.Time `json:"process_started,omitzero"`
	// Credentials is the file bd reads the password from; the file's folder is
	// one no developer run can read.
	Credentials string `json:"credentials,omitempty"`
	// Build is the harness build that started it.
	Build string `json:"build,omitempty"`
	// StoppedAt and Reason are when and why it was stopped.
	StoppedAt time.Time `json:"stopped_at,omitzero"`
	Reason    string    `json:"reason,omitempty"`
}

// StoreServerSwitch stops and starts the tracker's server for the live rebuild,
// which already holds the store lock exclusive.
type StoreServerSwitch interface {
	// StopHeld stops the server and reports whether one was running and its
	// process has ended, which can be true beside an error. False with an error
	// is a process that may still be running.
	StopHeld(ctx context.Context, why string) (bool, error)
	StartHeld(ctx context.Context) error
}

// EmbeddedDatabase is the name of the database directory a server can be started
// on, and ServerLink the link inside it to the folder the server keeps its own
// secret in (internal/supervise, trackerserver.go).
const (
	EmbeddedDatabase = "embeddeddolt"
	ServerLink       = ".dolt"
	serverLinkPath   = EmbeddedDatabase + "/" + ServerLink
)

// LiveStore is the store this client's calls reach, and its database
// directory; both are empty where there is none.
func (c Client) LiveStore() (store, database string) {
	store = storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return "", ""
	}
	return store, databaseDirectory(store)
}

// HoldStore takes the store lock exclusive, so no harness call is in progress
// and none starts until what it returns is called.
func (c Client) HoldStore(ctx context.Context) (func(), error) { return c.lockStore(ctx, false) }

// ReadServerRecord is the record in store, and false where there is none.
func ReadServerRecord(store string) (ServerRecord, bool, error) {
	content, err := os.ReadFile(filepath.Join(store, serverRecordName))
	if errors.Is(err, fs.ErrNotExist) {
		return ServerRecord{}, false, nil
	}
	if err != nil {
		return ServerRecord{}, false, fmt.Errorf("read the record of the tracker's server: %w", err)
	}
	var record ServerRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return ServerRecord{}, false, fmt.Errorf("read the record of the tracker's server: %w", err)
	}
	switch record.State {
	case ServerStarting, ServerStopped:
	case ServerRunning:
		if record.PID <= 0 || record.Port <= 0 || record.Credentials == "" {
			return ServerRecord{}, false, errors.New("the record of the tracker's server says it is running and not where")
		}
	default:
		return ServerRecord{}, false, fmt.Errorf("the record of the tracker's server is in no state this build knows: %q", record.State)
	}
	return record, true, nil
}

// WriteServerRecord replaces the record in store.
func WriteServerRecord(store string, record ServerRecord) error {
	root, err := repowrite.NewRoot(store)
	if err != nil {
		return fmt.Errorf("record the tracker's server: %w", err)
	}
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("record the tracker's server: %w", err)
	}
	if _, err := root.WriteFile(serverRecordName, append(content, '\n')); err != nil {
		return fmt.Errorf("record the tracker's server: %w", err)
	}
	return nil
}

// ServerAnswers reports whether a connection to the server's port is taken.
func ServerAnswers(ctx context.Context, port int) bool {
	dialer := net.Dialer{Timeout: serverDialLimit}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

// ServerProcessRuns reports whether process pid is a server holding database.
// A process of that number holding no file of the database is another that
// took the number, or one that has exited and is not collected yet.
func (c Client) ServerProcessRuns(ctx context.Context, pid int, database string) (bool, error) {
	if !processExists(pid) {
		return false, nil
	}
	holders, err := c.StoreHolders(ctx, database)
	if err != nil {
		return false, fmt.Errorf("whether process %d is the tracker's server could not be told: %w", pid, err)
	}
	for _, holder := range holders {
		if holder.PID == pid {
			return true, nil
		}
	}
	return false, nil
}

// StoreHolders is every process that has database open.
func (c Client) StoreHolders(ctx context.Context, database string) ([]StoreHolder, error) {
	lookup, cancel := context.WithTimeout(ctx, holderLookupTimeout)
	defer cancel()
	return c.holdersFinder().Holders(lookup, database)
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

// serverBusy is a call that could not tell whether a server held the store for
// as long as it could wait, and so was never made. It reads as ErrUnanswered.
type serverBusy struct{ error }

func (b serverBusy) Unwrap() error        { return b.error }
func (b serverBusy) Is(target error) bool { return target == ErrUnanswered }

// enterStore takes what a call holds while bd runs: the store lock shared, its
// turn, and the way it reaches the store, as the environment bd is run with. It
// waits out a server that cannot be told running or stopped, as the comment at
// the top of this file says.
func (c Client) enterStore(ctx context.Context) (environment []string, leave func(), err error) {
	waiting, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	for {
		unlockStore, err := c.lockStore(ctx, true)
		if err != nil {
			return nil, nil, err
		}
		endTurn, err := c.takeTurn(ctx)
		if err != nil {
			unlockStore()
			return nil, nil, err
		}
		environment, unknown := c.reachStore(ctx)
		if unknown == "" {
			return environment, func() { endTurn(); unlockStore() }, nil
		}
		endTurn()
		unlockStore()
		select {
		case <-waiting.Done():
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			return nil, nil, serverBusy{fmt.Errorf("the tracker's server did not come back within %s, so this call was not made and the store was not opened directly: %s; the supervisor starts the server again, and `yoyo stop` returns the store to direct opening: %w", c.timeout(), unknown, waiting.Err())}
		case <-time.After(serverWaitPoll):
		}
	}
}

// reachStore is the environment bd is run with, or why it cannot be told how
// the store may be reached.
func (c Client) reachStore(ctx context.Context) (environment []string, unknown string) {
	environment = c.environment()
	if c.beadsDir != "" {
		return environment, ""
	}
	store, database := c.LiveStore()
	if store == "" {
		return environment, ""
	}
	record, found, err := ReadServerRecord(store)
	switch {
	case err != nil:
		return nil, err.Error()
	case !found:
		return environment, ""
	case record.State == ServerStopped:
		runs, err := c.ServerProcessRuns(ctx, record.PID, database)
		if err != nil {
			return nil, err.Error()
		}
		if runs {
			return nil, fmt.Sprintf("its record says it is stopped, and its process %d is still running", record.PID)
		}
		return append(withoutServerSettings(environment), "BEADS_DOLT_SERVER_MODE=0"), ""
	case record.State == ServerStarting:
		return nil, "its record says it is being started"
	case !ServerAnswers(ctx, record.Port):
		return nil, fmt.Sprintf("its record says it is running as process %d, and port %d takes no connection", record.PID, record.Port)
	}
	return append(withoutServerSettings(environment), serverSettings(record)...), ""
}

// serverSettings is how bd is told to reach the store through the server. The
// password is never in it: bd reads that from the credentials file.
func serverSettings(record ServerRecord) []string {
	return []string{
		"BEADS_DOLT_SERVER_MODE=1",
		"BEADS_DOLT_AUTO_START=0",
		"BEADS_DOLT_SHARED_SERVER=0",
		"BEADS_DOLT_SERVER_HOST=127.0.0.1",
		"BEADS_DOLT_SERVER_PORT=" + strconv.Itoa(record.Port),
		"BEADS_DOLT_SERVER_USER=" + ServerUser,
		"BEADS_CREDENTIALS_FILE=" + record.Credentials,
	}
}

// withoutServerSettings is an environment with everything that says how bd
// reaches a server taken out.
func withoutServerSettings(environment []string) []string {
	kept := make([]string, 0, len(environment)+8)
	for _, entry := range environment {
		if strings.HasPrefix(entry, "BEADS_DOLT_") || strings.HasPrefix(entry, "BEADS_CREDENTIALS_FILE=") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// StoreReach is how the tracker's store is reached, for a surface to say.
type StoreReach struct {
	// ThroughServer says calls go through the server, since Since.
	ThroughServer bool      `json:"through_server"`
	Since         time.Time `json:"since,omitzero"`
	PID           int       `json:"pid,omitempty"`
	Port          int       `json:"port,omitempty"`
	// Why is why the store is opened directly, or why calls are waiting.
	Why string `json:"why,omitempty"`
	// Waiting says it cannot be told whether the server is running, so calls
	// wait for it.
	Waiting bool `json:"waiting,omitempty"`
}

// Reach is how this client's store is reached, and false where it finds none.
func (c Client) Reach(ctx context.Context) (StoreReach, bool) {
	store, _ := c.LiveStore()
	if store == "" {
		return StoreReach{}, false
	}
	record, found, err := ReadServerRecord(store)
	_, unknown := c.reachStore(ctx)
	switch {
	case err != nil || unknown != "":
		return StoreReach{Waiting: true, Why: unknown, PID: record.PID, Port: record.Port}, true
	case found && record.State == ServerRunning:
		return StoreReach{ThroughServer: true, Since: record.StartedAt, PID: record.PID, Port: record.Port}, true
	case found:
		return StoreReach{Since: record.StoppedAt, Why: record.Reason}, true
	case ServerHold != "":
		return StoreReach{Why: "its server is not switched on: " + ServerHold}, true
	default:
		return StoreReach{Why: "the supervisor has not started its server"}, true
	}
}

// Says is the reach in one sentence, with no full stop.
func (r StoreReach) Says() string {
	switch {
	case r.Waiting:
		return "the tracker is not being reached: its calls wait for its server, because " + r.Why
	case r.ThroughServer:
		return fmt.Sprintf("the tracker is reached through its server, process %d on port %d, since %s", r.PID, r.Port, r.Since.Local().Format("2006-01-02 15:04 MST"))
	case !r.Since.IsZero():
		return fmt.Sprintf("the tracker's store is opened directly, since %s: %s", r.Since.Local().Format("2006-01-02 15:04 MST"), r.Why)
	default:
		return "the tracker's store is opened directly: " + r.Why
	}
}

// OutsideHolders is every process outside the harness that has the store's
// files open directly, and why that could not be told where it could not. It
// takes the store lock exclusive while it looks, so no call of the harness's
// own is among them, and leaves the tracker's server out.
func (c Client) OutsideHolders(ctx context.Context) ([]StoreHolder, string) {
	_, database := c.LiveStore()
	if database == "" {
		return nil, ""
	}
	unlock, err := c.HoldStore(ctx)
	if err != nil {
		return nil, err.Error()
	}
	defer unlock()
	return c.storeHolders(ctx, database)
}

// withoutServer is holders with the tracker's server left out.
func (c Client) withoutServer(holders []StoreHolder) []StoreHolder {
	store, _ := c.LiveStore()
	record, found, err := ReadServerRecord(store)
	if store == "" || err != nil || !found || record.State != ServerRunning {
		return holders
	}
	var others []StoreHolder
	for _, holder := range holders {
		if holder.PID != record.PID {
			others = append(others, holder)
		}
	}
	return others
}

// The local setting that sends every other bd on this machine through the
// server: the lines between these markers in the store's .env file, which bd
// reads and keeps out of Git. (metadata.json, which also has one, is committed.)
const (
	localSettingName  = ".env"
	localSettingBegin = "# begin yoyodyne tracker server (written by the supervisor; do not edit)"
	localSettingEnd   = "# end yoyodyne tracker server"
)

// LocalSettingPath is the local setting's file in store.
func LocalSettingPath(store string) string { return filepath.Join(store, localSettingName) }

// WriteLocalSetting writes the local setting for record, keeping every other
// line of the file. It refuses a file whose other lines already say how bd
// reaches a server, since bd would read both.
func WriteLocalSetting(store string, record ServerRecord) error {
	if strings.ContainsAny(record.Credentials, "\n\r\"'`#$\\") {
		return fmt.Errorf("the credentials file %q has a character bd's local setting cannot carry", record.Credentials)
	}
	kept, _, err := localSettingRest(store)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(kept, "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
		if strings.HasPrefix(line, "BEADS_DOLT_") || strings.HasPrefix(line, "BEADS_CREDENTIALS_FILE") {
			return fmt.Errorf("%s already says how bd reaches a server (%s); remove that line", LocalSettingPath(store), strings.SplitN(line, "=", 2)[0])
		}
	}
	block := localSettingBegin + "\n" + strings.Join(serverSettings(record), "\n") + "\n" + localSettingEnd + "\n"
	return writeLocalSetting(store, kept+block)
}

// RemoveLocalSetting takes the local setting out again, and the file with it
// where nothing else was in it.
func RemoveLocalSetting(store string) error {
	kept, found, err := localSettingRest(store)
	if err != nil || !found {
		return err
	}
	if strings.TrimSpace(kept) != "" {
		return writeLocalSetting(store, kept)
	}
	root, err := repowrite.NewRoot(store)
	if err == nil {
		_, err = root.RemoveFile(localSettingName)
	}
	if err != nil {
		return fmt.Errorf("remove bd's local setting: %w", err)
	}
	return nil
}

func writeLocalSetting(store, content string) error {
	root, err := repowrite.NewRoot(store)
	if err == nil {
		_, err = root.WriteFile(localSettingName, []byte(content))
	}
	if err != nil {
		return fmt.Errorf("write bd's local setting: %w", err)
	}
	return nil
}

// localSettingRest is the file's content without the setting, ending in a
// newline where it is not empty, and whether the setting was there.
func localSettingRest(store string) (string, bool, error) {
	content, err := os.ReadFile(LocalSettingPath(store))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read bd's local setting: %w", err)
	}
	rest := string(content)
	before, after, found := strings.Cut(rest, localSettingBegin+"\n")
	if found {
		_, tail, ended := strings.Cut(after, localSettingEnd+"\n")
		if !ended {
			return "", false, fmt.Errorf("%s has the tracker server's setting begun and not ended; take its lines out by hand", LocalSettingPath(store))
		}
		rest = before + tail
	}
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	return rest, found, nil
}
