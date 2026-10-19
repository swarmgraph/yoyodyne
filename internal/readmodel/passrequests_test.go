package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakePassRequests struct {
	requests map[string][]runstate.PassRequest
	err      error
}

func (f fakePassRequests) List(agent string) ([]runstate.PassRequest, error) {
	return f.requests[agent], f.err
}

// passRequestsOf records requests in a real store, so what is read back is what
// the scheduler writes: one completed, one failed, and one still waiting.
func passRequestsOf(t *testing.T, agent string) *runstate.PassRequestStore {
	t.Helper()
	store, err := runstate.NewPassRequestStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	ask := func(at time.Time) runstate.PassRequest {
		id, err := runstate.NewPassRequestID()
		if err != nil {
			t.Fatal(err)
		}
		recorded, err := store.Request(runstate.PassRequest{SchemaVersion: runstate.PassRequestSchemaVersion, ProductID: "yoyodyne",
			ID: id, Agent: agent, AskedBy: runstate.PassAskerProductManager, RequestedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		return recorded
	}
	pass := 0
	end := func(request runstate.PassRequest, at time.Time, outcome runstate.PassRequestOutcome, problem string) {
		pass++
		name := fmt.Sprintf("%s#%d", agent, pass)
		if _, err := store.Take(agent, request.ID, name, at); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Close(agent, request.ID, name, outcome, problem, at.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	end(ask(moment.Add(-3*time.Hour)), moment.Add(-3*time.Hour), runstate.PassRequestCompleted, "")
	end(ask(moment.Add(-2*time.Hour)), moment.Add(-2*time.Hour), runstate.PassRequestFailed, "a turn of the pass was\nnot answered with its account")
	ask(moment.Add(-time.Minute))
	return store
}

// Each request for an instance's pass is carried with what became of it and
// since when, in the read model `yoyo status --json` emits and on the lines
// `yoyo status` prints, so whoever asked can read it.
func TestEachPassRequestIsReadWithWhatBecameOfIt(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.PassRequests = passRequestsOf(t, "writing-pgm")
	instance := instanceNamed(t, sources, "writing-pgm")
	var states []runstate.PassRequestState
	for _, request := range instance.PassRequests {
		states = append(states, request.State)
	}
	if want := []runstate.PassRequestState{runstate.PassRequestStateCompleted, runstate.PassRequestStateFailed, runstate.PassRequestStateWaiting}; fmt.Sprint(states) != fmt.Sprint(want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
	failed := instance.PassRequests[1]
	if !failed.Since.Equal(moment.Add(-2*time.Hour + 5*time.Minute)) {
		t.Errorf("failed since %s, want when its pass ended", failed.Since)
	}
	if other := instanceNamed(t, sources, "factory-pgm"); other.PassRequests == nil || len(other.PassRequests) != 0 {
		t.Errorf("factory-pgm requests = %#v, want empty rather than absent", other.PassRequests)
	}

	encoded, err := json.Marshal(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"pass_requests":[`, `"state":"failed"`, `"since":`, `"asked_by":"product-manager"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("JSON = %s, want it to carry %s", encoded, want)
		}
	}

	rendered := ReadStanding(context.Background(), sources).RenderProgramManagers()
	for _, want := range []string{
		"    Pass requests:\n",
		"asked by the Lead Product Manager at " + localMoment(moment.Add(-3*time.Hour)) + ": completed at " + localMoment(moment.Add(-3*time.Hour+5*time.Minute)) + " by the pass writing-pgm#1",
		": failed at " + localMoment(moment.Add(-2*time.Hour+5*time.Minute)) + " in the pass writing-pgm#2: a turn of the pass was not answered with its account\n",
		": waiting to be taken at the scheduler's next pull\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("RenderProgramManagers() =\n%s\nwant it to contain %q", rendered, want)
		}
	}
	if strings.Count(rendered, "Pass requests:") != 1 {
		t.Errorf("RenderProgramManagers() =\n%s\nwant no requests listed for an instance nobody asked", rendered)
	}
}

// Every open request is carried, and only the latest of the closed ones, so a
// long history does not crowd the line.
func TestOnlyTheLatestClosedPassRequestsAreCarried(t *testing.T) {
	t.Parallel()

	var requests []runstate.PassRequest
	for index := 0; index < 8; index++ {
		at := moment.Add(time.Duration(index-10) * time.Hour)
		closed := at.Add(time.Minute)
		requests = append(requests, runstate.PassRequest{ID: fmt.Sprintf("pass-request-%02d", index), Agent: "writing-pgm",
			AskedBy: runstate.PassAskerOperator, RequestedAt: at, Pass: "writing-pgm#1", ClosedAt: &closed, Outcome: runstate.PassRequestCompleted})
	}
	sources := programManagerSources()
	sources.PassRequests = fakePassRequests{requests: map[string][]runstate.PassRequest{"writing-pgm": requests}}
	shown := instanceNamed(t, sources, "writing-pgm").PassRequests
	if len(shown) != maxPassRequestsShown || shown[0].ID != "pass-request-03" || shown[len(shown)-1].ID != "pass-request-07" {
		t.Errorf("shown = %+v, want the latest %d", shown, maxPassRequestsShown)
	}

	// An open request made after them is carried beside the latest five
	// closed ones, and pushes none of them off.
	requests = append(requests, runstate.PassRequest{ID: "pass-request-08", Agent: "writing-pgm",
		AskedBy: runstate.PassAskerOperator, RequestedAt: moment.Add(-time.Hour)})
	sources.PassRequests = fakePassRequests{requests: map[string][]runstate.PassRequest{"writing-pgm": requests}}
	shown = instanceNamed(t, sources, "writing-pgm").PassRequests
	if len(shown) != maxPassRequestsShown+1 || shown[0].ID != "pass-request-03" || shown[len(shown)-1].ID != "pass-request-08" || shown[len(shown)-1].State != runstate.PassRequestStateWaiting {
		t.Errorf("shown = %+v, want the open request and the latest %d closed ones", shown, maxPassRequestsShown)
	}
}

// A request log that cannot be read is said, rather than read as nobody having
// asked.
func TestAnUnreadablePassRequestLogIsSaid(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.PassRequests = fakePassRequests{err: errors.New("permission denied")}
	_, problem := ReadProgramManagers(sources)
	if !strings.Contains(problem, "the pass requests of writing-pgm could not be read") || !strings.Contains(problem, "permission denied") {
		t.Errorf("problem = %q, want the unreadable log said", problem)
	}
}
