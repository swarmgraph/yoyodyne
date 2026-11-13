package orchestrator

// A developer asks the harness for an MCP call by ending its turn with the
// yoyodyne-mcp block, and the harness carries it out and resumes the same
// session within the same attempt (docs/designs/mcp-tools-through-the-harness.md,
// section 3).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// askSearch is a reply that ends asking for one call to mail.search.
const askSearch = "I need the invoice thread before I can finish.\n\n```yoyodyne-mcp\n" +
	`{"requests":[{"tool":"mail.search","arguments":{"query":"invoice"},"why":"which invoice the item means"}]}` + "\n```"

const finishedAccount = "Added feature.txt using the invoice the search found; the configured check passes. No risk outstanding."

// fakeMCP stands in for the one call path: it answers every call as performed
// and keeps what it was asked, and with whom.
type fakeMCP struct {
	mu      sync.Mutex
	asked   [][]mcpcall.Request
	callers []mcpcall.CallerRecord
}

func (f *fakeMCP) Do(_ context.Context, caller mcpcall.Caller, requests []mcpcall.Request) ([]mcpcall.Answer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, requests)
	f.callers = append(f.callers, caller.Record())
	answers := make([]mcpcall.Answer, len(requests))
	for index, request := range requests {
		answers[index] = mcpcall.Answer{Tool: request.Tool, Outcome: mcpcall.OutcomePerformed, Result: `{"subject":"Invoice 42"}`}
	}
	return answers, nil
}

func (f *fakeMCP) rounds() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

// withMCPTools gives the pipeline's developer the read tool mail.search. The
// server also declares mail.send, an act tool, which a developer may not list
// and is never told of.
func withMCPTools(pipeline Pipeline, calls MCPCalls) Pipeline {
	pipeline.Config.MCP = config.MCP{Servers: map[string]config.MCPServer{
		"mail": {Command: []string{"mail-mcp"}, Tools: map[string]config.MCPToolClass{"search": config.MCPRead, "send": config.MCPAct}},
	}}
	developer := pipeline.Config.Agents["developer"]
	developer.MCPTools = []string{"mail.search"}
	pipeline.Config.Agents["developer"] = developer
	pipeline.MCP = calls
	return pipeline
}

