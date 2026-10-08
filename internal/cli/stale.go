package cli

// What a change to an upstream document left unanswered downstream, as the
// operator reads it: the artifacts that trace to a changed document and have not
// been over it since, and the admitted work pulled under wording that has moved.
//
// There is no command here that resolves any of it, and no exit status that
// treats it as a failure. Marking work stale must not stop or discard it — a
// change to a goal's wording is frequently not a change to what the work should
// do, and a harness that failed a build or cancelled a queue over an edit would
// teach an operator not to edit. What to do about each of these is the
// operator's decision or the owning role's; this says the condition is there and
// names what changed.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
)

// maxRenderedStaleEntries bounds how many stale documents and how many stale
// work items are listed, and maxRenderedChanges how many upstream changes are
// named under one of them. What an operator needs is what to look at, not an
// export, so the lists are cut while the counts stay exact.
const (
	maxRenderedStaleEntries = 20
	maxRenderedChanges      = 3
)

type staleOutput struct {
	Documents []staleness.Document `json:"documents,omitempty"`
	WorkItems []staleness.WorkItem `json:"work_items,omitempty"`
	Unjudged  []staleness.Unjudged `json:"unjudged,omitempty"`
	// Contradictions are documents of the product's intent in force that say
	// opposite things, each naming both.
	Contradictions []staleness.Contradiction `json:"contradictions,omitempty"`
	Admitted       int                       `json:"admitted"`
	Judged         int                       `json:"judged"`
	// WorkUnavailable says why no work item was judged when the tracker could not
	// be read, so a report carrying only the documents is never read as a queue
	// nothing has moved under.
	WorkUnavailable string `json:"work_unavailable,omitempty"`
	Error           string `json:"error,omitempty"`
}

// reportStaleness reads the artifacts and the admitted work and says what is
// downstream of a change nobody has answered. A tracker that cannot be read
// costs the work half of the report rather than the whole of it: the documents
// are readable without it, and losing them too would leave an operator with
// nothing over a tracker that is merely absent.
func reportStaleness(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("stale", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "stale does not accept positional arguments")
		printStaleUsage(stderr)
		return 2
	}

	// The repository and the tracker are all this reads, so they are resolved
	// directly rather than through the parts a run is built from: nothing here
	// makes a worktree, holds a lease, or writes durable state, and a command that
	// only reads documents should not refuse to run wherever one of those cannot
	// be built.
	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		return reportStaleError(stdout, stderr, *jsonOutput, err)
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return reportStaleError(stdout, stderr, *jsonOutput, fmt.Errorf("resolve product repository: %w", err))
	}
	artifacts, err := artifactStore(repository, resolved.Config.Product).Load()
	if err != nil {
		return reportStaleError(stdout, stderr, *jsonOutput, fmt.Errorf("read the recorded artifacts: %w", err))
	}
	goals := goal.Collect(resolved.Config.Product.IntentRoot(repository), artifacts)
	unavailable := ""
	admitted, err := admittedWorkItems(ctx, beads.Client{Runner: execution.OSProcessRunner{}, Dir: repository})
	if err != nil {
		unavailable = err.Error()
		admitted = nil
	}
	report := staleness.Survey(artifacts, goals, admitted)

	// A document in an artifact home with no usable identity, and a goals
	// document nobody can read goals out of, are each a hole nothing can be
	// followed through. They are named whichever way the report is rendered,
	// because a chain with a hole in it reports less staleness than there is and
	// the count above would read as the whole answer.
	for _, problem := range artifacts.Problems {
		fmt.Fprintf(stderr, "not an artifact: %s\n", problem)
	}
	for _, problem := range goals.Problems {
		fmt.Fprintf(stderr, "goals not read: %s\n", problem)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, staleOutput{
			Documents:       report.Documents,
			WorkItems:       report.WorkItems,
			Unjudged:        report.Unjudged,
			Contradictions:  report.Contradictions,
			Admitted:        report.Admitted,
			Judged:          report.Judged,
			WorkUnavailable: unavailable,
		})
	}
	printStaleness(stdout, report, unavailable)
	// Nothing here is a failure. Staleness is a condition to look at, and an exit
	// status that treated it as a broken build would make an amendment something
	// to avoid making.
	return 0
}

