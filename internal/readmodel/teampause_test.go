package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamclaim"
)

type fetchedTeamPause struct {
	pause teamclaim.Pause
	set   bool
}

func (f fetchedTeamPause) Fetched(context.Context) (teamclaim.Pause, bool, error) {
	return f.pause, f.set, nil
}

// In team mode the banner says the team pause is set and who set it, ahead of
// the limit on waiting pull requests; cleared, or with team mode off and so no
// pause wired, nothing is said.
func TestTheBannerSaysTheTeamPauseAndWhoSetIt(t *testing.T) {
	t.Parallel()
	pause := teamclaim.Pause{SetBy: "mason", Factory: "yoyodyne on alpha", SetAt: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), Commit: "0123456789abcdef0123456789abcdef01234567"}
	read := func(source TeamPause, limit int) Standing {
		sources := quietSources()
		sources.Runs = indexedRuns{fakeRuns{recorded: []runstate.State{waitingRun(41, "item-one", "Recorded title one")}}}
		sources.MaxWaitingPullRequests = limit
		sources.TeamPause = source
		return ReadStanding(context.Background(), sources)
	}

	held := read(fetchedTeamPause{pause: pause, set: true}, 1)
	if held.TeamPause == nil || held.TeamPause.SetBy != "mason" {
		t.Fatalf("team pause = %#v, want the pause mason set", held.TeamPause)
	}
	for _, want := range []string{"The team pause is set", "no factory working this repository starts new work", "mason (yoyodyne on alpha) set it at"} {
		if !strings.Contains(held.RenderBanner(), want) || !strings.Contains(held.RenderBrief(), want) {
			t.Fatalf("banner does not say %q:\n%s", want, held.RenderBanner())
		}
	}
	if held.WaitingPullRequests == nil {
		t.Fatalf("the waiting pull requests were dropped from the reading under the team pause")
	}

	if cleared := read(fetchedTeamPause{}, 0); cleared.TeamPause != nil || cleared.RenderBanner() != "" {
		t.Fatalf("cleared: team pause = %#v, banner %q; want nothing said", cleared.TeamPause, cleared.RenderBanner())
	}
	if off := read(nil, 0); off.TeamPause != nil || off.RenderBanner() != "" {
		t.Fatalf("team mode off: team pause = %#v, banner %q; want nothing said", off.TeamPause, off.RenderBanner())
	}
}
