package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const projectWithSweep = `version: 1
extends: builtin:v1
product:
  id: example
  repository: .
recurring_tasks:
  development-manager-sweep:
    role: development-manager
    every: 1h
    enabled: true
    max_turns: 4
    prompt: |
      Sweep for unresolved issues and fix what your authority allows.
`

func TestRecurringTaskLoadsAsRoleCadenceAndPrompt(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, projectWithSweep, nil).Config
	task, err := cfg.RecurringTaskNamed("development-manager-sweep")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if task.Role != domain.RoleDevelopmentManager {
		t.Errorf("role = %q, want %q", task.Role, domain.RoleDevelopmentManager)
	}
	if task.Every.Duration() != time.Hour {
		t.Errorf("every = %s, want 1h", task.Every)
	}
	if !task.Enabled {
		t.Error("enabled = false, want the configured task switched on")
	}
	if task.Turns() != 4 {
		t.Errorf("turns = %d, want the configured 4", task.Turns())
	}
	if !strings.Contains(task.Prompt, "Sweep for unresolved issues") {
		t.Errorf("prompt = %q, want what the project wrote", task.Prompt)
	}
}

// A project that schedules nothing is the default and stays it: the capability
// is opted in to, and a configuration that never mentions it schedules nothing
// rather than inheriting somebody's idea of a sensible cadence.
func TestProjectWithoutRecurringTasksSchedulesNothing(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	if len(cfg.RecurringTasks) != 0 {
		t.Errorf("recurring tasks = %v, want a project that schedules nothing", cfg.RecurringTasks)
	}
	if _, err := cfg.RecurringTaskNamed("anything"); err == nil {
		t.Error("RecurringTaskNamed() on an unscheduled project returned no error")
	}
}

// The invariant this schema is held to, asserted rather than described: no key
// here grants anything. The strict loader is what enforces it, so the check is
// that the authority-shaped names somebody would reach for first are refused as
// keys that do not exist.
func TestRecurringTaskCannotGrantAuthority(t *testing.T) {
	t.Parallel()

	for _, granting := range []string{
		"    capabilities:\n      - work-item-mutate\n",
		"    tools:\n      - bash\n",
		"    account: other\n",
		"    grants:\n      - direct-work\n",
	} {
		_, err := loadProjectError(t, strings.Replace(projectWithSweep, "    max_turns: 4\n", granting, 1), nil)
		if err == nil {
			t.Fatalf("a recurring task carrying %q loaded, and configuration must not be able to widen authority", strings.TrimSpace(granting))
		}
	}
}

// A task may name the model its turns ask for, and one that does not leaves the
// role's own model in charge — which is every task written before the key.
func TestRecurringTaskNamesItsOwnModel(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, strings.Replace(projectWithSweep, "    max_turns: 4\n", "    max_turns: 4\n    model: sonnet\n", 1), nil).Config
	task, err := cfg.RecurringTaskNamed("development-manager-sweep")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if task.ModelSelector() != "sonnet" {
		t.Errorf("model = %q, want the configured sonnet", task.ModelSelector())
	}

	unnamed, err := loadProject(t, projectWithSweep, nil).Config.RecurringTaskNamed("development-manager-sweep")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if unnamed.ModelSelector() != "" {
		t.Errorf("model = %q, want none named, so the role's model applies", unnamed.ModelSelector())
	}
}

// The task's model is held to the rule an agent's model is, and refused with the
// same reason, so a selector that could never name a model is found at load
// rather than on the first firing.
func TestRecurringTaskModelIsValidatedAsAnAgentsModelIs(t *testing.T) {
	t.Parallel()

	for name, model := range map[string]string{
		"two words":           "    model: \"son net\"\n",
		"a flag":              "    model: \"--sonnet\"\n",
		"longer than allowed": "    model: " + strings.Repeat("s", MaxModelSelectorBytes+1) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadProjectError(t, strings.Replace(projectWithSweep, "    max_turns: 4\n", "    max_turns: 4\n"+model, 1), nil)
			if err == nil {
				t.Fatalf("a recurring task whose model is %s loaded", name)
			}
			if !strings.Contains(err.Error(), `recurring task "development-manager-sweep" model selector`) {
				t.Errorf("error = %v, want it to name the task and the agent model rule's reason", err)
			}
		})
	}
}

func TestRecurringTaskRefusesWhatCouldNeverRun(t *testing.T) {
	t.Parallel()

	for name, replacement := range map[string]struct{ from, to string }{
		"a cadence below the floor":    {"    every: 1h\n", "    every: 30s\n"},
		"a role nobody fills":          {"    role: development-manager\n", "    role: nobody\n"},
		"more turns than the bound":    {"    max_turns: 4\n", "    max_turns: 99\n"},
		"a name that is no identifier": {"  development-manager-sweep:\n", "  Development Manager Sweep:\n"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadProjectError(t, strings.Replace(projectWithSweep, replacement.from, replacement.to, 1), nil)
			if err == nil {
				t.Fatalf("%s loaded, and it describes a task that could never run", name)
			}
		})
	}
}

