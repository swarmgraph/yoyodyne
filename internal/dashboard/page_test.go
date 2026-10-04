package dashboard

// These tests are the evidence for the page: its eight sections, each with an
// empty, a loading, and an error state beside its ready one, rendered from the
// read model and from nothing else.
//
// The page is drawn by its own script, which a Go test cannot run. So the
// script is run under Node, against the fixtures under testdata/fixtures, by
// testdata/render.js, and what it leaves in the document is held to the renders
// under testdata/renders — one page per scenario, which a reviewer opens in a
// browser beside the stylesheet or reads as text.
//
// A machine without Node therefore fails the render test rather than skipping
// it: the page's only behavioural evidence is the comparison against those
// renders, and a run that quietly passes without making it reports a green
// suite for a page nothing drew. The one environment that skips is one that
// declares its own absence of Node in YOYODYNE_NODE_UNAVAILABLE, which nothing
// here sets and which is therefore a statement somebody made about the
// environment they built. The fixture-shape and route tests below hold either
// way.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var updateRenders = flag.Bool("update-renders", false, "rewrite the rendered pages under testdata/renders from the fixtures")

// sections are the eight the page carries, by the id each carries in the
// shell: the five the design names, the spend box above Running now, and the
// factory problems and program managers under Provider capacity.
var sections = []string{"band", "spend", "live", "pipeline", "throughput", "capacity", "factory", "managers"}

// popups are the three dialogs the page opens over the sections: a grouping —
// of the pipeline listed by title, or of the attention line listed by what
// waits — one thing's card, a work item's or an attention entry's, and one
// program manager instance's current lane report.
var popups = []string{"grouping", "card", "report"}

// sectionStates are the states every section has, each a child the panel
// shows when its data-state names it. A pop-up has the same four, and is
// closed besides.
var sectionStates = []string{"loading", "error", "empty", "ready"}

// fixture reads one fixture as the JSON the server would have sent.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "fixtures", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

// strict decodes JSON refusing any field the Go type does not carry, which is
// what holds a hand-written fixture to the read model's actual shape.
func strict(t *testing.T, name string, body []byte, into any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("fixture %s is not the read model's shape: %v", name, err)
	}
}

// The shell carries the eight sections, and each of them carries its four states
// with the lines the script fills, so a section the script has not reached yet
// says it is reading rather than being blank.
func TestTheShellCarriesEverySectionEachWithItsStates(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, body := w.get("/", nil)
	for _, id := range sections {
		if !strings.Contains(body, `<section id="`+id+`" class="panel `+id+`" data-state="loading"`) {
			t.Fatalf("the shell lacks section %q opening in its loading state:\n%s", id, body)
		}
		for _, state := range sectionStates {
			if !strings.Contains(body, `class="section-`+state+`"`) {
				t.Fatalf("the shell lacks a %s state:\n%s", state, body)
			}
		}
		for _, line := range []string{"-problem", "-remedy", "-empty"} {
			if !strings.Contains(body, `id="`+id+line+`"`) {
				t.Fatalf("section %q lacks its %s line:\n%s", id, line, body)
			}
		}
	}
	if strings.Count(body, `class="panel `) != len(sections) {
		t.Fatalf("the shell carries %d panels, not %d:\n%s", strings.Count(body, `class="panel `), len(sections), body)
	}
	// The two pop-ups are dialogs, closed to begin with, each with the same four
	// states a section has and a button that closes it.
	for _, id := range popups {
		if !strings.Contains(body, `<div id="`+id+`" class="popup" role="dialog" aria-modal="true" aria-labelledby="`+id+`-heading" data-state="loading" hidden>`) {
			t.Fatalf("the shell lacks the %s pop-up, closed and a dialog:\n%s", id, body)
		}
		for _, line := range []string{"-problem", "-remedy", "-empty", "-close", "-backdrop", "-heading"} {
			if !strings.Contains(body, `id="`+id+line+`"`) {
				t.Fatalf("pop-up %q lacks its %s:\n%s", id, line, body)
			}
		}
	}
	if strings.Count(body, `class="popup"`) != len(popups) {
		t.Fatalf("the shell carries %d pop-ups, not %d:\n%s", strings.Count(body, `class="popup"`), len(popups), body)
	}
	// Closed means not shown: the stylesheet gives a pop-up a display of its
	// own, which on its own would beat the user agent's `[hidden]` rule and
	// show both dialogs over the page on load, so it holds the attribute off
	// itself — the render driver prunes by attribute and cannot see this.
	_, stylesheet := w.get("/assets/dashboard.css", nil)
	for _, rule := range []string{"[hidden] {\n  display: none !important;\n}", ".popup[hidden] {\n  display: none;\n}"} {
		if !strings.Contains(stylesheet, rule) {
			t.Fatalf("the stylesheet lacks %q, so a closed pop-up would be shown:\n%s", rule, stylesheet)
		}
	}
}

