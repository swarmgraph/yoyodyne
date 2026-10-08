package triage

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func stoppedRunEntry() Entry {
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           Key(ClassStoppedRun, "run-0123456789abcdef0123456789abcdef"),
		Class:         ClassStoppedRun,
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-task",
		RecordedAt:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Blocker:       "Yoyodyne stopped this item: the repair budget was spent.",
		Counters:      Counters{ReviewRounds: 3, ReviewRoundsCap: 4, RepairAttempts: 2, RepairGrantAttempts: 2},
	}
}

func publicationEntry() Entry {
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           Key(ClassPublication, "run-0123456789abcdef0123456789abcdef"),
		Class:         ClassPublication,
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-task",
		RecordedAt:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Publication: &Publication{
			Number:     42,
			URL:        "https://forge.invalid/pull/42",
			State:      "OPEN",
			ApprovedAt: time.Date(2026, 8, 19, 6, 30, 0, 0, time.UTC),
		},
		Counters: Counters{ReviewRounds: 1, ReviewRoundsCap: 4, RepairAttempts: 0, RepairGrantAttempts: 2},
	}
}

func TestAForgeFailureOffersRepairAndNeverOffersTheStandingRearm(t *testing.T) {
	t.Parallel()
	entry := publicationEntry()
	entry.Check = &Check{Command: "build", ForgeHeadCommit: "red-head", Output: "build: failure"}
	entry.Counters.PublicationRearms, entry.Counters.MergeRearmsCap = 1, 2
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	rendered := entry.Render()
	if !strings.Contains(rendered, "A repair continues the preserved change") || !strings.Contains(rendered, "Failing forge check: build (commit red-head)") {
		t.Fatalf("the forge handback does not name repair:\n%s", rendered)
	}
	if strings.Contains(rendered, "merge request may be repeated") || strings.Contains(rendered, "exit 0") {
		t.Fatalf("the forge failure offers a re-arm or invents an exit code:\n%s", rendered)
	}
}

func TestAnEntryIsRefusedWhenItCannotSayWhatStopped(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		entry func() Entry
		want  string
	}{
		{
			name: "a stopped run with neither a blocker nor a failure",
			entry: func() Entry {
				entry := stoppedRunEntry()
				entry.Blocker = ""
				return entry
			},
			want: "carries the durable blocker",
		},
		{
			name: "a publication entry with no publication",
			entry: func() Entry {
				entry := publicationEntry()
				entry.Publication = nil
				return entry
			},
			want: "carries the publication",
		},
		{
			// A merged publication is the thing that was supposed to happen, and
			// docketing one would put finished work in front of somebody as
			// stopped work.
			name: "a publication the forge merged",
			entry: func() Entry {
				entry := publicationEntry()
				entry.Publication.Merged = true
				return entry
			},
			want: "not stuck",
		},
		{
			name: "a publication with nothing to measure an age from",
			entry: func() Entry {
				entry := publicationEntry()
				entry.Publication.ApprovedAt = time.Time{}
				return entry
			},
			want: "approved_at is required",
		},
		{
			// The key is what makes two records of one stoppage impossible, so a
			// key that names some other event is refused rather than stored.
			name: "a key that does not name the event it describes",
			entry: func() Entry {
				entry := stoppedRunEntry()
				entry.Key = Key(ClassStoppedRun, "run-ffffffffffffffffffffffffffffffff")
				return entry
			},
			want: "does not name the stopped_run event",
		},
		{
			name: "a class nothing dockets",
			entry: func() Entry {
				entry := stoppedRunEntry()
				entry.Class = "something-else"
				return entry
			},
			want: "class \"something-else\"",
		},
		{
			name: "a blocker too long to keep",
			entry: func() Entry {
				entry := stoppedRunEntry()
				entry.Blocker = strings.Repeat("x", MaxBlockerBytes+1)
				return entry
			},
			want: "blocker is",
		},
		{
			name: "more findings than an entry carries",
			entry: func() Entry {
				entry := stoppedRunEntry()
				for range MaxFindings + 1 {
					entry.Findings = append(entry.Findings, Finding{Severity: "blocker", Message: "fix it"})
				}
				return entry
			},
			want: "exceeds the bound",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.entry().Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

func TestAWellFormedEntryOfEitherClassIsAccepted(t *testing.T) {
	t.Parallel()

	// A merge the forge performed that the harness could not finish is the one
	// merged publication that still needs a person, and it says so by carrying
	// what the harness recorded about it.
	unconfirmed := publicationEntry()
	unconfirmed.Publication.Merged = true
	unconfirmed.Publication.Message = "the merge landed and confirming it failed"

	for _, entry := range []Entry{stoppedRunEntry(), publicationEntry(), unconfirmed} {
		if err := entry.Validate(); err != nil {
			t.Fatalf("Validate() error = %v for %s", err, entry.Class)
		}
	}
}

// A step to continue at is only ever said of a resumable stall. Which steps are
// accepted is the run state's to say, and the docket store asks it.
func TestAStepToContinueAtRequiresAResumableStall(t *testing.T) {
	t.Parallel()

	stall := func(step string) Entry {
		entry := stoppedRunEntry()
		entry.SessionResumable = true
		entry.ResumesAt = step
		entry.Artifacts.DeveloperSession = "developer-session"
		entry.Artifacts.WorktreePath = "/worktrees/yoyodyne-task"
		entry.Artifacts.Branch = "yoyodyne/yoyodyne-task/01234567"
		return entry
	}
	for _, step := range []string{"", "checking", "reviewing"} {
		if err := stall(step).Validate(); err != nil {
			t.Fatalf("Validate() error = %v for a stall resumed at %q", err, step)
		}
	}
	notResumable := stall("reviewing")
	notResumable.SessionResumable = false
	if err := notResumable.Validate(); err == nil || !strings.Contains(err.Error(), "requires session_resumable") {
		t.Fatalf("Validate() error = %v, want a step refused on a stoppage that is not a resumable stall", err)
	}
}

// The key is derived from the event rather than generated, which is the whole
// of what makes docketing idempotent: two processes that notice one stoppage
// have to produce the same key without talking to each other.
func TestOneEventHasOneKeyWhoeverDerivesIt(t *testing.T) {
	t.Parallel()

	run := "run-0123456789abcdef0123456789abcdef"
	if Key(ClassStoppedRun, run) != Key(ClassStoppedRun, " "+run+" ") {
		t.Fatalf("the same stoppage produced two keys")
	}
	if Key(ClassStoppedRun, run) == Key(ClassPublication, run) {
		t.Fatalf("a run's stoppage and its publication share a key, so one would hide the other")
	}
	// A publication is the run and the pull request together, so two publications
	// of one run are two events rather than one entry standing for both.
	if PublicationKey(run, 42) == PublicationKey(run, 43) {
		t.Fatalf("two publications of one run share a key, so one would hide the other")
	}
	if !strings.HasPrefix(PublicationKey(run, 42), Key(ClassPublication, run)) {
		t.Fatalf("a publication key does not name the publication event: %s", PublicationKey(run, 42))
	}
}

// A publication entry names the run and the pull request it is about, and the
// entries already on the docket name the run alone. Both are accepted, because
// the docket is an append-only log that nothing rewrites: refusing the older
// form would make every docket carrying one unreadable.
func TestAPublicationEntryIsAcceptedUnderEitherKeyItCanCarry(t *testing.T) {
	t.Parallel()

	current := publicationEntry()
	current.Key = PublicationKey(current.RunID, current.Publication.Number)
	if err := current.Validate(); err != nil {
		t.Fatalf("Validate() error = %v for the key the harness now writes", err)
	}
	// The key of some other pull request of the same run is not this entry's, and
	// is what the check exists to catch.
	other := publicationEntry()
	other.Key = PublicationKey(other.RunID, other.Publication.Number+1)
	if err := other.Validate(); err == nil || !strings.Contains(err.Error(), "does not name the publication event") {
		t.Fatalf("Validate() error = %v, want the mismatched publication refused", err)
	}
}

// The docket is where a decision is made, so what has already been decided has
// to be on it. A guard that refuses a second re-run while the entry shows
// nothing recorded is how one authorized recovery is spent twice.
func TestARenderedEntrySaysWhatTriageHasAlreadyDecided(t *testing.T) {
	t.Parallel()

	decided := stoppedRunEntry()
	decided.Counters.RepairGrants, decided.Counters.RepairGrantsCap = 1, 1
	decided.Counters.Reruns, decided.Counters.RerunsCap = 1, 1
	decided.Counters.MergeRearmsCap = 1
	for _, want := range []string{
		"1 of 1 repair grant(s)",
		"1 of 1 re-run(s), 0 carried out",
		// The re-arms are stated as the item total they are, with no ceiling
		// beside them: the ceiling is read per publication, and a stopped run is
		// about none.
		"0 merge re-arm(s) across every publication",
		"already recorded and not yet carried out",
	} {
		if rendered := decided.Render(); !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}

	// Once it has been carried out against this stoppage, the entry says that
	// instead: the counter is a total nothing clears, so it cannot say on its own
	// whether this stoppage may still be run again.
	carriedOut := decided
	carriedOut.Counters.RerunsCarriedOut = 1
	carriedOut.Rerun = &Rerun{
		ClaimedAt: time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC),
		RunID:     "run-fedcba9876543210fedcba9876543210",
	}
	rendered := carriedOut.Render()
	if !strings.Contains(rendered, "already re-run as run run-fedcba9876543210fedcba9876543210") {
		t.Fatalf("rendered entry does not say this stoppage was re-run:\n%s", rendered)
	}
	if strings.Contains(rendered, "not yet carried out") {
		t.Fatalf("a spent decision was rendered as one still standing:\n%s", rendered)
	}

	// A claim whose fresh run never existed is still a claim, and the entry says
	// so rather than reading as a stoppage nothing was done about.
	unstarted := carriedOut
	unstarted.Rerun = &Rerun{ClaimedAt: time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC)}
	if rendered := unstarted.Render(); !strings.Contains(rendered, "no fresh run was recorded for it") {
		t.Fatalf("rendered entry does not say the claim started nothing:\n%s", rendered)
	}
}

