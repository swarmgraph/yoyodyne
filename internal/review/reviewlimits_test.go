package review

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// largestItemsMeasured is how many of the tracker's largest items have their
// whole context assembled. Assembling every item reads the product intent once
// each, and the context grows with the item's own text, so the largest few are
// where the largest context is.
const largestItemsMeasured = 12

// changedLine is the line every file of a measured change is given.
const changedLine = "a line the change adds to the file\n"

// This test is a measurement (docs/decisions/review-size-limits.md, section 6):
// it rebuilds the largest review this repository would send, from what the
// repository holds, beside a diff at the highest limit a project may set today,
// in the two shapes a change at that limit can take, and reports its figures.
// The review is not required to fit the fixed total.
//
// The evidence that does not depend on the change:
//   - the work item context: assembled, as a review assembles it, for each of
//     the largest items in the tracker's export, with the product intent this
//     repository delivers; the largest is kept;
//   - every invariant this repository records, whatever its scope;
//   - the reviewer persona this project binds (.yoyodyne/personas/reviewer.md);
//   - every check this project configures, each failing with both streams at
//     the length a review quotes, and the forge's checks and the passed reruns
//     of failing tests at a few KiB each;
//   - the developer's latest summary and one earlier one for each repair
//     attempt this project allows (execution.repair_attempts_before_replan),
//     each at the length the run record keeps;
//   - the share of the review set aside for the record of MCP calls, whole.
//
// The change itself, rendered by gitworktree.Manager.UnifiedChanges over a real
// repository exactly as a review renders it, in two shapes, each with nothing
// kept out so the size check lets it through:
//   - one file whose diff fills the limit, which is the least a change adds
//     beside its patch;
//   - many small files whose diffs together fill the limit. Its status
//     listing, diff stat and list of changed files are shown whole beside the
//     patch, and they grow with the number of files rather than with any limit
//     a project sets.
//
// For each shape the test reports how large the review is and the highest diff
// limit that shape really fits. What is asserted is that the evidence could be
// assembled whole and that the size check lets the change through.
func TestTheReviewEvidenceBesideTheHighestDiffLimitIsMeasured(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	project, err := config.Load(filepath.Join(root, ".yoyodyne", "config.yaml"))
	if err != nil {
		t.Fatalf("load this repository's configuration: %v", err)
	}

	items, err := beads.ReadExport(root, nil)
	if err != nil {
		t.Fatalf("read this repository's tracker export: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("this repository's tracker export holds no items, so nothing measures the largest context")
	}
	sort.SliceStable(items, func(i, j int) bool { return itemTextBytes(items[i]) > itemTextBytes(items[j]) })
	largestContext, largestItem := "", ""
	for _, item := range items[:min(largestItemsMeasured, len(items))] {
		bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: root, WorkItem: item, Specifications: project.Product.Specifications})
		if err != nil {
			t.Fatalf("assemble the context of %s: %v", item.ID, err)
		}
		if len(bundle.Text) > len(largestContext) {
			largestContext, largestItem = bundle.Text, item.ID
		}
	}

	invariants, err := invariant.Store{RepositoryRoot: root, Directory: project.Product.Invariants}.Load()
	if err != nil {
		t.Fatalf("load this repository's invariants: %v", err)
	}
	var scopes []string
	for _, recorded := range invariants.Active {
		scopes = append(scopes, recorded.Scope...)
	}
	delivered := invariants.Select(strings.Join(scopes, "\n"))
	if len(delivered.Omitted) > 0 {
		t.Logf("invariants past their own delivery bound, not shown: %v", delivered.Omitted)
	}

	persona, err := os.ReadFile(filepath.Join(root, ".yoyodyne", "personas", "reviewer.md"))
	if err != nil {
		t.Fatalf("read the reviewer persona this project binds: %v", err)
	}

	var results []checks.Result
	for _, command := range project.Checks {
		results = append(results, checks.Result{Command: command, Process: execution.ProcessResult{
			ExitCode: 2,
			Stdout:   strings.Repeat("o", 2*maxCheckOutputBytes),
			Stderr:   strings.Repeat("e", 2*maxCheckOutputBytes),
		}})
	}

	summary := strings.Repeat("s", runstate.MaxDeveloperSummaryBytes)
	earlier := make([]AttemptSummary, project.Execution.RepairAttemptsBeforeReplan)
	for index := range earlier {
		earlier[index] = AttemptSummary{Attempt: index + 1, Text: summary}
	}

	request := newRequest(nil)
	request.WorkItemID = largestItem
	request.Context = largestContext
	request.Invariants = delivered.Text()
	request.Checks = results
	request.CIChecks = strings.Repeat("c", 2<<10)
	request.UntouchedReruns = strings.Repeat("u", 4<<10)
	request.Environments = strings.Repeat("n", MaxEnvironmentEvidenceBytes)
	request.DeveloperSummary = summary
	request.DeveloperSummaryContext = "the latest attempt's account"
	request.EarlierDeveloperSummaries = earlier
	request.ApprovingReview = strings.Repeat("a", runstate.MaxReviewSummaryBytes)
	request.Verification = strings.Repeat("v", 1<<10)
	request.Landing = strings.Repeat("l", 1<<10)

	ceiling := min(gitworktree.MaxReviewDiffBytes, gitworktree.MaxReviewDiffAndContextBytes-gitworktree.WorkItemContextBytes)
	system := len(reviewSystemPrompt(ScopeWorkItem, string(persona)))
	// What a review is shown beside the rendered change, with the MCP share
	// counted whole.
	besideChange := func(changes gitworktree.ChangeDiff) int {
		with := request
		with.Changes = changes
		return system + len(reviewEvidencePrompt(with)) - len(renderChanges(changes, with.evidenceLocation())) + MaxMCPCallEvidenceBytes
	}
	fixed := besideChange(gitworktree.ChangeDiff{})
	t.Logf("the review evidence that does not depend on the change is %d bytes: work item context %d (for %s), contract and persona %d, invariants %d, checks %d, developer summaries %d, MCP share %d",
		fixed, len(largestContext), largestItem, system, len(request.Invariants), len(renderChecks(results, nil, "")), len(summary)*(1+len(earlier)), MaxMCPCallEvidenceBytes)

	// measure reports one shape's review against the total.
	measure := func(shape string, changes gitworktree.ChangeDiff) {
		t.Helper()
		if over := changes.OverReviewBound(); len(over) != 0 || len(changes.Patch) < ceiling*3/4 {
			t.Fatalf("%s: patch %d bytes, %d files kept out; want a patch filling the %d-byte limit with nothing kept out, a change the size check lets through", shape, len(changes.Patch), len(over), ceiling)
		}
		rendered := len(renderChanges(changes, request.evidenceLocation()))
		alongside := rendered - len(changes.Patch)
		total := besideChange(changes) + rendered
		highest := int(float64(MaxReviewInputBytes-fixed) / (1 + float64(alongside)/float64(len(changes.Patch))))
		t.Logf("%s: %d files, a %d-byte patch rendered to %d bytes (status %d, diff stat %d); the review is %d bytes of the %d allowed, and the highest diff limit this shape fits is about %d KiB",
			shape, len(changes.Files)+changes.FilesOmitted, len(changes.Patch), rendered, len(changes.Status), len(changes.DiffStat), total, MaxReviewInputBytes, highest>>10)
	}

	measure("one file", renderedChange(t, ceiling, 1, (ceiling-8<<10)/(len(changedLine)+1)))
	// Each small file's diff is the same size, so a few of them say how many
	// fill the limit; the sample is kept small because every rendering builds a
	// repository on whatever machine runs the suite.
	const sampled = 10
	sample := renderedChange(t, ceiling, sampled, 1)
	measure("many small files", renderedChange(t, ceiling, (ceiling-8<<10)/(len(sample.Patch)/sampled), 1))
}

