package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/evaluation"
	"github.com/mason-bryant/yoyodyne/internal/research"
)

// The whole workflow the operator gets: they bring an idea, the product manager
// gathers evidence, and it records a recommendation that outlives the
// conversation. What it records is advice — nothing reaches the queue and no
// document changes — and the harness's own account of what it retrieved is kept
// beside the citations.
func TestAnIdeaBecomesADurableRecommendationAndNothingElse(t *testing.T) {
	t.Parallel()

	asked := "Let me check what has been tried.\n\n" +
		research.Fence + "\n" +
		`{"queries":[{"source":"web","question":"plugin marketplace prior art","why":"whether this has worked elsewhere"}]}` +
		"\n```"
	evaluated := "Three projects tried it and two report it grew adoption. I would not do it yet.\n\n" +
		evaluation.Fence + "\n" + testEvaluationBlock + "\n```"
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: evaluated},
	}}
	retrieved := time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)
	sources := &fakeResearch{
		policy: research.Policy{Sources: []research.Source{{Name: "web", Command: "search"}}},
		findings: []research.Finding{{
			Source:      "web",
			Question:    "plugin marketplace prior art",
			RetrievedAt: retrieved,
			Evidence:    "three projects have tried it",
		}},
	}
	record := &fakeEvaluations{}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{}}
	options := testOptions(t, provider)
	options.Research = sources
	options.Evaluations = record
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What if we built a plugin marketplace?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(record.recorded) != 1 {
		t.Fatalf("the record holds %d evaluation(s)", len(record.recorded))
	}
	recorded := record.recorded[0]
	if recorded.Entry.Recommendation != evaluation.RecommendDefer {
		t.Fatalf("recommendation = %q", recorded.Entry.Recommendation)
	}
	// The conversation and the turn are the harness's, not the agent's, so the
	// evaluation leads back to the exchange it came from once that exchange is
	// long over.
	if recorded.ConversationID != session.Evidence().ConversationID || recorded.Role != domain.RoleProductManager {
		t.Fatalf("attribution = %#v", recorded)
	}
	// What the harness actually retrieved travels with the recommendation, with
	// its own retrieval time. The citations are the agent's account of what it
	// read; this is what was fetched.
	if len(recorded.Research) != 1 || !recorded.Research[0].RetrievedAt.Equal(retrieved) {
		t.Fatalf("research on the record = %#v", recorded.Research)
	}
	if len(recorded.Entry.Sources) != 1 || recorded.Entry.Sources[0].Reference != "https://example.test/prior-art" {
		t.Fatalf("citations = %#v", recorded.Entry.Sources)
	}
	// Facts, inference, and uncertainty are kept apart, which is what a reader is
	// here to check.
	if len(recorded.Entry.Facts) != 1 || len(recorded.Entry.Inferences) != 1 || len(recorded.Entry.Uncertainties) != 1 {
		t.Fatalf("entry = %#v", recorded.Entry)
	}

	// Advice and nothing else: nothing reached the queue, nothing was proposed
	// for approval by recording it, and no document changed.
	if len(tracker.created) != 0 {
		t.Fatalf("recording an evaluation created %d work item(s)", len(tracker.created))
	}
	if len(reply.Proposals) != 0 || len(reply.Admitted) != 0 {
		t.Fatalf("recording an evaluation proposed %#v and admitted %#v", reply.Proposals, reply.Admitted)
	}
	if reply.Evaluation == nil || reply.Evaluation.ID != recorded.ID {
		t.Fatalf("reply evaluation = %#v", reply.Evaluation)
	}
}

// An evaluation and a proposal are separate things in the same reply. The
// recommendation is recorded, the work still goes through whatever approval the
// project asks for, and the two never stand in for each other.
func TestAnEvaluationDoesNotAdmitTheWorkItRecommends(t *testing.T) {
	t.Parallel()

	answer := "I would do it, and here is the item.\n\n" +
		"```yoyodyne-proposal\n" +
		`{"items":[{"kind":"feature","title":"Survey plugin demand","description":"ask twenty users","rationale":"the evaluation says the evidence is thin","goal":"` + recordedGoal + `"}]}` +
		"\n```\n\n" +
		evaluation.Fence + "\n" + testEvaluationBlock + "\n```"
	record := &fakeEvaluations{}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
	}})
	// The per-item gate, which is what a project that has not opted out has: the
	// work is put to the operator and nothing is created.
	options.Admission = Admission{WorkItems: domain.ApprovalHuman}
	options.Evaluations = record
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Is a plugin marketplace worth it?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(record.recorded) != 1 {
		t.Fatalf("the record holds %d evaluation(s)", len(record.recorded))
	}
	// The proposal is still waiting on the operator. An evaluation recommending
	// the work did not admit it, which is the boundary the whole arrangement
	// rests on.
	if len(reply.Proposals) != 1 || len(reply.Admitted) != 0 {
		t.Fatalf("proposals = %#v, admitted = %#v", reply.Proposals, reply.Admitted)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("the queue gained %d item(s) with nobody approving", len(tracker.created))
	}
}