// mcpSaid is every sentence the harness wrote into a run's event stream about
// the developer's MCP asks.
func mcpSaid(t *testing.T, store *runstate.Store, runID string) []string {
	t.Helper()
	events, err := store.LoadEvents(runID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	var said []string
	for _, event := range events {
		if event.Source == "harness" && strings.Contains(string(event.Payload), "asking for MCP calls") {
			said = append(said, string(event.Payload))
		}
	}
	return said
}

// The developer ends its first turn asking for a call. The harness makes it and
// resumes the same session with the answer; the developer finishes, and the
// run goes on as one whose developer finished the first time, with no repair
// attempt spent and the asking reply never taken as the account.
func TestADeveloperAskIsCarriedOutAndTheSameSessionResumed(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{askSearch, finishedAccount}
	calls := &fakeMCP{}
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline = withMCPTools(pipeline, calls)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !outcome.WorkItemClosed {
		t.Fatalf("a developer that asked for a call did not finish its run: %#v", outcome)
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want the ask to spend none", outcome.RepairAttempts)
	}
	if outcome.Summary != finishedAccount {
		t.Fatalf("summary = %q, want the account the finished turn gave, never the asking reply", outcome.Summary)
	}
	if calls.rounds() != 1 || len(calls.asked[0]) != 1 || calls.asked[0][0].Tool != "mail.search" || string(calls.asked[0][0].Arguments) != `{"query":"invoice"}` {
		t.Fatalf("the call path was asked %#v, want the one search the block asked for", calls.asked)
	}
	if caller := calls.callers[0]; caller.Kind != mcpcall.CallerDeveloper || caller.Run != outcome.RunID || caller.Agent != "developer" || caller.Attempt != 0 {
		t.Fatalf("the call was made for %#v, want this run's developer in its first attempt", caller)
	}
	requests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(requests) != 2 {
		t.Fatalf("the developer was invoked %d time(s), want the asking turn and the one resumed with the answer", len(requests))
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("the change was reviewed %d time(s), want once, after the resumed turn", reviews)
	}
	first, resumed := requests[0], requests[1]
	for _, want := range []string{"# Asking the harness for an MCP call", "`mail.search` (read)", "```yoyodyne-mcp"} {
		if !strings.Contains(first.Prompt, want) {
			t.Fatalf("the developer holding a tool was not told %q:\n%s", want, first.Prompt)
		}
	}
	if strings.Contains(first.Prompt, "mail.send") {
		t.Fatalf("the developer was told of an act tool it does not hold:\n%s", first.Prompt)
	}
	if resumed.SessionID != provider.DeveloperSession {
		t.Fatalf("the resumed turn ran in session %q, want the session that asked", resumed.SessionID)
	}
	for _, want := range []string{"The answers to the MCP calls you asked for", "Invoice 42", "untrusted data", "No repair attempt was spent"} {
		if !strings.Contains(resumed.Prompt, want) {
			t.Fatalf("the resumed turn was not sent %q:\n%s", want, resumed.Prompt)
		}
	}
	said := mcpSaid(t, store, outcome.RunID)
	if len(said) != 1 || !strings.Contains(said[0], "mail.search: performed") || !strings.Contains(said[0], "no repair attempt is spent") {
		t.Fatalf("the run's record does not say why the developer was invoked again: %q", said)
	}
}

// A developer that holds no MCP tool is told nothing about the block, and a
// block it writes anyway is read as any reply is: the run's instructions and
// endings are exactly what they were before the block existed.
func TestADeveloperHoldingNoMCPToolIsToldNothing(t *testing.T) {
	t.Parallel()

	for name, configure := range map[string]func(Pipeline, *fakeMCP) Pipeline{
		"no call path": func(p Pipeline, _ *fakeMCP) Pipeline { return withMCPTools(p, nil) },
		"no tools listed": func(p Pipeline, calls *fakeMCP) Pipeline {
			p = withMCPTools(p, calls)
			developer := p.Config.Agents["developer"]
			developer.MCPTools = nil
			p.Config.Agents["developer"] = developer
			return p
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tracker := newOutcomeTracker()
			provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
			provider.DeveloperFinalText = finishedAccount + "\n\n```yoyodyne-mcp\n" + `{"requests":[{"tool":"mail.search"}]}` + "\n```"
			calls := &fakeMCP{}
			pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
			pipeline = configure(pipeline, calls)

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Status != runstate.StatusSucceeded {
				t.Fatalf("outcome = %#v, want a succeeded run", outcome)
			}
			requests := provider.RequestsForRole(domain.RoleDeveloper)
			if len(requests) != 1 || calls.rounds() != 0 {
				t.Fatalf("developer invocations = %d, calls = %d; want one invocation and no call", len(requests), calls.rounds())
			}
			if strings.Contains(requests[0].Prompt, "yoyodyne-mcp") || strings.Contains(requests[0].Prompt, "MCP call") {
				t.Fatalf("a developer holding no tool was told of the block:\n%s", requests[0].Prompt)
			}
		})
	}
}

