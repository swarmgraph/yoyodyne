package orchestrator

// Skipping a scheduled pass that has nothing new to hand its role.
//
// About half of the development manager's, the program manager's and the
// architect's scheduled passes asked for nothing but reads, memory notes and a
// lane report, and each still paid to start a provider session (section 2 of
// docs/designs/reducing-what-the-factory-costs.md). So before a scheduled pass
// is started, the harness checks, with no provider call, whether anything the
// pass would be handed has changed since the task's last completed pass, and
// where nothing has, the pass is not started.
//
// # How "changed" is read
//
// Every pass the trigger starts records a digest of each thing the check reads,
// taken before its first turn (runstate.Sweep.Fingerprints). The check takes
// the same digests now and compares them with the last completed pass's. A
// digest is of the records as they stand rather than of timestamps, so it needs
// nothing the stores do not already keep, and a record that changed and changed
// back reads as unchanged, which is what the role would have been handed. Taking
// them before the first turn means a change made while the pass ran is a change
// the next check sees, so nothing arriving mid-pass is skipped over.
//
// What is read is held here, for each role, and is not configuration: a list a
// project could edit could hide a change from the role that has to act on it.
// A project chooses only whether passes are skipped and how long the floor is
// (config.PassSkipping).
//
// # When the pass is started anyway
//
// Anything the check cannot read is a change: a pass is skipped only on a
// reading that says positively that nothing moved. So is a task with no
// completed pass, a last completed pass that recorded no digests, and a floor
// reached: every role has a full pass at least every full_pass_every, whatever
// the check finds, so a change it does not read waits no longer than that.
//
// For the development manager and the program manager whose lane covers
// factory health, a health problem still present an hour or more after the
// last completed pass is a change too. A stall shows the same problems pass
// after pass, so their digest does not move; without this, an idle slot with
// ready work, or a run left waiting, could go unexamined until the floor.
//
// # What a skipped pass is
//
// The firing was claimed before the check, so a skipped pass is the one
// instance of its window and the cadence runs on from it. It is recorded in the
// sweep log with the reason and the completed pass nothing changed since, and
// it takes no turn, spends nothing, and moves no cursor. Passes woken by
// anything other than the schedule — events, a summons, a request, a critical
// report — are never checked.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// PassChangeKind is one thing the check before a scheduled pass reads.
type PassChangeKind string

const (
	// ChangeLanded is the target branch: a change landed on it.
	ChangeLanded PassChangeKind = "landed"
	// ChangeWork is the work items the role owns, carries, or is named on, and
	// for a program manager the items in its lane.
	ChangeWork PassChangeKind = "work"
	// ChangeAddressed is the unhandled reports addressed to the role, and for
	// the Lead Product Manager, who handles every report, the whole pile.
	ChangeAddressed PassChangeKind = "reports"
	// ChangeProposals is the undecided changes proposed to the role's documents.
	ChangeProposals PassChangeKind = "proposals"
	// ChangeMessages is what is waiting in the role's conversation to be told:
	// a question from another role, a message from the operator, a handoff.
	ChangeMessages PassChangeKind = "messages"
	// ChangeDocuments is the role's documents: confirmed, sent, landed, or
	// returned.
	ChangeDocuments PassChangeKind = "documents"
	// ChangeFollowUps is the follow-ups the role owns whose deadline has passed.
	ChangeFollowUps PassChangeKind = "follow-ups"
	// ChangeStops is the development manager's list of stopped runs.
	ChangeStops PassChangeKind = "stopped-runs"
	// ChangeHealth is the factory's health problems, for the development
	// manager and the program manager whose lane covers factory health.
	ChangeHealth PassChangeKind = "health"
	// ChangeLane is the events in a program manager's lane past its cursor.
	ChangeLane PassChangeKind = "lane"
)

// Describe is the kind in the words a sentence about the check uses.
func (k PassChangeKind) Describe() string {
	switch k {
	case ChangeLanded:
		return "changes landed on the target branch"
	case ChangeWork:
		return "the work items it owns, carries or is named on"
	case ChangeAddressed:
		return "reports addressed to it"
	case ChangeProposals:
		return "changes proposed to its documents"
	case ChangeMessages:
		return "questions and messages waiting in its conversation"
	case ChangeDocuments:
		return "its documents confirmed, sent, landed or returned"
	case ChangeFollowUps:
		return "deadlines passed on follow-ups it owns"
	case ChangeStops:
		return "its list of stopped runs"
	case ChangeHealth:
		return "the factory's health problems"
	case ChangeLane:
		return "events in its lane"
	}
	return string(k)
}