// Every fixture the page is rendered from is the read model's own shape: a
// field the model does not carry is refused, so the renders cannot drift from
// what the server actually sends.
func TestTheFixturesAreTheReadModelsShape(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(filepath.Join("testdata", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	standings, throughputs, spends, items, reports := 0, 0, 0, 0, 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		body := fixture(t, name)
		switch {
		case strings.HasPrefix(name, "standing-"):
			var standing readmodel.Standing
			strict(t, name, body, &standing)
			// A line that could be read is a list, empty rather than absent; a line
			// that could not is absent, as the server sends it, so the page's
			// handling of both is what the renders exercise. The item lists follow
			// the queue the same way, and name what the counts count.
			if standing.ObservedAt.IsZero() || (standing.RunningProblem == "") != (standing.Running != nil) || (standing.NotStartableProblem == "") != (standing.NotStartable != nil) {
				t.Fatalf("fixture %s is not a standing as the server sends one: %+v", name, standing)
			}
			if (standing.NotStartableProblem == "") != (standing.AdmittedItems != nil) || (standing.NotStartableProblem == "") != (standing.StartableItems != nil) ||
				(standing.AdmittedItems != nil && len(standing.AdmittedItems) != standing.Admitted) || (standing.StartableItems != nil && len(standing.StartableItems) != standing.Startable) {
				t.Fatalf("fixture %s names items the counts do not count: admitted %d over %d, startable %d over %d", name, standing.Admitted, len(standing.AdmittedItems), standing.Startable, len(standing.StartableItems))
			}
			standings++
		case strings.HasPrefix(name, "throughput-"):
			var throughput readmodel.Throughput
			strict(t, name, body, &throughput)
			if len(throughput.Windows) != 2 || throughput.Windows[0].Label != "today" || throughput.Windows[1].Label != "last 7 days" {
				t.Fatalf("fixture %s does not carry the two windows: %+v", name, throughput)
			}
			for _, window := range throughput.Windows {
				if (throughput.RunsProblem == "") != (window.LandedItems != nil) || (window.LandedItems != nil && len(window.LandedItems) != window.Landed) {
					t.Fatalf("fixture %s names %d landed runs in %q against a count of %d", name, len(window.LandedItems), window.Label, window.Landed)
				}
			}
			throughputs++
		case strings.HasPrefix(name, "spend-"):
			var spend readmodel.Spend
			strict(t, name, body, &spend)
			// The two windows the box shows, in the order it shows them and with
			// the rolling one said to be rolling; and the month behind them,
			// which is a list where it could be read and absent where it could
			// not, as the server sends it.
			if len(spend.Windows) != 2 || spend.Windows[0].Label != "last 24 hours" || !spend.Windows[0].Rolling ||
				spend.Windows[1].Label != "last 7 days" || spend.Windows[1].Rolling {
				t.Fatalf("fixture %s does not carry the two windows: %+v", name, spend)
			}
			if (spend.Problem == "") != (spend.Days != nil) || (spend.Days != nil && len(spend.Days) != 30) {
				t.Fatalf("fixture %s names %d days beside a problem of %q", name, len(spend.Days), spend.Problem)
			}
			spends++
		case strings.HasPrefix(name, "item-"):
			var item readmodel.WorkItem
			strict(t, name, body, &item)
			// The fixture is the item it is named for, with every field the card
			// labels present — the lists empty rather than absent — and its run
			// either carried or accounted for.
			if item.ID != strings.TrimPrefix(name, "item-") || item.ObservedAt.IsZero() || item.Labels == nil {
				t.Fatalf("fixture %s is not a work item as the server sends one: %+v", name, item)
			}
			items++
		case strings.HasPrefix(name, "report-"):
			var answer readmodel.ProgramManagerReport
			strict(t, name, body, &answer)
			// The fixture is the instance it is named for, with its lists empty
			// rather than absent, as the server sends it, and a report where the
			// instance says one was written.
			instance := answer.Instance
			if instance.Agent != strings.TrimPrefix(name, "report-") || answer.ObservedAt.IsZero() || instance.Blockers == nil || instance.Claims == nil || instance.RestartRequests == nil ||
				(answer.Report != nil) != (instance.ReportWrittenAt != nil) || (answer.Report != nil && answer.Report.Remaining == nil) {
				t.Fatalf("fixture %s is not a program manager's report as the server sends one: %+v", name, answer)
			}
			reports++
		default:
			t.Fatalf("fixture %s is none of a standing, a throughput, a spend, an item, or a report", name)
		}
	}
	if standings < 4 || throughputs < 3 || spends < 3 || items < 3 || reports < 3 {
		t.Fatalf("expected the standing, throughput, spend, item, and report fixtures, found %d, %d, %d, %d, and %d", standings, throughputs, spends, items, reports)
	}
}

// The throughput is served as JSON to the token and to nobody else, refused
// whole when the state cannot be read, and carries what the read model said
// about each window — including which source it could not read.
func TestServesTheThroughputToTheTokenAlone(t *testing.T) {
	t.Parallel()
	var throughput readmodel.Throughput
	strict(t, "throughput-degraded", fixture(t, "throughput-degraded"), &throughput)
	w := serve(t, stubReader{standing: standingWith("title"), throughput: throughput})

	response, body := w.get("/api/throughput", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("throughput with the token: %d %s", response.StatusCode, body)
	}
	for _, expected := range []string{`"label":"today"`, `"label":"last 7 days"`, `"landed":4`, `"landed_items":[{"run_id"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("throughput lacks %s: %s", expected, body)
		}
	}
	if strings.Contains(body, "runs_problem") || strings.Contains(body, "cost_usd") {
		t.Fatalf("the throughput reports a problem it did not have, or a cost the spend owns: %s", body)
	}
	if response, body := w.get("/api/throughput", nil); response.StatusCode != http.StatusUnauthorized || strings.Contains(body, "landed") {
		t.Fatalf("throughput without the token: %d %s", response.StatusCode, body)
	}
	if response, _ := w.get("/api/throughput", all(bearer(w.server.Token()), withHost("evil.test"))); response.StatusCode != http.StatusForbidden {
		t.Fatalf("throughput to a foreign host: %d", response.StatusCode)
	}
	if response, _ := w.request(http.MethodPost, "/api/throughput", bearer(w.server.Token())); response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("throughput POST: %d", response.StatusCode)
	}

	broken := serve(t, stubReader{failure: errors.New("the state root could not be resolved")})
	response, body = broken.get("/api/throughput", bearer(broken.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `"error":"the state root could not be resolved"`) || strings.Contains(body, "windows") {
		t.Fatalf("throughput over unreadable state: %d %s", response.StatusCode, body)
	}
}

// The spend is served as JSON to the token and to nobody else, refused whole
// when the state cannot be read, and carries what the read model said about
// each window and about the month behind them.
func TestServesTheSpendToTheTokenAlone(t *testing.T) {
	t.Parallel()
	var spend readmodel.Spend
	strict(t, "spend-busy", fixture(t, "spend-busy"), &spend)
	w := serve(t, stubReader{standing: standingWith("title"), spend: spend})

	response, body := w.get("/api/spend", bearer(w.server.Token()))
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("spend with the token: %d %s", response.StatusCode, body)
	}
	for _, expected := range []string{
		`"label":"last 24 hours"`, `"rolling":true`, `"label":"last 7 days"`, `"since_day":"2026-09-13"`,
		`"cost_usd":1232.58`, `"unpriced":1`, `"floor":true`, `"reaches":"2026-09-01"`,
		`"days":[{"day":"2026-09-19"`, `"reached":false`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("the spend lacks %s: %s", expected, body)
		}
	}
	if strings.Contains(body, "problem") {
		t.Fatalf("a source that was read is reported as a problem: %s", body)
	}
	if response, body := w.get("/api/spend", nil); response.StatusCode != http.StatusUnauthorized || strings.Contains(body, "cost_usd") {
		t.Fatalf("spend without the token: %d %s", response.StatusCode, body)
	}
	if response, _ := w.get("/api/spend", all(bearer(w.server.Token()), withHost("evil.test"))); response.StatusCode != http.StatusForbidden {
		t.Fatalf("spend to a foreign host: %d", response.StatusCode)
	}
	if response, _ := w.request(http.MethodPost, "/api/spend", bearer(w.server.Token())); response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("spend POST: %d", response.StatusCode)
	}

	broken := serve(t, stubReader{failure: errors.New("the state root could not be resolved")})
	response, body = broken.get("/api/spend", bearer(broken.server.Token()))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `"error":"the state root could not be resolved"`) || strings.Contains(body, "windows") {
		t.Fatalf("spend over unreadable state: %d %s", response.StatusCode, body)
	}
}

// The vocabularies the page reads are the model's own, held at compile time
// where the page's evidence is read: a refusal's kind is the queue's HoldKind
// and not a type of this package's or the model's, a run's stage is the
// model's Stage, and the ledger the throughput is priced from is the stream
// store `yoyo status --spend` prices — the same type, satisfying the same
// interface, so the page cannot be handed a second pricing.
var (
	_ backlog.HoldKind = readmodel.Refused{}.Kind
	_ readmodel.Stage  = readmodel.RunningRun{}.Stage
	_ readmodel.Mover  = readmodel.Attention{}.Mover
	_ readmodel.Ledger = (*runstate.StreamStore)(nil)
	_ readmodel.Runs   = (*runstate.Store)(nil)
)

// The movers the page names are the model's vocabulary, every one of them, in
// the model's order, and in the model's words, so the tile that counts what
// waits on a person counts by the value each entry carries rather than by a
// reading of the sentence beside it, says the operator's count first because
// the model puts it first, and names each mover as the terminal does.
func TestTheNeedsAHumanTileCountsByTheModelsMovers(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, script := w.get("/assets/dashboard.js", nil)
	var named []readmodel.Mover
	labels := map[readmodel.Mover]string{}
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, `{ mover: "`) {
			continue
		}
		parts := strings.Split(line, `"`)
		if len(parts) < 5 {
			t.Fatalf("the script's mover line does not carry a token and a label: %q", line)
		}
		named = append(named, readmodel.Mover(parts[1]))
		labels[readmodel.Mover(parts[1])] = parts[3]
	}
	movers := readmodel.Movers()
	if len(named) != len(movers) {
		t.Fatalf("the script names %d movers, and the model's vocabulary holds %d: %v against %v", len(named), len(movers), named, movers)
	}
	for i, mover := range movers {
		if named[i] != mover {
			t.Fatalf("the script's mover %d is %q, and the model's is %q", i, named[i], mover)
		}
		if labels[mover] != mover.Possessive() {
			t.Fatalf("the script names %q as %q, and the model says %q", mover, labels[mover], mover.Possessive())
		}
	}
	// The counting reads each entry's mover and nothing else of the entry: the
	// two sentences beside it are shown on the entry's card, never parsed.
	_, counting, found := strings.Cut(script, "function byMover(")
	if !found {
		t.Fatalf("the script has no byMover:\n%s", script)
	}
	counting, _, _ = strings.Cut(counting, "\n  }")
	if !strings.Contains(counting, "entry.mover") {
		t.Fatalf("the counting does not read each entry's mover from the model:\n%s", counting)
	}
	if strings.Contains(counting, "whose") || strings.Contains(counting, "what") {
		t.Fatalf("the counting reads the mover off the sentence beside it rather than the value the model carries:\n%s", counting)
	}
}

// The list and card use the label supplied by the read model, which owns the
// vocabulary and its plain wording. The page keeps no second kind registry.
func TestTheEntryCardsAreHeadedByTheModelsKinds(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, script := w.get("/assets/dashboard.js", nil)
	for _, expected := range []string{`add("Kind", entry.label)`, `heading.textContent = entry.label`, `label: each.entry.label`} {
		if !strings.Contains(script, expected) {
			t.Fatalf("the list and card must read the model's label: missing %s", expected)
		}
	}
	// The card is drawn from the entries the standing carries, and from
	// nothing the page fetches: the tracker and the amendment store are never
	// read for it.
	// The sentences it shows are the ones the model titled, each work item
	// beside its title, where the model could title one.
	for _, expected := range []string{"standing.needs_human.filter", "entry.amendment", "entry.owed_step", "entry.executor", "entry.said_what", "entry.said_whose"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("the script does not read %q from the standing:\n%s", expected, script)
		}
	}
	for _, forbidden := range []string{`"/api/amendments`, `"/api/attention`, `"/api/needs`} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("the script fetches an entry through %q rather than reading the standing:\n%s", forbidden, script)
		}
	}
}

