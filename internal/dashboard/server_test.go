package dashboard

// These tests are the security evidence the dashboard hands its reviewer. Each
// convention the design established is driven from the outside of the handler:
// a request with the wrong provenance or no credential is refused with nothing
// of the read model on it, every response carries the policy, and a value that
// reaches HTML reaches it as text.
//
// They drive the handler in process rather than over a socket, as every other
// HTTP test in the repository does, because the sandbox a check runs in does
// not grant a listener. The one test that binds skips where it is refused.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// injected is the text every test plants where an unescaped value would run.
const injected = `<script>alert("owned")</script>`

// port is the port the handler is told it is bound on.
const port = "45123"

// stubReader is a read model that answers with what the test put in it.
type stubReader struct {
	standing   readmodel.Standing
	throughput readmodel.Throughput
	spend      readmodel.Spend
	delivery   readmodel.Delivery
	// items is what WorkItem answers by id; an id not in it is one the tracker
	// holds nothing under.
	items map[string]readmodel.WorkItem
	// reports is what ProgramManagerReport answers by name; a name not in it is
	// one the read model knows no instance under.
	reports map[string]readmodel.ProgramManagerReport
	backlog readmodel.Backlog
	failure error
}

func (r stubReader) Standing(context.Context) (readmodel.Standing, error) {
	return r.standing, r.failure
}

func (r stubReader) Throughput(context.Context) (readmodel.Throughput, error) {
	return r.throughput, r.failure
}

func (r stubReader) Spend(context.Context) (readmodel.Spend, error) {
	return r.spend, r.failure
}

func (r stubReader) Delivery(context.Context) (readmodel.Delivery, error) {
	return r.delivery, r.failure
}

func (r stubReader) WorkItem(_ context.Context, id string) (readmodel.WorkItem, error) {
	if r.failure != nil {
		return readmodel.WorkItem{}, r.failure
	}
	item, found := r.items[id]
	if !found {
		return readmodel.WorkItem{}, fmt.Errorf("%w: bd show failed: issue not found: %s", readmodel.ErrNoSuchWorkItem, id)
	}
	return item, nil
}

func (r stubReader) ProgramManagerReport(_ context.Context, agent string) (readmodel.ProgramManagerReport, error) {
	if r.failure != nil {
		return readmodel.ProgramManagerReport{}, r.failure
	}
	answer, found := r.reports[agent]
	if !found {
		return readmodel.ProgramManagerReport{}, readmodel.ErrNoSuchProgramManager
	}
	return answer, nil
}

func (r stubReader) Backlog(context.Context) (readmodel.Backlog, error) {
	return r.backlog, r.failure
}

// world is one server, bound in name only, and the handler that answers for it.
type world struct {
	t       *testing.T
	server  *Server
	handler http.Handler
}

func serve(t *testing.T, reader Reader) *world {
	t.Helper()
	server, err := New("yoyodyne", reader)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server.bound(45123)
	return &world{t: t, server: server, handler: server.Handler()}
}

// request makes one request with the bound Host, shaped as the test wants, and
// returns the response with its body read.
func (w *world) request(method, path string, shape func(*http.Request)) (*http.Response, string) {
	w.t.Helper()
	request := httptest.NewRequest(method, "http://127.0.0.1:"+port+path, nil)
	if shape != nil {
		shape(request)
	}
	recorder := httptest.NewRecorder()
	w.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	read, err := io.ReadAll(response.Body)
	if err != nil {
		w.t.Fatalf("read %s: %v", path, err)
	}
	return response, string(read)
}

func (w *world) get(path string, shape func(*http.Request)) (*http.Response, string) {
	w.t.Helper()
	return w.request(http.MethodGet, path, shape)
}

func bearer(token string) func(*http.Request) {
	return func(request *http.Request) { request.Header.Set("Authorization", "Bearer "+token) }
}

func withHost(host string) func(*http.Request) {
	return func(request *http.Request) { request.Host = host }
}

func withOrigin(origin string) func(*http.Request) {
	return func(request *http.Request) { request.Header.Set("Origin", origin) }
}

func all(shapes ...func(*http.Request)) func(*http.Request) {
	return func(request *http.Request) {
		for _, shape := range shapes {
			shape(request)
		}
	}
}

