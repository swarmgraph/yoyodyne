package supervise

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// replacingChild is the dashboard as the supervisor sees it: moved onto a
// deployed build by starting its replacement first, and serving at an address.
type replacingChild struct {
	*deployableChild
	replaceErr error
	replaced   int
}

func (r *replacingChild) Replace(context.Context) (Ensured, error) {
	r.note("replace")
	if r.replaceErr != nil {
		return Ensured{}, r.replaceErr
	}
	r.replaced++
	r.mu.Lock()
	r.build = r.binary.revision()
	r.mu.Unlock()
	return Ensured{Started: true, PID: 5000 + r.replaced, Log: "/state/dashboard.log"}, nil
}

func (r *replacingChild) Serving(context.Context) Serving {
	return Serving{URL: "http://127.0.0.1:8765/", Health: "ready since 2026-10-14 09:00 PDT", TokenFile: "/state/dashboard/token"}
}

// A part that replaces itself is moved onto a deployed build without being
// stopped by the supervisor first — its replacement is ready before the old one
// goes — and the move is a restart, with nothing counted against its bound. A
// replacement that could not be made ready leaves the old one serving, and is
// tried again at the next look at builds.
func TestADashboardIsReplacedOntoADeployedBuildWithoutAGap(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	clock := &clock{now: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	binary := newBinary(t, "1111111111111111")
	var events []string
	dashboard := &replacingChild{deployableChild: newDeployable(config.ServiceDashboard, binary, &events)}
	supervisor := deploySupervisor(t, store, clock, binary, dashboard)

	supervisor.Tick(context.Background())
	first := child(t, loaded(t, store), config.ServiceDashboard)
	if first.State != runstate.ChildRunning || first.URL != "http://127.0.0.1:8765/" || !strings.Contains(first.Health, "ready since") {
		t.Fatalf("dashboard = %+v, want it running with where it serves and its health", first)
	}
	said := DescribeChild(first)
	for _, want := range []string{"serving at http://127.0.0.1:8765/", "(ready since", "its token in /state/dashboard/token, which `yoyo dashboard token` prints"} {
		if !strings.Contains(said, want) {
			t.Errorf("DescribeChild = %q, want %q in it", said, want)
		}
	}

	dashboard.replaceErr = errors.New("the dashboard was not confirmed ready within 1m0s")
	binary.deploy(t, "2222222222222222")
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	failed := child(t, loaded(t, store), config.ServiceDashboard)
	if failed.State != runstate.ChildRunning || failed.Failures != 0 || failed.RestartingInto != "" || !strings.Contains(failed.Redeploy, "left serving") || dashboard.stopCount() != 0 {
		t.Fatalf("dashboard after a failed replacement = %+v (stops %d), want the old one serving, nothing counted, and the next look to try again", failed, dashboard.stopCount())
	}

	dashboard.replaceErr = nil
	clock.advance(DeployEvery)
	supervisor.Tick(context.Background())
	got := child(t, loaded(t, store), config.ServiceDashboard)
	if dashboard.stopCount() != 0 {
		t.Errorf("the supervisor stopped the dashboard itself %d times; a replacement stops the old one only once it is ready", dashboard.stopCount())
	}
	if got.State != runstate.ChildRunning || got.PID != 5001 || got.Build != "2222222222222222" || got.Restarts != 1 || got.Failures != 0 || got.Redeploy != "" {
		t.Errorf("dashboard after the replacement = %+v, want the replacement running on the deployed build, counted as a restart and not a failure", got)
	}
	if strings.Count(strings.Join(events, ","), "replace") != 2 {
		t.Errorf("events = %v, want the replacement tried and tried again", events)
	}
}
