package backend

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Both of the providers this build's vocabulary names are expressed as
// endpoints, each carrying the compiled adapter that reaches it. They are still
// different endpoints: what a role may be served on is decided by the postures a
// provider can be held to.
//
// This is where the second runnable endpoint is actually asserted. Until
// yoyodyne-ifd.347 there was none — the adapter written under yoyodyne-ifd.6 sat
// on a branch nothing merged, so a Codex endpoint was expressed and refused at
// dispatch for want of anything to launch it — and the failover work that
// generalizes across endpoints has no second endpoint to be proven on unless
// this holds.
func TestBothBuiltInProvidersAreExpressedAsEndpoints(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	claude, err := registry.Endpoint(domain.BackendClaudeCode, "default", "sonnet")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if !claude.Runnable() || claude.AdapterVersion != ClaudeCodeAdapterVersion {
		t.Fatalf("the Claude Code endpoint = %#v, want the compiled adapter's version", claude)
	}
	if err := registry.EligibleFor(claude, domain.RoleReviewer); err != nil {
		t.Fatalf("the reviewer on Claude Code = %v, want the endpoint that holds every posture", err)
	}

	codex, err := registry.Endpoint(domain.BackendCodex, "default", "gpt-5")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if !codex.Runnable() || codex.AdapterVersion != CodexAdapterVersion {
		t.Fatalf("the Codex endpoint = %#v, want the compiled adapter's version", codex)
	}
	if codex.Same(claude) {
		t.Fatalf("the two built-in endpoints share a key: %q", codex.Key())
	}
	if err := registry.EligibleFor(codex, domain.RoleReviewer); err != nil {
		t.Fatalf("reviewer on Codex: %v", err)
	}
	// The developer's posture Codex can hold, and this build can now launch it, so
	// nothing refuses that endpoint at all.
	if err := registry.EligibleFor(codex, domain.RoleDeveloper); err != nil {
		t.Fatalf("the developer on Codex = %v, want the endpoint this build can launch", err)
	}
}

// Registry eligibility agrees with the roles the native sandbox can serve.
func TestCodexServesEveryRoleByCapability(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if err := registry.Serves(domain.BackendCodex, domain.RoleDeveloper); err != nil {
		t.Fatalf("Serves(codex, developer) = %v, want the role Codex's sandbox can be held to", err)
	}
	for _, role := range domain.Roles() {
		if err := registry.Serves(domain.BackendCodex, role); err != nil {
			t.Errorf("Serves(codex, %q): %v", role, err)
		}
	}

	// And the description this build actually ships is that one: an adapter named
	// beside the version that identifies it, which is what makes a Codex endpoint
	// something a run can be dispatched to.
	landed, known := BuiltInDescriptor(domain.BackendCodex)
	if !known {
		t.Fatal("this build ships no description of the Codex backend")
	}
	if landed.Adapter != domain.BackendCodex || landed.AdapterVersion != CodexAdapterVersion {
		t.Fatalf("the Codex description = %#v, want the adapter this build ships", landed)
	}
	endpoint := Endpoint{
		Provider:       landed.ID,
		AdapterVersion: landed.AdapterVersion,
		AccountAlias:   "default",
		Model:          "gpt-5",
	}
	if !endpoint.Runnable() {
		t.Fatalf("a Codex endpoint carrying an adapter version = %#v, want one this build could launch", endpoint)
	}
	if refusal := landed.RoleRefusal(domain.RoleDeveloper); refusal != "" {
		t.Fatalf("RoleRefusal(developer) = %q, want the role Codex serves", refusal)
	}
	if refusal := landed.RoleRefusal(domain.RoleReviewer); refusal != "" {
		t.Fatalf("RoleRefusal(reviewer) = %q", refusal)
	}
}

// The adapter and its version are one fact stated twice, and they have to stay
// that way: an endpoint reports whether anything here can launch it from the
// version alone, and a descriptor that named an adapter without one would be an
// endpoint nothing could route that every check upstream believed was fine.
func TestEveryProviderThisBuildCanLaunchNamesItsAdapterVersion(t *testing.T) {
	t.Parallel()

	for _, descriptor := range BuiltInDescriptors() {
		if descriptor.Runnable() == (descriptor.AdapterVersion != "") {
			continue
		}
		t.Errorf("%q names adapter %q and adapter version %q; the two are set together",
			descriptor.ID, descriptor.Adapter, descriptor.AdapterVersion)
	}
}

