// Package inventory is the harness's own vocabulary: every term of art a person
// or a role reads, what it means, and the decision proposed for it. Two things
// read it. `go run ./scripts/vocabulary` measures each term and writes
// docs/vocabulary-inventory.md from it, and the terms check in internal/terms
// allows a term listed here by name while its decision is pending, so a term
// already found is not refused as a new coinage. Keeping the list in one
// package both read is what keeps the check from going stale with the
// document: the check reads the list, never the document generated from it.
package inventory

import (
	"regexp"
	"strings"
)

// Decision is what the inventory proposes for one term. The Lead Product
// Manager records the decision that stands on yoyodyne-ifd.437.18; this is the
// proposal it starts from.
type Decision string

const (
	// Replace means the term is decoration or jargon with an ordinary wording
	// that says the same thing: write Words instead, and list the term in the
	// register as replaced so the terms check refuses its return.
	Replace Decision = "replace"
	// Register means the term names something real that a person has to be able
	// to point at, usually a command, a flag, or a decision a role types: keep
	// it, with Meaning as its row in docs/terms.md.
	Register Decision = "register"
	// Keep means the term already has a row in the register and the inventory
	// proposes no change to it.
	Keep Decision = "keep"
)

// Term is one word or phrase of the harness's own vocabulary.
type Term struct {
	// Term is the word as the inventory names it.
	Term string
	// Match is what is looked for, each at the start of a word and ignoring
	// case. A stem catches its inflections: `stoppage` catches `stoppages`, and
	// `docket` catches `docketed`. A match written in parts is looked for however
	// its parts are spaced, as internal/terms looks for a registered term.
	Match []string
	// Exact holds every match to the spelling written, for a term whose other
	// spellings are ordinary English: `carry-out` the noun is the coinage, and
	// `carry out` the verb is not.
	Exact bool
	// Whole holds every match to a whole word, for a stem that also begins
	// ordinary words.
	Whole bool
	// Except is dropped where it matches at the same place as Match, for a term
	// whose ordinary use would swamp the count: `pull` is counted, and `pull
	// request` is not.
	Except []string
	// Meaning is what the term means, in one plain sentence.
	Meaning string
	// Decision is what the inventory proposes.
	Decision Decision
	// Words is what to write instead, for a term proposed for replacement, or
	// the reason it is kept, for one proposed for registration or kept.
	Words string
	// Note is anything a reader of the counts needs, such as a second meaning
	// or ordinary uses the match cannot tell apart.
	Note string
}

