package readmodel

import (
	"fmt"
	"sort"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RoleDefinitionStatus compares a definition with its latest activation. An
// amended file has an activation on record but is not activated as it stands,
// and neither is a file that has moved since the person activated it: the
// activation names the file it was made about as well as the content.
type RoleDefinitionStatus struct {
	Definition   config.RoleDefinition    `json:"definition"`
	Activation   *runstate.RoleActivation `json:"activation,omitempty"`
	Activated    bool                     `json:"activated"`
	AmendedSince bool                     `json:"amended_since"`
	MovedSince   bool                     `json:"moved_since,omitempty"`
}

// RoleDefinitions is the shared derivation every surface uses. History is in
// the store's order, newest first; an older matching digest cannot authorize a
// file that differs from the person's latest decision.
func RoleDefinitions(definitions map[string]config.RoleDefinition, history []runstate.RoleActivation) []RoleDefinitionStatus {
	latest := make(map[string]runstate.RoleActivation)
	for _, activation := range history {
		if _, seen := latest[activation.Name]; !seen {
			latest[activation.Name] = activation
		}
	}
	roles := make([]RoleDefinitionStatus, 0, len(definitions))
	for name, definition := range definitions {
		status := RoleDefinitionStatus{Definition: definition}
		if activation, found := latest[name]; found {
			status.Activation = &activation
			status.AmendedSince = activation.Digest != definition.Digest
			status.MovedSince = activation.Source != definition.Source
			status.Activated = !status.AmendedSince && !status.MovedSince
		}
		roles = append(roles, status)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Definition.Name < roles[j].Definition.Name })
	return roles
}

// ActivationCheck answers, for the configuration loader, whether one role
// definition is activated as it stands, read through the same derivation
// `yoyo role list` reports. A definition nobody has activated, one amended
// since its latest activation, and one that now sits in a file other than the
// one activated are each refused, naming what a person does to activate it.
func ActivationCheck(history []runstate.RoleActivation) config.RoleActivationCheck {
	return func(definition config.RoleDefinition) error {
		statuses := RoleDefinitions(map[string]config.RoleDefinition{definition.Name: definition}, history)
		status := statuses[0]
		switch {
		case status.Activated:
			return nil
		case status.Activation == nil:
			return fmt.Errorf("nobody has activated it; a person activates it with `yoyo role activate %s`", definition.Name)
		case status.AmendedSince:
			return fmt.Errorf("its file has changed since %s activated digest %s; a person activates the file as it stands with `yoyo role activate %s`", status.Activation.Person, status.Activation.Digest, definition.Name)
		default:
			return fmt.Errorf("it now sits at %s, but %s activated it at %s; a person activates it where it now stands with `yoyo role activate %s`", definition.Source, status.Activation.Person, status.Activation.Source, definition.Name)
		}
	}
}
