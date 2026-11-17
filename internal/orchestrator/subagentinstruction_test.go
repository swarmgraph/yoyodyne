package orchestrator

// A run with sub-agents on is told how to use them, by the instruction built
// into the program for its arrangement of models — developer-sub-agents-opus-
// main.md for an Opus developer with cheaper sub-agents,
// developer-sub-agents-sonnet-main.md for a Sonnet developer with Opus
// sub-agents, developer-sub-agents.md otherwise — placed after the developer
// persona and noted on the run's record with the build it came from; a run
// with them off, or on a provider that does not offer them, is told nothing of
// the kind (docs/designs/developer-sub-agents.md).

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const instructionTestPersona = "# Developer persona\n\nPrefer the smallest change that satisfies the criteria.\n"

const instructionTestBuild = "0123456789abcdef0123456789abcdef01234567"

func instructionOf(t *testing.T) string {
	t.Helper()
	return instructionText(t, subagentInstructionFile, subagentInstruction)
}

// instructionFor is the trimmed text of the instruction for a developer on
// mainModel with sub-agents on subagentModel.
func instructionFor(t *testing.T, mainModel, subagentModel string) string {
	t.Helper()
	file, text := subagentInstructionFor(mainModel, subagentModel)
	return instructionText(t, file, text)
}

func instructionText(t *testing.T, file, text string) string {
	t.Helper()
	instruction := strings.TrimSpace(text)
	if instruction == "" {
		t.Fatalf("the built-in sub-agent instruction %s is empty", file)
	}
	return instruction
}

// everyInstruction is the trimmed text of each built-in instruction.
func everyInstruction(t *testing.T) []string {
	t.Helper()
	return []string{
		instructionText(t, subagentInstructionFile, subagentInstruction),
		instructionText(t, opusMainInstructionFile, opusMainInstruction),
		instructionText(t, sonnetMainInstructionFile, sonnetMainInstruction),
	}
}

// The harness chooses the instruction from the main and sub-agent models,
// reading each by the family its name carries.
func TestTheSubagentInstructionIsChosenFromTheArrangement(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		main, sub, file string
	}{
		{"opus", "sonnet", opusMainInstructionFile},
		{"claude-opus-4-7", "claude-sonnet-4-6", opusMainInstructionFile},
		{"opus[1m]", "haiku", opusMainInstructionFile},
		{"sonnet", "opus", sonnetMainInstructionFile},
		{"claude-sonnet-4-6", "Claude-Opus-4-7", sonnetMainInstructionFile},
		{"opus", "opus", subagentInstructionFile},
		{"sonnet", "sonnet", subagentInstructionFile},
		{"haiku", "opus", subagentInstructionFile},
		{"gpt-6.1-sol", "sonnet", subagentInstructionFile},
		{"", "", subagentInstructionFile},
	} {
		file, text := subagentInstructionFor(test.main, test.sub)
		if file != test.file {
			t.Errorf("a developer on %q with sub-agents on %q gets %s, want %s", test.main, test.sub, file, test.file)
		}
		if strings.TrimSpace(text) == "" {
			t.Errorf("the instruction %s is empty", file)
		}
	}
	texts := everyInstruction(t)
	for i := range texts {
		for j := i + 1; j < len(texts); j++ {
			if texts[i] == texts[j] {
				t.Errorf("two arrangements are given the same instruction:\n%s", texts[i])
			}
		}
	}
}

// An Opus developer with cheaper sub-agents hands the exploration and the
// running and fixing of checks over through a structured brief and a fixed report, and
// reviews the diff.
func TestTheOpusMainInstructionHandsOffReadingAndChecks(t *testing.T) {
	t.Parallel()

	instruction := instructionFor(t, "opus", "sonnet")
	for _, point := range []string{
		"Hand the exploration to a sub-agent.",
		"Ask for a summary of at most twenty lines",
		"Hand the writing, the checks and the fixes to a sub-agent, one bounded piece at a time.",
		"Files: the files to change",
		"Change: what the change must do",
		"Done when: the conditions that must hold",
		"Conventions: read CLAUDE.md or AGENTS.md and follow it",
		"Report: reply in these three lines and nothing else",
		"Changed: the files changed",
		"Checks: each command run and whether it passed",
		"Open: anything not done or not certain",
		"Review each piece by its diff alone",
		"review the complete change as one diff",
		"A sub-agent's report that checks passed is not your verification.",
	} {
		if !strings.Contains(instruction, point) {
			t.Errorf("the Opus-main instruction does not say %q:\n%s", point, instruction)
		}
	}
}