// The words a program manager's badge says are the model's statuses, every one
// of them, so a status the page has no class for cannot arrive unannounced, and
// the section and the report card read the instances the standing carries and
// the report the model serves, and nothing else.
func TestTheProgramManagersSectionSaysTheModelsStatuses(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, script := w.get("/assets/dashboard.js", nil)
	var named []readmodel.ProgramManagerStatus
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, `{ status: "`) {
			continue
		}
		named = append(named, readmodel.ProgramManagerStatus(strings.Split(line, `"`)[1]))
	}
	statuses := []readmodel.ProgramManagerStatus{readmodel.ProgramManagerBlocked, readmodel.ProgramManagerStale, readmodel.ProgramManagerWorking}
	if len(named) != len(statuses) {
		t.Fatalf("the script names %d statuses, and the model has %d: %v against %v", len(named), len(statuses), named, statuses)
	}
	for i, status := range statuses {
		if named[i] != status {
			t.Fatalf("the script's status %d is %q, and the model's is %q", i, named[i], status)
		}
	}
	for _, expected := range []string{"standing.program_managers", "standing.program_managers_problem", `"/api/program-managers/" + encodeURIComponent(agent)`, "instance.stale_says", "instance.blockers", "instance.claims"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("the script does not read %q from the model:\n%s", expected, script)
		}
	}
}

// The pipeline reads the model's own figures: the startable count and each
// run's stage arrive on the standing, and the script keeps no list of phases
// and makes no subtraction of one line from another to get either.
func TestThePipelineReadsTheModelsCountAndFold(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, script := w.get("/assets/dashboard.js", nil)
	// The groupings list what the model names — the admitted items, the
	// startable ones, and the landed runs — rather than assembling a list from
	// the other lines.
	for _, expected := range []string{"standing.startable", "run.stage === name", `item.kind === "stalled"`, "standing.admitted_items", "standing.startable_items", "period.landed_items", `"/api/items/" + encodeURIComponent(id)`} {
		if !strings.Contains(script, expected) {
			t.Fatalf("the script does not read %q from the model:\n%s", expected, script)
		}
	}
	for _, forbidden := range []string{`"checking"`, `"cleaning_up"`, `"completing"`, "standing.admitted -"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("the script derives what the model provides, through %q:\n%s", forbidden, script)
		}
	}
}

// nodeDecision is what to do about Node on the machine a render was asked for:
// run the script under the Node at Path, skip saying Skip, or fail saying Fail.
// Exactly one of the three is ever set. It is a value rather than three calls on
// a *testing.T so that every path is reachable from a test — the failing one
// most of all, which cannot be exercised by letting it fail for real.
type nodeDecision struct {
	Path string
	Skip string
	Fail string
}

