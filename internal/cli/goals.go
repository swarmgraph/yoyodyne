package cli

// The goals a repository records, and what the admitted work says it is for.
//
// No command here decides what a piece of work is for, and that is the same
// decision the artifact commands make about writing artifacts: that judgement is
// the product manager's, made in the conversation where the operator can see it.
// What the harness owns is reading the goals, resolving what an item names
// against them, and saying which items are attributed to what — which is what
// this reports.
//
// Two commands here write, and both are that boundary rather than an exception
// to it. Witnessing copies the goal an item's own notes already state into the
// tracker's metadata, where replacing those notes cannot reach it.
// Re-attributing appends the goal an item already named, named by that goal's
// identity instead of by its wording, so the next amendment to the wording
// leaves the item attributed. Neither decides anything about any work — each
// records a goal somebody else chose — which is precisely why either can run
// over a backlog somebody else attributed.
//
// One command here refuses, and it is the same boundary again seen from in
// front: guarding reads a shell command an agent session is about to run and
// stops the one that would replace an item's notes without carrying its goal
// through, and the one that would set an item's status with no note saying
// what moved it. It judges no work either -- what it knows is what the command
// would destroy or leave unaccounted, which is a fact about the command and not
// about the item.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// goalsTrackerTimeout bounds one tracker command taken to read the admitted
// work, the same way every other command bounds its own: a tracker that has
// gone slow costs this report rather than hanging it.
const goalsTrackerTimeout = 30 * time.Second

type goalsOutput struct {
	Goals []goal.Goal `json:"goals,omitempty"`
	// BriefGoals are what the goals above link upward to, listed so a reader
	// resolving a link by hand has both ends of it. It is the listing's to carry
	// and only the listing's, because that is the report the goals themselves are
	// in — see attributionOutput for why the audit carries neither half.
	BriefGoals   []goal.BriefGoal   `json:"brief_goals,omitempty"`
	Problems     []goal.Problem     `json:"problems,omitempty"`
	LinkProblems []goal.LinkProblem `json:"link_problems,omitempty"`
	WrapProblems []goal.WrapProblem `json:"wrap_problems,omitempty"`
	// Coverage is what the audit read and what it left unread. It belongs to the
	// audit and to nothing else here: a listing of the goals reads no work, and a
	// sweep reports what it wrote rather than what it judged.
	Coverage *auditCoverage `json:"coverage,omitempty"`
	// Attributions are the work items the audit read and what each says it is for.
	Attributions []itemAttribution `json:"attributions,omitempty"`
	// Witnessed are the items a sweep recorded a witness on, and the goal each
	// one's own notes stated.
	Witnessed []itemWitness `json:"witnessed,omitempty"`
	// Reattributed are the items the migration moved onto their goal's identity,
	// and Unmatched are the ones it left exactly as they were and why. The second
	// is carried beside the first for the reason the audit carries its coverage:
	// a migration read from its successes alone reads as complete.
	Reattributed []itemReattribution `json:"reattributed,omitempty"`
	Unmatched    []itemReattribution `json:"unmatched,omitempty"`
	// Origins are the items the backfill set an origin on, or would have with
	// --dry-run, from what their own notes state.
	Origins []itemOrigin `json:"origins,omitempty"`
	Error   string       `json:"error,omitempty"`
}

// itemOrigin is one item the origin backfill wrote, and what it wrote. Failure
// is carried per item for the reason the witness sweep carries it.
type itemOrigin struct {
	WorkItemID  string `json:"work_item_id"`
	Asker       string `json:"asker"`
	AskedBy     string `json:"asked_by"`
	OnBehalfOf  string `json:"on_behalf_of"`
	AdmittedBy  string `json:"admitted_by"`
	Report      string `json:"report,omitempty"`
	Directive   string `json:"directive,omitempty"`
	Description string `json:"description"`
	Failure     string `json:"failure,omitempty"`
}

// itemAttribution is one admitted work item, the goal it serves, and the
// relevant goals recorded beside it.
type itemAttribution struct {
	RelevantGoals []string         `json:"relevant_goals"`
	WorkItemID    string           `json:"work_item_id"`
	Title         string           `json:"title"`
	Status        string           `json:"status"`
	Priority      int              `json:"priority"`
	Attribution   goal.Attribution `json:"attribution"`
}

// itemWitness is one item a sweep witnessed, and what it witnessed. Failure is
// carried per item rather than stopping the sweep: a tracker that refused one
// write has not made the other items less worth protecting.
type itemWitness struct {
	WorkItemID string `json:"work_item_id"`
	Goal       string `json:"goal"`
	Failure    string `json:"failure,omitempty"`
}

