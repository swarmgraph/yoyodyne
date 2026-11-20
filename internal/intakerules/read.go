package intakerules

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// Repository is what reading a rules document needs of Git: where the target
// branch stands, and one file as a commit holds it. gitworktree.Manager is one.
type Repository interface {
	TargetCommit(ctx context.Context, branch string) (string, error)
	FileAtCommit(ctx context.Context, commit, path string, maxBytes int64) (gitworktree.FileAt, error)
}

var _ Repository = (*gitworktree.Manager)(nil)

// Request names the document one query run reads.
type Request struct {
	// Query is the name of the query whose run this is.
	Query string
	// Path is the repository-relative path the query names its rules by.
	Path string
	// TargetBranch is the branch the rules are read from.
	TargetBranch string
	// Goals is what each rule's goal is checked against.
	Goals goal.Set
}

// Rules is a query's rules document as one run read it.
type Rules struct {
	Query string `json:"query"`
	Path  string `json:"path"`
	// Branch and Commit are where the document was read: the target branch, and
	// the commit it stood at when the run started. Every decision made under
	// these rules records the commit, so the rule a message was judged by can be
	// read again exactly as it was.
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Parsed
}

// Read reads the rules document as the target branch holds it now, and never
// as any working tree has it. The commit is resolved once, so every part of the
// document is read from the same commit.
//
// The error return is for the read failing rather than the document: a branch
// that cannot be resolved, or Git failing, is an error, and the run tries again
// later with nothing marked as read. A document that is missing, too large, or
// cannot be read as rules is a result with Unreadable set, because that is a
// document somebody has to fix rather than a read to try again.
func Read(ctx context.Context, repository Repository, request Request) (Rules, error) {
	if repository == nil {
		return Rules{}, errors.New("no repository to read the rules document from")
	}
	if strings.TrimSpace(request.TargetBranch) == "" {
		return Rules{}, errors.New("a target branch is required to read the rules document from")
	}
	if problem := config.IntakeRulesPathProblem(request.Path); problem != "" {
		return Rules{}, fmt.Errorf("rules path %q %s", request.Path, problem)
	}
	if reason, unknown := request.Goals.Uncheckable(); unknown {
		// Without the goals no rule's goal can be checked, and refusing every rule
		// for that would report a document to fix when nothing in it is wrong.
		return Rules{}, fmt.Errorf("the goals a rule's goal is checked against cannot be read: %s", reason)
	}
	commit, err := repository.TargetCommit(ctx, request.TargetBranch)
	if err != nil {
		return Rules{}, fmt.Errorf("find where %s stands: %w", request.TargetBranch, err)
	}
	rules := Rules{Query: request.Query, Path: request.Path, Branch: request.TargetBranch, Commit: commit}
	file, err := repository.FileAtCommit(ctx, commit, request.Path, MaxDocumentBytes)
	switch {
	case errors.Is(err, gitworktree.ErrNotAtCommit):
		rules.Unreadable = fmt.Sprintf("%s holds no file at %s", request.TargetBranch, request.Path)
		return rules, nil
	case err != nil:
		return Rules{}, fmt.Errorf("read %s at %s: %w", request.Path, commit, err)
	case file.Content == nil && file.Size > 0:
		rules.Unreadable = fmt.Sprintf("it is %d bytes, and a rules document is read only up to %d", file.Size, MaxDocumentBytes)
		return rules, nil
	}
	rules.Parsed = Parse(file.Content, request.Goals)
	return rules, nil
}
