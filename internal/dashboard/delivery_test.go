package dashboard

// These tests are the evidence for the delivery page: its handler and the
// link that reaches it, the policy it is served under, that it loads nothing
// from anywhere else and the one chart library the backlog page loads, and
// what it draws from a reading, run under Node.

import (
	"context"
	"encoding/json"
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
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The page is served without the token, as the backlog page is, so it carries
// nothing of the read model: its states, its time scale with both of the
// reading's scales, its own scripts, and the links back. The main page's top
// status band links to it beside the backlog page, and each of the two pages
// links to the other.
func TestServesTheDeliveryPageWithoutTheTokenAndNothingOfTheReadModel(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title"), delivery: deliveryFixture()})
	w.server.Product = injected

	response, body := w.get("/delivery", nil)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("delivery page: %d %s", response.StatusCode, body)
	}
	for _, expected := range []string{
		`<main id="page" data-state="signin">`, "state-loading", "state-error", "state-ready",
		`<select id="delivery-scale">`, `value="daily"`, `value="weekly"`,
		`id="delivery-zone"`, `id="delivery-measures"`, `id="delivery-nochart"`,
		`<a class="page-link" href="/">`, `<a class="page-link" href="/backlog">`,
		`<script src="/assets/delivery-figures.js"></script>`, `<script src="/assets/delivery.js"></script>`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("the delivery page lacks %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "yoyodyne-ifd.501") || strings.Contains(body, "claude-opus") || strings.Contains(body, injected) {
		t.Fatalf("the delivery page carries read-model or unescaped text:\n%s", body)
	}
	for _, asset := range []string{"/assets/delivery.js", "/assets/delivery-figures.js"} {
		response, _ := w.get(asset, nil)
		if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/javascript") {
			t.Fatalf("%s: %d %s", asset, response.StatusCode, response.Header.Get("Content-Type"))
		}
	}
	if response, _ := w.get("/assets/delivery.html", nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("the delivery template source is served raw: %d", response.StatusCode)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if response, _ := w.request(method, "/delivery", bearer(w.server.Token())); response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s /delivery: %d", method, response.StatusCode)
		}
	}
	if response, _ := w.get("/delivery", withHost("evil.test")); response.StatusCode != http.StatusForbidden {
		t.Fatalf("delivery page to a foreign host: %d", response.StatusCode)
	}

	_, main := w.get("/", nil)
	if !regexp.MustCompile(`(?s)id="band".*<a class="page-link" href="/backlog">[^<]*</a>[^<]*<a class="page-link" href="/delivery">.*id="spend"`).MatchString(main) {
		t.Fatalf("the top status band carries no link to the delivery page beside the backlog page's:\n%s", main)
	}
	_, backlog := w.get("/backlog", nil)
	if !strings.Contains(backlog, `<a class="page-link" href="/delivery">`) {
		t.Fatalf("the backlog page does not link to the delivery page:\n%s", backlog)
	}
}

// The policy is the one every response carries, unchanged by the page, and
// the page and its scripts name nothing outside this origin: the one request
// the page makes is for the delivery measures, on this origin.
func TestTheDeliveryPageLoadsNothingFromAnywhereElse(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})

	const unchanged = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	for _, path := range []string{"/delivery", "/assets/delivery.js", "/assets/delivery-figures.js", "/api/delivery"} {
		response, _ := w.get(path, nil)
		if got := response.Header.Get("Content-Security-Policy"); got != unchanged {
			t.Fatalf("%s (%d): policy %q, want it unchanged as %q", path, response.StatusCode, got, unchanged)
		}
	}

	_, page := w.get("/delivery", nil)
	for _, forbidden := range []string{"<script>", "<style", " style=", "onload=", "onchange=", "onsubmit=", "http://", "https://", "//cdn", `src="//`, `href="//`} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("the delivery page depends on %q, which the policy refuses or which leaves this origin:\n%s", forbidden, page)
		}
	}
	for _, reference := range regexp.MustCompile(`(?:src|href)="([^"]*)"`).FindAllStringSubmatch(page, -1) {
		if !strings.HasPrefix(reference[1], "/") || strings.HasPrefix(reference[1], "//") {
			t.Fatalf("the delivery page names %q, which is not a path on this origin", reference[1])
		}
	}
	for _, script := range []string{"/assets/delivery.js", "/assets/delivery-figures.js"} {
		_, body := w.get(script, nil)
		for _, forbidden := range []string{"http://", "https://", "//cdn", ".style.", ".style=", `"style"`, "cssText", "insertRule", "innerHTML", "eval(", "new Function", "importScripts", "XMLHttpRequest", "WebSocket", "EventSource", "sendBeacon"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s carries %q", script, forbidden)
			}
		}
		for _, fetched := range regexp.MustCompile(`fetch\(([^,)]*)`).FindAllStringSubmatch(body, -1) {
			if strings.TrimSpace(fetched[1]) != `"/api/delivery"` {
				t.Fatalf("%s fetches %s, not this origin's delivery measures", script, fetched[1])
			}
		}
	}
}

