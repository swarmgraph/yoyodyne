package readmodel

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeSupervision struct {
	running     bool
	recorded    runstate.Supervision
	found       bool
	failRunning error
	failLoad    error
}

func (f fakeSupervision) Running() (bool, error) { return f.running, f.failRunning }

func (f fakeSupervision) Load() (runstate.Supervision, bool, error) {
	return f.recorded, f.found, f.failLoad
}

// A part the supervisor has left down is down and not coming back on its own,
// so it is on the attention line with the supervisor's own reason, and the
// record is carried whole beside the lines for the surfaces that read it.
func TestADegradedServiceWaitsOnTheOperatorWithTheSupervisorsReason(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Supervision = fakeSupervision{
		running: true,
		found:   true,
		recorded: runstate.Supervision{
			PID: 4242,
			Children: []runstate.SupervisedChild{
				{Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 77},
				{Service: config.ServiceScheduler, State: runstate.ChildDegraded, Reason: "died 6 times within 2m0s of being started, most recently at 2026-09-19T12:03:00Z, so it is left down"},
			},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if standing.Services == nil || !standing.Services.SupervisorRunning || !standing.Services.Recorded || standing.Services.Record.PID != 4242 {
		t.Fatalf("Services = %+v, want the supervisor running and its record carried", standing.Services)
	}
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("NeedsHuman = %+v, want the one degraded child", standing.NeedsHuman)
	}
	if got := standing.NeedsHuman[0]; !strings.Contains(got.What(), "scheduler service is degraded") || !strings.Contains(got.What(), "died 6 times") || !strings.HasPrefix(got.Whose(), "the operator's") {
		t.Errorf("attention = %+v, want the child, the reason, and whose move it is", got)
	}
	rendered := standing.Render()
	if !strings.Contains(rendered, "Needs a human (1):\n  the scheduler service is degraded: died 6 times") {
		t.Errorf("rendered:\n%s\nwant the degraded child on the attention line", rendered)
	}
}

// A running part is not attention, a product nothing has supervised says so
// rather than reporting parts down, and a reading with nothing wired says
// nothing at all.
func TestARunningServiceAndAnUnsupervisedProductWaitOnNobody(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Supervision = fakeSupervision{running: true, found: true, recorded: runstate.Supervision{
		PID:      1,
		Children: []runstate.SupervisedChild{{Service: config.ServiceSlack, State: runstate.ChildRunning}},
	}}
	if standing := ReadStanding(context.Background(), sources); len(standing.NeedsHuman) != 0 {
		t.Errorf("a running service was reported as waiting on somebody: %+v", standing.NeedsHuman)
	}

	sources.Supervision = fakeSupervision{}
	standing := ReadStanding(context.Background(), sources)
	if standing.Services == nil || standing.Services.SupervisorRunning || standing.Services.Recorded || len(standing.NeedsHuman) != 0 {
		t.Errorf("an unsupervised product = %+v, %+v, want no supervisor, no record, and nothing waiting", standing.Services, standing.NeedsHuman)
	}

	sources.Supervision = nil
	if standing := ReadStanding(context.Background(), sources); standing.Services != nil {
		t.Errorf("Services = %+v with nothing wired, want nil", standing.Services)
	}
}

// A record that cannot be read is said on the line it would have been said on
// rather than read as every part running.
func TestAnUnreadableSupervisionRecordIsSaidRatherThanAssumedHealthy(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Supervision = fakeSupervision{failLoad: errors.New("decode supervision record: unexpected EOF")}
	standing := ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.ServicesProblem, "unexpected EOF") || !strings.Contains(standing.NeedsHumanProblem, "unexpected EOF") {
		t.Errorf("problems = %q / %q, want the unreadable record named on both", standing.ServicesProblem, standing.NeedsHumanProblem)
	}
}

