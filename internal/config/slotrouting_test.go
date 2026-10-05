package config

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const routingAccounts = `accounts:
  default:
    provider: claude-code
  codex-account:
    provider: codex
`

func TestSlotRoutingResolvesTheFourSlotsBeforeLabelModels(t *testing.T) {
	t.Parallel()
	project := minimalProjectConfig + routingAccounts + `execution:
  max_concurrent_developers: 4
  developer_models:
    - label: reliability
      model: sonnet
  developer_slots:
`
	for slot := 1; slot <= 4; slot++ {
		primary, alternate := "claude-code, model: opus, account: default", "codex, model: gpt-6.1-sol, account: codex-account"
		if slot == 1 {
			primary, alternate = alternate, primary
		}
		project += fmt.Sprintf("    - number: %d\n      prefer: [reliability]\n      routing:\n        enabled: true\n        primary: {provider: %s}\n        alternate: {provider: %s}\n", slot, primary, alternate)
	}
	project += "agents:\n  developer:\n    instances: 4\n"
	resolved := loadProject(t, project, nil)
	for slot := 1; slot <= 4; slot++ {
		pair, err := resolved.ResolveDeveloperEndpoints(slot, "developer", []string{"reliability"})
		if err != nil {
			t.Fatal(err)
		}
		want := domain.BackendClaudeCode
		if slot == 1 {
			want = domain.BackendCodex
		}
		if pair.Primary.Endpoint.Provider != want || pair.Primary.Endpoint.Model == "sonnet" || pair.Alternate == nil || pair.Alternate.Endpoint.Provider == want || !pair.Enabled || !pair.Explicit || pair.Slot != slot {
			t.Fatalf("slot %d = %+v", slot, pair)
		}
		if pair.Revision != resolved.Config.Revision() || !strings.Contains(pair.Origin, resolved.Path) || !strings.Contains(pair.Primary.Origins["model"], "routing.primary.model") {
			t.Fatalf("missing selection origins: %+v", pair)
		}
	}
	if !resolved.Config.Execution.DeveloperSlots[0].Prefers([]string{"reliability"}) {
		t.Fatal("slot 1 lost its preference")
	}
	reviewer, err := resolved.ResolveReviewerEndpoints("reviewer")
	if err != nil || reviewer.Primary.Endpoint.Model != resolved.Config.Agents["reviewer"].Model || reviewer.Explicit || reviewer.Slot != 0 {
		t.Fatalf("reviewer inherited slot routing: %+v, %v", reviewer, err)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil || !strings.Contains(string(encoded), `"routing"`) {
		t.Fatalf("effective configuration lost routing: %s, %v", encoded, err)
	}
}

func TestRoutingDefaultsDisabledFallbackAndLegacyLabelPrecedence(t *testing.T) {
	t.Parallel()
	resolved := loadProject(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 2
  developer_models:
    - {label: docs, model: sonnet}
    - {label: tests, model: haiku}
  developer_slots:
    - routing:
        enabled: false
        primary: {}
        alternate: {model: sonnet}
    - {}
agents:
  developer:
    instances: 2
    model: opus
    effort: high
`, nil)
	pair, err := resolved.ResolveDeveloperEndpoints(1, "developer", []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	if pair.Enabled || !pair.Explicit || pair.Primary.Endpoint.Model != "opus" || pair.Alternate.Endpoint.Model != "sonnet" || pair.Primary.Endpoint.AccountAlias != "default" || pair.Primary.Effort != "high" || pair.Alternate.Effort != "high" {
		t.Fatalf("default resolution = %+v", pair)
	}
	if !strings.Contains(pair.Primary.Origins["model"], "agents.developer.model") || !strings.Contains(pair.Alternate.Origins["account"], "agents.developer.account") {
		t.Fatalf("inherited origins = %+v", pair)
	}
	legacy, err := resolved.ResolveDeveloperEndpoints(2, "developer", []string{"tests", "docs"})
	if err != nil || legacy.Primary.Endpoint.Model != "sonnet" || legacy.Explicit || !strings.Contains(legacy.Primary.Origins["model"], "first") {
		t.Fatalf("legacy precedence = %+v, %v", legacy, err)
	}
	unchanged := loadProject(t, minimalProjectConfig, nil)
	legacy, err = unchanged.ResolveDeveloperEndpoints(1, "developer", nil)
	if err != nil || legacy.Primary.Model != unchanged.Config.Agents["developer"].Model || legacy.Alternate != nil || legacy.Enabled {
		t.Fatalf("legacy defaults = %+v, %v", legacy, err)
	}
}

func TestReviewerPairsResolveIndependentlyInBothDirections(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ provider, model, account, alternate, alternateModel, alternateAccount, effort string }{
		{"codex", "gpt-6.1-sol", "codex-account", "claude-code", "opus", "default", "ultra"},
		{"claude-code", "opus", "default", "codex", "gpt-6.1-sol", "codex-account", "high"},
	} {
		resolved := loadProject(t, minimalProjectConfig+routingAccounts+fmt.Sprintf(`agents:
  reviewer:
    backend: %s
    model: %s
    account: %s
    effort: %s
    failover:
      enabled: true
      provider: %s
      model: %s
      account: %s
      effort: ""
`, test.provider, test.model, test.account, test.effort, test.alternate, test.alternateModel, test.alternateAccount), nil)
		pair, err := resolved.ResolveReviewerEndpoints("reviewer")
		if err != nil {
			t.Fatal(err)
		}
		if pair.Primary.Endpoint.Provider != domain.Backend(test.provider) || pair.Alternate.Endpoint.Provider != domain.Backend(test.alternate) || pair.Posture != "read-only" || pair.Role != domain.RoleReviewer || !pair.Enabled {
			t.Fatalf("reviewer pair = %+v", pair)
		}
		if pair.Alternate.Effort == "ultra" || !strings.Contains(pair.Alternate.Origins["effort"], "failover.effort") {
			t.Fatalf("alternate inherited incompatible effort: %+v", pair.Alternate)
		}
		encoded, err := json.Marshal(pair)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"directory", "credential", "token", "secret"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("persistable pair contains %s: %s", forbidden, encoded)
			}
		}
	}
}

func TestRoutingCollectsInvalidSlotAndEndpointFields(t *testing.T) {
	t.Parallel()
	_, err := loadProjectError(t, minimalProjectConfig+`execution:
  max_concurrent_developers: 3
  developer_slots:
    - number: 0
      routing:
        primary: {provider: unknown, model: "bad model", account: unknown}
    - number: 2
      routing:
        alternate: {provider: codex, model: unknown, effort: bananas}
    - number: 2
      routing:
        primary: {}
        alternate: {model: opus}
agents:
  developer:
    instances: 3
    model: opus
`, nil)
	if err == nil {
		t.Fatal("invalid pairs loaded")
	}
	for _, want := range []string{"slot 1.number", "must be between", "slot 3.number repeats slot 2", "slot numbers must follow list order", "slot 1.routing.primary.provider", "slot 1.routing.primary.model", "slot 1.routing.primary.account", "slot 1.routing.alternate is required", "slot 2.routing.primary is required", "not established", "does not accept", "same endpoint"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
}

func TestRoutingRefusesIneligibleAndIncompletePairsEvenWhenDisabled(t *testing.T) {
	t.Parallel()
	writesOnly := strings.Replace(crossingProviders, "      - read-only\n", "", 1)
	noDeveloper := strings.Replace(crossingProviders, "      - developer\n", "", 1)
	for _, test := range []struct{ name, providers, routing, agents, want string }{
		{"alternate with no model", "", "primary: {}\n        alternate: {}", "", "alternate.model"},
		{"same endpoint after account inheritance", "", "primary: {}\n        alternate: {model: opus, account: default}", "", "same endpoint"},
		{"invalid primary model pin", "", "primary: {model: opus, model_version: opus}\n        alternate: {model: sonnet}", "", "pins model version"},
		{"authentication mismatch", routingAccounts, "primary: {}\n        alternate: {provider: codex, model: gpt-6.1-sol}", "", "would authenticate as nobody"},
		{"role refused", noDeveloper, "primary: {}\n        alternate: {provider: second-provider, model: opus, account: second-account}", "", "does not support role"},
		{"primary effort invalid", "", "primary: {effort: bananas}\n        alternate: {model: sonnet}", "", "does not accept"},
		{"alternate effort invalid", routingAccounts, "primary: {provider: codex, model: gpt-6.1-sol, account: codex-account}\n        alternate: {provider: claude-code, model: opus, account: default}", "    effort: ultra\n", "does not accept"},
		{"model-specific effort invalid", routingAccounts, "primary: {provider: codex, model: gpt-6.1-sol, account: codex-account}\n        alternate: {model: gpt-6-luna}", "    effort: ultra\n", "does not accept"},
		{"unknown same-provider account", "", "primary: {}\n        alternate: {model: sonnet, account: missing}", "", "not one this configuration declares"},
		{"unknown alternate provider", "", "primary: {}\n        alternate: {model: opus, provider: missing}", "", "not one this project names"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadProjectError(t, minimalProjectConfig+test.providers+`execution:
  developer_slots:
    - routing:
        enabled: false
        `+test.routing+"\nagents:\n  developer:\n    model: opus\n"+test.agents, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
		})
	}
	_, err := loadProjectError(t, minimalProjectConfig+writesOnly+`agents:
  reviewer:
    failover:
      enabled: false
      provider: second-provider
      model: opus
      account: second-account
`, nil)
	if err == nil || !strings.Contains(err.Error(), `cannot hold the "read-only"`) {
		t.Fatalf("incompatible reviewer accepted: %v", err)
	}
}

func TestRoutingValidatesBothPinnedModelSelectors(t *testing.T) {
	t.Parallel()
	_, err := loadProjectError(t, minimalProjectConfig+routingAccounts+`execution:
  developer_slots:
    - routing:
        primary: {provider: codex, account: codex-account, model: gpt-6-luna, model_version: gpt-6-astra, effort: ultra}
        alternate: {provider: claude-code, model: opus, account: default}
`, nil)
	if err == nil || !strings.Contains(err.Error(), `model "gpt-6-luna"`) {
		t.Fatalf("family fallback effort accepted: %v", err)
	}
}

func TestRoutingRejectsWrongRoleAndSlotAtResolution(t *testing.T) {
	t.Parallel()
	resolved := loadProject(t, minimalProjectConfig, nil)
	for _, slot := range []int{0, -1, 2} {
		if _, err := resolved.ResolveDeveloperEndpoints(slot, "developer", nil); err == nil {
			t.Fatalf("slot %d resolved", slot)
		}
	}
	if _, err := resolved.ResolveDeveloperEndpoints(1, "reviewer", nil); err == nil {
		t.Fatal("reviewer served as developer")
	}
	if _, err := resolved.ResolveReviewerEndpoints("developer"); err == nil {
		t.Fatal("developer served as reviewer")
	}
}

func TestReloadPreservesTheLastCompleteValidConfiguration(t *testing.T) {
	t.Parallel()
	resolved := loadProject(t, minimalProjectConfig+`execution:
  developer_slots:
    - routing:
        enabled: true
        primary: {}
        alternate: {model: sonnet}
`, nil)
	before := resolved
	snapshot, err := resolved.ResolveDeveloperEndpoints(1, "developer", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		minimalProjectConfig + "execution:\n  developer_slots:\n    - routing: {primary: {model: haiku}}\n",
		minimalProjectConfig + "agents:\n  reviewer:\n    persona: {version: v1, path: missing.md}\n",
		minimalProjectConfig + "execution: {new_unknown_key: true}\n",
	} {
		if err := os.WriteFile(resolved.Path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Reload(); err == nil {
			t.Fatal("invalid replacement reloaded")
		}
		if !reflect.DeepEqual(before, resolved) {
			t.Fatal("rejected replacement changed the last valid configuration")
		}
	}
	replacement := minimalProjectConfig + "execution:\n  developer_slots:\n    - routing:\n        enabled: false\n        primary: {model: haiku}\n        alternate: {model: sonnet}\n"
	if err := os.WriteFile(resolved.Path, []byte(replacement), 0600); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Reload(); err != nil {
		t.Fatal(err)
	}
	after, err := resolved.ResolveDeveloperEndpoints(1, "developer", nil)
	if err != nil || after.Primary.Model != "haiku" || after.Revision == snapshot.Revision || after.Enabled {
		t.Fatalf("new configuration = %+v, %v", after, err)
	}
	if snapshot.Primary.Model != before.Config.Agents["developer"].Model || !snapshot.Enabled {
		t.Fatal("reload changed a previously resolved selection")
	}
}

func TestSameProviderModelsResolveTheirOwnEffortDefaults(t *testing.T) {
	t.Parallel()
	resolved := loadProject(t, minimalProjectConfig+`execution:
  developer_slots:
    - routing:
        enabled: true
        primary: {provider: codex, model: gpt-6-sol}
        alternate: {model: gpt-6-astra}
agents:
  developer:
    effort: ""
`, nil)
	pair, err := resolved.ResolveDeveloperEndpoints(1, "developer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if pair.Primary.Effort != "medium" || pair.Alternate.Effort != "low" || pair.Primary.Endpoint.Same(pair.Alternate.Endpoint) {
		t.Fatalf("model defaults = %+v", pair)
	}
	if !strings.Contains(pair.Alternate.Origins["effort"], "model gpt-6-astra default") {
		t.Fatalf("default origin = %s", pair.Alternate.Origins["effort"])
	}
}

func TestExplicitModelClearsAnInheritedVersionPin(t *testing.T) {
	t.Parallel()
	resolved := loadProject(t, minimalProjectConfig+`execution:
  developer_slots:
    - routing:
        primary: {model: sonnet}
        alternate: {model: haiku}
agents:
  developer:
    model_version: claude-opus-4-6
`, nil)
	pair, err := resolved.ResolveDeveloperEndpoints(1, "developer", nil)
	if err != nil || pair.Primary.ModelVersion != "" || pair.Primary.Endpoint.Model != "sonnet" {
		t.Fatalf("explicit model inherited pin: %+v, %v", pair, err)
	}
}

func TestReviewerRejectsTheSameEndpointAfterResolvingAccountDefaults(t *testing.T) {
	t.Parallel()
	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  reviewer:
    account: default
    failover:
      model: opus
`, nil)
	if err == nil || !strings.Contains(err.Error(), "same endpoint") {
		t.Fatalf("resolved duplicate reviewer endpoint = %v", err)
	}
}

func TestRunAlternateEffortIsNotOfferedToConversationRoles(t *testing.T) {
	t.Parallel()
	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  development-manager:
    failover:
      model: sonnet
      effort: high
`, nil)
	if err == nil || !strings.Contains(err.Error(), "only for developer and reviewer") {
		t.Fatalf("unconsumed conversation effort accepted: %v", err)
	}
}

func TestLegacyLabelChoiceIsNotReinterpretedAsAnExplicitPair(t *testing.T) {
	t.Parallel()
	resolved := loadProject(t, minimalProjectConfig+`execution:
  developer_models:
    - {label: docs, model: sonnet}
agents:
  developer:
    model: opus
    failover: {enabled: true, model: sonnet}
`, nil)
	pair, err := resolved.ResolveDeveloperEndpoints(1, "developer", []string{"docs"})
	if err != nil || pair.Primary.Model != "sonnet" || pair.Explicit {
		t.Fatalf("legacy label model refused: %+v, %v", pair, err)
	}
}

func TestSlotRoutingCopiesAReplacementListAndItsNestedOptions(t *testing.T) {
	t.Parallel()
	document := mustDecodeDocument(t, minimalProjectConfig+`execution:
  developer_slots:
    - number: 1
      prefer: [reliability]
      routing:
        enabled: true
        primary: {model: opus, effort: high}
        alternate: {model: sonnet}
`)
	resolved, err := resolveLayers([]layer{{origin: "project", document: document}})
	if err != nil {
		t.Fatal(err)
	}
	slot := &(*document.Execution.DeveloperSlots)[0]
	*slot.Number = 2
	slot.Prefer[0] = "changed"
	slot.Routing.Primary.Model = "haiku"
	*slot.Routing.Primary.Effort = "low"
	slot.Routing.Alternate.Model = "haiku"
	kept := resolved.Config.Execution.DeveloperSlots[0]
	if *kept.Number != 1 || kept.Prefer[0] != "reliability" || kept.Routing.Primary.Model != "opus" || *kept.Routing.Primary.Effort != "high" || kept.Routing.Alternate.Model != "sonnet" {
		t.Fatalf("resolved slot shares document storage: %+v", kept)
	}
}

func TestDisabledRunAlternateStillValidatesItsExplicitEffort(t *testing.T) {
	t.Parallel()
	for _, block := range []string{
		"model: sonnet\n      effort: bananas",
		"effort: high",
	} {
		_, err := loadProjectError(t, minimalProjectConfig+`agents:
  developer:
    failover:
      enabled: false
      `+block+"\n", nil)
		if err == nil {
			t.Fatalf("invalid disabled alternate loaded: %s", block)
		}
	}
}
