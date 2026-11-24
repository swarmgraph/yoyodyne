package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const laneRemit = "# Reliability lane\n\nWatch what keeps the line from stalling.\n"

// programManagerAgent is one program manager instance's block, indented to sit
// under `agents:`.
func programManagerAgent(name, lane, remitPath string) string {
	return `  ` + name + `:
    role: program-manager
    backend: claude-code
    model: opus
    lane: ` + lane + `
    remit:
      version: r1
      path: ` + remitPath + `
    triggers:
      every: 1h
      on: [landings, stoppages]
`
}

func TestAProgramManagerInstanceLoadsItsLaneRemitAndTriggers(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+"agents:\n"+programManagerAgent("reliability-pm", "reliability", "remits/reliability.md"),
		map[string]string{"remits/reliability.md": laneRemit})
	agent := resolved.Config.Agents["reliability-pm"]
	if got := resolved.Config.AgentLane("reliability-pm"); got != "reliability" {
		t.Fatalf("lane = %q, want reliability", got)
	}
	if agent.Remit.Text != laneRemit || agent.Remit.Version != "r1" || agent.Remit.Path != "remits/reliability.md" || agent.Remit.Bytes != len(laneRemit) {
		t.Fatalf("remit = %+v, want the file the block names", agent.Remit)
	}
	if agent.Triggers.Every.Duration() != time.Hour {
		t.Fatalf("triggers.every = %s, want 1h", agent.Triggers.Every)
	}
	if len(agent.Triggers.On) != 2 || agent.Triggers.On[0] != TriggerLandings || agent.Triggers.On[1] != TriggerStoppages {
		t.Fatalf("triggers.on = %v, want [landings stoppages]", agent.Triggers.On)
	}
	for _, key := range []string{"lane", "remit", "triggers"} {
		if resolved.Origins["agents.reliability-pm."+key] == "" {
			t.Errorf("agents.reliability-pm.%s has no origin recorded", key)
		}
	}
}

// A lane has one owner or it is not a lane, and the refusal names both agents
// and the lane, because either agent is the one somebody might move.
func TestTwoInstancesNamingOneLaneAreRefusedNamingBothAndTheLane(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+"agents:\n"+
		programManagerAgent("pm-one", "reliability", "remits/one.md")+
		programManagerAgent("pm-two", "reliability", "remits/two.md"),
		map[string]string{"remits/one.md": laneRemit, "remits/two.md": laneRemit})
	if err == nil {
		t.Fatal("LoadResolved() error = nil, want a shared lane refused")
	}
	for _, want := range []string{`"pm-one"`, `"pm-two"`, `lane "reliability"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %s", err, want)
		}
	}
}

func TestInstanceKeysOnAnyOtherRoleAreRefusedNamingTheKeyAndTheRole(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"lane": `    lane: reliability
`,
		"remit": `    remit:
      version: r1
      path: remits/reliability.md
`,
		"triggers": `    triggers:
      every: 1h
`,
	}
	for key, block := range cases {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			_, err := loadProjectError(t, minimalProjectConfig+"agents:\n  architect:\n"+block,
				map[string]string{"remits/reliability.md": laneRemit})
			if err == nil {
				t.Fatalf("LoadResolved() error = nil, want %s refused on an architect", key)
			}
			want := `agent "architect" sets ` + key + `, which only an agent of the program-manager role carries; its role is "architect"`
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

// A remit is held to every rule a persona is, and refused in the persona rules'
// own wording with the document's kind in place of "persona".
func TestARemitIsRefusedByThePersonaRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		files    map[string]string
		wantText string
	}{
		{name: "absent", path: "remits/missing.md", wantText: `resolve remit "remits/missing.md"`},
		{name: "oversized", path: "remits/big.md", files: map[string]string{"remits/big.md": strings.Repeat("x", MaxRemitBytes+1)},
			wantText: `remit "remits/big.md" is 32769 bytes, limit is 32768`},
		{name: "not markdown", path: "remits/lane.txt", files: map[string]string{"remits/lane.txt": laneRemit},
			wantText: `remit path "remits/lane.txt" must be a Markdown file`},
		{name: "traversal", path: "../lane.md",
			wantText: `remit path "../lane.md" must not traverse outside the project .yoyodyne directory`},
		{name: "absolute", path: "/etc/lane.md",
			wantText: `remit path "/etc/lane.md" must be relative to the project .yoyodyne directory`},
		{name: "empty", path: "remits/empty.md", files: map[string]string{"remits/empty.md": "  \n"},
			wantText: `agent "pm" remit "remits/empty.md" is empty`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadProjectError(t, minimalProjectConfig+"agents:\n"+programManagerAgent("pm", "reliability", tc.path), tc.files)
			if err == nil {
				t.Fatal("LoadResolved() error = nil, want the remit refused")
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("error = %v, want %q", err, tc.wantText)
			}
		})
	}
}

func TestARemitSymlinkedOutOfTheConfigurationDirectoryIsRefused(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig+"agents:\n"+programManagerAgent("pm", "reliability", "remits/lane.md"), nil)
	outside := filepath.Join(t.TempDir(), "lane.md")
	if err := os.WriteFile(outside, []byte(laneRemit), 0o600); err != nil {
		t.Fatal(err)
	}
	remits := filepath.Join(project, DirectoryName, "remits")
	if err := os.MkdirAll(remits, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(remits, "lane.md")); err != nil {
		t.Fatal(err)
	}
	_, err := LoadResolved(filepath.Join(project, DirectoryName, FileName))
	if err == nil || !strings.Contains(err.Error(), `remit "remits/lane.md" resolves outside`) {
		t.Fatalf("error = %v, want the remit refused as resolving outside the directory", err)
	}
}

func TestTriggersOutsideTheClosedSetOrUnderTheFloorAreRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		triggers string
		want     string
	}{
		{name: "unknown event", triggers: "      on: [landings, merges]\n",
			want: `agent "pm" triggers.on names "merges", which is not an event the harness wakes an instance on; the events are "landings", "admissions", "stoppages"`},
		{name: "repeated event", triggers: "      on: [landings, landings]\n",
			want: `agent "pm" triggers.on names "landings" twice`},
		{name: "under the floor", triggers: "      every: 1m\n",
			want: `agent "pm" triggers.every is 1m0s, and the shortest interval allowed is 5m0s`},
		{name: "a full-pass floor past the bound", triggers: "      every: 1h\n      full_pass_every: 13h\n",
			want: `agent "pm" triggers.full_pass_every is 13h0m0s, and it must be between 1h0m0s and 12h0m0s`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			block := `  pm:
    role: program-manager
    backend: claude-code
    model: opus
    lane: reliability
    triggers:
` + tc.triggers
			_, err := loadProjectError(t, minimalProjectConfig+"agents:\n"+block, nil)
			if err == nil {
				t.Fatal("LoadResolved() error = nil, want the triggers refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestALaneTheTrackerWouldNotCarryIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  pm:
    role: program-manager
    backend: claude-code
    model: opus
    lane: "two words"
`, nil)
	if err == nil || !strings.Contains(err.Error(), `agent "pm" lane: label "two words" is not an identifier`) {
		t.Fatalf("error = %v, want the lane held to the label rule", err)
	}
}

