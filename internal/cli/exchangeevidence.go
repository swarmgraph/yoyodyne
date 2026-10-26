package cli

// The readers an exchange round's records are resolved through.
//
// Each kind is read through the store that already serves it — the amendment
// log, the tracker read, the artifact store, the run store, the report pile, a
// conversation's event log — and nothing here writes to any of them. What the
// exchange package does with what they return (the bounds, the redaction, the
// framing, what a refusal says) is decided there, once, for every provider; this
// file only reads.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// evidenceDocumentRevisions is how many of a document's latest revisions and
// approvals an attachment lists. The whole log is in the file; what a round
// needs is where the document has got to.
const evidenceDocumentRevisions = 3

// Each reader is an interface naming the one read it makes, so a test can
// supply a store that fails or answers as it is told.
type (
	evidenceAmendments interface {
		List() ([]amendment.Record, error)
	}
	evidenceRuns interface {
		Read(runID string) (runstate.State, error)
		Recorded() ([]runstate.State, error)
		DocumentWaits() ([]runstate.DocumentWait, error)
	}
	evidenceReports interface {
		List() ([]report.Report, error)
		Handlings() ([]report.Handling, error)
	}
	evidenceTracker interface {
		Show(ctx context.Context, id string) (beads.WorkItem, error)
	}
	evidenceConversations interface {
		Recorded() ([]runstate.Conversation, error)
		LoadEvents(conversationID string) ([]execution.Event, error)
	}
	evidenceDocuments interface {
		Load() (artifact.Set, error)
	}
)

// exchangeEvidenceReaders is everything the readers read, named so a test can
// build one with stores of its own.
type exchangeEvidenceReaders struct {
	productID     domain.ProductID
	repository    string
	amendments    evidenceAmendments
	runs          evidenceRuns
	reports       evidenceReports
	tracker       evidenceTracker
	conversations evidenceConversations
	documents     evidenceDocuments
	goals         func() goal.Set
	// fileState is the commit the checkout is at and whether a document's file
	// differs from it there, with an empty commit where it cannot be told. It is
	// a field so a test does not need git.
	fileState    func(ctx context.Context, path string) fileState
	redactValues []string
	now          func() time.Time
}

// exchangeEvidence is the resolver a conversation's conductor reads a round's
// records through.
func exchangeEvidence(parts components) exchange.Evidence {
	readers := exchangeEvidenceReaders{
		productID:  parts.config.Product.ID,
		repository: parts.repository,
		tracker:    parts.tracker(),
		documents:  artifactStore(parts.repository, parts.config.Product),
		goals: func() goal.Set {
			goals, _ := loadGoals(parts.repository, parts.config.Product)
			return goals
		},
		fileState:    gitFileState(parts.repository),
		redactValues: parts.redactValues,
	}
	// A store that was never built is left nil, so its kind is read as one the
	// harness could not read rather than as one that answered nil.
	if parts.amendments != nil {
		readers.amendments = parts.amendments
	}
	if parts.store != nil {
		readers.runs = parts.store
	}
	if parts.reports != nil {
		readers.reports = parts.reports
	}
	if conversations, err := runstate.NewConversationStore(parts.stateRoot, parts.config.Product.ID); err == nil {
		readers.conversations = conversations
	}
	return readers.evidence()
}

func (r exchangeEvidenceReaders) evidence() exchange.Evidence {
	sources := map[exchange.EvidenceKind]exchange.EvidenceSource{}
	if r.amendments != nil {
		sources[exchange.EvidenceAmendment] = exchange.EvidenceSource{Store: "the amendment log", Read: r.readAmendment}
	}
	if r.tracker != nil {
		sources[exchange.EvidenceWorkItem] = exchange.EvidenceSource{Store: "the tracker", Read: r.readWorkItem}
	}
	if r.documents != nil {
		sources[exchange.EvidenceDocument] = exchange.EvidenceSource{Store: "the artifact store", Read: r.readDocument}
	}
	if r.runs != nil {
		sources[exchange.EvidenceRun] = exchange.EvidenceSource{Store: "the run store", Read: r.readRun}
	}
	if r.reports != nil {
		sources[exchange.EvidenceReport] = exchange.EvidenceSource{Store: "the report pile", Read: r.readReport}
	}
	if r.conversations != nil {
		sources[exchange.EvidenceTurn] = exchange.EvidenceSource{Store: "the conversation log", Read: r.readTurn}
	}
	redactor := execution.NewRedactor(r.redactValues...)
	return exchange.Evidence{
		Sources:       sources,
		Redact:        redactor.Redact,
		Conversations: r.conversationsOf,
		Now:           r.now,
	}
}

