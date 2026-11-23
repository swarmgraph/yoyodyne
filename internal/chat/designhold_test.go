package chat

// The needs-design label in a conversation (docs/designs/needs-design-gate.md):
// the Lead Product Manager, the development manager, and a program manager in
// its lane put it on; only the architect takes it off; and her removal is
// refused while the design the item waits on is not readable by its developer.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func designHoldSession(role domain.AgentRole, lane string) *Session {
	session := &Session{options: Options{Lane: lane}}
	session.state.Role = role
	return session
}

// Each role allowed to set the label may, through its own path — the label
// action, or the labels a creation carries — and every other role is refused.
func TestOnlyTheRolesAllowedToMayPutTheNeedsDesignLabelOn(t *testing.T) {
	t.Parallel()

	add := TrackerAction{Action: actionLabel, ID: "yoyodyne-ifd.1", Add: domain.NeedsDesignLabel, Reason: "how should the retry behave?"}
	parent := "yoyodyne-ifd.1"
	create := TrackerAction{Action: actionCreate, Title: "Build", Description: "d", Goal: recordedGoal, Parent: &parent,
		Labels: []string{domain.NeedsDesignLabel}, Kind: domain.WorkItemKindFeature, Reason: "the behaviour is undecided"}
	for _, setter := range []struct {
		role domain.AgentRole
		lane string
	}{
		{domain.RoleProductManager, ""},
		{domain.RoleDevelopmentManager, ""},
		{domain.RoleProgramManager, testLane},
	} {
		session := designHoldSession(setter.role, setter.lane)
		for _, action := range []TrackerAction{add, create} {
			if err := session.authorize(parsedReply{Actions: []TrackerAction{action}}); err != nil {
				t.Errorf("the %s putting the label on through %q: authorize() = %v, want it permitted", setter.role, action.Action, err)
			}
		}
	}
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDeveloper, domain.RoleReviewer} {
		err := designHoldSession(role, "").authorize(parsedReply{Actions: []TrackerAction{add}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) {
			t.Errorf("the %s putting the label on: authorize() = %v, want it refused", role, err)
		}
	}
}

// Taking the label off is the architect's alone; every role that may otherwise
// label is refused it, and the architect may take off only that label.
func TestOnlyTheArchitectTakesTheNeedsDesignLabelOff(t *testing.T) {
	t.Parallel()

	remove := TrackerAction{Action: actionLabel, ID: "yoyodyne-ifd.1", Remove: domain.NeedsDesignLabel, Reason: "the design landed"}
	for _, labeller := range []struct {
		role domain.AgentRole
		lane string
	}{
		{domain.RoleProductManager, ""},
		{domain.RoleDevelopmentManager, ""},
		{domain.RoleProgramManager, testLane},
	} {
		err := designHoldSession(labeller.role, labeller.lane).authorize(parsedReply{Actions: []TrackerAction{remove}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "only the architect removes it") {
			t.Errorf("the %s taking the label off: authorize() = %v, want it refused naming the architect", labeller.role, err)
		}
	}

	architect := designHoldSession(domain.RoleArchitect, "")
	if err := architect.authorize(parsedReply{Actions: []TrackerAction{remove}}); err != nil {
		t.Fatalf("the architect taking the label off: authorize() = %v, want it permitted", err)
	}
	for _, other := range []TrackerAction{
		{Action: actionLabel, ID: "yoyodyne-ifd.1", Remove: "bug", Reason: "not a bug"},
		{Action: actionLabel, ID: "yoyodyne-ifd.1", Add: "reliability", Reason: "a stall fix"},
	} {
		err := architect.authorize(parsedReply{Actions: []TrackerAction{other}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "the one change this role makes") {
			t.Errorf("the architect's label %+v: authorize() = %v, want it refused", other, err)
		}
	}
	// Labelling is the one tracker write she gains: nothing else about an item
	// another role owns becomes hers.
	authority, _ := AuthorityFor(domain.RoleArchitect)
	for _, action := range []string{actionUpdate, actionCreate, actionPark, actionClose, actionReprioritize} {
		if authority.MayAct(action) {
			t.Errorf("the architect may ask for %q, want only the needs-design removal among the writes", action)
		}
	}
}

type fakeLandedDesigns struct {
	design LandedDesign
	found  bool
	err    error
	asked  []string
}

func (f *fakeLandedDesigns) NamingItem(_ context.Context, id string) (LandedDesign, bool, error) {
	f.asked = append(f.asked, id)
	return f.design, f.found, f.err
}

// architectRemoval runs the architect's removal of the label from one item
// through a whole turn, and returns what the tracker was asked to write.
func architectRemoval(t *testing.T, item beads.WorkItem, landed LandedDesigns) (TrackerOutcome, *fakeTracker) {
	t.Helper()
	tracker := &fakeTracker{items: map[string]beads.WorkItem{item.ID: item}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-architect", FinalText: trackerReply("The design is settled; lifting the hold.",
			`{"action":"label","id":"`+item.ID+`","remove":"needs-design","reason":"retries back off exponentially, as the design says"}`)},
		{SessionID: "session-architect", FinalText: "Done."},
	}})
	options.Role = domain.RoleArchitect
	options.Tracker = tracker
	options.LandedDesigns = landed
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "lift the hold on the retry item")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v, want the one removal", reply.Actions)
	}
	return reply.Actions[0], tracker
}

