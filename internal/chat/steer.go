package chat

// The operator steers development from inside the conversation. Everything here
// runs on the operator's own instruction and never on the product manager's:
// whatever authority it has over what the queue says, it cannot reach the Work
// behind these methods, and it learns what happened the same way it learns
// anything else, as evidence carried into its next turn.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// commandHelp is what the conversation understands besides talking. It is
// deliberately short: these are the verbs the product loop needs, and anything
// else is still a sentence to the product manager.
const commandHelp = `Commands the harness carries out for you:
  /status                     what is in flight, claimed, blocked, available, and done, with what the done work cost
  /backlog                    the admitted work in the Lead Product Manager's order, and what is next
  /show <beads-id>            one bounded work item read, as the tracker holds it, and what each run for it cost
  /diff [beads-id]            what a run changed, from the run's own record
  /reports                    what agents have reported without it stopping their work
  /refresh                    re-read the repository and tracker into this conversation
  /work <beads-id>            run one work item now, while you keep talking
  /wait                       wait for the run this conversation started and report it
  /stop [beads-id] [reason]   stop one item's run wherever it is running, and settle what it left
  /hold [reason]              stop the harness starting anything more on its own; running work carries on
  /release                    let the harness start work on its own again
  /intake                     whether the harness may start work on its own, and why not
  /stop-everything [reason]   hold intake and stop every run in flight, settling what each left
  /redirect <beads-id> <what to do differently>
                              record direction on an item, stopping it first if it is running
  /directives                 what you have directed, and what still applies
  /directive <what you have decided>
                              record a directive that takes effect now
  /directive ambiguous <what is unresolved> | <what you said>
                              record one you have not settled; it pauses the work it affects
  /directive artifact <artifact> <what is unresolved> | <what changes>
                              record one that changes a governed artifact; it pauses too
  /resolve <directive-id> <how it was settled>
                              settle a directive and let the work it paused carry on
  /withdraw <directive-id> <why you no longer mean it>
                              take a directive out of force, keeping what it said
  /help                       this list
  /exit                       end the conversation, stopping anything it is running

Anything that does not begin with a slash is said to the agent you are talking to.
`

// IsCommand reports whether what the operator said is a command the harness
// carries out rather than something said to the product manager. It is one rule
// in one place because a single message follows it too: `/reports` typed at
// `yoyo chat --message` is the operator asking the harness for something, and
// passing it to the product manager would spend a turn asking it to carry out
// a command it has no way to reach.
func IsCommand(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "/")
}

// splitCommand separates what the operator typed into the command's name and
// its argument. It is the one place that rule lives, because the refusal a
// single message makes and the dispatcher that carries the command out have to
// agree about what was typed, and two copies of a parsing rule agree only until
// somebody changes one of them.
//
// It splits on the first whitespace rather than on a space, so `/stop` followed
// by a tab names the item it plainly names instead of arriving as a command
// nobody recognizes, and it lowercases the name, so a command means the same
// thing however it was capitalized.
func splitCommand(line string) (name, argument string) {
	trimmed := strings.TrimSpace(line)
	cut := strings.IndexFunc(trimmed, unicode.IsSpace)
	if cut < 0 {
		return strings.ToLower(trimmed), ""
	}
	return strings.ToLower(trimmed[:cut]), strings.TrimSpace(trimmed[cut:])
}

// singleMessageRule is what one command means outside a conversation.
type singleMessageRule struct {
	// why is the reason the command needs a conversation, and what does the same
	// job from a command line. It is empty for a command that answers a single
	// message exactly as it answers a conversation, which is every command that
	// is read-only or durable.
	why string
	// onlyBare marks a refusal that is about the bare form of the command alone,
	// so an argument makes it mean the same thing in a single message as it
	// means in a conversation.
	onlyBare bool
}

// singleMessage records, for every command the conversation dispatches, what it
// means in a single message. It is a table of decisions rather than a list of
// refusals because a list of refusals is a decision nobody makes: a command
// added to the dispatcher would be permitted at `yoyo chat --message` by
// nothing but nobody having thought about it. Every command below is a decision
// somebody made, and TestEveryDispatchedCommandRecordsWhatASingleMessageMeans
// reads the dispatcher's own switch and fails on a command missing from here.
var singleMessage = map[string]singleMessageRule{
	"/status":          {},
	"/backlog":         {},
	"/show":            {},
	"/diff":            {},
	"/reports":         {},
	"/refresh":         {},
	"/help":            {},
	"/hold":            {},
	"/release":         {},
	"/intake":          {},
	"/stop-everything": {},
	"/redirect":        {},
	"/directives":      {},
	"/directive":       {},
	"/resolve":         {},
	"/withdraw":        {},
	"/exit":            {why: "ends a conversation, and a single message is not one"},
	"/quit":            {why: "ends a conversation, and a single message is not one"},
	"/work":            {why: "runs a work item in the background of the conversation that started it, and this process exits before such a run could finish; `yoyo run <beads-id>` runs one from a command line"},
	"/wait":            {why: "acts on the run the conversation started, and a single message never started one"},
	// Stopping a named item reads durable state and asks whichever process is
	// working on it, so it means exactly the same thing in a single message as it
	// does in a conversation. Only the bare form is about a run this process
	// started, and a single message never started one.
	"/stop": {
		why:      "with no work item acts on the run the conversation started, and a single message never started one; name the item to stop, as /stop <beads-id>",
		onlyBare: true,
	},
}

// Command carries out one operator command outside the interactive loop, which
// is what a single message that turns out to be a command needs. It is the same
// table the conversation dispatches, minus the commands that only mean anything
// inside one: a message is answered and the process exits, so a command that
// starts or acts on a run this process owns is refused with what to reach for
// instead rather than half-carried-out.
func (s *Session) Command(ctx context.Context, line string, out io.Writer) error {
	name, argument := splitCommand(line)
	if why := needsConversation(name, argument); why != "" {
		return fmt.Errorf("%s %s", name, why)
	}
	// Only /exit and /quit end the loop, and both are refused above, so there is
	// nothing here for the exit to mean.
	_, err := s.command(ctx, line, out)
	return err
}

// needsConversation says why a command means nothing in a single message, and
// what does the same job from a command line. It reads the decision recorded
// against the command rather than deciding anything itself, so a command with
// no decision recorded cannot be permitted here by omission.
func needsConversation(name, argument string) string {
	rule := singleMessage[name]
	if rule.why == "" {
		return ""
	}
	if rule.onlyBare && argument != "" {
		return ""
	}
	return rule.why
}