// conversationsOf is the conversations agents filling one role hold, which is
// whose turns a round between that role and another may name.
func (r exchangeEvidenceReaders) conversationsOf(role domain.AgentRole) []string {
	if r.conversations == nil {
		return nil
	}
	recorded, err := r.conversations.Recorded()
	if err != nil {
		return nil
	}
	var held []string
	for _, conversation := range recorded {
		if conversation.Role == role {
			held = append(held, conversation.ConversationID)
		}
	}
	return held
}

// readAmendment is an amendment's proposal, decision and publication, each
// read from where it is recorded: the proposal and the decision from the
// amendment log, and the publication from the document runs and waits for the
// document the proposal names, started after it was approved.
func (r exchangeEvidenceReaders) readAmendment(_ context.Context, id string) (exchange.EvidenceRecord, error) {
	if !amendment.ValidID(id) {
		return exchange.EvidenceRecord{}, exchange.ErrNoRecord
	}
	records, err := r.amendments.List()
	if err != nil {
		return exchange.EvidenceRecord{}, err
	}
	proposal, found := amendment.Find(records, id)
	if !found {
		return exchange.EvidenceRecord{}, exchange.ErrNoRecord
	}
	evidence := exchange.AmendmentEvidence{
		ID: proposal.ID,
		Proposal: exchange.AmendmentProposal{
			Artifact:   proposal.Artifact,
			Kind:       string(proposal.Kind),
			Owner:      proposal.Owner.Title(),
			Role:       proposal.Role.Title(),
			Agent:      proposal.Agent,
			Change:     proposal.Change,
			Why:        proposal.Why,
			Intent:     string(proposal.Intent),
			RunID:      proposal.RunID,
			WorkItemID: proposal.WorkItemID,
			RaisedAt:   proposal.RaisedAt,
		},
		Decision: exchange.AmendmentDecision{State: exchange.AmendmentUndecided},
	}
	if decision, decided := amendment.DecisionOn(records, id); decided {
		evidence.Decision = exchange.AmendmentDecision{
			State:        exchange.AmendmentDecisionState(decision.Verdict),
			Authority:    decision.Authority.Title(),
			Reason:       decision.Reason,
			Conversation: decision.Conversation,
			At:           decision.DecidedAt,
		}
		evidence.Decision.Decider = "operator"
		if decision.Decider == amendment.DeciderOwner {
			evidence.Decision.Decider = decision.Authority.Title()
		}
		if decision.Verdict == amendment.VerdictApproved {
			publication, err := r.publication(proposal.Artifact, decision.DecidedAt)
			if err != nil {
				return exchange.EvidenceRecord{}, err
			}
			publication.Matched = publicationMatched
			evidence.Publication = publication
		}
	}
	record := evidence.Record("the amendment log")
	record.ProductID = proposal.ProductID
	return record, nil
}

// publicationMatched is what an amendment's attachment says about how its
// publication was found; see publication.
const publicationMatched = "matched by the document and by starting after the approval, since nothing records which run carries which amendment; a run publishing another revision of the same document would read the same"