// A provider a project declared is reached by an adapter this build ships, so
// its endpoints carry that adapter's version beside the provider's own name.
// That pair is what lets a record say which harness code read what a provider
// said, which the provider name alone cannot.
func TestADeclaredProviderCarriesTheAdapterVersionThatRunsIt(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(map[domain.Backend]ProviderPlugin{"my-harness": declaredPlugin()})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	endpoint, err := registry.Endpoint("my-harness", "second", "opus")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if endpoint.AdapterVersion != ClaudeCodeAdapterVersion {
		t.Fatalf("the declared provider's endpoint = %#v, want the version of the adapter it runs on", endpoint)
	}
	if endpoint.Provider != "my-harness" {
		t.Fatalf("Provider = %q, want the provider the project declared rather than the adapter", endpoint.Provider)
	}

	// A provider this project does not name has no endpoint at all: an identity
	// nothing could route or read back is refused where it is built.
	if _, err := registry.Endpoint("carrier-pigeon", "default", "opus"); err == nil {
		t.Fatal("Endpoint() accepted a provider this project does not name")
	}
}

// Eligibility is derived from what a provider declared rather than from its
// name, and the two discriminators are the ones capability validation already
// knows: the reviewer's no-tools posture and the developer's worktree-write.
func TestRoleEligibilityComesFromTheDeclaration(t *testing.T) {
	t.Parallel()

	developerOnly := declaredPlugin()
	developerOnly.Roles = []domain.AgentRole{domain.RoleDeveloper}
	developerOnly.Postures = []Posture{PostureWorktreeWrite}
	noWrites := declaredPlugin()
	noWrites.Postures = []Posture{PostureReadOnly}

	registry, err := NewRegistry(map[domain.Backend]ProviderPlugin{
		"developer-only": developerOnly,
		"no-writes":      noWrites,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	for _, test := range []struct {
		name     string
		provider domain.Backend
		role     domain.AgentRole
		want     string
	}{
		{name: "a provider that serves the role and holds its posture", provider: "developer-only", role: domain.RoleDeveloper},
		{name: "a role the provider does not serve", provider: "developer-only", role: domain.RoleReviewer, want: `does not support role "reviewer"`},
		{name: "a role whose posture the provider cannot hold", provider: "no-writes", role: domain.RoleDeveloper, want: `cannot hold the "worktree-write" tool access`},
		{name: "a name that is not a role at all", provider: "no-writes", role: "security-reviewer", want: "not one of the harness's roles"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := registry.Serves(test.provider, test.role)
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("Serves() = %v, want the role served", err)
			case test.want == "":
			case err == nil:
				t.Fatalf("Serves() accepted %q on %q, want a refusal naming %q", test.role, test.provider, test.want)
			case !strings.Contains(err.Error(), test.want):
				t.Fatalf("Serves() = %v, want a refusal naming %q", err, test.want)
			}
		})
	}
}

// A substitution is the one moment a role's endpoint changes without anybody
// having configured the change, so it is checked against the posture the role
// requires. Moving a reviewer onto a provider whose sandbox cannot express
// no-tools is refused with the posture and the endpoint both named.
func TestASubstitutionCannotMoveARoleOntoAnEndpointThatCannotHoldItsPosture(t *testing.T) {
	t.Parallel()

	writesOnly := declaredPlugin()
	writesOnly.Postures = []Posture{PostureWorktreeWrite}
	registry, err := NewRegistry(map[domain.Backend]ProviderPlugin{"writes-only": writesOnly})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	from, err := registry.Endpoint(domain.BackendClaudeCode, "default", "opus")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	to, err := registry.Endpoint("writes-only", "default", "opus")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}

	refusal := registry.Substitutable(from, to, domain.RoleReviewer)
	if refusal == nil {
		t.Fatal("Substitutable() moved a reviewer onto an endpoint that cannot hold the read-only posture")
	}
	for _, want := range []string{`"read-only"`, "writes-only", `role "reviewer"`} {
		if !strings.Contains(refusal.Error(), want) {
			t.Fatalf("Substitutable() = %v, want a refusal naming %s", refusal, want)
		}
	}

	// The same substitution for the role that endpoint can hold is permitted, so
	// what the check refuses is the posture and never the substitution itself.
	if err := registry.Substitutable(from, to, domain.RoleDeveloper); err != nil {
		t.Fatalf("Substitutable() = %v, want the developer moved onto an endpoint that holds worktree-write", err)
	}

	// Another model on the same endpoint is the substitution the harness makes
	// today, and it changes no posture at all.
	sameProvider := from
	sameProvider.Model = "sonnet"
	if err := registry.Substitutable(from, sameProvider, domain.RoleReviewer); err != nil {
		t.Fatalf("Substitutable() = %v, want another model on the same provider permitted", err)
	}
}

