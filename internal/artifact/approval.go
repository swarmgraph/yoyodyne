package artifact

// The operator's approval of a document, recorded against the revision it was
// given for.
//
// An approval given in conversation is a fact about a person that nothing on
// disk holds. Everything downstream of the goals then has to treat an approved
// goal and a draft one as the same document, because they are the same document:
// the only difference between them lived in a chat log nobody reads
// programmatically. Recording it is what makes the difference readable, and
// recording it against a revision is what keeps it honest — an approval that
// named only the artifact would still read as current after the document was
// rewritten underneath it, which is the failure this exists to prevent.
//
// So an approval names the revision it was given for, the revision log is
// append-only, and an index into it always means the same change. An artifact
// amended after its approval is therefore distinguishable from one still
// approved, by arithmetic rather than by judgement: a revision that changed what
// the document says, and that the operator did not delegate, was recorded after
// the approved one. A revision that only gave its goals identifiers is not one
// of those (see identity.go), because it changed nothing the operator was asked
// to agree to. The one judgement in it is the owning role's, recorded on the
// revision itself: a change to a product document the Lead Product Manager
// records as consistent with intent is delegated, and the approval stands
// through it (see rewording.go).
//
// # What this deliberately does not do
//
// Nothing here moves a gate. An unapproved artifact still loads, still governs
// what is downstream of it, and stops nothing; an amendment after approval does
// not retire the document, change its status, or refuse anything. Approval and
// lifecycle status are kept apart on purpose — the status says what the document
// is to the product, and the approval says what the operator agreed to and
// when — because a record that silently deactivated a document the moment its
// owner corrected a sentence would be a gate nobody asked this to add.
//
// What reads this record can now make one thing turn on it. A project that has
// set `approvals.work_items` to automatic admits work to the queue without
// asking the operator where it traces to a goal an approved goals document
// states, so there an unapproved or amended-since goals document is one nothing
// is admitted under and the operator is asked per item instead. That gate lives
// where admission happens rather than here, it is off until a project turns it
// on, and this package still refuses nothing: what it does is answer, honestly,
// the question that gate asks.
//
// Who approves is not a role. Every artifact is drafted by the role that owns
// it, so an approval a role could record would be that role approving its own
// document; approval of what the product is for is the operator's, and it is
// recorded as theirs. That is also why recording one does not go through
// Authorize, which is the boundary between roles: the operator is not one of
// them, and holding the operator to a role's ownership would be the wrong
// boundary in the wrong direction.
//
// What requires approval at all is the project's decision rather than this
// package's. Policy carries the `approvals` configuration into the terms this
// package thinks in, so what needs approving is what the configuration says
// needs approving.

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Approver is who gave an approval. It is a closed set of one because the
// approval that matters here is a person's: a harness able to record itself as
// the approver would be approving the intent it was given, which is the one
// signature this record exists to carry.
type Approver string

// ApproverOperator is the person the harness works for.
const ApproverOperator Approver = "operator"

func (a Approver) Valid() bool { return a == ApproverOperator }

// Approval is one recorded approval: which revision of the document was
// approved, who approved it, when, and how the approval was given.
type Approval struct {
	// Revision is the index into the artifact's revision log that was approved.
	// It is an index rather than a copy of the revision because the log is
	// append-only: an index names one change for good, and a copy could disagree
	// with the entry it claims to be.
	Revision int      `yaml:"revision" json:"revision"`
	By       Approver `yaml:"by" json:"by"`
	// At is when the approval was recorded. That is usually when it was given and
	// is not always — an approval given in conversation is recorded afterwards —
	// so this is the time the harness can honestly attest to, and when it was
	// actually given belongs in the reason, where a person says it.
	At time.Time `yaml:"at" json:"at"`
	// Reason is how the approval was given and what it covered — the conversation
	// it was given in, the amendment it was given for. It is required, for the
	// same reason a revision's is and a stronger one: this record speaks for a
	// person, and an approval nobody can trace back to how they gave it is a claim
	// the harness made on their behalf.
	Reason string `yaml:"reason" json:"reason"`
}

