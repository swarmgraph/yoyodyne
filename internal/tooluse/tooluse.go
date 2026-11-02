// Package tooluse compares the tool calls an invocation's stream showed with
// the tool access the invocation was launched under, and reports every call
// that does not match as an ERROR where the operator looks.
//
// It detects and reports, and does nothing else. Nothing here blocks, cancels,
// retries, or changes what an invocation was allowed to do, and nothing it finds
// changes the invocation's result, its verdict, or whether its change can be
// integrated: the sandboxes and tool lists each adapter launches a session under
// are what stop a tool, and they stay exactly as they were. What this adds is
// that a call one of them let through, or refused, is seen.
//
// The report it files is the harness's own critical report (report.Report with
// report.HarnessReporter), which is what already reaches the status listing,
// the dashboard, and the Slack channel. Each one carries the stable identity of
// the call it is about, so a call the stream reported twice, a resumed read of
// the same session, or the same invocation observed again files nothing more.
package tooluse

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// ErrorLabel opens every report of unexpected tool use, so it reads as an
// error on a surface that can render nothing but the words.
const ErrorLabel = "ERROR: unexpected tool use."

// GapLabel opens every report of tool use the harness could not fully see.
const GapLabel = "Tool use not fully observed."

// maxCallReports bounds how many calls one invocation files a report each for.
// Past it, one more report says how many more there were, so a session that
// called a forbidden tool a hundred times is one screen rather than a hundred.
const maxCallReports = 10

// Reports is the pile the reports are filed into and read back from to keep
// one report per call. runstate.ReportStore satisfies it.
type Reports interface {
	Append(reported report.Report) error
	List() ([]report.Report, error)
}

// Watch is the observer every metered invocation is handed to.
type Watch struct {
	Reports      Reports
	RepositoryID string
	// Build is the harness revision making the invocation, empty where the
	// binary recorded none.
	Build string
	Clock execution.Clock
	// Problem is handed a report that could not be filed. It is never the
	// invocation's failure; nil writes it to standard error, so it is not lost.
	Problem func(error)
	// Show, where set, is where each report is also printed, one line, as soon
	// as it is filed, so a person watching the command that made the
	// invocation sees it there and not only in the pile. The line is the
	// report's own text, which opens with ErrorLabel or GapLabel.
	Show io.Writer
}

// Finding is one call that does not match the invocation's tool policy.
type Finding struct {
	Key  string
	Call backend.ToolCall
}

// Check is the comparison itself: every call the stream showed that the policy
// the adapter launched the session under does not permit, and every limit on
// what the stream showed. A result whose adapter did not read the stream for
// tool calls is a limit rather than a session that used none.
func Check(request backend.RunRequest, result backend.RunResult) (unexpected []backend.ToolCall, gaps []string) {
	visibility := result.ToolVisibility
	if !visibility.Read {
		if ran(result) {
			gaps = append(gaps, "the adapter did not report the tool calls this invocation made, so whether it used any is not known")
		}
		return nil, gaps
	}
	policy := visibility.Policy
	if policy == "" {
		policy = policyFor(request)
	}
	for _, call := range result.ToolCalls {
		if !policy.Permits(call.Kind, visibility.Subagents) {
			unexpected = append(unexpected, call)
		}
	}
	return unexpected, append(gaps, visibility.Gaps...)
}

// ran says the adapter reached the provider, which is the only invocation
// whose tool use there is anything to say about.
func ran(result backend.RunResult) bool {
	return result.Process.Status != "" || result.SessionID != "" || result.FinalText != ""
}

// policyFor is the policy a result that named none is compared with: the
// developer's tools for a session that writes its worktree, and none for any
// other, which is the narrowest reading of a session nobody described.
func policyFor(request backend.RunRequest) backend.ToolPolicy {
	if posture, err := backend.RequestPosture(request); err == nil && posture == backend.PostureWorktreeWrite {
		return backend.ToolPolicyDeveloper
	}
	return backend.ToolPolicyNone
}

// ObserveToolUse compares one invocation's tool calls with its policy and files
// what does not match. Nothing it does reaches the invocation.
func (w Watch) ObserveToolUse(attribution spend.Attribution, request backend.RunRequest, result backend.RunResult) {
	if err := w.observe(attribution, request, result); err != nil {
		w.problem(fmt.Errorf("ERROR: report unexpected tool use by the %s on invocation %s: %w", roleWords(request.Role), request.RunID, err))
	}
}