// The four fields are one identity. Two endpoints differing in any of them are
// different endpoints, which is what a pool keys on and what a record says.
func TestAnEndpointIsIdentifiedByAllFourOfItsFields(t *testing.T) {
	t.Parallel()

	base := Endpoint{Provider: domain.BackendClaudeCode, AdapterVersion: ClaudeCodeAdapterVersion, AccountAlias: "one", Model: "opus"}
	for _, test := range []struct {
		name  string
		other Endpoint
	}{
		{name: "another provider", other: Endpoint{Provider: "my-harness", AdapterVersion: ClaudeCodeAdapterVersion, AccountAlias: "one", Model: "opus"}},
		{name: "another adapter version", other: Endpoint{Provider: domain.BackendClaudeCode, AdapterVersion: ClaudeCodeAdapterVersion + "-other", AccountAlias: "one", Model: "opus"}},
		{name: "another account", other: Endpoint{Provider: domain.BackendClaudeCode, AdapterVersion: ClaudeCodeAdapterVersion, AccountAlias: "two", Model: "opus"}},
		{name: "another model", other: Endpoint{Provider: domain.BackendClaudeCode, AdapterVersion: ClaudeCodeAdapterVersion, AccountAlias: "one", Model: "sonnet"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if base.Same(test.other) {
				t.Fatalf("%s and %s read as one endpoint", base, test.other)
			}
		})
	}
	if !base.Same(base) {
		t.Fatal("an endpoint is not the same as itself")
	}

	// An endpoint that could not name what it is is refused where it is built.
	// The adapter version is not among the required three: an endpoint on a
	// provider this build cannot launch is a real endpoint refused at routing.
	for _, test := range []struct {
		name     string
		endpoint Endpoint
	}{
		{name: "no provider", endpoint: Endpoint{AccountAlias: "one", Model: "opus"}},
		{name: "no account", endpoint: Endpoint{Provider: domain.BackendClaudeCode, Model: "opus"}},
		{name: "no model", endpoint: Endpoint{Provider: domain.BackendClaudeCode, AccountAlias: "one"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.endpoint.Validate(); err == nil {
				t.Fatalf("Validate() accepted %#v", test.endpoint)
			}
		})
	}
	if err := (Endpoint{Provider: domain.BackendCodex, AccountAlias: "one", Model: "gpt-5"}).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want an endpoint on a provider with no adapter accepted and refused at routing", err)
	}
}

// What refuses a configuration and what refuses a substitution are one
// derivation. A second reading of the same declaration is what lets the two
// disagree, which is exactly the disagreement nobody would see until a window
// closed on a running system.
func TestTheRoleRefusalIsWhatConfigurationValidationReads(t *testing.T) {
	t.Parallel()

	descriptor, known := BuiltInDescriptor(domain.BackendCodex)
	if !known {
		t.Fatal("BuiltInDescriptor() found no Codex descriptor")
	}
	if refusal := descriptor.RoleRefusal(domain.RoleDeveloper); refusal != "" {
		t.Fatalf("RoleRefusal(developer) = %q, want the role Codex serves", refusal)
	}
	// A declaration that omits read-only is still refused for that posture.
	descriptor.Postures = []Posture{PostureWorktreeWrite}
	refusal := descriptor.RoleRefusal(domain.RoleReviewer)
	if !strings.Contains(refusal, `cannot hold the "read-only" tool access`) {
		t.Fatalf("RoleRefusal(reviewer) = %q, want the posture named", refusal)
	}
	// The posture is what the refusal names, rather than the role: sending an
	// operator to the roles list would be sending them to fix something that is
	// not wrong there.
	if strings.Contains(refusal, "does not support role") {
		t.Fatalf("RoleRefusal(reviewer) = %q, want the posture rather than the roles list", refusal)
	}
}

// The adapter version a record falls back to is the built-in's own, and a
// provider a project declared has none to fall back to. Both are stated rather
// than guessed: a record naming an adapter nothing established would read as
// evidence.
func TestTheRecordedAdapterVersionFallsBackOnlyToWhatThisBuildKnows(t *testing.T) {
	t.Parallel()

	if version := AdapterVersionFor(domain.BackendClaudeCode); version != ClaudeCodeAdapterVersion {
		t.Fatalf("AdapterVersionFor(claude-code) = %q, want the compiled adapter's version", version)
	}
	if version := AdapterVersionFor(domain.BackendCodex); version != CodexAdapterVersion {
		t.Fatalf("AdapterVersionFor(codex) = %q, want the compiled adapter's version", version)
	}
	if version := AdapterVersionFor("my-harness"); version != "" {
		t.Fatalf("AdapterVersionFor(my-harness) = %q, want nothing for a provider this build has no description of", version)
	}
}
