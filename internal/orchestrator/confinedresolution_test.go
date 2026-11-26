package orchestrator

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// resolveAndChangeMore resolves the conflict and also changes a file Git's
// merge settled by itself, so the resolution is not confined.
func resolveAndChangeMore(dir string) error {
	if err := resolveBothSides(dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "other.txt"), []byte("not part of the conflict\n"), 0o600)
}

// A resolution confined to the conflicting file rejoins the queue with no
// review in between, recorded with both heads and the comparison; its
// candidate is reviewed whole, shown the conflict and how it was resolved, and
// the run is recorded approved only once that review approved the candidate.
func TestAConfinedResolutionRejoinsWithNoReviewAndIsReviewedOnceAsTheCandidate(t *testing.T) {
	t.Parallel()

	r, provider, admitted, put := handedBackForAConflict(t)
	approvedHead := put.HarnessCommit
	provider.DeveloperSession = "resolving-session"
	reviewsBefore := len(provider.RequestsForRole(domain.RoleReviewer))
	rejoined, err := continueRun(t, r, admitted.RunID)
	if err != nil || rejoined.MergeQueue == nil {
		t.Fatalf("Continue() = %#v, %v; want the resolution back in the queue", rejoined, err)
	}
	if asked := len(provider.RequestsForRole(domain.RoleReviewer)); asked != reviewsBefore {
		t.Fatalf("%d reviews asked before the resolution rejoined, want none", asked-reviewsBefore)
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ReviewDecision != "" || state.ReviewSessionID != "" {
		t.Fatalf("run review %q by %q before the candidate's review; want none recorded", state.ReviewDecision, state.ReviewSessionID)
	}
	resolution := state.ConflictResolution
	if !resolution.Confined() || resolution.Approved.HeadCommit != approvedHead || resolution.ResolvedHead != state.HarnessCommit ||
		!slices.Equal(resolution.Comparison.Conflicting, []string{"feature.txt"}) || !strings.Contains(resolution.Comparison.Resolutions, "+something else") {
		t.Fatalf("conflict resolution = %#v, want the confined comparison of the resolution with Git's merge", resolution)
	}
	key := rejoined.MergeQueue.Key()
	entry, standing, err := r.queue.Standing(key, admitted.RunID)
	if err != nil || !standing {
		t.Fatalf("Standing() = %v, %v", standing, err)
	}
	if entry.ApprovedHead != approvedHead || entry.ResolvedHead != state.HarnessCommit || entry.Head() != state.HarnessCommit ||
		entry.Resolution == nil || entry.Resolution.ApprovedBy != "reviewer-session" || !slices.Equal(entry.Resolution.Conflicting, []string{"feature.txt"}) {
		t.Fatalf("entry = %#v, want the approved head with its review, the resolved head apart, and the comparison", entry)
	}
	if says := entry.HeadSays(); !strings.Contains(says, "no reviewer has approved") || strings.Contains(says, "approved head "+entry.ResolvedHead) {
		t.Fatalf("the entry's head is said as %q, which calls the resolved head approved", says)
	}
	if !strings.Contains(r.tracker.Notes, "No reviewer has approved the resolved head") {
		t.Fatalf("item notes do not say the resolved head is unreviewed:\n%s", r.tracker.Notes)
	}

	if pass := r.catchUpPass(t, key); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the resolution landed on its candidate's review", pass)
	}
	reviews := provider.RequestsForRole(domain.RoleReviewer)
	if len(reviews) != reviewsBefore+1 {
		t.Fatalf("%d reviews after the resolution, want only the candidate's", len(reviews)-reviewsBefore)
	}
	for _, want := range []string{"No reviewer has seen the resolution", "feature.txt", "+something else", entry.ResolvedHead, approvedHead} {
		if !strings.Contains(reviews[len(reviews)-1].Prompt, want) {
			t.Fatalf("the candidate's reviewer was not told %q:\n%s", want, reviews[len(reviews)-1].Prompt)
		}
	}
	generations, err := r.queue.Generations(key, entry.EntryID)
	if err != nil || len(generations) == 0 {
		t.Fatalf("Generations() = %v, %v", generations, err)
	}
	if last := generations[len(generations)-1]; last.AuthorSession != "resolving-session" || !slices.Contains(last.OtherAuthors, "developer-session") {
		t.Fatalf("candidate authors = %q, %v; want the reviewer held to both sessions", last.AuthorSession, last.OtherAuthors)
	}
	landed, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if landed.Integration == nil || landed.ReviewDecision != runstate.ReviewApprove || landed.ReviewSessionID != "reviewer-session" || landed.ReviewApproves != "implementation" {
		t.Fatalf("landed run = integration %#v, review %q by %q as %q; want the candidate's approval recorded", landed.Integration, landed.ReviewDecision, landed.ReviewSessionID, landed.ReviewApproves)
	}
	if !r.tracker.Closed {
		t.Fatal("the item was not closed on the landing")
	}
}

