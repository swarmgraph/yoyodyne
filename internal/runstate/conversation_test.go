package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestConversationStoreRoundTripsAcrossProcesses(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	if _, err := store.Load(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}); !errors.Is(err, ErrNoConversation) {
		t.Fatalf("Load() error = %v, want ErrNoConversation", err)
	}

	conversation := testConversation(t)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	conversation.ProviderSessionID = "session-1"
	conversation.ProviderModel = "opus"
	conversation.ProviderResolvedModel = "claude-opus-5-20260514"
	// The account the turn was answered on and the configuration in force while it
	// was travel with the backend and the selectors, because those four together
	// are what the durable-state-is-provider-independent invariant asks of a
	// provider invocation — and a conversation turn is one. Under a pool the alias
	// is the only thing that says whose subscription paid for it.
	conversation.AccountAlias = "research"
	conversation.ConfigRevision = "cfg-0123456789ab"
	// And so does the harness that answered it. A conversation an operator leaves
	// open is held by a process that goes on running whatever binary started it,
	// so which one that is has to be in the record rather than inferred from when
	// the conversation began.
	conversation.Build = "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900"
	conversation.Turns = 1
	conversation.PendingTrackerResults = "- t1.1: closed yoyodyne-2\n"
	// What the agent is reasoning from travels with the record, because the
	// process that briefed it is usually not the one that resumes it: a resumed
	// conversation that cannot say how old its picture is describes a repository
	// as it was and sounds exactly as certain about it.
	conversation.ContextGatheredAt = conversation.StartedAt
	conversation.ContextCommit = "a1a1a1a1a1a1"
	// So does the work item it last ran, for the same reason: "what did that
	// change" is a question about the run the operator last watched, and the
	// process that started it is often not the one they come back to.
	conversation.LastRunWorkItemID = "yoyodyne-ifd.39"
	// And so do the proposed changes the conversation has already put to the role
	// that owns the documents: a pending proposal stays pending until somebody
	// decides it, so a record that did not survive the process would deliver the
	// whole undecided queue again on every restart.
	conversation.DeliveredAmendmentIDs = []string{"amendment-0123456789abcdef0123456789abcdef"}
	conversation.LastSequence = 4
	conversation.UpdatedAt = conversation.StartedAt.Add(time.Minute)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() update error = %v", err)
	}

	// A second store over the same root is what a restarted process sees.
	loaded, err := newConversationStore(t, root).Load(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, conversation) {
		t.Fatalf("Load() = %#v, want %#v", loaded, conversation)
	}
}

// A picture a refresh read and no turn delivered outlives the process that read
// it: the record says which picture is waiting, and the text of it waits beside
// the record. It is beside rather than inside because a picture is close to the
// megabyte a record may be altogether — a record carrying one would be a record
// the store refused to save, which is a conversation that can record nothing at
// all in exchange for one saved re-read.
func TestAPictureWaitingForDeliveryOutlivesTheProcessThatReadIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	read := conversation.StartedAt.Add(time.Hour)
	conversation.PendingPicture = &PendingPicture{
		GatheredAt:                read,
		Commit:                    "b2b2b2b2b2b2",
		ShippedDocumentationBytes: 912345,
		Replaces:                  conversation.StartedAt,
		ReplacesCommit:            "a1a1a1a1a1a1",
		Commits:                   500,
		TrackerChanges:            40,
		Trigger:                   "harness",
		Threshold:                 20,
	}
	conversation.UpdatedAt = read
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.SavePendingPictureText(identity, "# Product context\n\nNewer.\n"); err != nil {
		t.Fatalf("SavePendingPictureText() error = %v", err)
	}

	// A second store over the same root is what the process that delivers it sees.
	next := newConversationStore(t, root)
	loaded, err := next.Load(identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded.PendingPicture, conversation.PendingPicture) {
		t.Fatalf("the waiting picture = %#v, want %#v", loaded.PendingPicture, conversation.PendingPicture)
	}
	text, err := next.PendingPictureText(identity)
	if err != nil {
		t.Fatalf("PendingPictureText() error = %v", err)
	}
	if text != "# Product context\n\nNewer.\n" {
		t.Fatalf("PendingPictureText() = %q", text)
	}
	// The text is not a conversation, so listing the conversations never reads it
	// as one.
	recorded, err := next.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("Recorded() = %d conversations, want the one", len(recorded))
	}

	// Delivering it clears both halves, and a conversation with nothing waiting
	// reports nothing rather than failing.
	if err := next.ClearPendingPictureText(identity); err != nil {
		t.Fatalf("ClearPendingPictureText() error = %v", err)
	}
	if err := next.ClearPendingPictureText(identity); err != nil {
		t.Fatalf("second ClearPendingPictureText() error = %v", err)
	}
	if text, err := next.PendingPictureText(identity); err != nil || text != "" {
		t.Fatalf("PendingPictureText() after clearing = %q, %v", text, err)
	}

	// A picture nothing can date or attribute is one nothing can deliver, so the
	// record refuses it rather than keeping a re-read nobody can use.
	undated := conversation
	undated.PendingPicture = &PendingPicture{Commit: "b2b2b2b2b2b2"}
	if err := store.Save(undated); err == nil || !strings.Contains(err.Error(), "must say when it was gathered") {
		t.Fatalf("Save() with an undated waiting picture error = %v", err)
	}
}

// The text beside the record is bounded, and the bound is above what a turn may
// be handed: a picture larger than that is one no conversation could carry
// anyway, and a file that grew without one is a state directory that fills up on
// a conversation nobody is having.
func TestThePictureWaitingBesideARecordIsBounded(t *testing.T) {
	t.Parallel()

	store := newConversationStore(t, t.TempDir())
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	oversized := strings.Repeat("x", MaxPendingPictureBytes+1)
	if err := store.SavePendingPictureText(identity, oversized); err == nil || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("SavePendingPictureText() oversized error = %v", err)
	}
}

