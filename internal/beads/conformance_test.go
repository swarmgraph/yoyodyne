package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// conformanceTimeout bounds one bd command in these checks. Nothing here reaches
// a network -- both remotes are bare repositories on this machine -- so the only
// thing the bound has to catch is a bd that has hung, and any figure catches
// that. What it must not catch is a bd that is merely slow: the first call in a
// project starts its database engine, every check here starts its own, and the
// suite runs them in parallel beside whatever else the machine is doing. At
// ninety seconds that is what it caught -- `bd create` in
// TestParentFieldConformance ran past it under -race with two check passes on
// the machine, where the whole test takes about a minute on its own.
//
// So it is sized for the loaded machine rather than the idle one, as the
// Makefile's TEST_TIMEOUT is, and kept under that figure so a hung bd is still
// ended here and reported as the command it was rather than by the test
// binary's own timeout.
const conformanceTimeout = 10 * time.Minute

// TestSyncRemoteConformance checks the two things about bd this adapter assumes
// and a scripted runner can only restate: that `dolt remote list --json`
// answers with the fields SyncRemotes decodes, and that `dolt remote add` over
// a name the tracker already holds replaces it rather than refusing. The second
// is what `yoyo init --tracker-remote` rests on -- the flag exists to repoint a
// tracker that already has an origin -- so it is checked against bd itself.
func TestSyncRemoteConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)

	root := t.TempDir()
	first := filepath.Join(root, "first.git")
	second := filepath.Join(root, "second.git")
	runCommand(t, root, "git", "init", "-q", "--bare", first)
	runCommand(t, root, "git", "init", "-q", "--bare", second)

	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	configured, err := client.SetSyncRemote(ctx, "origin", first)
	if err != nil {
		t.Fatalf("SetSyncRemote() error = %v", err)
	}
	if configured.Name != "origin" || !strings.Contains(configured.URL, first) {
		t.Fatalf("SetSyncRemote() = %#v, want a remote at %s", configured, first)
	}

	// The case `--tracker-remote` exists for: a tracker that already has an
	// origin, pointed somewhere else.
	repointed, err := client.SetSyncRemote(ctx, "origin", second)
	if err != nil {
		t.Fatalf("SetSyncRemote() over an existing remote error = %v", err)
	}
	if !strings.Contains(repointed.URL, second) {
		t.Fatalf("SetSyncRemote() = %#v, want the remote repointed at %s", repointed, second)
	}

	remotes, err := client.SyncRemotes(ctx)
	if err != nil {
		t.Fatalf("SyncRemotes() error = %v", err)
	}
	if len(remotes) != 1 || remotes[0].Name != "origin" || !strings.Contains(remotes[0].URL, second) {
		t.Fatalf("SyncRemotes() = %#v, want one origin at %s", remotes, second)
	}
}

// TestExecutorMetadataConformance checks the assumption both executor writes
// rest on and a scripted runner can only restate: that bd stores the marker and
// gives it back, in each of the two spellings it takes — the whole metadata
// object a creation carries, and the single key an update sets.
//
// It is here rather than only in the fake because both write paths now refuse a
// marker bd did not store, and a refusal is only correct if bd's answer actually
// carries what it stored. If bd stopped echoing metadata on creation, every
// conversation-executed admission would fail rather than silently go unmarked —
// a loud failure rather than the quiet one, but a failure the fakes could never
// show. The third assertion is the other half: a creation given no metadata
// omits the key, which is what makes a missing executor unambiguously one that
// was not stored rather than one that was never asked for.
func TestExecutorMetadataConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	// A creation carrying the marker gets it back, which is what lets admission
	// refuse a marker that was not stored.
	created, err := client.Create(ctx, NewWorkItem{
		Title:       "Promote the brief",
		Description: "The architect promotes it in conversation.",
		Type:        "task",
		Executor:    domain.ConversationWith(domain.RoleArchitect),
	})
	if err != nil {
		t.Fatalf("Create() with an executor error = %v", err)
	}
	if created.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("Create() executor = %q, want bd to echo the marker it stored", created.Executor)
	}
	// And it survives being read back separately, which is what selection does.
	shown, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if shown.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("Show() executor = %q, want the stored marker", shown.Executor)
	}

	// The other spelling: one key set on an item that already exists, which is how
	// work admitted before the marker existed acquires one.
	ordinary, err := client.Create(ctx, NewWorkItem{Title: "Ordinary work", Description: "A run carries it.", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A creation given no metadata carries no key at all, so an absent executor is
	// unambiguous rather than a default that might have been applied.
	if !ordinary.Executor.DeveloperRun() {
		t.Fatalf("Create() executor = %q, want ordinary work to carry none", ordinary.Executor)
	}
	marked, err := client.Update(ctx, ordinary.ID, WorkItemChange{Executor: domain.ConversationWith(domain.RoleArchitect)})
	if err != nil {
		t.Fatalf("Update() with an executor error = %v", err)
	}
	if marked.Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("Update() executor = %q, want bd to echo the marker it set", marked.Executor)
	}
}