// renderedChange is a change to the given number of existing files, each given
// the given number of lines, rendered by the worktree manager under a diff
// limit of limit bytes as a review renders it. The files are tracked and
// modified, as a developer's change to existing code is, so the limit on new
// files does not apply to them.
func renderedChange(t *testing.T, limit, files, lines int) gitworktree.ChangeDiff {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	git := func(dir string, args ...string) {
		t.Helper()
		if output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v error = %v: %s", args, err, output)
		}
	}
	write := func(dir, content string) {
		t.Helper()
		for index := 0; index < files; index++ {
			target := filepath.Join(dir, "internal", fmt.Sprintf("pkg%02d", index/50), fmt.Sprintf("file_%04d.go", index))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	git(repository, "init", "-q", "-b", "main")
	git(repository, "config", "user.name", "Yoyodyne Test")
	git(repository, "config", "user.email", "yoyodyne@example.invalid")
	git(repository, "config", "maintenance.auto", "false")
	git(repository, "config", "gc.auto", "0")
	write(repository, "package p\n")
	git(repository, "add", "--all")
	git(repository, "commit", "-q", "-m", "base")
	manager, err := gitworktree.New(gitworktree.Options{
		Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		Timeout: testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), gitworktree.CreateRequest{RunID: reviewRunID, WorkItemID: "yoyodyne-task", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repository, "worktree", "remove", "--force", "--force", worktree.Path).Run()
	})
	write(worktree.Path, "package p\n"+strings.Repeat(changedLine, lines))
	changes, err := manager.UnifiedChanges(context.Background(), worktree, gitworktree.DiffLimits{MaxTotalBytes: limit})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	return changes
}

func itemTextBytes(item beads.WorkItem) int {
	return len(item.Title) + len(item.Description) + len(item.Design) + len(item.AcceptanceCriteria) + len(item.Notes)
}
