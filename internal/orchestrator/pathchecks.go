package orchestrator

import (
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
)

// addedCheck is a path check the per-run gate runs for this change, and why.
type addedCheck struct {
	command string
	// paths is the check's paths file, which with its command is how
	// describePathChecks tells the configured checks the gate added from the
	// ones it passed over.
	paths  string
	reason string
	// providerCLIs is the path check's needs_provider_clis.
	providerCLIs bool
}

// pathChecksFor chooses which of the configured path checks this change runs:
// each one whose paths file covers a path the change touches.
//
// A change that touches the paths file itself runs the check whatever the file
// now says. Otherwise the author of a change could narrow or empty the list and
// switch off the check that exists to hold that change, which would leave the
// gate resting on the author again. So the worktree's copy of a list only ever
// decides for a change that left the list as the target branch holds it, and
// widening a list costs the change that widens it one run of the check. A check
// whose list cannot be read runs rather than being passed over, because a gate
// that skipped it would be deciding from a declaration it does not have — and
// the reason says so, so the stage's record names what to fix.
func pathChecksFor(root string, configured []config.PathCheck, changed []string) []addedCheck {
	var added []addedCheck
	for _, check := range configured {
		if checks.ChangesFile(changed, check.Paths) {
			added = append(added, addedCheck{
				command:      check.Command,
				paths:        check.Paths,
				providerCLIs: check.NeedsProviderCLIs,
				reason:       "the change touches " + check.Paths + " itself, and a list is never judged by the change that edits it",
			})
			continue
		}
		patterns, err := checks.ReadPathPatterns(root, check.Paths)
		if err != nil {
			added = append(added, addedCheck{
				command:      check.Command,
				paths:        check.Paths,
				providerCLIs: check.NeedsProviderCLIs,
				reason:       "its paths file could not be read, so it runs rather than being passed over: " + err.Error(),
			})
			continue
		}
		if touched, ok := checks.Touching(patterns, changed); ok {
			added = append(added, addedCheck{
				command:      check.Command,
				paths:        check.Paths,
				providerCLIs: check.NeedsProviderCLIs,
				reason:       "the change touches " + touched + ", which " + check.Paths + " lists",
			})
		}
	}
	return added
}

// withPathChecks is the configured checks followed by the ones added for this
// change, and which of them, by position, run with the provider CLIs on their
// search path.
func withPathChecks(configured []string, added []addedCheck) ([]string, map[int]bool) {
	commands := append([]string(nil), configured...)
	var providerCLIs map[int]bool
	for _, check := range added {
		if check.providerCLIs {
			if providerCLIs == nil {
				providerCLIs = map[int]bool{}
			}
			providerCLIs[len(commands)] = true
		}
		commands = append(commands, check.command)
	}
	return commands, providerCLIs
}

// describePathChecks says, for the stage's record, what the gate added and what
// it passed over: each configured path check is named either with the path that
// added it or as not run because the change touches nothing its list covers,
// so a run without one reads as having been judged not to need it rather than
// as having forgotten it. Nothing where no path check is configured.
func describePathChecks(configured []config.PathCheck, added []addedCheck) string {
	parts := make([]string, 0, len(configured))
	for _, check := range added {
		parts = append(parts, check.command+" added because "+check.reason)
	}
	for _, check := range configured {
		if !slices.ContainsFunc(added, func(ran addedCheck) bool { return ran.command == check.Command && ran.paths == check.Paths }) {
			parts = append(parts, check.Command+" not run because the change touches nothing "+check.Paths+" lists")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, "; ")
}
