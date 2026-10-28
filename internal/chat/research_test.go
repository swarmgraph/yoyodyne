package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/research"
)

// The operator brings an idea, the product manager asks the harness to find
// something out, and it answers from what came back. The evidence is delivered
// inside the same message rather than left for the next one, exactly as the
// tracker's results are.
func TestTheProductManagerGathersEvidenceAndAnswersFromIt(t *testing.T) {
	t.Parallel()

	asked := "I do not know what the licences say, so let me check.\n\n" +
		research.Fence + "\n" +
		`{"queries":[{"source":"web","question":"AGPL and Apache 2.0 compatibility","why":"the recommendation turns on it"}]}` +
		"\n```"
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "They are compatible one way only, so this would work."},
	}}
	sources := &fakeResearch{
		policy: research.Policy{Sources: []research.Source{{Name: "web", Command: "search"}}},
		findings: []research.Finding{{
			Source:      "web",
			Question:    "AGPL and Apache 2.0 compatibility",
			RetrievedAt: time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC),
			Evidence:    "Compatible one way only.",
		}},
	}
	options := testOptions(t, provider)
	options.Research = sources
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What if we vendored that AGPL library?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(sources.asked) != 1 || sources.asked[0].Source != "web" {
		t.Fatalf("the harness asked %#v", sources.asked)
	}
	// One round of research, reported to the operator: it spends their money
	// outside this machine, so a question nobody is told about is spending they
	// cannot see.
	if len(reply.Research) != 1 || reply.Research[0].Problem != "" || len(reply.Research[0].Findings) != 1 {
		t.Fatalf("reply research = %#v", reply.Research)
	}
	if !strings.Contains(reply.Research[0].Render(), "web: AGPL and Apache 2.0 compatibility") {
		t.Fatalf("the round does not name what was asked: %q", reply.Research[0].Render())
	}
	// The evidence reached the next round of the same message, framed as
	// untrusted, and the prose from both rounds is what the operator reads.
	if len(provider.requests) != 2 {
		t.Fatalf("the message took %d turn(s)", len(provider.requests))
	}
	continuation := provider.requests[1].Prompt
	for _, required := range []string{"Compatible one way only.", "untrusted text retrieved from outside", "2026-08-23T09:30:00Z"} {
		if !strings.Contains(continuation, required) {
			t.Fatalf("the continuation = %q, want it to contain %q", continuation, required)
		}
	}
	if !strings.Contains(reply.Text, "compatible one way only") {
		t.Fatalf("reply = %q", reply.Text)
	}
	// The role is still toolless. Whatever it may have the harness do, it runs
	// nothing itself.
	for _, request := range provider.requests {
		if len(request.AllowedTools) != 0 {
			t.Fatalf("the conversation was granted tools: %#v", request.AllowedTools)
		}
	}
}

// The sources are delivered with the turn rather than written into the
// contract, because which sources exist is the project's own and it moves. A
// project that permitted none says so, so the role tells the operator it could
// not check rather than answering from memory as though it had.
func TestTheTurnSaysWhichSourcesArePermitted(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		research Research
		want     string
		absent   string
	}{
		{
			name:     "sources configured",
			research: &fakeResearch{policy: research.Policy{Sources: []research.Source{{Name: "web", Command: "search", Describes: "public web search"}}}},
			want:     "public web search",
		},
		{
			name:     "no source configured",
			research: &fakeResearch{},
			want:     "configured no research sources",
		},
		{
			name:   "no capability wired at all",
			want:   "no research capability wired to it",
			absent: "yoyodyne-research block is available",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Noted."},
			}}
			options := testOptions(t, provider)
			options.Research = test.research
			session := openTestSession(t, options)
			if _, err := session.Send(context.Background(), "What if we did X?"); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			prompt := provider.requests[0].Prompt
			if !strings.Contains(prompt, test.want) {
				t.Fatalf("the turn does not say %q: %q", test.want, prompt)
			}
			if test.absent != "" && strings.Contains(prompt, test.absent) {
				t.Fatalf("the turn claims %q", test.absent)
			}
		})
	}

	// A role with no research authority is told nothing at all, rather than being
	// told there is nothing: those are different, and only one is worth the
	// context.
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "The design records that already."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleArchitect
	options.Agent = string(domain.RoleArchitect)
	options.Research = &fakeResearch{policy: research.Policy{Sources: []research.Source{{Name: "web", Command: "search"}}}}
	architect := openTestSession(t, options)
	if _, err := architect.Send(context.Background(), "Where is the lease recorded?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[0].Prompt, "Research sources available to you") {
		t.Fatalf("a role with no research authority was offered sources: %q", provider.requests[0].Prompt)
	}
}