// A prompt is the whole of what the harness has to say to the role, so a task
// with none wakes it for nothing. It is refused rather than fired with an empty
// message, which the role would answer by asking what was wanted.
func TestRecurringTaskRequiresSomethingToSay(t *testing.T) {
	t.Parallel()

	empty := strings.Replace(projectWithSweep,
		"    prompt: |\n      Sweep for unresolved issues and fix what your authority allows.\n",
		"    prompt: \"\"\n", 1)
	_, err := loadProjectError(t, empty, nil)
	if err == nil {
		t.Fatal("a recurring task with no prompt loaded")
	}
	if !strings.Contains(err.Error(), "prompt is required") {
		t.Errorf("error = %v, want it to name the missing prompt", err)
	}
}

// Turns default rather than being required, because a project that did not think
// about the bound still gets iteration: a heavy pass that says it has more to do
// is given another turn instead of being truncated at one.
func TestRecurringTaskIteratesByDefault(t *testing.T) {
	t.Parallel()

	if turns := (RecurringTask{}).Turns(); turns != DefaultRecurringTurns {
		t.Errorf("turns = %d, want the default %d", turns, DefaultRecurringTurns)
	}
	if DefaultRecurringTurns < 2 {
		t.Errorf("DefaultRecurringTurns = %d, and a default below 2 is no iteration at all", DefaultRecurringTurns)
	}
}

// The scheduled and the unscheduled task are named in one order whatever order a
// map hands them back in, because which task a pass fires must be decided by the
// schedule rather than by map iteration.
func TestRecurringTaskNamesAreOrdered(t *testing.T) {
	t.Parallel()

	cfg := Config{RecurringTasks: map[string]RecurringTask{
		"second-task": {},
		"first-task":  {},
		"third-task":  {},
	}}
	names := cfg.RecurringTaskNames()
	want := []string{"first-task", "second-task", "third-task"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}

// The generated file shows the shape, switched off. An operator given no sign of
// the schema has to be told it by somebody, which is the state the commented
// Slack and operators sections above it exist to end.
func TestScaffoldShowsTheRecurringSectionCommented(t *testing.T) {
	t.Parallel()

	resolved := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."})
	rendered, err := os.ReadFile(resolved.Path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, want := range []string{
		"# recurring_tasks:\n",
		"#   development-manager-sweep:\n",
		"#     role: development-manager\n",
		"#     every: 1h\n",
	} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("generated configuration does not show %q:\n%s", want, rendered)
		}
	}
	if len(resolved.Config.RecurringTasks) != 0 {
		t.Errorf("recurring tasks = %v, want a generated project that schedules nothing", resolved.Config.RecurringTasks)
	}
}