// printStaleness describes a reading for an operator: what is downstream of a
// change, what changed, and what this did and did not judge. What it says about
// the work it could not judge is as much the report as the staleness is — an
// item missing from a listing reads exactly like an item nothing has moved
// under.
func printStaleness(stdout io.Writer, report staleness.Report, unavailable string) {
	fmt.Fprintln(stdout, headline(report, unavailable))

	if len(report.Documents) > 0 {
		fmt.Fprintln(stdout, "\ndocuments:")
		listed := report.Documents
		if len(listed) > maxRenderedStaleEntries {
			listed = listed[:maxRenderedStaleEntries]
		}
		for _, document := range listed {
			fmt.Fprintf(stdout, "  %s [%s] %s, last revised %s\n",
				document.ID, document.Kind, document.Path, day(document.RevisedAt))
			printChanges(stdout, document.Changes)
		}
		if remaining := len(report.Documents) - len(listed); remaining > 0 {
			fmt.Fprintf(stdout, "  %d further document(s) are not listed here.\n", remaining)
		}
	}

	if len(report.WorkItems) > 0 {
		fmt.Fprintln(stdout, "\nopen work:")
		listed := report.WorkItems
		if len(listed) > maxRenderedStaleEntries {
			listed = listed[:maxRenderedStaleEntries]
		}
		for _, item := range listed {
			fmt.Fprintf(stdout, "  %s [p%d, %s] %s\n", item.ID, item.Priority, item.Status,
				singleLine(item.Title))
			fmt.Fprintf(stdout, "    admitted %s, serving a goal stated in %s: %s\n",
				day(item.AdmittedAt), item.ArtifactID, singleLine(item.Goal))
			printChanges(stdout, item.Changes)
		}
		if remaining := len(report.WorkItems) - len(listed); remaining > 0 {
			fmt.Fprintf(stdout, "  %d further work item(s) are not listed here.\n", remaining)
		}
	}

	if len(report.Contradictions) > 0 {
		fmt.Fprintln(stdout, "\ncontradictions between documents of the product's intent:")
		for _, contradiction := range report.Contradictions {
			fmt.Fprintf(stdout, "  %s: %s\n", contradiction.Kind, strings.Join(contradiction.Documents, " and "))
			if contradiction.Statement != "" {
				fmt.Fprintf(stdout, "    about: %s\n", singleLine(contradiction.Statement))
			}
			fmt.Fprintf(stdout, "    %s\n", singleLine(contradiction.Reason))
		}
	}

	fmt.Fprintf(stdout, "\n%s\n", judged(report, unavailable))
	for _, unjudged := range report.Unjudged {
		fmt.Fprintf(stdout, "  %s: %s\n", unjudged.WorkItemID, unjudged.Reason)
	}
	if report.Anything() {
		fmt.Fprintln(stdout, "\nnone of this is stopped, closed, or reordered. A change to a document's wording")
		fmt.Fprintln(stdout, "is often not a change to what the work should do, so what to do about each of")
		fmt.Fprintln(stdout, "these is yours to decide or the owning role's.")
	}
}

// headline is the first line, and it counts only what was actually read. A
// tracker nothing could read leaves the work out of the count rather than
// contributing a zero to it: "0 open work items" over a queue nobody looked at
// is the confident emptiness this whole report exists to replace.
func headline(report staleness.Report, unavailable string) string {
	if strings.TrimSpace(unavailable) != "" {
		if len(report.Documents) == 0 {
			return "no document downstream of a recorded change is unanswered." + contradictionCount(report)
		}
		verb := "are"
		if len(report.Documents) == 1 {
			verb = "is"
		}
		return fmt.Sprintf("%s %s downstream of a change nobody has answered.",
			plural(len(report.Documents), "document", "documents"), verb) + contradictionCount(report)
	}
	if len(report.Documents) == 0 && len(report.WorkItems) == 0 {
		return "nothing downstream of a recorded change is unanswered." + contradictionCount(report)
	}
	return fmt.Sprintf("%s and %s are downstream of a change nobody has answered.",
		plural(len(report.Documents), "document", "documents"),
		plural(len(report.WorkItems), "open work item", "open work items")) + contradictionCount(report)
}

