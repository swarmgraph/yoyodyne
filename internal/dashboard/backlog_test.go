package dashboard

// These tests are the evidence for the backlog page: its handler, the policy
// it is served under, the scripts it loads and that they load nothing from
// anywhere else, the chart library held to its record, and the counting the
// chart draws, run under Node.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// The page is served without the token, because a browser needs it before it
// can present one, so it carries nothing of the read model: its states, its
// three controls with every choice the design names, and its own scripts. It
// is read-only like every other path.
func TestServesTheBacklogPageWithoutTheTokenAndNothingOfTheReadModel(t *testing.T) {
	t.Parallel()
	filed := time.Date(2026, 10, 5, 19, 51, 1, 0, time.UTC)
	w := serve(t, stubReader{standing: standingWith("title"), backlog: readmodel.Backlog{Items: []readmodel.BacklogItem{
		{ID: "yoyodyne-ifd.77", Title: "a secret title", FiledAt: &filed, Labels: []string{}, State: readmodel.BacklogReady},
	}}})
	w.server.Product = injected

	response, body := w.get("/backlog", nil)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("backlog page: %d %s", response.StatusCode, body)
	}
	for _, expected := range []string{
		`<main id="page" data-state="signin">`, "state-loading", "state-error", "state-ready",
		`id="backlog" class="panel backlog" data-state="loading"`, "section-loading", "section-error", "section-empty", "section-ready",
		`<select id="backlog-show">`, `value="filed"`, `value="open"`, `value="closed"`,
		`<select id="backlog-split">`, `value="priority"`, `value="done_by"`, `value="label"`, `value="state"`, `value="filed_by"`,
		`<select id="backlog-scale">`, `value="hourly"`, `value="daily"`, `value="weekly"`,
		`id="backlog-splits" type="checkbox"`,
		`<a class="page-link" href="/">`,
		`<script src="/assets/backlog-count.js"></script>`, `<script src="/assets/backlog.js"></script>`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("the backlog page lacks %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "a secret title") || strings.Contains(body, "yoyodyne-ifd.77") || strings.Contains(body, injected) {
		t.Fatalf("the backlog page carries read-model or unescaped text:\n%s", body)
	}
	for _, asset := range []string{"/assets/backlog.js", "/assets/backlog-count.js"} {
		response, body := w.get(asset, nil)
		if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/javascript") || strings.Contains(body, "a secret title") {
			t.Fatalf("%s: %d %s", asset, response.StatusCode, response.Header.Get("Content-Type"))
		}
	}
	if response, _ := w.get("/assets/backlog.html", nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("the backlog template source is served raw: %d", response.StatusCode)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if response, _ := w.request(method, "/backlog", bearer(w.server.Token())); response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s /backlog: %d", method, response.StatusCode)
		}
	}
	if response, _ := w.get("/backlog", withHost("evil.test")); response.StatusCode != http.StatusForbidden {
		t.Fatalf("backlog page to a foreign host: %d", response.StatusCode)
	}

	// The main page reaches it from the top status band.
	_, main := w.get("/", nil)
	if !regexp.MustCompile(`(?s)id="band".*<a class="page-link" href="/backlog">.*id="spend"`).MatchString(main) {
		t.Fatalf("the top status band carries no link to the backlog page:\n%s", main)
	}
}

// The policy is the one every response carries, unchanged by the page: script
// and everything else from this origin alone. The page, its scripts, and its
// stylesheet name nothing outside this origin, so no request leaves for a CDN
// or anywhere else, and they need nothing the policy refuses.
func TestTheBacklogPageLoadsNothingFromAnywhereElse(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})

	const unchanged = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	paths := []string{"/backlog", "/assets/backlog.js", "/assets/backlog-count.js", "/assets/dashboard.css", "/api/backlog"}
	if chartLibraryPresent() {
		paths = append(paths, "/"+chartLibraryAsset)
	}
	for _, path := range paths {
		response, _ := w.get(path, nil)
		if got := response.Header.Get("Content-Security-Policy"); got != unchanged {
			t.Fatalf("%s (%d): policy %q, want it unchanged as %q", path, response.StatusCode, got, unchanged)
		}
	}

	_, page := w.get("/backlog", nil)
	for _, forbidden := range []string{"<script>", "<style", " style=", "onload=", "onchange=", "onsubmit=", "http://", "https://", "//cdn", `src="//`, `href="//`} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("the backlog page depends on %q, which the policy refuses or which leaves this origin:\n%s", forbidden, page)
		}
	}
	// Every script, stylesheet, and link the page names is a path on this
	// origin.
	for _, reference := range regexp.MustCompile(`(?:src|href)="([^"]*)"`).FindAllStringSubmatch(page, -1) {
		if !strings.HasPrefix(reference[1], "/") || strings.HasPrefix(reference[1], "//") {
			t.Fatalf("the backlog page names %q, which is not a path on this origin", reference[1])
		}
	}
	for _, script := range []string{"/assets/backlog.js", "/assets/backlog-count.js"} {
		_, body := w.get(script, nil)
		for _, forbidden := range []string{"http://", "https://", "//cdn", ".style.", ".style=", `"style"`, "cssText", "insertRule", "eval(", "new Function", "importScripts", "XMLHttpRequest", "WebSocket", "EventSource", "sendBeacon"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s carries %q:\n%s", script, forbidden, body)
			}
		}
		// The one request the page makes is for the backlog, on this origin.
		for _, fetched := range regexp.MustCompile(`fetch\(([^,)]*)`).FindAllStringSubmatch(body, -1) {
			if strings.TrimSpace(fetched[1]) != `"/api/backlog"` {
				t.Fatalf("%s fetches %s, not this origin's backlog", script, fetched[1])
			}
		}
	}
}