// TestParkingMetadataConformance checks the one assumption a scripted runner
// cannot restate: that bd accepts a metadata value set to nothing, and gives the
// key back as empty rather than as the value it held before.
//
// The release rests entirely on it. Parking and releasing are one write with one
// shape — the same key set to the reason or to nothing — and if bd refused an
// empty value or kept the old one, releasing parked work would fail loudly at
// the read-back check and there would be no way to put a parked item back into
// the queue from the conversation at all.
func TestParkingMetadataConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	const reason = "off the critical path by the scope decision"
	created, err := client.Create(ctx, NewWorkItem{
		Title:       "The thin Codex backend",
		Description: "A second provider, deferred.",
		Type:        "task",
		Parking:     reason,
	})
	if err != nil {
		t.Fatalf("Create() with a parking error = %v", err)
	}
	if created.Parking.Reason() != reason {
		t.Fatalf("Create() parking = %q, want bd to echo the reason it stored", created.Parking)
	}
	// It survives a separate read, which is what selection does.
	shown, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if shown.Parking.Reason() != reason {
		t.Fatalf("Show() parking = %q, want the stored reason", shown.Parking)
	}

	// The other spelling, and the way the queue that provoked this gets parked:
	// one key set on an item that already exists.
	ordinary, err := client.Create(ctx, NewWorkItem{Title: "Ordinary work", Description: "A run carries it.", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if ordinary.Parking.Parked() {
		t.Fatalf("Create() parking = %q, want ordinary work to carry none", ordinary.Parking)
	}
	parking := domain.WorkItemParking(reason)
	parked, err := client.Update(ctx, ordinary.ID, WorkItemChange{Parking: &parking})
	if err != nil {
		t.Fatalf("Update() with a parking error = %v", err)
	}
	if parked.Parking.Reason() != reason {
		t.Fatalf("Update() parking = %q, want bd to echo the reason it set", parked.Parking)
	}

	// The assertion this whole test exists for: the same key set to nothing puts
	// the work back, and reads back as unparked rather than as what it was.
	released := domain.WorkItemParking("")
	back, err := client.Update(ctx, ordinary.ID, WorkItemChange{Parking: &released})
	if err != nil {
		t.Fatalf("Update() releasing a parking error = %v", err)
	}
	if back.Parking.Parked() {
		t.Fatalf("Update() parking = %q after a release, want bd to have cleared it", back.Parking)
	}
	reread, err := client.Show(ctx, ordinary.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if reread.Parking.Parked() {
		t.Fatalf("Show() parking = %q after a release, want released work to read as unparked", reread.Parking)
	}
}

// TestLabelConformance checks what the label writes rest on and a scripted
// runner can only restate: that bd takes labels on a creation and gives them
// back, that an update adds and removes one, that both survive a separate read
// and appear in a listing — which is what a survey reads — and that a child
// created under a labelled parent carries the parent's labels, which is what
// the development manager's contract tells her to expect of a decomposition.
//
// It is the test the admission practice rests on: an item admitted with the
// label is read back through bd carrying it, in each of the three ways the
// harness reads items.
func TestLabelConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	// Admitted with the label, in the one write that admits it.
	created, err := client.Create(ctx, NewWorkItem{
		Title:       "The sweep never clears a preserved-branch stoppage",
		Description: "A stall fix under the reliability directive.",
		Type:        "task",
		Labels:      []string{"reliability"},
	})
	if err != nil {
		t.Fatalf("Create() with a label error = %v", err)
	}
	if !created.HasLabel("reliability") {
		t.Fatalf("Create() labels = %v, want bd to echo the label it stored", created.Labels)
	}
	shown, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if !shown.HasLabel("reliability") {
		t.Fatalf("Show() labels = %v, want the stored label", shown.Labels)
	}
	listed, err := client.List(ctx, "open")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	index := slices.IndexFunc(listed, func(item WorkItem) bool { return item.ID == created.ID })
	if index < 0 || !listed[index].HasLabel("reliability") {
		t.Fatalf("List() = %#v, want the created item listed with its label", listed)
	}

	// Work admitted before the practice acquires the label afterwards, one write
	// each way, and loses it the same way.
	ordinary, err := client.Create(ctx, NewWorkItem{Title: "Ordinary work", Description: "A run carries it.", Type: "task"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(ordinary.Labels) != 0 {
		t.Fatalf("Create() labels = %v, want ordinary work to carry none", ordinary.Labels)
	}
	labelled, err := client.Update(ctx, ordinary.ID, WorkItemChange{AddLabels: []string{"reliability"}, AppendNotes: "Labelled reliability: a stall fix."})
	if err != nil {
		t.Fatalf("Update() adding a label error = %v", err)
	}
	if !labelled.HasLabel("reliability") {
		t.Fatalf("Update() labels = %v, want bd to echo the label it added", labelled.Labels)
	}
	// Adding a label the item carries is a write bd accepts and leaves as it was.
	if again, err := client.Update(ctx, ordinary.ID, WorkItemChange{AddLabels: []string{"reliability"}}); err != nil || !slices.Equal(again.Labels, labelled.Labels) {
		t.Fatalf("Update() adding a label the item carries = %v, %v; want it accepted and unchanged", again.Labels, err)
	}
	bare, err := client.Update(ctx, ordinary.ID, WorkItemChange{RemoveLabels: []string{"reliability"}})
	if err != nil {
		t.Fatalf("Update() removing a label error = %v", err)
	}
	if bare.HasLabel("reliability") {
		t.Fatalf("Update() labels = %v after a removal, want bd to have taken it off", bare.Labels)
	}
	reread, err := client.Show(ctx, ordinary.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if reread.HasLabel("reliability") {
		t.Fatalf("Show() labels = %v after a removal, want none", reread.Labels)
	}

	// A child of a labelled parent inherits the label, merged with its own.
	child, err := client.Create(ctx, NewWorkItem{
		Title: "Carve out the repair half", Description: "d", Type: "task", Parent: created.ID, Labels: []string{"bug"},
	})
	if err != nil {
		t.Fatalf("Create() under a labelled parent error = %v", err)
	}
	if !child.HasLabel("reliability") || !child.HasLabel("bug") {
		t.Fatalf("Create() under a labelled parent labels = %v, want the parent's label beside its own", child.Labels)
	}
}

// TestGoalWitnessConformance pins the bd behaviours the goal-attribution
// hardening rests on, and which every other check covering it can only restate:
// the client's tests drive a scripted runner, so all of them pass identically
// against a bd that refuses these flags or drops what they store.
//
// Three assumptions are checked, in the order one item meets them.
//
// A creation passes --notes and --metadata together, because bd takes a
// creation's whole metadata as one JSON object: the witness rides alongside the
// notes it witnesses rather than in a second call, and a bd that refused the
// pair would fail every goal-carrying admission at runtime on both admission
// paths.
//
// An update passes --append-notes and --set-metadata together, and
// --set-metadata splits its argument at the first = and stores the rest
// verbatim. A goal statement can contain an =, and one stored cut short at it is
// a statement that would be put back wrong.
//
// Metadata survives a replace-style `bd update --notes`. That is the assumption
// the whole attribution-destruction detector rests on: the witness is kept in
// the tracker's metadata precisely because the write that destroys an
// attribution cannot reach it, and a bd version that cleared metadata alongside
// the notes would turn every destroyed attribution back into work nobody ever
// attributed -- the one state the audit deliberately does not fail on. That
// failure is silent, which is why it is checked here rather than trusted.
func TestGoalWitnessConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	const admission = "Admitted to the backlog by the product manager in conversation chat-2f0."
	const admitted = "Run development nearly autonomously, with the product manager as the human's routine interface."
	created, err := client.Create(ctx, NewWorkItem{
		Title:       "Admit the queue",
		Description: "The queue the scheduler pulls from.",
		Type:        "task",
		Notes:       admission + "\n" + goal.Note(admitted),
	})
	if err != nil {
		t.Fatalf("Create() with attributed notes error = %v; bd must accept --notes and --metadata on one creation", err)
	}
	if named, records := goal.NamedIn(created.Notes); !records || named != admitted {
		t.Fatalf("Create() notes name %q (recorded = %v), want %q; bd must store --notes given alongside --metadata",
			named, records, admitted)
	}
	if created.GoalWitness.Statement != admitted {
		t.Fatalf("Create() witness = %#v, want the statement %q; bd must store --metadata given alongside --notes",
			created.GoalWitness, admitted)
	}

	// The other spelling, and the one a re-attribution takes. The statement
	// carries an = so what is checked is the split rather than only the write:
	// --set-metadata=key=value has to divide at the first one.
	const reattributed = "Keep what a run costs visible, so spend = what the operator chose rather than what they discovered."
	updated, err := client.Update(ctx, created.ID, WorkItemChange{AppendNotes: goal.Note(reattributed)})
	if err != nil {
		t.Fatalf("Update() re-attributing error = %v; bd must accept --append-notes and --set-metadata on one update", err)
	}
	if !strings.Contains(updated.Notes, admission) {
		t.Fatalf("Update() notes = %q, want what the item already recorded still in them; --append-notes must add rather than replace",
			updated.Notes)
	}
	if named, _ := goal.NamedIn(updated.Notes); named != reattributed {
		t.Fatalf("Update() notes name %q, want the appended attribution %q", named, reattributed)
	}
	if updated.GoalWitness.Statement != reattributed {
		t.Fatalf("Update() witness = %#v, want %q stored verbatim; --set-metadata must split at the first = and keep the rest",
			updated.GoalWitness, reattributed)
	}

	// The write nothing in this package makes, and the one the witness exists to
	// outlive: an item's notes replaced wholesale from outside the harness. It is
	// spelled as bd itself because the client has no way to spell it.
	runCommand(t, project, "bd", "update", created.ID, "--notes=Rewritten wholesale by a careless writer.")
	destroyed, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	// The destruction has to have actually happened, or what follows proves
	// nothing about what survives it.
	if named, records := goal.NamedIn(destroyed.Notes); records {
		t.Fatalf("Show() notes still name %q after a wholesale replacement, so this check says nothing about the witness", named)
	}
	if destroyed.GoalWitness.Statement != reattributed {
		t.Fatalf("Show() witness = %#v after the notes were replaced, want %q still recorded; a bd that clears an item's "+
			"metadata when its notes are replaced makes every destroyed attribution read as work nobody ever attributed",
			destroyed.GoalWitness, reattributed)
	}
}

// TestAppendedNoteDurabilityConformance replays the shape yoyodyne-ifd.283 was
// in when two operator directions were reported written to it and read back as
// absent: an item whose notes are already tens of kilobytes, appended to twice in
// a row, in the state that item was actually in.
//
// It is here rather than only against the fake because the fake replays whatever
// it was handed, so every scripted check of the read-back passes identically
// against a bd that drops a long append, drops one onto an item whose notes are
// already large, or keeps only the last of two. What that would cost is the whole
// of what this item is about: notes are where the reasoning behind a decision is
// recorded, and one silently dropped is reasoning nobody knows is gone.
//
// The 283 notes were 64 kilobytes when the directions were written, so the size
// here is that order rather than a token one, and the append is the operator's
// own paragraph rather than a word.
func TestAppendedNoteDurabilityConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	// An item carrying the accumulated record of a long-lived piece of work: the
	// admission and the goal at the front, and every run's outcome after it.
	var history strings.Builder
	history.WriteString("Admitted to the backlog by the product manager in conversation chat-2f0.\n\n")
	history.WriteString(goal.Note("Run development nearly autonomously, with the product manager as the human's routine interface."))
	for len(history.String()) < 64<<10 {
		fmt.Fprintf(&history, "\nYoyodyne stopped this item: a configured check still failed after every permitted attempt.\n"+
			"Repair attempts: 2 of 2 permitted\nRun: run-%030d\n", history.Len())
	}
	created, err := client.Create(ctx, NewWorkItem{
		Title:       "The development manager's hourly sweep",
		Description: "Every hour the harness wakes the development manager to look for unresolved issues.",
		Type:        "task",
		Notes:       history.String(),
	})
	if err != nil {
		t.Fatalf("Create() with a long history error = %v", err)
	}

	const first = "Scope addition, operator-directed 2026-09-07: FORGE HYGIENE joins the sweep's findings. The sweep reads the " +
		"tracker but not the forge today, which is why 30 open pull requests accumulated unnoticed - most superseded by later " +
		"runs that landed their items. The development manager's hourly sweep reports open pull requests whose items are " +
		"closed, or whose branches are already contained in the target branch, as findings; the closing machinery is " +
		"yoyodyne-ifd.69. The operator's division: the sweep does the noticing, 69 does the closing."
	const second = "Noted by the product manager, after turn 479.\n\nReason: The operator directed this twice and it is not on " +
		"the item; re-issued and verified by reading rather than by trusting the confirmation line."

	// Twice in a row, which is how it was actually written, and each write is the
	// one the caller was told had landed.
	for _, note := range []string{first, second} {
		updated, err := client.Update(ctx, created.ID, WorkItemChange{AppendNotes: note})
		if err != nil {
			t.Fatalf("Update() appending to a long history error = %v; a note reported as written must be on the item", err)
		}
		if !strings.Contains(updated.Notes, note) {
			t.Fatalf("Update() answered with notes that do not carry what it appended; bd must echo the notes it stored")
		}
	}

	// The description is confirmed off the same answer, so what bd echoes for it is
	// pinned here too: a bd that stopped carrying the field would send every
	// description edit through a second read rather than failing, and this is what
	// would say so.
	const scope = "Every hour the harness wakes the development manager, and the sweep reads the forge as well as the tracker."
	rewritten, err := client.Update(ctx, created.ID, WorkItemChange{Description: scope})
	if err != nil {
		t.Fatalf("Update() replacing the description error = %v; a description reported as written must be on the item", err)
	}
	if rewritten.Description != scope {
		t.Fatalf("Update() description = %q, want bd to echo the description it stored", rewritten.Description)
	}

	// Read back separately, which is what the product manager and the operator both
	// do when they check a write rather than trusting the confirmation.
	shown, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	for _, note := range []string{first, second} {
		if !strings.Contains(shown.Notes, note) {
			t.Fatalf("the item does not carry a note that was reported applied, so a confirmed note write is not durable:\n%s",
				shown.Notes[max(0, len(shown.Notes)-2000):])
		}
	}
	// What the item said before is still there. An append that replaced the history
	// would be the attribution loss this project already has a guard for, arriving
	// through the path that guard does not cover.
	if named, records := goal.NamedIn(shown.Notes); !records || !strings.Contains(shown.Notes, "chat-2f0") {
		t.Fatalf("appending to the notes took the item's own record with it: goal recorded = %v, named %q", records, named)
	}
}

// TestConcurrentWriteConformance holds the adapter to what bd itself does not
// do: two writes to one item that overlap both survive.
//
// bd 1.1.2 loses one of them now and then. An append reads the notes already
// there and writes them back with its line added, a metadata key is set the
// same way, and two that overlap each read the same notes; the one that finishes
// second writes over the first, and both exit 0 answering with their own line in
// place. Against bd directly, six appends and three other writes at once to one
// item lost an append or a key in 4 of 15 batches
// (docs/diagnoses/yoyodyne-ifd-433-23-concurrent-writes-to-one-item.md).
// Client.write queues writes to one item on a lock every client of the store
// shares, and this makes the overlapping writes a run, a reconcile, and a
// conversation make to one item, through separate clients as separate
// processes would, and reads every one of them back.
//
// A write the conformance bound ended is not judged, because whether it landed
// depends on where bd was when it was ended; it is logged by name. Every other
// error fails, and so does any write that answered and is not on the item.
func TestConcurrentWriteConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	version := bdVersion(t, project)
	ctx := context.Background()
	newClient := func() Client {
		return Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	}

	contested, err := newClient().Create(ctx, NewWorkItem{
		Title:       "The item everything writes to",
		Description: "A run, a reconcile, and a conversation all reach it.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	const appends = 8
	landing := "0123456789abcdef0123456789abcdef01234567"
	cost := Cost{TotalUSD: 1.25, Runs: 1}
	type outcome struct {
		who string
		err error
	}
	outcomes := make(chan outcome, appends+2)
	var writing sync.WaitGroup
	write := func(who string, do func(Client) error) {
		writing.Add(1)
		go func() {
			defer writing.Done()
			outcomes <- outcome{who: who, err: do(newClient())}
		}()
	}
	for writer := 0; writer < appends; writer++ {
		write(fmt.Sprintf("append %d", writer), func(c Client) error {
			_, err := c.RecordOutcome(ctx, contested.ID, appendedBy(writer))
			return err
		})
	}
	write("cost", func(c Client) error {
		_, err := c.RecordCost(ctx, contested.ID, cost)
		return err
	})
	write("landing", func(c Client) error {
		_, err := c.RecordLanding(ctx, contested.ID, landing)
		return err
	})
	writing.Wait()
	close(outcomes)

	stalled := map[string]bool{}
	for result := range outcomes {
		switch {
		case result.err == nil:
		case endedByBound(result.err):
			stalled[result.who] = true
			t.Logf("%s: ended by the %s conformance bound before bd answered, so it is not read back: %v", result.who, conformanceTimeout, result.err)
		default:
			t.Errorf("%s: overlapping write to one item failed: %v", result.who, result.err)
		}
	}

	item, err := newClient().Show(ctx, contested.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	judged := 0
	for writer := 0; writer < appends; writer++ {
		if stalled[fmt.Sprintf("append %d", writer)] {
			continue
		}
		judged++
		if !strings.Contains(item.Notes, appendedBy(writer)) {
			t.Errorf("work item %s notes = %q after %d overlapping appends, want append %d's in them: it answered "+
				"success and is not on the item, which is a write bd lost because the adapter let it overlap another",
				contested.ID, item.Notes, appends, writer)
		}
	}
	if !stalled["cost"] {
		judged++
		if item.Cost == nil || formatCost(item.Cost.TotalUSD) != formatCost(cost.TotalUSD) || item.Cost.Runs != cost.Runs {
			t.Errorf("work item %s cost = %#v after overlapping writes, want %#v: the price answered success and is not on the item", contested.ID, item.Cost, cost)
		}
	}
	if !stalled["landing"] {
		judged++
		if item.Landing != landing {
			t.Errorf("work item %s landing = %q after overlapping writes, want %q: the landing answered success and is not on the item", contested.ID, item.Landing, landing)
		}
	}
	if judged == 0 {
		t.Skipf("SKIPPED, not passed: every write against %s was ended by the conformance bound, so nothing was left to read back", version)
	}
	t.Logf("RAN against %s: %d of %d overlapping writes to one item were judged by reading it back", version, judged, appends+2)
}

// appendedBy is what one concurrent writer appends, distinct per writer so a
// write that was accepted and dropped is told from one that never happened.
func appendedBy(writer int) string {
	return fmt.Sprintf("outcome recorded by writer %d", writer)
}

// endedByBound reports whether a bd write failed because a bound ended it --
// the client's on the invocation, or the same bound on the wait for its item's
// lock -- rather than because bd answered with an error.
func endedByBound(err error) bool {
	var failed processFailure
	if errors.As(err, &failed) && failed.status == execution.ProcessTimedOut {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// bdVersion is the bd a conformance check ran against, which is what its
// verdict is about: these checks pin somebody else's software, and a pass
// against one version says nothing about the next.
func bdVersion(t *testing.T, dir string) string {
	t.Helper()

	command := exec.Command("bd", "version")
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bd version error = %v: %s", err, output)
	}
	return strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
}

// TestWritesToOneItemQueue holds Client.write to the queue it exists for,
// without a bd: a write to an item whose lock another writer holds waits for
// it, and a write to a different item does not.
func TestWritesToOneItemQueue(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	storeAt(t, filepath.Join(project, ".beads"), "metadata.json")
	ctx := context.Background()
	held := Client{Dir: project, Timeout: time.Minute}
	release, err := held.lockItem(ctx, "yoyodyne-1")
	if err != nil {
		t.Fatalf("lockItem() error = %v", err)
	}

	other := Client{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: "{}"}}}, Dir: project, Timeout: time.Minute}
	if _, err := other.write(ctx, "yoyodyne-2", "update", "yoyodyne-2"); err != nil {
		t.Fatalf("write() to a different item error = %v, want it made while yoyodyne-1 is held", err)
	}

	queued := Client{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: "{}"}}}, Dir: project, Timeout: 200 * time.Millisecond}
	if _, err := queued.write(ctx, "yoyodyne-1", "update", "yoyodyne-1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write() to an item whose lock is held error = %v, want it to wait out its bound rather than run", err)
	}

	// A client pointed at a subdirectory reaches the same store bd would, so it
	// queues on the same lock rather than writing past it.
	nested := filepath.Join(project, "internal", "beads")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	below := Client{Runner: &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: "{}"}}}, Dir: nested, Timeout: 200 * time.Millisecond}
	if _, err := below.write(ctx, "yoyodyne-1", "update", "yoyodyne-1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write() from a subdirectory to an item whose lock is held error = %v, want it to queue on the same lock", err)
	}

	release()
	if _, err := queued.write(ctx, "yoyodyne-1", "update", "yoyodyne-1"); err != nil {
		t.Fatalf("write() after the lock was released error = %v", err)
	}
}

