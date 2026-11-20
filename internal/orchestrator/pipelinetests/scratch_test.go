package pipelinetests

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/testharness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// What moving scratch out of the repository's Git directory is for
// (docs/decisions/developer-run-scratch-outside-git.md): a link a run leaves in
// its scratch folder -- the Codex CLI's tmp/arg0 links to its own program are
// the ones that did it -- appears nowhere under the repository, so a git server
// confined to the repository, which refuses every call while any link in it
// leads outside, still answers once the run has ended. That holds for a run
// whose worktree is kept for a person to merge, whose scratch stays, and for
// one that landed and was cleaned up, whose scratch is removed with its links
// removed as links.
//
// The control puts the same link where scratch used to be cut, under `.git`,
// and the server's check refuses, so a pass is about where the folder is rather
// than about a check that refuses nothing.
func TestALinkARunLeavesInScratchDoesNotStopTheGitServer(t *testing.T) {
	t.Parallel()

	for _, landed := range []bool{false, true} {
		name := "kept for a person to merge"
		if landed {
			name = "landed and cleaned up"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := testharness.PipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			program := filepath.Join(t.TempDir(), "codex")
			if err := os.WriteFile(program, []byte("a program outside the repository\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			var scratch string
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				scratch = request.ScratchDirectory
				arg0 := filepath.Join(scratch, "home", "tmp", "arg0", "codex-arg0abc")
				if err := os.MkdirAll(arg0, 0o700); err != nil {
					return err
				}
				for _, link := range []string{filepath.Join(arg0, "apply_patch"), filepath.Join(arg0, "codex-execve-wrapper")} {
					if err := os.Symlink(program, link); err != nil {
						return err
					}
				}
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, testharness.ApproveVerdict)
			var pipeline orchestrator.Pipeline
			if landed {
				pipeline, _ = testharness.NewAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			} else {
				pipeline, _ = testharness.NewPipeline(t, repository, tracker, provider, []string{"exit 0"})
			}

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Status != runstate.StatusSucceeded || scratch == "" {
				t.Fatalf("outcome = %s, scratch = %q; want a succeeded run that was handed a scratch folder", outcome.Status, scratch)
			}
			if landed {
				if !outcome.WorktreeRemoved || outcome.ScratchRemovalProblem != "" {
					t.Fatalf("outcome: worktree removed %t, scratch removal problem %q", outcome.WorktreeRemoved, outcome.ScratchRemovalProblem)
				}
				if _, err := os.Lstat(scratch); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("the run's scratch folder is still there after clean-up (err = %v)", err)
				}
				if _, err := os.Stat(program); err != nil {
					t.Fatalf("removing the scratch folder removed what its links pointed at: %v", err)
				}
			} else if _, err := os.Lstat(filepath.Join(scratch, "home", "tmp", "arg0", "codex-arg0abc", "apply_patch")); err != nil {
				t.Fatalf("the kept run's link is not where it left it: %v", err)
			}

			if links := outwardLinksUnder(t, repository); len(links) != 0 {
				t.Fatalf("links leading out of the repository after the run: %v", links)
			}
			if refusal := gitServerRefusal(t, repository); refusal != "" {
				t.Fatalf("the git server refused a call on the repository after the run ended: %s", refusal)
			}

			// The control: the same link where scratch was cut before the move.
			old := filepath.Join(repository, ".git", "yoyodyne", "scratch", outcome.RunID)
			if err := os.MkdirAll(old, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(program, filepath.Join(old, "apply_patch")); err != nil {
				t.Fatal(err)
			}
			if gitServerRefusal(t, repository) == "" {
				t.Fatal("the git server's check passed with a link leading out of .git, so this test cannot tell where scratch is")
			}
		})
	}
}

// outwardLinksUnder is every link under root, its Git directory included, that
// resolves, or names a target, outside root: the check a git server confined to
// the repository makes before it answers.
func outwardLinksUnder(t *testing.T, root string) []string {
	t.Helper()
	boundary, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	var links []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&fs.ModeSymlink == 0 {
			return err
		}
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			named, readErr := os.Readlink(path)
			if readErr != nil {
				return readErr
			}
			if !filepath.IsAbs(named) {
				named = filepath.Join(filepath.Dir(path), named)
			}
			target = filepath.Clean(named)
		}
		if relative, err := filepath.Rel(boundary, target); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			links = append(links, path+" -> "+target)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return links
}

// gitServerRefusal is why a git server confined to repository would refuse a
// call on it, or nothing where it would answer: the server the project
// declares, mcp-server-git, refuses every call while any link under the
// repository leads outside it. The real server is asked in internal/mcpcall,
// the one package that speaks to servers
// (TestTheGitServerAnswersWithALinkLeftInAStateDirectoryScratch); this is its
// check, which runs everywhere.
func gitServerRefusal(t *testing.T, repository string) string {
	t.Helper()
	if links := outwardLinksUnder(t, repository); len(links) != 0 {
		return "links lead outside the repository: " + strings.Join(links, ", ")
	}
	return ""
}
