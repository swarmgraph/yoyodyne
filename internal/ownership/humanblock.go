package ownership

import (
	"fmt"
	"strings"
)

// OperatorReason is the closed list of reasons a program manager's lane report
// may give for listing something under "Blocked on a human": a change of
// fundamental intent, or one of the physical acts PersonOnlyReason names. A
// decision a role could make is none of these; it is a defect to report, never
// an entry here (`docs/designs/program-manager.md`, under "The lane report").
type OperatorReason string

const (
	// OperatorFundamentalIntent is a change to the fundamental goals: the one
	// decision that is the operator's own.
	OperatorFundamentalIntent OperatorReason = "fundamental-intent"
	OperatorCredential        OperatorReason = OperatorReason(PersonCredential)
	OperatorRepositorySetting OperatorReason = OperatorReason(PersonRepositorySetting)
	OperatorProtectedFile     OperatorReason = OperatorReason(PersonProtectedFile)
)

// OperatorReasons is the whole list, in the order a refusal names it.
func OperatorReasons() []OperatorReason {
	return []OperatorReason{OperatorFundamentalIntent, OperatorCredential, OperatorRepositorySetting, OperatorProtectedFile}
}

// Valid reports whether a reason is on the list.
func (r OperatorReason) Valid() bool {
	for _, known := range OperatorReasons() {
		if r == known {
			return true
		}
	}
	return false
}

// Check refuses a reason that is not on the list, naming the list.
func (r OperatorReason) Check() error {
	if r.Valid() {
		return nil
	}
	names := make([]string, 0, len(OperatorReasons()))
	for _, known := range OperatorReasons() {
		names = append(names, string(known))
	}
	return fmt.Errorf("reason %q is not one only the operator can act on; the reasons are %s, and a decision a role could make is a defect to report rather than something blocked on a human", r, strings.Join(names, ", "))
}

// Says is the reason in words, for a line a person reads.
func (r OperatorReason) Says() string {
	switch r {
	case OperatorFundamentalIntent:
		return "a change to the fundamental goals, which only the operator decides"
	case OperatorCredential:
		return "a credential or account only a person holds"
	case OperatorRepositorySetting:
		return "a repository setting the harness's token cannot change"
	case OperatorProtectedFile:
		return "a file beyond every grant, which only a person may edit"
	}
	return "a reason that is not on the closed list"
}

// ResolveBlockedOnAHuman is who moves one "Blocked on a human" entry: the
// operator for a reason on the list, and otherwise the Lead Product Manager,
// who handles an entry that names the operator for no reason he alone can act
// on as the defect it is.
func ResolveBlockedOnAHuman(reason OperatorReason) Mover {
	if reason.Valid() {
		return MoverOperator
	}
	return MoverProductManager
}
