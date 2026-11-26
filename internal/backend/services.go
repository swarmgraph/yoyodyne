package backend

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ServicesDirectory is the harness's services folder, which an adapter refuses
// to every session that can read files or run commands, whatever its role
// (docs/decisions/developer-runs-and-local-services.md). A session whose folder
// cannot be found is an error, and the adapter does not start it.
//
// A developer, or any session given a run's scratch folder, has it found from
// that folder. Every other session -- a review, a conversation, a helper -- has
// no folder of its own, and has it found from stateRoot, the state directory the
// adapter was built with.
func ServicesDirectory(request RunRequest, stateRoot string) (string, error) {
	if request.Role == domain.RoleDeveloper || strings.TrimSpace(request.ScratchDirectory) != "" {
		repository := request.RepositoryRoot
		if repository == "" {
			repository = request.WorkingDirectory
		}
		return execution.ServicesDirectoryDeniedTo(request.ScratchDirectory, request.RunID, repository)
	}
	if strings.TrimSpace(stateRoot) == "" {
		return "", fmt.Errorf("the %s session has no state directory to find the harness's %s folder in, so it cannot be refused that folder and is not started", request.Role, execution.ServicesDirectoryName)
	}
	return execution.ServicesDirectoryDenied(stateRoot)
}
