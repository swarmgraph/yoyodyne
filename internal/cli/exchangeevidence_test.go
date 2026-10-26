package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The amendment and item of the exchange this was built for (see
// internal/exchange/evidence_test.go).
const (
	evidenceAmendmentID = "amendment-fdc094d6a05f0f396e318d83c81b7184"
	evidenceItemID      = "yoyodyne-ifd.436.2.1"
)

var (
	evidenceRaised   = time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	evidenceDecided  = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	evidenceObserved = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
)

func evidenceProposal() amendment.Record {
	return amendment.Record{Proposal: &amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            evidenceAmendmentID,
		Role:          domain.RoleDevelopmentManager,
		Agent:         "development-manager",
		RunID:         "run-raised",
		WorkItemID:    evidenceItemID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "release-recovery",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        "state how an interrupted release tag is recovered",
		Why:           "the release-tag recovery item has no ruling to build against",
		RaisedAt:      evidenceRaised,
	}}
}

func evidenceApproval() amendment.Record {
	return amendment.Record{Decision: &amendment.Decision{
		SchemaVersion: amendment.SchemaVersion,
		ProposalID:    evidenceAmendmentID,
		Verdict:       amendment.VerdictApproved,
		Authority:     domain.RoleArchitect,
		Decider:       amendment.DeciderOwner,
		Reason:        "the recovery is the owner's to state",
		Conversation:  "chat-architect",
		DecidedAt:     evidenceDecided,
	}}
}

type fixedAmendments struct {
	records []amendment.Record
	err     error
}

func (f fixedAmendments) List() ([]amendment.Record, error) { return f.records, f.err }

type fixedRuns struct {
	states []runstate.State
	waits  []runstate.DocumentWait
}

func (f fixedRuns) Read(runID string) (runstate.State, error) {
	for _, state := range f.states {
		if state.RunID == runID {
			return state, nil
		}
	}
	return runstate.State{}, fmt.Errorf("open run state: %w", fs.ErrNotExist)
}
func (f fixedRuns) Recorded() ([]runstate.State, error)             { return f.states, nil }
func (f fixedRuns) DocumentWaits() ([]runstate.DocumentWait, error) { return f.waits, nil }

// documentRun is a run publishing the release-recovery document, started at
// the moment given.
func documentRun(id string, started time.Time, status runstate.Status, landedAt string) runstate.State {
	state := runstate.State{
		RunID:     id,
		ProductID: "yoyodyne",
		Status:    status,
		StartedAt: started,
		UpdatedAt: started,
		Document: &runstate.DocumentPublication{
			Candidate:      artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: "release-recovery"}},
			ConversationID: "chat-architect",
			Turn:           12,
			Owner:          domain.RoleArchitect,
		},
	}
	if landedAt != "" {
		state.Integration = &runstate.Integration{TargetBranch: "main", TargetCommit: landedAt}
	}
	return state
}

func amendmentReaders(records []amendment.Record, runs fixedRuns) exchangeEvidenceReaders {
	return exchangeEvidenceReaders{
		productID:  "yoyodyne",
		amendments: fixedAmendments{records: records},
		runs:       runs,
		now:        func() time.Time { return evidenceObserved },
	}
}

func resolveOne(t *testing.T, readers exchangeEvidenceReaders, reference exchange.Reference) exchange.Attachment {
	t.Helper()
	attachments := readers.evidence().Resolve(context.Background(), []exchange.Reference{reference},
		exchange.EvidenceScope{ProductID: "yoyodyne", Asker: exchange.Party{Role: domain.RoleDevelopmentManager, Conversation: "chat-dm"}, Answerer: exchange.Party{Role: domain.RoleArchitect}})
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v", attachments)
	}
	return attachments[0]
}