// PassScope is whose scheduled pass the check reads for: the role, and for a
// program manager the instance and its lane. Health marks a pass handed the
// factory's health: the development manager's, and the factory-flow program
// manager's.
type PassScope struct {
	Role   domain.AgentRole
	Agent  string
	Lane   string
	Health bool
}

// PassChangeSources reads the records the trigger has no reading of its own
// for, as digests. It is satisfied in internal/cli over the tracker, the
// repository, the conversation record, the run store, the follow-ups, the
// docket, and a health observation.
type PassChangeSources interface {
	Fingerprints(ctx context.Context, scope PassScope, kinds []PassChangeKind) PassFingerprints
}

// PassFingerprints is one reading: a digest for each kind read, why each kind
// that could not be read was not, and how many health conditions stand now.
type PassFingerprints struct {
	Digests          map[PassChangeKind]string
	Unread           map[PassChangeKind]string
	HealthConditions int
}

// HealthStillPresentAfter is how long after the last completed pass a health
// problem still standing counts as a change for a role handed the factory's
// health.
const HealthStillPresentAfter = time.Hour

// passChangeKinds is what the check reads for a scope, held here and not in
// configuration for the reason the file's comment gives.
func passChangeKinds(scope PassScope) []PassChangeKind {
	kinds := []PassChangeKind{ChangeLanded, ChangeWork, ChangeAddressed, ChangeProposals, ChangeMessages, ChangeDocuments, ChangeFollowUps}
	if scope.Role == domain.RoleDevelopmentManager && scope.Agent == "" {
		kinds = append(kinds, ChangeStops)
	}
	if scope.Health {
		kinds = append(kinds, ChangeHealth)
	}
	return kinds
}

// passScope is the scope of a pass of a task's role or of an instance.
func (t Trigger) passScope(role domain.AgentRole, agent string) PassScope {
	scope := PassScope{Role: role, Agent: agent, Health: t.observesHealth(role, agent)}
	if agent != "" {
		scope.Lane = strings.TrimSpace(t.Instances[agent].Lane)
	}
	return scope
}

// passFingerprints takes the digests a pass of the scope is compared by. Nil
// where no sources are wired, which is a trigger that never skips.
func (t Trigger) passFingerprints(ctx context.Context, scope PassScope, task config.RecurringTask) *PassFingerprints {
	if t.Changes == nil {
		return nil
	}
	kinds := passChangeKinds(scope)
	reading := t.Changes.Fingerprints(ctx, scope, kinds)
	if reading.Digests == nil {
		reading.Digests = map[PassChangeKind]string{}
	}
	if reading.Unread == nil {
		reading.Unread = map[PassChangeKind]string{}
	}
	// The reports and the proposals are read through what the trigger already
	// holds, so they are the same records the pass is put.
	if t.Pile != nil {
		if digest, err := t.addressedDigest(scope.Role); err != nil {
			reading.Unread[ChangeAddressed] = err.Error()
			delete(reading.Digests, ChangeAddressed)
		} else {
			reading.Digests[ChangeAddressed] = digest
		}
	}
	if t.Amendments != nil && scope.Agent == "" {
		batch := t.amendmentBatch(task)
		if batch.problem != "" {
			reading.Unread[ChangeProposals] = batch.problem
			delete(reading.Digests, ChangeProposals)
		} else {
			ids := make([]string, 0, len(batch.pending))
			for id := range batch.pending {
				ids = append(ids, id)
			}
			reading.Digests[ChangeProposals] = digestLines(ids)
		}
	}
	return &reading
}

// addressedDigest is the digest of the unhandled reports the role is put: those
// addressed to it, or for the Lead Product Manager the whole unhandled pile.
func (t Trigger) addressedDigest(role domain.AgentRole) (string, error) {
	reports, err := t.Pile.List()
	if err != nil {
		return "", fmt.Errorf("the reports could not be read: %w", err)
	}
	handlings, err := t.Pile.Handlings()
	if err != nil {
		return "", fmt.Errorf("what became of the reports could not be read: %w", err)
	}
	waiting := report.Unhandled(reports, handlings)
	if role != domain.RoleProductManager {
		waiting = readmodel.ReportsAddressedTo(readmodel.MoverOf(role), reports, handlings)
	}
	ids := make([]string, 0, len(waiting))
	for _, reported := range waiting {
		ids = append(ids, reported.ID)
	}
	return digestLines(ids), nil
}

