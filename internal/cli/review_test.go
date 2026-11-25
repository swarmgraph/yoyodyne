package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/review"
)

func TestReviewRequiresTheBaseItIsMeasuredAgainst(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"review"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "review requires --base") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"review", "--base", "main", "milestone"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("Run() with a positional argument code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "does not accept positional arguments") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// The outcome of a branch-scope repair verdict is that the branch is not
// approved. Nothing already integrated is touched by it, and the exit code is
// what stops anything downstream reading a repair or a failed review as an
// approval of the branch. It follows the verdict and nothing else: a truncation
// is reported and never refused here, which is the case below.
func TestReviewFailsUnlessTheAccumulatedChangeWasApproved(t *testing.T) {
	t.Parallel()

	approved := orchestrator.BranchReviewOutcome{
		ReviewID: "run-abcdef0123456789abcdef0123456789", Branch: "milestone", BaseRef: "main",
		BaseCommit: "1111111111111111111111111111111111111111",
		HeadCommit: "2222222222222222222222222222222222222222",
		Commits:    3, Decision: review.DecisionApprove, Summary: "the commits agree",
		SessionID: "branch-review-session", Model: "opus",
	}
	repaired := approved
	repaired.Decision = review.DecisionRepair
	repaired.Summary = "the two halves disagree about the key they use"
	repaired.Findings = []review.Finding{{
		Severity: review.SeverityMajor,
		Message:  "store.go writes under key and reader.go reads under other",
		Location: &review.Location{File: "reader.go", Line: 3},
	}}
	unanswered := approved
	unanswered.Decision = ""
	unanswered.Summary = ""

	for _, test := range []struct {
		name    string
		outcome orchestrator.BranchReviewOutcome
		err     error
		code    int
		want    string
	}{
		{name: "approved", outcome: approved, code: 0, want: "review: approve (session branch-review-session, model opus)"},
		{name: "repair", outcome: repaired, code: 1, want: "major [reader.go:3]"},
		{name: "no verdict", outcome: unanswered, err: errors.New("the reviewer never answered"), code: 1, want: "reviewed milestone against main"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			if code := reportBranchReview(&stdout, &stderr, false, test.outcome, test.err); code != test.code {
				t.Fatalf("reportBranchReview() code = %d, want %d (stderr %q)", code, test.code, stderr.String())
			}
			if !strings.Contains(stdout.String()+stderr.String(), test.want) {
				t.Fatalf("output = %q, want it to contain %q", stdout.String()+stderr.String(), test.want)
			}
		})
	}

	// A repair verdict says plainly that the work stays integrated, because the
	// harness does not undo promotions on a second opinion.
	var stdout, stderr bytes.Buffer
	reportBranchReview(&stdout, &stderr, false, repaired, nil)
	if !strings.Contains(stderr.String(), "stays integrated") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	// The JSON form answers the same question, and carries the verdict with it.
	stdout.Reset()
	stderr.Reset()
	if code := reportBranchReview(&stdout, &stderr, true, repaired, nil); code != 1 {
		t.Fatalf("reportBranchReview(--json) code = %d, want 1", code)
	}
	var decoded branchReviewOutput
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Review == nil || decoded.Review.Decision != review.DecisionRepair || len(decoded.Review.Findings) != 1 {
		t.Fatalf("decoded review = %#v", decoded.Review)
	}

	// A range whose test data alone outgrew the bound is one the reviewer may
	// approve, and the command exits zero on that approval as it does on any
	// other: the exit code answers whether the branch was approved, and the
	// truncation is reported beside it rather than refusing it. Reviewer.Review
	// is where the approval is decided, so this is the half of yoyodyne-ifd.425's
	// rule a reviewer test cannot reach — a refusal here would reject an approved
	// fixture-clipped range with nothing about the verdict in question.
	clipped := approved
	clipped.Truncated = true
	stdout.Reset()
	stderr.Reset()
	if code := reportBranchReview(&stdout, &stderr, false, clipped, nil); code != 0 {
		t.Fatalf("reportBranchReview() of an approved truncated range code = %d, want 0 (stderr %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "what was not shown is unreviewed") {
		t.Fatalf("stderr = %q, want the bound's omission reported beside the approval", stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "stays integrated") {
		t.Fatalf("an approved range was told what the branch now needs: %q", stdout.String()+stderr.String())
	}
}

// A shadow review is asked to measure the reviewer, not to judge the branch, so
// it exits on whether a verdict was obtained. A repair verdict is the ordinary
// result of one, and failing on it would make measuring a reviewer look like
// finding the branch wanting.
func TestAShadowReviewExitsOnWhetherItGotAVerdictRatherThanOnApproval(t *testing.T) {
	t.Parallel()

	shadowed := orchestrator.BranchReviewOutcome{
		ReviewID: "review-abcdef0123456789abcdef0123456789", Branch: "milestone", BaseRef: "main",
		BaseCommit: "1111111111111111111111111111111111111111",
		HeadCommit: "2222222222222222222222222222222222222222",
		Commits:    3, Decision: review.DecisionRepair, Summary: "the two halves disagree",
		Findings: []review.Finding{{
			Severity: review.SeverityMajor,
			Message:  "store.go writes under key and reader.go reads under other",
			Location: &review.Location{File: "reader.go", Line: 3},
		}},
		SessionID: "branch-review-session", Model: "sonnet", Shadow: true,
	}

	var stdout, stderr bytes.Buffer
	if code := reportBranchReview(&stdout, &stderr, false, shadowed, nil); code != 0 {
		t.Fatalf("reportBranchReview() of a shadow repair verdict code = %d, want 0 (stderr %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "shadow-reviewed milestone") {
		t.Errorf("stdout = %q", stdout.String())
	}
	// It says what it decided nothing about, and never that the branch needs
	// work: a shadow verdict is not the branch's answer to anything.
	if !strings.Contains(stdout.String(), "approves nothing") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "is not approved as an accumulated change") {
		t.Errorf("a shadow review reported on the branch: %q", stderr.String())
	}

	// A shadow review that never answered measured nothing, and says so through
	// the exit code the same way any other failed review does.
	stdout.Reset()
	stderr.Reset()
	unanswered := shadowed
	unanswered.Decision = ""
	unanswered.Findings = nil
	if code := reportBranchReview(&stdout, &stderr, false, unanswered, errors.New("the reviewer never answered")); code != 1 {
		t.Fatalf("reportBranchReview() of an unanswered shadow review code = %d, want 1", code)
	}

	// And an approving shadow verdict still approves nothing.
	approving := shadowed
	approving.Decision = review.DecisionApprove
	approving.Findings = nil
	if approving.Approved() {
		t.Error("a shadow verdict read as an approval of the branch")
	}
}

// Choosing the reviewer's model at the terminal is a measurement and only a
// measurement. A review the configuration did not choose the reviewer for must
// not be able to leave an approval of a branch behind it, and the refusal is
// where that is enforced rather than in whoever remembers to pass --shadow.
func TestReviewRefusesAModelChosenForAGatingReview(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"review", "--base", "main", "--model", "sonnet"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--model only makes sense with --shadow") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	// Comparing makes no review, so anything that describes one to make is
	// refused rather than ignored.
	for _, arguments := range [][]string{
		{"review", "--compare", "--base", "main"},
		{"review", "--compare", "--shadow"},
		{"review", "--compare", "--model", "sonnet"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(arguments, &stdout, &stderr, "test"); code != 2 {
			t.Fatalf("Run(%v) code = %d, want 2", arguments, code)
		}
		if !strings.Contains(stderr.String(), "reads the recorded reviews and makes none") {
			t.Fatalf("Run(%v) stderr = %q", arguments, stderr.String())
		}
	}
}

// `yoyo review` is a person's own command: with epic mode on and every epic
// ended it starts its reviewer as it does with epic mode never turned on, and
// nothing is kept as refused.
func TestReviewStartsWithEpicModeOnAndNoEpicOpen(t *testing.T) {
	// Not parallel: the state root and the PATH are set here.
	t.Setenv(execution.AgentRoleVariable, "")
	for _, epicMode := range []bool{false, true} {
		stateRoot, project, bin := t.TempDir(), t.TempDir(), t.TempDir()
		t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
		configPath := managedWorktreeProject(t, project)
		withReviewer := validConfig + "  reviewer:\n    role: reviewer\n    backend: claude-code\n    model: opus\n"
		if err := os.WriteFile(configPath, []byte(withReviewer), 0o600); err != nil {
			t.Fatal(err)
		}
		git(t, project, "branch", "base", "main~1")
		started := filepath.Join(bin, "started")
		script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + started + "'\nexit 3\n"
		if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if epicMode {
			operator, err := epicscope.ForOperator(stateRoot, "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := operator.Start(epicscope.Naming{Epic: "yoyodyne-ab1", From: "terminal"}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := operator.End("yoyodyne-ab1", "", time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		_, stderr, _ := runCLI(t, "review", "--base", "base", "--config", configPath)
		calls, err := os.ReadFile(started)
		if err != nil || !strings.Contains(string(calls), "--name yoyodyne-review-") {
			t.Errorf("epic mode %t: the reviewer was started with %q (%v); stderr %q", epicMode, calls, err, stderr)
		}
		if _, err := os.Stat(filepath.Join(stateRoot, "projects", "yoyodyne", "not-started.json")); !os.IsNotExist(err) {
			t.Errorf("epic mode %t: something was kept as refused: %v", epicMode, err)
		}
	}
}
