// Package releasecut is the harness cutting a release the Lead Product Manager
// decided: the release step of the supervisor's maintenance pass, under
// approvals.releases: automatic.
//
// The authority is the release-tag ruling in docs/designs/v1-harness-design.md
// (The release tag): the decision is the Lead Product Manager's recorded act,
// the release item she admitted; the notes and their readiness stamp land
// through that item's own reviewed run; and the cut is this pass and nothing
// else — never an agent, a conversation, or a developer run. So this prepares
// nothing and writes nothing to main. It finds a decided release whose tag is
// not on origin, starts from the commit origin's default branch holds, runs
// every gate a person's cut runs (scripts/cut-release.sh, in its automatic
// mode, in a worktree of its own at that commit), and pushes the tag under the
// harness's identity. Notes that are missing, older than the decision, or
// carrying a stale stamp refuse the cut and hand the release back to the Lead
// Product Manager, once for each version of the notes.
//
// Interrupted publication follows the architect's ruling on release-tag
// recovery (Settle interrupted release-tag publication, yoyodyne-ifd.436.2.1):
// the cut records its intent — version, commit, the stamp's verdict and
// definition and the notes' digest, the gates, who cut — before any tag
// exists, and creating, pushing and confirming the tag are each safe to
// repeat from that record. A later pass reads one of five states and acts on
// it: no tag anywhere (the same intent is tried again while origin's default
// branch is still at its commit; otherwise it is stale, and the next cut runs
// the gates again), a local tag origin does not have (pushed), origin holding
// the tag on the intent's commit after a push nobody saw land (adopted),
// origin or this repository holding the tag on some other commit (held for
// the Lead Product Manager to choose a new version, with nothing moved or
// deleted), and origin not answering (waited out, counted). The third pass
// that cannot confirm the push holds the cut for her too.
//
// What it does not decide is retried on the machinery that already exists:
// the pass's own cadence is the only retry interval, a commit whose gates were
// red is not cut again until origin's default branch moves, and the operator's
// pause stops it while the intake hold does not, because a cut selects no work.
package releasecut

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// StepName is the step's name on the maintenance pass's record.
const StepName = "release"

const (
	// CutTimeout bounds the gates: the adoption walkthrough, make check, and the
	// release build, which a person's cut runs in about forty minutes.
	CutTimeout = 2 * time.Hour
	// gitTimeout bounds one Git command, and pushTimeout one that reaches the
	// remote.
	gitTimeout  = 2 * time.Minute
	pushTimeout = 5 * time.Minute
	// MaxUnconfirmedTurns is how many passes may try to publish a tag without
	// reading it back from origin before the cut is held for the Lead Product
	// Manager, per the recovery ruling.
	MaxUnconfirmedTurns = 3
	// preparationExit is how scripts/cut-release.sh says, in its automatic
	// mode, that the notes or their stamp are not ready: the release item's run
	// has preparing still to do.
	preparationExit = 3
	maxDetailBytes  = 1 << 10
)

// The gates a person's cut holds, in the order scripts/cut-release.sh runs
// them, which a cut's intent records as passed.
var gates = []string{
	"HEAD at origin's default branch, with a clean tree",
	"the release's notes",
	"release readiness, matching the notes' stamp",
	"the adoption walkthrough",
	"make check",
	"the release build",
}

// Tracker is the work the cut reads and the release item it writes on. It is
// satisfied by beads.Client, and its only write appends a note: the pass
// writes no tracker status.
type Tracker interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
	RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error)
}

// Cuts is the record of the cuts. It is satisfied by *runstate.ReleaseCutStore.
type Cuts interface {
	List() ([]runstate.ReleaseCut, error)
	Put(runstate.ReleaseCut) error
}

// Reports is the collected pile the channel reads. It is satisfied by
// *runstate.ReportStore.
type Reports interface {
	Append(report.Report) error
}

// Holds is the operator's pause. It is satisfied by *runstate.OperatorHoldStore.
type Holds interface {
	Held() (runstate.OperatorHold, bool, error)
}

// Cutter cuts the decided releases of one product.
type Cutter struct {
	Product      domain.ProductID
	RepositoryID string
	// Build is the harness revision the cut runs under, for the reports it
	// files.
	Build string
	// Mode is approvals.releases.
	Mode domain.ApprovalMode
	// Repository is the primary checkout, whose refs the tag is written into.
	Repository string
	// Remote is execution.remote.
	Remote string
	// WorkRoot is where the cut's own worktree is made, outside the
	// repository and outside the run worktrees.
	WorkRoot string
	// Environ is what the gates are run with.
	Environ []string
	// CutBy says who cut, on the intent: the pass and its process.
	CutBy  string
	Runner execution.ProcessRunner

	Tracker Tracker
	Cuts    Cuts
	Reports Reports
	Holds   Holds
	Now     func() time.Time
}

