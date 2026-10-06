package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/terms/inventory"
)

// TestCollectReadsEachSurfaceAndNothingElse measures a small repository laid
// out the way this one is, so a surface that stopped being read, or a test file
// or a comment that started being counted, fails here.
func TestCollectReadsEachSurfaceAndNothingElse(t *testing.T) {
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/cli/status.go", "package cli\n\n// a stoppage in a comment\nconst key = \"stoppage\"\nconst line = \"one stoppage waits\"\n")
	write("internal/cli/status_test.go", "package cli\n\nconst x = \"a stoppage in a test\"\n")
	write("internal/cli/testdata/fixture.go", "package fixture\n\nconst x = \"a stoppage in test data\"\n")
	write("internal/config/builtin/v1/personas/developer.md", "A stoppage, and another stoppage.\n")
	write("README.md", "One stoppage.\n")
	write("docs/operations.md", "A stoppage here.\n")
	write(OutputPath, "Every stoppage counted by itself would be counted twice.\n")
	write("docs/diagnoses/old.md", "A stoppage in a record.\n")
	write("docs/terms.md", "## The register\n\n| Term | In plain words | Where it is used |\n|---|---|---|\n")
	write("docs/designs/design.md", "---\nid: design\nreason: a stoppage in the frontmatter\n---\n\n# Design\n\nA stoppage in the prose.\n")
	write("docs/product/brief.md", "# Brief\n")
	write("docs/decisions/decision.md", "# Decision\n")

	texts, err := Collect(root)
	if err != nil {
		t.Fatal(err)
	}
	measured := Measure([]inventory.Term{{Term: "stoppage", Match: []string{"stoppage"}}}, texts)[0]
	want := [surfaces]int{Printed: 1, Personas: 2, Guides: 2, Governed: 1}
	if measured.Totals != want {
		t.Fatalf("counted %v by surface, want %v; places %v", measured.Totals, want, measured.Places)
	}
}

func TestRenderLinksEveryTermToItsEntry(t *testing.T) {
	var out bytes.Buffer
	measurements := Measure(inventory.Inventory[:3], []Text{{Surface: Guides, Where: "docs/work.md", Body: "a stoppage on the docket"}})
	Render(&out, measurements, inventory.StopWords, []string{"merge-limbo"}, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC))
	document := out.String()
	for _, want := range []string{
		"These were measured on 2026-09-28.",
		"| [`stoppage`](#stoppage) | replace |",
		"### stoppage",
		"- **Where:** 1 in all, in 1 place: `docs/work.md` 1.",
		"| `recording` |",
		"## Compounds still to be decided",
		"- `merge-limbo`",
	} {
		if !strings.Contains(document, want) {
			t.Errorf("the rendered inventory does not say %q", want)
		}
	}
}
