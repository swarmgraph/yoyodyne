package cli

// What the check before a scheduled pass reads, as digests, over the records
// the harness already keeps (orchestrator/passskip.go). Nothing here calls a
// provider or writes anything: each kind is one read of one record, reduced to
// the lines that say what the role would be handed, and digested.
//
// A kind that cannot be read is reported as unread rather than digested as
// empty, because the check starts the pass on anything it could not read.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/health"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// passChangeGitTimeout bounds the one Git read the check makes.
const passChangeGitTimeout = 30 * time.Second

// passChanges reads each kind from the store that keeps it. Every field is
// optional: a source not wired is a kind reported unread, so a pass is never
// skipped on a record nobody read.
type passChanges struct {
	// head reads the commit the target branch stands at.
	head func(ctx context.Context) (string, error)
	// items lists the tracker's work items.
	items interface {
		List(ctx context.Context, status string) ([]beads.WorkItem, error)
	}
	// conversations lists the roles' conversation records.
	conversations func() ([]runstate.Conversation, error)
	// runs lists the runs still in flight, from the run index, for the
	// documents a role sent to land: one joins the list when it is sent and
	// leaves it when it lands or is returned, so either moves the digest.
	runs func() ([]runstate.State, error)
	// followUps lists the follow-ups the product keeps.
	followUps func() ([]followup.FollowUp, error)
	// docket lists the stopped runs on the development manager's list.
	docket func() ([]triage.Entry, error)
	// health observes the factory's health for a pass and returns the identity
	// of every condition it found, and whether every source was read whole.
	health func(ctx context.Context, role domain.AgentRole, agent string) ([]string, bool)
	now    func() time.Time
}

// Fingerprints reads each kind asked for.
func (p passChanges) Fingerprints(ctx context.Context, scope orchestrator.PassScope, kinds []orchestrator.PassChangeKind) orchestrator.PassFingerprints {
	reading := orchestrator.PassFingerprints{Digests: map[orchestrator.PassChangeKind]string{}, Unread: map[orchestrator.PassChangeKind]string{}}
	record := func(kind orchestrator.PassChangeKind) func([]string, error) {
		return func(lines []string, err error) {
			if err != nil {
				reading.Unread[kind] = err.Error()
				return
			}
			reading.Digests[kind] = orchestrator.DigestLines(lines)
		}
	}
	var items []beads.WorkItem
	var itemsErr error
	itemsRead := false
	listItems := func() ([]beads.WorkItem, error) {
		if !itemsRead {
			itemsRead = true
			if p.items == nil {
				itemsErr = fmt.Errorf("no tracker is wired to read")
			} else {
				items, itemsErr = p.items.List(ctx, "")
			}
		}
		return items, itemsErr
	}
	for _, kind := range kinds {
		switch kind {
		case orchestrator.ChangeLanded:
			record(kind)(p.landedLines(ctx))
		case orchestrator.ChangeWork:
			listed, err := listItems()
			record(kind)(workLines(listed, scope), err)
		case orchestrator.ChangeMessages:
			record(kind)(p.messageLines(scope))
		case orchestrator.ChangeDocuments:
			record(kind)(p.documentLines(scope))
		case orchestrator.ChangeFollowUps:
			record(kind)(p.followUpLines(scope))
		case orchestrator.ChangeStops:
			record(kind)(p.stopLines())
		case orchestrator.ChangeHealth:
			if p.health == nil {
				reading.Unread[kind] = "no health observation is wired"
				continue
			}
			conditions, complete := p.health(ctx, scope.Role, scope.Agent)
			if !complete {
				reading.Unread[kind] = "the health observation could not read every source whole"
				continue
			}
			reading.HealthConditions = len(conditions)
			record(kind)(conditions, nil)
		}
	}
	return reading
}

func (p passChanges) landedLines(ctx context.Context) ([]string, error) {
	if p.head == nil {
		return nil, fmt.Errorf("no repository is wired to read")
	}
	head, err := p.head(ctx)
	if err != nil {
		return nil, err
	}
	return []string{head}, nil
}