// A stopped or escalated run that published names its request beside its
// branch, and says what the record last knew the forge did with it: nothing,
// a merge armed and queued, or merged. A publication entry says all of that
// below in its own section and gets no second line here.
func TestARenderedEntryNamesTheRequestARunLeftOnTheForge(t *testing.T) {
	t.Parallel()

	open := stoppedRunEntry()
	open.Artifacts.PullRequest = 544
	open.Artifacts.PullRequestURL = "https://forge.invalid/pull/544"
	if rendered := open.Render(); !strings.Contains(rendered, "Pull request (open on the forge, unmerged, no merge armed): #544 https://forge.invalid/pull/544") {
		t.Fatalf("rendered entry does not name the open request:\n%s", rendered)
	}
	queued := open
	queued.Artifacts.PullRequestMergeQueued = true
	if rendered := queued.Render(); !strings.Contains(rendered, "Pull request (open on the forge, merge armed and queued): #544") {
		t.Fatalf("rendered entry does not say the merge is armed:\n%s", rendered)
	}
	merged := queued
	merged.Artifacts.PullRequestMerged = true
	if rendered := merged.Render(); !strings.Contains(rendered, "Pull request (merged): #544") {
		t.Fatalf("rendered entry does not say the request merged:\n%s", rendered)
	}
	if rendered := stoppedRunEntry().Render(); strings.Contains(rendered, "Pull request (") {
		t.Fatalf("an entry about a run that published nothing names a request:\n%s", rendered)
	}
}

// A grant is more than a count of grants, and the rest of it is what the harness
// already told whoever recorded the decision: how many rounds it came to, and
// whether the cap cut it. An entry that carried the count alone disagreed with
// the sentence the development manager had just been handed.
func TestARenderedEntrySaysWhatARecordedGrantCameTo(t *testing.T) {
	t.Parallel()

	// The record left behind by "the grant was cut from 2 round(s) to the 1 the
	// cap still had room for": one grant, worth one round, cut, committing the
	// item to the whole of its cap.
	granted := stoppedRunEntry()
	granted.Counters.RepairGrants, granted.Counters.RepairGrantsCap = 1, 1
	granted.Counters.GrantedRounds, granted.Counters.TruncatedGrants = 1, 1
	granted.Counters.CommittedRounds = 4
	rendered := granted.Render()
	for _, want := range []string{
		"1 of 1 repair grant(s) worth 1 review round(s), 1 of them cut down to the room the cap still had",
		"4 of 4 are committed by a grant not yet spent",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}

	// A grant given in full says what it was worth and nothing about a cut that
	// did not happen.
	full := stoppedRunEntry()
	full.Counters.RepairGrants, full.Counters.RepairGrantsCap = 1, 2
	full.Counters.GrantedRounds = 2
	if rendered := full.Render(); !strings.Contains(rendered, "1 of 2 repair grant(s) worth 2 review round(s);") {
		t.Fatalf("rendered entry does not say what the grant was worth:\n%s", rendered)
	}
	if rendered := full.Render(); strings.Contains(rendered, "cut down") {
		t.Fatalf("a grant given in full was rendered as one the cap cut:\n%s", rendered)
	}
}

