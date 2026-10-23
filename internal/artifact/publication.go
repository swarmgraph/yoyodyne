package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ConfirmedDocument is the immutable candidate the harness confirmed or the
// operator approved, its last revision carrying that approval. Content
// includes generated metadata and the owner's prose; Before names the file it
// replaces so a later target cannot silently overwrite another revision.
type ConfirmedDocument struct {
	Artifact Artifact `json:"artifact"`
	Content  string   `json:"content"`
	Before   string   `json:"before,omitempty"`
}

func DocumentIdentity(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// PrepareConfirmation checks ownership and policy and renders the candidate
// without writing into the primary checkout. A false result retains the human
// confirmation path.
//
// A document of the product's intent answers to no approval setting. What makes
// a document product intent is the setting that governs it rather than its kind:
// the brief, the goals, the non-goals, the operating rules, and anything else
// filed in the specification home. One whose revision — a creation or an
// amendment — the Lead Product Manager recorded as consistent with intent,
// naming the work item that directed it, is approved as hers, because the
// operator's ruling of 2026-09-26 delegated that decision to her; it is not put
// to the operator however approvals.goals or approvals.brief is set. Any other
// revision of one, marked fundamental or not marked at all, is the operator's,
// however those settings are set: an automatic setting is not a way to approve a
// change of what the goals admit without him. Nor is the mark, where it cannot
// be right: creating the goals or the brief, and a revision of either that adds
// or removes a goal, are his however they are marked (markOverruled).
func (s Store) PrepareConfirmation(role domain.AgentRole, write Write, policy Policy, reason string, now time.Time) (ConfirmedDocument, bool, error) {
	recorded, body, err := s.prepareCandidate(role, write, now)
	if err != nil {
		return ConfirmedDocument{}, false, err
	}
	name, mode, governed := policy.SettingFor(recorded)
	if IntentPolicy(name) {
		revision := len(recorded.Revisions) - 1
		overruled, err := s.markOverruled(role, write, recorded, body)
		if err != nil {
			return ConfirmedDocument{}, false, err
		}
		if overruled != "" {
			return ConfirmedDocument{}, false, nil
		}
		recorded.Approvals = append(recorded.Approvals, Approval{Revision: revision, By: ApproverProductManager, At: now.UTC(), Reason: DelegatedApprovalReason(recorded.Revisions[revision])})
		confirmed, err := s.renderCandidate(recorded, body)
		if err != nil {
			return ConfirmedDocument{}, false, err
		}
		return confirmed, true, nil
	}
	if !governed || mode != domain.ApprovalAutomatic {
		return ConfirmedDocument{}, false, nil
	}
	recorded.Approvals = append(recorded.Approvals, Approval{Revision: len(recorded.Revisions) - 1, By: ApproverHarness, Policy: name, At: now.UTC(), Reason: reason})
	confirmed, err := s.renderCandidate(recorded, body)
	if err != nil {
		return ConfirmedDocument{}, false, err
	}
	return confirmed, true, nil
}

// ConsistentMarkOverruled says why a write its owner marked consistent with
// intent still waits for the operator, in a clause a person reads, and is empty
// for a write that is not so marked and for one whose mark is taken. It is what
// PrepareConfirmation decides by, offered apart so whoever is left waiting can
// be told why.
func (s Store) ConsistentMarkOverruled(role domain.AgentRole, write Write, now time.Time) (string, error) {
	if write.Intent != IntentConsistent {
		return "", nil
	}
	recorded, body, err := s.prepareCandidate(role, write, now)
	if err != nil {
		return "", err
	}
	if !IntentKind(recorded.Kind) {
		return "", nil
	}
	return s.markOverruled(role, write, recorded, body)
}

// markOverruled is why a prepared revision of the product's intent is not the
// Lead Product Manager's to approve, or empty where it is. Beyond what the
// revision records (Rewording), it reads what the revision does to the goals:
// creating a document that states them, or adding or removing one, is a change
// of what the goals admit whatever the mark says.
//
// The write's own action is read as well as the revision's, because a creation
// that was already transcribed by hand is recorded as an amendment of the copy
// on disk (prepareCandidate), and that copy is one the operator has not
// approved either.
func (s Store) markOverruled(role domain.AgentRole, write Write, recorded Artifact, body string) (string, error) {
	revision := recorded.Revisions[len(recorded.Revisions)-1]
	if delegated, why := recorded.Rewording(revision); !delegated {
		return why, nil
	}
	if role != domain.RoleProductManager {
		return fmt.Sprintf("it was written by the %s, and only the %s's own change is approved as hers", role.Title(), domain.RoleProductManager.Title()), nil
	}
	if !StatesGoals(recorded.Kind) {
		return "", nil
	}
	if write.Action == WriteCreate {
		return createdGoalsReason(recorded.Kind), nil
	}
	_, before, err := s.loadOne(recorded.ID)
	if err != nil {
		return "", err
	}
	if change := GoalChange(before, body); change != "" {
		return change + ", and adding or removing a goal changes what the goals admit, so it is the operator's to approve whatever it is marked", nil
	}
	return "", nil
}

// PrepareApproval renders the candidate an operator approved, with their
// approval recorded against the revision it produces, without writing into the
// primary checkout. It is PrepareConfirmation for a document a person decided
// rather than a policy, so it answers to no approval setting: the operator may
// approve any revision its owner may write, a change of fundamental intent
// included. The candidate lands the way a confirmed one does, through a reviewed
// run, so an approval never leaves the checkout holding a change nobody
// committed.
func (s Store) PrepareApproval(role domain.AgentRole, write Write, reason string, now time.Time) (ConfirmedDocument, error) {
	recorded, body, err := s.prepareCandidate(role, write, now)
	if err != nil {
		return ConfirmedDocument{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return ConfirmedDocument{}, fmt.Errorf("approving artifact %q needs a reason saying how the approval was given", recorded.ID)
	}
	recorded.Approvals = append(recorded.Approvals, Approval{Revision: len(recorded.Revisions) - 1, By: ApproverOperator, At: now.UTC(), Reason: strings.TrimSpace(reason)})
	return s.renderCandidate(recorded, body)
}

// prepareCandidate checks the role's authority over a write and records the
// revision it would make, returning the document and its body without writing
// either.
func (s Store) prepareCandidate(role domain.AgentRole, write Write, now time.Time) (Artifact, string, error) {
	if err := s.CheckWrite(role, write); err != nil {
		// An older waiting creation can already have been transcribed by hand.
		// Only the same owner-authored document can be confirmed in that case;
		// different content must return to its owner rather than be overwritten.
		if write.Action != WriteCreate {
			return Artifact{}, "", err
		}
		set, loadErr := s.Load()
		if loadErr != nil {
			return Artifact{}, "", loadErr
		}
		existing, found := set.Find(write.ID)
		if !found || existing.Kind != write.Kind || existing.Title != write.Title || path.Dir(existing.Path) != path.Clean(write.Directory) || !reflect.DeepEqual(existing.Supports, trimmedList(write.Supports)) {
			return Artifact{}, "", err
		}
		_, body, readErr := s.loadOne(existing.ID)
		if readErr != nil || strings.TrimSpace(body) != strings.TrimSpace(write.Body) {
			return Artifact{}, "", err
		}
		write.Action, write.Kind, write.Directory = WriteRevise, "", ""
		if err := s.CheckWrite(role, write); err != nil {
			return Artifact{}, "", err
		}
	}
	var recorded Artifact
	var err error
	body := write.Body
	switch write.Action {
	case WriteCreate:
		recorded, _, err = s.prepareCreate(role, write.Draft(), now)
	default:
		var amended string
		recorded, amended, err = s.prepareAmend(role, write.ID, write.Amendment(), now)
		// A section revision is the document as the checkout holds it with one
		// section replaced, which only the store's splice knows; a whole revision
		// is the owner's body exactly as written.
		if strings.TrimSpace(write.Section) != "" {
			body = amended
		}
	}
	if err != nil {
		return Artifact{}, "", err
	}
	return recorded, body, nil
}

// renderCandidate is the file a prepared document with its approval recorded
// would be, bound to the file it replaces.
func (s Store) renderCandidate(recorded Artifact, body string) (ConfirmedDocument, error) {
	if err := recorded.Validate(); err != nil {
		return ConfirmedDocument{}, err
	}
	content, err := render(recorded, body)
	if err != nil {
		return ConfirmedDocument{}, err
	}
	if len(content) > MaxFileBytes {
		return ConfirmedDocument{}, fmt.Errorf("confirmed document exceeds %d bytes", MaxFileBytes)
	}
	confirmed := ConfirmedDocument{Artifact: recorded, Content: content}
	prior, err := os.ReadFile(filepath.Join(s.RepositoryRoot, recorded.Path))
	if err == nil {
		confirmed.Before = DocumentIdentity(prior)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ConfirmedDocument{}, err
	}
	return confirmed, nil
}

// DelegatedApprovalReason is the reason the Lead Product Manager's approval of
// a revision records: the work item that directed it first, so the approval
// names it the way her revision does, then whose decision it was and why it was
// hers. The reason given for a harness confirmation is not carried into it,
// because it says the approval came from a setting, which this one did not.
func DelegatedApprovalReason(revision Revision) string {
	item, _ := DirectingItem(revision.Reason)
	change := "amendment"
	if revision.Action == ActionCreated {
		change = "creation"
	}
	return fmt.Sprintf("%s - approved by the %s, who recorded this %s as consistent with intent; a change consistent with the goals is hers to approve under the operator's ruling of 2026-09-26, so he was not asked",
		item, domain.RoleProductManager.Title(), change)
}

// OperatorApproved reports a candidate the operator approved, rather than one
// the harness confirmed under an automatic approval setting.
func (d ConfirmedDocument) OperatorApproved() bool {
	approval, ok := d.Artifact.LatestApproval()
	return ok && approval.By == ApproverOperator
}

// ProductManagerApproved reports a candidate the Lead Product Manager approved
// as a change consistent with intent, which the operator delegated to her.
func (d ConfirmedDocument) ProductManagerApproved() bool {
	approval, ok := d.Artifact.LatestApproval()
	return ok && approval.By == ApproverProductManager
}

// Decided reports a candidate whose approval is somebody's decision rather than
// a setting's: the operator's, or the Lead Product Manager's under the authority
// he delegated to her. Such a candidate answers to no approval setting, and goes
// through a reviewed run whatever the integration setting, left for the
// operator to merge where integration is not automatic.
func (d ConfirmedDocument) Decided() bool {
	return d.OperatorApproved() || d.ProductManagerApproved()
}

// IntentPolicy reports whether an approval setting governs the product's
// intent. A document it governs is confirmed by policy only on its owner's
// recorded claim that the revision leaves fundamental intent unchanged.
func IntentPolicy(name string) bool {
	return name == "approvals.goals" || name == "approvals.brief"
}

// IntentKind reports whether a kind is product intent wherever it is filed.
func IntentKind(kind Kind) bool {
	switch kind {
	case KindBrief, KindGoals, KindNonGoals, KindRules:
		return true
	}
	return false
}

// ConsistentIntentClaim reports whether a revision is a creation or an
// amendment its owner recorded as consistent with intent, directed by a work
// item.
func ConsistentIntentClaim(revision Revision) bool {
	_, directed := DirectingItem(revision.Reason)
	return (revision.Action == ActionAmended || revision.Action == ActionCreated) && revision.Intent == IntentConsistent && directed
}

// Validate checks that the saved bytes and saved identity describe one file.
func (d ConfirmedDocument) Validate() error {
	var problems []error
	if len(d.Content) > MaxFileBytes {
		problems = append(problems, errors.New("confirmed content exceeds the artifact file limit"))
	}
	if d.Before != "" {
		decoded, err := hex.DecodeString(d.Before)
		if err != nil || len(decoded) != sha256.Size {
			problems = append(problems, errors.New("confirmed document has an invalid prior content identity"))
		}
	}
	if err := d.Artifact.Validate(); err != nil {
		problems = append(problems, err)
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	parsed, err := parse(d.Content)
	if err != nil {
		return err
	}
	parsed.Path = d.Artifact.Path
	if !reflect.DeepEqual(parsed, d.Artifact) {
		return errors.New("confirmed content and artifact identity disagree")
	}
	if path.Clean(d.Artifact.Path) != d.Artifact.Path || strings.HasPrefix(d.Artifact.Path, "../") || path.IsAbs(d.Artifact.Path) || path.Base(d.Artifact.Path) != d.Artifact.ID+".md" {
		return errors.New("confirmed document has an invalid path")
	}
	approval, ok := d.Artifact.LatestApproval()
	if !ok || !approval.By.Valid() || approval.Revision != len(d.Artifact.Revisions)-1 {
		return errors.New("confirmed document needs the operator's or the Lead Product Manager's approval, or the harness's confirmation, for its last revision")
	}
	if approval.By == ApproverHarness && IntentKind(d.Artifact.Kind) && !IntentPolicy(approval.Policy) {
		return fmt.Errorf("a %s document is product intent and cannot be confirmed under %s", d.Artifact.Kind, approval.Policy)
	}
	return nil
}