// command runs one operator command. Commands are executed here rather than
// sent to the product manager: what the harness does is the operator's
// decision, and the product manager is told about it afterwards like any other
// evidence.
func (s *Session) command(ctx context.Context, line string, out io.Writer) (bool, error) {
	// A run that finished while the operator was typing is collected before the
	// command acts, so a command never decides anything about a run that is
	// already over — stopping something finished, or reporting it as in flight.
	// What it crossed on the way is said first, because collecting the run is
	// what forgets there was one.
	s.reportMilestones(out)
	s.reportFinishedWork(out)
	// The line is read the way Session.Command reads it, so what a single
	// message refuses and what the conversation carries out are decisions about
	// the same command rather than about two readings of one line.
	name, argument := splitCommand(line)
	switch name {
	case "/exit", "/quit":
		return true, nil
	case "/help":
		fmt.Fprint(out, commandHelp)
		// How a message of more than one line is typed depends on what this
		// console turned out to support, so it is said here rather than written
		// into the list above, which is the same on every terminal there is.
		if s.composing != "" {
			fmt.Fprintln(out, s.composing)
		}
		fmt.Fprintln(out)
		return false, nil
	case "/status":
		// A paused harness leads, before anything about the work: every group
		// below is describing a queue that is not moving, and why it is not moving
		// is the first thing the operator has to be told.
		fmt.Fprint(out, s.operatorHoldBanner())
		// A held intake leads for the same reason, and after the pause because it is
		// the narrower fact: a harness that is paused is not choosing work either,
		// and saying so twice at the top would bury the difference between them.
		fmt.Fprint(out, s.intakeBanner())
		survey, err := s.SurveyWork(ctx)
		if err != nil {
			return false, err
		}
		if item, startedAt, ok := s.RunningWork(); ok {
			fmt.Fprintf(out, "this conversation started work on %s at %s and is still waiting for it.\n",
				item, startedAt.UTC().Format(time.RFC3339))
		}
		// The survey dresses itself: what is running, blocked, done, and failed
		// gets the colour that state has everywhere, which is a distinction the
		// group headings already make in words.
		fmt.Fprint(out, survey.Render(s.theme))
		fmt.Fprintln(out)
		return false, nil
	case "/refresh":
		// A refresh that was taken and could not be recorded still happened, so
		// it is reported before the failure rather than behind it.
		refreshed, err := s.Refresh(ctx)
		fmt.Fprint(out, refreshed.Render())
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out)
		return false, nil
	case "/backlog":
		queue, err := s.ReadBacklog(ctx)
		if err != nil {
			return false, err
		}
		fmt.Fprint(out, queue.Render())
		fmt.Fprintln(out)
		return false, nil
	case "/show":
		if argument == "" {
			return false, errors.New("name the work item to show, as /show <beads-id>; /status and /backlog list what there is")
		}
		item, err := s.ShowWorkItem(ctx, argument)
		if err != nil {
			return false, err
		}
		// It is the same rendering the product manager is given when it reads an
		// item, so what the operator sees here is what the agent could see.
		fmt.Fprint(out, renderWorkItemEvidence(item, s.options.Goals))
		// What it cost comes from the run records rather than the tracker, and it
		// is broken down by attempt: a single total answers what an item cost, and
		// only the breakdown says what the harness spent it on. A price nobody
		// could read never withholds the item, which is what was asked for. The
		// runs section a role's read carries comes from the same reading.
		price, err := s.WorkItemPrice(ctx, argument)
		fmt.Fprint(out, s.renderItemRunsFrom(ctx, item.ID, price, err))
		printItemPrice(out, price, err)
		fmt.Fprintln(out)
		return false, nil
	case "/diff":
		changes, err := s.RunChanges(ctx, argument)
		if err != nil {
			return false, err
		}
		fmt.Fprint(out, changes.Render())
		fmt.Fprintln(out)
		return false, nil
	case "/reports":
		reports, handled, err := s.ReadReports()
		// A pile that could not be read at all is the answer failing. A pile that
		// was read while what became of it was not is the annotation failing, and
		// the reports are still what the operator asked for: they are printed,
		// with the gap named rather than shown as nothing having been handled.
		if err != nil && reports == nil {
			return false, err
		}
		if err != nil {
			fmt.Fprintf(out, "none of these is shown as handled: %v\n", err)
		}
		fmt.Fprint(out, renderCollectedReports(s.theme, reports, handled, s.buildGauge(), s.workItemTitles(), s.options.clock().Now(), readmodel.ReadTextTerms(s.options.Repository)))
		fmt.Fprintln(out)
		return false, nil
	case "/work":
		if argument == "" {
			return false, errors.New("name the work item to run, as /work <beads-id>; /status lists what is available")
		}
		if err := s.StartWork(ctx, argument); err != nil {
			return false, err
		}
		fmt.Fprintf(out, "started work on %s. It runs while you keep talking; /status says how it is going, /wait waits for it, and on a terminal it reports itself the moment it finishes.\n\n", argument)
		return false, nil
	case "/wait":
		finished, err := s.WaitForWork(ctx)
		if finished == nil {
			return false, err
		}
		// The run ended, so it is reported even when recording that it did
		// failed; the failure travels with it rather than replacing it.
		s.printFinishedRun(out, finished, err)
		return false, nil
	case "/stop":
		// The first word is the item when it names something in flight, and part of
		// the reason otherwise, so `/stop` and `/stop because it is looping` both
		// still mean the run this conversation started.
		id, reason := s.readStopArgument(ctx, argument)
		stopped, err := s.StopWork(ctx, id, reason)
		fmt.Fprint(out, stopped.Render())
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out)
		return false, nil
	case "/stop-everything":
		// What was achieved is printed before any failure, because holding intake
		// and stopping four of five runs is most of what was asked for: reporting it
		// as a failed command would send the operator to do it all again.
		stopped, err := s.StopEverything(ctx, argument)
		fmt.Fprint(out, stopped.Render())
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out)
		return false, nil
	case "/hold":
		// A hold is durable the moment it is placed, so what it achieved is printed
		// before any failure that followed, for the same reason a directive's is.
		intake, err := s.HoldIntake(argument)
		fmt.Fprint(out, intake.Render())
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out)
		return false, nil
	case "/release":
		intake, err := s.ReleaseIntake()
		fmt.Fprint(out, intake.Render())
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out)
		return false, nil
	case "/intake":
		intake, err := s.ReadIntake()
		if err != nil {
			return false, err
		}
		fmt.Fprint(out, intake.Render())
		fmt.Fprintln(out)
		return false, nil
	case "/redirect":
		id, direction, _ := strings.Cut(argument, " ")
		directed, err := s.DirectWork(ctx, id, direction)
		fmt.Fprint(out, directed.Render())
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out)
		return false, nil
	case "/directives":
		recorded, err := s.ReadDirectives(ctx)
		if err != nil {
			return false, err
		}
		fmt.Fprint(out, renderDirectives(recorded))
		fmt.Fprintln(out)
		return false, nil
	case "/directive":
		request, err := parseDirectiveCommand(argument)
		if err != nil {
			return false, err
		}
		// A directive that pauses work is durable and in force the moment it is
		// recorded, so what recording it achieved is printed before any failure
		// that followed: a pause reported as a failed command would send the
		// operator to record it a second time.
		recorded, err := s.RecordDirective(ctx, request)
		fmt.Fprint(out, recorded.Render())
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out)
		return false, nil
	case "/resolve":
		id, resolution, _ := strings.Cut(argument, " ")
		resolved, err := s.ResolveDirective(ctx, id, resolution)
		fmt.Fprint(out, resolved.Render())
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out)
		return false, nil
	case "/withdraw":
		id, reason, _ := strings.Cut(argument, " ")
		// A withdrawal is durable the moment it is written, so what it achieved is
		// printed before any failure that followed, for the same reason recording a
		// directive is: an operator told their withdrawal failed would go and make
		// it a second time, against a directive that is already out of force.
		withdrawn, err := s.WithdrawDirective(ctx, id, reason)
		fmt.Fprint(out, withdrawn.Render())
		if err != nil {
			return false, err
		}
		fmt.Fprintln(out)
		return false, nil
	default:
		return false, fmt.Errorf("%s is not a command; /help lists what this conversation understands", name)
	}
}

