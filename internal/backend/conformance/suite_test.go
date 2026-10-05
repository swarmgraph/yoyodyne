package conformance

// The suite itself: the conditions every adapter is asked about, what each of
// them must leave the harness able to do, and the machinery that puts one
// provider's own words through the adapter that reads them. What the package is
// for is in doc.go; the adapters' words are in adapters_test.go.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	compiled "github.com/mason-bryant/yoyodyne/internal/backend/adapters"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Condition is one thing a provider can do to an invocation, named in the terms
// the harness reasons in rather than in any provider's own.
//
// The six here are the ones a wrong answer costs something. Capacity and the
// model are the two refusals the harness has an answer to — a window to wait for
// and another selector to ask — so reading either as anything else throws the
// answer away. A refusal that judges the work stands however often it is asked,
// so reading it as weather spends a run's whole relaunch budget on an answer
// that cannot change. A network failure is the reverse: it judged nothing and
// names no condition to wait for, so reading it as a verdict fails a whole run
// on weather, which is what a person spent a week reconciling by hand. And
// authentication and an unreachable provider are the two nothing but a person
// or the network ends, so reading either as a death spends the relaunch budget
// and then blocks a run that had nothing wrong with it — which happened to at
// least two runs in the 2026-09-15..18 outage — and reading either as a refusal
// that stands fails it outright.
type Condition string

const (
	// CapacityExhausted is a usage limit refusing work: the account has no
	// capacity now and will have some later.
	CapacityExhausted Condition = "capacity-exhausted"
	// ModelUnavailable is the provider saying it has not got the model this
	// attempt asked for. It stands for that selector and says nothing about any
	// other.
	ModelUnavailable Condition = "model-unavailable"
	// AuthenticationRejected is the provider refusing the account the invocation
	// was made under. Nothing about the work was judged, and nothing changes
	// until a person logs in.
	AuthenticationRejected Condition = "authentication-rejected"
	// ProviderUnreachable is nothing answering at the provider's API at all: the
	// machine offline or asleep, a name that does not resolve, a connection
	// refused. Nothing was judged, and nothing changes until the network is back.
	ProviderUnreachable Condition = "provider-unreachable"
	// NetworkFailure is the attempt dying in transit — a connection that went
	// away mid-reply, a stream that stopped. It judged nothing and names no
	// condition that lifts.
	NetworkFailure Condition = "network-failure"
	// WorkRefused is a refusal that is a judgment on the work rather than an
	// environment failure: the provider read the request and would not serve it.
	WorkRefused Condition = "work-refused"
)

// Conditions is every condition an adapter is asked about, in the order they are
// documented.
var Conditions = []Condition{
	CapacityExhausted,
	ModelUnavailable,
	AuthenticationRejected,
	ProviderUnreachable,
	NetworkFailure,
	WorkRefused,
}

// Response is what the harness is left able to do about an invocation. It is the
// suite's unit of agreement rather than the contract's answer, because the
// answer is what a dialect said and this is what a caller acts on: two dialects
// may reach one response by different answers, and an adapter that reports the
// right answer and loses it on the way to the result has still left every caller
// downstream doing the wrong thing.
type Response string

const (
	// ResponseWaitForTheWindow is a limit the run waits out.
	ResponseWaitForTheWindow Response = "wait for the window to reopen"
	// ResponseAskForAnotherModel is a selector the provider has not got, which
	// the caller answers by naming one it does have.
	ResponseAskForAnotherModel Response = "ask for another model"
	// ResponseMakeAnotherAttempt is a death that judged nothing, which asks for
	// another attempt against a budget the harness keeps.
	ResponseMakeAnotherAttempt Response = "make another attempt"
	// ResponseRefusalStands is a refusal with nothing to wait for and nothing to
	// relaunch into.
	ResponseRefusalStands Response = "take the refusal as standing"
	// ResponseWaitForTheProvider is a provider nobody is logged into or nobody
	// can reach: a wait that spends nothing and ends when the provider answers.
	ResponseWaitForTheProvider Response = "wait for the provider to answer again, spending nothing"
	// ResponseCarriedOn is an invocation the harness has no complaint about,
	// which is the wrong answer to every condition here.
	ResponseCarriedOn Response = "carry on as though nothing was refused"
)

// Requires is the response every adapter must leave the harness holding for this
// condition, whichever provider met it and however that provider spelled it.
func (c Condition) Requires() Response {
	switch c {
	case CapacityExhausted:
		return ResponseWaitForTheWindow
	case ModelUnavailable:
		return ResponseAskForAnotherModel
	case NetworkFailure:
		return ResponseMakeAnotherAttempt
	case AuthenticationRejected, ProviderUnreachable:
		return ResponseWaitForTheProvider
	case WorkRefused:
		return ResponseRefusalStands
	default:
		return ""
	}
}

