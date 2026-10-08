package config

// This repository's own personas, held to the template it ships.
//
// Yoyodyne is the one repository that carries two copies of every persona:
// builtin/v1/personas beside this file, which is what "yoyo init" gives a new
// project, and .yoyodyne/personas at the root, which is what this repository's
// own roles read. On 2026-09-27 the rule against routing approvals to the
// operator (yoyodyne-ifd.430.21) and the rule for naming work items by what
// they are (yoyodyne-ifd.430.22) landed in the template alone and closed as
// done, and every role here ran without them (yoyodyne-ifd.430.26). This is the
// check that would have refused both.
//
// docs/diagnoses/yoyodyne-ifd-430-26-template-only-personas.md lists what each
// live copy lacked when the gap was found.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// templateOnlyPassages is where a template passage this repository's copy
// deliberately does not carry says so. Each entry names a persona file and the
// opening of the template passage, with the reason. Adding to it is the decision
// to let this repository's roles read something other than what it ships, and
// an entry that no longer matches a missing passage fails the test, so the list
// cannot outlive what it declares.
var templateOnlyPassages = map[string]map[string]string{
	"development-manager.md": {
		"- Check what appears to wait on the operator": "Replacing the coined vocabulary in role guidance (yoyodyne-ifd.437.19) rewords this passage to drop the retired \"whose move\", but grants no write to .yoyodyne/personas/development-manager.md. The change lands as evidence until an authorized change carries the rewording into the bound copy; remove this declaration then.",
	},
	"product-manager.md": {
		"- Check each admission against all recorded goals": "Recording relevant goals at admission (yoyodyne-ifd.433.12) ships this guidance, but grants no write to .yoyodyne/personas/product-manager.md. The change lands as evidence until an authorized change carries this passage into the bound copy; remove this declaration then.",
	},
	"program-manager.md": {
		"You own one outcome": "the live copy also names docs/designs/program-manager.md, this repository's own design for the role, which a new project does not have",
	},
}

// TestThisRepositorysPersonasCarryEveryTemplatePassage fails when a passage of a
// shipped persona is missing from the copy this repository's roles read. The
// live copy may say more than the template -- a pointer to this repository's
// own design, a habit only this project has -- but never less, because a
// template passage the live copy lacks is a behaviour recorded as delivered
// that no role here shows.
func TestThisRepositorysPersonasCarryEveryTemplatePassage(t *testing.T) {
	t.Parallel()

	templateDirectory := filepath.Join("builtin", "v1", bundlePersonaDirectory)
	liveDirectory := filepath.Join("..", "..", ".yoyodyne", "personas")
	entries, err := os.ReadDir(templateDirectory)
	if err != nil {
		t.Fatalf("read %s: %v", templateDirectory, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		template, err := os.ReadFile(filepath.Join(templateDirectory, entry.Name()))
		if err != nil {
			t.Fatalf("read template persona: %v", err)
		}
		live, err := os.ReadFile(filepath.Join(liveDirectory, entry.Name()))
		if err != nil {
			t.Errorf("the template ships %s and this repository's roles have no copy of it: %v", entry.Name(), err)
			continue
		}
		used := map[string]bool{}
		for _, passage := range missingPassages(string(template), string(live)) {
			if opening, reason, declared := templateOnlyPassage(entry.Name(), passage); declared {
				used[opening] = true
				t.Logf("%s: template-only by declaration (%s): %q", entry.Name(), reason, passage)
				continue
			}
			t.Errorf("%s: the template carries a passage the copy under .yoyodyne/personas does not, so no role here reads it. Carry it into the live copy, which needs .yoyodyne/personas granted, or declare it in templateOnlyPassages with the reason this repository's roles should not read it:\n%s", entry.Name(), passage)
		}
		for opening := range templateOnlyPassages[entry.Name()] {
			if !used[opening] {
				t.Errorf("%s: templateOnlyPassages declares %q, and no template passage the live copy lacks opens that way any more; remove the declaration", entry.Name(), opening)
			}
		}
	}
	for persona := range templateOnlyPassages {
		if _, err := os.Stat(filepath.Join(templateDirectory, persona)); err != nil {
			t.Errorf("templateOnlyPassages declares passages for %s, which the template does not ship", persona)
		}
	}
}

// A passage the live copy lacks is found, a reflowed one is not, and a passage
// only the live copy has is not held against it.
func TestMissingPassagesComparesPassagesNotLayout(t *testing.T) {
	t.Parallel()

	template := "# Persona\n\nOne rule, wrapped\nacross two lines.\n\n- First habit.\n- Second habit,\n  also wrapped.\n\n## Decisions\n\nThe new rule.\n"
	live := "# Persona\n\nOne rule, wrapped across two lines.\n\n- First habit.\n- A habit only this project has.\n- Second habit, also wrapped.\n"
	got := missingPassages(template, live)
	want := []string{"## Decisions", "The new rule."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("missingPassages() = %q, want %q", got, want)
	}
	if missing := missingPassages(template, template); len(missing) != 0 {
		t.Errorf("a copy identical to its template is missing %q", missing)
	}
}

func templateOnlyPassage(persona, passage string) (opening, reason string, declared bool) {
	for opening, reason := range templateOnlyPassages[persona] {
		if strings.HasPrefix(passage, opening) {
			return opening, reason, true
		}
	}
	return "", "", false
}

// missingPassages is every passage of template that live does not carry, in
// template order. A passage is a paragraph, a heading, or one list entry, with
// its line breaks and runs of spaces folded, so reflowing a paragraph or adding
// an entry to a list is not a difference and dropping or rewording one is.
func missingPassages(template, live string) []string {
	carried := map[string]bool{}
	for _, passage := range personaPassages(live) {
		carried[passage] = true
	}
	var missing []string
	for _, passage := range personaPassages(template) {
		if !carried[passage] {
			missing = append(missing, passage)
		}
	}
	return missing
}

var listEntry = regexp.MustCompile(`^(- |\d+\. )`)

func personaPassages(text string) []string {
	var passages []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			passages = append(passages, strings.Join(strings.Fields(strings.Join(current, " ")), " "))
			current = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "#"):
			flush()
			current = append(current, line)
			flush()
		case listEntry.MatchString(line):
			flush()
			current = append(current, line)
		default:
			current = append(current, line)
		}
	}
	flush()
	return passages
}
