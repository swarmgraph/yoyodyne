package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The developer implements the item and, on the way, finds the design it was
// working from wrong. It says so through the one channel it has: the change is
// recorded for the architect to decide, the run integrates exactly as it would
// have, and no document was touched.
func TestAProposedChangeIsRecordedWithoutChangingWhatTheRunDid(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	design := writeDesignArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalText = "implemented the work item\n\n" +
		reportBlock(`{"severity":"note","message":"worth knowing"}`) +
		"\n" + amendmentBlock(`{"artifact":"v1-design","change":"say which ordering holds","why":"the item cannot satisfy both"}`)
	recorder := &fakeAmendments{}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Reports = &fakeReports{}
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("a proposal changed what the run did: %#v", outcome)
	}
	if outcome.AmendmentProblem != "" {
		t.Fatalf("a readable proposal was reported as a problem: %q", outcome.AmendmentProblem)
	}
	if len(recorder.appended) != 1 || len(outcome.Amendments) != 1 {
		t.Fatalf("recorded = %#v", recorder.appended)
	}
	proposed := recorder.appended[0]
	// The harness resolved who is being asked; the developer only named the
	// document.
	if proposed.Owner != domain.RoleArchitect || proposed.Kind != artifact.KindDesign || proposed.Artifact != "v1-design" {
		t.Fatalf("proposal = %#v", proposed)
	}
	if proposed.Role != domain.RoleDeveloper || proposed.RunID != outcome.RunID || proposed.WorkItemID != tracker.Item.ID {
		t.Fatalf("proposal is not attributed to the run that made it: %#v", proposed)
	}
	// Both channels came out of one reply, and neither took the other with it.
	if len(outcome.Reports) != 1 {
		t.Fatalf("reports = %#v", outcome.Reports)
	}
	if outcome.Summary != "implemented the work item" {
		t.Fatalf("summary = %q", outcome.Summary)
	}
	// Nothing an unapproved proposal contains reaches the artifact. The document
	// is byte for byte what it was before the run that argued with it.
	current, err := os.ReadFile(filepath.Join(repository, design))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(current), "The ordering is unspecified.") || strings.Contains(string(current), "say which ordering holds") {
		t.Fatalf("the proposal reached the document:\n%s", current)
	}
}

// The other owner, reached the same way. The architect does not execute yet, so
// the product manager is the only owner a proposal can actually be delivered to
// in conversation, and a mechanism that resolved only the architect's documents
// would leave that half unreachable while every check still passed. The goals
// are the product manager's, they live in the specifications home rather than
// beside the designs, and a developer proposing a change to one has to arrive at
// the product manager.
func TestAChangeProposedToTheProductManagersOwnDocumentReachesTheProductManager(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	goals := writeGoalsArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalText = "implemented the work item\n\n" +
		amendmentBlock(`{"artifact":"v1-goals","change":"say what recovery means for a killed run","why":"the goal cannot be told apart from the one above it"}`)
	recorder := &fakeAmendments{}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.AmendmentProblem != "" {
		t.Fatalf("a change to a recorded goals document was not resolvable: %q", outcome.AmendmentProblem)
	}
	if len(recorder.appended) != 1 {
		t.Fatalf("recorded = %#v", recorder.appended)
	}
	proposed := recorder.appended[0]
	if proposed.Owner != domain.RoleProductManager || proposed.Kind != artifact.KindGoals || proposed.Artifact != "v1-goals" {
		t.Fatalf("proposal = %#v", proposed)
	}
	// The conversation delivers to an owner by role, so a proposal that resolved
	// to the product manager is one that conversation would carry.
	if pending := amendment.PendingFor([]amendment.Record{{Proposal: &proposed}}, domain.RoleProductManager); len(pending) != 1 {
		t.Fatalf("the proposal is not pending for the role it names: %#v", pending)
	}
	// And it is still a proposal rather than an edit: the goals say what they said.
	current, err := os.ReadFile(filepath.Join(repository, goals))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(current), "say what recovery means") {
		t.Fatalf("the proposal reached the document:\n%s", current)
	}
}

// A developer that could not be talked out of its argument makes it again on
// every repair attempt. That is one disagreement, and it has to arrive as one
// proposal: minting a fresh id per attempt would put the same argument in the
// queue up to repair_attempts_before_replan times and make whoever decides
// answer it once per copy to clear it.
func TestTheSameArgumentMadeAgainOnARepairAttemptIsOneProposal(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		// The first attempt leaves the check failing, so the developer is asked
		// again and says the same thing about the design both times.
		if attempts == 1 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalText = "worked on it\n\n" +
		amendmentBlock(`{"artifact":"v1-design","change":"say which ordering holds","why":"the item cannot satisfy both"}`)
	recorder := &fakeAmendments{}
	command := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{command})
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want the developer asked twice", outcome.RepairAttempts)
	}
	if len(recorder.appended) != 1 || len(outcome.Amendments) != 1 {
		t.Fatalf("one argument made twice produced %d proposal(s): %#v", len(recorder.appended), recorder.appended)
	}
	// Dropping the repeat is not the same as losing it: the first one is recorded
	// and is the one waiting on the architect.
	if recorder.appended[0].Artifact != "v1-design" || recorder.appended[0].Owner != domain.RoleArchitect {
		t.Fatalf("proposal = %#v", recorder.appended[0])
	}
	if outcome.AmendmentProblem != "" {
		t.Fatalf("a dropped repeat was reported as a lost proposal: %q", outcome.AmendmentProblem)
	}
}