func (w Watch) observe(attribution spend.Attribution, request backend.RunRequest, result backend.RunResult) error {
	unexpected, gaps := Check(request, result)
	if len(unexpected) == 0 && len(gaps) == 0 {
		return nil
	}
	if w.Reports == nil {
		return fmt.Errorf("there is no report pile to file %d unexpected call(s) and %d limit(s) in", len(unexpected), len(gaps))
	}
	existing, err := w.Reports.List()
	if err != nil {
		return fmt.Errorf("read the report pile: %w", err)
	}
	filed := map[string]bool{}
	for _, reported := range existing {
		if reported.ToolUse != "" {
			filed[reported.ToolUse] = true
		}
	}
	invocation := describe(attribution, request, result)
	redactor := execution.NewRedactor(request.RedactValues...)
	var entries []pending
	var fresh []pending
	for index, call := range unexpected {
		key := callKey(attribution, request, result, call, index)
		if filed[key] {
			continue
		}
		filed[key] = true
		fresh = append(fresh, pending{key: key, severity: report.SeverityCritical, message: callMessage(request, result, call, invocation)})
	}
	if len(fresh) > maxCallReports {
		more := fresh[maxCallReports]
		entries = append(fresh[:maxCallReports:maxCallReports], pending{
			key:      more.key + "+more",
			severity: report.SeverityCritical,
			message: fmt.Sprintf("%s The %s made %d more calls %s does not permit than are reported one by one, on %s. "+
				"The invocation's event log names every one of them.",
				ErrorLabel, roleWords(request.Role), len(fresh)-maxCallReports, policyWords(result, request), invocation),
		})
	} else {
		entries = fresh
	}
	if len(gaps) > 0 {
		key := gapKey(attribution, request, result)
		if !filed[key] {
			entries = append(entries, pending{key: key, severity: report.SeverityWarning, message: gapMessage(request, gaps, invocation)})
		}
	}
	for _, entry := range entries {
		collected, err := report.Collect([]report.Entry{{Severity: entry.severity, Message: oneline.Bound(redactor.Redact(entry.message), report.MaxMessageBytes)}}, report.Attribution{
			Role:         report.HarnessReporter,
			RunID:        owner(attribution, request),
			WorkItemID:   attribution.WorkItemID,
			Build:        w.Build,
			ProductID:    attribution.ProductID,
			RepositoryID: w.RepositoryID,
		}, w.now())
		if err != nil {
			return err
		}
		for _, reported := range collected {
			reported.ToolUse = entry.key
			if err := w.Reports.Append(reported); err != nil {
				return err
			}
			if w.Show != nil {
				fmt.Fprintf(w.Show, "%s (report %s)\n", reported.Message, reported.ID)
			}
		}
	}
	return nil
}

type pending struct {
	key      string
	severity report.Severity
	message  string
}

// callMessage is the two sentences one unexpected call is reported in.
func callMessage(request backend.RunRequest, result backend.RunResult, call backend.ToolCall, invocation string) string {
	tool := call.Tool
	if strings.TrimSpace(tool) == "" {
		tool = "an unnamed tool"
	} else {
		tool = fmt.Sprintf("the %q tool", tool)
	}
	identity := ""
	if call.ID != "" {
		identity = fmt.Sprintf(", call %s", call.ID)
	}
	if call.Parent != "" {
		identity += fmt.Sprintf(", made inside call %s", call.Parent)
	}
	return fmt.Sprintf("%s The %s %s %s (%s) on %s%s, though it was launched with %s. "+
		"This is reported only: the invocation carried on and its outcome is unchanged, so somebody has to read its event log to see why the tool was used.",
		ErrorLabel, roleWords(request.Role), outcomeWords(call.Outcome), tool, kindWords(call.Kind), invocation, identity, policyWords(result, request))
}

// gapMessage is the two sentences an invocation's unobserved tool use is
// reported in.
func gapMessage(request backend.RunRequest, gaps []string, invocation string) string {
	return fmt.Sprintf("%s For the %s on %s: %s. "+
		"This does not mean no tools were used; it means the harness could not see whether they were, so unexpected tool use there would not be reported.",
		GapLabel, roleWords(request.Role), invocation, strings.Join(gaps, "; "))
}

