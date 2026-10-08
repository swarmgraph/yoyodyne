package cli

// The companion intent repository, as `yoyo init` and `yoyo setup` offer it.
//
// A project may keep its governed documents — the specifications, the designs,
// the decision records and their invariants — in a Git repository of their own
// in the project's directory in the machine home, rather than in the project's
// repository, so a contributor to a repository they do not own commits nothing
// of Yoyodyne's there (docs/designs/machine-home.md). Every reader finds it
// through config.Product.IntentRoot; what is here is the part a person sees:
// naming it in the configuration, and creating or cloning it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// intentCommandTimeout bounds the one Git command establishing the repository
// runs. A clone reaches a remote, so it is given longer than a local command
// would need.
const intentCommandTimeout = 5 * time.Minute

// What became of the companion intent repository.
const (
	intentCreated = "created"
	intentCloned  = "cloned"
	intentPresent = "present"
	intentFailed  = "failed"
)

// companionIntent is what init or setup did about the companion intent
// repository.
type companionIntent struct {
	Status string `json:"status"`
	// Path is where the repository is, absolute.
	Path string `json:"path"`
	// From is what it was cloned from, where it was.
	From string `json:"from,omitempty"`
	// Written is the directory indexes written into a repository created here.
	Written []string `json:"written,omitempty"`
	// Holds says what the repository holds, in this project's own directories.
	Holds string `json:"holds"`
	// Reason carries Git's own words where the repository could not be made.
	Reason string `json:"reason,omitempty"`
}

// intentHolds says what a project's governed documents are and where each is
// filed, which is what a person choosing a companion repository is choosing to
// move out of the project's.
func intentHolds(product config.Product) string {
	return fmt.Sprintf("the product brief, goals, and other specifications (%s), the designs (%s), and the decision records (%s) with the invariants (%s)",
		product.Specifications, product.Designs, product.Decisions, product.Invariants)
}

// isGitRepository reports whether a directory is the top of a Git checkout.
func isGitRepository(directory string) bool {
	_, err := os.Stat(filepath.Join(directory, ".git"))
	return err == nil
}

// establishIntentRepository creates the companion intent repository the
// configuration names, or clones it from where a teammate shares it, and
// leaves one that is already there exactly as it is.
//
// A repository it creates gets the directory index every artifact home gets,
// written through the confined-write primitive rooted at the new repository; a
// cloned one already carries whatever its owners wrote. Nothing is committed:
// what lands in the new repository is the operator's to commit and push, as
// every document the harness writes for them is.
func establishIntentRepository(ctx context.Context, runner execution.ProcessRunner, product config.Product, from string) companionIntent {
	outcome := companionIntent{Path: product.IntentRepository, Holds: intentHolds(product)}
	if outcome.Path == "" {
		outcome.Status = intentFailed
		outcome.Reason = "the configuration names no companion intent repository"
		return outcome
	}
	from = strings.TrimSpace(from)
	if info, err := os.Stat(outcome.Path); err == nil {
		switch {
		case !info.IsDir():
			outcome.Status = intentFailed
			outcome.Reason = fmt.Sprintf("%s is there and is not a directory", outcome.Path)
		case isGitRepository(outcome.Path):
			outcome.Status = intentPresent
		default:
			outcome.Status = intentFailed
			outcome.Reason = fmt.Sprintf("%s is there and is not a Git repository; move it aside, or make it one with `git init`", outcome.Path)
		}
		return outcome
	} else if !errors.Is(err, os.ErrNotExist) {
		outcome.Status = intentFailed
		outcome.Reason = err.Error()
		return outcome
	}

	args := []string{"init", "--quiet", outcome.Path}
	outcome.Status = intentCreated
	if from != "" {
		args = []string{"clone", "--quiet", from, outcome.Path}
		outcome.Status = intentCloned
		outcome.From = from
	}
	result, err := runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    args,
		Env:     execution.GitEnvironment(nil),
		Timeout: intentCommandTimeout,
	}, nil)
	if err != nil {
		return companionIntent{Status: intentFailed, Path: outcome.Path, From: from, Holds: outcome.Holds, Reason: fmt.Sprintf("git could not be run: %v", err)}
	}
	if result.Status != execution.ProcessSucceeded {
		reason := singleLine(firstNonEmpty(result.Stderr, result.Stdout))
		if reason == "" {
			reason = "git " + args[0] + " did not succeed"
		}
		return companionIntent{Status: intentFailed, Path: outcome.Path, From: from, Holds: outcome.Holds, Reason: reason}
	}
	if outcome.Status == intentCloned {
		return outcome
	}
	root, err := repowrite.NewRoot(outcome.Path)
	if err != nil {
		outcome.Reason = "the repository was created, and its directory indexes could not be written: " + err.Error()
		return outcome
	}
	for _, status := range artifacthome.Inspect(root, config.Config{Product: product}) {
		if status.State != artifacthome.StateMissing {
			continue
		}
		written, err := artifacthome.Write(root, status.Home)
		if err != nil {
			outcome.Reason = "the repository was created, and a directory index could not be written: " + err.Error()
			return outcome
		}
		outcome.Written = append(outcome.Written, written)
	}
	return outcome
}

// describeCompanionIntent says what became of the companion intent repository
// and what it holds, in the sentence init and setup print.
func describeCompanionIntent(outcome companionIntent, repository string) string {
	switch outcome.Status {
	case intentCreated:
		return fmt.Sprintf("created the companion intent repository %s; it holds %s, so nothing of Yoyodyne's is committed to %s. "+
			"Commit what is in it and push it wherever your team shares it; a teammate runs `yoyo init --external --intent-from <its URL>`",
			outcome.Path, outcome.Holds, repository)
	case intentCloned:
		return fmt.Sprintf("cloned the companion intent repository from %s into %s; it holds %s, so nothing of Yoyodyne's is committed to %s",
			outcome.From, outcome.Path, outcome.Holds, repository)
	case intentPresent:
		return fmt.Sprintf("the companion intent repository %s is already there; it holds %s", outcome.Path, outcome.Holds)
	default:
		return fmt.Sprintf("the companion intent repository %s could not be made: %s; until it is there, nothing reads this project's intent", outcome.Path, outcome.Reason)
	}
}

// chooseIntentRepository names the companion intent repository in a
// configuration kept outside the repository, by appending the intent block to
// it. It edits the file as text rather than re-encoding it, for the reason the
// Slack step does: a generated configuration is mostly comments. What it would
// write is loaded before it is kept, and the file is put back as it was if it
// does not load.
func chooseIntentRepository(path string) error {
	directory := filepath.Dir(path)
	root, err := repowrite.NewRoot(directory)
	if err != nil {
		return err
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	edited := strings.TrimRight(string(original), "\n") + "\n" + config.IntentSection(home.IntentDirectoryName)
	if _, err := root.WriteFile(filepath.Base(path), []byte(edited)); err != nil {
		return err
	}
	if _, err := config.LoadResolved(path); err != nil {
		if _, restore := root.WriteFile(filepath.Base(path), original); restore != nil {
			return fmt.Errorf("the configuration this wrote does not load (%v), and putting it back failed too: %w", err, restore)
		}
		return fmt.Errorf("the configuration this would write does not load: %w", err)
	}
	return nil
}