func TestConversationStoreAppendsAndReadsEvents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	for sequence := uint64(1); sequence <= 2; sequence++ {
		event, err := execution.NewEvent(conversation.ConversationID, sequence, time.Now().UTC(), execution.EventAgentMessage, "provider.claude-code", nil)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		if err := store.AppendEvent(event); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}
	events, err := newConversationStore(t, root).LoadEvents(conversation.ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if len(events) != 2 || events[1].Sequence != 2 {
		t.Fatalf("LoadEvents() = %#v", events)
	}

	// An event that names something other than a conversation never reaches a
	// path.
	stray, err := execution.NewEvent("run-0123456789abcdef0123456789abcdef", 1, time.Now().UTC(), execution.EventAgentMessage, "provider.claude-code", nil)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(stray); err == nil || !strings.Contains(err.Error(), "conversation id is invalid") {
		t.Fatalf("AppendEvent() stray error = %v", err)
	}
}

func TestConversationStoreHoldsOneConversationAtATime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	held, err := newConversationStore(t, root).Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if _, err := newConversationStore(t, root).Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}); err == nil ||
		!strings.Contains(err.Error(), "already held") {
		t.Fatalf("second Hold() error = %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	// A released conversation is immediately available again.
	regained, err := newConversationStore(t, root).Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() after release error = %v", err)
	}
	if err := regained.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// Observing a conversation must take nothing from it. The four-line status asks
// this of every conversation on every reading and the heartbeat asks hourly, so
// a probe that took the lease and let it go again would be refusing the
// operator their own chat for the instant it held.
func TestConversationInFlightObservesWithoutTakingTheLease(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	for ask := 1; ask <= 3; ask++ {
		if inFlight, err := store.InFlight(identity); inFlight || err != nil {
			t.Fatalf("ask %d of a free conversation = %v, %v", ask, inFlight, err)
		}
	}

	held, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() after asking error = %v, want the conversation still free", err)
	}
	// A hold this process took is what another process holding it looks like from
	// outside: the stamp is written by whoever owns the lease, and read by
	// anybody.
	if inFlight, err := store.InFlight(identity); !inFlight || err != nil {
		t.Fatalf("a held conversation = %v, %v, want it reported in flight", inFlight, err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if inFlight, err := store.InFlight(identity); inFlight || err != nil {
		t.Fatalf("a released conversation = %v, %v, want it free again", inFlight, err)
	}
	// A hold is observable per agent, exactly as it is taken per agent.
	sibling := ConversationIdentity{Agent: "development-manager", Role: domain.RoleDevelopmentManager}
	lease, err := store.Hold(sibling)
	if err != nil {
		t.Fatalf("Hold(sibling) error = %v", err)
	}
	defer lease.Release()
	if inFlight, err := store.InFlight(identity); inFlight || err != nil {
		t.Fatalf("a sibling's hold reported on %s = %v, %v", identity, inFlight, err)
	}
}

// The whole of the defect: a conversation somebody wants to have must be there
// to take, however often something else is asking whether it is in use.
func TestAConversationIsNeverRefusedByAProbe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}

	asking := make(chan struct{})
	asked := make(chan error, 1)
	go func() {
		var problem error
		for {
			select {
			case <-asking:
				asked <- problem
				return
			default:
			}
			if _, err := store.InFlight(identity); err != nil && problem == nil {
				problem = err
			}
		}
	}()

	for attempt := 1; attempt <= 200; attempt++ {
		lease, err := store.Hold(identity)
		if err != nil {
			close(asking)
			<-asked
			t.Fatalf("Hold() attempt %d error = %v, want a conversation no probe can refuse", attempt, err)
		}
		if err := lease.Release(); err != nil {
			close(asking)
			<-asked
			t.Fatalf("Release() attempt %d error = %v", attempt, err)
		}
	}
	close(asking)
	if err := <-asked; err != nil {
		t.Fatalf("observing a conversation being held and released error = %v", err)
	}
}

// A holder that exits without releasing leaves its stamp behind, and the
// operating system has already dropped its lock. The probe's answer has to be
// the operating system's, or a killed chat would read as a turn in flight for
// as long as the state directory lasts.
func TestAStampFromADeadHolderIsNotATurnInFlight(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	lease, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	stamp := filepath.Join(store.Root(), "product-manager.holder")
	written, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatalf("the hold left no stamp to observe: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := os.Stat(stamp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a released hold left its stamp behind: %v", err)
	}

	// The stamp of a process that is gone, which is what a killed holder leaves.
	// It is put back by hand because nothing the store does can produce it.
	gone := exitedProcess(t)
	stale := strings.Replace(string(written), fmt.Sprintf(`"pid": %d`, os.Getpid()), fmt.Sprintf(`"pid": %d`, gone), 1)
	if stale == string(written) {
		t.Fatalf("the stamp does not name this process, so this test proves nothing:\n%s", written)
	}
	if err := os.WriteFile(stamp, []byte(stale), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if inFlight, err := store.InFlight(identity); inFlight || err != nil {
		t.Fatalf("a stamp from a process that has gone = %v, %v, want the conversation free", inFlight, err)
	}
	// And it is free to take, which is the answer the operating system was
	// already giving.
	regained, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() after a stale stamp error = %v", err)
	}
	if err := regained.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// A stamp that is there and will not decode is a failure to answer rather than
// an answer: a reader that guessed would be inventing whether somebody is
// mid-turn.
func TestAnUnreadableStampIsAProblemRatherThanAnAnswer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "product-manager.holder"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if inFlight, err := store.InFlight(identity); inFlight || err == nil {
		t.Fatalf("an unreadable stamp = %v, %v, want a stated problem", inFlight, err)
	}
	// A name that could never be a path is refused before anything is read, for
	// the same reason.
	if inFlight, err := store.InFlight(ConversationIdentity{Agent: "Not An Agent", Role: domain.RoleProductManager}); inFlight || err == nil {
		t.Fatalf("an unaskable identity = %v, %v, want a stated problem", inFlight, err)
	}
}

// exitedProcess is the identifier of a process that has run and gone, which is
// the closest thing a test has to a holder that was killed.
func exitedProcess(t *testing.T) int {
	t.Helper()

	command := exec.Command(os.Args[0], "-test.run=TestNoSuchTestExistsHere")
	command.Env = append(os.Environ(), "GO_TEST_EXITED_PROCESS=1")
	if err := command.Start(); err != nil {
		t.Fatalf("start a process to let it exit: %v", err)
	}
	pid := command.Process.Pid
	// The wait is what makes it gone rather than merely finishing, and it is also
	// what reaps it: a zombie is still a process the null signal reaches.
	if err := command.Wait(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("wait for the process to exit: %v", err)
		}
	}
	return pid
}