// Pass is the release step of one maintenance pass.
func (c *Cutter) Pass(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepName}
	if c.Mode != domain.ApprovalAutomatic {
		return skipped(step, "approvals.releases is human, so a release the Lead Product Manager decided is cut by a person with make release")
	}
	if c.Runner == nil || c.Tracker == nil || c.Cuts == nil {
		return failed(step, "the release cut is not wired with a process runner, the tracker, and its record, so nothing is cut")
	}
	cuts, err := c.Cuts.List()
	if err != nil {
		return failed(step, fmt.Sprintf("the record of release cuts could not be read, so nothing is cut: %v", err))
	}
	owed := c.announceOwed(ctx, cuts)
	if c.Holds != nil {
		hold, held, err := c.Holds.Held()
		if err != nil {
			return failed(step, prefixed(owed, fmt.Sprintf("whether the operator's pause is placed could not be read, so nothing is cut: %v", err)))
		}
		if held {
			return skipped(step, prefixed(owed, fmt.Sprintf("the operator's pause has been placed since %s, so no release is cut while it stands", local(hold.HeldAt))))
		}
	}
	for _, cut := range cuts {
		if cut.State == runstate.ReleaseCutIntent {
			outcome := c.recover(ctx, cut)
			outcome.Detail = prefixed(owed, outcome.Detail)
			return outcome
		}
	}
	outcome := c.next(ctx, cuts)
	outcome.Detail = prefixed(owed, outcome.Detail)
	return outcome
}

// decided is one release the Lead Product Manager decided.
type decided struct {
	item    beads.WorkItem
	version string
	order   [3]int
}

// releaseTitle is how a release item names its version: "Cut v0.6.0: ...", as
// every release item the Lead Product Manager's sweep admits is titled.
var releaseTitle = regexp.MustCompile(`^Cut (?:release )?(v[0-9]+\.[0-9]+\.[0-9]+)\b`)

// Decided is the releases the Lead Product Manager has decided and nobody has
// closed, lowest version first: an unfinished item titled for a version, that
// she admitted.
func Decided(items []beads.WorkItem) []beads.WorkItem {
	var found []decided
	for _, item := range items {
		if version, ok := decidedVersion(item); ok {
			found = append(found, decided{item: item, version: version, order: semver(version)})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		for k := range found[i].order {
			if found[i].order[k] != found[j].order[k] {
				return found[i].order[k] < found[j].order[k]
			}
		}
		return false
	})
	releases := make([]beads.WorkItem, 0, len(found))
	for _, release := range found {
		releases = append(releases, release.item)
	}
	return releases
}

func decidedVersion(item beads.WorkItem) (string, bool) {
	if item.Status == "closed" || item.Origin.AdmittedBy != domain.RoleProductManager {
		return "", false
	}
	match := releaseTitle.FindStringSubmatch(strings.TrimSpace(item.Title))
	if match == nil {
		return "", false
	}
	return match[1], true
}

func semver(version string) [3]int {
	var parts [3]int
	for index, part := range strings.SplitN(strings.TrimPrefix(version, "v"), ".", 3) {
		parts[index], _ = strconv.Atoi(part)
	}
	return parts
}

// next finds the release to cut and cuts it.
func (c *Cutter) next(ctx context.Context, cuts []runstate.ReleaseCut) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepName}
	items, err := c.Tracker.List(ctx, "")
	if err != nil {
		return failed(step, fmt.Sprintf("the tracker could not be read, so no decided release was looked for: %v", err))
	}
	releases := Decided(items)
	if len(releases) == 0 {
		return skipped(step, "no release the Lead Product Manager decided is waiting to be cut")
	}
	// One a day is the release schedule the operator delegated, as the Lead
	// Product Manager's sweep is told it ("Never cut more than one a day") and
	// as a release item's own done condition states it. The design's ruling
	// has the harness cut under that schedule, so the cut holds to it too
	// rather than relying on the admissions alone.
	for _, cut := range cuts {
		if cut.State == runstate.ReleaseCutPushed && sameDay(cut.UpdatedAt, c.now()) {
			return skipped(step, fmt.Sprintf("%s was cut today, at %s, and no more than one release is cut a day", cut.Version, local(cut.UpdatedAt)))
		}
	}
	var passedOver []string
	for _, item := range releases {
		version, _ := decidedVersion(item)
		if reason := standing(cuts, version); reason != "" {
			passedOver = append(passedOver, version+" "+reason)
			continue
		}
		commit, present, err := c.remoteTag(ctx, version)
		if err != nil {
			return skipped(step, fmt.Sprintf("whether origin holds %s could not be read, so it is not cut on this pass: %v", version, err))
		}
		if present {
			passedOver = append(passedOver, fmt.Sprintf("%s is already on origin, at %s", version, short(commit)))
			continue
		}
		local, err := c.localTag(ctx, version)
		if err != nil {
			return failed(step, fmt.Sprintf("whether this repository holds %s could not be read: %v", version, err))
		}
		if local != "" {
			passedOver = append(passedOver, fmt.Sprintf("%s is tagged in this repository at %s but not on origin, by a cut that was not the harness's, so it is left for whoever made it", version, short(local)))
			continue
		}
		return c.cut(ctx, cuts, item, version)
	}
	return skipped(step, "nothing to cut: "+strings.Join(passedOver, "; "))
}

