package config

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// RoutedEndpoint contains only the configuration safe to persist with a run.
// Account authentication, local paths, health and capacity are not read here.
type RoutedEndpoint struct {
	Endpoint     backend.Endpoint  `json:"endpoint"`
	Model        string            `json:"model"`
	ModelVersion string            `json:"model_version,omitempty"`
	Effort       string            `json:"effort,omitempty"`
	Origins      map[string]string `json:"origins"`
}

// ResolvedEndpointPair is configuration support for later execution consumers.
// It does not promise that either endpoint currently has credentials or capacity.
type ResolvedEndpointPair struct {
	Slot          int              `json:"slot,omitempty"`
	Role          domain.AgentRole `json:"role"`
	Posture       backend.Posture  `json:"posture"`
	Primary       RoutedEndpoint   `json:"primary"`
	Alternate     *RoutedEndpoint  `json:"alternate,omitempty"`
	Enabled       bool             `json:"enabled"`
	EnabledOrigin string           `json:"enabled_origin"`
	Explicit      bool             `json:"explicit"`
	Revision      string           `json:"revision"`
	Origin        string           `json:"origin"`
}

// ResolveDeveloperEndpoints gives an explicit slot pair precedence over label
// models. Slots without routing retain the existing first-label-match rule.
func (r Resolved) ResolveDeveloperEndpoints(slot int, name string, labels []string) (ResolvedEndpointPair, error) {
	if slot < 1 || slot > r.Config.Execution.MaxConcurrentDevelopers {
		return ResolvedEndpointPair{}, fmt.Errorf("developer slot %d must be between 1 and execution.max_concurrent_developers (%d)", slot, r.Config.Execution.MaxConcurrentDevelopers)
	}
	var routing *domain.EndpointPair
	if slot <= len(r.Config.Execution.DeveloperSlots) {
		routing = r.Config.Execution.DeveloperSlots[slot-1].Routing
	}
	return r.resolveRoleEndpoints(slot, name, domain.RoleDeveloper, routing, labels)
}

// ResolveReviewerEndpoints reads only the reviewer's configuration. Developer
// slot choices and labels cannot change its endpoints or read-only posture.
func (r Resolved) ResolveReviewerEndpoints(name string) (ResolvedEndpointPair, error) {
	return r.resolveRoleEndpoints(0, name, domain.RoleReviewer, nil, nil)
}