// decideNode reads the machine the way the render test needs it read.
//
// Node being installed settles it, whatever anything declares: a declaration is
// a statement that an environment has no Node, and one that has it can draw the
// page. Node being absent is a failure unless that environment declared the
// absence itself, which is the whole of what stands between a missing tool and a
// suite that passes without ever running the page's script.
func decideNode(lookPath func(string) (string, error), getenv func(string) string) nodeDecision {
	if node, err := lookPath(NodeProgram); err == nil {
		return nodeDecision{Path: node}
	}
	if declared := strings.TrimSpace(getenv(NodeUnavailableVariable)); declared != "" {
		return nodeDecision{Skip: fmt.Sprintf(
			"%s is not on the PATH and %s says this environment deliberately has none (%q), so the page's script was not run here; the renders under testdata/renders are the last run's evidence",
			NodeProgram, NodeUnavailableVariable, declared)}
	}
	return nodeDecision{Fail: fmt.Sprintf(
		"%s is not on the PATH, so the page's script was never run and the renders under testdata/renders were never compared — this suite passing would say nothing about the page. Install %s, which %s names as a development dependency for the dashboard, or, if this environment is meant to have none, set %s to say which environment that is",
		NodeProgram, NodeProgram, NodeDocumentation, NodeUnavailableVariable)}
}

// renderer is the Node the render test runs the page's script under, or the end
// of that test one way or the other.
func renderer(t *testing.T) string {
	t.Helper()
	switch decision := decideNode(exec.LookPath, os.Getenv); {
	case decision.Path != "":
		return decision.Path
	case decision.Skip != "":
		t.Skip(decision.Skip)
	default:
		t.Fatal(decision.Fail)
	}
	return ""
}

// How the render test decides whether it can run is itself evidence, because the
// decision is what a green suite means: a machine without Node fails and says
// what to do about it, and only an environment that declares its own absence
// skips.
func TestARenderWithoutNodeFailsUnlessDeclaredUnavailable(t *testing.T) {
	t.Parallel()

	installed := func(string) (string, error) { return "/usr/local/bin/node", nil }
	absent := func(program string) (string, error) {
		return "", fmt.Errorf("exec: %q: executable file not found in $PATH", program)
	}
	nothingDeclared := func(string) string { return "" }
	declaring := func(value string) func(string) string {
		return func(name string) string {
			if name == NodeUnavailableVariable {
				return value
			}
			return ""
		}
	}

	t.Run("a machine with Node renders", func(t *testing.T) {
		t.Parallel()
		decision := decideNode(installed, nothingDeclared)
		if decision.Path != "/usr/local/bin/node" {
			t.Fatalf("decideNode() = %+v, want the Node it found", decision)
		}
	})

	// A declaration is about an environment that has no Node. One that has Node
	// anyway draws the page rather than taking the declaration's word for it,
	// which is what keeps a variable exported in a shell profile from silently
	// standing the evidence down on the machine the checks run on.
	t.Run("a machine with Node renders although it declares otherwise", func(t *testing.T) {
		t.Parallel()
		decision := decideNode(installed, declaring("an old export nobody cleared"))
		if decision.Path == "" || decision.Skip != "" {
			t.Fatalf("decideNode() = %+v, want the Node it found and no skip", decision)
		}
	})

	t.Run("a declared absence skips and quotes the declaration", func(t *testing.T) {
		t.Parallel()
		decision := decideNode(absent, declaring("this container is built without Node"))
		if decision.Skip == "" || decision.Path != "" || decision.Fail != "" {
			t.Fatalf("decideNode() = %+v, want a skip alone", decision)
		}
		for _, named := range []string{NodeUnavailableVariable, "this container is built without Node"} {
			if !strings.Contains(decision.Skip, named) {
				t.Errorf("skip = %q, want it to name %q", decision.Skip, named)
			}
		}
	})

	// An empty declaration declares nothing. A variable exported with no value
	// is the shape a half-written sandbox leaves behind, and reading it as a
	// declaration would be the silence this arrangement exists to end, reachable
	// by accident.
	t.Run("an empty declaration is no declaration", func(t *testing.T) {
		t.Parallel()
		for _, value := range []string{"", "   "} {
			decision := decideNode(absent, declaring(value))
			if decision.Fail == "" || decision.Skip != "" {
				t.Fatalf("decideNode() with %q declared = %+v, want a failure", value, decision)
			}
		}
	})

	t.Run("an undeclared absence fails and names the way out", func(t *testing.T) {
		t.Parallel()
		decision := decideNode(absent, nothingDeclared)
		if decision.Fail == "" || decision.Path != "" || decision.Skip != "" {
			t.Fatalf("decideNode() = %+v, want a failure alone", decision)
		}
		// The failure has to name the tool that is missing, the document that
		// says to install it, and the declaration that is the other way out.
		// Anything less is a red run somebody has to go and diagnose.
		for _, named := range []string{NodeProgram, NodeDocumentation, NodeUnavailableVariable} {
			if !strings.Contains(decision.Fail, named) {
				t.Errorf("failure = %q, want it to name %q", decision.Fail, named)
			}
		}
	})
}

