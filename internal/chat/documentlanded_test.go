package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// refusingPublisher is the publisher of a project with a companion intent
// repository: it refuses every confirmed document, every time.
type refusingPublisher struct{ offers int }

func (p *refusingPublisher) PublishesDocuments() bool { return true }

func (p *refusingPublisher) PublishDocument(context.Context, runstate.DocumentPublication) (runstate.DocumentDelivery, error) {
	p.offers++
	return runstate.DocumentDelivery{}, errors.New("this project keeps its intent in a companion intent repository, and the reviewed run that lands a document there is not built yet")
}

// fakeLandings answers from a map of path to text, on the target branch and at
// the one commit it knows.
type fakeLandings struct {
	repository string
	commit     string
	target     map[string]string
	atCommit   map[string]string
	readErr    error
}

func (f *fakeLandings) OnTarget(_ context.Context, path string) (LandedFile, error) {
	if f.readErr != nil {
		return LandedFile{}, f.readErr
	}
	content, found := f.target[path]
	return LandedFile{Repository: f.repository, Branch: "main", Commit: f.commit, Found: found, Content: []byte(content)}, nil
}

func (f *fakeLandings) AtCommit(_ context.Context, commit, path string) (LandedFile, error) {
	if commit != f.commit {
		return LandedFile{}, fmt.Errorf("commit %s is unknown", commit)
	}
	content, found := f.atCommit[path]
	return LandedFile{Repository: f.repository, Commit: f.commit, Found: found, Content: []byte(content)}, nil
}

const landedCommit = "0123456789abcdef0123456789abcdef01234567"

func landedPath(id string) string { return "docs/product/" + id + ".md" }

func landedWrite(id, body string) artifact.Write {
	return artifact.Write{Action: artifact.WriteCreate, ID: id, Kind: artifact.KindRules, Title: "Operating rules", Directory: "docs/product", Body: body, Reason: "yoyodyne-ifd.433.17 - rules already given", Intent: artifact.IntentConsistent}
}

