// Package queuemode chooses, before a change is admitted to a merge queue,
// whether the harness runs that queue itself or hands the change to the
// forge's own, and admits it in the mode chosen. The design is
// docs/designs/integration-through-a-merge-queue.md, "Forge-native queue".
//
// The forge's queue is chosen only where its adapter establishes, and the
// forge enforces before landing, every requirement publish.QueueRequirements
// lists. A queue that exists, or checks that are green on a pull request's
// head, prove none of them. Anything short of that is the harness's queue,
// with the reason recorded; and where the target's protection lets neither
// queue land into it, nothing is admitted and the caller is given a hold that
// names what is missing and who can supply it.
//
// The mode is chosen once per entry. An entry already admitted keeps the mode
// and evidence it was admitted on, however the forge answers later: moving an
// entry between modes first withdraws it from the old one, and that is a
// later slice's explicit transfer, never a side effect of asking again.
//
// Nothing here enqueues, merges, rewrites a head, or moves an entry between
// modes. The pipeline admits through it when execution.merge_queue is on, and
// an explicit transfer chooses the other mode through Select.
package queuemode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// MaxObservationAge bounds how long before admission the forge may have been
// observed. The mode is chosen from what the forge says now; an older answer
// is asked again rather than trusted.
const MaxObservationAge = 5 * time.Minute

// maxExplanationBytes keeps an explanation inside what an entry records
// (runstate.MaxMergeQueueTextBytes), with room to spare.
const maxExplanationBytes = 1500

// Harness is what the selector is told about the project's own way of landing
// changes.
type Harness struct {
	// PullRequests is the project landing into its target through pull
	// requests the harness opens, which is the only way the harness's queue
	// lands into a protected target.
	PullRequests bool
	// CheckConfiguration identifies the project's configured checks and their
	// configuration. The forge's required checks qualify only where the
	// adapter reports this same configuration.
	CheckConfiguration string
}

// Hold is an admission neither queue can carry. Requirement says, in ordinary
// words, what the target's protection needs that neither queue supplies;
// Mover is who can supply it, resolved by internal/ownership; Step is what
// they would do. PersonOnly is set where the step is a repository setting.
type Hold struct {
	Requirement string
	Mover       ownership.Mover
	Step        string
	PersonOnly  *ownership.PersonOnlyRemedy
}

// Selection is the mode chosen and the evidence it was chosen on, or a hold.
// Mode is empty on a hold.
type Selection struct {
	Mode     runstate.MergeQueueMode
	Hold     *Hold
	Evidence runstate.MergeQueueModeEvidence
}

// Select chooses the mode for a change into branch from what the forge was
// observed to establish. An observation of another branch, one with no time,
// or one older than MaxObservationAge is no evidence at all, and is refused
// rather than chosen from.
func Select(branch string, observed publish.QueueCapabilities, harness Harness, now time.Time) (Selection, error) {
	if observed.TargetBranch != branch {
		return Selection{}, fmt.Errorf("the forge's answer is about %q, not %q, and the merge queue mode is not chosen from it", observed.TargetBranch, branch)
	}
	if observed.ObservedAt.IsZero() {
		return Selection{}, fmt.Errorf("the forge's answer about %s does not say when it was observed", branch)
	}
	if age := now.Sub(observed.ObservedAt); age > MaxObservationAge || age < -MaxObservationAge {
		return Selection{}, fmt.Errorf("the forge's answer about %s was observed at %s, not within %s of admission, and is asked again rather than trusted",
			branch, observed.ObservedAt.UTC().Format(time.RFC3339), MaxObservationAge)
	}
	unmet, reasons := nativeShortfall(observed, harness)
	evidence := runstate.MergeQueueModeEvidence{
		Forge: observed.Forge, ObservedAt: observed.ObservedAt.UTC(), TargetProtected: observed.Protected,
		ForgeQueue: observed.QueueAvailable, TimeoutMinutes: observed.TimeoutMinutes, Unmet: unmet,
	}
	if len(unmet) == 0 {
		evidence.Explanation = fmt.Sprintf("The forge's merge queue for %s establishes and enforces everything the harness's queue gates on, and waits %d minutes for checks.",
			branch, observed.TimeoutMinutes)
		return Selection{Mode: runstate.MergeQueueForge, Evidence: evidence}, nil
	}
	cannot := "The forge's merge queue cannot be used for " + branch + ": " + strings.Join(reasons, "; ") + "."
	if hold := harnessCannotLand(branch, observed, harness); hold != nil {
		hold.Requirement = oneline.Fold(hold.Requirement+" "+cannot, maxExplanationBytes)
		evidence.Explanation = hold.Requirement
		return Selection{Hold: hold, Evidence: evidence}, nil
	}
	evidence.Explanation = oneline.Fold(cannot+" The harness runs the queue itself.", maxExplanationBytes)
	return Selection{Mode: runstate.MergeQueueHarness, Evidence: evidence}, nil
}

