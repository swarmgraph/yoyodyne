package execution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// ServicesDirectoryName is the folder under the harness's state directory that
// holds what is needed to reach a service the harness runs for itself: every
// socket file such a service listens on, and every password or token one
// checks, and nothing of either is kept anywhere else. Every session the harness
// starts that can read files or run commands is refused the whole folder
// (docs/decisions/developer-runs-and-local-services.md).
const ServicesDirectoryName = "services"

// PrepareServicesDirectory creates the services folder under the harness's
// state directory stateRoot, with access for its owner only, and reports where
// it is with its links resolved. One already there is used as it is. It is
// refused where it is reached through a link, or where anyone but its owner has
// access to it: what it holds would then be readable outside the boundary it is
// for, and nothing here can tell whether it has been read.
func PrepareServicesDirectory(stateRoot string) (string, error) {
	if strings.TrimSpace(stateRoot) == "" {
		return "", fmt.Errorf("no state directory was given to keep the %s folder in", ServicesDirectoryName)
	}
	root, err := repowrite.NewRoot(stateRoot)
	if err != nil {
		return "", fmt.Errorf("confine the %s folder to the state directory: %w", ServicesDirectoryName, err)
	}
	created, err := prepareDirectory(root, ServicesDirectoryName)
	if err != nil {
		return "", fmt.Errorf("create the %s folder in the state directory: %w", ServicesDirectoryName, err)
	}
	if want := filepath.Join(root.Path(), ServicesDirectoryName); created != want {
		return "", fmt.Errorf("the %s folder is redirected by a symlink to %s", ServicesDirectoryName, created)
	}
	info, err := os.Lstat(created)
	if err != nil {
		return "", fmt.Errorf("inspect the %s folder: %w", ServicesDirectoryName, err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is not a directory only its owner can open (it is %s); chmod 700 makes it one", created, info.Mode())
	}
	return created, nil
}

// ServicesDirectoryDeniedTo is the services folder a run with a scratch folder
// has to be refused, worked out from the scratch folder cut for the run: the two
// are under one state directory, so no caller can name one and not the other.
// The folder is made if it is not there, because a sandbox on Linux can only
// refuse a path that exists.
//
// A run with no scratch folder of its own has no state directory to find the
// folder in and is refused, so there is no developer run without the refusal.
func ServicesDirectoryDeniedTo(scratch, runID, repositoryRoot string) (string, error) {
	if strings.TrimSpace(scratch) == "" {
		return "", fmt.Errorf("run %s was given no scratch directory, so the %s folder its sandbox has to refuse cannot be found", runID, ServicesDirectoryName)
	}
	if err := ConfirmScratchDirectory(scratch, runID, repositoryRoot); err != nil {
		return "", err
	}
	return ServicesDirectoryDenied(filepath.Dir(filepath.Dir(scratch)))
}

// ServicesDirectoryDenied is the services folder under stateRoot, made if it is
// not there, for a session to be refused. One whose path holds a character a
// provider's path rules give a meaning to is an error, since the rule would then
// refuse something else.
func ServicesDirectoryDenied(stateRoot string) (string, error) {
	services, err := PrepareServicesDirectory(stateRoot)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(services, "*?[]{}()\\\"") || strings.ContainsFunc(services, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return "", fmt.Errorf("a sandbox rule cannot name the folder %q exactly, because of a character in it that such rules give a meaning to; the state directory has to be somewhere else", services)
	}
	return services, nil
}