// standing says why a version is not cut again, where its record says one.
func standing(cuts []runstate.ReleaseCut, version string) string {
	for _, cut := range cuts {
		if cut.Version != version {
			continue
		}
		switch cut.State {
		case runstate.ReleaseCutPushed:
			return fmt.Sprintf("was pushed by the harness at %s, and waits on the Lead Product Manager to close its item", local(cut.UpdatedAt))
		case runstate.ReleaseCutBlocked:
			return "is held for the Lead Product Manager to choose a new version: " + cut.Detail
		}
	}
	return ""
}

// cut runs the gates on what origin's default branch holds and, where they
// pass, records the intent and publishes the tag.
func (c *Cutter) cut(ctx context.Context, cuts []runstate.ReleaseCut, item beads.WorkItem, version string) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepName}
	branch := c.defaultBranch(ctx)
	commit, err := c.remoteHead(ctx, branch)
	if err != nil {
		return skipped(step, fmt.Sprintf("where origin/%s is could not be read, so %s is not cut on this pass: %v", branch, version, err))
	}
	for _, cut := range cuts {
		if cut.Version == version && cut.Commit == commit && cut.State != runstate.ReleaseCutStale {
			return skipped(step, fmt.Sprintf("%s was refused at %s, which origin/%s still holds, and is tried again once it moves: %s", version, short(commit), branch, cut.Detail))
		}
	}
	notes := "docs/releases/" + version + ".md"
	refused := runstate.ReleaseCut{
		SchemaVersion: runstate.ReleaseCutSchemaVersion, ProductID: c.Product, Version: version,
		WorkItemID: item.ID, Commit: commit, State: runstate.ReleaseCutRefused, CutBy: c.CutBy,
		RecordedAt: c.now().UTC(), UpdatedAt: c.now().UTC(),
	}
	digest, present, err := c.blob(ctx, commit, notes)
	if err != nil {
		return failed(step, fmt.Sprintf("whether %s is on origin/%s could not be read: %v", notes, branch, err))
	}
	if !present {
		return c.handBack(ctx, cuts, refused, "", fmt.Sprintf("%s is not on origin/%s at %s", notes, branch, short(commit)))
	}
	if !item.CreatedAt.IsZero() {
		landed, err := c.committedAt(ctx, commit, notes)
		if err != nil {
			return failed(step, fmt.Sprintf("when %s last changed could not be read: %v", notes, err))
		}
		if landed.Before(item.CreatedAt) {
			return c.handBack(ctx, cuts, refused, digest, fmt.Sprintf("%s on origin/%s last changed %s, before the release was decided at %s, so it is not this release's preparation",
				notes, branch, local(landed), local(item.CreatedAt)))
		}
	}

	result, err := c.gates(ctx, version, commit)
	if err != nil {
		return failed(step, fmt.Sprintf("the gates for %s at %s could not be run: %v", version, short(commit), err))
	}
	said := lastWords(result)
	switch {
	case result.Status == execution.ProcessFailed && result.ExitCode == preparationExit:
		return c.handBack(ctx, cuts, refused, digest, said)
	case result.Status != execution.ProcessSucceeded:
		refused.Detail = runstate.BoundReleaseCutText(fmt.Sprintf("the gates for %s at %s were red, so no tag was written: %s", version, short(commit), said))
		// A red gate goes on the release item, once for each thing it says, and
		// not to the channel: it is the pass's to try again, not anybody's to act on.
		refused.Reported = true
		refused.Noted = notedBefore(cuts, version, func(cut runstate.ReleaseCut) bool { return !cut.Preparation && sameRefusal(cut.Detail, refused.Detail) })
		return c.record(ctx, refused, runstate.StepRan, refused.Detail)
	}
	checked := checkedCommit(result.Stdout)
	if checked != commit {
		return failed(step, fmt.Sprintf("the gates for %s passed on %q, not on %s, which origin/%s held when the cut started; nothing was tagged", version, checked, short(commit), branch))
	}
	verdict, definition, err := c.stamp(ctx, commit, notes)
	if err != nil {
		return failed(step, fmt.Sprintf("the readiness stamp in %s at %s could not be read after its gate passed: %v", notes, short(commit), err))
	}
	intent := refused
	intent.State = runstate.ReleaseCutIntent
	intent.ReadinessVerdict, intent.ReadinessDefinition, intent.NotesDigest = verdict, definition, digest
	intent.Gates = append([]string(nil), gates...)
	intent.Detail = fmt.Sprintf("every gate passed on %s; the tag has not been written yet", short(commit))
	if err := c.Cuts.Put(intent); err != nil {
		return failed(step, fmt.Sprintf("the gates for %s passed, but the intent to cut it could not be recorded, so no tag was written: %v", version, err))
	}
	return c.publish(ctx, intent)
}