// The counts say what has been decided; they do not say what deciding it again
// would meet. Leaving that to the guard is the round-trip this docket exists to
// remove: the development manager reads the entry, decides, and is refused by a
// budget the entry could have shown them.
func TestARenderedEntrySaysWhatARepeatedDecisionWouldMeet(t *testing.T) {
	t.Parallel()

	// An item nothing has been granted says nothing about grants: an entry that
	// announced every untouched budget is one a reader learns to skip.
	if rendered := stoppedRunEntry().Render(); strings.Contains(rendered, "repair grant for") {
		t.Fatalf("an item with no grant recorded was rendered as standing somewhere with them:\n%s", rendered)
	}

	// Its own budget refuses first, exactly as the guard asks it first.
	spent := stoppedRunEntry()
	spent.Counters.RepairGrants, spent.Counters.RepairGrantsCap = 1, 1
	spent.Counters.GrantedRounds = 1
	if rendered := spent.Render(); !strings.Contains(rendered,
		"A further repair grant for yoyodyne-task is refused: 1 of 1 permitted grant(s) are already recorded") {
		t.Fatalf("rendered entry does not say a further grant is refused:\n%s", rendered)
	}

	// Both spent is said as both. Naming one of them sends the operator to cross
	// it and the same decision back to be refused by the other, which is the two
	// override ceremonies minutes apart that this line exists to prevent.
	both := stoppedRunEntry()
	both.Counters.RepairGrants, both.Counters.RepairGrantsCap = 1, 1
	both.Counters.GrantedRounds, both.Counters.CommittedRounds = 1, 4
	if rendered := both.Render(); !strings.Contains(rendered,
		"refused by both of its budgets: 1 of 1 permitted grant(s) are already recorded, and 4 of 4 round(s) are spent or committed") {
		t.Fatalf("rendered entry does not say both budgets refuse a further grant:\n%s", rendered)
	}

	// With its own budget to spare, the round budget is what refuses, and the
	// figure it refuses against is what the item is committed to.
	committed := stoppedRunEntry()
	committed.Counters.RepairGrants, committed.Counters.RepairGrantsCap = 1, 2
	committed.Counters.GrantedRounds, committed.Counters.CommittedRounds = 1, 4
	if rendered := committed.Render(); !strings.Contains(rendered,
		"refused by the review round budget: 4 of 4 round(s) are spent or committed") {
		t.Fatalf("rendered entry does not say the round budget refuses a further grant:\n%s", rendered)
	}

	// A grant with room left and rounds unspent is a decision standing rather
	// than one to take again.
	standing := stoppedRunEntry()
	standing.Counters.ReviewRoundsCap = 6
	standing.Counters.RepairGrants, standing.Counters.RepairGrantsCap = 1, 2
	standing.Counters.GrantedRounds, standing.Counters.CommittedRounds = 2, 5
	if rendered := standing.Render(); !strings.Contains(rendered,
		"is recorded and its rounds are not spent, so this stoppage may be handed back on the decision that stands") {
		t.Fatalf("rendered entry does not say the grant still stands:\n%s", rendered)
	}

	// The re-arms are the third budget, and the only one read per publication
	// rather than per item: what a re-arm repeats is one already-authorized merge
	// request, so an item that published three times has three of them. So the
	// standing is stated against this entry's own publication and not against the
	// item's total.
	rearmed := publicationEntry()
	rearmed.Counters.MergeRearms, rearmed.Counters.MergeRearmsCap = 3, 1
	rearmed.Counters.PublicationRearms, rearmed.Counters.PublicationRearmsMade = 1, 1
	if rendered := rearmed.Render(); !strings.Contains(rendered,
		"A further merge re-arm of this publication is refused: 1 of 1 permitted re-arm(s) are already recorded against it") {
		t.Fatalf("rendered entry does not say a further re-arm is refused:\n%s", rendered)
	}
	// Decided and not yet made is the standing a re-arm's carry-out reads, and it
	// is the opposite answer to the one above.
	standingRearm := publicationEntry()
	standingRearm.Counters.MergeRearms, standingRearm.Counters.MergeRearmsCap = 1, 1
	standingRearm.Counters.PublicationRearms = 1
	if rendered := standingRearm.Render(); !strings.Contains(rendered,
		"is recorded and the harness has not made it, so its merge request may be repeated on the decision that stands") {
		t.Fatalf("rendered entry does not say the re-arm still stands:\n%s", rendered)
	}
	// An item's own total says nothing about this publication, so an entry whose
	// publication has spent nothing announces nothing however many re-arms the
	// item has had elsewhere.
	elsewhere := publicationEntry()
	elsewhere.Counters.MergeRearms, elsewhere.Counters.MergeRearmsCap = 4, 1
	if rendered := elsewhere.Render(); strings.Contains(rendered, "merge re-arm of this publication") {
		t.Fatalf("a publication with nothing spent was rendered against the item's total:\n%s", rendered)
	}
	if rendered := stoppedRunEntry().Render(); strings.Contains(rendered, "merge re-arm of this publication") {
		t.Fatalf("a stopped run was rendered as standing somewhere with a publication's re-arms:\n%s", rendered)
	}
}

// A triage record that could not be read is stated. Rendering it as an item with
// nothing decided about it is the one reading that turns an unreadable record
// into a decision taken twice.
func TestARenderedEntrySaysWhenTheTriageRecordCouldNotBeRead(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	entry.CountersProblem = "read what triage has recorded about yoyodyne-task: permission denied"
	rendered := entry.Render()
	if !strings.Contains(rendered, "Triage decisions could not be read: read what triage has recorded") {
		t.Fatalf("rendered entry does not say the record could not be read:\n%s", rendered)
	}
	if strings.Contains(rendered, "Triage decisions recorded") {
		t.Fatalf("an unreadable record was rendered as decisions:\n%s", rendered)
	}
}

func TestARenderedEntryCarriesTheEvidenceSomebodyDecidesOn(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	entry.Summary = "the change misses the acceptance criteria"
	entry.Findings = []Finding{{Severity: "blocker", Message: "add the missing file", File: "feature.txt", Line: 1}}
	entry.Check = &Check{Command: "make test", ExitCode: 1, Output: "FAIL\tinternal/thing"}
	entry.Artifacts = Artifacts{
		Branch:       "yoyodyne/task/abc",
		WorktreePath: "/state/worktrees/task",
		TargetBranch: "main",
	}
	rendered := entry.Render()
	for _, want := range []string{
		"stopped run",
		"yoyodyne-task",
		"the repair budget was spent",
		"the change misses the acceptance criteria",
		"Finding [blocker] (feature.txt:1): add the missing file",
		"Failing check: make test (exit 1)",
		"FAIL\tinternal/thing",
		"Branch (preserved as the run's record says, not checked): yoyodyne/task/abc",
		"Worktree (preserved as the run's record says, not checked): /state/worktrees/task",
		"Target branch: main",
		"3 of 4 review round(s) used",
		"2 repair attempt(s) spent",
		"a grant would hand it 2",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}
}

// An entry outlives the run that produced it, so a reader who finds one has the
// identifier and whatever the entry says in words. It says what the item is
// called where the run recorded it, and the identifier alone where it did not.
func TestARenderedEntryNamesTheItemInWordsWhereTheRunRecordedThem(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	entry.WorkItemTitle = "Slack thread headers carry the item's title"
	rendered := entry.Render()
	if !strings.Contains(rendered, "yoyodyne-task — Slack thread headers carry the item's title") {
		t.Fatalf("rendered entry does not name the item in words:\n%s", rendered)
	}
	untitled := stoppedRunEntry().Render()
	if !strings.Contains(untitled, "on yoyodyne-task (") {
		t.Fatalf("an entry with no recorded title does not name the item alone:\n%s", untitled)
	}
}

// An artifact the harness already removed is named as removed rather than
// omitted: a development manager sent after a worktree that is gone finds that
// out by going there, which is the errand the docket exists to remove. An entry
// written before the repository was looked in says its answer is the record's.
func TestARenderedEntrySaysWhichArtifactsAreStillThere(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	entry.Artifacts = Artifacts{
		Branch: "yoyodyne/task/abc", BranchRemoved: true,
		WorktreePath: "/state/worktrees/task", WorktreeRemoved: true,
	}
	rendered := entry.Render()
	if !strings.Contains(rendered, "Branch (removed as the run's record says, not checked)") ||
		!strings.Contains(rendered, "Worktree (removed as the run's record says, not checked)") {
		t.Fatalf("rendered entry describes removed artifacts as preserved:\n%s", rendered)
	}
}