// recordedFingerprints is a reading as the pass record keeps it: only the
// kinds that were read.
func recordedFingerprints(reading *PassFingerprints) map[string]string {
	if reading == nil || len(reading.Digests) == 0 {
		return nil
	}
	kept := make(map[string]string, len(reading.Digests))
	for kind, digest := range reading.Digests {
		if _, unread := reading.Unread[kind]; !unread && digest != "" {
			kept[string(kind)] = digest
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// lastCompletedPass is the task's latest pass that completed: it took a turn,
// the role gave its account, and nothing marks it failed or missed.
func (t Trigger) lastCompletedPass(name string) (runstate.Sweep, bool, error) {
	recorded, _, err := t.Reports.List()
	if err != nil {
		return runstate.Sweep{}, false, err
	}
	var last runstate.Sweep
	found := false
	for _, pass := range recorded {
		if pass.Task != name || !readmodel.PassSucceeded(pass) {
			continue
		}
		if !found || pass.StartedAt.After(last.StartedAt) {
			last, found = pass, true
		}
	}
	return last, found, nil
}

// passSkip is the check's answer: whether the scheduled pass is skipped, and if
// it is, the record of why.
type passSkip struct {
	skip         bool
	skipped      runstate.SkippedPass
	fingerprints map[string]string
}

// unchanged runs the check for one scheduled firing. laneEvents is how many
// events a program manager's pass would carry past its cursor, and zero for a
// recurring task.
func (t Trigger) unchanged(ctx context.Context, name string, task config.RecurringTask, scope PassScope, skipping config.PassSkipping, laneEvents int) passSkip {
	if t.Changes == nil || !skipping.Skip || t.Reports == nil {
		return passSkip{}
	}
	now := t.now()
	last, found, err := t.lastCompletedPass(name)
	if err != nil || !found {
		return passSkip{}
	}
	fullPassBy := last.StartedAt.Add(skipping.Floor)
	if !now.Before(fullPassBy) || len(last.Fingerprints) == 0 || laneEvents > 0 {
		return passSkip{}
	}
	reading := t.passFingerprints(ctx, scope, task)
	if reading == nil || len(reading.Unread) > 0 {
		return passSkip{}
	}
	kinds := passChangeKinds(scope)
	for _, kind := range kinds {
		before, recorded := last.Fingerprints[string(kind)]
		current, read := reading.Digests[kind]
		if !recorded || !read || before != current {
			return passSkip{}
		}
	}
	if scope.Health && reading.HealthConditions > 0 && !now.Before(last.EndedAt.Add(HealthStillPresentAfter)) {
		return passSkip{}
	}
	if scope.Agent != "" {
		kinds = append(kinds, ChangeLane)
	}
	named := fmt.Sprintf("the pass of %s that started at %s", name, localStamp(last.StartedAt))
	checked := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		checked = append(checked, kind.Describe())
	}
	reason := fmt.Sprintf("nothing new since the last completed pass, %s: the harness checked %s, and none had changed; a full pass is taken by %s whatever the check finds",
		named, strings.Join(checked, ", "), localStamp(fullPassBy))
	return passSkip{
		skip: true,
		skipped: runstate.SkippedPass{
			LastCompleted:   named,
			LastCompletedAt: last.StartedAt,
			FullPassBy:      fullPassBy,
			Reason:          boundedProblem([]string{reason}),
		},
		fingerprints: recordedFingerprints(reading),
	}
}

// recordSkip records a skipped firing and settles its claim, and says what
// became of it. The claim was made before the check, so the firing is the one
// instance of its window.
func (t Trigger) recordSkip(ctx context.Context, name string, task config.RecurringTask, agent string, due time.Time, decided passSkip) Fired {
	skipped := decided.skipped
	fired := Fired{Task: name, Role: task.Role, Skipped: skipped.Reason}
	recorded := runstate.Sweep{
		Task:         name,
		Role:         task.Role,
		Agent:        agent,
		StartedAt:    t.now(),
		Skipped:      &skipped,
		Fingerprints: decided.fingerprints,
	}
	recorded.EndedAt = recorded.StartedAt
	if !due.IsZero() && !due.After(recorded.StartedAt) {
		recorded.DueAt = due
	}
	t.settle(ctx, &fired, recorded)
	return fired
}

// digestLines is a digest of a set of lines, whatever order they were read in.
func digestLines(lines []string) string {
	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)
	sum := sha256.New()
	for _, line := range sorted {
		sum.Write([]byte(line))
		sum.Write([]byte{'\n'})
	}
	return hex.EncodeToString(sum.Sum(nil))[:32]
}

// DigestLines is digestLines for the sources outside this package, so every
// digest the check compares is taken the same way.
func DigestLines(lines []string) string { return digestLines(lines) }