// A Sonnet developer with Opus sub-agents consults them for planning and hard
// calls and never hands them the writing.
func TestTheSonnetMainInstructionConsultsOpusOnlyForPlanning(t *testing.T) {
	t.Parallel()

	instruction := instructionFor(t, "sonnet", "opus")
	for _, point := range []string{
		"Use a sub-agent as an adviser, never as a writer",
		"Before you write code, ask a sub-agent to review the plan.",
		"Consult a sub-agent again only for a hard call",
		"Never hand a sub-agent the writing of code, the running of checks, or the reading of files.",
		"Before you submit, read the complete change and run the checks your instructions name.",
	} {
		if !strings.Contains(instruction, point) {
			t.Errorf("the Sonnet-main instruction does not say %q:\n%s", point, instruction)
		}
	}
	if strings.Contains(instruction, "Hand the writing") {
		t.Errorf("the Sonnet-main instruction hands the writing over:\n%s", instruction)
	}
}

// The instruction says the four things the design requires of it.
func TestTheSubagentInstructionCarriesTheDesignsFourPoints(t *testing.T) {
	t.Parallel()

	instruction := instructionOf(t)
	for _, point := range []string{
		// Plan the change on the main model.
		"Plan the change yourself: read the work item, the design it names and the code it touches, and decide what has to change and in what order.",
		// Hand the writing of code over, one bounded piece at a time.
		"Hand the writing of code to sub-agents, one bounded piece at a time, telling each which files to change, what the change must do, and how to check it.",
		"Write code yourself only where handing it over would cost more than writing it, such as a change of a few lines.",
		// Read each piece before handing out the next.
		"Read what each sub-agent changed before you hand out the next piece.",
		// Verify the whole change before submitting.
		"Before you submit, verify the whole change yourself: read the complete change and run the checks your instructions name.",
		"A sub-agent's account that checks passed is not your verification.",
	} {
		if !strings.Contains(instruction, point) {
			t.Errorf("the instruction does not say %q:\n%s", point, instruction)
		}
	}
}

// Each instruction is its Markdown file and nothing else: no Go source in this
// package carries its wording, and the developer persona the template ships
// does not either.
func TestTheSubagentInstructionLivesOnlyInItsFile(t *testing.T) {
	t.Parallel()

	for _, phrase := range []string{
		"Hand the writing of code to sub-agents",
		"Hand the exploration to a sub-agent",
		"Use a sub-agent as an adviser, never as a writer",
	} {
		instructionPhraseLivesOnlyInItsFile(t, phrase)
	}
}

func instructionPhraseLivesOnlyInItsFile(t *testing.T, phrase string) {
	t.Helper()
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), phrase) {
			t.Errorf("%s carries the sub-agent instruction's wording %q; it belongs in its Markdown file", source, phrase)
		}
	}
	persona, err := os.ReadFile(filepath.Join("..", "config", "builtin", "v1", "personas", "developer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persona), phrase) {
		t.Errorf("the developer persona carries the sub-agent instruction's wording %q", phrase)
	}
}

// The instruction follows the persona's section and the prompt is otherwise
// unchanged.
func TestTheSubagentInstructionFollowsThePersona(t *testing.T) {
	t.Parallel()

	instruction := instructionOf(t)
	prompt := developerPrompt(instructionTestPersona, "", "# Work item\n\nthe item\n", "/scratch", []string{"make test"})
	instructed := withSubagentInstruction(prompt, instructionTestPersona, instruction)
	persona := strings.Index(instructed, "Prefer the smallest change")
	at := strings.Index(instructed, instruction)
	item := strings.Index(instructed, "# Work item")
	if persona < 0 || at < persona || item < at {
		t.Fatalf("persona at %d, instruction at %d, work item at %d; want them in that order:\n%s", persona, at, item, instructed)
	}
	if strings.Replace(instructed, instruction+"\n\n", "", 1) != prompt {
		t.Fatalf("adding the instruction changed the rest of the prompt:\n%s", instructed)
	}
	// A prompt with no persona section, such as a repair resuming a session,
	// carries it at its end.
	repair := "the repair prompt"
	if got := withSubagentInstruction(repair, instructionTestPersona, instruction); !strings.HasPrefix(got, repair) || !strings.HasSuffix(strings.TrimSpace(got), instruction) {
		t.Fatalf("a prompt without the persona got:\n%s", got)
	}
}