// A developer asked for a repair writes its amendment block again rather than
// copying the one before it, so one argument arrives spelled two ways. That is
// the same argument and reaches the architect once.
func TestTheSameArgumentRewordedOnARepairAttemptIsOneProposal(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// The two changes run-62e78d87 made of one argument, verbatim: the second
	// attempt asks for the same thing and shares barely half its wording with the
	// first, which the literal comparison this replaces read as a second proposal.
	provider.DeveloperFinalTextByAttempt = []string{
		"worked on it\n\n" + amendmentBlock(fmt.Sprintf(
			`{"artifact":"v1-design","change":%q,"why":"the architect's voice was asked for and the implementation had to settle a shape to ship at all"}`,
			rewordedGrantShapeArgument)),
		"worked on it\n\n" + amendmentBlock(fmt.Sprintf(
			`{"artifact":"v1-design","change":%q,"why":"the field set is a trust boundary rather than a formatting choice, and it now lives in the immutable developer contract"}`,
			rewordedGrantShapeRestatement)),
	}
	recorder := &fakeAmendments{}
	command := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{command})
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want the developer asked twice", outcome.RepairAttempts)
	}
	if len(recorder.appended) != 1 || len(outcome.Amendments) != 1 {
		t.Fatalf("one argument reworded produced %d proposal(s): %#v", len(recorder.appended), recorder.appended)
	}
	// The one that is waiting is the first, as it is for a repeat made word for
	// word: the restatement is dropped rather than replacing what it restates.
	if recorder.appended[0].Change != rewordedGrantShapeArgument {
		t.Fatalf("the restatement replaced what it restated: %q", recorder.appended[0].Change)
	}
	if outcome.AmendmentProblem != "" {
		t.Fatalf("a dropped restatement was reported as a lost proposal: %q", outcome.AmendmentProblem)
	}
	// Dropped is not the same as gone. The restatement is on the run's record with
	// the proposal it was folded into and the score it was folded on, so a pair the
	// comparison folded wrongly still has its second argument somewhere to find.
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.Amendments) != 2 {
		t.Fatalf("recorded proposals = %#v, want the raised one and the dropped one", recorded.Amendments)
	}
	raised, dropped := recorded.Amendments[0], recorded.Amendments[1]
	if raised.Dropped() || raised.ID != recorder.appended[0].ID || raised.Change != rewordedGrantShapeArgument {
		t.Fatalf("raised proposal = %#v, want the one in the log", raised)
	}
	if !dropped.Dropped() || dropped.FoldedInto != raised.ID || dropped.Change != rewordedGrantShapeRestatement || dropped.Role != domain.RoleDeveloper {
		t.Fatalf("dropped proposal = %#v, want the restatement folded into %s", dropped, raised.ID)
	}
	// The score is the comparison's own, so it is the one this pair is measured at
	// below rather than a number the record made up.
	want := amendmentLikeness(amendmentArgumentOf(amendment.Proposal{Artifact: "v1-design", Change: rewordedGrantShapeArgument}),
		amendmentArgumentOf(amendment.Proposal{Artifact: "v1-design", Change: rewordedGrantShapeRestatement}))
	if dropped.Likeness != want || dropped.Likeness < amendmentRestatementLikeness {
		t.Fatalf("likeness = %v, want %v", dropped.Likeness, want)
	}
}

