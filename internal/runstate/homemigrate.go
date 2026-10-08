package runstate

// The run-state half of `yoyo home migrate`: what has to be true of a home
// before anything in it moves, and the one rewrite of run records the move
// owes. The moves themselves are the home package's (home.MoveHome);
// docs/designs/machine-home.md is the design.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// MaxHomeMigrationByBytes bounds the account of who ran a migration, which is a
// process's command line and can be as long as whoever typed it made it.
const MaxHomeMigrationByBytes = 1024

// HomeMigration is what a migration recorded on a run whose worktree it moved:
// when, the path the run had recorded before, and who ran it.
type HomeMigration struct {
	At               time.Time `json:"at"`
	FromWorktreePath string    `json:"from_worktree_path"`
	By               string    `json:"by,omitempty"`
}

// Validate rejects a migration note that cannot say what it changed.
func (m HomeMigration) Validate() error {
	var problems []error
	if m.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if strings.TrimSpace(m.FromWorktreePath) == "" {
		problems = append(problems, errors.New("from_worktree_path is required"))
	}
	return errors.Join(problems...)
}

// InFlightError is a migration refused because something in the home is being
// worked on. It is one sentence naming each thing, and nothing has moved.
type InFlightError struct {
	Home     string
	InFlight []string
}

func (e *InFlightError) Error() string {
	verb := "are"
	if len(e.InFlight) == 1 {
		verb = "is"
	}
	return fmt.Sprintf("%s moved nothing, because %s %s in flight in %s; let each finish, or stop the product with `yoyo stop` "+
		"(which cancels any run the watch session is hosting, keeping its branch and worktree), then run it again",
		home.MigrateCommand, joinNamed(e.InFlight), verb, e.Home)
}

// HomeInFlight names everything a live process is working on under a home's
// product records — a run, a conversation's turn, a recurring pass and the
// watch session hosting it, the supervisor, the Slack sink — read by trying
// each lease the records keep without waiting and letting it go at once. A
// lease the operating system says is held belongs to a live process, because
// it drops one as its holder dies, so a run a redeploy preserved with nothing
// behind it is not in flight and moves with its records.
func HomeInFlight(root string) ([]string, error) {
	products, err := home.ProductIDs(root)
	if err != nil {
		return nil, err
	}
	var named []string
	for _, id := range products {
		directory := home.ProductDirectory(root, id)
		var held []string
		watching := false
		err := filepath.WalkDir(directory, func(target string, entry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".lease") || strings.HasSuffix(entry.Name(), ".lock")) {
				return nil
			}
			lease, taken, err := TryLeasePath(target, "the lease at "+target)
			if err != nil {
				return err
			}
			if taken {
				return lease.Release()
			}
			relative, err := filepath.Rel(directory, target)
			if err != nil {
				return err
			}
			if relative == watchLeaseFile {
				watching = true
			}
			held = append(held, describeHeldLease(root, id, filepath.ToSlash(relative)))
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("read what is in flight for %s: %w", id, err)
		}
		if watching {
			held = append(held, passesInFlight(root, id)...)
		}
		sort.Strings(held)
		for _, thing := range held {
			named = append(named, thing+" of "+id)
		}
	}
	return named, nil
}

// describeHeldLease names what a held lease is the lease of, in words.
func describeHeldLease(root, productID, relative string) string {
	switch {
	case relative == watchLeaseFile:
		described := "the watch session"
		if store, err := NewWatchStore(root, domain.ProductID(productID)); err == nil {
			if holder, found, err := store.Holder(); err == nil && found {
				described = fmt.Sprintf("the watch session %s (pid %d)", holder.SessionID, holder.PID)
			}
		}
		return described
	case strings.HasPrefix(relative, "runs/") && strings.HasSuffix(relative, ".lease"):
		runID := strings.TrimSuffix(strings.TrimPrefix(relative, "runs/"), ".lease")
		if store, err := NewStore(root, domain.ProductID(productID)); err == nil {
			if state, err := store.Read(runID); err == nil && strings.TrimSpace(state.WorkItemID) != "" {
				return fmt.Sprintf("run %s (%s)", runID, state.WorkItemID)
			}
		}
		return "run " + runID
	case strings.HasPrefix(relative, "conversations/") && strings.HasSuffix(relative, ".lease"):
		return fmt.Sprintf("a turn of the %s conversation", strings.TrimSuffix(strings.TrimPrefix(relative, "conversations/"), ".lease"))
	case filepath.Base(relative) == supervisorLeaseFile:
		return "the product's supervisor"
	case relative == "slack/.sink.lock":
		return "the Slack sink"
	case relative == "slack/.pass.lock":
		return "a pass of the Slack sink"
	default:
		return "the process holding " + relative
	}
}