// A developer that keeps asking is answered for every round its limit allows
// and one more, in which the path refuses it and tells it to go on without the
// call; asking again after that ends the run rather than resuming forever.
func TestADeveloperThatKeepsAskingPastItsLimitStops(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	provider.DeveloperFinalText = askSearch
	calls := &fakeMCP{}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline = withMCPTools(pipeline, calls)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil && outcome.Status == runstate.StatusSucceeded {
		t.Fatalf("a developer that never stopped asking finished its run: %#v", outcome)
	}
	rounds := mcpcall.DefaultLimits(mcpcall.CallerDeveloper).Rounds
	if calls.rounds() != rounds+1 {
		t.Fatalf("the call path was asked %d time(s), want %d rounds and the one it refuses past them", calls.rounds(), rounds)
	}
	if invocations := len(provider.RequestsForRole(domain.RoleDeveloper)); invocations != rounds+2 {
		t.Fatalf("the developer was invoked %d time(s), want %d", invocations, rounds+2)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 0 {
		t.Fatalf("a reply asking for calls reached the reviewer %d time(s)", reviews)
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want none spent on asking", outcome.RepairAttempts)
	}
}

// A turn that asks and also left its background work running is sent back for
// the background work first, told its block was not carried out; the block on
// the reply that follows is the one carried out.
func TestAnAskOnATurnThatLeftBackgroundWorkIsCarriedOutOnTheNextReply(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{askSearch, askSearch, finishedAccount}
	provider.DeveloperAfterReplyByAttempt = []*execution.AfterReply{endedAtBound()}
	calls := &fakeMCP{}
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline = withMCPTools(pipeline, calls)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.RepairAttempts != 0 || outcome.Summary != finishedAccount {
		t.Fatalf("outcome = %#v, want a run that finished on the account with no repair attempt spent", outcome)
	}
	requests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(requests) != 3 {
		t.Fatalf("the developer was invoked %d time(s), want the ask, the send-back, and the resume", len(requests))
	}
	if !strings.Contains(requests[1].Prompt, "Your turn ended while your own background work was still running") || !strings.Contains(requests[1].Prompt, "Nothing in it was called") {
		t.Fatalf("the turn sent back for its background work was not told its block waits:\n%s", requests[1].Prompt)
	}
	if calls.rounds() != 1 {
		t.Fatalf("the call path was asked %d time(s), want once, for the reply after the send-back", calls.rounds())
	}
	if !strings.Contains(requests[2].Prompt, "The answers to the MCP calls you asked for") {
		t.Fatalf("the third turn was not resumed with the answers:\n%s", requests[2].Prompt)
	}
	if said := harnessSaid(t, store, outcome.RunID); len(said) != 1 || !strings.Contains(said[0], "sent back to the developer") {
		t.Fatalf("the run's record does not say the turn was sent back for its background work: %q", said)
	}
}

// A provider that reports no session has nothing to resume with the answers,
// so the run stops as a resume that cannot be made does, and nothing is called.
func TestAnAskWithNoSessionToResumeStopsTheRun(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	provider.DeveloperSession = ""
	provider.DeveloperFinalText = askSearch
	calls := &fakeMCP{}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline = withMCPTools(pipeline, calls)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil && outcome.Status == runstate.StatusSucceeded {
		t.Fatalf("a run with no session to resume finished: %#v", outcome)
	}
	if !strings.Contains(errorText(err), "no session to resume") {
		t.Fatalf("err = %v, outcome = %#v; want it to say there was no session to resume", err, outcome)
	}
	if calls.rounds() != 0 || len(provider.RequestsForRole(domain.RoleDeveloper)) != 1 {
		t.Fatalf("calls = %d, developer invocations = %d; want nothing called and no second turn", calls.rounds(), len(provider.RequestsForRole(domain.RoleDeveloper)))
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// A block that cannot be read is still an ask: nothing is called, the session
// is resumed with the reason, and the reply is not taken as the account.
func TestAnUnreadableBlockIsAnsweredWithoutCallingAnything(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{"```yoyodyne-mcp\n{\"requests\":[{\"tool\":\"mail.search\",\"extra\":1}]}\n```", finishedAccount}
	calls := &fakeMCP{}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline = withMCPTools(pipeline, calls)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Summary != finishedAccount {
		t.Fatalf("outcome = %#v, want the account of the resumed turn", outcome)
	}
	if calls.rounds() != 0 {
		t.Fatalf("an unreadable block was called %d time(s)", calls.rounds())
	}
	requests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(requests) != 2 || !strings.Contains(requests[1].Prompt, "Your block could not be read, so nothing was called") {
		t.Fatalf("the developer was not resumed with why its block was refused: %d invocations", len(requests))
	}
}

// The same ask on Codex, through the real adapter over a scripted CLI: the
// second developer turn is a resume of the session the first one opened.
func TestADeveloperAskResumesTheSameSessionOnCodex(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker,
		orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict), []string{"exit 0"})
	pipeline.Config.Agents["developer"] = config.AgentConfig{
		Role: domain.RoleDeveloper, Backend: domain.BackendCodex, Model: "gpt-6.1-sol", Instances: 1,
	}
	cli := &askingCodexCLI{replies: []string{askSearch, orchestratortest.WithVerification(finishedAccount)}}
	pipeline.Backend = codex.Backend{Runner: cli, ConfigDir: t.TempDir()}
	calls := &fakeMCP{}
	pipeline = withMCPTools(pipeline, calls)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.RepairAttempts != 0 || outcome.Summary != finishedAccount {
		t.Fatalf("outcome = %#v, want a Codex run that finished on its account with no repair attempt spent", outcome)
	}
	if calls.rounds() != 1 {
		t.Fatalf("the call path was asked %d time(s), want once", calls.rounds())
	}
	turns := cli.developerTurns()
	if len(turns) != 2 {
		t.Fatalf("the developer took %d turn(s) on Codex, want the ask and its resume", len(turns))
	}
	if resumes(turns[0]) {
		t.Fatalf("the first turn resumed a session: %v", turns[0].Args)
	}
	if !resumes(turns[1]) || !strings.Contains(strings.Join(turns[1].Args, "\n"), codexSessionID) {
		t.Fatalf("the turn with the answers did not resume session %q: %v", codexSessionID, turns[1].Args)
	}
}

func resumes(command execution.Command) bool {
	for _, arg := range command.Args {
		if arg == "resume" {
			return true
		}
	}
	return false
}

// askingCodexCLI is the Codex CLI answering each developer turn with the next
// reply in turn, after the availability probes.
type askingCodexCLI struct {
	mu       sync.Mutex
	replies  []string
	commands []execution.Command
	turns    []execution.Command
}

func (c *askingCodexCLI) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	switch {
	case len(command.Args) == 0:
		return execution.ProcessResult{}, errors.New("no arguments")
	case command.Args[0] == "--version":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "codex-cli 0.44.0\n"}, nil
	case command.Args[0] == "login":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "Logged in using ChatGPT\n"}, nil
	case len(command.Args) >= 2 && command.Args[0] == "mcp" && command.Args[1] == "list":
		// Each developer turn first asks which MCP servers it would start, to
		// turn them off; this CLI lists none, and the listing is not a turn.
		if observer != nil {
			observer(execution.Output{Stream: execution.StreamStdout, Text: "[]"})
		}
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
	}
	c.mu.Lock()
	turn := len(c.turns)
	c.turns = append(c.turns, command)
	c.mu.Unlock()
	if err := os.WriteFile(filepath.Join(command.Dir, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
		return execution.ProcessResult{}, err
	}
	reply := c.replies[min(turn, len(c.replies)-1)]
	for _, line := range codexStream(reply) {
		if observer != nil {
			observer(execution.Output{Stream: execution.StreamStdout, Text: line})
		}
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func (c *askingCodexCLI) developerTurns() []execution.Command {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]execution.Command(nil), c.turns...)
}

