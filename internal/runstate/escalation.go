package runstate

// What the harness has already put in front of the development manager.
//
// A docketed stoppage reaches her by being delivered into her conversation, and
// the delivery is a provider turn: it costs money, and a stoppage delivered
// twice is the same evidence in front of her twice, which is how one authorized
// recovery becomes two decisions. So the delivery is claimed before it is made,
// against the docket entry rather than against the item — one stoppage is one
// thing to judge however much budget the item has left — and a claim already
// delivered is refused to whoever asks second.
//
// The claim is what serializes two processes delivering at once, and it does it
// under the record's own lock: an attempt too recent to repeat is refused there
// rather than only skipped by whoever was about to make it, so two sessions
// polling the same docket produce one attempt and one refusal instead of two
// turns. The conversation lease is a second boundary behind it — the loser of
// that race cannot open her conversation while the winner holds it — but a
// guarantee that rested only on the lease would be one this record claims and
// something else keeps.
//
// It is not a decision and records none of its own. What the development manager
// decides is recorded where every triage decision is recorded, on the work item
// and against the item's durable triage budget; this carries her decision only
// as the answer that came back, so a reader of this record can tell a stoppage
// she has judged from one that reached her and left her saying nothing.
//
// An attempt that could not be delivered is kept rather than erased, and may be
// made again a bounded number of times. That is the direction this record fails
// in: a delivery nobody can prove happened is worth repeating, because the cost
// of repeating it is one turn and a paragraph she has read before, and the cost
// of not repeating it is a stoppage nobody ever hears about — which is the whole
// of what this exists to prevent.
//
// Like the re-runs beside it, it is one home per machine, for the same reason:
// what the coordination of two harnesses over one repository would take belongs
// to the team-mode epic rather than here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// EscalationSchemaVersion is 1 and has never changed.
const EscalationSchemaVersion = 1

// MaxEscalationAttempts bounds how many times one stoppage is put in front of
// the development manager before the harness stops trying.
//
// It is small on purpose. Every attempt past the first is a turn spent on
// evidence she may already have read, so a bound of three is what separates
// riding out a provider that briefly failed mid-turn from a loop that spends
// every poll saying the same thing to her again. A stoppage that exhausts it is
// still on the docket, and what it then needs is a person — which the read
// model says by holding the item for one, rather than leaving it to be inferred
// from the quiet.
//
// What it bounds is turns that may have been taken, and only those. A delivery
// that provably asked her nothing gives its attempt back through Withdraw, so a
// conversation nothing can open is retried at the pace below for as long as it
// stays unopenable rather than counting down to abandonment. That is deliberate
// and it is the safer direction: the reasons a conversation cannot be opened are
// a signed-out provider, a conversation somebody has open, a role nobody has
// configured — every one of them something that clears — and a stoppage
// abandoned while the harness was misconfigured is one nothing re-delivers when
// the configuration is fixed.
const MaxEscalationAttempts = 3

// EscalationRetryDelay is how long a failed delivery is left alone before it is
// made again.
//
// The bound above is worth nothing without it. Whatever drives the delivery
// decides how often it looks, and the loop that does today looks once per pull —
// which on a busy queue is several times a minute, so three attempts counted and
// not paced would be three attempts inside one command and a stoppage abandoned
// in the time the provider took to restart. Paced, the same three span
// three quarters of an hour, which is what "briefly unreachable" is worth
// riding out.
//
// It is measured from the last attempt rather than the first, so a delivery that
// failed, waited, and failed again waits again rather than being abandoned on a
// clock that started before anybody knew there was a problem.
const EscalationRetryDelay = 15 * time.Minute

// maxEscalationTextBytes bounds the prose one record carries: the decision's
// reasoning as it came back, and the problem that stopped a delivery. Both are
// written by something outside this package, and a record that could not be
// saved is a delivery nobody can prove happened.
const maxEscalationTextBytes = 4 << 10

