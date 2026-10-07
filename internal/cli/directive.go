package cli

// Recording and settling the operator's durable directives.
//
// This is the administrative half of directives, and it exists so that a
// directive an operator gave to an agent other than the product manager can
// still be written down. The design says a directive applies regardless of which
// agent received it; a command that only worked from inside one conversation
// would make that true of one agent. What the record says about who received it
// is attribution, not routing: every run of every item reads the same records
// whatever role is named here.
//
// Recording an ambiguous or artifact-changing directive pauses work. That is the
// point of it rather than a side effect, so these commands say plainly what they
// stopped, and resolving one says plainly what it released.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type directiveOutput struct {
	Directives []directive.Directive `json:"directives,omitempty"`
	Error      string                `json:"error,omitempty"`
}

func runDirective(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printDirectiveUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listDirectives(args[1:], stdout, stderr)
	case "record":
		return recordDirective(args[1:], stdout, stderr)
	case "resolve":
		return resolveDirective(args[1:], stdout, stderr)
	case "withdraw":
		return withdrawDirective(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown directive command %q\n\n", args[0])
		printDirectiveUsage(stderr)
		return 2
	}
}

func listDirectives(args []string, stdout, stderr io.Writer) int {
	flags := newDirectiveFlags("directive list", stderr)
	all := flags.set.Bool("all", false, "include directives that no longer apply")
	if code, ok := flags.parse(args, 0); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	recorded, err := store.List()
	if err != nil {
		return reportDirectiveError(stdout, stderr, *flags.jsonOutput, err)
	}
	listed := recorded
	if !*all {
		// What an operator asks for by default is what still applies: the
		// directives holding work up, and the standing instructions in force. A
		// directive that no longer applies is a record of something that happened,
		// and mixing the two would bury the ones that still constrain the work.
		//
		// This asks whether the directive is in force rather than whether anybody
		// has accounted for it. An operational directive somebody carried out is
		// accounted for and still standing, and dropping it here on the strength of
		// its outcome would retire an instruction the operator never withdrew — from
		// the one listing they read to find out what is still in force.
		listed = nil
		for _, candidate := range recorded {
			if candidate.InForce() {
				listed = append(listed, candidate)
			}
		}
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, directiveOutput{Directives: listed})
	}
	if len(listed) == 0 {
		if *all {
			fmt.Fprintln(stdout, "no directives are recorded for this product")
		} else {
			fmt.Fprintln(stdout, "no directives are active for this product")
		}
		return 0
	}
	for _, recorded := range listed {
		fmt.Fprint(stdout, recorded.Render())
	}
	return 0
}

func recordDirective(args []string, stdout, stderr io.Writer) int {
	flags := newDirectiveFlags("directive record", stderr)
	kind := flags.set.String("kind", string(directive.KindOperational),
		"operational, artifact, or ambiguous; the last two pause the work they affect")
	receivedBy := flags.set.String("received-by", string(domain.RoleProductManager),
		"the role that was told this")
	artifact := flags.set.String("artifact", "", "the governed artifact an artifact directive changes")
	unresolved := flags.set.String("unresolved", "",
		"what has to be settled before the work this affects can carry on")
	scope := flags.set.String("scope", "",
		"comma-separated work items this affects (default: every item)")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	id, err := directive.NewID()
	if err != nil {
		return reportDirectiveError(stdout, stderr, *flags.jsonOutput, err)
	}
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     flags.productID,
		Kind:          directive.Kind(strings.TrimSpace(*kind)),
		ReceivedBy:    domain.AgentRole(strings.TrimSpace(*receivedBy)),
		ReceivedAt:    time.Now().UTC(),
		Text:          strings.TrimSpace(flags.argument()),
		Artifact:      strings.TrimSpace(*artifact),
		Unresolved:    strings.TrimSpace(*unresolved),
		Scope:         splitList(*scope),
	}
	if err := store.Record(recorded); err != nil {
		return reportDirectiveError(stdout, stderr, *flags.jsonOutput, err)
	}
	noteHandStepFor(*flags.configPath, stderr, handStep{
		kind:    intervention.KindDirective,
		items:   recorded.Scope,
		subject: recorded.ID,
		said:    "recorded " + string(recorded.Kind) + " directive " + recorded.ID + " with yoyo directive record",
	})
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, directiveOutput{Directives: []directive.Directive{recorded}})
	}
	fmt.Fprint(stdout, recorded.Render())
	if recorded.Pauses() {
		// A directive that pauses work is the whole reason this is enforced rather
		// than filed, so the command says what it just stopped rather than leaving
		// the operator to notice work going quiet.
		fmt.Fprintln(stdout, "the work this affects is paused: a run in flight for it stops at its next gate, and nothing new starts on it.")
		fmt.Fprintf(stdout, "`yoyo directive resolve %s` settles it and lets that work carry on.\n", recorded.ID)
	} else {
		fmt.Fprintln(stdout, "this directive is in effect from now; nothing is paused by it.")
	}
	return 0
}

