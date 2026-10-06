package cli

// Reading the canonical artifacts back: what identity each document carries,
// what it says it supports, and which files in the artifact homes are not
// artifacts at all.
//
// There is deliberately no create, amend, or retire here, unlike the invariant
// commands beside it, and the reason is not that the store lacks them. It has
// them, gated by the ownership boundary, and they are reached from the place a
// document is actually written: the owning role emits the document as a typed
// action in its conversation, it is confirmed under the approval policy or by
// the operator, and the harness performs the write under that role's authority
// (internal/artifact/write.go). A command
// here would need the document's prose typed at a shell, which is the
// transcription that path exists to end.
//
// What is left for these commands is reading the result back: refusing a
// document whose identity is missing, malformed, or claimed by another file, and
// reporting a revision recorded by a role that does not own the document.
//
// `identify` is the one role mutation exposed, because it needs no such answer:
// what it writes is identifiers rather than prose. It takes the goals document as
// it should now read, and the store refuses it unless the only difference is the
// bracketed identifiers the goal entries open with — so no prose reaches the
// document through it, and what is recorded is an identity revision, which leaves
// the operator's approval standing. It acts with the authority of the role that
// owns the document, the way the invariant commands act with the architect's, and
// records that it did.
//
// `approve` is here for the opposite reason, and is the one thing these commands
// write. An approval is the operator's, the operator is who runs a command, and
// what it records is a fact about them rather than prose about the product — so
// unlike an amendment it needs no answer to how a document gets written, and
// unlike a role's mutation it is nobody else's to make. Nothing here refuses an
// unapproved document and nothing stops loading one, and the one thing that
// turns on the record lives elsewhere: work is admitted to the queue without the
// operator being asked where it traces to a goal an approved goals document
// states, so approving the goals is what a project running that way is doing
// when it runs this.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

type artifactOutput struct {
	Artifacts []artifact.Artifact `json:"artifacts,omitempty"`
	// Approvals pairs each listed artifact with what is recorded about its
	// approval and what the configuration asks for, keyed by artifact id. The
	// state is reported rather than left to be derived, because "approved" and
	// "approved at a revision that has been amended since" is exactly the
	// distinction a reader needs and exactly the one that is easy to get wrong.
	Approvals         map[string]artifactApproval `json:"approvals,omitempty"`
	Problems          []artifact.Problem          `json:"problems,omitempty"`
	ReferenceProblems []artifact.ReferenceProblem `json:"reference_problems,omitempty"`
	// PendingCommit is where a write this command performed landed, in the same
	// words the terminal is told it. It is carried rather than left to the prose
	// because a machine-readable caller is the operator's own script, and it needs
	// the commit exactly as much as they do. Only a command that wrote sets it.
	PendingCommit string `json:"pending_commit,omitempty"`
	Error         string `json:"error,omitempty"`
}

// artifactApproval is what a machine-readable listing says about one document's
// approval.
type artifactApproval struct {
	State artifact.ApprovalState `json:"state"`
	// Required is whether this project asked for the operator's approval of this
	// kind of document, and Setting names the configuration that decided.
	Required bool   `json:"required"`
	Setting  string `json:"setting,omitempty"`
	Mode     string `json:"mode,omitempty"`
	// Approval is the most recent one recorded, absent when there is none.
	Approval *artifact.Approval `json:"approval,omitempty"`
	// RevisionsSinceApproval is how far the document has moved since, and is zero
	// unless the state is amended.
	RevisionsSinceApproval int `json:"revisions_since_approval,omitempty"`
}

func runArtifact(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printArtifactUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listArtifacts(args[1:], stdout, stderr)
	case "show":
		return showArtifact(args[1:], stdout, stderr)
	case "approve":
		return approveArtifact(args[1:], stdout, stderr)
	case "identify":
		return identifyArtifact(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown artifact command %q\n\n", args[0])
		printArtifactUsage(stderr)
		return 2
	}
}

