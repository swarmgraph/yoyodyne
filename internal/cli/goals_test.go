package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// ansiEscapes is what a theme adds and nothing else, so a test can check that a
// listing with the dressing stripped out is the listing that was written.
var ansiEscapes = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

// listingRunner answers each bd listing with the slice it asked for, so a test
// can exercise a read path that consults more than one of them. What it answers
// with is bd's own listing shape, metadata included, because the metadata is
// where the audit reads the witness that a goal was written.
type listingRunner struct {
	items map[string][]map[string]any
}

func (r *listingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	listed := []map[string]any{}
	for _, argument := range command.Args {
		if status, asked := strings.CutPrefix(argument, "--status="); asked {
			listed = r.items[status]
		}
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		return execution.ProcessResult{}, err
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
}

func TestTheGoalsWorkCanBeAttributedToAreListedWithWhereTheyAreStated(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+"\nIntent in, software out.\n")
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+`
# V1 goals

An introduction.

## Goals

- [traceable-chain] Maintain a traceable chain from the brief through to verification.
- Isolate implementation tasks in harness-managed worktrees.
`)

	stdout, stderr, code := runCLI(t, "goals", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	// The listing is laid out to be read: one goal to an entry, a blank line
	// between entries, where it is stated indented under it, and a closing line
	// saying where the chain goes above these goals. None of that is dressing —
	// this is a buffer rather than a terminal, and it is the whole of what the
	// listing says.
	want := `Maintain a traceable chain from the brief through to verification.
  identity: traceable-chain
  stated by: v1-goals (docs/product/goals/v1-goals.md)

Isolate implementation tasks in harness-managed worktrees.
  identity: none, so work naming it matches on its wording
  stated by: v1-goals (docs/product/goals/v1-goals.md)

upstream: these goals support the goals the product brief states, in docs/product/brief.md
`
	if stdout != want {
		t.Fatalf("list stdout = %q, want %q", stdout, want)
	}
	if strings.Contains(stdout, "\x1b") {
		t.Fatalf("a listing written to something that is not a terminal carried escapes: %q", stdout)
	}

	stdout, stderr, code = runCLI(t, "goals", "list", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("list --json code = %d, stderr = %q", code, stderr)
	}
	var listed struct {
		Goals []struct {
			Statement  string `json:"statement"`
			ArtifactID string `json:"artifact_id"`
			InForce    bool   `json:"in_force"`
		} `json:"goals"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(listed.Goals) != 2 || listed.Goals[0].ArtifactID != "v1-goals" || !listed.Goals[0].InForce {
		t.Fatalf("listed = %#v", listed.Goals)
	}
	// What is read by a program carries none of what was laid out for a person:
	// no escapes, and no closing line to be parsed as a goal.
	if strings.Contains(stdout, "\x1b") || strings.Contains(stdout, "upstream:") {
		t.Fatalf("--json carried the listing's presentation: %q", stdout)
	}
}

// The dressing the listing is read with on a terminal, and the promise it is
// held to: the statement weighted, the lines about it slanted, and every
// distinction still there once the escapes are gone. What separates two goals is
// the blank line, what says a line is about the goal above it is the indent, and
// what says a goal is no longer in force is said in words — so the listing on a
// terminal that cannot be dressed is the same listing.
func TestTheGoalsListingIsDressedWithoutTheDressingCarryingAnything(t *testing.T) {
	t.Parallel()

	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals: []goal.Goal{
			{
				Identity:   "traceable-chain",
				Statement:  "Maintain a traceable chain from the brief through to verification.",
				Supports:   "Every change traces to intent somebody approved",
				ArtifactID: "v1-goals",
				Path:       "docs/product/goals/v1-goals.md",
				InForce:    true,
			},
			{
				Statement:  "Ship the first version by hand.",
				ArtifactID: "v0-goals",
				Path:       "docs/product/goals/v0-goals.md",
			},
		},
		BriefPath: "docs/product/brief.md",
	}

	var plain strings.Builder
	printGoals(&plain, console.Theme{}, goals)

	var dressed strings.Builder
	printGoals(&dressed, console.NewTheme(
		func(name string) string { return map[string]string{"TERM": "xterm-256color"}[name] },
		func() int { return 80 },
	), goals)

	if !strings.Contains(dressed.String(), "\x1b") {
		t.Fatal("a terminal that permits dressing was written an undressed listing")
	}
	if stripped := ansiEscapes.ReplaceAllString(dressed.String(), ""); stripped != plain.String() {
		t.Fatalf("stripping the escapes changed the listing:\n%q\nwant\n%q", stripped, plain.String())
	}
	// The statement is the entry and the lines under it are about it, and the two
	// are dressed as what they are rather than alike.
	lines := strings.Split(dressed.String(), "\n")
	if !strings.HasPrefix(lines[0], "\x1b[1m") {
		t.Fatalf("the statement was not weighted: %q", lines[0])
	}
	for _, index := range []int{1, 2, 3} {
		if !strings.HasPrefix(lines[index], "\x1b[3m") {
			t.Fatalf("a line about the goal was not slanted: %q", lines[index])
		}
	}
	// The listing without any dressing at all is the whole of what it says: the
	// blank line between entries, the marker on a goal nobody may name now, and
	// the closing line naming the brief upstream.
	want := `Maintain a traceable chain from the brief through to verification.
  identity: traceable-chain
  stated by: v1-goals (docs/product/goals/v1-goals.md)
  supports: Every change traces to intent somebody approved

Ship the first version by hand. [no longer active]
  identity: none, so work naming it matches on its wording
  stated by: v0-goals (docs/product/goals/v0-goals.md)

upstream: these goals support the goals the product brief states, in docs/product/brief.md
`
	if plain.String() != want {
		t.Fatalf("undressed listing = %q, want %q", plain.String(), want)
	}
}

// A repository that records no brief is still told what these goals are for. It
// is not sent to a file that is not there — what is wrong is reported with the
// other broken links upstream, and inventing a path would send a reader looking
// for a document nobody wrote.
func TestTheClosingLineNamesNoBriefWhereTheRepositoryRecordsNone(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	printGoals(&out, console.Theme{}, goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: "Run development nearly autonomously.", ArtifactID: "v1-goals", Path: "docs/product/goals/v1-goals.md", InForce: true}},
	})
	if !strings.HasSuffix(out.String(), "\nupstream: these goals support the goals the product brief states\n") {
		t.Fatalf("listing = %q", out.String())
	}
}

func TestAGoalsDocumentStatingNoGoalsIsNamedRatherThanReadAsFewerGoals(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+"\n# V1 goals\n\nThe goals are still to be written.\n")

	stdout, stderr, code := runCLI(t, "goals", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	// Nothing can be attributed to it, and the operator is told which document to
	// open rather than being shown a listing that is simply short.
	if !strings.Contains(stderr, "goals not read: docs/product/goals/v1-goals.md") {
		t.Fatalf("list stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "no goal is active") {
		t.Fatalf("list stdout = %q", stdout)
	}
}

// The audit's own read path, end to end from the bd command line: a
// decomposition child carrying the note a creation wrote is found by the lookup
// the command actually performs and reported as serving its goal.
//
// It exists because the symptom that started this was phrased in this command's
// words — six children of yoyodyne-ifd.102 reading "it records no goal" — and a
// test that resolved the goal by calling the parse directly would leave open
// whether the loss was in how the audit locates an item and pulls its notes. So
// this runs the audit's own scoped read, which is the whole of that lookup, against a bd
// that answers with the item's notes, and then reportAttribution's own judging
// and rendering over what came back.
func TestTheAuditFindsADecompositionChildsGoalThroughItsOwnLookup(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	// The note a decomposition writes, in the shape internal/chat builds it:
	// provenance, the reason, and the goal on its own line at the end.
	child := "Created under yoyodyne-ifd.102, decomposing it by the development manager " +
		"in conversation chat-419cedb4, after turn 3.\n\nReason: nothing routes stopped work today.\n\n" +
		goal.Note(autonomy)
	bd := &listingRunner{items: map[string][]map[string]any{
		"open": {{
			"id": "yoyodyne-ifd.102.2", "title": "Triage docket", "status": "open",
			"priority": 1, "issue_type": "task", "notes": child,
		}},
		"blocked": {{
			// A blocked sibling too: the audit reads two slices of the tracker, and
			// an item found in only one of them would be reported on by only one.
			"id": "yoyodyne-ifd.102.7", "title": "Re-arm a dropped queued merge", "status": "blocked",
			"priority": 3, "issue_type": "task", "notes": child,
		}},
	}}

	admitted, err := auditScopes[scopeAll].workItems(context.Background(), beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"})
	if err != nil {
		t.Fatalf("workItems() error = %v", err)
	}
	if len(admitted) != 2 {
		t.Fatalf("the audit's lookup found %d item(s): %#v", len(admitted), admitted)
	}

	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: autonomy, ArtifactID: "v1-goals", InForce: true}},
	}
	attributions := attributionsOf(admitted, goals)
	if code := attributionExitCode(attributions); code != 0 {
		t.Fatalf("the audit failed a decomposition child: %#v", attributions)
	}

	var rendered bytes.Buffer
	printAttributions(&rendered, auditScopes[scopeAll], attributions, goals)
	report := rendered.String()
	// The words the symptom was reported in. If a decomposition child ever reads
	// as naming no goal again, it fails here in the same language the operator saw.
	if strings.Contains(report, "it records no goal") {
		t.Fatalf("a decomposition child reads as naming no goal:\n%s", report)
	}
	if !strings.Contains(report, "2 work item(s): 2 serve a recorded goal, 0 name none") {
		t.Fatalf("report = %q", report)
	}
}

// An item whose notes were replaced rather than appended to: the goal it was
// created under is gone from them, and the witness the tracker carries beside
// them is not. That is what actually happened to the six children of
// yoyodyne-ifd.102, and what made it cost a week is that the audit reported it
// as the one state it does not fail on.
func TestTheAuditFailsAnItemWhoseRecordedGoalWasWrittenOver(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	bd := &listingRunner{items: map[string][]map[string]any{
		"open": {{
			"id": "yoyodyne-ifd.102.2", "title": "Triage docket", "status": "open",
			"priority": 1, "issue_type": "task",
			// Everything a careless writer left behind, and nothing of what it
			// replaced — except in the metadata it could not reach, which is where
			// the goal that was written survives it.
			"notes":    "Constraints from the architect, recorded 2026-08-19.",
			"metadata": map[string]any{"yoyodyne_goal_recorded": autonomy},
		}},
		"blocked": {{
			// Beside it, an item that genuinely predates the check: no goal, and no
			// witness that one was ever written. It must stay grandfathered, or the
			// audit fails a backlog nobody has had the chance to attribute.
			"id": "yoyodyne-ifd.45", "title": "Admitted long ago", "status": "blocked",
			"priority": 2, "issue_type": "task", "notes": "Admitted by hand.",
		}},
	}}

	admitted, err := auditScopes[scopeAll].workItems(context.Background(), beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"})
	if err != nil {
		t.Fatalf("workItems() error = %v", err)
	}
	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: autonomy, ArtifactID: "v1-goals", InForce: true}},
	}
	attributions := attributionsOf(admitted, goals)

	states := map[string]goal.State{}
	for _, entry := range attributions {
		states[entry.WorkItemID] = entry.Attribution.State
	}
	if states["yoyodyne-ifd.102.2"] != goal.StateLost {
		t.Fatalf("the overwritten item reads as %q: %#v", states["yoyodyne-ifd.102.2"], attributions)
	}
	if states["yoyodyne-ifd.45"] != goal.StateUnattributed {
		t.Fatalf("the legacy item reads as %q: %#v", states["yoyodyne-ifd.45"], attributions)
	}
	// Loudly: the audit fails, rather than listing it among the items somebody
	// has yet to attribute.
	if code := attributionExitCode(attributions); code != 1 {
		t.Fatalf("exit code over a destroyed attribution = %d", code)
	}

	var rendered bytes.Buffer
	printAttributions(&rendered, auditScopes[scopeAll], attributions, goals)
	report := rendered.String()
	for _, want := range []string{
		"1 lost the goal they recorded",
		"having recorded a goal and lost it",
		"yoyodyne-ifd.102.2",
		"written over rather than never made",
		// The words to put back, quoted where the tracker kept them. A report that
		// only said an attribution was destroyed would leave whoever reads it to
		// re-derive a judgement somebody already made.
		autonomy,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report = %q, want it to contain %q", report, want)
		}
	}
}

// The sweep that closes the gap the witness leaves behind it: work attributed
// before any of this existed carries no witness, so replacing its notes would
// still read as work nobody ever attributed. It copies each item's own recorded
// goal to where a careless writer cannot reach it, and judges nothing.
func TestWitnessingRecordsTheGoalAnItemAlreadyStatesAndDecidesNothing(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	bd := &sweepRunner{listed: map[string][]map[string]any{
		"open": {
			// Attributed long before the witness existed: the goal is in the notes
			// and nothing outside them says so.
			{"id": "yoyodyne-ifd.102.2", "title": "Triage docket", "status": "open",
				"priority": 1, "issue_type": "task", "notes": "Admitted long ago.\n\n" + goal.Note(autonomy)},
			// Already witnessed: nothing to do, and writing again would be a second
			// write per item on every sweep.
			{"id": "yoyodyne-ifd.68", "title": "Slack reporting", "status": "open",
				"priority": 2, "issue_type": "task", "notes": goal.Note(autonomy),
				"metadata": map[string]any{"yoyodyne_goal_recorded": autonomy}},
			// Records no goal: there is nothing to witness, and a witness written
			// here would turn work nobody has attributed yet into work that reads as
			// having lost an attribution it never had. This is the one the sweep must
			// not touch.
			{"id": "yoyodyne-ifd.45", "title": "Admitted long ago", "status": "open",
				"priority": 3, "issue_type": "task", "notes": "Admitted by hand."},
		},
		// The two slices the backlog leaves out, and the two the recorded losses
		// actually landed in: the item somebody is working on right now, and the
		// closed items that were written over after they closed. A sweep scoped to
		// the queue protects neither.
		"in_progress": {
			{"id": "yoyodyne-ifd.99", "title": "Configurable roles", "status": "in_progress",
				"priority": 1, "issue_type": "task", "notes": goal.Note(autonomy)},
		},
		"closed": {
			{"id": "yoyodyne-ifd.4", "title": "Run one work item", "status": "closed",
				"priority": 1, "issue_type": "task", "notes": goal.Note(autonomy)},
		},
	}}

	tracker := beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"}
	swept, err := workItemsWithStatus(context.Background(), tracker, trackerStatuses)
	if err != nil {
		t.Fatalf("workItemsWithStatus() error = %v", err)
	}
	witnessed, failures := recordGoalWitnesses(context.Background(), tracker, swept)
	if failures != 0 {
		t.Fatalf("witnessed = %#v", witnessed)
	}
	if len(bd.written) != 3 {
		t.Fatalf("the sweep wrote %#v, want every unwitnessed attributed item whatever its status", bd.written)
	}
	for _, protected := range []string{"yoyodyne-ifd.102.2", "yoyodyne-ifd.99", "yoyodyne-ifd.4"} {
		if bd.written[protected] != autonomy {
			t.Fatalf("%s was not witnessed: the sweep wrote %#v", protected, bd.written)
		}
	}

	var rendered bytes.Buffer
	printWitnessed(&rendered, len(swept), witnessed)
	for _, want := range []string{"3 newly witnessed", "yoyodyne-ifd.102.2 witnessed: " + autonomy} {
		if !strings.Contains(rendered.String(), want) {
			t.Fatalf("witness stdout = %q, want it to contain %q", rendered.String(), want)
		}
	}

	// The item it just witnessed now reads as protected: replacing its notes
	// tomorrow is a loss the audit reports and fails, which is the whole point of
	// having swept it. Before the sweep the same replacement read as work nobody
	// had attributed.
	goals := goal.Set{Sources: []string{"v1-goals"}, Goals: []goal.Goal{{Statement: autonomy, ArtifactID: "v1-goals", InForce: true}}}
	overwritten := beads.WorkItem{ID: "yoyodyne-ifd.102.2", Notes: "Constraints from the architect.",
		GoalWitness: goal.Witness{Recorded: true, Statement: bd.written["yoyodyne-ifd.102.2"]}}
	if lost := goals.AttributionOf(overwritten.Notes, overwritten.GoalWitness); lost.State != goal.StateLost || lost.Recorded != autonomy {
		t.Fatalf("a swept item is not protected: %#v", lost)
	}

	// An item the tracker refused is named and does not stop the sweep, and the
	// sweep reports the failure: a sweep reported as done while an item stayed
	// uncovered is how a gap gets believed closed.
	refusing := &sweepRunner{
		listed:  map[string][]map[string]any{"open": {{"id": "yoyodyne-ifd.102.2", "title": "Triage docket", "status": "open", "priority": 1, "issue_type": "task", "notes": goal.Note(autonomy)}}},
		refuse:  true,
		refusal: "bd: the tracker is read-only",
	}
	refused := beads.Client{Runner: refusing, Binary: "bd-test", Dir: "/repo"}
	unwitnessed, err := workItemsWithStatus(context.Background(), refused, trackerStatuses)
	if err != nil {
		t.Fatalf("workItemsWithStatus() error = %v", err)
	}
	attempted, refusals := recordGoalWitnesses(context.Background(), refused, unwitnessed)
	if refusals != 1 || len(attempted) != 1 || attempted[0].Failure == "" {
		t.Fatalf("a refused write was not reported: %#v", attempted)
	}
	var refusedReport bytes.Buffer
	printWitnessed(&refusedReport, len(unwitnessed), attempted)
	if !strings.Contains(refusedReport.String(), "yoyodyne-ifd.102.2 could not be witnessed") {
		t.Fatalf("witness stdout = %q", refusedReport.String())
	}
}

// The migration off the wording: an attribution recorded before goals carried
// identities names the words, and the words are what the next amendment changes.
// What it must not do is decide anything about any work — the goal it records is
// the goal the item already named, and an item it cannot resolve is reported and
// left exactly as it was.
func TestReattributingMovesAnAttributionOntoItsGoalsIdentityAndGuessesAtNothing(t *testing.T) {
	t.Parallel()

	chain := "Maintain a traceable chain from the brief through to verification."
	worktrees := "Isolate implementation tasks in harness-managed worktrees."
	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals: []goal.Goal{
			{Identity: "traceable-chain", Statement: chain, ArtifactID: "v1-goals", InForce: true},
			// Beside it, a goal nobody has assigned an identity to yet. Work naming it
			// has nothing to be moved onto, which is a document to amend rather than
			// an item to correct.
			{Statement: worktrees, ArtifactID: "v1-goals", InForce: true},
		},
	}
	bd := &sweepRunner{listed: map[string][]map[string]any{
		"open": {
			// Attributed by its wording, to a goal that now carries an identity. This
			// is the one item the migration exists for.
			{"id": "yoyodyne-ifd.102.2", "title": "Triage docket", "status": "open",
				"priority": 1, "issue_type": "task", "notes": "Admitted long ago.\n\n" + goal.Note(chain)},
			// Already naming the identity: nothing to move, and a second note would be
			// a write per item on every run for no fact gained.
			{"id": "yoyodyne-ifd.68", "title": "Slack reporting", "status": "open",
				"priority": 2, "issue_type": "task", "notes": goal.Note("[traceable-chain] " + chain)},
			// Resolves, and the goal it resolves to carries no identity.
			{"id": "yoyodyne-ifd.99", "title": "Worktrees", "status": "open",
				"priority": 2, "issue_type": "task", "notes": goal.Note(worktrees)},
			// Names something no goals document states: the audit's finding, and not
			// a claim this may correct by moving it somewhere.
			{"id": "yoyodyne-ifd.45", "title": "Something else", "status": "open",
				"priority": 3, "issue_type": "task", "notes": goal.Note("A goal nobody wrote.")},
			// Names no goal at all: what work is for is the product manager's
			// judgement, and this writes none.
			{"id": "yoyodyne-ifd.7", "title": "Admitted by hand", "status": "open",
				"priority": 3, "issue_type": "task", "notes": "Admitted by hand."},
		},
	}}

	tracker := beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"}
	read, err := workItemsWithStatus(context.Background(), tracker, trackerStatuses)
	if err != nil {
		t.Fatalf("workItemsWithStatus() error = %v", err)
	}
	moved, unmatched, failures := recordGoalIdentities(context.Background(), tracker, goals, read, false)
	if failures != 0 {
		t.Fatalf("moved = %#v", moved)
	}
	if len(moved) != 1 || moved[0].WorkItemID != "yoyodyne-ifd.102.2" || moved[0].Identity != "traceable-chain" {
		t.Fatalf("moved = %#v", moved)
	}
	if len(bd.appended) != 1 || !strings.Contains(bd.appended["yoyodyne-ifd.102.2"], goal.Note("[traceable-chain] "+chain)) {
		t.Fatalf("the migration wrote %#v", bd.appended)
	}
	// The item now names the goal by the thing that does not move, and the witness
	// outside its notes was carried along with it.
	if bd.written["yoyodyne-ifd.102.2"] != "[traceable-chain] "+chain {
		t.Fatalf("the witness was not moved with the attribution: %#v", bd.written)
	}
	// Two items left exactly as they were, each said for what it is: a document to
	// amend, and a claim to correct.
	if len(unmatched) != 2 {
		t.Fatalf("unmatched = %#v", unmatched)
	}
	reasons := map[string]string{}
	for _, entry := range unmatched {
		reasons[entry.WorkItemID] = entry.Reason
	}
	if !strings.Contains(reasons["yoyodyne-ifd.99"], "carries no identity") {
		t.Fatalf("unmatched = %#v", unmatched)
	}
	if !strings.Contains(reasons["yoyodyne-ifd.45"], "no goal recorded in v1-goals") {
		t.Fatalf("unmatched = %#v", unmatched)
	}

	var rendered bytes.Buffer
	printReattributed(&rendered, len(read), moved, unmatched, false)
	for _, want := range []string{
		"5 work item(s) read: 1 re-attributed by identity, 2 could not be re-attributed",
		"yoyodyne-ifd.102.2 -> [traceable-chain] " + chain,
		"yoyodyne-ifd.99 left as it was",
	} {
		if !strings.Contains(rendered.String(), want) {
			t.Fatalf("reattribute stdout = %q, want it to contain %q", rendered.String(), want)
		}
	}

	// A migration over a live backlog is worth reading before it is run, so the
	// same report is available with nothing written.
	dry := &sweepRunner{listed: bd.listed}
	dryRead, err := workItemsWithStatus(context.Background(), beads.Client{Runner: dry, Binary: "bd-test", Dir: "/repo"}, trackerStatuses)
	if err != nil {
		t.Fatalf("workItemsWithStatus() error = %v", err)
	}
	planned, _, _ := recordGoalIdentities(context.Background(), beads.Client{Runner: dry, Binary: "bd-test", Dir: "/repo"}, goals, dryRead, true)
	if len(planned) != 1 || len(dry.appended) != 0 {
		t.Fatalf("--dry-run wrote %#v", dry.appended)
	}
}

// The other half of the sweep above, standing in front of the writer instead of
// behind it: the tool call an agent session is about to make, decided before the
// notes are replaced rather than reported after they were.
//
// This exercises the decision, which is what the harness owns. Whether Claude
// Code accepts the settings block that installs it, fires it for a Bash call, and
// finds `yoyo` on the run's PATH is an end-to-end fact about the provider that no
// unit test can reach; the path fails open, so a mistake there is silent and the
// witness above is what still holds the words to put back.
func TestTheGuardRefusesTheWriterAndSaysNothingAboutAnythingElse(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	for _, test := range []struct {
		name    string
		payload string
		denied  bool
	}{
		{
			name:    "the writer that destroys an attribution",
			payload: `{"tool_name":"Bash","tool_input":{"command":"bd update yoyodyne-ifd.45 --notes=\"replaced\""}}`,
			denied:  true,
		},
		{
			name:    "the same write carrying the attribution through",
			payload: `{"tool_name":"Bash","tool_input":{"command":"bd update yoyodyne-ifd.45 --notes=\"` + goal.Note(autonomy) + `\""}}`,
			denied:  true,
		},
		{
			name:    "a separate flag value carrying an invented attribution",
			payload: `{"tool_name":"Bash","tool_input":{"command":"bd update yoyodyne-ifd.45 --notes \"` + goal.Note("Something nobody ever attributed this to.") + `\""}}`,
			denied:  true,
		},
		{
			name:    "the spelling that adds rather than replaces",
			payload: `{"tool_name":"Bash","tool_input":{"command":"bd update yoyodyne-ifd.45 --append-notes=\"what I did\""}}`,
		},
		{
			// The other silent rewrite: the status moves and nothing on the item
			// says what moved it. This is the line that reopened two merged items
			// and released two escalations on 2026-09-18.
			name:    "a status set with no note saying what moved it",
			payload: `{"tool_name":"Bash","tool_input":{"command":"bd update yoyodyne-ifd.45 --status=open"}}`,
			denied:  true,
		},
		{
			name:    "the same move carrying its account",
			payload: `{"tool_name":"Bash","tool_input":{"command":"bd update yoyodyne-ifd.45 --status=open --append-notes=\"released for the repair the development manager handed back\""}}`,
		},
		{
			// Nothing but a shell command can carry the writer, and a guard with an
			// opinion about reading a file is a guard in the way of every run.
			name:    "a tool that is not a shell",
			payload: `{"tool_name":"Read","tool_input":{"command":"bd update yoyodyne-ifd.45 --notes=replaced"}}`,
		},
		{
			// A payload this cannot read allows the command and says so. Refusing it
			// would turn one unrecognised hook shape into a session that can run no
			// commands at all -- the guard being the outage instead of preventing one.
			name:    "a tool call it cannot read",
			payload: `not json at all`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if code := guardNotesReplacement(nil, strings.NewReader(test.payload), &stdout, &stderr); code != 0 {
				t.Fatalf("guard exit code = %d, stderr = %q", code, stderr.String())
			}
			if !test.denied {
				if stdout.String() != "" {
					t.Fatalf("guard said something about a command it has no opinion on: %q", stdout.String())
				}
				return
			}
			var decision hookDecision
			if err := json.Unmarshal(stdout.Bytes(), &decision); err != nil {
				t.Fatalf("guard stdout = %q: %v", stdout.String(), err)
			}
			if decision.Output.EventName != "PreToolUse" || decision.Output.Decision != "deny" {
				t.Fatalf("guard decision = %#v", decision.Output)
			}
			if !strings.Contains(decision.Output.Reason, "yoyodyne-ifd.45") ||
				!strings.Contains(decision.Output.Reason, "--append-notes") {
				t.Fatalf("guard reason = %q, want it to name the item and what to run instead", decision.Output.Reason)
			}
		})
	}
}

// The sweep must reach every status the audit reads, or an item could be
// reported as having lost a goal with no witness holding the words to put back.
// The two now walk the same list by default, which is the point rather than a
// coincidence: the sweep reached closed work first, the audit could not see it,
// and nine of the twelve recorded losses were in the gap.
//
// The narrower scope is pinned in the same place, because it is what the audit
// used to do and a reader asking for it is asking for less coverage on purpose.
func TestTheSweepReachesEveryStatusTheAuditCanRead(t *testing.T) {
	t.Parallel()

	swept := map[string]bool{}
	for _, status := range trackerStatuses {
		swept[status] = true
	}
	for name, scope := range auditScopes {
		for _, audited := range scope.read {
			if !swept[audited] {
				t.Fatalf("--scope=%s reads %q and the sweep does not reach it, so a loss there could be reported with no witness to put back", name, audited)
			}
		}
	}
	// The default is the wide one. An audit that has to be asked for its coverage
	// is an audit whose blind spot is the default, which is the defect this closed.
	if len(auditScopes[scopeAll].read) != len(trackerStatuses) {
		t.Fatalf("the audit's default reads %v, not every status the tracker holds %v", auditScopes[scopeAll].read, trackerStatuses)
	}
	if len(auditScopes[scopeQueue].read) >= len(trackerStatuses) {
		t.Fatalf("--scope=%s reads %v, which is not narrower than the tracker's %v", scopeQueue, auditScopes[scopeQueue].read, trackerStatuses)
	}
}

// What the audit says it covered, in both the report a person reads and the one a
// program does. It is here because a finding is worth what the ground it was
// looked for on is worth: the audit read the queue alone for as long as it took
// two diagnoses to be made wrong, and neither report said so.
func TestTheAuditStatesWhatItReadAndWhatItDidNot(t *testing.T) {
	t.Parallel()

	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: "Maintain a traceable chain.", ArtifactID: "v1-goals", InForce: true}},
	}
	attributions := []itemAttribution{
		{WorkItemID: "ifd.1", Title: "Attributed work", Status: "open", Attribution: goals.Attribute("Maintain a traceable chain.")},
	}

	var wide bytes.Buffer
	printAttributions(&wide, auditScopes[scopeAll], attributions, goals)
	if !strings.HasPrefix(wide.String(), "coverage: read open, in_progress, blocked, closed -- every status the tracker holds") {
		t.Fatalf("report = %q, want it to open with what it read", wide.String())
	}

	// The narrow scope says what it left out and how to read it, so nobody has to
	// know that "queue" means two of four statuses to know what was skipped.
	var narrow bytes.Buffer
	printAttributions(&narrow, auditScopes[scopeQueue], attributions, goals)
	for _, want := range []string{
		"coverage: read open, blocked",
		"in_progress and closed work was not checked",
		"--scope=all",
	} {
		if !strings.Contains(narrow.String(), want) {
			t.Fatalf("report = %q, want it to contain %q", narrow.String(), want)
		}
	}

	// A report with nothing in it is where the coverage matters most: an empty
	// audit over the whole tracker and an empty audit over nothing at all are
	// opposite answers, and the line above the tally is what tells them apart.
	var empty bytes.Buffer
	printAttributions(&empty, auditScopes[scopeQueue], nil, goals)
	if !strings.Contains(empty.String(), "coverage: read open, blocked") || !strings.Contains(empty.String(), "nothing to attribute") {
		t.Fatalf("empty report = %q", empty.String())
	}

	// The same thing a program reads, by the field names that are its contract.
	encoded, err := json.Marshal(attributionOutput(auditScopes[scopeQueue], attributions, goals))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded struct {
		Coverage struct {
			Scope  string   `json:"scope"`
			Read   []string `json:"read"`
			Unread []string `json:"unread"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %s", err, encoded)
	}
	if decoded.Coverage.Scope != scopeQueue ||
		strings.Join(decoded.Coverage.Read, ",") != "open,blocked" ||
		strings.Join(decoded.Coverage.Unread, ",") != "in_progress,closed" {
		t.Fatalf("coverage = %+v", decoded.Coverage)
	}
}