// An entry that carries what the repository held says that, and never the flags
// beside it: run-838ffc48's flags and its branch disagreed, and the branch was
// the one holding the approved change.
func TestARenderedEntrySaysWhatTheRepositoryHeldOverTheFlags(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC)
	entry := stoppedRunEntry()
	entry.Artifacts = Artifacts{
		Branch: "yoyodyne/task/abc", BranchRemoved: true,
		WorktreePath: "/state/worktrees/task", WorktreeRemoved: true,
		DeveloperSession: "session-1",
		Found: &Found{
			At: at, Branch: "yoyodyne/task/abc", WorktreePath: "/state/worktrees/task",
			BranchThere: true,
		},
	}
	rendered := entry.Render()
	for _, want := range []string{
		"Branch (checked and there at 2026-09-23T05:00:00Z): yoyodyne/task/abc",
		"Worktree (checked and NOT there at 2026-09-23T05:00:00Z): /state/worktrees/task",
		"Developer session (preserved, with no checkout left to continue it in)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Branch (removed") {
		t.Fatalf("an entry that looked rendered the removal flag:\n%s", rendered)
	}

	entry.Artifacts.Found = &Found{At: at, Branch: "yoyodyne/task/abc", Unknown: true, Unchecked: "the repository could not be asked (boom)"}
	if rendered := entry.Render(); !strings.Contains(rendered, "Branch (not checked: the repository could not be asked (boom))") {
		t.Fatalf("a look that failed is not said as one:\n%s", rendered)
	}
}

func TestARenderedPublicationSaysWhatTheForgeDidAndHowLongItHasBeenSitting(t *testing.T) {
	t.Parallel()

	entry := publicationEntry()
	entry.Publication.MergeQueued = true
	entry.Publication.Message = "the forge dropped the queued merge of pull request 42"
	rendered := entry.Render()
	for _, want := range []string{
		"unfinished publication",
		"Pull request #42 https://forge.invalid/pull/42",
		"Forge state: OPEN, the forge has its merge queued",
		"unmerged for 5h30m when docketed",
		"Forge merge message: the forge dropped the queued merge",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered publication is missing %q:\n%s", want, rendered)
		}
	}
}

// The counters exist so a decision is made against the budget rather than
// against the evidence alone, so an item at its cap says so where the decision
// is made rather than leaving it to be worked out from two numbers.
func TestARenderedEntrySaysWhenTheReviewRoundCapIsReached(t *testing.T) {
	t.Parallel()

	entry := stoppedRunEntry()
	if strings.Contains(entry.Render(), "the cap is reached") {
		t.Fatalf("an item inside its cap was rendered as having reached it:\n%s", entry.Render())
	}
	entry.Counters.ReviewRounds = entry.Counters.ReviewRoundsCap
	if !strings.Contains(entry.Render(), "another repair is not triage's to grant") {
		t.Fatalf("an item at its cap did not say so:\n%s", entry.Render())
	}
}

func TestAnAgeIsStatedTheWaySomebodyReadsIt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		age  time.Duration
		want string
	}{
		{age: 30 * time.Second, want: "30s"},
		{age: 90 * time.Minute, want: "1h30m"},
		{age: 50 * time.Hour, want: "2d2h"},
		// A clock that went backwards between the run ending and the docket
		// being built is not a negative age; it is no age at all.
		{age: -time.Hour, want: "0s"},
	} {
		if got := describeAge(test.age); got != test.want {
			t.Fatalf("describeAge(%s) = %q, want %q", test.age, got, test.want)
		}
	}
}

// A run that died holding its change left the work item carrying no blocker,
// because nothing got as far as recording one. The entry says what stopped it
// all the same, and says which of the two it is: a reader sent to the item for a
// blocker nobody wrote there is a reader who concludes the entry is wrong.
func TestAStoppedRunEntryStandsOnTheFailureWhereNothingRecordedABlocker(t *testing.T) {
	t.Parallel()

	died := stoppedRunEntry()
	died.Blocker = ""
	died.Failure = "publish the developer branch: remote rejected the push: Connection reset"
	if err := died.Validate(); err != nil {
		t.Fatalf("Validate() refused an entry that says what stopped the run: %v", err)
	}
	rendered := died.Render()
	if !strings.Contains(rendered, died.Failure) {
		t.Fatalf("the rendered entry does not say what stopped the run:\n%s", rendered)
	}
	if !strings.Contains(rendered, "carries no blocker") {
		t.Fatalf("the rendered entry does not say the item carries no blocker:\n%s", rendered)
	}
}

// And on the ordinary stoppage the blocker is the whole of it. The failure a run
// recorded says the same thing in different words, and an entry printing both
// would be the same fact twice on nearly every entry there is.
func TestAStoppedRunEntryWithABlockerDoesNotAlsoPrintTheFailure(t *testing.T) {
	t.Parallel()

	stopped := stoppedRunEntry()
	if strings.Contains(stopped.Render(), "carries no blocker") {
		t.Fatalf("an entry with a blocker claimed the item carries none:\n%s", stopped.Render())
	}
}

// An approved change whose replay conflicted names the conflict and a person
// as the next mover — or the repair-continue, once it extends to conflicts —
// and never the resume verb, which would meet the conflict again. It says so
// on the entry that carries no blocker in particular, because that is the entry
// yoyodyne-ifd.441 produced when the blocker write timed out, and it was then
// the only surface that could name the conflict at all.
func TestAReplayConflictEntryNamesTheConflictAndTheRepairNotTheResume(t *testing.T) {
	t.Parallel()

	conflicted := stoppedRunEntry()
	conflicted.Blocker = ""
	conflicted.Failure = "change cannot be replayed onto the moved target branch: replay onto main failed\nrecord the replay conflict as a blocker: bd update failed with status timed_out and exit code -1: "
	conflicted.ReplayConflict = &ReplayConflict{TargetBranch: "main", Detail: "change cannot be replayed onto the moved target branch", Phase: "integrating"}
	if err := conflicted.Validate(); err != nil {
		t.Fatalf("Validate() refused an entry carrying a replay conflict: %v", err)
	}
	rendered := conflicted.Render()
	for _, want := range []string{
		"run " + conflicted.RunID + "'s change is approved and its replay onto main conflicted",
		"a person to settle the conflict",
		"`yoyo triage repair " + conflicted.RunID + "`, which continues the developer that wrote it",
		"Next mover: you — this change is approved and its replay conflicted",
		"What the replay found",
		"carries no blocker",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the rendered entry does not say %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Next mover: the harness") {
		t.Fatalf("the rendered entry sends the reader to the resume verb:\n%s", rendered)
	}
	// A decision already recorded about it is still the harness's to carry out,
	// as for any other stoppage.
	decided := conflicted
	decided.Counters.Standing = Standing{Decided: true, Spends: true}
	if !strings.Contains(decided.Render(), "Next mover: the harness — a decision about this stoppage is already recorded") {
		t.Fatalf("a decided conflict did not name the carry-out as the next move:\n%s", decided.Render())
	}
	// The two classifications of one stop never stand together, and a conflict is
	// only ever a stopped run's.
	both := conflicted
	both.IntegrationStop = &IntegrationStop{Cause: "transport-failure", Phase: "integrating"}
	if err := both.Validate(); err == nil || !strings.Contains(err.Error(), "never both") {
		t.Fatalf("Validate() error = %v, want the conflict refused beside an integration stop", err)
	}
	published := publicationEntry()
	published.ReplayConflict = conflicted.ReplayConflict
	if err := published.Validate(); err == nil || !strings.Contains(err.Error(), "replay_conflict: only a stopped run") {
		t.Fatalf("Validate() error = %v, want the conflict refused on a publication entry", err)
	}
}

