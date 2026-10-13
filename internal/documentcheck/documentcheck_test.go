package documentcheck

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// repository is a checkout with a register naming nothing and one design to
// link to.
func repository(t *testing.T, register bool) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"docs/designs/other.md": "# Other\n\n## Why\n"}
	if register {
		files[terms.RegisterPath] = "# Terms\n\n" + terms.RegisterHeading + "\n\n| Term | In plain words | Where it is used |\n| --- | --- | --- |\n"
	}
	for relative, content := range files {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func refusal(t *testing.T, err error) *Refusal {
	t.Helper()
	var refused *Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("Check() error = %v, want a refusal", err)
	}
	return refused
}

func TestACoinedWordIsRefusedNamingTheWordAndItsLine(t *testing.T) {
	t.Parallel()

	refused := refusal(t, Check(repository(t, true), "docs/designs/new.md", "# New\n\nThe run is wedged.\n"))
	if len(refused.Problems) != 1 || refused.Problems[0] != (Problem{Line: 3, Rule: RuleCoinedWords, Reason: refused.Problems[0].Reason}) {
		t.Fatalf("problems = %+v, want one coined word on line 3", refused.Problems)
	}
	if message := refused.Error(); !strings.Contains(message, `line 3, coined words: "wedged"`) || !strings.Contains(message, "docs/designs/new.md") {
		t.Fatalf("Error() = %q, want the line, the rule, the word, and the document", message)
	}
}

func TestABrokenLinkIsRefusedNamingTheRuleAndItsLine(t *testing.T) {
	t.Parallel()

	refused := refusal(t, Check(repository(t, true), "docs/designs/new.md", "# New\n\nSee [why](other.md#nowhere).\n"))
	if len(refused.Problems) != 1 || refused.Problems[0].Line != 3 || refused.Problems[0].Rule != RuleLinks {
		t.Fatalf("problems = %+v, want one link on line 3", refused.Problems)
	}
	if !strings.Contains(refused.Error(), "line 3, links: ") {
		t.Fatalf("Error() = %q", refused.Error())
	}
}

func TestProblemsAreReportedInTheOrderTheBodyIsWritten(t *testing.T) {
	t.Parallel()

	refused := refusal(t, Check(repository(t, true), "docs/designs/new.md", "# New\n\n[gone](missing.md)\n\nIt is wedged.\n"))
	if len(refused.Problems) != 2 || refused.Problems[0].Rule != RuleLinks || refused.Problems[1].Rule != RuleCoinedWords {
		t.Fatalf("problems = %+v, want the link on line 3 before the word on line 5", refused.Problems)
	}
}

func TestACleanDocumentPasses(t *testing.T) {
	t.Parallel()

	if err := Check(repository(t, true), "docs/designs/new.md", "# New\n\nSee [why](other.md#why) and [here](#new).\n"); err != nil {
		t.Fatalf("Check() error = %v, want nothing", err)
	}
}

func TestARepositoryWithNoRegisterIsCheckedForLinksAlone(t *testing.T) {
	t.Parallel()

	root := repository(t, false)
	if err := Check(root, "docs/designs/new.md", "# New\n\nThe run is wedged.\n"); err != nil {
		t.Fatalf("Check() error = %v, want nothing where no register says which words are coined", err)
	}
	refused := refusal(t, Check(root, "docs/designs/new.md", "# New\n\n[gone](missing.md)\n"))
	if len(refused.Problems) != 1 || refused.Problems[0].Rule != RuleLinks {
		t.Fatalf("problems = %+v", refused.Problems)
	}
}