// Sample is one condition as its provider actually reports it: the lines that
// provider writes while the condition is met, and how its process ended. It is
// the provider's own words on purpose — a suite written in the contract's
// vocabulary would assert that each dialect agrees with itself, which is the one
// thing no divergence has ever been.
type Sample struct {
	// Name says which shape of the condition this is, for a failure that names
	// the case rather than the line number.
	Name string
	// Stream is the provider's own stdout, one envelope per line — or, for the
	// refusal a CLI makes before it writes a single envelope, the plain text it
	// wrote there instead.
	Stream string
	// Stderr is what the process wrote to its error stream, one line per line.
	// It is empty for nearly every sample: a provider says what it has to say on
	// its stream, and what it puts here is diagnostics. The exception is the
	// same refusal made before any envelope, which a CLI may put on either.
	Stderr string
	// ExitCode is what the process exited with. A non-zero one is a process the
	// runner reports as failed, which is what a provider that refused work
	// usually leaves behind and what a stream ending without a terminal has to
	// be paired with.
	ExitCode int
}

// Adapter is one provider's side of the suite: the description the harness
// resolves the adapter by, and that provider's own words for each condition.
//
// It carries a descriptor rather than a name so that a provider a project
// declared for itself can be put through the same suite as a built-in: what runs
// the samples is whichever adapter the descriptor names, reading them with
// whichever dialect the descriptor carries.
type Adapter struct {
	Descriptor backend.Descriptor
	Samples    map[Condition][]Sample
}

// Problem is one thing the suite found: a condition an adapter says nothing
// about, or a sample it classifies differently from the way every adapter must.
type Problem struct {
	Provider  domain.Backend
	Condition Condition
	Sample    string
	Detail    string
}

func (p Problem) String() string {
	where := string(p.Provider)
	if p.Condition != "" {
		where += " / " + string(p.Condition)
	}
	if p.Sample != "" {
		where += " / " + p.Sample
	}
	return where + ": " + p.Detail
}

// Verify runs every adapter's samples through that adapter and reports
// everything that disagrees with the conditions above. An empty result is the
// claim this package exists to make: these adapters classify the same provider
// conditions the same way.
func Verify(adapters []Adapter) []Problem {
	var problems []Problem
	for _, adapter := range adapters {
		problems = append(problems, adapter.verify()...)
	}
	return problems
}