// Validate reports every contract violation in the approval at once. Whether
// the revision it names exists is checked by the artifact, which is the only
// thing that holds the log the index points into.
func (a Approval) Validate() error {
	var problems []error
	if a.Revision < 0 {
		problems = append(problems, fmt.Errorf("revision %d is not an index into a revision log", a.Revision))
	}
	if !a.By.Valid() {
		problems = append(problems, fmt.Errorf("by %q must be %q; approval of what the product is for is the operator's", a.By, ApproverOperator))
	}
	if a.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	switch reason := strings.TrimSpace(a.Reason); {
	case reason == "":
		problems = append(problems, errors.New("reason is required, saying how the approval was given"))
	case len(reason) > MaxReasonBytes:
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(reason), MaxReasonBytes))
	}
	return errors.Join(problems...)
}

// ApprovalState is what a document's approval amounts to as it now stands. The
// three are separated because they are three different things to do: get the
// operator's approval, get it again for what changed, or nothing.
type ApprovalState string

const (
	// ApprovalUnapproved records no approval at all.
	ApprovalUnapproved ApprovalState = "unapproved"
	// ApprovalApproved is approved as it stands, under the operator's recorded
	// approval or the Lead Product Manager's delegated authority.
	ApprovalApproved ApprovalState = "approved"
	// ApprovalAmended was approved and has been amended since. The approval still
	// stands for what it was given for, and the document as it now reads is not
	// what was approved.
	ApprovalAmended ApprovalState = "amended"
)

// LatestApproval returns the most recent approval recorded against an artifact,
// and whether it has one. Approvals are recorded in the order they were given
// and each names a later revision than the one before it, so the last entry is
// the current one.
func (a Artifact) LatestApproval() (Approval, bool) {
	if len(a.Approvals) == 0 {
		return Approval{}, false
	}
	return a.Approvals[len(a.Approvals)-1], true
}

// ApprovalState reports whether the document as it now stands is what the
// operator approved.
//
// A revision that changed only goal identities does not count against the
// approval. It changed no goal's words, so the intent the operator agreed to is
// the intent the document still states, and reading it as amended-since would
// put every admission under the document back to the operator over a change to
// nothing they were asked about.
//
// Nor does a change the Lead Product Manager recorded as consistent with
// intent (see rewording.go). The goals admit and refuse the same work after it
// as before, which is what the operator approved, and reading it as
// amended-since would put every admission under the document back to the
// operator over a decision the operator's ruling of 2026-09-26 delegated. A
// creation recorded that way is approved under that delegation from the start.
func (a Artifact) ApprovalState() ApprovalState {
	if _, approved := a.approvedRevision(); !approved {
		return ApprovalUnapproved
	}
	if a.RevisionsSinceApproval() == 0 {
		return ApprovalApproved
	}
	return ApprovalAmended
}

// approvedRevision is the revision the document's approval rests on: either the
// operator's recorded approval or a creation recording already directed intent.
func (a Artifact) approvedRevision() (int, bool) {
	if latest, approved := a.LatestApproval(); approved {
		return latest.Revision, true
	}
	_, delegated := a.DelegatedCreation()
	return 0, delegated
}

// RevisionsSinceApproval counts the revisions recorded after the approved one
// that changed what the document says and that the operator did not delegate,
// which is how much of it has moved since the operator saw it. Identity
// revisions and delegated rewordings are not counted, for the reasons
// ApprovalState gives. It is zero for an artifact that is approved as it stands
// and for one that was never approved, because in neither case is there an
// approval something has drifted from.
func (a Artifact) RevisionsSinceApproval() int {
	latest, approved := a.approvedRevision()
	if !approved {
		return 0
	}
	count := 0
	for index := latest + 1; index < len(a.Revisions); index++ {
		revision := a.Revisions[index]
		if delegated, _ := a.Rewording(revision); revision.Action != ActionIdentified && !delegated {
			count++
		}
	}
	return count
}

// RewordingsSinceApproval returns the delegated rewordings recorded after the
// approved revision: what the Lead Product Manager changed under the operator's
// approval without asking again, which a surface names so the approval
// standing through them is visible rather than silent.
func (a Artifact) RewordingsSinceApproval() []Revision {
	latest, approved := a.approvedRevision()
	if !approved {
		return nil
	}
	var rewordings []Revision
	for index := latest + 1; index < len(a.Revisions); index++ {
		if delegated, _ := a.Rewording(a.Revisions[index]); delegated {
			rewordings = append(rewordings, a.Revisions[index])
		}
	}
	return rewordings
}

