package cli

import (
	"github.com/mason-bryant/yoyodyne/internal/config"
)

// configuredRepository is the repository a loaded configuration describes, and
// nothing when it cannot be resolved -- which is a question that cannot be asked
// rather than a failure of the command asking it. It is what the ignore rules
// are asked about (doctor.ConfigurationIgnored).
func configuredRepository(resolved config.Resolved) string {
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return ""
	}
	return repository
}