// The failure is held to the bound the blocker beside it is, and for the same
// reason: an entry too big to record is a stoppage that reaches nobody.
func TestAnEntryIsRefusedWhenItsFailureExceedsTheBound(t *testing.T) {
	t.Parallel()

	died := stoppedRunEntry()
	died.Blocker = ""
	died.Failure = strings.Repeat("x", MaxBlockerBytes+1)
	err := died.Validate()
	if err == nil || !strings.Contains(err.Error(), "failure is") {
		t.Fatalf("Validate() error = %v, want the failure refused for its length", err)
	}
}

func unreadyEntry() Entry {
	read := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	prerequisites := []Prerequisite{{
		Kind:     "forbidden-by-ruling",
		Missing:  `it says of itself: "Blocked until the architect's answer exists"`,
		Evidence: "the sentence is in the item's own statement, and nothing in the tracker records it as a dependency",
		Decides:  "the product manager, or the development manager who records the dependency",
	}}
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           UnreadyKey("yoyodyne-ifd.100.1", []string{"forbidden-by-ruling"}),
		Class:         ClassUnreadyItem,
		ProductID:     "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.100.1",
		WorkItemTitle: "Commit and publish an approved artifact write",
		RecordedAt:    read,
		Unready:       &Unready{Prerequisites: prerequisites, ReadAt: read},
		Counters:      Counters{ReviewRounds: 0, ReviewRoundsCap: 4, RepairGrantAttempts: 2},
	}
}

// The one entry on this docket with no run behind it. It is valid without one
// precisely because nothing ran: an entry that had to name a run could only be
// written by the run this exists to save.
func TestAnUnreadyItemEntryNamesNoRun(t *testing.T) {
	t.Parallel()

	entry := unreadyEntry()

	if err := entry.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want an entry about work that never started to be valid", err)
	}
	withRun := unreadyEntry()
	withRun.RunID = "run-0123456789abcdef0123456789abcdef"
	if err := withRun.Validate(); err == nil || !strings.Contains(err.Error(), "names no run") {
		t.Fatalf("Validate() error = %v, want an unready entry carrying a run to be refused", err)
	}
}

// The evidence that makes the class the thing it claims to be. An entry that
// cannot say what the item asks for is one nobody can act on.
func TestAnUnreadyItemEntryIsRefusedWithoutItsPrerequisites(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		entry func() Entry
		want  string
	}{
		{
			name:  "nothing recorded at all",
			entry: func() Entry { entry := unreadyEntry(); entry.Unready = nil; return entry },
			want:  "carries the prerequisites",
		},
		{
			name: "an empty list",
			entry: func() Entry {
				entry := unreadyEntry()
				entry.Unready = &Unready{ReadAt: entry.RecordedAt}
				return entry
			},
			want: "names at least one unmet prerequisite",
		},
		{
			name: "a prerequisite that does not say what is missing",
			entry: func() Entry {
				entry := unreadyEntry()
				entry.Unready.Prerequisites[0].Missing = ""
				return entry
			},
			want: "what is missing is required",
		},
		{
			name: "no reading time, so the entry reads as a standing fact",
			entry: func() Entry {
				entry := unreadyEntry()
				entry.Unready.ReadAt = time.Time{}
				return entry
			},
			want: "read_at is required",
		},
		{
			name:  "evidence that belongs to a run",
			entry: func() Entry { entry := unreadyEntry(); entry.Blocker = "something stopped"; return entry },
			want:  "work that never started",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.entry().Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

// The key is the item and what was found about it, so a session that meets the
// same unready item at every poll dockets it once — and one that later finds a
// different kind of prerequisite unmet dockets that as the separate finding it
// is. The kinds are sorted so that the order they were read in decides nothing.
func TestAnUnreadyItemIsKeyedByTheItemAndWhatWasFound(t *testing.T) {
	t.Parallel()

	first := UnreadyKey("yoyodyne-ifd.100.1", []string{"stale-pinpoint", "forbidden-by-ruling"})
	again := UnreadyKey("yoyodyne-ifd.100.1", []string{"forbidden-by-ruling", "stale-pinpoint"})
	if first != again {
		t.Fatalf("keys = %q and %q, want the order they were read in to decide nothing", first, again)
	}
	if other := UnreadyKey("yoyodyne-ifd.100.1", []string{"stale-pinpoint"}); other == first {
		t.Fatalf("key = %q, want a different finding about one item to be a different entry", other)
	}
	if !strings.Contains(first, "yoyodyne-ifd.100.1") || len(first) > MaxKeyBytes {
		t.Fatalf("key = %q, want it to name the item and stay inside the bound", first)
	}
	// A key that does not derive from what the entry describes would make two
	// records of one finding, which is what the derivation exists to prevent.
	entry := unreadyEntry()
	entry.Key = "unready_item:yoyodyne-ifd.100.1:something-else"
	if err := entry.Validate(); err == nil || !strings.Contains(err.Error(), "does not name") {
		t.Fatalf("Validate() error = %v, want a key that does not derive from the entry to be refused", err)
	}
}

// What a development manager reads. The run is not named, because there is none
// and an empty pair of brackets reads as a run whose identifier nobody recorded;
// and the reading is dated, because this is the one entry whose subject can go
// out of date without anybody touching the item.
func TestAnUnreadyItemRendersAsWorkThatNeverStarted(t *testing.T) {
	t.Parallel()

	rendered := unreadyEntry().Render()

	for _, required := range []string{
		"item the tree is not ready for",
		"nothing ran",
		"the tree was read at 2026-09-06T09:00:00Z",
		"Unmet [forbidden-by-ruling]",
		"Blocked until the architect's answer exists",
		"Who releases it",
		"the development manager who records the dependency",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, required)
		}
	}
}

// escalationEntry is a role's judgement that the item cannot be met as it
// stands: no blocker, no findings, no publication, and the account the
// development manager decides from.
func escalationEntry() Entry {
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           Key(ClassEscalation, "run-0123456789abcdef0123456789abcdef"),
		Class:         ClassEscalation,
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.100.1",
		WorkItemTitle: "Convert the management anchors",
		RecordedAt:    time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC),
		Escalation: &Escalation{
			RaisedBy: domain.RoleReviewer,
			Reason:   "the acceptance criteria ask for the conversion the entanglement ruling forbade, so no change here can meet them",
		},
		Counters: Counters{ReviewRounds: 1, ReviewRoundsCap: 4},
	}
}