// A work item that names the instruction's file has its text quoted in the
// context bundle. A quotation is not the instruction, so the invocation is still
// given it straight after the persona.
func TestAPromptQuotingTheSubagentInstructionIsStillGivenIt(t *testing.T) {
	t.Parallel()

	instruction := instructionOf(t)
	bundle := "# Work item\n\n## Referenced file: internal/orchestrator/developer-sub-agents.md\n\n" + instruction + "\n"
	prompt := developerPrompt(instructionTestPersona, "", bundle, "/scratch", []string{"make test"})
	instructed := withSubagentInstruction(prompt, instructionTestPersona, instruction)
	section := personaSection(instructionTestPersona)
	if !strings.Contains(instructed, section+instruction+"\n\n") {
		t.Fatalf("the instruction does not come straight after the persona:\n%s", instructed)
	}
	if got := strings.Count(instructed, instruction); got != 2 {
		t.Fatalf("the prompt carries the text %d time(s), want the instruction and the quotation:\n%s", got, instructed)
	}
	if !strings.HasSuffix(instructed, bundle) {
		t.Fatalf("the quotation in the work item changed:\n%s", instructed)
	}
}

// With sub-agents on, every developer invocation that has them is given the
// instruction for its arrangement of models, after its persona, and the run's
// record notes which one it was and the build it came from. The reviewer is
// never told.
func TestARunWithSubagentsOnIsGivenTheInstructionAndRecordsIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, main, sub, file string
	}{
		{name: "an Opus developer with Sonnet sub-agents", main: "opus", sub: "sonnet", file: opusMainInstructionFile},
		{name: "a Sonnet developer with Opus sub-agents", main: "sonnet", sub: "opus", file: sonnetMainInstructionFile},
		{name: "an Opus developer with Opus sub-agents", main: "opus", sub: "opus", file: subagentInstructionFile},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			instruction := instructionFor(t, test.main, test.sub)
			provider, _, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
				if pipeline == nil {
					return
				}
				pipeline.Build = instructionTestBuild
				withDeveloper(agents, func(agent *config.AgentConfig) {
					agent.Model = test.main
					agent.Persona = config.Persona{Version: "v1", Path: "personas/developer.md", Text: instructionTestPersona}
					agent.Subagents = config.Subagents{Enabled: true, Model: test.sub}
				})
			})
			attempts := provider.RequestsForRole(domain.RoleDeveloper)
			if len(attempts) < 2 {
				t.Fatalf("the run made %d developer attempt(s), want the first and its repair", len(attempts))
			}
			for index, request := range attempts {
				if request.Subagents == nil {
					t.Fatalf("developer attempt %d was not given sub-agents", index)
				}
				if request.Model != test.main || request.Subagents.Model != test.sub {
					t.Fatalf("developer attempt %d ran on %q with sub-agents on %q, want %q and %q", index, request.Model, request.Subagents.Model, test.main, test.sub)
				}
				if strings.Count(request.Prompt, instruction) != 1 {
					t.Fatalf("developer attempt %d carries its instruction %d time(s), want once:\n%s", index, strings.Count(request.Prompt, instruction), request.Prompt)
				}
				for _, other := range everyInstruction(t) {
					if other != instruction && strings.Contains(request.Prompt, other) {
						t.Fatalf("developer attempt %d carries another arrangement's instruction too:\n%s", index, request.Prompt)
					}
				}
			}
			first := attempts[0].Prompt
			if persona, at := strings.Index(first, "Prefer the smallest change"), strings.Index(first, instruction); persona < 0 || at < persona {
				t.Fatalf("the instruction does not follow the persona: persona at %d, instruction at %d", persona, at)
			}
			for _, request := range provider.RequestsForRole(domain.RoleReviewer) {
				for _, given := range everyInstruction(t) {
					if strings.Contains(request.Prompt, given) {
						t.Fatal("the reviewer was given a sub-agent instruction")
					}
				}
			}
			state, err := pipeline.Store.Load(outcome.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Subagents == nil || state.Subagents.Instruction == nil {
				t.Fatalf("the run's record does not note the instruction: %+v", state.Subagents)
			}
			given := state.Subagents.Instruction
			if given.File != test.file || given.Build != instructionTestBuild || given.At.IsZero() {
				t.Fatalf("the record notes the instruction as %+v, want %s", given, test.file)
			}
			if says := state.SubagentsSays(); !strings.Contains(says, "given the built-in sub-agent instruction "+test.file+" from build 0123456789ab") {
				t.Fatalf("the run says %q", says)
			}
		})
	}
}

