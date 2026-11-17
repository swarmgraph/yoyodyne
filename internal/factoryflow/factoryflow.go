// Package factoryflow is what the harness reads for every pass of the
// program manager watching the factory-flow lane, beside the health
// observation: the pull requests the harness opened that are still open, the
// documents waiting to land and the document landings they wait for, and the
// places either is piling up that the pass is to name as findings of its own.
//
// No role reaches the forge, so the forge is read here, by the harness, with
// the same client the publication path opens requests through. A forge that
// could not be read is said as such in the section, and never rendered as an
// empty list: a pass told "no open pull requests" when the reading failed would
// be told something false. Nothing here closes, retries, or changes anything;
// it reads and says.
package factoryflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runinspect"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// LandingHeldTooLong is how long every document landing may stand held before
// the pass is handed it as a stall. A document's landing ordinarily takes
// minutes to an hour: its checks, a review, and a merge. Three hours with every
// landing held means nothing else any role confirms can start landing, which
// is how the architect's documents stalled for a day unnoticed.
const LandingHeldTooLong = 3 * time.Hour

// maxListed bounds how many pull requests, and how many documents of each
// kind, the section lists. The counts beside them are whole.
const maxListed = 40

// maxLineBytes bounds one listed line, cut on a character boundary with the
// cut marked.
const maxLineBytes = 400

// branchPrefix is what every branch the harness pushes starts with.
const branchPrefix = "yoyodyne/"

// Forge is the forge as this reading takes it. It is satisfied by
// publish.GitHub.
type Forge interface {
	ListOpenStatus(ctx context.Context) ([]publish.OpenPullRequestStatus, error)
}

// Runs is the harness's own record of runs and of documents waiting for a
// landing. It is satisfied by *runstate.Store. Every question is asked of the
// run index or of one record by name, never by reading every run record: the
// runs in flight are the ones holding document landings, and a pull request's
// run is found from its branch.
type Runs interface {
	Incomplete() ([]runstate.State, error)
	Runs(workItemID string) ([]runstate.State, error)
	Read(runID string) (runstate.State, error)
	DocumentWaits() ([]runstate.DocumentWait, error)
}

// MergeQueues is the merge queues' own record, read to find the run a merge
// queue candidate's pull request carries. It is satisfied by
// *runstate.MergeQueueStore.
type MergeQueues interface {
	Keys() ([]runstate.MergeQueueKey, error)
	Entries(key runstate.MergeQueueKey) ([]runstate.MergeQueueEntry, error)
}

// Reader takes the reading. Forge is nil in a project whose publishing is not
// automatic, where the harness opens no pull requests at all.
type Reader struct {
	Forge Forge
	Runs  Runs
	// Queues is optional; without it a merge queue candidate's pull request is
	// named as one no record names.
	Queues MergeQueues
	// Landings is execution.document_landings as the configuration reads it.
	Landings int
	Now      func() time.Time
}

// Reading is what one pass is handed: the section its message carries, the
// stalls as findings the harness adds to the pass's account, and what could
// not be read.
type Reading struct {
	Section  string
	Findings []sweep.Finding
	Problems []string
}

// carrier is what one run of the harness's carries: a work item or a document.
type carrier struct {
	state    runstate.State
	run      string
	item     string
	title    string
	document *runstate.DocumentPublication
}

func (c carrier) says() string {
	if c.document != nil {
		return fmt.Sprintf("document %s, the %s's, run %s", describeDocument(*c.document), c.document.Owner.Title(), c.run)
	}
	what := "work item " + c.item
	if strings.TrimSpace(c.title) != "" {
		what += " (" + strings.TrimSpace(c.title) + ")"
	}
	return what + ", run " + c.run
}

func describeDocument(document runstate.DocumentPublication) string {
	artifact := document.Candidate.Artifact
	name := strings.TrimSpace(artifact.Title)
	if name == "" {
		name = artifact.Path
	}
	if artifact.ID != "" {
		return fmt.Sprintf("%q (%s)", name, artifact.ID)
	}
	return fmt.Sprintf("%q", name)
}

// documentKey is one document across every attempt and every copy confirmed
// of it.
func documentKey(document runstate.DocumentPublication) string {
	if id := strings.TrimSpace(document.Candidate.Artifact.ID); id != "" {
		return id
	}
	return document.Candidate.Artifact.Path
}