// nativeShortfall is every requirement the forge's queue does not establish
// and enforce, by name, and the reasons in ordinary words.
func nativeShortfall(observed publish.QueueCapabilities, harness Harness) ([]string, []string) {
	var unmet, reasons []string
	all := publish.QueueRequirements()
	if !observed.QueueAvailable {
		for _, requirement := range all {
			unmet = append(unmet, string(requirement))
		}
		if observed.Forge == "" {
			return unmet, []string{"the project has no forge that can say whether it has one"}
		}
		return unmet, []string{"the forge has none for it"}
	}
	for _, requirement := range all {
		if reason := shortfall(requirement, observed, harness); reason != "" {
			unmet = append(unmet, string(requirement))
			reasons = append(reasons, reason)
		}
	}
	return unmet, reasons
}

// shortfall is why the forge's queue falls short of one requirement, or ""
// where it meets it.
func shortfall(requirement publish.QueueRequirement, observed publish.QueueCapabilities, harness Harness) string {
	var found []publish.QueueRequirementEvidence
	for _, evidence := range observed.Requirements {
		if evidence.Requirement == requirement {
			found = append(found, evidence)
		}
	}
	says := requirement.Says()
	switch {
	case len(found) == 0:
		return "nothing says it can establish " + says
	case len(found) > 1:
		return "its adapter answered twice about " + says
	}
	evidence := found[0]
	switch {
	case !evidence.Established || !evidence.Enforced:
		if missing := strings.TrimSpace(evidence.Missing); missing != "" {
			return missing
		}
		if !evidence.Established {
			return "it cannot say " + says
		}
		return "it lands a change without knowing " + says
	case (requirement == publish.QueueRequiredChecks || requirement == publish.QueueIndependentApproval) &&
		evidence.BoundTo != publish.BoundToCandidate:
		return "its evidence of " + says + " is about the pull request's head, not the combined commit it lands"
	case requirement == publish.QueueRequiredChecks &&
		(harness.CheckConfiguration == "" || evidence.Configuration != harness.CheckConfiguration):
		return "the checks it requires are not the project's configured checks as they are configured now"
	case requirement == publish.QueueTimeoutPolicy && observed.TimeoutMinutes <= 0:
		return "it did not say " + says
	}
	return ""
}

// harnessCannotLand is the hold for a target the harness's own queue cannot
// land into as it is protected, or nil where it can. The harness lands into
// an unprotected target by its existing promotion, and into a protected one
// through its pull request; it never bypasses protection to do either.
func harnessCannotLand(branch string, observed publish.QueueCapabilities, harness Harness) *Hold {
	switch {
	case observed.QueueRequired:
		// The forge's queue builds and lands a combined commit of its own, so a
		// candidate the harness checked and reviewed would not be what landed.
		remedy := &ownership.PersonOnlyRemedy{
			Reason: ownership.PersonRepositorySetting,
			Target: "the merge queue requirement on " + branch,
			Step:   "remove the merge queue requirement from " + branch + "'s protection rules in the repository's settings, or leave execution.merge_queue off so changes integrate as they do now",
		}
		return &Hold{
			Requirement: branch + " lands changes only through the forge's merge queue, which lands a commit of its own rather than the one the harness checked and reviewed.",
			Mover:       ownership.ResolveQueueModeHold(remedy), Step: remedy.Step, PersonOnly: remedy,
		}
	case observed.Protected && !harness.PullRequests:
		return &Hold{
			Requirement: branch + " is protected and the project does not publish through pull requests, so the harness's queue has no permitted way to land into it.",
			Mover:       ownership.ResolveQueueModeHold(nil),
			Step:        "decide whether the project publishes into " + branch + " through pull requests, or integrates without the merge queue",
		}
	}
	return nil
}