// With sub-agents off, no invocation is given a sub-agent instruction and the
// record notes nothing.
func TestARunWithSubagentsOffIsNotGivenTheInstruction(t *testing.T) {
	t.Parallel()

	provider, _, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		withDeveloper(agents, func(agent *config.AgentConfig) {
			agent.Persona = config.Persona{Version: "v1", Path: "personas/developer.md", Text: instructionTestPersona}
		})
	})
	for _, request := range provider.RequestsMade() {
		for _, instruction := range everyInstruction(t) {
			if strings.Contains(request.Prompt, instruction) {
				t.Fatalf("the %s was given a sub-agent instruction with sub-agents off", request.Role)
			}
		}
	}
	state, err := pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Subagents != nil {
		t.Fatalf("a run with sub-agents off recorded %+v", state.Subagents)
	}
}

// Sub-agents turned on for a run on a provider that does not offer them leave
// the run without the instruction too, and its record notes none.
func TestARunOnAProviderWithoutSubagentsIsNotGivenTheInstruction(t *testing.T) {
	t.Parallel()

	provider, _, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		withDeveloper(agents, func(agent *config.AgentConfig) {
			agent.Backend, agent.Model = domain.BackendCodex, "gpt-6.1-sol"
			agent.Failover = config.Failover{Enabled: true, Provider: domain.BackendClaudeCode, Model: "opus"}
			agent.Subagents = config.Subagents{Enabled: true}
		})
	})
	for _, request := range provider.RequestsForRole(domain.RoleDeveloper) {
		for _, instruction := range everyInstruction(t) {
			if strings.Contains(request.Prompt, instruction) {
				t.Fatal("a Codex developer was given a sub-agent instruction")
			}
		}
	}
	state, err := pipeline.Store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Subagents == nil || state.Subagents.Instruction != nil {
		t.Fatalf("recorded sub-agents %+v, want the setting with no instruction noted", state.Subagents)
	}
}