// workLines is one line per work item the scope's role owns, carries, or is
// named on: the items its conversation carries, the items assigned to it, and
// for a program manager the items in its lane. Each line holds what a change to
// the item would move: its status, priority, assignee, parking, executor,
// labels, and its text.
func workLines(items []beads.WorkItem, scope orchestrator.PassScope) []string {
	var lines []string
	for _, item := range items {
		if !workItemIsTheRoles(item, scope) {
			continue
		}
		parking, _ := json.Marshal(item.Parking)
		lines = append(lines, strings.Join([]string{
			item.ID, item.Status, fmt.Sprint(item.Priority), item.Assignee, string(parking), string(item.Executor),
			strings.Join(item.Labels, ","),
			orchestrator.DigestLines([]string{item.Title, item.Description, item.Design, item.AcceptanceCriteria, item.FramedNotes, item.Notes}),
		}, "|"))
	}
	return lines
}

func workItemIsTheRoles(item beads.WorkItem, scope orchestrator.PassScope) bool {
	if item.Executor == domain.ConversationWith(scope.Role) {
		return true
	}
	assignee := strings.TrimSpace(item.Assignee)
	if assignee != "" && (assignee == string(scope.Role) || (scope.Agent != "" && assignee == scope.Agent)) {
		return true
	}
	if scope.Lane != "" {
		for _, label := range item.Labels {
			if label == scope.Lane {
				return true
			}
		}
	}
	return false
}

// messageLines is what is waiting in the scope's conversations to be told:
// handoffs, notices, and proposals, concerns, and writes held for its next turn.
//
// A handoff is read as the conversation's count of them (runstate.Conversation
// Told), not as the pending results text it was added to. That text also holds
// the results of the role's own last actions — its lane report, its memory
// notes, each with a version that rises every time — so reading it made every
// pass that wrote one look like a new message to the next check, and no such
// role's pass could ever be skipped. The role's own results are still delivered
// on its next started pass.
func (p passChanges) messageLines(scope orchestrator.PassScope) ([]string, error) {
	conversations, err := p.scopeConversations(scope)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, conversation := range conversations {
		pending, _ := json.Marshal(struct {
			Told      uint64
			Notices   []string
			Proposals []runstate.PendingProposal
			Concerns  []runstate.PendingConcern
			Writes    []runstate.PendingWrite
		}{conversation.Told, conversation.PendingNotices, conversation.PendingProposals, conversation.PendingConcerns, conversation.PendingWrites})
		lines = append(lines, conversation.ConversationID+"|"+orchestrator.DigestLines([]string{string(pending)}))
	}
	return lines, nil
}

// documentLines is the scope's documents: those its conversations hold handed
// back or returned, and every run in flight that carries one of its documents
// to land, with where that run stands.
func (p passChanges) documentLines(scope orchestrator.PassScope) ([]string, error) {
	conversations, err := p.scopeConversations(scope)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, conversation := range conversations {
		held, _ := json.Marshal(struct {
			Returns    map[string]int
			LastReturn map[string]string
			HandedBack []runstate.HandedBackDocument
		}{conversation.DocumentReturns, conversation.DocumentLastReturn, conversation.HandedBack})
		lines = append(lines, conversation.ConversationID+"|"+orchestrator.DigestLines([]string{string(held)}))
	}
	if p.runs == nil {
		return nil, fmt.Errorf("no run store is wired to read")
	}
	runs, err := p.runs()
	if err != nil {
		return nil, fmt.Errorf("the runs could not be read: %w", err)
	}
	for _, run := range runs {
		if run.Document == nil || run.Document.Owner != scope.Role {
			continue
		}
		lines = append(lines, strings.Join([]string{run.RunID, string(run.Status), string(run.Phase)}, "|"))
	}
	return lines, nil
}

func (p passChanges) scopeConversations(scope orchestrator.PassScope) ([]runstate.Conversation, error) {
	if p.conversations == nil {
		return nil, fmt.Errorf("no conversation record is wired to read")
	}
	recorded, err := p.conversations()
	if err != nil {
		return nil, fmt.Errorf("the conversations could not be read: %w", err)
	}
	var kept []runstate.Conversation
	for _, conversation := range recorded {
		if conversation.Role != scope.Role {
			continue
		}
		if scope.Agent != "" && conversation.Agent != scope.Agent {
			continue
		}
		kept = append(kept, conversation)
	}
	return kept, nil
}

