package ownership

import (
	"strings"
	"testing"
)

// A record naming the operator under "credential" says which secret or account
// the step needs; one that needs only read access, or says nothing, is refused,
// and a credential is taken with no other reason.
func TestCheckCredential(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		reason     OperatorReason
		credential *Credential
		refused    string
	}{
		{name: "a secret", reason: OperatorCredential, credential: &Credential{Needs: CredentialSecret, What: "the admin password"}},
		{name: "an account", reason: OperatorCredential, credential: &Credential{Needs: CredentialAccount, What: "the provider login"}},
		{name: "another reason with none", reason: OperatorProtectedFile},
		{name: "read access", reason: OperatorCredential, credential: &Credential{Needs: CredentialReadAccess, What: "the ticket tracker"}, refused: "missing capability"},
		{name: "saying nothing", reason: OperatorCredential, refused: "cannot tell"},
		{name: "an unknown need", reason: OperatorCredential, credential: &Credential{Needs: "write-access", What: "x"}, refused: "is not one of"},
		{name: "naming nothing", reason: OperatorCredential, credential: &Credential{Needs: CredentialSecret, What: " "}, refused: "must name"},
		{name: "another reason with one", reason: OperatorRepositorySetting, credential: &Credential{Needs: CredentialSecret, What: "x"}, refused: "taken only with"},
	} {
		err := CheckCredential(testCase.reason, testCase.credential)
		if testCase.refused == "" {
			if err != nil {
				t.Errorf("%s: CheckCredential() = %v, want it to pass", testCase.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), testCase.refused) {
			t.Errorf("%s: CheckCredential() = %v, want it refused saying %q", testCase.name, err, testCase.refused)
		}
	}
}

func TestMissingCapabilityNamesARoleAndASystem(t *testing.T) {
	t.Parallel()

	if err := (MissingCapability{Role: "developer", System: "the ticket tracker"}).Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if err := (MissingCapability{Role: "nobody", System: ""}).Validate(); err == nil ||
		!strings.Contains(err.Error(), "not a role") || !strings.Contains(err.Error(), "must name") {
		t.Fatalf("Validate() = %v, want both the role and the system refused", err)
	}
}