func listArtifacts(args []string, stdout, stderr io.Writer) int {
	flags := newArtifactFlags("artifact list", stderr)
	kind := flags.set.String("kind", "", "list only artifacts of one kind")
	if code, ok := flags.parse(args, 0); !ok {
		return code
	}
	store, policy, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	set, err := store.Load()
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	listed := set.Artifacts
	if strings.TrimSpace(*kind) != "" {
		selected := artifact.Kind(strings.TrimSpace(*kind))
		if !selected.Valid() {
			return reportArtifactError(stdout, stderr, *flags.jsonOutput, fmt.Errorf("unknown artifact kind %q", *kind))
		}
		listed = set.OfKind(selected)
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, artifactOutput{
			Artifacts:         listed,
			Approvals:         artifactApprovals(listed, policy),
			Problems:          set.Problems,
			ReferenceProblems: set.ReferenceProblems,
		})
	}
	if len(listed) == 0 {
		fmt.Fprintf(stdout, "no artifacts are recorded in %s\n", strings.Join(set.Homes, ", "))
	}
	for _, recorded := range listed {
		fmt.Fprintf(stdout, "%s [%s, %s] %s\n", recorded.ID, recorded.Kind, recorded.Status, recorded.Title)
		fmt.Fprintf(stdout, "  file: %s\n", recorded.Path)
		fmt.Fprintf(stdout, "  supports: %s\n", artifactSupports(recorded))
		fmt.Fprintf(stdout, "  approval: %s\n", renderArtifactApproval(recorded, policy))
	}
	// A document in an artifact home that carries no usable identity is not an
	// artifact anything can refer to, so it is named here rather than left to be
	// discovered by whatever tries to link to it.
	for _, problem := range set.Problems {
		fmt.Fprintf(stderr, "not an artifact: %s\n", problem)
	}
	// A relationship that does not hold is reported over the whole set rather
	// than over what --kind selected: the chain runs between kinds, and a listing
	// narrowed to the goals would otherwise hide the design that names one of
	// them and resolves to nothing.
	for _, problem := range set.ReferenceProblems {
		fmt.Fprintf(stderr, "%s: %s\n", problem.Kind, problem)
	}
	return 0
}

func showArtifact(args []string, stdout, stderr io.Writer) int {
	flags := newArtifactFlags("artifact show", stderr)
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, policy, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	set, err := store.Load()
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	found, ok := set.Find(flags.id())
	if !ok {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput,
			fmt.Errorf("no artifact %q is recorded in %s", flags.id(), strings.Join(set.Homes, ", ")))
	}
	problems := set.ReferenceProblemsFor(found.ID)
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, artifactOutput{
			Artifacts:         []artifact.Artifact{found},
			Approvals:         artifactApprovals([]artifact.Artifact{found}, policy),
			ReferenceProblems: problems,
		})
	}
	fmt.Fprintf(stdout, "%s [%s, %s] %s\n", found.ID, found.Kind, found.Status, found.Title)
	fmt.Fprintf(stdout, "file: %s\n", found.Path)
	fmt.Fprintf(stdout, "supports: %s\n", artifactSupports(found))
	fmt.Fprintf(stdout, "approval: %s\n\n", renderArtifactApproval(found, policy))
	for index, revision := range found.Revisions {
		fmt.Fprintf(stdout, "%s %s by the %s: %s\n",
			revision.At.UTC().Format(time.RFC3339), revision.Action, revision.By, revision.Reason)
		// Each approval is printed under the revision it was given for, because
		// which version of the document the operator saw is the whole of what
		// distinguishes an approval that still stands from one that has been
		// amended out from under.
		for _, approval := range found.Approvals {
			if approval.Revision == index {
				fmt.Fprintf(stdout, "  approved by the %s %s: %s\n",
					approval.By, approval.At.UTC().Format(time.RFC3339), approval.Reason)
			}
		}
	}
	// What this one document's place in the chain is wrong about, so somebody
	// asking after a single artifact is told without reading the whole listing.
	for _, problem := range problems {
		fmt.Fprintf(stderr, "%s: %s\n", problem.Kind, problem.Reason)
	}
	return 0
}