// The comparison memory is the run's record rather than the process's, so a run
// continued in a second process folds a restatement made there exactly as the
// first process would have. The first process here raises the argument, is
// handed back a failing check, and exits on its in-process bound when the
// provider refuses the repair for want of capacity; the second picks the same
// run up once the window has reset, and its developer restates the argument in
// other words. One argument is one proposal, whichever process heard it.
func TestARestatementMadeInAnotherProcessIsOneProposal(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	writeDesignArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}
	command := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	argue := func(change, why string) string {
		return "worked on it\n\n" + amendmentBlock(fmt.Sprintf(`{"artifact":"v1-design","change":%q,"why":%q}`, change, why))
	}

	// The first process: an attempt that argues and leaves the check failing, then
	// a repair the provider refuses until a reset this process will not wait for.
	first := &orchestratortest.Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	firstAttempts := 0
	first.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleDeveloper {
			return backend.RunResult{}, fmt.Errorf("unexpected role %q in the first process", request.Role)
		}
		firstAttempts++
		if firstAttempts == 1 {
			// A change short of the work, so the check fails on it and the repair the
			// second process continues has something to continue.
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "draft.txt"), []byte("started\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     first.DeveloperSession,
				ResolvedModel: developerResolved,
				FinalText:     argue(rewordedGrantShapeArgument, "the implementation had to settle a shape to ship at all"),
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     request.LastSequence,
			}, nil
		}
		return backend.RunResult{
			Backend:    domain.BackendClaudeCode,
			SessionID:  first.DeveloperSession,
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: limit,
			Process:    execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
			LastEvent:  request.LastSequence,
		}, nil
	}
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{command}), first),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	// A store over one root, built afresh for each process, is what two processes
	// share: the log on disk and nothing either of them holds in memory.
	firstLog, err := runstate.NewAmendmentStore(filepath.Join(worktreeRoot, "..", "amendments"), firstPipeline.Config.Product.ID)
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	firstPipeline.Amendments = firstLog
	paused, err := firstPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if !paused.Paused || paused.Status != runstate.StatusRunning || len(paused.Amendments) != 1 {
		t.Fatalf("first outcome = %#v, want a paused run that raised one proposal", paused)
	}

	// The second process: the same run, continued once the window has reset, whose
	// developer writes the argument again from scratch and does the work.
	second := &orchestratortest.Backend{DeveloperSession: "developer-session", ReviewerSession: "reviewer-session"}
	second.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		switch request.Role {
		case domain.RoleDeveloper:
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     second.DeveloperSession,
				ResolvedModel: developerResolved,
				FinalText:     argue(rewordedGrantShapeRestatement, "the field set is a trust boundary rather than a formatting choice"),
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     request.LastSequence,
			}, nil
		case domain.RoleReviewer:
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     second.ReviewerSession,
				ResolvedModel: reviewerResolved,
				FinalText:     approveVerdict,
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     request.LastSequence,
			}, nil
		default:
			return backend.RunResult{}, fmt.Errorf("unexpected role %q", request.Role)
		}
	}
	secondPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{command}), second),
		&pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	secondLog, err := runstate.NewAmendmentStore(filepath.Join(worktreeRoot, "..", "amendments"), secondPipeline.Config.Product.ID)
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	secondPipeline.Amendments = secondLog
	outcome, err := secondPipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("second outcome = %#v, want run %s continued to success", outcome, paused.RunID)
	}
	if len(second.RequestsForRole(domain.RoleDeveloper)) != 1 {
		t.Fatalf("the second process asked the developer %d time(s), want the one continued repair", len(second.RequestsForRole(domain.RoleDeveloper)))
	}

	listed, err := secondLog.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	// Read the way `yoyo amendment list` reads it, which is what the owner sees.
	records := amendment.Pending(listed)
	if len(records) != 1 || records[0].Change != rewordedGrantShapeArgument {
		t.Fatalf("one argument heard by two processes produced %d proposal(s): %#v", len(records), records)
	}
	if len(outcome.Amendments) != 0 || outcome.AmendmentProblem != "" {
		t.Fatalf("the second process raised or lost a proposal: %#v, %q", outcome.Amendments, outcome.AmendmentProblem)
	}
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.Amendments) != 2 || !recorded.Amendments[1].Dropped() || recorded.Amendments[1].FoldedInto != records[0].ID {
		t.Fatalf("recorded proposals = %#v, want the restatement folded into %s", recorded.Amendments, records[0].ID)
	}
}

// The five changes run-62e78d87's developer proposed to one design, verbatim
// from the amendment log. They are the whole of the evidence this comparison's
// boundary is set from, so they are quoted rather than paraphrased: the
// architect read two pairs of them as one argument each while deciding them, and
// read the ownership/enforcement pair — which ask for the same fact to be
// recorded in two different sections — as two.
const (
	// One argument, spelled twice. The second says "the work item" where the
	// first says "the work item's own text", and is otherwise word for word.
	enforcedSectionArgument = `the "What is enforced, and what is not" section should record a third enforced boundary: a developer's diff is refused on the configured artifact homes and the project configuration directory before any check or reviewer sees it, unless the work item's own text grants the path — leaving the "Not enforced for the developer" paragraph as it stands, because it is scoped to pushing and merging and this changes nothing about that half`
	enforcedSectionRepeat   = `the "What is enforced, and what is not" section should record a third enforced boundary: a developer's diff is refused on the configured artifact homes and the project configuration directory before any check or reviewer sees it, unless the work item grants the path — leaving the "Not enforced for the developer" paragraph as it stands, because it is scoped to pushing and merging and this changes nothing about that half`
	// The same argument written again from scratch, which is the case the literal
	// comparison missed: both ask the architect to settle the shape of the grant.
	rewordedGrantShapeArgument    = `the shape of the protected-path grant — a marker line in the work item's text, read from every field, with a directory grant covering its contents — should be settled or replaced by the architect, together with which role writes a grant into an item`
	rewordedGrantShapeRestatement = `the shape of the protected-path grant should be settled or replaced by the architect: the marker line, the four item fields it is read from (title, description, design guidance, acceptance criteria, and not the notes), and directory grants covering their contents`
	// Two proposals, not one: the same fact, asked for in two sections. This is
	// the closest thing in the run to a duplicate that is not one, and it is what
	// the boundary has to stay above.
	ownershipSectionArgument = `the artifact-ownership section should record that a developer's diff is refused deterministically on the artifact homes before review, with a grant in the work item's own text as the only exception`
)

