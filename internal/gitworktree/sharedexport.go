package gitworktree

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// SharedFile reads one file as the remote's target branch has it now, without
// moving the local branch or touching any checkout. It is how a machine in team
// mode reads the tracker's export another machine committed. found is false where
// the remote has no such branch or the branch no such file.
func (m *Manager) SharedFile(ctx context.Context, targetBranch, path string) (content []byte, found bool, err error) {
	if err := validateTargetBranch(targetBranch); err != nil {
		return nil, false, err
	}
	commit, onRemote, err := m.remoteCommit(ctx, m.remote, targetBranch)
	if err != nil {
		return nil, false, err
	}
	if !onRemote {
		return nil, false, nil
	}
	if err := m.fetchRemoteBranch(ctx, targetBranch, commit); err != nil {
		return nil, false, err
	}
	present, err := m.run(ctx, "-C", m.repositoryRoot, "cat-file", "-e", commit+":"+path)
	if err != nil {
		return nil, false, err
	}
	if present.Status != execution.ProcessSucceeded {
		return nil, false, nil
	}
	// Exact bytes, through the raw path: the ordinary one splits lines and
	// redacts, and an export is data to compare rather than output to show.
	var output bytes.Buffer
	result, err := m.runner.Run(ctx, execution.Command{
		Name:      m.gitBinary,
		Args:      []string{"-C", m.repositoryRoot, "cat-file", "blob", commit + ":" + path},
		Env:       execution.GitEnvironment(nil),
		RawStdout: &output,
		Timeout:   m.localTimeout(),
	}, nil)
	if err != nil {
		return nil, false, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, false, fmt.Errorf("read %s at %s failed with exit code %d: %s", path, commit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return output.Bytes(), true, nil
}
