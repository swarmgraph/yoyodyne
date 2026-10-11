package orchestrator

// The delivery loop as a workflow definition: the sequences this build ships,
// where a project keeps its own copy of one instead, the authority they are
// compiled under, what each registered step can answer with, and the one call
// that turns a file into a graph.
//
// It is the third door onto the same steps and it opens no wider than the
// second. `actions.go` registered the pipeline's steps under selectable names;
// this selects among those names and decides where each outcome goes, which is
// the whole of what a definition can do. Nothing here decides what a step is,
// what it requires, or whether it may be performed -- the registry declares the
// first two and the grant below answers the third, and a definition that
// selected an action this build does not register, or one requiring authority
// the grant does not confer, is refused before an instance exists.
//
// Nothing in the delivery path performs anything through them. `Pipeline.Run`
// still runs the sequence Go control flow puts it in; these definitions are
// compiled and walked by the parity harness in parity_test.go, which holds them
// against the recorded baseline of what the hard-coded path actually does, and
// stepped beside every real run by the declarative path in declarative.go, whose
// doors perform nothing. Making them the thing that runs a real work item needs
// what the design calls for and none of it is here: the step-attempt record, the
// idempotency key, and the reconciliation that make an action with a side effect
// safe to re-perform after a death.

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/workflow"
)

// The workflows this build ships, by the name an instance records it under. The
// two are one pipeline read under its two integration policies: what a project
// configured decides which of them a run would be bound to, which is a binding
// rather than a transition, because a definition has no way to ask what was
// configured.
const (
	// DeliveryWorkflowID is the loop an automatic project runs: the gate, the
	// promotion, and the cleanup after it.
	DeliveryWorkflowID = "delivery"
	// HumanApprovalWorkflowID is the same loop for a project whose integration a
	// person still approves, which stops after the checks.
	HumanApprovalWorkflowID = "delivery-human-approval"
)

// ProjectDefinitionPath is where a project's own copy of one delivery
// definition lives, relative to its Yoyodyne configuration directory. A project
// that wants a different sequence copies the built-in file there and edits it.
//
// Nothing is merged between the two. A project that writes one owns the whole
// sequence from then on, which is the only arrangement where reading the file
// tells somebody what its runs actually execute; the digest an instance pins
// says which of the two it was.
func ProjectDefinitionPath(id string) string {
	return "workflows/" + id + ".yaml"
}

//go:embed delivery.yaml
var deliveryDefinition []byte

//go:embed delivery-human-approval.yaml
var humanApprovalDefinition []byte

// deliveryGrant is the authority a delivery workflow is compiled and performed
// under.
//
// It is written out rather than derived from what the registered actions happen
// to require, and the difference is the whole reason it exists: a grant taken
// from the registry confers whatever the registry asks for and can therefore
// never refuse anything, which is a check that reports its own input back. This
// one is a statement of what the harness holds when it delivers, so an action
// that gains a capability nobody meant it to have is refused at compile against
// a list somebody has to change on purpose.
func deliveryGrant() (workflow.Grant, error) {
	return workflow.NewGrant(
		capability.WorkItemRead,
		capability.WorkItemMutate,
		capability.RepositoryRead,
		capability.WorktreeMutate,
		capability.TargetBranchMutate,
		capability.PromotionLease,
		capability.ProviderInvoke,
		capability.ChecksExecute,
		capability.ForgePublish,
		capability.RunStateMutate,
		capability.ReviewVerdict,
	)
}

// deliveryAnswers is what each registered step of the delivery loop can produce,
// in sorted order.
//
// A registered action declares its capabilities and not yet its outcomes, so
// `internal/workflow` cannot ask whether a definition routes outcomes the action
// it selected can actually return -- its own Validate says as much. This package
// can, because these are its own steps and it knows what each of them can end
// in. Holding a definition to it at compile is what keeps an outcome from being
// a string a definition invented: a transition on an outcome nothing produces is
// a branch that is never taken, and it reads exactly like coverage.
//
// The lists are what the run distinguishes rather than what the Go function
// returns. A budget is the run's own counter and no action reports one, so a
// failure handed back to the developer and the same failure with the budget
// spent are two outcomes here -- which is the only way a definition can say that
// a spent budget goes somewhere else.
var deliveryAnswers = map[string][]string{
	"work-item.claim":   {"claimed", "unavailable"},
	"candidate.develop": {"produced", "reissued", "relaunches-spent", "stopped"},
	"candidate.check":   {"failed", "failed-unrepaired", "passed", "refused", "refused-unrepaired", "unrunnable"},
	"candidate.review":  {"approved", "changes-requested", "stopped", "unresolved"},
	// "reconciling" is a replay conflict handed back to the developer that wrote
	// the change, which re-earns the whole gate from the developer's own state.
	// The built-in routes it to `develop`. A project's own copy made before that
	// transition existed does not, so a run executing such a copy records a
	// divergence when it takes it, and `yoyo config drift` reports the transition
	// as available to that project (see DeliveryDefinitionDrift).
	"candidate.integrate": {"conflicted", "contended", "integrated", "reconciling", "superseded"},
	// One outcome, because the run distinguishes nothing here: a promotion whose
	// merge the forge only queued leaves the item open for a later sweep to close,
	// and the run goes on to exactly the same next step either way. Where the two
	// integration policies part is which definition selects this state, not what
	// it answers with.
	"run.complete": {"completed"},
	"run.clean-up": {"cleaned", "partial"},
}

