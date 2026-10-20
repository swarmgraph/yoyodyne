package ownership

import (
	"fmt"
	"strings"
)

// OperatorReason is the closed list of reasons the operator is named as the
// owner of anything: a waiting entry the registry resolves (registry.go), an
// escalation, a report handling, an ask, or an entry a program manager's lane
// report lists under "Blocked on a human". Each is a change of fundamental
// intent or an act only a person can perform. A decision a role could make is
// none of these; it is a defect to report, never something for him. The list is
// the architect's ruling of 2026-10-17 on the ownership design
// (`docs/designs/ownership-and-the-operator.md`, "The operator's closed list"),
// where repository-setting is the design's forge-setting and protected-file its
// beyond-grant; adding a reason is an amendment to the invariant
// the-operator-owns-only-what-only-he-can-do, never a code change on its own.
type OperatorReason string

const (
	// OperatorFundamentalIntent is a change to the fundamental goals: the one
	// decision that is the operator's own.
	OperatorFundamentalIntent OperatorReason = "fundamental-intent"
	OperatorCredential        OperatorReason = OperatorReason(PersonCredential)
	OperatorRepositorySetting OperatorReason = OperatorReason(PersonRepositorySetting)
	OperatorProtectedFile     OperatorReason = OperatorReason(PersonProtectedFile)
	// OperatorHumanGate is a step an item reserved for a person with a
	// `human-gate:` declaration.
	OperatorHumanGate OperatorReason = "human-gate"
	// OperatorOwnHold is lifting a hold or pause the operator placed himself.
	OperatorOwnHold OperatorReason = "own-hold"
	// OperatorDivergedHistory is saying which history is right when a target
	// branch diverged from the forge, which needs rights on the protected branch
	// that no role holds.
	OperatorDivergedHistory OperatorReason = "diverged-history"
)

// OperatorReasons is the whole list, in the order a refusal names it.
func OperatorReasons() []OperatorReason {
	return []OperatorReason{
		OperatorFundamentalIntent,
		OperatorCredential,
		OperatorRepositorySetting,
		OperatorProtectedFile,
		OperatorHumanGate,
		OperatorOwnHold,
		OperatorDivergedHistory,
	}
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
	case OperatorHumanGate:
		return "a step the work item reserves for a person"
	case OperatorOwnHold:
		return "lifting a hold the operator placed himself"
	case OperatorDivergedHistory:
		return "saying which history is right on a target branch that diverged from the forge, which needs rights on the protected branch no role holds"
	}
	return "a reason that is not on the closed list"
}

// ResolveBlockedOnAHuman is who moves one "Blocked on a human" entry: the
// operator for a reason on the list, and otherwise the Lead Product Manager,
// who handles an entry that names the operator for no reason he alone can act
// on as the defect it is.
func ResolveBlockedOnAHuman(reason OperatorReason) Mover {
	return Resolve(KindLaneHumanBlock, Record{Reason: reason}).Owner
}
