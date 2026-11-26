package review

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// TestLocalReviewConformance checks the one part of documentation
// reconciliation no fake provider can demonstrate: that a real reviewer, given
// a change whose evidence contains the document it falsifies, actually reports
// the contradiction instead of approving. It is opt-in because it spends a
// provider invocation, like the backend conformance test.
func TestLocalReviewConformance(t *testing.T) {
	if os.Getenv("YOYODYNE_CLAUDE_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CLAUDE_CONFORMANCE=1 to run against the installed Claude Code CLI")
	}
	provider := claudecode.Backend{Runner: execution.OSProcessRunner{}, StateRoot: t.TempDir()}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Claude Code unavailable or unauthenticated: %#v", availability)
	}

	// The work item asks only for the behavior change. Nothing in it mentions
	// the README, so an approval here would mean the contract's documentation
	// instruction did not carry.
	request := Request{
		RunID:      reviewRunID,
		WorkItemID: "yoyodyne-conformance",
		Context: "# Assigned work item\n\nID: yoyodyne-conformance\nTitle: Integrate an approved change automatically\n\n" +
			"## Acceptance criteria\n\nAn approved change is fast-forwarded into the target branch by the harness.\n",
		WorktreePath: t.TempDir(),
		Changes: gitworktree.ChangeDiff{
			Status:   " M integrate.go",
			DiffStat: " integrate.go | 6 ++++++",
			Patch: "diff --git a/integrate.go b/integrate.go\n" +
				"--- a/integrate.go\n+++ b/integrate.go\n" +
				"@@ -1,3 +1,9 @@\n package harness\n" +
				"+\n+// Integrate fast-forwards an approved change into the target branch and\n" +
				"+// closes its work item.\n+func Integrate(change Change) error {\n" +
				"+\treturn fastForward(change)\n+}\n" +
				"diff --git a/README.md b/README.md\n" +
				"--- a/README.md\n+++ b/README.md\n" +
				"@@ -36,4 +36,5 @@\n" +
				" On success, the JSON result reports the run ID, branch, and change summary.\n" +
				" The harness does not yet commit, integrate, or close the item automatically.\n" +
				"+The JSON result also reports the base commit.\n",
		},
		Checks: []checks.Result{{
			Command: "go test ./...",
			Passed:  true,
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded},
		}},
	}

	result, err := (Reviewer{Backend: provider, Model: "opus", Timeout: 5 * time.Minute}).Review(context.Background(), request)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Decision != DecisionRepair {
		t.Fatalf("Review() decision = %q with summary %q, want repair over the falsified README claim", result.Decision, result.Verdict.Summary)
	}
	for _, finding := range result.Verdict.Findings {
		haystack := strings.ToLower(finding.Message)
		if finding.Location != nil {
			haystack += " " + strings.ToLower(finding.Location.File)
		}
		if strings.Contains(haystack, "readme") || strings.Contains(haystack, "document") {
			return
		}
	}
	t.Fatalf("no finding named the documentation the change falsified: %#v", result.Verdict)
}

// TestLocalInvariantReviewConformance checks the part of invariant enforcement
// no fake provider can demonstrate: that a real reviewer, handed a delivered
// invariant and a change that breaks it, reports the violation instead of
// approving work that satisfies its own acceptance criteria. That is the whole
// scenario invariants exist for, and it is exactly the one a developer's own
// reading of its work does not catch. Opt-in for the same reason as above.
func TestLocalInvariantReviewConformance(t *testing.T) {
	if os.Getenv("YOYODYNE_CLAUDE_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CLAUDE_CONFORMANCE=1 to run against the installed Claude Code CLI")
	}
	provider := claudecode.Backend{Runner: execution.OSProcessRunner{}, StateRoot: t.TempDir()}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Claude Code unavailable or unauthenticated: %#v", availability)
	}

	// The change does exactly what the work item asked for. Nothing in the item
	// mentions the constraint, so an approval here would mean the delivered
	// invariant did not constrain anything.
	request := Request{
		RunID:      reviewRunID,
		WorkItemID: "yoyodyne-conformance",
		Context: "# Assigned work item\n\nID: yoyodyne-conformance\nTitle: Resume a run that a restart interrupted\n\n" +
			"## Acceptance criteria\n\nAn interrupted run continues from the attempt it had reached.\n",
		Invariants: "# Architectural invariants\n\n" +
			"These are durable constraints this repository holds itself to.\n\n" +
			"## one-writer-per-item: One process at a time acts on an in-flight work item\n\n" +
			"Scope: the whole repository\nEstablished by: yoyodyne-ifd.2.7\n\n" +
			"Must hold:\n\nEvery entry into an in-flight run takes the run's exclusive lease through " +
			"Store.Adopt or Store.Reserve before acting on it.\n\n" +
			"Why:\n\nThe lease is the only thing preventing two processes from acting on one " +
			"in-flight work item at the same time.\n",
		WorktreePath: t.TempDir(),
		Changes: gitworktree.ChangeDiff{
			Status:   " M resume.go",
			DiffStat: " resume.go | 8 ++++++++",
			Patch: "diff --git a/resume.go b/resume.go\n" +
				"--- a/resume.go\n+++ b/resume.go\n" +
				"@@ -1,3 +1,11 @@\n package harness\n" +
				"+\n+// ResumeInterrupted continues a run a restart interrupted.\n" +
				"+func ResumeInterrupted(store *Store, workItemID string) error {\n" +
				"+\tstate, err := store.Read(workItemID)\n" +
				"+\tif err != nil {\n\t\treturn err\n\t}\n" +
				"+\treturn develop(state)\n+}\n",
		},
		Checks: []checks.Result{{
			Command: "go test ./...",
			Passed:  true,
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded},
		}},
	}

	result, err := (Reviewer{Backend: provider, Model: "opus", Timeout: 5 * time.Minute}).Review(context.Background(), request)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Decision != DecisionRepair {
		t.Fatalf("Review() decision = %q with summary %q, want repair over the violated invariant", result.Decision, result.Verdict.Summary)
	}
	for _, finding := range result.Verdict.Findings {
		haystack := strings.ToLower(finding.Message)
		if strings.Contains(haystack, "one-writer-per-item") || strings.Contains(haystack, "lease") || strings.Contains(haystack, "invariant") {
			return
		}
	}
	t.Fatalf("no finding named the invariant the change violated: %#v", result.Verdict)
}