// Escalation is the durable record of one docketed stoppage being put in front
// of the development manager. It is written before the turn is taken, so a
// process that dies between the two has recorded a delivery nobody made rather
// than made one nobody recorded — the same direction every triage record fails
// in, and the one the bounded retry above is here to soften.
type Escalation struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// DocketKey names the triage entry this delivered, which is what makes the
	// record one per stoppage rather than one per item or one per run.
	DocketKey  string `json:"docket_key"`
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	// DocketedAt is when the stoppage this record is about was docketed, which is
	// what makes the record one per stoppage rather than one per key. A key names
	// a run rather than one moment of it: a repair continues the run that stopped,
	// and a repaired run that dies again is docketed under the key its settled
	// entry carried, so a record that was only keyed would say the fresh stoppage
	// had already been put in front of her. A record written before this was kept
	// carries zero, and reads as being about whatever stoppage is asked after it —
	// the accounting in force when it was written rather than a record that is
	// wrong.
	DocketedAt time.Time `json:"docketed_at,omitempty"`
	// CarryOutRefusedAt identifies a permanent carry-out refusal delivered as a
	// new event on this stoppage, even if its original delivery predates the
	// DocketedAt field. The same refusal is delivered only once.
	CarryOutRefusedAt time.Time `json:"carry_out_refused_at,omitempty"`
	// Attempts counts the deliveries begun and not given back, including the one
	// in progress. It is what the bound above is read against, and it counts an
	// attempt whatever became of it: a delivery that may have reached her is spent
	// whether or not the harness could write down what came back. Zero is a
	// record whose only attempt was given back — nothing was asked of her, and
	// what the record is still here for is to pace the next one.
	Attempts         int       `json:"attempts"`
	FirstAttemptedAt time.Time `json:"first_attempted_at"`
	// LastAttemptedAt is when the most recent attempt was made, which is what the
	// retry delay is measured from. It is a field of its own rather than the
	// record's UpdatedAt because the two are stamped by different clocks: this one
	// by whatever is making the deliveries, and UpdatedAt by the store as it
	// writes. A pacing rule measured against the second would be comparing one
	// clock's reading with another's.
	LastAttemptedAt time.Time `json:"last_attempted_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	// DeliveredAt is when a turn was actually taken, and is absent while every
	// attempt so far failed before the development manager was asked anything.
	// It is what makes this record at-most-once rather than at-least-once: past
	// it, nothing delivers this stoppage again.
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	// ConversationID is her durable conversation the stoppage was delivered
	// into, so what she was shown can be read afterwards from the record of the
	// conversation rather than only from this.
	ConversationID string `json:"conversation_id,omitempty"`
	// Decision is what she recorded about this stoppage in answer, in the triage
	// vocabulary, and Reason the reasoning she gave. Both are empty on a delivery
	// she answered without deciding anything, which is a real answer and not a
	// failure: a stoppage she has looked at and left alone is not one nobody has
	// seen.
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Problem is what stopped the last attempt, and is cleared by a delivery that
	// worked. A record carrying one is a stoppage that may have reached nobody,
	// which is exactly the state somebody has to be able to find.
	Problem string `json:"problem,omitempty"`
}

// About reports this record being about the stoppage docketed at a moment. A
// record about an earlier stoppage under the same key is not: what it delivered
// was decided, and the work then stopped again, so the delivery it recorded says
// nothing about the stoppage now on the docket. Either side saying nothing about
// when — a record written before that was kept, or a caller not asking about a
// particular stoppage — reads as the same stoppage, which is the reading every
// record had before this distinction existed.
func (e Escalation) About(docketedAt time.Time) bool {
	if e.DocketedAt.IsZero() || docketedAt.IsZero() {
		return true
	}
	return !e.DocketedAt.Before(docketedAt)
}

// AboutEvent distinguishes a permanent carry-out refusal from the stoppage
// that was originally delivered, including records whose date was not kept.
func (e Escalation) AboutEvent(docketedAt, refusedAt time.Time) bool {
	if !refusedAt.IsZero() {
		return e.CarryOutRefusedAt.Equal(refusedAt)
	}
	return e.About(docketedAt)
}

// Delivered reports the development manager having been asked. It is the
// question every other reader asks of this record: a stoppage delivered is hers
// to answer, and one that is not is still the harness's to deliver.
func (e Escalation) Delivered() bool { return e.DeliveredAt != nil }

// Spent reports a stoppage the harness will not try to deliver again. Both
// halves end the trying, and they end it for opposite reasons: a delivered
// stoppage needs nothing more, and one whose attempts are gone needs a person.
func (e Escalation) Spent() bool { return e.Delivered() || e.Attempts >= MaxEscalationAttempts }

// Cooling reports an attempt too recent to be worth repeating yet. It is what
// makes the bound above a span of time rather than a burst: whatever drives the
// delivery decides how often it asks, and this decides how often asking does
// anything.
func (e Escalation) Cooling(now time.Time) bool {
	return now.Sub(e.LastAttemptedAt) < EscalationRetryDelay
}

// Validate reports every contract violation in the record at once.
func (e Escalation) Validate() error {
	var problems []error
	if e.SchemaVersion != EscalationSchemaVersion {
		problems = append(problems, fmt.Errorf("escalation schema version %d is not supported", e.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(e.DocketKey) == "" {
		problems = append(problems, errors.New("docket key is required"))
	}
	if strings.TrimSpace(e.RunID) == "" {
		problems = append(problems, errors.New("run id is required"))
	}
	if strings.TrimSpace(e.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	if e.Attempts < 0 {
		problems = append(problems, fmt.Errorf("attempts is %d, and a delivery cannot be begun a negative number of times", e.Attempts))
	}
	if e.FirstAttemptedAt.IsZero() {
		problems = append(problems, errors.New("first attempted at is required"))
	}
	// Required for the reason the retry delay is measured from it: a record that
	// could not say when it was last tried is one nothing can pace, and the
	// pacing is what keeps a bound counted in attempts from being spent in a
	// burst.
	if e.LastAttemptedAt.IsZero() {
		problems = append(problems, errors.New("last attempted at is required"))
	}
	if e.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated at is required"))
	}
	// A decision with nothing delivered would say the development manager
	// answered a question nobody asked her, which is the one thing this record
	// must never be able to say.
	if !e.Delivered() && strings.TrimSpace(e.Decision) != "" {
		problems = append(problems, fmt.Errorf("decision %q is recorded on a stoppage that was never delivered", e.Decision))
	}
	if len(e.Reason) > maxEscalationTextBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(e.Reason), maxEscalationTextBytes))
	}
	if len(e.Problem) > maxEscalationTextBytes {
		problems = append(problems, fmt.Errorf("problem is %d bytes, limit is %d", len(e.Problem), maxEscalationTextBytes))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid triage escalation: %w", err)
	}
	return nil
}

// Render describes one escalation for whoever is reading it: what was put in
// front of the development manager, and what came back.
func (e Escalation) Render() string {
	if !e.Delivered() {
		return fmt.Sprintf("%s: the stoppage of run %s has not reached the development manager after %d attempt(s): %s\n",
			e.WorkItemID, e.RunID, e.Attempts, nonEmptyProblem(e.Problem))
	}
	if decision := strings.TrimSpace(e.Decision); decision != "" {
		return fmt.Sprintf("%s: the stoppage of run %s reached the development manager at %s, who triaged it as %q\n",
			e.WorkItemID, e.RunID, e.DeliveredAt.UTC().Format(time.RFC3339), decision)
	}
	return fmt.Sprintf("%s: the stoppage of run %s reached the development manager at %s, who recorded no decision about it\n",
		e.WorkItemID, e.RunID, e.DeliveredAt.UTC().Format(time.RFC3339))
}

func nonEmptyProblem(problem string) string {
	if trimmed := strings.TrimSpace(problem); trimmed != "" {
		return trimmed
	}
	return "nothing was recorded about what stopped it"
}

// Delivery is what one attempt came to. An attempt whose At is zero never
// reached the development manager, which is what keeps the record's at-most-once
// guarantee about turns actually taken rather than about intentions.
type Delivery struct {
	At             time.Time
	ConversationID string
	Decision       string
	Reason         string
	Problem        string
}

// ErrEscalationSpent is what a second delivery of one docketed stoppage unwraps
// to, so a caller can tell "this stoppage has already been put to her" from a
// store that could not be read without matching on the words of either.
var ErrEscalationSpent = errors.New("this stoppage will not be put to the development manager again")

// EscalationSpentError names the escalation that already exists. It carries the
// record rather than only refusing, because what the asker needs is what became
// of the first delivery: a stoppage she answered is a different situation from
// one whose attempts were spent on a conversation nothing could open.
type EscalationSpentError struct {
	Existing Escalation
}

func (e EscalationSpentError) Error() string {
	if e.Existing.Delivered() {
		return fmt.Sprintf("the stoppage of run %s was put to the development manager at %s, and the harness delivers a docketed stoppage once",
			e.Existing.RunID, e.Existing.DeliveredAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("the stoppage of run %s could not be put to the development manager in %d attempt(s), so the harness stopped trying and it needs a person: %s",
		e.Existing.RunID, e.Existing.Attempts, nonEmptyProblem(e.Existing.Problem))
}

func (e EscalationSpentError) Unwrap() error { return ErrEscalationSpent }

// ErrEscalationCooling is what an attempt made too soon after the last one
// unwraps to. It is not a refusal of the delivery: the stoppage keeps every
// attempt it is owed, and what the caller is being told is that this is not the
// moment to make one.
var ErrEscalationCooling = errors.New("this stoppage was put to the development manager too recently to try again")

// EscalationCoolingError names the attempt that is still cooling and when the
// next one is due, so a caller that meets it can say which it was rather than
// only that something refused.
type EscalationCoolingError struct {
	Existing Escalation
	At       time.Time
}

func (e EscalationCoolingError) Error() string {
	return fmt.Sprintf("the stoppage of run %s was put to the development manager at %s, and the next attempt is not due until %s",
		e.Existing.RunID,
		e.Existing.LastAttemptedAt.UTC().Format(time.RFC3339),
		e.Existing.LastAttemptedAt.Add(EscalationRetryDelay).UTC().Format(time.RFC3339))
}

func (e EscalationCoolingError) Unwrap() error { return ErrEscalationCooling }

// EscalationStore is where the escalations live: one directory under the
// product, beside the re-runs and the counters, and one file per docketed
// stoppage inside it. A file each rather than one shared record is what keeps
// two stoppages out of each other's way.
type EscalationStore struct {
	root      string
	productID domain.ProductID
}

func NewEscalationStore(root string, productID domain.ProductID) (*EscalationStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &EscalationStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "escalations"),
		productID: productID,
	}, nil
}

// Escalations is the escalation record for this run store's product, reached
// from here for the reason the re-runs are: whoever can read what became of an
// item's runs can read what the harness has already done about one of them,
// without being told the state root a second time.
func (s *Store) Escalations() *EscalationStore {
	return &EscalationStore{
		root:      filepath.Join(filepath.Dir(s.root), "escalations"),
		productID: s.productID,
	}
}

// Escalated is every escalation this store's product holds, read through the
// accessor above. It exists so a reader that wants both halves of what the
// harness has stopped — the runs and what has been asked about them — takes one
// store rather than two, which is what keeps the two readings of one product
// from being wired to different roots.
func (s *Store) Escalated() ([]Escalation, error) {
	return s.Escalations().List()
}

func (s *EscalationStore) Root() string { return s.root }

// Attempt records that the harness is about to put this stoppage in front of
// the development manager, and refuses one it will not deliver again. It is
// written before the turn is taken, which is what makes the refusal mean
// anything: an attempt recorded afterwards would leave the window where two
// processes deliver the same stoppage at once wide open.
func (s *EscalationStore) Attempt(ctx context.Context, escalation Escalation) (Escalation, error) {
	key := strings.TrimSpace(escalation.DocketKey)
	if key == "" {
		return Escalation{}, errors.New("a docket entry is required to record its escalation")
	}
	at := escalation.FirstAttemptedAt
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	release, err := s.lock(ctx, key)
	if err != nil {
		return Escalation{}, err
	}
	defer release()

	attempted, found, err := s.load(key)
	if err != nil {
		return Escalation{}, err
	}
	// A record about an earlier stoppage under this key has spent nothing on this
	// one. The work stopped again after what it recorded was decided, so the
	// delivery starts over — attempts, pacing, and what came back — and the record
	// is about the stoppage that is actually on the docket from here on. One record
	// per key is kept, because a reader of the records asks what a key stands at.
	if found && !attempted.AboutEvent(escalation.DocketedAt, escalation.CarryOutRefusedAt) {
		found = false
	}
	if found {
		if attempted.Spent() {
			return Escalation{}, EscalationSpentError{Existing: attempted}
		}
		// Refused here rather than only where the delay is read, because here is
		// where the lock is. A caller that checked the pacing itself and then
		// claimed would be two reads and a write with a window between them, which
		// is exactly the window two concurrent sessions land in.
		if attempted.Cooling(at) {
			return Escalation{}, EscalationCoolingError{Existing: attempted, At: at}
		}
	} else {
		attempted = escalation
		attempted.Attempts = 0
		attempted.FirstAttemptedAt = at
	}
	attempted.SchemaVersion = EscalationSchemaVersion
	attempted.ProductID = s.productID
	attempted.DocketKey = key
	attempted.Attempts++
	// Stamped from the caller's clock, which is the same one the delay is read
	// against. The store's own UpdatedAt below is the wall clock and says when
	// this file was written, which is a different question.
	attempted.LastAttemptedAt = at
	attempted.UpdatedAt = at
	if err := attempted.Validate(); err != nil {
		return Escalation{}, err
	}
	if err := s.save(key, attempted); err != nil {
		return Escalation{}, err
	}
	return attempted, nil
}

// Settle records what one attempt came to: the turn that was taken, what the
// development manager decided, and what stopped a delivery that failed. It is a
// separate write from the attempt because the two are separated by the whole of
// a conversation turn — none of what it carries exists when the attempt is
// recorded.
func (s *EscalationStore) Settle(ctx context.Context, docketKey string, delivery Delivery) (Escalation, error) {
	key := strings.TrimSpace(docketKey)
	if key == "" {
		return Escalation{}, errors.New("a docket entry is required to settle its escalation")
	}

	release, err := s.lock(ctx, key)
	if err != nil {
		return Escalation{}, err
	}
	defer release()

	settled, found, err := s.load(key)
	if err != nil {
		return Escalation{}, err
	}
	if !found {
		return Escalation{}, fmt.Errorf("no escalation of the stoppage keyed %s is recorded, so there is nothing to settle", key)
	}
	if !delivery.At.IsZero() {
		delivered := delivery.At.UTC()
		settled.DeliveredAt = &delivered
	}
	if conversation := strings.TrimSpace(delivery.ConversationID); conversation != "" {
		settled.ConversationID = conversation
	}
	settled.Decision = strings.TrimSpace(delivery.Decision)
	settled.Reason = strings.TrimSpace(delivery.Reason)
	settled.Problem = strings.TrimSpace(delivery.Problem)
	settled.UpdatedAt = time.Now().UTC()
	if err := s.save(key, settled); err != nil {
		return Escalation{}, err
	}
	return settled, nil
}

// Withdraw gives back the attempt on one docketed stoppage, so the stoppage
// keeps every delivery it is owed rather than having spent one on nothing.
//
// It is narrow on purpose, exactly as the re-run's is: an attempt is spent by
// being recorded, and giving one back is sound only for a turn that produced no
// answer of hers. A record carrying a delivery is therefore refused rather than
// given back: something was said to her on it, and a second delivery of it is
// the thing this record exists to prevent.
//
// Where that boundary falls is stated here rather than left to each caller,
// because the callers cite this contract and must not each draw it somewhere
// else.
//
// A conversation that could not be opened is the plain case: the turn never
// happened, so it asked her nothing. A turn a cancellation killed is the other,
// and it is the narrower claim. Its give-back does not rest on her never having
// seen the message — the provider had the prompt from the moment the invocation
// started, and a transcript it had already written is hers to read on the next
// turn. It rests on what the turn produced: a cancelled turn that ended before a
// reply existed had no reply parsed, applied no action of hers, and moved no
// durable triage record, so a second delivery of it cannot become a second
// decision. A cancellation that landed after her answer arrived is not this and
// keeps its attempt: she answered, and this cannot claim she did not.
//
// What it gives back is the attempt and not the pacing. The record stays, with
// its moment on it, so the next delivery waits out the same delay a failed one
// does — because a provider with no capacity and a conversation somebody else is
// holding both last minutes or hours. A give-back that erased the record would
// put the next pull straight back into the same refusal, which on a busy queue is
// several a minute for as long as the limit lasts.
//
// A stoppage nothing has been recorded about is already what this would leave
// behind, so withdrawing one is not an error.
//
// It honours the context it is given, and a caller giving an attempt back after
// a cancellation has to know it: the read-modify-write below is serialized by a
// lock whose wait returns the context's error, so a give-back handed an
// already-cancelled context is refused the moment another process holds this
// record. The refusal is not certain, which is worse than if it were — an
// uncontended lock is taken without the context being consulted at all, so the
// same give-back succeeds or fails depending on what else wanted the record just
// then. A caller giving back a turn a cancellation killed must therefore hand
// this a context that survives that cancellation.
func (s *EscalationStore) Withdraw(ctx context.Context, docketKey, problem string) error {
	key := strings.TrimSpace(docketKey)
	if key == "" {
		return errors.New("a docket entry is required to withdraw its escalation")
	}

	release, err := s.lock(ctx, key)
	if err != nil {
		return err
	}
	defer release()

	attempted, found, err := s.load(key)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if attempted.Delivered() {
		return fmt.Errorf("the stoppage keyed %s was delivered to the development manager at %s, so its attempt was acted on and is not one to give back",
			key, attempted.DeliveredAt.UTC().Format(time.RFC3339))
	}
	if attempted.Attempts > 0 {
		attempted.Attempts--
	}
	attempted.Problem = strings.TrimSpace(problem)
	attempted.UpdatedAt = time.Now().UTC()
	return s.save(key, attempted)
}

// Find reports the escalation of one docketed stoppage. A stoppage nothing has
// been recorded about is the ordinary answer rather than a failure to look, and
// a record that cannot be read is neither: it is an error, because a delivery
// nobody can read must never be delivered through as though it were absent.
func (s *EscalationStore) Find(docketKey string) (Escalation, bool, error) {
	key := strings.TrimSpace(docketKey)
	if key == "" {
		return Escalation{}, false, errors.New("a docket entry is required to read its escalation")
	}
	return s.load(key)
}

// List reports every escalation recorded for this product, oldest attempt
// first. A product nothing has been escalated in lists nothing, which is not a
// failure to look; a record that cannot be read is one, for the reason Find
// gives.
func (s *EscalationStore) List() ([]Escalation, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the triage escalations: %w", err)
	}
	var recorded []Escalation
	for _, entry := range entries {
		// The directory also holds each record's lock file and the temporary files
		// a replacement is written through. Only the records are read: a lock is
		// named for the record it guards and would otherwise be decoded as one.
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		escalation, err := s.read(filepath.Join(s.root, name))
		if err != nil {
			return nil, err
		}
		recorded = append(recorded, escalation)
	}
	sort.Slice(recorded, func(first, second int) bool {
		return recorded[first].FirstAttemptedAt.Before(recorded[second].FirstAttemptedAt)
	})
	return recorded, nil
}

func (s *EscalationStore) load(docketKey string) (Escalation, bool, error) {
	recorded, err := s.read(s.path(docketKey))
	if errors.Is(err, os.ErrNotExist) {
		return Escalation{}, false, nil
	}
	if err != nil {
		return Escalation{}, false, err
	}
	// The file is named for a digest of the docket key rather than for the key
	// itself, so this is what catches two keys that were ever to land on one
	// name: the record says which stoppage it is about, and one that is not this
	// stoppage's is refused rather than treated as its delivery.
	if recorded.DocketKey != docketKey {
		return Escalation{}, false, fmt.Errorf("triage escalation at %s belongs to docket entry %q, not %q", s.path(docketKey), recorded.DocketKey, docketKey)
	}
	return recorded, true, nil
}

// read decodes one record. A file that is not there is reported as itself, so a
// caller asking about one stoppage can tell an absent escalation from an
// unreadable one; everything else is an error, because a delivery nobody can
// read must never be read as absent.
func (s *EscalationStore) read(path string) (Escalation, error) {
	file, err := os.Open(path)
	if err != nil {
		return Escalation{}, fmt.Errorf("open triage escalation: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var recorded Escalation
	if err := decoder.Decode(&recorded); err != nil {
		return Escalation{}, fmt.Errorf("decode triage escalation at %s: %w", path, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Escalation{}, fmt.Errorf("decode triage escalation at %s: %w", path, err)
	}
	if err := recorded.Validate(); err != nil {
		return Escalation{}, err
	}
	if recorded.ProductID != s.productID {
		return Escalation{}, fmt.Errorf("triage escalation belongs to product %q, not %q", recorded.ProductID, s.productID)
	}
	return recorded, nil
}

// save replaces one stoppage's escalation durably, as a temporary file and a
// rename, so a process that dies mid-write leaves the previous record rather
// than a truncated file nothing can read.
func (s *EscalationStore) save(docketKey string, escalation Escalation) error {
	escalation.SchemaVersion = EscalationSchemaVersion
	escalation.ProductID = s.productID
	escalation.DocketKey = docketKey
	if err := escalation.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create triage escalation directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".escalation-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary triage escalation: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary triage escalation: %w", err)
	}
	if err := writeJSONFile(temporary, "triage escalation", escalation); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary triage escalation: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path(docketKey)); err != nil {
		return fmt.Errorf("replace triage escalation: %w", err)
	}
	return syncDirectory(s.root)
}

// lock serializes the read-modify-write on one stoppage's record across every
// Yoyodyne process, and the lock file outlives the write for the reason the
// re-runs' does: removing it while another process held it would let a third
// take a lock on a file nobody else can see.
func (s *EscalationStore) lock(ctx context.Context, docketKey string) (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create triage escalation directory: %w", err)
	}
	file, err := os.OpenFile(s.path(docketKey)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open triage escalation lock: %w", err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the triage escalation of %s: %w", docketKey, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

// path names one stoppage's record, the same way the re-run beside it is named:
// a bounded readable rendering with a digest of the exact key after it, because
// a docket key is no more a file name than a tracker identifier is.
func (s *EscalationStore) path(docketKey string) string {
	return filepath.Join(s.root, triageKey(docketKey)+".json")
}