// deliverySequence is one delivery definition in force: the name it answers to,
// the bytes it was read from, and where those came from.
type deliverySequence struct {
	// ID is the workflow the definition is expected to declare, checked against
	// what it actually declares when it is compiled.
	ID string
	// Definition is the file's bytes — embedded for the one this build ships, so
	// that a checkout is not a prerequisite for compiling it, and read from disk
	// for a project's own.
	Definition []byte
	// Source is what a refusal calls the file, so a message read without this
	// package in front of somebody still says which file to open.
	Source string
	// Project is whether the project wrote this definition itself rather than its
	// being the one this build ships. It is what decides the cost of a defect: a
	// project's own file is one somebody wrote on purpose and can fix, so a run
	// that would have executed a broken one stops before it claims anything,
	// while the shipped file being wrong is a defect in the build that the parity
	// harness answers for and is no reason to stop delivering work.
	Project bool
}

// builtinDeliveryWorkflows is every delivery sequence this build ships.
func builtinDeliveryWorkflows() []deliverySequence {
	return []deliverySequence{
		{ID: DeliveryWorkflowID, Definition: deliveryDefinition, Source: "delivery.yaml"},
		{ID: HumanApprovalWorkflowID, Definition: humanApprovalDefinition, Source: "delivery-human-approval.yaml"},
	}
}

// deliverySequenceFor is the definition a run of one workflow executes: the
// project's own copy where it keeps one, otherwise the one this build ships.
//
// A project file that exists and cannot be read is refused rather than passed
// over. Falling back to the built-in there would execute a sequence nobody
// chose, under a name the project has already used for something else, which is
// exactly the silence the location exists to end.
func deliverySequenceFor(id, configPath string) (deliverySequence, error) {
	shipped, found := deliverySequence{}, false
	for _, builtin := range builtinDeliveryWorkflows() {
		if builtin.ID == id {
			shipped, found = builtin, true
		}
	}
	if !found {
		return deliverySequence{}, fmt.Errorf("no built-in definition is shipped as %q", id)
	}
	path := projectDefinitionPath(id, configPath)
	if path == "" {
		return shipped, nil
	}
	definition, err := os.ReadFile(path)
	if err != nil {
		return deliverySequence{}, projectDefinitionRefusal{fmt.Errorf("read workflow definition: %w", err)}
	}
	return deliverySequence{ID: id, Definition: definition, Source: path, Project: true}, nil
}