// The block is read off the reply by the fenced rules every channel shares,
// strictly, and a reply with none asks for nothing.
func TestReadAskReadsOnlyAWellFormedBlock(t *testing.T) {
	t.Parallel()

	if ask := mcpcall.ReadAsk(finishedAccount); ask.Found {
		t.Fatalf("a reply with no block asked: %#v", ask)
	}
	ask := mcpcall.ReadAsk(askSearch)
	if !ask.Found || ask.Problem != "" || len(ask.Requests) != 1 || ask.Requests[0].Why == "" {
		t.Fatalf("ask = %#v, want the one search", ask)
	}
	var arguments map[string]string
	if err := json.Unmarshal(ask.Requests[0].Arguments, &arguments); err != nil || arguments["query"] != "invoice" {
		t.Fatalf("arguments = %s (%v)", ask.Requests[0].Arguments, err)
	}
	for name, reply := range map[string]string{
		"empty":         "```yoyodyne-mcp\n\n```",
		"no requests":   "```yoyodyne-mcp\n{\"requests\":[]}\n```",
		"no tool":       "```yoyodyne-mcp\n{\"requests\":[{\"arguments\":{}}]}\n```",
		"unknown field": "```yoyodyne-mcp\n{\"requests\":[],\"more\":true}\n```",
		"two blocks":    askSearch + "\n\n" + askSearch,
		"unclosed":      "```yoyodyne-mcp\n{\"requests\":[{\"tool\":\"a.b\"}]}",
	} {
		if ask := mcpcall.ReadAsk(reply); !ask.Found || ask.Problem == "" || len(ask.Requests) != 0 {
			t.Errorf("%s: ask = %#v, want a found block refused with its reason", name, ask)
		}
	}
}
