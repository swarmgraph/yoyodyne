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

// ConfirmedDocument is the immutable candidate the harness confirmed. Content
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
func (s Store) PrepareConfirmation(role domain.AgentRole, write Write, policy Policy, reason string, now time.Time) (ConfirmedDocument, bool, error) {
	if err := s.CheckWrite(role, write); err != nil {
		// An older waiting creation can already have been transcribed by hand.
		// Only the same owner-authored document can be confirmed in that case;
		// different content must return to its owner rather than be overwritten.
		if write.Action != WriteCreate {
			return ConfirmedDocument{}, false, err
		}
		set, loadErr := s.Load()
		if loadErr != nil {
			return ConfirmedDocument{}, false, loadErr
		}
		existing, found := set.Find(write.ID)
		if !found || existing.Kind != write.Kind || existing.Title != write.Title || path.Dir(existing.Path) != path.Clean(write.Directory) || !reflect.DeepEqual(existing.Supports, trimmedList(write.Supports)) {
			return ConfirmedDocument{}, false, err
		}
		_, body, readErr := s.loadOne(existing.ID)
		if readErr != nil || strings.TrimSpace(body) != strings.TrimSpace(write.Body) {
			return ConfirmedDocument{}, false, err
		}
		write.Action, write.Kind, write.Directory = WriteRevise, "", ""
		if err := s.CheckWrite(role, write); err != nil {
			return ConfirmedDocument{}, false, err
		}
	}
	var recorded Artifact
	var err error
	switch write.Action {
	case WriteCreate:
		recorded, _, err = s.prepareCreate(role, write.Draft(), now)
	default:
		recorded, _, err = s.prepareAmend(role, write.ID, write.Amendment(), now)
	}
	if err != nil {
		return ConfirmedDocument{}, false, err
	}
	name, mode, governed := policy.SettingFor(recorded)
	if !governed || mode != domain.ApprovalAutomatic {
		return ConfirmedDocument{}, false, nil
	}
	// Product intent cannot be approved by policy where its owner has not
	// recorded that this revision leaves fundamental intent unchanged. What makes
	// a document product intent is the setting that governs it, not its kind: the
	// non-goals, the operating rules, and anything filed in the specification
	// home are governed as the goals are.
	if IntentPolicy(name) {
		if !ConsistentIntentClaim(recorded.Revisions[len(recorded.Revisions)-1]) || role != domain.RoleProductManager {
			return ConfirmedDocument{}, false, nil
		}
	}
	recorded.Approvals = append(recorded.Approvals, Approval{Revision: len(recorded.Revisions) - 1, By: ApproverHarness, Policy: name, At: now.UTC(), Reason: reason})
	if err := recorded.Validate(); err != nil {
		return ConfirmedDocument{}, false, err
	}
	content, err := render(recorded, write.Body)
	if err != nil {
		return ConfirmedDocument{}, false, err
	}
	if len(content) > MaxFileBytes {
		return ConfirmedDocument{}, false, fmt.Errorf("confirmed document exceeds %d bytes", MaxFileBytes)
	}
	confirmed := ConfirmedDocument{Artifact: recorded, Content: content}
	prior, err := os.ReadFile(filepath.Join(s.RepositoryRoot, recorded.Path))
	if err == nil {
		confirmed.Before = DocumentIdentity(prior)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ConfirmedDocument{}, false, err
	}
	return confirmed, true, nil
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

// ConsistentIntentClaim reports whether a revision is an amendment its owner
// recorded as consistent with intent, directed by a work item.
func ConsistentIntentClaim(revision Revision) bool {
	_, directed := DirectingItem(revision.Reason)
	return revision.Action == ActionAmended && revision.Intent == IntentConsistent && directed
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
	if !ok || approval.By != ApproverHarness || approval.Revision != len(d.Artifact.Revisions)-1 {
		return errors.New("confirmed document needs harness confirmation for its last revision")
	}
	if IntentKind(d.Artifact.Kind) && !IntentPolicy(approval.Policy) {
		return fmt.Errorf("a %s document is product intent and cannot be confirmed under %s", d.Artifact.Kind, approval.Policy)
	}
	return nil
}
