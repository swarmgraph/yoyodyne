package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/budget"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epiccheck"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maincheck"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// epicChecks makes the check of the whole epic (internal/epiccheck) for this
// product, in a session of its own with the reviewer agent's model, account and
// provider, once status shows every member of a named epic done.
type epicChecks struct {
	parts  components
	runner execution.ProcessRunner
}

// epicCheckSource marks what the harness writes in a check's event log, before
// the call and after it, which is what a check is looked up in.
const epicCheckSource = "harness.epic-check"

func epicChecksFrom(parts components) orchestrator.ScheduleEpicChecks {
	if parts.branchReviews == nil || agentNameForRole(parts.config, domain.RoleReviewer) == "" {
		return nil
	}
	return epicChecks{parts, parts.runner}
}

// Owed answers no while the operator has paused what the harness spends.
func (e epicChecks) Owed(ctx context.Context) (bool, error) {
	if _, held, err := e.parts.holds.Held(); err != nil || held {
		return false, err
	}
	due, err := e.due(ctx)
	return len(due) > 0, err
}

// due is each open epic whose check the harness owes, from the same reading of
// where the epic stands that status shows.
func (e epicChecks) due(ctx context.Context) ([]epicscope.Epic, error) {
	record, err := epicscope.Read(e.parts.stateRoot, e.parts.config.Product.ID)
	if err != nil || !record.On() {
		return nil, err
	}
	standing := readmodel.ReadStanding(ctx, standingSources(e.parts.configPath))
	if standing.Epics == nil {
		return nil, nil
	}
	var due []epicscope.Epic
	for _, open := range standing.Epics.Open {
		if epic, named := record.Named(open.WorkItemID); named && epic.Open() && open.CheckDue() {
			due = append(due, epic)
		}
	}
	return due, nil
}

// Check makes each check that is owed, or finishes one begun under the same
// identifier. Where another process is making them it does nothing.
func (e epicChecks) Check(ctx context.Context) (cost float64, err error) {
	store, err := e.parts.epicProgress()
	if err != nil {
		return 0, err
	}
	release, held, err := store.Alone()
	if err != nil || !held {
		return 0, err
	}
	defer release()
	due, err := e.due(ctx)
	if err != nil {
		return 0, err
	}
	checker, err := e.checker()
	if err != nil {
		return 0, err
	}
	checker.Judge = func(ctx context.Context, request epiccheck.Request) (string, error) {
		return e.judge(ctx, request, func(line runstate.Spend) { cost += line.AmountUSD })
	}
	var problems []error
	for _, epic := range due {
		naming := epicprogress.Of(epic)
		given, id, err := e.given(ctx, epic)
		if err == nil {
			_, _, err = checker.Check(ctx, naming, id, given)
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("epic %s: %w", epic.WorkItemID, err))
		}
	}
	return cost, errors.Join(problems...)
}

func (e epicChecks) checker() (epiccheck.Checker, error) {
	store, err := e.parts.epicProgress()
	if err != nil {
		return epiccheck.Checker{}, err
	}
	cfg := e.parts.config
	commands := slices.Compact(append(append([]string(nil), cfg.Checks...), cfg.LandingChecks...))
	runs, err := runstate.NewMainCheckStore(e.parts.stateRoot, cfg.Product.ID)
	if err != nil {
		return epiccheck.Checker{}, err
	}
	return epiccheck.Checker{
		Progress:  store,
		MayStart:  e.parts.wouldStart(),
		LookUp:    e.lookUp,
		Commands:  commands,
		Queued:    e.queued,
		Recorded:  runs.List,
		RunChecks: e.runChecks,
	}, nil
}

// queued is every merge queue candidate that landed as the commit it was checked
// on, with its finished check run. A queue whose record cannot be read gives
// nothing, so the checks are run instead, which is never wrong.
func (e epicChecks) queued() ([]epiccheck.Queued, error) {
	store, err := runstate.NewMergeQueueStore(e.parts.stateRoot, e.parts.config.Product.ID)
	if err != nil {
		return nil, err
	}
	keys, _ := store.Keys()
	var landed []epiccheck.Queued
	for _, key := range keys {
		entries, err := store.Entries(key)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			landing, found, err := store.Landing(key, entry.EntryID)
			if err != nil || !found {
				continue
			}
			generations, err := store.Generations(key, entry.EntryID)
			if err != nil {
				continue
			}
			landed = append(landed, queuedOf(landing, generations)...)
		}
	}
	return landed, nil
}

