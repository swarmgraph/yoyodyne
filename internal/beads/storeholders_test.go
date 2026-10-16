package beads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// fixedHolders is a finder that answers what it is told, remembering the paths
// it was asked about.
type fixedHolders struct {
	holders []StoreHolder
	err     error
	asked   *[]string
}

func (f fixedHolders) Holders(_ context.Context, path string) ([]StoreHolder, error) {
	if f.asked != nil {
		*f.asked = append(*f.asked, path)
	}
	return f.holders, f.err
}

// A call stopped at its limit names, beside the timeout, the process that held
// the store open at that moment and since when it had been running.
func TestACallStoppedAtItsLimitNamesWhatHeldTheStore(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	storeAt(t, filepath.Join(project, ".beads"), "metadata.json", "embeddeddolt/")
	since := time.Date(2026, 10, 16, 5, 41, 25, 0, time.Local)
	var asked []string
	client := Client{
		Runner:       &fakeRunner{results: []execution.ProcessResult{timedOutCall()}},
		Dir:          project,
		Holders:      fixedHolders{holders: []StoreHolder{{PID: 4242, Command: "bd export -o .beads/issues.jsonl", Since: since}}, asked: &asked},
		listingPause: func(context.Context, time.Duration) bool { return false },
	}

	_, err := client.Show(context.Background(), "yoyodyne-7my")
	if err == nil {
		t.Fatal("Show() error = nil, want the timeout")
	}
	for _, want := range []string{
		"bd show failed with status timed_out and exit code -1: reading work item yoyodyne-7my",
		"at the 30s limit each tracker call is given; when it was stopped, the tracker's store was held open by process 4242 (bd export -o .beads/issues.jsonl), running since " + since.Format("2006-01-02 15:04:05 MST"),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Show() error = %q, want it to say %q", err, want)
		}
	}
	if !timedOut(err) {
		t.Errorf("Show() error = %q, want it still read as a stalled tracker call", err)
	}
	if want := canonicalPath(filepath.Join(project, ".beads", "embeddeddolt")); len(asked) != 1 || asked[0] != want {
		t.Errorf("finder asked about %v, want the store's database %s", asked, want)
	}
}

// Where nothing held the store, or the finder could not tell, the account
// says which, and why it could not.
func TestACallStoppedAtItsLimitSaysWhenNoHolderCouldBeNamed(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	storeAt(t, filepath.Join(project, ".beads"), "metadata.json", "embeddeddolt/")
	for name, tc := range map[string]struct {
		client Client
		want   string
	}{
		"nobody held it": {
			client: Client{Dir: project, Holders: fixedHolders{}},
			want:   "no process had the tracker's store open when it was stopped",
		},
		"the finder could not tell": {
			client: Client{Dir: project, Holders: fixedHolders{err: errors.New("lsof could not be run: executable file not found in $PATH")}},
			want:   "which process held the tracker's store could not be told: lsof could not be run: executable file not found in $PATH",
		},
		"no store to look at": {
			client: Client{Holders: fixedHolders{holders: []StoreHolder{{PID: 1}}}},
			want:   "which process held the tracker's store could not be told: no store was found from the directory this process runs in",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := tc.client
			client.Runner = &fakeRunner{results: []execution.ProcessResult{timedOutCall()}}
			_, err := client.run(context.Background(), "list", "--all")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run() error = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// A call that never started because its caller's deadline had already passed
// waited on nothing, so nothing is asked about what held the store.
func TestACallThatNeverStartedAsksNothingAboutTheStore(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	storeAt(t, filepath.Join(project, ".beads"), "metadata.json", "embeddeddolt/")
	var asked []string
	client := Client{Runner: &deadlineRunner{}, Dir: project, Holders: fixedHolders{asked: &asked}}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := client.run(ctx, "show", "yoyodyne-1", "--json")
	if err == nil || !strings.Contains(err.Error(), "never started") {
		t.Fatalf("run() error = %v, want it to say the call never started", err)
	}
	if len(asked) != 0 || strings.Contains(err.Error(), "held") {
		t.Errorf("run() error = %q after asking about %v, want nothing asked", err, asked)
	}
}

// A write that waited out an item's write lock names what held the lock.
func TestAWriteThatWaitedOutItsLockNamesTheHolder(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	storeAt(t, filepath.Join(project, ".beads"), "metadata.json")
	ctx := context.Background()
	held := Client{Dir: project, Timeout: time.Minute}
	release, err := held.lockItem(ctx, "yoyodyne-1")
	if err != nil {
		t.Fatalf("lockItem() error = %v", err)
	}
	defer release()

	var asked []string
	queued := Client{Dir: project, Timeout: 100 * time.Millisecond, Holders: fixedHolders{holders: []StoreHolder{{PID: 4242, Command: "yoyo work --watch"}}, asked: &asked}}
	_, err = queued.lockItem(ctx, "yoyodyne-1")
	if err == nil || !strings.Contains(err.Error(), "when it was stopped, the write lock for yoyodyne-1 was held open by process 4242 (yoyo work --watch)") {
		t.Fatalf("lockItem() error = %v, want it to name what held the lock", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("lockItem() error = %v, want it still a deadline", err)
	}
	if len(asked) != 1 || !strings.HasSuffix(asked[0], filepath.Join(writeLockDirectory, "yoyodyne-1.lock")) {
		t.Errorf("finder asked about %v, want the item's lock file", asked)
	}
}

// lsof's answer is read for its process numbers and the names beside them,
// whatever it exited with, and its exit of 1 with nothing said is nobody
// holding the path rather than a failure.
func TestLsofAnswersAreReadForProcessNumbers(t *testing.T) {
	t.Parallel()
	holders, err := lsofProcesses(execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "p812\ncbd\nf6\np77\ncyoyo\np812\ncbd\n"})
	if err != nil || len(holders) != 2 || holders[0] != (StoreHolder{PID: 77, Command: "yoyo"}) || holders[1] != (StoreHolder{PID: 812, Command: "bd"}) {
		t.Errorf("lsofProcesses() = %+v, %v, want 77 and 812 once each, named", holders, err)
	}
	// lsof +D exits 1 when any file under the directory is not open, even where
	// it names a process that has one open.
	if holders, err := lsofProcesses(execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stdout: "p48279\ncbd\nf5\n"}); err != nil || len(holders) != 1 || holders[0].PID != 48279 {
		t.Errorf("lsofProcesses() for a holder with an exit of 1 = %+v, %v, want the holder", holders, err)
	}
	if holders, err := lsofProcesses(execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1}); err != nil || len(holders) != 0 {
		t.Errorf("lsofProcesses() for nothing open = %+v, %v, want no holder and no error", holders, err)
	}
	if _, err := lsofProcesses(execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "lsof: status error on /nowhere: No such file or directory"}); err == nil || !strings.Contains(err.Error(), "No such file or directory") {
		t.Errorf("lsofProcesses() for a failure = %v, want lsof's own words", err)
	}
}