// The comparison is a judgement about prose, so what makes it a judgement rather
// than a guess is that it is measured against arguments somebody has actually
// decided. Every pair of those five is checked here, in both directions, so a
// later change to the boundary or to the function words fails here rather than
// in an owner's queue.
func TestOneArgumentSpelledTwoWaysIsRecognisedAndTwoArgumentsAreNot(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		left  string
		right string
		same  bool
	}{
		{"a repeat differing by three words", enforcedSectionArgument, enforcedSectionRepeat, true},
		{"one argument written again from scratch", rewordedGrantShapeArgument, rewordedGrantShapeRestatement, true},
		{"one fact asked for in two sections", ownershipSectionArgument, enforcedSectionArgument, false},
		{"the same, against the repeat", ownershipSectionArgument, enforcedSectionRepeat, false},
		{"two unrelated changes", ownershipSectionArgument, rewordedGrantShapeArgument, false},
		{"and two more", enforcedSectionArgument, rewordedGrantShapeRestatement, false},
		{"and the last of them", enforcedSectionRepeat, rewordedGrantShapeArgument, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			left := amendmentArgumentOf(amendment.Proposal{Artifact: "v1-design", Change: testCase.left})
			right := amendmentArgumentOf(amendment.Proposal{Artifact: "v1-design", Change: testCase.right})
			if got := sameAmendmentArgument(left, right); got != testCase.same {
				t.Errorf("sameAmendmentArgument() = %v, want %v", got, testCase.same)
			}
			// Which of the two the developer wrote first decides nothing about
			// whether they are one argument, so the comparison is symmetric.
			if got := sameAmendmentArgument(right, left); got != testCase.same {
				t.Errorf("reversed, sameAmendmentArgument() = %v, want %v", got, testCase.same)
			}
		})
	}
}

// One document at a time. Two proposals that read alike are still two arguments
// when they are about different documents, because they are decided by different
// owners and the document is the one thing an agent does not get to assert
// loosely.
func TestOneChangeAskedOfTwoDocumentsIsTwoArguments(t *testing.T) {
	t.Parallel()

	left := amendmentArgumentOf(amendment.Proposal{Artifact: "v1-design", Change: enforcedSectionArgument})
	right := amendmentArgumentOf(amendment.Proposal{Artifact: "v1-goals", Change: enforcedSectionArgument})
	if sameAmendmentArgument(left, right) {
		t.Fatal("the same change to two documents was folded into one proposal")
	}
}

// A change written in function words and nothing else leaves no request to
// compare, so the only reading of it left is the literal one. Without this the
// two empty word sets would compare as wholly alike and the second proposal
// would be dropped.
func TestAChangeWithNoContentWordsIsComparedLiterally(t *testing.T) {
	t.Parallel()

	first := amendmentArgumentOf(amendment.Proposal{Artifact: "v1-design", Change: "it should be about this"})
	same := amendmentArgumentOf(amendment.Proposal{Artifact: "v1-design", Change: "It should be   about this"})
	other := amendmentArgumentOf(amendment.Proposal{Artifact: "v1-design", Change: "it should not be about that"})
	if !sameAmendmentArgument(first, same) {
		t.Error("the same wordless change was read as two arguments")
	}
	if sameAmendmentArgument(first, other) {
		t.Error("two different wordless changes were folded into one")
	}
}

// A different argument on a later attempt is a different proposal, so the
// deduplication cannot swallow something new the developer found.
func TestADifferentChangeOnARepairAttemptIsItsOwnProposal(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{
		"worked on it\n\n" + amendmentBlock(`{"artifact":"v1-design","change":"say which ordering holds","why":"the item cannot satisfy both"}`),
		"worked on it\n\n" + amendmentBlock(`{"artifact":"v1-design","change":"say what happens when the queue is empty","why":"nothing here answers it"}`),
	}
	recorder := &fakeAmendments{}
	command := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{command})
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(recorder.appended) != 2 || len(outcome.Amendments) != 2 {
		t.Fatalf("two different arguments produced %d proposal(s): %#v", len(recorder.appended), recorder.appended)
	}
	if recorder.appended[0].Change == recorder.appended[1].Change {
		t.Fatalf("the same change was recorded twice: %#v", recorder.appended)
	}
}