// The gap this closed, in the words of what was missed: an attribution destroyed
// on a closed item. The audit could not read it at all, so the loss was reported
// nowhere and the same destruction was diagnosed wrong twice. Now it is counted,
// named with the item, and failed for.
//
// Beside it, the finding on closed work that is deliberately not a failure: an
// item naming a goal the goals no longer state in those words. It named what the
// goals said when it was admitted, the work is finished, and there is nothing for
// anybody to correct -- so it is counted and named, and the audit does not go red
// over a goal somebody reworded afterwards.
func TestTheAuditFailsADestroyedAttributionOnClosedWorkAndNamesTheRestOfIt(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	stale := goal.Note("Run development autonomously.")
	bd := &listingRunner{items: map[string][]map[string]any{
		"closed": {
			{
				"id": "yoyodyne-ifd.121.3", "title": "Re-arm a dropped merge", "status": "closed",
				"priority": 1, "issue_type": "task",
				"notes":    "Constraints from the architect, recorded 2026-08-19.",
				"metadata": map[string]any{"yoyodyne_goal_recorded": autonomy},
			},
			{
				// Attributed under the goal's older wording, which was amended after
				// this closed. Reported, and not a failure.
				"id": "yoyodyne-ifd.68.10", "title": "Slack digest", "status": "closed",
				"priority": 2, "issue_type": "task", "notes": stale,
			},
		},
	}}

	tracker := beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"}
	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: autonomy, ArtifactID: "v1-goals", InForce: true}},
	}

	// The scope the audit used to have reads none of it, which is the defect
	// stated as a test: the loss is there and the report says nothing about it.
	queued, err := auditScopes[scopeQueue].workItems(context.Background(), tracker)
	if err != nil {
		t.Fatalf("workItems() error = %v", err)
	}
	if len(queued) != 0 {
		t.Fatalf("the queue scope read %#v", queued)
	}

	read, err := auditScopes[scopeAll].workItems(context.Background(), tracker)
	if err != nil {
		t.Fatalf("workItems() error = %v", err)
	}
	attributions := attributionsOf(read, goals)
	states := map[string]goal.State{}
	for _, entry := range attributions {
		states[entry.WorkItemID] = entry.Attribution.State
	}
	if states["yoyodyne-ifd.121.3"] != goal.StateLost {
		t.Fatalf("the destroyed attribution on closed work reads as %q: %#v", states["yoyodyne-ifd.121.3"], attributions)
	}
	if states["yoyodyne-ifd.68.10"] != goal.StateUnresolved {
		t.Fatalf("the stale attribution on closed work reads as %q: %#v", states["yoyodyne-ifd.68.10"], attributions)
	}
	if code := attributionExitCode(attributions); code != 1 {
		t.Fatalf("exit code over a destroyed attribution on closed work = %d", code)
	}
	// The stale one on its own does not fail, so a goal reworded after the work
	// closed does not turn the audit permanently red.
	finished := []itemAttribution{{WorkItemID: "yoyodyne-ifd.68.10", Status: "closed",
		Attribution: goals.AttributionOf(stale, goal.Witness{})}}
	if code := attributionExitCode(finished); code != 0 {
		t.Fatalf("exit code over a wrong attribution on finished work alone = %d", code)
	}
	// The same wrong claim on work still to be done does fail: there it is a claim
	// somebody can correct.
	live := []itemAttribution{{WorkItemID: "yoyodyne-ifd.68.10", Status: "open",
		Attribution: goals.AttributionOf(stale, goal.Witness{})}}
	if code := attributionExitCode(live); code != 1 {
		t.Fatalf("exit code over a wrong attribution on open work = %d", code)
	}

	var rendered bytes.Buffer
	printAttributions(&rendered, auditScopes[scopeAll], attributions, goals)
	report := rendered.String()
	for _, want := range []string{
		"2 work item(s): 0 serve a recorded goal",
		"1 lost the goal they recorded",
		"yoyodyne-ifd.121.3",
		"[p1, closed]",
		"1 of them on closed work, which fails like any other",
		"yoyodyne-ifd.68.10",
		"1 of them on closed work, which is counted here and does not fail",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report = %q, want it to contain %q", report, want)
		}
	}
}

