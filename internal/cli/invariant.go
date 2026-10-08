package cli

// The architect's own lifecycle for durable architectural invariants: create,
// amend, retire, and read them back.
//
// The architect agent does not execute yet, and this is how its artifacts are
// reachable until it does. The operator is the one role that may direct any
// agent, so these commands act with the architect's authority and record that
// they did — which is the documented override path rather than a second owner.
// Every mutation goes through the same package the harness reads with, so the
// authorization boundary is the same one a future architect agent will meet.

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
)

type invariantOutput struct {
	Invariants []invariant.Invariant `json:"invariants,omitempty"`
	Problems   []invariant.Problem   `json:"problems,omitempty"`
	Error      string                `json:"error,omitempty"`
}

func runInvariant(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printInvariantUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listInvariants(args[1:], stdout, stderr)
	case "show":
		return showInvariant(args[1:], stdout, stderr)
	case "create":
		return createInvariant(args[1:], stdout, stderr)
	case "amend":
		return amendInvariant(args[1:], stdout, stderr)
	case "retire":
		return retireInvariant(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown invariant command %q\n\n", args[0])
		printInvariantUsage(stderr)
		return 2
	}
}

func listInvariants(args []string, stdout, stderr io.Writer) int {
	flags := newInvariantFlags("invariant list", stderr)
	all := flags.set.Bool("all", false, "include retired invariants")
	if code, ok := flags.parse(args, 0); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	set, err := store.Load()
	if err != nil {
		return reportInvariantError(stdout, stderr, *flags.jsonOutput, err)
	}
	listed := set.Active
	if *all {
		listed = set.All()
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, invariantOutput{Invariants: listed, Problems: set.Problems})
	}
	if len(listed) == 0 {
		fmt.Fprintf(stdout, "no invariants are recorded in %s\n", set.Directory)
	}
	for _, recorded := range listed {
		fmt.Fprintf(stdout, "%s [%s] %s\n", recorded.ID, recorded.Status, recorded.Title)
		fmt.Fprintf(stdout, "  scope: %s\n", invariantScope(recorded))
	}
	// A file that is not an invariant is a gap in what the harness delivers, so
	// it is reported here rather than only inside a run's prompts.
	for _, problem := range set.Problems {
		fmt.Fprintf(stderr, "not an invariant: %s\n", problem)
	}
	return 0
}

func showInvariant(args []string, stdout, stderr io.Writer) int {
	flags := newInvariantFlags("invariant show", stderr)
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	set, err := store.Load()
	if err != nil {
		return reportInvariantError(stdout, stderr, *flags.jsonOutput, err)
	}
	found, ok := set.Find(flags.id())
	if !ok {
		return reportInvariantError(stdout, stderr, *flags.jsonOutput,
			fmt.Errorf("no invariant %q is recorded in %s", flags.id(), set.Directory))
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, invariantOutput{Invariants: []invariant.Invariant{found}})
	}
	fmt.Fprintf(stdout, "%s [%s] %s\n", found.ID, found.Status, found.Title)
	fmt.Fprintf(stdout, "file: %s\n", found.Path)
	fmt.Fprintf(stdout, "scope: %s\n", invariantScope(found))
	fmt.Fprintf(stdout, "established by: %s\n\n", strings.Join(found.EstablishedBy, ", "))
	fmt.Fprintf(stdout, "must hold:\n%s\n\nwhy:\n%s\n\n", found.Statement, found.Rationale)
	for _, revision := range found.Revisions {
		fmt.Fprintf(stdout, "%s %s by the %s: %s\n",
			revision.At.UTC().Format(time.RFC3339), revision.Action, revision.By, revision.Reason)
	}
	return 0
}

func createInvariant(args []string, stdout, stderr io.Writer) int {
	flags := newInvariantFlags("invariant create", stderr)
	title := flags.set.String("title", "", "one line naming what the constraint is")
	statement := flags.set.String("statement", "", "what must hold, stated tightly enough to be held to")
	rationale := flags.set.String("rationale", "", "why it holds, so a later reader can judge whether it still does")
	establishedBy := flags.set.String("established-by", "", "comma-separated work items or designs this came from")
	scope := flags.set.String("scope", "", "comma-separated repository paths it constrains (default: the whole repository)")
	reason := flags.set.String("reason", "", "why this invariant is being recorded now")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	created, err := store.Create(domain.RoleArchitect, invariant.Draft{
		ID:            flags.id(),
		Title:         *title,
		Statement:     *statement,
		Rationale:     *rationale,
		EstablishedBy: splitList(*establishedBy),
		Scope:         splitList(*scope),
		Reason:        *reason,
	}, time.Now())
	if err != nil {
		return reportInvariantError(stdout, stderr, *flags.jsonOutput, err)
	}
	return reportInvariant(stdout, stderr, *flags.jsonOutput, created, "recorded")
}

