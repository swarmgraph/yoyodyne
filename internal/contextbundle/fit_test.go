package contextbundle

import (
	"strings"
	"testing"
)

const fitBriefing = `# Product context

Fixed header.

## Recorded product intent

The brief and goals exist.

## Authoritative product intent: docs/product/goals/v1-goals.md

## Goals

- A goal.

## Standing goals

- The plain-language goal.

## Authoritative product intent: docs/product/brief.md

BRIEF
## Decision record: docs/decisions/old.md

DECISION
## Design: docs/designs/one.md

DESIGN

## What the product ships today

Label for what ships.

### Command help

` + "```\nHELP\n```" + `

### Shipped documentation: README.md

README

### Shipped documentation: docs/guide.md

GUIDE

The shipped documentation is 100 bytes across 2 document(s) on disk.

## Beads work items

- current work
`

func sizedBriefing() string {
	replace := func(text, marker string, size int) string {
		return strings.Replace(text, marker+"\n", strings.Repeat(strings.ToLower(marker)+" line of text\n", size/len(marker+" line of text\n")), 1)
	}
	text := fitBriefing
	text = replace(text, "BRIEF", 20<<10)
	text = replace(text, "DECISION", 20<<10)
	text = replace(text, "DESIGN", 30<<10)
	text = replace(text, "README", 10<<10)
	text = replace(text, "GUIDE", 40<<10)
	return text
}

func TestFitProductContextGivesWayInOrderLongestFirst(t *testing.T) {
	t.Parallel()
	text := sizedBriefing()
	fit := FitProductContext(text, len(text)-58<<10)
	if !fit.Fits || len(fit.Text) > len(text)-58<<10 {
		t.Fatalf("fit is %d bytes, fits=%v", len(fit.Text), fit.Fits)
	}
	var order []string
	for _, section := range fit.Sections {
		order = append(order, section.Path)
	}
	// The longer shipped document goes first, then the shorter; the command help
	// is next in the order, and the decision record after it has to give part.
	if strings.Join(order, ",") != "docs/guide.md,README.md,,docs/decisions/old.md" {
		t.Fatalf("gave way as %q", order)
	}
	if fit.Sections[3].KeptBytes == 0 || fit.Sections[0].KeptBytes != 0 {
		t.Fatalf("sections: %+v", fit.Sections)
	}
	for _, want := range []string{"The plain-language goal.", "- current work", "The shipped documentation is 100 bytes", "brief line of text", "design line of text", "Fixed header.", FittedHeading} {
		if !strings.Contains(fit.Text, want) {
			t.Fatalf("fitted briefing lost %q", want)
		}
	}
	if strings.Contains(fit.Text, "guide line of text") {
		t.Fatal("shipped document left out still carried")
	}
}

func TestFitProductContextKeepsTheStandingGoalsWhenNothingElseFits(t *testing.T) {
	t.Parallel()
	text := sizedBriefing()
	fit := FitProductContext(text, 1)
	if fit.Fits {
		t.Fatal("a one-byte target was reported met")
	}
	for _, want := range []string{"The plain-language goal.", "- current work", "## Recorded product intent"} {
		if !strings.Contains(fit.Text, want) {
			t.Fatalf("protected text lost: %q", want)
		}
	}
	if len(fit.Sections) != 6 || fit.Sections[len(fit.Sections)-1].Path != "docs/product/brief.md" {
		t.Fatalf("specification without standing goals should give way last: %+v", fit.Sections)
	}
}

func TestFitProductContextLeavesAFittingBriefingAlone(t *testing.T) {
	t.Parallel()
	text := sizedBriefing()
	if fit := FitProductContext(text, len(text)); !fit.Fits || fit.Text != text || len(fit.Sections) != 0 {
		t.Fatal("a briefing within its target was changed")
	}
}

func TestBriefingSectionsNamesWhatGivesWay(t *testing.T) {
	t.Parallel()
	kinds := map[string]string{}
	total := 0
	for _, section := range BriefingSections(sizedBriefing()) {
		kinds[section.Heading] = section.Kind
		total += section.Bytes
	}
	if total != len(sizedBriefing()) {
		t.Fatal("sections do not add up to the briefing")
	}
	for heading, want := range map[string]string{
		"## Authoritative product intent: docs/product/goals/v1-goals.md": "",
		"## Authoritative product intent: docs/product/brief.md":          IntentHeading,
		"### Shipped documentation: docs/guide.md":                        "Shipped documentation",
		"### Command help":    "Command help",
		"## Beads work items": "",
	} {
		if kind, ok := kinds[heading]; !ok || kind != want {
			t.Fatalf("%s is %q (%v), want %q", heading, kind, ok, want)
		}
	}
}

// A recurring turn's briefing opens with what does not move when work lands —
// the header, the recorded-intent section and the product intent whole — and
// the split ends exactly there, so two turns with the same intent are given the
// same opening byte for byte while every section that moves is in the rest.
func TestSplitUnchangingEndsAfterTheProductIntent(t *testing.T) {
	t.Parallel()
	kept := PassContext(sizedBriefing()).Text
	unchanging, rest := SplitUnchanging(kept)
	if unchanging+rest != kept {
		t.Fatal("the two parts do not join to the briefing")
	}
	for _, want := range []string{"# Product context", "## Recorded product intent", "## Standing goals", "brief line of text"} {
		if !strings.Contains(unchanging, want) {
			t.Errorf("the unchanging opening lacks %q", want)
		}
	}
	if !strings.HasPrefix(rest, "## Decision record: docs/decisions/old.md") {
		t.Fatalf("the rest does not open with the first section after the intent: %q", firstLine(rest))
	}
	for _, moving := range []string{"## Beads work items", PassHeading, "[Left out"} {
		if strings.Contains(unchanging, moving) {
			t.Errorf("the unchanging opening carries %q, which moves when work lands", moving)
		}
	}

	changed := strings.Replace(strings.Replace(sizedBriefing(), "- current work", "- other work", 1), "design line of text\n", "design line of text\nmore design\n", 1)
	again, otherRest := SplitUnchanging(PassContext(changed).Text)
	if again != unchanging || otherRest == rest {
		t.Fatal("a change to the work or a design moved the unchanging opening")
	}

	if opening, whole := SplitUnchanging("no header here\n"); opening != "" || whole != "no header here\n" {
		t.Fatal("a briefing without the product context's header was split")
	}
}