// publication is how far a document's approved change has got: the latest
// document run for it started after the approval, or a document waiting for a
// run, or nothing. Nothing records which run carries which amendment, so the
// match is by document and time: a later run publishing some other revision of
// the same document reads as this amendment's publication. The attachment says
// so (exchange.AmendmentPublication.Matched).
func (r exchangeEvidenceReaders) publication(document string, approved time.Time) (exchange.AmendmentPublication, error) {
	if r.runs == nil {
		return exchange.AmendmentPublication{}, errors.New("the run store is not available, so whether the change was published cannot be told")
	}
	states, err := r.runs.Recorded()
	if err != nil {
		return exchange.AmendmentPublication{}, fmt.Errorf("read the document runs: %w", err)
	}
	var runs []runstate.State
	for _, state := range states {
		if state.Document != nil && state.Document.Candidate.Artifact.ID == document && !state.StartedAt.Before(approved) {
			runs = append(runs, state)
		}
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].StartedAt.After(runs[j].StartedAt) })
	for _, state := range runs {
		if state.RecordedAsMerged() {
			landed := exchange.AmendmentPublication{State: exchange.AmendmentLanded, Run: state.RunID, Commit: state.Integration.TargetCommit}
			if state.CompletedAt != nil {
				landed.At = *state.CompletedAt
			}
			return landed, nil
		}
	}
	if len(runs) > 0 {
		latest := runs[0]
		if !latest.Status.Terminal() {
			return exchange.AmendmentPublication{State: exchange.AmendmentInRun, Run: latest.RunID}, nil
		}
		returned := exchange.AmendmentPublication{State: exchange.AmendmentReturned, Run: latest.RunID}
		switch {
		case strings.TrimSpace(latest.ReviewSummary) != "" && latest.ReviewDecision != "":
			returned.Reason = fmt.Sprintf("the reviewer %s it: %s", latest.ReviewDecision, singleLineText(latest.ReviewSummary))
		case latest.RecordedStopClass() != "":
			returned.Reason = fmt.Sprintf("it stopped (%s)", latest.RecordedStopClass())
		default:
			returned.Reason = fmt.Sprintf("it ended %s", latest.Status)
		}
		return returned, nil
	}
	waits, err := r.runs.DocumentWaits()
	if err != nil {
		return exchange.AmendmentPublication{}, fmt.Errorf("read the documents waiting for a run: %w", err)
	}
	for _, wait := range waits {
		if wait.Document.Candidate.Artifact.ID == document && !wait.Since.Before(approved) {
			return exchange.AmendmentPublication{State: exchange.AmendmentWaiting}, nil
		}
	}
	return exchange.AmendmentPublication{State: exchange.AmendmentNotWritten}, nil
}

// readWorkItem is what the tracker read returns for one item, with its notes
// window. The tracker read is this product's own, so an id it does not hold —
// another product's included — is no such record. The id is not judged by its
// prefix: a tracker's prefix is set apart from the product id and need not
// match it.
func (r exchangeEvidenceReaders) readWorkItem(ctx context.Context, id string) (exchange.EvidenceRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, trackerCommandTimeout)
	defer cancel()
	item, err := r.tracker.Show(ctx, id)
	switch {
	case errors.Is(err, beads.ErrNoSuchWorkItem):
		return exchange.EvidenceRecord{}, exchange.ErrNoRecord
	case err != nil:
		return exchange.EvidenceRecord{}, err
	}
	var goals goal.Set
	if r.goals != nil {
		goals = r.goals()
	}
	text, err := chat.WorkItemEvidence(item, goals)
	if err != nil {
		return exchange.EvidenceRecord{}, err
	}
	return exchange.EvidenceRecord{Revision: contentRevision(text), Text: text}, nil
}