// A proposal the harness cannot read, cannot resolve, or cannot keep is named on
// the outcome and costs the run nothing. A run that failed because an agent
// argued with the design would teach every agent to stop arguing with it.
func TestAProposalThatCannotBeRecordedNeverFailsTheRun(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]struct {
		reply    string
		recorder *fakeAmendments
		want     string
	}{
		"unreadable block": {
			reply:    "implemented the work item\n\n" + amendment.Fence + "\n{\"proposals\":[]}\n```\n",
			recorder: &fakeAmendments{},
			want:     "at least one change",
		},
		"document nobody records": {
			reply:    "implemented the work item\n\n" + amendmentBlock(`{"artifact":"invented","change":"x","why":"y"}`),
			recorder: &fakeAmendments{},
			want:     "no owner to decide",
		},
		"log refuses the write": {
			reply:    "implemented the work item\n\n" + amendmentBlock(`{"artifact":"v1-design","change":"x","why":"y"}`),
			recorder: &fakeAmendments{err: errors.New("the amendment log is read-only")},
			want:     "read-only",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			writeDesignArtifact(t, repository)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			provider.DeveloperFinalText = fixture.reply
			pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			pipeline.Amendments = fixture.recorder

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
				t.Fatalf("a lost proposal changed what the run did: %#v", outcome)
			}
			if len(outcome.Amendments) != 0 || !strings.Contains(outcome.AmendmentProblem, fixture.want) {
				t.Fatalf("outcome proposal evidence = %#v, %q", outcome.Amendments, outcome.AmendmentProblem)
			}
			// The account of the work survives whichever way the proposal went.
			if !strings.HasPrefix(outcome.Summary, "implemented the work item") {
				t.Fatalf("summary = %q", outcome.Summary)
			}
		})
	}
}

// A pipeline with nowhere to record says so once rather than losing the
// proposals silently, which is what a developer telling nobody looks like from
// the outside.
func TestAPipelineWithNowhereToRecordSaysTheProposalWasLost(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalText = "implemented the work item\n\n" +
		amendmentBlock(`{"artifact":"v1-design","change":"x","why":"y"}`)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(outcome.AmendmentProblem, "nothing records proposed amendments") {
		t.Fatalf("amendment problem = %q", outcome.AmendmentProblem)
	}
}

// The proposal has to still be there when somebody decides it, which is in
// another process and long after this run's own state is gone. Every other test
// here records through a fake, so the one seam that decides whether the channel
// works at all — the collected proposal reaching the log on disk, and a later
// reader finding it through the same calls `yoyo amendment list` makes — is the
// seam none of them cross. This crosses it with a real store, a real file, and a
// second reader opened over the same root the way a separate process opens it.
func TestAProposedChangeIsOnDiskForALaterProcessToList(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// Named once and used both to write the block and to check what comes back,
	// so the assertion cannot drift into agreeing with its own copy of the text.
	const (
		change = "say which ordering holds"
		why    = "the item cannot satisfy both"
	)
	provider.DeveloperFinalText = "implemented the work item\n\n" +
		amendmentBlock(`{"artifact":"v1-design","change":"`+change+`","why":"`+why+`"}`)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Reports = &fakeReports{}

	stateRoot := t.TempDir()
	// Keyed to the pipeline's own product rather than to a literal, because a
	// store built for a different product refuses every proposal the run makes
	// while the run carries on succeeding — a silent drop nothing else here
	// would catch.
	recorder, err := runstate.NewAmendmentStore(stateRoot, pipeline.Config.Product.ID)
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.AmendmentProblem != "" {
		t.Fatalf("a readable proposal was reported as a problem: %q", outcome.AmendmentProblem)
	}
	if len(outcome.Amendments) != 1 {
		t.Fatalf("the run reported %d proposal(s): %#v", len(outcome.Amendments), outcome.Amendments)
	}

	// A second store over the same root is what another process is: nothing this
	// run held in memory carries into it.
	later, err := runstate.NewAmendmentStore(stateRoot, pipeline.Config.Product.ID)
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	records, err := later.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	// Read the way the operator's command reads it, so a proposal that is on disk
	// but that `yoyo amendment list` would not show still fails here.
	waiting := amendment.Pending(records)
	if len(waiting) != 1 {
		t.Fatalf("a proposal the run recorded is not waiting to be decided: %#v", records)
	}
	proposed := waiting[0]
	if proposed.Artifact != "v1-design" || proposed.Kind != artifact.KindDesign || proposed.Owner != domain.RoleArchitect {
		t.Fatalf("proposal = %#v", proposed)
	}
	if proposed.Role != domain.RoleDeveloper || proposed.RunID != outcome.RunID || proposed.WorkItemID != tracker.Item.ID {
		t.Fatalf("proposal is not attributed to the run that made it: %#v", proposed)
	}
	// Identity and attribution say which proposal this is and who made it, but
	// the argument itself is the whole of what the architect decides. A defect
	// that wrote those two fields away would leave everything above this passing
	// while the operator reads an empty proposal — the same silent drop, one
	// layer in.
	if proposed.Change != change || proposed.Why != why {
		t.Fatalf("the proposal's argument did not survive the disk round trip: change = %q, why = %q", proposed.Change, proposed.Why)
	}
	// The proposal the operator will decide is the one the run said it made, so
	// an outcome that reports a proposal the log does not hold cannot pass.
	if proposed.ID != outcome.Amendments[0].ID {
		t.Fatalf("recorded proposal %q is not the one the run reported, %q", proposed.ID, outcome.Amendments[0].ID)
	}
}

