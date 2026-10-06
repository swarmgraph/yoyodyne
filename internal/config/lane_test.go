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

// The instances a pull considers for a pass are the program manager agents
// whose triggers say something; an instance with no triggers is woken by
// nobody.
func TestProgramManagerPassesAreTheInstancesTheirTriggersWake(t *testing.T) {
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
	if len(passes) != 1 || passes["reliability-pm"].Lane != "reliability" {
		t.Fatalf("passes = %+v, want the triggered instance alone", passes)
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
	if err == nil || !strings.Contains(err.Error(), `recurring task "reliability-pm" has the name of the program manager instance`) {
		t.Fatalf("LoadResolved() error = %v, want the shared name refused", err)
	}
}
