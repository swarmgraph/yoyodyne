package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// The tracker's server mode, started on a copy of the store a measurement made.
//
// bd's embedded store lets one process in at a time, so every tracker call
// waits behind every other. bd also has a server mode (`bd dolt start`), in
// which one server process holds the store and every bd talks to it over the
// network, so readers need not queue. Whether the harness may move to it turns
// on one thing the design on keeping the store small leaves open
// (docs/designs/tracker-store-size.md): a developer run must not be able to
// reach the store, and a server listening on this machine may be reachable
// from a developer run's sandbox, which lets a run connect to localhost. So a
// measurement starts server mode on a copy and records:
//
//  1. where the server listens: the port bd says it started on and every
//     address the operating system says the server's process is listening on;
//  2. whether a connection attempt made under the developer sandbox's network
//     rules reaches it, beside the same attempt made with no sandbox, which says
//     whether the server was answering at all;
//  3. which writes grew the store over the last week and by how much, read
//     from the store's own history through the server (`bd sql`, which bd runs
//     only in server mode): each kind of write, the commits it made, the rows
//     it changed, and the tables it changed them in.
//
// The server is started on a second copy, made from the measurement's own copy
// before the cleanup and the squashes run on that one, so nothing the server
// does to its data directory reaches the figures of the stages, and so the
// history is read before any squash removes it. bd is run there with server
// mode turned on and every variable that could point it at another data
// directory or a server shared between projects taken out (Client.environment),
// and a server whose data directory bd names outside the second copy is
// stopped at once without anything else being asked of it.
//
// A server started with `bd dolt start` runs in the background on its own and
// outlives the process that started it, so it is stopped however the
// measurement ends: `bd dolt stop`, then `bd dolt stop --force`, then the
// operating system's kill. One a killed measurement left running is stopped by
// the next, before it removes the copy that server was serving.

const (
	serverCopyName = "server-copy"
	// growthWindow is how far back the store's history is read.
	growthWindow = 7 * 24 * time.Hour
	// maxGrowthKinds is how many kinds of write a measurement names; the rest
	// are counted together.
	maxGrowthKinds = 20
	// maxGrowthTables bounds how many tables' changes are counted, which is far
	// more than bd keeps.
	maxGrowthTables = 40
	// serverStopLimit bounds stopping the server, which a measurement whose own
	// time has run out still does.
	serverStopLimit = 2 * time.Minute
	// reachLimit bounds one connection attempt, nc's own timeout included.
	reachLimit       = 15 * time.Second
	otherWritesKind  = "every other kind of write"
	sqlDateLayout    = "2006-01-02 15:04:05"
	sandboxExecPath  = "/usr/bin/sandbox-exec"
	netcatPath       = "/usr/bin/nc"
	netcatTimeoutSec = "5"
)

// developerNetworkProfile is the developer sandbox's network rules as a
// sandbox-exec profile: everything allowed but the network, and on the network
// only binding, accepting, and connecting on localhost. That is what the one
// network grant a developer run's Claude Code sandbox is given,
// `allowLocalBinding`, permits (internal/backend/claudecode, developerSettings);
// every other connection a run makes goes through the CLI's filtering proxy,
// which a connection to a local port does not. It reproduces those rules rather
// than being Claude Code's own profile, so the measurement says so.
const developerNetworkProfile = `(version 1)
(allow default)
(deny network*)
(allow network-bind (local ip "localhost:*"))
(allow network-inbound (local ip "localhost:*"))
(allow network-outbound (remote ip "localhost:*"))
`

// DeveloperSandboxReproduced says in words what the reach attempt is made
// under.
const DeveloperSandboxReproduced = "nc under sandbox-exec with the network rules a developer run's Claude Code sandbox is given (allowLocalBinding: binding, accepting, and connecting on localhost, and no other connection)"