// standingWith is a read model carrying the given text in every place a value
// a person wrote can reach: a title, a refusal, and an attention entry.
func standingWith(text string) readmodel.Standing {
	return readmodel.Standing{
		ObservedAt:   time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC),
		Running:      []readmodel.RunningRun{},
		Working:      []readmodel.WorkingTurn{},
		NotStartable: []readmodel.Refused{{WorkItemID: "yoyodyne-ifd.1", Title: text, Reason: text}},
		NeedsHuman: []readmodel.Attention{{
			Kind: readmodel.AttentionDirective, ID: "directive-1", Mover: ownership.MoverOperator,
			Directive: &directive.Directive{ID: "directive-1", Text: text, Unresolved: text},
		}},
	}
}

// The verb's whole contract from the outside: a token is generated, the read
// model is served as JSON to it, and the page shell is served with its states
// and nothing of the read model in it.
func TestServesTheReadModelAsJSONToTheTokenAndTheShellAsItsStates(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("ordinary title")})

	if len(w.server.Token()) != tokenBytes*2 {
		t.Fatalf("token %q is not %d hex characters", w.server.Token(), tokenBytes*2)
	}

	response, body := w.get("/api/standing", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("JSON with the token: %d %s", response.StatusCode, body)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("JSON content type %q", response.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, `"observed_at":"2026-09-18T12:00:00Z"`) || !strings.Contains(body, "ordinary title") {
		t.Fatalf("JSON does not carry the standing: %s", body)
	}

	response, body = w.get("/", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("shell: %d %s", response.StatusCode, body)
	}
	for _, state := range []string{`<main id="page" data-state="signin">`, "state-loading", "state-error", "state-ready", `/assets/dashboard.js`} {
		if !strings.Contains(body, state) {
			t.Fatalf("shell lacks %q: %s", state, body)
		}
	}
	// The shell is a shell: nothing of the read model is in it. It is served to
	// a browser that has no token yet, so this is what makes that safe.
	if strings.Contains(body, "ordinary title") || strings.Contains(body, "2026-09-18") {
		t.Fatalf("the shell carries read-model text: %s", body)
	}
	for _, asset := range []string{"/assets/dashboard.js", "/assets/dashboard.css"} {
		response, body := w.get(asset, nil)
		if response.StatusCode != http.StatusOK || strings.Contains(body, "ordinary title") {
			t.Fatalf("%s: %d %s", asset, response.StatusCode, body)
		}
	}
	if response, _ := w.get("/assets/shell.html", nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("the template source is served raw: %d", response.StatusCode)
	}
}

// Listen binds loopback and reports a URL that carries no token. The bind is
// what a sandbox refuses, so this skips rather than fails where it is refused.
func TestListenBindsLoopbackWithoutTheTokenInTheURL(t *testing.T) {
	t.Parallel()
	server, err := New("yoyodyne", stubReader{})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := server.Listen(0)
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("this environment grants no listener: %v", err)
		}
		t.Fatalf("Listen: %v", err)
	}
	defer server.listener.Close()
	parsed, err := url.Parse(bound)
	if err != nil {
		t.Fatalf("parse %q: %v", bound, err)
	}
	if parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.Port() == "0" || strings.Contains(bound, server.Token()) {
		t.Fatalf("URL %q is not loopback on a real port without the token", bound)
	}
	if !server.hosts["127.0.0.1:"+parsed.Port()] || !server.origins["http://localhost:"+parsed.Port()] {
		t.Fatalf("the bound names do not follow the port: %v %v", server.hosts, server.origins)
	}
}

// A request for the read model with no token is refused, and the refusal
// carries no byte of the read model. A wrong token is a missing one, and so is
// one anywhere but the Authorization header.
func TestRefusesAMissingToken(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("secret title")})

	response, body := w.get("/api/standing", nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JSON without a token: %d %s", response.StatusCode, body)
	}
	if !strings.Contains(body, `"error"`) || strings.Contains(body, "secret title") {
		t.Fatalf("JSON refusal is not a bare refusal: %s", body)
	}
	// Same length as the real one, so the refusal is not a length check.
	wrong := strings.Repeat("0", len(w.server.Token()))
	if response, _ := w.get("/api/standing", bearer(wrong)); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JSON with a wrong token: %d", response.StatusCode)
	}
	if response, _ := w.get("/api/standing?token="+w.server.Token(), nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JSON with the token in the URL: %d", response.StatusCode)
	}
	if response, _ := w.get("/api/standing", func(r *http.Request) { r.Header.Set("Authorization", w.server.Token()) }); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("JSON with the token in the header but not as a bearer: %d", response.StatusCode)
	}
}

