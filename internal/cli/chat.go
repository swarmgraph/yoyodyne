package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/evaluation"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/repositoryread"
	"github.com/mason-bryant/yoyodyne/internal/research"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// chatWorkItemStatus is the tracker slice a product conversation is built from.
// Open work is what product intent is currently being spent on; the closed
// history is not what the operator is deciding about.
const chatWorkItemStatus = "open"

// chatTrackerTimeout bounds one tracker command taken on a conversation's
// behalf, whether that is reading the state the product context is assembled
// from or creating an item the operator approved. An unresponsive tracker
// delays a conversation rather than hanging it at the prompt.
const chatTrackerTimeout = 30 * time.Second

type chatOutput struct {
	Evidence *chat.Evidence `json:"evidence,omitempty"`
	Reply    string         `json:"reply,omitempty"`
	// Proposals are what the turn proposed and nothing more. Nothing was created
	// for any of them: a single message has nobody standing at a prompt, so what
	// it proposes is reported and the decision arrives as its own message. What is
	// still waiting once this one is over is Pending, which is the wider list.
	Proposals []chat.PendingProposal `json:"proposals,omitempty"`
	// Admitted are the work items the turn put in the queue without asking, each
	// with what let it through. Unlike proposals these already exist, so they are
	// reported here for the same reason the actions are: a one-shot message has
	// nobody to tell afterwards, and this is the telling.
	Admitted []chat.AdmittedItem `json:"admitted,omitempty"`
	// Concerns are what this turn's product manager would not propose until
	// somebody answers it. A one-shot message has nobody at a prompt to answer,
	// so they are reported as the open questions they are rather than as work,
	// and the answer arrives as its own message. What is still waiting once this
	// one is over is Unanswered, which is the wider list.
	Concerns []chat.PendingConcern `json:"concerns,omitempty"`
	// Actions are the tracker changes the product manager made while answering.
	// Unlike proposals they already happened, so they are reported rather than
	// offered.
	Actions []chat.TrackerOutcome `json:"actions,omitempty"`
	// Exchanges are the rounds of asking another role the reply conducted. They
	// are reported for the reason the actions are, and for one more: a
	// conversation that went and asked another agent something is exactly what an
	// operator must never have to discover afterwards.
	Exchanges []chat.ExchangeRound `json:"exchanges,omitempty"`
	// Research are the rounds of evidence-gathering the reply set off, and
	// Evaluation the recommendation it recorded. Both already happened, so they
	// are reported for the reason the actions are: research spends the operator's
	// money outside this machine, and an evaluation is durable. EvaluationProblem
	// names one that could not be kept, because a lost evaluation is reasoning
	// nobody can find afterwards.
	Research          []chat.ResearchRound   `json:"research,omitempty"`
	Evaluation        *evaluation.Evaluation `json:"evaluation,omitempty"`
	EvaluationProblem string                 `json:"evaluation_problem,omitempty"`
	// RepositoryReads are the rounds of reading the repository at a recorded
	// commit the reply set off. Each is recorded on the conversation as the
	// commit, the path, and the time, and reported here for the reason the
	// research is: what a reply's advice rests on is the operator's to see.
	RepositoryReads []chat.RepositoryRound `json:"repository_reads,omitempty"`
	// Picture is how old the picture of the repository the reply was answered
	// from was, in landings on the target branch, and what the harness did about
	// it. It is reported for the reason the reads are: what a reply's advice
	// rests on is the operator's to see, and a re-read the harness made unasked
	// is one they have to be told about.
	Picture *chat.PictureAge `json:"picture,omitempty"`
	// CompactionSaves report the memory turn taken before rebuilding the provider
	// session, including one that failed and left the waiting message unanswered.
	CompactionSaves []chat.CompactionSave `json:"compaction_saves,omitempty"`
	// RecordCuts are the parts of the reply the conversation's event log holds
	// only the beginning of, with where each cut falls and the reply's whole
	// size. The reply above is whole; the durable record of it is not.
	RecordCuts []execution.ReplyCut `json:"record_cuts,omitempty"`
	// ResultsCarriedOver reports that the reply stopped where it did because the
	// product manager ran out of rounds of tracker actions, with results it has
	// not seen. They are recorded with the conversation and reach it when the
	// conversation is next spoken to, which for a one-shot message is a later
	// invocation.
	ResultsCarriedOver bool `json:"results_carried_over,omitempty"`
	// HandedBack is each tracker block the harness refused whole and handed back
	// to the role within the same message, in the harness's own words. What the
	// role re-issued after it is among the actions.
	HandedBack []string `json:"handed_back,omitempty"`
	// Reports are what the product manager filed for the operator while it
	// answered, and ReportProblem is one that could not be read or kept. Both
	// are reported here for the same reason the actions are: they already
	// happened, and a report nobody is shown is one nobody reads.
	Reports       []report.Report `json:"reports,omitempty"`
	ReportProblem string          `json:"report_problem,omitempty"`
	// Harness is what an operator command printed when the message turned out to
	// be one. It is a separate field from the reply because nothing said it: the
	// harness answered, the product manager was never asked, and no turn was
	// spent.
	Harness string `json:"harness,omitempty"`
	// Decisions are what the message decided about proposals the conversation was
	// waiting on, and Answers what it answered among the concerns it stopped on.
	// Like a command they are the harness's own answer rather than anything that
	// was said: the decision is carried out here, no turn is spent, and the agent
	// hears about it when it is next spoken to.
	Decisions []chat.DecisionOutcome `json:"decisions,omitempty"`
	Answers   []chat.AnswerOutcome   `json:"answers,omitempty"`
	// SideThread is set when the message was answered on a side thread rather
	// than on the main conversation — because that conversation was mid-turn and
	// the agent holds side threads, or because the message continued one. Reply
	// is then the side thread's prose, and nothing else in this document is
	// filled: a side thread proposes, admits, decides, and acts on nothing, and
	// what it promised is listed here as tentative.
	SideThread *sideThreadOutput `json:"side_thread,omitempty"`
	// Pending are the proposals still awaiting a decision once this message is
	// over, which is what a script deciding them next has to name. It is not the
	// same list as Proposals: that one is what this turn proposed, and this one is
	// everything nobody has decided, including proposals from earlier messages.
	// Unanswered is the same list for concerns: everything still waiting for an
	// answer, named by the identifier the next message answers it with.
	Pending    []chat.PendingProposal `json:"pending,omitempty"`
	Unanswered []chat.PendingConcern  `json:"unanswered,omitempty"`
	// Writes are the documents this turn wrote and PendingWrites is everything
	// still awaiting a decision once the message is over, kept apart for the
	// reason the proposals above are. Nothing was written to the repository for
	// either list: a document reaches it when an approval names it and not before.
	Writes        []chat.PendingWrite `json:"writes,omitempty"`
	PendingWrites []chat.PendingWrite `json:"pending_writes,omitempty"`
	// WriteDecisions are what the message decided about documents the
	// conversation was waiting on. Unlike the lists above these already happened:
	// an approved document is in the repository, with the operator's approval
	// recorded in its frontmatter.
	WriteDecisions []chat.WriteOutcome `json:"write_decisions,omitempty"`
	Error          string              `json:"error,omitempty"`
}

func runChat(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	message := flags.String("message", "", "send one message and print the reply instead of opening an interactive conversation")
	fresh := flags.Bool("new", false, "start a new conversation instead of resuming the recorded one")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON (requires --message)")
	sideThread := flags.String("side-thread", "", "continue the named side thread with --message instead of reaching the main conversation")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "chat does not accept positional arguments; use --message to send one message")
		printChatUsage(stderr)
		return 2
	}
	if *jsonOutput && *message == "" {
		fmt.Fprintln(stderr, "chat --json requires --message: an interactive conversation has no single result to encode")
		return 2
	}
	if err := sideThreadFlagProblems("chat", *sideThread, *message, *fresh); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	return converse(ctx, domain.RoleProductManager, conversationRequest{
		configPath: *configPath,
		message:    *message,
		fresh:      *fresh,
		jsonOutput: *jsonOutput,
		sideThread: *sideThread,
	}, stdin, stdout, stderr)
}

// conversationRequest is what an operator asked of one conversation, whichever
// command they reached it through. `yoyo chat` and `yoyo agent chat` differ in
// which role they address and in nothing else, so they share this rather than
// growing two conversations that drift apart.
type conversationRequest struct {
	// agentName is the configured agent the operator named, and is empty when
	// they named none. It is carried rather than resolved back from the role
	// because a role two agents fill has no single answer: an operator who named
	// one of them must reach that one, with its persona and its model, and a
	// command that resolved the role again would silently reach whichever sorted
	// first.
	agentName  string
	configPath string
	message    string
	fresh      bool
	jsonOutput bool
	// sideThread names a side thread the message continues, and is empty for
	// every message that reaches the main conversation or opens a side thread of
	// its own. It is only ever set with a message: a side thread is a bounded
	// number of turns and not a prompt somebody sits at.
	sideThread string
}

// converse holds one conversation with one role: a single message and its reply,
// or the interactive conversation the operator stays inside.
//
// A single message meets the role's main thread busy in one of two ways, and
// the agent's configuration chooses which. An agent that queues — every agent
// until it says otherwise — has the message wait behind the turn in flight,
// which is what every message did before side threads existed. An agent that
// holds side threads has it answered beside the busy turn instead, on a side
// thread of its own, and what that thread works out reaches the main thread as
// memory rather than as a turn. Nothing about the choice is authority: a side
// thread judges and drafts, and the main thread ratifies.
func converse(ctx context.Context, role domain.AgentRole, request conversationRequest, stdin io.Reader, stdout, stderr io.Writer) int {
	prepared, err := prepareChat(ctx, role, request.agentName, request.configPath, stderr)
	if err != nil {
		return reportChatFailure(stdout, stderr, request.jsonOutput, role, nil, err)
	}
	return converseWith(ctx, prepared, request, stdin, stdout, stderr)
}

