package dashboard

// Readiness: when a dashboard the supervisor starts begins serving.
//
// The design's section on the generated token says a supervised dashboard
// reports ready only once its token is published and the supervisor has made
// one request authenticated with that token and had it accepted, and that
// until then it serves nothing but its health answer. Both answers are here:
//
//   - /api/health is served without a token, at any time, and says whether the
//     dashboard is ready, which start of it this is, and which build. None of
//     that is a secret, and the token is never in it.
//   - /api/ready is the readiness request. It carries the token like every
//     request for the read model, and the first one accepted makes the
//     dashboard ready. A dashboard whose token could not be published refuses
//     it with the reason, and so never becomes ready.
//
// A supervised dashboard also listens on a second loopback port, chosen by the
// operating system, that answers only those two paths. Moving onto a new build
// starts the new dashboard while the old one still serves the configured port,
// so a request to that port can reach either of them; the second port reaches
// only the new one, which is what lets the supervisor confirm the dashboard it
// started before it stops the one it replaces.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
)

const (
	healthPath = "/api/health"
	readyPath  = "/api/ready"
)

// Health is the health answer, served without a token.
type Health struct {
	Ready   bool   `json:"ready"`
	StartID string `json:"start_id,omitempty"`
	Build   string `json:"build,omitempty"`
	// Problem is why a dashboard cannot become ready, where something stops it.
	Problem string `json:"problem,omitempty"`
}

// Supervise holds the server to the supervisor's readiness: it serves nothing
// but its health answer until a readiness request carrying its token is
// accepted, and then calls onReady once. startID and build are what its health
// answer names it as.
func (s *Server) Supervise(startID, build string, onReady func()) {
	s.gated = true
	s.ready.Store(false)
	s.startID = startID
	s.build = build
	s.onReady = onReady
}

// Identify names the server in its health answer, for a dashboard that is not
// gated.
func (s *Server) Identify(startID, build string) {
	s.startID = startID
	s.build = build
}

// Block keeps the server from ever becoming ready, with the reason its health
// answer and its refusal of the readiness request say. It is what a dashboard
// whose token could not be published is held to: it serves nothing, and never
// falls back to saying the token anywhere else.
func (s *Server) Block(problem string) {
	s.blocked = problem
	s.ready.Store(false)
	s.gated = true
}

// Ready reports whether the server serves the read model.
func (s *Server) Ready() bool { return s.ready.Load() }

// ListenReadiness binds the second loopback port, which answers only the health
// and readiness paths, and returns its URL.
func (s *Server) ListenReadiness() (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("dashboard: bind the readiness port: %w", err)
	}
	s.readyListener = listener
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	s.readyHosts = map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true}
	return "http://127.0.0.1:" + port + "/", nil
}

func (s *Server) notReady() string {
	if s.blocked != "" {
		return "this dashboard cannot become ready: " + s.blocked
	}
	return "this dashboard is not ready yet: it serves its health answer until the supervisor has confirmed it with its token"
}

func (s *Server) health() Health {
	health := Health{Ready: s.ready.Load(), StartID: s.startID, Build: s.build}
	if !health.Ready {
		health.Problem = s.notReady()
	}
	return health
}

func (s *Server) serveHealth(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, request, http.StatusOK, s.health())
}

// serveReady is the readiness request: refused without the token, refused
// with the reason where the dashboard cannot become ready, and otherwise the
// moment it becomes ready.
func (s *Server) serveReady(writer http.ResponseWriter, request *http.Request) {
	if !s.presented(request) {
		refuse(writer, request, http.StatusUnauthorized, "this dashboard requires its token, as a bearer token")
		return
	}
	if s.blocked != "" {
		refuse(writer, request, http.StatusServiceUnavailable, s.notReady())
		return
	}
	s.readyOnce.Do(func() {
		s.ready.Store(true)
		if s.onReady != nil {
			s.onReady()
		}
	})
	writeJSON(writer, request, http.StatusOK, s.health())
}

// serveReadiness is the second port's whole answer: the health and readiness
// paths behind the same header and Host checks every request passes, and
// nothing else.
func (s *Server) serveReadiness(writer http.ResponseWriter, request *http.Request) {
	header := writer.Header()
	header.Set("Content-Security-Policy", policy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "no-store")
	if !s.readyHosts[request.Host] {
		refuse(writer, request, http.StatusForbidden, "this dashboard answers only to the address it was started on")
		return
	}
	if request.Header.Get("Origin") != "" {
		refuse(writer, request, http.StatusForbidden, "the readiness port answers no page")
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		refuse(writer, request, http.StatusMethodNotAllowed, "this dashboard is read-only")
		return
	}
	switch request.URL.Path {
	case healthPath:
		s.serveHealth(writer, request)
	case readyPath:
		s.serveReady(writer, request)
	default:
		refuse(writer, request, http.StatusNotFound, "nothing is served at that path")
	}
}

func writeJSON(writer http.ResponseWriter, request *http.Request, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	if request.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(writer).Encode(value)
}