// readDocument is a document's kind, owner, current revision, its latest
// revisions and approvals, the commit its file was last changed in, any later
// revision written and not yet landed, and as much of its body as the bound
// leaves.
func (r exchangeEvidenceReaders) readDocument(ctx context.Context, id string) (exchange.EvidenceRecord, error) {
	set, err := r.documents.Load()
	if err != nil {
		return exchange.EvidenceRecord{}, err
	}
	document, found := set.Find(id)
	if !found {
		return exchange.EvidenceRecord{}, exchange.ErrNoRecord
	}
	current := len(document.Revisions) - 1
	revision := fmt.Sprintf("revision %d", current)
	var state fileState
	if r.fileState != nil {
		state = r.fileState(ctx, document.Path)
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "document: %s (%s), %s\n", document.ID, document.Title, document.Path)
	owner, _ := artifact.Owner(document.Kind)
	fmt.Fprintf(&rendered, "kind: %s, owned by the %s, status %s\n", document.Kind, owner.Title(), document.Status)
	fmt.Fprintf(&rendered, "current revision: %d\n", current)
	// The file is read from the checkout, so it is named as read at a commit only
	// where the checkout's copy is that commit's. A file with uncommitted changes
	// is said to have them, because the text attached is not what any commit holds.
	switch {
	case state.commit == "":
		rendered.WriteString("read at commit: not known; the file was read as it stands in the checkout\n")
	case state.dirty:
		fmt.Fprintf(&rendered, "read at commit: none; the file was read from the checkout, where it has changes not yet committed on top of %s\n", state.commit)
		revision += ", with uncommitted changes on " + state.commit
	default:
		fmt.Fprintf(&rendered, "read at commit: %s\n", state.commit)
		revision += " at " + state.commit
	}
	rendered.WriteString("latest revisions:\n")
	for i := max(0, len(document.Revisions)-evidenceDocumentRevisions); i < len(document.Revisions); i++ {
		entry := document.Revisions[i]
		fmt.Fprintf(&rendered, "  %d: %s by the %s, %s: %s\n", i, entry.Action, entry.By.Title(), exchange.LocalTime(entry.At), singleLineText(entry.Reason))
	}
	if len(document.Approvals) == 0 {
		rendered.WriteString("approvals: none recorded\n")
	} else {
		rendered.WriteString("latest approvals:\n")
		for i := max(0, len(document.Approvals)-evidenceDocumentRevisions); i < len(document.Approvals); i++ {
			approval := document.Approvals[i]
			fmt.Fprintf(&rendered, "  of revision %d by %s, %s\n", approval.Revision, approval.By, exchange.LocalTime(approval.At))
		}
	}
	if pending := r.pendingDocument(id); pending != "" {
		fmt.Fprintf(&rendered, "later revision: %s\n", pending)
	} else {
		rendered.WriteString("later revision: none written and waiting\n")
	}
	body, err := os.ReadFile(filepath.Join(r.repository, document.Path))
	if err != nil {
		return exchange.EvidenceRecord{}, fmt.Errorf("read %s: %w", document.Path, err)
	}
	rendered.WriteString("\nbody:\n")
	rendered.Write(body)
	return exchange.EvidenceRecord{Source: "the artifact store", Revision: revision, Text: rendered.String()}, nil
}

// pendingDocument names a later revision of a document written and not yet
// landed: a document run still in flight, or one waiting for a run.
func (r exchangeEvidenceReaders) pendingDocument(id string) string {
	if r.runs == nil {
		return ""
	}
	if states, err := r.runs.Recorded(); err == nil {
		for _, state := range states {
			if state.Document != nil && state.Document.Candidate.Artifact.ID == id && !state.Status.Terminal() {
				return fmt.Sprintf("written in conversation %s at turn %d, and run %s is publishing it", state.Document.ConversationID, state.Document.Turn, state.RunID)
			}
		}
	}
	if waits, err := r.runs.DocumentWaits(); err == nil {
		for _, wait := range waits {
			if wait.Document.Candidate.Artifact.ID == id {
				return fmt.Sprintf("written in conversation %s at turn %d, and waiting for a run since %s", wait.Document.ConversationID, wait.Document.Turn, exchange.LocalTime(wait.Since))
			}
		}
	}
	return ""
}