// Research is the product manager's. Another role that asks for it is refused by
// the harness rather than by prose, nothing is retrieved, and the reply is still
// the operator's to read.
func TestOnlyTheProductManagerGathersEvidence(t *testing.T) {
	t.Parallel()

	asked := "Let me look that up.\n\n" +
		research.Fence + "\n" +
		`{"queries":[{"source":"web","question":"q","why":"w"}]}` +
		"\n```"
	for _, role := range []domain.AgentRole{domain.RoleArchitect, domain.RoleDevelopmentManager, domain.RoleDeveloper, domain.RoleReviewer} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			sources := &fakeResearch{policy: research.Policy{Sources: []research.Source{{Name: "web", Command: "search"}}}}
			options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
			}})
			options.Role = role
			options.Agent = string(role)
			options.Research = sources
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "What do you make of this?")
			var refused *AuthorityError
			if !errors.As(err, &refused) {
				t.Fatalf("Send() error = %v, want an AuthorityError", err)
			}
			if !strings.Contains(refused.Error(), "evidence to be gathered from outside the repository") {
				t.Fatalf("refusal = %q", refused.Error())
			}
			if len(sources.asked) != 0 {
				t.Fatalf("a refused role still reached %d source(s)", len(sources.asked))
			}
			if !strings.Contains(reply.Text, "look that up") {
				t.Fatalf("the refusal swallowed the reply: %q", reply.Text)
			}
		})
	}
}

// A source that will not answer is something the role is told, so it can say it
// could not find out. The reply is not lost over it, and neither is the round.
func TestEvidenceThatCouldNotBeObtainedIsSaidRatherThanSwallowed(t *testing.T) {
	t.Parallel()

	asked := "Let me check.\n\n" +
		research.Fence + "\n" +
		`{"queries":[{"source":"web","question":"q","why":"w"}]}` +
		"\n```"
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "I could not find out, so I would not recommend either way yet."},
	}}
	options := testOptions(t, provider)
	// A conversation with the capability wired and no source permitted: the block
	// reaches nothing, and saying so is the point.
	options.Research = &fakeResearch{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What if we did X?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Research) != 1 || reply.Research[0].Problem == "" {
		t.Fatalf("reply research = %#v", reply.Research)
	}
	if !strings.Contains(provider.requests[1].Prompt, "Nothing was retrieved") {
		t.Fatalf("the role was not told the retrieval failed: %q", provider.requests[1].Prompt)
	}
	if !strings.Contains(reply.Research[0].Render(), "nothing was retrieved") {
		t.Fatalf("the operator was not told either: %q", reply.Research[0].Render())
	}
}

// One message gathers evidence a bounded number of times. Past the budget
// nothing further is asked, the refusal is on the reply, and the reply still lands.
func TestOneMessageGathersEvidenceABoundedNumberOfTimes(t *testing.T) {
	t.Parallel()

	asked := "Let me check.\n\n" +
		research.Fence + "\n" +
		`{"queries":[{"source":"web","question":"q","why":"w"}]}` +
		"\n```"
	results := make([]backendapi.RunResult, 0, maxResearchRounds+2)
	for i := 0; i < maxResearchRounds+1; i++ {
		results = append(results, backendapi.RunResult{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: asked})
	}
	results = append(results, backendapi.RunResult{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "That is as far as I can check."})
	provider := &fakeBackend{results: results}
	sources := &fakeResearch{
		policy:   research.Policy{Sources: []research.Source{{Name: "web", Command: "search"}}},
		findings: []research.Finding{{Source: "web", Question: "q", RetrievedAt: fixedClock{}.Now(), Evidence: "something"}},
	}
	options := testOptions(t, provider)
	options.Research = sources
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What if we did X?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(sources.asked) != maxResearchRounds {
		t.Fatalf("the harness went outside the machine %d time(s), want %d", len(sources.asked), maxResearchRounds)
	}
	spent := reply.Research[len(reply.Research)-1]
	if spent.Problem == "" || !strings.Contains(spent.Problem, "nothing further was asked") {
		t.Fatalf("the spent budget was not reported: %#v", spent)
	}
}

// fakeResearch stands in for the configured evidence sources and records exactly
// what it was asked, which is what makes "a refused role reached nothing" an
// assertion rather than a claim.
type fakeResearch struct {
	policy   research.Policy
	findings []research.Finding
	err      error
	asked    []research.Query
}

func (f *fakeResearch) Permitted() research.Policy { return f.policy }

func (f *fakeResearch) Search(_ context.Context, queries []research.Query) ([]research.Finding, error) {
	f.asked = append(f.asked, queries...)
	if f.err != nil {
		return nil, f.err
	}
	return f.findings, nil
}
