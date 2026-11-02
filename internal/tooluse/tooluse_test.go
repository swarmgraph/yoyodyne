package tooluse_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
	"github.com/mason-bryant/yoyodyne/internal/tooluse"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC) }

// pile is an in-memory report pile.
type pile struct {
	reports []report.Report
}

func (p *pile) Append(reported report.Report) error {
	p.reports = append(p.reports, reported)
	return nil
}

func (p *pile) List() ([]report.Report, error) {
	return append([]report.Report(nil), p.reports...), nil
}

func watch(reports tooluse.Reports) tooluse.Watch {
	return tooluse.Watch{Reports: reports, RepositoryID: "yoyodyne", Build: "abcdef1", Clock: fixedClock{},
		Problem: func(err error) { panic(err) }}
}

func runAttribution() spend.Attribution {
	return spend.Attribution{ProductID: "yoyodyne", Backend: domain.BackendClaudeCode, RunID: "run-1", WorkItemID: "yoyodyne-ifd.1"}
}

func read(policy backend.ToolPolicy, calls ...backend.ToolCall) backend.RunResult {
	return backend.RunResult{
		Backend:        domain.BackendClaudeCode,
		SessionID:      "session-1",
		Process:        execution.ProcessResult{Status: execution.ProcessSucceeded},
		ToolCalls:      calls,
		ToolVisibility: backend.ToolVisibility{Read: true, Policy: policy},
	}
}

func call(id, tool string, kind backend.ToolKind, outcome backend.ToolOutcome) backend.ToolCall {
	return backend.ToolCall{ID: id, Tool: tool, Kind: kind, Outcome: outcome}
}

func TestCheckComparesCallsWithThePolicy(t *testing.T) {
	t.Parallel()
	developer := backend.RunRequest{RunID: "run-1", Role: domain.RoleDeveloper}
	reviewer := backend.RunRequest{RunID: "run-1", Role: domain.RoleReviewer}
	manager := backend.RunRequest{RunID: "chat-1", Role: domain.RoleProductManager}
	cases := []struct {
		name    string
		request backend.RunRequest
		result  backend.RunResult
		want    []string
		gaps    int
	}{
		{"a developer's ordinary tools", developer, read(backend.ToolPolicyDeveloper,
			call("a", "Bash", backend.ToolKindShell, backend.ToolExecuted),
			call("b", "Edit", backend.ToolKindWrite, backend.ToolExecuted),
			call("c", "Read", backend.ToolKindRead, backend.ToolExecuted),
			call("d", "TodoWrite", backend.ToolKindSession, backend.ToolExecuted)), nil, 0},
		{"a developer reaching the network", developer, read(backend.ToolPolicyDeveloper,
			call("a", "WebFetch", backend.ToolKindWeb, backend.ToolDenied)), []string{"a"}, 0},
		{"a developer handing work to an agent it was not given", developer, read(backend.ToolPolicyDeveloper,
			call("a", "Task", backend.ToolKindAgent, backend.ToolExecuted)), []string{"a"}, 0},
		{"a reviewer's reads", reviewer, read(backend.ToolPolicyInspection,
			call("a", "Grep", backend.ToolKindRead, backend.ToolExecuted),
			call("b", "Bash", backend.ToolKindShell, backend.ToolExecuted)), nil, 0},
		{"a reviewer writing", reviewer, read(backend.ToolPolicyInspection,
			call("a", "Write", backend.ToolKindWrite, backend.ToolDenied)), []string{"a"}, 0},
		{"any call under no tools", manager, read(backend.ToolPolicyNone,
			call("a", "Read", backend.ToolKindRead, backend.ToolExecuted),
			call("b", "Bash", backend.ToolKindShell, backend.ToolAttempted)), []string{"a", "b"}, 0},
		{"no calls on a complete reading", manager, read(backend.ToolPolicyNone), nil, 0},
		{"a reading with a limit", manager, func() backend.RunResult {
			result := read(backend.ToolPolicyNone)
			result.ToolVisibility.Gaps = []string{"an unread event"}
			return result
		}(), nil, 1},
		{"an adapter that read nothing", manager, backend.RunResult{Process: execution.ProcessResult{Status: execution.ProcessSucceeded}}, nil, 1},
		{"an invocation that never reached a provider", manager, backend.RunResult{}, nil, 0},
	}
	for _, tc := range cases {
		unexpected, gaps := tooluse.Check(tc.request, tc.result)
		var ids []string
		for _, found := range unexpected {
			ids = append(ids, found.ID)
		}
		if !reflect.DeepEqual(ids, tc.want) || len(gaps) != tc.gaps {
			t.Errorf("%s: unexpected %v, gaps %q; want %v and %d", tc.name, ids, gaps, tc.want, tc.gaps)
		}
	}
}