func TestTheDeveloperContractSaysHowToProposeAChange(t *testing.T) {
	t.Parallel()

	// A developer told to propose upstream changes and given no mechanism has
	// only two moves left, both bad, so the mechanism is stated in the contract
	// where a persona cannot weaken it — and repeated on a repair attempt for the
	// same reason the rest of the contract is.
	for name, prompt := range map[string]string{
		"first attempt":       developerPrompt("", "", "# Assigned work item\n", "/scratch", nil, gitworktree.DiffLimits{}),
		"check repair":        checkRepairPrompt("", "/scratch", nil, gitworktree.DiffLimits{}, runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}, 1, 2),
		"path refusal repair": pathRefusalRepairPrompt("", "/scratch", nil, gitworktree.DiffLimits{}, runstate.PathRefusal{Paths: []string{"docs/designs/v1-design.md"}}, protectedpath.Protect(config.Config{}), 1, 2),
	} {
		for _, required := range []string{
			amendment.Fence,
			"A proposal is not an edit you have written in advance",
			// The prohibition is the whole of what stands between a refusal and a
			// durable false claim in the case the carry-back cannot reach: a
			// developer invoked once, whose refusal is only discoverable after its
			// only reply. Nothing asks that developer anything afterwards, so if this
			// paragraph goes the run-6ff896ba failure comes back with every check
			// still green.
			"Writing the block is not the same as the proposal being recorded",
			"nothing you write may assert that a proposal has been raised",
			// And that the harness can only answer afterwards, which is what makes
			// the prohibition follow rather than read as an arbitrary rule.
			"it can only tell you so after the reply that carried it",
		} {
			if !strings.Contains(prompt, required) {
				t.Fatalf("%s prompt is missing %q", name, required)
			}
		}
	}
}

// The failure this ends, from run-6ff896ba: a developer named a document the
// repository does not record, the refusal reached the operator and nobody else,
// and the developer went on to write into a checked-in file that it had raised a
// proposal nothing was holding. So the refusal is put in front of that developer
// before it can be asked anything again — and it is spent by the reply to that
// invocation, because a refusal repeated on every attempt is one the developer
// answers over and over.
func TestARefusedProposalIsPutInFrontOfTheDeveloperThatMadeIt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		attempts++
		// Two failing checks, so the developer is invoked three times: the attempt
		// that proposes, the one that is told, and one more that must not be told
		// again.
		if attempts < 3 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalTextByAttempt = []string{
		"worked on it\n\n" + amendmentBlock(`{"artifact":"invented","change":"say which ordering holds","why":"the item cannot satisfy both"}`),
		"worked on it",
	}
	command := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{command})
	pipeline.Amendments = &fakeAmendments{}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The refusal still costs the run nothing, which is the property the carry-back
	// must not have quietly changed.
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("a refused proposal changed what the run did: %#v", outcome)
	}
	requests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(requests) != 3 {
		t.Fatalf("developer invocations = %d, want the attempt that proposed and two repairs", len(requests))
	}
	// Nothing had been refused yet when the proposing attempt was asked.
	if strings.Contains(requests[0].Prompt, "A change you proposed was not recorded") {
		t.Fatalf("the first attempt was told about a refusal that had not happened:\n%s", requests[0].Prompt)
	}
	// The next one opens with it, in the harness's own words rather than a
	// paraphrase, and is told not to leave the claim behind in the change.
	told := requests[1].Prompt
	if !strings.HasPrefix(told, "# A change you proposed was not recorded") {
		t.Fatalf("the next invocation does not open with the refusal:\n%s", told)
	}
	for _, want := range []string{
		"no artifact answers to that id",
		"Do not describe the proposal as raised",
		"do not write into your change",
		// Refraining is not enough on its own. The refusal cannot be known before
		// the reply that carried it, so the attempt that proposed has already had
		// its turn — and in run-6ff896ba that is the turn the false claim was
		// written in. The attempt that is told has to be asked to undo it.
		"take that back out now",
	} {
		if !strings.Contains(told, want) {
			t.Fatalf("the carried refusal is missing %q:\n%s", want, told)
		}
	}
	// And the contract the invocation still carries under it, so the developer is
	// asked for the work as well as told about the refusal.
	if !strings.Contains(told, "Failing check: repair required") {
		t.Fatalf("the carried refusal replaced the repair the attempt was for:\n%s", told)
	}
	// Told once. The reply to that invocation spends it.
	if strings.Contains(requests[2].Prompt, "A change you proposed was not recorded") {
		t.Fatalf("the refusal was carried a second time:\n%s", requests[2].Prompt)
	}
}