// followUpLines is each open follow-up the scope's role owns whose deadline
// has passed, naming which deadline: back to its owner, or on to the owner's
// manager.
func (p passChanges) followUpLines(scope orchestrator.PassScope) ([]string, error) {
	if p.followUps == nil {
		return nil, fmt.Errorf("no follow-up record is wired to read")
	}
	waits, err := p.followUps()
	if err != nil {
		return nil, fmt.Errorf("the follow-ups could not be read: %w", err)
	}
	now := time.Now()
	if p.now != nil {
		now = p.now()
	}
	owner := ownership.MoverOf(scope.Role)
	var lines []string
	for _, wait := range waits {
		if wait.State != followup.StateOpen || wait.Owner != owner {
			continue
		}
		if !wait.BackToOwnerAt.IsZero() && !now.Before(wait.BackToOwnerAt) {
			lines = append(lines, wait.ID+"|back-to-owner")
		}
		if !wait.ToManagerAt.IsZero() && !now.Before(wait.ToManagerAt) {
			lines = append(lines, wait.ID+"|to-manager")
		}
	}
	return lines, nil
}

// stopLines is every entry on the development manager's list of stopped runs,
// whole, and every document held for her decision, so an entry added, decided, or changed moves the digest.
func (p passChanges) stopLines() ([]string, error) {
	if p.docket == nil {
		return nil, fmt.Errorf("no docket is wired to read")
	}
	entries, err := p.docket()
	if err != nil {
		return nil, fmt.Errorf("the list of stopped runs could not be read: %w", err)
	}
	lines := make([]string, 0, len(entries))
	if p.conversations != nil {
		recorded, err := p.conversations()
		if err != nil {
			return nil, fmt.Errorf("the documents held for the development manager could not be read: %w", err)
		}
		for _, held := range readmodel.HeldDocuments(recorded) {
			lines = append(lines, "held document|"+held.Reference)
		}
	}
	for _, entry := range entries {
		encoded, _ := json.Marshal(entry)
		lines = append(lines, entry.Key+"|"+orchestrator.DigestLines([]string{string(encoded)}))
	}
	return lines, nil
}

// passChangesFrom wires the check's sources over a product's records.
func passChangesFrom(parts components) orchestrator.PassChangeSources {
	changes := passChanges{
		items: withTrackerRecords(chatTracker(parts.runner, parts.repository), parts.trackerListings, parts.trackerSlowCalls),
		runs:  parts.store.Incomplete,
		head: func(ctx context.Context) (string, error) {
			return repositoryHead(ctx, parts.runner, parts.repository)
		},
		health: func(ctx context.Context, role domain.AgentRole, agent string) ([]string, bool) {
			// Observed without keeping: this observation is only compared, and
			// the pass, where one is started, takes and keeps its own.
			observer := productHealth{parts: parts}.observer()
			observer.Keeper = nil
			record := health.PassRecord(observer.Observe(ctx, health.Caller{Role: role, Agent: agent}, nil))
			return record.Delivered, record.Complete
		},
	}
	if parts.docket != nil {
		changes.docket = parts.docket.List
	}
	if store, err := runstate.NewFollowUpStore(parts.stateRoot, parts.config.Product.ID); err == nil {
		changes.followUps = store.List
	}
	if store, err := runstate.NewConversationStore(parts.stateRoot, parts.config.Product.ID); err == nil {
		changes.conversations = store.Recorded
	}
	return changes
}

// repositoryHead is the commit the primary checkout stands at, which is the
// target branch the harness lands changes on.
func repositoryHead(ctx context.Context, runner execution.ProcessRunner, repository string) (string, error) {
	result, err := runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    []string{"rev-parse", "--verify", "HEAD"},
		Dir:     repository,
		Env:     execution.GitEnvironment(nil),
		Timeout: passChangeGitTimeout,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("git could not be run to read the target branch: %w", err)
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("git could not read the target branch: %s", singleLine(firstNonEmpty(result.Stderr, result.Stdout)))
	}
	head := strings.TrimSpace(result.Stdout)
	if head == "" {
		return "", fmt.Errorf("git named no commit for the target branch")
	}
	return head, nil
}
