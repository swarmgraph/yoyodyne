package orchestrator

// The delivery pipeline's steps, named and registered.
//
// This is a second door onto functions the pipeline already calls, and nothing
// yet walks through it to perform anything. The declarative path in
// declarative.go steps copies of these actions whose Perform does nothing, so
// what it exercises is the topology and the authority they declare rather than
// the code behind them. Run still calls claim and develop directly; develop
// calls publishAttempt on its way out of an attempt; and verifyReviewAndFinish,
// the repair loop and finish call verify, reviewChange, integrate, complete and
// cleanUp between them — all in the order Go control flow puts them in, and this
// file changes none of that. What it adds is that each of
// those steps now has a name a workflow definition could select, and a statement
// in code of what performing it requires — which is the half of "configuration
// selects sequence, code grants capability" that has to exist before any
// sequence can be read out of a file.
//
// It is deliberately not a dispatcher, and it is deliberately not a second
// implementation. Every Perform below is one call to the function the pipeline
// itself calls, with nothing in between: if the two ever disagree it is because
// somebody changed the function, and both doors change together. Choosing when
// to walk through one, under which lease, with which parameters, and what to do
// with what comes back is the runtime's, and it waits.

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// deliveryStep is one step of the delivery pipeline: the registered action that
// is a second door onto it, and the run phases a run occupies while it runs.
//
// The phases are here rather than on the action because they are this pipeline's
// bookkeeping rather than anything an action means in general — and because they
// are what the repository test holds this registry's coverage against. A run's
// phase is already the durable record of "the step a run reached", so a later
// step that a run can actually be found in is one that has a phase, and a phase
// no registered step occupies fails that test rather than passing quietly.
type deliveryStep struct {
	action action.Action[*activeRun]
	// phases are the run phases this step occupies — the phases a run is in while
	// this step is the one being performed, rather than every phase the step
	// writes. Two steps can name the same phase: publishing happens inside the
	// developing phase, because a pull request is opened for the attempt that just
	// finished. A phase a step only moves the run into on its way out belongs to
	// whatever runs next — promoting a change ends by recording the completing
	// phase, and the step performed in it is run.complete. What must not happen is
	// a phase no step names at all.
	phases []runstate.Phase
}