// sweepRunner is a bd that answers listings and keeps what a witness wrote, so
// a sweep can be checked for what it did and did not touch.
type sweepRunner struct {
	listed map[string][]map[string]any
	// written is the witness each update carried, and appended is the text each
	// one added to the item's notes. Both are recorded because the two writes this
	// package makes are read differently: the sweep is judged by the witness it
	// stored, and the migration by the attribution it wrote onto the item.
	appended map[string]string
	written  map[string]string
	refuse   bool
	refusal  string
}

func (r *sweepRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	if len(command.Args) > 0 && command.Args[0] == "update" {
		if r.refuse {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: r.refusal}, nil
		}
		id := command.Args[1]
		for _, argument := range command.Args {
			if note, carried := strings.CutPrefix(argument, "--append-notes="); carried {
				if r.appended == nil {
					r.appended = map[string]string{}
				}
				r.appended[id] = note
			}
		}
		witnessed := ""
		for _, argument := range command.Args {
			statement, carried := strings.CutPrefix(argument, "--set-metadata=yoyodyne_goal_recorded=")
			if !carried {
				continue
			}
			witnessed = statement
			if r.written == nil {
				r.written = map[string]string{}
			}
			r.written[id] = statement
		}
		if witnessed != "" {
			// bd answers an update with the item as it holds it afterwards, and the
			// client reads that answer back before it reports the write as applied —
			// so the answer carries the note as well as the witness.
			item := map[string]any{"id": id, "title": "t", "status": "open", "priority": 1, "issue_type": "task",
				"notes": r.appended[id], "metadata": map[string]any{"yoyodyne_goal_recorded": witnessed}}
			encoded, err := json.Marshal([]map[string]any{item})
			if err != nil {
				return execution.ProcessResult{}, err
			}
			return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
		}
	}
	listed := []map[string]any{}
	for _, argument := range command.Args {
		if status, asked := strings.CutPrefix(argument, "--status="); asked {
			listed = r.listed[status]
		}
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		return execution.ProcessResult{}, err
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
}

