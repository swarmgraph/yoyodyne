package beads

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// unmarkedClaims is the read a claim makes first, for the tests of the other
// parts of a claim: it fails, so the claim is made unmarked, as it is whenever
// that read fails, and the scripted tracker answers the claim's own writes and
// reads exactly as it did before claims read anything first.
func unmarkedClaims(context.Context, string) (WorkItem, error) {
	return WorkItem{}, errors.New("not read by this test")
}

// assignedItemJSON is one work item as bd answers with it: the status, the
// notes, the assignee, and whether a claim marked that assignee as its own.
func assignedItemJSON(status, notes, assignee string, marked bool) string {
	metadata := `{}`
	if marked {
		metadata = `{"yoyodyne_claim_assignee":true}`
	}
	return fmt.Sprintf(`[{"id":"yoyodyne-1","title":"Implement feature","notes":%q,"status":%q,"priority":1,`+
		`"issue_type":"task","assignee":%q,"metadata":%s}]`, notes, status, assignee, metadata)
}

var clearClaimAssigneeArgs = []string{"update", "yoyodyne-1", "--assignee=", "--unset-metadata=yoyodyne_claim_assignee", "--json"}

// A claim marks the assignee it sets as its own where the item had none, and
// where an earlier claim had marked the one there; an assignee a person set is
// claimed over unmarked, so nothing the harness does afterwards clears it.
func TestAClaimMarksOnlyTheAssigneeItSets(t *testing.T) {
	t.Parallel()

	marked := []string{"update", "yoyodyne-1", "--claim", "--set-metadata=yoyodyne_claim_assignee=true", "--json"}
	unmarked := []string{"update", "yoyodyne-1", "--claim", "--json"}
	for _, test := range []struct {
		name   string
		before string
		want   []string
	}{
		{"no assignee", assignedItemJSON("open", "", "", false), marked},
		{"an assignee an earlier claim set", assignedItemJSON("open", "", "Mason Bryant", true), marked},
		{"an assignee a person set", assignedItemJSON("open", "", "Mason Bryant", false), unmarked},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{responses: []string{test.before, assignedItemJSON("in_progress", "", "Mason Bryant", test.want[3] != "--json")}}
			if _, _, err := (Client{Runner: runner}).Claim(context.Background(), "yoyodyne-1"); err != nil {
				t.Fatalf("Claim() error = %v", err)
			}
			want := [][]string{{"show", "yoyodyne-1", "--json"}, test.want}
			if !reflect.DeepEqual(runner.args, want) {
				t.Fatalf("bd args = %#v, want %#v", runner.args, want)
			}
		})
	}

	// A read that fails does not stop the claim. It is made unmarked, which costs
	// a name left on the item rather than a person's name taken off one.
	runner := &fakeRunner{
		results:   []execution.ProcessResult{{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "database is locked"}},
		responses: []string{"", assignedItemJSON("in_progress", "", "Mason Bryant", false)},
	}
	if _, _, err := (Client{Runner: runner}).Claim(context.Background(), "yoyodyne-1"); err != nil {
		t.Fatalf("Claim() after a failed read error = %v", err)
	}
	if got := runner.args[len(runner.args)-1]; !reflect.DeepEqual(got, unmarked) {
		t.Fatalf("claim after a failed read = %#v, want %#v", got, unmarked)
	}
}

// Every write that gives an item back after a run — releasing it, reopening it
// for an evidence landing, blocking it, clearing a block, closing it — clears
// the assignee the run's claim set, and leaves one a person set exactly where it
// is.
func TestEveryGiveBackClearsTheClaimsAssigneeAndNoPersons(t *testing.T) {
	t.Parallel()

	const note = "the harness gave this item back"
	parking := domain.WorkItemParking("")
	giveBacks := []struct {
		name   string
		status string
		answer func(assignee string, marked bool) string
		call   func(Client) (WorkItem, error)
	}{
		{"release", "open", func(a string, m bool) string { return assignedItemJSON("open", note, a, m) },
			func(c Client) (WorkItem, error) { return c.Release(context.Background(), "yoyodyne-1", note) }},
		{"reopen", "open", func(a string, m bool) string { return assignedItemJSON("open", note, a, m) },
			func(c Client) (WorkItem, error) { return c.Reopen(context.Background(), "yoyodyne-1", note, parking) }},
		{"block", "blocked", func(a string, m bool) string { return assignedItemJSON("blocked", note, a, m) },
			func(c Client) (WorkItem, error) { return c.Block(context.Background(), "yoyodyne-1", note) }},
		{"unblock", "open", func(a string, m bool) string { return assignedItemJSON("open", note, a, m) },
			func(c Client) (WorkItem, error) { return c.Unblock(context.Background(), "yoyodyne-1", note) }},
		{"complete", "closed", func(a string, m bool) string { return assignedItemJSON("closed", note, a, m) },
			func(c Client) (WorkItem, error) { return c.Complete(context.Background(), "yoyodyne-1", note) }},
	}
	for _, giveBack := range giveBacks {
		t.Run(giveBack.name+" clears the claim's assignee", func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{responses: []string{
				giveBack.answer("Mason Bryant", true),
				giveBack.answer("", false),
			}}
			item, err := giveBack.call(Client{Runner: runner})
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if item.Assignee != "" || item.ClaimAssignee || item.Status != giveBack.status {
				t.Fatalf("item = %#v, want it %s with no assignee", item, giveBack.status)
			}
			if len(runner.args) != 2 || !reflect.DeepEqual(runner.args[1], clearClaimAssigneeArgs) {
				t.Fatalf("bd args = %#v, want the give-back then %#v", runner.args, clearClaimAssigneeArgs)
			}
		})
		t.Run(giveBack.name+" leaves a person's assignee", func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{responses: []string{giveBack.answer("Mason Bryant", false)}}
			item, err := giveBack.call(Client{Runner: runner})
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if item.Assignee != "Mason Bryant" {
				t.Fatalf("assignee = %q, want the person's name left as it was", item.Assignee)
			}
			if len(runner.args) != 1 {
				t.Fatalf("bd args = %#v, want the give-back and nothing else", runner.args)
			}
		})
	}

	// A clear the tracker refuses does not undo the give-back, which is what
	// frees the item: it answers with the item still marked, so every surface
	// says the name is left over rather than a person's.
	runner := &fakeRunner{
		responses: []string{assignedItemJSON("open", note, "Mason Bryant", true)},
		failures:  map[int]execution.ProcessResult{1: {Status: execution.ProcessFailed, ExitCode: 1, Stderr: "database is locked"}},
	}
	item, err := (Client{Runner: runner}).Release(context.Background(), "yoyodyne-1", note)
	if err != nil {
		t.Fatalf("Release() with a refused clear error = %v, want the release to stand", err)
	}
	if item.Status != "open" || !item.HeldByClaimAssignee() {
		t.Fatalf("Release() = %#v, want the item open and still marked as the claim's", item)
	}
}