// The services line says which build each part is on and when it last moved,
// beside what the binary on disk is, and where a deploy is moving a part.
func TestTheServicesLineSaysWhichBuildEachPartIsOnAndWhenItMoved(t *testing.T) {
	t.Parallel()

	moved := time.Date(2026, 9, 28, 16, 40, 0, 0, time.UTC)
	sources := quietSources()
	sources.Supervision = fakeSupervision{
		running: true,
		found:   true,
		recorded: runstate.Supervision{
			PID:      4242,
			Deployed: "3d3d367a1b2c4d5e",
			Children: []runstate.SupervisedChild{
				{Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 77, Build: "3d3d367a1b2c4d5e", BuildSince: moved, Restarts: 1},
				{Service: config.ServiceScheduler, State: runstate.ChildRunning, PID: 78, Build: "1a2b3c4d5e6f7a8b", BuildSince: moved.Add(-time.Hour),
					Redeploy: "on build 1a2b3c4d5e6f, behind the deployed 3d3d367a1b2c; the watch restarts itself into it between runs"},
			},
		},
	}
	rendered := ReadStanding(context.Background(), sources).RenderServices()
	for _, want := range []string{
		"Services (supervisor running as pid 4242; the binary on disk is build 3d3d367a1b2c):\n",
		"  slack: running as pid 77, on build 3d3d367a1b2c since " + moved.Local().Format("2006-01-02 15:04 MST") + " (restarted into a deployed build once)\n",
		"  scheduler: running as pid 78, on build 1a2b3c4d5e6f since ",
		"; on build 1a2b3c4d5e6f, behind the deployed 3d3d367a1b2c; the watch restarts itself into it between runs\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered:\n%s\nwant it to contain %q", rendered, want)
		}
	}
}

// A product no supervisor has run for prints no services line at all.
func TestAnUnsupervisedProductPrintsNoServicesLine(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Supervision = fakeSupervision{}
	if rendered := ReadStanding(context.Background(), sources).RenderServices(); rendered != "" {
		t.Errorf("rendered %q, want nothing for a product no supervisor has run for", rendered)
	}
}

type fakeConfigReaders struct {
	mismatches []runstate.ConfigMismatch
	running    []runstate.ConfigReader
	err        error
}

func (f fakeConfigReaders) Mismatches() ([]runstate.ConfigMismatch, error) {
	return f.mismatches, f.err
}

func (f fakeConfigReaders) Running() ([]runstate.ConfigReader, error) {
	return f.running, f.err
}

// A running part whose build cannot read the configuration is on the attention
// line naming the part, its build, and the key, as the harness's move — the
// dashboard as well as the watch, with what restarts each said beside it.
func TestAPartWhoseBuildCannotReadTheConfigurationIsNamed(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	started := time.Date(2026, 9, 26, 23, 36, 0, 0, time.UTC)
	mismatches := []runstate.ConfigMismatch{
		{Service: "dashboard", PID: 4242, Build: "0364141b2c3d4e5f", ConfigPath: "/work/.yoyodyne/config.yaml", StartedAt: started, Keys: []string{"agents.developer.effort"}},
		{Service: "dashboard", PID: 4243, Build: "0364141b2c3d4e5f", ConfigPath: "/work/.yoyodyne/config.yaml", StartedAt: started, Keys: []string{"agents.developer.effort"}},
		{Service: "scheduler", PID: 4343, Build: "0364141b2c3d4e5f", ConfigPath: "/work/.yoyodyne/config.yaml", StartedAt: started, Keys: []string{"agents.developer.effort"}},
	}
	sources.ConfigReaders = fakeConfigReaders{mismatches: mismatches}
	standing := ReadStanding(context.Background(), sources)
	movers := map[string]Mover{}
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionConfigMismatch {
			movers[entry.ID] = entry.Mover
			if !strings.Contains(entry.What(), "build 0364141b2c3d") || !strings.Contains(entry.What(), "agents.developer.effort") {
				t.Errorf("what = %q, want the build and the key", entry.What())
			}
		}
	}
	if len(movers) != len(mismatches) {
		t.Fatalf("movers = %v, want all three instances", movers)
	}
	for _, mismatch := range mismatches {
		if movers[mismatch.InstanceID()] != MoverHarness {
			t.Fatalf("movers = %v, want every instance to be the harness's", movers)
		}
	}
	rendered := standing.Render()
	if !strings.Contains(rendered, "the dashboard service, running build 0364141b2c3d") {
		t.Errorf("rendered:\n%s\nwant the dashboard named", rendered)
	}

	sources.ConfigReaders = fakeConfigReaders{err: errors.New("decode configuration reader record: unexpected EOF")}
	unread := ReadStanding(context.Background(), sources)
	if !strings.Contains(unread.NeedsHumanProblem, "unexpected EOF") {
		t.Errorf("NeedsHumanProblem = %q, want the unreadable record said", unread.NeedsHumanProblem)
	}
}