// landedText is the file the store would render for a document with this body,
// which is what a confirmed candidate carries and what a repository holds once
// it landed.
func landedText(t *testing.T, id, body string) string {
	t.Helper()
	options, _ := documentOptions(t, &fakeBackend{})
	candidate, approved, err := options.Documents.(confirmationPreparer).PrepareConfirmation(domain.RoleProductManager, landedWrite(id, body), artifact.Policy{Goals: domain.ApprovalHuman}, "confirmed under policy", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || !approved {
		t.Fatalf("PrepareConfirmation() = %v, %v", approved, err)
	}
	return candidate.Content
}

// confirmedRecord is a document confirmed and held by a publisher that refuses
// it. before, where it is not empty, is the text the confirmed revision revises.
func confirmedRecord(t *testing.T, id, writeID, body, before string) *writeRecord {
	t.Helper()
	options, _ := documentOptions(t, &fakeBackend{})
	candidate, approved, err := options.Documents.(confirmationPreparer).PrepareConfirmation(domain.RoleProductManager, landedWrite(id, body), artifact.Policy{Goals: domain.ApprovalHuman}, "confirmed under policy", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || !approved {
		t.Fatalf("PrepareConfirmation() = %v, %v", approved, err)
	}
	if before != "" {
		candidate.Before = artifact.DocumentIdentity([]byte(before))
	}
	return &writeRecord{
		pending: PendingWrite{
			ID: writeID, Turn: 1,
			Write: landedWrite(id, body),
			Publication: &runstate.DocumentPublication{
				Candidate: candidate, WriteID: writeID, Turn: 1, Owner: domain.RoleProductManager,
				AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "opus",
			},
		},
	}
}

func heldSession(t *testing.T, landings DocumentLandings, records ...*writeRecord) (*Session, *refusingPublisher) {
	t.Helper()
	options, _ := documentOptions(t, &fakeBackend{})
	publisher := &refusingPublisher{}
	options.DocumentPublisher = publisher
	options.DocumentLandings = landings
	session := openTestSession(t, options)
	for _, record := range records {
		record.pending.ConversationID = session.state.ConversationID
		if record.pending.Publication != nil {
			record.pending.Publication.ConversationID = session.state.ConversationID
		}
		session.writes = append(session.writes, record)
	}
	return session, publisher
}

func landedEvents(t *testing.T, session *Session, eventType execution.EventType) []execution.Event {
	t.Helper()
	events, err := session.options.Store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	var found []execution.Event
	for _, event := range events {
		if event.Type == eventType {
			found = append(found, event)
		}
	}
	return found
}

func nextRevisionAccepted(t *testing.T, session *Session, id string) {
	t.Helper()
	write := landedWrite(id, "# Rules")
	if err := session.refuseCopies([]artifact.Write{write}); err != nil {
		t.Fatalf("a write of %s is refused: %v", id, err)
	}
	if err := session.refuseWrites(context.Background(), []artifact.Write{write}); err != nil {
		t.Fatalf("a write of %s is refused: %v", id, err)
	}
}

func TestAConfirmedDocumentFoundOnTheTargetAsConfirmedIsRecordedAsLanded(t *testing.T) {
	t.Parallel()

	content := landedText(t, "v2-goals", "# Goals")
	landings := &fakeLandings{repository: "the companion intent repository at /intent", commit: landedCommit, target: map[string]string{landedPath("v2-goals"): content + "\n"}}
	session, publisher := heldSession(t, landings, confirmedRecord(t, "v2-goals", "document-1.1", "# Goals", ""))
	if place := session.placeRevision(artifact.Write{ID: "v2-goals"}); place.behind != session.writes[0] {
		t.Fatal("a new revision did not wait behind the confirmed document before it was settled")
	}

	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if publisher.offers != 0 {
		t.Fatalf("publisher offered %d times, want a landed document never offered", publisher.offers)
	}
	if !session.writes[0].decided {
		t.Fatal("the landed document is still undecided")
	}
	told := session.state.PendingTrackerResults
	for _, want := range []string{"document-1.1 (v2-goals)", "main of the companion intent repository at /intent", "0123456789ab", "as confirmed", "waiting place is given back", "revision of what landed"} {
		if !strings.Contains(told, want) {
			t.Errorf("the role was told %q, want %q in it", told, want)
		}
	}
	events := landedEvents(t, session, execution.EventDocumentLanded)
	if len(events) != 1 || !strings.Contains(string(events[0].Payload), landedCommit) || !strings.Contains(string(events[0].Payload), "as confirmed") {
		t.Fatalf("landed events = %#v, want one naming the commit and how", events)
	}
	// The second pass tells nothing new.
	before := session.state.PendingTrackerResults
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if session.state.PendingTrackerResults != before || len(landedEvents(t, session, execution.EventDocumentLanded)) != 1 {
		t.Fatal("a second pass told the role again")
	}
	nextRevisionAccepted(t, session, "v2-goals")
}

func TestAConfirmedDocumentThatLandedInAnotherFormIsRecordedAsLanded(t *testing.T) {
	t.Parallel()

	landings := &fakeLandings{repository: "the companion intent repository at /intent", commit: landedCommit, target: map[string]string{landedPath("v2-goals"): landedText(t, "v2-goals", "# Goals, reworded by hand")}}
	session, _ := heldSession(t, landings, confirmedRecord(t, "v2-goals", "document-1.1", "# Goals", ""))

	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !session.writes[0].decided {
		t.Fatal("the document that landed in another form is still undecided")
	}
	if told := session.state.PendingTrackerResults; !strings.Contains(told, "in a form other than the one confirmed here") || !strings.Contains(told, "Read what landed before changing it") {
		t.Fatalf("the role was told %q", told)
	}
	if events := landedEvents(t, session, execution.EventDocumentLanded); len(events) != 1 || !strings.Contains(string(events[0].Payload), "in another form") {
		t.Fatalf("landed events = %#v", events)
	}
	nextRevisionAccepted(t, session, "v2-goals")
}

func TestARevisionNotYetLandedStaysHeld(t *testing.T) {
	t.Parallel()

	before := landedText(t, "v2-goals", "# Goals")
	for name, target := range map[string]map[string]string{
		"the file is still what the revision revises": {landedPath("v2-goals"): before},
		"no file":                         {},
		"another document is at the path": {landedPath("v2-goals"): landedText(t, "other", "x")},
	} {
		landings := &fakeLandings{repository: "the project's own repository", commit: landedCommit, target: target}
		session, publisher := heldSession(t, landings, confirmedRecord(t, "v2-goals", "document-1.1", "# Goals, revised", before))
		if err := session.PublishDocuments(context.Background()); err != nil {
			t.Fatal(err)
		}
		if session.writes[0].decided || strings.Contains(session.state.PendingTrackerResults, "landed") {
			t.Errorf("%s: decided = %v, told %q; want it held and nothing said about landing", name, session.writes[0].decided, session.state.PendingTrackerResults)
		}
		if publisher.offers != 1 {
			t.Errorf("%s: publisher offered %d times, want 1", name, publisher.offers)
		}
		if place := session.placeRevision(artifact.Write{ID: "v2-goals"}); place.behind != session.writes[0] {
			t.Errorf("%s: a new revision stopped waiting behind the confirmed document", name)
		}
	}
}

func TestAFailedReadOfTheTargetIsToldOnceAndLeavesTheDocumentHeld(t *testing.T) {
	t.Parallel()

	landings := &fakeLandings{commit: landedCommit, readErr: errors.New("the repository is not there")}
	session, _ := heldSession(t, landings, confirmedRecord(t, "v2-goals", "document-1.1", "# Goals", ""))
	for range 3 {
		if err := session.PublishDocuments(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if session.writes[0].decided {
		t.Fatal("a failed read decided the document")
	}
	if got := strings.Count(session.state.PendingTrackerResults, "the repository is not there"); got != 1 {
		t.Fatalf("the read failure was told %d times, want once: %q", got, session.state.PendingTrackerResults)
	}
}

func TestMarkLandedRefusesWhatTheCommitDoesNotHold(t *testing.T) {
	t.Parallel()

	landings := &fakeLandings{repository: "the companion intent repository at /intent", commit: landedCommit,
		atCommit: map[string]string{landedPath("v2-goals"): landedText(t, "other", "x")}}
	session, _ := heldSession(t, landings, confirmedRecord(t, "v2-goals", "document-1.1", "# Goals", ""))
	for name, call := range map[string]struct{ id, commit, want string }{
		"no such write":   {"document-9.9", landedCommit, "no document"},
		"unknown commit":  {"document-1.1", "deadbeef", "unknown"},
		"different id":    {"document-1.1", landedCommit, "a different document"},
		"no commit given": {"document-1.1", "", "name the commit"},
	} {
		_, err := session.MarkLanded(context.Background(), call.id, call.commit)
		if err == nil || !strings.Contains(err.Error(), call.want) {
			t.Errorf("%s: MarkLanded() error = %v, want %q", name, err, call.want)
		}
	}
	landings.atCommit = map[string]string{}
	if _, err := session.MarkLanded(context.Background(), "document-1.1", landedCommit); err == nil || !strings.Contains(err.Error(), "holds no file at docs/product/v2-goals.md") {
		t.Errorf("MarkLanded() with no file error = %v", err)
	}
	if session.writes[0].decided || len(landedEvents(t, session, execution.EventDocumentLanded)) != 0 || session.state.PendingTrackerResults != "" {
		t.Fatal("a refused /landed recorded something")
	}

	session.options.DocumentLandings = nil
	if _, err := session.MarkLanded(context.Background(), "document-1.1", landedCommit); err == nil || !strings.Contains(err.Error(), "nothing is configured") {
		t.Errorf("MarkLanded() with no reader error = %v", err)
	}
}

func TestMarkLandedGivesTheDocumentsPlaceBackWhicheverRepositoryHoldsIt(t *testing.T) {
	t.Parallel()

	for _, repository := range []string{"the companion intent repository at /intent", "the project's own repository at /project"} {
		content := landedText(t, "v2-goals", "# Goals")
		landings := &fakeLandings{repository: repository, commit: landedCommit, atCommit: map[string]string{landedPath("v2-goals"): content}}
		session, _ := heldSession(t, landings, confirmedRecord(t, "v2-goals", "document-1.1", "# Goals", ""))
		account, err := session.MarkLanded(context.Background(), "document-1.1", landedCommit)
		if err != nil {
			t.Fatalf("%s: %v", repository, err)
		}
		if !strings.Contains(account, repository) || !strings.Contains(account, "0123456789ab") {
			t.Errorf("%s: account = %q", repository, account)
		}
		if told := session.state.PendingTrackerResults; !strings.Contains(told, "landed by hand as commit 0123456789ab of "+repository) || !strings.Contains(told, "revision of what landed") {
			t.Errorf("%s: the role was told %q", repository, told)
		}
		events := landedEvents(t, session, execution.EventDocumentLanded)
		if len(events) != 1 || !strings.Contains(string(events[0].Payload), `"by":"operator"`) || !strings.Contains(string(events[0].Payload), `"as_confirmed":true`) {
			t.Errorf("%s: landed events = %#v", repository, events)
		}
		nextRevisionAccepted(t, session, "v2-goals")
	}
}

func TestMarkLandedAlsoSettlesADocumentStillWaitingForTheOperator(t *testing.T) {
	t.Parallel()

	content := landedText(t, "v2-goals", "# Goals")
	landings := &fakeLandings{repository: "the project's own repository", commit: landedCommit, atCommit: map[string]string{landedPath("v2-goals"): content}}
	waiting := confirmedRecord(t, "v2-goals", "document-1.1", "# Goals", "")
	waiting.pending.Publication = nil
	session, _ := heldSession(t, landings, waiting)
	if _, err := session.MarkLanded(context.Background(), "document-1.1", landedCommit); err != nil {
		t.Fatal(err)
	}
	if !waiting.decided {
		t.Fatal("a waiting document was not settled")
	}
	nextRevisionAccepted(t, session, "v2-goals")
}

func TestDropWriteGivesAHeldDocumentsPlaceBack(t *testing.T) {
	t.Parallel()

	var records []*writeRecord
	for index := 1; index <= runstate.MaxPendingWrites; index++ {
		id := fmt.Sprintf("held-%d", index)
		records = append(records, confirmedRecord(t, id, fmt.Sprintf("document-%d.1", index), "# Rules", ""))
	}
	session, _ := heldSession(t, nil, records...)
	fresh := artifact.Write{Action: artifact.WriteCreate, ID: "fresh", Kind: artifact.KindGoals, Title: "Fresh", Directory: "docs/product", Body: "# Goals", Reason: "drafted"}
	// The limit counts the write being added, so with every place taken it is
	// the one over it.
	if err := session.refuseWriteCount([]artifact.Write{fresh}, nil); err == nil {
		t.Fatal("held documents did not fill the places")
	}
	if _, err := session.DropWrite("document-1.1", "  "); err == nil || !strings.Contains(err.Error(), "recorded and told") {
		t.Fatalf("DropWrite() with no reason error = %v, want it refused", err)
	}
	if records[0].decided {
		t.Fatal("a refused drop decided the document")
	}

	account, err := session.DropWrite("document-1.1", "the intent repository already has it")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(account, "because: the intent repository already has it") {
		t.Errorf("account = %q", account)
	}
	if told := session.state.PendingTrackerResults; !strings.Contains(told, "The operator dropped document document-1.1 (held-1) because: the intent repository already has it. It is no longer offered and its waiting place is given back.") {
		t.Errorf("the role was told %q", told)
	}
	events := landedEvents(t, session, execution.EventDocumentDropped)
	if len(events) != 1 || !strings.Contains(string(events[0].Payload), "the intent repository already has it") {
		t.Errorf("dropped events = %#v", events)
	}
	if err := session.refuseWriteCount([]artifact.Write{fresh}, nil); err != nil {
		t.Fatalf("a new document is refused after a place was given back: %v", err)
	}
	if err := session.refuseWrites(context.Background(), []artifact.Write{fresh}); err != nil {
		t.Fatalf("a new document is refused after a place was given back: %v", err)
	}
	if _, err := session.DropWrite("document-1.1", "again"); err == nil {
		t.Fatal("a dropped document was dropped twice")
	}
}

func TestLandedAndDropDispatchThroughCommand(t *testing.T) {
	t.Parallel()

	content := landedText(t, "v2-goals", "# Goals")
	landings := &fakeLandings{repository: "the project's own repository", commit: landedCommit, atCommit: map[string]string{landedPath("v2-goals"): content}}
	session, _ := heldSession(t, landings,
		confirmedRecord(t, "v2-goals", "document-1.1", "# Goals", ""),
		confirmedRecord(t, "v2-other", "document-2.1", "# Other", ""))
	var out bytes.Buffer
	if err := session.Command(context.Background(), "/landed document-1.1 "+landedCommit, &out); err != nil || !session.writes[0].decided || !strings.Contains(out.String(), "recorded as landed") {
		t.Fatalf("/landed: err = %v, decided = %v, out = %q", err, session.writes[0].decided, out.String())
	}
	out.Reset()
	if err := session.Command(context.Background(), "/drop document-2.1 not wanted any more", &out); err != nil || !session.writes[1].decided || !strings.Contains(out.String(), "because: not wanted any more") {
		t.Fatalf("/drop: err = %v, decided = %v, out = %q", err, session.writes[1].decided, out.String())
	}
	if err := session.Command(context.Background(), "/drop document-2.1", &out); err == nil {
		t.Fatal("/drop with no id left to drop and no reason was accepted")
	}
}

func TestAnApprovalOfAConfirmedDocumentSaysWhatToDoInstead(t *testing.T) {
	t.Parallel()

	session, _ := heldSession(t, nil, confirmedRecord(t, "v2-goals", "document-1.1", "# Goals", ""))
	for _, decide := range []func() error{
		func() error { _, err := session.ApproveWrite("document-1.1"); return err },
		func() error { return session.DeclineWrite("document-1.1", "no") },
	} {
		err := decide()
		if err == nil || !strings.Contains(err.Error(), "/landed document-1.1 <commit>") || !strings.Contains(err.Error(), "/drop document-1.1 <reason>") {
			t.Fatalf("decision error = %v, want it to name /landed and /drop", err)
		}
	}
}
