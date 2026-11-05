package artifact

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
)

// anInvariantWrite is a well-formed invariant creation the tests vary one field
// of at a time.
func anInvariantWrite() Write {
	return Write{Action: WriteCreate, ID: "checks-are-honest", Kind: KindInvariant, Title: "Checks report what they did", Directory: "docs/decisions/invariants", EstablishedBy: []string{"yoyodyne-1m3"}, Body: "## Must hold\n\nA check reports its own result.\n\n## Why\n\nThe reviewer judges evidence.", Reason: "the architect's ruling"}
}

func TestAnInvariantWriteIsHeldToTheInvariantsOwnScheme(t *testing.T) {
	t.Parallel()

	store := generatedStore(t.TempDir())
	if err := store.CheckWrite(domain.RoleArchitect, anInvariantWrite()); err != nil {
		t.Fatalf("CheckWrite() = %v, want a well-formed invariant accepted", err)
	}
	path, err := store.WritePath(anInvariantWrite())
	if err != nil || path != "docs/decisions/invariants/checks-are-honest.md" {
		t.Fatalf("WritePath() = %q, %v", path, err)
	}
	cases := map[string]func(*Write){
		"no origin":                  func(w *Write) { w.EstablishedBy = nil },
		"a supports list":            func(w *Write) { w.Supports = []string{"v1-goals"} },
		"a missing why section":      func(w *Write) { w.Body = "## Must hold\n\nA check reports its own result." },
		"text before the sections":   func(w *Write) { w.Body = "Preamble.\n\n" + w.Body },
		"a third section":            func(w *Write) { w.Body += "\n\n## Notes\n\nDropped." },
		"a subdirectory":             func(w *Write) { w.Directory = "docs/decisions/invariants/nested" },
		"the decisions home instead": func(w *Write) { w.Directory = "docs/decisions" },
	}
	for name, change := range cases {
		write := anInvariantWrite()
		change(&write)
		if err := store.CheckWrite(domain.RoleArchitect, write); err == nil {
			t.Errorf("%s: CheckWrite() accepted it", name)
		}
	}
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleDeveloper, domain.RoleReviewer} {
		if err := store.CheckWrite(role, anInvariantWrite()); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("CheckWrite(%s) = %v, want ErrUnauthorized", role, err)
		}
	}
	design := aWrite()
	design.EstablishedBy = []string{"yoyodyne-1m3"}
	if err := design.Validate(); err == nil {
		t.Error("a design carrying established_by was accepted")
	}
}

func TestAnInvariantRevisionKeepsWhatItDoesNotChangeAndRefusesNoChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := generatedStore(root)
	invariants := invariant.Store{RepositoryRoot: root, Directory: "docs/decisions/invariants"}
	if _, err := invariants.Create(domain.RoleArchitect, invariant.Draft{ID: "checks-are-honest", Title: "Checks report what they did", Statement: "A check reports its own result.", Rationale: "The reviewer judges evidence.", EstablishedBy: []string{"yoyodyne-ifd.183"}, Scope: []string{"internal/checks"}, Reason: "recorded"}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	unchanged := Write{Action: WriteRevise, ID: "checks-are-honest", Body: "## Must hold\n\nA check reports its own result.\n\n## Why\n\nThe reviewer judges evidence.", Reason: "nothing"}
	if err := store.CheckWrite(domain.RoleArchitect, unchanged); err == nil || !strings.Contains(err.Error(), "changes nothing") {
		t.Fatalf("CheckWrite(unchanged) = %v", err)
	}
	section := unchanged
	section.Section = "Must hold"
	if err := store.CheckWrite(domain.RoleArchitect, section); err == nil {
		t.Fatal("a section revision of an invariant was accepted")
	}
	revised := unchanged
	revised.Body = "## Must hold\n\nA check that could not start is never a pass.\n\n## Why\n\nThe reviewer judges evidence."
	revised.Reason = "yoyodyne-1m3: the ruling"
	candidate, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, revised, Policy{Designs: domain.ApprovalAutomatic}, "confirmed", time.Now())
	if err != nil || !automatic {
		t.Fatalf("PrepareConfirmation() = %v, %v", automatic, err)
	}
	if candidate.Before == "" || candidate.Artifact.Kind != KindInvariant || len(candidate.Artifact.Revisions) != 2 {
		t.Fatalf("candidate = %+v", candidate)
	}
	parsed, err := invariant.Parse(candidate.Content)
	if err != nil || parsed.Statement != "A check that could not start is never a pass." || len(parsed.Scope) != 1 || parsed.EstablishedBy[0] != "yoyodyne-ifd.183" {
		t.Fatalf("candidate content = %+v, %v", parsed, err)
	}
	if _, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, revised, Policy{Designs: domain.ApprovalHuman}, "confirmed", time.Now()); err != nil || automatic {
		t.Fatalf("PrepareConfirmation(human) = %v, %v, want it left for the operator", automatic, err)
	}
	// The candidate's content has to be the invariant its identity names.
	tampered := candidate
	tampered.Content = strings.Replace(candidate.Content, "Checks report what they did", "Another title", 1)
	if err := tampered.Validate(); err == nil {
		t.Fatal("a candidate whose content names another title was accepted")
	}
}

func TestAnInvariantTheOperatorApprovesIsWrittenInItsOwnForm(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := generatedStore(root)
	written, isInvariant, err := store.RecordInvariant(domain.RoleArchitect, anInvariantWrite(), time.Now())
	if err != nil || !isInvariant || written.Path != "docs/decisions/invariants/checks-are-honest.md" {
		t.Fatalf("RecordInvariant() = %+v, %v, %v", written, isInvariant, err)
	}
	set, err := invariant.Store{RepositoryRoot: root, Directory: "docs/decisions/invariants"}.Load()
	if err != nil || len(set.Active) != 1 || len(set.Problems) != 0 {
		t.Fatalf("invariants = %+v, %v", set, err)
	}
	if _, isInvariant, err := store.RecordInvariant(domain.RoleArchitect, aWrite(), time.Now()); err != nil || isInvariant {
		t.Fatalf("RecordInvariant(design) = %v, %v, want it left to the ordinary write", isInvariant, err)
	}
}

func TestAnArtifactFileClaimingTheInvariantKindIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := generatedStore(root)
	directory := filepath.Join(root, "docs", "decisions")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nid: stray\nkind: invariant\ntitle: Stray\nstatus: active\nrevisions:\n    - action: created\n      by: architect\n      at: 2026-11-01T00:00:00Z\n      reason: hand-written\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(directory, "stray.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, found := set.Find("stray"); found || len(set.Problems) != 1 || !strings.Contains(set.Problems[0].Reason, "invariants directory") {
		t.Fatalf("set = %+v", set)
	}
}