// readStopArgument separates the item a stop names from the reason for it.
//
// It has to be decided rather than parsed, because a Beads identifier is a word
// and so is the first word of a sentence: "enough" is a syntactically perfect
// identifier, so shape alone cannot tell `/stop enough` — a reason for the run
// this conversation started — from `/stop yoyodyne-ifd.26`, an item somewhere
// else. What separates them is the world rather than the grammar, so the first
// word is taken as an item when it names something the harness actually has in
// flight, and as prose otherwise.
//
// A survey that cannot be read decides nothing here. The reading falls through
// to the same answer it would have reached without it, and whichever path the
// stop then takes reports its own failure rather than this inventing one.
func (s *Session) readStopArgument(ctx context.Context, argument string) (string, string) {
	trimmed := strings.TrimSpace(argument)
	first, rest, _ := strings.Cut(trimmed, " ")
	if first == "" || beads.ValidateIssueID(first) != nil {
		return "", trimmed
	}
	if survey, err := s.SurveyWork(ctx); err == nil {
		for _, run := range survey.InFlight {
			if run.WorkItemID == first {
				return first, strings.TrimSpace(rest)
			}
		}
	}
	// Nothing in flight answers to it. A conversation with a run of its own is
	// almost certainly being given a reason for that run, which is what this
	// command has always meant; one without is being named an item, and the stop
	// says plainly that nothing is in flight for it.
	if _, _, running := s.RunningWork(); running {
		return "", trimmed
	}
	return first, strings.TrimSpace(rest)
}

// attention is how the run this conversation started interrupts the prompt, or
// nothing when there is no run. It is what a prompt waits beside, so on a
// terminal the operator hears about a run when something happens to it rather
// than when they next say something — whether that is a phase it crossed or the
// end of it.
func (s *Session) attention() <-chan struct{} {
	if s.active == nil {
		return nil
	}
	return s.active.wake
}

// reportFinishedWork prints the run this conversation started once it has
// ended. It is still drained by the goroutine the operator is talking to rather
// than printed from the one that ran it, because everything a run's outcome
// touches — the pending notices, the conversation's record — belongs to this
// one. What has changed is when it is drained: the composing line has a region
// of its own now, so a finished run is written above whatever is being typed as
// soon as it finishes, instead of waiting for the operator to press a key.
func (s *Session) reportFinishedWork(out io.Writer) {
	finished, err := s.CollectWork()
	if finished == nil {
		if err != nil {
			fmt.Fprintf(out, "%v\n\n", err)
		}
		return
	}
	s.printFinishedRun(out, finished, err)
}

// printFinishedRun describes a run that has ended, along with anything that
// went wrong reporting it. The report is printed either way: an outcome nobody
// could write down is still the operator's to read.
//
// A run is the one thing here the operator was not waiting at the prompt for,
// so the terminal is asked for their attention as well: the bell, and the
// window title saying what happened. A conversation is often left in a
// background tab while a run works, and those are the only two places a
// terminal can say so where somebody who is not looking at it will notice.
func (s *Session) printFinishedRun(out io.Writer, finished *FinishedRun, recordErr error) {
	if finished == nil {
		return
	}
	headline := finished.Report.Headline()
	if alert := s.theme.Alert(headline); alert != "" {
		s.titled = true
		fmt.Fprintf(out, "\n%s%s", alert, finished.Report.Render(s.theme))
	} else {
		fmt.Fprintf(out, "\n%s", finished.Report.Render(s.theme))
	}
	if finished.Err != nil {
		fmt.Fprintf(out, "  the harness reported: %v\n", finished.Err)
	}
	if recordErr != nil {
		fmt.Fprintf(out, "  %v\n", recordErr)
	}
	fmt.Fprintln(out)
}

// finishActiveRun settles the conversation's own run as it ends. The process is
// about to exit and the run belongs to it, so the choice is between stopping it
// deliberately and abandoning it; only one of those leaves a record the
// operator can act on.
func (s *Session) finishActiveRun(ctx context.Context, out io.Writer) {
	s.reportMilestones(out)
	s.reportFinishedWork(out)
	run := s.active
	if run == nil {
		return
	}
	fmt.Fprintf(out, "ending this conversation stops the run on %s, because this process owns it.\n", run.workItemID)
	// The conversation may be ending because its context was cancelled, and
	// stopping still has to record and settle, so that work is given a context
	// of its own with room for both the wait and what follows it.
	stopContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*s.options.stopGrace())
	defer cancel()
	stopped, err := s.stopActive(stopContext, s.stopNote("the operator ended the conversation"))
	fmt.Fprint(out, stopped.Render())
	if err != nil {
		fmt.Fprintf(out, "%v\n", err)
	}
}

