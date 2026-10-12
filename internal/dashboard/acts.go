package dashboard

// The operator's own decisions, taken from the page.
//
// The dashboard reads, and it takes exactly three acts besides: approve and
// decline one thing waiting on the operator's decision, and ask about one
// before deciding it. What waits is the read model's list
// (readmodel.ReadOperatorDecisions) — a proposed change to a document, a
// document an owning role wrote in its conversation, and work proposed in one
// — and each act is carried out by the Actor the process is handed, which does
// what the command-line verb for the same decision does and records it the
// same way. Nothing here records anything itself, and nothing here can do
// what the command line cannot.
//
// Who acts is a person, never a token. The shared token the dashboard serves
// under reads everything and resolves to nobody, so a session holding it is
// refused every act with the reason. A person who binds a token of their own
// under operators.<name>.dashboard_token is that person while the tab holds
// it, and an act is then allowed only where that person holds the grant the
// decision needs: own-intent for a document or proposed work, and
// decide-amendments for a proposed change, with own-intent beside it where the
// change is to a document the Lead Product Manager owns.
//
// Every rule the reads hold, the acts hold too: the Host and Origin checks, the
// content security policy, the token only in the Authorization header, every
// value written by the page as text, and every refusal carrying its reason and
// nothing of the request. An act must also arrive as JSON, which a page on
// another origin cannot send here without the Origin this server refuses.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// The paths the acts are taken at. Nothing else is taken at all.
const (
	decidePath  = "/api/acts/decide"
	askPath     = "/api/acts/ask"
	sessionPath = "/api/session"
)

// maxActBytes bounds one act's body. A reason or a question is a paragraph,
// and the verbs carrying them out hold them to their own, smaller, bounds.
const maxActBytes = 16 << 10

// minPersonTokenBytes is the shortest token a person may bind. A person's token
// authorizes acts, so it is held to the entropy the generated shared token
// has: thirty-two random bytes written as hex.
const minPersonTokenBytes = 2 * tokenBytes

// ErrNotWaiting is a decision the records no longer hold as waiting: decided
// since the page last read, or never raised.
var ErrNotWaiting = errors.New("nothing is waiting on a decision under that id")

// Person is a human a session's own token resolves to: their name in the
// operators mapping and what they were granted there.
type Person struct {
	Name   string         `json:"name"`
	Grants []config.Grant `json:"grants"`
}

// Holds reports whether the person was granted an authority.
func (p Person) Holds(grant config.Grant) bool {
	return slices.Contains(p.Grants, grant)
}

// Actor carries out the operator's decisions as the command-line verbs do.
type Actor interface {
	// Find reads one waiting decision fresh from the durable records, by its
	// kind, the agent whose conversation holds it (empty for a proposed change),
	// and its id. One no longer waiting is ErrNotWaiting.
	Find(ctx context.Context, kind readmodel.DecisionKind, agent, id string) (readmodel.OperatorDecision, error)
	// Decide approves or declines it, recording the decision exactly as the
	// verb a person types does, and says what came of it.
	Decide(ctx context.Context, decision readmodel.OperatorDecision, approve bool, reason string) (string, error)
	// Ask puts the person's question about it to the conversation of the role
	// that raised it, or the Lead Product Manager's where that role cannot be
	// reached in one, and decides nothing. It says where the question went.
	Ask(ctx context.Context, decision readmodel.OperatorDecision, question string) (string, error)
}

type personToken struct {
	token  string
	person Person
}

// SetActor is what carries out the acts. A server with none refuses every act
// with the reason.
func (s *Server) SetActor(actor Actor) { s.actor = actor }

