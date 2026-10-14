// Package action is the registry of named operations a workflow definition may
// sequence.
//
// A registry is built in Go, from actions written in Go, and it is closed the
// moment it is built. That is the whole of the law it exists to enforce:
// configuration selects sequence, and code grants capability. A definition can
// name `candidate.integrate` and decide when it runs and what happens after it;
// it cannot describe a new action, and it cannot say that the one it named needs
// less than the action itself declares.
//
// The registry is deliberately not a dispatcher. It answers what actions exist,
// what each requires, and which trusted function each is a second door onto —
// and hands back the action so a caller can perform it. Deciding when to walk
// through the door, under which lease, with which parameters, and what to do
// with the outcome is the runtime's, and it is not here yet.
package action

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/mason-bryant/yoyodyne/internal/capability"
)

// Action is one named operation and everything trusted code says about it.
//
// It is generic over its subject because the subject is what the action acts
// on — a delivery run, and later a triage docket or a management request — and
// a registry that erased it would be one every caller had to assert its way out
// of. The type parameter is the registry's, so a registry holds actions over one
// subject and cannot be handed an action over another.
type Action[S any] struct {
	// Name is what a definition selects this action by. It is the registry's key
	// and it never changes: a name in a pinned definition has to keep meaning the
	// same operation for as long as an instance can still be resumed against it.
	Name string
	// Summary is what performing it does, in the words somebody reading a
	// definition needs rather than the words the implementation uses.
	Summary string
	// Wraps names the trusted function this action is a second door onto, as
	// `(*receiver).method`. It is here so that the claim this registry makes — that
	// it wraps the code the harness already runs rather than a second copy of it —
	// is something a test can hold against the source, and it is why the repository
	// test can fail on a step whose function was renamed out from under it.
	Wraps string
	// Capabilities is everything performing this action requires. It is declared
	// here, in code, and it is the only place it is declared: nothing a definition
	// says adds to it and nothing a definition says takes anything away.
	Capabilities []capability.Capability
	// Tool marks an action a conversation may request. A nil descriptor leaves
	// the action available only to its existing runtime.
	Tool *Tool
	// Perform is the door. It calls the same function the hard-coded pipeline
	// calls, with nothing in between, so that a caller reaching a step through the
	// registry and a caller reaching it directly are reaching one implementation.
	Perform func(ctx context.Context, subject S) error
}

// Registry is a closed set of actions over one kind of subject.
type Registry[S any] struct {
	byName map[string]Action[S]
	// names is declaration order, kept so that what a registry reports about
	// itself reads the same twice and in the order somebody wrote the actions
	// down, rather than in whichever order a map felt like.
	names []string
}

// New builds a registry, refusing anything that would make it untrustworthy.
//
// Everything it refuses, it refuses at construction. A registry is built once,
// from a literal table, so a defect in that table is a defect in the binary
// rather than in one run's luck — and finding it when the registry is built means
// finding it in every test that builds one, which is what makes these refusals
// worth having at all.
//
// The refusals are reported together rather than one at a time, because a table
// with three mistakes in it is worth seeing whole.
func New[S any](actions ...Action[S]) (Registry[S], error) {
	registry := Registry[S]{byName: make(map[string]Action[S], len(actions))}
	var problems []error
	for index, candidate := range actions {
		// An action with no name cannot be selected and cannot be reported on, so
		// it is named by its position instead — which is the only thing there is to
		// say about it.
		if candidate.Name == "" {
			problems = append(problems, fmt.Errorf("the action at position %d has no name", index))
			continue
		}
		if _, taken := registry.byName[candidate.Name]; taken {
			problems = append(problems, fmt.Errorf("%q is registered more than once; a definition selecting it would not say which one it meant", candidate.Name))
			continue
		}
		for _, required := range candidate.Capabilities {
			if !required.Known() {
				problems = append(problems, fmt.Errorf("%q requires %q, which is not a capability this repository declares; add it to internal/capability or name one that is there", candidate.Name, required))
			}
		}
		// An action that requires nothing has not said what it requires. The
		// distinction matters because the registry is where authority is written
		// down: an empty list reads as "needs no authority", and a step that
		// genuinely needs none is rare enough to be worth adding deliberately
		// rather than reachable by forgetting.
		if len(candidate.Capabilities) == 0 {
			problems = append(problems, fmt.Errorf("%q declares no capabilities; every action states what performing it requires", candidate.Name))
		}
		if candidate.Tool != nil {
			if err := candidate.Tool.Validate(candidate.Name, candidate.Capabilities); err != nil {
				problems = append(problems, err)
			}
		}
		// The two claims that make this a registry of trusted code rather than a
		// list of names: something to perform, and a statement of what it is a
		// second door onto.
		if candidate.Perform == nil {
			problems = append(problems, fmt.Errorf("%q has nothing to perform", candidate.Name))
		}
		if candidate.Wraps == "" {
			problems = append(problems, fmt.Errorf("%q names no function it wraps; an action is a second door onto trusted code and has to say onto what", candidate.Name))
		}
		registry.byName[candidate.Name] = clone(candidate)
		registry.names = append(registry.names, candidate.Name)
	}
	if len(problems) > 0 {
		return Registry[S]{}, errors.Join(problems...)
	}
	return registry, nil
}

// Names is every registered action, in declaration order.
func (r Registry[S]) Names() []string {
	return slices.Clone(r.names)
}

// Lookup is the action registered under a name, and whether there is one. A
// name nothing is registered under is the answer a definition referring to an
// action that does not exist gets, and it is deliberately not an error here: what
// to do about it belongs to whatever was reading the definition.
func (r Registry[S]) Lookup(name string) (Action[S], bool) {
	registered, found := r.byName[name]
	return clone(registered), found
}

// Actions is every registered action, in declaration order.
func (r Registry[S]) Actions() []Action[S] {
	registered := make([]Action[S], 0, len(r.names))
	for _, name := range r.names {
		registered = append(registered, clone(r.byName[name]))
	}
	return registered
}

func clone[S any](entry Action[S]) Action[S] {
	entry.Capabilities = slices.Clone(entry.Capabilities)
	if entry.Tool != nil {
		descriptor := *entry.Tool
		entry.Tool = &descriptor
	}
	return entry
}