// projectDefinitionPath is where this project keeps its own copy of one
// definition, and "" where it keeps none.
//
// The directories are the ones a configuration file keeps everything else it
// owns in — its personas first among them — so a project has one place its files
// live rather than a rule per kind of file. A pipeline that does not know where
// its configuration is has no project to read one from.
func projectDefinitionPath(id, configPath string) string {
	if configPath == "" {
		return ""
	}
	for _, directory := range config.ConfigurationDirectories(configPath) {
		candidate := filepath.Join(directory, filepath.FromSlash(ProjectDefinitionPath(id)))
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

// projectDefinitionRefusal is a defect in a definition the project wrote itself.
//
// It is a type rather than a message because it is the one thing wrong with an
// observation that stops a run. Everything else that can prevent a run being
// observed leaves the run alone — a missing instance store, a build whose own
// definitions do not compile — because observing delivery is never a reason
// delivery does not happen. A file the project wrote is the other case: the
// project asked for that sequence by writing it, so executing something else
// instead, or quietly executing nothing, would be worse than refusing while
// refusing is still free.
type projectDefinitionRefusal struct{ err error }

func (r projectDefinitionRefusal) Error() string { return r.err.Error() }

func (r projectDefinitionRefusal) Unwrap() error { return r.err }

// refuse marks what is wrong with a definition as the project's own where the
// project is what wrote it, which is what decides whether a run stops for it.
func (s deliverySequence) refuse(err error) error {
	if !s.Project {
		return err
	}
	return projectDefinitionRefusal{err}
}

// compileDelivery turns one definition into the graph an executor would walk, or
// refuses the whole of it.
//
// Everything it can refuse, it refuses here: a schema this build does not read,
// a state selecting an action nothing registers, a transition to a destination
// that does not exist, a step requiring authority the grant does not confer, a
// topology the separation policies will not admit, a file answering to another
// name, and an outcome no step produces. That ordering is the loader's point --
// a definition that cannot run is refused while refusing it still costs nothing.
func compileDelivery(sequence deliverySequence) (workflow.Graph[*activeRun], error) {
	registry, err := deliveryRegistry()
	if err != nil {
		// The registry is a literal table in this package, so this is a defect in
		// the build rather than anything wrong with the definition being read.
		return workflow.Graph[*activeRun]{}, fmt.Errorf("the delivery steps this build registers are not a registry: %w", err)
	}
	return compileDeliveryWith(sequence, registry)
}

// compileDeliveryWith is the same compile over a registry the caller supplies,
// which is the whole of what the observation needs that the production compile
// does not: the same definition, the same grant, the same refusals, and doors
// that perform nothing.
func compileDeliveryWith(sequence deliverySequence, registry action.Registry[*activeRun]) (workflow.Graph[*activeRun], error) {
	grant, err := deliveryGrant()
	if err != nil {
		return workflow.Graph[*activeRun]{}, fmt.Errorf("the authority a delivery run holds could not be stated: %w", err)
	}
	loader := workflow.Loader[*activeRun]{Registry: registry, Grant: grant}
	graph, err := loader.Load(bytes.NewReader(sequence.Definition))
	if err != nil {
		return workflow.Graph[*activeRun]{}, fmt.Errorf("%s: %w", sequence.Source, err)
	}
	// A definition that validated and compiled and answers to another name is not
	// the one that was asked for. Nothing downstream would notice: it would run
	// whatever it said perfectly well, under this grant, as the workflow somebody
	// meant to bind.
	if graph.ID() != sequence.ID {
		return workflow.Graph[*activeRun]{}, fmt.Errorf("%s declares the workflow %q; it is bound as %q", sequence.Source, graph.ID(), sequence.ID)
	}
	if err := routesOnlyOutcomesTheStepsProduce(sequence, graph); err != nil {
		return workflow.Graph[*activeRun]{}, err
	}
	return graph, nil
}

// routesOnlyOutcomesTheStepsProduce refuses a definition that routes an outcome
// the step it selected cannot produce.
//
// It is a subset rather than an equality, and the asymmetry is deliberate. An
// outcome nothing produces is a transition nothing ever takes, which is worth
// refusing because it reads as a case somebody covered. An outcome a step can
// produce and this definition does not route is a different thing: the
// human-approval sequence has no repair loop, so neither of the two budget-spent
// outcomes can arise in it, and demanding it route them would be demanding a
// destination for something that cannot happen. What the run would do with an
// unrouted outcome is stop where it stands and say so, which is the executor's
// answer rather than a defect in the file.
//
// Everything wrong is reported together, over the graph's sorted states, so one
// definition refused twice is refused identically.
func routesOnlyOutcomesTheStepsProduce(sequence deliverySequence, graph workflow.Graph[*activeRun]) error {
	var problems []error
	for _, state := range graph.States() {
		node, _ := graph.Node(state)
		answers, declared := deliveryAnswers[node.Action().Name]
		if !declared {
			// The registry and the table above are both literals in this package, so
			// this is a defect in the build rather than in the file being read.
			problems = append(problems, fmt.Errorf("the state %q selects %q, whose outcomes this build does not declare", state, node.Action().Name))
			continue
		}
		for _, routed := range node.Outcomes() {
			if slices.Contains(answers, routed) {
				continue
			}
			problems = append(problems, fmt.Errorf("the state %q routes the outcome %q, which %q never produces; it answers with %s",
				state, routed, node.Action().Name, strings.Join(answers, ", ")))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s: %w", sequence.Source, errors.Join(problems...))
	}
	return nil
}