func TestTheAuditFailsAWrongAttributionAndNotAMissingOne(t *testing.T) {
	t.Parallel()

	// The grandfathering decision, as the exit status states it: work admitted
	// before goals were checked names none and is somebody's to attribute, and a
	// rule that failed it would stop a backlog rather than close a gap. An item
	// naming a goal the goals do not state is a claim that is wrong.
	legacy := []itemAttribution{
		{WorkItemID: "ifd.1", Attribution: goal.Attribution{State: goal.StateUnattributed}},
		{WorkItemID: "ifd.2", Attribution: goal.Attribution{State: goal.StateAttributed}},
	}
	if code := attributionExitCode(legacy); code != 0 {
		t.Fatalf("exit code over a grandfathered backlog = %d", code)
	}
	wrong := append(legacy, itemAttribution{WorkItemID: "ifd.3", Attribution: goal.Attribution{State: goal.StateUnresolved}})
	if code := attributionExitCode(wrong); code != 1 {
		t.Fatalf("exit code over a wrong attribution = %d", code)
	}
}

func TestTheAuditSeparatesWorkWithNoGoalFromWorkWhoseGoalIsWrong(t *testing.T) {
	t.Parallel()

	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: "Maintain a traceable chain.", ArtifactID: "v1-goals", InForce: true}},
	}
	attributions := []itemAttribution{
		{WorkItemID: "ifd.1", Title: "Attributed work", Attribution: goals.Attribute("Maintain a traceable chain.")},
		{WorkItemID: "ifd.2", Title: "Legacy work", Attribution: goals.AttributionOf("Admitted long ago.", goal.Witness{})},
		{WorkItemID: "ifd.3", Title: "Misattributed work", Attribution: goals.Attribute("Ship the prototype.")},
	}

	var rendered bytes.Buffer
	printAttributions(&rendered, auditScopes[scopeAll], attributions, goals)
	report := rendered.String()
	if !strings.Contains(report, "3 work item(s): 1 serve a recorded goal, 1 name none, 1 name a goal the goals do not state, 0 lost the goal they recorded") {
		t.Fatalf("report = %q", report)
	}
	// Each item is under the heading that says what to do about it, so the two
	// ways of not being attributed never read as one pile of failures.
	for _, want := range []string{
		"naming a goal no goals document states",
		"naming no goal, which is what work admitted before goals were checked looks like",
		"serving a recorded goal",
		"ifd.3",
		"ifd.2",
		"ifd.1",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report = %q, want it to contain %q", report, want)
		}
	}
}

