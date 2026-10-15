package runstate

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// maxDocumentConflictPathBytes bounds the recorded path, which is structured
// rather than free text and so is refused rather than cut when it is longer.
const maxDocumentConflictPathBytes = 4096

// DocumentConflict is a document run finding, as it cut its checkout from the
// target branch, that the branch's copy of the document is no longer the copy
// the confirmed text was written against. Something else changed that copy
// after the text was written — usually a later revision of the same document —
// and waiting does not change that, so the run is never followed by another
// attempt of the same text. It is the development manager's to decide, and
// this record is what she decides from: which document, the commit whose copy
// the text was written against, and the commit the branch now stands at.
//
// It is written before the run's failure is, so the stop and its evidence are
// one save. It is not the replay conflict an approved change meets at its
// promotion (ReplayConflict): nothing has reviewed this text yet, and no
// developer is asked to reconcile it.
type DocumentConflict struct {
	// Path is the repository-relative file of the document.
	Path string `json:"path"`
	// TargetBranch is the branch the run was cut from, and TargetCommit the commit
	// it stood at: the copy that is there now.
	TargetBranch string `json:"target_branch"`
	TargetCommit string `json:"target_commit"`
	// WrittenAgainst is the commit on the target branch that made the copy the
	// text was written against. It is empty where the text was written as a new
	// document, or where none of the document's recent changes on the branch
	// made that copy; Says says which.
	WrittenAgainst string `json:"written_against,omitempty"`
	// NewDocument says the text was written as a document that did not exist
	// yet, so there was no copy to write it against.
	NewDocument bool      `json:"new_document,omitempty"`
	RecordedAt  time.Time `json:"recorded_at"`
}

// Validate reports every contract violation in the record at once.
func (c DocumentConflict) Validate() error {
	var problems []error
	if strings.TrimSpace(c.Path) == "" {
		problems = append(problems, errors.New("path is required, because it is the document that conflicts"))
	} else if clean := path.Clean(c.Path); clean != c.Path || path.IsAbs(c.Path) || clean == ".." || strings.HasPrefix(clean, "../") || len(c.Path) > maxDocumentConflictPathBytes {
		problems = append(problems, errors.New("path must be a clean repository-relative path"))
	}
	if strings.TrimSpace(c.TargetBranch) == "" {
		problems = append(problems, errors.New("target_branch is required, because it is what the document conflicts with"))
	} else if !validLocalBranch(c.TargetBranch) {
		problems = append(problems, errors.New("target_branch must be a local branch name"))
	}
	if !commitPattern.MatchString(c.TargetCommit) {
		problems = append(problems, errors.New("target_commit must be a full commit hash, because it is the copy the document conflicts with"))
	}
	if c.WrittenAgainst != "" && !commitPattern.MatchString(c.WrittenAgainst) {
		problems = append(problems, errors.New("written_against must be a full commit hash"))
	}
	if c.NewDocument && c.WrittenAgainst != "" {
		problems = append(problems, errors.New("a document written as new was written against no commit's copy"))
	}
	if c.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	return errors.Join(problems...)
}

// Says is the stop in one sentence: the document, the commit its text was
// written against, the commit the target branch now has, and that trying the
// same text again cannot land it.
func (c DocumentConflict) Says() string {
	var against string
	switch {
	case c.NewDocument:
		against = "the text was written as a new document, and " + c.TargetBranch + " now has a copy of it"
	case c.WrittenAgainst != "":
		against = fmt.Sprintf("the text was written against the copy commit %s made", c.WrittenAgainst)
	default:
		against = "none of the document's recent changes on " + c.TargetBranch + " made the copy the text was written against"
	}
	return fmt.Sprintf("the copy of %s on %s changed after this text was written: %s, and %s is now at commit %s, whose copy differs. Running the same text again cannot land it",
		c.Path, c.TargetBranch, against, c.TargetBranch, c.TargetCommit)
}
