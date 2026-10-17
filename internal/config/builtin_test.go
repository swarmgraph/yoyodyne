package config

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The bundle that ships inside the executable declares the schema version this
// executable implements. A project overwrites that version with its own, so
// nothing else would notice a bundle left behind by a schema change.
func TestBuiltinBundleDeclaresCurrentVersion(t *testing.T) {
	t.Parallel()

	loaded, err := loadBuiltinBundle(BuiltinV1)
	if err != nil {
		t.Fatalf("loadBuiltinBundle() error = %v", err)
	}
	if loaded.document.Version == nil {
		t.Fatalf("bundle %s declares no version", BuiltinV1)
	}
	if *loaded.document.Version != CurrentVersion {
		t.Errorf("bundle %s version = %d, want %d", BuiltinV1, *loaded.document.Version, CurrentVersion)
	}
}

// A bundle is embedded and read-only, so a version it should not be declaring
// is a defect in this executable. It fails at load, naming the bundle, rather
// than resolving into an effective configuration.
func TestBundleWithAnUnsupportedVersionFailsToLoad(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		bundle  string
		problem string
	}{
		{
			name:    "later version",
			bundle:  fmt.Sprintf("version: %d\n", CurrentVersion+1),
			problem: fmt.Sprintf("bundle builtin:v1 declares version %d, and this executable implements version %d", CurrentVersion+1, CurrentVersion),
		},
		{
			name:    "earlier version",
			bundle:  fmt.Sprintf("version: %d\n", CurrentVersion-1),
			problem: fmt.Sprintf("bundle builtin:v1 declares version %d, and this executable implements version %d", CurrentVersion-1, CurrentVersion),
		},
		{
			name:    "no version at all",
			bundle:  "execution:\n  max_concurrent_developers: 1\n",
			problem: fmt.Sprintf("bundle builtin:v1 must declare version %d", CurrentVersion),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files := fstest.MapFS{"bundle.yaml": &fstest.MapFile{Data: []byte(test.bundle)}}
			_, err := loadBundleFiles(BuiltinV1, files)
			if err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("loadBundleFiles() error = %v, want %q", err, test.problem)
			}
		})
	}
}

// The version check is one of several rules a bundle is held to, and adding it
// left the others in force.
func TestBundleRulesBeyondVersionStillHold(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		bundle  string
		problem string
	}{
		{
			name:    "extends another bundle",
			bundle:  "version: 1\nextends: builtin:v1\n",
			problem: "bundle builtin:v1 must not extend another bundle",
		},
		{
			name:    "declares a product",
			bundle:  "version: 1\nproduct:\n  id: example\n  repository: .\n",
			problem: "bundle builtin:v1 must not declare a product",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files := fstest.MapFS{"bundle.yaml": &fstest.MapFile{Data: []byte(test.bundle)}}
			_, err := loadBundleFiles(BuiltinV1, files)
			if err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("loadBundleFiles() error = %v, want %q", err, test.problem)
			}
		})
	}
}

// The template ships one persona per role the harness knows, named for the role,
// and nothing beside them. A role added to the harness without one is a role
// every project has to write a persona for by hand before it can configure an
// agent for it -- which is how the program manager's first persona was written.
func TestTheTemplateShipsOnePersonaPerRole(t *testing.T) {
	t.Parallel()

	loaded, err := loadBuiltinBundle(BuiltinV1)
	if err != nil {
		t.Fatalf("loadBuiltinBundle() error = %v", err)
	}
	shipped, err := loaded.shippedPersonas()
	if err != nil {
		t.Fatalf("shippedPersonas() error = %v", err)
	}
	want := map[string]bool{}
	for _, role := range domain.Roles() {
		want["personas/"+string(role)+".md"] = true
	}
	got := map[string]bool{}
	for _, personaPath := range shipped {
		got[personaPath] = true
		if !want[personaPath] {
			t.Errorf("the template ships %s, which names no role the harness knows", personaPath)
		}
		text, _, err := loaded.personas.load("persona", personaPath)
		if err != nil {
			t.Errorf("load %s: %v", personaPath, err)
			continue
		}
		if strings.TrimSpace(text) == "" {
			t.Errorf("%s is empty", personaPath)
		}
	}
	for personaPath := range want {
		if !got[personaPath] {
			t.Errorf("the template ships no %s", personaPath)
		}
	}
}