// BindPerson accepts one person's own token. A token too short to authorize
// anything, the shared token itself, and a token another person already bound
// are refused, and the refused person is left unbound: a token two people hold
// resolves to neither, and the shared token resolving to somebody would make
// every reader of the page that person.
func (s *Server) BindPerson(token string, person Person) error {
	token = strings.TrimSpace(token)
	switch {
	case len(token) < minPersonTokenBytes:
		return fmt.Errorf("the dashboard token bound for %s is %d characters, and a token that authorizes acts is at least %d, as `openssl rand -hex 32` makes", person.Name, len(token), minPersonTokenBytes)
	case subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1:
		return fmt.Errorf("the dashboard token bound for %s is the shared token the dashboard serves under, which reads only; bind a token of the person's own", person.Name)
	}
	for index, bound := range s.people {
		if subtle.ConstantTimeCompare([]byte(token), []byte(bound.token)) == 1 {
			s.people = slices.Delete(s.people, index, index+1)
			return fmt.Errorf("the dashboard tokens bound for %s and %s are the same token, which resolves to neither of them; each person binds a token of their own", bound.person.Name, person.Name)
		}
	}
	s.people = append(s.people, personToken{token: token, person: person})
	return nil
}

// presenter is who presented the request's token: whether it was one this
// server accepts at all, and the person it resolves to where it is a person's.
// The shared token is accepted and resolves to nobody.
func (s *Server) presenter(request *http.Request) (*Person, bool) {
	candidate, found := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !found {
		return nil, false
	}
	candidate = strings.TrimSpace(candidate)
	var resolved *Person
	for _, bound := range s.people {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(bound.token)) == 1 {
			person := bound.person
			resolved = &person
		}
	}
	if resolved != nil {
		return resolved, true
	}
	return nil, subtle.ConstantTimeCompare([]byte(candidate), []byte(s.token)) == 1
}

// actRequest is one act as the page sends it.
type actRequest struct {
	Kind     readmodel.DecisionKind `json:"kind"`
	Agent    string                 `json:"agent"`
	ID       string                 `json:"id"`
	Verdict  string                 `json:"verdict,omitempty"`
	Reason   string                 `json:"reason,omitempty"`
	Question string                 `json:"question,omitempty"`
}

// actAnswer is what an act that was carried out returns.
type actAnswer struct {
	Done     string `json:"done"`
	Decided  bool   `json:"decided"`
	Person   string `json:"person"`
	Decision string `json:"decision"`
}

// sessionAnswer is who the session is, for the page to say before anybody
// presses anything.
type sessionAnswer struct {
	Person *Person `json:"person,omitempty"`
	Says   string  `json:"says"`
	Acts   bool    `json:"acts"`
}

// unboundReason is the refusal every act from a session bound to nobody gets.
const unboundReason = "this session is bound to nobody: the dashboard's shared token reads and acts on nothing. To approve, decline, or ask from here, sign in with your own token, bound to you under operators.<your name>.dashboard_token in the configuration"

// serveSession says who the session is.
func (s *Server) serveSession(writer http.ResponseWriter, request *http.Request, person *Person) {
	answer := sessionAnswer{Says: unboundReason}
	switch {
	case person == nil:
	case s.actor == nil:
		answer.Person = person
		answer.Says = "this session is " + person.Name + ", and this dashboard was started with nothing to carry out an act, so it reads only"
	default:
		answer.Person = person
		answer.Acts = true
		answer.Says = "this session is " + person.Name + ", and an approve, decline, or ask from here is recorded as theirs"
	}
	writeJSON(writer, request, answer)
}