// errNoWork reports a conversation with no harness behind it. Such a
// conversation still discusses the product; it just cannot see or steer the
// work, and says so rather than appearing to do nothing.
var errNoWork = errors.New("no harness is wired to this conversation, so it can discuss work but cannot see or steer it")

// Directed is what recording operator direction achieved. Stopped is set when
// the direction interrupted the run that was already working on the item.
type Directed struct {
	WorkItemID string
	Recorded   bool
	Stopped    *Stopped
}

// SurveyWork reports development as the harness sees it now. It is read-only:
// asking what is in flight never changes what is.
func (s *Session) SurveyWork(ctx context.Context) (Survey, error) {
	if s.options.Work == nil {
		return Survey{}, errNoWork
	}
	survey, err := s.options.Work.Survey(ctx)
	if err != nil {
		return Survey{}, fmt.Errorf("read what the harness is working on: %w", err)
	}
	return survey, nil
}

// ReadBacklog reports the admitted work in the product manager's order. It is
// read-only, and it is the operator's window onto an ordering they did not set:
// the product manager decides what is admitted and what comes first, so the
// queue a development manager pulls from has to be visible from the conversation
// that orders it rather than only in the tracker.
func (s *Session) ReadBacklog(ctx context.Context) (backlog.Queue, error) {
	if s.options.Work == nil {
		return backlog.Queue{}, errNoWork
	}
	queue, err := s.options.Work.Backlog(ctx)
	if err != nil {
		return backlog.Queue{}, fmt.Errorf("read the backlog: %w", err)
	}
	return queue, nil
}

// ShowWorkItem reads the first bounded part of one work item. It goes through
// the same tracker capability the product manager reads items with: the operator
// asking what something is gets exactly what the agent discussing it could
// have, rather than a second account of the item assembled somewhere else. It
// is read-only, and it is the operator's own command, so nothing the product
// manager says can reach it.
func (s *Session) ShowWorkItem(ctx context.Context, workItemID string) (beads.WorkItem, error) {
	if s.options.Tracker == nil {
		return beads.WorkItem{}, errors.New("no work tracker is wired to this conversation, so it cannot read an item")
	}
	id := strings.TrimSpace(workItemID)
	if err := beads.ValidateIssueID(id); err != nil {
		return beads.WorkItem{}, err
	}
	item, err := s.options.Tracker.Show(ctx, id)
	if err != nil {
		return beads.WorkItem{}, fmt.Errorf("read work item %s: %w", id, err)
	}
	return item, nil
}

// WorkItemPrice reports what one work item cost, broken down by the runs it
// took. It reads the durable run records, which is what lets it price an item
// whose runs were made before anything recorded a price and an item nobody has
// closed yet. It is read-only: pricing work never changes it.
func (s *Session) WorkItemPrice(ctx context.Context, workItemID string) (ItemPrice, error) {
	if s.options.Work == nil {
		return ItemPrice{}, errNoWork
	}
	id := strings.TrimSpace(workItemID)
	if err := beads.ValidateIssueID(id); err != nil {
		return ItemPrice{}, err
	}
	price, err := s.options.Work.Price(ctx, id)
	if err != nil {
		return ItemPrice{}, fmt.Errorf("price the runs made for %s: %w", id, err)
	}
	return price, nil
}

// printItemPrice writes what an item cost beneath the item itself. A price
// that could not be read is said in a line rather than raised as a failure: the
// operator asked to see the item, and losing the item over its price tag would
// be a worse answer than the item without one.
func printItemPrice(out io.Writer, price ItemPrice, err error) {
	if err != nil {
		fmt.Fprintf(out, "cost: could not be read, so treat it as unknown rather than nothing: %v\n", err)
		return
	}
	fmt.Fprint(out, price.Render())
}

// RunChanges reports what the harness's most recent run of a work item changed.
// Naming nothing asks about the run this conversation last started, whether it
// is still going, already collected, or was started by an earlier process this
// conversation was resumed from, because that is the run an operator asking
// "what did that change" almost always means.
//
// It answers from the durable run record rather than from the repository. A run
// is cleaned up after it integrates — its worktree removed and its branch
// deleted — so a question answered by diffing a worktree would stop having an
// answer exactly when the work succeeded. What the record kept is what it
// changed, and, where the project publishes, the pull request it changed it in.
func (s *Session) RunChanges(ctx context.Context, workItemID string) (RunChanges, error) {
	if s.options.Work == nil {
		return RunChanges{}, errNoWork
	}
	id := strings.TrimSpace(workItemID)
	if id == "" {
		id = strings.TrimSpace(s.state.LastRunWorkItemID)
	}
	if id == "" {
		return RunChanges{}, errors.New("this conversation has not run anything, so name the work item, as /diff <beads-id>")
	}
	if err := beads.ValidateIssueID(id); err != nil {
		return RunChanges{}, err
	}
	changes, err := s.options.Work.Changes(ctx, id)
	if err != nil {
		return RunChanges{}, fmt.Errorf("read what the last run of %s changed: %w", id, err)
	}
	return changes, nil
}

