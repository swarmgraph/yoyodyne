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

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// intakeChanges compares the intake section of each configuration file the
// change touched, and that the path comparison did not already refuse, with
// the copy at the run's base. It answers the files whose section moved and
// what moved in each; a file the path comparison refused needs no second
// reason, because the whole file has to come back out of the change anyway.
//
// The base copy is read from the base commit rather than from any checkout,
// and the change's copy from the worktree, because the path comparison reads
// the change as base against worktree and the two must agree about one change.
// A copy that cannot be read or decoded is refused with the reason, since a
// section that cannot be compared cannot be shown to be unchanged
// (protectedpath.IntakeChanges explains why the section is compared at all).
func (a *activeRun) intakeChanges(ctx context.Context, changed, refused []string) ([]string, []string, error) {
	var files, settings []string
	for _, file := range protectedpath.IntakeConfigurationFiles {
		if !slices.Contains(changed, file) || slices.Contains(refused, file) {
			continue
		}
		moved, err := a.intakeChangesIn(ctx, file)
		if err != nil {
			return nil, nil, err
		}
		if len(moved) == 0 {
			continue
		}
		files = append(files, file)
		settings = append(settings, moved...)
	}
	return files, settings, nil
}

// intakeChangesIn compares one configuration file. Its error is reserved for
// the comparison failing to run at all — Git unable to read the base commit, or
// the worktree unreadable — which no edit of the change can repair; everything
// the change itself did is answered as a setting.
func (a *activeRun) intakeChangesIn(ctx context.Context, file string) ([]string, error) {
	base, err := a.pipeline.Worktrees.FileAtCommit(ctx, a.worktree.BaseCommit, file, protectedpath.MaxIntakeConfigurationBytes)
	var baseContent []byte
	switch {
	case errors.Is(err, gitworktree.ErrNotAtCommit):
		// No ordinary file at the base: it held no intake section.
	case err != nil:
		return nil, fmt.Errorf("read %s at the run's base to compare its %s section: %w", file, protectedpath.IntakeSection, err)
	case base.Content == nil:
		return []string{protectedpath.DescribeIntakeProblem(file, fmt.Errorf("the copy at the run's base is %d bytes, more than the %d read", base.Size, protectedpath.MaxIntakeConfigurationBytes))}, nil
	default:
		baseContent = base.Content
	}
	changeContent, err := readWorktreeConfiguration(a.worktree.Path, file)
	if errors.Is(err, protectedpath.ErrIntakeUnreadable) {
		return []string{protectedpath.DescribeIntakeProblem(file, err)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s in the change to compare its %s section: %w", file, protectedpath.IntakeSection, err)
	}
	moved, err := protectedpath.IntakeChanges(baseContent, changeContent)
	if err != nil {
		return []string{protectedpath.DescribeIntakeProblem(file, err)}, nil
	}
	return moved, nil
}

// readWorktreeConfiguration reads one configuration file as the worktree holds
// it, or nil where it is not there. Every part of the path has to be an
// ordinary directory or file inside the worktree: a link would let the change's
// copy be read from somewhere the change does not hold, so one is answered as
// unreadable and the change is refused on it.
func readWorktreeConfiguration(root, file string) ([]byte, error) {
	current := root
	parts := strings.Split(file, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		last := index == len(parts)-1
		if (!last && !info.IsDir()) || (last && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("%w: %s is not an ordinary file in the worktree", protectedpath.ErrIntakeUnreadable, file)
		}
		if last && info.Size() > protectedpath.MaxIntakeConfigurationBytes {
			return nil, fmt.Errorf("%w: %s is %d bytes, more than the %d read", protectedpath.ErrIntakeUnreadable, file, info.Size(), protectedpath.MaxIntakeConfigurationBytes)
		}
	}
	handle, err := os.Open(current)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	content, err := io.ReadAll(io.LimitReader(handle, protectedpath.MaxIntakeConfigurationBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > protectedpath.MaxIntakeConfigurationBytes {
		return nil, fmt.Errorf("%w: %s is more than the %d bytes read", protectedpath.ErrIntakeUnreadable, file, protectedpath.MaxIntakeConfigurationBytes)
	}
	if content == nil {
		content = []byte{}
	}
	return content, nil
}

// boundedIntakeSettings keeps what a refusal carries of the intake settings
// within what durable state accepts, counting what it dropped in a last entry
// rather than dropping it silently.
func boundedIntakeSettings(settings []string) []string {
	bounded := make([]string, 0, len(settings))
	for _, setting := range settings {
		if len(setting) > runstate.MaxIntakeSettingBytes {
			setting = strings.ToValidUTF8(setting[:runstate.MaxIntakeSettingBytes-len("…")], "") + "…"
		}
		bounded = append(bounded, setting)
	}
	if len(bounded) > runstate.MaxIntakeSettings {
		dropped := len(bounded) - (runstate.MaxIntakeSettings - 1)
		bounded = append(bounded[:runstate.MaxIntakeSettings-1], fmt.Sprintf("and %d further intake setting(s) not listed here", dropped))
	}
	return bounded
}