// StoreServer is what server mode came to on the second copy.
type StoreServer struct {
	// Said is what `bd dolt start` wrote, and Failed why server mode was not
	// measured, where it was not. A server that started and failed afterwards
	// keeps what was learned before it failed.
	Said   string `json:"said,omitempty"`
	Failed string `json:"failed,omitempty"`
	// PID and Port are the server's process and the port bd says it listens on,
	// and DataDir the data directory bd says it serves.
	PID     int    `json:"pid,omitempty"`
	Port    int    `json:"port,omitempty"`
	DataDir string `json:"data_dir,omitempty"`
	// Listening is every address the server's process listened on, as the
	// operating system reported them, and ListeningUnread why they could not be
	// read, where they could not.
	Listening       []string `json:"listening,omitempty"`
	ListeningUnread string   `json:"listening_unread,omitempty"`
	// Reach is the connection attempt made under the developer sandbox.
	Reach SandboxReach `json:"reach"`
	// Growth is which writes grew the store over the last week.
	Growth StoreGrowth `json:"growth"`
	// StopFailed is why the server could not be stopped by bd, and how it was
	// stopped instead or that it may still be running.
	StopFailed string `json:"stop_failed,omitempty"`
}

// SandboxReach is one attempt to connect to the server from under the
// developer sandbox, and the same attempt with no sandbox.
type SandboxReach struct {
	// Address is the address tried, and How what the attempt was made under.
	Address string `json:"address,omitempty"`
	How     string `json:"how,omitempty"`
	// Tried says the sandboxed attempt was made, Reached that it connected, and
	// Said what it wrote where it did not.
	Tried   bool   `json:"tried,omitempty"`
	Reached bool   `json:"reached,omitempty"`
	Said    string `json:"said,omitempty"`
	// ReachedUnsandboxed says the same attempt connected with no sandbox, which
	// is what makes a refusal under the sandbox the sandbox's rather than a
	// server that was not answering.
	ReachedUnsandboxed bool `json:"reached_unsandboxed,omitempty"`
	// Failed is why no attempt could be made.
	Failed string `json:"failed,omitempty"`
}

// StoreGrowth is which writes grew the store, read from its history.
type StoreGrowth struct {
	// Since is the start of the window read.
	Since time.Time `json:"since,omitzero"`
	// Commits is every commit in the window, and RowsChanged every row they
	// changed, across every table counted.
	Commits     int `json:"commits,omitempty"`
	RowsChanged int `json:"rows_changed,omitempty"`
	// Writes is each kind of write, most rows changed first; a kind is bd's own
	// name for the command that wrote, such as "bd: update".
	Writes []WriteGrowth `json:"writes,omitempty"`
	// Unread is each table whose changes could not be counted, with why.
	Unread []string `json:"unread,omitempty"`
	// Failed is why the history could not be read at all.
	Failed string `json:"failed,omitempty"`
}

// WriteGrowth is one kind of write over the window.
type WriteGrowth struct {
	Kind        string   `json:"kind"`
	Commits     int      `json:"commits"`
	RowsChanged int      `json:"rows_changed"`
	Tables      []string `json:"tables,omitempty"`
}

// ServerProbe is how a measurement learns where the server it started
// listens, and whether the developer sandbox reaches it.
type ServerProbe interface {
	Listening(ctx context.Context, pid int) ([]string, error)
	Reach(ctx context.Context, address string) SandboxReach
}

// OSServerProbe asks lsof what the server listens on, and tries the connection
// with nc, under sandbox-exec and without it.
type OSServerProbe struct {
	Runner execution.ProcessRunner
}