// The judgement is the whole of the entry, so an entry that cannot carry it is
// one she can read and not decide from.
func TestAnEscalationEntryCarriesTheJudgementItIsAbout(t *testing.T) {
	t.Parallel()

	if err := escalationEntry().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, refused := range []struct {
		name  string
		entry func(Entry) Entry
		says  string
	}{
		{
			name:  "no judgement at all",
			entry: func(e Entry) Entry { e.Escalation = nil; return e },
			says:  "carries the judgement",
		},
		{
			name:  "no account of it",
			entry: func(e Entry) Entry { e.Escalation.Reason = "  "; return e },
			says:  "the reason is required",
		},
		{
			// Every other role decides about work rather than doing it, so an entry
			// naming one describes an escalation nothing in a run could have raised.
			name:  "raised by a role that is not in a run",
			entry: func(e Entry) Entry { e.Escalation.RaisedBy = domain.RoleProductManager; return e },
			says:  "is not one of the roles that raises one",
		},
		{
			name:  "carrying a publication",
			entry: func(e Entry) Entry { e.Publication = &Publication{Number: 4}; return e },
			says:  "rather than a publication",
		},
	} {
		t.Run(refused.name, func(t *testing.T) {
			t.Parallel()
			entry := escalationEntry()
			entry.Escalation = &Escalation{RaisedBy: entry.Escalation.RaisedBy, Reason: entry.Escalation.Reason}
			err := refused.entry(entry).Validate()
			if err == nil || !strings.Contains(err.Error(), refused.says) {
				t.Fatalf("Validate() error = %v, want it to say %q", err, refused.says)
			}
		})
	}
}

// What a development manager reads. It has to say what she is being asked for —
// a decision about the item rather than about a change — and what the escalation
// cost, because every other entry on this docket is work that spent its budget
// before anybody heard about it.
func TestAnEscalationRendersAsADecisionAboutTheItem(t *testing.T) {
	t.Parallel()

	rendered := escalationEntry().Render()

	for _, required := range []string{
		"item raised as unmeetable",
		"Nothing was integrated",
		"the reviewer judged this item cannot be met as it stands",
		"in the round it reached",
		"replan, park, resequence, or redirect",
		"the entanglement ruling forbade",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, required)
		}
	}
}

// unstartedRunEntry is a dispatch that died before it claimed its item: the
// yoyodyne-ifd.285 shape. The failure is the whole of it, because there is
// nothing else — no item claimed, no worktree, no branch, no review.
func unstartedRunEntry() Entry {
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           Key(ClassUnstartedRun, "run-0123456789abcdef0123456789abcdef"),
		Class:         ClassUnstartedRun,
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-task",
		WorkItemTitle: "Two claim-path bd behaviors are pinned against the real store",
		RecordedAt:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Failure:       "claim work item: Error claiming yoyodyne-task: issue not claimable: status blocked",
		Counters:      Counters{ReviewRounds: 0, ReviewRoundsCap: 4, RepairGrantAttempts: 2},
	}
}