// One forbidden call is one durable, critical, clearly labelled report naming
// the role, provider, invocation, run, session, and tool; a denied one says it
// was refused and never that it ran; and nothing the agent was handed reaches
// it, nor a value the harness redacts.
func TestAForbiddenCallIsOneLabelledError(t *testing.T) {
	t.Parallel()
	reports := &pile{}
	request := backend.RunRequest{RunID: "run-1", Role: domain.RoleReviewer, LastSequence: 41, RedactValues: []string{"hunter2"}}
	result := read(backend.ToolPolicyInspection,
		call("call-w", "Write", backend.ToolKindWrite, backend.ToolDenied),
		call("call-hunter2", "Edit", backend.ToolKindWrite, backend.ToolExecuted))
	watch(reports).ObserveToolUse(runAttribution(), request, result)
	if len(reports.reports) != 2 {
		t.Fatalf("filed %d reports, want one for each forbidden call", len(reports.reports))
	}
	denied, executed := reports.reports[0], reports.reports[1]
	for _, reported := range reports.reports {
		if reported.Severity != report.SeverityCritical || reported.Role != report.HarnessReporter || reported.RunID != "run-1" ||
			reported.WorkItemID != "yoyodyne-ifd.1" || reported.ToolUse == "" || !strings.HasPrefix(reported.Message, tooluse.ErrorLabel) {
			t.Fatalf("report = %#v, want a critical harness report labelled as an error against the run", reported)
		}
		for _, want := range []string{"reviewer", "claude-code", "run run-1 for work item yoyodyne-ifd.1", "invocation run-1", "session session-1", "after event 41", "read-only inspection", "a fresh turn"} {
			if !strings.Contains(reported.Message, want) {
				t.Errorf("message %q does not name %q", reported.Message, want)
			}
		}
		if strings.Contains(reported.Message, "hunter2") {
			t.Errorf("message %q carries a redacted value", reported.Message)
		}
	}
	if !strings.Contains(denied.Message, `tried to use, and was refused, the "Write" tool`) || strings.Contains(denied.Message, " used the") {
		t.Errorf("denied call reported as %q, want it said as refused", denied.Message)
	}
	if !strings.Contains(executed.Message, `used the "Edit" tool`) {
		t.Errorf("executed call reported as %q", executed.Message)
	}
}

// The same call observed again -- a replayed stream, a resumed read of the
// same session, the same invocation handed over twice -- files nothing more,
// and a distinct call in the same session still files its own.
func TestACallIsReportedOnce(t *testing.T) {
	t.Parallel()
	reports := &pile{}
	w := watch(reports)
	request := backend.RunRequest{RunID: "chat-1", Role: domain.RoleProductManager}
	attribution := spend.Attribution{ProductID: "yoyodyne", Backend: domain.BackendCodex, ConversationID: "chat-1"}
	first := read(backend.ToolPolicyNone, call("call-1", "shell", backend.ToolKindShell, backend.ToolExecuted))
	w.ObserveToolUse(attribution, request, first)
	w.ObserveToolUse(attribution, request, first)
	resumed := request
	resumed.SessionID = "session-1"
	resumed.LastSequence = 90
	w.ObserveToolUse(attribution, resumed, read(backend.ToolPolicyNone,
		call("call-1", "shell", backend.ToolKindShell, backend.ToolExecuted),
		call("call-2", "shell", backend.ToolKindShell, backend.ToolExecuted)))
	if len(reports.reports) != 2 {
		t.Fatalf("filed %d reports, want one per distinct call", len(reports.reports))
	}
	if !strings.Contains(reports.reports[1].Message, "call call-2") || !strings.Contains(reports.reports[1].Message, "a resumed turn") {
		t.Fatalf("second report = %q, want the second call on the resumed turn", reports.reports[1].Message)
	}
}