// A server handed a token — one read from the keychain or the file the
// configuration names — requires exactly that token, from the bearer header
// alone, and refuses everything a generated one refuses. A server handed
// nothing is not made: it would be a dashboard requiring no credential at all.
func TestASuppliedTokenIsRequiredFromTheBearerHeaderAlone(t *testing.T) {
	t.Parallel()
	server, err := NewWithToken("yoyodyne", stubReader{standing: standingWith("secret title")}, "stored-token\n")
	if err != nil {
		t.Fatalf("NewWithToken: %v", err)
	}
	server.bound(45123)
	w := &world{t: t, server: server, handler: server.Handler()}
	if w.server.Token() != "stored-token" {
		t.Fatalf("Token() = %q, want the supplied token without the newline a store leaves on it", w.server.Token())
	}

	response, body := w.get("/api/standing", bearer("stored-token"))
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "secret title") {
		t.Fatalf("the supplied token as a bearer: %d %s", response.StatusCode, body)
	}
	if response, _ := w.get("/api/standing", nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", response.StatusCode)
	}
	if response, _ := w.get("/api/standing", bearer(strings.Repeat("0", len("stored-token")))); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong token of the same length: %d", response.StatusCode)
	}
	if response, _ := w.get("/api/standing?token=stored-token", nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the token in the URL: %d", response.StatusCode)
	}
	if response, _ := w.get("/api/standing", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "yoyo-dashboard-token", Value: "stored-token"})
	}); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the token in a cookie: %d", response.StatusCode)
	}

	for _, empty := range []string{"", "  \n"} {
		if _, err := NewWithToken("yoyodyne", stubReader{}, empty); err == nil {
			t.Fatalf("NewWithToken(%q) made a server requiring nothing", empty)
		}
	}
	if _, err := NewWithToken("yoyodyne", nil, "stored-token"); err == nil {
		t.Fatal("NewWithToken with no read model made a server")
	}
}

// The token is never a cookie, in either direction: no response sets one, and
// a cookie carrying the token is no credential. A cookie on 127.0.0.1 is sent
// to every port of 127.0.0.1, so a cookie would hand the credential to every
// other loopback service the operator's browser visits, and two dashboards
// would overwrite each other's; session storage in the page is scoped to the
// port, which is why the credential lives there and in the header only.
func TestTheTokenIsNeverACookie(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("secret title")})

	for _, path := range []string{"/", "/api/standing", "/assets/dashboard.js", "/assets/dashboard.css", "/nothing"} {
		response, _ := w.get(path, bearer(w.server.Token()))
		if len(response.Cookies()) != 0 || response.Header.Get("Set-Cookie") != "" {
			t.Fatalf("%s set a cookie: %v", path, response.Header)
		}
	}
	for _, name := range []string{"yoyo_dashboard", "token", "yoyo-dashboard-token", "session"} {
		response, body := w.get("/api/standing", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: name, Value: w.server.Token()})
		})
		if response.StatusCode != http.StatusUnauthorized || strings.Contains(body, "secret title") {
			t.Fatalf("a cookie %q carrying the token was accepted: %d %s", name, response.StatusCode, body)
		}
	}
	// The page's own script keeps it in session storage and nowhere else.
	_, script := w.get("/assets/dashboard.js", nil)
	if !strings.Contains(script, "sessionStorage") || strings.Contains(script, "document.cookie") || strings.Contains(script, "localStorage") {
		t.Fatalf("the script does not keep the token in session storage alone:\n%s", script)
	}
	if !strings.Contains(script, `Authorization: "Bearer " + current`) {
		t.Fatalf("the script does not present the token as a bearer:\n%s", script)
	}
}