// Read takes one reading. It never fails: what could not be read is said where
// it would have been listed, and named among the problems.
func (r Reader) Read(ctx context.Context) Reading {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	var reading Reading
	var inFlight []runstate.State
	runsRead := false
	if r.Runs == nil {
		reading.Problems = append(reading.Problems, "the run records are not wired here, so no pull request can be matched to its work and no document landing can be read")
	} else if incomplete, err := r.Runs.Incomplete(); err != nil {
		reading.Problems = append(reading.Problems, fmt.Sprintf("the runs in flight could not be read, so the document landings are unknown: %v", err))
	} else {
		inFlight, runsRead = incomplete, true
	}
	var queued []runstate.State

	var section strings.Builder
	fmt.Fprintf(&section, "## Factory flow: open pull requests and documents waiting to land\n\nRead by the harness itself at %s, for this pass. No role reaches the forge, so this is the only view of it you have.\n", localMoment(now))

	publications := map[string][]int{}
	documents := map[string]runstate.DocumentPublication{}
	section.WriteString("\n### Pull requests the harness opened that are still open\n\n")
	switch {
	case r.Forge == nil:
		section.WriteString("This project's publishing is not automatic, so the harness opens no pull requests and the forge was not read.\n")
	default:
		open, err := r.Forge.ListOpenStatus(ctx)
		if err != nil {
			problem := fmt.Sprintf("the forge could not be read on this pass, so what pull requests are open is NOT KNOWN; this is not an empty list, and nothing below about duplicate publications could be checked: %v", err)
			section.WriteString(cutLine(problem, 2*maxLineBytes) + "\n")
			reading.Problems = append(reading.Problems, problem)
			break
		}
		carriers := r.carriersFor(open)
		reading.Problems = append(reading.Problems, carriers.problems...)
		var ours []publish.OpenPullRequestStatus
		queuedRuns := map[string]bool{}
		for _, request := range open {
			found, matched := carriers.of(request)
			if matched || strings.HasPrefix(request.HeadBranch, branchPrefix) {
				ours = append(ours, request)
			}
			if matched && found.document != nil && found.state.AwaitingMergeQueue() && !queuedRuns[found.run] {
				queuedRuns[found.run] = true
				queued = append(queued, found.state)
			}
		}
		sort.SliceStable(ours, func(i, j int) bool {
			if !ours[i].CreatedAt.Equal(ours[j].CreatedAt) {
				return ours[i].CreatedAt.Before(ours[j].CreatedAt)
			}
			return ours[i].Number < ours[j].Number
		})
		if len(ours) == 0 {
			fmt.Fprintf(&section, "None: the forge holds %d open pull request(s), and none of them is the harness's.\n", len(open))
		} else {
			fmt.Fprintf(&section, "%d open, oldest first:\n\n", len(ours))
		}
		for index, request := range ours {
			found, matched := carriers.of(request)
			if found.document != nil {
				key := documentKey(*found.document)
				publications[key] = append(publications[key], request.Number)
				documents[key] = *found.document
			}
			if index >= maxListed {
				continue
			}
			what := "work the run records do not name"
			switch {
			case matched:
				what = found.says()
			case strings.HasPrefix(request.HeadBranch, runstate.MergeQueueCandidateBranchPrefix):
				what = "a merge queue candidate no run record names"
			case itemFromBranch(request.HeadBranch) != "":
				what = "work item " + itemFromBranch(request.HeadBranch) + " (read from its branch; no run record names it)"
			}
			section.WriteString("- " + cutLine(fmt.Sprintf("#%d carries %s; open %s; %s; checks: %s; branch %s",
				request.Number, what, age(now, request.CreatedAt), mergeability(request), request.Checks.Says(), request.HeadBranch), maxLineBytes) + "\n")
		}
		if len(ours) > maxListed {
			fmt.Fprintf(&section, "- and %d more not listed here; the count above is whole\n", len(ours)-maxListed)
		}
	}

	section.WriteString("\n### Documents waiting to land\n\n")
	landings := r.Landings
	if landings < 1 {
		landings = 1
	}
	var held []runstate.State
	if runsRead {
		for _, state := range inFlight {
			if state.HoldsDocumentLanding() {
				held = append(held, state)
			}
		}
		sort.SliceStable(held, func(i, j int) bool { return held[i].StartedAt.Before(held[j].StartedAt) })
		sort.SliceStable(queued, func(i, j int) bool { return queued[i].StartedAt.Before(queued[j].StartedAt) })
		fmt.Fprintf(&section, "Document landings (the places a confirmed document lands through, %d in all): %d held.\n", landings, len(held))
		for index, state := range held {
			if index >= maxListed {
				fmt.Fprintf(&section, "- and %d more held\n", len(held)-maxListed)
				break
			}
			section.WriteString("- " + cutLine(fmt.Sprintf("held by document %s, the %s's, run %s, since %s (%s); %s",
				describeDocument(*state.Document), state.Document.Owner.Title(), state.RunID, localMoment(state.StartedAt), age(now, state.StartedAt), waitsFor(state)), maxLineBytes) + "\n")
		}
		for free := len(held); free < landings; free++ {
			section.WriteString("- free\n")
		}
	} else {
		section.WriteString("Which document landings are held is NOT KNOWN: the run records could not be read.\n")
	}

	var waits []runstate.DocumentWait
	if r.Runs != nil {
		if listed, err := r.Runs.DocumentWaits(); err != nil {
			problem := fmt.Sprintf("the documents waiting for a document landing could not be read, so that list is NOT KNOWN rather than empty: %v", err)
			section.WriteString("\n" + cutLine(problem, 2*maxLineBytes) + "\n")
			reading.Problems = append(reading.Problems, problem)
		} else {
			waits = listed
			if len(waits) == 0 {
				section.WriteString("\nNo confirmed document is waiting for a document landing to free.\n")
			} else {
				fmt.Fprintf(&section, "\n%d confirmed document(s) waiting for a document landing to free, longest waiting first:\n\n", len(waits))
			}
			for index, wait := range waits {
				if index >= maxListed {
					fmt.Fprintf(&section, "- and %d more waiting\n", len(waits)-maxListed)
					break
				}
				section.WriteString("- " + cutLine(fmt.Sprintf("document %s, the %s's, from conversation %s, waiting since %s (%s), for attempt %d to start as soon as a document landing frees",
					describeDocument(wait.Document), wait.Document.Owner.Title(), wait.Document.ConversationID, localMoment(wait.Since), age(now, wait.Since), wait.Attempt+1), maxLineBytes) + "\n")
			}
		}
	}
	if len(queued) > 0 {
		fmt.Fprintf(&section, "\n%d approved document(s) waiting in the merge queue to land, with their pull requests open, oldest first:\n\n", len(queued))
		for index, state := range queued {
			if index >= maxListed {
				fmt.Fprintf(&section, "- and %d more in the merge queue\n", len(queued)-maxListed)
				break
			}
			since := state.StartedAt
			if state.MergeQueue != nil && !state.MergeQueue.AdmittedAt.IsZero() {
				since = state.MergeQueue.AdmittedAt
			}
			section.WriteString("- " + cutLine(fmt.Sprintf("document %s, the %s's, run %s, in the queue to %s since %s (%s); it waits for the merge queue to land it",
				describeDocument(*state.Document), state.Document.Owner.Title(), state.RunID, queueTarget(state), localMoment(since), age(now, since)), maxLineBytes) + "\n")
		}
	}

	reading.Findings = append(reading.Findings, duplicatePublications(publications, documents)...)
	if runsRead {
		if finding, found := landingsStalled(held, landings, len(waits), now); found {
			reading.Findings = append(reading.Findings, finding)
		}
	}
	section.WriteString("\n### Where work is piling up\n\n")
	if len(reading.Findings) == 0 {
		fmt.Fprintf(&section, "None: no document has more than one open publication pull request, and the document landings are not all held for longer than %s.\n", LandingHeldTooLong)
	} else {
		section.WriteString("Each of these is added to your pass's account as a finding of its own, so you need not restate it; say what you did about each.\n\n")
		for _, finding := range reading.Findings {
			section.WriteString("- " + finding.Issue + "\n")
		}
	}
	reading.Section = section.String()
	return reading
}