// The audit read by a program says what it judged and what was wrong with the
// goals it judged against, and it does not say what those goals link upward to.
// The pair belongs to `goals list`, which is the report the goals themselves are
// in: the upstream half on its own is a list with nothing here to resolve it
// against.
func TestTheAuditCarriesNoBriefGoalsForNothingHereToBeReadAgainst(t *testing.T) {
	t.Parallel()

	goals := goal.Set{
		Sources:    []string{"v1-goals"},
		Goals:      []goal.Goal{{Statement: "Maintain a traceable chain.", ArtifactID: "v1-goals", InForce: true}},
		BriefGoals: []goal.BriefGoal{{Name: "Intent in, software out", ArtifactID: "brief", Path: "docs/product/brief.md"}},
		Problems:   []goal.Problem{{Path: "docs/product/goals/v2-goals.md", Reason: "no goals under its Goals heading"}},
	}
	attributions := []itemAttribution{
		{WorkItemID: "ifd.1", Title: "Attributed work", Attribution: goals.Attribute("Maintain a traceable chain.")},
	}

	encoded, err := json.Marshal(attributionOutput(auditScopes[scopeAll], attributions, goals))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	// The field names are the operator-facing contract, so the test reads them by
	// name rather than through goalsOutput.
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %s", err, encoded)
	}
	if _, carried := decoded["brief_goals"]; carried {
		t.Fatalf("the audit carried the upstream half of the link: %s", encoded)
	}
	// What it does carry, so dropping the pair has not quietly dropped anything a
	// reader of this report was reading.
	for _, want := range []string{"attributions", "problems"} {
		if _, carried := decoded[want]; !carried {
			t.Fatalf("the audit carried no %q: %s", want, encoded)
		}
	}
}