// The entry made about a dispatch that never started. It is held to the evidence
// that makes it what it claims to be, exactly as every other class is: an entry
// that cannot say why the run never started is one nobody can act on, and one
// carrying evidence about a change describes a run that did not happen.
func TestAnUnstartedRunEntryIsHeldToWhatMakesItOne(t *testing.T) {
	t.Parallel()

	if err := unstartedRunEntry().Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want the entry accepted", err)
	}
	for _, test := range []struct {
		name  string
		entry func() Entry
		want  string
	}{
		{
			name: "it does not say why the run never started",
			entry: func() Entry {
				entry := unstartedRunEntry()
				entry.Failure = ""
				return entry
			},
			want: "carries the failure that stopped it before it claimed",
		},
		{
			// The item was never claimed, so nothing wrote a blocker on it. An entry
			// claiming one would send a reader to the item for words nobody put there.
			name: "it claims a blocker on an item nothing touched",
			entry: func() Entry {
				entry := unstartedRunEntry()
				entry.Blocker = "Yoyodyne stopped this item."
				return entry
			},
			want: "names no blocker",
		},
		{
			name: "it carries evidence about a change that was never made",
			entry: func() Entry {
				entry := unstartedRunEntry()
				entry.Findings = []Finding{{Severity: "blocker", Message: "add the missing file"}}
				return entry
			},
			want: "reached no change",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.entry().Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

// What the development manager reads. She is deciding about a dispatch rather
// than about a change, so the entry has to say that the item is untouched — the
// rest of this docket is work sitting on a branch, and a reader who carried that
// assumption here would go looking for something nobody made.
func TestAnUnstartedRunReadsAsADispatchRatherThanAStoppage(t *testing.T) {
	t.Parallel()

	entry := unstartedRunEntry()
	rendered := entry.Render()
	for _, want := range []string{
		"run that died before it started",
		"yoyodyne-task",
		"Nothing was started",
		"Why it never started",
		entry.Failure,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the rendered entry does not say %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Died holding its change") {
		t.Fatalf("the entry claims a change that was never made:\n%s", rendered)
	}
}

// unstartedAttemptEntry is a dispatch that never became a run at all: the
// 2026-09-13 shape. There is no run to name, because nothing reserved one, so
// what the entry carries is the selection the run record would have carried and
// the failure that stopped it.
func unstartedAttemptEntry() Entry {
	failure := "repository is not ready for an isolated run: the primary checkout has uncommitted changes"
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           AttemptKey("yoyodyne-ifd.353", failure),
		Class:         ClassUnstartedAttempt,
		ProductID:     "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.353",
		WorkItemTitle: "A stall the watchdog can see is one the operator is told about",
		RecordedAt:    time.Date(2026, 9, 13, 6, 25, 0, 0, time.UTC),
		Failure:       failure,
		Attempt: &Attempt{
			SelectedBecause:       "first in the product manager's order of 74 admitted items, 12 of them pullable",
			ExcludedForTheSession: true,
		},
		Counters: Counters{ReviewRoundsCap: 4, RepairGrantAttempts: 2},
	}
}

// The entry made about a dispatch that produced no run record. It is held to the
// evidence that makes it one, exactly as every other class is: an entry that
// cannot say what was attempted or what stopped it is the silence this class
// exists to end, wearing a docket entry's clothes.
func TestAnUnstartedAttemptEntryIsHeldToWhatMakesItOne(t *testing.T) {
	t.Parallel()

	if err := unstartedAttemptEntry().Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want the entry accepted", err)
	}
	for _, test := range []struct {
		name  string
		entry func() Entry
		want  string
	}{
		{
			name: "it does not say what stopped the dispatch",
			entry: func() Entry {
				entry := unstartedAttemptEntry()
				entry.Failure = ""
				return entry
			},
			want: "carries the failure that stopped it before any run record existed",
		},
		{
			name: "it does not say what was being attempted",
			entry: func() Entry {
				entry := unstartedAttemptEntry()
				entry.Attempt = nil
				return entry
			},
			want: "carries what was being attempted",
		},
		{
			// The selection is the one thing a run record would have held that nothing
			// else in the harness wrote down, so an entry without it answers "why was
			// this tried at all" with nothing.
			name: "it does not say why the item was selected",
			entry: func() Entry {
				entry := unstartedAttemptEntry()
				entry.Attempt = &Attempt{}
				return entry
			},
			want: "why the item was selected is required",
		},
		{
			// Naming a run is the one thing this class must not do: there is no run,
			// and a reader sent to look one up would be sent after a record nothing
			// ever wrote.
			name: "it names a run that was never recorded",
			entry: func() Entry {
				entry := unstartedAttemptEntry()
				entry.RunID = "run-0123456789abcdef0123456789abcdef"
				return entry
			},
			want: "names no run",
		},
		{
			name: "it claims a blocker on an item nothing touched",
			entry: func() Entry {
				entry := unstartedAttemptEntry()
				entry.Blocker = "Yoyodyne stopped this item."
				return entry
			},
			want: "names no blocker",
		},
		{
			name: "it carries evidence about a change that was never made",
			entry: func() Entry {
				entry := unstartedAttemptEntry()
				entry.Check = &Check{Command: "make check", ExitCode: 1}
				return entry
			},
			want: "produced no run",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.entry().Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

// The key is what makes docketing idempotent, and for this class it has to be
// derived from the item and the failure rather than from either alone. Keyed on
// the item, a dispatch that failed a new way months later would say nothing;
// keyed on the moment, the same dead dispatch would be docketed afresh every
// session.
func TestAnAttemptKeyIsTheItemAndTheFailureTogether(t *testing.T) {
	t.Parallel()

	item := "yoyodyne-ifd.353"
	failure := "repository is not ready for an isolated run"
	if AttemptKey(item, failure) != AttemptKey(item, failure) {
		t.Fatal("the same item failing the same way twice takes two keys, so one dead dispatch would be docketed over and over")
	}
	// Whitespace is not a different failure. The same error rewrapped by a
	// different writer is the same fact, and a key that disagreed would docket it
	// twice.
	if AttemptKey(item, failure) != AttemptKey(item, "repository is not ready\n   for an isolated run") {
		t.Fatal("the same failure rewrapped takes a different key")
	}
	if AttemptKey(item, failure) == AttemptKey(item, "the claude-code backend is not installed") {
		t.Fatal("two different failures about one item take one key, so the second would never be recorded")
	}
	if AttemptKey(item, failure) == AttemptKey("yoyodyne-ifd.354", failure) {
		t.Fatal("two items failing the same way take one key, so only one of them would be recorded")
	}
	if len(AttemptKey(item, failure)) > MaxKeyBytes {
		t.Fatalf("the key is %d bytes, which the entry refuses", len(AttemptKey(item, failure)))
	}
}

// What the development manager reads. She is deciding about a dispatch that left
// nothing at all — no run to look up, no item claimed — and the entry has to say
// that rather than leaving her to infer it from a missing identifier.
func TestAnUnstartedAttemptReadsAsADispatchThatLeftNoRecord(t *testing.T) {
	t.Parallel()

	entry := unstartedAttemptEntry()
	rendered := entry.Render()
	for _, want := range []string{
		"attempt that never became a run",
		"nothing ran",
		"yoyodyne-ifd.353",
		"no run was recorded",
		"Why it was selected",
		entry.Attempt.SelectedBecause,
		"will not try it again until the item changes",
		"Why it never started",
		entry.Failure,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the rendered entry does not say %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Died holding its change") {
		t.Fatalf("the entry claims a change that was never made:\n%s", rendered)
	}
}

// Every class the taxonomy holds is one a reader can be told about. A class
// missing from the list, or one the list names and Valid refuses, is a docket
// entry that renders as its own identifier or is refused for being itself.
func TestEveryClassIsNamedAndValid(t *testing.T) {
	t.Parallel()

	for _, class := range Classes() {
		if !class.Valid() {
			t.Fatalf("class %q is in the taxonomy and refused by Valid", class)
		}
		if title := class.Title(); title == "" || title == string(class) {
			t.Fatalf("class %q renders as %q, which is the identifier rather than something to read", class, title)
		}
	}
	if Class("unheard_of").Valid() {
		t.Fatal("a class nobody declared is accepted")
	}
}

func stoppageClosure() Closure {
	return Closure{
		SchemaVersion: ClosureSchemaVersion,
		Key:           Key(ClassStoppedRun, "run-0123456789abcdef0123456789abcdef"),
		ProductID:     "yoyodyne",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-task",
		Decision:      "escalate",
		Reason:        "the findings dispute the item's criteria",
		DecidedBy:     "the development manager in conversation chat-0123456789abcdef",
		ClosedAt:      time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
	}
}

// An entry no run was ever behind is settled without naming one: the pull that
// finds an unready item ready takes its entry off, and there is no run to name.
// Every other entry still needs the run its stoppage was.
func TestARunlessEntryIsClosedWithoutARun(t *testing.T) {
	t.Parallel()

	unready := stoppageClosure()
	unready.Key = UnreadyKey("yoyodyne-ifd.298", []string{"subject-not-in-repository"})
	unready.RunID = ""
	unready.Decision = "no-longer-unready"
	if err := unready.Validate(); err != nil {
		t.Fatalf("closing an unready entry with no run was refused: %v", err)
	}
	stopped := stoppageClosure()
	stopped.RunID = ""
	if err := stopped.Validate(); err == nil || !strings.Contains(err.Error(), "run id is required") {
		t.Fatalf("Validate() = %v, want a stoppage's closure to still need its run", err)
	}
}

// A closure takes a stoppage off the docket, so what it must never be is a
// record that cannot say which stoppage, what was decided, or by whom: each of
// those missing is an entry nobody is looking at any more with nothing saying
// why.
func TestAClosureIsRefusedWhenItCannotSayWhatWasSettledOrByWhom(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		closure func(Closure) Closure
		want    string
	}{
		{
			name:    "no entry",
			closure: func(c Closure) Closure { c.Key = "  "; return c },
			want:    "key is required",
		},
		{
			name:    "no decision",
			closure: func(c Closure) Closure { c.Decision = ""; return c },
			want:    "the decision that settled this stoppage is required",
		},
		{
			name:    "prose where a decision was expected",
			closure: func(c Closure) Closure { c.Decision = strings.Repeat("x", MaxDecisionBytes+1); return c },
			want:    "decision is",
		},
		{
			name:    "nobody",
			closure: func(c Closure) Closure { c.DecidedBy = ""; return c },
			want:    "who decided this is required",
		},
		{
			name:    "no moment",
			closure: func(c Closure) Closure { c.ClosedAt = time.Time{}; return c },
			want:    "closed_at is required",
		},
		{
			name:    "reasoning past the bound",
			closure: func(c Closure) Closure { c.Reason = strings.Repeat("x", MaxMessageBytes+1); return c },
			want:    "reason is",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.closure(stoppageClosure()).Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q refused", err, test.want)
			}
		})
	}
	if err := stoppageClosure().Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a well-formed closure accepted", err)
	}
}