// carriers is the run each open pull request carries, found by asking the
// run index for the runs of the work item its branch names, or the merge
// queues for the entry its candidate branch names, and reading that one run.
type carriers struct {
	byNumber map[int]carrier
	problems []string
}

func (r Reader) carriersFor(open []publish.OpenPullRequestStatus) carriers {
	found := carriers{byNumber: map[int]carrier{}}
	if r.Runs == nil {
		return found
	}
	itemRuns := map[string][]runstate.State{}
	var entries map[string]string
	queuesRead := false
	for _, request := range open {
		branch := request.HeadBranch
		switch {
		case strings.HasPrefix(branch, runstate.MergeQueueCandidateBranchPrefix):
			if r.Queues == nil {
				continue
			}
			if !queuesRead {
				queuesRead = true
				var err error
				if entries, err = queueEntryRuns(r.Queues); err != nil {
					found.problems = append(found.problems, fmt.Sprintf("the merge queues could not be read, so a merge queue candidate's pull request is not matched to its run: %v", err))
				}
			}
			run, ok := entries[strings.TrimPrefix(branch, runstate.MergeQueueCandidateBranchPrefix)]
			if !ok {
				continue
			}
			state, err := r.Runs.Read(run)
			if err != nil {
				found.problems = append(found.problems, fmt.Sprintf("run %s, which pull request #%d carries, could not be read: %v", run, request.Number, err))
				continue
			}
			found.byNumber[request.Number] = carrierOf(state)
		case itemFromBranch(branch) != "":
			item := itemFromBranch(branch)
			states, read := itemRuns[item]
			if !read {
				var err error
				if states, err = r.Runs.Runs(item); err != nil {
					found.problems = append(found.problems, fmt.Sprintf("the runs of %s, which pull request #%d carries, could not be read: %v", item, request.Number, err))
				}
				itemRuns[item] = states
			}
			for _, state := range states {
				if (state.PullRequest != nil && state.PullRequest.Number == request.Number) || state.Branch == branch {
					found.byNumber[request.Number] = carrierOf(state)
					break
				}
			}
		}
	}
	return found
}

