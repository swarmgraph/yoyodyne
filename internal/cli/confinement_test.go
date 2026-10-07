package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The initialization writer is held to the same topology matrix as every other
// repository-confined writer, rather than to a set of cases of its own. It is
// the first thing that ever writes into a project, so a scaffold that landed
// outside is a configuration the operator was told the path of and cannot find,
// in a place nothing commits and nothing reviews.
func TestTheInitializationWriterIsConfinedToTheProject(t *testing.T) {
	t.Parallel()

	writertest.Run(t, writertest.Writer{
		Name:      "yoyo init",
		Directory: config.DirectoryName,
		File:      config.FileName,
		Write: func(t *testing.T, root string) error {
			_, err := initializeProject(initializeOptions{Directory: root, ProductID: "example-product"})
			return err
		},
	})
}

// An external initialization writes into the machine home's project directory
// instead of into the repository, and is held to the same matrix there. The
// topologies are the ones that matter most for it: the home is a directory
// somebody's dotfiles may well have symlinked, and a write that followed one out
// of it would land a configuration where nothing looks for it again.
func TestTheExternalInitializationWriterIsConfinedToTheMachineHome(t *testing.T) {
	// The matrix builds each topology at the directory the writer was configured
	// with, which for this one is the project's directory under the home rather
	// than a fixed name.
	writertest.Run(t, writertest.Writer{
		Name:      "yoyo init --external",
		Directory: home.ProjectsDirectoryName + "/example-product",
		File:      config.FileName,
		Write: func(t *testing.T, root string) error {
			// A repository for the configuration to be bound to, one per home,
			// because a checkout records the home it was first used with. It needs
			// a `.git` and nothing else: what is looked for is the marker rather
			// than a working Git.
			repository := filepath.Join(t.TempDir(), "example-project")
			if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			t.Setenv(runstate.StateHomeVariable, root)
			_, err := initializeProject(initializeOptions{
				Directory: repository,
				ProductID: "example-product",
				External:  true,
			})
			return err
		},
	})
}