// Uncovered is every adapter this build ships that the given set says nothing
// about. It is the gate on a new adapter: an adapter that ships with no cases
// beside it is one nobody has asked how it reads a refusal, and the first
// evidence of a divergent answer would be a run that waited where it should have
// failed.
func Uncovered(adapters []Adapter) []domain.Backend {
	spokenFor := make(map[domain.Backend]bool, len(adapters))
	for _, adapter := range adapters {
		spokenFor[adapter.Descriptor.ID] = true
	}
	var missing []domain.Backend
	for _, id := range backend.RunnableAdapters() {
		if !spokenFor[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

func (a Adapter) verify() []Problem {
	if !a.Descriptor.Runnable() {
		return []Problem{{
			Provider: a.Descriptor.ID,
			Detail:   "names no adapter this build ships, so there is nothing to put a sample through",
		}}
	}
	var problems []Problem
	for _, condition := range Conditions {
		samples := a.Samples[condition]
		if len(samples) == 0 {
			problems = append(problems, Problem{
				Provider:  a.Descriptor.ID,
				Condition: condition,
				Detail: fmt.Sprintf("has no sample, so nothing says how this provider reports it; the harness has to be left to %s",
					condition.Requires()),
			})
			continue
		}
		for _, sample := range samples {
			problems = append(problems, a.classify(condition, sample)...)
		}
	}
	return problems
}

// classify puts one sample through the adapter the descriptor names and reports
// what the harness was left holding.
func (a Adapter) classify(condition Condition, sample Sample) []Problem {
	problem := func(detail string) []Problem {
		return []Problem{{Provider: a.Descriptor.ID, Condition: condition, Sample: sample.Name, Detail: detail}}
	}
	role, served := roleFor(a.Descriptor)
	if !served {
		return problem("serves no role whose posture it holds, so no invocation could be made of it at all")
	}
	provider, built := compiled.For(a.Descriptor, a.Descriptor.ID, streamRunner{sample: sample}, "")
	if !built {
		return problem(fmt.Sprintf("names adapter %q, which this build cannot construct", a.Descriptor.Adapter))
	}
	model := "opus"
	if a.Descriptor.Adapter == domain.BackendCodex {
		model = "gpt-6.1-sol"
	}
	result, err := provider.Run(context.Background(), backend.RunRequest{
		RunID:            conformanceRunID,
		Role:             role,
		Model:            model,
		WorkingDirectory: "/worktree",
		Prompt:           "do the work",
	})
	if err != nil {
		return problem(fmt.Sprintf("could not read this stream at all: %v", err))
	}
	response, held := responseTo(result)
	if len(held) > 1 {
		return problem(fmt.Sprintf("leaves the harness holding %s at once, so which answer a run took would depend on the order its caller read them",
			strings.Join(held, " and ")))
	}
	if response != condition.Requires() {
		return problem(fmt.Sprintf("leaves the harness to %s, and every adapter has to leave it to %s",
			response, condition.Requires()))
	}
	return nil
}

// responseTo is what the harness would do about an invocation that ended this
// way, read from the same fields on the result that every caller downstream
// reads.
//
// Two readings at once is named rather than resolved by precedence. Which of
// them a run acted on would depend on the order its caller happened to read
// them, so an adapter that leaves both is one whose behaviour is decided
// somewhere else entirely — and the contract already holds that they must not
// stand together, which makes this the place a dialect that breaks it surfaces.
func responseTo(result backend.RunResult) (Response, []string) {
	var held []string
	response := ResponseCarriedOn
	if result.UsageLimit != nil {
		held = append(held, "a usage limit")
		response = ResponseWaitForTheWindow
	}
	if result.ModelUnavailable != nil {
		held = append(held, "a model the provider has not got")
		response = ResponseAskForAnotherModel
	}
	if result.ServerOverload != nil {
		held = append(held, "a server overload")
		response = ResponseMakeAnotherAttempt
	}
	if result.TransientFailure != nil {
		held = append(held, "a transient failure")
		response = ResponseMakeAnotherAttempt
	}
	if result.ProviderOutage != nil {
		held = append(held, "a provider nobody is logged into or can reach")
		response = ResponseWaitForTheProvider
	}
	switch {
	case len(held) > 1:
		return "", held
	case len(held) == 1:
		return response, held
	case result.IsError:
		// Nothing on the result asks the harness to wait, to ask again, or to ask
		// for anything else, and the invocation failed: what is left is a refusal
		// with nothing to do about it but stop.
		return ResponseRefusalStands, held
	default:
		return ResponseCarriedOn, held
	}
}

// roleFor is a role this provider serves and holds a posture for. The developer
// is preferred because it is the role every provider that edits anything serves,
// and because an adapter that refuses it would refuse the only invocation the
// harness makes of it inside a run; a provider that serves only advisory roles
// is asked under the first of those instead.
func roleFor(descriptor backend.Descriptor) (domain.AgentRole, bool) {
	if descriptor.SupportsRole(domain.RoleDeveloper) && descriptor.SupportsPosture(backend.PostureFor(domain.RoleDeveloper)) {
		return domain.RoleDeveloper, true
	}
	for _, role := range descriptor.Roles {
		if descriptor.SupportsPosture(backend.PostureFor(role)) {
			return role, true
		}
	}
	return "", false
}

// conformanceRunID is the run every sample is invoked under. It is shaped like a
// real run identifier because an adapter puts a piece of one on the provider's
// command line, and a sample that could not be invoked the way a run is would be
// exercising something else.
const conformanceRunID = "run-c0nf0rmance0000000000000000000"

// streamRunner is a process that writes one sample's stream and exits. It stands
// in for the provider itself: what this suite is asking about is how the adapter
// reads what a provider said, so the provider is the sample and nothing is
// started.
type streamRunner struct {
	sample Sample
}

func (r streamRunner) Run(_ context.Context, _ execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	status := execution.ProcessSucceeded
	if r.sample.ExitCode != 0 {
		status = execution.ProcessFailed
	}
	if observer != nil {
		for _, line := range strings.Split(strings.TrimSuffix(r.sample.Stream, "\n"), "\n") {
			if line == "" {
				continue
			}
			observer(execution.Output{Stream: execution.StreamStdout, Text: line})
		}
		for _, line := range strings.Split(strings.TrimSuffix(r.sample.Stderr, "\n"), "\n") {
			if line == "" {
				continue
			}
			observer(execution.Output{Stream: execution.StreamStderr, Text: line})
		}
	}
	return execution.ProcessResult{
		Status:   status,
		ExitCode: r.sample.ExitCode,
		Stdout:   r.sample.Stream,
		Stderr:   r.sample.Stderr,
	}, nil
}
