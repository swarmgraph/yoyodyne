package cli

// The verbs that record a person's decision refuse a process an agent started.
//
// `yoyo pause`, `yoyo resume`, `yoyo release`, `yoyo artifact approve`,
// `yoyo role activate`, and `yoyo gate record` each
// write down something only a person decides: to stop spending, to start again,
// to let the harness choose work, to stand behind a document, to activate a role
// definition, or to attest to taking a step. Inside the harness
// that is enforced in Go -- no pipeline path writes any of those records -- but
// each verb is a binary anything with a shell can execute, and a developer run
// has a shell. So the guarantee held against the harness's own code and, against
// an agent, held only as a sentence in the documentation.
//
// Every process the harness launches for a role now carries the role in its
// environment (execution.AgentRoleVariable), and the allowlist carries it
// through to every shell and every `yoyo` the agent starts. These verbs read it
// before they read anything else, and refuse with a sentence saying whose act
// this is rather than with a permission error. What that catches is a mistake
// the system could make on its own -- a developer told to unblock the queue and
// reaching for `yoyo release`. An agent that means to defeat it can strip its
// own environment. Behind that, an approval is a write to a protected path,
// which the gate refuses in a run's change whatever wrote it and which is
// tested end to end; the holds are under the state root, where the only thing
// between a stripped environment and a write is the provider's own sandbox,
// which the harness enables for a developer run and neither declares the
// policy of nor verifies. Gate acts live under that same state root and hold
// the same boundary.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// refusedToAgentProcess is the refusal a verb that records a person's decision
// gives a process the harness launched for a role, and nil for any other
// process. verb is the command as typed, and act is what a person does with it,
// in the sentence the refusal says.
func refusedToAgentProcess(verb, act string) error {
	role, launched := execution.LaunchedForRole(nil)
	if !launched {
		return nil
	}
	return fmt.Errorf("%s is refused from a process the harness launched for the %s: %s, and an agent's process is not one", verb, role, act)
}
