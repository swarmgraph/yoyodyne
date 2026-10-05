package runstate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

const PassFailureThreshold = 3

// PassFailure is a run of failed passes, derived from the sweep log. A later
// failure changes its count, never its identity; only its own pass succeeding
// ends it. Completing the watcher's pass or handling its report does not.
type PassFailure struct {
	ProductID     domain.ProductID `json:"product_id"`
	Task          string           `json:"task"`
	Failures      int              `json:"failures"`
	FirstAt       time.Time        `json:"first_at"`
	RaisedAt      time.Time        `json:"raised_at"`
	LatestAt      time.Time        `json:"latest_at"`
	Role          domain.AgentRole `json:"role,omitempty"`
	Agent         string           `json:"agent,omitempty"`
	FailureOutput string           `json:"failure_output,omitempty"`
	Problem       string           `json:"problem"`
	ClearedAt     time.Time        `json:"cleared_at,omitempty"`
}

func (f PassFailure) ReportID(product domain.ProductID) string {
	sum := sha256.Sum256([]byte(string(product) + "\n" + f.Task + "\n" + f.FirstAt.UTC().Format(time.RFC3339Nano)))
	return fmt.Sprintf("report-%x", sum[:16])
}

func (f PassFailure) Says() string {
	said := fmt.Sprintf("the product pass %s has failed %d times in a row since %s; latest: %s",
		f.Task, f.Failures, f.FirstAt.In(time.Local).Format("2006-01-02 15:04 MST"), lastPassErrorLine(f.Problem))
	if f.FailureOutput != "" {
		said += "\nLast session output:\n" + f.FailureOutput
	}
	return said
}

func lastPassErrorLine(problem string) string {
	line := strings.Join(strings.Fields(problem), " ")
	if len(line) <= 512 {
		return line
	}
	cut := len(line) - 512
	for !utf8.RuneStart(line[cut]) {
		cut++
	}
	return "..." + line[cut:]
}

func PassFailureOwnersSays(owner ownership.PassFailure) string {
	watcher := "the factory-flow program manager " + owner.Agent
	if owner.Fallback {
		watcher = "the development manager (no factory-flow program manager is configured)"
	}
	said := watcher + " watches and must answer this finding; the development manager resolves the cause"
	if owner.Resolver == domain.RoleProgramManager {
		said = watcher + " must answer this finding and resolve the cause because the development manager’s own pass is failing"
	}
	if owner.Resolver == domain.RoleProductManager {
		said = "the Lead Product Manager must answer this finding and resolve the cause because the role that would answer it has its own pass failing"
	}
	if owner.PersonStep != "" {
		said += "; the operator must " + strings.Join(strings.Fields(owner.PersonStep), " ")
	}
	return said
}

// PassFailuresOf returns raised failure runs, including their later successful
// endings. Missed cadences and held conversations are observations rather than
// failed executions. A partial account is progress, but clears nothing until a
// completed pass is recorded.
func PassFailuresOf(passes []Sweep) []PassFailure {
	ordered := append([]Sweep(nil), passes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartedAt.Before(ordered[j].StartedAt) })
	active := map[string]PassFailure{}
	var ended []PassFailure
	for _, pass := range ordered {
		if pass.Missed != nil && pass.Missed.How != MissCancelled {
			continue
		}
		failed, succeeded := passFailedOrSucceeded(pass)
		f := active[pass.Task]
		if failed {
			if f.Failures == 0 {
				f.ProductID, f.Task, f.FirstAt = pass.ProductID, pass.Task, pass.StartedAt
			}
			f.Failures++
			f.LatestAt, f.Problem = pass.StartedAt, pass.Problem
			f.Role, f.Agent, f.FailureOutput = pass.Role, pass.Agent, pass.FailureOutput
			for _, step := range pass.Steps {
				if step.Outcome == StepFailed {
					f.Problem = step.Name + ": " + step.Detail
				}
			}
			if f.Failures == PassFailureThreshold {
				f.RaisedAt = pass.EndedAt
			}
			active[pass.Task] = f
		} else if succeeded {
			if f.Failures >= PassFailureThreshold {
				f.ClearedAt = pass.EndedAt
				ended = append(ended, f)
			}
			delete(active, pass.Task)
		}
	}
	for _, f := range active {
		if f.Failures >= PassFailureThreshold {
			ended = append(ended, f)
		}
	}
	sort.Slice(ended, func(i, j int) bool {
		if ended[i].Task == ended[j].Task {
			return ended[i].FirstAt.Before(ended[j].FirstAt)
		}
		return ended[i].Task < ended[j].Task
	})
	return ended
}