// recover finishes a cut whose intent is recorded, from the state the tag is
// found in.
func (c *Cutter) recover(ctx context.Context, intent runstate.ReleaseCut) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepName}
	remote, present, err := c.remoteTag(ctx, intent.Version)
	if err != nil {
		return c.unconfirmed(ctx, intent, fmt.Sprintf("origin could not be read for %s: %v", intent.Version, err))
	}
	if present && remote == intent.Commit {
		return c.confirmed(ctx, intent, fmt.Sprintf("origin already holds %s on %s, so the push an earlier pass made landed", intent.Version, short(intent.Commit)))
	}
	if present {
		return c.block(ctx, intent, fmt.Sprintf("origin holds %s on %s, not on %s, which the cut checked", intent.Version, short(remote), short(intent.Commit)))
	}
	local, err := c.localTag(ctx, intent.Version)
	if err != nil {
		return failed(step, fmt.Sprintf("whether this repository holds %s could not be read: %v", intent.Version, err))
	}
	if local == "" {
		// No tag anywhere: the gates still describe what would be tagged only
		// while origin's default branch has not moved off the commit they ran on.
		branch := c.defaultBranch(ctx)
		head, err := c.remoteHead(ctx, branch)
		if err != nil {
			return c.unconfirmed(ctx, intent, fmt.Sprintf("where origin/%s is could not be read: %v", branch, err))
		}
		if head != intent.Commit {
			intent.State = runstate.ReleaseCutStale
			intent.Detail = runstate.BoundReleaseCutText(fmt.Sprintf("no tag was written for %s at %s, and origin/%s has moved on to %s, so the gates no longer describe what would be tagged; the next pass cuts it afresh",
				intent.Version, short(intent.Commit), branch, short(head)))
			intent.UpdatedAt = c.now().UTC()
			return c.record(ctx, intent, runstate.StepRan, intent.Detail)
		}
	}
	return c.publish(ctx, intent)
}

// publish writes the tag where it is not, pushes it, and reads it back. Each
// step is safe to repeat.
func (c *Cutter) publish(ctx context.Context, intent runstate.ReleaseCut) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepName}
	local, err := c.localTag(ctx, intent.Version)
	if err != nil {
		return failed(step, fmt.Sprintf("whether this repository holds %s could not be read: %v", intent.Version, err))
	}
	switch {
	case local == "":
		if err := c.tag(ctx, intent.Version, intent.Commit); err != nil {
			return c.unconfirmed(ctx, intent, fmt.Sprintf("the tag %s could not be written on %s: %v", intent.Version, short(intent.Commit), err))
		}
	case local != intent.Commit:
		return c.block(ctx, intent, fmt.Sprintf("this repository holds %s on %s, not on %s, which the cut checked", intent.Version, short(local), short(intent.Commit)))
	}
	pushErr := c.push(ctx, intent.Version)
	remote, present, err := c.remoteTag(ctx, intent.Version)
	switch {
	case err != nil:
		return c.unconfirmed(ctx, intent, fmt.Sprintf("%s was pushed%s, and origin could not be read back to confirm it: %v", intent.Version, failure(pushErr), err))
	case present && remote == intent.Commit:
		return c.confirmed(ctx, intent, fmt.Sprintf("pushed %s on %s", intent.Version, short(intent.Commit)))
	case present:
		return c.block(ctx, intent, fmt.Sprintf("origin holds %s on %s, not on %s, which the cut checked", intent.Version, short(remote), short(intent.Commit)))
	default:
		return c.unconfirmed(ctx, intent, fmt.Sprintf("%s was pushed%s, and origin does not hold it", intent.Version, failure(pushErr)))
	}
}

// unconfirmed counts one more pass that could not confirm the tag, and holds
// the cut for the Lead Product Manager at the bound.
func (c *Cutter) unconfirmed(ctx context.Context, intent runstate.ReleaseCut, why string) runstate.SweepStep {
	intent.UnconfirmedTurns++
	if intent.UnconfirmedTurns >= MaxUnconfirmedTurns {
		return c.block(ctx, intent, fmt.Sprintf("%d passes could not confirm %s on origin; the last said: %s", intent.UnconfirmedTurns, intent.Version, why))
	}
	intent.Detail = runstate.BoundReleaseCutText(fmt.Sprintf("%s; tried again on the next pass (%d of %d)", why, intent.UnconfirmedTurns, MaxUnconfirmedTurns))
	intent.UpdatedAt = c.now().UTC()
	return c.record(ctx, intent, runstate.StepRan, intent.Detail)
}