// An entry that has been decided must never render as one nobody has looked at.
// A closed entry is not listed on the development manager's docket at all, so
// this is what says so wherever else one is shown.
func TestARenderedEntrySaysTheDecisionThatClosedIt(t *testing.T) {
	t.Parallel()

	closed := stoppedRunEntry()
	settled := stoppageClosure()
	closed.Closed = &settled
	rendered := closed.Render()
	for _, want := range []string{
		`Decided: "escalate"`,
		"the development manager in conversation",
		"2026-08-20T09:00:00Z",
		"dispute the item's criteria",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the rendered entry is missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(stoppedRunEntry().Render(), "Decided:") {
		t.Fatalf("an entry nobody has decided about rendered as decided:\n%s", stoppedRunEntry().Render())
	}
	// A decision that only holds for a while says so, because the entry carrying
	// it is back on the docket the moment it lapses.
	waited := stoppedRunEntry()
	wait := stoppageClosure()
	wait.Decision = "wait"
	wait.RevisitAfter = wait.ClosedAt.Add(2 * time.Hour)
	waited.Closed = &wait
	if !strings.Contains(waited.Render(), "to be looked at again after 2026-08-20T11:00:00Z") {
		t.Fatalf("a decision that lapses did not say when:\n%s", waited.Render())
	}
}

// Waiting is the decision that holds for a while rather than settling anything,
// and everything else settles the stoppage for good.
func TestADecisionHoldsUntilTheMomentItNames(t *testing.T) {
	t.Parallel()

	settled := stoppageClosure()
	if !settled.Holds(settled.ClosedAt.Add(10 * 365 * 24 * time.Hour)) {
		t.Fatalf("a decision that settled the stoppage stopped holding")
	}
	waited := stoppageClosure()
	waited.RevisitAfter = waited.ClosedAt.Add(2 * time.Hour)
	if !waited.Holds(waited.ClosedAt.Add(time.Hour)) {
		t.Fatalf("a decision to wait stopped holding before the moment it named")
	}
	if waited.Holds(waited.RevisitAfter) {
		t.Fatalf("a decision to wait still held at the moment it named")
	}
	// A moment already past is a decision that closed nothing, which nobody could
	// tell from one that settled the stoppage.
	lapsed := stoppageClosure()
	lapsed.RevisitAfter = lapsed.ClosedAt
	if err := lapsed.Validate(); err == nil || !strings.Contains(err.Error(), "look again") {
		t.Fatalf("Validate() error = %v, want a decision to look again before it was made refused", err)
	}
}

// Silence is the one outcome forbidden here. A decision the harness tried to
// carry out and a gate stopped has to say so on the entry the development
// manager reads, naming the gate and what would clear it — otherwise a decision
// recorded days ago and never fired looks exactly like one nothing has reached.
func TestARenderedEntrySaysWhichGateStoppedTheCarryOut(t *testing.T) {
	t.Parallel()

	refused := stoppedRunEntry()
	refused.CarryOut = &CarryOut{
		Decision:  "repair",
		Gate:      "the work the stopped run preserved",
		Refusal:   "the worktree is not as the harness left it",
		Clears:    "somebody saying what became of the worktree the stopped run preserved",
		Attempts:  3,
		RefusedAt: time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC),
	}
	rendered := refused.Render()
	for _, want := range []string{
		`tried to carry out the "repair" you decided`,
		"the work the stopped run preserved refused it",
		"3 attempts",
		"the worktree is not as the harness left it",
		"What would clear it: somebody saying what became of the worktree",
		"Nothing was spent",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}

	// A gate that clears without anybody doing anything is worded as waiting, and
	// that difference is the whole of what she does about it: reading a full
	// harness as a refusal is how one decision gets made twice.
	waiting := refused
	waiting.CarryOut = &CarryOut{
		Decision:  "rerun",
		Gate:      "developer capacity",
		Refusal:   "every developer slot is occupied: 2 active, limit 2",
		Clears:    "a developer slot freeing, which needs nobody",
		Waiting:   true,
		Attempts:  1,
		RefusedAt: time.Date(2026, 8, 19, 13, 0, 0, 0, time.UTC),
	}
	rendered = waiting.Render()
	for _, want := range []string{"is waiting on developer capacity", "1 attempt", "What it is waiting for"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "refused it") {
		t.Fatalf("a gate that clears on its own was rendered as a refusal:\n%s", rendered)
	}

	// And the ordinary entry says nothing at all: a decision the harness has not
	// been stopped on has nothing to report, so a line here always means something.
	if rendered := stoppedRunEntry().Render(); strings.Contains(rendered, "carry out the") {
		t.Fatalf("an entry nothing was stopped on reports a carry-out:\n%s", rendered)
	}
}

// Fold keeps one live entry per run, the one recorded last and in its own place,
// with the rest beneath it oldest first — lapsed decisions included — and leaves
// the entries that name no run exactly as they were.
func TestFoldKeepsOneLiveEntryPerRun(t *testing.T) {
	t.Parallel()

	stopped := stoppedRunEntry()
	lapsed := Closure{SchemaVersion: ClosureSchemaVersion, Key: stopped.Key, Decision: "wait", ClosedAt: stopped.RecordedAt.Add(time.Minute)}
	stopped.Closed = &lapsed
	escalated := stoppedRunEntry()
	escalated.Key = Key(ClassEscalation, escalated.RunID)
	escalated.Class = ClassEscalation
	escalated.Blocker = ""
	escalated.Escalation = &Escalation{RaisedBy: domain.RoleReviewer, Reason: "the criteria contradict the design"}
	escalated.RecordedAt = stopped.RecordedAt.Add(time.Hour)
	published := stoppedRunEntry()
	published.Key = PublicationKey(published.RunID, 42)
	published.Class = ClassPublication
	published.Blocker = ""
	published.Publication = &Publication{Number: 42, ApprovedAt: stopped.RecordedAt}
	published.RecordedAt = stopped.RecordedAt.Add(2 * time.Hour)
	unready := Entry{Class: ClassUnreadyItem, Key: "unready_item:yoyodyne-other:file", WorkItemID: "yoyodyne-other", RecordedAt: stopped.RecordedAt}

	folded := Fold([]Entry{unready, published, stopped, escalated})
	if len(folded) != 2 || folded[0].Key != unready.Key || folded[1].Key != published.Key {
		t.Fatalf("folded = %#v, want the unready item and the run's latest entry, in place", folded)
	}
	earlier := folded[1].Earlier
	if len(earlier) != 2 || earlier[0].Class != ClassStoppedRun || earlier[1].Class != ClassEscalation {
		t.Fatalf("earlier = %#v, want the stoppage then the escalation beneath", earlier)
	}
	if earlier[0].Closed == nil || earlier[0].Closed.Decision != "wait" || earlier[1].Escalation == nil {
		t.Fatalf("earlier = %#v, want each docketing kept whole, its decision with it", earlier)
	}
	// Folded is not summarized: the stoppage's own evidence renders beneath the
	// live entry exactly as it would listed on its own.
	rendered := folded[1].Render()
	for _, want := range []string{stopped.Blocker, "the criteria contradict the design", "Docketed 2 time(s) before"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered live entry is missing %q:\n%s", want, rendered)
		}
	}
	if again := Fold([]Entry{stopped}); len(again) != 1 || again[0].Earlier != nil {
		t.Fatalf("Fold of one entry = %#v, want it untouched", again)
	}
}
