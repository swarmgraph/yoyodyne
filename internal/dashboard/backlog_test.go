package dashboard

// These tests are the evidence for the backlog page: its handler, the policy
// it is served under, the scripts it loads and that they load nothing from
// anywhere else, the chart library held to its pinned-file record, and the counting the
// chart draws, run under Node.

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/outsidefile"
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
	if chartLibraryLoads(assets) {
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

// The build's own chart library is held to its pinned-file record: the
// version and checksum the harness wrote there, and the license the record
// names (chartlib.go). Before the harness has fetched it, the page loads no
// chart script and says so; once it has, the page loads it and the dashboard
// serves it as script. Nothing reads or needs the hand-written record the
// pinned-file records replaced.
func TestTheChartLibraryMatchesItsPinnedRecord(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, page := w.get("/backlog", nil)
	loads := strings.Contains(page, `<script src="/`+chartLibraryAsset+`"></script>`)

	content, err := fs.ReadFile(assets, chartLibraryRecord)
	if err != nil {
		t.Fatalf("this build carries no %s, the record the harness fetches the chart library from: %v", chartLibraryRecord, err)
	}
	record, err := outsidefile.ParseRecord(chartLibraryRecord, content)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(record.URL, "@"+record.Version+"/") || record.License != "MIT" {
		t.Fatalf("%s records %s at %s under %s", chartLibraryRecord, record.Version, record.URL, record.License)
	}
	if _, err := fs.Stat(assets, "assets/chartjs.md"); err == nil {
		t.Fatalf("assets/chartjs.md is in this build; the pinned-file records replaced it, and nothing reads it")
	}

	_, err = chartLibraryIn(assets)
	switch {
	case errors.Is(err, errNoChartLibrary):
		if loads {
			t.Fatalf("the backlog page loads %s, which this build does not carry", chartLibraryAsset)
		}
		if !strings.Contains(page, "The chart library is not in this build") {
			t.Fatalf("a build without the chart library does not say so on the backlog page:\n%s", page)
		}
		t.Logf("the harness has not fetched the chart library into this build; the backlog page shows its figures as a table")
		return
	case err != nil:
		t.Fatal(err)
	}
	if !loads {
		t.Fatalf("this build carries %s and the backlog page does not load it:\n%s", chartLibraryAsset, page)
	}
	response, _ := w.get("/"+chartLibraryAsset, nil)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("%s is not served as script: %d %s", chartLibraryAsset, response.StatusCode, response.Header.Get("Content-Type"))
	}
}

// chartFixture is a chart library, its license, and the records the harness
// leaves once it has fetched both, as plain files the server is handed in
// place of its own assets.
func chartFixture() fstest.MapFS {
	library := []byte("/*!\n * Chart.js v4.4.1\n * https://www.chartjs.org\n */\nwindow.Chart = function () {};\n")
	license := []byte("The MIT License (MIT)\n\nCopyright (c) 2014-2022 Chart.js Contributors\n\nPermission is hereby granted, free of charge, to any person obtaining a copy\n")
	record := func(file, address string, content []byte) []byte {
		return []byte("file: " + file + "\nurl: " + address + "\nversion: 4.4.1\nlicense: MIT\nlicense_file: chart.js-LICENSE.md\nfor: yoyodyne-ifd.432.33\n" +
			"sha256: " + outsidefile.Checksum(content) + "\nsize: " + strconv.Itoa(len(content)) + "\nfetched_from: " + address + "\nfetched_at: 2026-10-26T15:00:00Z\n")
	}
	return fstest.MapFS{
		"assets/chart.umd.js":                                   {Data: library},
		"assets/chart.umd.js" + outsidefile.RecordSuffix:        {Data: record("chart.umd.js", "https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.js", library)},
		"assets/chart.js-LICENSE.md":                            {Data: license},
		"assets/chart.js-LICENSE.md" + outsidefile.RecordSuffix: {Data: record("chart.js-LICENSE.md", "https://cdn.jsdelivr.net/npm/chart.js@4.4.1/LICENSE.md", license)},
	}
}

// With a fixture library and its records in place of the build's own, the page
// loads the library when it matches its record, and otherwise loads none, so
// its script shows the figures as the table alone (backlog.js), when the
// library is absent or disagrees with its record: a checksum
// that is not the file's, no checksum at all, a version the file does not
// name, or no license beside it.
func TestTheBacklogPageLoadsTheChartLibraryOnlyWhereItMatchesItsRecord(t *testing.T) {
	t.Parallel()
	const script = `<script src="/` + chartLibraryAsset + `"></script>`
	const table = "The chart library is not in this build"
	recordOf := func(files fstest.MapFS) string { return string(files[chartLibraryRecord].Data) }

	for _, test := range []struct {
		name  string
		shape func(fstest.MapFS)
		loads bool
	}{
		{name: "the library matches its record", shape: func(fstest.MapFS) {}, loads: true},
		{name: "the library is absent", shape: func(files fstest.MapFS) { delete(files, chartLibraryAsset) }},
		{name: "the library and its license are absent", shape: func(files fstest.MapFS) {
			delete(files, chartLibraryAsset)
			delete(files, "assets/chart.js-LICENSE.md")
		}},
		{name: "the library is not the file its record's checksum names", shape: func(files fstest.MapFS) {
			files[chartLibraryAsset] = &fstest.MapFile{Data: append([]byte("/* changed by hand */\n"), files[chartLibraryAsset].Data...)}
		}},
		{name: "the record carries no checksum", shape: func(files fstest.MapFS) {
			files[chartLibraryRecord] = &fstest.MapFile{Data: []byte(regexp.MustCompile(`(?m)^(sha256|size|fetched_from|fetched_at):.*\n`).ReplaceAllString(recordOf(files), ""))}
		}},
		{name: "the library does not name its recorded version", shape: func(files fstest.MapFS) {
			files[chartLibraryRecord] = &fstest.MapFile{Data: []byte(strings.Replace(recordOf(files), "version: 4.4.1", "version: 4.4.2", 1))}
		}},
		{name: "the license is absent", shape: func(files fstest.MapFS) { delete(files, "assets/chart.js-LICENSE.md") }},
		{name: "the record is absent", shape: func(files fstest.MapFS) { delete(files, chartLibraryRecord) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files := chartFixture()
			test.shape(files)
			w := serve(t, stubReader{standing: standingWith("title")})
			w.server.files = files

			_, page := w.get("/backlog", nil)
			if loads := strings.Contains(page, script); loads != test.loads {
				t.Fatalf("the backlog page loads the chart library: %v, want %v:\n%s", loads, test.loads, page)
			}
			// The figures are a table either way, and the page's script shows
			// the note saying so wherever no library was loaded.
			if !strings.Contains(page, `id="backlog-table"`) || !strings.Contains(page, table) {
				t.Fatalf("the backlog page carries no table of its figures or no note for a build without the chart library:\n%s", page)
			}
			if test.loads {
				library, err := chartLibraryIn(files)
				if err != nil || library.Record.Version != "4.4.1" || !strings.Contains(library.License, "Permission is hereby granted") {
					t.Fatalf("chartLibraryIn() = %+v, %v", library, err)
				}
				response, body := w.get("/"+chartLibraryAsset, nil)
				if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/javascript") || !strings.Contains(body, "Chart.js v4.4.1") {
					t.Fatalf("%s is not served as script: %d %s", chartLibraryAsset, response.StatusCode, response.Header.Get("Content-Type"))
				}
			}
		})
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