// describe names the invocation the way a person finds it again: which
// provider, which record it belongs to, the invocation and its session, and
// where in that record's event log it began.
func describe(attribution spend.Attribution, request backend.RunRequest, result backend.RunResult) string {
	var parts []string
	provider := string(attribution.Backend)
	if provider == "" {
		provider = string(result.Backend)
	}
	if provider == "" {
		provider = "an unnamed provider"
	}
	parts = append(parts, provider)
	switch {
	case attribution.RunID != "":
		where := "run " + attribution.RunID
		if attribution.WorkItemID != "" {
			where += " for work item " + attribution.WorkItemID
		}
		parts = append(parts, where)
	case attribution.BranchReviewID != "":
		parts = append(parts, "branch review "+attribution.BranchReviewID)
	case attribution.ConversationID != "":
		parts = append(parts, "conversation "+attribution.ConversationID)
	case attribution.SideStreamID != "":
		parts = append(parts, "side thread "+attribution.SideStreamID)
	case attribution.ExchangeID != "":
		parts = append(parts, "exchange "+attribution.ExchangeID)
	}
	if within := strings.TrimSpace(attribution.Within); within != "" {
		parts = append(parts, within)
	}
	parts = append(parts, "invocation "+request.RunID)
	if session := strings.TrimSpace(result.SessionID); session != "" {
		parts = append(parts, "session "+session)
	}
	if request.SessionID != "" {
		parts = append(parts, "a resumed turn")
	} else {
		parts = append(parts, "a fresh turn")
	}
	parts = append(parts, fmt.Sprintf("after event %d of its log", request.LastSequence))
	return strings.Join(parts, ", ")
}

// owner is the record the report is filed against: the run, conversation, side
// thread, exchange, or branch review the invocation belongs to.
func owner(attribution spend.Attribution, request backend.RunRequest) string {
	for _, id := range []string{attribution.RunID, attribution.ConversationID, attribution.SideStreamID, attribution.ExchangeID, attribution.BranchReviewID} {
		if id != "" {
			return id
		}
	}
	return request.RunID
}

// callKey is one call's stable identity. A provider's call identifier is
// unique within its session, so provider, session, and call name the call
// however often it is reported; a call whose identifier is unique only within
// one invocation, or that has none, is named with where it was observed too.
func callKey(attribution spend.Attribution, request backend.RunRequest, result backend.RunResult, call backend.ToolCall, index int) string {
	scope := strings.TrimSpace(result.SessionID)
	if scope == "" {
		scope = owner(attribution, request) + "/" + request.RunID
	}
	id := call.ID
	switch {
	case id == "":
		id = fmt.Sprintf("@%d#%d", request.LastSequence, index)
	case call.InvocationID:
		// The provider numbers its calls afresh in every invocation, so the
		// invocation is part of the call's name: the same invocation observed
		// again is the same call, and a resumed turn's item_0 is a new one.
		id = fmt.Sprintf("%s@%d", id, request.LastSequence)
	}
	return oneline.Bound(fmt.Sprintf("tool-call:%s:%s:%s", attribution.Backend, scope, id), 1<<10)
}

// gapKey is one invocation's identity, for its single report of what could not
// be seen.
func gapKey(attribution spend.Attribution, request backend.RunRequest, result backend.RunResult) string {
	return oneline.Bound(fmt.Sprintf("tool-gap:%s:%s/%s@%d", attribution.Backend, owner(attribution, request), request.RunID, request.LastSequence), 1<<10)
}

func outcomeWords(outcome backend.ToolOutcome) string {
	switch outcome {
	case backend.ToolDenied:
		return "tried to use, and was refused,"
	case backend.ToolExecuted:
		return "used"
	case backend.ToolFailed:
		return "used, and it reported a failure,"
	default:
		return "started, with no result seen for,"
	}
}

func kindWords(kind backend.ToolKind) string {
	switch kind {
	case backend.ToolKindRead:
		return "it reads files"
	case backend.ToolKindShell:
		return "it runs commands"
	case backend.ToolKindWrite:
		return "it changes files"
	case backend.ToolKindWeb:
		return "it reaches the network"
	case backend.ToolKindExternal:
		return "it is served from outside the provider"
	case backend.ToolKindAgent:
		return "it hands work to another agent"
	case backend.ToolKindSession:
		return "it keeps notes inside the session"
	default:
		return "nothing records what it does"
	}
}

func policyWords(result backend.RunResult, request backend.RunRequest) string {
	policy := result.ToolVisibility.Policy
	if policy == "" {
		policy = policyFor(request)
	}
	return policy.Describe()
}

func roleWords(role domain.AgentRole) string {
	if role == "" {
		return "agent"
	}
	return strings.ReplaceAll(string(role), "-", " ")
}

func (w Watch) now() time.Time {
	if w.Clock != nil {
		return w.Clock.Now()
	}
	return time.Now()
}

func (w Watch) problem(err error) {
	if w.Problem != nil {
		w.Problem(err)
		return
	}
	fmt.Fprintln(os.Stderr, err)
}
