package runstate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
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
// failure changes its count, never its identity; only its own pass carrying out
// its work ends it, whether that pass finished or reported more work waiting.
// Completing the watcher's pass or handling its report does not.
type PassFailure struct {
	ProductID domain.ProductID `json:"product_id"`
	Task      string           `json:"task"`
	Failures  int              `json:"failures"`
	FirstAt   time.Time        `json:"first_at"`
	RaisedAt  time.Time        `json:"raised_at"`
	LatestAt  time.Time        `json:"latest_at"`
	// WentWrong is what stopped the latest failed pass, in ordinary words, and
	// Problem is that pass's record. A line about the failure leads with the
	// first, because the record is written for whoever debugs the harness.
	WentWrong string    `json:"went_wrong,omitempty"`
	Problem   string    `json:"problem"`
	ClearedAt time.Time `json:"cleared_at,omitempty"`
}

func (f PassFailure) ReportID(product domain.ProductID) string {
	sum := sha256.Sum256([]byte(string(product) + "\n" + f.Task + "\n" + f.FirstAt.UTC().Format(time.RFC3339Nano)))
	return fmt.Sprintf("report-%x", sum[:16])
}

func (f PassFailure) Says() string {
	said := fmt.Sprintf("the product pass %s has failed %d times in a row since %s; latest: %s",
		f.Task, f.Failures, f.FirstAt.In(time.Local).Format("2006-01-02 15:04 MST"), lastPassErrorLine(f.Problem))
	if wrong := strings.Join(strings.Fields(f.WentWrong), " "); wrong != "" {
		said = wrong + ": " + said
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
	if owner.PersonStep != "" {
		said += "; the operator must " + strings.Join(strings.Fields(owner.PersonStep), " ")
	}
	return said
}

// PassFailuresOf returns raised failure runs, including their later endings.
// Missed cadences and held conversations are observations rather than failed
// executions. A partial pass — one that carried out its actions, gave its
// account, and said more work is waiting — is not a failure, and it ends a run
// of them as a completed pass does: a role whose queue is never empty would
// otherwise hold a finding that can never clear.
func PassFailuresOf(passes []Sweep) []PassFailure {
	ordered := append([]Sweep(nil), passes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartedAt.Before(ordered[j].StartedAt) })
	active := map[string]PassFailure{}
	var ended []PassFailure
	for _, pass := range ordered {
		if pass.Missed != nil && pass.Missed.How != MissCancelled {
			continue
		}
		failed, worked := passFailedOrWorked(pass)
		f := active[pass.Task]
		if failed {
			if f.Failures == 0 {
				f.ProductID, f.Task, f.FirstAt = pass.ProductID, pass.Task, pass.StartedAt
			}
			f.Failures++
			f.LatestAt, f.Problem, f.WentWrong = pass.StartedAt, pass.Problem, PassWentWrong(pass)
			for _, step := range pass.Steps {
				if step.Outcome == StepFailed {
					f.Problem = step.Name + ": " + step.Detail
				}
			}
			if f.Failures == PassFailureThreshold {
				f.RaisedAt = pass.EndedAt
			}
			active[pass.Task] = f
		} else if worked {
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

// passFailedOrWorked says whether a pass failed, and whether it did its work.
// A role's pass fails only where a turn's actions were not carried out, it
// never started, it was stopped under it, or it gave no account at all. Its
// account having come from the last of several report blocks, or saying more
// work waits, is neither: the pass did its work. A pass that took no turn and
// failed nothing — a wait on the provider — is neither either.
func passFailedOrWorked(pass Sweep) (bool, bool) {
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
	return false, pass.Result != nil
}

// PassWentWrong says in ordinary words what made a failed pass fail, for the
// front of every line about it. It reads what the record states as fields,
// and the harness's own sentences where a field does not say: a refused ask, an
// unreadable one, and a refused document are named, and any other turn that
// failed is said as a turn whose actions were not carried out.
func PassWentWrong(pass Sweep) string {
	role := pass.Role.Title()
	if role == "" {
		role = "role"
	}
	switch {
	case pass.HarnessPass():
		for _, step := range pass.Steps {
			if step.Outcome == StepFailed {
				return fmt.Sprintf("the %s step of the pass failed", step.Name)
			}
		}
		return "the pass reported a problem"
	case pass.NotStarted != "":
		return pass.NotStarted.Describe() + ", so nothing was asked of the " + role
	case pass.Missed != nil:
		return "the pass was stopped before it finished, so what it would have looked at waits for the next one"
	case !pass.Failed && pass.Turns > 0 && pass.Result == nil:
		return "the " + role + " gave no account of the pass, so what it found is only in its conversation"
	}
	turn := "a turn"
	if found := failedTurn.FindStringSubmatch(pass.Problem); found != nil {
		turn = "turn " + found[1]
	}
	if asked := refusedAsk.FindStringSubmatch(pass.Problem); asked != nil {
		return fmt.Sprintf("the %s asked the %s a question, which it may not do, so the actions of %s were not carried out", role, asked[1], turn)
	}
	switch {
	case strings.Contains(pass.Problem, "an ask the harness cannot read"):
		return fmt.Sprintf("the %s wrote a question for another role that the harness could not read, so the actions of %s were not carried out", role, turn)
	case strings.Contains(pass.Problem, "which that role has no authority for"):
		return fmt.Sprintf("the %s asked for something its role may not do, so the actions of %s were not carried out", role, turn)
	case strings.Contains(pass.Problem, "a document the harness will not record"):
		return fmt.Sprintf("the %s wrote a document the harness would not record, so the actions of %s were not carried out", role, turn)
	}
	return fmt.Sprintf("%s of the %s's pass stopped before its actions were carried out", turn, role)
}

var (
	failedTurn = regexp.MustCompile(`turn (\d+) of the recurring task \S+ failed`)
	refusedAsk = regexp.MustCompile(`asked for a question put to the ([a-z ]+?), which that role has no authority for`)
)

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
				PassFailureTask: f.Task, RecordedAt: f.RaisedAt, Message: f.Says() + ". " + PassFailureOwnersSays(ownership.ResolvePassFailure(watcher, nil)) + "."}
			if err := reports.Append(r); err != nil {
				return err
			}
			known[id] = true
		}
		if known[id] && !f.ClearedAt.IsZero() && !cleared[id] {
			if err := reports.Handle(report.Handling{SchemaVersion: report.HandlingSchemaVersion, PassFailureCleared: f.Failures, ReportID: id, Role: report.HarnessReporter,
				RunID: f.Task + "@" + f.ClearedAt.UTC().Format(time.RFC3339Nano), ProductID: s.productID, RepositoryID: attribution.RepositoryID,
				RecordedAt: f.ClearedAt, Reason: fmt.Sprintf("The product pass %s carried out its work at %s, clearing the finding after %d consecutive failures.", f.Task, f.ClearedAt.In(time.Local).Format("2006-01-02 15:04 MST"), f.Failures)}); err != nil {
				return err
			}
		}
	}
	return nil
}