// The clean-up's write clears the assignee and any marker, appends why, and
// refuses to clear with nothing said.
func TestClearLeftoverAssigneeSaysWhy(t *testing.T) {
	t.Parallel()

	const note = "a run's claim left this assignee"
	runner := &fakeRunner{responses: []string{assignedItemJSON("open", note, "", false)}}
	item, err := (Client{Runner: runner}).ClearLeftoverAssignee(context.Background(), "yoyodyne-1", note)
	if err != nil {
		t.Fatalf("ClearLeftoverAssignee() error = %v", err)
	}
	if item.Assignee != "" {
		t.Fatalf("ClearLeftoverAssignee() = %#v, want no assignee", item)
	}
	want := [][]string{{"update", "yoyodyne-1", "--assignee=", "--unset-metadata=yoyodyne_claim_assignee", "--append-notes=" + FrameNote(note), "--json"}}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("bd args = %#v, want %#v", runner.args, want)
	}
	if _, err := (Client{Runner: &fakeRunner{}}).ClearLeftoverAssignee(context.Background(), "yoyodyne-1", " "); err == nil {
		t.Fatal("ClearLeftoverAssignee() with no note error = nil")
	}
	still := &fakeRunner{responses: []string{assignedItemJSON("open", note, "Mason Bryant", false)}}
	if _, err := (Client{Runner: still}).ClearLeftoverAssignee(context.Background(), "yoyodyne-1", note); err == nil ||
		!strings.Contains(err.Error(), "still names") {
		t.Fatalf("ClearLeftoverAssignee() not cleared error = %v", err)
	}
}

// Against the installed bd: a claim marks the assignee it sets, a release
// clears it, and an assignee a person set before the claim survives the run.
func TestClaimAssigneeConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	created, err := client.Create(ctx, NewWorkItem{Title: "Claimed by a run", Description: "Nobody set an assignee.", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	claimed, _, err := client.Claim(ctx, created.ID)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Assignee == "" || !claimed.HeldByClaimAssignee() {
		t.Fatalf("Claim() = %#v, want an assignee marked as the claim's", claimed)
	}
	released, err := client.Release(ctx, created.ID, "the run ended")
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	shown, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if released.Assignee != "" || shown.Assignee != "" || shown.ClaimAssignee {
		t.Fatalf("after the release the item names %q (answered %q, marked %v), want nobody", shown.Assignee, released.Assignee, shown.ClaimAssignee)
	}

	byHand, err := client.Create(ctx, NewWorkItem{Title: "Assigned by a person", Description: "A person set the assignee.", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The tracker's actor is the checkout's Git name, so this is the one person
	// whose assignment a claim goes through rather than refusing.
	runCommand(t, project, "bd", "update", byHand.ID, "--assignee=Yoyodyne Test")
	claimed, _, err = client.Claim(ctx, byHand.ID)
	if err != nil {
		t.Fatalf("Claim() over a person's assignment error = %v", err)
	}
	if claimed.ClaimAssignee {
		t.Fatalf("Claim() marked a person's assignee as its own: %#v", claimed)
	}
	if _, err := client.Complete(ctx, byHand.ID, "landed"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	shown, err = client.Show(ctx, byHand.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if shown.Assignee != "Yoyodyne Test" {
		t.Fatalf("after closing the item names %q, want the person's assignment left", shown.Assignee)
	}
}