// A claim on a conversation can be put down and taken up again, which is what
// lets the operator's console stop being the reason nothing else can reach the
// agent. While it is down the conversation belongs to whoever takes it.
func TestAConversationPutDownIsReachableAndCanBeTakenUpAgain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	hold, err := newConversationStore(t, root).Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if !hold.Held() {
		t.Fatal("a fresh claim does not have the conversation")
	}
	// A claim is exclusive exactly as the plain lease is, for as long as it is
	// held: this is the first thing that would be wrong if it had stopped
	// locking at all.
	if _, err := newConversationStore(t, root).Hold(identity); err == nil || !errors.Is(err, ErrConversationHeld) {
		t.Fatalf("Hold() against a claimed conversation error = %v, want ErrConversationHeld", err)
	}

	if err := hold.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if hold.Held() {
		t.Fatal("a released claim still reports the conversation as held")
	}
	// The other process — the harness relaying, or the operator's assistant —
	// gets the conversation the moment the console is not mid-turn.
	other, err := newConversationStore(t, root).Hold(identity)
	if err != nil {
		t.Fatalf("Hold() against a released claim error = %v", err)
	}
	if err := other.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	if err := hold.Retake(context.Background()); err != nil {
		t.Fatalf("Retake() error = %v", err)
	}
	if !hold.Held() {
		t.Fatal("a retaken claim does not have the conversation")
	}
	// Taking up a conversation this process already has changes nothing rather
	// than deadlocking against itself.
	if err := hold.Retake(context.Background()); err != nil {
		t.Fatalf("Retake() while held error = %v", err)
	}
	if _, err := newConversationStore(t, root).Hold(identity); !errors.Is(err, ErrConversationHeld) {
		t.Fatalf("Hold() after Retake() error = %v, want ErrConversationHeld", err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("final Release() error = %v", err)
	}
}

// The process that owns a conversation defers its release for the life of the
// command, and the conversation is put down and taken up many times underneath
// that defer. So the deferred release routinely runs against a hold that is
// already down — every conversation that ends at the prompt is one — and it has
// to be a no-op rather than anything at all.
func TestReleasingAConversationThatIsAlreadyDownIsANoOp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	hold, err := newConversationStore(t, root).Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	// This is the deferred one, running behind a conversation that put itself
	// down at the prompt and never took it back.
	if err := hold.Release(); err != nil {
		t.Fatalf("Release() on a hold that is already down error = %v", err)
	}
	if hold.Held() {
		t.Fatal("a twice-released hold reports the conversation as held")
	}
	// And it let go of nothing it did not own: somebody else's claim, taken
	// while this one was down, survives the second release rather than being
	// dropped by it.
	other, err := newConversationStore(t, root).Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() by another process error = %v", err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("third Release() error = %v", err)
	}
	if _, err := newConversationStore(t, root).Hold(identity); !errors.Is(err, ErrConversationHeld) {
		t.Fatalf("Hold() error = %v, want ErrConversationHeld: a stale release dropped another process's claim", err)
	}
	if err := other.Release(); err != nil {
		t.Fatalf("other Release() error = %v", err)
	}
}

// The first claim waits too, which is what `yoyo chat --message` does when it
// arrives mid-turn. Refusing it was the other half of the seam: the harness
// relaying to the product manager, and a second window the operator opens
// beside the first, were both turned away by a turn that was over moments
// later. What the conversation serializes is turns, so an arriving claim queues
// behind one rather than failing on it.
//
// Nothing in it is a clock. The store says when the claim has found the
// conversation held and queued, and the claim itself is what says when it got
// through — so the test waits on each of those and on nothing else. It used to
// give the claim fifty milliseconds to wrongly take the conversation and thirty
// seconds to rightly take it, and under the race detector beside another suite
// the second of those was still too short: the polling goroutine was starved
// past it and took the conversation after the test had given up.
func TestAFirstClaimWaitsForAnInFlightTurnRatherThanRefusing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	// The turn already in flight, taken the way a console takes one back.
	mine, err := newConversationStore(t, root).Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	queued := make(chan struct{})
	store := newConversationStore(t, root)
	store.queued = func() { close(queued) }
	claimed := make(chan claimOutcome, 1)
	go func() {
		hold, err := store.Claim(context.Background(), identity)
		claimed <- claimOutcome{hold: hold, err: err}
	}()
	select {
	case <-queued:
	case outcome := <-claimed:
		if outcome.err != nil {
			t.Fatalf("a second claim was refused rather than queued: %v", outcome.err)
		}
		outcome.hold.Release()
		t.Fatal("a second claim took the conversation while a turn was in flight")
	}

	if err := mine.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	outcome := <-claimed
	if outcome.err != nil {
		t.Fatalf("Claim() error = %v after the turn in flight ended", outcome.err)
	}
	if !outcome.hold.Held() {
		t.Fatal("the claim that waited does not have the conversation")
	}
	if err := outcome.hold.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// claimOutcome is what a claim made on another goroutine came back with.
type claimOutcome struct {
	hold *ConversationHold
	err  error
}

// The caller that has something better to do than wait still gets its refusal.
// A background delivery has asked the agent nothing yet, so it comes back later
// rather than holding its lease open for the length of somebody else's turn.
func TestAClaimThatWillNotWaitIsRefusedWhileATurnIsInFlight(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	mine, err := newConversationStore(t, root).Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := newConversationStore(t, root).TryClaim(identity); !errors.Is(err, ErrConversationHeld) {
		t.Fatalf("TryClaim() error = %v, want ErrConversationHeld", err)
	}
	// Put down at the prompt is not mid-turn, so the delivery gets its turn
	// without the operator closing anything.
	if err := mine.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	delivery, err := newConversationStore(t, root).TryClaim(identity)
	if err != nil {
		t.Fatalf("TryClaim() against a conversation put down error = %v", err)
	}
	if err := delivery.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// A claim that cannot wait any longer gives up rather than hanging, which is
// what Ctrl-C at a `yoyo chat` that is waiting its turn amounts to.
func TestAClaimGivesUpWhenItsContextIsDone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	mine, err := newConversationStore(t, root).Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	defer mine.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := newConversationStore(t, root).Claim(ctx, identity); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Claim() error = %v, want a deadline", err)
	}
}