// approvalProblems reports what makes a recorded approval unusable: one that
// names a revision the artifact does not have, and one recorded out of order.
// Both are refusals rather than reports, unlike a revision recorded under the
// wrong role: an approval that points nowhere says nothing about the document
// at all, and reading it as an approval would be worse than reading it as
// missing.
func (a Artifact) approvalProblems() []error {
	var problems []error
	previous := -1
	for index, approval := range a.Approvals {
		if err := approval.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("approvals[%d]: %w", index, err))
		}
		switch {
		case approval.Revision < 0:
			// Already reported above, and nothing further can be said about an index
			// that is not one.
		case approval.Revision >= len(a.Revisions):
			problems = append(problems, fmt.Errorf("approvals[%d] approves revision %d, and this artifact records %d",
				index, approval.Revision, len(a.Revisions)))
		case approval.Revision <= previous:
			problems = append(problems, fmt.Errorf("approvals[%d] approves revision %d, which is not later than the revision approved before it; one revision is approved once, and approvals are recorded in the order they were given",
				index, approval.Revision))
		}
		if approval.Revision > previous {
			previous = approval.Revision
		}
	}
	return problems
}

// Policy is what a project requires the operator's approval of: its `approvals`
// configuration, in the terms this package thinks in. It is passed in rather
// than read here because what requires approval is the project's decision and
// this package's job is only to say what is recorded — which is also why an
// artifact can be approved whether or not anything required it, and why nothing
// here refuses an artifact for being unapproved.
type Policy struct {
	Brief   domain.ApprovalMode
	Goals   domain.ApprovalMode
	Designs domain.ApprovalMode
	// SpecificationsHome is product.specifications, repository-relative. Every
	// document filed there is authoritative product intent — the operator's
	// direction of 2026-09-27 — so one that is neither the brief nor the goals is
	// governed as the goals are rather than as a design would be. Empty leaves the
	// kind alone to decide.
	SpecificationsHome string
}

// SettingFor is Setting for one recorded document: its kind decides, save that
// a document filed in the specification home that the kind would otherwise leave
// to approvals.designs, or to nothing, is governed by approvals.goals. It is
// product intent by where it is filed, and approving the product's intent is
// what that setting says the operator does.
func (p Policy) SettingFor(recorded Artifact) (name string, mode domain.ApprovalMode, governed bool) {
	switch recorded.Kind {
	case KindBrief, KindGoals, KindNonGoals, KindRules:
		return p.Setting(recorded.Kind)
	}
	if p.inSpecificationsHome(recorded.Path) {
		return "approvals.goals", p.Goals, true
	}
	return p.Setting(recorded.Kind)
}

// RequiresFor is Requires for one recorded document, by SettingFor.
func (p Policy) RequiresFor(recorded Artifact) bool {
	_, mode, governed := p.SettingFor(recorded)
	return governed && mode == domain.ApprovalHuman
}

func (p Policy) inSpecificationsHome(documentPath string) bool {
	home := strings.Trim(path.Clean(filepath.ToSlash(strings.TrimSpace(p.SpecificationsHome))), "/")
	if home == "" || home == "." {
		return false
	}
	return strings.HasPrefix(path.Clean(filepath.ToSlash(documentPath)), home+"/")
}

// Setting returns the configured approval that governs a kind, named as it is
// written in the configuration, and whether one governs it at all.
func (p Policy) Setting(kind Kind) (name string, mode domain.ApprovalMode, governed bool) {
	switch kind {
	case KindBrief:
		return "approvals.brief", p.Brief, true
	case KindGoals, KindNonGoals:
		// One setting governs both. The configuration states one, and what a
		// project means by approving its goals is the pair: the non-goals are where
		// the goals stop, and a bound on intent that nobody approved is as much an
		// unapproved statement of intent as a goal is.
		return "approvals.goals", p.Goals, true
	case KindRules:
		// The operating rules are product intent filed beside the goals, so the
		// setting that governs the product's intent governs them too.
		return "approvals.goals", p.Goals, true
	case KindDesign, KindSpecification:
		return "approvals.designs", p.Designs, true
	default:
		// A decision record is the architect's account of how something was
		// decided rather than a statement of what the product should do, and no
		// approval setting names one. That is stated here rather than left to fall
		// out of a missing case: nothing requires approving it, deliberately.
		return "", "", false
	}
}