// TestStoreDirectoryFollowsBd holds the lock's location to the store bd itself
// writes to from the same directory: a lock taken anywhere else is one a second
// writer to the same store never meets, which is the unqueued write the lock
// exists to remove.
func TestStoreDirectoryFollowsBd(t *testing.T) {
	t.Parallel()

	main := canonicalPath(t.TempDir())
	if err := os.Mkdir(filepath.Join(main, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(main, ".beads")
	storeAt(t, store, "metadata.json", "embeddeddolt/")

	// A linked worktree checks out the tracked half of .beads and not the
	// database, so bd goes to the main repository's store from it.
	worktree := filepath.Join(main, "worktrees", "one")
	gitDir := filepath.Join(main, ".git", "worktrees", "one")
	storeAt(t, gitDir)
	storeAt(t, filepath.Join(worktree, ".beads"), "metadata.json")
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")
	writeFile(t, filepath.Join(gitDir, "commondir"), "../..\n")

	// A worktree whose .beads redirects names the store it redirects to.
	redirected := filepath.Join(main, "worktrees", "two")
	redirectedGitDir := filepath.Join(main, ".git", "worktrees", "two")
	storeAt(t, redirectedGitDir)
	storeAt(t, filepath.Join(redirected, ".beads"), "metadata.json")
	writeFile(t, filepath.Join(redirected, ".beads", "redirect"), "# the main checkout's store\n../../.beads\n")
	writeFile(t, filepath.Join(redirected, ".git"), "gitdir: "+redirectedGitDir+"\n")
	writeFile(t, filepath.Join(redirectedGitDir, "commondir"), "../..\n")

	// A symlink to the checkout is the same store reached another way.
	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(main, linked); err != nil {
		t.Fatal(err)
	}

	// A checkout with no store anywhere above it is one bd finds nothing in.
	bare := canonicalPath(t.TempDir())
	if err := os.Mkdir(filepath.Join(bare, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, dir, env, want string
	}{
		{name: "the checkout itself", dir: main, want: store},
		{name: "a subdirectory of it", dir: filepath.Join(main, "worktrees"), want: store},
		{name: "a linked worktree", dir: worktree, want: store},
		{name: "a subdirectory of a linked worktree", dir: filepath.Join(worktree, ".beads"), want: store},
		{name: "a redirected worktree", dir: redirected, want: store},
		{name: "a symlink to the checkout", dir: linked, want: store},
		{name: "BEADS_DIR over the directory", dir: bare, env: store, want: store},
		{name: "no store", dir: bare, want: ""},
	} {
		if got := storeDirectory(test.dir, test.env); got != test.want {
			t.Errorf("%s: storeDirectory(%q) = %q, want %q", test.name, test.dir, got, test.want)
		}
	}
}

// storeAt makes dir with the named files in it, a trailing slash naming a
// directory.
func storeAt(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeFile(t, filepath.Join(dir, name), "{}\n")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDecompositionEdgeConformance pins the direction bd states decomposition
// in, which until now was pinned only by a payload captured by hand: a scripted
// runner replays the direction it was written with, so it agrees with itself
// whichever way bd actually answers.
//
// The direction is what a listing turns on. bd states parentage as an edge
// attributed to the child and naming the parent, and the epic beside it carries
// no such edge of its own; a reading with those the other way round would see an
// epic as work broken out of its own child, defer the child, and run the epic.
// That is a failure the guard could have rather than one it has had:
// yoyodyne-ifd.121 and the child carrying its execution were started as two
// developer runs of one scope thirteen hours before any parentage-keyed guard
// existed, so the double-run says nothing about the direction either way. See
// docs/diagnoses/yoyodyne-ifd-273-121-double-run-mechanism.md.
//
// The edge is asserted directly rather than only through DecomposedFrom, because
// bd answers the parent as a field as well, and a reading that found the field
// would report the right answer whatever the edge said.
func TestDecompositionEdgeConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	epic, err := client.Create(ctx, NewWorkItem{
		Title:       "Split the README",
		Description: "The work it was broken into is below it.",
		Type:        "epic",
	})
	if err != nil {
		t.Fatalf("Create() an epic error = %v", err)
	}
	child, err := client.Create(ctx, NewWorkItem{
		Title:       "Execute the README split",
		Description: "One piece of it.",
		Type:        "task",
		Parent:      epic.ID,
	})
	if err != nil {
		t.Fatalf("Create() a child error = %v; bd must accept --parent", err)
	}

	// The listing shape rather than a single item's, because that is what
	// selection reads and where the direction was got wrong.
	listed, err := client.Ready(ctx)
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	byID := make(map[string]WorkItem, len(listed))
	for _, item := range listed {
		byID[item.ID] = item
	}
	decomposed, hasChild := byID[child.ID]
	whole, hasEpic := byID[epic.ID]
	if !hasChild || !hasEpic {
		t.Fatalf("Ready() = %#v, want both %s and %s", listed, epic.ID, child.ID)
	}

	if named := parentEdgesOf(decomposed); len(named) != 1 || named[0] != epic.ID {
		t.Fatalf("the child's own parent-child edges name %v, want just the epic %s; bd must attribute the edge to the "+
			"child and point it at the parent", named, epic.ID)
	}
	if named := parentEdgesOf(whole); len(named) != 0 {
		t.Fatalf("the epic's own parent-child edges name %v, want none; an edge stated in the other direction reads as an "+
			"epic having been broken out of its own child", named)
	}
	if got := decomposed.DecomposedFrom(); got != epic.ID {
		t.Fatalf("the child's DecomposedFrom() = %q, want the epic %s", got, epic.ID)
	}
	if got := whole.DecomposedFrom(); got != "" {
		t.Fatalf("the epic's DecomposedFrom() = %q, want nothing: it was broken out of nothing", got)
	}
}

// TestParentFieldConformance pins the other half of how bd states parentage: the
// field beside the item, on every read path selection consumes.
//
// No reading in the harness rests on the field alone any more: orchestrator's
// in-flight sequencing keyed on beads.WorkItem.Parent until yoyodyne-ifd.261 and
// now reads DecomposedFrom, as the queue-side coverage check already did. What
// the field still decides is which answer those readers get — DecomposedFrom
// prefers it over the edge, because that is the tracker answering the question
// directly — so a bd that stopped populating it, or that populated it with
// something the edges disagree with, changes what the guard and the coverage
// check see. That is worth pinning here for the reason the edge is: every other
// check of either drives a scripted runner that replays whatever it was handed.
//
// The field's history is settled rather than assumed: bd 1.1.2 is the version
// this project has had installed since 2026-07-26, twenty-five days before the
// yoyodyne-ifd.121 double-run, and it populates the field. So the field was not
// gained after that incident and no version floor is owed to it. What this check
// is for is the other direction — a bd that drops the field later — which is why
// it asserts rather than trusts. The floor it pins the behaviour at is the bd
// this ran against, and `bd version` names it.
//
// The export is the one shape that states only the edge, carrying no parent
// field on any item; nothing here reads the export, and beads.WorkItem's own
// DecomposedFrom is what covers it.
func TestParentFieldConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	epic, err := client.Create(ctx, NewWorkItem{
		Title:       "Split the README",
		Description: "The work it was broken into is below it.",
		Type:        "epic",
	})
	if err != nil {
		t.Fatalf("Create() an epic error = %v", err)
	}
	child, err := client.Create(ctx, NewWorkItem{
		Title:       "Execute the README split",
		Description: "One piece of it.",
		Type:        "task",
		Parent:      epic.ID,
	})
	if err != nil {
		t.Fatalf("Create() a child error = %v; bd must accept --parent", err)
	}

	// Every read path a pull makes, because the guard that rests on the field
	// reads the queue and the claimed slice alike, and a field carried on one
	// listing and not another is the same inertness in a narrower place.
	listed, err := client.List(ctx, "open")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	ready, err := client.Ready(ctx)
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	shown, err := client.Show(ctx, child.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	shownEpic, err := client.Show(ctx, epic.ID)
	if err != nil {
		t.Fatalf("Show() the epic error = %v", err)
	}

	for _, path := range []struct {
		name  string
		items []WorkItem
	}{
		{name: "List(open)", items: listed},
		{name: "Ready()", items: ready},
		{name: "Show()", items: []WorkItem{shown, shownEpic}},
	} {
		byID := make(map[string]WorkItem, len(path.items))
		for _, item := range path.items {
			byID[item.ID] = item
		}
		decomposed, hasChild := byID[child.ID]
		whole, hasEpic := byID[epic.ID]
		if !hasChild || !hasEpic {
			t.Fatalf("%s did not answer both %s and %s", path.name, epic.ID, child.ID)
		}
		if decomposed.Parent != epic.ID {
			t.Fatalf("%s gave the child's parent field as %q, want the epic %s; the field is what DecomposedFrom "+
				"answers with where it is there, so a bd that stops populating it moves every reader onto the edge",
				path.name, decomposed.Parent, epic.ID)
		}
		if whole.Parent != "" {
			t.Fatalf("%s gave the epic's parent field as %q, want nothing: it was broken out of nothing", path.name, whole.Parent)
		}
	}
}

// TestListedNotesConformance pins what the recovery from a timed-out creation
// rests on and which only a fake has ever answered: that an item bd lists carries
// its title, its parent, and its notes exactly as they were created.
//
// A `bd create` killed at the timeout after the store took it is not asked for
// again blindly, because a second creation is a second item — that is how
// yoyodyne-ifd.428.21 and 428.22 came from one admission. The conversation's
// tracker (chat's landedCreation) instead lists everything and takes the item
// whose title, parent, and notes are the creation's own for the one that landed.
// Every check of that match drives an in-memory tracker that hands back whatever
// it was given, so all of them pass identically against a bd whose listing
// folds, trims, or cuts a line of the notes — and against that bd the match
// never fires, and the duplicates it exists to stop come back with nothing
// failing.
//
// The notes are the shape an admission actually writes: several lines, blank
// lines between them, and a Goal served line, which is the part a listing that
// cut or reflowed long text would lose first. They are compared byte for byte
// rather than as the match compares them, so a difference the match would
// tolerate today is still seen here before a later reading comes to depend on
// it. The parent is set because the match requires it and a child is what a
// decomposition admits.
func TestListedNotesConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	epic, err := client.Create(ctx, NewWorkItem{
		Title:       "Duplicate admissions from one timed-out write",
		Description: "The work it was broken into is below it.",
		Type:        "epic",
	})
	if err != nil {
		t.Fatalf("Create() an epic error = %v", err)
	}

	const title = "A second admission citing a report already admitted from is refused"
	notes := "Admitted to the backlog by the product manager in conversation chat-91253e0e070c17b0663651cc48602122, after turn 762.\n" +
		"\n" +
		"Reason: The reviewer's report from the 433.2 run: the duplicate guard's only evidence is a fake, and a real " +
		"listing that does not round-trip notes would leave the guard silently inert. It is long enough that a listing " +
		"which wrapped or cut long lines would show it here rather than in the tracker.\n" +
		"\n" +
		goal.Note("Run development nearly autonomously. The human's routine interface is the product manager: they state "+
			"intent, approve the brief and goals, and answer questions the product manager escalates.") + "\n" +
		"\n" +
		"Admitted from report report-0a3f2715a01305b58f18038e0e2737e2, filed at \"warning\" by the reviewer."
	created, err := client.Create(ctx, NewWorkItem{
		Title:       title,
		Description: "One piece of it.",
		Type:        "task",
		Parent:      epic.ID,
		Notes:       notes,
	})
	if err != nil {
		t.Fatalf("Create() with multi-line notes error = %v", err)
	}

	// The unfiltered listing, because that is the one the recovery reads: it does
	// not know what status the landed item was left in.
	listed, err := client.List(ctx, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	item, found := itemIn(listed, created.ID)
	if !found {
		t.Fatalf("List() = %#v, want the created item %s among them; the recovery cannot find an item the listing omits",
			listed, created.ID)
	}
	if item.Notes != notes {
		t.Fatalf("List() gave the notes as\n%q\nwant them byte for byte as created\n%q\nthe recovery from a timed-out "+
			"creation matches on them, so a listing that alters them makes every retry a duplicate", item.Notes, notes)
	}
	if item.Title != title {
		t.Fatalf("List() gave the title as %q, want %q as created; the recovery matches on it", item.Title, title)
	}
	if got := item.DecomposedFrom(); got != epic.ID {
		t.Fatalf("List() gave the item as decomposed from %q, want the epic %s; the recovery matches on it", got, epic.ID)
	}
	if named, records := goal.NamedIn(item.Notes); !records || named == "" {
		t.Fatalf("List() notes name no goal (recorded = %v), want the Goal served line carried through", records)
	}
}

// TestUnfilteredListingConformance pins what the readers of closed work rest on
// and which only a scripted listing had ever answered: that a listing asked for
// no status carries closed work, and that a listing of closed work is all of it.
//
// The first is what the docket window decides dead entries from, and what the
// duplicate guard judges an admission against: both list with no status because
// closed work is what they are looking for. bd's own listing given no status
// leaves closed work out, so until everyStatus was passed both read every item
// as unfinished — every docket entry live, no admission a duplicate of landed
// work — and nothing failed. The item is closed the way the harness closes one,
// so the status under test is one this project actually writes.
//
// The second is what the reconcile sweep's closure pass rests on: it lists
// closed work and sweeps the entries on it, and an entry on an item that fell
// past a page is never swept. So there are more closed items here than bd's
// default page of fifty. They are imported closed rather than closed one at a
// time, because closing sixty items through bd takes minutes and what is under
// test is the listing rather than the close.
func TestUnfilteredListingConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	finished, err := client.Create(ctx, NewWorkItem{
		Title:       "The run that stopped here landed after all",
		Description: "Its docket entry is dead.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := client.Complete(ctx, finished.ID, "landed"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	unfinished, err := client.Create(ctx, NewWorkItem{
		Title:       "The run that stopped here is still somebody's",
		Description: "Its docket entry is live.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	listed, err := client.List(ctx, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	closed, found := itemIn(listed, finished.ID)
	if !found {
		t.Fatalf("List() with no status = %#v, want the closed item %s among them; the docket window and the duplicate "+
			"guard read closed work off this listing, and without it every entry reads as live", listed, finished.ID)
	}
	if closed.Status != closedDependencyStatus {
		t.Fatalf("List() with no status gives %s as %q, want closed", finished.ID, closed.Status)
	}
	// The control, without which the listing above could be a listing of closed
	// work only and pass.
	if open, found := itemIn(listed, unfinished.ID); !found || open.Status != statusOpen {
		t.Fatalf("List() with no status gives %s as %#v (found = %v), want it listed open", unfinished.ID, open, found)
	}

	const pastThePage = 60
	var rows strings.Builder
	for i := range pastThePage {
		fmt.Fprintf(&rows, `{"title":"Closed work %d","description":"d","issue_type":"task","priority":2,"status":"closed","closed_at":"2026-09-01T00:00:00Z"}`+"\n", i)
	}
	imported := filepath.Join(t.TempDir(), "closed.jsonl")
	if err := os.WriteFile(imported, []byte(rows.String()), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runCommand(t, project, "bd", "import", imported)

	for _, status := range []string{"", closedDependencyStatus} {
		listed, err := client.List(ctx, status)
		if err != nil {
			t.Fatalf("List(%q) error = %v", status, err)
		}
		var closedCount int
		for _, item := range listed {
			if item.Status == closedDependencyStatus {
				closedCount++
			}
		}
		if want := pastThePage + 1; closedCount != want {
			t.Fatalf("List(%q) carries %d closed items, want all %d; a listing cut at a page leaves the entries on "+
				"everything past it unswept and live", status, closedCount, want)
		}
	}
}

// blocksEdge is the bd relation that makes one item wait for another. It is
// spelled again here rather than taken from the client, because a check of what
// bd answers that named the relation the way the reading under test names it
// would agree with that reading by construction whatever bd said.
const blocksEdge = "blocks"

// TestBlockerStatusConformance pins what bd says about a blocker that is not
// finished, on each shape a start decision is made from. Every other check of
// those decisions can only restate the assumption: they drive a scripted runner
// that replays the status it was written with, so all of them pass identically
// whichever way bd answers.
//
// Two readings rest on it. The pipeline's start gate reads the item bd shows for
// it and refuses any blocking dependency that is not closed
// (orchestrator.blockingDependencies); the claim reads the same shape through
// WorkItem.WaitingOn to decide whether a status of blocked is stale. The case
// both have to survive is a blocker that is in flight rather than open: an item
// started beside the run already working what it waits for is two runs over one
// dependency, and nothing downstream looks again.
//
// bd does not answer the two shapes alike, and the difference is the whole
// reason WaitingOn asks in both directions. `bd show` embeds the depended-on
// item and carries its real status, in flight included. `bd list` states the
// edge alone and carries no status at all, which is why WaitingOn falls back to
// whether the depended-on item is still in the backlog — and an in-flight
// blocker has left it, so that fallback cannot see one. The show path is
// therefore asserted exactly: it is the only reading that refuses a start beside
// a blocker somebody is working, and a bd that stopped carrying the status there
// would leave nothing that does.
//
// What the listing carries is asserted only as not-closed, which is the whole of
// what the backlog's reading needs from it. A listing that named an unfinished
// blocker closed would release the item outright; one that carries no status is
// the case the fallback already covers, and one that started carrying the real
// status would only make the fallback redundant.
func TestBlockerStatusConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	blocker, err := client.Create(ctx, NewWorkItem{
		Title:       "Release the stale statuses",
		Description: "The work the item below waits for.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() a blocker error = %v", err)
	}
	dependent, err := client.Create(ctx, NewWorkItem{
		Title:       "Pull from the released queue",
		Description: "It waits on the release above.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() the item that waits error = %v", err)
	}
	if err := client.AddBlocker(ctx, dependent.ID, blocker.ID); err != nil {
		t.Fatalf("AddBlocker() error = %v", err)
	}

	// An open blocker first: it is the case both readings already agree on, so a
	// status wrong here would say nothing about the case where they part.
	shown, err := client.Show(ctx, dependent.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if got := blockingStatusOf(t, shown, blocker.ID); got != statusOpen {
		t.Fatalf("Show() gives the open blocker's status as %q, want open; the run-start gate refuses on this status and "+
			"nothing else looks at the blocker itself", got)
	}

	// The case the readings part on, and the one the stale-status release made
	// reachable: the blocker is being worked right now.
	claimed, _, err := client.Claim(ctx, blocker.ID)
	if err != nil {
		t.Fatalf("Claim() the blocker error = %v", err)
	}
	if claimed.Status != "in_progress" {
		t.Fatalf("Claim() the blocker gave status %q, want in_progress; this check says nothing about an in-flight blocker "+
			"unless the blocker is actually in flight", claimed.Status)
	}

	shown, err = client.Show(ctx, dependent.ID)
	if err != nil {
		t.Fatalf("Show() with the blocker in flight error = %v", err)
	}
	if got := blockingStatusOf(t, shown, blocker.ID); got != "in_progress" {
		t.Fatalf("Show() gives the in-flight blocker's status as %q, want in_progress; a bd that stops carrying it leaves "+
			"the run-start gate reading no unfinished blocker and starting a run beside the one already working it", got)
	}

	// The reading the claim actually makes, over the shape it makes it on. The
	// admitted work is what the backlog is assembled from, and an in-flight
	// blocker is in neither half of it, so the status bd carries on the edge is
	// the only thing here that names the wait.
	unfinished, err := client.unfinished(ctx)
	if err != nil {
		t.Fatalf("read the admitted work error = %v", err)
	}
	if _, queued := unfinished[blocker.ID]; queued {
		t.Fatalf("the in-flight blocker %s is still in the admitted work, so what follows would pass on the fallback "+
			"rather than on the status bd carries", blocker.ID)
	}
	if waiting := shown.WaitingOn(unfinished); len(waiting) != 1 || waiting[0] != blocker.ID {
		t.Fatalf("the item's own reading names %v as what it waits for, want just the in-flight blocker %s; this is what "+
			"decides whether a blocked status is stale, and an item released past it is a second run over one dependency",
			waiting, blocker.ID)
	}

	listed, err := client.List(ctx, statusOpen)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	waiting, found := itemIn(listed, dependent.ID)
	if !found {
		t.Fatalf("List(open) = %#v, want the waiting item %s among them", listed, dependent.ID)
	}
	if got := blockingStatusOf(t, waiting, blocker.ID); got == closedDependencyStatus {
		t.Fatalf("List(open) gives the in-flight blocker's status as closed; every reading believes a status the listing " +
			"carries, so this releases an item whose blocker somebody is working")
	}

	// The tracker's own readiness answer, which is what the backlog takes for open
	// work rather than deciding itself: it must not offer an item whose blocker is
	// still being worked.
	ready, err := client.Ready(ctx)
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if _, offered := itemIn(ready, dependent.ID); offered {
		t.Fatalf("Ready() offers %s while its blocker %s is in flight, want it held back; the backlog takes this answer "+
			"for open work rather than deciding readiness itself", dependent.ID, blocker.ID)
	}

	// And the control, without which the assertion above would pass against a bd
	// whose ready list simply never named the item.
	if _, err := client.Complete(ctx, blocker.ID, "landed"); err != nil {
		t.Fatalf("Complete() the blocker error = %v", err)
	}
	ready, err = client.Ready(ctx)
	if err != nil {
		t.Fatalf("Ready() after the blocker closed error = %v", err)
	}
	if _, offered := itemIn(ready, dependent.ID); !offered {
		t.Fatalf("Ready() = %#v, want %s offered once its only blocker closed", ready, dependent.ID)
	}
}

// TestBlockedClaimConformance pins what bd does with a claim on an item whose
// status is blocked, which is the shape every item the stale-status release put
// back within reach arrives in: the run-start gate admits blocked work and
// refuses it on its dependencies instead (orchestrator's startableStatuses), so
// what the tracker does with the claim that follows is the whole of whether such
// a release starts anything.
//
// bd refuses it, and refuses it on the status alone — the item here waits on
// nothing at all. That refusal is why Claim does not simply pass the claim
// through: it re-reads the item, and where nothing unfinished blocks it, it
// corrects the status and takes the item. Between the release and that recovery
// landing, every released item was selectable and unclaimable at once, and
// yoyodyne-ifd.285 was dispatched twenty-nine times in twenty hours.
//
// Three things are asserted, and each of them is load-bearing on its own. bd
// refuses. The refusal says what staleBlockedRefusal matches, because that
// pattern is the only thing the recovery is entered on and a bd that reworded
// the message would silently stop recovering — every released item unclaimable
// again, with no line of this repository having changed. And the recovery
// actually lands against bd rather than against a scripted answer: the item
// reads in_progress afterwards, on a separate read.
func TestBlockedClaimConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	created, err := client.Create(ctx, NewWorkItem{
		Title:       "Finish what a stopped run left",
		Description: "A run stopped on it, and nothing it waits for is unfinished.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Blocked the way the harness itself blocks work, so the status under test is
	// one this project actually writes rather than one this check invented.
	blocked, err := client.Block(ctx, created.ID, "The run that stopped here recorded why.")
	if err != nil {
		t.Fatalf("Block() error = %v", err)
	}
	if len(blocked.Dependencies) != 0 {
		t.Fatalf("the blocked item carries dependencies %#v, want none; the refusal below has to be about the status",
			blocked.Dependencies)
	}

	// The bare claim, which is what Claim makes first and what every caller made
	// before the recovery existed.
	if item, err := client.claim(ctx, created.ID); err == nil {
		t.Fatalf("bd claimed the blocked item %#v; it refused before, so the recovery below now runs over a claim that "+
			"already succeeded and this check no longer says what the claim path meets", item)
	} else if !staleBlockedRefusal.MatchString(err.Error()) {
		t.Fatalf("bd refused the blocked claim with %v, which staleBlockedRefusal does not match; the recovery is entered "+
			"on that pattern and nothing else, so a reworded refusal leaves every released item unclaimable", err)
	}

	// And the recovery, against bd rather than against a replayed answer: nothing
	// unfinished blocks this item, so the status is stale and the claim takes it.
	claimed, _, err := client.Claim(ctx, created.ID)
	if err != nil {
		t.Fatalf("Claim() a blocked item waiting on nothing error = %v; the released items all arrive in this shape, so a "+
			"claim that cannot take one releases nothing", err)
	}
	if claimed.Status != "in_progress" {
		t.Fatalf("Claim() status = %q, want in_progress; the pipeline refuses a claimed item that reads anything else", claimed.Status)
	}
	shown, err := client.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() after the claim error = %v", err)
	}
	if shown.Status != "in_progress" {
		t.Fatalf("Show() status = %q after the claim, want in_progress; a correction held in the process rather than "+
			"written to the tracker leaves the item reading as blocked to everything that opens it", shown.Status)
	}

	// The other half of the same gate, and the reason correcting the status is not
	// a way around it: an item that really does wait on unfinished work stays
	// refused, and stays blocked.
	waiting, err := client.Create(ctx, NewWorkItem{
		Title:       "Start beside the run working the blocker",
		Description: "It waits on work somebody is doing.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() the waiting item error = %v", err)
	}
	if err := client.AddBlocker(ctx, waiting.ID, created.ID); err != nil {
		t.Fatalf("AddBlocker() error = %v", err)
	}
	if _, err := client.Block(ctx, waiting.ID, "A run stopped here too."); err != nil {
		t.Fatalf("Block() the waiting item error = %v", err)
	}
	// The blocker above is the item this test just claimed, so it is in flight
	// rather than open: it has left the backlog, and the status bd carries on the
	// edge is the only thing that says it is unfinished.
	if _, _, err := client.Claim(ctx, waiting.ID); err == nil {
		t.Fatalf("Claim() took %s while %s was in flight, want the refusal to stand; a correction that cannot tell a stale "+
			"status from a live dependency starts a second run over one piece of work", waiting.ID, created.ID)
	} else if !strings.Contains(err.Error(), created.ID) {
		t.Fatalf("Claim() error = %v, want it to name the unfinished work %s", err, created.ID)
	}
	stillBlocked, err := client.Show(ctx, waiting.ID)
	if err != nil {
		t.Fatalf("Show() after the refused claim error = %v", err)
	}
	if stillBlocked.Status != statusBlocked {
		t.Fatalf("Show() status = %q after a refused claim, want blocked; a refusal that moved the item leaves work in a "+
			"status no run and no audit accounts for", stillBlocked.Status)
	}
}

// closedDependencyStatus is finished work, as a dependency reports it. Every
// reading of a blocker turns on it: work a listing calls closed is work no
// dependency on it holds anything back for.
const closedDependencyStatus = "closed"

// blockingStatusOf is what one reading of an item says about the blocker it
// waits for: the status carried on its own blocking dependency, and empty where
// the shape carries none. A reading with no such edge at all fails, because it
// says nothing about the status on an edge it lost.
func blockingStatusOf(t *testing.T, item WorkItem, blockerID string) string {
	t.Helper()

	for _, dependency := range item.Dependencies {
		if !strings.EqualFold(dependency.Type, blocksEdge) || dependency.ID != blockerID {
			continue
		}
		return dependency.Status
	}
	t.Fatalf("the reading of %s carries no blocking dependency on %s: %#v", item.ID, blockerID, item.Dependencies)
	return ""
}

// itemIn finds one item in a listing bd answered.
func itemIn(items []WorkItem, id string) (WorkItem, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return WorkItem{}, false
}

// The harness takes an explicit snapshot for readers and disables automatic
// exports on its ordinary bd invocations. This checks both halves against bd
// itself, with auto-export enabled in the project file and its interval set to
// one nanosecond: a write would rewrite the snapshot without the override.
func TestTrackerExportConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	// bd's own spelling of the knob, and the one the project this harness runs on
	// carries in its .beads/config.yaml.
	runCommand(t, project, "bd", "config", "set", "export.auto", "true")
	runCommand(t, project, "bd", "config", "set", "export.interval", "1ns")

	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	created, err := client.Create(ctx, NewWorkItem{
		Title:       "Read the work around this run's own",
		Description: "A run sweeps the export for what cites what.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := client.RefreshExport(ctx); err != nil {
		t.Fatalf("RefreshExport() error = %v", err)
	}
	path := filepath.Join(project, ExportPath)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Claimed rather than only created, because the item a run most needs to find
	// in the dump is its own, and the claim is the last write before it looks.
	claimed, _, err := client.Claim(ctx, created.ID)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Status != "in_progress" {
		t.Fatalf("Claim() status = %q, want in_progress; the item this check looks for was never claimed", claimed.Status)
	}
	if _, err := client.RecordOutcome(ctx, claimed.ID, "The write leaves the snapshot alone."); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || !os.SameFile(beforeInfo, afterInfo) || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("ordinary harness writes rewrote the full tracker export")
	}
	if err := client.RefreshExport(ctx); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the tracker's dump at %s error = %v; a project that asked bd for the JSONL export must get "+
			"one written there, or every run reads the copy the last release cut committed", ExportPath, err)
	}
	named, err := exportedIDs(content)
	if err != nil {
		t.Fatalf("the dump at %s is not the JSONL of work items a run reads it as: %v", ExportPath, err)
	}
	if !slices.Contains(named, claimed.ID) {
		t.Fatalf("the dump at %s names %v, want the just-claimed item %s among them; a dump that lags the store is a "+
			"run reading its own work item as absent", ExportPath, named, claimed.ID)
	}
	if !strings.Contains(string(content), "The write leaves the snapshot alone.") {
		t.Fatal("the requested fresh snapshot did not carry the latest write")
	}
}

// exportedIDs is what one JSONL dump names, and refuses a dump whose lines are
// not work items at all. A file that is there but says nothing a run can read is
// the same failure as a file that is not there.
func exportedIDs(content []byte) ([]string, error) {
	var named []string
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var exported struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &exported); err != nil {
			return nil, fmt.Errorf("decode %q: %w", line, err)
		}
		if exported.ID == "" {
			return nil, fmt.Errorf("line %q names no work item", line)
		}
		named = append(named, exported.ID)
	}
	if len(named) == 0 {
		return nil, errors.New("the dump names nothing")
	}
	return named, nil
}

