package config

import (
	"reflect"
	"strings"
	"testing"
)

// twoOperators binds two humans across all three namespaces with different
// grants, which is the shape the mapping exists for: authority is the human's,
// and several humans hold different amounts of it.
const twoOperators = `operators:
  mason:
    git_email: hamneggs@gmail.com
    forge_account: mason-bryant
    slack_member_id: U0BR9M2EKKP
    grants:
      - own-intent
      - direct-work
  jordan:
    git_email: jordan@example.com
    forge_account: jordan-q
    slack_member_id: W76543210
    grants:
      - direct-work
`

// The whole point of binding namespaces to one human is that an act arriving
// through any of them reaches the same authority. A commit carries an address, a
// push carries a forge account, and a thread reply carries a member id; none of
// them carries a person, so each has to resolve to one.
func TestAuthorityResolvesAHumanFromEveryBoundNamespace(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+twoOperators, nil).Config
	for _, arrival := range []struct {
		namespace  Namespace
		identifier string
	}{
		{NamespaceGitEmail, "hamneggs@gmail.com"},
		{NamespaceForgeAccount, "mason-bryant"},
		{NamespaceSlackMember, "U0BR9M2EKKP"},
	} {
		name, operator, found := cfg.ResolveOperator(arrival.namespace, arrival.identifier)
		if !found || name != "mason" {
			t.Fatalf("ResolveOperator(%s, %q) = %q, %v, want mason", arrival.namespace, arrival.identifier, name, found)
		}
		if !operator.Holds(GrantOwnIntent) {
			t.Errorf("%s reached mason without the grants attached to mason", arrival.namespace)
		}
		if !cfg.OperatorHolds(arrival.namespace, arrival.identifier, GrantOwnIntent) {
			t.Errorf("OperatorHolds(%s, ...) = false, want the human's authority whichever namespace asked", arrival.namespace)
		}
	}

	// And the same act through a namespace bound to somebody with less authority
	// gets that person's authority rather than the first one found.
	if cfg.OperatorHolds(NamespaceGitEmail, "jordan@example.com", GrantOwnIntent) {
		t.Error("jordan holds own-intent, which jordan was not granted")
	}
	if !cfg.OperatorHolds(NamespaceGitEmail, "jordan@example.com", GrantDirectWork) {
		t.Error("jordan does not hold direct-work, which jordan was granted")
	}
}

// An identifier nobody bound reaches nobody. That is what makes the mapping the
// whole of who the project recognizes rather than a list of exceptions to a
// default that lets everybody in.
func TestAnUnboundIdentifierResolvesToNobodyAndHoldsNothing(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+twoOperators, nil).Config
	for _, arrival := range []struct {
		namespace  Namespace
		identifier string
	}{
		{NamespaceGitEmail, "stranger@example.com"},
		{NamespaceForgeAccount, "stranger"},
		{NamespaceSlackMember, "U99999999"},
		// An empty identifier must not match the humans who bound nothing in that
		// namespace, which is the failure mode a naive comparison has.
		{NamespaceGitEmail, ""},
		{NamespaceSlackMember, "   "},
	} {
		if _, _, found := cfg.ResolveOperator(arrival.namespace, arrival.identifier); found {
			t.Errorf("ResolveOperator(%s, %q) found somebody", arrival.namespace, arrival.identifier)
		}
		if cfg.OperatorHolds(arrival.namespace, arrival.identifier, GrantDirectWork) {
			t.Errorf("OperatorHolds(%s, %q) = true, want an unbound identifier to hold nothing", arrival.namespace, arrival.identifier)
		}
	}

	// A human who bound only one namespace is unreachable through the others
	// rather than reachable through all of them.
	cfg = loadProject(t, minimalProjectConfig+`operators:
  mason:
    git_email: hamneggs@gmail.com
    grants:
      - direct-work
`, nil).Config
	if _, _, found := cfg.ResolveOperator(NamespaceSlackMember, "U0BR9M2EKKP"); found {
		t.Error("a member id nobody bound resolved to the human who bound only an address")
	}
}

