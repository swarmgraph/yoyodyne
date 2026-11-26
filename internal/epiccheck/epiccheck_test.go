package epiccheck

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
)

const (
	product = "yoyodyne"
	epic    = "yoyodyne-ep"
	member  = "yoyodyne-ep.1"
	removed = "yoyodyne-ep.2"
	commit  = "0f35eeff1e4a"

	stoppedRoot = "EPICCHECK_TEST_STOPPED_ROOT"
)

var namedAt = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func operatorAt(t *testing.T, root string) *epicscope.Operator {
	t.Helper()
	operator, err := epicscope.ForOperator(root, product)
	must(t, err)
	return operator
}

// named is a state root with the epic named, and one of its two members taken out
// by the operator.
func named(t *testing.T, root string) epicprogress.Naming {
	t.Helper()
	operator := operatorAt(t, root)
	started, err := operator.Start(epicscope.Naming{Epic: epic, From: "terminal", Description: "Do both things.", Members: []string{member, removed}}, namedAt)
	must(t, err)
	_, err = operator.Remove(epic, removed, "not needed now", namedAt.Add(time.Minute))
	must(t, err)
	return epicprogress.Of(started.Epic)
}

// checker answers what is given, and counts each call and look-up.
type checker struct {
	Checker
	sent, looked int
}

func checkerAt(t *testing.T, root string, answer string) *checker {
	t.Helper()
	store, err := epicprogress.Open(root, product)
	must(t, err)
	c := &checker{}
	c.Checker = Checker{
		Progress: store,
		MayStart: func(start maystart.Start) maystart.Decision { return maystart.Decide(root, product, start) },
		LookUp: func(string) (string, epicprogress.Looked, error) {
			c.looked++
			return "", epicprogress.NotSent, nil
		},
		Judge: func(context.Context, Request) (string, error) {
			c.sent++
			return answer, nil
		},
	}
	return c
}

func answering(findings ...epicprogress.Finding) string {
	encoded, err := json.Marshal(recorded{Findings: findings})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func entryOf(t *testing.T, root string, naming epicprogress.Naming) epicprogress.Entry {
	t.Helper()
	record, err := epicscope.Read(root, product)
	must(t, err)
	view, err := epicprogress.Read(root, product, record)
	must(t, err)
	entry, _ := view.Entry(naming)
	return entry
}

var (
	met        = epicprogress.Finding{Asks: "the first thing", Answer: epicprogress.Met, Says: "internal/a.go and its test"}
	takenOut   = epicprogress.Finding{Asks: "the second thing", Answer: epicprogress.TakenOut, Removal: removed}
	notMet     = epicprogress.Finding{Asks: "the second thing", Answer: epicprogress.NotMet, Says: "nothing reads the new file"}
	cannotTell = epicprogress.Finding{Asks: "the second thing", Answer: epicprogress.CannotBeTold, Says: "no check covers it"}
	personStep = epicprogress.Finding{Asks: "the second thing", Answer: epicprogress.CannotBeTold, Says: "a person signs the release", ForAPerson: true}
)

// Each answer is kept with the commit the check read, and only met and taken out
// together write the delivered entry.
func TestEachAnswerIsKeptAndOnlyMetOrTakenOutDelivers(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		findings  []epicprogress.Finding
		found     string
		delivered bool
	}{
		"met":                   {[]epicprogress.Finding{met, {Asks: "again", Answer: epicprogress.Met, Says: "the test"}}, epicprogress.FoundAllMet, true},
		"met and taken out":     {[]epicprogress.Finding{met, takenOut}, epicprogress.FoundAllMet, true},
		"not met":               {[]epicprogress.Finding{met, notMet, cannotTell}, epicprogress.FoundNotMet, false},
		"cannot be told":        {[]epicprogress.Finding{met, personStep, cannotTell}, epicprogress.FoundCannotTell, false},
		"a step for a person":   {[]epicprogress.Finding{takenOut, personStep}, epicprogress.FoundPersonsStep, false},
		"only cannot be told":   {[]epicprogress.Finding{cannotTell}, epicprogress.FoundCannotTell, false},
		"taken out and not met": {[]epicprogress.Finding{takenOut, notMet}, epicprogress.FoundNotMet, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			naming := named(t, root)
			c := checkerAt(t, root, answering(test.findings...))
			check, decision, err := c.Check(context.Background(), naming, "check-1", commit)
			must(t, err)
			if !decision.MayStart() || c.sent != 1 || c.looked != 0 {
				t.Fatalf("decision %+v, sent %d, looked up %d", decision, c.sent, c.looked)
			}
			entry := entryOf(t, root, naming)
			if len(entry.Checks) != 1 || entry.Checks[0].Commit != commit || entry.Checks[0].Found != test.found || len(entry.Checks[0].Findings) != len(test.findings) || check.Found != test.found {
				t.Fatalf("kept %+v, want one check of %s that found %q", entry.Checks, commit, test.found)
			}
			if got := len(entry.Deliveries) == 1; got != test.delivered || len(entry.Deliveries) > 1 {
				t.Fatalf("deliveries %+v, want delivered %t", entry.Deliveries, test.delivered)
			}
			if test.delivered {
				delivery := entry.Deliveries[0]
				if !delivery.At.Equal(entry.Checks[0].At) || !strings.Contains(delivery.Evidence, "check-1") || !strings.Contains(delivery.Evidence, commit) {
					t.Fatalf("the delivery %+v does not say when and on what evidence", delivery)
				}
			}
			// Made again under the same identifier, it is answered from what was kept.
			if _, _, err := c.Check(context.Background(), naming, "check-1", commit); err != nil || c.sent != 1 || len(entryOf(t, root, naming).Checks) != 1 {
				t.Fatalf("made again: %v, sent %d", err, c.sent)
			}
		})
	}
}