// The chart library and its record go in together, and the record says the
// exact version, where the file came from, the file's checksum, and the MIT
// license text (chartlib.go). The page loads the library exactly when the
// build carries it, so a build without it asks for nothing that is not there.
func TestTheVendoredChartLibraryMatchesItsRecord(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, page := w.get("/backlog", nil)
	loads := strings.Contains(page, `<script src="/`+chartLibraryAsset+`"></script>`)

	library, libraryErr := fs.ReadFile(assets, chartLibraryAsset)
	record, recordErr := fs.ReadFile(assets, chartLibraryRecord)
	switch {
	case libraryErr != nil && recordErr != nil:
		if loads {
			t.Fatalf("the backlog page loads %s, which this build does not carry", chartLibraryAsset)
		}
		if !strings.Contains(page, "The chart library is not in this build") {
			t.Fatalf("a build without the chart library does not say so on the backlog page:\n%s", page)
		}
		t.Logf("this build carries no chart library; the backlog page shows its figures as a table")
		return
	case libraryErr != nil:
		t.Fatalf("%s is checked in without %s, the library it records", chartLibraryRecord, chartLibraryAsset)
	case recordErr != nil:
		t.Fatalf("%s is checked in without %s, the record of its version, source, checksum, and license", chartLibraryAsset, chartLibraryRecord)
	}

	if !loads {
		t.Fatalf("this build carries %s and the backlog page does not load it:\n%s", chartLibraryAsset, page)
	}
	read := readChartRecord(record)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(read.Version) {
		t.Fatalf("%s records version %q, not one exact version", chartLibraryRecord, read.Version)
	}
	if !strings.Contains(read.Source, read.Version) {
		t.Fatalf("%s records source %q, which does not name version %s", chartLibraryRecord, read.Source, read.Version)
	}
	sum := sha256.Sum256(library)
	if got := hex.EncodeToString(sum[:]); got != read.SHA256 {
		t.Fatalf("%s has SHA-256 %s; %s records %q", chartLibraryAsset, got, chartLibraryRecord, read.SHA256)
	}
	for _, expected := range []string{"MIT License", "Permission is hereby granted, free of charge", "Chart.js"} {
		if !strings.Contains(read.License, expected) {
			t.Fatalf("%s lacks %q from the license text:\n%s", chartLibraryRecord, expected, read.License)
		}
	}
	if !strings.Contains(string(library), read.Version) {
		t.Fatalf("%s does not name version %s, which its record says it is", chartLibraryAsset, read.Version)
	}
	response, _ := w.get("/"+chartLibraryAsset, nil)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("%s is not served as script: %d %s", chartLibraryAsset, response.StatusCode, response.Header.Get("Content-Type"))
	}
}

// The counting the chart draws holds to worked examples, run by
// testdata/backlog-count.test.js under Node with the machine set to Tokyo
// time, so counting that ignored the zone it is given and used the machine's
// would fail here. Node is found or declared absent exactly as for the main
// page's renders.
func TestTheBacklogCountsHoldUnderNode(t *testing.T) {
	node := renderer(t)
	command := exec.CommandContext(context.Background(), node, filepath.Join("testdata", "backlog-count.test.js"))
	command.Env = append(os.Environ(), "TZ=Asia/Tokyo")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("backlog-count.test.js: %v\n%s", err, output)
	}
}