// block holds the cut for the Lead Product Manager. Nothing is moved or
// deleted: a tag of the version on another commit is somebody's, and choosing
// a new version is hers.
func (c *Cutter) block(ctx context.Context, intent runstate.ReleaseCut, why string) runstate.SweepStep {
	intent.State = runstate.ReleaseCutBlocked
	intent.Detail = runstate.BoundReleaseCutText(why + "; nothing was moved or deleted, and the release waits on the Lead Product Manager to choose a new version")
	intent.UpdatedAt = c.now().UTC()
	intent.Noted, intent.Reported = false, false
	return c.announced(ctx, intent)
}

// confirmed records the tag on origin and says so.
func (c *Cutter) confirmed(ctx context.Context, intent runstate.ReleaseCut, why string) runstate.SweepStep {
	intent.State = runstate.ReleaseCutPushed
	intent.Detail = runstate.BoundReleaseCutText(why)
	intent.UpdatedAt = c.now().UTC()
	intent.Noted, intent.Reported = false, false
	return c.announced(ctx, intent)
}

// handBack refuses a cut over its preparation and returns the release to the
// Lead Product Manager, once for each version of the notes.
func (c *Cutter) handBack(ctx context.Context, cuts []runstate.ReleaseCut, refused runstate.ReleaseCut, digest, why string) runstate.SweepStep {
	refused.Preparation = true
	refused.NotesDigest = digest
	refused.Detail = runstate.BoundReleaseCutText(fmt.Sprintf("%s was not cut and nothing was written: %s", refused.Version, why))
	told := notedBefore(cuts, refused.Version, func(cut runstate.ReleaseCut) bool {
		return cut.Preparation && cut.NotesDigest == digest && cut.Reported
	})
	refused.Noted, refused.Reported = told, told
	if told {
		return c.record(ctx, refused, runstate.StepRan, refused.Detail+"; the Lead Product Manager was already handed these notes back")
	}
	return c.announced(ctx, refused)
}

// announced records a cut and writes its note and report.
func (c *Cutter) announced(ctx context.Context, cut runstate.ReleaseCut) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepName}
	if err := c.Cuts.Put(cut); err != nil {
		return failed(step, fmt.Sprintf("%s; and that could not be recorded: %v", cut.Detail, err))
	}
	cut, problem := c.announce(ctx, cut)
	if problem != "" {
		return failed(step, cut.Detail+"; "+problem)
	}
	return ran(step, cut.Detail)
}

// record stores a cut and reports it as the step's outcome.
func (c *Cutter) record(ctx context.Context, cut runstate.ReleaseCut, outcome runstate.SweepStepOutcome, detail string) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepName}
	if err := c.Cuts.Put(cut); err != nil {
		return failed(step, fmt.Sprintf("%s; and that could not be recorded: %v", detail, err))
	}
	// A refusal goes on the release item; an intent still being published, or
	// one gone stale, is the pass's own business and says nothing until it
	// settles.
	if cut.State == runstate.ReleaseCutRefused && (!cut.Noted || !cut.Reported) {
		if _, problem := c.announce(ctx, cut); problem != "" {
			return failed(step, detail+"; "+problem)
		}
	}
	step.Outcome, step.Detail = outcome, detail
	return step
}

// announceOwed writes what earlier passes recorded and could not say.
func (c *Cutter) announceOwed(ctx context.Context, cuts []runstate.ReleaseCut) string {
	var problems []string
	for _, cut := range cuts {
		if cut.State == runstate.ReleaseCutIntent || cut.State == runstate.ReleaseCutStale || (cut.Noted && cut.Reported) {
			continue
		}
		if _, problem := c.announce(ctx, cut); problem != "" {
			problems = append(problems, problem)
		}
	}
	return strings.Join(problems, "; ")
}

// announce writes a cut's note on its release item and its report, each once,
// and records which were written.
func (c *Cutter) announce(ctx context.Context, cut runstate.ReleaseCut) (runstate.ReleaseCut, string) {
	var problems []string
	if !cut.Noted {
		if _, err := c.Tracker.RecordOutcome(ctx, cut.WorkItemID, c.note(cut)); err != nil {
			problems = append(problems, fmt.Sprintf("the note on %s could not be written, and is tried again on the next pass: %v", cut.WorkItemID, err))
		} else {
			cut.Noted = true
		}
	}
	if !cut.Reported {
		if err := c.report(cut); err != nil {
			problems = append(problems, fmt.Sprintf("the report could not be filed, and is tried again on the next pass: %v", err))
		} else {
			cut.Reported = true
		}
	}
	if err := c.Cuts.Put(cut); err != nil {
		problems = append(problems, fmt.Sprintf("what was said about %s could not be recorded: %v", cut.Version, err))
	}
	return cut, strings.Join(problems, "; ")
}