// An answer that is not whole, and taken out naming no removal in the operator's
// record, are refused, and nothing is kept of the check.
func TestAnAnswerOfTakenOutThatNamesNoRemovalIsRefused(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		finding epicprogress.Finding
		says    string
	}{
		"a member":            {epicprogress.Finding{Asks: "the first thing", Answer: epicprogress.TakenOut, Removal: member}, "holds no removal of " + member},
		"an item never in it": {epicprogress.Finding{Asks: "the first thing", Answer: epicprogress.TakenOut, Removal: "yoyodyne-other"}, "holds no removal of yoyodyne-other"},
		"no item":             {epicprogress.Finding{Asks: "the first thing", Answer: epicprogress.TakenOut}, "is not met, taken out"},
		"met saying nothing":  {epicprogress.Finding{Asks: "the first thing", Answer: epicprogress.Met}, "is not met, taken out"},
		"no answer":           {epicprogress.Finding{Asks: "the first thing", Says: "looked"}, "is not met, taken out"},
		"nothing asked":       {epicprogress.Finding{Answer: epicprogress.Met, Says: "looked"}, "is not met, taken out"},
		"a person's step met": {epicprogress.Finding{Asks: "the first thing", Answer: epicprogress.Met, Says: "looked", ForAPerson: true}, "is not met, taken out"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			naming := named(t, root)
			_, _, err := checkerAt(t, root, answering(met, test.finding)).Check(context.Background(), naming, "check-1", commit)
			if err == nil || !strings.Contains(err.Error(), test.says) {
				t.Fatalf("error %v, want one saying %q", err, test.says)
			}
			if entry := entryOf(t, root, naming); len(entry.Checks)+len(entry.Deliveries) != 0 {
				t.Fatalf("kept %+v", entry)
			}
		})
	}
	root := t.TempDir()
	naming := named(t, root)
	if _, _, err := checkerAt(t, root, answering()).Check(context.Background(), naming, "check-1", commit); err == nil || !strings.Contains(err.Error(), "an answer for each thing") {
		t.Fatalf("a check with no answer: %v", err)
	}
}

func progressFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(home.ProductDirectory(root, product), "*"))
	must(t, err)
	files := map[string]string{}
	for _, path := range found {
		if strings.HasSuffix(path, ".lock") {
			continue
		}
		content, err := os.ReadFile(path)
		must(t, err)
		files[filepath.Base(path)] = string(content)
	}
	return files
}