// A Host that is not the bound address is refused before anything else is
// looked at, token or no token.
func TestRefusesAForeignHost(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("secret title")})

	for _, host := range []string{"dashboard.example.com", "127.0.0.1:1", "127.0.0.1", "evil.test:" + port, ""} {
		for _, path := range []string{"/", "/api/standing", "/assets/dashboard.css"} {
			response, body := w.get(path, all(bearer(w.server.Token()), withHost(host)))
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("Host %q on %s: %d %s", host, path, response.StatusCode, body)
			}
			if strings.Contains(body, "secret title") || strings.Contains(body, "<main") || (host != "" && strings.Contains(body, host)) {
				t.Fatalf("Host %q on %s: refusal carries the read model, the page, or the host: %s", host, path, body)
			}
		}
	}
	// The two names for the bound address are both the bound address.
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port} {
		response, body := w.get("/api/standing", all(bearer(w.server.Token()), withHost(host)))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("Host %q: %d %s", host, response.StatusCode, body)
		}
	}
}

// An Origin that is not this origin is a page elsewhere scripting requests at
// this port, and is refused whatever credential rides along with it.
func TestRefusesAForeignOrigin(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("secret title")})

	for _, origin := range []string{"http://evil.test", "https://127.0.0.1:" + port, "http://127.0.0.1:1", "null"} {
		for _, path := range []string{"/", "/api/standing"} {
			response, body := w.get(path, all(bearer(w.server.Token()), withOrigin(origin)))
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("Origin %q on %s: %d %s", origin, path, response.StatusCode, body)
			}
			if strings.Contains(body, "secret title") || strings.Contains(body, "<main") || strings.Contains(body, origin) {
				t.Fatalf("Origin %q on %s: refusal carries the read model, the page, or the origin: %s", origin, path, body)
			}
		}
	}
	for _, origin := range []string{"http://127.0.0.1:" + port, "http://localhost:" + port} {
		response, body := w.get("/api/standing", all(bearer(w.server.Token()), withOrigin(origin)))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("own origin %q: %d %s", origin, response.StatusCode, body)
		}
	}
}

// Every response carries the policy, and the policy allows nothing from
// anywhere but this origin, no inline script, and no framing — refusals
// included, because a refusal is a page a browser renders too.
func TestEveryResponseCarriesTheContentSecurityPolicy(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})

	for _, probe := range []struct {
		path  string
		shape func(*http.Request)
	}{
		{"/", nil},
		{"/api/standing", bearer(w.server.Token())},
		{"/api/standing", nil},
		{"/assets/dashboard.css", nil},
		{"/assets/dashboard.js", nil},
		{"/nothing-here", nil},
		{"/", withHost("evil.test")},
		{"/", withOrigin("http://evil.test")},
	} {
		response, _ := w.get(probe.path, probe.shape)
		csp := response.Header.Get("Content-Security-Policy")
		for _, directive := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
			if !strings.Contains(csp, directive) {
				t.Fatalf("%s (%d): policy %q lacks %q", probe.path, response.StatusCode, csp, directive)
			}
		}
		for _, forbidden := range []string{"unsafe-inline", "unsafe-eval", "https:", "http:", "cdn", "*"} {
			if strings.Contains(csp, forbidden) {
				t.Fatalf("%s: policy %q allows %q", probe.path, csp, forbidden)
			}
		}
		if response.Header.Get("X-Content-Type-Options") != "nosniff" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s: headers %v", probe.path, response.Header)
		}
	}
}

// The shell carries no inline script and no inline style, and loads nothing
// from anywhere else, because the policy would refuse them and a page that
// depended on any of them would be blank.
func TestTheShellNeedsNothingThePolicyRefuses(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, body := w.get("/", nil)
	for _, forbidden := range []string{"<script>", "<style", " style=", "onload=", "onsubmit=", "https://", "http://"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the page depends on %q, which the policy refuses:\n%s", forbidden, body)
		}
	}
	// The script sets no style either: every look is a class the stylesheet
	// owns, so a state the script moves a panel into is one the policy allows.
	_, script := w.get("/assets/dashboard.js", nil)
	for _, forbidden := range []string{".style.", ".style=", `"style"`, "cssText", "insertRule"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("the script sets a style through %q, which the policy refuses:\n%s", forbidden, script)
		}
	}
	_, stylesheet := w.get("/assets/dashboard.css", nil)
	for _, forbidden := range []string{"url(", "@import", "https://", "http://"} {
		if strings.Contains(stylesheet, forbidden) {
			t.Fatalf("the stylesheet loads %q, which the policy refuses:\n%s", forbidden, stylesheet)
		}
	}
}