// queuedOf is each attempt of a landing whose generation's own candidate is the
// commit that landed and whose checks finished with a result for every command.
// A candidate the forge combined into a commit of its own was not checked as the
// commit that landed, so it gives none.
func queuedOf(landing runstate.MergeQueueLanding, generations []runstate.MergeQueueGeneration) []epiccheck.Queued {
	var landed []epiccheck.Queued
	for _, attempt := range landing.Attempts {
		if attempt.Landed == nil {
			continue
		}
		at := slices.IndexFunc(generations, func(generation runstate.MergeQueueGeneration) bool { return generation.Number == attempt.Generation })
		if at < 0 {
			continue
		}
		generation := generations[at]
		run := generation.CheckRun
		if generation.Candidate != attempt.Landed.Commit || run == nil || run.FinishedAt == nil || run.Problem != "" {
			continue
		}
		queued := epiccheck.Queued{Commit: generation.Candidate, CheckSet: generation.Checks.Digest, At: *run.FinishedAt}
		for _, result := range run.Results {
			check := runstate.MainCheckResult{Command: result.Command, Outcome: runstate.MainCheckFailed, Detail: result.FailingTests}
			switch {
			case result.Passed:
				check.Outcome, check.Detail = runstate.MainCheckPassed, ""
			case result.CouldNotRun != "":
				check.Outcome, check.Detail = runstate.MainCheckCouldNotRun, result.CouldNotRun
			case check.Detail == "":
				check.Detail = fmt.Sprintf("it ended %s with exit code %d", result.Status, result.ExitCode)
			}
			queued.Checks = append(queued.Checks, check)
		}
		landed = append(landed, queued)
	}
	return landed
}

// checkout runs the checks in a worktree of the commit's own under the product's
// state, with the runner and limits every landing check runs under.
func (e epicChecks) checkout() *maincheck.Check {
	cfg := e.parts.config
	return &maincheck.Check{
		Repository: e.parts.repository,
		Remote:     cfg.Execution.Remote,
		WorkRoot:   filepath.Join(home.ProductDirectory(e.parts.stateRoot, string(cfg.Product.ID)), "epic-check"),
		Git:        e.parts.runner,
		Checks: checks.Runner{
			Process:           e.parts.runner,
			Timeout:           cfg.Execution.LandingCheckTimeout.Duration(),
			RedactValues:      e.parts.redactValues,
			HiddenExecutables: providerExecutables(cfg),
			Permits:           heavyCheckPermits(cfg, e.parts.stateRoot, e.parts.runner),
			FailingTests:      cfg.FailingTests,
		},
		CheckTimeout: cfg.Execution.LandingCheckTimeout.Duration(),
		Product:      cfg.Product.ID,
		Now:          time.Now,
	}
}

func (e epicChecks) runChecks(ctx context.Context, commit string, commands []string) ([]runstate.MainCheckResult, error) {
	checkout := e.checkout()
	checkout.Commands = commands
	return checkout.RunOn(ctx, commit, "check-"+commit)
}

// given is what the check of epic reads, and the identifier it is made under: the
// one begun and not finished, with the commits it read, or a new one reading the
// target branch's head, or each approved head where changes were handed over.
func (e epicChecks) given(ctx context.Context, epic epicscope.Epic) (epiccheck.Given, string, error) {
	record, err := epicscope.Read(e.parts.stateRoot, e.parts.config.Product.ID)
	if err != nil {
		return epiccheck.Given{}, "", err
	}
	view, err := epicprogress.Read(e.parts.stateRoot, e.parts.config.Product.ID, record)
	if err != nil {
		return epiccheck.Given{}, "", err
	}
	entry, _ := view.Entry(epicprogress.Of(epic))
	items, err := beads.ReadExport(e.parts.repository, nil)
	if err != nil {
		return epiccheck.Given{}, "", fmt.Errorf("read the tracker's item export: %w", err)
	}
	given := epiccheck.Given{Epic: epic, Entry: entry, Titles: map[string]string{}}
	children := map[string][]string{}
	for _, item := range items {
		given.Titles[item.ID] = item.Title
		if parent := item.DecomposedFrom(); parent != "" {
			children[parent] = append(children[parent], item.ID)
		}
	}
	for _, id := range view.Items(epic) {
		if epic.TakenOut(id) {
			continue
		}
		// A member done because the children it holds are is given with each
		// child's change as part of its own, at any depth; the children are not
		// members, and the sessions that wrote them may not make the check either.
		seen := map[string]bool{}
		for _, part := range slices.Concat([]string{id}, descendants(id, children, seen)) {
			runs, err := e.parts.store.Runs(part)
			if err != nil {
				return epiccheck.Given{}, "", fmt.Errorf("read the runs of %s: %w", part, err)
			}
			title := given.Titles[part]
			if part != id {
				title = fmt.Sprintf("%s, part of %s", title, id)
			}
			member, handed := memberOf(part, title, runs)
			given.Members = append(given.Members, member)
			if handed {
				given.HandedOver = true
				given.Commits = append(given.Commits, member.Change)
			}
		}
	}
	// The heads handed over now, before a check under way puts back the ones it read.
	given.Again = given.HandedOver && entry.Repaired(epic, given.Commits)
	id, underway := entry.CheckUnderway()
	if underway {
		if commits := entry.CheckCommits(id); len(commits) > 0 {
			given.Commits = commits
			return given, id, nil
		}
	} else if id, err = runstate.NewBranchReviewID(); err != nil {
		return epiccheck.Given{}, "", err
	}
	if !given.HandedOver {
		head, err := e.checkout().Head(ctx)
		if err != nil {
			return epiccheck.Given{}, "", fmt.Errorf("read the target branch: %w", err)
		}
		given.Commits = []string{head}
	}
	return given, id, nil
}