func TestTheAuditReportsNothingCheckedRatherThanNothingFound(t *testing.T) {
	t.Parallel()

	// A repository whose goals could not be read must not have its queue reported
	// as unattributed: nothing was checked, and saying so is the whole of what is
	// honest.
	var rendered bytes.Buffer
	printAttributions(&rendered, auditScopes[scopeAll], []itemAttribution{{WorkItemID: "ifd.1"}}, goal.Unreadable("the artifact homes are outside the repository"))
	if !strings.Contains(rendered.String(), "none of them checked: the goals could not be read") {
		t.Fatalf("report = %q", rendered.String())
	}

	// A destroyed attribution is the exception, because saying it needs no goals
	// document: the tracker witnesses a goal was written and the item no longer
	// carries one. It is also what the audit exits non-zero for here, and a
	// failure with nothing said about it is worse than none.
	unreadable := goal.Unreadable("the artifact homes are outside the repository")
	lost := []itemAttribution{
		{WorkItemID: "ifd.1"},
		{WorkItemID: "ifd.102.2", Title: "Triage docket", Status: "open", Priority: 1,
			Attribution: unreadable.AttributionOf("Constraints from the architect.", goal.Witness{Recorded: true, Statement: "Run development nearly autonomously."})},
	}
	var withLoss bytes.Buffer
	printAttributions(&withLoss, auditScopes[scopeAll], lost, unreadable)
	if !strings.Contains(withLoss.String(), "ifd.102.2") || !strings.Contains(withLoss.String(), "written over rather than never made") {
		t.Fatalf("report = %q", withLoss.String())
	}
	if code := attributionExitCode(lost); code != 1 {
		t.Fatalf("exit code over a destroyed attribution nothing could be checked against = %d", code)
	}
}