// passesInFlight names the recurring passes a watch session holding the lease
// has claimed and not yet settled.
func passesInFlight(root, productID string) []string {
	store, err := NewSweepStore(root, domain.ProductID(productID))
	if err != nil {
		return nil
	}
	claims, err := filepath.Glob(filepath.Join(store.Root(), "claim-*.json"))
	if err != nil {
		return nil
	}
	var passes []string
	for _, claim := range claims {
		task := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(claim), "claim-"), ".json")
		found, ok, err := store.Find(task)
		if err == nil && ok && !found.Settled() {
			passes = append(passes, "the recurring pass "+task)
		}
	}
	return passes
}

func joinNamed(named []string) string {
	switch len(named) {
	case 0:
		return "nothing"
	case 1:
		return named[0]
	case 2:
		return named[0] + " and " + named[1]
	default:
		return strings.Join(named[:len(named)-1], ", ") + ", and " + named[len(named)-1]
	}
}

// RewriteMigratedWorktrees rewrites the worktree path on every run record of the
// products under to that names a worktree the migration moved out of from, to
// the path the worktree now has, and keeps the path it had in the record's
// HomeMigration. Nothing else about a record changes, so a preserved run is
// re-adopted with every counter it had. A record naming a path whose worktree
// is not at its new place is left as it is, which is what makes this as safe to
// repeat as the moves: a second migration rewrites only what the first did not.
// It returns each rewrite as a move of the recorded path.
func RewriteMigratedWorktrees(from, to string, products []string, now time.Time, by string) ([]home.Moved, []home.LeftBehind, error) {
	var rewritten []home.Moved
	var left []home.LeftBehind
	for _, id := range products {
		store, err := NewStore(to, domain.ProductID(id))
		if err != nil {
			return rewritten, left, err
		}
		runs, unreadable, err := store.RecordedReadable()
		if err != nil {
			return rewritten, left, fmt.Errorf("read the runs of %s: %w", id, err)
		}
		for _, skipped := range unreadable {
			left = append(left, home.LeftBehind{What: "a run record of " + id, Path: filepath.Join(store.Root(), skipped.Record),
				Why: "it could not be read, so its recorded worktree path was not rewritten: " + skipped.Err.Error(), Failed: true})
		}
		for _, recorded := range runs {
			moved, ok := home.MigratedWorktreePath(from, to, id, recorded.WorktreePath)
			if !ok {
				continue
			}
			if _, err := os.Lstat(moved); err != nil {
				continue
			}
			// The path is recorded resolved, as the worktree manager records the
			// paths it cuts and compares them: a home reached through a symlink
			// would otherwise name a worktree its own manager calls another.
			if resolved, err := filepath.EvalSymlinks(moved); err == nil {
				moved = resolved
			}
			state, err := store.Load(recorded.RunID)
			if err != nil {
				left = append(left, home.LeftBehind{What: "the recorded worktree path of run " + recorded.RunID, Path: recorded.WorktreePath, Why: err.Error(), Failed: true})
				continue
			}
			before := state.WorktreePath
			state.WorktreePath = moved
			state.HomeMigration = &HomeMigration{At: now.UTC(), FromWorktreePath: before, By: by}
			if err := store.Save(state); err != nil {
				left = append(left, home.LeftBehind{What: "the recorded worktree path of run " + recorded.RunID, Path: before, Why: err.Error(), Failed: true})
				continue
			}
			rewritten = append(rewritten, home.Moved{What: fmt.Sprintf("the recorded worktree path of run %s (%s)", state.RunID, state.WorkItemID), From: before, To: moved})
		}
	}
	return rewritten, left, nil
}