// Inventory is the harness's own vocabulary as of 2026-09-28: every term of art
// found where a person or a role reads it, with a proposed decision for each.
// It was assembled by reading the printed strings, the shipped personas, the
// guides, and the governed documents for words used in a sense ordinary English
// does not give them, starting from the words the operator met on 2026-09-27
// and the terms already in docs/terms.md. The -candidates flag lists compounds
// no term here covers, which is where a later reading starts.
var Inventory = []Term{
	{
		Term:     "stoppage",
		Match:    []string{"stoppage"},
		Meaning:  "A run that stopped and is waiting for the development manager to decide what happens to it.",
		Decision: Replace,
		Words:    "a stopped run, or a run that stopped",
	},
	{
		Term:     "docket",
		Match:    []string{"docket"},
		Meaning:  "The development manager's list of stopped runs, runs that died before they started, and items dispatch would not start; `docketed` is being put on it.",
		Decision: Replace,
		Words:    "the development manager's list of stopped runs; for `docketed`, put in front of the development manager. The register's row is retired once the replacements land; `DocketStore` and other identifiers keep their names",
		Note:     "Registered today. It is proposed for replacement because it is the most frequent term in this inventory and the operator asked what `docketed` meant.",
	},
	{
		Term:     "crossing",
		Match:    []string{"crossing"},
		Meaning:  "Two things: the development manager raising one item's budget cap by one step past a refusal, with a reason; and a conversation turn answered by a permitted alternate model when the configured one refused.",
		Decision: Replace,
		Words:    "for a budget, raising the cap (\"raised the repair cap to 3\"); for a model, a turn answered by the alternate model. The triage decision value `cross` keeps its name",
	},
	{
		Term:     "carry-out",
		Match:    []string{"carry-out"},
		Exact:    true,
		Meaning:  "The harness acting on a decision the development manager recorded about a stopped run.",
		Decision: Replace,
		Words:    "carrying out her decision; for the queue of them, decisions waiting to be carried out",
		Note:     "Only the hyphenated noun is counted; the verb `carry out` is ordinary.",
	},
	{
		Term:     "environmental stop",
		Match:    []string{"environmental"},
		Meaning:  "A run ended by something outside the work — the sandbox, the network, the tracker, a provider limit — which spends none of the item's budgets.",
		Decision: Replace,
		Words:    "stopped by something outside the work, naming what it was",
		Note:     "The item replacing the three words the operator met first (yoyodyne-ifd.437.17) replaces this one. `environmental refusal` and `environmental cause` are counted with it.",
	},
	{
		Term:     "idle bound",
		Match:    []string{"idle bound"},
		Meaning:  "The harness ending a run whose AI session produced no output for a set time.",
		Decision: Replace,
		Words:    "the time limit on a session that produces no output: \"produced no output for five minutes, so the harness ended the run\"",
		Note:     "The item replacing the three words the operator met first (yoyodyne-ifd.437.17) replaces this one.",
	},
	{
		Term:     "provider's stream",
		Match:    []string{"provider's stream", "went silent"},
		Meaning:  "The output of the AI session running a role; `went silent` is that output stopping.",
		Decision: Replace,
		Words:    "the AI session's output; for `went silent`, produced no output",
	},
	{
		Term:     "stall",
		Match:    []string{"stall"},
		Meaning:  "Nothing starting although work is ready, or a session producing no output.",
		Decision: Replace,
		Words:    "nothing is starting although work is ready; or, of a session, produced no output",
		Note:     "Counts include the `--stall-after` flag of `yoyo work`, whose name changes only with the command; a rename is its own decision.",
	},
	{
		Term:     "continuation",
		Match:    []string{"continuation"},
		Meaning:  "Resuming a stopped run in its kept worktree and session rather than starting it fresh; a `stall continuation` resumes one ended for producing no output, a `repair continuation` resumes one mid-repair.",
		Decision: Replace,
		Words:    "resuming the stopped run in the same session",
		Note:     "The item replacing the three words the operator met first (yoyodyne-ifd.437.17) replaces `stall continuation`.",
	},
	{
		Term:     "lane",
		Match:    []string{"lane"},
		Meaning:  "The work stream one program manager owns, identified by its tracker label.",
		Decision: Replace,
		Words:    "work stream, the word the register's `program manager` row already uses; for `lane report`, the program manager's report; for `lane label`, the work stream's label",
	},
	{
		Term:     "summons",
		Match:    []string{"summon"},
		Meaning:  "The harness starting a development manager turn at once rather than at its next scheduled time.",
		Decision: Replace,
		Words:    "starts the development manager's turn at once",
	},
	{
		Term:     "park",
		Match:    []string{"park"},
		Meaning:  "Keeping an item in the Lead Product Manager's order but stopping the harness from selecting it until somebody releases it with `unpark`, with the reason shown wherever it is listed.",
		Decision: Register,
		Words:    "`park` and `unpark` are actions a role records and the operator reads in backlog listings, so the word is a name rather than decoration",
	},
	{
		Term:     "pull",
		Match:    []string{"pull"},
		Except:   []string{"pull request"},
		Meaning:  "One time the harness picks the next ready item and starts a run on it.",
		Decision: Replace,
		Words:    "the next time the harness picks work",
		Note:     "`pull request` is not counted. `git pull` and other ordinary uses are, so the counts are an upper bound.",
	},
	{
		Term:     "the line",
		Match:    []string{"the line"},
		Whole:    true,
		Meaning:  "The harness pictured as a production line choosing and running work, as in \"the line is choosing nothing\".",
		Decision: Replace,
		Words:    "the harness, or say what is happening: \"no work is starting\"",
		Note:     "Counts include ordinary uses such as \"the top line\" of a message, so they are an upper bound.",
	},
	{
		Term:     "brake",
		Match:    []string{"brake"},
		Meaning:  "The automatic hold on starting new work after a set number of blocked runs in a row, also written `storm brake`.",
		Decision: Replace,
		Words:    "the automatic stop on starting work; for `braked`, stopped. The register's row is retired once the replacements land",
		Note:     "Registered today. Proposed for replacement because the operator has asked for 'stopped' in place of 'braked'.",
	},
	{
		Term:     "probe",
		Match:    []string{"probe"},
		Meaning:  "Two things: one trial run started under the automatic stop to see whether work can land again; and the first command a developer runs in its worktree to show commands can run there.",
		Decision: Replace,
		Words:    "a trial run; for the developer's, a first command run to show commands work. The decision value `probe` and the verification block's `probe` field keep their names",
	},
	{
		Term:     "watch session",
		Match:    []string{"watch session", "watching session", "the watch", "watch lease"},
		Whole:    true,
		Meaning:  "The long-running `yoyo work --watch` process that picks ready work and starts runs.",
		Decision: Replace,
		Words:    "the process that starts work, or `yoyo work --watch` where the command is the point",
	},
	{
		Term:     "the standing",
		Match:    []string{"the standing", "on the standing"},
		Whole:    true,
		Meaning:  "The list of role instances and what each is doing, where a restart request is shown.",
		Decision: Replace,
		Words:    "the list of role instances",
		Note:     "Counts include ordinary uses such as \"the standing decision\", so they are an upper bound.",
	},
	{
		Term:     "settle",
		Match:    []string{"settle"},
		Meaning:  "Bringing something interrupted or half-finished to a recorded end: a run, a directive, a promotion.",
		Decision: Replace,
		Words:    "finish, resolve, or record how it ended — whichever the sentence means",
	},
	{
		Term:     "witness",
		Match:    []string{"witness"},
		Meaning:  "The tracker's recorded copy of an item's `Goal served:` line, kept so a destroyed attribution can be found and restored; `yoyo goals witness` records it.",
		Decision: Register,
		Words:    "it is a subcommand of `yoyo goals`, so the word is typed and cannot be swept out of its help without renaming the command",
	},
	{
		Term:     "drain",
		Match:    []string{"drain"},
		Meaning:  "A queue emptying, or a restarting process waiting for the runs it hosts to finish first.",
		Decision: Replace,
		Words:    "empties; for a restart, waiting for its runs to finish. `execution.redeploy_drain_limit` keeps its name",
	},
	{
		Term:     "mover",
		Match:    []string{"mover"},
		Meaning:  "Whoever an item or a stopped run is waiting on to act next.",
		Decision: Replace,
		Words:    "who it is waiting on. The `waiting_on` field keeps its name",
	},
	{
		Term:     "shadow review",
		Match:    []string{"shadow"},
		Meaning:  "A second review of the same change by another model, recorded for comparison and deciding nothing.",
		Decision: Register,
		Words:    "it is the `--shadow` flag of `yoyo review`",
	},
	{
		Term:     "landing",
		Match:    []string{"landing"},
		Meaning:  "A change merging onto the target branch; in a developer's reply, its claim of what that merge does to the item — closes it, lands evidence, or escalates.",
		Decision: Replace,
		Words:    "merge, or merged change; for a developer's claim, what the change does to the item. The `yoyodyne-landing` block keeps its name",
	},
	{
		Term:     "exchange",
		Match:    []string{"exchange"},
		Meaning:  "A question one role puts to another, which the harness delivers by invoking the other role and records with what it cost.",
		Decision: Register,
		Words:    "it is the command `yoyo exchange`",
		Note:     "Counts include ordinary uses, so they are an upper bound.",
	},
	{
		Term:     "intake hold",
		Match:    []string{"intake"},
		Meaning:  "A stop on the harness choosing new work by itself, placed by the operator or by the automatic stop; `yoyo release` lifts it, and work already running carries on.",
		Decision: Register,
		Words:    "it names the operator's control over autonomous work, is written in an architectural invariant, and has its own command to lift it",
	},
	{
		Term:     "operator hold",
		Match:    []string{"operator hold", "operator's hold"},
		Meaning:  "The operator pausing everything the harness would spend on a provider, with `yoyo pause`.",
		Decision: Replace,
		Words:    "the operator's pause, after the command that sets it",
	},
	{
		Term:     "sweep",
		Match:    []string{"sweep"},
		Meaning:  "One run of a recurring task and the record it leaves; `yoyo sweeps` reads them.",
		Decision: Register,
		Words:    "it is the command `yoyo sweeps` and the heading every recurring task's findings are listed under",
	},
	{
		Term:     "firing",
		Match:    []string{"firing"},
		Meaning:  "One scheduled run of a recurring task, as in `FAILED FIRING`.",
		Decision: Replace,
		Words:    "run of the task; `FAILED FIRING` becomes `FAILED RUN`",
	},
	{
		Term:     "pass",
		Match:    []string{"a pass", "each pass", "the pass", "next pass", "missed pass", "periodic pass", "scheduling pass", "every pass"},
		Whole:    true,
		Meaning:  "One scheduled turn of a program manager, the supervisor, or the scheduler.",
		Decision: Replace,
		Words:    "turn, or run — whichever the role does",
		Note:     "Only noun phrases are counted; the verb, as in \"checks pass\", is ordinary.",
	},
	{
		Term:     "context bundle",
		Match:    []string{"context bundle"},
		Meaning:  "The documents and state a role is given to read at the start of each turn.",
		Decision: Replace,
		Words:    "what the role is given to read at the start of its turn",
	},
	{
		Term:     "repair",
		Match:    []string{"repair"},
		Except:   []string{"repair grant"},
		Meaning:  "A developer's further attempt at its own change, in the same worktree, after a failed check or review findings; `yoyo triage repair` asks for one.",
		Decision: Register,
		Words:    "it is a `yoyo triage` verb and a budget the status line counts",
		Note:     "Counts include ordinary uses such as \"backlog repair\", so they are an upper bound.",
	},
	{
		Term:     "repair grant",
		Match:    []string{"repair grant"},
		Meaning:  "The development manager allowing a stopped item further review rounds.",
		Decision: Replace,
		Words:    "further review rounds granted",
	},
	{
		Term:     "needs-a-human",
		Match:    []string{"needs a human", "needs a person"},
		Meaning:  "The line in `yoyo status` and the channel listing what is waiting on a person.",
		Decision: Replace,
		Words:    "waiting on you, the operator's own words",
	},
	{
		Term:     "integration target",
		Match:    []string{"integration target"},
		Meaning:  "The branch an approved change is merged onto.",
		Decision: Replace,
		Words:    "target branch, which the same strings already use",
	},
	{
		Term:     "promotion",
		Match:    []string{"promot"},
		Meaning:  "Moving the target branch forward to include an approved change.",
		Decision: Replace,
		Words:    "merging the change onto the target branch",
		Note:     "An architectural invariant says `promotion`; its wording is the architect's.",
	},
	{
		Term:     "lease",
		Match:    []string{"lease"},
		Meaning:  "A lock one process holds so no other process does the same thing at the same time.",
		Decision: Replace,
		Words:    "lock",
		Note:     "`release` is a different word and is not counted.",
	},
	{
		Term:     "floor",
		Match:    []string{"floor"},
		Meaning:  "A total that is at least the figure shown, because some runs left no record to price.",
		Decision: Replace,
		Words:    "at least: \"at least $4.20; 2 runs could not be priced\"",
	},
	{
		Term:     "triage",
		Match:    []string{"triage"},
		Meaning:  "The development manager deciding what happens to each stopped run, and `yoyo triage` carrying the decision out.",
		Decision: Register,
		Words:    "it is the command `yoyo triage`",
	},
	{
		Term:     "protected path",
		Match:    []string{"protected path", "protected-path"},
		Meaning:  "A path a developer's change may not touch unless its work item carries a `protected-path grant:` line naming it.",
		Decision: Register,
		Words:    "it is the grant line an item carries and the refusal a developer receives",
	},
	{
		Term:     "direct-work",
		Match:    []string{"direct-work", "own-intent"},
		Exact:    true,
		Meaning:  "The two authorities a person can hold in a project's configuration: `own-intent`, over what the product is for, and `direct-work`, over work already running.",
		Decision: Register,
		Words:    "they are configuration values a person writes",
	},
	{
		Term:     "side thread",
		Match:    []string{"side thread", "side stream", "sidestream"},
		Meaning:  "A secondary conversation a role opens to work something out, which judges and drafts but acts on nothing, and merges its conclusion back into the main conversation.",
		Decision: Register,
		Words:    "register `side thread` and write it for `side stream`, which is the same thing; the `sidestream` package keeps its name",
	},
	{
		Term:     "raise",
		Match:    []string{"a raise", "the raise", "raise's"},
		Whole:    true,
		Meaning:  "A role putting an item in front of the development manager as unmeetable as written.",
		Decision: Replace,
		Words:    "escalating the item as unmeetable",
	},
	{
		Term:     "remit",
		Match:    []string{"remit"},
		Meaning:  "What a program manager is responsible for.",
		Decision: Replace,
		Words:    "what it is responsible for",
	},
	{
		Term:     "wake",
		Match:    []string{"woken", "wakeup", "wake-up", "wake"},
		Whole:    true,
		Meaning:  "The harness starting another turn of a role or resuming a waiting process.",
		Decision: Replace,
		Words:    "started again, or given another turn",
	},
	{
		Term:     "gate",
		Match:    []string{"gate"},
		Whole:    true,
		Meaning:  "A check a change has to pass before it merges: the configured checks, independent review, the protected paths.",
		Decision: Replace,
		Words:    "name the check",
		Note:     "Counts are of the whole word only, so `gates` and `gated` are not included.",
	},
	{
		Term:     "usage window",
		Match:    []string{"usage window"},
		Meaning:  "The period a provider's usage limit applies to, after which it resets.",
		Decision: Replace,
		Words:    "the provider's usage limit, until it resets at a named time",
	},
	{
		Term:     "sink",
		Match:    []string{"sink"},
		Meaning:  "The process that posts to Slack.",
		Decision: Replace,
		Words:    "the Slack reporter. The register's row is retired once the replacements land; `internal/slack` identifiers keep their names",
		Note:     "Registered today. The coined-terms sweep (yoyodyne-ifd.206) called it the strongest rename candidate: a dataflow term with no ordinary meaning that helps.",
	},
	{
		Term:     "heartbeat",
		Match:    []string{"heartbeat"},
		Meaning:  "How often `yoyo slack` repeats that no work is starting.",
		Decision: Keep,
		Words:    "registered; it is a flag name whose help says it in plain words",
	},
	{
		Term:     "steer",
		Match:    []string{"steer"},
		Meaning:  "Directing the work, or changing what is being worked on.",
		Decision: Keep,
		Words:    "registered; an ordinary verb that reads correctly where it is used",
	},
	{
		Term:     "handback",
		Match:    []string{"handback"},
		Meaning:  "Handing the work back to the developer that made it.",
		Decision: Keep,
		Words:    "registered; it reaches nobody outside code",
	},
	{
		Term:     "discharge",
		Match:    []string{"discharge"},
		Meaning:  "A change being the work its item asked for, so the item closes on it.",
		Decision: Keep,
		Words:    "registered; the developer and reviewer contracts decide on it",
	},
	{
		Term:     "re-arm",
		Match:    []string{"re-arm"},
		Meaning:  "Repeating a merge request a forge dropped, once per publication.",
		Decision: Keep,
		Words:    "registered; it is the verb `yoyo triage rearm`",
	},
	{
		Term:     "seat",
		Match:    []string{"seat"},
		Whole:    true,
		Meaning:  "One running instance of a role, as against the developer slot it fills.",
		Decision: Keep,
		Words:    "registered; the operator's own word",
	},
	{
		Term:     "minute zero",
		Match:    []string{"minute zero"},
		Meaning:  "Before development begins.",
		Decision: Keep,
		Words:    "registered while an invariant's wording carries it; it retires when the architect rewords that invariant",
	},
}