func TestTheListPrintsTheBriefLinkAndReportsEachWayItBreaks(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+`
# Product brief

An introduction.

## Goals

- **Intent in, software out** — the harness carries approved intent to merged code.
`)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+`
# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from intent to verification.
  *Supports: intent in, software out.*
- Isolate implementation tasks in harness-managed worktrees.
- Publish work as pull requests the harness opens.
  *Supports: a claim the brief does not state.*
`)

	stdout, stderr, code := runCLI(t, "goals", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "supports: intent in, software out.") {
		t.Fatalf("list stdout = %q, want the resolved link printed beside the goal", stdout)
	}
	for _, want := range []string{
		"goal not linked to the brief:",
		"it names no brief goal",
		"a claim the brief does not state",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("list stderr = %q, want it to contain %q", stderr, want)
		}
	}

	jsonStdout, jsonStderr, jsonCode := runCLI(t, "goals", "list", "--json", "--config", configPath)
	if jsonCode != 0 {
		t.Fatalf("json list code = %d, stderr = %q", jsonCode, jsonStderr)
	}
	// The field names are the operator-facing contract, so the test decodes
	// them by name rather than through goalsOutput.
	var decoded struct {
		BriefGoals   []goal.BriefGoal   `json:"brief_goals"`
		LinkProblems []goal.LinkProblem `json:"link_problems"`
	}
	if err := json.Unmarshal([]byte(jsonStdout), &decoded); err != nil {
		t.Fatalf("decode json listing: %v", err)
	}
	if len(decoded.BriefGoals) != 1 || decoded.BriefGoals[0].Name != "Intent in, software out" {
		t.Fatalf("brief_goals = %+v, want the one bolded brief claim by name", decoded.BriefGoals)
	}
	if len(decoded.LinkProblems) != 2 {
		t.Fatalf("link_problems = %+v, want the unstated and dangling goals reported", decoded.LinkProblems)
	}
}

