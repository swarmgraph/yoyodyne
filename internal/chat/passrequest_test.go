package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func passRequestBlock(payload string) string {
	return passRequestFence + "\n" + payload + "\n```\n"
}

// knownInstances stands in for the CLI's check that the agent named is a
// configured program manager instance.
type knownInstances struct {
	store *runstate.PassRequestStore
	names map[string]bool
}

func (k knownInstances) Request(request runstate.PassRequest) (runstate.PassRequest, error) {
	if !k.names[request.Agent] {
		return runstate.PassRequest{}, errors.New(request.Agent + " is not a program manager instance this project configures")
	}
	return k.store.Request(request)
}

// askingProductManagerSession opens the Lead Product Manager's conversation
// over a pass request store, answering with the replies given in order.
func askingProductManagerSession(t *testing.T, root string, replies ...string) (*Session, *fakeBackend, *runstate.PassRequestStore) {
	t.Helper()

	requests, err := runstate.NewPassRequestStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewPassRequestStore() error = %v", err)
	}
	results := make([]backendapi.RunResult, 0, len(replies))
	for _, reply := range replies {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: reply})
	}
	provider := &fakeBackend{results: results}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.PassRequests = knownInstances{store: requests, names: map[string]bool{"technical-health-pm": true}}
	return openTestSession(t, options), provider, requests
}

// The Lead Product Manager asks a program manager instance for a pass from her
// conversation: the request is recorded as hers, naming her conversation and
// turn and its focus, and she is told nothing has run yet.
func TestTheLeadProductManagerAsksForAPassFromHerConversation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session, provider, requests := askingProductManagerSession(t, root,
		"I will ask the technical-health program manager to look at the tests.\n\n"+passRequestBlock(`{"agent":"technical-health-pm","focus":{"area":"test quality"}}`),
		"Noted.",
	)
	reply, err := session.Send(context.Background(), "Have the code quality looked at.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(reply.Text, "yoyodyne-pass-request") {
		t.Errorf("the pass request block reached the operator's prose:\n%s", reply.Text)
	}
	if !strings.Contains(provider.requests[0].SystemPrompt, "yoyodyne-pass-request") {
		t.Error("the Lead Product Manager's contract does not say how to ask for a pass")
	}
	if reply.PassRequest == nil || !reply.PassRequest.Recorded {
		t.Fatalf("reply.PassRequest = %+v, want the request recorded", reply.PassRequest)
	}
	open, found, err := requests.Open("technical-health-pm")
	if err != nil || !found {
		t.Fatalf("Open() = %v, %v, want the request open", found, err)
	}
	if open.AskedBy != runstate.PassAskerProductManager || open.ConversationID != session.state.ConversationID || open.Turn != 1 ||
		len(open.Focus.Areas) != 1 || open.Focus.Areas[0] != "test quality" {
		t.Errorf("request = %+v, want hers, from this conversation's first turn, with its area", open)
	}

	if _, err := session.Send(context.Background(), "And?"); err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	next := provider.requests[1].Prompt
	for _, required := range []string{"# Pass request", "Recorded as " + open.ID, "Nothing has run yet", "Do not say the pass has happened"} {
		if !strings.Contains(next, required) {
			t.Errorf("the next turn is not told %q:\n%s", required, next)
		}
	}
}

// An agent that is not a program manager instance, and a focus naming both an
// area and paths, are refused and nothing is recorded.
func TestAPassRequestForAnUnknownInstanceOrABadFocusIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session, _, requests := askingProductManagerSession(t, root,
		passRequestBlock(`{"agent":"developer"}`),
		passRequestBlock(`{"agent":"technical-health-pm","focus":{"area":"dead code","paths":["internal/chat"]}}`),
	)
	first, err := session.Send(context.Background(), "ask the developer")
	if err != nil || first.PassRequest == nil || first.PassRequest.Recorded || !strings.Contains(first.PassRequest.Failure, "not a program manager instance") {
		t.Fatalf("first = %+v, %v, want the unknown instance refused", first.PassRequest, err)
	}
	second, err := session.Send(context.Background(), "ask with both")
	if err != nil || second.PassRequest == nil || second.PassRequest.Recorded || !strings.Contains(second.PassRequest.Failure, "not both") {
		t.Fatalf("second = %+v, %v, want the doubled focus refused", second.PassRequest, err)
	}
	if listed, err := requests.List("technical-health-pm"); err != nil || len(listed) != 0 {
		t.Errorf("List() = %+v, %v, want nothing recorded", listed, err)
	}
}

// Only a role holding pass.request — the Lead Product Manager — may ask: every
// other role's block is refused before anything is recorded, the program
// manager's own included.
func TestOnlyTheLeadProductManagerMayAskForAPass(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		session := &Session{}
		session.state.Role = role
		err := session.authorize(parsedReply{PassRequest: &PassAsk{Agent: "technical-health-pm"}})
		var refusal *AuthorityError
		switch {
		case role == domain.RoleProductManager && err != nil:
			t.Errorf("authorize() of the Lead Product Manager's pass request = %v, want it permitted", err)
		case role != domain.RoleProductManager && !errors.As(err, &refusal):
			t.Errorf("authorize() of the %s's pass request = %v, want an authority refusal", role, err)
		}
	}
}

// A pass request block is read strictly.
func TestAPassRequestBlockIsReadStrictly(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		``,
		`{"agent":"technical-health-pm","now":true}`,
		`{"agent":"technical-health-pm"} {}`,
	} {
		if _, err := decodePassRequest(payload); err == nil {
			t.Errorf("decodePassRequest(%q) accepted the block", payload)
		}
	}
}
