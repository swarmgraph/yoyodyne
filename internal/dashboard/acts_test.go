package dashboard

// These tests are the evidence for the operator's acts from the page: an
// approve, a decline, and an ask carried out for a person holding the grant
// the decision needs, and every refusal the design names — no token, the
// shared token, a person without the grant, a decision no longer waiting, an
// act that is not one of the three, and every provenance check the reads
// hold — each with its reason, nothing carried out, and nothing of the request
// reflected back.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// Two people's own tokens, long enough to be bound, and the three decisions
// the actor holds as waiting: a change to a design, a change to the goals, and
// a document written in the Lead Product Manager's conversation.
var (
	ownerToken   = strings.Repeat("a1", tokenBytes)
	readerToken  = strings.Repeat("b2", tokenBytes)
	architectDoc = readmodel.OperatorDecision{Kind: readmodel.DecisionAmendment, ID: "amendment-3f9a1c2e8b7d4f6a9c1e2b3d4f5a6b7c", Label: "proposed document change", Role: domain.RoleDeveloper, Owner: domain.RoleArchitect, AskRole: domain.RoleDeveloper, AskAgent: "developer"}
	goalsChange  = readmodel.OperatorDecision{Kind: readmodel.DecisionAmendment, ID: "amendment-7c2b9e4d1a6f3c8e5b0d2f4a6c8e1b3d", Label: "proposed document change", Role: domain.RoleArchitect, Owner: domain.RoleProductManager}
	document     = readmodel.OperatorDecision{Kind: readmodel.DecisionDocument, ID: "document-41.1", Label: "document to approve", Role: domain.RoleProductManager, Agent: "product-manager"}
)

// recordingActor is an Actor that holds what it was asked to do.
type recordingActor struct {
	mu      sync.Mutex
	waiting []readmodel.OperatorDecision
	decided []string
	asked   []string
}

func (a *recordingActor) Find(_ context.Context, kind readmodel.DecisionKind, agent, id string) (readmodel.OperatorDecision, error) {
	for _, decision := range a.waiting {
		if decision.Kind == kind && decision.Agent == agent && decision.ID == id {
			return decision, nil
		}
	}
	return readmodel.OperatorDecision{}, ErrNotWaiting
}

func (a *recordingActor) Decide(_ context.Context, decision readmodel.OperatorDecision, approve bool, reason string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	verb := "declined"
	if approve {
		verb = "approved"
	}
	a.decided = append(a.decided, verb+" "+decision.ID+" "+reason)
	return verb + " " + decision.ID, nil
}

func (a *recordingActor) Ask(_ context.Context, decision readmodel.OperatorDecision, question string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, decision.ID+" "+question)
	return "asked the " + decision.AskRole.Title() + " about " + decision.ID, nil
}

func (a *recordingActor) nothingDone(t *testing.T) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.decided)+len(a.asked) != 0 {
		t.Fatalf("an act was carried out: decided %v, asked %v", a.decided, a.asked)
	}
}

// acting is a server with an actor holding the three decisions, a person who
// owns intent and decides amendments, and a person granted only direct-work.
func acting(t *testing.T) (*world, *recordingActor) {
	t.Helper()
	w := serve(t, stubReader{standing: standingWith("title")})
	actor := &recordingActor{waiting: []readmodel.OperatorDecision{architectDoc, goalsChange, document}}
	w.server.SetActor(actor)
	if err := w.server.BindPerson(ownerToken, Person{Name: "mason", Grants: []config.Grant{config.GrantOwnIntent, config.GrantDirectWork, config.GrantDecideAmendments}}); err != nil {
		t.Fatal(err)
	}
	if err := w.server.BindPerson(readerToken, Person{Name: "colleague", Grants: []config.Grant{config.GrantDirectWork}}); err != nil {
		t.Fatal(err)
	}
	return w, actor
}