// An address and a forge account are case-insensitive where they live, so
// somebody does not stop being themselves for capitalizing their own address. A
// member id is an opaque id the workspace issued and is compared exactly.
func TestIdentifiersAreComparedTheWayTheirOwnNamespaceCompares(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+twoOperators, nil).Config
	for _, arrival := range []struct {
		namespace  Namespace
		identifier string
	}{
		{NamespaceGitEmail, "HamnEggs@Gmail.com"},
		{NamespaceForgeAccount, "Mason-Bryant"},
	} {
		if name, _, found := cfg.ResolveOperator(arrival.namespace, arrival.identifier); !found || name != "mason" {
			t.Errorf("ResolveOperator(%s, %q) = %q, %v, want mason", arrival.namespace, arrival.identifier, name, found)
		}
	}
	if _, _, found := cfg.ResolveOperator(NamespaceSlackMember, "u0br9m2ekkp"); found {
		t.Error("a lowercased member id matched; member ids are opaque and compared exactly")
	}
}

// The allow-list is derived rather than authored: a list maintained beside the
// grants is a list that disagrees with them, silently, about authority.
func TestTheSlackAllowListIsDerivedFromDirectWorkGrants(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`slack:
  enabled: true
  channel: C0123456789
operators:
  mason:
    slack_member_id: U0BR9M2EKKP
    grants:
      - own-intent
      - direct-work
  jordan:
    slack_member_id: W76543210
    grants:
      - direct-work
  # Granted direct-work, but no member id bound: they hold the authority, and
  # Slack is not a boundary they can reach it through.
  ash:
    git_email: ash@example.com
    grants:
      - direct-work
  # A member id bound and nothing granted, which is how somebody is recognized
  # without being authorized.
  robin:
    slack_member_id: U11111111
`, nil).Config

	want := []string{"W76543210", "U0BR9M2EKKP"}
	got := cfg.SlackOperators()
	if len(got) != len(want) {
		t.Fatalf("SlackOperators() = %#v, want exactly the direct-work holders with a member id", got)
	}
	for _, member := range want {
		if !contains(got, member) {
			t.Errorf("SlackOperators() = %#v, missing %q", got, member)
		}
	}

	// Ordering follows the operator names, so the same mapping derives the same
	// list every time rather than whatever order a map was walked in.
	if second := cfg.SlackOperators(); !reflect.DeepEqual(got, second) {
		t.Fatalf("SlackOperators() = %#v then %#v, want a stable derivation", got, second)
	}
}

// Who the project recognizes is the wider reading of the same mapping: every
// human it names who bound a member id, whatever they were granted. The sink
// reads it to tell a colleague it may refuse for a missing grant from somebody
// it has never heard of, and the two lists must be derived from one mapping or
// they are two answers to who this project knows.
func TestEveryBoundMemberIsRecognizedWhateverTheyWereGranted(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`operators:
  mason:
    slack_member_id: U0BR9M2EKKP
    grants:
      - direct-work
  # Recognized and granted nothing, which is who this derivation exists for.
  robin:
    slack_member_id: U11111111
  # Granted direct-work with no member id bound: nothing arriving from Slack can
  # be recognized as them.
  ash:
    git_email: ash@example.com
    grants:
      - direct-work
`, nil).Config

	want := []string{"U0BR9M2EKKP", "U11111111"}
	if got := cfg.SlackMembers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SlackMembers() = %#v, want every bound member id in operator-name order: %#v", got, want)
	}
	if got := cfg.SlackOperators(); len(got) != 1 || got[0] != "U0BR9M2EKKP" {
		t.Fatalf("SlackOperators() = %#v, want recognizing somebody to have granted them nothing", got)
	}
	if names := cfg.OperatorNames(); !reflect.DeepEqual(names, []string{"ash", "mason", "robin"}) {
		t.Fatalf("OperatorNames() = %#v, want the humans a stranger would be told to reach out to", names)
	}
}

