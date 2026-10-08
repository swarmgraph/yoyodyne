package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Intent says which repository holds the project's governed documents: the
// specifications, the designs, the decision records with their invariants. By
// default that is the project's own repository. A project may instead keep them
// in a companion intent repository, an ordinary Git repository of their own kept
// in the project's directory in the machine home, so that a contributor to a
// repository they do not own commits nothing of Yoyodyne's there
// (docs/designs/machine-home.md, "The companion intent repository").
//
// The homes themselves — product.specifications and the rest — keep meaning the
// same directories; what this moves is the repository they are relative to.
// Product.IntentRoot is the one answer every reader asks.
type Intent struct {
	// Repository is the companion intent repository, relative to the directory
	// the configuration file is in, which for a configuration kept in the machine
	// home is the project's own directory there: `intent` is
	// `~/.yoyodyne/projects/<id>/intent`. Empty means the project's own
	// repository holds the documents.
	Repository string `yaml:"repository,omitempty" json:"repository,omitempty"`
}

type intentDocument struct {
	Repository *string `yaml:"repository"`
}

// IntentRoot is where this project's governed documents are read from: the
// companion intent repository where the configuration names one, and the
// project's repository otherwise. Every reader of the brief, the goals, the
// designs, the decision records, and the invariants resolves its root here and
// nowhere else, so a project that moves its intent cannot have one reader
// looking in the old place.
func (p Product) IntentRoot(projectRepository string) string {
	if p.IntentRepository != "" {
		return p.IntentRepository
	}
	return projectRepository
}

// HasIntentRepository reports whether the governed documents are kept outside
// the project's repository.
func (p Product) HasIntentRepository() bool { return p.IntentRepository != "" }

// validateIntentRepository holds the configured path inside the directory the
// configuration is in: the intent repository is the project's, kept in its
// directory in the machine home, and a path climbing out of that directory would
// make whatever it named the product's intent.
func validateIntentRepository(repository string) error {
	trimmed := strings.TrimSpace(repository)
	if trimmed == "" {
		return nil
	}
	clean := filepath.Clean(filepath.FromSlash(trimmed))
	if filepath.IsAbs(trimmed) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("intent repository %q must be a directory inside the project's directory, such as \"intent\"", repository)
	}
	return nil
}

// resolveIntentRepository turns a configured companion intent repository into
// the absolute path every reader is given. It is resolved where the
// configuration's own location is known, which is the only place a relative
// path can mean anything.
//
// A configuration committed in the repository may not name one. Choosing a
// companion repository is choosing to keep everything of Yoyodyne's out of the
// project's, and a committed `.yoyodyne/config.yaml` is already something of
// Yoyodyne's there; it is also read by every clone, while the intent repository
// is a path on one machine.
func resolveIntentRepository(cfg *Config, configPath string) error {
	repository := strings.TrimSpace(cfg.Intent.Repository)
	if repository == "" {
		return nil
	}
	if inRepository(configPath) {
		return ValidationError{Problems: []string{fmt.Sprintf(
			"intent.repository is set in %s, which is committed with the project's repository; a companion intent repository is chosen in the configuration kept in the project's directory in the machine home (`yoyo init --external --intent`), because it exists to keep everything of Yoyodyne's out of the repository",
			configPath)}}
	}
	cfg.Product.IntentRepository = filepath.Join(ProjectDirectory(configPath), filepath.Clean(filepath.FromSlash(repository)))
	return nil
}

// inRepository reports whether a configuration file is the one a repository
// carries: `.yoyodyne/config.yaml` or the legacy `.yoyodyne.yaml`.
func inRepository(configPath string) bool {
	return filepath.Base(filepath.Dir(configPath)) == DirectoryName || filepath.Base(configPath) == LegacyFileName
}