// The carry-back has to survive the process, not just the loop. A run stopped
// after the reply that was refused is continued by triage in another process
// entirely, and the developer it hands the work back to is the one that has still
// never been told.
func TestARefusedProposalIsDurableForAnInvocationInAnotherProcess(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.DeveloperFinalText = "implemented the work item\n\n" +
		amendmentBlock(`{"artifact":"invented","change":"say which ordering holds","why":"the item cannot satisfy both"}`)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Amendments = &fakeAmendments{}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Read back through the store, the way a later process reads it: the words are
	// on disk rather than only in the prompt this run happened to build.
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.RefusedAmendments) != 1 || !strings.Contains(recorded.RefusedAmendments[0].Problem, "no artifact answers to that id") {
		t.Fatalf("the refusal did not survive the run: %#v", recorded.RefusedAmendments)
	}
	// Tagged with who proposed it, which is what decides who is shown it.
	if recorded.RefusedAmendments[0].Role != domain.RoleDeveloper {
		t.Fatalf("the refusal is not attributed to the role that earned it: %#v", recorded.RefusedAmendments[0])
	}
	// The other half is the audit trail: the same refusal, in the same words the
	// outcome carries, on the record rather than only on the outcome. The outcome
	// is printed once by `yoyo run` and gone with the process, and a refusal
	// written only there was, afterwards, indistinguishable from a proposal never
	// made — which is how one run's three lost proposals went unnoticed for four
	// runs. Nothing spends this: the carried refusal above is emptied by the
	// developer's next reply, and this stays.
	if recorded.AmendmentProblem == "" || recorded.AmendmentProblem != outcome.AmendmentProblem {
		t.Fatalf("the record says %q of the refused proposal, the outcome %q", recorded.AmendmentProblem, outcome.AmendmentProblem)
	}
}

// The recorded account outlives the carried refusal. A developer told about a
// refusal spends what it was shown, and what an auditor reads afterwards has to
// still say the proposal was refused — otherwise a run whose developer was told
// reads exactly as a run whose developer proposed nothing.
func TestTheRecordedRefusalOutlivesTheCarriedOne(t *testing.T) {
	t.Parallel()

	run := &activeRun{}
	run.noteAmendmentProblem(domain.RoleDeveloper, errors.New("invented: no artifact answers to that id"))
	run.clearCarriedAmendmentRefusals(domain.RoleDeveloper)
	if len(run.state.RefusedAmendments) != 0 {
		t.Fatalf("the developer's reply did not spend what it was shown: %#v", run.state.RefusedAmendments)
	}
	if !strings.Contains(run.state.AmendmentProblem, "no artifact answers to that id") {
		t.Fatalf("spending the carried refusal took the recorded one with it: %q", run.state.AmendmentProblem)
	}
	// A second refusal accumulates onto the record rather than replacing the first:
	// two lost proposals are two facts, on the record as on the outcome.
	run.noteAmendmentProblem(domain.RoleDeveloper, errors.New("v1-design: the developer owns no document and proposes against all of them"))
	if !strings.Contains(run.state.AmendmentProblem, "no artifact answers to that id") ||
		!strings.Contains(run.state.AmendmentProblem, "v1-design") {
		t.Fatalf("the record lost one of two refusals: %q", run.state.AmendmentProblem)
	}
	if run.state.AmendmentProblem != run.outcome.AmendmentProblem {
		t.Fatalf("the record and the outcome disagree: %q against %q", run.state.AmendmentProblem, run.outcome.AmendmentProblem)
	}
	// And a record that has accumulated past its bound is folded to it rather than
	// left for the store to refuse, because a state file the store will not take
	// loses the whole run rather than the tail of one line.
	for i := 0; i < 20; i++ {
		run.noteAmendmentProblem(domain.RoleDeveloper, fmt.Errorf("refusal %d: %s", i, strings.Repeat("x", maxAmendmentProblemBytes)))
	}
	if len(run.state.AmendmentProblem) > runstate.MaxChannelProblemBytes {
		t.Fatalf("the recorded problem is %d bytes, which the record's own bound refuses", len(run.state.AmendmentProblem))
	}
	if err := run.state.Validate(); err != nil && strings.Contains(err.Error(), "amendment_problem") {
		t.Fatalf("the recorded problem is a shape the store refuses: %v", err)
	}
}

// A refusal is shown to the role that earned it and to nobody else. Only the
// developer's reply is scanned for proposals today, so only a developer can earn
// one — but what makes that safe has to be the attribution rather than the single
// caller, because a refusal shown to the wrong agent is that agent told not to
// claim something it never said while the agent that did say it goes on believing
// its proposal landed. That is the failure this item exists to end, one role over.
func TestARefusalIsNotShownToARoleThatDidNotEarnIt(t *testing.T) {
	t.Parallel()

	const problem = "a change the reviewer proposed was not recorded: invented: no artifact answers to that id"
	carried := []runstate.AmendmentRefusal{{Role: domain.RoleReviewer, Problem: problem}}

	// The developer is shown nothing, because none of it is the developer's.
	if section := carriedAmendmentRefusals(domain.RoleDeveloper, carried); section != "" {
		t.Fatalf("another role's refusal was put in front of the developer:\n%s", section)
	}
	// The role that earned it still is.
	section := carriedAmendmentRefusals(domain.RoleReviewer, carried)
	if !strings.Contains(section, problem) {
		t.Fatalf("the proposing role was not shown its own refusal:\n%s", section)
	}

	// And spending one role's refusals leaves every other role's where they are: a
	// developer reply says the developer was told, and nothing about anybody else.
	run := &activeRun{state: runstate.State{RefusedAmendments: append(
		[]runstate.AmendmentRefusal{{Role: domain.RoleDeveloper, Problem: "a change the developer proposed was not recorded: invented: no artifact answers to that id"}},
		carried...)}}
	run.clearCarriedAmendmentRefusals(domain.RoleDeveloper)
	if len(run.state.RefusedAmendments) != 1 || run.state.RefusedAmendments[0].Role != domain.RoleReviewer {
		t.Fatalf("clearing one role's refusals took another role's with them: %#v", run.state.RefusedAmendments)
	}
}