func amendInvariant(args []string, stdout, stderr io.Writer) int {
	flags := newInvariantFlags("invariant amend", stderr)
	title := flags.set.String("title", "", "replace the title")
	statement := flags.set.String("statement", "", "replace what must hold")
	rationale := flags.set.String("rationale", "", "replace why it holds")
	establishedBy := flags.set.String("established-by", "", "replace the work items or designs this came from")
	scope := flags.set.String("scope", "", "replace the repository paths it constrains")
	reason := flags.set.String("reason", "", "why the invariant is changing")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	// Only the flags actually given are changes; an amendment says exactly what
	// it amends rather than rewriting the fields nobody mentioned.
	amendment := invariant.Amendment{Reason: *reason}
	flags.set.Visit(func(supplied *flag.Flag) {
		switch supplied.Name {
		case "title":
			amendment.Title = title
		case "statement":
			amendment.Statement = statement
		case "rationale":
			amendment.Rationale = rationale
		case "established-by":
			amendment.EstablishedBy = listPointer(*establishedBy)
		case "scope":
			amendment.Scope = listPointer(*scope)
		}
	})
	amended, err := store.Amend(domain.RoleArchitect, flags.id(), amendment, time.Now())
	if err != nil {
		return reportInvariantError(stdout, stderr, *flags.jsonOutput, err)
	}
	return reportInvariant(stdout, stderr, *flags.jsonOutput, amended, "amended")
}

func retireInvariant(args []string, stdout, stderr io.Writer) int {
	flags := newInvariantFlags("invariant retire", stderr)
	reason := flags.set.String("reason", "", "why the constraint no longer applies")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	retired, err := store.Retire(domain.RoleArchitect, flags.id(), *reason, time.Now())
	if err != nil {
		return reportInvariantError(stdout, stderr, *flags.jsonOutput, err)
	}
	// The file stays: a retired invariant is a recorded decision to stop
	// enforcing something, which is not the same as an invariant that vanished.
	return reportInvariant(stdout, stderr, *flags.jsonOutput, retired, "retired")
}

// invariantFlags is the flag set every invariant command shares: which
// configuration to read the directory from, and how to report the result.
type invariantFlags struct {
	set        *flag.FlagSet
	name       string
	configPath *string
	jsonOutput *bool
	// args are the positional arguments, collected by parse rather than read off
	// the flag set, because the flags may come after them.
	args []string
}

func newInvariantFlags(name string, stderr io.Writer) *invariantFlags {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return &invariantFlags{
		set:        set,
		name:       name,
		configPath: set.String("config", "", "configuration file path (default: the nearest project configuration)"),
		jsonOutput: set.Bool("json", false, "emit machine-readable JSON"),
	}
}

// parse reads the flags and the positional arguments, in whatever order they
// were typed, which is what parseArguments is for: `invariant show <id> --json`
// is how anybody reads one out of a listing.
func (f *invariantFlags) parse(args []string, positional int) (int, bool) {
	parsed, err := parseArguments(f.set, args)
	if err != nil {
		return 2, false
	}
	f.args = parsed
	if len(f.args) != positional {
		if positional == 0 {
			fmt.Fprintf(f.set.Output(), "%s does not accept positional arguments\n", f.name)
		} else {
			fmt.Fprintf(f.set.Output(), "%s requires exactly one invariant id\n", f.name)
		}
		printInvariantUsage(f.set.Output())
		return 2, false
	}
	return 0, true
}

// id is the invariant a command was given, for the commands that take one.
func (f *invariantFlags) id() string {
	return argumentAt(f.args, 0)
}

// store resolves the invariants directory the same way every other command
// resolves the repository: relative to the project rather than to the .yoyodyne
// directory the configuration happens to live in.
func (f *invariantFlags) store(stderr io.Writer) (invariant.Store, int) {
	resolved, err := loadConfiguration(*f.configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return invariant.Store{}, 1
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		fmt.Fprintf(stderr, "resolve product repository: %v\n", err)
		return invariant.Store{}, 1
	}
	return invariant.StoreFor(repository, resolved.Config.Product), 0
}

func reportInvariant(stdout, stderr io.Writer, jsonOutput bool, recorded invariant.Invariant, action string) int {
	if jsonOutput {
		return writeJSON(stdout, stderr, invariantOutput{Invariants: []invariant.Invariant{recorded}})
	}
	fmt.Fprintf(stdout, "%s %s in %s\n", action, recorded.ID, recorded.Path)
	return 0
}

func reportInvariantError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, invariantOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	// Everything that reaches here failed for a reason the command stated, which
	// is a different thing from the exit code 2 a mistyped command gets.
	return 1
}

func invariantScope(recorded invariant.Invariant) string {
	if len(recorded.Scope) == 0 {
		return "the whole repository"
	}
	return strings.Join(recorded.Scope, ", ")
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	list := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			list = append(list, trimmed)
		}
	}
	return list
}

// listPointer makes an amendment's list field addressable, including when the
// operator supplied an empty one: clearing a scope is a real amendment, and it
// has to be distinguishable from not mentioning the scope at all.
func listPointer(value string) *[]string {
	list := splitList(value)
	return &list
}

func printInvariantUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo invariant <list|show|create|amend|retire> [options]

Durable architectural invariants are the architect's: cross-cutting constraints
a work item must not break even when its own change looks correct. The harness
delivers the ones relevant to a work item into the developer's context and the
reviewer's evidence, so nobody has to transcribe one into a bead. These commands
act with the architect's authority and record that they did.

  list [--all]                    list the active invariants, or every recorded one
  show [options] <id>             print one invariant and its recorded revisions
  create [options] <id>           record a new invariant
  amend [options] <id>            change one that exists, recording why
  retire --reason <why> <id>      stop enforcing one; the file and the reason stay

Options may come before or after the invariant id, as they may for every other
command that takes one.

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON

create options:
  --title, --statement, --rationale, --established-by, --reason are required
  --scope <paths>       comma-separated repository paths (default: the whole repository)

amend options:
  --reason is required; --title, --statement, --rationale, --established-by,
  and --scope each replace what they name and leave the rest alone`)
}