func (p OSServerProbe) Listening(ctx context.Context, pid int) ([]string, error) {
	if p.Runner == nil {
		return nil, errors.New("no process runner is wired to ask lsof")
	}
	// -a joins the process and the listening sockets, and -F n is lsof's
	// machine form, an `n<address>` line for each.
	result, err := p.Runner.Run(ctx, execution.Command{
		Name:    "lsof",
		Args:    []string{"-w", "-n", "-P", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-F", "n"},
		Timeout: holderLookupTimeout,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("lsof could not be run: %w", err)
	}
	var listening []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		if address, found := strings.CutPrefix(strings.TrimSpace(line), "n"); found && address != "" {
			listening = append(listening, address)
		}
	}
	// lsof exits 1 having found nothing, which is an answer.
	if result.Status != execution.ProcessSucceeded && len(listening) == 0 && strings.TrimSpace(result.Stderr) != "" {
		return nil, fmt.Errorf("lsof failed: %s", oneline.Fold(strings.TrimSpace(result.Stderr), maxMeasureSaid))
	}
	return listening, nil
}

func (p OSServerProbe) Reach(ctx context.Context, address string) SandboxReach {
	reach := SandboxReach{Address: address, How: DeveloperSandboxReproduced}
	if runtime.GOOS != "darwin" {
		reach.Failed = "the developer sandbox is reproduced only on macOS, and this is " + runtime.GOOS
		return reach
	}
	if p.Runner == nil {
		reach.Failed = "no process runner is wired to try the connection"
		return reach
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		reach.Failed = fmt.Sprintf("%s is not an address to connect to: %v", address, err)
		return reach
	}
	netcat := []string{"-z", "-G", netcatTimeoutSec, host, port}
	plain, err := p.Runner.Run(ctx, execution.Command{Name: netcatPath, Args: netcat, Timeout: reachLimit}, nil)
	if err != nil {
		reach.Failed = fmt.Sprintf("nc could not be run: %v", err)
		return reach
	}
	reach.ReachedUnsandboxed = plain.Status == execution.ProcessSucceeded
	sandboxed, err := p.Runner.Run(ctx, execution.Command{
		Name:    sandboxExecPath,
		Args:    append([]string{"-p", developerNetworkProfile, netcatPath}, netcat...),
		Timeout: reachLimit,
	}, nil)
	if err != nil {
		reach.Failed = fmt.Sprintf("sandbox-exec could not be run: %v", err)
		return reach
	}
	reach.Tried = true
	reach.Reached = sandboxed.Status == execution.ProcessSucceeded
	if !reach.Reached {
		reach.Said = oneline.Fold(firstNonBlank(strings.TrimSpace(sandboxed.Stderr), strings.TrimSpace(sandboxed.Stdout), fmt.Sprintf("it ended %s with exit code %d", sandboxed.Status, sandboxed.ExitCode)), maxMeasureSaid)
	}
	return reach
}

// serverProbe is the client's probe: the one it was given, or the operating
// system's.
func (c Client) serverProbe() ServerProbe {
	if c.Server != nil {
		return c.Server
	}
	return OSServerProbe{Runner: execution.OSProcessRunner{}}
}

// serverClient is a client over a copy of the store in server mode.
func (c Client) serverClient(project string) Client {
	served := c
	served.Dir = project
	served.beadsDir = filepath.Join(project, ".beads")
	served.serverMode = true
	return served
}

// stopLeftServer stops a server an earlier measurement left running on its
// second copy, which only a measurement killed before it stopped its server
// leaves, so that removing the copy does not leave a server serving nothing.
func (c Client) stopLeftServer(ctx context.Context, work *repowrite.PinnedRoot) {
	left, err := work.Exists(serverCopyName + "/.beads/dolt-server.pid")
	if err != nil || !left {
		return
	}
	served := c.serverClient(filepath.Join(work.Path(), serverCopyName))
	stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), serverStopLimit)
	defer cancel()
	_, _ = served.runWithin(stopping, serverStopLimit, "dolt", "stop", "--force")
}