// note is what the release item is told.
func (c *Cutter) note(cut runstate.ReleaseCut) string {
	switch {
	case cut.State == runstate.ReleaseCutPushed:
		return fmt.Sprintf("The harness cut %s and pushed its tag to %s at %s, under its own identity, on commit %s. Every gate a person's cut runs passed on that commit first: %s. The notes' readiness stamp ended in %s, pinned to %s, and matched a fresh reading. Cut by %s. This is the evidence this item's done condition asks for; the Lead Product Manager closes it.",
			cut.Version, c.remote(), local(cut.UpdatedAt), cut.Commit, strings.Join(cut.Gates, "; "), orNone(cut.ReadinessVerdict), orNone(cut.ReadinessDefinition), orNone(cut.CutBy))
	case cut.State == runstate.ReleaseCutBlocked:
		return fmt.Sprintf("The harness did not finish cutting %s, and the release is held for the Lead Product Manager: %s.", cut.Version, cut.Detail)
	case cut.Preparation:
		return fmt.Sprintf("The harness did not cut %s, because its preparation is not on main: %s. This release's reviewed run drafts and curates docs/releases/%s.md, stamps it with yoyo conformance --notes, and lands both through review; the harness then cuts it on its next maintenance pass. It is handed back to the Lead Product Manager for that run.",
			cut.Version, cut.Detail, cut.Version)
	default:
		return fmt.Sprintf("The harness did not cut %s: %s. It tries again once main moves past %s.", cut.Version, cut.Detail, short(cut.Commit))
	}
}

// report files the warning the channel says.
func (c *Cutter) report(cut runstate.ReleaseCut) error {
	if c.Reports == nil {
		return errors.New("no report pile is wired")
	}
	var message string
	switch {
	case cut.State == runstate.ReleaseCutPushed:
		message = fmt.Sprintf("The harness cut %s and pushed its tag to %s on %s, after every release gate passed. The release workflow publishes it from that tag, and the Lead Product Manager closes %s on this evidence.",
			cut.Version, c.remote(), short(cut.Commit), cut.WorkItemID)
	case cut.State == runstate.ReleaseCutBlocked:
		message = fmt.Sprintf("The harness stopped cutting %s: %s. The Lead Product Manager has to choose what happens to the release on %s.",
			cut.Version, cut.Detail, cut.WorkItemID)
	default:
		message = fmt.Sprintf("The harness did not cut %s: %s. The release item %s needs its reviewed run to land the notes and their readiness stamp before the harness can cut it.",
			cut.Version, cut.Detail, cut.WorkItemID)
	}
	id, err := report.NewID()
	if err != nil {
		return err
	}
	return c.Reports.Append(report.Report{
		SchemaVersion: report.SchemaVersion, ID: id, Role: report.HarnessReporter,
		RunID: "release-cut:" + cut.Key(), WorkItemID: cut.WorkItemID, Build: c.Build,
		ProductID: c.Product, RepositoryID: c.RepositoryID, Severity: report.SeverityWarning,
		Message: oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)), RecordedAt: c.now().UTC(),
	})
}

// gates runs scripts/cut-release.sh in its automatic mode, in a worktree of
// the cut's own detached at commit. The worktree is removed afterwards however
// the gates ended.
func (c *Cutter) gates(ctx context.Context, version, commit string) (execution.ProcessResult, error) {
	if err := os.MkdirAll(c.WorkRoot, 0o700); err != nil {
		return execution.ProcessResult{}, fmt.Errorf("create %s: %w", c.WorkRoot, err)
	}
	path := filepath.Join(c.WorkRoot, version)
	// A worktree an interrupted cut left is removed before this one is made; it
	// holds nothing but a checkout of a commit and what the gates built.
	_, _ = c.git(ctx, c.Repository, gitTimeout, "worktree", "remove", "--force", path)
	_, _ = c.git(ctx, c.Repository, gitTimeout, "worktree", "prune")
	if _, err := os.Stat(path); err == nil {
		return execution.ProcessResult{}, fmt.Errorf("%s is in the way and is not a worktree Git could remove; remove it and the next pass cuts", path)
	}
	if _, err := c.git(ctx, c.Repository, gitTimeout, "worktree", "add", "--detach", path, commit); err != nil {
		return execution.ProcessResult{}, err
	}
	defer func() {
		cleanup := context.WithoutCancel(ctx)
		_, _ = c.git(cleanup, c.Repository, gitTimeout, "worktree", "remove", "--force", path)
	}()
	environ := execution.ForgeEnvironment(c.Environ)
	environ = append(environ,
		"YOYODYNE_RELEASE_CUT=automatic",
		// The release-readiness gate reads the work items through bd, whose
		// store is the primary checkout's.
		"BEADS_DIR="+filepath.Join(c.Repository, ".beads"),
	)
	return c.Runner.Run(ctx, execution.Command{
		Name:    "bash",
		Args:    []string{filepath.Join(path, "scripts", "cut-release.sh"), version},
		Dir:     path,
		Env:     environ,
		Timeout: CutTimeout,
	}, nil)
}