// A turn taken back at the prompt is as visible from outside as the first one
// was. The stamp and the lock are taken on one path, so a console mid-turn is
// what `yoyo agent list` and the standing status report — a retake that locked
// without stamping would have shown every such turn as an idle machine.
func TestAConversationTakenBackIsStampedAsInFlight(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	hold, err := store.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if inFlight, err := store.InFlight(identity); !inFlight || err != nil {
		t.Fatalf("a claimed conversation = %v, %v, want a turn in flight", inFlight, err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	// Put down at the prompt: nobody is talking to the agent, and no surface
	// should say otherwise.
	if inFlight, err := store.InFlight(identity); inFlight || err != nil {
		t.Fatalf("a conversation put down = %v, %v, want no turn in flight", inFlight, err)
	}
	if err := hold.Retake(context.Background()); err != nil {
		t.Fatalf("Retake() error = %v", err)
	}
	if inFlight, err := store.InFlight(identity); !inFlight || err != nil {
		t.Fatalf("a conversation taken back = %v, %v, want a turn in flight", inFlight, err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("final Release() error = %v", err)
	}
}

// Taking a conversation back waits for whoever has it rather than refusing: the
// operator has already typed, and the other process's turn ends on its own.
func TestTakingAConversationBackWaitsForWhoeverHasIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	queued := make(chan struct{})
	store := newConversationStore(t, root)
	store.queued = func() { close(queued) }
	hold, err := store.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	other, err := newConversationStore(t, root).Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	// As for the first claim above: the store says when the retake has queued,
	// and the retake says when it got through.
	retaken := make(chan error, 1)
	go func() { retaken <- hold.Retake(context.Background()) }()
	select {
	case <-queued:
	case err := <-retaken:
		t.Fatalf("Retake() returned %v while another process held the conversation", err)
	}

	if err := other.Release(); err != nil {
		t.Fatalf("other Release() error = %v", err)
	}
	if err := <-retaken; err != nil {
		t.Fatalf("Retake() error = %v", err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("final Release() error = %v", err)
	}
}

// A wait that cannot end is not one an operator has to serve: a cancelled
// context gives the conversation back to the caller as a failure rather than
// leaving them at a prompt that never answers.
func TestTakingAConversationBackGivesUpWhenItsContextIsDone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	hold, err := newConversationStore(t, root).Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	other, err := newConversationStore(t, root).Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer other.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := hold.Retake(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Retake() error = %v, want a deadline", err)
	}
	if hold.Held() {
		t.Fatal("a Retake() that gave up still reports the conversation as held")
	}
}

func TestConversationStoreRefusesForeignAndMalformedRecords(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	foreign := testConversation(t)
	foreign.ProductID = "other"
	if err := store.Save(foreign); err == nil || !strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("Save() foreign product error = %v", err)
	}

	conversation := testConversation(t)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	path := filepath.Join(store.Root(), string(domain.RoleProductManager)+".json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"conversation_id":"chat-1"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Load(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}); err == nil {
		t.Fatal("Load() malformed error = nil")
	}
}

func TestConversationValidateRejectsIncoherentRecords(t *testing.T) {
	t.Parallel()

	valid := testConversation(t)
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Conversation)
		want   string
	}{
		{name: "schema", mutate: func(c *Conversation) { c.SchemaVersion = 2 }, want: "schema_version"},
		{name: "id", mutate: func(c *Conversation) { c.ConversationID = "chat-nope" }, want: "conversation_id is invalid"},
		{name: "role", mutate: func(c *Conversation) { c.Role = "../escape" }, want: "role"},
		{name: "backend", mutate: func(c *Conversation) { c.Backend = "carrier pigeon" }, want: "backend is invalid"},
		{name: "turns", mutate: func(c *Conversation) { c.Turns = -1 }, want: "turns cannot be negative"},
		{
			name:   "unauditable turn",
			mutate: func(c *Conversation) { c.Turns = 1 },
			want:   "requires the requested model selector",
		},
		{
			name:   "backwards clock",
			mutate: func(c *Conversation) { c.UpdatedAt = c.StartedAt.Add(-time.Second) },
			want:   "updated_at cannot be before started_at",
		},
		{
			// A conversation may name no account, because one recorded before the
			// harness pinned them names none. What it may not do is name an account
			// nothing could have configured, which reads as evidence about whose
			// subscription answered it and is not.
			name:   "an account nothing configured",
			mutate: func(c *Conversation) { c.AccountAlias = "Someone Else's" },
			want:   "account_alias is not an account alias",
		},
		{
			name:   "a revision of no digest",
			mutate: func(c *Conversation) { c.ConfigRevision = "yesterday's" },
			want:   "config_revision is not a configuration revision",
		},
		{
			// And the same for the build: a conversation may name none, because one
			// recorded before the harness pinned it does and because a binary carrying
			// no revision of its own leaves it empty. What it may not do is name
			// something no repository could resolve, which reads as an answer to
			// "which harness held this" and is not one.
			name:   "a build that is not a revision",
			mutate: func(c *Conversation) { c.Build = "the one from Tuesday" },
			want:   "build is not a revision",
		},
		{
			// A picture may predate the conversation that carries it, because it
			// is assembled before the record exists. What it may not do is claim
			// to have been taken later than the record was written, which would
			// make every comparison against it read as fresher than it is.
			name:   "a picture from the future",
			mutate: func(c *Conversation) { c.ContextGatheredAt = c.UpdatedAt.Add(time.Second) },
			want:   "context_gathered_at cannot be after updated_at",
		},
		{
			// What waits inside the record has to keep the record loadable, so an
			// unbounded account of pending results is refused where it is written
			// rather than discovered when the next process cannot read it.
			name:   "unbounded pending results",
			mutate: func(c *Conversation) { c.PendingTrackerResults = strings.Repeat("x", MaxPendingTrackerResultBytes+1) },
			want:   "pending tracker results are",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			conversation := testConversation(t)
			test.mutate(&conversation)
			if err := conversation.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestNewConversationIDIsUniqueAndWellFormed(t *testing.T) {
	t.Parallel()

	first, err := NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	second, err := NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	if first == second || !conversationIDPattern.MatchString(first) {
		t.Fatalf("conversation ids = %q, %q", first, second)
	}
}

func newConversationStore(t *testing.T, root string) *ConversationStore {
	t.Helper()

	store, err := NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	return store
}

func testConversation(t *testing.T) Conversation {
	t.Helper()

	id, err := NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	started := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	return Conversation{
		SchemaVersion:  ConversationSchemaVersion,
		ConversationID: id,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Role:           domain.RoleProductManager,
		Backend:        domain.BackendClaudeCode,
		StartedAt:      started,
		UpdatedAt:      started,
	}
}

// Two agents filling one role hold two conversations, with two provider
// sessions and two leases. A store that keyed on the role alone would have each
// of them resuming the other's session under its own persona and model.
func TestConversationsAreKeptPerAgentRatherThanPerRole(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	house := ConversationIdentity{Agent: "house-architect", Role: domain.RoleArchitect}
	visiting := ConversationIdentity{Agent: "visiting-architect", Role: domain.RoleArchitect}

	recorded := testConversation(t)
	recorded.Agent = house.Agent
	recorded.Role = house.Role
	recorded.ProviderSessionID = "session-house"
	recorded.ProviderModel = "opus"
	recorded.Turns = 1
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(house)
	if err != nil || loaded.ProviderSessionID != "session-house" {
		t.Fatalf("Load(house) = %q, err = %v", loaded.ProviderSessionID, err)
	}
	if _, err := store.Load(visiting); !errors.Is(err, ErrNoConversation) {
		t.Fatalf("Load(visiting) error = %v, want ErrNoConversation", err)
	}

	// The lease is per agent for the same reason: one architect talking must not
	// stop the other, because they are not sharing anything to serialize.
	held, err := store.Hold(house)
	if err != nil {
		t.Fatalf("Hold(house) error = %v", err)
	}
	defer held.Release()
	if _, err := store.Hold(house); !errors.Is(err, ErrConversationHeld) {
		t.Fatalf("Hold(house) twice error = %v, want ErrConversationHeld", err)
	}
	sibling, err := store.Hold(visiting)
	if err != nil {
		t.Fatalf("Hold(visiting) error = %v, want it free while its sibling is held", err)
	}
	defer sibling.Release()

	// A record whose agent disagrees with the file it is in is somebody's edit
	// rather than a conversation, and resuming it would put one agent's session
	// behind another's persona. It is written by hand here because nothing the
	// store does can produce it.
	misfiled, err := os.ReadFile(filepath.Join(store.Root(), "house-architect.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	rewritten := strings.Replace(string(misfiled), `"agent": "house-architect"`, `"agent": "visiting-architect"`, 1)
	if rewritten == string(misfiled) {
		t.Fatalf("the record does not name its agent, so this test proves nothing:\n%s", misfiled)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "house-architect.json"), []byte(rewritten), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Load(house); err == nil || !strings.Contains(err.Error(), "belongs to agent") {
		t.Fatalf("Load(house) error = %v, want a refusal naming the agent", err)
	}
}

// A conversation recorded before the agent was part of the identity keeps
// loading, under the agent named for its role — the only agent that could have
// written it — and acquires the agent the next time it is saved.
func TestAConversationRecordedWithoutAnAgentStillLoads(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	legacy := testConversation(t)
	legacy.ProviderSessionID = "session-1"
	legacy.ProviderModel = "opus"
	legacy.Turns = 1
	if legacy.Agent != "" {
		t.Fatal("the fixture already names an agent; this test proves nothing")
	}
	if err := store.Save(legacy); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "product-manager.json")); err != nil {
		t.Fatalf("the record is not where the role-keyed layout put it: %v", err)
	}

	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	loaded, err := store.Load(identity)
	if err != nil || loaded.ProviderSessionID != "session-1" {
		t.Fatalf("Load() = %q, err = %v", loaded.ProviderSessionID, err)
	}
	loaded.Agent = identity.Agent
	if err := store.Save(loaded); err != nil {
		t.Fatalf("Save() stamped error = %v", err)
	}
	stamped, err := store.Load(identity)
	if err != nil || stamped.Agent != "product-manager" {
		t.Fatalf("Load() agent = %q, err = %v", stamped.Agent, err)
	}
}

// Something has to be able to ask what conversations this product has held
// without knowing which agents were configured for it. The reporting sink is the
// first thing that does, and what it lists is what it will read logs from.
func TestConversationStoreListsEveryRecordedConversation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	if recorded, err := store.Recorded(); err != nil || len(recorded) != 0 {
		t.Fatalf("Recorded() on an untouched product = %v, %v", recorded, err)
	}

	manager := testConversation(t)
	manager.Agent = "product-manager"
	if err := store.Save(manager); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	developmentManager := testConversation(t)
	developmentManager.Agent = "development-manager"
	developmentManager.Role = domain.RoleDevelopmentManager
	if err := store.Save(developmentManager); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// The leases and the event logs live in the same directory, and only a file
	// named for an agent holds a conversation.
	lease, err := store.Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer lease.Release()
	event, err := execution.NewEvent(manager.ConversationID, 1, manager.StartedAt, execution.EventAgentMessage, "harness.chat", nil)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	recorded, err := store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("Recorded() = %#v, want the two conversations", recorded)
	}
	found := map[string]domain.AgentRole{}
	for _, conversation := range recorded {
		found[conversation.ConversationID] = conversation.Role
	}
	if found[manager.ConversationID] != domain.RoleProductManager || found[developmentManager.ConversationID] != domain.RoleDevelopmentManager {
		t.Fatalf("Recorded() = %#v, want each conversation under the role that holds it", found)
	}
}

