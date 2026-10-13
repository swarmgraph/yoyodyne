package config

import (
	"strings"
	"testing"
)

func TestAHeavyCheckNamedWithNoNumberGetsOnePlace(t *testing.T) {
	resolved := resolveFixture(t, `
checks:
  - make test
  - make race RACE_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"
execution:
  heavy_checks:
    make race:
    make test: 2
`)
	if got := resolved.Config.Execution.HeavyChecks; got["make race"] != 1 || got["make test"] != 2 || len(got) != 2 {
		t.Fatalf("heavy_checks = %v, want make race 1 and make test 2", got)
	}
	if origin := resolved.Origins["execution.heavy_checks"]; !strings.Contains(origin, "config.yaml") {
		t.Fatalf("heavy_checks origin = %q, want the project file", origin)
	}
	name, limit, heavy := resolved.Config.Execution.HeavyCheck(`make race RACE_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"`)
	if !heavy || name != "make race" || limit != 1 {
		t.Fatalf("HeavyCheck = %q, %d, %t; want make race with one place", name, limit, heavy)
	}
	if _, _, heavy := resolved.Config.Execution.HeavyCheck("make racecar"); heavy {
		t.Fatal("make racecar was read as make race: a name matches only up to a space")
	}
	if _, _, heavy := resolved.Config.Execution.HeavyCheck("make vet"); heavy {
		t.Fatal("make vet, which no name matches, was read as heavy")
	}
}

func TestNoHeavyChecksDeclaredIsEveryCheckStartingAtOnce(t *testing.T) {
	resolved := resolveFixture(t, `
checks:
  - make race
`)
	if len(resolved.Config.Execution.HeavyChecks) != 0 {
		t.Fatalf("heavy_checks = %v, want none", resolved.Config.Execution.HeavyChecks)
	}
	if _, _, heavy := resolved.Config.Execution.HeavyCheck("make race"); heavy {
		t.Fatal("a check was heavy with none declared")
	}
}

func TestTheLongestHeavyCheckNameWins(t *testing.T) {
	execution := Execution{HeavyChecks: map[string]int{"make": 3, "make race": 1}}
	if name, limit, _ := execution.HeavyCheck("make race -v"); name != "make race" || limit != 1 {
		t.Fatalf("HeavyCheck = %q, %d; want the longer name", name, limit)
	}
}

func TestAHeavyCheckNamingNoConfiguredCheckOrNoPlaceIsRefused(t *testing.T) {
	_, err := resolveFixtureErr(t, `
checks:
  - make test
execution:
  heavy_checks:
    make rcae: 1
    make test: 0
`)
	if err == nil {
		t.Fatal("a heavy check naming no configured check was accepted")
	}
	for _, want := range []string{`execution.heavy_checks names "make rcae", which is no configured check`, `execution.heavy_checks gives "make test" 0 places`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to say %q", err, want)
		}
	}
}

func resolveFixture(t *testing.T, body string) Resolved {
	t.Helper()
	return loadProject(t, minimalProjectConfig+body, nil)
}

func resolveFixtureErr(t *testing.T, body string) (Resolved, error) {
	t.Helper()
	return loadProjectError(t, minimalProjectConfig+body, nil)
}