// measureServer copies the measurement's copy, project, into a second copy,
// starts server mode on it, and measures it as the comment at the top of this
// file says. It never fails: what could not be done is said on what it
// returns. The second copy is removed before it returns.
func (c Client) measureServer(ctx context.Context, work *repowrite.PinnedRoot, project string, now time.Time) *StoreServer {
	server := &StoreServer{}
	if err := work.RemoveAll(serverCopyName); err != nil {
		server.Failed = fmt.Sprintf("the second copy an earlier measurement left could not be removed: %v", err)
		return server
	}
	defer work.RemoveAll(serverCopyName)
	if err := copyStore(filepath.Join(project, ".beads"), work, serverCopyName); err != nil {
		server.Failed = fmt.Sprintf("the copy could not be copied again to start the server on: %v", err)
		return server
	}
	served := c.serverClient(filepath.Join(work.Path(), serverCopyName))
	said, err := served.runWithin(ctx, measureCallLimit, "dolt", "start")
	server.Said = oneline.Fold(strings.TrimSpace(string(said)), maxMeasureSaid)
	// Stopped whatever this comes to, a start that failed included, since bd may
	// have started a server before it failed.
	defer served.stopServer(ctx, server)
	if err != nil {
		server.Failed = "bd dolt start failed: " + oneline.Fold(err.Error(), maxMeasureSaid)
		return server
	}
	server.PID, server.Port, server.DataDir = parseServerStart(string(said))
	if server.PID == 0 || server.Port == 0 {
		server.Failed = "bd dolt start did not say which process and port the server started as"
		return server
	}
	if !within(served.Dir, server.DataDir) {
		server.Failed = fmt.Sprintf("bd said the server serves %q, which is not inside the second copy %s, so it was stopped and nothing was asked of it", server.DataDir, served.Dir)
		return server
	}
	probe := c.serverProbe()
	lookup, cancel := context.WithTimeout(ctx, holderLookupTimeout)
	listening, err := probe.Listening(lookup, server.PID)
	cancel()
	server.Listening = listening
	if err != nil {
		server.ListeningUnread = oneline.Fold(err.Error(), maxMeasureSaid)
	}
	server.Reach = probe.Reach(ctx, reachAddress(listening, server.Port))
	server.Growth = served.measureGrowth(ctx, now.Add(-growthWindow))
	return server
}

// stopServer stops the server on the second copy, by bd where it can and by
// the operating system where it cannot, and records on server what it could
// not do.
func (c Client) stopServer(ctx context.Context, server *StoreServer) {
	stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), serverStopLimit)
	defer cancel()
	_, err := c.runWithin(stopping, serverStopLimit, "dolt", "stop")
	if err == nil {
		return
	}
	said := "bd dolt stop failed: " + oneline.Fold(err.Error(), maxMeasureSaid)
	_, forced := c.runWithin(stopping, serverStopLimit, "dolt", "stop", "--force")
	switch {
	case forced == nil && server.PID != 0:
		server.StopFailed = said + "; bd dolt stop --force stopped it"
		return
	case server.PID == 0:
		// A start that failed before it said what it started leaves nothing this
		// can name to kill; bd's stop failing there is it finding no server.
		return
	}
	process, err := os.FindProcess(server.PID)
	if err == nil {
		err = process.Kill()
	}
	if err != nil {
		server.StopFailed = fmt.Sprintf("%s, and so did --force, and process %d could not be killed, so it may still be running: %v", said, server.PID, err)
		return
	}
	server.StopFailed = fmt.Sprintf("%s, and so did --force, so process %d was killed", said, server.PID)
}

var (
	serverStarted  = regexp.MustCompile(`PID (\d+), port (\d+)`)
	serverDataLine = regexp.MustCompile(`(?m)^\s*Data:\s*(.+?)\s*$`)
)

// parseServerStart reads what `bd dolt start` wrote: "Dolt server started (PID
// <pid>, port <port>)" and a "Data: <directory>" line.
func parseServerStart(said string) (pid, port int, dataDir string) {
	if match := serverStarted.FindStringSubmatch(said); match != nil {
		pid, _ = strconv.Atoi(match[1])
		port, _ = strconv.Atoi(match[2])
	}
	if match := serverDataLine.FindStringSubmatch(said); match != nil {
		dataDir = match[1]
	}
	return pid, port, dataDir
}

