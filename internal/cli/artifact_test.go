package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestArtifactsAreListedAndShownWithTheirIdentity(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", `---
id: brief
kind: brief
title: Product brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T12:00:00Z
      reason: identity added when artifact governance arrived
---

Intent in, software out.
`)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", `---
id: v1-goals
kind: goals
title: V1 goals
supports:
    - brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T12:00:00Z
      reason: identity added when artifact governance arrived
---

## Goals

- Something.
`)
	// A document in an artifact home with no identity is not an artifact anything
	// can refer to, so it is named rather than silently governed.
	writeArtifact(t, project, "docs/decisions/undecided.md", "# A decision nobody identified\n")

	stdout, stderr, code := runCLI(t, "artifact", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"brief [brief, active] Product brief", "v1-goals [goals, active] V1 goals", "supports: brief"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list stdout = %q, want it to contain %q", stdout, want)
		}
	}
	if !strings.Contains(stderr, "not an artifact: docs/decisions/undecided.md") {
		t.Fatalf("list stderr = %q", stderr)
	}

	stdout, stderr, code = runCLI(t, "artifact", "list", "--config", configPath, "--kind", "goals")
	if code != 0 {
		t.Fatalf("list --kind code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "brief [brief") || !strings.Contains(stdout, "v1-goals") {
		t.Fatalf("list --kind stdout = %q", stdout)
	}

	stdout, stderr, code = runCLI(t, "artifact", "show", "--config", configPath, "--json", "v1-goals")
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	var shown struct {
		Artifacts []struct {
			ID        string   `json:"id"`
			Kind      string   `json:"kind"`
			Status    string   `json:"status"`
			Supports  []string `json:"supports"`
			Path      string   `json:"path"`
			Revisions []struct {
				Action string `json:"action"`
				By     string `json:"by"`
			} `json:"revisions"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(shown.Artifacts) != 1 {
		t.Fatalf("shown = %#v", shown)
	}
	goals := shown.Artifacts[0]
	if goals.Kind != "goals" || goals.Status != "active" || goals.Path != "docs/product/goals/v1-goals.md" {
		t.Fatalf("goals = %#v", goals)
	}
	if len(goals.Supports) != 1 || goals.Supports[0] != "brief" || len(goals.Revisions) != 1 || goals.Revisions[0].By != "product-manager" {
		t.Fatalf("goals = %#v", goals)
	}

	if _, stderr, code = runCLI(t, "artifact", "show", "--config", configPath, "nothing-by-this-name"); code != 1 {
		t.Fatalf("show of an unknown id code = %d, stderr = %q", code, stderr)
	}
}