// approveArtifact records that the operator approved a document as it now
// stands. The revision it applies to is never asked for: it is the last one the
// document records, so an approval cannot be recorded against a version of the
// document nobody was looking at.
func approveArtifact(args []string, stdout, stderr io.Writer) int {
	flags := newArtifactFlags("artifact approve", stderr)
	reason := flags.set.String("reason", "", "how this approval was given and what it covered; required")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	// An approval is the operator's, and it is what admits work against the
	// goals without asking them -- so a run that could record one could approve
	// a goal for itself. A shell an agent opened is refused before the store is
	// even opened; the protected-path gate refuses the write it would have made.
	if err := refusedToAgentProcess("yoyo artifact approve", "a person approves an artifact"); err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	store, policy, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	approved, err := store.Approve(flags.id(), *reason, time.Now())
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	listed := []artifact.Artifact{approved}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, artifactOutput{
			Artifacts:     listed,
			Approvals:     artifactApprovals(listed, policy),
			PendingCommit: artifact.PendingCommit(store.RepositoryRoot, approved.Path),
		})
	}
	fmt.Fprintf(stdout, "%s [%s, %s] %s\n", approved.ID, approved.Kind, approved.Status, approved.Title)
	fmt.Fprintf(stdout, "file: %s\n", approved.Path)
	fmt.Fprintf(stdout, "approval: %s\n", renderArtifactApproval(approved, policy))
	// Said every time, because the one way this could quietly become something
	// else is somebody reading an approval as a change to the document or as a
	// gate that has now opened. It is neither.
	fmt.Fprintln(stdout, "recorded in the document's frontmatter; nothing the document says changed, and no gate moved")
	// And where that record now sits, which is the other thing said every time:
	// this command wrote into the checkout the configuration pointed it at, and
	// the write stops there. It is printed after the approval rather than before
	// it because what they asked for is the approval and this is what it cost.
	fmt.Fprintln(stdout, artifact.PendingCommit(store.RepositoryRoot, approved.Path))
	return 0
}

// identifyArtifact records a goals document's goals being given identifiers,
// under the authority of the role that owns the document. The operator's approval
// is neither asked for nor moved, and the command says so, because that is the
// whole of why the verb exists: an identifier changes no goal's words.
func identifyArtifact(args []string, stdout, stderr io.Writer) int {
	flags := newArtifactFlags("artifact identify", stderr)
	bodyFile := flags.set.String("body-file", "", "the document below its frontmatter as it should now read, identifiers added; required")
	reason := flags.set.String("reason", "", "why the identifiers are being recorded; required")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	if strings.TrimSpace(*bodyFile) == "" {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, fmt.Errorf("artifact identify requires --body-file, the document as it should now read"))
	}
	body, err := os.ReadFile(*bodyFile)
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, fmt.Errorf("read --body-file: %w", err))
	}
	store, policy, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	set, err := store.Load()
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	found, ok := set.Find(flags.id())
	if !ok {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput,
			fmt.Errorf("no artifact %q is recorded in %s", flags.id(), strings.Join(set.Homes, ", ")))
	}
	owner, owned := artifact.Owner(found.Kind)
	if !owned {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, fmt.Errorf("no role owns a %s artifact", found.Kind))
	}
	identified, err := store.Identify(owner, found.ID, string(body), *reason, time.Now())
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	listed := []artifact.Artifact{identified}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, artifactOutput{
			Artifacts:     listed,
			Approvals:     artifactApprovals(listed, policy),
			PendingCommit: artifact.PendingCommit(store.RepositoryRoot, identified.Path),
		})
	}
	fmt.Fprintf(stdout, "%s [%s, %s] %s\n", identified.ID, identified.Kind, identified.Status, identified.Title)
	fmt.Fprintf(stdout, "file: %s\n", identified.Path)
	fmt.Fprintf(stdout, "recorded as an identity revision by the %s; no goal's words changed\n", owner)
	fmt.Fprintf(stdout, "approval: %s\n", renderArtifactApproval(identified, policy))
	fmt.Fprintln(stdout, artifact.PendingCommit(store.RepositoryRoot, identified.Path))
	return 0
}

// artifactFlags is the flag set every artifact command shares: which
// configuration to read the homes from, and how to report the result.
type artifactFlags struct {
	set        *flag.FlagSet
	name       string
	configPath *string
	jsonOutput *bool
	// args are the positional arguments, collected by parse rather than read off
	// the flag set, because the flags may come after them.
	args []string
}

func newArtifactFlags(name string, stderr io.Writer) *artifactFlags {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return &artifactFlags{
		set:        set,
		name:       name,
		configPath: set.String("config", "", "configuration file path (default: the nearest project configuration)"),
		jsonOutput: set.Bool("json", false, "emit machine-readable JSON"),
	}
}

// parse reads the flags and the positional arguments, in whatever order they
// were typed, which is what parseArguments is for.
func (f *artifactFlags) parse(args []string, positional int) (int, bool) {
	parsed, err := parseArguments(f.set, args)
	if err != nil {
		return 2, false
	}
	f.args = parsed
	if len(f.args) != positional {
		if positional == 0 {
			fmt.Fprintf(f.set.Output(), "%s does not accept positional arguments\n", f.name)
		} else {
			fmt.Fprintf(f.set.Output(), "%s requires exactly one artifact id\n", f.name)
		}
		printArtifactUsage(f.set.Output())
		return 2, false
	}
	return 0, true
}