// scriptedLookups answers lsof and ps the way it is told to, so the finder's
// reading of the two is checked without either running.
type scriptedLookups struct {
	lsof  execution.ProcessResult
	ps    execution.ProcessResult
	psErr error
}

func (s scriptedLookups) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	if command.Name == "ps" {
		return s.ps, s.psErr
	}
	return s.lsof, nil
}

// A holder whose start ps cannot say keeps the name lsof gave it and says why
// its start is not known; one ps answers for gets its start and its command.
func TestAHolderSaysWhyItsStartIsNotKnown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lsof := execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stdout: "p812\ncbd\n"}
	refused, err := OSHolderFinder{Runner: scriptedLookups{lsof: lsof, ps: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "ps: operation not permitted"}}}.Holders(context.Background(), dir)
	if err != nil || len(refused) != 1 {
		t.Fatalf("Holders() = %+v, %v, want the one holder", refused, err)
	}
	if said := refused[0].Says(); said != "process 812 (bd), since when could not be read: ps did not answer: ps: operation not permitted" {
		t.Errorf("Says() = %q, want the name lsof gave and why the start is unknown", said)
	}
	answered, err := OSHolderFinder{Runner: scriptedLookups{lsof: lsof, ps: execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "  812 Thu Oct 16 05:41:25 2026 /usr/local/bin/bd list --all\n"}}}.Holders(context.Background(), dir)
	if err != nil || len(answered) != 1 || answered[0].Command != "bd list --all" || answered[0].Since.IsZero() {
		t.Errorf("Holders() = %+v, %v, want the holder with ps's command and start", answered, err)
	}
}

// ps's answer is read for when each process started and what it was started
// as, with the program named by its base name.
func TestPsAnswersAreReadForStartAndCommand(t *testing.T) {
	t.Parallel()
	described := psProcesses("  812 Thu Oct 16 05:41:25 2026     /usr/local/bin/bd export -o .beads/issues.jsonl\n   77 Wed Oct  1 09:02:03 2026 bd\n", time.UTC)
	export, found := described[812]
	if !found || export.Command != "bd export -o .beads/issues.jsonl" || !export.Since.Equal(time.Date(2026, 10, 16, 5, 41, 25, 0, time.UTC)) {
		t.Errorf("psProcesses()[812] = %+v, want the export with its start", export)
	}
	if bare := described[77]; bare.Command != "bd" || !bare.Since.Equal(time.Date(2026, 10, 1, 9, 2, 3, 0, time.UTC)) {
		t.Errorf("psProcesses()[77] = %+v, want bd with its start", bare)
	}
	if long := boundedCommand("/usr/local/bin/bd " + strings.Repeat("x", 400)); len(long) > maxHolderCommandBytes+len(oneline.Marker) || !strings.HasPrefix(long, "bd x") {
		t.Errorf("boundedCommand() = %q, want it bounded", long)
	}
}

// A list of holders names the first few and counts the rest, and this process
// is marked as itself.
func TestHoldersAreSaidInWords(t *testing.T) {
	t.Parallel()
	var holders []StoreHolder
	for pid := 1; pid <= maxHoldersSaid+2; pid++ {
		holders = append(holders, StoreHolder{PID: pid})
	}
	if said := SayHolders(holders); !strings.HasSuffix(said, "process 5; and 2 more") {
		t.Errorf("SayHolders() = %q, want five named and two counted", said)
	}
	if said := (StoreHolder{PID: os.Getpid()}).Says(); !strings.Contains(said, "(this one)") {
		t.Errorf("Says() = %q, want this process marked", said)
	}
}
