package terms

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckDocumentReportsCoinageOnTheLineOfTheBody(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), nil)
	problems, err := CheckDocument(directory, "docs/designs/new.md",
		"# New\n\nThe queue is wedged.\n\nIt names a whose-thing nobody defines.\n")
	if err != nil {
		t.Fatalf("CheckDocument() error = %v", err)
	}
	if len(problems) != 2 {
		t.Fatalf("CheckDocument() = %v, want the coined word and the compound", problems)
	}
	if problems[0].Line != 3 || problems[0].Term != "wedged" || !strings.Contains(problems[0].Reason, "stuck") {
		t.Errorf("first problem = %+v, want wedged on line 3 with its plain wording", problems[0])
	}
	if problems[1].Line != 5 || problems[1].Term != "whose-thing" || problems[1].Path != "docs/designs/new.md" {
		t.Errorf("second problem = %+v, want the compound on line 5 of docs/designs/new.md", problems[1])
	}
}

func TestCheckDocumentAllowsWhatTheFullCheckAllows(t *testing.T) {
	t.Parallel()

	// A registered term, a replaced term whose row still excuses this document,
	// and the document's own name written as a compound are each allowed, as they
	// are once the document is filed and the whole repository is checked.
	directory := root(t, registerReplacing(
		[]string{"| `posture` | tool access | `docs/designs/landing-route.md` |"},
		"| `sidecar` | a separate directory | the state guide |",
	), nil)
	problems, err := CheckDocument(directory, "docs/designs/landing-route.md",
		"# Landing route\n\nThe sidecar keeps its posture.\n\nThe landing-route design says so.\n")
	if err != nil {
		t.Fatalf("CheckDocument() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("CheckDocument() = %v, want nothing", problems)
	}
	// The same words in a document the row does not name are refused.
	problems, err = CheckDocument(directory, "docs/designs/other.md", "# Other\n\nThe posture changed.\n")
	if err != nil {
		t.Fatalf("CheckDocument() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Term != "posture" {
		t.Fatalf("CheckDocument() = %v, want posture refused", problems)
	}
}

func TestCheckDocumentSaysSoWhereNoRegisterIsKept(t *testing.T) {
	t.Parallel()

	if _, err := CheckDocument(t.TempDir(), "docs/designs/new.md", "# New\n\nWedged.\n"); !errors.Is(err, ErrNoRegister) {
		t.Fatalf("CheckDocument() error = %v, want ErrNoRegister", err)
	}
}
