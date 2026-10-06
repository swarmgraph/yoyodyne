package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// reportHome prints where the machine home is and which setting put it there.
// It is what a script beside the binary asks rather than reading the variables
// itself: a script that worked the home out from YOYODYNE_STATE_HOME and
// XDG_STATE_HOME alone would miss the machine file, and on a machine whose state
// has moved would read and write under a home the harness no longer uses. It
// resolves and reports, and records nothing.
func reportHome(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("home", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printHomeUsage(stderr) }
	pathOnly := flags.Bool("path", false, "print the home's path and nothing else, for a script")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "home does not accept positional arguments")
		return 2
	}
	resolved, err := runstate.ResolveRoot(os.Getenv, os.UserHomeDir, runtime.GOOS)
	if err != nil {
		return commandFailure(stdout, stderr, *jsonOutput, err)
	}
	machine, err := runstate.MachinePath(os.UserHomeDir)
	if err != nil {
		return commandFailure(stdout, stderr, *jsonOutput, err)
	}
	earlierLayout := home.EarlierLayout(resolved.Path)
	switch {
	case *pathOnly:
		fmt.Fprintln(stdout, resolved.Path)
		return 0
	case *jsonOutput:
		return writeJSON(stdout, stderr, map[string]any{
			"home":           resolved.Path,
			"origin":         resolved.Origin,
			"machine_file":   machine,
			"earlier_layout": earlierLayout,
			"projects":       filepath.Join(resolved.Path, home.ProjectsDirectoryName),
			"accounts":       filepath.Join(resolved.Path, "accounts"),
		})
	}
	fmt.Fprintf(stdout, "home: %s\n", resolved.Path)
	fmt.Fprintf(stdout, "from: %s\n", describeHomeOrigin(resolved))
	fmt.Fprintf(stdout, "machine file: %s\n", machine)
	if earlierLayout {
		fmt.Fprintln(stdout, "layout: the earlier one — each product's records under products/ and its worktrees under worktrees/, until the migration moves them")
	} else {
		fmt.Fprintf(stdout, "layout: one directory per project under %s\n", filepath.Join(resolved.Path, home.ProjectsDirectoryName))
	}
	return 0
}

// describeHomeOrigin says in words which setting put the home where it is.
func describeHomeOrigin(resolved runstate.ResolvedRoot) string {
	switch {
	case resolved.Origin == home.OriginEnvironment:
		return "YOYODYNE_STATE_HOME in this shell's environment"
	case resolved.Origin == home.OriginXDG:
		return "XDG_STATE_HOME in this shell's environment"
	case resolved.Origin == home.OriginDefault:
		return "the default, ~/" + home.DirectoryName
	case resolved.Origin == home.OriginEarlierDefault:
		return "the earlier builds' default, kept because it exists and ~/" + home.DirectoryName + " does not; nothing has moved it yet"
	case strings.HasPrefix(resolved.Origin, "machine:"):
		return "state_root in " + strings.TrimPrefix(resolved.Origin, "machine:")
	default:
		return resolved.Origin
	}
}

func printHomeUsage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`
Usage: yoyo home [options]

Print where the machine home is — ~/.yoyodyne unless YOYODYNE_STATE_HOME,
state_root in ~/.yoyodyne/machine.yaml, or XDG_STATE_HOME moves it, and the
earlier builds' default while that exists and ~/.yoyodyne does not — which of
those put it there, and how it is laid out. It records nothing. A script that
needs the home asks this rather than reading the variables itself.

Options:
  --path   print the home's path and nothing else
  --json   emit machine-readable JSON`))
}
