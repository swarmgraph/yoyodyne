// Package dashboard serves the read model over HTTP, to a browser on this
// machine and to nothing else.
//
// It is the repository's first web service, and the observability-and-dashboard
// design establishes the conventions it holds to here, so that the next web
// surface starts from them rather than rediscovering each one:
//
//   - It binds loopback only, and loopback alone is not trusted: every request
//     for the read model carries a bearer token — one the process generated at
//     start and printed once, or one read from the store the configuration
//     names and never printed — in the Authorization header and never in a URL, where
//     it would reach a browser history, a referrer, and every log a proxy
//     keeps. It is never in a cookie either: a cookie on 127.0.0.1 is sent to
//     every other service on every other port of 127.0.0.1, so a cookie would
//     hand the credential to whatever else the operator's browser visits on
//     this machine. The page keeps it in the browser's session storage, which
//     is scoped to this origin, port included.
//   - The Host and Origin headers are validated against the address it bound,
//     and anything else is refused. A page on some other origin that scripts a
//     request at this port is refused on the Origin; a DNS name a browser is
//     pointed at that resolves here is refused on the Host.
//   - Every response carries a content-security policy that allows nothing but
//     this origin's own script and style, so nothing is loaded from a CDN and no
//     inline script runs — including one that reached the page through a value
//     that was not escaped.
//   - Every value that reaches HTML goes through html/template, so a product id
//     renders as text; everything the read model says reaches the page through
//     JSON and is written by the page's script as text.
//   - Every failure fails closed. A missing or wrong token, a foreign Host, a
//     foreign Origin, a polled reading no build has ever produced, and a work
//     item or report that cannot be read each produce a refusal that carries
//     no part of the read model, never a page with a quarter of the answer on
//     it. A polled reading whose later build failed is served whole as the
//     last good one, with the failure beside its age.
//
// What is served without a token is the page shell and its own script and
// style: static text compiled into the binary, with nothing of the read model
// in it, which is what a browser needs before it can present a token at all.
// Everything that reads state is behind the token: the standing, the
// throughput, the spend, one work item at a time at /api/items/<id>, which is
// what the page opens a card from, and one program manager instance's lane
// report at a time at /api/program-managers/<agent>. The page never reads the tracker; the
// card is the read model's projection of the item, served here like the rest.
//
// It is a projection, never an engine: it owns no workflow, conversation,
// provider, or configuration state, and offers no write of any kind. What it
// holds is the token and the latest snapshot of each polled reading, in
// memory, for exactly as long as the process runs. Restarting it changes
// nothing about the harness and loses no history, because the history is in
// the durable records it reads.
package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

//go:embed assets
var assets embed.FS

// shell is the page, parsed once. Every value the template is handed is escaped
// by the package on the way into HTML, which is the whole reason the page is a
// template rather than a string.
var shell = template.Must(template.ParseFS(assets, "assets/shell.html"))

// tokenBytes is the entropy behind one token. Thirty-two bytes is more than any
// guess on a loopback port could ever cover, and it renders as sixty-four hex
// characters, which is short enough to paste.
const tokenBytes = 32

// shutdownGrace bounds how long a stop waits for requests in flight. A reading
// is bounded by the tracker timeout, so anything still open past this is stuck
// rather than working.
const shutdownGrace = 5 * time.Second

// policy is the content-security policy every response carries, refusals
// included. Nothing is allowed from anywhere but this origin, and inline script
// and style are not allowed at all: a script that reaches the page through an
// unescaped value has nowhere to run.
const policy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// Reader is the read model as the server is handed it. It is an interface so
// the security conventions can be driven without a state directory, which is
// the only way a refusal nobody may weaken gets a test that holds it.
type Reader interface {
	// Standing reads the read model. An error is a refusal of the whole answer,
	// never a partial one: what the read model could answer with a source missing
	// it says inside the Standing, line by line.
	Standing(ctx context.Context) (readmodel.Standing, error)
	// Throughput reads what landed over the model's two windows. It is a second
	// reading rather than part of the first because it reads every recorded run
	// rather than the ones in flight. The same rule holds: an error refuses the
	// whole answer, and a source missing is said inside it.
	Throughput(ctx context.Context) (readmodel.Throughput, error)
	// Spend reads what the harness spent over the last twenty-four hours and
	// the last seven local days, and what each of the last thirty days cost. It
	// is a reading of its own because it prices every event log the month
	// holds, which is seconds of work the page asks for once a minute rather
	// than once every ten seconds. The same rule holds.
	Spend(ctx context.Context) (readmodel.Spend, error)
	// WorkItem reads one work item whole — the tracker's fields and the run the
	// harness last made for it — for the card the page opens on an item. It is
	// asked for one item at a time, when a card is opened, because it costs a
	// tracker command. An error refuses the whole answer: one that is
	// readmodel.ErrNoSuchWorkItem is the tracker holding nothing under the id,
	// and any other is the item not being readable.
	WorkItem(ctx context.Context, id string) (readmodel.WorkItem, error)
	// ProgramManagerReport reads one program manager instance — the instance as
	// the standing carries it, and its current lane report whole — for the card
	// the page opens on an instance's report. It is asked for when that card is
	// opened. An error refuses the whole answer: one that is
	// readmodel.ErrNoSuchProgramManager is the read model knowing no instance by
	// the name, and any other is the state not being readable.
	ProgramManagerReport(ctx context.Context, agent string) (readmodel.ProgramManagerReport, error)
}