// contradictionCount adds to the headline how many contradictions were found,
// and nothing where there are none. It is a clause of its own rather than part
// of the count before it because a contradiction is not downstream of anything:
// it is two documents of the product's intent saying opposite things now.
func contradictionCount(report staleness.Report) string {
	if len(report.Contradictions) == 0 {
		return ""
	}
	return fmt.Sprintf(" %s between documents of the product's intent, listed below.",
		plural(len(report.Contradictions), "contradiction", "contradictions"))
}

// printChanges names what changed upstream of one stale thing, most recent
// first. The reason each change recorded is carried because it is what decides
// whether anything has to be done: a rewording and a reversal of intent look
// identical without it. A rewording the Lead Product Manager recorded as
// consistent with intent is said as one, so it is not read as an amendment the
// operator has yet to see.
func printChanges(stdout io.Writer, changes []staleness.Change) {
	listed := changes
	if len(listed) > maxRenderedChanges {
		listed = listed[:maxRenderedChanges]
	}
	for _, change := range listed {
		action := string(change.Action)
		if change.Rewording {
			action = "reworded, consistent with intent,"
		}
		fmt.Fprintf(stdout, "    %s was %s %s by the %s: %s\n",
			change.ArtifactID, action, day(change.At), change.By,
			singleLine(change.Reason))
	}
	if remaining := len(changes) - len(listed); remaining > 0 {
		fmt.Fprintf(stdout, "    and %d earlier change(s) upstream of it\n", remaining)
	}
}

// judged says what this reading could and could not answer for. The tracker
// being unreadable and the queue naming no goal are different answers and are
// stated apart, because one is something to fix about the machine and the other
// is work somebody has yet to attribute.
func judged(report staleness.Report, unavailable string) string {
	if strings.TrimSpace(unavailable) != "" {
		return "no work item was judged: the admitted work could not be read (" +
			singleLine(unavailable) + ")."
	}
	if report.Admitted == 0 {
		return "no work is admitted, so there was none to judge."
	}
	rendered := fmt.Sprintf("%s: %d of them name a goal these changes could be followed to",
		plural(report.Admitted, "admitted item", "admitted items"), report.Judged)
	if unattributed := report.Admitted - report.Judged - len(report.Unjudged); unattributed > 0 {
		rendered += fmt.Sprintf(`, and %d name none this could follow ("yoyo goals attribution" reports those)`, unattributed)
	}
	if len(report.Unjudged) > 0 {
		rendered += fmt.Sprintf(", and %d could not be judged", len(report.Unjudged))
	}
	return rendered + "."
}

// day renders a moment as the date it happened on. What an operator is deciding
// is whether a document has been over a change, and the day answers that while a
// timestamp to the second buries it.
func day(moment time.Time) string {
	if moment.IsZero() {
		return "at no recorded time"
	}
	return moment.UTC().Format("2006-01-02")
}

func plural(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", count, many)
}

func reportStaleError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, staleOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printStaleUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo stale [options]

What a change to an upstream document left unanswered downstream. An artifact is
stale when something it supports -- the goal a design serves, the brief a goal
serves, through as many artifacts as the chain runs -- recorded a change after
the artifact itself was last revised. An admitted work item is stale when the
goals document stating the goal it serves, or anything upstream of that, changed
after the item was admitted.

Nothing is stored to make this true and nothing has to be marked: the artifacts'
own revision logs and the tracker's record of when each item was admitted say it,
so any reading of them reports the same thing, whether the document was amended
through the harness or edited by hand.

Stale is not cancelled. Nothing here stops, closes, blocks, or reorders any of
it, and this exits zero whatever it finds -- a change to a goal's wording is
frequently not a change to what the work should do, and a harness that failed
over an edit would teach you not to edit. What to do about each of these is
yours to decide, or the owning role's.

A rewording of the goals the Lead Product Manager recorded as consistent with
intent is listed as a rewording rather than as an amendment: your approval
stands through it, and the work admitted under the old wording is still listed,
because it may still read differently.

An artifact stops being reported when its owner records a revision of it later
than the change, which is the durable record that somebody looked. A work item
carries only when it was admitted, so a stale item stays reported until it is
closed.

Every document in the product's specification home is authoritative, so two of
them that are active and say opposite things are reported here too, naming both: two
briefs that are active, one statement one document states as a goal and another rules
out as a non-goal, and one goal identity two documents give to different goals.
Which of them is right is the owner's to decide.

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON`)
}