// The examples are only worth showing if the gesture they ask for works, so the
// block is uncommented here and loaded: an example that does not load is worse
// than none, because the operator who tried it has no reason to think the fault
// is the file's.
//
// Both are checked, and the second is the one that would otherwise rot
// unnoticed: a scaffolded block whose first entry loads passes a test that only
// asks about the first entry, whatever happened to the rest of it.
func TestScaffoldedRecurringExampleLoadsWhenUncommented(t *testing.T) {
	t.Parallel()

	resolved := loadScaffoldEdited(t, ScaffoldOptions{ProductID: "example", Repository: "."}, func(content string) string {
		return uncommentScaffoldBlock(t, content, "recurring_tasks:")
	})
	task, err := resolved.Config.RecurringTaskNamed("development-manager-sweep")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if task.Role != domain.RoleDevelopmentManager || task.Every.Duration() != time.Hour || !task.Enabled {
		t.Errorf("task = %+v, want the development manager woken hourly", task)
	}
	if !strings.Contains(task.Prompt, "root-cause work") {
		t.Errorf("prompt = %q, want the filing instruction the example carries", task.Prompt)
	}
	// The pile's own standing reader. Every role files reports and only the
	// product manager can record what became of one, so a project that schedules
	// nothing works the pile only when somebody opens a conversation.
	triage, err := resolved.Config.RecurringTaskNamed("report-triage")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if triage.Role != domain.RoleProductManager || triage.Every.Duration() != time.Hour || !triage.Enabled {
		t.Errorf("task = %+v, want the product manager woken hourly for the pile", triage)
	}
	// The instruction that makes the pass drain the pile rather than read it: a
	// turn that decides about nothing leaves every report it was shown unhandled
	// and offered again.
	if !strings.Contains(triage.Prompt, `"handle"`) {
		t.Errorf("prompt = %q, want the instruction to record what became of each report", triage.Prompt)
	}
	// Both passes end in an account a person reads, so both are told to name the
	// work they cite by what it is rather than by its number alone.
	for _, prompt := range []string{task.Prompt, triage.Prompt} {
		if !namesWorkItemsByWhatTheyAre(prompt) {
			t.Errorf("prompt = %q, want the rule that a work item is named by what it is", prompt)
		}
		// Both passes decide things, so both are told to decide what their
		// authority covers rather than put it to the operator for approval.
		if !decidesAndReports(prompt) {
			t.Errorf("prompt = %q, want the rule that a decision the role can make is made and reported afterwards", prompt)
		}
	}
	// The queue's own standing reader. A change proposed to a design reaches
	// the architect only when somebody opens her conversation, and forty-four
	// stood undecided before this pass existed to be configured.
	amendments, err := resolved.Config.RecurringTaskNamed("architect-amendments")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if amendments.Role != domain.RoleArchitect || amendments.Every.Duration() != 6*time.Hour || !amendments.Enabled {
		t.Errorf("task = %+v, want the architect woken every six hours for the queue", amendments)
	}
	// The instruction that makes the pass argue the queue rather than read it,
	// and the line that keeps a recommendation from being read as a decision.
	for _, want := range []string{`"recommendations"`, "You decide nothing"} {
		if !strings.Contains(amendments.Prompt, want) {
			t.Errorf("prompt = %q, want %q", amendments.Prompt, want)
		}
	}
	// Its account is read by a person too. It is not told to decide and report,
	// because every decision on a proposed change is the operator's to record.
	if !namesWorkItemsByWhatTheyAre(amendments.Prompt) {
		t.Errorf("prompt = %q, want the rule that a work item is named by what it is", amendments.Prompt)
	}
	architect, err := resolved.Config.RecurringTaskNamed("architect-pass")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if architect.Role != domain.RoleArchitect || architect.Every.Duration() != time.Hour || !architect.Enabled {
		t.Errorf("task = %+v, want the commented architect pass at 1h", architect)
	}
	if !strings.Contains(architect.Prompt, "priority first, then age within each priority") || strings.Contains(strings.ToLower(architect.Prompt), "oldest first") {
		t.Errorf("prompt = %q, want priority then age", architect.Prompt)
	}
	// The Lead Product Manager's sweep carries the audit of closed work as its
	// third job: what it records, the correction it admits, the bound on how many,
	// and the program managers' audits it reads rather than repeats.
	sweep, err := resolved.Config.RecurringTaskNamed("product-manager-sweep")
	if err != nil {
		t.Fatalf("RecurringTaskNamed() error = %v", err)
	}
	if sweep.Role != domain.RoleProductManager || !sweep.Enabled {
		t.Errorf("task = %+v, want the Lead Product Manager's sweep", sweep)
	}
	flat := strings.Join(strings.Fields(sweep.Prompt), " ")
	for _, want := range []string{
		"Three jobs this pass.",
		"Third, audit the work closed since your last pass",
		"against every standing goal",
		"audits",
		`priority 0 naming the closed items it corrects in "corrects"`,
		"widen the open correction",
		"At most three corrections a pass, taking the violation shared by the most closed items first; mark the rest deferred.",
		"a program manager whose lane is a standing goal already audited an item",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("prompt = %q, want %q", flat, want)
		}
	}
	if !namesWorkItemsByWhatTheyAre(sweep.Prompt) || !decidesAndReports(sweep.Prompt) {
		t.Errorf("prompt = %q, want the naming and decide-and-report rules", sweep.Prompt)
	}
}

func TestThisProjectsArchitectPassUsesPriorityThenAgeEvery45Minutes(t *testing.T) {
	t.Parallel()
	path := "../../.yoyodyne/config.yaml"
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	task, err := cfg.RecurringTaskNamed("architect-pass")
	if err != nil {
		t.Fatal(err)
	}
	if task.Role != domain.RoleArchitect || !task.Enabled || task.Every.Duration() != 45*time.Minute {
		t.Errorf("architect pass = %+v, want enabled every 45m", task)
	}
	if !strings.Contains(task.Prompt, "priority first, then age within each priority") || strings.Contains(strings.ToLower(task.Prompt), "oldest first") {
		t.Errorf("prompt = %q, want priority then age", task.Prompt)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, rationale := range []string{"51 to 56 minutes", "92 to 95 percent cached", "57 to 74", "not a provider guarantee"} {
		if !strings.Contains(string(content), rationale) {
			t.Errorf("the cadence has lost the observed cache rationale %q", rationale)
		}
	}
}

func TestMinimumCadenceIsAboveTheAccident(t *testing.T) {
	t.Parallel()

	// The floor exists to catch "1m" written where "1h" was meant, so it has to
	// be above the plausible typo and below any cadence a sweep of a domain is
	// worth running at.
	if MinRecurringInterval <= time.Minute {
		t.Errorf("MinRecurringInterval = %s, which does not catch a minute typed for an hour", MinRecurringInterval)
	}
}