// A value that reaches HTML reaches it as text. The product id is repository
// text and the one value the server writes into the page; everything the read
// model says reaches the page as JSON, where a tag is escaped as well, and the
// page's script writes it as text.
func TestEscapesEveryValueReachingHTML(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith(injected)})
	w.server.Product = injected

	response, body := w.get("/", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("shell: %d", response.StatusCode)
	}
	if strings.Contains(body, injected) || strings.Count(body, "&lt;script&gt;alert(&#34;owned&#34;)&lt;/script&gt;") != 2 {
		t.Fatalf("the product id reached the shell unescaped, or not in both places:\n%s", body)
	}

	_, body = w.get("/api/standing", bearer(w.server.Token()))
	if strings.Contains(body, "<script>") || !strings.Contains(body, `\`+`u003cscript\`+`u003e`) {
		t.Fatalf("the JSON carries a raw tag:\n%s", body)
	}

	// The failure a refusal carries is JSON, never HTML, so a store's message
	// carrying a tag reaches the page as text too.
	broken := serve(t, stubReader{failure: errors.New("open run store: " + injected)})
	response, body = broken.get("/api/standing", bearer(broken.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("unreadable state: %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if strings.Contains(body, "<script>") {
		t.Fatalf("the failure reached the refusal as markup:\n%s", body)
	}

	// The page's script writes every value as text, never as markup.
	_, script := w.get("/assets/dashboard.js", nil)
	for _, forbidden := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval("} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("the script writes markup through %q:\n%s", forbidden, script)
		}
	}
}

// The page counts each of the four lines from the list the model carries and
// replaces the count with "could not be read" where the model says that line
// could not be — never a zero assembled from nothing, which is the rule every
// surface of the read model holds. The script is static, so this holds it to
// naming each line's problem field beside its list.
func TestThePageSaysAnUnreadableLineInPlaceOfItsCount(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, script := w.get("/assets/dashboard.js", nil)
	for _, line := range []struct{ list, problem string }{
		{"running", "running_problem"},
		{"working", "working_problem"},
		{"not_startable", "not_startable_problem"},
		{"needs_human", "needs_human_problem"},
	} {
		if !strings.Contains(script, `list: "`+line.list+`", problem: "`+line.problem+`"`) {
			t.Fatalf("the script does not pair %q with %q:\n%s", line.list, line.problem, script)
		}
	}
	if !strings.Contains(script, "could not be read") {
		t.Fatalf("the script never says a line could not be read:\n%s", script)
	}
}

// Durable state that cannot be read is a refusal of the read model, carrying
// the reason and nothing else — never a partial answer.
func TestRefusesUnreadableState(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{failure: errors.New("the state root could not be resolved")})

	response, body := w.get("/api/standing", bearer(w.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("JSON over unreadable state: %d %s", response.StatusCode, body)
	}
	if !strings.Contains(body, `"error":"the state root could not be resolved"`) || strings.Contains(body, "observed_at") {
		t.Fatalf("JSON refusal is not a bare refusal: %s", body)
	}
	// Without the token the refusal is the token's, and says nothing about the
	// state: what stopped the state being read is for whoever holds the token.
	response, body = w.get("/api/standing", nil)
	if response.StatusCode != http.StatusUnauthorized || strings.Contains(body, "state root") {
		t.Fatalf("unreadable state said to no token: %d %s", response.StatusCode, body)
	}
}

// One work item is served whole as JSON to the token and to nobody else, at
// /api/items/<id>. An id the tracker holds nothing under is refused as not
// found, in fixed words that do not name the id back; an id that is not the
// tracker's shape is refused before anything is asked, as a path nothing is
// served at; and an item that could not be read is refused as unavailable with
// the reason. Every value in the item reaches the page as JSON with its tags
// escaped, the notes and the description first among them.
func TestServesOneWorkItemToTheTokenAlone(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title"), items: map[string]readmodel.WorkItem{
		"yoyodyne-ifd.1": {ID: "yoyodyne-ifd.1", Title: "a " + injected, Labels: []string{}, Description: injected, Notes: injected, Run: &readmodel.ItemRun{RunID: "run-1", Failure: injected}},
	}})

	response, body := w.get("/api/items/yoyodyne-ifd.1", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("item with the token: %d %s", response.StatusCode, body)
	}
	for _, expected := range []string{`"id":"yoyodyne-ifd.1"`, `"labels":[]`, `"description":"`, `"notes":"`, `"failure":"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("the item lacks %s: %s", expected, body)
		}
	}
	// The tag is escaped in every one of the four fields it was planted in, and
	// nowhere does it reach the body raw.
	if strings.Count(body, "u003cscript") != 4 || strings.Contains(body, "<script>") {
		t.Fatalf("the item carries a raw tag, or fewer escaped ones than were planted: %s", body)
	}

	if response, body := w.get("/api/items/yoyodyne-ifd.1", nil); response.StatusCode != http.StatusUnauthorized || strings.Contains(body, "yoyodyne-ifd.1") {
		t.Fatalf("item without the token: %d %s", response.StatusCode, body)
	}
	if response, _ := w.get("/api/items/yoyodyne-ifd.1", all(bearer(w.server.Token()), withHost("evil.test"))); response.StatusCode != http.StatusForbidden {
		t.Fatalf("item to a foreign host: %d", response.StatusCode)
	}
	if response, _ := w.request(http.MethodPost, "/api/items/yoyodyne-ifd.1", bearer(w.server.Token())); response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("item POST: %d", response.StatusCode)
	}

	response, body = w.get("/api/items/yoyodyne-ifd.9", bearer(w.server.Token()))
	if response.StatusCode != http.StatusNotFound || !strings.Contains(body, `"error":"no work item is recorded under that id"`) || strings.Contains(body, "yoyodyne-ifd.9") {
		t.Fatalf("a missing item: %d %s", response.StatusCode, body)
	}
	for _, malformed := range []string{"/api/items/", "/api/items/../escape", "/api/items/a%20b", "/api/items/" + url.PathEscape(injected)} {
		response, body := w.get(malformed, bearer(w.server.Token()))
		if response.StatusCode != http.StatusNotFound || strings.Contains(body, "script") || strings.Contains(body, "escape") {
			t.Fatalf("a malformed id at %s: %d %s", malformed, response.StatusCode, body)
		}
	}

	broken := serve(t, stubReader{failure: errors.New("the work item could not be read: bd show failed: " + injected)})
	response, body = broken.get("/api/items/yoyodyne-ifd.1", bearer(broken.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `"error":"the work item could not be read`) || strings.Contains(body, "<script>") {
		t.Fatalf("an unreadable item: %d %s", response.StatusCode, body)
	}
}

