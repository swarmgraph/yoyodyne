package artifact

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repositoryread"
)

func TestReplaceSectionChangesOnlyTheNamedSection(t *testing.T) {
	body := "\n# Design\n\nOpening.\n\n## Scope\n\nOld scope.\n\n### Detail\n\nOld detail.\n\n```sh\n## not a heading\n```\n\n## Limits\n\nUnchanged limits.\n"
	got, err := ReplaceSection(body, "## Scope", "New scope.\n\n### Detail\n\nNew detail.")
	if err != nil {
		t.Fatal(err)
	}
	want := "\n# Design\n\nOpening.\n\n## Scope\n\nNew scope.\n\n### Detail\n\nNew detail.\n\n## Limits\n\nUnchanged limits.\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// Named without its marks, and as the last section of the document.
	last, err := ReplaceSection(body, "Limits", "New limits.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(last, body[:strings.Index(body, "## Limits")]) || !strings.HasSuffix(last, "## Limits\n\nNew limits.\n") {
		t.Fatalf("last section: %q", last)
	}
	// A heading inside fenced code is not one, and the fence does not end the
	// section it sits in.
	if _, err := ReplaceSection(body, "not a heading", "x"); err == nil {
		t.Fatal("a line inside a code fence was read as a heading")
	}
}

func TestReplaceSectionRefusesWhatIsNotOneSection(t *testing.T) {
	body := "# A\n\n## Twice\n\none\n\n## Twice\n\ntwo\n\n## Once\n\nthree\n"
	for name, test := range map[string]struct{ heading, replacement, says string }{
		"missing":         {"Absent", "text", "no heading"},
		"ambiguous":       {"Twice", "text", "more than one"},
		"empty":           {"Once", "  ", "left empty"},
		"sibling heading": {"Once", "text\n\n## Another", "at its level or above"},
		"heading again":   {"Once", "## Once\n\ntext", "at its level or above"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReplaceSection(body, test.heading, test.replacement); err == nil || !strings.Contains(err.Error(), test.says) {
				t.Fatalf("err = %v, want it to say %q", err, test.says)
			}
		})
	}
}

// A design longer than a role can read is revised one section at a time,
// confirmed under the automatic designs setting, and the candidate that lands
// differs from the document only in that section.
func TestASectionOfADocumentLongerThanTheReadLimitIsRevisedAlone(t *testing.T) {
	store := generatedStore(t.TempDir())
	var long strings.Builder
	long.WriteString("# Long design\n\nWhy it exists.\n")
	for index := 0; long.Len() <= repositoryread.MaxContentBytes+MaxWriteBodyBytes; index++ {
		long.WriteString("\n## Part " + strings.Repeat("x", index%7) + string(rune('a'+index%26)) + "\n\n" + strings.Repeat("Words the owner wrote and nobody rewrites. ", 40) + "\n")
	}
	long.WriteString("\n## The section to revise\n\nWhat it said before.\n\n## After it\n\nWhat follows, untouched.\n")
	original := long.String()
	if _, err := store.Create(domain.RoleArchitect, Draft{ID: "long-design", Kind: KindDesign, Title: "Long design", Directory: designsHome, Body: original, Reason: "owner decision"}, moment()); err != nil {
		t.Fatal(err)
	}
	before := documentBody(t, store, designsHome+"/long-design.md")
	if len(before) <= repositoryread.MaxContentBytes {
		t.Fatalf("document is %d bytes, not longer than the read limit", len(before))
	}

	write := Write{Action: WriteRevise, ID: "long-design", Section: "The section to revise", Body: "What it says now.", Reason: "yoyodyne-ifd.433.21 - the section is revised"}
	if err := store.CheckWrite(domain.RoleArchitect, write); err != nil {
		t.Fatal(err)
	}
	candidate, automatic, err := store.PrepareConfirmation(domain.RoleArchitect, write, Policy{Designs: domain.ApprovalAutomatic}, "confirmed under policy", moment().Add(time.Hour))
	if err != nil || !automatic {
		t.Fatalf("confirmation: %v, %v", automatic, err)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatal(err)
	}
	marker := "## The section to revise\n"
	cut := strings.Index(before, marker) + len(marker)
	tail := "\n## After it\n\nWhat follows, untouched.\n"
	if !strings.HasSuffix(candidate.Content, before[:cut]+"\nWhat it says now.\n"+tail) {
		t.Fatal("the landed body is not the document with only that section changed")
	}
	if approval, _ := candidate.Artifact.LatestApproval(); approval.By != ApproverHarness || approval.Policy != "approvals.designs" {
		t.Fatalf("approval = %+v", approval)
	}
	if candidate.Before == "" {
		t.Fatal("the candidate is not bound to the file it was spliced into")
	}

	// A heading the document does not have is refused before anybody is asked.
	write.Section = "No such section"
	if err := store.CheckWrite(domain.RoleArchitect, write); err == nil || !strings.Contains(err.Error(), "no heading") {
		t.Fatalf("missing section: %v", err)
	}
	// A creation has no sections to replace.
	if err := (Write{Action: WriteCreate, ID: "new", Kind: KindDesign, Title: "New", Directory: designsHome, Section: "Scope", Body: "x", Reason: "r"}).Validate(); err == nil || !strings.Contains(err.Error(), "section is refused on a creation") {
		t.Fatalf("section on a creation: %v", err)
	}
}
