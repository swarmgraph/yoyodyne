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

// ResolvePassFailureForRole is ResolvePassFailure for the pass of one role,
// so the role that cannot run is never the one named to answer for it. The
// development manager's own failing pass goes to the factory-flow program
// manager, or to the Lead Product Manager where none is configured; the
// factory-flow program manager's own goes to the Lead Product Manager. Every
// other pass is routed exactly as ResolvePassFailure routes it, and a recorded
// person-only remedy still names the operator whichever role's pass it is.
func ResolvePassFailureForRole(factoryFlowAgent string, role domain.AgentRole, failedAgent string, remedy *PersonOnlyRemedy) PassFailure {
	answer := ResolvePassFailure(factoryFlowAgent, remedy)
	toProductManager := func() {
		answer.Watcher, answer.Agent, answer.Resolver, answer.Fallback = domain.RoleProductManager, "", domain.RoleProductManager, false
		if answer.Mover != MoverOperator {
			answer.Mover = MoverProductManager
		}
	}
	switch {
	case role == domain.RoleDevelopmentManager && factoryFlowAgent != "":
		answer.Resolver = domain.RoleProgramManager
	case role == domain.RoleDevelopmentManager:
		toProductManager()
	case role == domain.RoleProgramManager && factoryFlowAgent != "" && failedAgent == factoryFlowAgent:
		toProductManager()
	}
	return answer
}