// StartWork has the harness run one work item. The run happens in the
// background so the conversation stays a conversation: the operator keeps
// talking while the harness works, and asks what became of it afterwards. One
// run at a time, because concurrency belongs to the scheduler rather than to
// whoever is chatting.
func (s *Session) StartWork(ctx context.Context, workItemID string) error {
	if s.options.Work == nil {
		return errNoWork
	}
	id := strings.TrimSpace(workItemID)
	if err := beads.ValidateIssueID(id); err != nil {
		return err
	}
	if running, _, ok := s.RunningWork(); ok {
		return fmt.Errorf("this conversation is already working on %s; stop it before starting another", running)
	}
	// The decision is recorded before the run starts, so a process that dies at
	// the wrong moment still leaves evidence that work was asked for. The item
	// goes into the conversation's own record at the same moment and for the
	// same reason: the process that started a run is often not the one the
	// operator comes back to ask what it changed.
	s.state.LastRunWorkItemID = id
	if err := s.emit(execution.EventWorkStarted, map[string]any{"work_item_id": id}); err != nil {
		return fmt.Errorf("record the start of work on %s: %w", id, err)
	}
	runContext, cancel := context.WithCancel(ctx)
	run := &activeRun{
		workItemID: id,
		startedAt:  s.options.clock().Now(),
		cancel:     cancel,
		done:       make(chan struct{}),
		wake:       make(chan struct{}, 1),
	}
	work := s.options.Work
	// The operator named the item, so that is what the run records as why it
	// exists. It names the conversation and the turn for the same reason a stopped
	// item's note does: what shows up in a survey afterwards has to trace back to
	// the intent that started it.
	selection := Selection{
		By: OperatorSelected,
		Reason: fmt.Sprintf("the operator ran this item by name from product-manager conversation %s, after turn %d",
			s.state.ConversationID, s.state.Turns),
	}
	go func() {
		// The run ending is itself something the operator is waiting to hear, so
		// it asks for their attention the same way a crossing does — after done
		// is closed, so whoever it wakes finds a run that has actually ended.
		defer run.signal()
		defer close(run.done)
		run.report, run.err = work.Run(runContext, id, selection)
	}()
	// The run is watched only where the console can say something about it
	// without disturbing the operator. A stream has no moment at which it could:
	// reporting between two lines that are already buffered would make a
	// redirected transcript depend on how long a run took, which is exactly what
	// the plain console refuses to do with everything else.
	if s.theme.Permitted() {
		go s.watchProgress(runContext, work, run)
	}
	s.active = run
	s.notice("the operator started work on %s, and the harness is running it now", id)
	return nil
}

// RunningWork reports the work item this conversation started and has not
// collected the result of yet.
func (s *Session) RunningWork() (string, time.Time, bool) {
	if s.active == nil {
		return "", time.Time{}, false
	}
	return s.active.workItemID, s.active.startedAt, true
}

// CollectWork reports the run this conversation started once it has ended, and
// nothing at all while it is still going: a run in flight is what a survey is
// for. The error is a recording failure only. The run it describes is already
// over and its report is returned alongside, because an outcome nobody could
// write down is still an outcome the operator has to see.
func (s *Session) CollectWork() (*FinishedRun, error) {
	run := s.active
	if run == nil {
		return nil, nil
	}
	select {
	case <-run.done:
	default:
		return nil, nil
	}
	run.cancel()
	s.active = nil
	finished := &FinishedRun{WorkItemID: run.workItemID, Report: run.report, Err: run.err}
	s.notice("work on %s finished: %s", run.workItemID, finished.Report.Headline())
	if err := s.emit(execution.EventWorkFinished, finishedPayload(finished)); err != nil {
		return finished, fmt.Errorf("record the end of work on %s: %w", run.workItemID, err)
	}
	return finished, nil
}