// Every shipped persona tells its role to name a work item by what it is, with
// its identifier after it, and to treat a bare identifier as a defect. The
// operator read a lane report saying "434.9 and 434.3 were read and compose"
// and could not tell what either item was; a role whose persona is silent on it
// can write the same sentence to him again.
func TestEveryShippedPersonaNamesWorkItemsByWhatTheyAre(t *testing.T) {
	t.Parallel()

	loaded, err := loadBuiltinBundle(BuiltinV1)
	if err != nil {
		t.Fatalf("loadBuiltinBundle() error = %v", err)
	}
	shipped, err := loaded.shippedPersonas()
	if err != nil {
		t.Fatalf("shippedPersonas() error = %v", err)
	}
	if len(shipped) == 0 {
		t.Fatal("the template ships no personas to check")
	}
	for _, personaPath := range shipped {
		text, _, err := loaded.personas.load("persona", personaPath)
		if err != nil {
			t.Errorf("load %s: %v", personaPath, err)
			continue
		}
		if !namesWorkItemsByWhatTheyAre(text) {
			t.Errorf("%s does not say that a work item is named by what it is, with its identifier after it, and that an identifier alone is a defect", personaPath)
		}
	}
}

// namesWorkItemsByWhatTheyAre reports whether text states the naming rule,
// however its lines are wrapped.
func namesWorkItemsByWhatTheyAre(text string) bool {
	flat := strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.Contains(flat, "by what it is, with its identifier after it") &&
		strings.Contains(flat, "an identifier alone is a defect")
}

// Every shipped persona tells its role that a decision its authority covers is
// made by the role and reported to the operator afterwards, that an approval
// routed to the operator is a defect, and the test that tells the one decision
// that is the operator's -- a change of fundamental intent -- from everything
// else. The operator's rule of 2026-09-26 is that asking him to approve
// something, or approving it on his behalf, is a bug; a persona silent on it is
// how the next role routes the next approval to him.
func TestEveryShippedPersonaDecidesAndReportsRatherThanRoutingApprovals(t *testing.T) {
	t.Parallel()

	loaded, err := loadBuiltinBundle(BuiltinV1)
	if err != nil {
		t.Fatalf("loadBuiltinBundle() error = %v", err)
	}
	shipped, err := loaded.shippedPersonas()
	if err != nil {
		t.Fatalf("shippedPersonas() error = %v", err)
	}
	if len(shipped) == 0 {
		t.Fatal("the template ships no personas to check")
	}
	for _, personaPath := range shipped {
		text, _, err := loaded.personas.load("persona", personaPath)
		if err != nil {
			t.Errorf("load %s: %v", personaPath, err)
			continue
		}
		if !decidesAndReports(text) {
			t.Errorf("%s does not say that a decision the role can make is made and reported afterwards, that an approval routed to the operator is a defect, and how to tell a change of fundamental intent", personaPath)
		}
	}
}

// decidesAndReports reports whether text states the rule that a role makes the
// decisions its authority covers and reports them afterwards, and the test for
// the one decision that is the operator's, however its lines are wrapped.
func decidesAndReports(text string) bool {
	flat := strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.Contains(flat, "report it") &&
		strings.Contains(flat, "afterwards") &&
		strings.Contains(flat, "an approval routed to") &&
		strings.Contains(flat, "is a defect") &&
		strings.Contains(flat, "fundamental intent") &&
		strings.Contains(flat, "admit any work they refused before, or refuse any work they admitted")
}

// The shipped program manager persona says to write Blocked on a human as the
// report's own list, and that the harness derives the other two and carries the
// entries; the copy this repository's roles read is held to the same passage by
// the live persona test.
func TestTheShippedProgramManagerPersonaWritesTheLaneReportsOpeningLists(t *testing.T) {
	t.Parallel()

	loaded, err := loadBuiltinBundle(BuiltinV1)
	if err != nil {
		t.Fatalf("loadBuiltinBundle() error = %v", err)
	}
	text, _, err := loaded.personas.load("persona", "personas/program-manager.md")
	if err != nil {
		t.Fatalf("load the program manager persona: %v", err)
	}
	for _, want := range []string{"**Completed:**", "**Handed off:**", "**Blocked on a human:**", "`blocked_on_a_human`", "The harness carries each Blocked on a human entry"} {
		if !strings.Contains(text, want) {
			t.Errorf("the shipped program manager persona does not say %q", want)
		}
	}
}