// descendants is every child an item holds in the tracker, at any depth, in order.
func descendants(id string, children map[string][]string, seen map[string]bool) []string {
	seen[id] = true
	var below []string
	for _, child := range slices.Sorted(slices.Values(children[id])) {
		if !seen[child] {
			below = append(below, child)
			below = append(below, descendants(child, children, seen)...)
		}
	}
	return below
}

// memberOf is one member as the check is given it, from its latest run that is its
// work: the commit its change landed as, or the approved head handed over, and its
// review; and every session that wrote or reviewed it. handed is a change still
// waiting on a person's merge.
func memberOf(id, title string, runs []runstate.State) (epiccheck.Member, bool) {
	member := epiccheck.Member{WorkItemID: id, Title: title, Review: "no review is recorded"}
	var work *runstate.State
	for i, run := range runs {
		member.Sessions = append(member.Sessions, run.ProviderSessionID, run.ReviewSessionID)
		if run.Discharges() && (work == nil || run.StartedAt.After(work.StartedAt)) {
			work = &runs[i]
		}
	}
	if work == nil {
		return member, false
	}
	member.Review = fmt.Sprintf("%s: %s", work.ReviewDecision, strings.TrimSpace(work.ReviewSummary))
	handed := false
	switch {
	case work.Integration != nil && work.Integration.TargetCommit != "":
		member.Change = work.Integration.TargetCommit
	case work.PullRequest != nil && work.PullRequest.MergeCommit != "":
		member.Change = work.PullRequest.MergeCommit
	case work.PullRequest != nil && work.PullRequest.HeadCommit != "":
		member.Change, handed = work.PullRequest.HeadCommit, !work.PullRequest.Merged
	case work.ReviewDecision == runstate.ReviewApprove && work.ReviewHeadCommit != "" && work.ReviewHeadCommit != work.ReviewBaseCommit:
		// An approved head a person brought onto the target branch by hand, which
		// is how the read model counts it landed.
		member.Change = work.ReviewHeadCommit
	}
	return member, handed
}

// ended is what the harness writes of a check's call once it has ended: the
// recorded answer, or why nothing answered. Failed is that a session was started,
// so the call may have cost something.
type ended struct {
	Recorded string `json:"recorded,omitempty"`
	Refused  string `json:"refused,omitempty"`
	Failed   bool   `json:"failed,omitempty"`
}

// maxCheckFailures is how many sessions may end without answering one check
// before nothing more is sent for it and the development manager decides, as for
// any call whose result cannot be known.
const maxCheckFailures = 6

// checkLog is how a check's log stands: the last sequence, how many sessions
// ended without answering, and what the last ending said.
type checkLog struct {
	sequence uint64
	failures int
	looked   epicprogress.Looked
	recorded string
}

func readCheckLog(events []execution.Event) checkLog {
	log := checkLog{looked: epicprogress.NotSent}
	for _, event := range events {
		log.sequence = max(log.sequence, event.Sequence)
		if event.Source != epicCheckSource {
			continue
		}
		var end ended
		switch {
		case event.Type == execution.EventReviewStarted:
			log.looked, log.recorded = epicprogress.CannotTell, ""
		case json.Unmarshal(event.Payload, &end) == nil && end.Recorded != "":
			log.looked, log.recorded = epicprogress.Found, end.Recorded
		default:
			log.looked, log.recorded = epicprogress.NotSent, ""
			if end.Failed {
				log.failures++
			}
		}
	}
	return log
}

// permission is the budget's leave to send a check again.
func (l checkLog) permission() budget.Permission {
	return budget.Decide(budget.Spent{Turns: l.failures}, budget.Limits{Turns: maxCheckFailures}).Permission
}

// lookUp finds what became of a check that was reserved: its answer where the log
// kept one, never sent where nothing was sent or a session ended without an answer
// and another may be sent, and cannot tell where a session was started and nothing
// came back, or no further session may be sent.
func (e epicChecks) lookUp(id string) (string, epicprogress.Looked, error) {
	events, err := e.parts.branchReviews.LoadEvents(id)
	if err != nil {
		return "", epicprogress.CannotTell, nil
	}
	log := readCheckLog(events)
	if log.looked == epicprogress.NotSent && !log.permission().Granted() {
		return fmt.Sprintf("%d sessions ended without answering", log.failures), epicprogress.CannotTell, nil
	}
	return log.recorded, log.looked, nil
}