// A lane's label is put on everything its program manager files, so a lane
// named for the operator's raised mark would write that mark without his words.
func TestALaneNamedForTheOperatorsRaisedLabelIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  pm:
    role: program-manager
    backend: claude-code
    model: opus
    lane: raised-by-operator
`, nil)
	if err == nil || !strings.Contains(err.Error(), `agent "pm" lane is "raised-by-operator"`) {
		t.Fatalf("error = %v, want the lane refused for taking the operator's label", err)
	}
}

// A remit is guidance handed to every prompt, so editing one moves the revision
// the way editing a persona does; an agent carrying none digests as before.
func TestEditingARemitMovesTheConfigurationRevision(t *testing.T) {
	t.Parallel()

	contents := minimalProjectConfig + "agents:\n" + programManagerAgent("pm", "reliability", "remits/lane.md")
	before := loadProject(t, contents, map[string]string{"remits/lane.md": laneRemit}).Config.Revision()
	after := loadProject(t, contents, map[string]string{"remits/lane.md": laneRemit + "\nAlso watch the docket.\n"}).Config.Revision()
	if before == after {
		t.Fatalf("revision %s did not move when the remit text changed", before)
	}
}

// The instances a pull considers for a pass are every program manager agent:
// one with triggers is woken by them, and one with none only by a request for
// its pass, which the pull must consider to take.
func TestProgramManagerPassesAreEveryInstanceTriggeredOrNot(t *testing.T) {
	t.Parallel()

	quiet := `  quiet-pm:
    role: program-manager
    backend: claude-code
    model: opus
    lane: quiet
`
	resolved := loadProject(t, minimalProjectConfig+"agents:\n"+programManagerAgent("reliability-pm", "reliability", "remits/reliability.md")+quiet,
		map[string]string{"remits/reliability.md": laneRemit})
	passes := resolved.Config.ProgramManagerPasses()
	if len(passes) != 2 || passes["reliability-pm"].Lane != "reliability" || passes["quiet-pm"].Triggers.Defined() {
		t.Fatalf("passes = %+v, want the triggered instance and the one that runs only when asked", passes)
	}
}

// An instance's passes are recorded and paced under its own name, so a
// recurring task written under that name is refused rather than sharing one
// cadence with it.
func TestARecurringTaskNamedForATriggeredInstanceIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+"agents:\n"+programManagerAgent("reliability-pm", "reliability", "remits/reliability.md")+`recurring_tasks:
  reliability-pm:
    role: program-manager
    every: 1h
    enabled: true
    prompt: watch the lane