// A resolution that also changes a file Git's merge settled is reviewed
// before it rejoins, and is admitted as an ordinary approved change.
func TestAResolutionThatChangesAnotherFileIsReviewedBeforeItRejoins(t *testing.T) {
	t.Parallel()

	r, provider, admitted, _ := handedBackWithResolver(t, resolveAndChangeMore)
	provider.DeveloperSession = "resolving-session"
	reviewsBefore := len(provider.RequestsForRole(domain.RoleReviewer))
	rejoined, err := continueRun(t, r, admitted.RunID)
	if err != nil || rejoined.MergeQueue == nil {
		t.Fatalf("Continue() = %#v, %v", rejoined, err)
	}
	if asked := len(provider.RequestsForRole(domain.RoleReviewer)); asked != reviewsBefore+1 {
		t.Fatalf("%d reviews before the resolution rejoined, want one", asked-reviewsBefore)
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if c := state.ConflictResolution.Comparison; c == nil || state.ConflictResolution.Confined() || !slices.Equal(c.Outside, []string{"other.txt"}) {
		t.Fatalf("comparison = %#v, want other.txt found outside the conflicting file", c)
	}
	entry, _, err := r.queue.Standing(rejoined.MergeQueue.Key(), admitted.RunID)
	if err != nil || entry.ResolvedHead != "" || entry.Resolution != nil || entry.ApprovedHead != state.HarnessCommit {
		t.Fatalf("entry = %#v, %v; want the reviewed resolution admitted on its own approval", entry, err)
	}
}

// Where the candidate's review refuses a confined resolution, the hand-back
// closes the path for the run: its next resolution is reviewed before it
// rejoins.
func TestARefusedConfinedCandidateClosesThePathForTheRun(t *testing.T) {
	t.Parallel()

	r, provider := newCatchUpRunWithVerdicts(t, resolveBothSides, approveVerdict, minorVerdict, approveVerdict)
	admitted := r.run(t)
	conflictOnTarget(t, r.repository, "something else\n")
	key := admitted.MergeQueue.Key()
	if pass := r.catchUpPass(t, key); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflict handed back", pass)
	}
	provider.DeveloperSession = "resolving-session"
	if rejoined, err := continueRun(t, r, admitted.RunID); err != nil || rejoined.MergeQueue == nil {
		t.Fatalf("Continue() = %#v, %v", rejoined, err)
	}
	if pass := r.catchUpPass(t, key); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the candidate refused by its review and handed back", pass)
	}
	refusedEntry, err := r.queue.Entries(key)
	if err != nil || len(refusedEntry) == 0 {
		t.Fatalf("Entries() = %v, %v", refusedEntry, err)
	}
	last := refusedEntry[len(refusedEntry)-1]
	landing, _, err := r.queue.Landing(key, last.EntryID)
	if err != nil || landing.Handback == nil || last.ResolvedHead == "" {
		t.Fatalf("landing = %#v, entry %#v, %v; want the resolved entry handed back", landing, last, err)
	}
	if h := landing.Handback; h.ApprovedHead != last.ApprovedHead || h.ResolvedHead != last.ResolvedHead || h.Head() != last.ResolvedHead {
		t.Fatalf("hand-back heads approved %q resolved %q; want approved %q and resolved %q kept apart", h.ApprovedHead, h.ResolvedHead, last.ApprovedHead, last.ResolvedHead)
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.ConfinedRefused, "refused") {
		t.Fatalf("confined refused = %q, want the refusal recorded", state.ConfinedRefused)
	}
	run := &activeRun{pipeline: r.pipeline, state: state}
	if why := run.confinedPathClosed(); why == "" {
		t.Fatal("the path stays open after the candidate's review refused it")
	}
}