// A run given an instruction keeps the note when it is moved to another
// developer slot, whatever that slot says about sub-agents, and its next
// invocation is given the instruction for its arrangement — here an Opus
// developer with Sonnet sub-agents — only where the new slot turns them on.
func TestARunMovedToAnotherSlotKeepsTheNoteOfTheInstruction(t *testing.T) {
	t.Parallel()

	instruction := instructionFor(t, testDeveloperModel, config.DefaultSubagentModel)
	on, off := true, false
	for _, test := range []struct {
		name  string
		slot  domain.DeveloperSlot
		given bool
		says  string
	}{
		{name: "a slot stating nothing", slot: domain.DeveloperSlot{}, says: "an earlier attempt was given the built-in sub-agent instruction developer-sub-agents.md from build 0123456789ab"},
		{name: "a slot turning them off", slot: domain.DeveloperSlot{Subagents: &domain.SlotSubagents{Enabled: &off}}, says: "an earlier attempt was given the built-in sub-agent instruction developer-sub-agents.md from build 0123456789ab"},
		{name: "a slot turning them on", slot: domain.DeveloperSlot{Subagents: &domain.SlotSubagents{Enabled: &on}}, given: true, says: "given the built-in sub-agent instruction developer-sub-agents-opus-main.md from build fedcba987654"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
			built, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			pipeline := &built
			pipeline.Backend = modelUsageReportingBackend{Backend: provider}
			pipeline.Build = "fedcba9876543210fedcba9876543210fedcba98"
			pipeline.Config.Execution.MaxConcurrentDevelopers = 3
			pipeline.Config.Execution.DeveloperSlots = []domain.DeveloperSlot{{}, test.slot, {Subagents: &domain.SlotSubagents{Enabled: &on}}}
			withDeveloper(pipeline.Config.Agents, func(agent *config.AgentConfig) {
				agent.Persona = config.Persona{Version: "v1", Path: "personas/developer.md", Text: instructionTestPersona}
			})

			told := runstate.SubagentInstruction{File: subagentInstructionFile, Build: instructionTestBuild, At: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
			stopped := approvedStoppedState()
			stopped.LastSequence = 4
			stopped.Subagents = &runstate.SubagentRecord{Enabled: true, Model: config.DefaultSubagentModel, Slot: 3, Instruction: &told}
			if err := store.Create(stopped); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			stopped, err := store.ClaimSlot(ctx, stopped, 3, 3, time.Now())
			if err != nil {
				t.Fatalf("ClaimSlot() error = %v", err)
			}
			for index, number := range []int{1, 3} {
				occupant := approvedStoppedState()
				occupant.RunID, occupant.WorkItemID = "run-"+strings.Repeat(string(rune('a'+index)), 32), "yoyodyne-occupant-"+strconv.Itoa(index)
				occupant.Status, occupant.Phase, occupant.CompletedAt = runstate.StatusRunning, runstate.PhaseDeveloping, nil
				occupant.IntegrationStop, occupant.Environmental, occupant.Failure = nil, nil, ""
				if err := store.Create(occupant); err != nil {
					t.Fatalf("Create(occupant) error = %v", err)
				}
				if _, err := store.ClaimSlot(ctx, occupant, number, 3, time.Now()); err != nil {
					t.Fatalf("ClaimSlot(occupant in %d) error = %v", number, err)
				}
			}
			live := stopped
			live.Status, live.Phase, live.CompletedAt = runstate.StatusRunning, runstate.PhaseDeveloping, nil
			live.IntegrationStop, live.Environmental, live.Failure = nil, nil, ""
			if err := store.ReserveLive(ctx, live, 3); err != nil {
				t.Fatalf("ReserveLive() error = %v", err)
			}
			taken, err := store.Load(stopped.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if taken.RecordedSlot() != 2 {
				t.Fatalf("taken up in slot %d, want slot 2", taken.RecordedSlot())
			}

			run := &activeRun{pipeline: *pipeline, state: taken}
			run.worktree.Path = repository
			if _, err := run.attemptDevelopment(ctx, developerPrompt(instructionTestPersona, "", "# Work item\n", "/scratch", nil), ""); err != nil {
				t.Fatalf("attemptDevelopment() error = %v", err)
			}
			requests := provider.RequestsForRole(domain.RoleDeveloper)
			if len(requests) != 1 {
				t.Fatalf("%d developer invocation(s), want one", len(requests))
			}
			if got := strings.Contains(requests[0].Prompt, instruction); got != test.given {
				t.Fatalf("the invocation carries the instruction: %t, want %t", got, test.given)
			}
			if got := requests[0].Subagents != nil; got != test.given {
				t.Fatalf("the invocation has sub-agents: %t, want %t", got, test.given)
			}
			if err := store.Save(run.state); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			saved, err := store.Load(stopped.RunID)
			if err != nil {
				t.Fatal(err)
			}
			wantFile, wantBuild := subagentInstructionFile, instructionTestBuild
			if test.given {
				wantFile, wantBuild = opusMainInstructionFile, pipeline.Build
			}
			if saved.Subagents == nil || saved.Subagents.Instruction == nil || saved.Subagents.Instruction.File != wantFile {
				t.Fatalf("the record lost the note of the instruction, or names the wrong one: %+v", saved.Subagents)
			}
			if saved.Subagents.Instruction.Build != wantBuild {
				t.Fatalf("the record names build %q, want %q", saved.Subagents.Instruction.Build, wantBuild)
			}
			if saved.Subagents.Slot != 2 {
				t.Fatalf("the record names slot %d, want the one the run is in", saved.Subagents.Slot)
			}
			if says := saved.SubagentsSays(); !strings.Contains(says, test.says) {
				t.Fatalf("the run says %q, want %q", says, test.says)
			}
		})
	}
}