func TestBrokenArtifactRelationshipsAreReportedRatherThanRefused(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	// A design naming a goal nobody wrote, and a goal naming nothing at all.
	writeArtifact(t, project, "docs/designs/v1-harness.md", artifactDocument("v1-harness", "design", "V1 harness design", []string{"goals-that-moved"}))
	writeArtifact(t, project, "docs/product/goals/loose-goals.md", artifactDocument("loose-goals", "goals", "Goals nobody tied to the brief", nil))

	stdout, stderr, code := runCLI(t, "artifact", "list", "--config", configPath)
	// Surfacing rather than refusing: the listing succeeds and still holds every
	// document, and what is wrong with the chain goes to stderr beside it.
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"brief [brief, active]", "v1-harness [design, active]", "loose-goals [goals, active]"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list stdout = %q, want it to contain %q", stdout, want)
		}
	}
	for _, want := range []string{
		`dangling-reference: v1-harness (docs/designs/v1-harness.md)`,
		`"goals-that-moved"`,
		`orphan: loose-goals (docs/product/goals/loose-goals.md)`,
		"names nothing upstream",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("list stderr = %q, want it to contain %q", stderr, want)
		}
	}
	// The brief is the root and is not reported for supporting nothing.
	if strings.Contains(stderr, "orphan: brief") {
		t.Fatalf("the brief was reported as an orphan: %q", stderr)
	}

	// Asking after one document says what is wrong with that one document.
	stdout, stderr, code = runCLI(t, "artifact", "show", "--config", configPath, "--json", "v1-harness")
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	var shown struct {
		ReferenceProblems []struct {
			Kind   string `json:"kind"`
			ID     string `json:"id"`
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"reference_problems"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	// Both a dangling reference and, because that reference was its only way
	// upstream, nothing connecting it to the brief.
	if len(shown.ReferenceProblems) != 2 {
		t.Fatalf("reference problems = %#v", shown.ReferenceProblems)
	}
	for _, problem := range shown.ReferenceProblems {
		if problem.ID != "v1-harness" || problem.Path != "docs/designs/v1-harness.md" {
			t.Fatalf("problem = %#v", problem)
		}
	}
}

func TestApprovalIsRecordedAgainstTheDocumentAndSurvivesReadingItBack(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"}))
	writeArtifact(t, project, "docs/designs/v1-harness.md", artifactDocument("v1-harness", "design", "V1 harness design", []string{"v1-goals"}))

	stdout, stderr, code := runCLI(t, "artifact", "approve", "--config", configPath, "brief",
		"--reason", "approved by the operator in conversation on 2026-08-17")
	if code != 0 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"approved as it stands", "given by the operator", "no gate moved"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("approve stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// What the configuration asks for is what each unapproved document is
	// reported against: the goals are the operator's to approve and the design,
	// under `approvals.designs: automatic`, is asked of nobody.
	stdout, stderr, code = runCLI(t, "artifact", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"approval: approved as it stands",
		"none recorded, and approvals.goals is human, so this document is yours to approve",
		"none recorded; approvals.designs is automatic, so none is asked for",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list stdout = %q, want it to contain %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "artifact", "show", "--config", configPath, "--json", "brief")
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	var shown struct {
		Approvals map[string]struct {
			State    string `json:"state"`
			Required bool   `json:"required"`
			Setting  string `json:"setting"`
			Mode     string `json:"mode"`
			Approval *struct {
				Revision int    `json:"revision"`
				By       string `json:"by"`
				Reason   string `json:"reason"`
			} `json:"approval"`
			RevisionsSinceApproval int `json:"revisions_since_approval"`
		} `json:"approvals"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	brief := shown.Approvals["brief"]
	if brief.State != "approved" || !brief.Required || brief.Setting != "approvals.brief" || brief.Mode != "human" {
		t.Fatalf("brief approval = %#v", brief)
	}
	if brief.Approval == nil || brief.Approval.Revision != 0 || brief.Approval.By != "operator" {
		t.Fatalf("brief approval = %#v", brief.Approval)
	}

	// The document is edited in the file by the role that owns it, which is how
	// an approval comes to be about a version that no longer exists. It reads as
	// what it is rather than staying quietly approved.
	writeArtifact(t, project, "docs/product/brief.md",
		strings.TrimSuffix(artifactDocument("brief", "brief", "Product brief", nil), "---\n")+
			"    - action: amended\n      by: product-manager\n      at: 2026-08-18T12:00:00Z\n      reason: restated what the product is for\n"+
			"approvals:\n    - revision: 0\n      by: operator\n      at: 2026-08-17T12:00:00Z\n      reason: approved by the operator in conversation\n"+
			"---\n\nIntent in, software out.\n")

	stdout, stderr, code = runCLI(t, "artifact", "show", "--config", configPath, "brief")
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"approved and amended since",
		"one revision was recorded after it",
		"approved by the operator 2026-08-17T12:00:00Z: approved by the operator in conversation",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("show stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// Approving again is what makes the current document approved, and it is
	// recorded against the revision that changed it rather than replacing what
	// was approved before.
	if _, stderr, code = runCLI(t, "artifact", "approve", "--config", configPath, "brief", "--reason", "approved again after the restatement"); code != 0 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	stdout, _, _ = runCLI(t, "artifact", "show", "--config", configPath, "brief")
	// Both approvals are printed, each under the revision it was given for: the
	// record of how the document came to be approved is the point of keeping it.
	if !strings.Contains(stdout, "approved as it stands") || strings.Count(stdout, "\n  approved by the operator ") != 2 {
		t.Fatalf("show stdout = %q", stdout)
	}
}

func TestApprovingWithoutSayingHowItWasGivenIsRefused(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	before, err := os.ReadFile(filepath.Join(project, "docs/product/brief.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// This record speaks for a person, so it says how they gave it or it is not
	// recorded at all.
	if _, stderr, code := runCLI(t, "artifact", "approve", "--config", configPath, "brief"); code != 1 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	if _, stderr, code := runCLI(t, "artifact", "approve", "--config", configPath, "nothing-by-this-name", "--reason", "because"); code != 1 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	after, err := os.ReadFile(filepath.Join(project, "docs/product/brief.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("a refused approval wrote to the document: %q", after)
	}
}

// The write an approval performs lands in the operator's own checkout and stops
// there, which costs them a dirty checkout that every run then refuses to start
// over. The command that performed the write is what says so, at the moment it
// writes, rather than leaving them to meet it as an unexplained refusal from the
// next command they run.
func TestApprovingSaysTheWriteLandedInTheCheckoutAndIsYoursToCommit(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"}))

	stdout, stderr, code := runCLI(t, "artifact", "approve", "--config", configPath, "brief",
		"--reason", "approved by the operator in conversation on 2026-08-17")
	if code != 0 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	// The file is named, because what the operator has to commit is that file and
	// it is what the run's refusal will name back at them. So is the checkout it
	// landed in: which one that is follows the configuration this command was
	// given, and the run that refuses to start is a run over that one.
	if !strings.Contains(stdout, artifact.PendingCommit(project, "docs/product/brief.md")) {
		t.Fatalf("approve stdout = %q, want it to say where the write landed", stdout)
	}

	// A machine-readable caller is the operator's own script, so it is told the
	// same thing in the same words rather than left to infer it from the prose.
	stdout, stderr, code = runCLI(t, "artifact", "approve", "--config", configPath, "--json", "v1-goals",
		"--reason", "approved with the adoption goal added")
	if code != 0 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	var written struct {
		PendingCommit string `json:"pending_commit"`
	}
	if err := json.Unmarshal([]byte(stdout), &written); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if written.PendingCommit != artifact.PendingCommit(project, "docs/product/goals/v1-goals.md") {
		t.Fatalf("pending_commit = %q", written.PendingCommit)
	}

	// Nothing that only reads says it: a listing is not a write, and telling a
	// reader to commit something nothing changed would be an invented obligation.
	for _, read := range [][]string{
		{"artifact", "list", "--config", configPath},
		{"artifact", "show", "--config", configPath, "brief"},
		{"artifact", "show", "--config", configPath, "--json", "brief"},
	} {
		stdout, _, code = runCLI(t, read...)
		if code != 0 {
			t.Fatalf("%v code = %d", read, code)
		}
		if strings.Contains(stdout, pendingCommitSaid) {
			t.Fatalf("%v stdout = %q, want nothing about committing", read, stdout)
		}
	}
}

// pendingCommitSaid is the part of that sentence a command saying nothing about
// committing must not contain. It is the opening rather than the whole, because
// the rest of the sentence names a path and a checkout that differ per case.
const pendingCommitSaid = "is now an uncommitted change in"

// Which checkout the write landed in is the configuration's answer rather than a
// constant: `--config` naming another project, `YOYODYNE_CONFIG` in the
// environment, and a linked worktree carrying its own `.yoyodyne` each point
// `approve` at a different repository. The run that refuses to start is a run
// over that one, so the sentence names it — an operator told "your checkout"
// about a write that landed somewhere else goes looking for a dirty file that is
// not there.
func TestApprovingNamesTheCheckoutTheWriteLandedIn(t *testing.T) {
	t.Parallel()

	// Two projects, each with its own configuration and its own brief, which is
	// what pointing one command at two repositories looks like from here.
	first := writeConfig(t, validConfig)
	firstProject := filepath.Dir(first)
	writeArtifact(t, firstProject, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	second := writeConfig(t, validConfig)
	secondProject := filepath.Dir(second)
	writeArtifact(t, secondProject, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))

	for _, approved := range []struct {
		configPath string
		project    string
		other      string
	}{
		{first, firstProject, secondProject},
		{second, secondProject, firstProject},
	} {
		stdout, stderr, code := runCLI(t, "artifact", "approve", "--config", approved.configPath, "brief",
			"--reason", "approved by the operator in conversation")
		if code != 0 {
			t.Fatalf("approve code = %d, stderr = %q", code, stderr)
		}
		// Asserted against the checkout's own path rather than against what
		// PendingCommit returns: comparing the command's output to the function it
		// calls says the two agree and nothing about what either says, and a
		// sentence that named no checkout would satisfy it.
		if !strings.Contains(stdout, approved.project) {
			t.Fatalf("approve stdout = %q, want it to name the checkout %s", stdout, approved.project)
		}
		if !strings.Contains(stdout, artifact.PendingCommit(approved.project, "docs/product/brief.md")) {
			t.Fatalf("approve stdout = %q, want the sentence the package owns", stdout)
		}
		// And names only the one it wrote to: a sentence that could name either is
		// one an operator cannot act on.
		if strings.Contains(stdout, approved.other) {
			t.Fatalf("approve stdout = %q, want nothing about %s", stdout, approved.other)
		}
	}

	// The negative assertions elsewhere match on the opening of that sentence, so
	// it has to still be the opening of it.
	if !strings.Contains(artifact.PendingCommit(firstProject, "docs/product/brief.md"), pendingCommitSaid) {
		t.Fatalf("PendingCommit no longer contains %q", pendingCommitSaid)
	}
}

// A refused approval writes nothing, so it says nothing about committing. The
// four refusals are the whole of what `approve` turns away: no reason, no such
// artifact, one already approved as it stands, and one that was retired.
func TestARefusedApprovalSaysNothingAboutCommitting(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		strings.TrimSuffix(artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"}), "---\n")+
			"    - action: retired\n      by: product-manager\n      at: 2026-08-18T12:00:00Z\n      reason: the v1 goals were met\n"+
			"---\n")
	// Retirement has to agree with the status, which is what makes this document a
	// retired one rather than a malformed one.
	retired, err := os.ReadFile(filepath.Join(project, "docs/product/goals/v1-goals.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		strings.Replace(string(retired), "status: active", "status: retired", 1))

	if _, stderr, code := runCLI(t, "artifact", "approve", "--config", configPath, "brief",
		"--reason", "approved in conversation"); code != 0 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}

	for _, refused := range []struct {
		args []string
		// because is what the refusal has to say, so a case that stopped for some
		// other reason is not counted as having covered this one.
		because string
	}{
		// No reason: this record speaks for a person.
		{[]string{"artifact", "approve", "--config", configPath, "brief"},
			"needs a reason saying how the approval was given"},
		// No such artifact.
		{[]string{"artifact", "approve", "--config", configPath, "nothing-by-this-name", "--reason", "because"},
			`no artifact "nothing-by-this-name" is recorded`},
		// Already approved as it stands, so there is nothing to record.
		{[]string{"artifact", "approve", "--config", configPath, "brief", "--reason", "approving it twice"},
			"is already approved as it stands"},
		// Retired, and not approved afterwards.
		{[]string{"artifact", "approve", "--config", configPath, "v1-goals", "--reason", "approving what was retired"},
			"was retired on 2026-08-18T12:00:00Z and is not approved afterwards"},
	} {
		stdout, stderr, code := runCLI(t, refused.args...)
		if code != 1 || !strings.Contains(stderr, refused.because) {
			t.Fatalf("%v code = %d, stderr = %q, want %q", refused.args, code, stderr, refused.because)
		}
		if strings.Contains(stdout, pendingCommitSaid) ||
			strings.Contains(stderr, pendingCommitSaid) {
			t.Fatalf("%v said something was written: stdout = %q, stderr = %q", refused.args, stdout, stderr)
		}
	}

	// And the same in the machine-readable form, where an error is reported in
	// place of the write rather than beside it.
	stdout, _, code := runCLI(t, "artifact", "approve", "--config", configPath, "--json",
		"nothing-by-this-name", "--reason", "because")
	if code != 1 {
		t.Fatalf("approve code = %d", code)
	}
	var refused struct {
		PendingCommit string `json:"pending_commit"`
		Error         string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &refused); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if refused.PendingCommit != "" || refused.Error == "" {
		t.Fatalf("refused approval = %#v", refused)
	}
}

// artifactDocument renders a well-formed artifact's frontmatter, so a test about
// the relationships between documents says only what they support. The creating
// role is the one that owns the kind, because a revision recorded by any other
// is an artifact the harness refuses.
func artifactDocument(id, kind, title string, supports []string) string {
	owner, _ := artifact.Owner(artifact.Kind(kind))
	var rendered strings.Builder
	rendered.WriteString("---\nid: " + id + "\nkind: " + kind + "\ntitle: " + title + "\n")
	if len(supports) > 0 {
		rendered.WriteString("supports:\n")
		for _, reference := range supports {
			rendered.WriteString("    - " + reference + "\n")
		}
	}
	rendered.WriteString("status: active\nrevisions:\n")
	rendered.WriteString("    - action: created\n      by: " + string(owner) + "\n      at: 2026-08-17T12:00:00Z\n      reason: recorded when identity arrived\n")
	rendered.WriteString("---\n")
	return rendered.String()
}

func writeArtifact(t *testing.T, project, relative, content string) {
	t.Helper()
	path := filepath.Join(project, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// Recording goal identifiers is the owning role's write and asks the operator
// for nothing: an approved goals document stays approved as it stands, and a
// body that changed a goal's words is refused without touching the file.
func TestIdentifyingGoalsLeavesTheApprovalStandingAndRefusesAnyOtherChange(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		strings.TrimSuffix(artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"}), "---\n")+
			"approvals:\n    - revision: 0\n      by: operator\n      at: 2026-08-17T13:00:00Z\n      reason: approved in conversation\n"+
			"---\n\n## Goals\n\n- Maintain a traceable chain.\n")
	body := filepath.Join(t.TempDir(), "v1-goals.md")
	if err := os.WriteFile(body, []byte("## Goals\n\n- Maintain an auditable chain.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	document := filepath.Join(project, "docs/product/goals/v1-goals.md")
	before, err := os.ReadFile(document)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if _, stderr, code := runCLI(t, "artifact", "identify", "--config", configPath, "v1-goals",
		"--body-file", body, "--reason", "identifiers"); code != 1 || !strings.Contains(stderr, "is an amendment") {
		t.Fatalf("identify code = %d, stderr = %q", code, stderr)
	}
	if after, _ := os.ReadFile(document); string(after) != string(before) {
		t.Fatalf("a refused identity revision wrote to the document: %q", after)
	}

	if err := os.WriteFile(body, []byte("## Goals\n\n- [traceable-chain] Maintain a traceable chain.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stdout, stderr, code := runCLI(t, "artifact", "identify", "--config", configPath, "v1-goals",
		"--body-file", body, "--reason", "yoyodyne-ifd.344 - goal identifiers recorded")
	if code != 0 {
		t.Fatalf("identify code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"identity revision by the product-manager", "approval: approved as it stands", "uncommitted change"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("identify stdout = %q, want it to contain %q", stdout, want)
		}
	}
	if after, _ := os.ReadFile(document); !strings.Contains(string(after), "action: identified") ||
		!strings.Contains(string(after), "- [traceable-chain] Maintain a traceable chain.") {
		t.Fatalf("document = %q", after)
	}
}

// A goals document the Lead Product Manager has reworded under the operator's
// approval reads as approved and says so, so a document that no longer reads word
// for word as it was approved is not silently the same thing. An amendment
// labelled consistent that the record does not carry — no item named — is still
// counted, and the reason it is counted is said, so the label does not read as
// ignored.
func TestAnApprovedGoalsDocumentNamesTheRewordingsItStandsThrough(t *testing.T) {
	t.Parallel()

	approvedAt := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	goals := artifact.Artifact{
		ID: "v1-goals", Kind: artifact.KindGoals,
		Revisions: []artifact.Revision{
			{Action: artifact.ActionCreated, By: domain.RoleProductManager, At: approvedAt, Reason: "recorded"},
			{Action: artifact.ActionAmended, By: domain.RoleProductManager, At: approvedAt.Add(time.Hour),
				Reason: "yoyodyne-ifd.437.11 - the autonomy goal names the Lead Product Manager", Intent: artifact.IntentConsistent},
		},
		Approvals: []artifact.Approval{{Revision: 0, By: artifact.ApproverOperator, At: approvedAt, Reason: "approved"}},
	}
	policy := artifact.Policy{Goals: domain.ApprovalHuman}
	rendered := renderArtifactApproval(goals, policy)
	for _, want := range []string{"approved as it stands", "the Lead Product Manager has since recorded 1 rewording as consistent with intent"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}

	goals.Revisions = append(goals.Revisions, artifact.Revision{Action: artifact.ActionAmended, By: domain.RoleProductManager,
		At: approvedAt.Add(2 * time.Hour), Reason: "tightened the wording", Intent: artifact.IntentConsistent})
	rendered = renderArtifactApproval(goals, policy)
	for _, want := range []string{
		"approved and amended since",
		"one revision was recorded after it",
		"1 rewording as consistent with intent",
		"revision 2 is labelled consistent and still counts, because its reason does not open with the work item that directed it",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}
}

func TestRulesRecordingExistingIntentAreListedAndShownWithoutOperatorApproval(t *testing.T) {
	t.Parallel()

	for _, intent := range []artifact.Intent{artifact.IntentConsistent, artifact.IntentFundamental, ""} {
		t.Run(string(intent), func(t *testing.T) {
			t.Parallel()
			configPath := writeConfig(t, validConfig)
			resolved, err := loadConfiguration(configPath)
			if err != nil {
				t.Fatal(err)
			}
			store := artifactStore(filepath.Dir(configPath), resolved.Config.Product)
			reason := "yoyodyne-ifd.433.17 - records rules the operator already gave"
			if _, err := store.Create(domain.RoleProductManager, artifact.Draft{
				ID: "operating-rules", Kind: artifact.KindRules, Title: "Operating rules",
				Directory: resolved.Config.Product.Specifications, Body: "## Rules\n\n- Notes are append-only.",
				Reason: reason, Intent: intent,
			}, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			for _, command := range [][]string{{"list"}, {"show", "operating-rules"}} {
				args := append([]string{"artifact"}, command...)
				args = append(args, "--config", configPath)
				stdout, stderr, code := runCLI(t, args...)
				if code != 0 {
					t.Fatalf("%v code = %d, stderr = %q", command, code, stderr)
				}
				if intent == artifact.IntentConsistent {
					for _, want := range []string{"approved as it stands", "recorded by the Lead Product Manager", "as consistent with intent", reason} {
						if !strings.Contains(stdout, want) {
							t.Fatalf("%v output = %q, want %q", command, stdout, want)
						}
					}
					if strings.Contains(stdout, "yours to approve") || strings.Contains(stdout, "given by the operator") {
						t.Fatalf("delegated creation was put to or attributed to the operator: %q", stdout)
					}
				} else if !strings.Contains(stdout, "yours to approve") {
					t.Fatalf("%v output = %q; want an operator approval for %q", command, stdout, intent)
				}
				stdout, stderr, code = runCLI(t, append(args, "--json")...)
				if code != 0 {
					t.Fatalf("%v --json code = %d, stderr = %q", command, code, stderr)
				}
				var output artifactOutput
				if err := json.Unmarshal([]byte(stdout), &output); err != nil {
					t.Fatal(err)
				}
				approval := output.Approvals["operating-rules"]
				if intent == artifact.IntentConsistent {
					if approval.State != artifact.ApprovalApproved || approval.Approval != nil || approval.DelegatedCreation == nil || approval.DelegatedCreation.Reason != reason {
						t.Fatalf("delegated approval = %#v", approval)
					}
				} else if approval.State != artifact.ApprovalUnapproved || approval.DelegatedCreation != nil {
					t.Fatalf("unapproved creation = %#v", approval)
				}
			}
			if intent == artifact.IntentConsistent {
				body := "## Rules\n\n- Notes may be replaced."
				if _, err := store.Amend(domain.RoleProductManager, "operating-rules", artifact.Amendment{
					Body: &body, Intent: artifact.IntentFundamental, Reason: "yoyodyne-ifd.433.19 - new intent",
				}, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)); err != nil {
					t.Fatal(err)
				}
				stdout, stderr, code := runCLI(t, "artifact", "show", "operating-rules", "--config", configPath)
				if code != 0 || !strings.Contains(stdout, "approved and amended since") || !strings.Contains(stdout, reason) {
					t.Fatalf("fundamental revision: code %d, output %q, stderr %q", code, stdout, stderr)
				}
			}
		})
	}
}