// serveAct takes one act. The order is the reads' order carried on: the
// request is checked before anything is read, the person before the decision,
// and the decision before anything is done.
func (s *Server) serveAct(writer http.ResponseWriter, request *http.Request, person *Person) {
	if person == nil {
		refuse(writer, request, http.StatusForbidden, unboundReason)
		return
	}
	if mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		refuse(writer, request, http.StatusUnsupportedMediaType, "an act is sent as application/json")
		return
	}
	if s.actor == nil {
		refuse(writer, request, http.StatusServiceUnavailable, "this dashboard was started with nothing to carry out an act, so it reads only")
		return
	}
	var act actRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxActBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&act); err != nil {
		refuse(writer, request, http.StatusBadRequest, "the act could not be read: it is one JSON object naming kind, agent, id, and verdict and reason, or question")
		return
	}
	if problem := act.problem(request.URL.Path); problem != "" {
		refuse(writer, request, http.StatusBadRequest, problem)
		return
	}
	decision, err := s.actor.Find(request.Context(), act.Kind, act.Agent, act.ID)
	if errors.Is(err, ErrNotWaiting) {
		refuse(writer, request, http.StatusConflict, "that is no longer waiting on a decision: it was decided since this page last read, or it was never raised. Nothing was done")
		return
	}
	if err != nil {
		refuse(writer, request, http.StatusServiceUnavailable, err.Error())
		return
	}
	if missing := missingGrant(*person, decision); missing != "" {
		refuse(writer, request, http.StatusForbidden, missing)
		return
	}
	if request.URL.Path == askPath {
		done, err := s.actor.Ask(request.Context(), decision, act.Question)
		if err != nil {
			refuse(writer, request, http.StatusConflict, "the question was not asked: "+err.Error())
			return
		}
		writeJSON(writer, request, actAnswer{Done: done, Person: person.Name, Decision: decision.Key()})
		return
	}
	done, err := s.actor.Decide(request.Context(), decision, act.Verdict == "approve", act.Reason)
	if err != nil {
		refuse(writer, request, http.StatusConflict, "nothing was decided: "+err.Error())
		return
	}
	writeJSON(writer, request, actAnswer{Done: done, Decided: true, Person: person.Name, Decision: decision.Key()})
}

// problem says what makes an act unusable before anything is looked up.
func (a actRequest) problem(path string) string {
	if !a.Kind.Valid() {
		return fmt.Sprintf("kind %q is not something the dashboard decides; it decides %s", a.Kind, describeKinds())
	}
	if strings.TrimSpace(a.ID) == "" || len(a.ID) > 128 {
		return "an act names the id of what it decides"
	}
	if a.Kind == readmodel.DecisionAmendment {
		if a.Agent != "" {
			return "a proposed change is decided in no conversation, so an act on one names no agent"
		}
	} else if err := domain.ValidateIdentifier("agent", a.Agent); err != nil {
		return "an act on a document or proposed work names the agent whose conversation holds it"
	}
	if path == askPath {
		if strings.TrimSpace(a.Question) == "" {
			return "an ask carries the question"
		}
		if a.Verdict != "" || a.Reason != "" {
			return "an ask decides nothing, so it carries no verdict and no reason"
		}
		return ""
	}
	if a.Verdict != "approve" && a.Verdict != "decline" {
		return fmt.Sprintf("verdict %q is not one the dashboard takes; it takes \"approve\" and \"decline\"", a.Verdict)
	}
	if a.Question != "" {
		return "a decision carries a reason rather than a question"
	}
	return ""
}

// missingGrant is the refusal for a person who may not decide this, or empty
// where they may. Asking about a decision is held to the same grant as making
// it, because the ask is the first half of making it.
func missingGrant(person Person, decision readmodel.OperatorDecision) string {
	var needed []config.Grant
	switch decision.Kind {
	case readmodel.DecisionAmendment:
		needed = append(needed, config.GrantDecideAmendments)
		if decision.Owner == domain.RoleProductManager {
			needed = append(needed, config.GrantOwnIntent)
		}
	default:
		needed = append(needed, config.GrantOwnIntent)
	}
	for _, grant := range needed {
		if !person.Holds(grant) {
			return fmt.Sprintf("%s does not hold %q, which deciding a %s needs; nothing was done", person.Name, grant, decision.Label)
		}
	}
	return ""
}

func describeKinds() string {
	names := make([]string, 0, len(readmodel.DecisionKinds()))
	for _, kind := range readmodel.DecisionKinds() {
		names = append(names, fmt.Sprintf("%q", kind))
	}
	return strings.Join(names, ", ")
}

// writeJSON is one answer as JSON, escaped for HTML as the readings are.
func writeJSON(writer http.ResponseWriter, request *http.Request, answer any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(true)
	_ = encoder.Encode(answer)
}