// The path is closed wherever something else must approve the new head, or
// the change is no longer the resolution that passed its checks.
func TestTheConfinedPathIsClosedWhereAnotherApprovalOrAnotherChangeStands(t *testing.T) {
	t.Parallel()

	r, _ := newCatchUpRun(t, resolveBothSides)
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	open := runstate.State{
		HarnessCommit: head, BaseCommit: base, RepairAttempts: 1,
		ChecksPassed: &runstate.ChecksPassed{Commit: head, Attempt: 1},
		ConflictResolution: &runstate.ConflictResolution{
			Approved: runstate.ApprovedChange{HeadCommit: strings.Repeat("c", 40)}, TargetCommit: base, ResolvedHead: head, Attempt: 1, RecordedAt: time.Now(),
		},
	}
	if why := (&activeRun{pipeline: r.pipeline, state: open}).confinedPathClosed(); why != "" {
		t.Fatalf("confinedPathClosed() = %q, want the path open", why)
	}
	for name, change := range map[string]func(*activeRun){
		"a person approves integration": func(a *activeRun) { a.pipeline.Config.Approvals.Integration = domain.ApprovalHuman },
		"the merge queue is off":        func(a *activeRun) { a.pipeline.Config.Execution.MergeQueue = false },
		"refused before":                func(a *activeRun) { a.state.ConfinedRefused = "the candidate's review refused it" },
		"reviewed already":              func(a *activeRun) { a.state.ReviewDecision = runstate.ReviewApprove },
		"a later attempt":               func(a *activeRun) { a.state.RepairAttempts = 2 },
		"another commit":                func(a *activeRun) { a.state.HarnessCommit = strings.Repeat("d", 40) },
		"checks narrowed and not left": func(a *activeRun) {
			a.state.ChecksPassed = &runstate.ChecksPassed{Commit: head, Attempt: 1, Narrowed: "./internal/x"}
		},
		"no resolution": func(a *activeRun) { a.state.ConflictResolution = nil },
	} {
		run := &activeRun{pipeline: r.pipeline, state: open}
		change(run)
		if why := run.confinedPathClosed(); why == "" {
			t.Errorf("%s: the path is open", name)
		}
	}
}

// Nothing the queue says of a resolved entry calls its resolved head approved.
func TestAResolvedEntryIsNeverNamedApproved(t *testing.T) {
	t.Parallel()

	approved, resolved := strings.Repeat("a", 40), strings.Repeat("b", 40)
	entry := runstate.MergeQueueEntry{ApprovedHead: approved, ResolvedHead: resolved, TargetBranch: "main"}
	generation := runstate.MergeQueueGeneration{TargetBranch: "main", Candidate: strings.Repeat("c", 40), TargetBase: strings.Repeat("d", 40)}
	for _, said := range []string{headNamed(entry), headLines(entry), candidateAdmittedSays(entry, generation), entry.HeadSays()} {
		for _, wrong := range []string{"approved head " + resolved, "approved at head `" + resolved, "Approved head: " + resolved} {
			if strings.Contains(said, wrong) {
				t.Errorf("%q calls the resolved head approved", said)
			}
		}
		if !strings.Contains(said, resolved) || !strings.Contains(said, approved) {
			t.Errorf("%q does not name both heads", said)
		}
	}
	plain := runstate.MergeQueueEntry{ApprovedHead: approved}
	if headNamed(plain) != "the approved head "+approved || headLines(plain) != "Approved head: "+approved+"\n" {
		t.Errorf("an ordinary entry is named %q / %q", headNamed(plain), headLines(plain))
	}
}