// readRun is a run's item, state, attempts, stop reason and outcome as its
// record holds them.
func (r exchangeEvidenceReaders) readRun(_ context.Context, id string) (exchange.EvidenceRecord, error) {
	state, err := r.runs.Read(id)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return exchange.EvidenceRecord{}, exchange.ErrNoRecord
	case err != nil && !strings.HasPrefix(id, "run-"):
		return exchange.EvidenceRecord{}, exchange.ErrNoRecord
	case err != nil:
		return exchange.EvidenceRecord{}, err
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "run: %s\n", state.RunID)
	if state.WorkItemID != "" {
		fmt.Fprintf(&rendered, "work item: %s %s\n", state.WorkItemID, state.WorkItemTitle)
	}
	if state.Document != nil {
		fmt.Fprintf(&rendered, "publishes document: %s, %s\n", state.Document.Candidate.Artifact.ID, state.Document.Origin())
	}
	fmt.Fprintf(&rendered, "state: %s", state.Status)
	if state.Phase != "" {
		fmt.Fprintf(&rendered, ", phase %s", state.Phase)
	}
	rendered.WriteString("\n")
	fmt.Fprintf(&rendered, "started: %s\n", exchange.LocalTime(state.StartedAt))
	if state.CompletedAt != nil {
		fmt.Fprintf(&rendered, "ended: %s\n", exchange.LocalTime(*state.CompletedAt))
	}
	fmt.Fprintf(&rendered, "repair attempts: %d, review rounds: %d\n", state.RepairAttempts, state.ReviewRounds)
	if class := state.RecordedStopClass(); class != "" {
		fmt.Fprintf(&rendered, "stop reason: %s\n", class)
	}
	if state.ReviewDecision != "" {
		fmt.Fprintf(&rendered, "review: %s: %s\n", state.ReviewDecision, singleLineText(state.ReviewSummary))
	}
	if state.LandingOutcome != "" {
		fmt.Fprintf(&rendered, "landing claimed: %s: %s\n", state.LandingOutcome, singleLineText(state.LandingReason))
	}
	switch {
	case state.RecordedAsMerged():
		fmt.Fprintf(&rendered, "outcome: landed at commit %s on %s\n", state.Integration.TargetCommit, state.Integration.TargetBranch)
	case state.Status.Terminal():
		rendered.WriteString("outcome: ended without landing\n")
	default:
		rendered.WriteString("outcome: not yet settled\n")
	}
	return exchange.EvidenceRecord{
		ProductID: state.ProductID,
		Source:    "the run store",
		Revision:  fmt.Sprintf("updated %s", state.UpdatedAt.UTC().Format(time.RFC3339)),
		Text:      rendered.String(),
	}, nil
}

// readReport is a report's whole message, who filed it, and how it was handled.
func (r exchangeEvidenceReaders) readReport(_ context.Context, id string) (exchange.EvidenceRecord, error) {
	reports, err := r.reports.List()
	if err != nil {
		return exchange.EvidenceRecord{}, err
	}
	var found *report.Report
	for i := range reports {
		if reports[i].ID == id {
			found = &reports[i]
			break
		}
	}
	if found == nil {
		return exchange.EvidenceRecord{}, exchange.ErrNoRecord
	}
	handlings, err := r.reports.Handlings()
	if err != nil {
		return exchange.EvidenceRecord{}, err
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "report: %s, at %s\n", found.ID, found.Severity)
	fmt.Fprintf(&rendered, "filed by: the %s", found.Role.Title())
	if found.Agent != "" {
		fmt.Fprintf(&rendered, " (agent %s)", found.Agent)
	}
	fmt.Fprintf(&rendered, ", from %s, %s\n", found.RunID, exchange.LocalTime(found.RecordedAt))
	if found.For != "" {
		fmt.Fprintf(&rendered, "addressed to: the %s\n", found.For.Title())
	}
	if found.WorkItemID != "" {
		fmt.Fprintf(&rendered, "work item: %s\n", found.WorkItemID)
	}
	fmt.Fprintf(&rendered, "message: %s\n", strings.TrimSpace(found.Message))
	revision := "unhandled"
	handled := false
	for _, handling := range handlings {
		if handling.ReportID != id {
			continue
		}
		handled = true
		revision = "handled " + handling.RecordedAt.UTC().Format(time.RFC3339)
		fmt.Fprintf(&rendered, "handled by: the %s, %s: %s\n", handling.Role.Title(), exchange.LocalTime(handling.RecordedAt), singleLineText(handling.Reason))
	}
	if !handled {
		rendered.WriteString("handled: not yet\n")
	}
	return exchange.EvidenceRecord{ProductID: found.ProductID, Source: "the report pile", Revision: revision, Text: rendered.String()}, nil
}