// The delivery page loads the backlog page's chart library, the one copy the
// build carries, on the same terms: where it matches its pinned record and
// not otherwise. No second chart library is in the build.
func TestTheDeliveryPageSharesTheBacklogPagesChartLibrary(t *testing.T) {
	t.Parallel()
	const script = `<script src="/` + chartLibraryAsset + `"></script>`

	w := serve(t, stubReader{standing: standingWith("title")})
	_, delivery := w.get("/delivery", nil)
	_, backlog := w.get("/backlog", nil)
	if !strings.Contains(delivery, script) || !strings.Contains(backlog, script) {
		t.Fatalf("the two pages do not both load %s from this build", chartLibraryAsset)
	}
	libraries, err := fs.Glob(assets, "assets/*.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range libraries {
		content, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatal(err)
		}
		if name != chartLibraryAsset && strings.Contains(string(content), "Chart.js v") {
			t.Fatalf("%s is a second copy of the chart library; the delivery page shares %s", name, chartLibraryAsset)
		}
	}

	for _, test := range []struct {
		name  string
		shape func(files map[string]bool)
		loads bool
	}{
		{name: "the library matches its record", loads: true},
		{name: "the library is absent", shape: func(drop map[string]bool) { drop[chartLibraryAsset] = true }},
		{name: "the license is absent", shape: func(drop map[string]bool) { drop["assets/chart.js-LICENSE.md"] = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files := chartFixture()
			drop := map[string]bool{}
			if test.shape != nil {
				test.shape(drop)
			}
			for name := range drop {
				delete(files, name)
			}
			w := serve(t, stubReader{standing: standingWith("title")})
			w.server.files = files
			_, page := w.get("/delivery", nil)
			if loads := strings.Contains(page, script); loads != test.loads {
				t.Fatalf("the delivery page loads the chart library: %v, want %v:\n%s", loads, test.loads, page)
			}
			if !strings.Contains(page, "The chart library is not in this build") {
				t.Fatalf("the delivery page carries no note for a build without the chart library:\n%s", page)
			}
		})
	}
}

// The page draws the reading as the read model encodes it: the fixture is the
// read model's own types, encoded as /api/delivery encodes them, and
// testdata/delivery.test.js holds the figures and the page's charts and
// tables to it under Node, with the machine in Tokyo time so a label taken
// from the browser's zone rather than the reading's would fail.
func TestTheDeliveryPageDrawsTheReadingUnderNode(t *testing.T) {
	node := renderer(t)
	encoded, err := json.Marshal(deliveryFixture())
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "delivery.json")
	if err := os.WriteFile(fixture, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(context.Background(), node, filepath.Join("testdata", "delivery.test.js"), fixture)
	command.Env = append(os.Environ(), "TZ=Asia/Tokyo")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("delivery.test.js: %v\n%s", err, output)
	}
}