// The page's script draws every section in every state from the fixtures, and
// what it draws is what the renders under testdata/renders hold. Each of the
// eight sections reaches each of its four states in at least one scenario, each
// of the three pop-ups reaches each of its four and is closed in another, the
// page reaches its own four, and no scenario sets a style or sends the token
// anywhere but as a bearer to this origin — render.js refuses both.
func TestThePageRendersEverySectionInEveryState(t *testing.T) {
	node := renderer(t)
	out := t.TempDir()
	if *updateRenders {
		out = filepath.Join("testdata", "renders")
	}
	command := exec.CommandContext(context.Background(), node, filepath.Join("testdata", "render.js"), "--out", out)
	// The renders carry clock times, so they are rendered in one timezone
	// whatever machine renders them.
	command.Env = append(os.Environ(), "TZ=UTC")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render.js: %v\n%s", err, output)
	}

	var matrix map[string]struct {
		Page     string            `json:"page"`
		Sections map[string]string `json:"sections"`
		Popups   map[string]string `json:"popups"`
	}
	rendered, err := os.ReadFile(filepath.Join(out, "matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rendered, &matrix); err != nil {
		t.Fatalf("matrix.json: %v\n%s", err, rendered)
	}
	reached := map[string]map[string]bool{}
	pageStates := map[string]bool{}
	for scenario, result := range matrix {
		pageStates[result.Page] = true
		for _, id := range sections {
			state, present := result.Sections[id]
			if !present {
				t.Fatalf("scenario %s says nothing about section %s", scenario, id)
			}
			if reached[id] == nil {
				reached[id] = map[string]bool{}
			}
			reached[id][state] = true
		}
		for _, id := range popups {
			state, present := result.Popups[id]
			if !present {
				t.Fatalf("scenario %s says nothing about the %s pop-up", scenario, id)
			}
			if reached[id] == nil {
				reached[id] = map[string]bool{}
			}
			reached[id][state] = true
		}
	}
	for _, id := range sections {
		for _, state := range sectionStates {
			if !reached[id][state] {
				t.Errorf("no scenario renders section %s in its %s state", id, state)
			}
		}
	}
	for _, id := range popups {
		for _, state := range append([]string{"closed"}, sectionStates...) {
			if !reached[id][state] {
				t.Errorf("no scenario leaves the %s pop-up %s", id, state)
			}
		}
	}
	for _, state := range []string{"signin", "loading", "error", "ready"} {
		if !pageStates[state] {
			t.Errorf("no scenario leaves the page in its %s state", state)
		}
	}

	// The words, not just the states: what the fixtures say has to land on the
	// page as text, and a zero must never stand in for a line that could not be
	// read.
	page := func(scenario string) string {
		body, err := os.ReadFile(filepath.Join(out, scenario+".html"))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	for scenario, expectations := range map[string][]string{
		"pass-failures":            {"Factory problems", "maintenance has failed 5 times", "factory-flow program manager factory-flow-pm", "development manager resolves the cause"},
		"pass-failures-unreadable": {"product pass failures could not be read whole: permission denied"},
		"busy": {
			"Dashboard 3 of 3: the page, five sections, with its empty, loading, and error states",
			"cost unknown (its event log is gone)",
			"approved, resuming integration",
			// The effort level beside the model it was asked of.
			"claude-code · claude-opus-5 · medium effort · account pool-b",
			// The held-back work by what it waits on, each pile with its next step
			// and whose it is; the ready work waiting for a slot counted apart from
			// it; and whether any of it is the operator's.
			"waits on the development manager's decision about a stopped run (most)",
			"next: she decides what becomes of each stopped run: a repair, a re-run, a wait, a re-scope, or an escalation; whose: the development manager's",
			"waits on the harness carrying out a decision already recorded (most)",
			"is done in the Lead Product Manager's conversation, not by a run (most)",
			"is parked by the Lead Product Manager (most)",
			"admitted items not startable now",
			"of 9 admitted items; awaiting a decision: 1, awaiting carry-out: 1; and 3 ready, waiting for a developer slot; 3 slots, all taken",
			`<span class="stage-unit">3 ready, waiting for a developer slot; 3 slots, all taken</span>`,
			"1 item here is the operator's — 1 waits on an unresolved directive; everything else is somebody else's to move. Needs a human:",
			// What waits on a person, counted per mover in the model's order: the
			// operator's is the figure, and each role's and the harness's are
			// beside it, out of the whole the terminal prints. The label opens
			// the list.
			`<button class="grouping-open tile-label" type="button" data-grouping="attention">Needs a human</button>`,
			`<span class="figure">1</span>`,
			`<span class="unit">thing waiting on the operator</span>`,
			`<span class="detail">of 8 things waiting in all; the Lead Product Manager's: 2, the architect's: 2, the development manager's: 1, the harness's: 2</span>`,
			"Needs a human: 1 thing waiting on the operator; waiting on others: the Lead Product Manager's: 2, the architect's: 2, the development manager's: 1, the harness's: 2.",
			"22 runs reached the target branch",
			// A stopped run is said as what it ended as, never as a wait it is
			// still in.
			"9 stopped on a blocker, 3 cancelled",
			"Stops by cause",
			"operator-stop: 3, provider-idle: 1, repair-budget: 9, unknown: 4",
			"at least $1,232.58 from 452 invocations",
			"1 exchange record could not be read, so the cost is a floor",
			`<span class="held-state">capacity-blocked</span>`,
			"What to do: nothing needs doing",
			"no reset named, and nothing probes: the run stopped",
		},
		"quiet":      {"The harness is idle", "Nothing is running, and no conversation has a turn in flight.", "The backlog is empty", "Nothing ran in the last 7 days", "Nothing is recorded as spent: no run, conversation, branch review, side thread, or exchange here has a priced record.", "No run or conversation is waiting on provider capacity", `<p id="managers-empty" class="empty">No instance of the program manager role is configured, and none has a restart request open.</p>`},
		"degraded":   {`<span class="figure">—</span>`, `<li class="stage stage-unreadable">`, "Could not be read: the admitted work could not be read", `<button class="grouping-open pile-label" type="button" data-grouping="stage:developing">developing</button>`},
		"unreadable": {"Could not be read: the recorded runs could not be read: open runs: input/output error", "Could not be read: the spend could not be read: open streams: input/output error", "yoyo doctor says whether bd answers in this checkout"},
		// Nothing waits on the operator and everything waits on a role or the
		// harness: the commonest real case. The operator's figure is zero, and
		// no role's count is said as his.
		"roles": {
			`<span class="figure">0</span>`,
			`<span class="unit">things waiting on the operator</span>`,
			`<span class="detail">of 6 things waiting in all; the Lead Product Manager's: 2, the architect's: 2, the development manager's: 1, the harness's: 1</span>`,
			"Needs a human: nothing waiting on the operator; waiting on others: the Lead Product Manager's: 2, the architect's: 2, the development manager's: 1, the harness's: 1.",
		},
		"held": {
			`<p id="banner" class="banner" role="status">Every role is paused`, "Every role is held: 5 agents on opus, and none names an alternate", "are ready and nothing is choosing work: Paused on the provider's usage window until 18:50Z (most)", "next: the harness asks again when the provider's usage window lifts; whose: nobody's", "Nothing here is the operator's: under his rule of 2026-09-26 only a change to the fundamental goals is", "the harness is choosing nothing: Paused on the provider's usage window until 18:50Z",
			// One thing waiting, and it is the operator's: the figure says so and
			// there is no breakdown to give.
			`<span class="figure">1</span>`, `<span class="unit">thing waiting on the operator</span>`, "Needs a human: 1 thing waiting on the operator.",
		},
		// A failed poll backs the page off: it says when it asks again, later
		// than its ordinary clock.
		"stale": {`class="freshness freshness-stale">stale<`, "so this is the reading from 14:05:09, and the page asks again in 20s, less often while the dashboard answers slowly or not at all"},
		// The age of the snapshot each answer was served from: said beside the
		// moment it was observed while it is fresh, marked stale without a
		// warning during routine lag, and said with the failure beside it where
		// the dashboard's latest build failed. The render driver also checks
		// just below, at, and just above the page's own age-warning threshold.
		"snapshot":     {`class="freshness">taken 4s ago; asks again every 10 s<`},
		"snapshot-old": {`class="freshness freshness-stale">stale<`, `<p id="stale" class="stale" role="status" hidden></p>`},
		"snapshot-failed": {
			`class="freshness freshness-stale">stale<`, "The dashboard's last reading of the standing failed — bd list timed out after 30s — so what is shown is the reading taken 34s ago.",
			`<p id="throughput-stale" class="stale" role="status">The dashboard's last reading of the throughput failed — the state root could not be resolved — so what is shown is the reading taken 1m ago.</p>`,
		},
		"throughput-stale": {`class="freshness freshness-stale">stale<`, "The last reading failed for the throughput", `<p id="throughput-stale" class="stale" role="status">The last reading failed`},
		// The spend box while the month is still being priced, and after a poll
		// that failed on a page that had already been drawn: the figures it had
		// stay, marked stale, rather than the box going blank.
		"spend-pending": {`<section id="spend" class="panel spend" data-state="loading"`, "Pricing the last thirty days…"},
		"spend-stale":   {`class="freshness freshness-stale">stale<`, "The last reading failed for the spend", `<p id="spend-stale" class="stale" role="status">The last reading failed`, "at least $191.53 from 84 invocations"},
		// The month behind the box: one line per local day, newest first, with
		// the by-kind split beside each; the days the log does not reach saying
		// so rather than reading as zero; and the two lines that are not days —
		// the spend with no moment, and the records that could not be priced.
		"spend-days": {
			`<h2 id="grouping-heading" class="popup-title">Spend by day (32 lines)</h2>`,
			"one line per local day for the past 30 days, newest first, priced from the spend log",
			`<span class="grouping-title grouping-day">2026-09-19</span>`,
			"$92.00 from 31 invocations — $57.04 on 19 runs, $27.60 on 9 conversations, $4.60 on 2 branch reviews, $2.76 on 1 exchange",
			`<span class="grouping-title grouping-day">2026-09-08</span>`, "nothing spent",
			`<li class="grouping-item grouping-day-unreached">`,
			`<span class="grouping-title grouping-day">2026-08-21</span>`, "no priced record reaches this far back",
			"$4.25 from 2 invocations whose moment could not be read, counted in every window above and on no day here",
			"1 exchange record could not be read, so every figure here and above is a floor",
		},
		"spend-days-empty":   {`<h2 id="grouping-heading" class="popup-title">Spend by day</h2>`, `<p id="grouping-empty" class="empty">Nothing is recorded as spent: no run, conversation, branch review, side thread, or exchange here has a priced record.</p>`},
		"spend-days-error":   {`<h2 id="grouping-heading" class="popup-title">Spend by day</h2>`, "the spend could not be read: open streams: input/output error", "yoyo status --spend 30 prices the same records"},
		"spend-days-loading": {`<h2 id="grouping-heading" class="popup-title">Spend by day</h2>`, `aria-labelledby="grouping-heading" data-state="loading"`, `<span id="grouping-waiting">Pricing the last thirty days…</span>`},
		"refused":            {"permission denied", "yoyo doctor says what cannot be read"},
		"wrong-token":        {"that is not the token this dashboard printed when it started"},
		// The card: every field under a plain label, the run in the terminal's
		// words, and a field the item has nothing in saying so.
		"card": {
			`<h2 id="card-heading" class="popup-title">Dashboard 3 of 3: the page, five sections, with its empty, loading, and error states</h2>`,
			"<dt>Id</dt>", "<dt>Title</dt>", "<dt>Status</dt>", "<dt>Priority</dt>", "<dt>Labels</dt>", "<dt>Parent</dt>", "<dt>Description</dt>", "<dt>Design</dt>", "<dt>Acceptance criteria</dt>", "<dt>Notes</dt>", "<dt>Run</dt>",
			"<dd>in_progress</dd>", "<dd>P1 (0 is the most urgent, 4 the least)</dd>", "<dd>dashboard</dd>", "<dd>yoyodyne-ifd.141</dd>",
			`<p class="card-run-flight">in flight — developing, 12m elapsed, $3.41 so far</p>`,
			"branch: yoyodyne/yoyodyne-ifd-141-3/c07d6849",
			"developer session: 0f2c41ab-7e05-4c3d-9a1b-6e8f0d2a4c71",
		},
		"card-loading": {"Reading the work item…", `<h2 id="card-heading" class="popup-title">yoyodyne-ifd.201</h2>`},
		"card-missing": {`<p id="card-empty" class="empty">No work item is recorded under yoyodyne-ifd.212`},
		"card-refused": {"Could not be read: the work item could not be read: bd show failed", "yoyo status yoyodyne-ifd.230 says the same thing at the terminal"},
		// The grouping: the items behind a figure, by title, each a button that
		// opens its card, with the pipeline's own word for it beside.
		"grouping": {
			`<h2 id="grouping-heading" class="popup-title">Held back (6 items)</h2>`,
			`<button class="item-open grouping-title" type="button" data-item="yoyodyne-ifd.200">Slack threads carry the item's title</button>`,
			`<span class="grouping-detail">waiting on yoyodyne-ifd.199</span>`,
			`data-item="yoyodyne-ifd.219"`,
		},
		"grouping-landed":  {`Landed last 7 days (4 items)`, "from 2026-09-13, local days, newest first", `data-item="yoyodyne-ifd.439"`, "landed 2026-09-19 13:41:00", `data-item="yoyodyne-ifd.435"`},
		"grouping-empty":   {`<h2 id="grouping-heading" class="popup-title">Startable</h2>`, "the harness is choosing nothing: Paused on the provider's usage window until 18:50Z", `<p id="grouping-empty" class="empty">No admitted item is startable.</p>`},
		"grouping-error":   {`<h2 id="grouping-heading" class="popup-title">Admitted</h2>`, "Could not be read: the admitted work could not be read: bd list", "yoyo doctor says whether bd answers in this checkout"},
		"grouping-loading": {`<h2 id="grouping-heading" class="popup-title">Landed today</h2>`, `<span id="grouping-waiting">Reading what the runs came to…</span>`},
		// The card over the grouping it was opened from: a stopped run with its
		// change preserved, said as `yoyo status` says it.
		"grouping-card": {
			`<h2 id="grouping-heading" class="popup-title">Held back: waits on the development manager's decision about a stopped run (1 item)</h2>`,
			"next: she decides what becomes of each stopped run", `data-item="yoyodyne-ifd.153"`,
			// Each held item says since when, in the reader's zone — the renders are
			// drawn in UTC — and how long before the reading that was.
			"held since 2026-09-10 08:00 UTC, 9 days ago; run run-5035c832 stopped on it",
			`<h2 id="card-heading" class="popup-title">Triage names the phase a run stopped in</h2>`,
			`<p class="card-run-preserved">preserved: stopped, reviewing — work preserved</p>`,
			"reason: review: the reviewer asked for repair 3 times",
			`<dd class="card-none">none</dd>`,
			"cost $18.62",
		},
		// The Needs-a-human list: every entry by what it is, in the terminal's
		// words, with its kind and who it is waiting on, the operator's first, and
		// each a button that opens its card.
		"attention": {
			`<h2 id="grouping-heading" class="popup-title">What is waiting, and on whom (8 things)</h2>`,
			`<button class="item-open grouping-title" type="button" data-entry="directive:directive-4f2c">directive directive-4f2c is unresolved: which branch does this land on?</button>`,
			`<button class="item-open grouping-title" type="button" data-entry="owed-step:run-2b6f0d3e8a1c4f7b9e5d2a8c6f1b3e70">cleanup of the branch and worktree for yoyodyne-ifd.222 is not finished</button>`,
			`data-entry="amendment:amendment-3f9a1c2e8b7d4f6a9c1e2b3d4f5a6b7c"`, `data-entry="amendment:amendment-7c2b9e4d1a6f3c8e5b0d2f4a6c8e1b3d"`,
			`data-entry="conversation-carried-item:yoyodyne-ifd.188"`, `data-entry="held-work:decision"`, `data-entry="held-work:carry-out"`, `data-entry="report:"`,
			`<span class="item-id">proposed document change</span>`, `<span class="grouping-detail">the architect's — nothing reaches the document until they or the operator decide it</span>`, `<span class="grouping-detail">the harness's — the decision is made, and what is outstanding is the harness acting on it</span>`,
		},
		"attention-empty": {`<h2 id="grouping-heading" class="popup-title">What is waiting, and on whom</h2>`, `<p id="grouping-empty" class="empty">Nothing waits on the operator or anybody else.</p>`},
		"attention-error": {`<h2 id="grouping-heading" class="popup-title">What is waiting, and on whom</h2>`, "Could not be read: the recorded directives could not be read: open directives: permission denied"},
		// An amendment's card: the target document, the proposer, the proposed
		// change, and why, whole, with the item the proposer was working on
		// opening its own card.
		"attention-amendment": {
			`<h2 id="card-heading" class="popup-title">proposed document change</h2>`,
			"<dt>What</dt>", "<dt>Waiting on</dt>", "<dt>Kind</dt>", "<dt>Mover</dt>",
			"<dd>the architect's — nothing reaches the document until they or the operator decide it</dd>",
			"<dt>Document</dt>", "<dd>v1-harness-design</dd>", "<dt>Document kind</dt>", "<dd>design</dd>", "<dt>Owner</dt>", "<dd>architect</dd>",
			"<dt>Proposed by</dt>", "<dd>developer (agent developer)</dd>", "<dt>In run</dt>", "<dd>run-5c1e9b2d7a4f3e8c6b0d1f2a3c4e5b6d</dd>",
			`<button class="item-open item-id" type="button" data-item="yoyodyne-ifd.210">yoyodyne-ifd.210</button>`,
			"<dt>Proposed change</dt>", "<dd>The design should say that configuration selects sequence and never grants authority.</dd>",
			"<dt>Why</dt>", "<dd>The invariant exists and the design does not cite it.</dd>",
			"<dt>Raised</dt>", "<dd>2026-09-19 11:20:00</dd>", "<dd>amendment-3f9a1c2e8b7d4f6a9c1e2b3d4f5a6b7c</dd>",
		},
		// An owed step's card: the run, the item, where it stopped, and the
		// command that settles it, in the terminal's sentence.
		"attention-owed-step": {
			`<h2 id="card-heading" class="popup-title">run not finished</h2>`,
			"<dd>the harness's — `yoyo reconcile` finishes the run's cleanup and records it</dd>",
			"<dt>Run</dt>", "<dd>run-2b6f0d3e8a1c4f7b9e5d2a8c6f1b3e70</dd>",
			`data-item="yoyodyne-ifd.222"`, "<dt>Ended</dt>", "<dd>succeeded</dd>", "<dt>Phase</dt>", "<dd>cleaning_up</dd>",
		},
		// A carried item's card: the item and the role.
		"attention-carried-item": {
			`<h2 id="card-heading" class="popup-title">work in conversation</h2>`,
			`<dd>yoyodyne-ifd.188 is admitted for "conversation:product-manager" rather than a developer run</dd>`,
			`<button class="item-open item-id" type="button" data-item="yoyodyne-ifd.188">yoyodyne-ifd.188</button>`,
			"<dt>Executor</dt>", "<dd>conversation:product-manager</dd>", "<dt>Role</dt>", "<dd>product-manager</dd>", "<dd>the Lead Product Manager's</dd>",
		},
		// The item's own card, opened from the entry's.
		"attention-carried-item-card": {`<h2 id="card-heading" class="popup-title">The goals document gains a legibility clause</h2>`, "<dd>Executor: conversation:product-manager.</dd>"},
		// A poll after the card was opened finds the entry settled, and the
		// list empty; and one that finds the line unreadable says so on both.
		"attention-settled":    {`<p id="grouping-empty" class="empty">Nothing waits on the operator or anybody else.</p>`, "This entry is no longer waiting: it was settled since the page last read where the harness stands, at 14:15:09."},
		"attention-unreadable": {`<p id="grouping-problem" class="problem">Could not be read: the recorded directives could not be read`, `<p id="card-problem" class="problem">Could not be read: the recorded directives could not be read`},
	} {
		body := page(scenario)
		for _, expected := range expectations {
			if !strings.Contains(body, expected) {
				t.Errorf("the %s render lacks %q", scenario, expected)
			}
		}
	}
	// The held items are listed oldest hold first, in the order the model gave
	// them. The two held waits are two piles now, so the order is read where
	// both are listed: the whole of what is held back.
	if held := page("grouping"); !strings.Contains(held, "held since 2026-09-19") || strings.Index(held, "held since 2026-09-10") > strings.Index(held, "held since 2026-09-19") {
		t.Errorf("what is held back does not list the oldest hold first")
	}
	if strings.Contains(page("degraded"), `<span class="figure">0</span>`) {
		t.Errorf("the degraded render counts an unreadable line as zero")
	}
	// A closed pop-up is not in the render at all: a page scenario carries no
	// pop-up, a pop-up scenario carries the pop-ups it left open over the page
	// it names rather than that page again, and the one that closed both with
	// Escape carries neither.
	for _, scenario := range []string{"busy", "closed", "attention-closed", "report-closed"} {
		if strings.Contains(page(scenario), `class="popup"`) {
			t.Errorf("the %s render carries a pop-up nobody opened", scenario)
		}
	}
	for scenario, beneath := range map[string]string{"card": "busy", "grouping-error": "degraded", "closed": "busy", "attention-amendment": "busy", "attention-error": "degraded", "spend-days": "busy", "report": "busy"} {
		if body := page(scenario); strings.Contains(body, `class="panel `) || !strings.Contains(body, "the "+beneath+" render") {
			t.Errorf("the %s render does not stand alone over the %s render", scenario, beneath)
		}
	}
	// Every item the busy page names opens a card, from Running now and from
	// every grouping: the title and the id of each running item, and each entry
	// of a grouping, carry the item they open.
	for _, id := range []string{"yoyodyne-ifd.141.3", "yoyodyne-ifd.201", "yoyodyne-ifd.212", "yoyodyne-ifd.230"} {
		if strings.Count(page("busy"), `data-item="`+id+`"`) != 2 {
			t.Errorf("the busy render does not open %s from both its title and its id", id)
		}
	}
	for _, key := range []string{"admitted", "held", "startable", "running", "landed:today", "landed:week", "pile:held-decision", "pile:held-carry-out", "pile:directive", "stage:developing", "stage:integrating", "attention", "spend:days"} {
		if !strings.Contains(page("busy"), `data-grouping="`+key+`"`) {
			t.Errorf("the busy render has nothing that opens the %s grouping", key)
		}
	}
	// A stage that could not be read still opens, so the reason is readable in
	// full rather than only as a dash; so does the attention tile, and so does
	// the tile when nothing waits.
	for _, key := range []string{"admitted", "held", "startable", "attention", "spend:days"} {
		if !strings.Contains(page("degraded"), `data-grouping="`+key+`"`) {
			t.Errorf("the degraded render has nothing that opens the unreadable %s stage", key)
		}
	}
	// Every entry of the busy list opens a card, and every card's entry key is
	// one the list carries: the attention render names each key once as an
	// opener, and the amendment card names the item its proposer was working
	// on as an opener of its own.
	if strings.Count(page("attention"), `data-entry="`) != 8 {
		t.Errorf("the attention render does not open a card on each of the 8 entries")
	}
	if !strings.Contains(page("attention-amendment"), `data-item="yoyodyne-ifd.210"`) {
		t.Errorf("the amendment card does not open the item its proposer was working on")
	}
	// Every instance the busy page lists opens its current report.
	for _, agent := range []string{"docs-pgm", "factory-pgm", "writing-pgm"} {
		if strings.Count(page("busy"), `data-report="`+agent+`"`) != 1 {
			t.Errorf("the busy render does not open %s's report from its row", agent)
		}
	}
	// A report card ends with what it shows and never with a file path: the
	// fixtures carry report_path, and none of it reaches a card a person reads.
	for _, scenario := range []string{"report", "report-blocked", "report-unwritten"} {
		body := page(scenario)
		if strings.Contains(body, "Report file") || strings.Contains(body, "report.json") || strings.Contains(body, "/Users/somebody") {
			t.Errorf("the %s render shows the report's file path to a person", scenario)
		}
	}
	// The same model fields reach both the list and the opened card for a red
	// queued merge, unfinished cleanup, and a merge the forge dropped. Live
	// checks appear only in Running, never among ended-run attention.
	var steps readmodel.Standing
	strict(t, "standing-run-steps", fixture(t, "standing-run-steps"), &steps)
	if !strings.Contains(page("run-steps"), "Checks still running") || strings.Contains(page("attention-run-steps"), "run-live") {
		t.Error("live checks must be running, absent from attention")
	}
	if len(steps.NeedsHuman) != 4 {
		t.Fatal("expected cleanup and dropped-merge decisions to be separate entries")
	}
	for _, entry := range steps.NeedsHuman {
		wantMover := readmodel.MoverHarness
		wantLabel := "merge stuck"
		if entry.Kind == readmodel.AttentionPublication {
			wantMover = readmodel.MoverDevelopmentManager
		}
		if entry.Kind == readmodel.AttentionOwedStep && (entry.OwedStep.PullRequest == nil || !entry.OwedStep.PullRequest.MergeQueued) {
			wantLabel = "run not finished"
		}
		if entry.Mover != wantMover || entry.Label() != wantLabel {
			t.Fatalf("wrong mover or label: %+v", entry)
		}
		scenario := "attention-" + entry.ID
		if entry.ID == "run-dropped" && entry.Kind == readmodel.AttentionOwedStep {
			scenario += "-cleanup"
		}
		card := page(scenario)
		list := page("attention-run-steps")
		for _, words := range []string{entry.What(), entry.Whose(), entry.Label()} {
			if !strings.Contains(card, words) || !strings.Contains(list, words) {
				t.Errorf("%s: list and card must both carry %q", entry.ID, words)
			}
		}
		if !strings.Contains(strings.Join(strings.Fields(card), " "), `<dt>Kind</dt> <dd>`+entry.Label()+`</dd>`) {
			t.Errorf("%s: card must show the label", entry.ID)
		}
	}
	// The card acts on nothing: every button on it opens or closes a pop-up.
	for _, scenario := range []string{"attention-amendment", "attention-owed-step", "attention-carried-item", "report", "report-blocked"} {
		for _, button := range strings.Split(page(scenario), "<button")[1:] {
			if !strings.Contains(button, `data-item="`) && !strings.Contains(button, `data-entry="`) && !strings.Contains(button, `data-grouping="`) && !strings.Contains(button, `-close"`) {
				t.Errorf("the %s render carries a button that neither opens nor closes a pop-up: %.120s", scenario, button)
			}
		}
	}
	if strings.Contains(page("held"), "the harness pulls next") {
		t.Errorf("the held render offers items the harness pulls next under a banner saying it is choosing nothing")
	}
	// A run the harness stopped is never told it asks again.
	for _, scenario := range []string{"busy", "held"} {
		for _, entry := range strings.Split(page(scenario), `<li class="held `)[1:] {
			if strings.HasPrefix(entry, "held-capacity-blocked") && strings.Contains(entry, "it asks again at the probe interval") {
				t.Errorf("the %s render tells a capacity-blocked run it asks again", scenario)
			}
		}
	}
	// Every render is a document a browser opens as the page: the doctype, the
	// html element, and nothing of the driver's own.
	for scenario := range matrix {
		if body := page(scenario); !strings.HasPrefix(body, "<!doctype html>\n<html lang=\"en\">") || strings.Contains(body, "#document") {
			t.Errorf("the %s render is not a plain document:\n%.200s", scenario, body)
		}
	}

	if *updateRenders {
		return
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "renders"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		expected, err := os.ReadFile(filepath.Join("testdata", "renders", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(filepath.Join(out, entry.Name()))
		if err != nil {
			t.Fatalf("render.js did not write %s, which testdata/renders holds: %v", entry.Name(), err)
		}
		if !bytes.Equal(expected, actual) {
			t.Errorf("%s differs from the render under testdata/renders; run `go test ./internal/dashboard -run TestThePageRendersEverySectionInEveryState -update-renders` and review the change", entry.Name())
		}
	}
	for scenario := range matrix {
		if _, err := os.Stat(filepath.Join("testdata", "renders", scenario+".html")); err != nil {
			t.Errorf("scenario %s has no render under testdata/renders; run with -update-renders", scenario)
		}
	}
}