func TestConversationStoreRefusesToListARecordItCannotRead(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	if err := store.Save(testConversation(t)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "product-manager.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Recorded(); err == nil || !strings.Contains(err.Error(), "product-manager.json") {
		t.Fatalf("Recorded() error = %v, want the file it could not read named", err)
	}
}

// The undecided proposals a conversation carries are the largest thing in its
// record, so a conversation that filled the bound must still be one that saves.
// The failure this guards is total: a record that cannot be written is a
// conversation that cannot take another turn.
func TestAConversationFullOfUndecidedProposalsStillSaves(t *testing.T) {
	t.Parallel()

	store, err := NewConversationStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	now := time.Now().UTC()
	conversation := Conversation{
		SchemaVersion:  ConversationSchemaVersion,
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Agent:          "product-manager",
		Role:           "product-manager",
		Backend:        domain.BackendClaudeCode,
		StartedAt:      now,
		UpdatedAt:      now,
		// And the rest of what the record carries at its own bounds, because the
		// state file holds all of it at once or none of it.
		PendingTrackerResults: strings.Repeat("r", MaxPendingTrackerResultBytes),
	}
	for i := 0; i < MaxPendingProposals; i++ {
		conversation.PendingProposals = append(conversation.PendingProposals, PendingProposal{
			ID:          fmt.Sprintf("%d.1", i+1),
			Turn:        i + 1,
			Title:       strings.Repeat("t", 200),
			Description: strings.Repeat("d", 8<<10),
			Rationale:   strings.Repeat("w", 8<<10),
			Goal:        strings.Repeat("g", 400),
		})
	}
	// And the questions nobody has answered, at their own bound and each at the
	// bounds the concern contract allows, beside the proposals rather than
	// instead of them.
	for i := 0; i < MaxPendingConcerns; i++ {
		conversation.PendingConcerns = append(conversation.PendingConcerns, PendingConcern{
			ID:       fmt.Sprintf("c%d.1", i+1),
			Turn:     i + 1,
			Kind:     "conflict",
			Subject:  strings.Repeat("s", 200),
			Goal:     strings.Repeat("g", 200),
			Detail:   strings.Repeat("d", 4<<10),
			Question: strings.Repeat("q", 4<<10),
			Options:  []string{strings.Repeat("a", 200), strings.Repeat("b", 200)},
		})
	}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(ConversationIdentity{Agent: "product-manager", Role: "product-manager"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.PendingProposals) != MaxPendingProposals {
		t.Fatalf("loaded %d proposal(s), want %d", len(loaded.PendingProposals), MaxPendingProposals)
	}
	if len(loaded.PendingConcerns) != MaxPendingConcerns || len(loaded.PendingConcerns[0].Options) != 2 {
		t.Fatalf("loaded %d concern(s), want %d with their answers", len(loaded.PendingConcerns), MaxPendingConcerns)
	}
	// One past either bound, or a question nobody can name, is refused.
	conversation.PendingConcerns = append(conversation.PendingConcerns, PendingConcern{ID: "c99.1"})
	if err := store.Save(conversation); err == nil || !strings.Contains(err.Error(), "unanswered concerns") {
		t.Fatalf("Save() past the concern bound error = %v, want the bound named", err)
	}
	conversation.PendingConcerns = []PendingConcern{{Turn: 1}}
	if err := store.Save(conversation); err == nil || !strings.Contains(err.Error(), "pending_concerns[0] has no id") {
		t.Fatalf("Save() of a nameless concern error = %v, want it refused", err)
	}
}

// A refused tracker block is claimed for a wakeup once.
//
// The claim is what makes the wakeup at most once per refusal. Two sessions
// polling one record must produce one turn, and a pass that reads the claim
// afterwards must find nothing to do — otherwise a refusal the role cannot answer
// is a turn spent on every pull, for as long as it stands.
func TestARefusedTrackerBlockIsClaimedForAWakeupOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	refused := conversation.StartedAt.Add(time.Minute)
	conversation.Turns = 1
	conversation.ProviderModel = "opus"
	conversation.UpdatedAt = refused
	conversation.RefusedBlock = &TrackerRefusal{
		Turn:      1,
		Actions:   3,
		Problem:   "decode tracker actions: actions[0]: reason is the parking reason at 512 bytes, limit is 480",
		RefusedAt: refused,
	}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	identity := conversation.Identity()
	woke := refused.Add(time.Minute)
	claimed, err := store.ClaimRefusalWakeup(identity, woke)
	if err != nil {
		t.Fatalf("ClaimRefusalWakeup() error = %v", err)
	}
	if !claimed.WokenAt.Equal(woke) || claimed.Turn != 1 {
		t.Fatalf("claimed = %#v, want the refusal of turn 1 marked woken at %s", claimed, woke)
	}
	// What the wakeup carries is the refusal in the harness's own words, so the
	// turn it starts and the record it was claimed from are about one thing.
	if claimed.Problem != conversation.RefusedBlock.Problem {
		t.Fatalf("claimed refusal = %q, want the recorded one", claimed.Problem)
	}

	// A second pass — another session, another process — finds nothing owed.
	if _, err := newConversationStore(t, root).ClaimRefusalWakeup(identity, woke.Add(time.Minute)); !errors.Is(err, ErrNoRefusalAwaitingWakeup) {
		t.Fatalf("second ClaimRefusalWakeup() error = %v, want ErrNoRefusalAwaitingWakeup", err)
	}

	// And so does a pass over a refusal the harness has handed to the operator
	// instead, which is the other way a refusal stops being one to wake for.
	escalated := testConversation(t)
	escalated.Agent = "second-product-manager"
	escalated.Turns = 1
	escalated.ProviderModel = "opus"
	escalated.RefusedBlock = &TrackerRefusal{
		Turn:      2,
		Problem:   "decode tracker actions: actions[0]: handle report \"nope\" is not a report identifier",
		RefusedAt: refused,
		Escalated: "the harness woke this conversation to correct the refusal of turn 1 and the block it sent back was refused too",
	}
	if err := store.Save(escalated); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := store.ClaimRefusalWakeup(escalated.Identity(), woke); !errors.Is(err, ErrNoRefusalAwaitingWakeup) {
		t.Fatalf("ClaimRefusalWakeup() on an escalated refusal error = %v, want ErrNoRefusalAwaitingWakeup", err)
	}
}

