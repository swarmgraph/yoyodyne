package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// maxLandedDocumentBytes bounds a document read back from the repository its
// documents are kept in.
const maxLandedDocumentBytes = 1 << 20

// documentLandings reads Git in the repository the project's governed documents
// are kept in: the companion intent repository where the project has one, the
// project's own repository otherwise. It reads commits, never the working tree,
// so a file written and not committed does not count as landed.
type documentLandings struct {
	runner       execution.ProcessRunner
	root         string
	worktreeRoot string
	remote       string
	companion    bool
}

// conversationDocumentLandings is the reader a conversation is given.
func conversationDocumentLandings(parts components) chat.DocumentLandings {
	return documentLandings{
		runner:       parts.runner,
		root:         parts.config.Product.IntentRoot(parts.repository),
		worktreeRoot: parts.worktreeRoot,
		remote:       parts.config.Execution.Remote,
		companion:    parts.config.Product.HasIntentRepository(),
	}
}

func (l documentLandings) repository() string {
	if l.companion {
		return "the companion intent repository at " + l.root
	}
	return "the project's repository at " + l.root
}

func (l documentLandings) manager() (*gitworktree.Manager, error) {
	manager, err := gitworktree.New(gitworktree.Options{
		Runner:         l.runner,
		RepositoryRoot: l.root,
		WorktreeRoot:   l.worktreeRoot,
		Remote:         l.remote,
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", l.repository(), err)
	}
	return manager, nil
}

// OnTarget reads the path as the branch that repository's checkout is on holds it.
func (l documentLandings) OnTarget(ctx context.Context, path string) (chat.LandedFile, error) {
	manager, err := l.manager()
	if err != nil {
		return chat.LandedFile{}, err
	}
	branch, err := manager.CurrentBranch(ctx)
	if err != nil {
		return chat.LandedFile{}, fmt.Errorf("find the target branch of %s: %w", l.repository(), err)
	}
	commit, err := manager.TargetCommit(ctx, branch)
	if err != nil {
		return chat.LandedFile{}, fmt.Errorf("resolve branch %s of %s: %w", branch, l.repository(), err)
	}
	file, err := l.read(ctx, manager, commit, path)
	file.Branch = branch
	return file, err
}

// AtCommit reads the path as the named commit holds it.
func (l documentLandings) AtCommit(ctx context.Context, commit, path string) (chat.LandedFile, error) {
	manager, err := l.manager()
	if err != nil {
		return chat.LandedFile{}, err
	}
	full, err := manager.ResolveCommitish(ctx, commit)
	if err != nil {
		return chat.LandedFile{}, fmt.Errorf("commit %s is not in %s: %w", commit, l.repository(), err)
	}
	return l.read(ctx, manager, full, path)
}

func (l documentLandings) read(ctx context.Context, manager *gitworktree.Manager, commit, path string) (chat.LandedFile, error) {
	landed := chat.LandedFile{Repository: l.repository(), Commit: commit}
	file, err := manager.FileAtCommit(ctx, commit, path, maxLandedDocumentBytes)
	if errors.Is(err, gitworktree.ErrNotAtCommit) {
		return landed, nil
	}
	if err != nil {
		return landed, fmt.Errorf("read %s at %s of %s: %w", path, commit, l.repository(), err)
	}
	if file.Content == nil && file.Size > maxLandedDocumentBytes {
		return landed, fmt.Errorf("%s at %s of %s is %d bytes, over the %d the harness reads as a document", path, commit, l.repository(), file.Size, maxLandedDocumentBytes)
	}
	landed.Found = true
	landed.Content = file.Content
	return landed, nil
}