// act posts one act as the page does.
func (w *world) act(path, body string, shapes ...func(*http.Request)) (*http.Response, string) {
	w.t.Helper()
	return w.request(http.MethodPost, path, func(request *http.Request) {
		request.Body = ioNopCloser(body)
		request.ContentLength = int64(len(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://127.0.0.1:"+port)
		for _, shape := range shapes {
			shape(request)
		}
	})
}

func approveOf(decision readmodel.OperatorDecision) string {
	return `{"kind":"` + string(decision.Kind) + `","agent":"` + decision.Agent + `","id":"` + decision.ID + `","verdict":"approve"}`
}

func answerOf(t *testing.T, body string) actAnswer {
	t.Helper()
	var answer actAnswer
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("answer %q: %v", body, err)
	}
	return answer
}

func TestAPersonApprovesDeclinesAndAsksFromThePage(t *testing.T) {
	t.Parallel()
	w, actor := acting(t)

	response, body := w.act(decidePath, approveOf(architectDoc), bearer(ownerToken))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("approve: %d %s", response.StatusCode, body)
	}
	if answer := answerOf(t, body); !answer.Decided || answer.Person != "mason" || answer.Done != "approved "+architectDoc.ID {
		t.Fatalf("approve answered %+v", answer)
	}

	decline := `{"kind":"document","agent":"product-manager","id":"document-41.1","verdict":"decline","reason":"not yet"}`
	if response, body := w.act(decidePath, decline, bearer(ownerToken)); response.StatusCode != http.StatusOK {
		t.Fatalf("decline: %d %s", response.StatusCode, body)
	}
	// A decline's reason is optional here; the verb carrying it out decides
	// whether the record needs one.
	bare := `{"kind":"amendment","agent":"","id":"` + goalsChange.ID + `","verdict":"decline"}`
	if response, body := w.act(decidePath, bare, bearer(ownerToken)); response.StatusCode != http.StatusOK {
		t.Fatalf("decline with no reason: %d %s", response.StatusCode, body)
	}

	ask := `{"kind":"amendment","agent":"","id":"` + architectDoc.ID + `","question":"which section?"}`
	response, body = w.act(askPath, ask, bearer(ownerToken))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ask: %d %s", response.StatusCode, body)
	}
	if answer := answerOf(t, body); answer.Decided || !strings.Contains(answer.Done, "asked the developer") {
		t.Fatalf("ask answered %+v", answer)
	}

	actor.mu.Lock()
	defer actor.mu.Unlock()
	want := []string{"approved " + architectDoc.ID + " ", "declined document-41.1 not yet", "declined " + goalsChange.ID + " "}
	if strings.Join(actor.decided, "|") != strings.Join(want, "|") {
		t.Fatalf("decided %q, want %q", actor.decided, want)
	}
	if len(actor.asked) != 1 || actor.asked[0] != architectDoc.ID+" which section?" {
		t.Fatalf("asked %q", actor.asked)
	}
}

// The shared token reads everything and acts on nothing, and a request with
// no token, or a token nobody bound, is refused before anything is read.
func TestTheTokenAloneNeverAuthorizesAnAct(t *testing.T) {
	t.Parallel()
	w, actor := acting(t)
	cases := []struct {
		name   string
		shape  func(*http.Request)
		status int
		says   string
	}{
		{"no token", nil, http.StatusUnauthorized, "requires the token"},
		{"a token nobody bound", bearer(strings.Repeat("c3", tokenBytes)), http.StatusUnauthorized, "requires the token"},
		{"the shared token", bearer(w.server.Token()), http.StatusForbidden, "bound to nobody"},
	}
	for _, each := range cases {
		shapes := []func(*http.Request){}
		if each.shape != nil {
			shapes = append(shapes, each.shape)
		}
		response, body := w.act(decidePath, approveOf(architectDoc), shapes...)
		if response.StatusCode != each.status || !strings.Contains(body, each.says) {
			t.Fatalf("%s: %d %s", each.name, response.StatusCode, body)
		}
		if response, body := w.act(askPath, `{"kind":"amendment","agent":"","id":"`+architectDoc.ID+`","question":"why?"}`, shapes...); response.StatusCode != each.status {
			t.Fatalf("%s asking: %d %s", each.name, response.StatusCode, body)
		}
	}
	actor.nothingDone(t)

	// What the session is, said to the page before anything is pressed.
	_, body := w.get(sessionPath, bearer(w.server.Token()))
	var shared sessionAnswer
	if err := json.Unmarshal([]byte(body), &shared); err != nil || shared.Acts || shared.Person != nil || !strings.Contains(shared.Says, "bound to nobody") {
		t.Fatalf("the shared token's session: %s", body)
	}
	_, body = w.get(sessionPath, bearer(ownerToken))
	var own sessionAnswer
	if err := json.Unmarshal([]byte(body), &own); err != nil || !own.Acts || own.Person == nil || own.Person.Name != "mason" {
		t.Fatalf("a person's session: %s", body)
	}
	// A person's own token reads what the shared one does.
	if response, _ := w.get("/api/standing", bearer(ownerToken)); response.StatusCode != http.StatusOK {
		t.Fatalf("a person's token reading the standing: %d", response.StatusCode)
	}
}