func (c *Cutter) tag(ctx context.Context, version, commit string) error {
	name, email := gitworktree.HarnessIdentity()
	_, err := c.run(ctx, c.Repository, gitTimeout, append(execution.GitEnvironment(nil),
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email),
		"git", "-c", "user.name="+name, "-c", "user.email="+email, "tag", "-a", version, "-m", version, commit)
	return err
}

func (c *Cutter) push(ctx context.Context, version string) error {
	ref := "refs/tags/" + version
	_, err := c.run(ctx, c.Repository, pushTimeout, execution.ForgeEnvironment(nil), "git", "push", "--quiet", c.remote(), ref+":"+ref)
	return err
}

// remoteTag reads the commit origin's tag of a version names, and whether it
// has one at all.
func (c *Cutter) remoteTag(ctx context.Context, version string) (string, bool, error) {
	ref := "refs/tags/" + version
	out, err := c.run(ctx, c.Repository, pushTimeout, execution.ForgeEnvironment(nil), "git", "ls-remote", c.remote(), ref, ref+"^{}")
	if err != nil {
		return "", false, err
	}
	var direct, peeled string
	for _, line := range strings.Split(out, "\n") {
		object, name, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found {
			continue
		}
		switch name {
		case ref:
			direct = object
		case ref + "^{}":
			peeled = object
		}
	}
	if peeled != "" {
		return peeled, true, nil
	}
	return direct, direct != "", nil
}

// remoteHead reads the commit origin's branch is at, and fetches it so it is
// here to check out.
func (c *Cutter) remoteHead(ctx context.Context, branch string) (string, error) {
	ref := "refs/heads/" + branch
	out, err := c.run(ctx, c.Repository, pushTimeout, execution.ForgeEnvironment(nil), "git", "ls-remote", c.remote(), ref)
	if err != nil {
		return "", err
	}
	object, _, found := strings.Cut(strings.TrimSpace(out), "\t")
	if !found || object == "" {
		return "", fmt.Errorf("%s has no branch %s", c.remote(), branch)
	}
	if _, err := c.run(ctx, c.Repository, pushTimeout, execution.ForgeEnvironment(nil), "git", "fetch", "--quiet", c.remote(), ref); err != nil {
		return "", err
	}
	if _, err := c.git(ctx, c.Repository, gitTimeout, "cat-file", "-e", object+"^{commit}"); err != nil {
		return "", fmt.Errorf("origin/%s is at %s, which the fetch did not bring here", branch, object)
	}
	return object, nil
}

func (c *Cutter) defaultBranch(ctx context.Context) string {
	out, err := c.git(ctx, c.Repository, gitTimeout, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+c.remote()+"/HEAD")
	if branch := strings.TrimPrefix(strings.TrimSpace(out), c.remote()+"/"); err == nil && branch != "" {
		return branch
	}
	return "main"
}

func (c *Cutter) localTag(ctx context.Context, version string) (string, error) {
	result, err := c.Runner.Run(ctx, execution.Command{
		Name: "git", Args: []string{"rev-parse", "-q", "--verify", "refs/tags/" + version + "^{commit}"},
		Dir: c.Repository, Env: execution.GitEnvironment(nil), Timeout: gitTimeout,
	}, nil)
	if err != nil {
		return "", err
	}
	switch {
	case result.Status == execution.ProcessSucceeded:
		return strings.TrimSpace(result.Stdout), nil
	case result.Status == execution.ProcessFailed && result.ExitCode == 1 && strings.TrimSpace(result.Stderr) == "":
		return "", nil
	}
	return "", fmt.Errorf("git rev-parse %s", describe(result))
}

// blob reads the Git object name of a file at a commit, and whether it is
// there.
func (c *Cutter) blob(ctx context.Context, commit, path string) (string, bool, error) {
	result, err := c.Runner.Run(ctx, execution.Command{
		Name: "git", Args: []string{"rev-parse", "-q", "--verify", commit + ":" + path},
		Dir: c.Repository, Env: execution.GitEnvironment(nil), Timeout: gitTimeout,
	}, nil)
	if err != nil {
		return "", false, err
	}
	if result.Status == execution.ProcessSucceeded {
		return strings.TrimSpace(result.Stdout), true, nil
	}
	if result.Status == execution.ProcessFailed {
		return "", false, nil
	}
	return "", false, fmt.Errorf("git rev-parse %s", describe(result))
}

func (c *Cutter) committedAt(ctx context.Context, commit, path string) (time.Time, error) {
	out, err := c.git(ctx, c.Repository, gitTimeout, "log", "-1", "--format=%cI", commit, "--", path)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(out))
}

// The two lines of a readiness section the cut's gate compares, as
// internal/cli/conformance.go renders them and scripts/cut-release.sh reads
// them.
var (
	stampVerdict    = regexp.MustCompile("ended in \\*\\*([^*]+)\\*\\*")
	stampDefinition = regexp.MustCompile("Pinned to `([^`]+)`")
)

const (
	readinessBegin = "<!-- yoyodyne:release-readiness -->"
	readinessEnd   = "<!-- /yoyodyne:release-readiness -->"
)

