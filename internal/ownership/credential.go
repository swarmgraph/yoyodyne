package ownership

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// What a step named the operator's under the credential reason needs.
//
// The operator's list is closed, so "credential" is the nearest fit for
// anything that needs access, and a role reaches for it for a check that needs
// only to read an outside system — a ticket tracker, a service that switches features on and off,
// another repository — which no person's secret is needed for: a role could do
// it if it were given read access. Not having that access is a capability the
// harness lacks, a defect for the Lead Product Manager to admit, and never the
// operator's to supply. So a record naming him under "credential" says which
// secret or account the step needs, a record whose step needs only read access
// is refused and recorded as a missing capability instead, and a record that
// does not say which is handed back to the role that raised it to say, rather
// than reaching him by default.

// CredentialNeed is what a step needs: a secret or an account only a person
// holds, which is the operator's, or read access a role lacks, which is not.
type CredentialNeed string

const (
	// CredentialSecret is a password, token, key, or an acceptance that needs
	// the machine owner's password, which only a person holds.
	CredentialSecret CredentialNeed = "secret"
	// CredentialAccount is an account only a person holds or can sign in to,
	// such as a provider login or a paid plan's limit.
	CredentialAccount CredentialNeed = "account"
	// CredentialReadAccess is reading an outside system that a role could read
	// itself if it had read access. It never names the operator.
	CredentialReadAccess CredentialNeed = "read-access"
)

// Credential is what a record naming the operator under "credential" says the
// step needs, and which secret or account that is.
type Credential struct {
	// Needs is the kind of thing the step needs.
	Needs CredentialNeed `json:"needs"`
	// What is the secret or account, named so a person knows which one: "the
	// admin password for xcodebuild -license", "the Claude Code login".
	What string `json:"what"`
}

// Validate refuses a credential that does not say what kind of thing the step
// needs, or does not name it on one line.
func (c Credential) Validate() error {
	var problems []error
	switch c.Needs {
	case CredentialSecret, CredentialAccount, CredentialReadAccess:
	default:
		problems = append(problems, fmt.Errorf("credential needs %q is not one of %q, %q, or %q", c.Needs, CredentialSecret, CredentialAccount, CredentialReadAccess))
	}
	if what := strings.TrimSpace(c.What); what == "" || len(what) > 4096 || strings.ContainsAny(what, "\r\n") {
		problems = append(problems, errors.New(`credential "what" must name the secret, account, or outside system on one line of at most 4096 bytes`))
	}
	return errors.Join(problems...)
}

// Says is the credential in words, for the line a person reads.
func (c Credential) Says() string {
	switch c.Needs {
	case CredentialSecret:
		return "a secret only a person holds: " + strings.TrimSpace(c.What)
	case CredentialAccount:
		return "an account only a person holds: " + strings.TrimSpace(c.What)
	}
	return "read access to " + strings.TrimSpace(c.What)
}

// MissingCapability is read access to an outside system that a role needs and
// does not have: a defect the Lead Product Manager admits, never something the
// operator is asked for.
type MissingCapability struct {
	// Role is the role that would read the system if it had read access.
	Role domain.AgentRole `json:"role"`
	// System is the outside system, named so the defect can say which.
	System string `json:"system"`
}

// Validate refuses a missing capability that does not name a role the harness
// knows and the system on one line.
func (m MissingCapability) Validate() error {
	var problems []error
	if !m.Role.Valid() {
		problems = append(problems, fmt.Errorf("missing_capability role %q is not a role the harness knows", m.Role))
	}
	if system := strings.TrimSpace(m.System); system == "" || len(system) > 4096 || strings.ContainsAny(system, "\r\n") {
		problems = append(problems, errors.New(`missing_capability "system" must name the outside system on one line of at most 4096 bytes`))
	}
	return errors.Join(problems...)
}

// Says is the missing capability in words.
func (m MissingCapability) Says() string {
	return fmt.Sprintf("the %s has no read access to %s", m.Role.Title(), strings.TrimSpace(m.System))
}

// CheckCredential holds a record naming the operator under "credential" to
// saying which secret or account the step needs. It refuses, with what to do
// instead, a credential given with another reason; one that says the step needs
// only read access, which is a missing capability for a role; and none at all,
// which leaves the harness unable to tell the two apart, so the role that
// raised it is asked rather than the operator named. A reason other than
// "credential" with no credential passes; CheckOperatorRouting holds the
// reason itself to the list.
func CheckCredential(reason OperatorReason, credential *Credential) error {
	if reason != OperatorCredential {
		if credential != nil {
			return fmt.Errorf(`"credential" is taken only with the reason %q, and this names %q`, OperatorCredential, reason)
		}
		return nil
	}
	if credential == nil {
		return fmt.Errorf(`naming the operator under %q needs "credential" saying which it is, and this says nothing, so the harness cannot tell a secret or account only a person holds from read access a role lacks: give {"needs":"secret" or "account","what":"the secret or account"} where only a person holds it, and where a role could do the step if it could read the outside system, it is not his: record it as a missing capability naming the role and the system, a defect for the Lead Product Manager to admit`, OperatorCredential)
	}
	if err := credential.Validate(); err != nil {
		return err
	}
	if credential.Needs == CredentialReadAccess {
		return fmt.Errorf(`reading %s needs only read access, which a role could be given and no person's secret is needed for, so it is not the operator's: record it as a missing capability naming the role that would read it and %s, a defect for the Lead Product Manager to admit`,
			strings.TrimSpace(credential.What), strings.TrimSpace(credential.What))
	}
	return nil
}