// queueEntryRuns is the run each merge queue entry was admitted for, by entry.
func queueEntryRuns(queues MergeQueues) (map[string]string, error) {
	keys, err := queues.Keys()
	if err != nil {
		return nil, err
	}
	runs := map[string]string{}
	for _, key := range keys {
		entries, err := queues.Entries(key)
		if err != nil {
			return runs, err
		}
		for _, entry := range entries {
			runs[entry.EntryID] = entry.RunID
		}
	}
	return runs, nil
}

func carrierOf(state runstate.State) carrier {
	return carrier{state: state, run: state.RunID, item: state.WorkItemID, title: state.WorkItemTitle, document: state.Document}
}

func (c carriers) of(request publish.OpenPullRequestStatus) (carrier, bool) {
	found, ok := c.byNumber[request.Number]
	return found, ok
}

// itemFromBranch reads the work item out of a run branch, yoyodyne/<item>/<run>.
func itemFromBranch(branch string) string {
	if !strings.HasPrefix(branch, branchPrefix) || strings.HasPrefix(branch, runstate.MergeQueueCandidateBranchPrefix) {
		return ""
	}
	item, _, found := strings.Cut(strings.TrimPrefix(branch, branchPrefix), "/")
	if !found {
		return ""
	}
	return item
}

// mergeability is whether the forge says the request can merge, in words.
func mergeability(request publish.OpenPullRequestStatus) string {
	var said string
	switch strings.ToUpper(request.Mergeable) {
	case "MERGEABLE":
		said = "can merge without conflict"
	case "CONFLICTING":
		said = "CANNOT merge: it conflicts with its base branch"
	default:
		said = "whether it can merge is not yet known to the forge"
	}
	if request.Draft {
		said += ", and it is a draft"
	}
	if requirement := publish.MergeRequirement(request.MergeState); requirement != "" && !strings.EqualFold(request.MergeState, "UNKNOWN") {
		said += "; held from merging because " + requirement
	}
	return said
}