// stamp reads the verdict and definition the notes' readiness stamp records.
func (c *Cutter) stamp(ctx context.Context, commit, path string) (string, string, error) {
	notes, err := c.git(ctx, c.Repository, gitTimeout, "show", commit+":"+path)
	if err != nil {
		return "", "", err
	}
	return Stamp(notes)
}

// Stamp reads the verdict and definition a release's notes record in their
// readiness section.
func Stamp(notes string) (string, string, error) {
	start := strings.Index(notes, readinessBegin)
	if start == -1 {
		return "", "", errors.New("the notes carry no readiness section")
	}
	stop := strings.Index(notes[start:], readinessEnd)
	if stop == -1 {
		return "", "", errors.New("the notes open a readiness section and never close it")
	}
	section := notes[start : start+stop]
	verdict := stampVerdict.FindStringSubmatch(section)
	if verdict == nil {
		return "", "", errors.New("the readiness section carries no verdict")
	}
	definition := ""
	if pinned := stampDefinition.FindStringSubmatch(section); pinned != nil {
		definition = pinned[1]
	}
	return verdict[1], definition, nil
}

var checkedLine = regexp.MustCompile(`(?m)^checked commit: ([0-9a-f]{40,64})$`)

func checkedCommit(stdout string) string {
	if match := checkedLine.FindStringSubmatch(stdout); match != nil {
		return match[1]
	}
	return ""
}

// git runs one Git command in dir and returns its output, or an error saying
// how it ended.
func (c *Cutter) git(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	return c.run(ctx, dir, timeout, execution.GitEnvironment(nil), "git", args...)
}

func (c *Cutter) run(ctx context.Context, dir string, timeout time.Duration, env []string, name string, args ...string) (string, error) {
	result, err := c.Runner.Run(ctx, execution.Command{Name: name, Args: args, Dir: dir, Env: env, Timeout: timeout}, nil)
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return result.Stdout, fmt.Errorf("%s %s %s", name, firstWord(args), describe(result))
	}
	return result.Stdout, nil
}

func (c *Cutter) remote() string {
	if c.Remote != "" {
		return c.Remote
	}
	return "origin"
}

func (c *Cutter) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// notedBefore reports whether an earlier record of the version already said
// what this one would.
func notedBefore(cuts []runstate.ReleaseCut, version string, same func(runstate.ReleaseCut) bool) bool {
	for _, cut := range cuts {
		if cut.Version == version && cut.State == runstate.ReleaseCutRefused && cut.Noted && same(cut) {
			return true
		}
	}
	return false
}

// sameRefusal compares two refusals by what they say after the commit, so the
// same red gate at two commits is said once.
func sameRefusal(a, b string) bool {
	_, afterA, _ := strings.Cut(a, ": ")
	_, afterB, _ := strings.Cut(b, ": ")
	return afterA == afterB
}

func describe(result execution.ProcessResult) string {
	said := strings.TrimSpace(result.Stderr)
	if said == "" {
		said = strings.TrimSpace(result.Stdout)
	}
	ended := fmt.Sprintf("ended %s, exit %d", result.Status, result.ExitCode)
	if said == "" {
		return ended
	}
	return ended + ": " + oneline.Fold(said, maxDetailBytes)
}

// lastWords is how the gates ended in a line: the script's own last refusal
// where it made one, and otherwise the end of what it said.
func lastWords(result execution.ProcessResult) string {
	for _, stream := range []string{result.Stderr, result.Stdout} {
		lines := strings.Split(strings.TrimSpace(stream), "\n")
		for index := len(lines) - 1; index >= 0; index-- {
			if line := strings.TrimSpace(lines[index]); strings.HasPrefix(line, "cut-release: ") {
				return strings.TrimPrefix(line, "cut-release: ")
			}
		}
	}
	return describe(result)
}

func failure(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf(" (the push said: %v)", err)
}

func firstWord(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func prefixed(owed, detail string) string {
	if owed == "" {
		return detail
	}
	return detail + "; and " + owed
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.In(time.Local).Date()
	by, bm, bd := b.In(time.Local).Date()
	return ay == by && am == bm && ad == bd
}

// local is a moment in the operator's local time, with the zone named.
func local(at time.Time) string { return at.In(time.Local).Format("2006-01-02 15:04 MST") }

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func orNone(text string) string {
	if strings.TrimSpace(text) == "" {
		return "nothing recorded"
	}
	return text
}

func skipped(step runstate.SweepStep, detail string) runstate.SweepStep {
	step.Outcome, step.Detail = runstate.StepSkipped, detail
	return step
}

func ran(step runstate.SweepStep, detail string) runstate.SweepStep {
	step.Outcome, step.Detail = runstate.StepRan, detail
	return step
}

func failed(step runstate.SweepStep, detail string) runstate.SweepStep {
	step.Outcome, step.Detail = runstate.StepFailed, detail
	return step
}