// Recording is the product manager's. Another role that records one is refused
// by the harness rather than by prose, nothing is kept, and the reply still
// reaches the operator.
func TestOnlyTheProductManagerRecordsAnEvaluation(t *testing.T) {
	t.Parallel()

	answer := "Here is what I make of it.\n\n" + evaluation.Fence + "\n" + testEvaluationBlock + "\n```"
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDevelopmentManager, domain.RoleDeveloper, domain.RoleReviewer} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			record := &fakeEvaluations{}
			options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
			}})
			options.Role = role
			options.Agent = string(role)
			options.Evaluations = record
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "What do you make of this idea?")
			var refused *AuthorityError
			if !errors.As(err, &refused) {
				t.Fatalf("Send() error = %v, want an AuthorityError", err)
			}
			if !strings.Contains(refused.Error(), "an evaluation to be recorded") {
				t.Fatalf("refusal = %q", refused.Error())
			}
			if len(record.recorded) != 0 {
				t.Fatalf("a refused role still recorded %d evaluation(s)", len(record.recorded))
			}
			if !strings.Contains(reply.Text, "what I make of it") {
				t.Fatalf("the refusal swallowed the reply: %q", reply.Text)
			}
		})
	}
}

// An evaluation that could not be kept costs the reasoning and nothing else.
// The conversation is intact, the reply is real, and the operator is told what
// was lost rather than left believing it was written down.
func TestAnEvaluationThatCannotBeKeptIsSaidRatherThanSwallowed(t *testing.T) {
	t.Parallel()

	answer := "Here is what I make of it.\n\n" + evaluation.Fence + "\n" + testEvaluationBlock + "\n```"
	for _, test := range []struct {
		name        string
		evaluations Evaluations
		want        string
	}{
		{name: "nowhere to keep it", want: "no evaluation record is configured"},
		{name: "the record refused it", evaluations: &fakeEvaluations{err: errors.New("disk is full")}, want: "disk is full"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
			}})
			options.Evaluations = test.evaluations
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "Is this worth doing?")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if reply.Evaluation != nil {
				t.Fatalf("an unkept evaluation was reported as kept: %#v", reply.Evaluation)
			}
			if !strings.Contains(reply.EvaluationProblem, test.want) {
				t.Fatalf("problem = %q, want it to contain %q", reply.EvaluationProblem, test.want)
			}
			if !strings.Contains(reply.Text, "what I make of it") {
				t.Fatalf("the failure swallowed the reply: %q", reply.Text)
			}
		})
	}
}

// An unreadable block is not a broken conversation: the turn completed, the
// answer is real, and what is lost is the record of the reasoning.
func TestAnUnreadableEvaluationBlockLosesOnlyTheRecord(t *testing.T) {
	t.Parallel()

	record := &fakeEvaluations{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{
			SessionID:     "session-1",
			ResolvedModel: "claude-opus-5-20260514",
			FinalText:     "Here is what I make of it.\n\n" + evaluation.Fence + "\n{\"evaluation\":{\"idea\":\"i\",\"recommendation\":\"maybe\"}}\n```",
		},
	}})
	options.Evaluations = record
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Is this worth doing?")
	var unreadable *EvaluationError
	if !errors.As(err, &unreadable) {
		t.Fatalf("Send() error = %v, want an EvaluationError", err)
	}
	if len(record.recorded) != 0 {
		t.Fatalf("an unreadable block still recorded %d evaluation(s)", len(record.recorded))
	}
	if !strings.Contains(reply.Text, "what I make of it") {
		t.Fatalf("the failure swallowed the reply: %q", reply.Text)
	}
}

// The contract the product manager carries is what makes both capabilities
// something it knows it has and knows the bounds of. A persona cannot widen
// either, and the contract still says the role reaches nothing itself.
func TestTheContractCarriesResearchAndEvaluation(t *testing.T) {
	t.Parallel()

	prompt := SystemPrompt(domain.RoleProductManager, Admission{}, nil, hostilePersona)
	for _, required := range []string{
		research.Fence,
		evaluation.Fence,
		"admits no work, changes no document, and approves nothing",
		"Tracker operations, document writes, and configured research sources remain available only through the bounded harness blocks below",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("the product manager's contract does not carry %q", required)
		}
	}
	// No other role is told it may do either, because no other role may.
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDevelopmentManager, domain.RoleDeveloper, domain.RoleReviewer} {
		other := SystemPrompt(role, Admission{}, nil, "")
		if strings.Contains(other, research.Fence) || strings.Contains(other, evaluation.Fence) {
			t.Fatalf("the %s is offered a capability it has no authority for", role)
		}
	}
}

// fakeEvaluations stands in for the durable record and keeps exactly what it was
// given, which is what makes "a refused role recorded nothing" an assertion.
type fakeEvaluations struct {
	recorded []evaluation.Evaluation
	err      error
}

func (f *fakeEvaluations) Append(recorded evaluation.Evaluation) error {
	if f.err != nil {
		return f.err
	}
	f.recorded = append(f.recorded, recorded)
	return nil
}

const testEvaluationBlock = `{"evaluation":{
  "idea":"a plugin marketplace",
  "recommendation":"defer",
  "alignment":"no goal covers third-party extensions yet",
  "reasoning":"the evidence is thin and nothing in the goals asks for it",
  "facts":[{"claim":"three projects have tried it","source":"https://example.test/prior-art"}],
  "inferences":["the maintenance cost lands on us rather than on the plugin authors"],
  "uncertainties":["how many of our users would write one"],
  "counterevidence":["two of the three report it grew adoption"],
  "sources":[{"reference":"https://example.test/prior-art","source":"web","note":"prior art"}],
  "evidence_gap":"nothing addressed our own users",
  "follow_up":"a bounded survey before anything is admitted"
}}`