func resolveDirective(args []string, stdout, stderr io.Writer) int {
	flags := newDirectiveFlags("directive resolve", stderr)
	resolution := flags.set.String("resolution", "",
		"how it was settled: the answer to the question, or what was decided about the artifact")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	resolved, err := store.Resolve(flags.argument(), *resolution, time.Now())
	if err != nil {
		return reportDirectiveError(stdout, stderr, *flags.jsonOutput, err)
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, directiveOutput{Directives: []directive.Directive{resolved}})
	}
	fmt.Fprint(stdout, resolved.Render())
	fmt.Fprintln(stdout, "the work this paused can carry on; a run it stopped continues from where it stopped when it is started again.")
	return 0
}

// withdrawDirective takes a directive out of force. It is the only act that ends
// an operational directive, and who did it is the one thing the record cannot
// work out for itself, so this asks rather than assuming.
//
// There is deliberately no default. A command line is not proof of an operator:
// agents run this binary too, under the harness, and a default of "the operator"
// would put the operator's name on a directive an agent ended — on the single
// field that exists to say who ended it, in a record whose whole purpose is to
// still be answerable long after the run that wrote it. An approval can assume
// its typist because approving is refused to everything but a person; nothing
// refuses an agent a command line, so this is the honest arrangement rather than
// the convenient one.
func withdrawDirective(args []string, stdout, stderr io.Writer) int {
	flags := newDirectiveFlags("directive withdraw", stderr)
	reason := flags.set.String("reason", "",
		"why you no longer mean it; required, because the record keeps what was said and this is what says why it stopped applying")
	by := flags.set.String("by", "", "who is withdrawing it; required, and it is what the record answers for who ended the directive")
	as := flags.set.String("as", "",
		"the role withdrawing it, where an agent is: developer, reviewer, architect, development-manager, or product-manager; leave it out if you are the operator")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	if strings.TrimSpace(*by) == "" {
		// Refused here rather than left to the record, so the message can say what
		// to put there. The record refuses it too, which is what holds for every
		// other caller.
		return reportDirectiveError(stdout, stderr, *flags.jsonOutput,
			errors.New("say who is withdrawing it, as --by: yourself if you are the operator, or the agent and the run if a run is doing it; a command line does not say who typed at it"))
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	// The role is what a thread the directive came from is answered in the voice
	// of, and the record refuses one the harness does not have.
	withdrawn, err := store.Withdraw(flags.argument(), *by, domain.AgentRole(strings.TrimSpace(*as)), *reason, time.Now())
	if err != nil {
		return reportDirectiveError(stdout, stderr, *flags.jsonOutput, err)
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, directiveOutput{Directives: []directive.Directive{withdrawn}})
	}
	fmt.Fprint(stdout, withdrawn.Render())
	// Said every time, because the one way a withdrawal could quietly become
	// something else is somebody reading it as the record being cleaned up. It is
	// not: the directive is still there, still says what the operator said, and is
	// listed by `directive list --all` as withdrawn.
	fmt.Fprintln(stdout, "this directive no longer applies; nothing is enforced against it and no run is held by it from now.")
	fmt.Fprintln(stdout, "nothing was deleted: what it said is kept, and `yoyo directive list --all` shows it as withdrawn.")
	if withdrawn.Kind.Pauses() && !withdrawn.Resolved() {
		fmt.Fprintln(stdout, "the work it paused can carry on, without what it was waiting for having been answered.")
	}
	return 0
}

// directiveFlags is the flag set every directive command shares: which
// configuration to read the product from, and how to report the result.
type directiveFlags struct {
	set        *flag.FlagSet
	name       string
	configPath *string
	jsonOutput *bool
	// productID is filled in when the store is built, because recording a
	// directive has to stamp it with the product whose records it lands in.
	productID domain.ProductID
	// args are the positional arguments, collected by parse rather than read off
	// the flag set, because the flags may come after them.
	args []string
}

