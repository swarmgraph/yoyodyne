package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Environments: what a project declares a run needs to judge its work items'
// criteria, such as a test client on one platform or a browser, each with a
// command that says whether it is usable and the integration test that judges
// the criteria it serves (docs/designs/checking-a-run-environment.md, section
// 1). A work item names the environments its criteria need on one line of its
// description beginning "Environments:" (section 4, EnvironmentsLine).
//
// Every rule here is one the configuration can be held to when it is read. An
// entry is the operator's to write and never a role's (RoleMayApply), because
// its check and its test are commands that run on this machine.

// DefaultEnvironmentTimeLimit is how long one run of an environment's check or
// test may take where the entry names no time_limit.
const DefaultEnvironmentTimeLimit = 15 * time.Minute

// EnvironmentPlace is where an environment's check and test run.
type EnvironmentPlace string

const (
	// EnvironmentInDeveloper runs them under exactly the restrictions the
	// developer session's own commands run under, so the developer can run the
	// same test itself. It is the default.
	EnvironmentInDeveloper EnvironmentPlace = "developer"
	// EnvironmentInHarness runs them where the project's declared checks run,
	// outside the developer's sandbox, for a tool that sandbox refuses.
	EnvironmentInHarness EnvironmentPlace = "harness"
)

// Environment is one declared environment.
type Environment struct {
	// Name is how a work item's Environments: line refers to it.
	Name string `yaml:"name" json:"name"`
	// Needs names install steps, by their names in Install, that must have
	// succeeded in the checkout before the check runs.
	Needs []string `yaml:"needs,omitempty" json:"needs,omitempty"`
	// Machine says the environment needs something installed for the whole
	// machine rather than in the checkout, which a person installs.
	Machine bool `yaml:"machine,omitempty" json:"machine,omitempty"`
	// HowToInstall is the plain instruction a person follows to install a
	// Machine prerequisite. The harness shows it as written and never runs it.
	HowToInstall string `yaml:"how_to_install,omitempty" json:"how_to_install,omitempty"`
	// Where is where the check and the test run. Empty is EnvironmentInDeveloper.
	Where EnvironmentPlace `yaml:"where,omitempty" json:"where,omitempty"`
	// Check is a short command that exits zero when the environment is usable.
	Check string `yaml:"check" json:"check"`
	// Test is the integration test that judges the criteria the environment
	// serves.
	Test string `yaml:"test" json:"test"`
	// TimeLimit bounds one run of Check or Test. Zero is
	// DefaultEnvironmentTimeLimit.
	TimeLimit Duration `yaml:"time_limit,omitempty" json:"time_limit,omitempty"`
}

// Place is where the environment's check and test run.
func (e Environment) Place() EnvironmentPlace {
	if strings.TrimSpace(string(e.Where)) == "" {
		return EnvironmentInDeveloper
	}
	return EnvironmentPlace(strings.TrimSpace(string(e.Where)))
}

// Limit is how long one run of the check or the test may take.
func (e Environment) Limit() time.Duration {
	if e.TimeLimit <= 0 {
		return DefaultEnvironmentTimeLimit
	}
	return e.TimeLimit.Duration()
}

// environmentProblems holds every entry to the rules the design gives it when
// the configuration is read, naming the entry in each: a name, no two entries
// sharing one, every need an install step the project declares, a
// how_to_install only on a machine environment, a where that is one of the two
// places, and a check and a test to run.
func environmentProblems(environments []Environment, install []InstallStep) []string {
	steps := map[string]bool{}
	for _, step := range install {
		steps[strings.TrimSpace(step.Name)] = true
	}
	var problems []string
	names := map[string]int{}
	for index, environment := range environments {
		label := fmt.Sprintf("environment %d", index)
		name := strings.TrimSpace(environment.Name)
		if name == "" {
			problems = append(problems, label+": name cannot be empty")
		} else {
			label = fmt.Sprintf("environment %q", name)
			if earlier, seen := names[name]; seen {
				problems = append(problems, fmt.Sprintf("%s: environment %d has the same name; a work item could not say which one it means", label, earlier))
			} else {
				names[name] = index
			}
		}
		for _, need := range environment.Needs {
			if !steps[strings.TrimSpace(need)] {
				problems = append(problems, fmt.Sprintf("%s: needs %q, which is not the name of an install step this project declares", label, need))
			}
		}
		if strings.TrimSpace(environment.HowToInstall) != "" && !environment.Machine {
			problems = append(problems, label+": how_to_install is set without machine: true; it is the instruction a person follows to install something for the whole machine, so say machine: true or leave it out")
		}
		if place := environment.Place(); place != EnvironmentInDeveloper && place != EnvironmentInHarness {
			problems = append(problems, fmt.Sprintf("%s: where is %q, and must be %q or %q", label, environment.Where, EnvironmentInDeveloper, EnvironmentInHarness))
		}
		if strings.TrimSpace(environment.Check) == "" {
			problems = append(problems, label+": check cannot be empty; it is the command that says whether the environment is usable")
		}
		if strings.TrimSpace(environment.Test) == "" {
			problems = append(problems, label+": test cannot be empty; it is the integration test that judges the criteria the environment serves")
		}
		if environment.TimeLimit < 0 {
			problems = append(problems, fmt.Sprintf("%s: time_limit cannot be negative; leave it out for %s", label, DefaultEnvironmentTimeLimit))
		}
	}
	return problems
}