// The incident and its two later states, each read from the amendment log and
// the run store as they would stand: proposed with nothing decided (even with a
// landed run for the document beside it), approved with nothing written, and
// approved and landed at a commit. A run for the document that started before
// the approval is not this proposal's publication.
func TestAnAmendmentIsReadAsProposedDecidedAndPublishedSeparately(t *testing.T) {
	t.Parallel()

	before := documentRun("run-before", evidenceRaised, runstate.StatusSucceeded, "1111111")
	cases := []struct {
		name    string
		records []amendment.Record
		runs    fixedRuns
		want    []string
		absent  []string
	}{
		{
			name:    "proposed and undecided",
			records: []amendment.Record{evidenceProposal()},
			runs:    fixedRuns{states: []runstate.State{before, documentRun("run-after", evidenceObserved, runstate.StatusSucceeded, "2222222")}},
			want:    []string{"none: nothing has decided this proposal", "not written: nothing has been published, because nothing has been decided"},
			absent:  []string{"1111111", "2222222", "landed"},
		},
		{
			name:    "approved and not written",
			records: []amendment.Record{evidenceProposal(), evidenceApproval()},
			runs:    fixedRuns{states: []runstate.State{before}},
			want:    []string{"approved by the architect", "decided in: conversation chat-architect", "not written: the change is approved"},
			absent:  []string{"1111111"},
		},
		{
			name:    "approved and waiting",
			records: []amendment.Record{evidenceProposal(), evidenceApproval()},
			runs:    fixedRuns{waits: []runstate.DocumentWait{{Document: *documentRun("w", evidenceObserved, runstate.StatusPending, "").Document, Since: evidenceObserved}}},
			want:    []string{"written and waiting"},
		},
		{
			name:    "approved and in a run",
			records: []amendment.Record{evidenceProposal(), evidenceApproval()},
			runs:    fixedRuns{states: []runstate.State{documentRun("run-going", evidenceObserved, runstate.StatusRunning, "")}},
			want:    []string{"in a run: run run-going is publishing it"},
		},
		{
			name:    "approved and returned",
			records: []amendment.Record{evidenceProposal(), evidenceApproval()},
			runs:    fixedRuns{states: []runstate.State{documentRun("run-back", evidenceObserved, runstate.StatusFailed, "")}},
			want:    []string{"returned: run run-back did not land it"},
		},
		{
			name:    "approved and landed",
			records: []amendment.Record{evidenceProposal(), evidenceApproval()},
			runs:    fixedRuns{states: []runstate.State{before, documentRun("run-landed", evidenceObserved, runstate.StatusSucceeded, "9f8e7d6c5b4a")}},
			want:    []string{"approved by the architect", "landed at commit 9f8e7d6c5b4a, by run run-landed", "how this was found: matched by the document"},
			absent:  []string{"1111111"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attachment := resolveOne(t, amendmentReaders(tc.records, tc.runs), exchange.Reference{What: "amendment", ID: evidenceAmendmentID})
			if attachment.Status != exchange.EvidenceAttached || attachment.Source != "the amendment log" {
				t.Fatalf("attachment = %#v", attachment)
			}
			for _, heading := range []string{"Proposal:\n", "\nDecision:\n", "\nPublication:\n"} {
				if !strings.Contains(attachment.Text, heading) {
					t.Errorf("the three states are not stated separately; missing %q:\n%s", heading, attachment.Text)
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(attachment.Text, want) {
					t.Errorf("attachment does not say %q:\n%s", want, attachment.Text)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(attachment.Text, absent) {
					t.Errorf("attachment says %q:\n%s", absent, attachment.Text)
				}
			}
		})
	}
}

// An amendment nobody raised, and an id of the wrong shape, are absent rather
// than invented; an amendment log that cannot be read is unreadable.
func TestAnAmendmentThatCannotBeSuppliedIsSaidSo(t *testing.T) {
	t.Parallel()

	readers := amendmentReaders([]amendment.Record{evidenceProposal()}, fixedRuns{})
	for _, id := range []string{"amendment-" + strings.Repeat("0", 32), "amendment-fdc0"} {
		if attachment := resolveOne(t, readers, exchange.Reference{What: "amendment", ID: id}); attachment.Status != exchange.EvidenceAbsent {
			t.Errorf("%s = %#v, want absent", id, attachment)
		}
	}
	readers.amendments = fixedAmendments{err: errors.New("amendments.jsonl: permission denied")}
	if attachment := resolveOne(t, readers, exchange.Reference{What: "amendment", ID: evidenceAmendmentID}); attachment.Status != exchange.EvidenceUnreadable || !attachment.Retried {
		t.Errorf("attachment = %#v, want unreadable after a retry", attachment)
	}
}

type fixedTracker struct {
	items map[string]beads.WorkItem
	err   error
}

func (f fixedTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	if f.err != nil {
		return beads.WorkItem{}, f.err
	}
	item, found := f.items[id]
	if !found {
		return beads.WorkItem{}, fmt.Errorf("%w: %s", beads.ErrNoSuchWorkItem, id)
	}
	return item, nil
}

// A work item is the tracker read's rendering with its notes window; one the
// tracker does not hold is absent, whatever its prefix.
func TestAWorkItemIsWhatTheTrackerReadReturns(t *testing.T) {
	t.Parallel()

	readers := exchangeEvidenceReaders{productID: "yoyodyne", tracker: fixedTracker{items: map[string]beads.WorkItem{
		evidenceItemID: {ID: evidenceItemID, Title: "Recover an interrupted release tag", Status: "open", Notes: "Parked until the ruling is published."},
	}}}
	attachment := resolveOne(t, readers, exchange.Reference{What: "work-item", ID: evidenceItemID})
	if attachment.Status != exchange.EvidenceAttached || !strings.Contains(attachment.Text, "Recover an interrupted release tag") || !strings.Contains(attachment.Text, "Parked until the ruling is published.") {
		t.Fatalf("attachment = %#v", attachment)
	}
	if !strings.HasPrefix(attachment.Revision, "content ") {
		t.Errorf("Revision = %q, want a content revision", attachment.Revision)
	}
	if missing := resolveOne(t, readers, exchange.Reference{What: "work-item", ID: "yoyodyne-ifd.999"}); missing.Status != exchange.EvidenceAbsent {
		t.Errorf("missing = %#v, want absent", missing)
	}
	// The tracker's prefix need not be the product id: an item this product's
	// tracker holds under any prefix is attached, and one it does not hold is
	// no such record rather than refused by its name.
	readers.productID = "acme"
	if attached := resolveOne(t, readers, exchange.Reference{What: "work-item", ID: evidenceItemID}); attached.Status != exchange.EvidenceAttached {
		t.Errorf("an item under a prefix other than the product id = %#v, want it attached", attached)
	}
	if other := resolveOne(t, readers, exchange.Reference{What: "work-item", ID: "otherproduct-1"}); other.Status != exchange.EvidenceAbsent {
		t.Errorf("other = %#v, want absent", other)
	}
}

type fixedReports struct {
	reports   []report.Report
	handlings []report.Handling
}

func (f fixedReports) List() ([]report.Report, error)        { return f.reports, nil }
func (f fixedReports) Handlings() ([]report.Handling, error) { return f.handlings, nil }

// A report is its whole message, who filed it, and how it was handled; a run is
// its item, state, attempts, stop reason and outcome; another product's report
// is refused.
func TestReportsAndRunsAreReadFromTheirOwnRecords(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("the build can start against the ruling. ", 20)
	readers := exchangeEvidenceReaders{
		productID: "yoyodyne",
		reports: fixedReports{
			reports: []report.Report{
				{ID: "report-7ac7", Role: domain.RoleArchitect, RunID: "chat-architect", ProductID: "yoyodyne", Severity: report.SeverityNote, Message: long, RecordedAt: evidenceRaised},
				{ID: "report-other", Role: domain.RoleArchitect, ProductID: "elsewhere", Severity: report.SeverityNote, Message: "not ours", RecordedAt: evidenceRaised},
			},
			handlings: []report.Handling{{ReportID: "report-7ac7", Role: domain.RoleProductManager, Reason: "named as covering the build", RecordedAt: evidenceDecided}},
		},
		runs: fixedRuns{states: []runstate.State{documentRun("run-landed", evidenceObserved, runstate.StatusSucceeded, "9f8e7d6c5b4a")}},
	}
	filed := resolveOne(t, readers, exchange.Reference{What: "report", ID: "report-7ac7"})
	if filed.Status != exchange.EvidenceAttached || !strings.Contains(filed.Text, strings.TrimSpace(long)) || !strings.Contains(filed.Text, "named as covering the build") {
		t.Errorf("report = %#v, want the whole message and its handling", filed)
	}
	if other := resolveOne(t, readers, exchange.Reference{What: "report", ID: "report-other"}); other.Status != exchange.EvidenceRefused {
		t.Errorf("other product's report = %#v, want refused", other)
	}
	run := resolveOne(t, readers, exchange.Reference{What: "run", ID: "run-landed"})
	if run.Status != exchange.EvidenceAttached || !strings.Contains(run.Text, "outcome: landed at commit 9f8e7d6c5b4a") {
		t.Errorf("run = %#v", run)
	}
	if missing := resolveOne(t, readers, exchange.Reference{What: "run", ID: "run-nothing"}); missing.Status != exchange.EvidenceAbsent {
		t.Errorf("missing run = %#v, want absent", missing)
	}
}

type fixedConversations struct {
	conversations []runstate.Conversation
	events        map[string][]execution.Event
}

func (f fixedConversations) Recorded() ([]runstate.Conversation, error) { return f.conversations, nil }
func (f fixedConversations) LoadEvents(id string) ([]execution.Event, error) {
	return f.events[id], nil
}

// A turn is what was said to the role and its reply, counted as the
// conversation counts turns — one per provider call that completed — and only
// of a party's own conversation; a turn past the end is absent. One call that
// logged several agent messages is one turn whose reply is the last of them,
// and a call that failed is not a turn.
func TestATurnIsOneTurnOfAPartysOwnConversation(t *testing.T) {
	t.Parallel()

	sequence := uint64(0)
	event := func(kind execution.EventType, text string) execution.Event {
		sequence++
		payload := map[string]any{}
		if text != "" {
			payload["text"] = text
		}
		made, err := execution.NewEvent("chat-architect", sequence, evidenceRaised, kind, "harness.chat", payload)
		if err != nil {
			t.Fatal(err)
		}
		return made
	}
	readers := exchangeEvidenceReaders{
		productID: "yoyodyne",
		conversations: fixedConversations{
			conversations: []runstate.Conversation{{ConversationID: "chat-architect", Role: domain.RoleArchitect}, {ConversationID: "chat-pm", Role: domain.RoleProductManager}},
			events: map[string][]execution.Event{"chat-architect": {
				event(execution.EventOperatorMessage, "first question"),
				event(execution.EventRunStarted, ""),
				event(execution.EventAgentMessage, "looking at the repository first"),
				event(execution.EventAgentMessage, "first reply"),
				event(execution.EventRunCompleted, ""),
				event(execution.EventOperatorMessage, "second question"),
				event(execution.EventRunStarted, ""),
				event(execution.EventAgentMessage, "a reply that never finished"),
				event(execution.EventRunFailed, ""),
				event(execution.EventRunStarted, ""),
				event(execution.EventAgentMessage, "the ruling is drafted, not published"),
				event(execution.EventRunCompleted, ""),
			}},
		},
	}
	first := resolveOne(t, readers, exchange.Reference{What: "turn", ID: "chat-architect#1"})
	if first.Status != exchange.EvidenceAttached || !strings.Contains(first.Text, "first question") || !strings.Contains(first.Text, "the role replied:\nfirst reply") || strings.Contains(first.Text, "second") {
		t.Errorf("turn 1 = %#v, want the first call with its last message as the reply", first)
	}
	second := resolveOne(t, readers, exchange.Reference{What: "turn", ID: "chat-architect#2"})
	if second.Status != exchange.EvidenceAttached || !strings.Contains(second.Text, "second question") ||
		!strings.Contains(second.Text, "the ruling is drafted, not published") || strings.Contains(second.Text, "first") || strings.Contains(second.Text, "never finished") {
		t.Errorf("turn 2 = %#v, want the second completed call and nothing around it", second)
	}
	if past := resolveOne(t, readers, exchange.Reference{What: "turn", ID: "chat-architect#3"}); past.Status != exchange.EvidenceAbsent {
		t.Errorf("past = %#v, want absent", past)
	}
	if other := resolveOne(t, readers, exchange.Reference{What: "turn", ID: "chat-pm#1"}); other.Status != exchange.EvidenceRefused {
		t.Errorf("another role's turn = %#v, want refused", other)
	}
}

// The same round put to an architect answering on Claude Code and to one
// answering on Codex carries the same attachments, and the prompt each provider
// is sent renders them identically: cases one to seven of the design, side by
// side.
func TestTheSameRecordsAreAttachedOnEveryProvider(t *testing.T) {
	t.Parallel()

	failing := 0
	readers := amendmentReaders([]amendment.Record{evidenceProposal()}, fixedRuns{states: []runstate.State{documentRun("run-1", evidenceObserved, runstate.StatusSucceeded, "9f8e7d6c5b4a")}})
	readers.tracker = fixedTracker{items: map[string]beads.WorkItem{evidenceItemID: {ID: evidenceItemID, Title: "Recover an interrupted release tag", Status: "open", Notes: strings.Repeat("a long note. ", 1000)}}}
	readers.reports = fixedReports{}
	readers.redactValues = []string{"xoxb-secret"}
	evidence := readers.evidence()
	evidence.Sources[exchange.EvidenceDocument] = exchange.EvidenceSource{Store: "the artifact store", Read: func(context.Context, string) (exchange.EvidenceRecord, error) {
		return exchange.EvidenceRecord{Revision: "revision 4", Text: "the design now; token xoxb-secret\n" + strings.Repeat("body ", 2000)}, nil
	}}
	evidence.Sources[exchange.EvidenceReport] = exchange.EvidenceSource{Store: "the report pile", Read: func(context.Context, string) (exchange.EvidenceRecord, error) {
		failing++
		return exchange.EvidenceRecord{}, errors.New("report pile locked")
	}}
	refers := []exchange.Reference{
		{What: "amendment", ID: evidenceAmendmentID},                       // 1: proposed, undecided
		{What: "run", ID: "run-1"},                                         // 2: landed, with its commit
		{What: "run", ID: "run-nothing"},                                   // 3: absent
		{What: "document", ID: "release-recovery", Revision: "revision 3"}, // 4: moved, and cut to its bound
		{What: "report", ID: "report-1"},                                   // 5: unreadable
		{What: "work-item", ID: evidenceItemID},                            // 6: the tracker read's own rendering
		{What: "memory", ID: "architect"},                                  // 7: refused
	}

	prompts := map[domain.Backend]string{}
	attached := map[domain.Backend][]exchange.Attachment{}
	for _, answering := range []domain.Backend{domain.BackendClaudeCode, domain.BackendCodex} {
		cfg := answeringConfig()
		architect := cfg.Agents["architect"]
		architect.Backend = answering
		cfg.Agents["architect"] = architect
		provider := &capturingBackend{result: backend.RunResult{FinalText: "It is undecided.", CostUSD: 0.1}}
		conductor := exchange.Conductor{
			Store:     &memoryExchangeStore{},
			Voice:     exchangeVoice{config: cfg, provider: provider, repository: t.TempDir(), productID: "yoyodyne", redactValues: []string{"xoxb-secret"}},
			Reports:   &discardReports{},
			ProductID: "yoyodyne",
			MaxRounds: 3,
			Now:       func() time.Time { return evidenceObserved },
			NewID:     func() (string, error) { return "exchange-d892d320f8c090465f15a07007a3e29d", nil },
			Evidence:  evidence,
		}
		recorded, err := conductor.Put(context.Background(),
			exchange.Ask{Role: domain.RoleArchitect, Question: "does this amendment supply the ruling the item needs?", Refers: refers},
			exchange.Party{Role: domain.RoleDevelopmentManager, Conversation: "chat-dm"})
		if err != nil {
			t.Fatalf("%s: Put() = %v", answering, err)
		}
		if provider.calls != 1 {
			t.Fatalf("%s: the round reached the provider %d time(s)", answering, provider.calls)
		}
		attached[answering] = recorded.Rounds[0].Evidence
		prompt := provider.request.Prompt
		at := strings.Index(prompt, "## Records attached to this round")
		if at < 0 {
			t.Fatalf("%s: the prompt carries no records:\n%s", answering, prompt)
		}
		prompts[answering] = prompt[at:]
	}

	if !reflect.DeepEqual(attached[domain.BackendClaudeCode], attached[domain.BackendCodex]) {
		t.Errorf("the attachments differ between providers:\nclaude: %#v\ncodex:  %#v", attached[domain.BackendClaudeCode], attached[domain.BackendCodex])
	}
	if prompts[domain.BackendClaudeCode] != prompts[domain.BackendCodex] {
		t.Errorf("the records are rendered differently between providers")
	}
	statuses := []exchange.EvidenceStatus{}
	for _, attachment := range attached[domain.BackendCodex] {
		statuses = append(statuses, attachment.Status)
	}
	want := []exchange.EvidenceStatus{exchange.EvidenceAttached, exchange.EvidenceAttached, exchange.EvidenceAbsent, exchange.EvidenceAttached,
		exchange.EvidenceUnreadable, exchange.EvidenceAttached, exchange.EvidenceRefused}
	if !reflect.DeepEqual(statuses, want) {
		t.Errorf("statuses = %v, want %v", statuses, want)
	}
	codex := attached[domain.BackendCodex]
	if !codex[3].Moved || !codex[3].Cut || strings.Contains(prompts[domain.BackendCodex], "xoxb-secret") {
		t.Errorf("moved %v, cut %v, or a secret reached the prompt", codex[3].Moved, codex[3].Cut)
	}
	if !strings.Contains(codex[0].Text, "none: nothing has decided this proposal") {
		t.Errorf("the amendment is not undecided:\n%s", codex[0].Text)
	}
}

type memoryExchangeStore struct{ saved map[string]exchange.Exchange }

func (m *memoryExchangeStore) Save(recorded exchange.Exchange) error {
	if err := recorded.Validate(); err != nil {
		return err
	}
	if m.saved == nil {
		m.saved = map[string]exchange.Exchange{}
	}
	m.saved[recorded.ID] = recorded
	return nil
}

func (m *memoryExchangeStore) Load(id string) (exchange.Exchange, error) {
	recorded, found := m.saved[id]
	if !found {
		return exchange.Exchange{}, exchange.ErrNoExchange
	}
	return recorded, nil
}

type discardReports struct{}

func (discardReports) Append(report.Report) error { return nil }

type fixedDocuments struct{ set artifact.Set }

func (f fixedDocuments) Load() (artifact.Set, error) { return f.set, nil }

// A document is read from the checkout, so it is named as read at a commit only
// where the checkout's copy is that commit's; a file with uncommitted changes
// says so and names no commit it was not read at.
func TestADocumentNamesTheCommitItWasReadAtOnlyWhereItWas(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "docs", "release-recovery.md"), []byte("the recovery, as the file holds it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := artifact.Artifact{
		ID: "release-recovery", Kind: artifact.KindDesign, Title: "Release recovery", Status: artifact.StatusActive,
		Path:      "docs/release-recovery.md",
		Revisions: []artifact.Revision{{Action: artifact.ActionCreated, By: domain.RoleArchitect, At: evidenceRaised, Reason: "drafted"}},
	}
	for _, tc := range []struct {
		name     string
		state    fileState
		want     string
		revision string
	}{
		{"clean", fileState{commit: "abc1234"}, "read at commit: abc1234", "revision 0 at abc1234"},
		{"uncommitted changes", fileState{commit: "abc1234", dirty: true}, "read at commit: none; the file was read from the checkout, where it has changes not yet committed on top of abc1234", "revision 0, with uncommitted changes on abc1234"},
		{"unknown", fileState{}, "read at commit: not known", "revision 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readers := exchangeEvidenceReaders{
				productID:  "yoyodyne",
				repository: repository,
				documents:  fixedDocuments{set: artifact.Set{Artifacts: []artifact.Artifact{document}}},
				fileState:  func(context.Context, string) fileState { return tc.state },
			}
			attached := resolveOne(t, readers, exchange.Reference{What: "document", ID: "release-recovery"})
			if attached.Status != exchange.EvidenceAttached || !strings.Contains(attached.Text, tc.want) || !strings.Contains(attached.Text, "the recovery, as the file holds it") {
				t.Fatalf("attachment = %#v, want it to say %q", attached, tc.want)
			}
			if attached.Revision != tc.revision {
				t.Errorf("Revision = %q, want %q", attached.Revision, tc.revision)
			}
		})
	}
}

// The real reader tells a file with uncommitted changes from one without.
func TestGitFileStateSeesUncommittedChanges(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	git("init", "-q")
	path := filepath.Join(repository, "design.md")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "design.md")
	git("commit", "-q", "-m", "one")
	state := gitFileState(repository)(context.Background(), "design.md")
	if state.commit == "" || state.dirty {
		t.Fatalf("committed file = %#v, want a commit and no changes", state)
	}
	if err := os.WriteFile(path, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := gitFileState(repository)(context.Background(), "design.md"); !after.dirty || after.commit != state.commit {
		t.Errorf("edited file = %#v, want it marked as changed on %s", after, state.commit)
	}
}