// StopWord is one word of the stop-cause vocabulary: the word a stopped run's
// reason is printed after on every surface.
type StopWord struct {
	Word     string
	Meaning  string
	Decision Decision
	Words    string
}

// StopWords are the ten stop-cause words yoyodyne-ifd.409 adds, which the
// operator asked to have confirmed before anything depends on them. They are
// listed rather than measured: they are on that item's branch and not yet on
// the main branch, and most of them are ordinary words that a count could not
// tell from their ordinary use.
var StopWords = []StopWord{
	{"checks", "A configured check kept failing or could not run, a protected path kept being touched, or nothing was recorded running.", Replace, "stopped at the checks"},
	{"review", "Findings the repair budget could not resolve, a reviewer that could not answer, or an approval whose independence could not be shown.", Replace, "stopped at review"},
	{"integration", "Merging onto the target branch failed: the target kept moving, the change conflicts, or local and remote went different ways.", Replace, "could not be merged"},
	{"publish", "Publishing the merged change to the forge failed, although the local merge landed.", Replace, "could not be published to the forge"},
	{"cleanup", "Removing the run's worktree and branch after its work merged failed.", Replace, "merged, but its worktree could not be removed"},
	{"recording", "The run's work was done but its completion record could not be written.", Replace, "done, but the harness could not record that it finished"},
	{"provider", "The AI provider ended the run without the work being judged.", Replace, "the AI provider ended the run"},
	{"environmental", "Something outside the work refused the run, which spends none of its budgets.", Replace, "stopped by something outside the work"},
	{"cancelled", "The operator asked the run to stop, or its context was cancelled.", Keep, "an ordinary word; keep it"},
	{"harness", "One of the harness's own steps around the work failed: saving state, writing the tracker, making a scratch directory.", Replace, "the harness's own step failed"},
}