`, map[string]string{"remits/reliability.md": laneRemit})
	if err == nil || !strings.Contains(err.Error(), `recurring task "reliability-pm" has the name of a program manager instance`) {
		t.Fatalf("LoadResolved() error = %v, want the shared name refused", err)
	}
}

// The architect's wake over the needs-design label is recorded and paced under
// a name of its own, so a recurring task written under that name is refused
// rather than sharing one cadence with it.
func TestARecurringTaskNamedForTheNeedsDesignWakeIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`recurring_tasks:
  architect-needs-design:
    role: architect
    every: 1h
    enabled: true
    prompt: look at the labelled work
`, nil)
	if err == nil || !strings.Contains(err.Error(), `recurring task "architect-needs-design" has the name the architect's wake over the needs-design label`) {
		t.Fatalf("LoadResolved() error = %v, want the reserved name refused", err)
	}
}

// A program manager's triggers may name a model of their own, which the passes
// they start ask for; a block naming none leaves the agent's model to every
// pass, and the agent's own model is untouched either way.
func TestAProgramManagersTriggersMayNameTheirOwnModel(t *testing.T) {
	t.Parallel()

	withModel := strings.Replace(programManagerAgent("cheap-pm", "reliability", "remits/reliability.md"),
		"      on: [landings, stoppages]\n", "      on: [landings, stoppages]\n      model: sonnet\n", 1)
	resolved := loadProject(t, minimalProjectConfig+"agents:\n"+withModel+programManagerAgent("plain-pm", "writing", "remits/reliability.md"),
		map[string]string{"remits/reliability.md": laneRemit})
	cheap := resolved.Config.Agents["cheap-pm"]
	if cheap.Triggers.ModelSelector() != "sonnet" || cheap.Model != "opus" {
		t.Fatalf("cheap-pm triggers model = %q, model = %q; want sonnet for its passes and opus for the agent", cheap.Triggers.ModelSelector(), cheap.Model)
	}
	plain := resolved.Config.Agents["plain-pm"]
	if plain.Triggers.ModelSelector() != "" || plain.Model != "opus" {
		t.Fatalf("plain-pm triggers model = %q, model = %q; want none, leaving the agent's opus", plain.Triggers.ModelSelector(), plain.Model)
	}
}

// A trigger model may name a kind, which the passes ask for as the model the
// agent's provider names for that kind.
func TestATriggerModelNamingAKindResolvesToTheProvidersModelForThePasses(t *testing.T) {
	t.Parallel()

	byKind := strings.Replace(programManagerAgent("cheap-pm", "reliability", "remits/reliability.md"),
		"      on: [landings, stoppages]\n", "      on: [landings, stoppages]\n      model: cheaper\n", 1)
	resolved := loadProject(t, minimalProjectConfig+"agents:\n"+byKind, map[string]string{"remits/reliability.md": laneRemit})
	if written := resolved.Config.Agents["cheap-pm"].Triggers.ModelSelector(); written != "cheaper" {
		t.Fatalf("written triggers model = %q, want the kind as written", written)
	}
	if asked := resolved.Config.ProgramManagerPasses()["cheap-pm"].Triggers.ModelSelector(); asked != "sonnet" {
		t.Fatalf("passes ask for %q, want sonnet, Claude Code's cheaper model", asked)
	}
}

// A trigger model is refused when it cannot name a model at all, or when the
// agent's provider cannot serve it, and the refusal names the model.
func TestATriggerModelTheProviderCannotServeIsRefusedNamingIt(t *testing.T) {
	t.Parallel()

	codexAgent := func(model, effort string) string {
		return `  pm:
    role: program-manager
    backend: codex
    model: ` + model + `
    effort: ` + effort + `
    lane: reliability
    triggers:
      every: 1h
`
	}
	cases := []struct {
		name, agent, model string
		want               []string
	}{
		{name: "not a model name", agent: programManagerAgent("pm", "reliability", "remits/reliability.md"), model: "--dangerous",
			want: []string{`agent "pm" triggers.model`, `model selector "--dangerous" must be a single model name`}},
		{name: "not in the Codex catalog", agent: codexAgent("gpt-6-astra", "high"), model: "unlisted-family",
			want: []string{`agent "pm (triggers.model)"`, `Codex model "unlisted-family"`, "not established"}},
		{name: "a Codex model refusing the agent's effort", agent: codexAgent("gpt-6-astra", "ultra"), model: "gpt-6-luna",
			want: []string{`agent "pm (triggers.model)"`, `effort "ultra"`}},
		{name: "a kind the provider names no model for", agent: codexAgent("gpt-6-astra", "high"), model: "cheaper",
			want: []string{`agent "pm" triggers.model is "cheaper"`, "provider codex", "names no cheaper model in execution.model_kinds"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			agent := strings.Replace(tc.agent, "      every: 1h\n", "      every: 1h\n      model: "+tc.model+"\n", 1)
			_, err := loadProjectError(t, minimalProjectConfig+"agents:\n"+agent, map[string]string{"remits/reliability.md": laneRemit})
			if err == nil {
				t.Fatal("LoadResolved() error = nil, want the trigger model refused")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want %q", err, want)
				}
			}
		})
	}
}