// judge makes one check in a new session that is never resumed and may use no
// tool. It asks internal/budget's leave first, which is refused once
// maxCheckFailures sessions ended without answering, and records a usage limit
// the provider met, naming the check, which has neither a run nor a conversation.
func (e epicChecks) judge(ctx context.Context, request epiccheck.Request, spent func(runstate.Spend)) (string, error) {
	parts, cfg := e.parts, e.parts.config
	events, err := parts.branchReviews.LoadEvents(request.ID)
	if err != nil {
		return "", err
	}
	before := readCheckLog(events)
	if !before.permission().Granted() {
		return "", errors.New("no further session may be started for the check")
	}
	sequence := before.sequence
	write := func(kind execution.EventType, payload any) error {
		event, err := execution.NewEvent(request.ID, sequence+1, time.Now(), kind, epicCheckSource, payload)
		if err == nil {
			err = parts.branchReviews.AppendEvent(event)
		}
		if err == nil {
			sequence++
		}
		return err
	}
	if err := write(execution.EventReviewStarted, nil); err != nil {
		return "", fmt.Errorf("record that the check is sent: %w", err)
	}
	name := agentNameForRole(cfg, domain.RoleReviewer)
	reviewer := cfg.Agents[name]
	account, err := cfg.AgentAccountEndpoint(parts.stateRoot, name)
	if err != nil {
		return "", errors.Join(err, write(execution.EventReviewCompleted, ended{Refused: err.Error()}))
	}
	provider := spend.Metered{
		Provider: providerBackendIn(cfg, parts.stateRoot, reviewer.Backend, e.runner, account.Directory),
		Log:      parts.spend,
		ToolUse:  parts.toolUse(),
		Recorded: spent,
		Attribution: spend.Attribution{ProductID: cfg.Product.ID, Agent: name, Phase: runstate.SpendPhaseReview, AccountAlias: account.Alias,
			ConfigRevision: cfg.Revision(), Backend: reviewer.Backend, BranchReviewID: request.ID, Billing: cfg.AccountBilling(account.Alias)},
	}
	result, err := provider.Run(ctx, backend.RunRequest{
		RunID: request.ID, Role: domain.RoleReviewer, WorkingDirectory: parts.repository,
		Prompt: execution.NewRedactor(parts.redactValues...).Redact(request.Prompt), SystemPrompt: request.System,
		Model: reviewer.Model, Effort: cfg.InvocationEffort(reviewer, reviewer.Model), AllowedTools: []string{},
		Timeout: reviewer.TimeLimits.ReviewLimit(), LastSequence: sequence, RedactValues: parts.redactValues,
		EventSink: func(event execution.Event) error {
			sequence = max(sequence, event.Sequence)
			return parts.branchReviews.AppendEvent(event)
		},
		AccountAlias: account.Alias, AccountConfigDir: account.Directory,
	})
	if limit := result.UsageLimit; limit != nil && (err != nil || result.IsError) && parts.usageLimits != nil {
		refusal := runstate.UsageLimitExhaustion{SchemaVersion: runstate.UsageLimitSchemaVersion, ProductID: cfg.Product.ID, At: time.Now().UTC(),
			Waiting: "the check of the whole of a named epic, " + request.ID, Kind: limit.Kind, AccountAlias: account.Alias, Model: strings.TrimSpace(reviewer.Model)}
		if !limit.ResetsAt.IsZero() {
			resets := limit.ResetsAt.UTC()
			refusal.ResetsAt = &resets
		}
		if recordErr := parts.usageLimits.Record(refusal); recordErr != nil {
			err = errors.Join(err, fmt.Errorf("record the provider's refusal: %w", recordErr))
		}
	}
	if err == nil && result.IsError {
		err = errors.New("the checking session failed: " + result.DescribeFailure())
	}
	if err != nil {
		// A call the provider refused costs nothing and is sent again; one whose
		// session started may have cost something and counts toward the limit on
		// sessions for this check.
		failed := result.UsageLimit == nil && result.ServerOverload == nil && result.ProviderOutage == nil && (result.SessionID != "" || result.LastEvent != 0)
		return "", errors.Join(err, write(execution.EventReviewCompleted, ended{Refused: err.Error(), Failed: failed}))
	}
	says, err := epiccheck.Recorded(result.FinalText, result.SessionID)
	if err != nil {
		return "", errors.Join(err, write(execution.EventReviewCompleted, ended{Refused: err.Error()}))
	}
	if err := write(execution.EventReviewCompleted, ended{Recorded: says}); err != nil {
		return "", fmt.Errorf("record the answer: %w", err)
	}
	return says, nil
}
