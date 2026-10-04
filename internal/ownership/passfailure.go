// Package ownership decides who moves a finding. A person is named only for
// a recorded step that cannot be performed by the harness or a role.
package ownership

import "github.com/mason-bryant/yoyodyne/internal/domain"

// PassFailure is the ownership of a repeatedly failing scheduled pass.
type PassFailure struct {
	Watcher    domain.AgentRole `json:"watcher"`
	Agent      string           `json:"agent,omitempty"`
	Resolver   domain.AgentRole `json:"resolver"`
	Mover      Mover            `json:"mover"`
	PersonStep string           `json:"person_step,omitempty"`
	Fallback   bool             `json:"fallback,omitempty"`
}

// ResolvePassFailure uses the factory-flow instance where configured. The
// personStep comes only from a report handling that records NeedsOperator;
// failure prose and configuration cannot grant that ownership.
func ResolvePassFailure(factoryFlowAgent, personStep string) PassFailure {
	answer := PassFailure{Watcher: domain.RoleProgramManager, Agent: factoryFlowAgent,
		Resolver: domain.RoleDevelopmentManager, Mover: MoverProgramManager}
	if factoryFlowAgent == "" {
		answer.Watcher, answer.Mover, answer.Fallback = domain.RoleDevelopmentManager, MoverDevelopmentManager, true
	}
	if personStep != "" {
		answer.Mover, answer.PersonStep = MoverOperator, personStep
	}
	return answer
}