// The check may start for a naming that has not ended and whose progress can be
// read. For an ended naming it may not, and where the progress cannot be read the
// decision could not tell; in both no call is made and nothing is kept, whether
// epic mode is on or off.
func TestTheCheckStartsOnlyForAnOpenNamingWhoseProgressCanBeRead(t *testing.T) {
	t.Parallel()
	ended := func(t *testing.T, root string, naming epicprogress.Naming) {
		_, err := operatorAt(t, root).End(epic, "", namedAt.Add(time.Hour))
		must(t, err)
	}
	unreadable := func(t *testing.T, root string, naming epicprogress.Naming) {
		store, err := epicprogress.Open(root, product)
		must(t, err)
		must(t, store.Begin(naming))
		must(t, os.Remove(filepath.Join(home.ProductDirectory(root, product), "epic-progress.json")))
	}
	for name, test := range map[string]struct {
		setUp func(*testing.T, string, epicprogress.Naming)
		on    maystart.Answer
	}{
		"ended":      {ended, maystart.MayNot},
		"unreadable": {unreadable, maystart.CouldNotTell},
	} {
		for _, mode := range []string{"on", "off"} {
			t.Run(name+" with epic mode "+mode, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				naming := named(t, root)
				test.setUp(t, root, naming)
				want := test.on
				if mode == "off" {
					_, err := operatorAt(t, root).TurnOff()
					must(t, err)
					want = maystart.May // the second rule answers first
				}
				before := progressFiles(t, root)
				c := checkerAt(t, root, answering(met))
				_, decision, err := c.Check(context.Background(), naming, "check-1", commit)
				if decision.Answer != want || c.sent+c.looked != 0 || (err == nil) != (mode == "on") {
					t.Fatalf("decision %+v, error %v, sent %d, looked up %d", decision, err, c.sent, c.looked)
				}
				after := progressFiles(t, root)
				if len(after) != len(before) {
					t.Fatalf("files %v, were %v", after, before)
				}
				for file, content := range before {
					if after[file] != content {
						t.Errorf("%s changed", file)
					}
				}
			})
		}
	}
	root := t.TempDir()
	naming := named(t, root)
	c := checkerAt(t, root, answering(met))
	if _, decision, err := c.Check(context.Background(), naming, "check-1", commit); err != nil || !decision.MayStart() || c.sent != 1 {
		t.Fatalf("an open naming: %+v, %v, sent %d", decision, err, c.sent)
	}
}

// TestStoppedChecker is the process that a test stops in the middle of a call:
// the call is reserved, and the process ends before any answer is kept.
func TestStoppedChecker(t *testing.T) {
	root := os.Getenv(stoppedRoot)
	if root == "" {
		t.Skip("run by the test that stops a check partway")
	}
	record, err := epicscope.Read(root, product)
	must(t, err)
	opened, _ := record.Named(epic)
	c := checkerAt(t, root, "")
	c.Judge = func(context.Context, Request) (string, error) {
		os.Exit(3)
		return "", nil
	}
	_, _, _ = c.Check(context.Background(), epicprogress.Of(opened), "check-1", commit)
	t.Fatal("the call returned")
}

// The harness stops between the reservation and the answer. Started again, it
// looks the first call up before it sends anything, and sends nothing where the
// look-up found the answer.
func TestACheckStoppedBetweenItsReservationAndItsAnswerIsLookedUpFirst(t *testing.T) {
	t.Parallel()
	for name, looked := range map[string]epicprogress.Looked{"found": epicprogress.Found, "never sent": epicprogress.NotSent} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			naming := named(t, root)
			command := exec.Command(os.Args[0], "-test.run=^TestStoppedChecker$")
			command.Env = append(os.Environ(), stoppedRoot+"="+root)
			if output, err := command.CombinedOutput(); err == nil || command.ProcessState.ExitCode() != 3 {
				t.Fatalf("the process was not stopped in the call: %v\n%s", err, output)
			}
			entry := entryOf(t, root, naming)
			if len(entry.Reservations) != 1 || entry.Reservations[0].ID != "check-1" || entry.Reservations[0].Result != nil || len(entry.Checks) != 0 {
				t.Fatalf("after the stop the entry is %+v, want the call reserved and nothing answered", entry)
			}
			c := checkerAt(t, root, answering(met, takenOut))
			var order []string
			c.LookUp = func(id string) (string, epicprogress.Looked, error) {
				order = append(order, "look up "+id)
				if looked == epicprogress.Found {
					return answering(met, takenOut), looked, nil
				}
				return "", looked, nil
			}
			judge := c.Judge
			c.Judge = func(ctx context.Context, request Request) (string, error) {
				order = append(order, "send "+request.ID)
				return judge(ctx, request)
			}
			check, _, err := c.Check(context.Background(), naming, "check-1", commit)
			must(t, err)
			want := "look up check-1"
			if looked == epicprogress.NotSent {
				want += ", send check-1"
			}
			if got := strings.Join(order, ", "); got != want || !check.Passed() {
				t.Fatalf("started again it did %q and found %q, want %q and all met", got, check.Found, want)
			}
			if entry := entryOf(t, root, naming); len(entry.Checks) != 1 || len(entry.Deliveries) != 1 {
				t.Fatalf("kept %+v", entry)
			}
		})
	}
}