// deliveryFixture is a reading of four days and one week: a day whose run
// records could not be read, so every measure is not recorded; a day nothing
// merged; a day with two merges and every figure, a stage the event logs could
// not time and true zeros among them; and today, with one merge, the usage
// records unreadable, and a breakage still open.
func deliveryFixture() readmodel.Delivery {
	zone := readmodel.DeliveryZone()
	day := func(date string) (time.Time, time.Time) {
		start, _ := time.ParseInLocation(time.DateOnly, date, zone)
		return start, start.AddDate(0, 0, 1)
	}
	seconds := func(median, p90 int64) *readmodel.DurationSummary {
		return &readmodel.DurationSummary{Count: 2, MedianSeconds: median, P90Seconds: p90, MinSeconds: median, P25Seconds: median, P75Seconds: p90, MaxSeconds: p90}
	}
	rate := func(value float64) *float64 { return &value }
	count := func(value int) *int { return &value }
	runs := []string{"the run records", "every run's event log"}
	const runsUnread = "the run records could not be read: permission denied"
	const usageUnread = "the usage log could not be read: permission denied"

	unread := readmodel.DeliveryMeasure{ReadFrom: runs, NotRecorded: runsUnread}
	start0, end0 := day("2026-10-02")
	unreadable := readmodel.DeliveryPeriod{
		Label: "2026-10-02", Start: start0, End: end0,
		Merges:        readmodel.DeliveryMerges{DeliveryMeasure: unread},
		LeadTime:      readmodel.DeliveryLeadTime{DeliveryMeasure: unread},
		ChangeFailure: readmodel.DeliveryChangeFailure{DeliveryMeasure: unread},
		Recovery:      readmodel.DeliveryRecovery{DeliveryMeasure: readmodel.DeliveryMeasure{ReadFrom: []string{"the recurring passes"}, NotRecorded: "the recurring passes could not be read"}},
		Rework:        readmodel.DeliveryRework{DeliveryMeasure: unread},
		HandSteps:     readmodel.DeliveryHandStepCount{DeliveryMeasure: unread},
		FirstPass:     readmodel.DeliveryFirstPass{DeliveryMeasure: unread},
		Tokens:        readmodel.DeliveryTokens{DeliveryMeasure: unread},
	}

	read := readmodel.DeliveryMeasure{ReadFrom: runs}
	stagesOf := func(timed bool, reason string) []readmodel.DeliveryStage {
		stages := []readmodel.DeliveryStage{}
		for index, stage := range []readmodel.LeadStage{readmodel.LeadQueued, readmodel.LeadDeveloping, readmodel.LeadChecks, readmodel.LeadReview, readmodel.LeadRepairing, readmodel.LeadMerging} {
			entry := readmodel.DeliveryStage{Stage: stage}
			switch {
			case !timed:
				entry.NotRecorded = reason
			case stage == readmodel.LeadReview:
				entry.NotRecorded = "no event log of a change merged records what its runs did"
			default:
				entry.Duration = seconds(int64(600*(index+1)), int64(1200*(index+1)))
			}
			stages = append(stages, entry)
		}
		return stages
	}
	start1, end1 := day("2026-10-03")
	quiet := readmodel.DeliveryPeriod{
		Label: "2026-10-03", Start: start1, End: end1,
		Merges:        readmodel.DeliveryMerges{DeliveryMeasure: read, Count: count(0), Changes: []readmodel.DeliveredChange{}},
		LeadTime:      readmodel.DeliveryLeadTime{DeliveryMeasure: read, Stages: stagesOf(false, "nothing merged")},
		ChangeFailure: readmodel.DeliveryChangeFailure{DeliveryMeasure: read, Failed: count(0)},
		Recovery:      readmodel.DeliveryRecovery{DeliveryMeasure: read, Breakages: []readmodel.DeliveryBreakage{}},
		Rework:        readmodel.DeliveryRework{DeliveryMeasure: read},
		HandSteps:     readmodel.DeliveryHandStepCount{DeliveryMeasure: read, Count: &readmodel.InterventionCount{Changes: []readmodel.ChangeInterventions{}, Floor: readmodel.InterventionsFloor}},
		FirstPass:     readmodel.DeliveryFirstPass{DeliveryMeasure: read},
		Tokens:        readmodel.DeliveryTokens{DeliveryMeasure: read, ByModel: []readmodel.ModelTokens{}},
	}

	cleared := start1.Add(30 * time.Hour)
	start2, end2 := day("2026-10-04")
	busy := readmodel.DeliveryPeriod{
		Label: "2026-10-04", Start: start2, End: end2,
		Merges: readmodel.DeliveryMerges{DeliveryMeasure: read, Count: count(2), Changes: []readmodel.DeliveredChange{
			{WorkItemID: "yoyodyne-ifd.501", RunID: "run-1", MergedAt: start2.Add(10 * time.Hour)},
			{WorkItemID: "yoyodyne-ifd.502", RunID: "run-2", MergedAt: start2.Add(14 * time.Hour)},
		}},
		LeadTime: readmodel.DeliveryLeadTime{DeliveryMeasure: read, Total: seconds(7200, 90000), Stages: stagesOf(true, ""),
			ReviewRounds: &readmodel.CountSummary{Count: 2, Median: 1, P90: 2, Max: 2}, RepairRounds: &readmodel.CountSummary{Count: 2, Median: 0, P90: 1, Max: 1}},
		ChangeFailure: readmodel.DeliveryChangeFailure{DeliveryMeasure: read, Merges: 2, Failed: count(0), Rate: rate(0)},
		Recovery: readmodel.DeliveryRecovery{DeliveryMeasure: read, Recovered: seconds(5400, 5400), Breakages: []readmodel.DeliveryBreakage{
			{Kind: readmodel.BreakageSchedulerStall, Since: cleared.Add(-90 * time.Minute), ClearedAt: &cleared, Seconds: 5400},
		}},
		Rework: readmodel.DeliveryRework{DeliveryMeasure: read, Merges: 2, Bugs: 1, Untyped: 1, Rate: rate(0.5), UntypedShare: rate(0.5),
			Minimum: "a minimum: a merge whose item records no kind is counted as not a bug fix"},
		HandSteps: readmodel.DeliveryHandStepCount{DeliveryMeasure: read, Count: &readmodel.InterventionCount{Merged: 2, Attributed: 3, Unattributed: 1, PerChange: 1.5, Floor: readmodel.InterventionsFloor}},
		FirstPass: readmodel.DeliveryFirstPass{DeliveryMeasure: read, Merges: 2, Known: 2, Passed: 1, Rate: rate(0.5)},
		Tokens: readmodel.DeliveryTokens{DeliveryMeasure: read, Merges: 2, Recorded: 2, ByModel: []readmodel.ModelTokens{
			{Model: "claude-opus-5-5", Changes: 2, Tokens: runstate.TokenUsage{InputTokens: 100000, OutputTokens: 20000}, PerChange: readmodel.TokensPerChange{Input: 50000, Output: 10000, Total: 60000}},
			{Model: "gpt-5-codex", Changes: 1, Tokens: runstate.TokenUsage{InputTokens: 30000}, PerChange: readmodel.TokensPerChange{Input: 15000, Total: 15000}},
		}},
	}

	start3, end3 := day("2026-10-05")
	still := start3.Add(2 * time.Hour)
	today := readmodel.DeliveryPeriod{
		Label: "2026-10-05", Start: start3, End: end3, Current: true,
		Merges:        readmodel.DeliveryMerges{DeliveryMeasure: read, Count: count(1), Changes: []readmodel.DeliveredChange{{WorkItemID: "yoyodyne-ifd.503", RunID: "run-3", MergedAt: start3.Add(time.Hour)}}},
		LeadTime:      readmodel.DeliveryLeadTime{DeliveryMeasure: read, Total: seconds(3600, 3600), Stages: stagesOf(true, "")},
		ChangeFailure: readmodel.DeliveryChangeFailure{DeliveryMeasure: read, Merges: 1, Failed: count(1), Rate: rate(1), Reverted: 1, Settling: 1, FailedChanges: []readmodel.FailedChange{{WorkItemID: "yoyodyne-ifd.503", Why: []string{"reverted"}}}},
		Recovery: readmodel.DeliveryRecovery{DeliveryMeasure: read, Breakages: []readmodel.DeliveryBreakage{}, StillBroken: []readmodel.DeliveryBreakage{
			{Kind: readmodel.BreakageFactoryStall, Subject: "the merge queue", Since: still, Seconds: 3600},
		}},
		Rework:    readmodel.DeliveryRework{DeliveryMeasure: read, Merges: 1, Features: 1, Rate: rate(0), UntypedShare: rate(0)},
		HandSteps: readmodel.DeliveryHandStepCount{DeliveryMeasure: read, Count: &readmodel.InterventionCount{Merged: 1, PerChange: 0, Floor: readmodel.InterventionsFloor}},
		FirstPass: readmodel.DeliveryFirstPass{DeliveryMeasure: read, Merges: 1, Unknown: 1},
		Tokens:    readmodel.DeliveryTokens{DeliveryMeasure: readmodel.DeliveryMeasure{ReadFrom: runs, NotRecorded: usageUnread}, Merges: 1},
	}

	week := busy
	week.Label, week.Start, week.End, week.Current = "2026-09-28", start0.AddDate(0, 0, -4), start0.AddDate(0, 0, 3), true
	return readmodel.Delivery{
		ObservedAt: start3.Add(3 * time.Hour),
		Zone:       readmodel.DeliveryZoneName,
		Days:       []readmodel.DeliveryPeriod{unreadable, quiet, busy, today},
		Weeks:      []readmodel.DeliveryPeriod{week},
		Sources: []readmodel.DeliverySource{
			{Name: "the run records", Read: true},
			{Name: "the usage log", Read: false, Problem: "permission denied"},
		},
	}
}