// WaitForWork blocks until the run this conversation started has ended, and
// then reports it. It is what an operator does when there is nothing to say
// until the work is finished. A cancelled context stops the waiting, not the
// run: the run keeps going and is still this conversation's to collect.
func (s *Session) WaitForWork(ctx context.Context) (*FinishedRun, error) {
	if s.active == nil {
		return nil, errors.New("this conversation is not running anything to wait for")
	}
	select {
	case <-s.active.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.CollectWork()
}

// StopWork stops one item's run. Naming nothing means the run this conversation
// started, which is what it has always meant; naming an item stops that item's
// run wherever it is running, including in a process this conversation has
// nothing to do with.
//
// The two paths differ only in who does the cancelling. A run this process owns
// is cancelled here and settled here. A run somewhere else is asked to stop,
// which it does at its next provider call, and this waits for that to happen so
// it can say what actually became of it — never that it stopped something it
// only asked about.
func (s *Session) StopWork(ctx context.Context, workItemID, reason string) (Stopped, error) {
	if s.options.Work == nil {
		return Stopped{}, errNoWork
	}
	trimmed := strings.TrimSpace(reason)
	if len(trimmed) > MaxOperatorMessageBytes {
		return Stopped{}, fmt.Errorf("stop reason is %d bytes, limit is %d", len(trimmed), MaxOperatorMessageBytes)
	}
	id := strings.TrimSpace(workItemID)
	if id == "" {
		if s.active == nil {
			return Stopped{}, errors.New("this conversation is not running anything to stop; name the item to stop, as /stop <beads-id>, and /status shows what the harness has in flight")
		}
		return s.stopActive(ctx, s.stopNote(trimmed))
	}
	if err := beads.ValidateIssueID(id); err != nil {
		return Stopped{}, err
	}
	if running, _, ok := s.RunningWork(); ok && running == id {
		return s.stopActive(ctx, s.stopNote(trimmed))
	}
	return s.stopTracked(ctx, id, trimmed)
}

// StopEverything stops all the work there is: it holds intake so nothing more is
// started, and then stops every run the harness has in flight, whichever process
// is carrying each one. The order is deliberate — holding first is what stops
// this becoming a race against a development manager starting the next item
// while the last one is being stopped.
//
// It reports what happened to each run rather than what was asked of it, and one
// run that could not be stopped never hides the others: an operator who reached
// for this needs to know which of them is still going.
func (s *Session) StopEverything(ctx context.Context, reason string) (Everything, error) {
	if s.options.Work == nil {
		return Everything{}, errNoWork
	}
	trimmed := strings.TrimSpace(reason)
	if len(trimmed) > MaxOperatorMessageBytes {
		return Everything{}, fmt.Errorf("stop reason is %d bytes, limit is %d", len(trimmed), MaxOperatorMessageBytes)
	}
	stopped := Everything{}
	var problems []error
	// Intake is held first and its failure does not stop the rest. Failing here
	// would leave every run in flight untouched over a switch, which is the
	// opposite of what somebody reaching for this verb wants.
	if s.options.Intake != nil {
		intake, err := s.HoldIntake(trimmed)
		stopped.Intake = &intake
		if err != nil {
			problems = append(problems, err)
		}
	}
	survey, err := s.SurveyWork(ctx)
	if err != nil {
		// Without the survey there is no list of runs to stop, so this is the one
		// failure that ends the verb. What was already done — the hold — is reported
		// with it rather than lost.
		return stopped, errors.Join(append(problems, err)...)
	}
	// The run this conversation started is stopped through the path that owns it,
	// because cancelling it here is faster and surer than asking it to notice a
	// file. Everything else is asked.
	if running, _, ok := s.RunningWork(); ok {
		one, err := s.stopActive(ctx, s.stopNote(trimmed))
		stopped.Stopped = append(stopped.Stopped, one)
		if err != nil {
			problems = append(problems, fmt.Errorf("stop %s: %w", running, err))
		}
	}
	for _, run := range survey.InFlight {
		if stopped.covers(run.WorkItemID) {
			continue
		}
		one, err := s.stopRun(ctx, run, trimmed)
		stopped.Stopped = append(stopped.Stopped, one)
		if err != nil {
			problems = append(problems, fmt.Errorf("stop %s: %w", run.WorkItemID, err))
		}
	}
	return stopped, errors.Join(problems...)
}

// Everything is what stopping everything achieved: what became of intake, and
// what became of each run that was in flight when it started.
type Everything struct {
	// Intake is what holding intake did, and is absent from a conversation with
	// no intake switch wired to it. Absent means nothing was held, so nothing
	// claims it was.
	Intake  *IntakeReport `json:"intake,omitempty"`
	Stopped []Stopped     `json:"stopped,omitempty"`
}

// covers reports an item already accounted for, so a run this conversation
// started is never also asked to stop from the survey listing.
func (e Everything) covers(workItemID string) bool {
	for _, stopped := range e.Stopped {
		if stopped.WorkItemID == workItemID {
			return true
		}
	}
	return false
}

// Render describes what stopping everything did. It says the whole of it: the
// hold, every run it stopped, and every run it only asked.
func (e Everything) Render() string {
	var rendered strings.Builder
	if e.Intake != nil {
		rendered.WriteString(e.Intake.Render())
	}
	if len(e.Stopped) == 0 {
		rendered.WriteString("nothing was in flight, so no run was stopped.\n")
		return rendered.String()
	}
	for _, stopped := range e.Stopped {
		rendered.WriteString(stopped.Render())
	}
	return rendered.String()
}

// stopTracked stops the run in flight for one item, from outside the process
// working on it. It finds the run by reading rather than by holding anything: a
// run another process owns is that process's to end, so what happens here is a
// request, a wait, and an honest account of what the wait found.
func (s *Session) stopTracked(ctx context.Context, workItemID, reason string) (Stopped, error) {
	survey, err := s.SurveyWork(ctx)
	if err != nil {
		return Stopped{}, err
	}
	for _, run := range survey.InFlight {
		if run.WorkItemID == workItemID {
			return s.stopRun(ctx, run, reason)
		}
	}
	// Nothing is in flight for the item. That is usually because the work is over,
	// and saying which is worth more than refusing: an operator who typed this was
	// looking at something, and "it finished" and "the harness has never run it"
	// are different answers.
	if progress, err := s.options.Work.Progress(ctx, workItemID); err == nil {
		return Stopped{}, fmt.Errorf("nothing is in flight for %s, so there is nothing to stop; its last run %s ended with status %s",
			workItemID, progress.RunID, progress.Status)
	}
	return Stopped{}, fmt.Errorf("nothing is in flight for %s, so there is nothing to stop; /status shows what is", workItemID)
}

// stopRun asks one recorded run to stop and reports what became of it.
//
// The request is recorded before it is made, so a process that dies while
// carrying it out leaves evidence that the operator asked. Then it waits, and
// what it waits for is the run's own record going terminal — which is the run
// itself saying it has ended, rather than this concluding anything on its
// behalf. A run that reaches its own conclusion in the meantime is reported as
// exactly that: the stop arrived too late and stopped nothing.
func (s *Session) stopRun(ctx context.Context, run RunSnapshot, reason string) (Stopped, error) {
	stopped := Stopped{WorkItemID: run.WorkItemID, RunID: run.RunID, Requested: true}
	note := s.stopNote(reason)
	var problems []error
	if err := s.emit(execution.EventWorkStopped, map[string]any{
		"work_item_id": run.WorkItemID,
		"run_id":       run.RunID,
		"note":         note,
		"requested":    true,
	}); err != nil {
		problems = append(problems, fmt.Errorf("record stopping work on %s: %w", run.WorkItemID, err))
	}
	if err := s.options.Work.RequestStop(ctx, StopRequest{
		RunID:      run.RunID,
		WorkItemID: run.WorkItemID,
		Reason:     reason,
	}); err != nil {
		// Nothing was asked, so nothing is waited for and nothing is settled: a
		// request that was not recorded will never reach the run.
		return stopped, errors.Join(append(problems, fmt.Errorf("ask the run on %s to stop: %w", run.WorkItemID, err))...)
	}
	s.notice("the operator asked the run on %s to stop", run.WorkItemID)
	ended, err := s.awaitStoppedRun(ctx, run)
	if err != nil {
		problems = append(problems, err)
	}
	if ended == nil {
		// The run has not ended within the grace. It keeps everything it has and
		// stops at its next provider call, so nothing here decides anything about
		// it: saying it stopped would be a claim about a run that is still going.
		return stopped, errors.Join(append(problems, s.recordStopOutcome(stopped))...)
	}
	stopped.Finished = true
	stopped.Report = *ended
	if endedBeforeStopped(ended.Status) {
		// The run reached its own conclusion before the request reached it. Nothing
		// was stopped, so nothing says it was, and nothing is written on the item.
		stopped.AlreadyFinished = true
		s.notice("the operator asked to stop work on %s, but it had already finished: %s", run.WorkItemID, stopped.Report.Headline())
		return stopped, errors.Join(append(problems, s.recordStopOutcome(stopped))...)
	}
	s.notice("the operator stopped work on %s", run.WorkItemID)
	if err := s.options.Work.Direct(ctx, run.WorkItemID, note); err != nil {
		problems = append(problems, fmt.Errorf("record why work on %s was stopped: %w", run.WorkItemID, err))
	}
	// A run stopped in another process leaves exactly what one stopped here
	// leaves: a worktree, a branch, and a claimed item nobody is acting on. It is
	// settled the same way, by the same reconciliation, which is what keeps
	// stopping one verb rather than one verb and a follow-up.
	settlements, err := s.options.Work.Settle(ctx)
	if err != nil {
		problems = append(problems, fmt.Errorf("settle what the stopped run left behind: %w", err))
	}
	stopped.Settlements = settlements
	return stopped, errors.Join(append(problems, s.recordStopOutcome(stopped))...)
}

// awaitStoppedRun waits for a run to end after being asked to, and reports what
// it ended as. Nothing at all means it had not ended when the grace ran out,
// which is a fact about the run rather than a failure: a provider invocation
// already streaming is not interrupted, so a run mid-generation stops only once
// that generation is over.
//
// It reads the run's record rather than watching a process, because there is no
// process here to watch. That is also what makes it truthful about the case this
// exists to report: a run that finished on its own reads as succeeded, and a run
// the request ended reads as cancelled.
func (s *Session) awaitStoppedRun(ctx context.Context, run RunSnapshot) (*RunReport, error) {
	// The wait is counted in probes rather than measured against a clock, which
	// is what keeps it bounded: this waits on another process's record changing,
	// and a wait that read the time to decide whether to give up would depend on a
	// clock this conversation may not be the one advancing.
	for probes := probesWithin(s.options.stopGrace()); ; probes-- {
		progress, err := s.options.Work.Progress(ctx, run.WorkItemID)
		if err != nil {
			return nil, fmt.Errorf("read what became of the run on %s: %w", run.WorkItemID, err)
		}
		if progress.RunID == run.RunID && terminalRunStatus(progress.Status) {
			return stoppedReport(run, progress), nil
		}
		if probes <= 1 {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(stopProbe):
		}
	}
}

// stopProbe is how often a stopped run's record is re-read while waiting for it
// to end. It costs one small file read and never a request to the provider, so
// it is short enough that a run that stops promptly is reported promptly.
const stopProbe = 250 * time.Millisecond

// probesWithin is how many readings of a run's record fit in the grace. It is
// never fewer than one: a grace of nothing still means look once, because the
// run may already have ended and reporting it as still going would be wrong in
// exactly the direction that matters.
func probesWithin(grace time.Duration) int {
	probes := int(grace / stopProbe)
	if probes < 1 {
		return 1
	}
	return probes
}

// terminalRunStatus reports a run that has stopped. The statuses are named here
// rather than imported for the reason the pause causes are: a conversation stays
// independent of how a run is executed, and a status this does not recognize is
// treated as still going, because concluding the wrong way would report a run
// that is still working as one that stopped.
func terminalRunStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled", "timed_out":
		return true
	default:
		return false
	}
}