// parentEdgesOf reports what an item's own parent-child edges name. An edge the
// tracker attributes to some other item is not this item's, which is the whole
// of the distinction the direction check turns on.
func parentEdgesOf(item WorkItem) []string {
	var named []string
	for _, dependency := range item.Dependencies {
		if !strings.EqualFold(dependency.Type, parentChildDependency) {
			continue
		}
		if dependency.IssueID != "" && dependency.IssueID != item.ID {
			continue
		}
		named = append(named, dependency.ID)
	}
	return named
}

// newTracker cuts one conformance check a scratch Beads store of its own and
// answers the directory bd runs in. Nothing here reaches the project's own
// tracker or a network: the store is thrown away with the test's temporary
// directory.
//
// It skips where bd is not installed. bd is a required dependency of the harness
// rather than an optional integration, so that is a statement about the machine
// running the tests and not about these checks being optional.
//
// A skip reads as `ok` in a suite run without -v, so a green suite alone does
// not say these ran. Where YOYODYNE_REQUIRE_BD is set a missing bd fails
// instead, which is how a machine that is meant to have run them says so.
func newTracker(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("bd"); err != nil {
		if os.Getenv("YOYODYNE_REQUIRE_BD") != "" {
			t.Fatalf("bd is not installed and YOYODYNE_REQUIRE_BD is set, so this check fails rather than skipping: %v", err)
		}
		t.Skipf("SKIPPED, not passed: bd is not installed: %v", err)
	}
	root := t.TempDir()
	project := filepath.Join(root, "tracker")
	runCommand(t, root, "git", "init", "-q", "-b", "main", project)
	// bd init commits what it writes, which needs an identity the machine may
	// not have configured.
	runCommand(t, project, "git", "config", "user.email", "yoyodyne@example.invalid")
	runCommand(t, project, "git", "config", "user.name", "Yoyodyne Test")
	runCommand(t, project, "bd", "init")
	return project
}

func runCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	command := exec.Command(name, args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v error = %v: %s", name, args, err, output)
	}
}

// The ready default is 100 even when stdout is a pipe. This checks that the
// no-cap request really returns more than that against the installed tracker,
// alongside the scheduler test that checks how all those rows are offered.
func TestReadyListingBeyondDefaultConformance(t *testing.T) {
	t.Parallel()
	project := newTracker(t)
	const total = 120
	var rows strings.Builder
	for index := range total {
		fmt.Fprintf(&rows, `{"title":"Ready work %d","description":"d","issue_type":"task","priority":2,"status":"open"}`+"\n", index)
	}
	imported := filepath.Join(t.TempDir(), "ready.jsonl")
	if err := os.WriteFile(imported, []byte(rows.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	runCommand(t, project, "bd", "import", imported)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	items, err := client.Ready(context.Background())
	if err != nil {
		t.Fatalf("Ready(): %v", err)
	}
	seen := make(map[string]bool, total)
	for _, item := range items {
		if seen[item.ID] {
			t.Fatalf("Ready() returned %s twice", item.ID)
		}
		seen[item.ID] = true
	}
	if len(seen) != total {
		t.Fatalf("Ready() returned %d items, want all %d past the default cap", len(seen), total)
	}
}
