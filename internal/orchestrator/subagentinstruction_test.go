package orchestrator

// A run with sub-agents on is told to hand the coding to them, by the
// instruction built into the program from developer-sub-agents.md, placed after
// the developer persona and noted on the run's record with the build it came
// from; a run with them off, or on a provider that does not offer them, is told
// nothing of the kind (docs/designs/developer-sub-agents.md).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const instructionTestPersona = "# Developer persona\n\nPrefer the smallest change that satisfies the criteria.\n"

const instructionTestBuild = "0123456789abcdef0123456789abcdef01234567"

func instructionOf(t *testing.T) string {
	t.Helper()
	instruction := strings.TrimSpace(subagentInstruction)
	if instruction == "" {
		t.Fatal("the built-in sub-agent instruction is empty")
	}
	return instruction
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

// The instruction is the Markdown file and nothing else: no Go source in this
// package carries its wording, and the developer persona the template ships
// does not either.
func TestTheSubagentInstructionLivesOnlyInItsFile(t *testing.T) {
	t.Parallel()

	phrase := "Hand the writing of code to sub-agents"
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
			t.Errorf("%s carries the sub-agent instruction's wording; it belongs in %s", source, subagentInstructionFile)
		}
	}
	persona, err := os.ReadFile(filepath.Join("..", "config", "builtin", "v1", "personas", "developer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persona), phrase) {
		t.Errorf("the developer persona carries the sub-agent instruction")
	}
}

// The instruction follows the persona's section and the prompt is otherwise
// unchanged.
func TestTheSubagentInstructionFollowsThePersona(t *testing.T) {
	t.Parallel()

	instruction := instructionOf(t)
	prompt := developerPrompt(instructionTestPersona, "", "# Work item\n\nthe item\n", "/scratch", []string{"make test"})
	instructed := withSubagentInstruction(prompt, instructionTestPersona)
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
	if got := withSubagentInstruction(repair, instructionTestPersona); !strings.HasPrefix(got, repair) || !strings.HasSuffix(strings.TrimSpace(got), instruction) {
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
	instructed := withSubagentInstruction(prompt, instructionTestPersona)
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

// With sub-agents on, every developer invocation that has them is told to hand
// the coding over, after its persona, and the run's record notes that it was
// and the build the instruction came from. The reviewer is never told.
func TestARunWithSubagentsOnIsGivenTheInstructionAndRecordsIt(t *testing.T) {
	t.Parallel()

	instruction := instructionOf(t)
	provider, _, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		pipeline.Build = instructionTestBuild
		withDeveloper(agents, func(agent *config.AgentConfig) {
			agent.Persona = config.Persona{Version: "v1", Path: "personas/developer.md", Text: instructionTestPersona}
			agent.Subagents = config.Subagents{Enabled: true}
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
		if strings.Count(request.Prompt, instruction) != 1 {
			t.Fatalf("developer attempt %d carries the instruction %d time(s), want once:\n%s", index, strings.Count(request.Prompt, instruction), request.Prompt)
		}
	}
	first := attempts[0].Prompt
	if persona, at := strings.Index(first, "Prefer the smallest change"), strings.Index(first, instruction); persona < 0 || at < persona {
		t.Fatalf("the instruction does not follow the persona: persona at %d, instruction at %d", persona, at)
	}
	for _, request := range provider.RequestsForRole(domain.RoleReviewer) {
		if strings.Contains(request.Prompt, instruction) {
			t.Fatal("the reviewer was given the sub-agent instruction")
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
	if given.File != "developer-sub-agents.md" || given.Build != instructionTestBuild || given.At.IsZero() {
		t.Fatalf("the record notes the instruction as %+v", given)
	}
	if says := state.SubagentsSays(); !strings.Contains(says, "told to hand the coding to them by the built-in instruction developer-sub-agents.md from build 0123456789ab") {
		t.Fatalf("the run says %q", says)
	}
}

// With sub-agents off, no invocation is told to hand the coding over and the
// record notes nothing.
func TestARunWithSubagentsOffIsNotGivenTheInstruction(t *testing.T) {
	t.Parallel()

	instruction := instructionOf(t)
	provider, _, outcome, pipeline := subagentRun(t, func(pipeline *Pipeline, agents map[string]config.AgentConfig) {
		if pipeline == nil {
			return
		}
		withDeveloper(agents, func(agent *config.AgentConfig) {
			agent.Persona = config.Persona{Version: "v1", Path: "personas/developer.md", Text: instructionTestPersona}
		})
	})
	for _, request := range provider.RequestsMade() {
		if strings.Contains(request.Prompt, instruction) {
			t.Fatalf("the %s was given the sub-agent instruction with sub-agents off", request.Role)
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

	instruction := instructionOf(t)
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
		if strings.Contains(request.Prompt, instruction) {
			t.Fatal("a Codex developer was given the sub-agent instruction")
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
