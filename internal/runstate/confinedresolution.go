package runstate

// A conflict resolution confined to the conflicting files, decision 4 of
// docs/designs/approved-changes-that-no-longer-merge.md and "A resolved change
// whose approval was of an earlier head" in
// docs/designs/integration-through-a-merge-queue.md.
//
// An approved change handed back because it no longer merged is resolved in a
// new session. The harness compares the resolved head with Git's own merge of
// the approved head onto the same target. Where every file outside the
// conflicting ones is exactly as that merge leaves it, the resolution is
// confined, and once its checks pass it rejoins the merge queue on the approval
// of the head it resolves, with no review in between: the queue's candidate is
// reviewed whole before it lands, by a reviewer who is none of its authors and
// who is shown the conflicts and how each was resolved. The run records the
// resolution here (State.ConflictResolution), and the queue's entry records its
// two heads apart (MergeQueueEntry.ResolvedHead), so nothing calls the
// resolved head approved before the candidate's review approves it.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxResolutionDiffBytes bounds the diff of how the conflicts were resolved
// that a run's record keeps for the candidate's reviewer.
const MaxResolutionDiffBytes = 48 << 10

// maxMergeQueueResolutionPaths bounds the conflicting files an entry names;
// the run's record keeps more (MaxConflictedPaths).
const maxMergeQueueResolutionPaths = 20

// ConflictResolution is a run's resolution of a conflict handed back to its
// approved change, recorded when the resolution's checks passed.
type ConflictResolution struct {
	// Handback is the hand-back whose conflict was resolved (ConflictHandback).
	Handback string `json:"handback,omitempty"`
	// Approved is the head the reviewer approved, with that review.
	Approved ApprovedChange `json:"approved"`
	// TargetCommit is the target the resolution was built on: the same main the
	// approved head is merged onto for the comparison.
	TargetCommit string `json:"target_commit"`
	// ResolvedHead is the commit the resolving session's attempt produced,
	// whose checks passed.
	ResolvedHead string `json:"resolved_head"`
	// Attempt is the repair attempt the checks passed on.
	Attempt    int       `json:"attempt"`
	RecordedAt time.Time `json:"recorded_at"`
	// Comparison is the harness's comparison with Git's merge, absent until it
	// is made.
	Comparison *ResolutionComparison `json:"comparison,omitempty"`
}

// ResolutionComparison is what comparing a resolution with Git's merge of the
// approved head onto the same target found.
type ResolutionComparison struct {
	ComparedAt time.Time `json:"compared_at"`
	// Problem is why the comparison could not be made. A comparison that could
	// not be made never finds a resolution confined.
	Problem string `json:"problem,omitempty"`
	// MergedTree is the tree Git's merge leaves, conflict markers and all.
	MergedTree string `json:"merged_tree,omitempty"`
	// Conflicting are the files Git's merge could not settle, bounded, with
	// how many more there were.
	Conflicting        []string `json:"conflicting,omitempty"`
	ConflictingOmitted int      `json:"conflicting_omitted,omitempty"`
	// Outside are the files outside the conflicting ones that the resolution
	// left otherwise than Git's merge did, bounded, with how many more.
	Outside        []string `json:"outside,omitempty"`
	OutsideOmitted int      `json:"outside_omitted,omitempty"`
	// Resolutions is the diff from Git's merge to the resolved head over the
	// conflicting files, bounded by MaxResolutionDiffBytes.
	Resolutions          string `json:"resolutions,omitempty"`
	ResolutionsTruncated bool   `json:"resolutions_truncated,omitempty"`
}

// Confined reports a comparison that was made and found every file outside
// the conflicting ones exactly as Git's merge leaves it.
func (c ResolutionComparison) Confined() bool {
	return c.Problem == "" && !c.ComparedAt.IsZero() && commitPattern.MatchString(c.MergedTree) && len(c.Outside) == 0 && c.OutsideOmitted == 0
}

// Says is the comparison in a sentence.
func (c ResolutionComparison) Says() string {
	switch {
	case c.Problem != "":
		return "the comparison with Git's own merge could not be made: " + c.Problem
	case c.Confined():
		return fmt.Sprintf("every file outside the %s is exactly as Git's own merge of the approved head leaves it", conflictingSays(len(c.Conflicting)+c.ConflictingOmitted))
	}
	outside := strings.Join(c.Outside, ", ")
	if c.OutsideOmitted > 0 {
		outside += fmt.Sprintf(" and %d more", c.OutsideOmitted)
	}
	return "the resolution also changed files Git's own merge settled by itself: " + outside
}

func conflictingSays(count int) string {
	if count == 1 {
		return "one conflicting file"
	}
	return fmt.Sprintf("%d conflicting files", count)
}

// Confined reports a resolution whose comparison found it confined to the
// conflicting files.
func (r *ConflictResolution) Confined() bool {
	return r != nil && r.Comparison != nil && r.Comparison.Confined()
}

func (s State) validateConflictResolution() []error {
	// The free texts are bounded where every run record text is
	// (State.recordedTexts).
	r := s.ConflictResolution
	if r == nil {
		return nil
	}
	var problems []error
	for _, commit := range []struct{ field, value string }{
		{"approved head", r.Approved.HeadCommit}, {"target commit", r.TargetCommit}, {"resolved head", r.ResolvedHead},
	} {
		if !commitPattern.MatchString(commit.value) {
			problems = append(problems, fmt.Errorf("conflict_resolution: %s %q is not a full commit id", commit.field, commit.value))
		}
	}
	if r.Attempt < 0 || r.RecordedAt.IsZero() {
		problems = append(problems, errors.New("conflict_resolution: records the attempt and when its checks passed"))
	}
	if c := r.Comparison; c != nil {
		switch {
		case c.ComparedAt.IsZero():
			problems = append(problems, errors.New("conflict_resolution: a comparison records when it was made"))
		case c.Problem == "" && !commitPattern.MatchString(c.MergedTree):
			problems = append(problems, fmt.Errorf("conflict_resolution: merged tree %q is not a full object id", c.MergedTree))
		case len(c.Conflicting) > MaxConflictedPaths || len(c.Outside) > MaxConflictedPaths:
			problems = append(problems, errors.New("conflict_resolution: the comparison carries more than a run's record keeps"))
		case c.ConflictingOmitted < 0 || c.OutsideOmitted < 0:
			problems = append(problems, errors.New("conflict_resolution: omitted counts are never negative"))
		}
	}
	return problems
}