// Every work item is served as JSON at /api/backlog to the token and to nobody
// else, refused whole when the state cannot be read, with tracker text escaped
// and an unrecorded origin said as not recorded.
func TestServesTheBacklogToTheTokenAlone(t *testing.T) {
	t.Parallel()
	filed := time.Date(2026, 10, 5, 19, 51, 1, 0, time.UTC)
	w := serve(t, stubReader{standing: standingWith("title"), backlog: readmodel.Backlog{Items: []readmodel.BacklogItem{
		{ID: "yoyodyne-ifd.1", Title: "a " + injected, FiledAt: &filed, Labels: []string{}, DoneBy: readmodel.DoneByDeveloperRun,
			State: readmodel.BacklogReady, Origin: readmodel.BacklogOrigin{FiledBy: readmodel.OriginNotRecorded}},
	}}})

	response, body := w.get("/api/backlog", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("backlog with the token: %d %s", response.StatusCode, body)
	}
	for _, expected := range []string{`"id":"yoyodyne-ifd.1"`, `"filed_at":"2026-10-05T19:51:01Z"`, `"closed_at":null`,
		`"done_by":"developer run"`, `"state":"ready"`, `"filed_by":"not recorded"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("the backlog lacks %s: %s", expected, body)
		}
	}
	if strings.Contains(body, "<script>") || !strings.Contains(body, "u003cscript") {
		t.Fatalf("the backlog carries a raw tag: %s", body)
	}

	if response, body := w.get("/api/backlog", nil); response.StatusCode != http.StatusUnauthorized || strings.Contains(body, "yoyodyne-ifd.1") {
		t.Fatalf("backlog without the token: %d %s", response.StatusCode, body)
	}
	if response, _ := w.get("/api/backlog", all(bearer(w.server.Token()), withHost("evil.test"))); response.StatusCode != http.StatusForbidden {
		t.Fatalf("backlog to a foreign host: %d", response.StatusCode)
	}
	if response, _ := w.request(http.MethodPost, "/api/backlog", bearer(w.server.Token())); response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("backlog POST: %d", response.StatusCode)
	}

	broken := serve(t, stubReader{failure: errors.New("the state root could not be resolved")})
	response, body = broken.get("/api/backlog", bearer(broken.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `"error":"the state root could not be resolved"`) || strings.Contains(body, "items") {
		t.Fatalf("backlog over unreadable state: %d %s", response.StatusCode, body)
	}
}

// The dashboard is read-only at the protocol: every method but GET and HEAD
// is refused, on every path, with or without the token.
func TestRefusesEveryWrite(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		for _, path := range []string{"/", "/api/standing", "/session"} {
			response, _ := w.request(method, path, bearer(w.server.Token()))
			if response.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s: %d", method, path, response.StatusCode)
			}
		}
	}
}

// One program manager instance and its lane report are served whole as JSON to
// the token and to nobody else, at /api/program-managers/<agent>. A name the
// read model knows no instance under is refused as not found, in fixed words
// that do not name it back; a name that is not an agent's shape is refused
// before anything is read; and state that could not be read is refused as
// unavailable with the reason. What the instance wrote reaches the page as JSON
// with its tags escaped.
func TestServesOneProgramManagersReportToTheTokenAlone(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title"), reports: map[string]readmodel.ProgramManagerReport{
		"factory-pgm": {
			Instance: readmodel.ProgramManager{Agent: "factory-pgm", Lane: "reliability", Status: readmodel.ProgramManagerBlocked, Blocked: true,
				Blockers: []readmodel.ProgramManagerBlocker{{What: injected, WaitingOn: readmodel.MoverProductManager, Cites: "report-1", Record: readmodel.CitedReport}},
				Claims:   []readmodel.ProgramManagerClaim{}, RestartRequests: []runstate.RestartRequest{}},
			Report: &readmodel.LaneReportText{Summary: injected, Remaining: []string{injected}, Version: 2, ConversationID: "chat-1", Turn: 3},
		},
	}})

	response, body := w.get("/api/program-managers/factory-pgm", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("report with the token: %d %s", response.StatusCode, body)
	}
	for _, expected := range []string{`"instance":{"agent":"factory-pgm","lane":"reliability","status":"blocked"`, `"report":{"summary":"`, `"remaining":["`, `"version":2`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("the report lacks %s: %s", expected, body)
		}
	}
	if strings.Count(body, "u003cscript") != 3 || strings.Contains(body, "<script>") {
		t.Fatalf("the report carries a raw tag, or fewer escaped ones than were planted: %s", body)
	}

	if response, body := w.get("/api/program-managers/factory-pgm", nil); response.StatusCode != http.StatusUnauthorized || strings.Contains(body, "factory-pgm") {
		t.Fatalf("report without the token: %d %s", response.StatusCode, body)
	}
	if response, _ := w.get("/api/program-managers/factory-pgm", all(bearer(w.server.Token()), withHost("evil.test"))); response.StatusCode != http.StatusForbidden {
		t.Fatalf("report to a foreign host: %d", response.StatusCode)
	}
	if response, _ := w.request(http.MethodPost, "/api/program-managers/factory-pgm", bearer(w.server.Token())); response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("report POST: %d", response.StatusCode)
	}

	response, body = w.get("/api/program-managers/nobody-pgm", bearer(w.server.Token()))
	if response.StatusCode != http.StatusNotFound || !strings.Contains(body, `"error":"no program manager instance is recorded under that name"`) || strings.Contains(body, "nobody-pgm") {
		t.Fatalf("a missing instance: %d %s", response.StatusCode, body)
	}
	for _, malformed := range []string{"/api/program-managers/", "/api/program-managers/../escape", "/api/program-managers/a%20b", "/api/program-managers/" + url.PathEscape(injected)} {
		response, body := w.get(malformed, bearer(w.server.Token()))
		if response.StatusCode != http.StatusNotFound || strings.Contains(body, "script") || strings.Contains(body, "escape") {
			t.Fatalf("a malformed name at %s: %d %s", malformed, response.StatusCode, body)
		}
	}

	broken := serve(t, stubReader{failure: errors.New("the state root could not be resolved")})
	response, body = broken.get("/api/program-managers/factory-pgm", bearer(broken.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `"error":"the state root could not be resolved"`) || strings.Contains(body, "instance") {
		t.Fatalf("an unreadable report: %d %s", response.StatusCode, body)
	}
}