// A wakeup the provider refused for want of capacity is given back, and the
// pacing is what keeps the give-back from being a burst.
//
// The wakeup runs on the non-model side so that a provider window cannot stop it
// being scheduled; that is worth nothing if a window silently spends the one turn
// a refusal is owed. So the attempt comes back and the moment of it does not: the
// refusal is claimable again once the delay has passed, and not before.
func TestAWakeupTheProviderRefusedIsGivenBackAndPaced(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	refused := conversation.StartedAt.Add(time.Minute)
	conversation.Turns = 1
	conversation.ProviderModel = "opus"
	conversation.UpdatedAt = refused
	conversation.RefusedBlock = &TrackerRefusal{
		Turn:      1,
		Actions:   2,
		Problem:   "decode tracker actions: unexpected trailing content after the actions",
		RefusedAt: refused,
	}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	identity := conversation.Identity()
	windowed := refused.Add(time.Minute)
	if _, err := store.ClaimRefusalWakeup(identity, windowed); err != nil {
		t.Fatalf("ClaimRefusalWakeup() error = %v", err)
	}
	returned, err := store.WithdrawRefusalWakeup(context.Background(), identity, 1, "the provider declined this turn for want of capacity")
	if err != nil {
		t.Fatalf("WithdrawRefusalWakeup() error = %v", err)
	}
	given := loadRefusal(t, store, identity)
	if returned.Turn != given.Turn || returned.Attempts != given.Attempts || !returned.WokenAt.IsZero() || returned.Escalated != "" {
		t.Fatalf("WithdrawRefusalWakeup() = %#v, want the refusal as the record now holds it, %#v", returned, given)
	}
	// The turn is back, so the window has not spent the only one the refusal had;
	// the attempt and the moment of it stand, so the retry is both bounded and
	// paced rather than made on the very next pull and made for ever.
	if !given.WokenAt.IsZero() {
		t.Fatalf("refusal = %#v, want the turn it never took given back", given)
	}
	if given.Attempts != 1 || !given.LastAttemptAt.Equal(windowed) {
		t.Fatalf("refusal = %#v, want the attempt kept so the bound can be reached", given)
	}
	if given.AwaitingWakeup(windowed.Add(time.Second)) {
		t.Fatalf("refusal = %#v, want it left alone until %s has passed", given, RefusalWakeupRetryDelay)
	}
	if !given.AwaitingWakeup(windowed.Add(RefusalWakeupRetryDelay)) {
		t.Fatalf("refusal = %#v, want it claimable once the delay has passed", given)
	}
	// And a pass that comes back after the delay claims it, which is the wakeup
	// surviving the window rather than being spent by it.
	if _, err := store.ClaimRefusalWakeup(identity, windowed.Add(RefusalWakeupRetryDelay)); err != nil {
		t.Fatalf("ClaimRefusalWakeup() after the delay error = %v", err)
	}
}