// id is the artifact a command was given, for the commands that take one.
func (f *artifactFlags) id() string {
	if len(f.args) == 0 {
		return ""
	}
	return f.args[0]
}

// store resolves the artifact homes the same way every other command resolves
// the repository: relative to the project rather than to the .yoyodyne
// directory the configuration happens to live in.
func (f *artifactFlags) store(stderr io.Writer) (artifact.Store, artifact.Policy, int) {
	resolved, err := loadConfiguration(*f.configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return artifact.Store{}, artifact.Policy{}, 1
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		fmt.Fprintf(stderr, "resolve product repository: %v\n", err)
		return artifact.Store{}, artifact.Policy{}, 1
	}
	return artifactStore(repository, resolved.Config.Product), artifactPolicy(resolved.Config.Approvals, resolved.Config.Product), 0
}

// artifactPolicy is the approvals configuration in the terms the artifact
// package thinks in. What requires the operator's approval is decided by the
// project's configuration rather than by the kind of document: a project that
// says its designs need approving gets that, and one that says its goals do not
// is told so rather than nagged.
func artifactPolicy(approvals config.Approvals, product config.Product) artifact.Policy {
	return artifact.Policy{Brief: approvals.Brief, Goals: approvals.Goals, Designs: approvals.Designs, SpecificationsHome: product.Specifications}
}

// artifactStore is how the configured directories become a store, in one place
// rather than at each call site.
func artifactStore(repositoryRoot string, product config.Product) artifact.Store {
	return artifact.StoreFor(repositoryRoot, product)
}

func reportArtifactError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, artifactOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func artifactSupports(recorded artifact.Artifact) string {
	if len(recorded.Supports) == 0 {
		return "nothing upstream"
	}
	return strings.Join(recorded.Supports, ", ")
}

// renderArtifactApproval says what is recorded about one document's approval,
// and what this project asked for. A document nobody approved is reported
// differently depending on whether anything wanted it approved: an unapproved
// brief is something for the operator to do, and an unapproved design in a
// project whose designs are automatic is nothing at all.
func renderArtifactApproval(recorded artifact.Artifact, policy artifact.Policy) string {
	setting, mode, governed := policy.SettingFor(recorded)
	latest, approved := recorded.LatestApproval()
	if approved {
		given := fmt.Sprintf("given by the %s %s, for revision %d",
			latest.By, latest.At.UTC().Format(time.RFC3339), latest.Revision)
		if recorded.ApprovalState() == artifact.ApprovalApproved {
			return "approved as it stands, " + given + rewordedSince(recorded)
		}
		return fmt.Sprintf("approved and amended since — %s, and %s recorded after it, so the document as it now reads is not what was approved%s%s",
			given, laterRevisions(recorded.RevisionsSinceApproval()), rewordedSince(recorded), undelegated(recorded, latest.Revision))
	}
	switch {
	case policy.RequiresFor(recorded):
		return fmt.Sprintf("none recorded, and %s is %s, so this document is yours to approve", setting, mode)
	case governed:
		return fmt.Sprintf("none recorded; %s is %s, so none is asked for", setting, mode)
	default:
		return fmt.Sprintf("none recorded; no approval setting governs a %s artifact", recorded.Kind)
	}
}

// rewordedSince names the rewordings the approval stands through, so a document
// read as approved that no longer reads word for word as it was approved says
// so, and says whose decision that was.
func rewordedSince(recorded artifact.Artifact) string {
	rewordings := recorded.RewordingsSinceApproval()
	if len(rewordings) == 0 {
		return ""
	}
	return fmt.Sprintf("; the %s has since recorded %s as consistent with intent, which the approval stands through",
		domain.RoleProductManager.Title(), plural(len(rewordings), "rewording", "rewordings"))
}

