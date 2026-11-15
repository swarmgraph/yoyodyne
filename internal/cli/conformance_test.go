package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConformanceRefusesTheTagAndNamesWhatDiverged(t *testing.T) {
	// Not parallel: the state root the workflow instance is recorded under is
	// set for this process.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeConformingProduct(t, project)
	writeArtifact(t, project, "docs/guide.md", "# Guide\n\nSee [the design](designs/gone.md).\n")

	stdout, stderr, code := runCLI(t, "conformance", "--config", configPath)
	if code != 1 {
		t.Fatalf("conformance code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	for _, want := range []string{
		"something the system records about itself is no longer true",
		"references  diverges",
		"docs/guide.md",
		"the tag is refused until each mismatch above is reconciled; nothing in this repository was changed",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("conformance stdout = %q, want it to contain %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "conformance", "--config", configPath, "--json")
	if code != 1 {
		t.Fatalf("conformance --json code = %d, stderr = %q", code, stderr)
	}
	var reported struct {
		Workflow   string `json:"workflow"`
		Digest     string `json:"digest"`
		Definition string `json:"definition"`
		Terminal   string `json:"terminal"`
		Conforms   bool   `json:"conforms"`
		Findings   []struct {
			Step       string   `json:"step"`
			Outcome    string   `json:"outcome"`
			Mismatches []string `json:"mismatches"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(stdout), &reported); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if reported.Workflow != "release-readiness" || reported.Terminal != "mismatch" || reported.Conforms {
		t.Fatalf("reported = %#v", reported)
	}
	if reported.Definition != "built in" {
		t.Fatalf("a project with no definition of its own ran %q", reported.Definition)
	}
	if !strings.HasPrefix(reported.Digest, "wf-") {
		t.Fatalf("the result is pinned to %q", reported.Digest)
	}
	last := reported.Findings[len(reported.Findings)-1]
	if last.Step != "references" || last.Outcome != "diverges" || len(last.Mismatches) == 0 {
		t.Fatalf("the last finding is %#v", last)
	}
}

func TestConformanceRunsTheProjectsOwnDefinitionAndShipsTheResultInTheNotes(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeConformingProduct(t, project)

	// A sequence of the project's own: the two checks that read the repository
	// and neither of the two that read the tracker, which is what a project with
	// no tracker to read would write. The states are named the way this project
	// would name them rather than the way the shipped definition does, because
	// `action:` is what selects a check and what the state is called is the
	// file's business.
	definition := filepath.Join(project, ".yoyodyne", "workflows", "release-readiness.yaml")
	if err := os.MkdirAll(filepath.Dir(definition), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(definition, []byte(`schema: 1
id: release-readiness
summary: what this project checks before it tags
initial: check-artifacts
states:
  check-artifacts:
    action: conformance.artifacts
    on:
      conforms: check-links
      diverges: mismatch
  check-links:
    action: conformance.references
    on:
      conforms: ready
      diverges: mismatch
terminals:
  ready: {}
  mismatch: {}
`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "conformance", "--config", configPath)
	if code != 0 {
		t.Fatalf("conformance code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "the system matches what it records about itself") {
		t.Fatalf("conformance stdout = %q", stdout)
	}
	if !strings.Contains(stdout, definition) {
		t.Fatalf("the result does not name the definition it ran: %q", stdout)
	}
	if strings.Contains(stdout, "goals") {
		t.Fatalf("a sequence that names no goals check ran one: %q", stdout)
	}
	// The state the project named, with the check behind it, so the reading is
	// against both the file they wrote and this build.
	for _, want := range []string{"check-artifacts (artifacts)", "check-links (references)"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("conformance stdout = %q, want it to contain %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "conformance", "--config", configPath, "--notes")
	if code != 0 {
		t.Fatalf("conformance --notes code = %d, stderr = %q", code, stderr)
	}
	// The release cut replaces what is between these two lines in a release's
	// notes, so they are as much the contract as the prose between them.
	if !strings.HasPrefix(stdout, "<!-- yoyodyne:release-readiness -->\n") {
		t.Fatalf("the section does not open with the marker the cut looks for: %q", stdout)
	}
	if !strings.HasSuffix(stdout, "<!-- /yoyodyne:release-readiness -->\n") {
		t.Fatalf("the section does not close with the marker the cut looks for: %q", stdout)
	}
	for _, want := range []string{"## Release readiness", "ended in **ready**", "- **check-artifacts (artifacts)** — conforms"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("the notes section = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestConformanceRefusesArgumentsItCannotAnswer(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	if _, stderr, code := runCLI(t, "conformance", "--config", configPath, "--json", "--notes"); code != 2 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if _, stderr, code := runCLI(t, "conformance", "--config", configPath, "everything"); code != 2 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	_, stderr, code := runCLI(t, "conformance", "--config", configPath, "--workflow", filepath.Join(t.TempDir(), "absent.yaml"))
	if code != 1 {
		t.Fatalf("a definition that is not there was not refused: code = %d", code)
	}
	if !strings.Contains(stderr, "absent.yaml") {
		t.Fatalf("the refusal does not name the definition it could not read: %q", stderr)
	}
}

// writeConformingProduct writes the documents a product has to record for the
// repository half of the gate to pass: a brief, goals that trace to it, and one
// architectural invariant.
func writeConformingProduct(t *testing.T, project string) {
	t.Helper()
	writeArtifact(t, project, "docs/product/brief.md", revisedArtifact("brief", "brief", nil,
		revision("created", "2026-08-01T00:00:00Z", "recorded when identity arrived"))+`
# Brief

## Goals

- **Intent goes in and merged software comes out.** A person says what they want
  and receives working software.
`)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", revisedArtifact("v1-goals", "goals", []string{"brief"},
		revision("created", "2026-08-10T00:00:00Z", "recorded when identity arrived"))+`
# Goals

## Goals

- serving the chain
  *Supports: intent goes in and merged software comes out.*
`)
	writeArtifact(t, project, "docs/decisions/invariants/nothing-is-promoted-unjudged.md", `---
id: nothing-is-promoted-unjudged
title: Nothing is promoted unjudged
status: active
established_by:
    - item-1
revisions:
    - action: created
      by: architect
      at: 2026-08-12T00:00:00Z
      reason: recorded when the fixture was written
---

## Must hold

No change reaches the target branch without a verdict on it.

## Why

A change promoted on nobody's verdict is a change nobody agreed to.
`)
}

// TestConformanceStampsFromTheTrackersExportInsideADeveloperRun is the release
// item's own run stamping its notes. Its sandbox refuses the tracker to every
// bd command and refuses writes to the harness's state, so both are made so
// here: the project has no tracker to open, and the directory the workflow's
// records go in is read-only. Read from the export, the reading still passes,
// says where it read the work from, and leaves no record behind.
func TestConformanceStampsFromTheTrackersExportInsideADeveloperRun(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateHome)
	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeConformingProduct(t, project)
	writeTrackerExport(t, project,
		`{"_type":"issue","id":"item-1","title":"Admitted work","status":"open","priority":1,"issue_type":"task","notes":"Goal served: serving the chain"}`,
		`{"_type":"issue","id":"item-2","title":"Finished work naming nothing","status":"closed","priority":2,"issue_type":"task","notes":"Goal served: a goal nobody states"}`,
	)

	// The ordinary reading, before the state is closed off: with no tracker to
	// open, the goals check judged nothing and the reading is refused. That is
	// the failure a release run met, and what --from-export exists to answer.
	stdout, stderr, code := runCLI(t, "conformance", "--config", configPath, "--notes")
	if code != 1 || !strings.Contains(stdout, "the work tracker could not be read") {
		t.Fatalf("conformance without a tracker: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	records := instanceDirectory(t, stateHome)
	before := directoryNames(t, records)
	if err := os.Chmod(records, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(records, 0o700) })
	if _, stderr, code := runCLI(t, "conformance", "--config", configPath, "--notes"); code != 1 || !strings.Contains(stderr, "create workflow instance") {
		t.Fatalf("with the state read-only the ordinary reading exited %d, stderr = %q; the test is not reproducing a developer run's sandbox", code, stderr)
	}

	stdout, stderr, code = runCLI(t, "conformance", "--config", configPath, "--notes", "--from-export")
	if code != 0 {
		t.Fatalf("conformance --notes --from-export code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	for _, want := range []string{
		"<!-- yoyodyne:release-readiness -->\n",
		"ended in **ready**",
		"**goals** — conforms — 1 admitted item(s), 1 serving a recorded goal",
		"read from the tracker's export, .beads/issues.jsonl",
		"<!-- /yoyodyne:release-readiness -->\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("the notes section = %q, want it to contain %q", stdout, want)
		}
	}
	if after := directoryNames(t, records); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("a reading from the export wrote into the harness's state: before %v, after %v", before, after)
	}
}

// TestConformanceFromTheExportStillRefusesWhatGenuinelyFails is the other half:
// reading the export changes where the work is read from and nothing about what
// is judged. Work that names a goal nobody states is refused, and an export that
// is not there is a tracker that could not be read rather than an empty backlog.
func TestConformanceFromTheExportStillRefusesWhatGenuinelyFails(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeConformingProduct(t, project)

	stdout, stderr, code := runCLI(t, "conformance", "--config", configPath, "--notes", "--from-export")
	if code != 1 || !strings.Contains(stdout, "ended in **mismatch**") || !strings.Contains(stdout, "the work tracker could not be read") {
		t.Fatalf("with no export: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	writeTrackerExport(t, project,
		`{"_type":"issue","id":"item-1","title":"Admitted work","status":"open","priority":1,"issue_type":"task","notes":"Goal served: serving the chain"}`,
		`{"_type":"issue","id":"item-2","title":"Work serving nothing stated","status":"blocked","priority":2,"issue_type":"task","notes":"Goal served: a goal nobody states"}`,
	)
	stdout, stderr, code = runCLI(t, "conformance", "--config", configPath, "--notes", "--from-export")
	if code != 1 {
		t.Fatalf("an admitted item naming no stated goal passed: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	for _, want := range []string{"ended in **mismatch**", "**goals** — diverges", "item-2"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("the notes section = %q, want it to contain %q", stdout, want)
		}
	}

	// A notes failure the repository half finds is refused the same way.
	writeTrackerExport(t, project,
		`{"_type":"issue","id":"item-1","title":"Admitted work","status":"open","priority":1,"issue_type":"task","notes":"Goal served: serving the chain"}`,
	)
	writeArtifact(t, project, "docs/releases/v9.9.9.md", "# v9.9.9\n\nSee [the design](../designs/gone.md).\n")
	stdout, _, code = runCLI(t, "conformance", "--config", configPath, "--notes", "--from-export")
	if code != 1 || !strings.Contains(stdout, "**references** — diverges") {
		t.Fatalf("a broken link in the notes passed: code = %d, stdout = %q", code, stdout)
	}
}

func writeTrackerExport(t *testing.T, project string, lines ...string) {
	t.Helper()
	path := filepath.Join(project, ".beads", "issues.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// instanceDirectory is the one directory under the state home that holds a
// conformance reading's workflow record.
func instanceDirectory(t *testing.T, stateHome string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(stateHome, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "release-readiness-") {
			found = filepath.Dir(path)
		}
		return nil
	})
	if err != nil || found == "" {
		t.Fatalf("no conformance record under %s: %v", stateHome, err)
	}
	return found
}

func directoryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
