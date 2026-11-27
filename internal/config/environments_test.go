package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const androidEnvironment = `environments:
  - name: android client
    needs: [npm packages]
    check: scripts/ready-android.sh
    test: scripts/integration-android.sh
`

// A project that declares no environment, or an empty list, loads exactly as
// it did before environments existed.
func TestNoEnvironmentLoadsAsBefore(t *testing.T) {
	t.Parallel()

	before := loadProject(t, minimalProjectConfig, nil)
	empty := loadProject(t, minimalProjectConfig+"environments: []\n", nil)
	if len(before.Config.Environments) != 0 || len(empty.Config.Environments) != 0 {
		t.Fatalf("environments = %#v and %#v, want none", before.Config.Environments, empty.Config.Environments)
	}
	before.Path, empty.Path = "", ""
	delete(empty.Origins, "environments")
	before.Config.Product.Repository, empty.Config.Product.Repository = "", ""
	if !reflect.DeepEqual(before.Config, empty.Config) {
		t.Fatalf("an empty environments list changed the effective configuration")
	}
}

// A declared environment is read with every field, and where it names none it
// runs where the developer does, needs nothing for the whole machine, and has
// fifteen minutes.
func TestAnEnvironmentIsReadWithItsDefaults(t *testing.T) {
	t.Parallel()

	resolved, err := loadInstallProject(t, installProject(t, npmInstall+androidEnvironment+`  - name: web ui
    machine: true
    how_to_install: "npx playwright install chromium"
    where: harness
    check: scripts/ready-browser.sh
    test: scripts/integration-web.sh
    time_limit: 4m
`))
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if len(resolved.Config.Environments) != 2 {
		t.Fatalf("environments = %#v, want two", resolved.Config.Environments)
	}
	android, web := resolved.Config.Environments[0], resolved.Config.Environments[1]
	if android.Name != "android client" || !reflect.DeepEqual(android.Needs, []string{"npm packages"}) || android.Machine ||
		android.Place() != EnvironmentInDeveloper || android.Limit() != DefaultEnvironmentTimeLimit || DefaultEnvironmentTimeLimit != 15*time.Minute ||
		android.Check != "scripts/ready-android.sh" || android.Test != "scripts/integration-android.sh" {
		t.Fatalf("android = %#v, want it read with the defaults", android)
	}
	if !web.Machine || web.HowToInstall != "npx playwright install chromium" || web.Place() != EnvironmentInHarness || web.Limit() != 4*time.Minute {
		t.Fatalf("web = %#v, want every field it wrote", web)
	}
	if resolved.Origins["environments"] == "" {
		t.Fatalf("origins = %#v, want where environments came from", resolved.Origins)
	}
}

// Each thing the design refuses is refused when the configuration is read,
// naming the entry and what is wrong with it.
func TestAnEnvironmentIsRefusedWhenReadNamingTheEntry(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		environments string
		want         []string
	}{
		{
			name:         "two entries share a name",
			environments: androidEnvironment + "  - name: android client\n    check: a\n    test: b\n",
			want:         []string{`environment "android client"`, "environment 0 has the same name"},
		},
		{
			name:         "needs names an undeclared install step",
			environments: strings.Replace(androidEnvironment, "[npm packages]", "[client library]", 1),
			want:         []string{`environment "android client"`, `needs "client library"`, "not the name of an install step"},
		},
		{
			name:         "how_to_install without machine",
			environments: androidEnvironment + "    how_to_install: install the SDK\n",
			want:         []string{`environment "android client"`, "how_to_install is set without machine: true"},
		},
		{
			name:         "where is neither place",
			environments: androidEnvironment + "    where: ci\n",
			want:         []string{`environment "android client"`, `where is "ci"`, `"developer" or "harness"`},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadInstallProject(t, installProject(t, npmInstall+test.environments))
			if err == nil {
				t.Fatalf("LoadResolved() = nil, want it refused")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("LoadResolved() = %v, want it to say %q", err, want)
				}
			}
		})
	}

	problems := environmentProblems([]Environment{{Name: " "}, {Name: "x", Check: "c", Test: "t", TimeLimit: Duration(-time.Minute)}}, nil)
	for _, want := range []string{"environment 0: name cannot be empty", "check cannot be empty", "test cannot be empty", "time_limit cannot be negative"} {
		if !strings.Contains(strings.Join(problems, "\n"), want) {
			t.Fatalf("problems = %q, want one saying %q", problems, want)
		}
	}
}

