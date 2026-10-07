package cli

// Reading the changes agents have proposed to documents they do not own, and
// deciding them.
//
// A decision is the only thing these commands write, and it is written to the
// proposal rather than to the document: approving records that the owner's
// authority came down in favour of the change, and the change itself is then
// made in the artifact by the role that owns it, under a revision saying so.
// Nothing here edits an artifact, which is what keeps a proposal from being an
// edit somebody wrote in advance and got approved by another name.
//
// Who decides follows the same model as the invariant commands beside these.
// The owning role decides its own documents where it runs; the architect agent
// does not execute yet, so the operator decides in its stead, and the record
// says the operator exercised the architect's authority rather than pretending
// the architect answered.

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type amendmentOutput struct {
	Proposals []amendment.Proposal `json:"proposals,omitempty"`
	// Decided pairs each listed proposal with what became of it, keyed by
	// proposal id, so a machine-readable listing says which of them is still
	// waiting without the reader having to join two lists.
	Decided map[string]amendment.Decision `json:"decided,omitempty"`
	Error   string                        `json:"error,omitempty"`
}

func runAmendment(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printAmendmentUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listAmendments(args[1:], stdout, stderr)
	case "show":
		return showAmendment(args[1:], stdout, stderr)
	case "approve":
		return decideAmendment("approve", amendment.VerdictApproved, args[1:], stdout, stderr)
	case "decline":
		return decideAmendment("decline", amendment.VerdictDeclined, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown amendment command %q\n\n", args[0])
		printAmendmentUsage(stderr)
		return 2
	}
}

func listAmendments(args []string, stdout, stderr io.Writer) int {
	flags := newAmendmentFlags("amendment list", stderr)
	all := flags.set.Bool("all", false, "include proposals that have already been decided")
	owner := flags.set.String("owner", "", "list only what one role is being asked to decide")
	if code, ok := flags.parse(args, 0); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	records, err := store.List()
	if err != nil {
		return reportAmendmentError(stdout, stderr, *flags.jsonOutput, err)
	}
	listed := amendment.Pending(records)
	if *all {
		listed = amendment.Proposals(records)
	}
	if named := strings.TrimSpace(*owner); named != "" {
		// A role nobody owns anything as is refused rather than filtered on. An
		// operator who mistyped it would otherwise read "nothing is waiting" as an
		// answer about the queue instead of about their typing.
		if !ownsArtifacts(domain.AgentRole(named)) {
			return reportAmendmentError(stdout, stderr, *flags.jsonOutput,
				fmt.Errorf("no role %q owns any artifact; --owner is one of %s", named, strings.Join(artifactOwners(), ", ")))
		}
		listed = ownedBy(listed, domain.AgentRole(named))
	}
	decided := decisionsOn(records, listed)
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, amendmentOutput{Proposals: listed, Decided: decided})
	}
	if len(listed) == 0 {
		fmt.Fprintln(stdout, "no proposed changes are waiting to be decided")
		return 0
	}
	for _, proposal := range listed {
		fmt.Fprint(stdout, proposal.Render())
		if decision, settled := decided[proposal.ID]; settled {
			fmt.Fprint(stdout, decision.Render())
		}
	}
	return 0
}

func showAmendment(args []string, stdout, stderr io.Writer) int {
	flags := newAmendmentFlags("amendment show", stderr)
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	records, err := store.List()
	if err != nil {
		return reportAmendmentError(stdout, stderr, *flags.jsonOutput, err)
	}
	found, ok := amendment.Find(records, flags.id())
	if !ok {
		return reportAmendmentError(stdout, stderr, *flags.jsonOutput,
			fmt.Errorf("no proposed change %q was raised for this product", flags.id()))
	}
	decided := decisionsOn(records, []amendment.Proposal{found})
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, amendmentOutput{Proposals: []amendment.Proposal{found}, Decided: decided})
	}
	fmt.Fprint(stdout, found.Render())
	if decision, settled := decided[found.ID]; settled {
		fmt.Fprint(stdout, decision.Render())
		return 0
	}
	// Waiting on the operator rather than on the owner, because nothing records an
	// owner's decision: the owning role argues about a proposal where it runs, and
	// the decision is made here.
	fmt.Fprintf(stdout, "      waiting on you, under the %s's authority\n", found.Owner)
	return 0
}

// decideAmendment records what became of one proposal. The authority is the
// artifact's owner and is never asked for: it follows from the document, so an
// operator cannot decide a design change under the product manager's authority
// by naming the wrong role.
func decideAmendment(verb string, verdict amendment.Verdict, args []string, stdout, stderr io.Writer) int {
	flags := newAmendmentFlags("amendment "+verb, stderr)
	reason := flags.set.String("reason", "", "why this was decided; required when declining")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	records, err := store.List()
	if err != nil {
		return reportAmendmentError(stdout, stderr, *flags.jsonOutput, err)
	}
	found, ok := amendment.Find(records, flags.id())
	if !ok {
		return reportAmendmentError(stdout, stderr, *flags.jsonOutput,
			fmt.Errorf("no proposed change %q was raised for this product", flags.id()))
	}
	decision, err := found.Decide(verdict, amendment.DeciderOperator, *reason, time.Now())
	if err != nil {
		return reportAmendmentError(stdout, stderr, *flags.jsonOutput, err)
	}
	if err := store.Decide(decision); err != nil {
		return reportAmendmentError(stdout, stderr, *flags.jsonOutput, err)
	}
	step := handStep{
		kind:    intervention.KindApprove,
		subject: found.ID + " on " + found.Artifact,
		said:    "approved the proposed change " + found.ID + " to " + found.Artifact + " with yoyo amendment approve",
	}
	if verdict != amendment.VerdictApproved {
		step.kind = intervention.KindDecline
		step.said = "declined the proposed change " + found.ID + " to " + found.Artifact + " with yoyo amendment decline"
	}
	noteHandStepFor(*flags.configPath, stderr, step)
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, amendmentOutput{
			Proposals: []amendment.Proposal{found},
			Decided:   map[string]amendment.Decision{found.ID: decision},
		})
	}
	fmt.Fprint(stdout, found.Render())
	fmt.Fprint(stdout, decision.Render())
	if verdict == amendment.VerdictApproved {
		// Said in the imperative because it is the next thing somebody has to do.
		// An approval that everybody reads as "done" is how an approved change
		// quietly never happens.
		fmt.Fprintf(stdout, "amend %s as the %s, recording the revision under that role\n", found.Artifact, found.Owner)
	}
	return 0
}