// within reports whether path is inside directory, each read through any
// symbolic links, and false for an empty path.
func within(directory, path string) bool {
	if path == "" {
		return false
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedDirectory, resolved)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// reachAddress is the address the reach attempt tries: the first the server's
// process listened on, with a wildcard read as this machine, or the port bd
// named on the loopback address bd binds by default.
func reachAddress(listening []string, port int) string {
	for _, address := range listening {
		host, listened, err := net.SplitHostPort(address)
		if err != nil {
			continue
		}
		if host == "*" || host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		return net.JoinHostPort(host, listened)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

var tableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// measureGrowth reads which writes grew the store since since, from its
// history, through the server.
func (c Client) measureGrowth(ctx context.Context, since time.Time) StoreGrowth {
	growth := StoreGrowth{Since: since.UTC()}
	from := since.UTC().Format(sqlDateLayout)
	kinds, err := c.sqlRows(ctx, fmt.Sprintf("SELECT SUBSTRING_INDEX(message, ' (', 1) AS kind, COUNT(*) AS commits FROM dolt_log WHERE date >= '%s' GROUP BY kind", from))
	if err != nil {
		growth.Failed = "the store's history could not be read: " + oneline.Fold(err.Error(), maxMeasureSaid)
		return growth
	}
	writes := map[string]*WriteGrowth{}
	kindOf := func(kind string) *WriteGrowth {
		if writes[kind] == nil {
			writes[kind] = &WriteGrowth{Kind: kind}
		}
		return writes[kind]
	}
	for _, row := range kinds {
		write := kindOf(stringValue(row["kind"]))
		write.Commits += intValue(row["commits"])
		growth.Commits += intValue(row["commits"])
	}
	tables, err := c.sqlRows(ctx, fmt.Sprintf("SELECT DISTINCT table_name FROM dolt_diff WHERE date >= '%s'", from))
	if err != nil {
		growth.Failed = "which tables the store's history changed could not be read: " + oneline.Fold(err.Error(), maxMeasureSaid)
		return growth
	}
	var names []string
	for _, row := range tables {
		if name := stringValue(row["table_name"]); tableName.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > maxGrowthTables {
		growth.Unread = append(growth.Unread, fmt.Sprintf("%d tables beyond the first %d were not counted", len(names)-maxGrowthTables, maxGrowthTables))
		names = names[:maxGrowthTables]
	}
	for _, name := range names {
		rows, err := c.sqlRows(ctx, fmt.Sprintf("SELECT SUBSTRING_INDEX(l.message, ' (', 1) AS kind, COUNT(*) AS rows_changed FROM dolt_diff_%s AS x JOIN dolt_log AS l ON l.commit_hash = x.to_commit WHERE x.to_commit_date >= '%s' GROUP BY kind", name, from))
		if err != nil {
			growth.Unread = append(growth.Unread, name+": "+oneline.Fold(err.Error(), maxMeasureSaid))
			continue
		}
		for _, row := range rows {
			changed := intValue(row["rows_changed"])
			if changed == 0 {
				continue
			}
			write := kindOf(stringValue(row["kind"]))
			write.RowsChanged += changed
			write.Tables = append(write.Tables, name)
			growth.RowsChanged += changed
		}
	}
	growth.Writes = rankedWrites(writes)
	return growth
}

// rankedWrites is every kind of write, most rows changed first and then most
// commits, the first maxGrowthKinds named and the rest counted together.
func rankedWrites(writes map[string]*WriteGrowth) []WriteGrowth {
	ranked := make([]WriteGrowth, 0, len(writes))
	for _, write := range writes {
		ranked = append(ranked, *write)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].RowsChanged != ranked[j].RowsChanged {
			return ranked[i].RowsChanged > ranked[j].RowsChanged
		}
		if ranked[i].Commits != ranked[j].Commits {
			return ranked[i].Commits > ranked[j].Commits
		}
		return ranked[i].Kind < ranked[j].Kind
	})
	if len(ranked) <= maxGrowthKinds {
		return ranked
	}
	rest := WriteGrowth{Kind: otherWritesKind}
	for _, write := range ranked[maxGrowthKinds:] {
		rest.Commits += write.Commits
		rest.RowsChanged += write.RowsChanged
	}
	return append(ranked[:maxGrowthKinds], rest)
}

// sqlRows runs one read through the server and reads its rows.
func (c Client) sqlRows(ctx context.Context, query string) ([]map[string]any, error) {
	data, err := c.runWithin(ctx, measureCallLimit, "sql", "--json", query)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var rows []map[string]any
	if err := decoder.Decode(&rows); err != nil {
		return nil, fmt.Errorf("bd sql did not answer with rows: %w", err)
	}
	return rows, nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// intValue reads a count bd sql wrote as a number or, as the driver hands
// MySQL's text answers back, as a string.
func intValue(value any) int {
	switch typed := value.(type) {
	case json.Number:
		if whole, err := typed.Int64(); err == nil {
			return int(whole)
		}
		if float, err := typed.Float64(); err == nil {
			return int(float)
		}
	case string:
		if whole, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return whole
		}
	case float64:
		return int(typed)
	}
	return 0
}