// deliverySteps is every step the delivery pipeline executes today.
//
// The order is the order Run reaches them in, which is documentation rather than
// sequence: nothing here decides what runs when, and the first thing that does
// will read a definition instead.
func deliverySteps() []deliveryStep {
	return []deliveryStep{
		{
			action: action.Action[*activeRun]{
				Name:    "work-item.claim",
				Summary: "take the work item this run was dispatched for and assemble the context its developer is given",
				Wraps:   "(*activeRun).claim",
				Capabilities: []capability.Capability{
					capability.WorkItemRead,
					capability.WorkItemMutate,
					capability.RepositoryRead,
				},
				Perform: func(ctx context.Context, a *activeRun) error { return a.claim(ctx) },
			},
			// Claiming happens before the run is developing anything, so it occupies
			// no phase: a run interrupted inside it is a run that had not started,
			// which is what the pending status already says.
			phases: nil,
		},
		{
			action: action.Action[*activeRun]{
				Name:    "candidate.develop",
				Summary: "invoke the developer against this run's worktree until it produces a change or the run stops trying",
				Wraps:   "(*activeRun).develop",
				// The forge is here because develop ends by calling publishAttempt, so
				// going through this door pushes the run branch and opens or updates the
				// pull request exactly as candidate.publish does. A declaration that named
				// only what the function's own body reaches would understate the authority
				// this action actually needs, which is the one thing the registry exists
				// to state truthfully. The worktree write is both the commit every
				// invocation's work is recorded in and the publishing one above it; the
				// repository read is the change summary recordDevelopment takes of what
				// the invocation produced.
				Capabilities: []capability.Capability{
					capability.ProviderInvoke,
					capability.RepositoryRead,
					capability.WorktreeMutate,
					capability.ForgePublish,
					capability.RunStateMutate,
				},
				// The prompt and the session are exactly what Run hands it for a fresh
				// attempt. A repair is the same function with the findings in the prompt
				// and the developer's session resumed, which is the same action with
				// different parameters — and parameters are the runtime's, so this door
				// opens onto the first attempt and the pipeline still makes the rest.
				Perform: func(ctx context.Context, a *activeRun) error {
					return a.develop(ctx, developerPrompt(a.pipeline.developer().Persona.Text, a.deliveredInvariants().Text(), a.context, a.scratch, a.pipeline.Config.Checks), "")
				},
			},
			phases: []runstate.Phase{runstate.PhaseDeveloping},
		},
		{
			action: action.Action[*activeRun]{
				Name:    "candidate.publish",
				Summary: "push the run branch the attempt was committed on, and open or update its pull request",
				Wraps:   "(*activeRun).publishAttempt",
				Capabilities: []capability.Capability{
					capability.WorktreeMutate,
					capability.ForgePublish,
					capability.RunStateMutate,
				},
				Perform: func(ctx context.Context, a *activeRun) error { return a.publishAttempt(ctx) },
			},
			// Publishing is part of the developer's phase rather than a phase of its
			// own: it happens on the way out of an attempt, so a run interrupted in it
			// is a run interrupted developing.
			phases: []runstate.Phase{runstate.PhaseDeveloping},
		},
		{
			action: action.Action[*activeRun]{
				Name:    "candidate.check",
				Summary: "refuse a change that touched paths this work item does not grant, that is too large for the reviewer's copy, or that its developer ran nothing against, then run the project's configured checks over it",
				Wraps:   "(*activeRun).verify",
				Capabilities: []capability.Capability{
					capability.RepositoryRead,
					capability.ChecksExecute,
					capability.RunStateMutate,
				},
				Perform: func(ctx context.Context, a *activeRun) error { return a.verify(ctx) },
			},
			phases: []runstate.Phase{runstate.PhaseChecking},
		},
		{
			action: action.Action[*activeRun]{
				Name:    "candidate.review",
				Summary: "obtain one independent verdict on the change from a reviewer with read-only access",
				Wraps:   "(*activeRun).reviewChange",
				Capabilities: []capability.Capability{
					capability.RepositoryRead,
					capability.ProviderInvoke,
					capability.RunStateMutate,
					// The verdict is what performing this produces, and naming it is what
					// makes the separation policies askable of any sequence that selects
					// this step: `internal/separation` reads a topology in capabilities, so
					// a review step that did not declare the verdict would be one no rule
					// could tell from a step that merely reads the change and invokes a
					// provider. It is the reviewer's alone, and this action is where the
					// harness exercises it on the reviewer's behalf.
					capability.ReviewVerdict,
				},
				// The verdict itself is not returned through this door, and nothing is
				// lost by that: reviewChange records the decision on the run's durable
				// state and on its outcome before it returns, so the answer is read from
				// the run rather than from the call. What the door cannot yet carry is a
				// typed outcome for a definition to branch on, which is what the runtime
				// adds when actions gain outcomes.
				Perform: func(ctx context.Context, a *activeRun) error {
					_, err := a.reviewChange(ctx)
					return err
				},
			},
			phases: []runstate.Phase{runstate.PhaseReviewing},
		},
		{
			action: action.Action[*activeRun]{
				Name:    "candidate.integrate",
				Summary: "take the target branch's promotion lease, promote the approved change onto it, and settle the publication that carried it",
				Wraps:   "(*activeRun).integrate",
				Capabilities: []capability.Capability{
					capability.PromotionLease,
					capability.TargetBranchMutate,
					capability.ForgePublish,
					capability.RunStateMutate,
				},
				// One action rather than four. Taking the lease, checking where the
				// remote stands, moving the branch and merging the request are the
				// promotion, and a definition that could order them separately could
				// order the branch moved without the lease — which is the race the lease
				// exists to stop. Security-sensitive internals are not assembled from
				// individually optional pieces.
				Perform: func(ctx context.Context, a *activeRun) error { return a.integrate(ctx) },
			},
			// Promoting ends by recording the completing phase, and the step performed
			// in it is the next one rather than this one.
			phases: []runstate.Phase{runstate.PhaseIntegrating},
		},
		{
			action: action.Action[*activeRun]{
				Name:    "run.complete",
				Summary: "record on the work item what this run produced, settle it once its promotion is -- closed where the run's landing discharges the item, back in the backlog where it lands evidence instead -- and price what it spent",
				Wraps:   "(*activeRun).complete",
				Capabilities: []capability.Capability{
					// The tracker write is the whole of what makes this a delivery step
					// rather than bookkeeping: the outcome is recorded on the item and an
					// integrated item is settled against it. Pricing writes what the run cost
					// against the same item, which is why nothing else appears here.
					capability.WorkItemMutate,
					capability.RunStateMutate,
				},
				// The outcome is not returned through this door for the same reason
				// candidate.review's verdict is not: what the run produced is already on
				// its durable state and on its outcome before this returns, so the answer
				// is read from the run rather than from the call.
				Perform: func(ctx context.Context, a *activeRun) error {
					_, err := a.complete(ctx)
					return err
				},
			},
			phases: []runstate.Phase{runstate.PhaseCompleting},
		},
		{
			action: action.Action[*activeRun]{
				Name:    "run.clean-up",
				Summary: "remove the worktree and branch this run created, once its change is proven to be somewhere else, and record the run as complete once nothing it made is left",
				Wraps:   "(*activeRun).cleanUp",
				Capabilities: []capability.Capability{
					capability.WorktreeMutate,
					capability.RunStateMutate,
				},
				Perform: func(ctx context.Context, a *activeRun) error { return a.cleanUp(ctx) },
			},
			phases: []runstate.Phase{runstate.PhaseCleaningUp},
		},
	}
}

// deliveryRegistry is the registry built from those steps.
//
// It returns an error rather than panicking or building lazily because the
// refusals are the point: a duplicate name or a capability nothing declares is a
// defect in the table above, and it is found the moment anything asks for the
// registry — which, until dispatch exists, is this package's own tests.
func deliveryRegistry() (action.Registry[*activeRun], error) {
	steps := deliverySteps()
	actions := make([]action.Action[*activeRun], 0, len(steps))
	for _, step := range steps {
		actions = append(actions, step.action)
	}
	return action.New(actions...)
}