// endedBeforeStopped reports a run that reached its own conclusion rather than
// being ended by the request, which is the one case where a stop stopped nothing.
//
// The recorded status is what says so, and it can, because a run that honors a
// stop request records itself as cancelled — the same status a run cancelled in
// this process records. Anything else is a run that succeeded, failed, or was
// stopped on time under its own steam, all of which happened without reference to
// the request. It is the counterpart of endedOnItsOwn for a run this process
// never held: there is no returned error to read here and no in-memory report,
// only what the run wrote down.
func endedBeforeStopped(status string) bool {
	return status != "cancelled"
}

// stoppedReport describes an ended run from what its record says, which is all
// there is: this process never ran it and has no outcome of its own to report.
// It claims integration only from the recorded promotion, exactly as the
// pipeline's own report does.
func stoppedReport(run RunSnapshot, progress RunProgress) *RunReport {
	report := RunReport{
		RunID:        progress.RunID,
		WorkItemID:   run.WorkItemID,
		Status:       progress.Status,
		Branch:       run.Branch,
		Integrated:   progress.Integrated,
		TargetBranch: progress.TargetBranch,
	}
	if progress.Integrated {
		return &report
	}
	// A run that ended without integrating and was not asked to succeed is
	// reported as having been stopped, in the terms the record supports: it did
	// not finish. The status beside it says which way it went.
	if progress.Status != "succeeded" {
		report.Failure = "the run ended with status " + progress.Status
	}
	return &report
}

// DirectWork records durable operator direction on a work item, where the next
// attempt at it reads it. Redirecting the item this conversation is running
// stops that run first: an attempt working from the old direction is not
// redirected by writing a new one down beside it.
func (s *Session) DirectWork(ctx context.Context, workItemID, direction string) (Directed, error) {
	if s.options.Work == nil {
		return Directed{}, errNoWork
	}
	id := strings.TrimSpace(workItemID)
	if err := beads.ValidateIssueID(id); err != nil {
		return Directed{}, err
	}
	trimmed := strings.TrimSpace(direction)
	if trimmed == "" {
		return Directed{}, errors.New("say what the work should do differently; a redirection with no direction is not one")
	}
	if len(trimmed) > MaxOperatorMessageBytes {
		return Directed{}, fmt.Errorf("direction is %d bytes, limit is %d", len(trimmed), MaxOperatorMessageBytes)
	}

	directed := Directed{WorkItemID: id}
	var problems []error
	if running, _, ok := s.RunningWork(); ok && running == id {
		stopped, err := s.stopActive(ctx, s.redirectNote(trimmed))
		directed.Stopped = &stopped
		if err != nil {
			problems = append(problems, err)
		}
		if !stopped.Finished {
			// The old attempt is still going, so the direction is deliberately
			// not recorded yet: recording it now would describe the work as
			// redirected while the attempt it redirects is still running.
			return directed, errors.Join(problems...)
		}
	}
	if err := s.emit(execution.EventWorkDirected, map[string]any{"work_item_id": id, "direction": trimmed}); err != nil {
		problems = append(problems, fmt.Errorf("record direction for %s: %w", id, err))
	}
	if err := s.options.Work.Direct(ctx, id, s.directionNote(trimmed)); err != nil {
		problems = append(problems, fmt.Errorf("record direction on %s where the work is tracked: %w", id, err))
		return directed, errors.Join(problems...)
	}
	directed.Recorded = true
	s.notice("the operator redirected %s: %s", id, trimmed)
	return directed, errors.Join(problems...)
}