// The carried list is bounded, so a run whose every reply is an unreadable block
// cannot fill durable state or the next prompt with refusals. Past the bound the
// refusal is still named on the outcome, which is the half that reaches the
// operator.
func TestCarriedAmendmentRefusalsStopAtTheBound(t *testing.T) {
	t.Parallel()

	run := &activeRun{}
	for i := 0; i < runstate.MaxCarriedAmendmentRefusals+3; i++ {
		run.noteAmendmentProblem(domain.RoleDeveloper, fmt.Errorf("refusal %d", i))
	}
	if len(run.state.RefusedAmendments) != runstate.MaxCarriedAmendmentRefusals {
		t.Fatalf("carried %d refusals, want the bound of %d", len(run.state.RefusedAmendments), runstate.MaxCarriedAmendmentRefusals)
	}
	// What was dropped from the carry is still on the outcome for the operator.
	if !strings.Contains(run.outcome.AmendmentProblem, "refusal 12") {
		t.Fatalf("a refusal past the bound was lost to the operator too: %q", run.outcome.AmendmentProblem)
	}
	// And every one of them is in a shape the store will take, since a refusal the
	// state refuses is a refusal nobody is ever told.
	for index, refused := range run.state.RefusedAmendments {
		if err := refused.Validate(); err != nil {
			t.Fatalf("carried refusal %d is not storable: %v", index, err)
		}
	}
}

// One refusal longer than the bound is folded to it rather than left for the
// store to refuse, because a refusal the state will not take is a refusal nobody
// is ever told. Today's callers fold to a shorter bound of their own before they
// get here, so this asks the unit whose guarantee it is rather than going through
// them: what must hold is that nothing this appends is a shape the state rejects,
// whatever the caller hands it.
func TestALongAmendmentRefusalIsFoldedToWhatTheStateWillTake(t *testing.T) {
	t.Parallel()

	run := &activeRun{}
	run.carryAmendmentRefusal(domain.RoleDeveloper, strings.Repeat("x", runstate.MaxAmendmentRefusalBytes*2))
	if len(run.state.RefusedAmendments) != 1 {
		t.Fatalf("carried = %#v", run.state.RefusedAmendments)
	}
	if err := run.state.RefusedAmendments[0].Validate(); err != nil {
		t.Fatalf("an over-long refusal was carried in a shape the store refuses: %v", err)
	}
}

// amendmentBlock renders what an agent writes when it proposes a change, the way
// the contract asks for it: after everything else it says.
func amendmentBlock(proposals ...string) string {
	return amendment.Fence + "\n{\"proposals\":[" + strings.Join(proposals, ",") + "]}\n```\n"
}

// writeDesignArtifact gives the fixture repository one of the architect's
// documents to propose a change to, and writeGoalsArtifact one of the product
// manager's — in the home each kind actually lives in, since which home a
// document is loaded from is what decides whether it can be proposed against at
// all. Both return where the document is.
func writeDesignArtifact(t *testing.T, repository string) string {
	t.Helper()
	return writeArtifact(t, repository, filepath.Join("docs", "designs", "v1-design.md"), `---
id: v1-design
kind: design
title: V1 design
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-17T12:00:00Z
      reason: recorded when identity arrived
---

The ordering is unspecified.
`)
}

func writeGoalsArtifact(t *testing.T, repository string) string {
	t.Helper()
	return writeArtifact(t, repository, filepath.Join("docs", "product", "v1-goals.md"), `---
id: v1-goals
kind: goals
title: V1 goals
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T12:00:00Z
      reason: recorded when identity arrived
---

# V1 goals

What the product is for.

## Goals

- A run that cannot finish leaves its work recoverable.
`)
}

// writeArtifact commits the document, because the developer's worktree is
// created from the repository and an uncommitted file in the primary checkout is
// a change the harness refuses to work around.
func writeArtifact(t *testing.T, repository, relative, content string) string {
	t.Helper()
	path := filepath.Join(repository, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "record an artifact")
	return relative
}

// fakeAmendments is the durable log without a filesystem. err refuses every
// append, which is how a run that cannot keep a proposal is tested.
type fakeAmendments struct {
	appended []amendment.Proposal
	err      error
}

func (f *fakeAmendments) Append(proposal amendment.Proposal) error {
	if f.err != nil {
		return f.err
	}
	if err := proposal.Validate(); err != nil {
		return err
	}
	f.appended = append(f.appended, proposal)
	return nil
}