// The give-back is bounded: a provider that goes on refusing stops earning
// wakeups rather than earning one every quarter of an hour for ever, and what the
// refusal falls back to is the role's own next turn.
//
// The exhausted state is reached through the store's own calls rather than
// written by hand, because a bound only holds if the code can actually walk into
// it: the first version of this gave the attempt back along with the turn, so the
// count never passed one and the arm that stops the trying was unreachable.
func TestWakeupsForOneRefusalAreBounded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	refused := conversation.StartedAt.Add(time.Minute)
	conversation.Turns = 1
	conversation.ProviderModel = "opus"
	conversation.UpdatedAt = refused
	conversation.RefusedBlock = &TrackerRefusal{
		Turn:      1,
		Problem:   "decode tracker actions: the tracker block is empty",
		RefusedAt: refused,
	}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// A provider that never takes a wakeup — out of capacity, down, or signed out:
	// each one claims, puts nothing in front of the role, and gives the turn back.
	identity := conversation.Identity()
	at := refused
	var last time.Time
	for attempt := 1; attempt <= MaxRefusalWakeups; attempt++ {
		claimed, err := store.ClaimRefusalWakeup(identity, at)
		if err != nil {
			t.Fatalf("ClaimRefusalWakeup() attempt %d error = %v", attempt, err)
		}
		if claimed.Attempts != attempt {
			t.Fatalf("attempt %d recorded %d attempt(s), want the count to accumulate", attempt, claimed.Attempts)
		}
		given, err := store.WithdrawRefusalWakeup(context.Background(), identity, 1, "the provider is answering nobody: not logged in")
		if err != nil {
			t.Fatalf("WithdrawRefusalWakeup() attempt %d error = %v", attempt, err)
		}
		// Every give-back but the last leaves the refusal owed a turn and says
		// nothing to the operator; the last is the one that hands it over.
		if exhausted := given.Escalated != ""; exhausted != (attempt == MaxRefusalWakeups) {
			t.Fatalf("attempt %d gave back %#v, want it handed to the operator only on attempt %d", attempt, given, MaxRefusalWakeups)
		}
		last = at
		at = at.Add(RefusalWakeupRetryDelay)
	}

	// The attempts are gone, so no later pass makes another however long it waits.
	spent := loadRefusal(t, store, identity)
	if spent.Attempts != MaxRefusalWakeups {
		t.Fatalf("refusal = %#v, want every attempt counted", spent)
	}
	if spent.AwaitingWakeup(at.Add(24 * time.Hour)) {
		t.Fatalf("refusal = %#v, want the harness to have stopped trying after %d attempts", spent, MaxRefusalWakeups)
	}
	if _, err := store.ClaimRefusalWakeup(identity, at.Add(24*time.Hour)); !errors.Is(err, ErrNoRefusalAwaitingWakeup) {
		t.Fatalf("ClaimRefusalWakeup() error = %v, want ErrNoRefusalAwaitingWakeup", err)
	}
	// And it did not go quiet. The record says why the harness stopped, and the
	// conversation's own log carries the unresolved refusal every surface already
	// says, stamped with the attempt that spent the bound.
	if !strings.Contains(spent.Escalated, "provider never took the turn") || !spent.WokenAt.IsZero() {
		t.Fatalf("refusal = %#v, want it handed to the operator as one no turn was ever taken for", spent)
	}
	events, err := store.LoadEvents(conversation.ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].Type != execution.EventTrackerRefusalUnresolved || !events[0].Timestamp.Equal(last) {
		t.Fatalf("events = %#v, want exactly one unresolved refusal at %s", events, last)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode the unresolved refusal: %v", err)
	}
	if payload["attempts"] != float64(MaxRefusalWakeups) || payload["never_taken"] != "the provider is answering nobody: not logged in" ||
		payload["woken"] != false || payload["refused_again"] != false || payload["problem"] != spent.Problem {
		t.Fatalf("payload = %#v, want the exhausted wakeups said with the refusal and what the last attempt met", payload)
	}
	recorded, err := store.Load(identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.LastSequence != events[0].Sequence {
		t.Fatalf("last sequence = %d, want the record to carry the event it wrote, %d", recorded.LastSequence, events[0].Sequence)
	}
}