// A person acts only where they hold the grant the decision needs: own-intent
// for a document, and decide-amendments for a proposed change, with own-intent
// beside it for a change to a document the Lead Product Manager owns.
func TestAPersonWithoutTheGrantIsRefusedWithIt(t *testing.T) {
	t.Parallel()
	w, actor := acting(t)
	for _, decision := range []readmodel.OperatorDecision{architectDoc, document} {
		response, body := w.act(decidePath, approveOf(decision), bearer(readerToken))
		if response.StatusCode != http.StatusForbidden || !strings.Contains(body, "colleague does not hold") {
			t.Fatalf("%s: %d %s", decision.ID, response.StatusCode, body)
		}
	}
	actor.nothingDone(t)

	partial := serve(t, stubReader{standing: standingWith("title")})
	partial.server.SetActor(actor)
	if err := partial.server.BindPerson(ownerToken, Person{Name: "reviewer", Grants: []config.Grant{config.GrantDecideAmendments}}); err != nil {
		t.Fatal(err)
	}
	response, body := partial.act(decidePath, approveOf(goalsChange), bearer(ownerToken))
	if response.StatusCode != http.StatusForbidden || !strings.Contains(body, `own-intent`) {
		t.Fatalf("a change to the goals without own-intent: %d %s", response.StatusCode, body)
	}
	if response, body := partial.act(decidePath, approveOf(architectDoc), bearer(ownerToken)); response.StatusCode != http.StatusOK {
		t.Fatalf("a change to a design with decide-amendments: %d %s", response.StatusCode, body)
	}
}

