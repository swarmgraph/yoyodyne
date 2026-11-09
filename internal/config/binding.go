package config

import (
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

// OriginBinding marks a product.repository taken from the project's binding
// rather than from the file, followed by the binding's path.
const OriginBinding = "binding:"

// followBinding holds a configuration kept in the machine home's project
// directory to the repository its binding names. The machine-home design makes
// the binding the record of which repository a project is: it is what
// discovery finds such a configuration by, and `yoyo project bind` is the
// remedy for a moved or re-cloned repository. A file whose product.repository
// names another repository — edited by hand, or left by a bind made before bind
// rewrote it — is out of date, and every command uses the bound repository
// instead. Deciding it here, in the one loader every command reads the
// configuration through, is what keeps worktrees, the target branch, the
// checks, the tracker, the state-root marker, and the artifact folders from
// each deciding it differently.
//
// A configuration is kept in the project directory when it is the
// `config.yaml` of `<home>/projects/<product id>/` beside a binding; that is
// read from where the file is rather than from the home this shell resolves,
// so `--config` naming the file is held the same way. A configuration
// committed in its repository is never touched.
func (r *Resolved) followBinding(path string) error {
	directory := filepath.Dir(path)
	productID := string(r.Config.Product.ID)
	if productID == "" || filepath.Base(path) != home.ConfigFileName || filepath.Base(directory) != productID ||
		filepath.Base(filepath.Dir(directory)) != home.ProjectsDirectoryName {
		return nil
	}
	root := filepath.Dir(filepath.Dir(directory))
	binding, found, err := home.ReadBinding(root, productID)
	if err != nil || !found {
		return err
	}
	r.BindingPath = home.BindingPath(root, productID)
	configured := r.Config.Product.Repository
	named := configured
	if named != "" && !filepath.IsAbs(named) {
		named = filepath.Join(directory, named)
	}
	if named != "" {
		if common, err := home.CommonGitDirectory(named); err == nil && common != "" && home.SameDirectory(common, binding.GitCommonDirectory) {
			return nil
		}
	}
	r.ConfiguredRepository = configured
	r.Config.Product.Repository = home.RepositoryOf(binding.GitCommonDirectory)
	if r.Origins == nil {
		r.Origins = map[string]string{}
	}
	r.Origins["product.repository"] = OriginBinding + r.BindingPath
	return nil
}