// A give-back for a refusal that has since been answered and replaced must not
// re-open the one that took its place.
func TestAGiveBackNamingAnAnsweredRefusalChangesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	refused := conversation.StartedAt.Add(time.Minute)
	conversation.Turns = 4
	conversation.ProviderModel = "opus"
	conversation.UpdatedAt = refused
	conversation.RefusedBlock = &TrackerRefusal{
		Turn:          4,
		Problem:       "decode tracker actions: the tracker block is empty",
		RefusedAt:     refused,
		Attempts:      1,
		LastAttemptAt: refused,
		WokenAt:       refused,
	}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := store.WithdrawRefusalWakeup(context.Background(), conversation.Identity(), 1, "the provider declined this turn for want of capacity"); err != nil {
		t.Fatalf("WithdrawRefusalWakeup() error = %v", err)
	}
	standing := loadRefusal(t, store, conversation.Identity())
	if standing.WokenAt.IsZero() || standing.Attempts != 1 {
		t.Fatalf("refusal = %#v, want a give-back naming another turn to have changed nothing", standing)
	}
}

func loadRefusal(t *testing.T, store *ConversationStore, identity ConversationIdentity) TrackerRefusal {
	t.Helper()

	recorded, err := store.Load(identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.RefusedBlock == nil {
		t.Fatalf("the conversation records no refused block")
	}
	return *recorded.RefusedBlock
}

// The picture an agent last received is kept beside its record, bounded as the
// waiting one is, so a later refresh can be delivered as what moved since it.
func TestThePictureAnAgentLastReceivedIsKeptBesideItsRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "development-manager", Role: domain.RoleDevelopmentManager}
	store := newConversationStore(t, root)
	if text, err := store.DeliveredPictureText(identity); err != nil || text != "" {
		t.Fatalf("DeliveredPictureText() before anything was kept = %q, %v", text, err)
	}
	if err := store.SaveDeliveredPictureText(identity, "# Product context\n\nDelivered.\n"); err != nil {
		t.Fatalf("SaveDeliveredPictureText() error = %v", err)
	}
	// Kept apart from the waiting picture: delivering one never loses the other.
	if err := store.SavePendingPictureText(identity, "# Product context\n\nWaiting.\n"); err != nil {
		t.Fatalf("SavePendingPictureText() error = %v", err)
	}
	text, err := newConversationStore(t, root).DeliveredPictureText(identity)
	if err != nil || text != "# Product context\n\nDelivered.\n" {
		t.Fatalf("DeliveredPictureText() = %q, %v", text, err)
	}
	// It is not a conversation record, so listing the conversations passes it by.
	if _, unreadable, err := store.RecordedReadable(); err != nil || len(unreadable) != 0 {
		t.Fatalf("RecordedReadable() = %v, %v", unreadable, err)
	}
	if err := store.ClearDeliveredPictureText(identity); err != nil {
		t.Fatalf("ClearDeliveredPictureText() error = %v", err)
	}
	if err := store.ClearDeliveredPictureText(identity); err != nil {
		t.Fatalf("second ClearDeliveredPictureText() error = %v", err)
	}
	if text, err := store.DeliveredPictureText(identity); err != nil || text != "" {
		t.Fatalf("DeliveredPictureText() after clearing = %q, %v", text, err)
	}
	oversized := strings.Repeat("x", MaxPendingPictureBytes+1)
	if err := store.SaveDeliveredPictureText(identity, oversized); err == nil || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("SaveDeliveredPictureText() oversized error = %v", err)
	}
}

// HeldBy names the conversations one process is holding, and nothing once it
// lets them go or for a process holding none.
func TestHeldByNamesTheConversationsOneProcessHolds(t *testing.T) {
	t.Parallel()

	store := newConversationStore(t, t.TempDir())
	held, err := store.Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if agents, err := store.HeldBy(os.Getpid()); err != nil || strings.Join(agents, ",") != "product-manager" {
		t.Fatalf("HeldBy(this process) = %v, %v, want the conversation it holds", agents, err)
	}
	if agents, err := store.HeldBy(os.Getpid() + 1); err != nil || len(agents) != 0 {
		t.Fatalf("HeldBy(another process) = %v, %v, want nothing", agents, err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if agents, err := store.HeldBy(os.Getpid()); err != nil || len(agents) != 0 {
		t.Fatalf("HeldBy(this process) after release = %v, %v, want nothing", agents, err)
	}
}

// Documents held for sending are bounded apart from the ones waiting for a
// decision or publication, so two held documents leave room for two more of
// the others, and the held ones have their own bound.
func TestConversationBoundsHeldDocumentsApart(t *testing.T) {
	t.Parallel()

	write := func(id string, held bool) PendingWrite {
		pending := PendingWrite{ID: id, Turn: 1, Action: "create", Artifact: id, Body: "# A\n\nB.", Reason: "why"}
		if held {
			pending.Hold = &artifact.SendingHold{UntilClosed: "yoyodyne-fix", Reason: "waits for the fix"}
		}
		return pending
	}
	conversation := testConversation(t)
	conversation.PendingWrites = []PendingWrite{write("a", true), write("b", true), write("c", false), write("d", false)}
	if err := conversation.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want two held and two waiting accepted", err)
	}
	conversation.PendingWrites = append(conversation.PendingWrites, write("e", false))
	if err := conversation.Validate(); err == nil || !strings.Contains(err.Error(), "3 undecided documents") {
		t.Fatalf("Validate() error = %v, want the third waiting document refused", err)
	}
	conversation.PendingWrites = []PendingWrite{write("a", true), write("b", true), write("c", true), write("d", true), write("e", true)}
	if err := conversation.Validate(); err == nil || !strings.Contains(err.Error(), "5 documents held for sending") {
		t.Fatalf("Validate() error = %v, want the fifth held document refused", err)
	}
}