// Admitter admits approved changes to their merge queue in the mode chosen
// for them.
type Admitter struct {
	Queue runstate.MergeQueueAdmitter
	// Forge is the adapter asked what its merge queue establishes, and nil for
	// a project with no forge that can say.
	Forge   publish.QueueCapabilityReader
	Harness Harness
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Admitted is what one admission did. Entry is the queue's entry for the run
// and New says whether this call wrote it. Hold is set, and Entry empty, where
// nothing was admitted because neither queue can land into the target.
type Admitted struct {
	Entry runstate.MergeQueueEntry
	New   bool
	Hold  *Hold
	// Evidence is what a hold was decided on.
	Evidence runstate.MergeQueueModeEvidence
}

// Admit admits one approved change. The caller's Mode and ModeEvidence are
// replaced by the selector's.
//
// The queue is read before anything else, so an admission whose earlier save
// may or may not have landed is found before a second is written. A run
// already in the queue is answered with its entry as recorded — its mode, and
// the evidence that mode was chosen on — without asking the forge again: a
// changed answer moves no entry and grants it no new way to land.
func (a Admitter) Admit(ctx context.Context, admission runstate.MergeQueueAdmission) (Admitted, error) {
	if a.Queue == nil {
		return Admitted{}, errors.New("merge queue admission needs a queue")
	}
	existing, found, err := a.existing(admission)
	if err != nil {
		return Admitted{}, err
	}
	if found {
		return a.admitAsRecorded(ctx, admission, existing)
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	branch := admission.Key.TargetBranch
	observed := publish.QueueCapabilities{TargetBranch: branch, ObservedAt: now()}
	if a.Forge != nil {
		var err error
		observed, err = a.Forge.QueueCapabilities(ctx, branch)
		if err != nil {
			return Admitted{}, fmt.Errorf("learn what the forge's merge queue establishes for %s; nothing was admitted: %w", branch, err)
		}
	}
	selection, err := Select(branch, observed, a.Harness, now())
	if err != nil {
		return Admitted{}, fmt.Errorf("choose the merge queue mode for %s; nothing was admitted: %w", branch, err)
	}
	if selection.Hold != nil {
		return Admitted{Hold: selection.Hold, Evidence: selection.Evidence}, nil
	}
	admission.Mode, admission.ModeEvidence = selection.Mode, selection.Evidence
	entry, admitted, err := a.Queue.Admit(ctx, admission)
	var conflict runstate.MergeQueueConflictError
	if errors.As(err, &conflict) {
		// Another admission of this run landed between the read above and this
		// write, perhaps in the mode the forge was answering then. Whether it
		// is this admission is decided against its own mode.
		return a.admitAsRecorded(ctx, admission, conflict.Admitted)
	}
	if err != nil {
		return Admitted{}, err
	}
	return Admitted{Entry: entry, New: admitted}, nil
}

// admitAsRecorded asks the queue for the admission in the mode the run's
// entry already records, which answers that entry where the admission is the
// same one and refuses it with runstate.MergeQueueConflictError where anything
// else — another head among them — differs. It writes nothing either way.
func (a Admitter) admitAsRecorded(ctx context.Context, admission runstate.MergeQueueAdmission, recorded runstate.MergeQueueEntry) (Admitted, error) {
	admission.Mode, admission.ModeEvidence = recorded.Mode, recorded.ModeEvidence
	entry, _, err := a.Queue.Admit(ctx, admission)
	if err != nil {
		return Admitted{}, err
	}
	return Admitted{Entry: entry}, nil
}

// existing is the queue's entry for the admission's run, if the queue still
// holds one. A run whose entry was handed back or released has none: its next
// admission is a new entry, and its mode is chosen afresh.
func (a Admitter) existing(admission runstate.MergeQueueAdmission) (runstate.MergeQueueEntry, bool, error) {
	entry, found, err := a.Queue.Standing(admission.Key, admission.RunID)
	if err != nil {
		return runstate.MergeQueueEntry{}, false, fmt.Errorf("read the merge queue for %s before admitting to it: %w", admission.Key.TargetBranch, err)
	}
	return entry, found, nil
}