// More than one copy of a part running, and a copy on an old build, are in the
// reading `yoyo status` draws from: each copy with its process, build, and
// start, and which is the supervisor's, said on the services line, on the
// attention line with whose move it is, and among the factory problems. A part
// with one copy is counted and raises nothing.
func TestMoreThanOneCopyAndACopyOnAnOldBuildAreInTheReadModel(t *testing.T) {
	t.Parallel()

	deployed := "3d3d367a1b2c4d5e"
	started := time.Date(2026, 10, 4, 17, 35, 0, 0, time.UTC)
	sources := quietSources()
	sources.Supervision = fakeSupervision{running: true, found: true, recorded: runstate.Supervision{
		PID:      4242,
		Deployed: deployed,
		Children: []runstate.SupervisedChild{
			{Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 77, Build: deployed},
			{Service: config.ServiceDashboard, State: runstate.ChildNotYet},
			{Service: config.ServiceScheduler, State: runstate.ChildRunning, PID: 78, Build: deployed},
		},
	}}
	sources.ConfigReaders = fakeConfigReaders{running: []runstate.ConfigReader{
		{Service: "supervisor", PID: 4242, Build: deployed, StartedAt: started.Add(time.Hour)},
		{Service: "slack", PID: 77, Build: deployed, StartedAt: started.Add(time.Hour)},
		{Service: "slack", PID: 99, Build: "1a2b3c4d5e6f7a8b", StartedAt: started},
		{Service: "scheduler", PID: 78, Build: deployed, StartedAt: started.Add(time.Hour)},
		// Copies of a part the supervisor does not start are not its to count.
		{Service: "dashboard", PID: 80, Build: "1a2b3c4d5e6f7a8b", StartedAt: started},
		{Service: "dashboard", PID: 81, Build: deployed, StartedAt: started},
	}}
	standing := ReadStanding(context.Background(), sources)

	counts := map[string]int{}
	var slack ServiceCopies
	for _, copies := range standing.Services.Copies {
		counts[copies.Service] = copies.Count()
		if copies.Service == "slack" {
			slack = copies
		}
	}
	if want := map[string]int{"slack": 2, "scheduler": 1, "supervisor": 1}; !reflect.DeepEqual(counts, want) {
		t.Fatalf("copies counted = %v, want %v", counts, want)
	}
	if extra := slack.Extra(); len(extra) != 1 || extra[0].PID != 99 || !extra[0].Behind || extra[0].Build != "1a2b3c4d5e6f7a8b" || !extra[0].StartedAt.Equal(started) {
		t.Fatalf("slack's extra copies = %+v, want pid 99, on its old build, with its start", extra)
	}

	var raised []Attention
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionServiceCopies {
			raised = append(raised, entry)
		}
	}
	if len(raised) != 1 || raised[0].ID != "slack" || raised[0].Mover != MoverDevelopmentManager {
		t.Fatalf("attention = %+v, want the slack service alone, the development manager's", raised)
	}
	if what := raised[0].What(); !strings.Contains(what, "the slack service has 2 copies running") || !strings.Contains(what, "pid 99 on build 1a2b3c4d5e6f, an old build,") {
		t.Errorf("what = %q, want the count and the copy on its old build", what)
	}
	inFactory := false
	for _, entry := range standing.FactoryProblems {
		inFactory = inFactory || (entry.Kind == AttentionServiceCopies && entry.ID == "slack")
	}
	if !inFactory {
		t.Errorf("factory problems = %+v, want the extra copy among them", standing.FactoryProblems)
	}
	rendered := standing.Render() + standing.RenderServices()
	for _, want := range []string{"Waiting on the development manager", "the slack service has 2 copies running, where one should run", "(not the supervisor's, left running)"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered:\n%s\nwant %q in it", rendered, want)
		}
	}
}
