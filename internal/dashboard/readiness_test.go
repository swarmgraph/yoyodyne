package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

const readyPort = "45124"

func (w *world) readiness(path string, shape func(*http.Request)) (*http.Response, string) {
	w.t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:"+readyPort+path, nil)
	if shape != nil {
		shape(request)
	}
	recorder := httptest.NewRecorder()
	w.server.serveReadiness(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	read, _ := io.ReadAll(response.Body)
	return response, string(read)
}

func supervisedWorld(t *testing.T) (*world, *int) {
	t.Helper()
	item := readmodel.WorkItem{ID: "yoyodyne-ifd.1", Title: "an item"}
	w := serve(t, stubReader{items: map[string]readmodel.WorkItem{"yoyodyne-ifd.1": item}})
	readied := 0
	w.server.Supervise("dashboard-0123456789abcdef", "abc123", func() { readied++ })
	w.server.readyHosts = map[string]bool{"127.0.0.1:" + readyPort: true}
	return w, &readied
}

// A supervised dashboard serves nothing but its health answer until a request
// carrying its token is accepted on its readiness path; that request makes it
// ready once, and from then it serves the read model as any dashboard does.
func TestASupervisedDashboardServesOnlyItsHealthUntilItsTokenIsAccepted(t *testing.T) {
	t.Parallel()
	w, readied := supervisedWorld(t)
	token := w.server.Token()

	for _, path := range []string{"/", "/assets/page.js", "/api/standing", "/api/items/yoyodyne-ifd.1"} {
		response, body := w.get(path, bearer(token))
		if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "not ready yet") {
			t.Errorf("GET %s before ready = %d %q, want refused as not ready", path, response.StatusCode, body)
		}
	}
	response, body := w.get(healthPath, nil)
	var health Health
	if err := json.Unmarshal([]byte(body), &health); err != nil || response.StatusCode != http.StatusOK || health.Ready || health.StartID != "dashboard-0123456789abcdef" || health.Build != "abc123" {
		t.Errorf("health before ready = %d %q", response.StatusCode, body)
	}
	if response, _ := w.readiness(readyPath, nil); response.StatusCode != http.StatusUnauthorized {
		t.Errorf("a readiness request with no token = %d, want refused", response.StatusCode)
	}
	if response, _ := w.readiness(readyPath, bearer("not-the-token")); response.StatusCode != http.StatusUnauthorized || *readied != 0 {
		t.Errorf("a readiness request with the wrong token = %d (readied %d), want refused", response.StatusCode, *readied)
	}
	if response, _ := w.readiness("/api/standing", bearer(token)); response.StatusCode != http.StatusNotFound {
		t.Errorf("the readiness port served /api/standing: %d", response.StatusCode)
	}
	if response, _ := w.readiness(readyPath, all(bearer(token), withHost("127.0.0.1:"+port))); response.StatusCode != http.StatusForbidden {
		t.Errorf("the readiness port answered a Host that is not its own: %d", response.StatusCode)
	}

	response, body = w.readiness(readyPath, bearer(token))
	if err := json.Unmarshal([]byte(body), &health); err != nil || response.StatusCode != http.StatusOK || !health.Ready || health.StartID != "dashboard-0123456789abcdef" {
		t.Fatalf("readiness request with the token = %d %q, want it ready and naming its start", response.StatusCode, body)
	}
	w.readiness(readyPath, bearer(token))
	if *readied != 1 {
		t.Errorf("onReady was called %d times, want once", *readied)
	}
	if response, body := w.get("/api/items/yoyodyne-ifd.1", bearer(token)); response.StatusCode != http.StatusOK || !strings.Contains(body, "an item") {
		t.Errorf("GET an item once ready = %d %q", response.StatusCode, body)
	}
	if _, said := w.get(healthPath, nil); strings.Contains(said, token) {
		t.Errorf("the health answer carried the token: %q", said)
	}
}

// A dashboard whose token could not be published is held to serving nothing:
// its readiness request is refused with the reason, it never becomes ready,
// and its health answer says why without the token.
func TestADashboardWhoseTokenCouldNotBePublishedNeverBecomesReady(t *testing.T) {
	t.Parallel()
	w, readied := supervisedWorld(t)
	token := w.server.Token()
	w.server.Block("the token file /state/dashboard/token is a symbolic link")

	response, body := w.readiness(readyPath, bearer(token))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "symbolic link") || *readied != 0 || w.server.Ready() {
		t.Errorf("readiness request of a blocked dashboard = %d %q (readied %d)", response.StatusCode, body, *readied)
	}
	if response, body := w.get("/api/standing", bearer(token)); response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "cannot become ready") {
		t.Errorf("GET /api/standing of a blocked dashboard = %d %q", response.StatusCode, body)
	}
	_, body = w.get(healthPath, nil)
	if !strings.Contains(body, "symbolic link") || strings.Contains(body, token) {
		t.Errorf("health of a blocked dashboard = %q, want the reason and never the token", body)
	}
}

// A dashboard started by hand is ready from the start, as it always was.
func TestADashboardStartedByHandIsReadyAtOnce(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{})
	if !w.server.Ready() {
		t.Error("a dashboard nobody supervises is not ready")
	}
	if response, _ := w.get(healthPath, nil); response.StatusCode != http.StatusOK {
		t.Errorf("health = %d", response.StatusCode)
	}
}