// What could not be seen is one warning for the invocation, naming the
// provider, the invocation, and the limit, and saying it is not a claim that
// no tool was used.
func TestALimitIsOneWarningThatClaimsNothing(t *testing.T) {
	t.Parallel()
	reports := &pile{}
	result := read(backend.ToolPolicyInspection)
	result.Backend = domain.BackendCodex
	result.ToolVisibility.Gaps = []string{`Codex wrote a "item.started" event the harness does not read`, "another limit"}
	request := backend.RunRequest{RunID: "side-1", Role: domain.RoleArchitect, LastSequence: 7}
	attribution := spend.Attribution{ProductID: "yoyodyne", Backend: domain.BackendCodex, SideStreamID: "side-1"}
	watch(reports).ObserveToolUse(attribution, request, result)
	watch(reports).ObserveToolUse(attribution, request, result)
	if len(reports.reports) != 1 {
		t.Fatalf("filed %d reports, want one warning for the invocation", len(reports.reports))
	}
	warning := reports.reports[0]
	if warning.Severity != report.SeverityWarning || !strings.HasPrefix(warning.Message, tooluse.GapLabel) || warning.RunID != "side-1" {
		t.Fatalf("report = %#v", warning)
	}
	for _, want := range []string{"codex", "side thread side-1", "after event 7", "item.started", "another limit", "does not mean no tools were used"} {
		if !strings.Contains(warning.Message, want) {
			t.Errorf("message %q does not name %q", warning.Message, want)
		}
	}
}

// Each invocation path files against the record it belongs to, and an
// exchange or a side turn keeps its own identity and where in its log the turn
// began.
func TestEachInvocationPathIsNamed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		attribution spend.Attribution
		role        domain.AgentRole
		owner, says string
	}{
		{spend.Attribution{RunID: "run-9", WorkItemID: "yoyodyne-ifd.2"}, domain.RoleDeveloper, "run-9", "run run-9 for work item yoyodyne-ifd.2"},
		{spend.Attribution{RunID: "run-9", WorkItemID: "yoyodyne-ifd.2"}, domain.RoleReviewer, "run-9", "run run-9"},
		{spend.Attribution{BranchReviewID: "branch-review-3"}, domain.RoleReviewer, "branch-review-3", "branch review branch-review-3"},
		{spend.Attribution{ConversationID: "chat-4"}, domain.RoleDevelopmentManager, "chat-4", "conversation chat-4"},
		{spend.Attribution{SideStreamID: "side-5"}, domain.RoleArchitect, "side-5", "side thread side-5"},
		{spend.Attribution{ExchangeID: "exchange-6"}, domain.RoleProgramManager, "exchange-6", "exchange exchange-6"},
	} {
		reports := &pile{}
		tc.attribution.ProductID = "yoyodyne"
		tc.attribution.Backend = domain.BackendClaudeCode
		watch(reports).ObserveToolUse(tc.attribution, backend.RunRequest{RunID: tc.owner, Role: tc.role, LastSequence: 12},
			read(backend.ToolPolicyNone, call("c", "WebSearch", backend.ToolKindWeb, backend.ToolExecuted)))
		if len(reports.reports) != 1 || reports.reports[0].RunID != tc.owner || !strings.Contains(reports.reports[0].Message, tc.says) ||
			!strings.Contains(reports.reports[0].Message, "after event 12") {
			t.Errorf("%s: reports = %#v, want one against %s saying %q", tc.says, reports.reports, tc.owner, tc.says)
		}
	}
}

func TestManyCallsAreBounded(t *testing.T) {
	t.Parallel()
	reports := &pile{}
	var calls []backend.ToolCall
	for index := 0; index < 13; index++ {
		calls = append(calls, call(fmt.Sprintf("c%d", index), "Read", backend.ToolKindRead, backend.ToolExecuted))
	}
	watch(reports).ObserveToolUse(runAttribution(), backend.RunRequest{RunID: "run-1", Role: domain.RoleArchitect}, read(backend.ToolPolicyNone, calls...))
	if len(reports.reports) != 11 || !strings.Contains(reports.reports[10].Message, "made 3 more calls") {
		t.Fatalf("filed %d reports, last %q; want ten and one saying three more", len(reports.reports), reports.reports[len(reports.reports)-1].Message)
	}
}