// converseWith is the conversation from the point the role is resolved: which
// thread the message reaches, and then the conversation itself. It is separate
// from converse so the routing can be driven over real stores without a
// provider to resolve.
func converseWith(ctx context.Context, prepared preparedChat, request conversationRequest, stdin io.Reader, stdout, stderr io.Writer) int {
	role := prepared.identity.Role
	// A message continuing a side thread is that thread's next turn and never
	// touches the main conversation, held or not.
	if request.sideThread != "" {
		return askAside(ctx, prepared, request, "", stdout, stderr)
	}
	var hold *runstate.ConversationHold
	var err error
	switch {
	case request.mayGoAside(prepared):
		// The claim that refuses rather than waits is what decides: a main thread
		// held right now is what a side thread is for, and one nobody holds takes
		// the message itself exactly as it always has.
		hold, err = prepared.store.TryClaim(prepared.identity)
		if errors.Is(err, runstate.ErrConversationHeld) {
			beside, loadErr := prepared.store.Read(prepared.identity)
			if loadErr == nil {
				return askAside(ctx, prepared, request, beside.ConversationID, stdout, stderr)
			}
			// A main thread held with no record to read yet — its first turn is
			// still in flight — is one no side thread can be opened beside, because
			// a side stream names the conversation it belongs to. The message waits
			// for it, which is what it would have done before the knob was set, and
			// says so rather than waiting silently.
			fmt.Fprintf(stderr, "the %s is mid-turn and its conversation has no record yet to hold a side thread beside, so this message waits for it: %v\n",
				chat.RoleTitle(role), loadErr)
			hold, err = prepared.claim(ctx, true, stderr)
		}
	default:
		hold, err = prepared.claim(ctx, true, stderr)
	}
	if err != nil {
		return reportChatFailure(stdout, stderr, request.jsonOutput, role, nil, err)
	}
	defer hold.Release()
	session, err := prepared.open(ctx, hold, request.fresh, true, stderr)
	if err != nil {
		return reportChatFailure(stdout, stderr, request.jsonOutput, role, nil, err)
	}

	if request.message != "" {
		return runChatMessage(ctx, session, role, request.message, request.jsonOutput, stdout, stderr)
	}

	// The conversation is held over a console rather than the raw streams: on a
	// terminal that gives the operator's typing a region output never writes
	// into, and anywhere else it is the same conversation as an ordinary stream
	// of text. Either way what is recorded is identical.
	screen := console.Open(console.Options{In: stdin, Out: stdout})
	defer screen.Close()
	printChatHeader(screen, role, session.Evidence(), session.Freshness(ctx))
	converseErr := session.Converse(ctx, screen)
	// What was decided about is the console's to dress, and it is asked before
	// the console is closed: restoring the terminal changes how it reads input,
	// not what it is allowed to be shown in.
	theme := screen.Theme()
	// The terminal is handed back before the closing report, so the evidence
	// below is written to a terminal in the state the operator's shell left it.
	if err := screen.Close(); err != nil {
		fmt.Fprintf(stderr, "restore the terminal: %v\n", err)
	}
	printOpenConcerns(stdout, theme, session.Concerns())
	printUndecidedProposals(stdout, theme, session.Proposals())
	printUndecidedWrites(stdout, theme, session.Writes())
	printChatEvidence(stdout, session.Evidence())
	if converseErr != nil {
		fmt.Fprintf(stderr, "conversation ended: %v\n", converseErr)
		return 1
	}
	return 0
}