// The two states no build reddens over are the two this listing has to carry, so
// the check that moved here is exercised rather than assumed: a goal hard-wrapped
// across lines and a goal naming a brief claim the brief does not state are each
// reported by `yoyo goals list`, on stderr with a place to open and in `--json`
// under their own field, and neither of them stops the command.
func TestTheListReportsAWrappedGoalAndADanglingBriefLinkWithoutRefusing(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+`
# Product brief

An introduction.

## Goals

- **Intent in, software out** — the harness carries approved intent to merged code.
`)
	goalsPath := "docs/product/goals/v1-goals.md"
	writeArtifact(t, project, goalsPath, artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+`
# V1 goals

An introduction.

## Goals

- Run development nearly autonomously. The human's routine interface is the
  product manager, who states intent and answers what is escalated.
  *Supports: intent in, software out.*
- Publish work as pull requests the harness opens.
  *Supports: a claim the brief does not state.*
`)

	stdout, stderr, code := runCLI(t, "goals", "list", "--config", configPath)
	// Neither state refuses: the goal is stated and work naming it still
	// resolves, which is why these are read on stderr rather than failed on.
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "Run development nearly autonomously.") {
		t.Fatalf("list stdout = %q, want the wrapped goal still listed as a goal work can name", stdout)
	}
	for _, want := range []string{
		"goal not written on one line: " + goalsPath + ":",
		"goal not linked to the brief:",
		"a claim the brief does not state",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("list stderr = %q, want it to contain %q", stderr, want)
		}
	}

	jsonStdout, jsonStderr, jsonCode := runCLI(t, "goals", "list", "--json", "--config", configPath)
	if jsonCode != 0 {
		t.Fatalf("json list code = %d, stderr = %q", jsonCode, jsonStderr)
	}
	// Decoded by field name, because what a program reads is the operator-facing
	// contract rather than whatever shape this package happens to marshal.
	var decoded struct {
		WrapProblems []goal.WrapProblem `json:"wrap_problems"`
		LinkProblems []goal.LinkProblem `json:"link_problems"`
	}
	if err := json.Unmarshal([]byte(jsonStdout), &decoded); err != nil {
		t.Fatalf("decode json listing: %v", err)
	}
	if len(decoded.WrapProblems) != 1 || len(decoded.LinkProblems) != 1 {
		t.Fatalf("wrap_problems = %+v and link_problems = %+v, want one of each", decoded.WrapProblems, decoded.LinkProblems)
	}
	if kind := decoded.LinkProblems[0].Kind; kind != goal.LinkDangling {
		t.Fatalf("link problem kind = %q, want %q", kind, goal.LinkDangling)
	}
	// The line is a place to open rather than a number: it names the physical
	// line the wrapped entry starts on, counted from the top of the file.
	wrapped := decoded.WrapProblems[0]
	content, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(goalsPath)))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(string(content), "\n")
	if wrapped.Line < 1 || wrapped.Line > len(lines) {
		t.Fatalf("wrap problem = %#v, want a line in a file of %d lines", wrapped, len(lines))
	}
	if got := lines[wrapped.Line-1]; !strings.HasPrefix(got, "- Run development nearly autonomously.") {
		t.Fatalf("line %d is %q, want the entry the wrapped goal opens on", wrapped.Line, got)
	}
}

func TestGoalsAuditCarriesRelevantGoalsInTextAndJSON(t *testing.T) {
	t.Parallel()
	relevant := []string{"Maintain a traceable chain.", "Use ordinary words."}
	goals := goal.Set{Sources: []string{"v1-goals"}, Goals: []goal.Goal{
		{Statement: relevant[0], ArtifactID: "v1-goals", InForce: true},
		{Statement: relevant[1], ArtifactID: "v1-goals", InForce: true},
	}}
	items := []beads.WorkItem{
		{ID: "ifd.1", Title: "Record relevance", Status: "open", Notes: goal.Note(relevant[0]), RelevantGoals: relevant},
		{ID: "ifd.2", Title: "Assess older work", Status: "open"},
	}
	attributions := attributionsOf(items, goals)
	var rendered bytes.Buffer
	printAttributions(&rendered, auditScopes[scopeAll], attributions, goals)
	for _, want := range []string{"relevant goals: Maintain a traceable chain.; Use ordinary words.", "relevant goals: none recorded"} {
		if !strings.Contains(rendered.String(), want) {
			t.Fatalf("text omitted %q: %s", want, rendered.String())
		}
	}
	encoded, err := json.Marshal(attributionOutput(auditScopes[scopeAll], attributions, goals))
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Attributions []struct {
			WorkItemID    string   `json:"work_item_id"`
			RelevantGoals []string `json:"relevant_goals"`
		} `json:"attributions"`
	}
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Attributions) != 2 || strings.Join(output.Attributions[0].RelevantGoals, ";") != strings.Join(relevant, ";") || output.Attributions[1].RelevantGoals == nil {
		t.Fatalf("JSON did not carry both items' lists: %s", encoded)
	}
	items[0].RelevantGoals[0] = "Changed after projection."
	if attributions[0].RelevantGoals[0] != "Maintain a traceable chain." {
		t.Fatal("audit retained the tracker slice")
	}
}

func TestUncheckedGoalsAuditListsRelevantGoalsBesideLostAttributions(t *testing.T) {
	t.Parallel()
	items := []beads.WorkItem{{ID: "ifd.1", Title: "Restore attribution", Status: "open", GoalWitness: goal.Witness{Recorded: true, Statement: "Maintain a traceable chain."}, RelevantGoals: []string{"Use ordinary words."}}}
	goals := goal.Unreadable("the goals could not be read")
	var rendered bytes.Buffer
	printAttributions(&rendered, auditScopes[scopeAll], attributionsOf(items, goals), goals)
	if !strings.Contains(rendered.String(), "relevant goals: Use ordinary words.") {
		t.Fatalf("unchecked listing omitted relevance: %s", rendered.String())
	}
}
