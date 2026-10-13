package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// fakePublications is the harness's hand for a decision about a stopped
// document publication: which runs are document runs, and every decision it was
// asked to carry out.
type fakePublications struct {
	documents map[string]bool
	retried   []runstate.DocumentDecision
	retired   []runstate.DocumentDecision
	runs      []string
	err       error
}

func (f *fakePublications) DocumentRun(_ context.Context, runID string) (bool, error) {
	return f.documents[runID], nil
}

func (f *fakePublications) RetryDocument(_ context.Context, runID string, decision runstate.DocumentDecision) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.runs = append(f.runs, runID)
	f.retried = append(f.retried, decision)
	return "a fresh run waits for a document landing", nil
}

func (f *fakePublications) RetireDocument(_ context.Context, runID string, decision runstate.DocumentDecision) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.runs = append(f.runs, runID)
	f.retired = append(f.retired, decision)
	return "its pull request #1173 is closed with the reason", nil
}

func documentDecisionReply(t *testing.T, publications StoppedPublications, docket TriageEntries, action string) Reply {
	t.Helper()

	options := triageOptions(t, &fakeTracker{items: map[string]beads.WorkItem{}}, nil, trackerReply("Deciding the stopped publication.", action))
	options.Reports = &fakeReports{}
	options.Docket = docket
	options.Publications = publications
	return triageSend(t, options)
}

// Both decisions about a stopped document publication name its run and no work
// item, because it has none: each is carried out by the harness's hand, recorded
// with her reasoning and who she is, and closes the run's docket entry.
func TestTheDevelopmentManagerRetriesOrRetiresADocumentPublicationWithNoWorkItem(t *testing.T) {
	t.Parallel()

	for _, decided := range []string{"retry", "retire"} {
		t.Run(decided, func(t *testing.T) {
			t.Parallel()

			publications := &fakePublications{documents: map[string]bool{stoppedRun: true}}
			docket := &fakeDocket{closes: 1}
			reply := documentDecisionReply(t, publications, docket,
				`{"action":"triage","run":"`+stoppedRun+`","decision":"`+decided+`","reason":"the newest copy of the configurable workflows revision"}`)

			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v", reply.Actions)
			}
			recorded := publications.retired
			if decided == "retry" {
				recorded = publications.retried
			}
			if len(recorded) != 1 || len(publications.runs) != 1 || publications.runs[0] != stoppedRun {
				t.Fatalf("carried out %v on %v, want one %s of the run named", recorded, publications.runs, decided)
			}
			if !strings.Contains(recorded[0].Reason, "newest copy") || !strings.HasPrefix(recorded[0].DecidedBy, "the development manager in conversation ") {
				t.Fatalf("decision = %#v, want her reasoning and her conversation", recorded[0])
			}
			if len(docket.closed) != 1 || docket.closed[0].RunID != stoppedRun || docket.closed[0].Decision != decided || !slices.Contains(docket.closed[0].Classes, triage.ClassStoppedRun) {
				t.Fatalf("closed = %#v, want the run's stopped-run entry settled by the decision", docket.closed)
			}
			rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
			if !strings.Contains(rendered, "document publication of run "+stoppedRun) {
				t.Fatalf("she was not told what was done:\n%s", rendered)
			}
		})
	}
}

// A decision the harness could not carry out closes nothing, so the stopped
// publication is put to her again.
func TestADocumentDecisionThatFailsLeavesTheEntryStanding(t *testing.T) {
	t.Parallel()

	publications := &fakePublications{documents: map[string]bool{stoppedRun: true}, err: errors.New("pull request #1173 was not closed")}
	docket := &fakeDocket{closes: 1}
	reply := documentDecisionReply(t, publications, docket,
		`{"action":"triage","run":"`+stoppedRun+`","decision":"retire","reason":"an older copy"}`)

	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "was not closed") {
		t.Fatalf("actions = %#v, want the failure reported", reply.Actions)
	}
	if len(docket.closed) != 0 {
		t.Fatalf("closed = %#v, want nothing closed", docket.closed)
	}
}

// A decision that needs a work item is refused on a document publication, in
// the sentence naming the two that answer one; and the two that answer one are
// refused without the run they decide.
func TestADocumentPublicationIsDecidedOnlyByRetryOrRetire(t *testing.T) {
	t.Parallel()

	publications := &fakePublications{documents: map[string]bool{stoppedRun: true}}
	reply := documentDecisionReply(t, publications, &fakeDocket{closes: 1},
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"split it"}`)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, `"retry"`) || !strings.Contains(reply.Actions[0].Failure, "has no work item") {
		t.Fatalf("actions = %#v, want the decision refused naming retry and retire", reply.Actions)
	}

	for _, invalid := range []string{
		`{"action":"triage","decision":"retry","reason":"no run named"}`,
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"` + stoppedRun + `","decision":"retire","reason":"an item named"}`,
	} {
		if _, _, err := decodeTrackerActions(`{"actions":[` + invalid + `]}`); err == nil {
			t.Fatalf("%s validated, want it refused", invalid)
		}
	}
	if _, _, err := decodeTrackerActions(`{"actions":[{"action":"triage","run":"` + stoppedRun + `","decision":"retire","reason":"an older copy"}]}`); err != nil {
		t.Fatalf("a retirement naming only the run was refused: %v", err)
	}
}