// MergeQueueResolution is what an entry admitted on the approval of the head
// it resolves records of the comparison that found its resolution confined.
type MergeQueueResolution struct {
	// ApprovedBy is the review session that approved the approved head.
	ApprovedBy string `json:"approved_by"`
	// TargetCommit is the target the resolution was built on and the approved
	// head was merged onto for the comparison.
	TargetCommit string `json:"target_commit"`
	// MergedTree is the tree Git's merge left.
	MergedTree string `json:"merged_tree"`
	// Conflicting are the files Git's merge could not settle, bounded, and how
	// many more there were.
	Conflicting        []string  `json:"conflicting"`
	ConflictingOmitted int       `json:"conflicting_omitted,omitempty"`
	ComparedAt         time.Time `json:"compared_at"`
}

// NewMergeQueueResolution is the entry's record of a run's confined
// resolution.
func NewMergeQueueResolution(r ConflictResolution) MergeQueueResolution {
	c := *r.Comparison
	kept := c.Conflicting[:min(len(c.Conflicting), maxMergeQueueResolutionPaths)]
	return MergeQueueResolution{
		ApprovedBy: r.Approved.ReviewSessionID, TargetCommit: r.TargetCommit, MergedTree: c.MergedTree,
		Conflicting: append([]string(nil), kept...), ConflictingOmitted: c.ConflictingOmitted + len(c.Conflicting) - len(kept),
		ComparedAt: c.ComparedAt.UTC(),
	}
}

func (r MergeQueueResolution) validate() []error {
	var problems []error
	if err := mergeQueueText("approving review session", r.ApprovedBy, true); err != nil {
		problems = append(problems, err)
	}
	for _, object := range []struct{ field, value string }{{"resolution's target", r.TargetCommit}, {"merged tree", r.MergedTree}} {
		if !commitPattern.MatchString(object.value) {
			problems = append(problems, fmt.Errorf("%s %q is not a full object id", object.field, object.value))
		}
	}
	if len(r.Conflicting) > maxMergeQueueResolutionPaths || r.ConflictingOmitted < 0 {
		problems = append(problems, errors.New("the resolution names more conflicting files than an entry keeps"))
	}
	for _, path := range r.Conflicting {
		if err := mergeQueueText("conflicting file", path, true); err != nil {
			problems = append(problems, err)
		}
	}
	if r.ComparedAt.IsZero() {
		problems = append(problems, errors.New("the time of the comparison is required"))
	}
	return problems
}

func (r *MergeQueueResolution) same(other *MergeQueueResolution) bool {
	if r == nil || other == nil {
		return r == nil && other == nil
	}
	return r.ApprovedBy == other.ApprovedBy && r.TargetCommit == other.TargetCommit && r.MergedTree == other.MergedTree
}

// Head is the commit the entry's candidate is built from and lands: its
// resolved head where it has one, and its approved head otherwise. Wherever the
// queue speaks of an entry's admitted head, this is meant.
func (e MergeQueueEntry) Head() string {
	if e.ResolvedHead != "" {
		return e.ResolvedHead
	}
	return e.ApprovedHead
}

// HeadSays names the entry's head for a person, without calling a resolved
// head approved: no reviewer has judged it until the candidate's review does.
func (e MergeQueueEntry) HeadSays() string {
	return headSays(e.ApprovedHead, e.ResolvedHead)
}

// Head is the commit the run's entry was admitted to be built from, as
// MergeQueueEntry.Head.
func (r MergeQueueAdmissionRecord) Head() string {
	if r.ResolvedHead != "" {
		return r.ResolvedHead
	}
	return r.ApprovedHead
}

// HeadSays names the run's admitted head as MergeQueueEntry.HeadSays does.
func (r MergeQueueAdmissionRecord) HeadSays() string {
	return headSays(r.ApprovedHead, r.ResolvedHead)
}

func headSays(approved, resolved string) string {
	if resolved == "" {
		return "approved head " + approved
	}
	return fmt.Sprintf("resolved head %s, which no reviewer has approved yet, resolving the conflict of approved head %s", resolved, approved)
}

func (e MergeQueueEntry) validateResolution() []error {
	switch {
	case e.ResolvedHead == "" && e.Resolution == nil:
		return nil
	case e.ResolvedHead == "" || e.Resolution == nil:
		return []error{errors.New("an entry admitted on the approval of the head it resolves records both its resolved head and the comparison")}
	case !commitPattern.MatchString(e.ResolvedHead):
		return []error{fmt.Errorf("resolved head %q is not a full commit id", e.ResolvedHead)}
	case e.ResolvedHead == e.ApprovedHead:
		return []error{errors.New("a resolved head is a commit apart from the approved head")}
	}
	return e.Resolution.validate()
}

// Head is the commit the hand-back's entry was built from and the run's record
// holds: its resolved head where it has one, and its approved head otherwise.
func (h MergeQueueHandback) Head() string {
	if h.ResolvedHead != "" {
		return h.ResolvedHead
	}
	return h.ApprovedHead
}