// Anything but the three acts is refused with the reason, and nothing about
// the request is reflected back.
func TestEveryOtherActIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	w, actor := acting(t)
	cases := []struct {
		name   string
		path   string
		body   string
		shape  func(*http.Request)
		status int
		says   string
	}{
		{"a decision no longer waiting", decidePath, `{"kind":"amendment","agent":"","id":"amendment-00000000000000000000000000000000","verdict":"approve"}`, nil, http.StatusConflict, "no longer waiting"},
		{"a kind the page does not decide", decidePath, `{"kind":"directive","agent":"","id":"directive-4f2c","verdict":"approve"}`, nil, http.StatusBadRequest, "is not something the dashboard decides"},
		{"a verdict that is neither", decidePath, `{"kind":"amendment","agent":"","id":"` + architectDoc.ID + `","verdict":"later"}`, nil, http.StatusBadRequest, "is not one the dashboard takes"},
		{"an ask that decides", askPath, `{"kind":"amendment","agent":"","id":"` + architectDoc.ID + `","question":"why?","verdict":"approve"}`, nil, http.StatusBadRequest, "decides nothing"},
		{"an ask with no question", askPath, `{"kind":"amendment","agent":"","id":"` + architectDoc.ID + `"}`, nil, http.StatusBadRequest, "carries the question"},
		{"a field the act does not carry", decidePath, `{"kind":"amendment","agent":"","id":"` + architectDoc.ID + `","verdict":"approve","by":"operator"}`, nil, http.StatusBadRequest, "could not be read"},
		{"a document named without its conversation", decidePath, `{"kind":"document","agent":"","id":"document-41.1","verdict":"approve"}`, nil, http.StatusBadRequest, "names the agent"},
		{"a form post", decidePath, approveOf(architectDoc), func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, http.StatusUnsupportedMediaType, "application/json"},
		{"another path", "/api/acts/release", approveOf(architectDoc), nil, http.StatusMethodNotAllowed, "takes nothing but approve and decline"},
		{"a post to a reading", "/api/standing", approveOf(architectDoc), nil, http.StatusMethodNotAllowed, "takes nothing but approve and decline"},
		{"a foreign origin", decidePath, approveOf(architectDoc), withOrigin("http://evil.example"), http.StatusForbidden, "refuses requests from any other origin"},
		{"a foreign host", decidePath, approveOf(architectDoc), withHost("evil.example:" + port), http.StatusForbidden, "answers only to the address"},
	}
	for _, each := range cases {
		shapes := []func(*http.Request){bearer(ownerToken)}
		if each.shape != nil {
			shapes = append(shapes, each.shape)
		}
		response, body := w.act(each.path, each.body, shapes...)
		if response.StatusCode != each.status || !strings.Contains(body, each.says) {
			t.Fatalf("%s: %d %s", each.name, response.StatusCode, body)
		}
		if response.Header.Get("Content-Security-Policy") != policy {
			t.Fatalf("%s: the refusal carries no policy", each.name)
		}
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		response, _ := w.request(method, decidePath, bearer(ownerToken))
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s: %d", method, decidePath, response.StatusCode)
		}
	}
	// An id carrying markup is refused without the markup coming back.
	response, body := w.act(decidePath, `{"kind":"amendment","agent":"","id":"`+strings.ReplaceAll(injected, `"`, `\"`)+`","verdict":"approve"}`, bearer(ownerToken))
	if response.StatusCode != http.StatusConflict || strings.Contains(body, "<script>") {
		t.Fatalf("an id carrying markup: %d %s", response.StatusCode, body)
	}
	actor.nothingDone(t)
}

// A dashboard handed nothing to carry out an act reads only, and says so.
func TestADashboardWithNoActorReadsOnly(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	if err := w.server.BindPerson(ownerToken, Person{Name: "mason", Grants: []config.Grant{config.GrantOwnIntent, config.GrantDecideAmendments}}); err != nil {
		t.Fatal(err)
	}
	response, body := w.act(decidePath, approveOf(architectDoc), bearer(ownerToken))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "reads only") {
		t.Fatalf("%d %s", response.StatusCode, body)
	}
}

// A token is bound to a person only where it can authorize anything: long
// enough, not the shared token, and nobody else's.
func TestAPersonsTokenIsRefusedWhereItWouldResolveToTheWrongPerson(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	if err := w.server.BindPerson("short", Person{Name: "mason"}); err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("a short token: %v", err)
	}
	if err := w.server.BindPerson(w.server.Token(), Person{Name: "mason"}); err == nil || !strings.Contains(err.Error(), "shared token") {
		t.Fatalf("the shared token: %v", err)
	}
	if err := w.server.BindPerson(ownerToken, Person{Name: "mason"}); err != nil {
		t.Fatal(err)
	}
	if err := w.server.BindPerson(ownerToken, Person{Name: "colleague"}); err == nil || !strings.Contains(err.Error(), "resolves to neither") {
		t.Fatalf("a token two people bound: %v", err)
	}
	// Neither of the two resolves now: the token reads nothing at all.
	if response, _ := w.get(sessionPath, bearer(ownerToken)); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a token two people bound still reads: %d", response.StatusCode)
	}
}

func ioNopCloser(body string) *nopCloser { return &nopCloser{Reader: strings.NewReader(body)} }

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }
