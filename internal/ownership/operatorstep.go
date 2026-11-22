package ownership

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Checking the step a request to the operator asks him to take.
//
// A reason from the closed list says why only he can act; it does not say the
// step can be taken. A request naming him for "protected-file" can name a
// folder the repository does not have, or a file a developer run may change,
// and either way he is asked for something he cannot do or need not do. A
// request naming a hold he placed himself can outlive the hold. So the step is
// checked against the world when the request is recorded, and again while it
// waits: the paths a "protected-file" step names must exist and be beyond every
// grant, and an "own-hold" request must name a hold that still stands. The
// other reasons name nothing this can look at, and pass.

// OperatorStepRecheckAfter is how long a request naming the operator waits on
// his list before the paths its step names are checked again. A hold is checked
// on every reading, because the moment it is lifted nothing waits on him.
const OperatorStepRecheckAfter = 72 * time.Hour

// StepWorld is what a step is checked against. Each part is optional, and a
// part left out is not checked rather than read as failing: a check that cannot
// look refuses nothing.
type StepWorld struct {
	// Repository is the checkout a relative path in the step is read in.
	Repository string
	// Home is the person's home directory, for a path written from "~/".
	Home string
	// OwnHold says whether a hold or pause the operator placed himself stands
	// now. An error is read as standing, because a hold nobody can read must
	// never be treated as lifted.
	OwnHold func() (bool, error)
}

// CheckOperatorStep refuses a request naming the operator for a reason whose
// step cannot be carried out, or is not his: a "protected-file" step naming no
// file, a file that does not exist, or a file a developer run may change, and
// an "own-hold" request when no hold he placed stands. The step is the request's
// words, and remedy is the person-only remedy it carries, where it carries one.
func CheckOperatorStep(reason OperatorReason, step string, remedy *PersonOnlyRemedy, world StepWorld) error {
	switch reason {
	case OperatorProtectedFile:
		return checkProtectedFileStep(step, remedy, world)
	case OperatorOwnHold:
		if world.OwnHold == nil {
			return nil
		}
		if stands, err := world.OwnHold(); err == nil && !stands {
			return errors.New(`it names a hold or pause the operator placed himself, and none stands now, so nothing waits on him; a request for a hold that is gone is recorded as done rather than put to him`)
		}
	}
	return nil
}

// RecheckOperatorStep is CheckOperatorStep for a request already waiting on
// the operator's list since recordedAt: a hold is checked on every reading, and
// the paths a step names once the request has waited OperatorStepRecheckAfter.
func RecheckOperatorStep(reason OperatorReason, step string, remedy *PersonOnlyRemedy, recordedAt, now time.Time, world StepWorld) error {
	if reason != OperatorOwnHold && (now.IsZero() || recordedAt.IsZero() || now.Sub(recordedAt) < OperatorStepRecheckAfter) {
		return nil
	}
	return CheckOperatorStep(reason, step, remedy, world)
}

func checkProtectedFileStep(step string, remedy *PersonOnlyRemedy, world StepWorld) error {
	named := NamedPaths(step)
	if remedy != nil {
		named = appendNew(named, NamedPaths(remedy.Target)...)
		if target := strings.TrimSpace(remedy.Target); target != "" {
			named = appendNew(named, target)
		}
		named = appendNew(named, NamedPaths(remedy.Step)...)
	}
	if len(named) == 0 {
		return errors.New(`it is given as "protected-file" and names no file, so nobody can tell which file only a person may change; name the file, such as .claude/settings.json`)
	}
	var problems []error
	for _, named := range named {
		if err := checkProtectedPath(named, world); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// checkProtectedPath is one named path: it has to exist, where the check can
// look, and only a person may change it. A path outside the repository is out
// of every developer run's reach, so only its existence is asked.
func checkProtectedPath(named string, world StepWorld) error {
	var onDisk string
	inRepository := true
	switch {
	case strings.HasPrefix(named, "~/"):
		inRepository = false
		if world.Home != "" {
			onDisk = filepath.Join(world.Home, filepath.FromSlash(strings.TrimPrefix(named, "~/")))
		}
	case filepath.IsAbs(named):
		onDisk = filepath.Clean(named)
		inRepository = false
		if world.Repository != "" {
			if relative, err := filepath.Rel(world.Repository, onDisk); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				inRepository, named = true, filepath.ToSlash(relative)
			}
		}
	default:
		if world.Repository != "" {
			onDisk = filepath.Join(world.Repository, filepath.FromSlash(path.Clean(named)))
		}
	}
	if onDisk != "" {
		if _, err := os.Lstat(onDisk); errors.Is(err, os.ErrNotExist) {
			where := "the repository"
			if !inRepository {
				where = "this machine"
			}
			return fmt.Errorf("%s does not exist in %s, so the step naming it cannot be carried out; name the file that is there, or say what has to be created and why only a person may create it", named, where)
		}
	}
	// The folder the role definitions are kept in is as far beyond a grant as
	// the definitions in it.
	if inRepository && !personOnlyFile(named) && !strings.EqualFold(path.Clean(named), ".yoyodyne/roles") {
		return fmt.Errorf("%s is a file a developer run may change, so changing it is work to admit rather than a step only a person can take; only Claude Code's settings files and the role definitions under .yoyodyne/roles are beyond every grant", named)
	}
	return nil
}

// pathPattern is the characters a path in a step is written in.
var pathPattern = regexp.MustCompile(`^[A-Za-z0-9._~/-]+$`)

// NamedPaths is every file or folder path written in a step, in the order
// written. A word counts as a path when it holds a slash and reads as one: it
// starts with ".", "~/" or "/", its last part has an extension, or it ends in a
// slash. That leaves out "and/or", a date, and a link, which hold a slash and
// name no file.
func NamedPaths(step string) []string {
	var paths []string
	for _, word := range strings.Fields(step) {
		word = strings.TrimLeft(word, "\"'`([{<")
		word = strings.TrimRight(word, "\"'`)]}>,;:.!?")
		if !strings.Contains(word, "/") || strings.Contains(word, "://") || !pathPattern.MatchString(word) {
			continue
		}
		if strings.Trim(word, "/.~") == "" {
			continue
		}
		last := path.Base(word)
		if strings.HasPrefix(word, ".") || strings.HasPrefix(word, "~/") || strings.HasPrefix(word, "/") ||
			strings.HasSuffix(word, "/") || strings.Contains(strings.TrimPrefix(last, "."), ".") {
			paths = appendNew(paths, word)
		}
	}
	return paths
}

func appendNew(paths []string, more ...string) []string {
	for _, candidate := range more {
		seen := false
		for _, existing := range paths {
			if path.Clean(existing) == path.Clean(candidate) {
				seen = true
				break
			}
		}
		if !seen {
			paths = append(paths, candidate)
		}
	}
	return paths
}