// Server is one dashboard process: the token it generated or was handed, the
// address it bound, and the read model it projects.
type Server struct {
	// Product is the product id, for the page's title. It is repository-supplied
	// text and is escaped like everything else.
	Product string
	reader  Reader
	token   string

	listener net.Listener
	// standing, throughput, and spend are the three readings the page polls,
	// each built in the background once per interval and served to every
	// request as the latest snapshot with its age.
	standing   *snapshot
	throughput *snapshot
	spend      *snapshot
	// hosts is every Host header that names the bound address, and origins every
	// Origin header that does. Both are fixed at Listen and read on every request.
	hosts   map[string]bool
	origins map[string]bool
}

// New makes a server holding a fresh token for the read model it is handed. The
// token is the process's whole credential, so a source of randomness that fails
// is a server that does not start.
func New(product string, reader Reader) (*Server, error) {
	if reader == nil {
		return nil, errors.New("dashboard: no read model to serve")
	}
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("dashboard: generate token: %w", err)
	}
	return withSnapshots(&Server{Product: product, reader: reader, token: hex.EncodeToString(raw)}), nil
}

// NewWithToken makes a server holding the token it is handed — one read from
// the store the configuration names, which is what lets the token outlive a
// restart. The server holds it exactly as it holds a generated one: in memory,
// accepted from the bearer header alone. An empty token is refused, because a
// server that required nothing would be a server with no credential at all.
func NewWithToken(product string, reader Reader, token string) (*Server, error) {
	if reader == nil {
		return nil, errors.New("dashboard: no read model to serve")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("dashboard: the supplied token is empty")
	}
	return withSnapshots(&Server{Product: product, reader: reader, token: strings.TrimSpace(token)}), nil
}

// withSnapshots gives the server its three background readings, on the
// page's own clocks: the standing every ten seconds, and the throughput and
// the spend every minute.
func withSnapshots(s *Server) *Server {
	s.standing = newSnapshot(standingInterval, func(ctx context.Context) (any, error) { return s.reader.Standing(ctx) })
	s.throughput = newSnapshot(minuteInterval, func(ctx context.Context) (any, error) { return s.reader.Throughput(ctx) })
	s.spend = newSnapshot(minuteInterval, func(ctx context.Context) (any, error) { return s.reader.Spend(ctx) })
	return s
}

// Token is the credential every request for the read model has to present. It
// is for the process that started the server to print once where the token was
// generated, and never where it was supplied; nothing here writes it anywhere.
func (s *Server) Token() string { return s.token }

// Listen binds loopback on the port asked for, or on one the operating system
// chooses when the port is zero, and returns the URL a browser is pointed at.
// It binds 127.0.0.1 by name rather than "localhost", which on some machines
// resolves to an IPv6 address the listener is not on.
func (s *Server) Listen(port int) (string, error) {
	if port < 0 || port > 65535 {
		return "", fmt.Errorf("dashboard: port %d is not a port", port)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return "", fmt.Errorf("dashboard: bind loopback: %w", err)
	}
	s.listener = listener
	s.bound(listener.Addr().(*net.TCPAddr).Port)
	return s.URL(), nil
}

// bound fixes the Host and Origin values that name this server, from the port
// it is on. It is what Listen calls, and what a test calls in place of a
// listener the sandbox it runs in will not grant.
func (s *Server) bound(port int) {
	bound := strconv.Itoa(port)
	s.hosts = map[string]bool{"127.0.0.1:" + bound: true, "localhost:" + bound: true}
	s.origins = map[string]bool{"http://127.0.0.1:" + bound: true, "http://localhost:" + bound: true}
}