// itemReattribution is one item the migration read: what its notes named, the
// identity it was moved onto, and — where it was not moved — why it was left
// alone. Failure is carried per item for the reason the sweep carries it: a
// tracker that refused one write has not made the others less worth moving.
type itemReattribution struct {
	WorkItemID string `json:"work_item_id"`
	// Named is the goal the item's notes recorded, in the words they recorded it
	// in, so a reader can see what was resolved as well as what it resolved to.
	Named    string `json:"named"`
	Identity string `json:"identity,omitempty"`
	// Goal is the statement the identity picks out, as the document now words it.
	Goal    string `json:"goal,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Failure string `json:"failure,omitempty"`
}

func runGoals(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printGoalsUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listGoals(args[1:], stdout, stderr)
	case "attribution":
		return reportAttribution(ctx, args[1:], stdout, stderr)
	case "witness":
		return witnessRecordedGoals(ctx, args[1:], stdout, stderr)
	case "reattribute":
		return reattributeByIdentity(ctx, args[1:], stdout, stderr)
	case "origins":
		return backfillOrigins(ctx, args[1:], stdout, stderr)
	case "guard":
		// The tool call being decided arrives on stdin, which is why this is the
		// one goals command bound to the process's own input.
		return guardNotesReplacement(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown goals command %q\n\n", args[0])
		printGoalsUsage(stderr)
		return 2
	}
}

// listGoals prints the goals work may be attributed to. A goals document whose
// goals could not be read is named on stderr rather than leaving the listing
// looking like the whole of what the product intends.
func listGoals(args []string, stdout, stderr io.Writer) int {
	flags := newGoalsFlags("goals list", stderr)
	if code, ok := flags.parse(args); !ok {
		return code
	}
	goals, err := flags.goals()
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, goalsOutput{
			Goals:        goals.Goals,
			BriefGoals:   goals.BriefGoals,
			Problems:     goals.Problems,
			LinkProblems: goals.LinkProblems,
			WrapProblems: goals.WrapProblems,
		})
	}
	printGoals(stdout, console.ThemeFor(stdout, os.Getenv), goals)
	printGoalsProblems(stderr, goals)
	return 0
}

// printGoals is the listing itself, laid out to be read rather than parsed: one
// goal to an entry, with a blank line between entries, its statement weighted
// and the lines about it slanted and indented under it.
//
// The dressing is an addition and never the meaning, which is the discipline
// every surface here holds to and is recorded once in docs/developing-yoyo.md
// rather than re-argued at each of them. What it means for this listing is what
// is worth saying: what separates one goal from the next is the blank line,
// what says a line is about the goal above it is the indent and its label, and
// what says a goal is no longer something work may name is the marker in words —
// so this listing piped to a file, read where NO_COLOR is set, or shown on a
// terminal that says it is dumb says everything it says on a terminal that can
// be dressed. `--json` carries none of it.
func printGoals(stdout io.Writer, theme console.Theme, goals goal.Set) {
	if reason, uncheckable := goals.Uncheckable(); uncheckable {
		fmt.Fprintf(stdout, "no goal is active: %s\n", reason)
	}
	for index, recorded := range goals.Goals {
		if index > 0 {
			fmt.Fprintln(stdout)
		}
		state := ""
		if !recorded.InForce {
			// A goal in a document no longer in force is listed and marked, because
			// it is what an old attribution resolves to and reading it as a goal
			// somebody could still name would be wrong.
			state = " [no longer active]"
		}
		fmt.Fprint(stdout, theme.Entry(fmt.Sprintf("%s%s\n", recorded.Statement, state)))
		// The identity is printed under the words rather than in front of them,
		// because the words are what a reader came for and the identity is what
		// work names. A goal that carries none says so: it is the goal an amendment
		// would orphan the work under, and a reader shown nothing there would have
		// to know the absence meant something.
		if recorded.Identity != "" {
			fmt.Fprint(stdout, theme.Detail(fmt.Sprintf("  identity: %s\n", recorded.Identity)))
		} else {
			fmt.Fprint(stdout, theme.Detail("  identity: none, so work naming it matches on its wording\n"))
		}
		fmt.Fprint(stdout, theme.Detail(fmt.Sprintf("  stated by: %s (%s)\n", recorded.ArtifactID, recorded.Path)))
		if recorded.Supports != "" {
			fmt.Fprint(stdout, theme.Detail(fmt.Sprintf("  supports: %s\n", recorded.Supports)))
		}
	}
	printGoalsUpstream(stdout, theme, goals)
}

// printGoalsUpstream closes the listing by saying where the chain goes above it:
// these goals are not the top of it, and the brief they support is a file the
// reader can open. It is what a reader who took this listing for the whole of
// the product's intent was missing, and it is printed whether or not any goal
// was listed — somebody shown no goals at all is the reader most in need of
// being sent upstream.
//
// The brief is named from the artifacts rather than assumed, because where a
// product keeps its brief is that product's to decide. A repository recording
// none is told what these goals support without being sent to a file that is not
// there; what is wrong there is reported on stderr with the other broken links.
func printGoalsUpstream(stdout io.Writer, theme console.Theme, goals goal.Set) {
	line := "upstream: these goals support the goals the product brief states"
	if goals.BriefPath != "" {
		line += ", in " + goals.BriefPath
	}
	fmt.Fprintln(stdout)
	fmt.Fprint(stdout, theme.Detail(line+"\n"))
}

// reportAttribution says what the work the tracker holds is for: which items
// name a goal the goals state, which name none, which name something they do not
// state, and which recorded one and lost it.
//
// It reads every status by default, closed work included. That is a widening --
// it read the queue alone until yoyodyne-ifd.276 -- and the reason is that nine
// of the twelve recorded attribution losses were on closed items, so the slice
// the audit could not see is the slice the losses were actually in. What it
// covered is printed with what it found, because a report that says only what it
// found is one whose blind spot reads as a clean bill.
//
// The exit status separates the three ways an item is not attributed, because
// they are not the same finding. Work admitted before goals were checked names
// none, is grandfathered, and reporting it as a failure would fail a backlog
// nobody has had the chance to attribute yet. An item naming a goal the goals do
// not state is a claim that is wrong, and an item whose recorded goal was written
// over is a record that was destroyed; those two are what this exits non-zero
// for, on the terms attributionExitCode states for finished work.
func reportAttribution(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newGoalsFlags("goals attribution", stderr)
	// The scope is this command's alone, so it is registered on this command's
	// own flag set rather than on the one every goals command shares.
	scopeName := flags.set.String("scope", scopeAll,
		"which work to read: all (every status the tracker holds) or queue (open and blocked only)")
	if code, ok := flags.parse(args); !ok {
		return code
	}
	scope, known := auditScopes[*scopeName]
	if !known {
		fmt.Fprintf(stderr, "unknown scope %q: it is %q or %q\n", *scopeName, scopeAll, scopeQueue)
		printGoalsUsage(stderr)
		return 2
	}
	parts, err := buildComponents(*flags.configPath)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	goals, err := loadGoals(parts.repository, parts.config.Product)
	if err != nil {
		// The goals could not be read, so nothing here is a judgement about any
		// item. It is still reported against the work, because "nothing checked
		// this" is the answer and an empty report is not.
		fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	read, err := scope.workItems(ctx, parts.tracker())
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}

	attributions := attributionsOf(read, goals)
	if *flags.jsonOutput {
		if code := writeJSON(stdout, stderr, attributionOutput(scope, attributions, goals)); code != 0 {
			return code
		}
	} else {
		printAttributions(stdout, scope, attributions, goals)
		printGoalsProblems(stderr, goals)
	}
	return attributionExitCode(attributions)
}

// attributionOutput is what the audit says when it is read by a program: the
// items and what each is for, beside everything that was wrong with reading the
// goals it judged them against.
//
// It carries no brief goals, which the listing carries. The pair is worth
// printing where both ends of the link are, and this report holds neither the
// goals the link points from nor anything that resolves against it — the
// upstream half alone is a list a reader has nothing to read it against.
//
// It does carry what was read and what was not, for the same reason the printed
// report opens with it: a program deciding anything from an empty findings list
// has to be able to tell an audit that found nothing from one that looked
// nowhere.
func attributionOutput(scope auditScope, attributions []itemAttribution, goals goal.Set) goalsOutput {
	coverage := scope.coverage()
	return goalsOutput{
		Coverage:     &coverage,
		Attributions: attributions,
		Problems:     goals.Problems,
		LinkProblems: goals.LinkProblems,
		WrapProblems: goals.WrapProblems,
	}
}

// witnessRecordedGoals records, on every work item whose notes state a goal and
// which the tracker holds no witness for, the goal those notes state. It is
// what closes the gap the witness would otherwise leave: an attribution written
// before the witness existed is protected by nothing, and replacing its notes
// tomorrow would leave it reading as work nobody ever attributed.
//
// It walks every status rather than the backlog -- see trackerStatuses for why
// the item somebody is working on and the item somebody closed are exactly the
// ones a backlog-scoped sweep would have left out.
//
// It writes no attribution, which is why it is here at all. What it stores is
// what the item already says about itself, copied to where a careless writer
// cannot reach it — no judgement about what any work is for, and nothing the
// product manager did not already record in the conversation.
//
// One item's failure does not end the sweep. A tracker that refused one write
// has not made the rest less worth protecting, and every failure is named.
func witnessRecordedGoals(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newGoalsFlags("goals witness", stderr)
	if code, ok := flags.parse(args); !ok {
		return code
	}
	parts, err := buildComponents(*flags.configPath)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	tracker := parts.tracker()
	swept, err := workItemsWithStatus(ctx, tracker, trackerStatuses)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}

	witnessed, failures := recordGoalWitnesses(ctx, tracker, swept)
	if *flags.jsonOutput {
		if code := writeJSON(stdout, stderr, goalsOutput{Witnessed: witnessed}); code != 0 {
			return code
		}
	} else {
		printWitnessed(stdout, len(swept), witnessed)
	}
	if failures > 0 {
		return 1
	}
	return 0
}

// recordGoalWitnesses is the sweep itself: it writes a witness for every item
// whose notes state a goal and which carries none, and reports what it wrote
// and how many writes the tracker refused.
func recordGoalWitnesses(ctx context.Context, tracker beads.Client, admitted []beads.WorkItem) ([]itemWitness, int) {
	var witnessed []itemWitness
	failures := 0
	for _, item := range admitted {
		statement, records := goal.NamedIn(item.Notes)
		// An item stating no goal is left alone: there is nothing to witness, and a
		// witness written over one would turn work nobody has attributed yet into
		// work that reads as having lost an attribution it never had. One already
		// witnessed is left alone too — the witness records what was written, and
		// rewriting it every sweep would be a write per item for no fact gained.
		if !records || item.GoalWitness.Recorded {
			continue
		}
		recorded := itemWitness{WorkItemID: item.ID, Goal: statement}
		writeCtx, cancel := context.WithTimeout(ctx, goalsTrackerTimeout)
		_, err := tracker.RecordGoalWitness(writeCtx, item.ID, statement)
		cancel()
		if err != nil {
			recorded.Failure = err.Error()
			failures++
		}
		witnessed = append(witnessed, recorded)
	}
	return witnessed, failures
}

// backfillOrigins sets, on every work item that records no origin, the origin
// its own notes already state: the role that admitted it and the report or
// directive it was admitted from, in the words an admission writes them in. It
// is the one-time backfill for work admitted before admissions recorded where
// they came from as fields; work admitted since records its origin in the same
// write as the admission.
//
// It guesses nothing, and that leaves most old items alone. An item whose notes
// cite neither a report nor a directive is skipped, because notes never said
// whether the operator or a sweep asked for the work, and an item whose notes do
// not name exactly one admitting role, report, and directive is skipped too.
// What is skipped reads as origin unknown, which is the truth about it.
//
// It walks every status, for the reason the witness sweep does, and one item's
// failure does not end it.
func backfillOrigins(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newGoalsFlags("goals origins", stderr)
	dryRun := flags.set.Bool("dry-run", false, "report the origins that would be set and write nothing")
	if code, ok := flags.parse(args); !ok {
		return code
	}
	parts, err := buildComponents(*flags.configPath)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	tracker := parts.tracker()
	swept, err := workItemsWithStatus(ctx, tracker, trackerStatuses)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	set, failures := recordOrigins(ctx, tracker, swept, *dryRun)
	if *flags.jsonOutput {
		if code := writeJSON(stdout, stderr, goalsOutput{Origins: set}); code != 0 {
			return code
		}
	} else {
		printOrigins(stdout, len(swept), set, *dryRun)
	}
	if failures > 0 {
		return 1
	}
	return 0
}

// originRecorder is the one tracker write the backfill makes.
type originRecorder interface {
	RecordOrigin(ctx context.Context, id string, origin domain.WorkItemOrigin) (beads.WorkItem, error)
}

// recordOrigins is the backfill itself: for every item that records no origin
// and whose notes state one exactly, it writes that origin, and it reports what
// it wrote and how many writes the tracker refused.
func recordOrigins(ctx context.Context, tracker originRecorder, admitted []beads.WorkItem, dryRun bool) ([]itemOrigin, int) {
	var set []itemOrigin
	failures := 0
	for _, item := range admitted {
		if item.Origin.Known() {
			continue
		}
		origin, stated := chat.OriginFromNotes(item.Notes)
		if !stated {
			continue
		}
		entry := itemOrigin{
			WorkItemID:  item.ID,
			Asker:       string(origin.Asker),
			AskedBy:     origin.AskedBy(),
			OnBehalfOf:  origin.OnBehalfOf(),
			AdmittedBy:  string(origin.AdmittedBy),
			Report:      origin.Report,
			Directive:   origin.Directive,
			Description: origin.Describe(),
		}
		if !dryRun {
			writeCtx, cancel := context.WithTimeout(ctx, goalsTrackerTimeout)
			_, err := tracker.RecordOrigin(writeCtx, item.ID, origin)
			cancel()
			if err != nil {
				entry.Failure = err.Error()
				failures++
			}
		}
		set = append(set, entry)
	}
	return set, failures
}

func printOrigins(stdout io.Writer, swept int, set []itemOrigin, dryRun bool) {
	verb := "given an origin"
	if dryRun {
		verb = "would be given an origin (nothing was written)"
	}
	fmt.Fprintf(stdout, "%d work item(s): %d %s from their own notes; the rest already record one or state none exactly, and read as they did\n",
		swept, len(set), verb)
	for _, entry := range set {
		if entry.Failure != "" {
			fmt.Fprintf(stdout, "  %s could not be given an origin: %s\n", entry.WorkItemID, entry.Failure)
			continue
		}
		fmt.Fprintf(stdout, "  %s: %s\n", entry.WorkItemID, entry.Description)
	}
}

// reattributeByIdentity moves every attribution that matches on a goal's
// wording onto that goal's identity. It is the migration the identity work
// needs: attributions written before goals carried identifiers name the words,
// and the words are exactly what the next amendment changes.
//
// It resolves each item's recorded goal against the goals as they now stand and
// records what it resolved to, appending rather than replacing — the item's
// history of what it was admitted under is part of how the queue came to be what
// it is, and replacing notes is the loss the witness exists for.
//
// It guesses at nothing. An item whose recorded goal resolves to nothing, or
// resolves to a goal that carries no identity yet, is reported and left exactly
// as it was: the first is a claim somebody has to correct and the second is a
// document somebody has to amend, and neither is a judgement this can make about
// what the work is for.
func reattributeByIdentity(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newGoalsFlags("goals reattribute", stderr)
	// A migration over a live backlog is worth being able to read before it is
	// run, which is what this is for: the report is the same one, and nothing is
	// written.
	dryRun := flags.set.Bool("dry-run", false, "report what would be re-attributed and write nothing")
	if code, ok := flags.parse(args); !ok {
		return code
	}
	parts, err := buildComponents(*flags.configPath)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	goals, err := loadGoals(parts.repository, parts.config.Product)
	if err != nil {
		// Nothing is re-attributed against goals nobody could read. An item moved
		// onto an identity read out of a half-loaded set would be an attribution the
		// harness invented, which is the failure this whole mechanism exists to make
		// impossible.
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	if reason, uncheckable := goals.Uncheckable(); uncheckable {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput,
			fmt.Errorf("nothing was re-attributed: %s", reason))
	}
	tracker := parts.tracker()
	read, err := workItemsWithStatus(ctx, tracker, trackerStatuses)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}

	moved, unmatched, failures := recordGoalIdentities(ctx, tracker, goals, read, *dryRun)
	if *flags.jsonOutput {
		if code := writeJSON(stdout, stderr, goalsOutput{Reattributed: moved, Unmatched: unmatched}); code != 0 {
			return code
		}
	} else {
		printReattributed(stdout, len(read), moved, unmatched, *dryRun)
		printGoalsProblems(stderr, goals)
	}
	// An item nobody could move is what somebody has to act on, so it is in the
	// status as well as in the report: a migration that left work behind and
	// exited zero is one whose leftovers are found by eye or not at all.
	if failures > 0 || len(unmatched) > 0 {
		return 1
	}
	return 0
}

// recordGoalIdentities is the migration itself. One item's failure does not end
// it, for the reason a refused witness does not end the sweep: a tracker that
// refused one write has not made the rest less worth moving.
func recordGoalIdentities(ctx context.Context, tracker beads.Client, goals goal.Set, read []beads.WorkItem, dryRun bool) (moved []itemReattribution, unmatched []itemReattribution, failures int) {
	for _, item := range read {
		named, records := goal.NamedIn(item.Notes)
		if !records {
			// An item that records no goal is not this command's to attribute. What
			// work is for is the product manager's judgement, and an item that names
			// none is reported by the audit for them to settle.
			continue
		}
		attribution := goals.Attribute(named)
		entry := itemReattribution{WorkItemID: item.ID, Named: named}
		switch {
		case !attribution.ResolvedByWording():
			// Already on an identity, or not resolving at all. The second is the
			// audit's finding rather than this command's, and re-attributing an item
			// whose claim is wrong would be correcting the claim rather than moving it.
			if attribution.Resolved() {
				continue
			}
			entry.Reason = attribution.Reason
			unmatched = append(unmatched, entry)
			continue
		case attribution.Goal.Identity == "":
			entry.Goal = attribution.Goal.Statement
			entry.Reason = fmt.Sprintf("the goal it names is stated by %s and carries no identity, so there is nothing to move it onto; the identity is assigned in the goals document, which is the Lead Product Manager's to amend",
				attribution.Goal.ArtifactID)
			unmatched = append(unmatched, entry)
			continue
		}
		entry.Identity = attribution.Goal.Identity
		entry.Goal = attribution.Goal.Statement
		if dryRun {
			moved = append(moved, entry)
			continue
		}
		writeCtx, cancel := context.WithTimeout(ctx, goalsTrackerTimeout)
		_, err := tracker.Update(writeCtx, item.ID, beads.WorkItemChange{AppendNotes: reattributionNote(attribution)})
		cancel()
		if err != nil {
			entry.Failure = err.Error()
			failures++
		}
		moved = append(moved, entry)
	}
	return moved, unmatched, failures
}

// reattributionNote is what one migrated item records: why the line was added,
// and then the attribution itself in the form everything else reads. The goal is
// the one the item already named — what changes is that the item now names it by
// the thing that does not move.
func reattributionNote(attribution goal.Attribution) string {
	return fmt.Sprintf("Attribution recorded by identity (yoyo goals reattribute): the goal this item named resolved to %s in %s, and the identity is what the attribution now matches on, so re-wording the goal leaves this item attributed.\n\n%s",
		attribution.Goal.Identity, attribution.Goal.ArtifactID, goal.Note(attribution.Goal.Reference()))
}

// printReattributed is the migration's report: what it moved, and what it could
// not move and why. The second half is the point of running it — an item left
// behind is one an amendment still orphans — so it is printed even when nothing
// moved at all.
func printReattributed(stdout io.Writer, read int, moved, unmatched []itemReattribution, dryRun bool) {
	did, could := "re-attributed", "could not be re-attributed"
	if dryRun {
		did, could = "would be re-attributed", "would not be re-attributed"
		fmt.Fprintln(stdout, "nothing was written: this is what --dry-run reports")
	}
	fmt.Fprintf(stdout, "%d work item(s) read: %d %s by identity, %d %s\n", read, len(moved), did, len(unmatched), could)
	for _, entry := range moved {
		if entry.Failure != "" {
			fmt.Fprintf(stdout, "  %s could not be written: %s\n", entry.WorkItemID, entry.Failure)
			continue
		}
		fmt.Fprintf(stdout, "  %s -> [%s] %s\n", entry.WorkItemID, entry.Identity, entry.Goal)
	}
	for _, entry := range unmatched {
		fmt.Fprintf(stdout, "  %s left as it was: %s\n", entry.WorkItemID, entry.Reason)
	}
}

// maxToolCallBytes bounds the tool call read from stdin. It is generous because
// a replacement's notes are prose and a command line can carry a lot of it, and
// it is bounded at all for the reason every other read here is: a hook is given
// whatever the session hands it, and a guard that could be made to hold a
// session's whole memory is a guard that becomes the failure.
const maxToolCallBytes = 1 << 20

// toolCall is the tool invocation a Claude Code `PreToolUse` hook is given on
// stdin. Only the two fields this decides on are read; everything else the hook
// carries belongs to whoever put it there.
type toolCall struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// hookDecision is the answer a `PreToolUse` hook gives back, in the shape the
// hook protocol reads it in. Nothing is printed to allow a call: silence is how
// the protocol spells "this hook has no opinion", and it is what nearly every
// command an agent runs gets.
type hookDecision struct {
	Output hookDecisionOutput `json:"hookSpecificOutput"`
}

type hookDecisionOutput struct {
	EventName string `json:"hookEventName"`
	Decision  string `json:"permissionDecision"`
	Reason    string `json:"permissionDecisionReason"`
}

// guardNotesReplacement decides one shell command an agent session is about to
// run: it refuses `bd update --notes` because notes are append-only, refuses
// `bd update --status` where no note is appended saying what moved the status,
// and says nothing about anything else.
//
// It is deliberately the only thing here that never fails a session. A tool call
// this cannot read is allowed, and said so on stderr: this stands in front of
// every shell command an agent takes, so refusing what it cannot parse would
// turn a hook payload it did not recognise into a session that can run no
// commands at all. That is the guard being the outage instead of preventing one.
//
// What makes fail-open honest is that this is not the only protection. A
// witnessed attribution destroyed on work the audit reads is a loss
// `goals attribution` reports and exits non-zero for, and on the work it does
// not read the witness still holds the words to put back. This is the writer
// stopped before the fact; the witness is what survives it.
func guardNotesReplacement(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("goals guard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "goals guard does not accept positional arguments; the tool call it decides is read from stdin")
		return 2
	}
	payload, err := io.ReadAll(io.LimitReader(stdin, maxToolCallBytes))
	if err != nil {
		fmt.Fprintf(stderr, "goals guard read the tool call: %v; the command was allowed unexamined\n", err)
		return 0
	}
	var call toolCall
	if err := json.Unmarshal(payload, &call); err != nil {
		fmt.Fprintf(stderr, "goals guard could not read the tool call: %v; the command was allowed unexamined\n", err)
		return 0
	}
	// Only a shell command can carry the writer. Every other tool reaches the
	// tracker through the harness, which appends rather than replaces.
	if call.ToolName != "Bash" {
		return 0
	}
	// Two rules over the same line, the notes first: a command replacing
	// the notes is refused for what it destroys before anything is said about
	// where it moves the status, because the destroyed record is the loss that
	// leaves nothing behind. A bare status move is the other silent rewrite --
	// the status changes and nothing on the item says what moved it.
	refusal := beads.DestroyedAttribution(call.ToolInput.Command)
	if refusal == "" {
		refusal = beads.UnaccountedStatusMove(call.ToolInput.Command)
	}
	if refusal == "" {
		return 0
	}
	return writeJSON(stdout, stderr, hookDecision{Output: hookDecisionOutput{
		EventName: "PreToolUse",
		Decision:  "deny",
		Reason:    refusal,
	}})
}

func printWitnessed(stdout io.Writer, swept int, witnessed []itemWitness) {
	fmt.Fprintf(stdout, "%d work item(s): %d newly witnessed, %d already witnessed or recording no goal\n",
		swept, len(witnessed), swept-len(witnessed))
	for _, entry := range witnessed {
		if entry.Failure != "" {
			fmt.Fprintf(stdout, "  %s could not be witnessed: %s\n", entry.WorkItemID, entry.Failure)
			continue
		}
		fmt.Fprintf(stdout, "  %s witnessed: %s\n", entry.WorkItemID, entry.Goal)
	}
}

// attributionsOf judges every admitted item against the goals: what the item
// records, and what the tracker witnesses was once recorded on it. It is one
// function rather than a loop inside the command so that what the audit reports
// is exactly what anything else asking the same question gets.
func attributionsOf(admitted []beads.WorkItem, goals goal.Set) []itemAttribution {
	attributions := make([]itemAttribution, 0, len(admitted))
	for _, item := range admitted {
		attributions = append(attributions, itemAttribution{
			RelevantGoals: append([]string{}, item.RelevantGoals...),
			WorkItemID:    item.ID,
			Title:         item.Title,
			Status:        item.Status,
			Priority:      item.Priority,
			Attribution:   goals.AttributionOf(item.Notes, item.GoalWitness),
		})
	}
	return attributions
}

// attributionExitCode is the status the audit exits with: non-zero for an item
// whose attribution is wrong or was destroyed, and zero for one that never had
// one. Which of those is which is goal.Attribution.Divergent, where the
// grandfathering decision is stated once — the release-readiness gate is held to
// the same rule, and two copies of it would be two answers to what the backlog
// has to trace to.
//
// Reporting a destroyed attribution without failing is how six items stayed
// orphaned for as long as it took somebody to read the report by eye, which is
// why the status is taken from the report rather than left to the reader.
//
// Closed work is read now, and it is held to one half of that rule rather than
// both. A destroyed attribution fails wherever it is found: a record written over
// after the item closed is a record written over, the witness holds the words,
// and putting them back is something somebody can actually do. A wrong
// attribution on closed work is counted and named and does not fail, because the
// item named what the goals stated when it was admitted and the ordinary way that
// stops resolving is a goal reworded afterwards -- which is `yoyo stale`'s to
// report, and is not a claim anybody can correct on work that is finished. Both
// halves are said in the report, so neither is a rule a reader has to infer from
// an exit code.
func attributionExitCode(attributions []itemAttribution) int {
	for _, entry := range attributions {
		if !entry.Attribution.Divergent() {
			continue
		}
		if entry.Status == closedStatus && entry.Attribution.State != goal.StateLost {
			continue
		}
		return 1
	}
	return 0
}

// trackerStatuses is every status an item can be in, rather than the backlog's
// `open` and `blocked`. Both the sweep and the audit's default scope walk all of
// it, and they walk one list so that the work a loss is reported on is work the
// sweep held the words to put back for.
//
// The scope is wider than the backlog's deliberately, because what destroys an
// attribution is a command somebody types and it reaches a claimed or a closed
// item exactly as easily as a queued one. Of the twelve recorded losses, one was
// on an item somebody was working on and nine were on closed items, several of
// those written over after the item closed -- so a sweep scoped to the backlog
// would have covered two of the twelve.
//
// The sweep reached this far before the audit did, and what the gap cost is why
// the audit reaches it now: an audit that could not read closed work reported
// nothing about nine of those twelve losses, which is how the intact
// counter-example on a closed item was missed and the same destruction was
// misdiagnosed twice.
var trackerStatuses = []string{"open", "in_progress", "blocked", "closed"}

// closedStatus is the tracker's name for work that is finished. The audit reads
// it and judges it by a rule of its own -- see attributionExitCode.
const closedStatus = "closed"

const (
	// scopeAll reads every status the tracker holds, and is what the audit does
	// unless it is asked for less. An attribution destroyed on closed work is
	// destroyed all the same, and an audit that cannot see it reports nothing
	// rather than reporting nothing wrong.
	scopeAll = "all"
	// scopeQueue reads what the backlog is assembled from, which is all this
	// audit could read before. It is kept because what the work still to be done
	// traces to is a narrower question somebody may actually be asking, and
	// answering it should not mean reading past the whole tracker by eye.
	scopeQueue = "queue"
)

// auditScope is the tracker slices one run of the audit reads. It is a named
// thing rather than a list passed around because the audit has to say what it
// covered: a report that leaves the coverage to be guessed is one where "nothing
// wrong" and "nothing looked at" read alike, which is the whole of what went
// wrong here.
type auditScope struct {
	name string
	read []string
}

var auditScopes = map[string]auditScope{
	scopeAll:   {name: scopeAll, read: trackerStatuses},
	scopeQueue: {name: scopeQueue, read: backlogStatuses},
}

// workItems is the audit's read path: the tracker slices this scope covers, in
// the order the backlog is ordered.
func (s auditScope) workItems(ctx context.Context, tracker beads.Client) ([]beads.WorkItem, error) {
	return workItemsWithStatus(ctx, tracker, s.read)
}

// coverage is what this scope read and what it left unread, taken from the one
// list of statuses rather than written down twice.
func (s auditScope) coverage() auditCoverage {
	covered := map[string]bool{}
	for _, status := range s.read {
		covered[status] = true
	}
	var unread []string
	for _, status := range trackerStatuses {
		if !covered[status] {
			unread = append(unread, status)
		}
	}
	return auditCoverage{Scope: s.name, Read: s.read, Unread: unread}
}

// auditCoverage is what one run of the audit read and what it did not, carried
// in the report itself. A finding is only worth what the ground it was looked
// for on is worth, and the reader of a report that does not say which statuses
// were walked has no way to tell a clean audit from a blind one.
type auditCoverage struct {
	Scope  string   `json:"scope"`
	Read   []string `json:"read"`
	Unread []string `json:"unread,omitempty"`
}

// saying is the coverage as a line to be read rather than parsed.
func (c auditCoverage) saying() string {
	line := "coverage: read " + strings.Join(c.Read, ", ")
	if len(c.Unread) == 0 {
		return line + " -- every status the tracker holds, so nothing went unchecked"
	}
	return line + fmt.Sprintf("; %s work was not checked, which --scope=%s reads",
		strings.Join(c.Unread, " and "), scopeAll)
}

// admittedWorkItems is the work that has been admitted and is not finished,
// assembled from the same tracker slices the backlog is. The audit no longer
// reads through it -- it has a scope of its own, and reads wider by default --
// but the staleness report, the conformance sweep, and the scheduler all ask
// this narrower question and mean it.
func admittedWorkItems(ctx context.Context, tracker beads.Client) ([]beads.WorkItem, error) {
	return workItemsWithStatus(ctx, tracker, backlogStatuses)
}

// workItemsWithStatus reads the tracker one status at a time, because that is
// what the tracker offers, and orders what it read the way the backlog is
// ordered so a report and a sweep walk the same items in the same order.
func workItemsWithStatus(ctx context.Context, tracker beads.Client, statuses []string) ([]beads.WorkItem, error) {
	var read []beads.WorkItem
	for _, status := range statuses {
		listCtx, cancel := context.WithTimeout(ctx, goalsTrackerTimeout)
		items, err := tracker.List(listCtx, status)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("list %s work items: %w", status, err)
		}
		read = append(read, items...)
	}
	backlog.Sort(read)
	return read, nil
}

// printAttributions is the report itself, and it opens with what was read rather
// than with what was found. The order is the point: an audit blind to a slice of
// the tracker prints the same clean tally as one that checked it, and the line
// above the tally is what tells the two apart.
func printAttributions(stdout io.Writer, scope auditScope, attributions []itemAttribution, goals goal.Set) {
	fmt.Fprintln(stdout, scope.coverage().saying())
	if len(attributions) == 0 {
		fmt.Fprintln(stdout, "no work was read, so there is nothing to attribute")
		return
	}
	if reason, uncheckable := goals.Uncheckable(); uncheckable {
		// Nothing was checked, so no count is reported. A tally against goals
		// nobody could read would say the queue traces to intent that was never
		// consulted.
		fmt.Fprintf(stdout, "%d work item(s), none of them checked: %s\n", len(attributions), reason)
		// An attribution that was destroyed is still said, because saying it needs
		// no goals document: what it rests on is that the tracker witnesses a goal
		// was written and the item no longer carries one. It is also what this
		// exits non-zero for, and an unexplained failure is worse than none.
		for _, entry := range attributions {
			if entry.Attribution.State == goal.StateLost {
				fmt.Fprintf(stdout, "  %s [p%d, %s] %s\n    %s\n",
					entry.WorkItemID, entry.Priority, entry.Status, entry.Title, entry.Attribution.Reason)
				fmt.Fprintf(stdout, "    relevant goals: %s\n", goals.DescribeRelevant(entry.RelevantGoals))
			}
		}
		return
	}
	grouped := map[goal.State][]itemAttribution{}
	for _, entry := range attributions {
		grouped[entry.Attribution.State] = append(grouped[entry.Attribution.State], entry)
	}
	fmt.Fprintf(stdout, "%d work item(s): %d serve a recorded goal, %d name none, %d name a goal the goals do not state, %d lost the goal they recorded\n",
		len(attributions), len(grouped[goal.StateAttributed]), len(grouped[goal.StateUnattributed]),
		len(grouped[goal.StateUnresolved]), len(grouped[goal.StateLost]))
	// What the resolved items matched on is said in the tally, because it is the
	// difference between a backlog an amendment cannot touch and one an amendment
	// orphans, and both tally identically without this line.
	if wording := resolvedByWording(attributions); wording > 0 {
		fmt.Fprintf(stdout, "  %d of those match on the goal's wording rather than on its identity, so re-wording that goal orphans them; `yoyo goals reattribute` records the identity where the goal carries one\n", wording)
	}
	for _, group := range []struct {
		state  goal.State
		saying string
		// closed is what having finished means for this group, said where the group
		// holds any finished work. The audit reads closed items now and does not
		// treat every finding on them alike, so the difference is printed rather
		// than left to be worked out from an exit code.
		closed string
	}{
		// The destroyed attributions are listed first, above the wrong ones. They
		// are the only group that got there without anybody deciding anything, and
		// the only one where the answer may already be known: each item's own line
		// says whether the tracker kept the words to put back.
		{goal.StateLost, "having recorded a goal and lost it, which is a record destroyed rather than a judgement nobody made",
			"%d of them on closed work, which fails like any other: a record written over after the item closed is still a record written over, and the words to put back are the tracker's to give\n"},
		{goal.StateUnresolved, "naming a goal no goals document states, which is a claim to correct",
			"%d of them on closed work, which is counted here and does not fail: the item named what the goals stated at the time, and a goal reworded after it closed is what `yoyo stale` reports rather than a claim anybody can correct\n"},
		{goal.StateUnattributed, "naming no goal, which is what work admitted before goals were checked looks like", ""},
		{goal.StateAttributed, "serving a recorded goal", ""},
	} {
		entries := grouped[group.state]
		if len(entries) == 0 {
			continue
		}
		fmt.Fprintf(stdout, "\n%s:\n", group.saying)
		if closed := closedIn(entries); closed > 0 && group.closed != "" {
			fmt.Fprintf(stdout, "  "+group.closed, closed)
		}
		for _, entry := range entries {
			fmt.Fprintf(stdout, "  %s [p%d, %s] %s\n", entry.WorkItemID, entry.Priority, entry.Status, entry.Title)
			fmt.Fprintf(stdout, "    %s\n", attributionDetail(entry.Attribution))
			fmt.Fprintf(stdout, "    relevant goals: %s\n", goals.DescribeRelevant(entry.RelevantGoals))
		}
	}
}

// resolvedByWording counts the items whose attribution resolved on the goal's
// wording rather than on its identity. They are the items an amendment to that
// wording orphans, and they are counted here rather than at each place that says
// so, because the audit and the migration have to agree about which items they
// are.
func resolvedByWording(attributions []itemAttribution) int {
	matched := 0
	for _, entry := range attributions {
		if entry.Attribution.ResolvedByWording() {
			matched++
		}
	}
	return matched
}

// closedIn counts the entries in a group that are on work already finished. What
// that means is the group's to say; how many there are is the same question in
// both groups that say it.
func closedIn(entries []itemAttribution) int {
	closed := 0
	for _, entry := range entries {
		if entry.Status == closedStatus {
			closed++
		}
	}
	return closed
}

// attributionDetail is the one line under an item that says what its
// attribution amounts to: the goal it resolved to, or what is wrong with it.
//
// A resolved attribution says what it matched on as well as what it matched.
// An item that named the goal's identity is one no amendment can orphan; an item
// that matched on the wording is one the next amendment does orphan, and it
// reads identically until that happens — so the difference is printed rather
// than left to be inferred from what the line does not say.
func attributionDetail(attribution goal.Attribution) string {
	if !attribution.Resolved() {
		return attribution.Reason
	}
	if attribution.Identity != "" {
		return fmt.Sprintf("[%s] %s (%s)", attribution.Identity, attribution.Goal.Statement, attribution.Goal.ArtifactID)
	}
	if attribution.Goal.Identity != "" {
		return fmt.Sprintf("%s (%s), matched on its wording though the goal carries the identity %q; `yoyo goals reattribute` records it",
			attribution.Goal.Statement, attribution.Goal.ArtifactID, attribution.Goal.Identity)
	}
	return fmt.Sprintf("%s (%s), matched on its wording because the goal carries no identity, so re-wording that goal orphans this item",
		attribution.Goal.Statement, attribution.Goal.ArtifactID)
}

func printGoalsProblems(stderr io.Writer, goals goal.Set) {
	for _, problem := range goals.Problems {
		fmt.Fprintf(stderr, "goals not read: %s\n", problem)
	}
	// A broken link upstream is reported beside the goals rather than instead of
	// them: the goal is still stated and work can still be attributed to it, and
	// what is wrong is the chain above it.
	for _, problem := range goals.LinkProblems {
		fmt.Fprintf(stderr, "goal not linked to the brief: %s\n", problem)
	}
	// A hard-wrapped goal is reported for the same reason and in the same place:
	// the statement was rejoined and work can be attributed to it, and what is
	// wrong is that the goal the file records is the rejoining rather than
	// anything the file says outright.
	for _, problem := range goals.WrapProblems {
		fmt.Fprintf(stderr, "goal not written on one line: %s\n", problem)
	}
	// An identity two goals in force carry is reported here for the same reason:
	// both goals are stated and both are listed, and what is wrong is that work
	// naming that identity names neither of them.
	for _, problem := range goals.IdentityProblems {
		fmt.Fprintf(stderr, "goal identity stated twice: %s\n", problem)
	}
}

// loadGoals reads the goals the repository records, through the same artifact
// homes every other command reads artifacts from. A failure returns a set that
// says why rather than an empty one: a repository whose goals could not be read
// and one that records none lead to opposite conclusions about the same
// attribution, and a caller that carried on with an empty set would report the
// wrong one.
func loadGoals(repository string, product config.Product) (goal.Set, error) {
	artifacts, err := artifactStore(repository, product).Load()
	if err != nil {
		return goal.Unreadable(err.Error()), fmt.Errorf("read the recorded goals: %w", err)
	}
	return goal.Collect(repository, artifacts), nil
}

// goalsFlags is the flag set every goals command shares.
type goalsFlags struct {
	set        *flag.FlagSet
	name       string
	configPath *string
	jsonOutput *bool
}

func newGoalsFlags(name string, stderr io.Writer) *goalsFlags {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return &goalsFlags{
		set:        set,
		name:       name,
		configPath: set.String("config", "", "configuration file path (default: the nearest project configuration)"),
		jsonOutput: set.Bool("json", false, "emit machine-readable JSON"),
	}
}

func (f *goalsFlags) parse(args []string) (int, bool) {
	positional, err := parseArguments(f.set, args)
	if err != nil {
		return 2, false
	}
	if len(positional) != 0 {
		fmt.Fprintf(f.set.Output(), "%s does not accept positional arguments\n", f.name)
		printGoalsUsage(f.set.Output())
		return 2, false
	}
	return 0, true
}

// goals resolves the recorded goals the same way the artifact commands resolve
// the homes they read: relative to the project rather than to the .yoyodyne
// directory the configuration happens to live in.
func (f *goalsFlags) goals() (goal.Set, error) {
	resolved, err := loadConfiguration(*f.configPath)
	if err != nil {
		return goal.Set{}, err
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return goal.Set{}, fmt.Errorf("resolve product repository: %w", err)
	}
	return loadGoals(repository, resolved.Config.Product)
}

func reportGoalsError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, goalsOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printGoalsUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo goals <list|attribution|witness|reattribute|origins|guard> [options]

The goals the repository records, read from the goals artifacts themselves: each
entry under a goals document's `+"`Goals`"+` heading is a goal that work can be
attributed to. An entry opening with an identifier in square brackets — as in
`+"`- [traceable-chain] Maintain a traceable chain ...`"+` — carries that identifier
as its stable identity, and an attribution resolves by naming it. The goal's
words are what a person reads and what a work item displays; they are not what
the match depends on, so re-wording a goal orphans no item attributed by
identity and refuses no admission that names the identity.

A goal stating no identifier is matched on its words alone, which is the older
arrangement and the one a re-wording breaks. So is an admission that quotes a
goal's earlier wording and carries no identifier: the words are the whole of
what it gave, and they now match nothing. Naming the identity is what closes
that, which is what the roles are asked for and what the harness records.

No command here decides what a piece of work is for. That is the
Lead Product Manager's judgement, made in the conversation where the operator
can see it, and what the harness owns is resolving what an item names and saying
what it found. Three commands do write, and none writes a judgement: "witness"
copies the goal an item's notes already state into the tracker's metadata,
"reattribute" appends the goal an item already named, named by that goal's
identity, and "origins" copies where an item's notes say it came from into the
tracker's metadata.

  list          the goals work may be attributed to, their identities, and where
                each is stated
  attribution   what each work item the tracker holds says it is for
  witness       record, where a careless writer cannot reach it, the goal each
                work item's notes already state
  reattribute   move an attribution that matches on a goal's wording onto that
                goal's identity
  origins       record, as fields, who asked for each work item admitted before
                admissions recorded it, where the item's own notes say exactly
  guard         refuse a shell command that would replace an item's notes and
                destroy the goal recorded in them, or set an item's status
                with no note saying what moved it

"list" is laid out to be read: one goal to an entry, a blank line between
entries, and where the goal is stated indented under it, closing with a line
naming what these goals sit underneath — the goals the product brief states, and
the file to open for them. On a terminal the statement is bold and the lines
about it italic; the emphasis is an addition and nothing else, so a listing piped
to a file, read where `+"`NO_COLOR`"+` is set, or shown on a terminal that says it is
dumb says exactly what it says dressed. `+"`--json`"+` carries none of it.

An item admitted before goals were checked names none. That is reported and is
not a failure: it is somebody's to attribute, and nothing refuses to run it.
"attribution" exits non-zero for an item naming a goal no goals document states,
which is a claim that is wrong rather than one nobody has made, and for an item
the tracker witnesses recorded a goal and no longer carries one, which is a
record something wrote over rather than a judgement nobody made.

"attribution" reads every status the tracker holds -- open, in_progress, blocked,
and closed -- because nine of the twelve recorded attribution losses were on
closed items, and an audit that could not see them reported nothing about any of
them. "--scope=queue" reads open and blocked alone, which is the narrower
question of what the work still to be done traces to. Either way the report opens
with what it read and what it did not, so a clean tally cannot be mistaken for a
slice nobody looked at.

Finished work is held to one half of the failure rule. A destroyed attribution on
a closed item fails like any other, because the record was written over and the
witness holds the words to put back. An item naming a goal no goals document
states is counted and named on closed work and does not fail: it named what the
goals stated at the time, and a goal reworded after it closed is what "yoyo
stale" reports rather than a claim anybody can now correct.

"witness" writes, and it writes no attribution: the
goal it stores is the one the item's own notes state, copied into the tracker's
metadata so that replacing those notes is a loss "attribution" can report rather
than one it cannot see. An attribution made before this existed carries no
witness until it is swept, which is why it is worth running once over a backlog.
It sweeps every work item the tracker holds and not only the queue, for the same
reason the audit reads that far: the command that destroys an attribution reaches
a claimed or a closed item just as easily, and most of the losses on record were
on closed ones.

"reattribute" is the migration off the wording. An attribution recorded before
goals carried identities names the words, and the words are what the next
amendment changes; this resolves what each item recorded against the goals as
they now stand and appends the same goal named by its identity. It writes no
judgement about any work: the goal is the one the item already named, and what
changes is that the item names it by the thing that does not move. An item whose
recorded goal resolves to nothing, or whose goal carries no identity yet, is
reported and left exactly as it was rather than guessed at, and it exits non-zero
while any item is left behind. "--dry-run" reports the same thing and writes
nothing.

"origins" is the one-time backfill of where work came from. Every admission
now records who asked for the work and on whose behalf as fields on the item,
in the same write as the admission; an item admitted before that reads as
origin unknown. This sets those fields on an older item from what its own notes
already state -- the role that admitted it, and the report or directive it was
admitted from -- and from nothing else. Notes never said whether the operator
or a sweep asked, so an item citing neither a report nor a directive is left
unknown rather than guessed at, as is one whose notes name more than one of
anything. An item that already records an origin is never rewritten.
"--dry-run" reports what would be set and writes nothing.

"guard" is the same loss stopped before it happens. It reads a `+"`PreToolUse`"+` tool
call on stdin, as an agent session's hook gives it, and refuses every recognized
shell command running `+"`bd update <id> --notes`"+`. Notes are append-only: preserve
every earlier note and append a correction with `+"`--append-notes`"+`. A replacement
is refused even when it carries a `+"`Goal served:`"+` line. It decides from the
command line alone and never reads the item or opens the tracker. It also refuses
`+"`bd update <id> --status`"+`
where no `+"`--append-notes`"+` is on the same command, because a status set with no
note saying what moved it is the other silent rewrite -- four items were moved
backwards that way on 2026-09-18 -- and the direction of the move is not readable
from the line, so a note is asked for on every status set here. `+"`--claim`"+` is
not a status set and passes. It takes no options, prints nothing at all to allow
a command, and allows a tool call it cannot read rather than failing a session
over a payload it did not recognise.

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON
  --scope <all|queue>   ("attribution" only) which work to read: every status the
                        tracker holds, or open and blocked alone (default: all)
  --dry-run             ("reattribute" only) report what would be re-attributed
                        and write nothing`)
}