// The report is durable in the product's pile, keeps its call identity when
// read back, and reaches the operator's channel as a critical report in the
// harness's voice -- the same path the status listing and the dashboard read.
func TestTheErrorIsDurableAndReachesTheChannel(t *testing.T) {
	t.Parallel()
	store, err := runstate.NewReportStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	w := watch(store)
	request := backend.RunRequest{RunID: "run-1", Role: domain.RoleProductManager}
	result := read(backend.ToolPolicyNone, call("c", "Bash", backend.ToolKindShell, backend.ToolExecuted))
	w.ObserveToolUse(runAttribution(), request, result)
	// Another process reading the same pile files nothing more.
	watch(store).ObserveToolUse(runAttribution(), request, result)
	listed, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ToolUse == "" {
		t.Fatalf("pile = %#v, want one report keeping its call identity", listed)
	}
	// The attention line the status listing and the dashboard print names it.
	if waiting := readmodel.AddressedReports(listed, nil); len(waiting) != 1 || waiting[0].ID != listed[0].ID {
		t.Fatalf("attention line lists %#v, want the error named", waiting)
	}
	notification, err := notify.FromReport(listed[0])
	if err != nil {
		t.Fatal(err)
	}
	if notification.Event.Kind != notify.KindReportFiled || notification.Event.Severity != report.SeverityCritical ||
		!strings.HasPrefix(notification.Event.Text, tooluse.ErrorLabel) {
		t.Fatalf("notification = %#v, want a critical report that reads as an error", notification.Event)
	}
}

// A report that cannot be filed is said, and is never the invocation's
// failure.
func TestAFilingFailureIsSaidAndNotTheInvocations(t *testing.T) {
	t.Parallel()
	var said []error
	w := tooluse.Watch{Reports: failingPile{}, Problem: func(err error) { said = append(said, err) }}
	w.ObserveToolUse(runAttribution(), backend.RunRequest{RunID: "run-1", Role: domain.RoleArchitect},
		read(backend.ToolPolicyNone, call("c", "Read", backend.ToolKindRead, backend.ToolExecuted)))
	if len(said) != 1 || !strings.HasPrefix(said[0].Error(), "ERROR:") {
		t.Fatalf("said %v, want the failure said once as an error", said)
	}
}

type failingPile struct{}

func (failingPile) Append(report.Report) error     { return errors.New("disk full") }
func (failingPile) List() ([]report.Report, error) { return nil, nil }

// provider is a provider that answers with the same result and error every
// time.
type provider struct {
	result backend.RunResult
	err    error
}

func (p provider) Run(context.Context, backend.RunRequest) (backend.RunResult, error) {
	return p.result, p.err
}

// Reporting is not enforcement: an invocation reported for unexpected tool use
// returns exactly the result and the error it would have returned unobserved,
// verdict text and all.
func TestReportingLeavesTheInvocationUnchanged(t *testing.T) {
	t.Parallel()
	result := read(backend.ToolPolicyNone, call("c", "Write", backend.ToolKindWrite, backend.ToolExecuted))
	result.FinalText = `{"verdict":"approve"}`
	for _, failure := range []error{nil, errors.New("provider failed")} {
		request := backend.RunRequest{RunID: "run-1", Role: domain.RoleReviewer}
		plain, plainErr := spend.Metered{Provider: provider{result, failure}, Attribution: runAttribution()}.Run(context.Background(), request)
		reports := &pile{}
		observed, observedErr := spend.Metered{Provider: provider{result, failure}, Attribution: runAttribution(), ToolUse: watch(reports)}.Run(context.Background(), request)
		if len(reports.reports) != 1 {
			t.Fatalf("filed %d reports, want the forbidden call reported", len(reports.reports))
		}
		if !reflect.DeepEqual(plain, observed) || fmt.Sprint(plainErr) != fmt.Sprint(observedErr) {
			t.Fatalf("observed invocation returned %#v, %v; unobserved returned %#v, %v", observed, observedErr, plain, plainErr)
		}
	}
}

// Every invocation the harness makes goes through spend.Metered, and every
// place outside a test that builds one hands it the tool-use observer. An
// invocation path added without it fails here rather than going unwatched.
func TestEveryMeteredInvocationIsWatched(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..")
	var unwatched []string
	sites := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isMetered(literal.Type, file.Name.Name) {
				return true
			}
			sites++
			for _, element := range literal.Elts {
				if pair, ok := element.(*ast.KeyValueExpr); ok {
					if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "ToolUse" {
						return true
					}
				}
			}
			unwatched = append(unwatched, path)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sites == 0 {
		t.Fatal("found no metered invocation at all, so this test is reading the wrong tree")
	}
	if len(unwatched) > 0 {
		t.Fatalf("spend.Metered built without ToolUse in %v", unwatched)
	}
}

func isMetered(expression ast.Expr, pkg string) bool {
	switch typed := expression.(type) {
	case *ast.SelectorExpr:
		qualifier, ok := typed.X.(*ast.Ident)
		return ok && qualifier.Name == "spend" && typed.Sel.Name == "Metered"
	case *ast.Ident:
		return pkg == "spend" && typed.Name == "Metered"
	}
	return false
}