// runChatMessage answers one thing the operator said from a command line. It is
// either a command the harness carries out or a message the role answers, and
// which of the two it is is the conversation's own rule rather than a second one
// written here: a slash means the same thing in a single message as it does at
// the prompt.
func runChatMessage(ctx context.Context, session *chat.Session, role domain.AgentRole, message string, jsonOutput bool, stdout, stderr io.Writer) int {
	if err := session.PublishDocuments(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Without this the command would be said to the agent, which has no way to
	// carry one out and every reason to be confused by one — and the operator
	// would pay for the turn.
	if chat.IsCommand(message) {
		return runChatCommand(ctx, session, message, jsonOutput, stdout, stderr)
	}
	// An answer to a proposal this conversation is still waiting on decides it
	// here rather than being said to the agent, and an answer to a concern it
	// stopped on answers that. Without this the operator's "y" arrived as
	// ordinary speech to a role that cannot create the item, so the approval was
	// spent, nothing reached the queue, and nothing said so — and once a message
	// could decide a proposal, a "yes" meant for a question approved whatever
	// proposal was undecided instead.
	if decided, settled, err := session.Decide(ctx, message); settled {
		return reportChatDecisions(stdout, stderr, jsonOutput, role, session, decided, err)
	}
	// An answer to a document the conversation is waiting on is carried out here
	// for the same reason, and the cost of getting it wrong is higher: the "y"
	// that reached the agent as speech was an approval to write a file, and the
	// document went back to being something a person transcribed by hand.
	if outcomes, decided, err := session.DecideWrites(message); decided {
		return reportChatWriteDecisions(stdout, stderr, jsonOutput, role, session, outcomes, err)
	}
	// A one-shot message resumes the same recorded conversation an interactive
	// one does, and carries the same risk of answering from a picture taken hours
	// ago. It is said on stderr because stdout is the reply and, with --json, a
	// document.
	fmt.Fprintln(stderr, session.Freshness(ctx))
	reply, err := session.Send(ctx, message)
	if err != nil {
		// The answer travels with the failure. A turn that produced one is worth
		// reading even when what it proposed could not be read.
		return reportChatFailure(stdout, stderr, jsonOutput, role, &reply, err, session.RenderReply)
	}
	if jsonOutput {
		evidence := reply.Evidence
		return writeJSON(stdout, stderr, chatOutput{
			Evidence:  &evidence,
			Reply:     reply.Text,
			Proposals: reply.Proposals,
			// Everything still awaiting a decision, exactly as the text output
			// below lists it. A script reads this document where a person reads
			// that, so a turn that reported only what it had just proposed would
			// hide the earlier proposals from the reader least able to go
			// looking for them.
			Pending:    session.Proposals(),
			Unanswered: session.Concerns(),
			// The documents this turn wrote, and everything still waiting: nothing
			// was written for either, and a script that will approve one next has
			// to be able to name it.
			Writes:             reply.Writes,
			PendingWrites:      session.Writes(),
			Admitted:           reply.Admitted,
			Concerns:           reply.Concerns,
			Actions:            reply.Actions,
			Exchanges:          reply.Exchanges,
			Research:           reply.Research,
			RepositoryReads:    reply.RepositoryReads,
			Picture:            reply.Picture,
			CompactionSaves:    reply.CompactionSaves,
			RecordCuts:         reply.RecordCuts,
			Evaluation:         reply.Evaluation,
			EvaluationProblem:  reply.EvaluationProblem,
			ResultsCarriedOver: reply.ResultsCarriedOver,
			HandedBack:         reply.HandedBack,
			Reports:            reply.Reports,
			ReportProblem:      reply.ReportProblem,
		})
	}
	fmt.Fprintln(stdout, session.RenderReply(reply.Text))
	// A one-shot message has no console to ask, so what it may be dressed with is
	// asked of the stream it is writing to. A redirected one is undressed, which
	// is the same answer an interactive conversation over the same stream gives.
	theme := console.ThemeFor(stdout, os.Getenv)
	printChatHandedBack(stdout, reply.HandedBack)
	printChatActions(stdout, role, reply.Actions, reply.ResultsCarriedOver)
	printChatResearch(stdout, reply.Research)
	printChatRepositoryReads(stdout, reply.RepositoryReads)
	printChatPicture(stdout, reply.Picture)
	printChatCompactionSaves(stdout, reply.CompactionSaves)
	printChatRecordCuts(stdout, reply.RecordCuts)
	printChatEvaluation(stdout, reply.Evaluation, reply.EvaluationProblem)
	printChatExchanges(stdout, role, reply.Exchanges)
	printChatAdmitted(stdout, reply.Admitted)
	printChatReports(stdout, theme, role, reply.Reports, reply.ReportProblem, session.RenderReply)
	// Everything unanswered and everything undecided is listed rather than only
	// what this turn raised or proposed: an answer or a decision arrives as its
	// own message, so what the operator has to be able to name is the whole of
	// what is still waiting on them.
	printChatConcerns(stdout, theme, role, session.Concerns())
	printChatProposals(stdout, role, session.Proposals())
	printChatWrites(stdout, role, session.Writes())
	printChatEvidence(stdout, reply.Evidence)
	return 0
}

// reportChatDecisions reports what a message decided about the proposals a
// conversation was waiting on, or answered among the concerns it stopped on.
// What was decided is written whether or not the answer went on to fail, for
// the reason a failed command still writes what it did: a decision that was
// recorded happened, and the failure is about the rest of the answer.
func reportChatDecisions(stdout, stderr io.Writer, jsonOutput bool, role domain.AgentRole, session *chat.Session, decided chat.Decided, err error) int {
	pending := session.Proposals()
	unanswered := session.Concerns()
	if jsonOutput {
		evidence := session.Evidence()
		output := chatOutput{Evidence: &evidence, Decisions: decided.Decisions, Answers: decided.Answers, Pending: pending, Unanswered: unanswered}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	printChatDecisions(stdout, decided)
	// A one-shot message has no console to ask, so what it may be dressed with is
	// asked of the stream it is writing to, as the reply path asks.
	theme := console.ThemeFor(stdout, os.Getenv)
	printChatConcerns(stdout, theme, role, unanswered)
	printChatProposals(stdout, role, pending)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	return 0
}

// reportChatWriteDecisions reports what a message decided about the documents a
// conversation was waiting on. Like the proposal decisions beside it, what was
// carried out is written whether or not the answer went on to fail: a document
// that reached the repository is in it, and the failure is about the rest.
func reportChatWriteDecisions(stdout, stderr io.Writer, jsonOutput bool, role domain.AgentRole, session *chat.Session, outcomes []chat.WriteOutcome, err error) int {
	pending := session.Writes()
	if jsonOutput {
		evidence := session.Evidence()
		output := chatOutput{Evidence: &evidence, WriteDecisions: outcomes, PendingWrites: pending}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	printChatWriteDecisions(stdout, outcomes)
	printChatWrites(stdout, role, pending)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	return 0
}

// printChatDecisions says what the operator's message did to each proposal or
// concern it named. Nothing here was said by the agent, so it is written
// plainly rather than as part of a reply: an approval that created an item and
// one the tracker refused are the two things the operator has to be able to
// tell apart.
func printChatDecisions(writer io.Writer, decided chat.Decided) {
	fmt.Fprint(writer, renderDecisions(decided))
}

// renderDecisions is what a message that decided or answered something says
// back. It is one rendering rather than one per client: a decision made from a
// channel and the same decision made at a terminal are the same act, and two
// accounts of it that could drift apart would be the operator reading two
// answers to one question.
func renderDecisions(decided chat.Decided) string {
	var rendered strings.Builder
	if len(decided.Decisions) > 0 {
		fmt.Fprintf(&rendered, "You decided %d proposal(s):\n\n", len(decided.Decisions))
		for _, made := range decided.Decisions {
			rendered.WriteString(made.Render())
		}
	}
	if len(decided.Answers) > 0 {
		fmt.Fprintf(&rendered, "You answered %d question(s):\n\n", len(decided.Answers))
		for _, answered := range decided.Answers {
			rendered.WriteString(answered.Render())
		}
	}
	return rendered.String()
}

// runChatCommand carries out an operator command that arrived as a single
// message. The harness answers it rather than the product manager, so there is
// no turn, no reply, and nothing spent — which is also why the conversation's
// freshness is not printed here: how old its picture of the product is says
// nothing about an answer it had no part in.
func runChatCommand(ctx context.Context, session *chat.Session, line string, jsonOutput bool, stdout, stderr io.Writer) int {
	var rendered strings.Builder
	err := session.Command(ctx, line, &rendered)
	return reportChatCommand(stdout, stderr, jsonOutput, session.Evidence(), rendered.String(), err)
}

// reportChatCommand shows what a command did. What it printed is written
// whether or not it went on to fail, for the same reason a failed turn still
// carries its reply: a command that recorded something and then could not
// report it recorded it all the same. The failure itself is the command's,
// which is what the exit code says.
func reportChatCommand(stdout, stderr io.Writer, jsonOutput bool, evidence chat.Evidence, rendered string, err error) int {
	if jsonOutput {
		output := chatOutput{Evidence: &evidence, Harness: rendered}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	fmt.Fprint(stdout, rendered)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	return 0
}

// openChat builds a role's conversation from configuration: the configured
// agent filling that role, the repository's own Markdown, the tracker state as
// it stands, the harness the operator steers work with, and the durable record a
// previous process left behind. The returned hold is this process's claim on
// that conversation: taken here, put down whenever nobody is talking to the
// agent, and released for good by the caller.
//
// Everything the operator's own commands need is wired for every role, because
// those commands are the operator's authority rather than the agent's and they
// mean the same thing in every conversation. What differs between roles is what
// the role itself may ask for, and that is the contract and the authority table
// in the chat package rather than anything decided here.
//
// attended says the operator's own command is at the other end of this
// conversation — `yoyo chat`, interactive or `--message` — rather than a turn the
// harness is taking for itself, and it is the one thing the two kinds of caller
// genuinely disagree about. It decides two waits the same way. A turn already in
// flight: the operator waits, because they have already typed the command, a
// turn ends on its own, and being turned away by one was the seam that made the
// product manager unreachable; a background delivery does not, because nothing
// was asked of the agent yet, the attempt is given back and a later pass makes
// it, and a dispatcher holding its lease and its budget open for the length of
// somebody else's turn is a worse answer than coming back. And a provider with
// no capacity: the operator's turn waits the limit out under the bounds a run
// waits under, because the alternative is the work the turn was about to do
// being lost and an unattended `--message` caller having nobody to retry it;
// a background turn does not, because every one of those callers already paces
// itself on the refusal — the next cadence, the next pull — and one that slept
// through the window would hold the scheduler that took it for hours.
func openChat(ctx context.Context, role domain.AgentRole, agentName, configPath string, fresh, attended bool, stderr io.Writer) (*chat.Session, *runstate.ConversationHold, error) {
	return openChatOnModel(ctx, role, agentName, configPath, "", fresh, attended, stderr)
}

// openChatOnModel is openChat with the turns this session takes asking for model
// rather than the agent's own, where model is not empty. It is how a recurring
// task that names its own model is served: the same conversation, account,
// failover, and authority, on the task's model for the turns it takes. Nothing
// about the conversation's durable record is changed by it, so the next session
// opened on the conversation asks for the agent's model again.
func openChatOnModel(ctx context.Context, role domain.AgentRole, agentName, configPath, model string, fresh, attended bool, stderr io.Writer) (*chat.Session, *runstate.ConversationHold, error) {
	prepared, err := prepareChat(ctx, role, agentName, configPath, stderr)
	if err != nil {
		return nil, nil, err
	}
	if err := prepared.onModel(model); err != nil {
		return nil, nil, err
	}
	hold, err := prepared.claim(ctx, attended, stderr)
	if err != nil {
		return nil, nil, err
	}
	session, err := prepared.open(ctx, hold, fresh, attended, stderr)
	if err != nil {
		return nil, nil, errors.Join(err, hold.Release())
	}
	return session, hold, nil
}

// preparedChat is one role's conversation resolved as far as it can be without
// taking it: the agent that fills the role, the provider and the account that
// serve it, and the stores it is recorded in. It is where both ways of reaching
// the role start from — the main thread, claimed and opened below, and a side
// thread held beside it while the main thread is busy — so the agent a side
// question reaches is the agent the main conversation would have been with,
// under the same account and the same provider. A side thread continued by its
// identifier is held to the same agent: the runner refuses a stream another
// agent opened, so the account and the memory a continuation lands on are
// always this agent's own.
type preparedChat struct {
	parts    components
	name     string
	agent    config.AgentConfig
	account  config.AccountEndpoint
	provider chat.Backend
	// runner builds adapters for other role invocations; nil keeps an
	// explicitly injected backend.
	runner   execution.ProcessRunner
	store    *runstate.ConversationStore
	memories *runstate.MemoryStore
	// laneReports is where a program manager's lane report is kept.
	laneReports *runstate.LaneReportStore
	identity    runstate.ConversationIdentity
	// model is the selector this session's turns ask for in place of the agent's
	// own, and empty for every session but a recurring task's that named one.
	model string
}

// onModel has this session's turns ask for model rather than the agent's own. An
// empty model leaves the agent's, which is every conversation but a recurring
// task's that names one. The selector is held to the rule the agent's own is,
// because it reaches the provider's command line exactly as that one does.
func (p *preparedChat) onModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	if err := config.ValidateModelSelector(model); err != nil {
		return fmt.Errorf("%s agent %s turn %s", p.identity.Role, p.name, err)
	}
	p.model = model
	return nil
}

// requestedModel is the family selector this session's turns ask for.
func (p preparedChat) requestedModel() string {
	if p.model != "" {
		return p.model
	}
	return p.agent.Model
}

// modelVersion is the pinned version this session's turns ask for. A pin is a
// version of the agent's own model family, so a session asking for another
// model carries none: a pin to one family's version asked for under another
// family's alias would be a request for neither.
func (p preparedChat) modelVersion() string {
	if p.model != "" && p.model != strings.TrimSpace(p.agent.Model) {
		return ""
	}
	return p.parts.config.AgentModelVersion(p.name)
}

// effort is the effort level this session's turns ask for. It is the agent's
// whatever model the session asks for, because a task's own model moves which
// model answers and not how hard it is asked to think.
func (p preparedChat) effort() string {
	return p.parts.config.InvocationEffort(p.agent, p.requestedModel())
}

// prepareChat resolves everything a conversation with the role needs that does
// not depend on holding it.
func prepareChat(ctx context.Context, role domain.AgentRole, agentName, configPath string, stderr io.Writer) (preparedChat, error) {
	// The conversation is built over the same components a run is, because
	// steering work from inside it means executing exactly the runs
	// `yoyodyne run` would have executed.
	parts, err := buildComponents(configPath)
	if err != nil {
		return preparedChat{}, err
	}
	cfg := parts.config

	name, agent, err := conversationAgent(cfg, role, agentName)
	if err != nil {
		return preparedChat{}, err
	}
	// A conversation runs on whatever adapter this build has, so what is asked is
	// whether the backend the agent named resolves to one — which a provider the
	// project declared does, and a backend nothing can launch does not.
	if !providerRuns(cfg, agent.Backend) {
		return preparedChat{}, fmt.Errorf("a conversation requires an agent on a backend this build can launch, and the %s agent %s is configured for %q", role, name, agent.Backend)
	}
	if err := config.ValidateModelSelector(agent.Model); err != nil {
		return preparedChat{}, fmt.Errorf("%s agent %s %s", role, name, err)
	}

	processRunner := parts.runner
	// The conversation is held under the account its agent is configured for, and
	// the availability below is asked of that same account: a check made against
	// the machine's own login would report a conversation ready to open on an
	// account nobody had signed in to.
	account, err := conversationAccount(cfg, parts.stateRoot, name)
	if err != nil {
		return preparedChat{}, fmt.Errorf("resolve the account %s agent %s runs under: %w", role, name, err)
	}
	// The provider is built from what the project declares — its adapter, its
	// executable, and its dialect — and then pointed at the account's own
	// provider home. The two are separate decisions: which provider runs the
	// agent is the project's, and which login it runs under is this machine's.
	provider := providerBackendIn(cfg, agent.Backend, processRunner, account.Directory)
	// A provider that was there and did not answer -- its version check timed out,
	// exited nonzero, or was cancelled with the pass that asked -- is this error,
	// in the adapter's words, and never the "not installed" below. The
	// cancellation is carried rather than restated, so a pass stopped because its
	// scheduler was stopped reads as that where the pass is recorded, rather than
	// as a failed firing with a cause nobody could fix.
	availability, err := provider.CheckAvailability(ctx)
	if err != nil {
		return preparedChat{}, fmt.Errorf("ask whether the %s backend is ready for %s agent %s: %w", agent.Backend, role, name, err)
	}
	// Availability and login remedies name the configured provider and account.
	// A provider whose executable cannot run here is refused only where the agent
	// has no alternate on another provider that can: one that can serves the
	// conversation's turns, as it would serve a turn the configured provider
	// refused, and the operator is told which installation is missing.
	if !availability.Installed {
		refused := errors.New(availability.NotInstalled(agent.Backend))
		standIn, servable := servableAlternate(ctx, cfg, parts.stateRoot, name, processRunner)
		if !servable {
			return preparedChat{}, refused
		}
		fmt.Fprintf(stderr, "warning: %v\nThis conversation's turns are served by its configured alternate, %s, until that is fixed.\n",
			refused, runstate.DescribeServingModel(standIn.endpoint.Provider, standIn.model))
	} else if !availability.Authenticated {
		// A login nobody has renewed is a wait rather than a refusal, and it is
		// recorded on the product before the conversation refuses to open: the
		// process that meets it here is usually a scheduled firing nobody is
		// watching, and the record is what tells the operator to log in.
		refused := fmt.Errorf("%w: the %s backend is not authenticated for account %q; run `%s` before starting a conversation (auth method: %s)",
			chat.ErrProviderAway, agent.Backend, account.Alias, accountLoginCommand(cfg, agent.Backend, account), availability.AuthMethod)
		if _, recordErr := parts.outages.Notice(runstate.ProviderOutageObservation{
			Cause:        domain.ProviderUnauthenticated,
			Provider:     agent.Backend,
			AccountAlias: account.Alias,
			Detail:       refused.Error(),
			Waiting:      fmt.Sprintf("the %s conversation", role.Title()),
			At:           time.Now().UTC(),
		}); recordErr != nil {
			return preparedChat{}, errors.Join(refused, fmt.Errorf("record that the provider is answering nobody: %w", recordErr))
		}
		return preparedChat{}, refused
	}
	// A provider that reports itself logged in ends an outage of that kind, on
	// the same evidence the scheduler clears one on. An unreachable one is left
	// standing: this check reads a local record and says nothing about the
	// network, which the first served turn settles.
	if standing, found, err := parts.outages.Standing(); err == nil && found && standing.Cause == domain.ProviderUnauthenticated {
		if _, _, err := parts.outages.Clear(); err != nil {
			fmt.Fprintf(stderr, "warning: the provider is logged in again and the outage could not be cleared: %v\n", err)
		}
	}

	store, err := runstate.NewConversationStore(parts.stateRoot, cfg.Product.ID)
	if err != nil {
		return preparedChat{}, err
	}
	// What this agent knows. A management role's turns are briefed from it and
	// record what they conclude into it, through the context actions; every
	// role's turns read from it what the side conversations held beside this one
	// concluded. The redaction values are the store's, so what a turn writes is
	// redacted before it reaches the disk, exactly as a side stream's merge is.
	memories, err := runstate.NewMemoryStore(parts.stateRoot, cfg.Product.ID, parts.redactValues...)
	if err != nil {
		return preparedChat{}, err
	}
	// Where a program manager rewrites its lane report, redacted against the same
	// values, so a report reaches the disk only as a durable record may.
	laneReports, err := runstate.NewLaneReportStore(parts.stateRoot, cfg.Product.ID, readmodel.CheckLaneReportMover, parts.redactValues...)
	if err != nil {
		return preparedChat{}, err
	}
	return preparedChat{
		parts:       parts,
		name:        name,
		agent:       agent,
		account:     account,
		provider:    provider,
		runner:      processRunner,
		store:       store,
		memories:    memories,
		laneReports: laneReports,
		// The conversation is held, recorded, and resumed under the agent that
		// holds it, so two agents configured for one role are two conversations
		// rather than one they would take turns overwriting.
		identity: runstate.ConversationIdentity{Agent: name, Role: role},
	}, nil
}

// claim takes the role's main conversation for this process.
//
// Claiming queues behind whoever is mid-turn rather than refusing, which is the
// whole of what makes the product manager reachable: `yoyo chat --message` from
// another terminal, and a second window the operator opens beside the first,
// both wait out a turn instead of being turned away by one. Ctrl-C ends the
// wait, and the wait is said out loud once it lasts long enough to notice, so an
// operator whose command has not come back knows what it is behind. The refusing
// claim comes back at once and so says nothing.
func (p preparedChat) claim(ctx context.Context, attended bool, stderr io.Writer) (*runstate.ConversationHold, error) {
	var hold *runstate.ConversationHold
	if err := chat.AwaitConversation(p.identity.Role, stderr, func() error {
		var err error
		if attended {
			hold, err = p.store.Claim(ctx, p.identity)
		} else {
			hold, err = p.store.TryClaim(p.identity)
		}
		return err
	}); err != nil {
		return nil, err
	}
	return hold, nil
}

// open builds the conversation over a hold this process has already taken.
func (p preparedChat) open(ctx context.Context, hold *runstate.ConversationHold, fresh, attended bool, stderr io.Writer) (*chat.Session, error) {
	parts, cfg, repository := p.parts, p.parts.config, p.parts.repository
	name, agent, account, provider, processRunner := p.name, p.agent, p.account, p.provider, p.parts.runner
	role, store, memories, laneReports := p.identity.Role, p.store, p.memories, p.laneReports

	// The goals the repository records, which is what work admitted in this
	// conversation has to name. A repository whose goals cannot be read still
	// opens a conversation: what it costs is the check, and every action that
	// would have been checked says so rather than reading as approved.
	goals, err := loadGoals(repository, cfg.Product)
	if err != nil {
		fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	for _, problem := range goals.Problems {
		fmt.Fprintf(stderr, "warning: goals not read: %s\n", problem)
	}
	// The artifact homes a run may not write into and the documents they own,
	// which is what an item's done-conditions are checked against as it is
	// admitted. Homes whose documents cannot be read still check the paths: a
	// condition naming a design by its path is refused whether or not the design
	// set loaded, and what is lost is the check by name, which is said.
	homes := artifactHomes(repository, cfg, stderr)

	// Where this agent's turn goes if its own endpoint has no capacity, resolved
	// here because resolving it needs the configuration, the provider registry, and
	// the account homes — none of which a conversation is given. An agent that
	// named no alternate provider resolves to nothing at all, which is failover
	// within its own provider behaving exactly as it did.
	failover := conversationFailover(cfg, parts.stateRoot, name, processRunner, stderr)
	// A session asking for the model the agent's alternate names, on the agent's
	// own provider, has no alternate: failing over to the endpoint whose window
	// just closed is a second refusal rather than an alternate, which is the
	// reason the configuration refuses an agent that names its own model there.
	if p.model != "" && failover.backend == nil && strings.TrimSpace(failover.model) == p.model {
		failover = conversationAlternate{}
	}

	ground := newConversationGround(parts, role)
	briefing, err := ground.Gather(ctx)
	if err != nil {
		return nil, err
	}
	for _, problem := range briefing.Problems {
		fmt.Fprintf(stderr, "warning: %s\n", problem)
	}
	// Provider below is the backend the agent named rather than the adapter that
	// launches it: a conversation record has to say which provider answered it,
	// and a project that declared its own has a different answer from the
	// built-in. Providers is the set that name is checked against, so a
	// conversation on a backend nothing can run is refused before the provider is
	// invoked rather than on its first turn.
	session, err := chat.Open(chat.Options{
		Role:    role,
		Backend: provider,
		Store:   store,
		// This process's claim on the conversation, handed over so an interactive
		// one can put it down while the operator is at the prompt. What has to be
		// exclusive is a turn: an idle console that never let go was the reason
		// nothing else could reach the product manager until the operator closed
		// their window.
		Hold: hold,
		// The tracker and the harness behind the operator's commands are the
		// harness's own hands, not the product manager's: they are used only
		// where an operator approved a proposal or asked for something.
		Tracker: chatTracker(processRunner, repository),
		Work:    newConversationWork(parts),
		// The collected reports are the same pile the runs fill, read and written
		// from here because this conversation is where the operator already is.
		Reports: parts.reports,
		// And what counts a report's build against the target branch, asked of the
		// product's repository exactly as `yoyo reports` and the channel ask it, so
		// a report from a build that predates a fix says so before the product
		// manager admits work from it.
		Builds: repositoryDeployments{
			repository: repository,
			runner:     processRunner,
			timeout:    chatTrackerTimeout,
		},
		// The same directives every run reads before it commits to work. One
		// recorded from this conversation is not this conversation's: it belongs
		// to the product, and it reaches runs in other processes exactly as it
		// reaches this one.
		Directives: conversationDirectives{store: parts.directives, productID: cfg.Product.ID},
		// The same hold every run reads before it spends. A turn is a provider
		// invocation, so `yoyo pause` covers this conversation exactly as it covers
		// the work steered from it.
		Holds: parts.holds,
		// Where a provider refusing this conversation for want of capacity is
		// written down. It is the same log every other process outside a run
		// records one in, so an exhausted limit reaches the channel from wherever
		// it was met rather than only from a run.
		UsageLimits: parts.usageLimits,
		// And where a provider answering nobody is recorded when a turn meets it
		// and cleared when one is served, for the same reason: the wait is every
		// process's for as long as it lasts.
		ProviderOutages: parts.outages,
		// And where a served turn says which account and model it was served on,
		// which is what reads a refusal of them as lifted before its quoted reset.
		CapacityServed: parts.capacityServed,
		// The operator's switch over the work the harness chooses for itself, so
		// holding intake is something they can do from the conversation they are
		// already in rather than from a second tool.
		Intake: parts.intake,
		// The inter-role ask channel, wired for the roles that are on it. A
		// question one role cannot answer itself reaches the role that can through
		// here, rather than through the operator or through a work item.
		Exchanges:           conversationExchanges(parts, role, provider, p.runner),
		AskRoundsPerMessage: cfg.Exchange.MaxRounds,
		// The durable budget the development manager's triage decisions spend.
		// It is wired for that role alone, like the docket those decisions are
		// about, so a repair grant or a re-run is bounded by what the operator
		// configured wherever it is decided from.
		Triage: conversationTriage(parts, role),
		// The run records those decisions are checked against, so a decision lands
		// on the item whose work actually stopped: the run a docket entry names is
		// the run the decision has to be about. They are also what says where a
		// change is, so work carved out of a stoppage waits for the change it was
		// written against rather than reading as ready without it.
		Stoppages: conversationStoppages(parts, role),
		// The hand that carries out a stop she decides about a run in flight: the
		// operator's stop request, made on her behalf and naming her. Wired for
		// that role alone, so no other conversation can stop a run in her name.
		Stops: conversationStops(parts, role),
		// The hand that dockets the Lead Product Manager's decision about an item
		// whose run is in flight, for the development manager to decide the run
		// from. Wired for her conversation alone.
		InFlight: conversationInFlight(parts, role),
		// What the harness is holding for a person, which is what a repair of stale
		// backlog state is refused by. It is the same derivation the scheduler and
		// every operator surface read, so an item this conversation reports as held
		// is the item the queue is holding back.
		Held: conversationHeldWork(parts),
		// The docket those decisions are about, so a decision takes the stoppage it
		// settled off it. An entry nothing closed comes back on every docket after
		// it, because the docket is rebuilt from durable records at every scan.
		Docket: conversationDocketEntries(parts, role),
		// The same docket for whichever role closes or retires an item, so the
		// entries standing for that item are closed with it.
		ClosedItems: conversationClosedItems(parts),
		// The changes other roles have proposed to the documents this one owns.
		// They are read here so the owner hears the argument; deciding them is the
		// operator's, through `yoyo amendment`.
		Amendments: parts.amendments,
		// The agent's own memory: what a management role recorded in earlier turns,
		// briefed into each turn and written to by it, and what its side threads
		// concluded. A side conversation never speaks into this one: what it worked
		// out arrives as a memory revision naming the stream it came from, and
		// anything it promised stays tentative until this thread ratifies it.
		Memories: memories,
		// Where a program manager's lane report is rewritten, under the state root
		// beside the memory store and never inside it. It is redacted with the
		// values every durable record is, and wired for every role because the
		// authority to write one is decided in the chat package's table.
		LaneReports: laneReports,
		// How evidence from outside the repository is gathered on the role's
		// behalf, bounded by what the operator configured. It is the harness's own
		// hand like the tracker is: the role names a question and a permitted
		// source, and nothing about what runs or where it reaches is the role's.
		Research: conversationResearch(parts),
		// How a management role reads the repository: a named path resolved by the
		// harness's own Git against the tree of the commit HEAD names at that
		// moment, never the working tree. It is wired for every role because the
		// authority to ask is decided in the chat package's table rather than
		// here, and a reader nobody may ask is never asked.
		RepositoryReader: conversationRepositoryReader(parts),
		// Where a recorded recommendation about an operator's idea is kept. It is
		// wired for every role because the authority to record one is decided in
		// the chat package's table rather than here, and a store nobody may write
		// to is never written to.
		Evaluations: parts.evaluations,
		// Where a program manager's request that the supervisor restart a part is
		// recorded. It is wired for every role because the authority to make one
		// is decided in the chat package's table, and the only thing that acts on
		// a request is the supervisor's maintenance pass.
		RestartRequests: parts.restartRequests,
		// Where the operator's hand steps are recorded: the ones he takes through
		// this conversation's commands and answers, and, for a role holding
		// report.file, the ones it noticed him take outside the harness.
		Interventions: parts.interventions,
		// How a document this role owns reaches the repository: the role writes it,
		// the harness confirms it under policy, and performs the write through the
		// same ownership boundary every other mutation of these documents goes
		// through. It is the harness's hand like the tracker beside it — the role
		// still has no filesystem — and it is the same store `yoyo artifact` reads,
		// so a document written from a conversation is one that command lists.
		Documents:         artifactStore(repository, cfg.Product),
		DocumentPublisher: pipelineFrom(parts),
		DocumentPolicy:    artifact.Policy{Brief: cfg.Approvals.Brief, Goals: cfg.Approvals.Goals, Designs: cfg.Approvals.Designs, SpecificationsHome: cfg.Product.Specifications},
		// What work admitted here has to name. It is read from the repository
		// rather than from the conversation, so a goal retired since the
		// conversation opened stops being one work can be admitted under.
		Goals: goals,
		// What an item's done-conditions may not name without a grant. It is read
		// from the repository and the configuration for the reason the goals are,
		// and it is the same set the run reads before it claims the item.
		ArtifactHomes: homes,
		// What this project asks the operator about before work reaches the queue.
		// It is read from the configuration rather than decided here, so the same
		// answer governs a proposal and a direct admission.
		Admission: chat.Admission{
			WorkItems: cfg.Approvals.WorkItems,
			// And the classes of work this project has said it does not want to be
			// asked about, which is the operator narrowing their own gate rather than
			// the harness deciding anything.
			Exempt: cfg.Approvals.WorkItemExemptions,
		},
		// What every turn of this conversation spends, recorded where a run's spend
		// is recorded. A conversation costs money and the record has to say so:
		// what an operator asked to see is what the harness spends on their behalf,
		// and the management conversation is part of that rather than beside it.
		Spend: parts.spend,
		// The alias every turn's spend is charged to is the one this conversation
		// was actually opened under, which under a pool is the agent's own rather
		// than a configuration-wide single account there is none of.
		AccountAlias:   account.Alias,
		ConfigRevision: cfg.Revision(),
		// And which harness is holding it, read once here because a process does not
		// change binary while it lives — which is the whole reason a conversation
		// somebody leaves open for days is worth stamping.
		Build: buildinfo.Commit(),
		// The agent's configured model, or the recurring task's where the task
		// names its own for the turns it takes.
		Model: p.requestedModel(),
		// The exact version of that family this agent's turns ask for, empty for
		// every agent that pins none — which leaves the alias above floating, as it
		// always has. A version the provider has not got is served by the alias and
		// the substitution is recorded, so a pin never stops the agent.
		ModelVersion: p.modelVersion(),
		// And the effort level every one of those turns asks for, which is the
		// agent's whatever model serves the turn: a task's own model, a pinned
		// version's fallback, and a failover alternate all keep it.
		Effort: p.effort(),
		// The one alternate this agent's turn may be served by while the model above
		// has no capacity, empty for every agent that has not enabled failover. It
		// belongs to the agent's own block for the reason its account does: which
		// endpoints a persona is interchangeable across is the operator's judgement,
		// stated per agent rather than derived from the role.
		//
		// All four come from one resolution, because they are one answer. Where the
		// alternate is on this conversation's own provider the last three are empty
		// and the model is asked of the provider already serving it; where it is on
		// another they travel together, since a crossing needs the endpoint, the
		// adapter that reaches it, and the home it authenticates in or it is a turn
		// sent somewhere it cannot be answered from. And where a crossing would not
		// resolve, all four are empty: a model without the endpoint it belongs to is
		// a selector this conversation's own provider has never heard of.
		FailoverModel:            failover.model,
		FailoverEndpoint:         failover.endpoint,
		FailoverBackend:          failover.backend,
		FailoverAccountConfigDir: failover.configDir,
		// And how long a refusal that named no reset time stands before the
		// configured model is asked again, which is the same interval a run probes
		// one on. Without it the conversation would re-ask an exhausted model every
		// turn and announce the substitution every turn with it.
		UsageLimitUnknownResetPause: cfg.Execution.UsageLimitUnknownResetPause.Duration(),
		// And how long a turn the provider refused may wait for it to serve again,
		// for the operator's own command and for nothing else. They are the bounds a
		// run waits under, taken from the same configuration: an exhausted limit
		// stops a conversation exactly as it stops a run, and an operator who said
		// how long the harness may wait out one said it about every invocation they
		// pay for. The zero value a background turn gets waits for nothing, which
		// is the fail-fast those callers pace themselves on.
		UsageLimitPause: usageLimitPause(cfg, attended),
		Persona:         agent.Persona.Text,
		Remit:           agent.Remit.Text,
		// The lane the instance's tracker writes are confined to, enforced at the
		// act in the conversation's authority table.
		Lane:         agent.Lane,
		Agent:        name,
		Provider:     agent.Backend,
		Providers:    providerRegistry(cfg),
		Repository:   repository,
		ProductID:    cfg.Product.ID,
		RepositoryID: string(cfg.Product.RepositoryID),
		Briefing:     briefing,
		// The repository and the tracker are kept reachable so the conversation
		// can say how old its picture is and take a new one when the operator
		// asks. The product manager reaches neither: this is the harness's hand,
		// like the work it steers.
		Ground: ground,
		// And how far behind the target branch that picture may fall before a
		// turn re-reads it unasked. It is the project's number for when, read from
		// the configuration that refused any value that would mean never.
		RefreshAfterLandings: cfg.Conversation.RefreshAfterLandings,
		RedactValues:         parts.redactValues,
		Fresh:                fresh,
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// usageLimitPause is what a conversation's turn may spend waiting out a provider
// with no capacity for it: the run's own bounds for the operator's attended
// command, and nothing at all for a turn the harness takes for itself.
func usageLimitPause(cfg config.Config, attended bool) chat.UsageLimitPause {
	if !attended {
		return chat.UsageLimitPause{}
	}
	return chat.UsageLimitPause{
		Maximum:   cfg.Execution.UsageLimitMaxPause.Duration(),
		InProcess: cfg.Execution.UsageLimitInProcessPause.Duration(),
	}
}

// conversationAccount picks the provider account one agent's conversation is
// held under, and is the one place that decision is made. Three things are told
// the answer and all three have to be told the same one: the provider, which
// authenticates in that account's own home; the conversation's durable record,
// which says whose subscription is serving it; and every turn's cost line, which
// is what the money is attributed by.
//
// It is the agent's own account rather than a configuration-wide one, because
// under a pool there is no configuration-wide account to name — the answering
// agent's is the only alias that is true of the conversation. It is the same
// resolution the answering half of an exchange makes for the same reason.
func conversationAccount(cfg config.Config, stateRoot, agentName string) (config.AccountEndpoint, error) {
	return cfg.AgentAccountEndpoint(stateRoot, agentName)
}

// conversationAlternate is the whole of what one conversation may be served by
// when the endpoint it is held on has no capacity: the model its turn may ask
// for, and — where that model is on another provider — the endpoint, the adapter
// that reaches it, and the provider home it authenticates in.
//
// The model is here rather than read separately because the two halves are one
// answer. A model without the endpoint that serves it is a selector belonging to
// somewhere else, handed to the provider whose window just closed; a conversation
// given that would meet, at the moment the fallback existed to save its turn, the
// unknown-selector failure the fallback was supposed to prevent. So one function
// answers both, and the zero value is failover off.
type conversationAlternate struct {
	model     string
	endpoint  backend.Endpoint
	backend   backend.Backend
	configDir string
}

// conversationFailover resolves that alternate for one agent.
//
// A failover that will not resolve is a warning rather than a refusal, and that
// is the whole of the choice: what it costs is failover for this conversation,
// and refusing to open it would spend a working provider on a fallback nobody has
// needed yet. What it never costs is a substitution onto the wrong endpoint — a
// crossing that did not resolve leaves this conversation with no alternate at
// all, rather than with the alternate's model and none of the rest of it. The
// warning says so, so an operator who configured a crossing is told it is not one
// rather than finding out at the moment a window closes.
func conversationFailover(cfg config.Config, stateRoot, agentName string, runner execution.ProcessRunner, stderr io.Writer) conversationAlternate {
	agent := cfg.Agents[strings.TrimSpace(agentName)]
	if !agent.Failover.CrossesProviders(agent.Backend) {
		// Another model on the provider this conversation is already on, which needs
		// no second adapter and no second home: the turn is made through the one it
		// was already being made through, and there is nothing to resolve.
		return conversationAlternate{model: cfg.AgentFailoverModel(agentName)}
	}
	providers := providerRegistry(cfg)
	if providers == nil {
		return unresolvedCrossing(stderr, agentName,
			errors.New("this project's declared providers would not build"))
	}
	choice, crosses, err := cfg.AgentFailoverEndpoint(providers, stateRoot, agentName)
	if err != nil {
		return unresolvedCrossing(stderr, agentName, err)
	}
	if !crosses {
		// The block names a provider and has failover switched off, which is the
		// operator keeping a choice rather than making one. Nothing is unresolved and
		// nothing is warned about.
		return conversationAlternate{}
	}
	if choice.Endpoint.Provider == agent.Backend {
		return conversationAlternate{model: choice.Endpoint.Model}
	}
	return conversationAlternate{
		model:     choice.Endpoint.Model,
		endpoint:  choice.Endpoint,
		backend:   providerBackendIn(cfg, choice.Endpoint.Provider, runner, choice.Account.Directory),
		configDir: choice.Account.Directory,
	}
}

// servableAlternate is the agent's alternate on another provider where its own
// executable and login are ready here, and false otherwise. It is asked only
// when the configured provider's executable cannot run, so a conversation that
// opens on it is one whose turns failover will move there.
func servableAlternate(ctx context.Context, cfg config.Config, stateRoot, agentName string, runner execution.ProcessRunner) (conversationAlternate, bool) {
	alternate := conversationFailover(cfg, stateRoot, agentName, runner, io.Discard)
	if alternate.backend == nil {
		return conversationAlternate{}, false
	}
	availability, err := alternate.backend.CheckAvailability(ctx)
	if err != nil || !availability.Installed || !availability.Authenticated {
		return conversationAlternate{}, false
	}
	return alternate, true
}

// unresolvedCrossing is a crossing this conversation cannot make, said out loud
// and then not made at all. Handing back the alternate's model without the
// endpoint it belongs to would ask this conversation's own provider for a model
// that is somewhere else, so what is handed back is nothing.
func unresolvedCrossing(stderr io.Writer, agentName string, reason error) conversationAlternate {
	fmt.Fprintf(stderr,
		"warning: agent %q fails over onto another provider and that endpoint could not be resolved, so this conversation cannot fail over at all: %v\n",
		agentName, reason)
	return conversationAlternate{}
}

// conversationAgent picks the agent a conversation is actually held with, and
// is the one place that decision is made. A named agent is used as named, so an
// operator who picked one of two architects gets that one's persona and model
// rather than whichever the role happens to resolve to; a conversation that
// names none takes the agent filling the role, which is what `yoyo chat` has
// always done. An agent named for one role and configured for another is
// refused rather than quietly answered by the wrong contract, because the role
// decides the authority and the name decides the persona and they must agree.
func conversationAgent(cfg config.Config, role domain.AgentRole, name string) (string, config.AgentConfig, error) {
	if strings.TrimSpace(name) == "" {
		resolved := agentNameForRole(cfg, role)
		if resolved == "" {
			return "", config.AgentConfig{}, fmt.Errorf("no %s agent is configured; there is nobody to talk to", role)
		}
		return resolved, cfg.Agents[resolved], nil
	}
	agent, configured := cfg.Agents[name]
	if !configured {
		return "", config.AgentConfig{}, fmt.Errorf("no agent named %q is configured; `yoyo agent list` names them", name)
	}
	if agent.Role != role {
		return "", config.AgentConfig{}, fmt.Errorf("agent %s fills the %s role, not %s", name, agent.Role, role)
	}
	return name, agent, nil
}

// conversationResearch is the evidence-gathering capability a conversation
// performs on the role's behalf, built from what this project permitted. It is
// always built, including for a project that permitted nothing: the runner then
// reports that there are no sources, which is what the role has to be told so it
// says it could not check rather than answering from memory.
//
// The source commands run in the repository, so a source an operator wrote as a
// script in their own project works the way they expect, and the redact values
// are the same ones every other provider-facing path uses — a source is the one
// thing here that reaches outside the machine, so what must not leave it is
// removed from the question before it does.
func conversationResearch(parts components) research.Runner {
	return research.Runner{
		Process:      parts.runner,
		Directory:    parts.repository,
		Policy:       parts.config.Research.Policy(),
		RedactValues: parts.redactValues,
	}
}

// conversationRepositoryReader is the repository-read capability a conversation
// performs on a management role's behalf. Every path it resolves is read out of
// the tree of one recorded commit in the primary checkout, so the working tree —
// which may hold an operator's uncommitted edit — is never what a role is shown,
// and the redact values are the same ones every other provider-facing path uses.
func conversationRepositoryReader(parts components) repositoryread.Reader {
	return repositoryread.Reader{
		Process:      parts.runner,
		Directory:    parts.repository,
		RedactValues: parts.redactValues,
	}
}

// chatTracker is the work-item client a conversation acts through: it reads the
// tracker state the product context is built from, and it is what an approved
// proposal is created with. Both are bounded the same way, so no tracker call a
// conversation makes can outlast the operator's patience.
func chatTracker(runner execution.ProcessRunner, repository string) beads.Client {
	return beads.Client{Runner: runner, Dir: repository, Timeout: chatTrackerTimeout}
}

// reportChatFailure reports a failed conversation, carrying whatever the turn
// still produced. A reply is nil when the conversation never opened.
func reportChatFailure(stdout, stderr io.Writer, jsonOutput bool, role domain.AgentRole, reply *chat.Reply, err error, wording ...func(string) string) int {
	render := func(text string) string { return text }
	if len(wording) > 0 {
		render = wording[0]
	}
	output := chatOutput{Error: err.Error()}
	if reply != nil {
		evidence := reply.Evidence
		output.Evidence = &evidence
		output.Reply = reply.Text
		output.Proposals = reply.Proposals
		// Work admitted before the turn failed is in the queue, so it travels with
		// the failure for the reason the tracker actions do: it already happened,
		// and nobody was asked about it.
		output.Admitted = reply.Admitted
		// A concern is recorded before the turn goes on to fail, and it is the one
		// thing in the reply that is waiting on a person, so it travels with the
		// failure rather than behind it.
		output.Concerns = reply.Concerns
		// A document recorded before the turn failed is waiting on the operator, so
		// it travels with the failure for the reason the concerns do: nothing was
		// written, and a document nobody can name has to be written out again.
		output.Writes = reply.Writes
		// A turn that failed may still have changed the tracker before it did, so
		// what it changed is reported with the failure rather than lost behind it.
		// The same is true of anything it reported: the report is already
		// collected, and it never had anything to do with the failure.
		output.Actions = reply.Actions
		// Research already happened and an evaluation was already recorded, so both
		// travel with the failure for the same reason the actions do.
		output.Research = reply.Research
		output.RepositoryReads = reply.RepositoryReads
		// The picture's age was measured and recorded before the turn was taken,
		// so it travels with the failure for the same reason.
		output.Picture = reply.Picture
		output.CompactionSaves = reply.CompactionSaves
		// A reply cut in the record is cut whichever way the turn ended.
		output.RecordCuts = reply.RecordCuts
		output.Evaluation = reply.Evaluation
		output.EvaluationProblem = reply.EvaluationProblem
		output.ResultsCarriedOver = reply.ResultsCarriedOver
		output.HandedBack = reply.HandedBack
		output.Reports = reply.Reports
		output.ReportProblem = reply.ReportProblem
	}
	if jsonOutput {
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		return 1
	}
	if output.Reply != "" {
		fmt.Fprintln(stdout, render(output.Reply))
	}
	theme := console.ThemeFor(stdout, os.Getenv)
	printChatHandedBack(stdout, output.HandedBack)
	printChatActions(stdout, role, output.Actions, output.ResultsCarriedOver)
	printChatResearch(stdout, output.Research)
	printChatRepositoryReads(stdout, output.RepositoryReads)
	printChatPicture(stdout, output.Picture)
	printChatCompactionSaves(stdout, output.CompactionSaves)
	printChatEvaluation(stdout, output.Evaluation, output.EvaluationProblem)
	printChatExchanges(stdout, role, output.Exchanges)
	printChatAdmitted(stdout, output.Admitted)
	printChatReports(stdout, theme, role, output.Reports, output.ReportProblem, render)
	printChatConcerns(stdout, theme, role, output.Concerns)
	printChatProposals(stdout, role, output.Proposals)
	printChatWrites(stdout, role, output.Writes)
	fmt.Fprintf(stderr, "chat failed: %v\n", err)
	if output.Evidence != nil && output.Evidence.ConversationID != "" {
		fmt.Fprintf(stderr, "conversation: %s\n", output.Evidence.ConversationID)
	}
	return 1
}

func printChatHeader(writer io.Writer, role domain.AgentRole, evidence chat.Evidence, freshness string) {
	state := "new conversation"
	if evidence.Resumed {
		state = fmt.Sprintf("resumed conversation after %d turn(s)", evidence.Turns)
	}
	fmt.Fprintf(writer, "%s: %s (%s, model %s)\n", chat.RoleTitle(role), evidence.ConversationID, state, evidence.RequestedModel)
	// How old its picture of the product is goes at the top, where an operator
	// cannot miss it, because everything below is what it will say about a
	// repository it may have read hours ago.
	fmt.Fprintln(writer, freshness)
	if role != domain.RoleProductManager {
		printOtherRoleHeader(writer, role)
		return
	}
	fmt.Fprintln(writer, "It owns the backlog: what is admitted to it, and the order work is pulled in.")
	fmt.Fprintln(writer, "It manages the work tracker itself: it can read, create, attribute to a goal,")
	fmt.Fprintln(writer, "update, reparent, reprioritize, link, unlink, close, and retire items, and every")
	fmt.Fprintln(writer, "change it makes is reported to you here. It has no files, commands, or network.")
	fmt.Fprintln(writer, "The brief and the goals are its documents to write: it hands you one to read and")
	fmt.Fprintln(writer, "approve, and the harness files it with your approval recorded in it. A design")
	fmt.Fprintln(writer, "or a decision record is the architect's, and it proposes a change there instead.")
	fmt.Fprintln(writer, "It may also propose work items; one is created only when you approve it by name,")
	fmt.Fprintln(writer, "and every one of them names a goal your goals state, checked rather than taken.")
	fmt.Fprintln(writer, "Work admitted before that check names none, and it says which items those are.")
	fmt.Fprintln(writer, "Several proposals are decided in one answer: approve 1,3 and decline 2 <reason>.")
	fmt.Fprintln(writer, "Work it cannot place under a goal, work it says would cut against one, and work")
	fmt.Fprintln(writer, "it judges to be against the product's intent are not proposed at all: it stops")
	fmt.Fprintln(writer, "and asks you instead.")
	fmt.Fprintln(writer, "Any agent can report something without it stopping their work; /reports")
	fmt.Fprintln(writer, "shows you what has been collected, and `yoyo reports` shows the same pile")
	fmt.Fprintln(writer, "without a conversation.")
	fmt.Fprintln(writer, "Changes other roles have proposed to the brief and the goals are carried into")
	fmt.Fprintln(writer, "this conversation for it to argue; you decide them with `yoyo amendment`.")
	fmt.Fprintln(writer, "Its picture of the repository and the tracker is the one gathered above; /refresh")
	fmt.Fprintln(writer, "reads them again into this conversation without discarding what has been said.")
	fmt.Fprintln(writer, "You steer the work yourself: /backlog, /status, /work, /stop, /redirect. /show")
	fmt.Fprintln(writer, "reads one item in full and /diff says what a run changed, both without leaving")
	fmt.Fprintln(writer, "this conversation. /help lists them.")
	fmt.Fprintln(writer, "End with /exit.")
	fmt.Fprintln(writer)
}

// printOtherRoleHeader opens a conversation with a role that is not the product
// manager. It says three things an operator needs before they spend a turn: what
// this role decides, what it may do from here, and where the thing they probably
// want next actually happens — because a conversation that lets somebody talk to
// the architect for ten minutes before they discover it cannot write the design
// has wasted their afternoon.
func printOtherRoleHeader(writer io.Writer, role domain.AgentRole) {
	authority, known := chat.AuthorityFor(role)
	if !known {
		fmt.Fprintf(writer, "The harness holds no conversation contract for %s.\n\n", role)
		return
	}
	fmt.Fprintf(writer, "It owns %s.\n", authority.Owns)
	fmt.Fprintln(writer, "It has no files, commands, or network, and it can read the work tracker but")
	switch {
	case len(authority.TrackerActions) > 2:
		fmt.Fprintln(writer, "may only build structure underneath work the Lead Product Manager has admitted:")
		fmt.Fprintln(writer, "it decomposes, links, and reparents, and it cannot admit work, reorder the")
		fmt.Fprintln(writer, "backlog, close an item, or retire one. Every change it makes is reported here.")
	default:
		fmt.Fprintln(writer, "changes nothing in it: it reads items and surveys the queue, and that is all.")
	}
	switch role {
	case domain.RoleArchitect:
		fmt.Fprintln(writer, "A design, a specification, or a decision record it writes here is put to you")
		fmt.Fprintln(writer, "as a document to approve, and the harness writes it under the architect's")
		fmt.Fprintln(writer, "authority with your approval recorded in it. An invariant is still yours to")
		fmt.Fprintln(writer, "record with `yoyo invariant`. Changes other roles proposed to its documents")
		fmt.Fprintln(writer, "are carried into this conversation for it to argue; you decide them with")
		fmt.Fprintln(writer, "`yoyo amendment`.")
	case domain.RoleDeveloper, domain.RoleReviewer:
		fmt.Fprintln(writer, "Its real work happens inside runs, with a worktree, checks, and a verdict, and")
		fmt.Fprintln(writer, "none of that is happening here. What you get here is its judgement.")
	}
	fmt.Fprintln(writer, "Anything it reports reaches `yoyo reports`, and `/reports` shows the same pile.")
	fmt.Fprintln(writer, "Its picture of the repository is the one gathered above; /refresh takes a new one.")
	fmt.Fprintln(writer, "The operator commands are the same ones `yoyo chat` has: /help lists them.")
	fmt.Fprintln(writer, "End with /exit.")
	fmt.Fprintln(writer)
}

// printChatActions reports what the role changed in the tracker while it
// answered. It is printed for a one-shot message as well as a conversation: the
// changes are already made, and a caller who is not told about them is reading a
// queue that moved without them.
// printChatHandedBack says which tracker blocks were refused and handed back
// within the reply, ahead of the actions, so the actions read as the re-issue.
func printChatHandedBack(writer io.Writer, refusals []string) {
	for _, refusal := range refusals {
		fmt.Fprintf(writer, "\n%s\nNothing in that block was carried out; the harness handed the refusal back within this message so it could re-issue the actions.\n", refusal)
	}
}

func printChatActions(writer io.Writer, role domain.AgentRole, actions []chat.TrackerOutcome, resultsCarriedOver bool) {
	if len(actions) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe %s acted on the tracker (%d action(s)):\n", chat.RoleTitle(role), len(actions))
	for _, action := range actions {
		fmt.Fprint(writer, action.Render())
	}
	// A reply that ran out of rounds stopped for a reason nobody can see in the
	// text, so it is said here rather than left to look like a finished thought.
	if resultsCarriedOver {
		fmt.Fprintln(writer, "It ran out of rounds of actions; what the last ones returned is recorded with")
		fmt.Fprintln(writer, "the conversation and reaches it the next time you say something to it.")
	}
}

// printChatExchanges reports what one role asked another while it answered. It
// is printed wherever the actions are, and for the same reason with one added:
// the exchange already happened and was already paid for, and a question put to
// another agent that the operator is not told about is the side conversation
// this channel exists not to be. The whole thread is durable; this is the line
// that says to go and read it.
func printChatExchanges(writer io.Writer, role domain.AgentRole, exchanges []chat.ExchangeRound) {
	if len(exchanges) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe %s asked another role (%d round(s)):\n", chat.RoleTitle(role), len(exchanges))
	for _, round := range exchanges {
		fmt.Fprintf(writer, "  asked the %s: %s\n", chat.RoleTitle(round.Asked), round.Question)
		if round.ID != "" {
			fmt.Fprintf(writer, "    %s, %s, round %d of %d, $%.4f\n", round.ID, round.State, round.Round, round.Rounds, round.CostUSD)
		}
		if round.Settled != "" {
			fmt.Fprintf(writer, "    settled: %s\n", round.Settled)
		}
		if round.Problem != "" {
			fmt.Fprintf(writer, "    unanswered: %s\n", round.Problem)
		}
	}
	fmt.Fprintln(writer, "  `yoyo exchange show <id>` is the whole of what was said.")
}

// printChatResearch names what the harness went and looked up while the reply
// was being written. It lists the questions and what each one returned in size
// and provenance rather than reprinting the evidence: the answer is in the reply
// above, and a page of retrieved text under it would bury the answer in its own
// sources.
func printChatResearch(writer io.Writer, rounds []chat.ResearchRound) {
	if len(rounds) == 0 {
		return
	}
	fmt.Fprintln(writer)
	for _, round := range rounds {
		fmt.Fprint(writer, round.Render())
	}
}

// printChatRepositoryReads names what the harness read from the repository and
// at which commit, one line per path. The content is in the reply above it.
func printChatRepositoryReads(writer io.Writer, rounds []chat.RepositoryRound) {
	if len(rounds) == 0 {
		return
	}
	fmt.Fprintln(writer)
	for _, round := range rounds {
		fmt.Fprint(writer, round.Render())
	}
}

// printChatPicture says what the harness did about the age of the picture the
// reply was answered from, where it did anything: a current picture prints
// nothing, for the reason the render prints nothing.
func printChatPicture(writer io.Writer, picture *chat.PictureAge) {
	if picture == nil {
		return
	}
	rendered := picture.Render()
	if rendered == "" {
		return
	}
	fmt.Fprintln(writer)
	fmt.Fprint(writer, rendered)
}

// printChatRecordCuts says that the record holds only part of the reply
// printed above, which the reader would otherwise take it to hold whole.
func printChatRecordCuts(writer io.Writer, cuts []execution.ReplyCut) {
	if rendered := chat.RenderRecordCuts(cuts); rendered != "" {
		fmt.Fprintln(writer)
		fmt.Fprint(writer, rendered)
	}
}

func printChatCompactionSaves(writer io.Writer, saves []chat.CompactionSave) {
	if len(saves) == 0 {
		return
	}
	fmt.Fprintln(writer)
	for _, save := range saves {
		fmt.Fprint(writer, save.Render())
	}
}

// printChatEvaluation names the recommendation that went into the record, and
// says in the same breath that it changed nothing. The second half is the part
// that matters: an evaluation is the one durable thing a conversation writes that
// decides nothing, and "recorded" read as "settled" is a decision the operator
// never made.
func printChatEvaluation(writer io.Writer, recorded *evaluation.Evaluation, problem string) {
	if recorded != nil {
		fmt.Fprintf(writer, "\nEvaluation recorded: %s — %s\n", recorded.Entry.Recommendation, recorded.Entry.Recommendation.Headline())
		fmt.Fprintf(writer, "  [%s] %s\n", recorded.ID, recorded.Entry.Idea)
		fmt.Fprintln(writer, "  advice only: nothing was admitted, approved, or changed by recording it")
		fmt.Fprintf(writer, "  `yoyo evaluation show %s` has the reasoning and the sources\n", recorded.ID)
	}
	if problem != "" {
		fmt.Fprintf(writer, "\nAn evaluation could not be kept: %s\n", problem)
	}
}

// printChatReports names what the product manager reported for the operator
// while it answered. It is printed for a one-shot message as well as a
// conversation: the report is already collected, and one that is only in the
// pile is one nobody has been told about yet.
func printChatReports(writer io.Writer, theme console.Theme, role domain.AgentRole, reports []report.Report, problem string, render func(string) string) {
	if len(reports) == 0 && problem == "" {
		return
	}
	if len(reports) > 0 {
		fmt.Fprintf(writer, "\nThe %s reported %d thing(s) for you:\n", chat.RoleTitle(role), len(reports))
		for _, reported := range reports {
			fmt.Fprint(writer, theme.Severity(console.Severity(reported.Severity), render(reported.Render())))
		}
	}
	if problem != "" {
		fmt.Fprintf(writer, "\na report was not collected: %s\n", problem)
	}
}

// printChatAdmitted reports what a one-shot message put in the queue without
// asking anybody. Unlike a proposal it is not something to decide, and that is
// exactly why it is printed: this is the one moment the operator is told, and a
// message that admitted work and said nothing about it would leave the queue
// having moved with no account of it anywhere they look.
func printChatAdmitted(writer io.Writer, admitted []chat.AdmittedItem) {
	if len(admitted) == 0 {
		return
	}
	fmt.Fprintf(writer, "\n%d work item(s) were admitted to the queue without asking you, and each one says why:\n\n", len(admitted))
	for _, item := range admitted {
		fmt.Fprint(writer, item.Render())
	}
}

// printChatProposals reports what is awaiting the operator's decision, and says
// how to make one. Nothing here was created, and the proposals are named by
// their own identifiers rather than numbered: a number is a position in a
// listing, and the listing an operator sees next is whatever their next command
// prints, while the identifier is the same word in every invocation. A decision
// sent as its own message has to name something that survives between the two.
func printChatProposals(writer io.Writer, role domain.AgentRole, proposals []chat.PendingProposal) {
	if len(proposals) == 0 {
		return
	}
	fmt.Fprintf(writer, "\n%d proposal(s) from the %s are awaiting your decision, and nothing was created for them:\n\n", len(proposals), chat.RoleTitle(role))
	for _, proposal := range proposals {
		fmt.Fprint(writer, proposal.Render())
	}
	first := proposals[0].ID
	fmt.Fprintf(writer, "\nDecide one from here: `yoyo chat --message \"approve %s\"` creates it, and\n`yoyo chat --message \"decline %s <reason>\"` turns it down. `yoyo chat` puts them\nto you as a prompt instead.\n", first, first)
}

// printChatWrites reports the documents awaiting the operator's decision, and
// says how to make one. Nothing was written for any of them: the document is in
// the conversation's record and the repository is untouched until an approval
// names it, which is why the identifier is printed rather than the position.
func printChatWrites(writer io.Writer, role domain.AgentRole, writes []chat.PendingWrite) {
	if len(writes) == 0 {
		return
	}
	fmt.Fprintf(writer, "\n%d document(s) the %s wrote are awaiting your decision, and nothing was written to the repository:\n\n", len(writes), chat.RoleTitle(role))
	for _, write := range writes {
		fmt.Fprintf(writer, "  [%s] %s\n", write.ID, write.Write.Describe())
		fmt.Fprintf(writer, "      because: %s\n", strings.TrimSpace(write.Write.Reason))
	}
	first := writes[0].ID
	fmt.Fprintf(writer, "\nDecide one from here: `yoyo chat --message \"approve %s\"` writes it and records\nyour approval in it, and `yoyo chat --message \"decline %s <reason>\"` turns it\ndown. `yoyo chat` shows you the document and asks instead.\n", first, first)
}

// printChatWriteDecisions says what the operator's message did to each document
// it named. Nothing here was said by the agent: the harness wrote the file, or
// did not, and which of the two happened is the whole of what has to be clear.
func printChatWriteDecisions(writer io.Writer, outcomes []chat.WriteOutcome) {
	if len(outcomes) == 0 {
		return
	}
	fmt.Fprintln(writer)
	for _, outcome := range outcomes {
		fmt.Fprint(writer, outcome.Render())
	}
}

// printUndecidedWrites names the documents a conversation ended without
// deciding, so a drafted document nobody filed is a visible loose end rather
// than something that has to be written out again.
func printUndecidedWrites(writer io.Writer, theme console.Theme, writes []chat.PendingWrite) {
	if len(writes) == 0 {
		return
	}
	var undecided strings.Builder
	fmt.Fprintf(&undecided, "%d document(s) were left undecided and nothing was written for them:\n", len(writes))
	for _, write := range writes {
		fmt.Fprintf(&undecided, "  [%s] %s\n", write.ID, write.Write.Describe())
	}
	fmt.Fprint(writer, theme.Proposal(undecided.String()))
}

// printChatConcerns reports what the product manager would not propose until it
// is answered, and says how to answer from here. There is nobody at a prompt in
// a one-shot message, so the questions are printed with what they are — raised,
// unanswered, and holding work that was never proposed — and each is named by
// its own identifier, for the reason a proposal is: an answer sent as its own
// message has to name something that survives between the two invocations.
func printChatConcerns(writer io.Writer, theme console.Theme, role domain.AgentRole, concerns []chat.PendingConcern) {
	if len(concerns) == 0 {
		return
	}
	fmt.Fprintf(writer, "\nThe %s will not propose %d thing(s) until it is answered. Nothing was proposed or created for them:\n\n", chat.RoleTitle(role), len(concerns))
	for _, concern := range concerns {
		fmt.Fprint(writer, concern.Render(theme))
	}
	first := concerns[0].ID
	fmt.Fprintf(writer, "\nAnswer one from here: `yoyo chat --message \"answer %s <what you decide>\"`, with the\nnumber of an answer on offer or your own words. `yoyo chat` puts them to you as a\nprompt instead.\n", first)
}

// printOpenConcerns names the questions a conversation ended without answering,
// so one nobody answered is a visible loose end rather than silence that reads
// as agreement.
// Each one is marked and dressed by what its kind asks for rather than the
// whole list being dressed as questions, because this is the listing where a
// conversation's loose ends are counted together: they are all unanswered, and
// which of them says the work would cut against a goal is the thing a count
// cannot say.
func printOpenConcerns(writer io.Writer, theme console.Theme, concerns []chat.PendingConcern) {
	if len(concerns) == 0 {
		return
	}
	fmt.Fprintf(writer, "%d question(s) from the Lead Product Manager were left unanswered, and the work behind them was never proposed:\n", len(concerns))
	for _, concern := range concerns {
		severity := concern.Concern.Kind.Severity()
		var one strings.Builder
		// The question itself is printed rather than only named, because what an
		// operator has to come back to is what was asked and not that something
		// was.
		fmt.Fprintf(&one, "  %-*s [%s] %s: %s\n", report.MarkerWidth, severity.Marker(),
			concern.ID, concern.Concern.Kind.Headline(), concern.Concern.Subject)
		fmt.Fprintf(&one, "      %s\n", strings.TrimSpace(concern.Concern.Question))
		fmt.Fprint(writer, theme.Severity(console.Severity(severity), theme.Questions(one.String())))
	}
}

// printUndecidedProposals names what a conversation left open, so a proposal
// nobody decided on ends as a visible loose end rather than as silence. It is
// dressed as what it is — something still waiting on the operator — and the
// text says so without the colour.
func printUndecidedProposals(writer io.Writer, theme console.Theme, proposals []chat.PendingProposal) {
	if len(proposals) == 0 {
		return
	}
	var undecided strings.Builder
	fmt.Fprintf(&undecided, "%d proposal(s) were left undecided and nothing was created for them:\n", len(proposals))
	for _, proposal := range proposals {
		fmt.Fprintf(&undecided, "  [%s] %s\n", proposal.ID, proposal.Proposal.Title)
	}
	fmt.Fprint(writer, theme.Proposal(undecided.String()))
}

func printChatEvidence(writer io.Writer, evidence chat.Evidence) {
	fmt.Fprintf(writer, "conversation: %s\n", evidence.ConversationID)
	fmt.Fprintf(writer, "model: %s\n", renderChatModel(evidence))
	if evidence.SessionID != "" {
		fmt.Fprintf(writer, "provider session: %s\n", evidence.SessionID)
	}
	// How close that session is to being compacted, where it has been measured.
	if evidence.SessionBudgetBytes > 0 {
		fmt.Fprintf(writer, "provider session size: %d of %d bytes before compaction\n", evidence.SessionBytes, evidence.SessionBudgetBytes)
	}
	fmt.Fprintf(writer, "turns: %d\n", evidence.Turns)
}

// renderChatModel reports the requested selector alongside what the provider
// resolved it to, because a floating alias only becomes evidence once the
// served model is named.
//
// A turn the permitted alternate served says so first, because the configured
// selector alone would read as a conversation being held on a model that in fact
// refused it. What the operator is told is the model that answered and the model
// it stood in for, in that order.
func renderChatModel(evidence chat.Evidence) string {
	requested := evidence.RequestedModel
	if evidence.ServedModel != "" {
		requested = evidence.ServedModel + " (failed over from " + evidence.RequestedModel + ")"
	}
	if evidence.ResolvedModel == "" || evidence.ResolvedModel == evidence.RequestedModel || evidence.ResolvedModel == evidence.ServedModel {
		return requested
	}
	return requested + " (resolved: " + evidence.ResolvedModel + ")"
}

func printChatUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo chat [options]

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --message <text>   send one message and print the reply instead of conversing
  --new              start a new conversation instead of resuming the recorded one
  --json             emit machine-readable JSON (requires --message)
  --side-thread <id> continue the named side thread with --message

A conversation also carries out operator commands: /backlog, /status, /show,
/diff, /reports, /refresh, /work, /wait, /stop, and /redirect. Ask it for /help
once it is open.

A --message that begins with a slash is carried out as one of those commands
rather than said to the Lead Product Manager. The commands that only mean
something inside a conversation — /work, /wait, /stop, and /exit (alias /quit) —
are refused there and say what to reach for instead.

A --message that decides a proposal this conversation is waiting on is carried
out here too, and no turn is spent on it. Two shapes decide: one that names the
proposal, as "approve 3.1" or "decline 3.1 <reason>", and one that is nothing but
decision words, as "y", "no", or "approve 1,3". Everything else is said to the
Lead Product Manager as it always was and leaves every proposal where it was —
including a reply that opens with one of those words, because "no, let us look at
the resolver instead" is a sentence rather than a decline.

A --message that answers a question the Lead Product Manager stopped on is
carried out the same way: "answer c3.1 <what you decide>" answers concern c3.1
with your words or the number of an answer it offered, and a bare "yes" or "no"
answers it where it is the only thing waiting. With a question and a proposal
both waiting, a message that names neither is refused with the list rather than
applied to either, so an answer meant for the question never approves the
proposal.

A --message that finds the conversation mid-turn waits for it, unless the agent is
configured with "conversations: side-threads": then it is answered beside the busy
turn on a side thread of its own, and the answer says so. A side thread judges and
answers and takes no action, so anything it promises is tentative until the main
conversation's next turn — which reads what the side thread concluded — ratifies
it. A side thread the agent left open for a further turn is continued with
--side-thread <id> and --message, by the agent that holds it; commands and
decisions always reach the main conversation.

A --message that decides a document the conversation wrote is carried out here as
well, and it has to name one: "approve document-4.1" writes the document and
records your approval in it, and "decline document-4.1 <reason>" turns it down.
A bare "y" decides no document, because a message is not an answer to a question
you were just asked.

This is the Lead Product Manager's conversation. Every other configured agent is
reached the same way through "yoyo agent chat <name>", which takes the same
options; "yoyo agent list" says who there is and what each one is in the middle
of.`)
}

// artifactHomes reads the artifact homes a run may not write into and the
// documents they own, for the done-condition check every admission makes. The
// homes come from the configuration and never fail; the documents come from the
// repository, and a set that could not be read costs the check by name rather
// than the conversation — a condition naming a design by its path is still
// refused, and the warning says what is not.
func artifactHomes(repository string, cfg config.Config, stderr io.Writer) protectedpath.Homes {
	documents, err := protectedpath.OwnedDocuments(repository, cfg.Product)
	if err != nil {
		fmt.Fprintf(stderr, "warning: the documents the artifact homes own could not be read, so a done-condition naming one by its name rather than its path is not refused at admission: %v\n", err)
	}
	return protectedpath.ArtifactHomes(cfg, documents...)
}