// readTurn is one turn of a conversation: what was said to the role since its
// previous turn, and its reply. Turns are counted from one the way the
// conversation counts them, one for each provider call that completed, so a
// turn number here is the number the conversation's own records use. One call
// can log several agent messages — Codex logs each one it sees and Claude Code
// one per block of text — and the reply is the last of them, which is the final
// text the turn took. A call that failed is not a turn, and what was said
// before it is carried to the call that retried it. The exchange package has
// already refused a conversation neither party holds; this reads only the turn
// named, never the history around it.
func (r exchangeEvidenceReaders) readTurn(_ context.Context, id string) (exchange.EvidenceRecord, error) {
	conversation, number, err := exchange.SplitTurn(id)
	if err != nil {
		return exchange.EvidenceRecord{}, exchange.ErrNoRecord
	}
	events, err := r.conversations.LoadEvents(conversation)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return exchange.EvidenceRecord{}, exchange.ErrNoRecord
		}
		return exchange.EvidenceRecord{}, err
	}
	var said []string
	var reply *execution.Event
	turns := 0
	for i, event := range events {
		switch event.Type {
		case execution.EventOperatorMessage:
			said = append(said, eventText(event))
		case execution.EventAgentMessage:
			reply = &events[i]
		case execution.EventRunFailed:
			reply = nil
		case execution.EventRunCompleted:
			turns++
			if turns < number {
				said, reply = nil, nil
				continue
			}
			var rendered strings.Builder
			fmt.Fprintf(&rendered, "conversation %s, turn %d, completed %s\n", conversation, number, exchange.LocalTime(event.Timestamp))
			for _, message := range said {
				fmt.Fprintf(&rendered, "\nsaid to the role:\n%s\n", message)
			}
			if reply != nil {
				fmt.Fprintf(&rendered, "\nthe role replied:\n%s\n", eventText(*reply))
			} else {
				rendered.WriteString("\nthe role's reply is not in the log\n")
			}
			return exchange.EvidenceRecord{
				Source:   "the conversation log",
				Revision: fmt.Sprintf("event %d", event.Sequence),
				Text:     rendered.String(),
			}, nil
		}
	}
	return exchange.EvidenceRecord{}, exchange.ErrNoRecord
}

func eventText(event execution.Event) string {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Text)
}

// contentRevision names a rendering by what it says, for a record whose store
// keeps no revision of its own: two reads that say the same thing share it.
func contentRevision(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "content " + hex.EncodeToString(sum[:6])
}

func singleLineText(text string) string { return strings.Join(strings.Fields(text), " ") }

// fileState is where a file read from the checkout stands against the commit
// the checkout is at.
type fileState struct {
	commit string
	dirty  bool
}

// gitFileState reads the commit the repository's checkout is at, and whether
// the file differs from that commit there: changed, staged, or not yet tracked.
// Either read failing leaves the commit unknown rather than guessed.
func gitFileState(repository string) func(ctx context.Context, path string) fileState {
	return func(ctx context.Context, path string) fileState {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		head, err := exec.CommandContext(ctx, "git", "-C", repository, "rev-parse", "HEAD").Output()
		if err != nil {
			return fileState{}
		}
		status, err := exec.CommandContext(ctx, "git", "-C", repository, "status", "--porcelain", "--", path).Output()
		if err != nil {
			return fileState{}
		}
		return fileState{commit: strings.TrimSpace(string(head)), dirty: strings.TrimSpace(string(status)) != ""}
	}
}
