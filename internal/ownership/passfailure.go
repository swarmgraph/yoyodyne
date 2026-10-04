// Package ownership decides who moves a finding. A person is named only for
// a recorded step that cannot be performed by the harness or a role.
package ownership

import "github.com/mason-bryant/yoyodyne/internal/domain"

// PassFailure is the ownership of a repeatedly failing scheduled pass.
type PassFailure struct {
	Watcher      domain.AgentRole `json:"watcher"`
	Agent        string           `json:"agent,omitempty"`
	Resolver     domain.AgentRole `json:"resolver"`
	Mover        Mover            `json:"mover"`
	PersonReason PersonOnlyReason `json:"person_reason,omitempty"`
	PersonTarget string           `json:"person_target,omitempty"`
	PersonStep   string           `json:"person_step,omitempty"`
	Fallback     bool             `json:"fallback,omitempty"`
}

// ResolvePassFailure uses the factory-flow instance where configured. The
// remedy must record a permitted person-only reason and its exact step;
// failure prose, an operator flag, and configuration cannot grant ownership.
func ResolvePassFailure(factoryFlowAgent string, remedy *PersonOnlyRemedy) PassFailure {
	answer := PassFailure{Watcher: domain.RoleProgramManager, Agent: factoryFlowAgent,
		Resolver: domain.RoleDevelopmentManager, Mover: MoverProgramManager}
	if factoryFlowAgent == "" {
		answer.Watcher, answer.Mover, answer.Fallback = domain.RoleDevelopmentManager, MoverDevelopmentManager, true
	}
	if remedy != nil && remedy.Validate() == nil {
		answer.Mover, answer.PersonReason, answer.PersonTarget, answer.PersonStep = MoverOperator, remedy.Reason, remedy.Target, remedy.Step
	}
	return answer
}