// EnvironmentsLinePrefix begins the one line of a work item's description that
// names the environments its criteria need.
const EnvironmentsLinePrefix = "Environments:"

// EnvironmentNeed is one environment an item's Environments: line names, with
// the numbers of the criteria it serves.
type EnvironmentNeed struct {
	Name     string `json:"name"`
	Criteria []int  `json:"criteria,omitempty"`
}

// EnvironmentsLine is what an item's Environments: line says.
type EnvironmentsLine struct {
	// Line is the line as the description wrote it, trimmed, for quoting.
	Line  string            `json:"line"`
	Needs []EnvironmentNeed `json:"needs"`
}

// ReadEnvironmentsLine reads the Environments: line of a work item's
// description, such as "Environments: android client (criteria 1, 2); ios
// client (criterion 3)". An entry is a name, optionally followed by the
// criteria it serves in brackets. found is false for a description with no
// such line, which needs no environment. A line that cannot be read, or a
// description with more than one, is an error quoting the line.
func ReadEnvironmentsLine(description string) (EnvironmentsLine, bool, error) {
	var lines []string
	for _, line := range strings.Split(description, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, EnvironmentsLinePrefix) {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 {
		return EnvironmentsLine{}, false, nil
	}
	if len(lines) > 1 {
		return EnvironmentsLine{}, true, fmt.Errorf("the description has %d lines beginning %q, %q and %q among them; name every environment on one line", len(lines), EnvironmentsLinePrefix, lines[0], lines[1])
	}
	read := EnvironmentsLine{Line: lines[0]}
	body := strings.TrimSpace(strings.TrimPrefix(lines[0], EnvironmentsLinePrefix))
	for _, entry := range strings.Split(body, ";") {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		need, problem := readEnvironmentEntry(entry)
		if problem != "" {
			return EnvironmentsLine{}, true, fmt.Errorf("the line %q cannot be read: %s", read.Line, problem)
		}
		read.Needs = append(read.Needs, need)
	}
	if len(read.Needs) == 0 {
		return EnvironmentsLine{}, true, fmt.Errorf("the line %q names no environment; name one, or take the line out for an item that needs none", read.Line)
	}
	return read, true, nil
}

// readEnvironmentEntry reads one entry of the line: "android client (criteria
// 1, 2)", "ios client (criterion 3)", or a name alone.
func readEnvironmentEntry(entry string) (EnvironmentNeed, string) {
	entry = strings.TrimSpace(entry)
	name, rest, bracketed := strings.Cut(entry, "(")
	need := EnvironmentNeed{Name: strings.Join(strings.Fields(name), " ")}
	if need.Name == "" {
		return need, fmt.Sprintf("%q names no environment before its criteria", entry)
	}
	if !bracketed {
		return need, ""
	}
	inside, after, closed := strings.Cut(rest, ")")
	if !closed || strings.TrimSpace(after) != "" {
		return need, fmt.Sprintf("%q has a bracket that is not closed at the end of the entry", entry)
	}
	words := strings.Fields(strings.ReplaceAll(inside, ",", " "))
	if len(words) > 0 && (strings.EqualFold(words[0], "criteria") || strings.EqualFold(words[0], "criterion")) {
		words = words[1:]
	}
	if len(words) == 0 {
		return need, fmt.Sprintf("%q names no criterion in its brackets", entry)
	}
	for _, word := range words {
		if strings.EqualFold(word, "and") {
			continue
		}
		number, err := strconv.Atoi(word)
		if err != nil || number < 1 {
			return need, fmt.Sprintf("%q names %q as a criterion, where a criterion is its number, such as (criteria 1, 2)", entry, word)
		}
		need.Criteria = append(need.Criteria, number)
	}
	return need, ""
}

// EnvironmentsLineProblem is why a work item with this description is refused
// for the environments it names, quoting the line, or empty where it names
// none or only environments the project declares. It is asked wherever an item
// is created or proposed.
func EnvironmentsLineProblem(description string, environments []Environment) string {
	line, found, err := ReadEnvironmentsLine(description)
	if !found {
		return ""
	}
	if err != nil {
		return err.Error()
	}
	declared := map[string]bool{}
	for _, environment := range environments {
		declared[strings.TrimSpace(environment.Name)] = true
	}
	var undeclared []string
	for _, need := range line.Needs {
		if !declared[need.Name] {
			undeclared = append(undeclared, strconv.Quote(need.Name))
		}
	}
	if len(undeclared) == 0 {
		return ""
	}
	known := "the project declares no environments"
	if len(environments) > 0 {
		names := make([]string, 0, len(environments))
		for _, environment := range environments {
			names = append(names, strconv.Quote(strings.TrimSpace(environment.Name)))
		}
		known = "the project declares " + strings.Join(names, ", ")
	}
	return fmt.Sprintf("the line %q names %s, which the project does not declare under environments (%s); name a declared environment, or ask the operator to declare it", line.Line, strings.Join(undeclared, ", "), known)
}
