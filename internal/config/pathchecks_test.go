package config

import (
	"strings"
	"testing"
)

// A path check has to say what it runs and name a file inside the repository
// listing what it vouches for; anything else is refused naming the entry.
func TestAPathCheckNamesItsCommandAndAListInsideTheRepository(t *testing.T) {
	t.Parallel()

	if problems := (PathCheck{Command: "make adoption", Paths: "scripts/walk-adoption.paths"}).problems(0); len(problems) != 0 {
		t.Fatalf("problems = %q, want none", problems)
	}
	for _, test := range []struct {
		check PathCheck
		want  string
	}{
		{check: PathCheck{Paths: "scripts/walk-adoption.paths"}, want: "command cannot be empty"},
		{check: PathCheck{Command: "make adoption"}, want: "paths must name the file"},
		{check: PathCheck{Command: "make adoption", Paths: "/etc/paths"}, want: "must be relative"},
		{check: PathCheck{Command: "make adoption", Paths: "../elsewhere.paths"}, want: "inside the repository"},
		{check: PathCheck{Command: "make adoption", Paths: "."}, want: "inside the repository"},
	} {
		problems := test.check.problems(2)
		if len(problems) != 1 || !strings.HasPrefix(problems[0], "path check 2: ") || !strings.Contains(problems[0], test.want) {
			t.Errorf("problems(%#v) = %q, want one naming %q", test.check, problems, test.want)
		}
	}
}

// A path check may ask to run with the provider CLIs on its search path, and
// one that does not ask is read as not needing them.
func TestAPathCheckSaysWhetherItNeedsTheProviderCLIs(t *testing.T) {
	t.Parallel()

	document, err := decodeDocument(strings.NewReader(`path_checks:
  - command: make codex-resume
    paths: scripts/codex-resume.paths
    needs_provider_clis: true
  - command: make adoption
    paths: scripts/walk-adoption.paths
`))
	if err != nil {
		t.Fatalf("decodeDocument() error = %v", err)
	}
	if document.PathChecks == nil || len(*document.PathChecks) != 2 {
		t.Fatalf("path checks = %#v, want two", document.PathChecks)
	}
	if checks := *document.PathChecks; !checks[0].NeedsProviderCLIs || checks[1].NeedsProviderCLIs {
		t.Fatalf("path checks = %#v, want only the first to need the provider CLIs", checks)
	}
}