// Unread are the compound words the terms check found, the day it began
// reading for new ones, that nothing yet accounted for: no row in the
// register, no term above, not ordinary English by the check's rules, and not
// on the register's list of ordinary compounds. Some are terms of art and some
// are ordinary; nobody has decided yet which. The check allows each
// by name until somebody does, so it refuses only a compound written after it
// began. Deciding one takes it off this list: an ordinary compound goes on the
// register's list of ordinary compounds, a term worth keeping gets a row in
// the register, a term worth replacing is reworded wherever it is written —
// and a word nothing writes any more is refused here until it is taken off.
var Unread = []string{
	"account-pooling", "account-selection", "action-count", "activation-digest",
	"admission-trigger", "advisory-once", "agent-context", "agent-continuity",
	"agent-memory", "allow-list", "already-made", "already-runnable", "already-running",
	"already-spent", "amended-since", "application-support", "approval-forgeability",
	"approved-and-amended-since", "architect-amendments", "architect-pass",
	"artifact-changing", "artifact-home", "at-act", "attach-detach", "authority-model",
	"authority-relevant", "authorization-by-capability", "back-link", "base-revision",
	"bespoke-per-specialist", "bounce-when-idle", "branch-publication", "broken-sandbox",
	"browser-profile", "bulk-clear", "bundle-improvement", "candidate-bound",
	"capability-and-scope", "capacity-wait", "carried-out", "carry-back", "changed-file",
	"changed-path", "channel-nobody-reads", "chat-spawns-subprocesses", "check-set",
	"check-stage", "check-to-use", "checked-in", "checked-shape", "claim-audit",
	"clean-tree", "cli-help", "closed-list", "closed-reason", "closed-status",
	"code-implementation", "coding-agent", "configuration-file", "configuration-guide",
	"configured-check", "conflict-avoidance", "conflict-handling",
	"content-security", "context-reconstruction", "context-size", "contributor-mode",
	"control-plane", "conversation-held", "conversation-holding", "cumulative-report",
	"cut-replies", "decide-and-report", "decided-change", "deciders-stop", "default-deny",
	"delivery-pipeline", "dependency-test", "description-not-intent", "developer-session",
	"developer-slot", "developer-written", "diagnosis-class", "directory-sync",
	"disable-auto-merge", "display-identity", "diverged-target", "done-condition",
	"done-conditions", "done-means", "dropped-merge", "duplicate-admission",
	"duplicate-run", "durable-state", "effective-configuration", "empty-delivery",
	"empty-tool", "endpoint-plus-invocation", "endpoint-switch", "event-recording",
	"evidence-confinement", "evidence-not-instruction", "execution-evidence",
	"execution-policy", "execution-termination", "factory-flow", "factory-health",
	"factory-problems", "failure-storm", "fast-forward-or-nothing", "file-granularity",
	"final-reply", "fixed-roles", "fixture-proven", "fixture-shape",
	"footprint-and-dependency", "force-pushing", "force-resolves", "forge-hygiene",
	"forge-url", "full-budget", "full-suite", "further-reading", "give-back",
	"goal-level-approval", "goal-quality", "goals-directory", "grant-refusal", "hand-back", "has-a-disposition",
	"hand-closer", "handed-over", "harness-error", "harness-made", "harness-run",
	"head-behind-target", "held-back", "held-work", "human-approval", "ignore-rule",
	"independent-reviewer", "individually-acceptable", "initialized-here",
	"integrated-work", "integration-resume", "integration-stop", "interrupted-review",
	"introduction-then-goals", "invalid-record", "invariants-index", "label-model",
	"launch-preparation", "legacy-migration", "long-held", "machine-read",
	"machine-too-busy", "management-conversation", "management-conversion",
	"management-loop", "management-request", "management-role", "mechanical-low-risk",
	"memory-budget", "memory-save", "merge-back", "merge-rather-than-move",
	"merge-semantics", "minimum-interval", "missing-report", "missing-target",
	"model-dependent", "model-provider", "model-selection", "model-unavailable",
	"model-version", "moved-anchor", "named-account", "native-resume", "new-file",
	"new-format", "newer-run", "no-fallback", "no-hosted-control-plane", "no-schema-change",
	"no-side-conversations", "no-stub", "not-startable", "nothing-running", "once-per-item",
	"once-per-publication", "operator-action", "operator-activation", "operator-attention",
	"partial-handoff", "pass-and-query", "past-reset",
	"path-string", "pinned-candidate", "pinned-install", "plan-mode", "preserved-run",
	"preserved-work", "private-identifier", "product-document", "product-management",
	"product-pass", "product-role", "product-to-global", "project-state", "protected-file",
	"protected-home", "protected-source", "provider-adapter", "provider-call",
	"provider-general", "provider-hold", "provider-outage", "provider-plugin",
	"provider-policy", "provider-refusal", "provider-stop", "provider-unavailable",
	"provider-wait", "queue-record", "queue-worker", "queued-head", "read-model",
	"readme-split", "ready-work", "reboot-class", "recent-activity", "records-store",
	"recoverable-failure", "recoverable-versus-terminal", "recovery-policy",
	"recurring-task", "red-target", "registered-assessor", "release-and-record",
	"release-readiness", "released-claim", "remote-boundary", "remote-target",
	"repeated-failure", "replay-test", "reportable-event", "repository-setting",
	"resolve-then-write", "restart-requests", "restart-surviving", "retire-raise",
	"review-independence", "review-input", "review-round", "revision-bound", "revision-log",
	"role-capability", "role-contract", "role-definition", "role-definitions", "role-name",
	"rolled-back", "run-activity", "run-by-run", "run-ending", "run-state", "run-status",
	"run-stop", "runs-on", "runtime-internal", "runtime-state", "safe-write", "same-cell",
	"same-path", "same-provider", "same-run", "scheduled-pass", "security-coordinator",
	"security-review", "shipped-surface", "silent-stream", "slack-reporting",
	"software-delivery-bound", "spend-follows-the-work", "spine-touching", "split-era",
	"stale-build", "state-schema", "step-attempt", "still-applies", "still-moving",
	"stopped-run", "stray-identity", "structured-output",
	"subject-continuity", "symlink-escape", "sync-window", "target-failure",
	"target-position", "team-mode", "technical-health", "terminal-result", "terminal-run",
	"timing-flake", "token-efficiency", "tool-access", "tool-control", "traceable-chain",
	"tracker-action", "tracker-sync", "tree-listing", "turn-boundary", "turn-size",
	"uncertain-save", "unmeetable-item-returns", "unowned-entry", "unresolved-at-cap",
	"unresolved-escalation", "usage-limit", "usage-pause", "vanished-process",
	"web-security", "web-service", "whole-content", "whole-spine", "whole-stage",
	"work-turn", "worktree-write", "wrap-in-actions", "writable-root", "write-shell",
}