// URL is where the server is listening, once it is. It carries no token: the
// token is presented in a header, never in a URL.
func (s *Server) URL() string {
	if s.listener == nil {
		return ""
	}
	return "http://" + s.listener.Addr().String() + "/"
}

// Serve answers requests until the context is cancelled, and then stops,
// waiting shutdownGrace for anything in flight. It returns nil on a stop that
// was asked for and the failure otherwise.
func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		return errors.New("dashboard: Serve before Listen")
	}
	// The readings are built under the server's lifetime, so stopping the
	// server stops their building with it.
	for _, reading := range []*snapshot{s.standing, s.throughput, s.spend} {
		reading.live(ctx)
	}
	server := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// The server's own log is left where it defaults, standard error, and
		// nothing here logs a request: a request line is where a token would
		// otherwise end up.
	}
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(s.listener) }()
	select {
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(grace); err != nil {
			return fmt.Errorf("dashboard: stop: %w", err)
		}
		<-stopped
		return nil
	}
}

// Handler is every route, behind the checks every request passes first.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

// serve is the one entry every request takes. The order is deliberate: the
// headers that make a refusal safe are set before anything can be refused, the
// request's provenance is checked before anything is served, and the credential
// is checked before anything is read.
func (s *Server) serve(writer http.ResponseWriter, request *http.Request) {
	header := writer.Header()
	header.Set("Content-Security-Policy", policy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Cache-Control", "no-store")

	// A Host that is not the bound address is a request that was not addressed
	// here — a name a browser was pointed at that happens to resolve to loopback,
	// or a rebinding attack that relies on exactly that. The refusal reflects
	// nothing back, because the one thing known about the request is that it
	// came from somewhere it should not have.
	if !s.hosts[request.Host] {
		refuse(writer, request, http.StatusForbidden, "this dashboard answers only to the address it was started on")
		return
	}
	// An Origin is sent by a browser making a request from a page, and one from
	// any origin but this one is a page elsewhere scripting requests at this
	// port. A request with no Origin is a navigation or a tool, and the token
	// decides those.
	if origin := request.Header.Get("Origin"); origin != "" && !s.origins[origin] {
		refuse(writer, request, http.StatusForbidden, "this dashboard refuses requests from any other origin")
		return
	}
	// Read-only means read-only at the protocol: there is nothing to post.
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		refuse(writer, request, http.StatusMethodNotAllowed, "this dashboard is read-only")
		return
	}

	switch {
	case request.URL.Path == "/":
		s.servePage(writer)
	case strings.HasPrefix(request.URL.Path, "/assets/"):
		s.serveAsset(writer, request)
	case request.URL.Path == "/api/standing", request.URL.Path == "/api/throughput", request.URL.Path == "/api/spend", strings.HasPrefix(request.URL.Path, "/api/items/"), strings.HasPrefix(request.URL.Path, "/api/program-managers/"):
		// The routes that read state, and so the ones the token guards.
		if !s.presented(request) {
			refuse(writer, request, http.StatusUnauthorized, "this dashboard requires the token it printed when it started, as a bearer token")
			return
		}
		switch {
		case request.URL.Path == "/api/throughput":
			s.serveSnapshot(writer, request, s.throughput)
		case request.URL.Path == "/api/spend":
			s.serveSnapshot(writer, request, s.spend)
		case request.URL.Path == "/api/standing":
			s.serveSnapshot(writer, request, s.standing)
		case strings.HasPrefix(request.URL.Path, "/api/program-managers/"):
			s.serveProgramManager(writer, request)
		default:
			s.serveWorkItem(writer, request)
		}
	default:
		refuse(writer, request, http.StatusNotFound, "nothing is served at that path")
	}
}

// presented says whether the request carries the token in the Authorization
// header, which is the one place it is accepted from: not a query string, and
// not a cookie. The comparison is constant-time, because a comparison that stops
// at the first wrong byte says how many bytes were right.
func (s *Server) presented(request *http.Request) bool {
	candidate, found := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !found {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(candidate)), []byte(s.token)) == 1
}

// servePage is the shell: the page with its states — asking for the token,
// loading, error, ready — and its eight sections, each with an empty, a loading,
// and an error state of its own, and nothing of the read model in any of them.
// It is static text the page's own script then fills from the JSON, so it is
// served to a browser that has no token yet, which is every browser before it
// signs in.
func (s *Server) servePage(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_ = shell.Execute(writer, struct{ Product string }{Product: s.Product})
}