// waitsFor is what a run holding a document landing is doing or waiting on.
func waitsFor(state runstate.State) string {
	if wait := runinspect.WaitOf(state); wait != nil {
		return "it waits on " + wait.Kind + ", released by " + wait.Release
	}
	phase := string(state.Phase)
	if phase == "" {
		phase = string(state.Status)
	}
	return "it is at its " + phase + " step, last recorded " + localMoment(state.UpdatedAt)
}

func queueTarget(state runstate.State) string {
	if state.MergeQueue != nil && state.MergeQueue.TargetBranch != "" {
		return state.MergeQueue.TargetBranch
	}
	return "its target branch"
}

// duplicatePublications is a finding for every document the forge holds more
// than one open publication pull request for, in document order.
func duplicatePublications(publications map[string][]int, documents map[string]runstate.DocumentPublication) []sweep.Finding {
	keys := make([]string, 0, len(publications))
	for key, numbers := range publications {
		if len(numbers) > 1 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var findings []sweep.Finding
	for _, key := range keys {
		numbers := append([]int(nil), publications[key]...)
		sort.Ints(numbers)
		listed := make([]string, len(numbers))
		for i, number := range numbers {
			listed[i] = fmt.Sprintf("#%d", number)
		}
		document := documents[key]
		findings = append(findings, sweep.Finding{
			Issue: cutLine(fmt.Sprintf("document %s, the %s's, has %d open publication pull requests at once: %s",
				describeDocument(document), document.Owner.Title(), len(numbers), strings.Join(listed, ", ")), sweep.MaxTextBytes),
			Disposition: sweep.DispositionLeft,
			Detail:      "noticed by the harness's own reading of the forge and the run records on this pass; the harness closes none of them, and only one of them can land the document",
		})
	}
	return findings
}

// landingsStalled is a finding where every document landing is held and has
// been for longer than LandingHeldTooLong, naming each role whose documents
// hold them.
func landingsStalled(held []runstate.State, landings, waiting int, now time.Time) (sweep.Finding, bool) {
	if len(held) < landings || len(held) == 0 {
		return sweep.Finding{}, false
	}
	for _, state := range held {
		if now.Sub(state.StartedAt) <= LandingHeldTooLong {
			return sweep.Finding{}, false
		}
	}
	byRole := map[domain.AgentRole][]string{}
	var roles []domain.AgentRole
	for _, state := range held {
		owner := state.Document.Owner
		if _, seen := byRole[owner]; !seen {
			roles = append(roles, owner)
		}
		byRole[owner] = append(byRole[owner], fmt.Sprintf("%s in run %s since %s", describeDocument(*state.Document), state.RunID, localMoment(state.StartedAt)))
	}
	parts := make([]string, 0, len(roles))
	for _, role := range roles {
		parts = append(parts, fmt.Sprintf("the %s's %s", role.Title(), strings.Join(byRole[role], "; ")))
	}
	issue := fmt.Sprintf("every document landing (%d of %d) has been held for longer than %s, so no other confirmed document can start landing: %s; %d document(s) wait behind them",
		len(held), landings, LandingHeldTooLong, strings.Join(parts, "; and "), waiting)
	return sweep.Finding{
		Issue:       cutLine(issue, sweep.MaxTextBytes),
		Disposition: sweep.DispositionLeft,
		Detail:      "noticed by the harness's own reading of the run records on this pass; the harness frees no landing itself",
	}, true
}

func localMoment(moment time.Time) string {
	return moment.Local().Format("2006-01-02 15:04 MST")
}

// age is how long ago a moment was, in hours and minutes.
func age(now, since time.Time) string {
	if since.IsZero() {
		return "for an unknown time"
	}
	elapsed := now.Sub(since)
	if elapsed < 0 {
		return "stamped ahead of this reading"
	}
	hours := int(elapsed / time.Hour)
	minutes := int((elapsed % time.Hour) / time.Minute)
	switch {
	case hours >= 48:
		return fmt.Sprintf("%d days %dh", hours/24, hours%24)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// cutLine bounds a line on a character boundary, saying it was cut.
func cutLine(line string, bound int) string {
	line = strings.Join(strings.Fields(line), " ")
	if len(line) <= bound {
		return line
	}
	const marker = " [cut here]"
	end := bound - len(marker)
	for end > 0 && !utf8.RuneStart(line[end]) {
		end--
	}
	return line[:end] + marker
}