// An environment is the operator's to write, as an install step is.
func TestARoleMayNotApplyAnEnvironment(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"environments", "environments.[].check", "environments.0.where"} {
		err := RoleMayApply(key)
		if err == nil {
			t.Fatalf("RoleMayApply(%q) = nil, want it refused", key)
		}
		for _, want := range []string{key + " is refused for a role's write", "stays the operator's", "runs on this machine"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("RoleMayApply(%q) = %v, want it to say %q", key, err, want)
			}
		}
	}
}

// The line is read into environment names and the criteria each serves; a
// description without one needs nothing.
func TestTheEnvironmentsLineIsReadIntoNamesAndCriteria(t *testing.T) {
	t.Parallel()

	description := "Builds the client.\n\n  Environments: android client (criteria 1, 2); ios  client (criterion 3); web ui\n\nDone means it works."
	line, found, err := ReadEnvironmentsLine(description)
	if err != nil || !found {
		t.Fatalf("ReadEnvironmentsLine() = %#v, %v, %v; want it read", line, found, err)
	}
	want := []EnvironmentNeed{{Name: "android client", Criteria: []int{1, 2}}, {Name: "ios client", Criteria: []int{3}}, {Name: "web ui"}}
	if !reflect.DeepEqual(line.Needs, want) || line.Line != "Environments: android client (criteria 1, 2); ios  client (criterion 3); web ui" {
		t.Fatalf("line = %#v, want %#v", line, want)
	}

	if _, found, err := ReadEnvironmentsLine("Builds the client. It needs environments: none."); found || err != nil {
		t.Fatalf("a description with no line = %v, %v; want nothing found", found, err)
	}
	for _, unreadable := range []string{
		"Environments: android client (criteria one)",
		"Environments: android client (criteria 1",
		"Environments: (criterion 1)",
		"Environments: ",
		"Environments: a\nEnvironments: b",
	} {
		if _, found, err := ReadEnvironmentsLine(unreadable); !found || err == nil || !strings.Contains(err.Error(), "Environments:") {
			t.Errorf("ReadEnvironmentsLine(%q) = %v, %v; want it refused quoting the line", unreadable, found, err)
		}
	}
}

// An item naming an environment the project does not declare is refused,
// quoting the line; one naming only declared ones, or none, is not.
func TestAnItemNamingAnUndeclaredEnvironmentIsRefusedQuotingTheLine(t *testing.T) {
	t.Parallel()

	declared := []Environment{{Name: "android client", Check: "c", Test: "t"}}
	line := "Environments: android client (criteria 1, 2); ios client (criterion 3)"
	problem := EnvironmentsLineProblem("Builds it.\n"+line, declared)
	for _, want := range []string{line, `"ios client"`, "does not declare", `"android client"`} {
		if !strings.Contains(problem, want) {
			t.Fatalf("problem = %q, want it to say %q", problem, want)
		}
	}
	if strings.Contains(problem, `names "android client"`) {
		t.Fatalf("problem = %q names the declared environment as undeclared", problem)
	}
	if problem := EnvironmentsLineProblem("Environments: android client (criterion 1)", declared); problem != "" {
		t.Fatalf("a declared environment was refused: %q", problem)
	}
	if problem := EnvironmentsLineProblem("Builds it with no line.", nil); problem != "" {
		t.Fatalf("an item with no line was refused: %q", problem)
	}
	if problem := EnvironmentsLineProblem("Environments: web ui", nil); !strings.Contains(problem, "declares no environments") {
		t.Fatalf("problem = %q, want it to say the project declares none", problem)
	}
}