// Requires reports that the operator's approval of a kind is what this project
// asked for.
func (p Policy) Requires(kind Kind) bool {
	_, mode, governed := p.Setting(kind)
	return governed && mode == domain.ApprovalHuman
}

// Approve records the operator's approval of an artifact as it currently
// stands, against the last revision its log carries.
//
// It deliberately does not go through Authorize. That boundary is between the
// roles the harness runs, and the operator is not one of them: an approval an
// owning role could record would be that role approving its own document. What
// this writes is only the approval — the document's prose, its title, what it
// supports, and its status are untouched, so an approval can never become a way
// to edit a document by another name.
func (s Store) Approve(id, reason string, now time.Time) (Artifact, error) {
	existing, body, err := s.loadOne(id)
	if err != nil {
		return Artifact{}, err
	}
	if ended, hasEnding := existing.Ended(); hasEnding {
		return Artifact{}, fmt.Errorf("artifact %q was %s on %s and is not approved afterwards: %s",
			id, ended.Action, ended.At.Format(time.RFC3339), ended.Reason)
	}
	if strings.TrimSpace(reason) == "" {
		return Artifact{}, fmt.Errorf("approving artifact %q needs a reason saying how the approval was given; this record speaks for a person, and one that cannot be traced back to them is a claim made on their behalf", id)
	}
	revision := len(existing.Revisions) - 1
	if latest, approved := existing.LatestApproval(); approved && existing.ApprovalState() == ApprovalApproved {
		return Artifact{}, fmt.Errorf("artifact %q is already approved as it stands, at revision %d on %s: %s",
			id, latest.Revision, latest.At.Format(time.RFC3339), latest.Reason)
	}
	approved := existing
	approved.Approvals = append(append([]Approval(nil), existing.Approvals...), Approval{
		Revision: revision,
		By:       ApproverOperator,
		At:       now.UTC(),
		Reason:   strings.TrimSpace(reason),
	})
	if err := approved.Validate(); err != nil {
		return Artifact{}, err
	}
	if err := s.write(approved.Path, approved, body); err != nil {
		return Artifact{}, err
	}
	return approved, nil
}

// PendingCommit is what a surface says, as it records an approval, about where
// that write landed: in the checkout the write was pointed at, uncommitted, and
// the operator's to commit.
//
// It is a function here rather than prose at each call site because every
// surface that writes an approval owes the operator the same fact, and a second
// one wording it differently would be a second account of what the harness just
// did. Its callers are `yoyo artifact approve` and the typed artifact write a
// conversation performs once the operator approves the document.
//
// What it says is the settled shape rather than an apology for an unfinished
// one. The write stops at the working tree because the only ways into the target
// branch are a reviewed promotion and the operator's own hand — a harness-made
// commit would be a promotion by another name, sweeping in tree state nothing
// reviewed — and because stopping there keeps a document to two readings:
// committed, or an edit of the operator's that is visibly holding the runs up.
// The price of that is a dirty checkout, which every run over it then refuses to
// start over, and saying so at the moment of the write is what keeps the operator
// from meeting it as an unexplained refusal from whatever they run next.
//
// The checkout is named rather than called "yours", because which one it is is
// the configuration's answer rather than a constant: a `--config` naming another
// project, a `YOYODYNE_CONFIG` in the environment, and a linked worktree
// carrying its own `.yoyodyne` each point a write at a different repository. The
// run that refuses is a run over that repository, so a sentence saying "your
// checkout" would be right about the ordinary case and quietly wrong wherever
// the write did not land in the checkout the operator's runs come out of —
// which is the one case where an operator would go looking for a dirty file that
// is not there.
func PendingCommit(repositoryRoot, path string) string {
	return fmt.Sprintf("%s is now an uncommitted change in %s, and a run against that checkout refuses to start while it is; committing it is yours, under your own identity", path, repositoryRoot)
}