// A typo in an authority record is found at load or it is found as an act that
// silently resolved to nobody. Everything is checked whether or not anything
// reads the namespace yet, for the reason the Slack section is checked whether
// or not reporting is switched on.
func TestTheOperatorsMappingIsCheckedAtLoad(t *testing.T) {
	t.Parallel()

	for name, mapping := range map[string]struct {
		yaml string
		want string
	}{
		"a grant the harness does not have": {yaml: `operators:
  mason:
    git_email: mason@example.com
    grants:
      - admin
`, want: `unknown grant "admin"`},
		"a grant written twice": {yaml: `operators:
  mason:
    git_email: mason@example.com
    grants:
      - direct-work
      - direct-work
`, want: `repeats "direct-work"`},
		"a human bound to nothing": {yaml: `operators:
  mason:
    grants:
      - direct-work
`, want: "binds no identifier namespace"},
		"one address bound to two humans": {yaml: `operators:
  mason:
    git_email: shared@example.com
  jordan:
    git_email: SHARED@example.com
`, want: "already bound to operator"},
		"one member id bound to two humans": {yaml: `operators:
  mason:
    slack_member_id: U0BR9M2EKKP
  jordan:
    slack_member_id: U0BR9M2EKKP
`, want: "already bound to operator"},
		"an address that is not one": {yaml: `operators:
  mason:
    git_email: "mason at example dot com"
`, want: "must be a single email address"},
		"a forge account that is not one": {yaml: `operators:
  mason:
    forge_account: "@mason"
`, want: "must be a forge account name"},
		"a member id that is a display name": {yaml: `operators:
  mason:
    slack_member_id: "@mason"
`, want: "must be a Slack member id"},
		"an identifier longer than the limit": {yaml: `operators:
  mason:
    git_email: "` + strings.Repeat("m", MaxOperatorIdentifierBytes) + `@example.com"
`, want: "limit is"},
		"an operator name that is not an identifier": {yaml: `operators:
  Mason Bryant:
    git_email: mason@example.com
`, want: "operator name"},
		"two humans owning intent": {yaml: `operators:
  mason:
    git_email: mason@example.com
    grants:
      - own-intent
  jordan:
    git_email: jordan@example.com
    grants:
      - own-intent
`, want: "intent has one owner"},
	} {
		_, err := loadProjectError(t, minimalProjectConfig+mapping.yaml, nil)
		if err == nil {
			t.Errorf("LoadResolved() with %s = nil, want a refusal", name)
			continue
		}
		if !strings.Contains(err.Error(), mapping.want) {
			t.Errorf("LoadResolved() with %s error = %v, want it to name %q", name, err, mapping.want)
		}
	}
}

// The mapping says who may act, so a mapping half from one layer and half from
// another is not the set of humans either layer named. An override replaces it
// outright — the rule the checks follow, for a stronger reason — and records
// where it came from like every other configured value.
func TestTheOperatorsMappingIsReplacedRatherThanMerged(t *testing.T) {
	t.Parallel()

	// A project that names nobody recognizes nobody, and says so without failing
	// to load: the mapping is optional the way the Slack section is.
	empty := loadProject(t, minimalProjectConfig, nil)
	if len(empty.Config.Operators) != 0 {
		t.Fatalf("operators = %#v, want a project that named nobody to recognize nobody", empty.Config.Operators)
	}
	if _, recorded := empty.Origins["operators"]; recorded {
		t.Error("a mapping no layer supplied recorded an origin")
	}

	resolved := loadProject(t, minimalProjectConfig+twoOperators, nil)
	if len(resolved.Config.Operators) != 2 {
		t.Fatalf("operators = %#v, want both named humans", resolved.Config.Operators)
	}
	if origin := resolved.Origins["operators"]; origin == "" {
		t.Fatal("the mapping must record where it came from, like every other configured value")
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// A human binds a dashboard token of their own by naming where it is kept, and
// is granted deciding proposed changes by name. What is bound is a store and
// never a token, and `generated` is no store, so it is refused.
func TestAHumanBindsADashboardTokenAndTheGrantToDecideAmendments(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`operators:
  mason:
    git_email: hamneggs@gmail.com
    dashboard_token: keychain
    grants:
      - own-intent
      - decide-amendments
  jordan:
    dashboard_token: file
    grants:
      - direct-work
  sam:
    git_email: sam@example.com
`, nil).Config
	bindings := cfg.DashboardBindings()
	want := []DashboardBinding{
		{Operator: "jordan", Source: DashboardTokenFile, Grants: []Grant{GrantDirectWork}},
		{Operator: "mason", Source: DashboardTokenKeychain, Grants: []Grant{GrantOwnIntent, GrantDecideAmendments}},
	}
	if !reflect.DeepEqual(bindings, want) {
		t.Fatalf("DashboardBindings() = %+v, want %+v", bindings, want)
	}

	_, err := loadProjectError(t, minimalProjectConfig+`operators:
  mason:
    dashboard_token: generated
    grants:
      - own-intent
`, nil)
	if err == nil || !strings.Contains(err.Error(), "operators.mason.dashboard_token") {
		t.Fatalf("a generated dashboard token = %v, want it refused by key", err)
	}
}