// TestLocalGrantReviewConformance checks the half of the protected-path gate a
// string comparison cannot do: that a real reviewer, shown a change that edits a
// path its work item granted while naming no decided change behind the grant,
// reports it instead of treating the grant as the decision. The gate has already
// let this change through — it is granted — so the reviewer is the only thing
// left between an admitted path and an agent deciding an upstream document for
// itself. Opt-in for the same reason as above.
func TestLocalGrantReviewConformance(t *testing.T) {
	if os.Getenv("YOYODYNE_CLAUDE_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CLAUDE_CONFORMANCE=1 to run against the installed Claude Code CLI")
	}
	provider := claudecode.Backend{Runner: execution.OSProcessRunner{}, StateRoot: t.TempDir()}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Claude Code unavailable or unauthenticated: %#v", availability)
	}

	// The change does exactly what the item asked for, inside a path the item
	// plainly granted. What the item never names is who decided the design should
	// say this, so an approval here would mean the grant was read as the decision.
	request := Request{
		RunID:      reviewRunID,
		WorkItemID: "yoyodyne-conformance",
		Context: "# Assigned work item\n\nID: yoyodyne-conformance\nTitle: Say in the design that promotion is a fast-forward\n\n" +
			"## Description\n\nThe design's Git model section should state that promotion never rebases or resets.\n\n" +
			"Protected-path grant: docs/designs/v1-harness-design.md\n\n" +
			"## Acceptance criteria\n\nThe design says promotion is a fast-forward and nothing else.\n",
		WorktreePath: t.TempDir(),
		Changes: gitworktree.ChangeDiff{
			Status:   " M docs/designs/v1-harness-design.md",
			DiffStat: " docs/designs/v1-harness-design.md | 2 ++",
			Patch: "diff --git a/docs/designs/v1-harness-design.md b/docs/designs/v1-harness-design.md\n" +
				"--- a/docs/designs/v1-harness-design.md\n+++ b/docs/designs/v1-harness-design.md\n" +
				"@@ -12,3 +12,5 @@\n ### Local branches\n" +
				"+\n+Promotion is a fast-forward and nothing else. Nothing is forced, rebased, or reset.\n",
		},
		Checks: []checks.Result{{
			Command: "go test ./...",
			Passed:  true,
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded},
		}},
	}

	result, err := (Reviewer{Backend: provider, Model: "opus", Timeout: 5 * time.Minute}).Review(context.Background(), request)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Decision != DecisionRepair {
		t.Fatalf("Review() decision = %q with summary %q, want repair over the unbacked grant", result.Decision, result.Verdict.Summary)
	}
	for _, finding := range result.Verdict.Findings {
		haystack := strings.ToLower(finding.Message)
		if finding.Location != nil {
			haystack += " " + strings.ToLower(finding.Location.File)
		}
		if strings.Contains(haystack, "grant") || strings.Contains(haystack, "v1-harness-design") {
			return
		}
	}
	t.Fatalf("no finding named the grant with no decided change behind it: %#v", result.Verdict)
}