// A landed confined resolution's branch and worktree are cleaned up from its
// resolved head, which is the run's last commit.
func TestCleanupOfALandedResolutionStartsFromItsResolvedHead(t *testing.T) {
	t.Parallel()

	approved, resolved, landed := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	state := runstate.State{
		HarnessCommit: resolved,
		MergeQueue:    &runstate.MergeQueueAdmissionRecord{ApprovedHead: approved, ResolvedHead: resolved},
		Integration:   &runstate.Integration{SourceCommit: landed},
	}
	if got := cleanupCommit(state); got != resolved {
		t.Fatalf("cleanupCommit() = %q, want the resolved head %q", got, resolved)
	}
}

// protectedBrief is a protected path the change and the target both change, so
// the conflict, and its resolution, lie under it.
const protectedBrief = "docs/product/brief.md"

// handedBackInAProtectedFile is a run whose approved change, granted
// protectedBrief's directory by its item, conflicts with the target in that
// file and is handed back by the queue.
func handedBackInAProtectedFile(t *testing.T) (*queueRun, *orchestratortest.Backend, Outcome) {
	t.Helper()
	r, provider := newCatchUpRunWriting(t, func(dir string) error {
		if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		return writeUpstream(t, dir, protectedBrief, "the change's brief\n")
	}, func(dir string) error {
		return writeUpstream(t, dir, protectedBrief, "the change's brief\nthe target's brief\n")
	}, approveVerdict)
	r.tracker.Item.Design = "protected-path grant: docs/product\n"
	admitted := r.run(t)
	if admitted.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the granted change admitted", admitted)
	}
	writeQueueFile(t, r.repository, protectedBrief, "the target's brief\n")
	runPipelineGit(t, r.repository, "add", protectedBrief)
	runPipelineGit(t, r.repository, "commit", "-m", "the target's own brief")
	if pass := r.catchUpPass(t, admitted.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflict handed back", pass)
	}
	provider.DeveloperSession = "resolving-session"
	return r, provider, admitted
}

// A confined resolution of a protected file passes the protected-path check
// again on its resolved head before it rejoins: where the item still grants
// the path, it rejoins on the earlier approval.
func TestAConfinedResolutionOfAProtectedFileIsCheckedAgainOnItsNewHead(t *testing.T) {
	t.Parallel()

	r, _, admitted := handedBackInAProtectedFile(t)
	rejoined, err := continueRun(t, r, admitted.RunID)
	if err != nil || rejoined.MergeQueue == nil {
		t.Fatalf("Continue() = %#v, %v; want the granted resolution back in the queue", rejoined, err)
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.PathRefusal != nil || !state.ConflictResolution.Confined() || !slices.Equal(state.ConflictResolution.Comparison.Conflicting, []string{protectedBrief}) {
		t.Fatalf("path refusal %#v, resolution %#v; want the protected file's resolution confined and passed", state.PathRefusal, state.ConflictResolution)
	}
	if state.ChecksPassed == nil || state.ChecksPassed.Commit != state.ConflictResolution.ResolvedHead {
		t.Fatalf("checks passed = %#v, want them earned on the resolved head", state.ChecksPassed)
	}
	if entry, _, err := r.queue.Standing(rejoined.MergeQueue.Key(), admitted.RunID); err != nil || entry.ResolvedHead != state.ConflictResolution.ResolvedHead {
		t.Fatalf("entry = %#v, %v; want the resolved head admitted", entry, err)
	}
}

// Where the item no longer grants the path, the resolution is refused on its
// new head and is not admitted on the earlier approval.
func TestAConfinedResolutionOfAProtectedFileIsRefusedWithoutItsGrant(t *testing.T) {
	t.Parallel()

	r, _, admitted := handedBackInAProtectedFile(t)
	r.tracker.Item.Design = ""
	r.pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0
	outcome, err := continueRun(t, r, admitted.RunID)
	if err == nil || !strings.Contains(err.Error(), "protected paths refused") {
		t.Fatalf("Continue() = %#v, %v; want the resolution refused for the protected path", outcome, err)
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.PathRefusal == nil {
		t.Fatal("no protected-path refusal is recorded against the resolution")
	}
	entries, err := r.queue.Entries(admitted.MergeQueue.Key())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.ResolvedHead != "" {
			t.Fatalf("entry %#v admits a resolution the protected-path check refused", entry)
		}
	}
}
