package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/outsidefile"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// PinnedFilesCheck is what the comparison of pinned outside files with their
// records is called where a run's checks are listed. It is the harness's own,
// run before the configured checks of every run, and not a command a project
// declares (docs/designs/pinned-outside-files.md).
const PinnedFilesCheck = "pinned outside files match their records"

// Bounds on the listing the comparison finds records in. A tree past them is
// one the comparison cannot say it read whole, which stops the run's checks
// rather than passing a record it never saw.
const (
	maxPinnedListingFiles = 500000
	maxPinnedListingBytes = 64 << 20
)

// gatePinnedFiles compares every pinned outside file the candidate holds with
// its record, and every record's harness fields with the target branch's copy,
// before the configured checks run (outsidefile.Compare). A problem fails the
// run's checks as a check that ran and failed, naming the file or the record,
// so it is repair input like any failing check: a developer that changed a
// pinned file by hand or wrote the harness's fields takes the change back out.
func (a *activeRun) gatePinnedFiles(ctx context.Context, changed []string) error {
	records, err := a.candidateRecords(ctx, changed)
	if err != nil {
		return stoppedBy(runstate.StopChecks, fmt.Errorf("find the pinned outside files' records: %w", err))
	}
	if len(records) == 0 {
		return nil
	}
	root, err := repowrite.NewRoot(a.worktree.Path)
	if err != nil {
		return stoppedBy(runstate.StopChecks, err)
	}
	landing := ""
	if a.state.OutsideFile != nil {
		landing = a.state.OutsideFile.Record
	}
	problems := outsidefile.Compare(outsidefile.Comparison{
		Records:   records,
		Candidate: worktreeReader(root),
		Base:      commitReader(ctx, a.pipeline.Worktrees, a.worktree.BaseCommit),
		Landing:   landing,
	})
	if len(problems) == 0 {
		return nil
	}
	lines := make([]string, 0, len(problems)+1)
	lines = append(lines, "The harness compared every pinned outside file with its record before the checks, and found:")
	for _, problem := range problems {
		lines = append(lines, "- "+problem.String())
	}
	output := strings.Join(lines, "\n") + "\n"
	result := checks.Result{
		Command:       PinnedFilesCheck,
		Process:       execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stdout: output},
		FailureOutput: output,
	}
	// It is the stage's result, as a check that ran and failed first would be.
	a.outcome.Checks = []checks.Result{result}
	return checkFailure{result: result}
}

// candidateRecords is every record path the candidate holds: the ones its
// branch commit carries, and the ones the change adds that are not committed
// yet. A record the change deletes is read as absent and imposes nothing.
func (a *activeRun) candidateRecords(ctx context.Context, changed []string) ([]string, error) {
	commit := a.worktree.HarnessCommit
	if commit == "" {
		commit = a.worktree.BaseCommit
	}
	var records []string
	if commit != "" {
		listing, err := a.pipeline.Worktrees.FilesAtCommit(ctx, commit, maxPinnedListingFiles, maxPinnedListingBytes)
		if err != nil {
			return nil, err
		}
		if listing.Omitted > 0 {
			return nil, fmt.Errorf("the listing of %s omitted %d path(s), so not every record could be found", commit, listing.Omitted)
		}
		records = outsidefile.Records(listing.Files)
	}
	for _, path := range outsidefile.Records(changed) {
		if !slices.Contains(records, path) {
			records = append(records, path)
		}
	}
	slices.Sort(records)
	return records, nil
}

// worktreeReader reads a path from the candidate as the checks see it, confined
// to the worktree: a path that is a link, or reaches through one, is an error
// rather than a file.
func worktreeReader(root repowrite.Root) outsidefile.Reader {
	return func(path string, maxBytes int64) ([]byte, bool, error) {
		resolved, err := root.Resolve(path)
		if err != nil {
			return nil, false, err
		}
		// The path as written, not as resolved: a pinned file that is itself a
		// link is not the file its record describes.
		info, err := os.Lstat(filepath.Join(root.Path(), filepath.FromSlash(path)))
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if !info.Mode().IsRegular() {
			return nil, false, fmt.Errorf("%s is not a regular file", path)
		}
		if info.Size() > maxBytes {
			return nil, false, fmt.Errorf("%s is %d bytes, more than the %d a pinned outside file may be", path, info.Size(), maxBytes)
		}
		file, err := os.Open(resolved)
		if err != nil {
			return nil, false, err
		}
		defer file.Close()
		content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
		if err != nil {
			return nil, false, err
		}
		return content, true, nil
	}
}

// commitReader reads a path as one commit holds it.
func commitReader(ctx context.Context, reader repositoryReader, commit string) outsidefile.Reader {
	if commit == "" {
		return nil
	}
	return func(path string, maxBytes int64) ([]byte, bool, error) {
		file, err := reader.FileAtCommit(ctx, commit, path, maxBytes)
		if errors.Is(err, gitworktree.ErrNotAtCommit) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if file.Content == nil && file.Size > 0 {
			return nil, false, fmt.Errorf("%s at %s is %d bytes, more than %d", path, commit, file.Size, maxBytes)
		}
		return file.Content, true, nil
	}
}