func newDirectiveFlags(name string, stderr io.Writer) *directiveFlags {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return &directiveFlags{
		set:        set,
		name:       name,
		configPath: set.String("config", "", "configuration file path (default: the nearest project configuration)"),
		jsonOutput: set.Bool("json", false, "emit machine-readable JSON"),
	}
}

// parse reads the flags and the positional arguments, in whatever order they
// were typed, which is what parseArguments is for: `directive resolve <id>
// --resolution ...` is how anybody settles one out of a listing.
func (f *directiveFlags) parse(args []string, positional int) (int, bool) {
	parsed, err := parseArguments(f.set, args)
	if err != nil {
		return 2, false
	}
	f.args = parsed
	if len(f.args) != positional {
		switch {
		case positional == 0:
			fmt.Fprintf(f.set.Output(), "%s does not accept positional arguments\n", f.name)
		case f.name == "directive record":
			fmt.Fprintf(f.set.Output(), "%s requires exactly one argument: what the operator said\n", f.name)
		default:
			fmt.Fprintf(f.set.Output(), "%s requires exactly one directive id\n", f.name)
		}
		printDirectiveUsage(f.set.Output())
		return 2, false
	}
	return 0, true
}

// argument is what a command was given to act on: the directive id for the
// commands that name one, and what the operator said for `record`.
func (f *directiveFlags) argument() string {
	return argumentAt(f.args, 0)
}

// store resolves the same product-scoped directive records every run reads, so a
// directive recorded here reaches the runs it affects rather than landing in a
// second pile beside them.
func (f *directiveFlags) store(stderr io.Writer) (*runstate.DirectiveStore, int) {
	parts, err := buildComponents(*f.configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}
	f.productID = parts.config.Product.ID
	return parts.directives, 0
}

func reportDirectiveError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, directiveOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	// Everything reaching here failed for a reason the command stated, which is a
	// different thing from the exit code 2 a mistyped command gets.
	return 1
}

func printDirectiveUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo directive <list|record|resolve|withdraw> [options]

A directive is something you told an agent to do. It is recorded for the product
rather than for the agent that heard it, so it reaches the work it affects
whichever agent that was.

Most directives are operational: they take effect immediately and nothing waits
for them. Two kinds pause the work they affect, because the work would otherwise
be done against intent that is being rewritten or was never settled:

  --kind artifact     it changes a governed artifact, and work derived from that
                      artifact waits until the change is decided
  --kind ambiguous    nobody can act on it without deciding something you did
                      not, and the work waits until you answer

A paused run is not cancelled. It keeps its claim, its branch, and its worktree,
and it continues from where it stopped once the directive is resolved.

  list [--all]                         the active directives, or every one
  record [options] <what you said>     record one, pausing what it affects
  resolve --resolution <how> <id>      settle one and release the work it paused
  withdraw --by <who> [--as <role>] --reason <why> <id>
                                       take one back; it stops applying

A directive that pauses work stops applying when it is resolved. An
operational one applies from the moment it is recorded and stays there:
recording what came of it says what it produced, and does not withdraw it.

Withdrawing is what ends a directive of any kind, and it is the only thing that
ends an operational one. It records who took it back and when, and it deletes
nothing: what you said is kept, list --all shows it as withdrawn, and from then
on nothing is enforced against it. Withdrawing one that pauses work lifts
that pause without answering what it was waiting for, which is what taking back
a question means.

Who is asked for rather than assumed. Agents run this binary too, so a command
line does not say who typed at it, and --by is the one field the record answers
for who ended the directive. An agent withdrawing one names its role with --as,
which is the voice a Slack thread the directive came from is answered in; the
operator leaves it out.

An id may be shortened to any prefix that names exactly one directive.

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON

record options:
  --kind <kind>         operational (default), artifact, or ambiguous
  --received-by <role>  the role you said it to (default: product-manager)
  --artifact <name>     the governed artifact an artifact directive changes
  --unresolved <what>   what has to be settled; required on the two pausing kinds
  --scope <items>       comma-separated work items (default: every item)

withdraw options:
  --reason <why>        why you no longer mean it; required
  --by <who>            who is withdrawing it; required
  --as <role>           the role withdrawing it, where an agent is; the operator leaves it out`)
}
