package orchestrator

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// A pull request somebody merged outside the harness is recorded with the
// commit the forge names as its merge, which after a squash or rebase is the
// only commit of it the target branch holds. A merge already recorded with no
// commit is asked about once more for it, and never again after that.
func TestRefreshRecordsTheMergeCommitTheForgeNames(t *testing.T) {
	t.Parallel()
	squashed := strings.Repeat("d", 40)
	for name, test := range map[string]struct {
		recordedMerged bool
		answer         publish.PullRequest
		want           string
	}{
		"a request found merged":                {false, publish.PullRequest{Number: 1, State: "MERGED", Merged: true, MergeCommit: squashed}, squashed},
		"a merge recorded with no commit":       {true, publish.PullRequest{Number: 1, State: "MERGED", Merged: true, MergeCommit: squashed}, squashed},
		"a merge the forge names no commit for": {true, publish.PullRequest{Number: 1, State: "MERGED", Merged: true}, ""},
		"a request found merged with no commit": {false, publish.PullRequest{Number: 1, State: "MERGED", Merged: true}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture, state := newPublicationFixture(t)
			if test.recordedMerged {
				saved := loadRun(t, fixture.store, state.RunID)
				saved.PullRequest.State, saved.PullRequest.Merged = "MERGED", true
				if err := fixture.store.Save(saved); err != nil {
					t.Fatalf("Save() error = %v", err)
				}
			}
			forge := orchestratortest.PublicationAnswers{state.Branch: test.answer}
			if refreshed := fixture.refresh(t, forge); len(refreshed) != 1 || !refreshed[0].Updated || refreshed[0].Failure != "" {
				t.Fatalf("refresh = %#v, want the record rewritten", refreshed)
			}
			saved := loadRun(t, fixture.store, state.RunID).PullRequest
			if !saved.Merged || saved.ForgeMergeCommit != test.want || !saved.MergeCommitAsked || saved.MergeCommit != "" {
				t.Fatalf("publication = %#v, want it merged with the forge's commit %q beside no confirmed one", saved, test.want)
			}
			// The answer ends the asking, whether or not it named a commit.
			if refreshed := fixture.refresh(t, forge); len(refreshed) != 0 {
				t.Fatalf("second refresh = %#v, want nothing asked", refreshed)
			}
		})
	}
}