// ownedBy narrows a listing to what one role is being asked to decide, which is
// how an operator deciding in an absent owner's stead reads only that owner's
// queue.
func ownedBy(proposals []amendment.Proposal, owner domain.AgentRole) []amendment.Proposal {
	matched := make([]amendment.Proposal, 0, len(proposals))
	for _, proposal := range proposals {
		if proposal.Owner == owner {
			matched = append(matched, proposal)
		}
	}
	return matched
}

// artifactOwners names the roles that own an artifact at all, derived from the
// ownership table rather than listed again here: a second list would be one a
// later kind could be added without.
func artifactOwners() []string {
	var owners []string
	for _, kind := range artifact.Kinds() {
		owner, known := artifact.Owner(kind)
		if !known || slices.Contains(owners, string(owner)) {
			continue
		}
		owners = append(owners, string(owner))
	}
	sort.Strings(owners)
	return owners
}

func ownsArtifacts(role domain.AgentRole) bool {
	return slices.Contains(artifactOwners(), string(role))
}

func decisionsOn(records []amendment.Record, proposals []amendment.Proposal) map[string]amendment.Decision {
	decided := make(map[string]amendment.Decision)
	for _, proposal := range proposals {
		if decision, settled := amendment.DecisionOn(records, proposal.ID); settled {
			decided[proposal.ID] = decision
		}
	}
	if len(decided) == 0 {
		return nil
	}
	return decided
}

// amendmentFlags is the flag set every amendment command shares: which
// configuration names the product whose log this is, and how to report the
// result.
type amendmentFlags struct {
	set        *flag.FlagSet
	name       string
	configPath *string
	jsonOutput *bool
	// args are the positional arguments, collected by parse rather than read off
	// the flag set, because the flags may come after them.
	args []string
}

func newAmendmentFlags(name string, stderr io.Writer) *amendmentFlags {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return &amendmentFlags{
		set:        set,
		name:       name,
		configPath: set.String("config", "", "configuration file path (default: the nearest project configuration)"),
		jsonOutput: set.Bool("json", false, "emit machine-readable JSON"),
	}
}

// parse reads the flags and the positional arguments, in whatever order they
// were typed, which is what parseArguments is for: `amendment approve <id>
// --reason ...` is what the usage text and the documentation say to type.
func (f *amendmentFlags) parse(args []string, positional int) (int, bool) {
	parsed, err := parseArguments(f.set, args)
	if err != nil {
		return 2, false
	}
	f.args = parsed
	if len(f.args) != positional {
		if positional == 0 {
			fmt.Fprintf(f.set.Output(), "%s does not accept positional arguments\n", f.name)
		} else {
			fmt.Fprintf(f.set.Output(), "%s requires exactly one proposal id\n", f.name)
		}
		printAmendmentUsage(f.set.Output())
		return 2, false
	}
	return 0, true
}

// id is the proposal a command was given, for the commands that take one.
func (f *amendmentFlags) id() string {
	return argumentAt(f.args, 0)
}

// store opens the product's proposal log. It is the same log the runs append
// to, addressed the same way: by the product the configuration names, in the
// state root everything durable shares.
func (f *amendmentFlags) store(stderr io.Writer) (*runstate.AmendmentStore, int) {
	resolved, err := loadConfiguration(*f.configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}
	store, err := runstate.NewAmendmentStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}
	return store, 0
}

func reportAmendmentError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, amendmentOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printAmendmentUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo amendment <list|show|approve|decline> [options]

The canonical documents each belong to one role, and a role that does not own
one proposes a change to it rather than making it. This is where those proposals
arrive: what document, what should change, why, who proposed it, and which role
is being asked to decide.

A decision is the only thing these commands write, and they write it to the
proposal rather than to the document. Approving records that the owner's
authority came down in favour of the change; the change itself is then made in
the document by the role that owns it, in a revision recorded under that role.
Nothing an unapproved proposal contains ever reaches a document, and neither
does anything an approved one contains.

Every decision is recorded here, by you, under the authority of the role that
owns the document. No agent records one: an owning role that runs is shown the
proposals against its documents and argues for or against them, and the
Lead Product Manager is told in so many words that it cannot decide one. So a
proposal waits on you whoever owns the document, and the record says you
exercised that role's authority.

  list [--all] [--owner <role>]   what is waiting to be decided
  show <id>                       one proposal and what became of it
  approve <id> [--reason <why>]   record the change as authorized
  decline <id> --reason <why>     turn it down, keeping why

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON`)
}