func passFailedOrSucceeded(pass Sweep) (bool, bool) {
	if pass.HarnessPass() {
		for _, step := range pass.Steps {
			if step.Outcome == StepFailed {
				return true, false
			}
		}
		return pass.Problem != "", pass.Problem == ""
	}
	if pass.Failed || pass.NotStarted != "" || pass.Missed != nil || (pass.Turns > 0 && pass.Result == nil) {
		return true, false
	}
	return false, pass.Result != nil && pass.Result.Status == "complete"
}

// RecordPassFailures files and clears findings in the existing report logs.
// It runs after a pass is appended, under the sweep store's lock, so two
// processes cannot file the same finding. The deterministic ID also allows a
// retry after a report was written but its caller died or failed to sync.
// A run that recovered before delivery still gets both its finding and clearing.
func (s *SweepStore) RecordPassFailures(ctx context.Context, reports *ReportStore, attribution report.Attribution, watcher string) error {
	if reports == nil {
		return nil
	}
	release, err := s.lock(ctx, "pass-failure-findings")
	if err != nil {
		return err
	}
	defer release()
	passes, unreadable, err := s.List()
	if err != nil {
		return err
	}
	if len(unreadable) > 0 {
		return fmt.Errorf("the sweep log has %d unreadable line(s), so failure findings cannot be filed or cleared safely", len(unreadable))
	}
	filed, err := reports.List()
	if err != nil {
		return err
	}
	handlings, err := reports.Handlings()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	cleared := map[string]bool{}
	for _, r := range filed {
		known[r.ID] = true
	}
	for _, h := range handlings {
		if h.Role == report.HarnessReporter && h.PassFailureCleared >= PassFailureThreshold {
			cleared[h.ReportID] = true
		}
	}
	for _, f := range PassFailuresOf(passes) {
		id := f.ReportID(s.productID)
		if !known[id] {
			r := report.Report{SchemaVersion: report.SchemaVersion, ID: id, Role: report.HarnessReporter,
				RunID: f.Task + "@" + f.FirstAt.UTC().Format(time.RFC3339Nano), ProductID: s.productID,
				RepositoryID: attribution.RepositoryID, Build: attribution.Build, Severity: report.SeverityWarning,
				PassFailureTask: f.Task, RecordedAt: f.RaisedAt, Message: f.Says() + ". " + PassFailureOwnersSays(ownership.ResolvePassFailureForRole(watcher, f.Role, f.Agent, nil)) + "."}
			if err := reports.Append(r); err != nil {
				return err
			}
			known[id] = true
		}
		if known[id] && !f.ClearedAt.IsZero() && !cleared[id] {
			if err := reports.Handle(report.Handling{SchemaVersion: report.HandlingSchemaVersion, PassFailureCleared: f.Failures, ReportID: id, Role: report.HarnessReporter,
				RunID: f.Task + "@" + f.ClearedAt.UTC().Format(time.RFC3339Nano), ProductID: s.productID, RepositoryID: attribution.RepositoryID,
				RecordedAt: f.ClearedAt, Reason: fmt.Sprintf("The product pass %s succeeded at %s, clearing the finding after %d consecutive failures.", f.Task, f.ClearedAt.In(time.Local).Format("2006-01-02 15:04 MST"), f.Failures)}); err != nil {
				return err
			}
		}
	}
	return nil
}

// FailureOutputTail retains a small, readable end of already redacted output.
func FailureOutputTail(output string) string {
	const limit = 2560
	if len(output) <= limit {
		return output
	}
	cut := len(output) - limit
	for cut < len(output) && !utf8.RuneStart(output[cut]) {
		cut++
	}
	return "[earlier output omitted; retaining the last 2560 bytes]\n" + output[cut:]
}