// undelegated says why an amendment labelled consistent with intent still
// counts against the approval, so the label does not read as ignored: what the
// record is missing is what whoever recorded it has to supply.
func undelegated(recorded artifact.Artifact, approvedRevision int) string {
	var missing []string
	for index := approvedRevision + 1; index < len(recorded.Revisions); index++ {
		revision := recorded.Revisions[index]
		if revision.Intent != artifact.IntentConsistent {
			continue
		}
		if delegated, why := recorded.Rewording(revision); !delegated {
			missing = append(missing, fmt.Sprintf("revision %d is labelled consistent and still counts, because %s", index, why))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "; " + strings.Join(missing, "; ")
}

func laterRevisions(count int) string {
	if count == 1 {
		return "one revision was"
	}
	return fmt.Sprintf("%d revisions were", count)
}

// artifactApprovals is the same reading as renderArtifactApproval, for a
// machine: the state, what the configuration asked for, and the approval itself.
func artifactApprovals(artifacts []artifact.Artifact, policy artifact.Policy) map[string]artifactApproval {
	approvals := make(map[string]artifactApproval, len(artifacts))
	for _, recorded := range artifacts {
		setting, mode, governed := policy.SettingFor(recorded)
		reported := artifactApproval{
			State:                  recorded.ApprovalState(),
			Required:               policy.RequiresFor(recorded),
			RevisionsSinceApproval: recorded.RevisionsSinceApproval(),
		}
		if governed {
			reported.Setting, reported.Mode = setting, string(mode)
		}
		if latest, approved := recorded.LatestApproval(); approved {
			reported.Approval = &latest
		}
		approvals[recorded.ID] = reported
	}
	return approvals
}

func printArtifactUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo artifact <list|show|approve|identify> [options]

The canonical documents upstream of a work item -- the product brief, the goals,
the designs and specifications, and the decision records -- each carry a stable
id, a kind, a lifecycle status, what they support upstream, and a revision log,
in frontmatter at the top of the file. The id is the file name, so a document
whose frontmatter claims another id, and two documents claiming one id, are
refused rather than reconciled.

The relationships are checked too, and reported rather than refused: a supports
entry naming an id no artifact answers to, and an artifact nothing connects back
to the brief, are named on stderr beside a set that still holds every document
it read. The brief is the root and the decision records are not downstream of
it, so neither is reported for supporting nothing.

Who changed each document is reported the same way. The Lead Product Manager
owns the brief and the goals and the architect owns the designs, specifications,
and decision records, so a revision log recording a change by any other role is
named on stderr as an unauthorized revision. The document still loads: the log
is append-only, and losing it would leave a document nobody could correct.

Your approval of one of these documents is recorded in the same frontmatter,
against the revision it was given for, so a document amended after you approved
it reads as approved-and-amended-since rather than as approved. The exception is
a rewording of the goals the Lead Product Manager records as consistent with
intent -- intent: consistent on the amendment, with a reason opening with the
work item that directed it -- because the goals then admit and refuse the same
work they did, and that change is delegated rather than yours; an amendment
recorded as fundamental, or one that does not say, is still put to you.

What needs your approval is your configuration's to say: approvals.brief and
approvals.goals default to human, approvals.designs to automatic, and a decision record is the
architect's account of a decision rather than a statement of intent, so nothing
asks you to approve one. A revision that only gives a goals document's goals
identifiers -- the bracketed name an entry opens with -- changes no goal's words,
so it is recorded as an identity revision rather than an amendment and leaves
your approval standing.

An unapproved document still loads, still governs what is downstream of it, and
stops nothing that reads it; approving writes nothing but the approval, and the
document itself stays the owning role's to change. That write lands in your
checkout and stops there -- nothing commits it and nothing opens a pull request
for it -- so the document is an uncommitted change until you commit it, and a run
against that checkout refuses to start while it is. Approving says so as it
writes, naming the checkout, which is the one this configuration points at rather
than whichever one you are standing in. What your approval
of the goals decides is what reaches the work queue: under approvals.work_items:
automatic, work that traces to a goal an approved goals document states is
admitted without asking you, and anything else is still put to you. That is why
approve is a person's verb: a process the harness launched for a role -- an
agent's shell, marked by YOYODYNE_AGENT_ROLE -- is refused it and told so.

  list [--kind <kind>]   list the recorded artifacts, and name what is not one
  show <id>              print one artifact and its recorded revisions
  approve <id>           record your approval of a document as it now stands
  identify <id>          record a goals document's goals being given identifiers,
                         as the role that owns it; refused if any words change

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON

list options:
  --kind <kind>         brief, goals, non-goals, design, specification, or decision

approve options:
  --reason <text>       how the approval was given and what it covered; required

identify options:
  --body-file <path>    the document below its frontmatter as it should now read
  --reason <text>       why the identifiers are being recorded; required`)
}