// serveReading is one reading of the read model as JSON, whole or refused. What
// the model could not read it says inside the answer, source by source; what
// stops the model being read at all is a refusal carrying the reason and
// nothing else.
func (s *Server) serveReading(writer http.ResponseWriter, request *http.Request, read func(context.Context) (any, error)) {
	reading, err := read(request.Context())
	if err != nil {
		if errors.Is(err, readmodel.ErrNoSuchWorkItem) {
			// A thing that is not recorded is a different answer from state that
			// could not be read: a page told the first shows it, and a page told
			// the second keeps asking. The reason is fixed words rather than the
			// tracker's, which would name the id back.
			refuse(writer, request, http.StatusNotFound, "no work item is recorded under that id")
			return
		}
		if errors.Is(err, readmodel.ErrNoSuchProgramManager) {
			refuse(writer, request, http.StatusNotFound, "no program manager instance is recorded under that name")
			return
		}
		refuse(writer, request, http.StatusServiceUnavailable, err.Error())
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	encoder := json.NewEncoder(writer)
	// The JSON is read by script and never written into HTML as markup, but a
	// "<" in a title escaped here costs nothing and closes the case where some
	// later reader does.
	encoder.SetEscapeHTML(true)
	_ = encoder.Encode(reading)
}

// serveSnapshot is one of the polled readings as JSON: the latest snapshot,
// with its age under "snapshot". A request never builds one. It waits only
// where no build has ended yet, and is refused with the failure only where
// every build so far has failed; a failure after a reading was taken is said
// beside that reading's age instead, so the page goes on showing it.
func (s *Server) serveSnapshot(writer http.ResponseWriter, request *http.Request, reading *snapshot) {
	encoded, age, err := reading.get(request.Context())
	if err == nil {
		encoded, err = withAge(encoded, age)
	}
	if err != nil {
		refuse(writer, request, http.StatusServiceUnavailable, err.Error())
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	_, _ = writer.Write(encoded)
}

// serveWorkItem is one work item as JSON, whole or refused, for the card the
// page opens on it. The id is the rest of the path, and it is checked against
// the tracker's own shape before anything is asked: an id that is not one is
// refused as a path nothing is served at, reflecting nothing, rather than being
// put on a command line. An id the tracker holds nothing under is refused as
// not found, and an item that could not be read at all as unavailable, each
// with its reason and nothing of the read model beside it.
func (s *Server) serveWorkItem(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/api/items/")
	if !readmodel.ValidWorkItemID(id) {
		refuse(writer, request, http.StatusNotFound, "nothing is served at that path")
		return
	}
	s.serveReading(writer, request, func(ctx context.Context) (any, error) { return s.reader.WorkItem(ctx, id) })
}

// serveProgramManager is one program manager instance's lane report as JSON,
// whole or refused, for the card the page opens on it. The name is the rest of
// the path and is held to an agent's shape before anything is read, and refused
// as a path nothing is served at, reflecting nothing, where it is not one.
func (s *Server) serveProgramManager(writer http.ResponseWriter, request *http.Request) {
	agent := strings.TrimPrefix(request.URL.Path, "/api/program-managers/")
	if !readmodel.ValidProgramManagerName(agent) {
		refuse(writer, request, http.StatusNotFound, "nothing is served at that path")
		return
	}
	s.serveReading(writer, request, func(ctx context.Context) (any, error) { return s.reader.ProgramManagerReport(ctx, agent) })
}

// serveAsset is the page's own script and style, from the binary. They are the
// only script and style the policy allows, and like the shell they are static
// text with nothing of the read model in them.
func (s *Server) serveAsset(writer http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(request.URL.Path, "/")
	content, err := fs.ReadFile(assets, name)
	if err != nil || strings.HasSuffix(name, ".html") {
		refuse(writer, request, http.StatusNotFound, "nothing is served at that path")
		return
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		writer.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	default:
		writer.Header().Set("Content-Type", "application/octet-stream")
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(content)
	}
}

// refuse is every refusal: JSON for a caller that asked for it or is at an API
// path, and plain text otherwise. It reflects nothing from the request.
func refuse(writer http.ResponseWriter, request *http.Request, status int, reason string) {
	if strings.HasPrefix(request.URL.Path, "/api/") || strings.Contains(request.Header.Get("Accept"), "application/json") {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(map[string]string{"error": reason})
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = fmt.Fprintln(writer, reason)
}