func (r Resolved) resolveRoleEndpoints(slot int, name string, role domain.AgentRole, routing *domain.EndpointPair, labels []string) (ResolvedEndpointPair, error) {
	name = strings.TrimSpace(name)
	agent, ok := r.Config.Agents[name]
	if !ok || agent.Role != role {
		return ResolvedEndpointPair{}, fmt.Errorf("agent %q must be a configured %s", name, role)
	}
	providers, err := r.Config.ProviderRegistry()
	if err != nil {
		return ResolvedEndpointPair{}, err
	}
	pair, problems := r.roleEndpoints(providers, slot, name, agent, routing, labels)
	if len(problems) > 0 {
		return ResolvedEndpointPair{}, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	pair.Revision = r.Config.Revision()
	return pair, nil
}

func (r Resolved) origin(key string) string {
	if source := r.Origins[key]; source != "" {
		return key + " (" + source + ")"
	}
	return key + " (" + OriginDefault + ")"
}

func (r Resolved) roleEndpoints(providers *backend.Registry, slot int, name string, agent AgentConfig, routing *domain.EndpointPair, labels []string) (ResolvedEndpointPair, []string) {
	prefix := "agents." + name
	defaults := agent
	defaults.Account = r.Config.agentAccountAlias(providers, name)
	fields := map[string]string{
		"provider": r.origin(prefix + ".backend"), "model": r.origin(prefix + ".model"),
		"model_version": r.origin(prefix + ".model_version"), "account": r.origin(prefix + ".account"),
		"effort": r.origin(prefix + ".effort"),
	}
	if strings.TrimSpace(agent.Account) == "" {
		fields["account"] += "; derived:accounts, first active account compatible with " + string(agent.Backend)
	}
	pair := ResolvedEndpointPair{Slot: slot, Role: agent.Role, Posture: backend.PostureFor(agent.Role), Origin: r.origin(prefix + ".model")}
	primarySpec := domain.EndpointSpec{}
	var alternateSpec *domain.EndpointSpec
	if routing != nil {
		prefix = fmt.Sprintf("execution.developer_slots slot %d.routing", slot)
		pair.Origin = r.origin("execution.developer_slots")
		pair.Explicit, pair.Enabled = true, routing.Enabled
		pair.EnabledOrigin = prefix + ".enabled (" + pair.Origin + ")"
		var missing []string
		if routing.Primary == nil {
			missing = append(missing, prefix+".primary is required")
		}
		if routing.Alternate == nil {
			missing = append(missing, prefix+".alternate is required")
		}
		// Report both absent halves; validate any supplied half as well.
		if routing.Primary != nil {
			primarySpec = *routing.Primary
		}
		alternateSpec = routing.Alternate
		primary, problems := r.resolveRoutedEndpoint(providers, prefix+".primary", defaults, primarySpec, fields, pair.Origin)
		pair.Primary = primary
		problems = append(missing, problems...)
		if alternateSpec != nil {
			alternateDefaults := defaults
			alternateDefaults.Backend, alternateDefaults.Account = primary.Endpoint.Provider, primary.Endpoint.AccountAlias
			alternateDefaults.Model, alternateDefaults.ModelVersion = "", ""
			alternateFields := copyOrigins(fields)
			alternateFields["provider"], alternateFields["account"] = primary.Origins["provider"], primary.Origins["account"]
			alternateFields["model_version"] = "no version pin: alternate.model"
			alternate, more := r.resolveRoutedEndpoint(providers, prefix+".alternate", alternateDefaults, *alternateSpec, alternateFields, pair.Origin)
			pair.Alternate = &alternate
			problems = append(problems, more...)
		}
		return pair, append(problems, identicalEndpointProblems(prefix, pair)...)
	}
	if agent.Role == domain.RoleDeveloper {
		choice := ResolveDeveloperModel(r.Config.Execution.DeveloperModels, labels, defaults.Model)
		if choice.Chosen() {
			defaults.Model = choice.Model
			if choice.Model != agent.Model {
				defaults.ModelVersion = ""
				fields["model_version"] = "no version pin: execution.developer_models"
			}
			fields["model"] = r.origin("execution.developer_models") + ": " + choice.Reason
		}
	}
	primary, problems := r.resolveRoutedEndpoint(providers, prefix, defaults, primarySpec, fields, "")
	pair.Primary = primary
	f := agent.Failover
	pair.Enabled = f.Enabled
	pair.EnabledOrigin = r.origin(prefix+".failover") + ": enabled"
	if f.Model != "" || f.Provider != "" || f.Account != "" || f.Effort != nil || f.Enabled {
		alternateDefaults := defaults
		alternateDefaults.Model, alternateDefaults.ModelVersion = "", ""
		alternate := domain.EndpointSpec{Provider: f.Provider, Model: f.Model, Account: f.Account, Effort: f.Effort}
		selected, more := r.resolveRoutedEndpoint(providers, prefix+".failover", alternateDefaults, alternate, fields, r.origin(prefix+".failover"))
		pair.Alternate = &selected
		problems = append(problems, more...)
	}
	if agent.Role == domain.RoleReviewer {
		problems = append(problems, identicalEndpointProblems(prefix, pair)...)
	}
	return pair, problems
}

func identicalEndpointProblems(prefix string, pair ResolvedEndpointPair) []string {
	if pair.Alternate != nil && pair.Primary.Endpoint.Model != "" && pair.Primary.Endpoint.Same(pair.Alternate.Endpoint) {
		return []string{prefix + ": primary and alternate resolve to the same endpoint"}
	}
	return nil
}

func copyOrigins(origins map[string]string) map[string]string {
	copy := make(map[string]string, len(origins))
	for field, origin := range origins {
		copy[field] = origin
	}
	return copy
}

func (r Resolved) resolveRoutedEndpoint(providers *backend.Registry, path string, defaults AgentConfig, spec domain.EndpointSpec, inherited map[string]string, source string) (RoutedEndpoint, []string) {
	agent := defaults
	origins := copyOrigins(inherited)
	stated := func(field string) { origins[field] = path + "." + field + " (" + source + ")" }
	if spec.Provider != "" {
		agent.Backend = domain.Backend(strings.TrimSpace(string(spec.Provider)))
		stated("provider")
	}
	if spec.Model != "" {
		agent.Model, agent.ModelVersion = strings.TrimSpace(spec.Model), ""
		stated("model")
		origins["model_version"] = "no version pin: " + origins["model"]
	}
	if spec.ModelVersion != "" {
		agent.ModelVersion = strings.TrimSpace(spec.ModelVersion)
		stated("model_version")
	}
	if spec.Account != "" {
		agent.Account = strings.TrimSpace(spec.Account)
		stated("account")
	}
	if spec.Effort != nil {
		agent.Effort = strings.TrimSpace(*spec.Effort)
		stated("effort")
	}
	var problems []string
	for _, field := range []struct{ name, value string }{{"provider", string(agent.Backend)}, {"account", agent.Account}} {
		if err := domain.ValidateIdentifier(field.name, field.value); err != nil {
			problems = append(problems, path+"."+field.name+": "+err.Error())
		}
	}
	if err := ValidateModelSelector(agent.Model); err != nil {
		problems = append(problems, path+".model: "+err.Error())
	}
	problems = append(problems, modelVersionProblems(path, agent.ModelVersion, agent.Model, "")...)
	problems = append(problems, effortProblems(providers, path, agent)...)
	if err := providers.Serves(agent.Backend, agent.Role); err != nil {
		problems = append(problems, path+".provider: "+err.Error())
	}
	if _, err := r.Config.Endpoint("", agent.Account); err != nil {
		problems = append(problems, path+".account: "+err.Error())
	}
	if err := r.Config.accountServes(providers, agent.Account, agent.Backend); err != nil {
		problems = append(problems, path+".account: "+err.Error())
	}
	model := agent.Model
	if agent.ModelVersion != "" {
		model = agent.ModelVersion
	}
	endpoint, err := providers.Endpoint(agent.Backend, agent.Account, model)
	if err != nil {
		problems = append(problems, path+": "+err.Error())
	}
	effort := agent.Effort
	if descriptor, ok := providers.Lookup(agent.Backend); ok {
		effort = descriptor.InvocationEffort(model, effort)
		if agent.Effort == "" {
			origins["effort"] += fmt.Sprintf("; provider %s model %s default", agent.Backend, model)
		}
	}
	return RoutedEndpoint{Endpoint: endpoint, Model: agent.Model, ModelVersion: agent.ModelVersion, Effort: effort, Origins: origins}, problems
}

func (c Config) slotRoutingProblems(providers *backend.Registry) []string {
	r := Resolved{Config: c}
	var problems []string
	for index, slot := range c.Execution.DeveloperSlots {
		if slot.Routing == nil {
			continue
		}
		for _, name := range sortedNames(c.Agents) {
			agent := c.Agents[name]
			if agent.Role != domain.RoleDeveloper {
				continue
			}
			_, more := r.roleEndpoints(providers, index+1, name, agent, slot.Routing, nil)
			problems = append(problems, more...)
		}
	}
	for _, name := range sortedNames(c.Agents) {
		agent := c.Agents[name]
		if agent.Role != domain.RoleReviewer || (agent.Failover == (Failover{})) {
			continue
		}
		_, more := r.roleEndpoints(providers, 0, name, agent, nil, nil)
		problems = append(problems, more...)
	}
	return problems
}

// Reload replaces the effective configuration only after the complete file,
// its inherited defaults, personas and role definitions have passed validation.
// The caller serializes reload with its own configuration consumers.
func (r *Resolved) Reload() error {
	if r.Path == "" {
		return fmt.Errorf("reload configuration: no source path is recorded")
	}
	next, err := LoadResolved(r.Path)
	if err != nil {
		return err
	}
	*r = next
	return nil
}