// While no design is readable where the design gate says it must be, the
// removal is refused and the label stays; a ruling in the notes does not count.
func TestTheArchitectsRemovalIsRefusedWhileNoDesignIsReadable(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{ID: "yoyodyne-ifd.431.5", Title: "Atomic JSON replacement", Status: "open",
		Labels: []string{domain.NeedsDesignLabel}, Notes: "Ruling: replace through a temporary file and rename."}
	landed := &fakeLandedDesigns{}
	outcome, tracker := architectRemoval(t, item, landed)
	if outcome.Applied || !strings.Contains(outcome.Failure, "no design committed in the designs home") ||
		!strings.Contains(outcome.Failure, "A ruling in the notes alone does not lift it") {
		t.Fatalf("outcome = %#v, want the removal refused for want of a readable design", outcome)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("updates = %#v, want nothing written while the design is unreadable", tracker.updates)
	}
	if strings.Join(landed.asked, " ") != item.ID {
		t.Fatalf("landed designs asked = %v, want the designs home asked about the item", landed.asked)
	}

	// A design field past the bound is not the readable design either.
	item.Design = strings.Repeat("x", MaxDesignFieldBytes+1)
	outcome, tracker = architectRemoval(t, item, &fakeLandedDesigns{})
	if outcome.Applied || !strings.Contains(outcome.Failure, "past the 8192 a design field may carry") || len(tracker.updates) != 0 {
		t.Fatalf("outcome = %#v, updates = %#v, want an oversized design field refused", outcome, tracker.updates)
	}

	// A designs home that could not be read lifts nothing.
	item.Design = ""
	outcome, tracker = architectRemoval(t, item, &fakeLandedDesigns{err: errors.New("git grep failed")})
	if outcome.Applied || !strings.Contains(outcome.Failure, "could not be read") || len(tracker.updates) != 0 {
		t.Fatalf("outcome = %#v, want an unreadable designs home refused", outcome)
	}
}

// A design in the item's own field lifts the label, and the removal records
// that form.
func TestTheArchitectRemovesTheLabelOnceTheDesignIsInTheDesignField(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{ID: "yoyodyne-ifd.431.5", Title: "Atomic JSON replacement", Status: "open",
		Labels: []string{domain.NeedsDesignLabel, "reliability"}, Design: "Write to a temporary file beside the target and rename it over."}
	landed := &fakeLandedDesigns{}
	outcome, tracker := architectRemoval(t, item, landed)
	if !outcome.Applied {
		t.Fatalf("outcome = %#v, want the removal carried out", outcome)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v, want one write", tracker.updates)
	}
	change := tracker.updates[0].change
	if strings.Join(change.RemoveLabels, " ") != domain.NeedsDesignLabel || len(change.AddLabels) != 0 {
		t.Fatalf("change = %#v, want exactly the needs-design label removed", change)
	}
	if !strings.Contains(change.AppendNotes, "Label needs-design removed: the design is in the item's own design field") ||
		!strings.Contains(change.AppendNotes, "by the architect") ||
		!strings.Contains(change.AppendNotes, "Reason: retries back off exponentially") {
		t.Fatalf("notes = %q, want the removal, its form, who made it, and why recorded", change.AppendNotes)
	}
	if len(landed.asked) != 0 {
		t.Fatalf("landed designs asked = %v, want the design field to settle it without reading the designs home", landed.asked)
	}
}

// A design landed in the designs home naming the item lifts the label, and the
// removal records which document and commit it was.
func TestTheArchitectRemovesTheLabelOnceADesignNamingTheItemHasLanded(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{ID: "yoyodyne-ifd.431.5", Title: "Atomic JSON replacement", Status: "open",
		Labels: []string{domain.NeedsDesignLabel}}
	landed := &fakeLandedDesigns{found: true, design: LandedDesign{
		Path: "docs/designs/atomic-writes.md", ID: "atomic-writes", Commit: "0123456789abcdef0123",
		At: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Reason: "yoyodyne-ifd.431.5: how a JSON file is replaced",
	}}
	outcome, tracker := architectRemoval(t, item, landed)
	if !outcome.Applied || len(tracker.updates) != 1 {
		t.Fatalf("outcome = %#v, updates = %#v, want the removal carried out", outcome, tracker.updates)
	}
	notes := tracker.updates[0].change.AppendNotes
	if !strings.Contains(notes, "the design is landed in docs/designs/atomic-writes.md (atomic-writes) at commit 0123456789ab") {
		t.Fatalf("notes = %q, want the landed design named", notes)
	}
}