// stopActive cancels the run this conversation started, records why on the work
// item, and settles what the cancelled run left behind the way an interrupted
// process's runs are settled. The request is recorded before anything is
// cancelled, so it survives a process that dies while carrying it out, and the
// outcome is recorded once it is known. A run that finished on its own before
// the cancellation reached it is recorded as exactly that: nothing is written
// on the item and nothing is settled, because nothing was stopped. See
// endedOnItsOwn for what separates the two.
func (s *Session) stopActive(ctx context.Context, note string) (Stopped, error) {
	run := s.active
	stopped := Stopped{WorkItemID: run.workItemID}
	var problems []error
	// This records the operator's request, not its result: the run has not been
	// asked to stop yet, and what became of it is only known afterwards. The
	// terminal event below is what says that, so a log reader is never left with
	// a request that reads like an accomplished fact.
	if err := s.emit(execution.EventWorkStopped, map[string]any{
		"work_item_id": run.workItemID,
		"note":         note,
		"requested":    true,
	}); err != nil {
		problems = append(problems, fmt.Errorf("record stopping work on %s: %w", run.workItemID, err))
	}
	run.cancel()
	select {
	case <-run.done:
	case <-time.After(s.options.stopGrace()):
		// The run is still winding down. It keeps its lease and its artifacts,
		// so nothing here may decide anything about it: saying it stopped would
		// be a claim about a run that is still going.
		problems = append(problems, fmt.Errorf("the run on %s has not given up after %s and is still in flight", run.workItemID, s.options.stopGrace()))
		return stopped, errors.Join(problems...)
	}
	s.active = nil
	stopped.Finished = true
	stopped.Report = run.report
	stopped.RunErr = run.err
	if endedOnItsOwn(stopped) {
		// The run reached its own conclusion before the cancellation reached it.
		// Nothing was stopped, so nothing says it was: a stop recorded on an item
		// whose work is already done would be a false account of both.
		stopped.AlreadyFinished = true
		s.notice("the operator asked to stop work on %s, but it had already finished: %s", run.workItemID, stopped.Report.Headline())
		return stopped, errors.Join(append(problems, s.recordStopOutcome(stopped))...)
	}
	s.notice("the operator stopped work on %s", run.workItemID)

	if err := s.options.Work.Direct(ctx, run.workItemID, note); err != nil {
		problems = append(problems, fmt.Errorf("record why work on %s was stopped: %w", run.workItemID, err))
	}
	// A cancelled run leaves the same thing an interrupted process leaves: a
	// worktree, a branch, and a claimed item nobody is acting on. Settling it
	// here is what keeps stopping a complete action rather than one that hands
	// the operator back to a second tool.
	settlements, err := s.options.Work.Settle(ctx)
	if err != nil {
		problems = append(problems, fmt.Errorf("settle what the stopped run left behind: %w", err))
	}
	stopped.Settlements = settlements
	// The outcome is recorded last, once it includes what the stop left behind,
	// so the durable record says exactly what the operator was shown.
	return stopped, errors.Join(append(problems, s.recordStopOutcome(stopped))...)
}

// endedOnItsOwn reports a run that reached its own conclusion before the
// cancellation reached it, which is the one case where a stop stopped nothing.
//
// The harness reporting no failure is what says so. Integration cannot be the
// test: the shipped bundle defaults `approvals.integration` to human, and under
// that policy a run that developed, checked, and reviewed its work
// successfully integrates nothing at all, so keying on a promotion would record
// a stop against every successful run that beat the cancellation. A run the
// cancellation really did end fails somewhere — its provider, its checks, or
// the recording of either — and is left to the stopping path.
//
// A run that paused itself is deliberately excluded. It reported no failure,
// but it is owed a continuation rather than finished, so the operator's stop is
// still recorded against it. Anything uncertain belongs on that side: a
// needless note and an idempotent settle cost far less than an interrupted run
// left with a preserved worktree and no blocker naming it.
func endedOnItsOwn(stopped Stopped) bool {
	return stopped.RunErr == nil && !stopped.Report.Paused
}

// recordStopOutcome writes the terminal record of a run the operator asked to
// stop. Every run this conversation starts ends in exactly one terminal event,
// whether it was collected or stopped, so the log always says what became of it
// rather than only what was asked for. It is what keeps the record and the
// operator's screen telling the same story: a run that integrated before the
// stop reached it is recorded as having finished on its own, and a run the stop
// really did end is recorded with what it left behind.
func (s *Session) recordStopOutcome(stopped Stopped) error {
	payload := map[string]any{
		"work_item_id":     stopped.WorkItemID,
		"report":           stopped.Report,
		"stopped":          !stopped.AlreadyFinished,
		"already_finished": stopped.AlreadyFinished,
	}
	if stopped.RunErr != nil {
		payload["failure"] = stopped.RunErr.Error()
	}
	if len(stopped.Settlements) > 0 {
		payload["settlements"] = stopped.Settlements
	}
	if err := s.emit(execution.EventWorkFinished, payload); err != nil {
		return fmt.Errorf("record the end of work on %s: %w", stopped.WorkItemID, err)
	}
	return nil
}

// stopNote is what a stopped item records about why. The conversation and the
// turn are named for the same reason an approved proposal names them: the item
// has to trace back to the intent that changed it.
func (s *Session) stopNote(reason string) string {
	note := fmt.Sprintf("The operator stopped this work from product-manager conversation %s, after turn %d.",
		s.state.ConversationID, s.state.Turns)
	if strings.TrimSpace(reason) != "" {
		note += "\n\nReason: " + strings.TrimSpace(reason)
	}
	return note
}

func (s *Session) redirectNote(direction string) string {
	return fmt.Sprintf("The operator stopped this work from product-manager conversation %s, after turn %d, in order to redirect it.\n\nNew direction: %s",
		s.state.ConversationID, s.state.Turns, strings.TrimSpace(direction))
}

func (s *Session) directionNote(direction string) string {
	return fmt.Sprintf("Operator direction from product-manager conversation %s, after turn %d. The next attempt at this item is expected to follow it.\n\n%s",
		s.state.ConversationID, s.state.Turns, strings.TrimSpace(direction))
}

// finishedPayload is what the conversation's own log keeps about a run it
// started. The run has its own event log; this records that this conversation
// asked for it and what came back.
func finishedPayload(finished *FinishedRun) map[string]any {
	payload := map[string]any{
		"work_item_id": finished.WorkItemID,
		"report":       finished.Report,
	}
	if finished.Err != nil {
		payload["failure"] = finished.Err.Error()
	}
	return payload
}

// Render describes what recording direction achieved, including the run it had
// to stop to mean anything.
func (d Directed) Render() string {
	var rendered strings.Builder
	if d.Stopped != nil {
		rendered.WriteString(d.Stopped.Render())
	}
	if d.Recorded {
		fmt.Fprintf(&rendered, "recorded your direction on %s; the next attempt at it reads it.\n", d.WorkItemID)
		// Only work the redirection actually interrupted is worth offering to
		// start again. Telling the operator to retry an item whose run finished
		// before the redirection reached it would send them after work that is
		// already done.
		if d.Stopped != nil && d.Stopped.Finished && !d.Stopped.AlreadyFinished {
			fmt.Fprintf(&rendered, "start it again with /work %s when you want it retried.\n", d.WorkItemID)
		}
	}
	return rendered.String()
}