var termParts = regexp.MustCompile(`[-\s]+`)

// Pattern is what a term is looked for with.
func Pattern(term Term) *regexp.Regexp {
	return compile(term.Match, term.Exact, term.Whole)
}

func compile(matches []string, exact, whole bool) *regexp.Regexp {
	var alternatives []string
	for _, match := range matches {
		if exact {
			alternatives = append(alternatives, regexp.QuoteMeta(match))
			continue
		}
		var parts []string
		for _, part := range termParts.Split(strings.TrimSpace(match), -1) {
			parts = append(parts, regexp.QuoteMeta(part))
		}
		alternatives = append(alternatives, strings.Join(parts, `[-\s]*`))
	}
	expression := `(?i)\b(?:` + strings.Join(alternatives, "|") + `)`
	if whole {
		expression += `\b`
	}
	return regexp.MustCompile(expression)
}

// Count is how many times term occurs in body.
func Count(term Term, body string) int {
	found := Pattern(term).FindAllStringIndex(body, -1)
	if len(term.Except) == 0 {
		return len(found)
	}
	excepted := make(map[int]bool)
	for _, at := range compile(term.Except, false, false).FindAllStringIndex(body, -1) {
		excepted[at[0]] = true
	}
	count := 0
	for _, at := range found {
		if !excepted[at[0]] {
			count++
		}
	}
	return count
}