// The Lead Product Manager's label records the question beside the label, which
// is what the architect reads.
func TestTheLeadProductManagerPutsTheLabelOnWithTheQuestion(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.431.5": {ID: "yoyodyne-ifd.431.5", Title: "Atomic JSON replacement", Status: "open"},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Holding it for the architect.",
			`{"action":"label","id":"yoyodyne-ifd.431.5","add":"needs-design","reason":"should a replacement survive a crash mid-write, and how?"}`)},
		{SessionID: "session-1", FinalText: "Held."},
	}})
	options.Tracker = tracker
	reply, err := openTestSession(t, options).Send(context.Background(), "431.5 needs a design")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.updates) != 1 {
		t.Fatalf("actions = %#v, updates = %#v, want the label put on", reply.Actions, tracker.updates)
	}
	change := tracker.updates[0].change
	if strings.Join(change.AddLabels, " ") != domain.NeedsDesignLabel ||
		!strings.Contains(change.AppendNotes, "Labelled needs-design by the Lead Product Manager") ||
		!strings.Contains(change.AppendNotes, "should a replacement survive a crash mid-write") {
		t.Fatalf("change = %#v, want the label, who set it, and the question recorded", change)
	}
}

// Every role that may set the label is told what it does and who lifts it, and
// the architect is told the one label action she has.
func TestTheContractsSayWhoSetsAndWhoRemovesTheLabel(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleProgramManager} {
		if contract := SystemPrompt(role, Admission{}, nil, ""); !strings.Contains(contract, "Only the architect takes it off") {
			t.Errorf("the %s contract does not say who removes the needs-design label", role)
		}
	}
	architect := SystemPrompt(domain.RoleArchitect, Admission{}, nil, "")
	for _, required := range []string{`{"action":"label","id":"beads-id","remove":"needs-design"`, "refuses the removal while neither is true"} {
		if !strings.Contains(architect, required) {
			t.Errorf("the architect contract does not state %q", required)
		}
	}
}

func TestNamesWorkItemMatchesTheItemAndNotItsChildren(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		text string
		want bool
	}{
		{"yoyodyne-2kr.2: the label's rules", true},
		{"designs the label for yoyodyne-2kr.2.", true},
		{"(yoyodyne-2kr.2)", true},
		{"yoyodyne-2kr.21: another item", false},
		{"yoyodyne-2kr.2.1 is a child", false},
		{"xyoyodyne-2kr.2 is not it", false},
		{"yoyodyne-2kr.21 and then yoyodyne-2kr.2 later", true},
		{"nothing here", false},
	} {
		if got := NamesWorkItem(tc.text, "yoyodyne-2kr.2"); got != tc.want {
			t.Errorf("NamesWorkItem(%q) = %t, want %t", tc.text, got, tc.want)
		}
	}
}

// Landed means committed: a design whose revision names the item counts at the
// commit HEAD names, and the same file written but not committed does not.
func TestGitLandedDesignsReadsOnlyWhatIsCommitted(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repository := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = repository
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	design := func(id, reason string) string {
		return "---\nid: " + id + "\nkind: design\ntitle: A design\nsupports:\n    - v1-goals\nstatus: active\nrevisions:\n" +
			"    - action: created\n      by: architect\n      at: 2026-10-20T12:00:00Z\n      reason: '" + reason + "'\n---\n# A design\n\nBody.\n"
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(repository, "docs", "designs", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("child.md", design("child", "yoyodyne-2kr.2.1: a child item, not this one"))
	write("mentions.md", design("mentions", "written for something else")+"\nIt mentions yoyodyne-2kr.2 in its prose only.\n")
	git("add", ".")
	git("commit", "-q", "-m", "designs")
	write("pending.md", design("pending", "yoyodyne-2kr.2: written and not landed"))

	landed := GitLandedDesigns{Process: execution.OSProcessRunner{}, Directory: repository, Home: "docs/designs"}
	if found, ok, err := landed.NamingItem(context.Background(), "yoyodyne-2kr.2"); err != nil || ok {
		t.Fatalf("NamingItem() = %#v, %t, %v, want nothing landed naming the item", found, ok, err)
	}

	git("add", ".")
	git("commit", "-q", "-m", "land the design")
	found, ok, err := landed.NamingItem(context.Background(), "yoyodyne-2kr.2")
	if err != nil || !ok {
		t.Fatalf("NamingItem() = %#v, %t, %v, want the landed design", found, ok, err)
	}
	if found.Path != "docs/designs/pending.md" || found.ID != "pending" || found.Commit == "" {
		t.Fatalf("found = %#v, want the landed design named with its commit", found)
	}
}
