package epicprogress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// lockWait bounds the wait for the lock, which is held for two small writes.
const lockWait = 5 * time.Second

// ErrResultUnknown is a paid call that was reserved and whose result cannot be
// known. Status says so and the development manager decides.
var ErrResultUnknown = errors.New("the result of a reserved call cannot be known")

// Store writes the two files. No operator's command opens one, and no role's
// block reaches one (onlywriter_test.go).
type Store struct {
	stateRoot string
	product   domain.ProductID
	now       func() time.Time
}

// Open opens the two files for the harness's writes.
func Open(stateRoot string, product domain.ProductID) (*Store, error) {
	if err := place(stateRoot, product); err != nil {
		return nil, err
	}
	return &Store{stateRoot: filepath.Clean(stateRoot), product: product, now: time.Now}, nil
}

// Begin writes a naming's empty entry and lists it, before the harness first
// acts on it. One that was begun is left as it is.
func (s *Store) Begin(naming Naming) error {
	return s.change(naming, func(*Entry) error { return errUnchanged })
}

// Join records that a piece joined an open epic. The operator's record is held
// still meanwhile, so an act of the operator cannot take the item between the
// check that it is free and the write.
func (s *Store) Join(epic string, piece Joined) error {
	return epicscope.UnderLock(s.stateRoot, s.product, func(record epicscope.Record) error {
		named, found := record.Named(epic)
		if !found || !named.Open() {
			return fmt.Errorf("epic %s is not open", epic)
		}
		if named.TakenOut(piece.WorkItemID) {
			return fmt.Errorf("the operator took work item %s out of epic %s, so it cannot join it again", piece.WorkItemID, epic)
		}
		view, err := Read(s.stateRoot, s.product, record)
		if err != nil {
			return err
		}
		if entry, _ := view.Entry(Of(named)); entry.Full() != "" {
			return fmt.Errorf("work item %s cannot join epic %s: %s", piece.WorkItemID, epic, entry.Full())
		}
		if holders := view.Holders(piece.WorkItemID); len(holders) > 0 {
			return fmt.Errorf("work item %s is already in epic %s, which has not ended", piece.WorkItemID, holders[0].WorkItemID)
		}
		return s.change(Of(named), func(entry *Entry) error {
			entry.Joined = append(entry.Joined, piece)
			return nil
		})
	})
}

// Ask records a piece that may join, made from a member of an open epic. One that
// is already recorded, or in an open epic, is left as it is.
func (s *Store) Ask(naming Naming, piece Piece) error {
	return epicscope.UnderLock(s.stateRoot, s.product, func(record epicscope.Record) error {
		view, epic, err := s.open(record, naming)
		if err != nil {
			return err
		}
		entry, _ := view.Entry(naming)
		if check, correcting := entry.Correcting(piece.WorkItemID); correcting {
			piece.From = FromCheck(check.ID)
		} else if !slices.Contains(view.Items(epic), piece.From) {
			return fmt.Errorf("work item %s is not in epic %s, so a piece of it cannot join", piece.From, epic.WorkItemID)
		}
		if len(view.Holders(piece.WorkItemID)) > 0 {
			return nil
		}
		piece.At, piece.Outcome = s.now().UTC(), nil
		return s.change(naming, func(entry *Entry) error {
			if entry.piece(piece.WorkItemID) >= 0 {
				return errUnchanged
			}
			entry.Pieces = append(entry.Pieces, piece)
			return nil
		})
	})
}

// Judged records what became of a piece that was asked about, and lets it join in
// the same write where the judgment says it may and nothing has happened since
// that keeps it out. The operator's record is held still meanwhile, as in Join.
func (s *Store) Judged(naming Naming, item string, outcome Outcome) error {
	return epicscope.UnderLock(s.stateRoot, s.product, func(record epicscope.Record) error {
		view, epic, err := s.open(record, naming)
		if err != nil {
			return err
		}
		return s.change(naming, func(entry *Entry) error {
			at := entry.piece(item)
			if at < 0 {
				return fmt.Errorf("work item %s was not asked about", item)
			}
			piece := &entry.Pieces[at]
			if piece.Outcome != nil {
				return errUnchanged
			}
			outcome.At = s.now().UTC()
			joins := outcome.Answer == AnswerInside
			switch holders := view.Holders(item); {
			case !joins:
			case len(holders) > 0:
				joins = false
				outcome.Why = "it was already in epic " + holders[0].WorkItemID
			case epic.TakenOut(item):
				joins = false
				outcome.Answer, outcome.Why = AnswerTakenOut, "the operator took it out, so it cannot join"
			case !slices.Contains(view.Items(epic), piece.From) && !entry.correctsFrom(item, piece.From):
				joins = false
				outcome.Answer, outcome.Why = AnswerOutside, piece.From+", which it was made from, is no longer in the epic"
			case entry.Full() != "":
				joins = false
				outcome.Answer, outcome.Why = AnswerLimit, entry.Full()
			}
			piece.Outcome = &outcome
			if joins {
				entry.Joined = append(entry.Joined, Joined{WorkItemID: item, From: piece.From, Judgment: piece.Judgment + ": " + outcome.Why, Session: outcome.Session, At: outcome.At})
			}
			return nil
		})
	})
}

// open is a reading of one naming that has not ended.
func (s *Store) open(record epicscope.Record, naming Naming) (View, epicscope.Epic, error) {
	epic, found := record.Named(naming.WorkItemID)
	if !found || !epic.Open() || !Of(epic).same(naming) {
		return View{}, epic, fmt.Errorf("that naming of epic %s is not open", naming.WorkItemID)
	}
	view, err := Read(s.stateRoot, s.product, record)
	return view, epic, err
}

// Alone holds the making of judgments against every other process, so that two do
// not make the same call at once. Nothing is held where another process has it.
func (s *Store) Alone() (release func() error, held bool, err error) {
	root, err := runstate.PinStateRoot(s.stateRoot)
	if err != nil {
		return nil, false, fmt.Errorf("pin the state root: %w", err)
	}
	defer root.Close()
	product, err := root.OpenDirectory(filepath.FromSlash(home.ProductDirectoryWithin(root.Path(), string(s.product))))
	if err != nil {
		return nil, false, fmt.Errorf("open the product's state directory: %w", err)
	}
	defer product.Close()
	lock, err := product.OpenLock(judgingLockName, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open the lock on joining judgments: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	release, err = runstate.LockFile(ctx, lock)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, false, nil
	}
	return release, err == nil, err
}

// Point reserves something whose reservation another durable record holds, such
// as a run's, before that record is made. The entry points to it, and no result
// is kept here.
func (s *Store) Point(naming Naming, id, purpose, heldBy string) error {
	if heldBy == "" {
		return errors.New("the record that holds the reservation is required")
	}
	_, _, err := s.reserve(naming, Reservation{ID: id, For: purpose, HeldBy: heldBy})
	return err
}

// ReserveRun reserves the start of a run in every epic the operator named that
// it is for, before the run's record is made: the one that holds its item, or,
// for a document, each that holds or is an item it answers. The run's record is
// what holds the reservation, so the entry points to it. While epic mode is off
// nothing is written. A run that is for no open epic is refused: one the decision
// allowed may have been ended since. withdraw takes back what this reserved,
// where the run's record was refused and so never made.
func (s *Store) ReserveRun(runID string, items []string) (withdraw func() error, err error) {
	record, err := epicscope.Read(s.stateRoot, s.product)
	if err != nil {
		return nil, err
	}
	if !record.On() {
		return func() error { return nil }, nil
	}
	view, err := Read(s.stateRoot, s.product, record)
	if err != nil {
		return nil, err
	}
	var reserved []Naming
	withdraw = func() error {
		var problems []error
		for _, naming := range reserved {
			problems = append(problems, s.change(naming, func(entry *Entry) error {
				entry.Reservations = slices.DeleteFunc(entry.Reservations, func(r Reservation) bool { return r.ID == runReservation(runID) })
				return nil
			}))
		}
		return errors.Join(problems...)
	}
	found := false
	problem := errors.New("it is in no open epic the operator named")
	for _, epic := range record.OpenEpics() {
		if !slices.ContainsFunc(append(view.Items(epic), epic.WorkItemID), func(item string) bool { return slices.Contains(items, item) }) {
			continue
		}
		// A document may answer a second epic whose progress can be read.
		if why := view.Unreadable(Of(epic)); why != "" {
			problem = fmt.Errorf("the progress of epic %s cannot be read: %s", epic.WorkItemID, why)
			continue
		}
		_, made, err := s.reserve(Of(epic), Reservation{ID: runReservation(runID), For: "run " + runID, HeldBy: "the record of run " + runID})
		if err != nil {
			return nil, errors.Join(err, withdraw())
		}
		found = true
		if made {
			reserved = append(reserved, Of(epic))
		}
	}
	if !found {
		return nil, problem
	}
	return withdraw, nil
}

func runReservation(runID string) string { return "run:" + runID }

// Looked is what looking up a reserved call found.
type Looked int

const (
	// CannotTell is the zero value, so a look-up that says nothing sends nothing.
	CannotTell Looked = iota
	NotSent
	Found
)

// Call makes one paid call for an epic, once. It is reserved before send. A
// reservation already there is never sent again without looking it up first: a
// result kept, or found by lookUp, is the answer; one that was never sent is
// sent; and one that cannot be told is kept as unknown, with what lookUp says of
// it, and is ErrResultUnknown from then on. A send that fails leaves the reservation to be looked up. The
// caller makes sure no other process makes the same call at once.
func (s *Store) Call(naming Naming, id, purpose string, lookUp func() (string, Looked, error), send func() (string, error)) (string, error) {
	return s.call(naming, id, purpose, s.reserve, lookUp, send)
}

// CallWhileOpen is Call for a naming that has not ended. The reservation is made
// while the operator's record is held still, and is refused for a naming that has
// ended, was never made, or whose progress cannot be read, so nothing is paid for
// one whatever the one decision answered. Epic mode being off does not refuse it.
func (s *Store) CallWhileOpen(naming Naming, id, purpose string, lookUp func() (string, Looked, error), send func() (string, error)) (string, error) {
	reserve := func(naming Naming, reservation Reservation) (standing Reservation, made bool, err error) {
		err = epicscope.UnderLock(s.stateRoot, s.product, func(record epicscope.Record) error {
			view, _, err := s.open(record, naming)
			if err != nil {
				return err
			}
			if why := view.Unreadable(naming); why != "" {
				return fmt.Errorf("the progress of epic %s cannot be read: %s", naming.WorkItemID, why)
			}
			standing, made, err = s.reserve(naming, reservation)
			return err
		})
		return standing, made, err
	}
	return s.call(naming, id, purpose, reserve, lookUp, send)
}

func (s *Store) call(naming Naming, id, purpose string, reserve func(Naming, Reservation) (Reservation, bool, error), lookUp func() (string, Looked, error), send func() (string, error)) (string, error) {
	reserved, made, err := reserve(naming, Reservation{ID: id, For: purpose})
	if err != nil {
		return "", err
	}
	if reserved.HeldBy != "" {
		return "", fmt.Errorf("%s is already reserved in %s", id, reserved.HeldBy)
	}
	unknown := fmt.Errorf("%w: %s", ErrResultUnknown, purpose)
	if kept := reserved.Result; kept != nil && kept.Unknown {
		return "", unknown
	} else if kept != nil {
		return kept.Says, nil
	}
	if !made {
		says, looked, err := lookUp()
		if err != nil {
			return "", err
		}
		if looked == Found {
			return says, s.settle(naming, id, Result{Says: says})
		}
		if looked != NotSent {
			if says == "" {
				says = "nothing shows whether it was sent"
			}
			return "", errors.Join(unknown, s.settle(naming, id, Result{Says: says, Unknown: true}))
		}
	}
	says, err := send()
	if err != nil {
		return "", err
	}
	return says, s.settle(naming, id, Result{Says: says})
}

// Checked keeps a check of the whole epic, made under the reservation of the same
// identifier, with what it found worked out from its answers. Where every answer is
// met or taken out, the epic's delivered entry is written in the same write, with
// when and on what evidence; any other answer writes none. A check already kept is
// left as it is. It is refused for a naming that has ended, for a check with no
// answer or one missing what its answer needs, and for an answer of taken out that
// names no item the operator's record holds as taken out.
func (s *Store) Checked(naming Naming, check Check) (Check, error) {
	err := epicscope.UnderLock(s.stateRoot, s.product, func(record epicscope.Record) error {
		_, epic, err := s.open(record, naming)
		if err != nil {
			return err
		}
		if strings.TrimSpace(check.Commit) == "" || len(check.Findings) == 0 {
			return errors.New("a check of the whole epic needs the commit it read and an answer for each thing the description asks")
		}
		for _, finding := range check.Findings {
			if !finding.valid() {
				return fmt.Errorf("the answer to %q is not met, taken out, not met or cannot be told with what that answer needs", finding.Asks)
			}
			if finding.Answer == TakenOut && !epic.TakenOut(finding.Removal) {
				return fmt.Errorf("the answer to %q is that the operator took it out, and the operator's record holds no removal of %s from epic %s", finding.Asks, finding.Removal, naming.WorkItemID)
			}
		}
		check.Found, check.At, check.Owed = FoundOf(check.Findings), s.now().UTC(), nil
		return s.change(naming, func(entry *Entry) error {
			if at := slices.IndexFunc(entry.Checks, func(kept Check) bool { return kept.ID == check.ID }); at >= 0 {
				check = entry.Checks[at]
				return errUnchanged
			}
			if reserved, found := entry.Reservation(check.ID); !found || reserved.Result == nil || reserved.Result.Unknown {
				return fmt.Errorf("the check %s was not reserved and answered", check.ID)
			}
			// Something missing owes one correction, for the first rounds only. After
			// them the Lead Product Manager decides, and nothing is owed.
			if check.Found == FoundNotMet && entry.Rounds(epic) < MaxRounds {
				check.Owed = &Correction{Missing: missing(check.Findings), At: check.At}
			}
			entry.Checks = append(entry.Checks, check)
			if check.Passed() {
				entry.Deliveries = append(entry.Deliveries, Delivery{At: check.At, Evidence: evidence(check)})
			}
			return nil
		})
	})
	return check, err
}

// missing is what a check found missing, each thing asked with what is missing.
func missing(findings []Finding) string {
	var said []string
	for _, finding := range findings {
		if finding.Answer == NotMet {
			said = append(said, finding.Asks+": "+finding.Says)
		}
	}
	return strings.Join(said, "; ")
}

// OweEvidence writes that one correction is owed for a check that could not tell,
// naming the evidence the Lead Product Manager's recorded answer says it lacked.
// It is refused for a naming that has ended, a check that did not answer cannot
// be told, and one that already owes a correction.
func (s *Store) OweEvidence(naming Naming, checkID, evidence string) error {
	if strings.TrimSpace(evidence) == "" {
		return errors.New("the evidence the check lacked is required")
	}
	return epicscope.UnderLock(s.stateRoot, s.product, func(record epicscope.Record) error {
		if _, _, err := s.open(record, naming); err != nil {
			return err
		}
		return s.change(naming, func(entry *Entry) error {
			check, err := entry.check(checkID)
			switch {
			case err != nil:
				return err
			case check.Found != FoundCannotTell:
				return fmt.Errorf("the check %s found %s, and evidence is owed only for one that could not tell", checkID, check.Found)
			case check.Owed != nil:
				return fmt.Errorf("the check %s already owes a correction, for %s", checkID, check.Owed.Missing)
			}
			check.Owed = &Correction{Missing: strings.TrimSpace(evidence), At: s.now().UTC()}
			return nil
		})
	})
}

// check is the check of the entry with the identifier, to change in place.
func (e *Entry) check(id string) (*Check, error) {
	at := slices.IndexFunc(e.Checks, func(check Check) bool { return check.ID == id })
	if at < 0 {
		return nil, fmt.Errorf("epic %s has no check %s", e.WorkItemID, id)
	}
	return &e.Checks[at], nil
}

// owedFor is why a piece may not be written for the check of an open epic, and nil
// where a correction is owed for it that names no piece, or names this one.
func owedFor(view View, epic epicscope.Epic, checkID, piece string) error {
	entry, _ := view.Entry(Of(epic))
	at := slices.IndexFunc(entry.Checks, func(check Check) bool { return check.ID == checkID })
	switch {
	case at < 0:
		return fmt.Errorf("epic %s has no check %s", epic.WorkItemID, checkID)
	case entry.Checks[at].Owed == nil:
		return fmt.Errorf("no correction is owed for the check %s of epic %s", checkID, epic.WorkItemID)
	case entry.Checks[at].Owed.Piece != "" && entry.Checks[at].Owed.Piece != piece:
		return fmt.Errorf("the correction owed for the check %s of epic %s has already been written, as %s", checkID, epic.WorkItemID, entry.Checks[at].Owed.Piece)
	}
	return nil
}

// openNamed is the open epic the operator named, and a reading of its progress.
func (s *Store) openNamed(record epicscope.Record, epic string) (View, epicscope.Epic, error) {
	named, found := record.Named(epic)
	if !found || !named.Open() {
		return View{}, named, fmt.Errorf("%s is not an open epic the operator named", epic)
	}
	view, err := Read(s.stateRoot, s.product, record)
	return view, named, err
}

// MayCorrect is why a piece correcting a check of the epic may not be written, and
// nil where it may: the epic is open and a correction is owed for the check that
// no piece has been written for.
func (s *Store) MayCorrect(epic, checkID string) error {
	record, err := epicscope.Read(s.stateRoot, s.product)
	if err != nil {
		return err
	}
	view, named, err := s.openNamed(record, epic)
	if err != nil {
		return err
	}
	return owedFor(view, named, checkID, "")
}

// Correcting writes the piece written for a check's owed correction into that
// correction, and answers the note of fixed form to write on the piece after it.
// Writing the same piece again changes nothing.
func (s *Store) Correcting(epic, checkID, piece string) (note string, err error) {
	err = epicscope.UnderLock(s.stateRoot, s.product, func(record epicscope.Record) error {
		view, named, err := s.openNamed(record, epic)
		if err != nil {
			return err
		}
		if err := owedFor(view, named, checkID, piece); err != nil {
			return err
		}
		if holders := view.Holders(piece); len(holders) > 0 {
			return fmt.Errorf("work item %s is already in epic %s", piece, holders[0].WorkItemID)
		}
		return s.change(Of(named), func(entry *Entry) error {
			check, err := entry.check(checkID)
			if err != nil {
				return err
			}
			note = CorrectionNote(epic, *check)
			if check.Owed.Piece == piece {
				return errUnchanged
			}
			check.Owed.Piece = piece
			return nil
		})
	})
	return note, err
}

// Noted writes that the note of fixed form is on the piece a correction of the
// open epic's check names.
func (s *Store) Noted(epic, checkID string) error {
	record, err := epicscope.Read(s.stateRoot, s.product)
	if err != nil {
		return err
	}
	named, found := record.Named(epic)
	if !found || !named.Open() {
		return fmt.Errorf("%s is not an open epic the operator named", epic)
	}
	return s.change(Of(named), func(entry *Entry) error {
		check, err := entry.check(checkID)
		if err != nil {
			return err
		}
		if check.Owed == nil || check.Owed.Piece == "" || check.Owed.Noted {
			return errUnchanged
		}
		check.Owed.Noted = true
		return nil
	})
}

// evidence is what a delivery rests on, in words: the check, the commit it read,
// and how many answers were met and which items were taken out.
func evidence(check Check) string {
	met, out := 0, []string{}
	for _, finding := range check.Findings {
		if finding.Answer == Met {
			met++
		} else {
			out = append(out, finding.Removal)
		}
	}
	says := fmt.Sprintf("the check of the whole epic %s read commit %s and found %d of %d asked met", check.ID, check.Commit, met, len(check.Findings))
	if len(out) > 0 {
		says += ", and the rest taken out by the operator with " + strings.Join(slices.Compact(slices.Sorted(slices.Values(out))), ", ")
	}
	return says
}

// reserve writes a reservation and answers the one that stands, and whether this
// made it.
func (s *Store) reserve(naming Naming, reservation Reservation) (standing Reservation, made bool, err error) {
	reservation.At = s.now().UTC()
	err = s.change(naming, func(entry *Entry) error {
		if at := slices.IndexFunc(entry.Reservations, func(r Reservation) bool { return r.ID == reservation.ID }); at >= 0 {
			standing = entry.Reservations[at]
			return errUnchanged
		}
		entry.Reservations = append(entry.Reservations, reservation)
		standing, made = reservation, true
		return nil
	})
	return standing, made, err
}

func (s *Store) settle(naming Naming, id string, result Result) error {
	result.At = s.now().UTC()
	return s.change(naming, func(entry *Entry) error {
		at := slices.IndexFunc(entry.Reservations, func(r Reservation) bool { return r.ID == id })
		if at < 0 {
			return fmt.Errorf("nothing was reserved as %s", id)
		}
		entry.Reservations[at].Result = &result
		return nil
	})
}

// errUnchanged is a change that asks for what the entry already says.
var errUnchanged = errors.New("nothing to change")

// change begins the naming where it was not, and then applies one change, each
// write finished before the next.
func (s *Store) change(naming Naming, apply func(*Entry) error) error {
	root, err := runstate.PinStateRoot(s.stateRoot)
	if err != nil {
		return fmt.Errorf("pin the state root: %w", err)
	}
	defer root.Close()
	directory := filepath.FromSlash(home.ProductDirectoryWithin(root.Path(), string(s.product)))
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return fmt.Errorf("create the product's state directory: %w", err)
	}
	product, err := root.OpenDirectory(directory)
	if err != nil {
		return fmt.Errorf("open the product's state directory: %w", err)
	}
	defer product.Close()
	lock, err := product.OpenLock(lockName, 0o600)
	if err != nil {
		return fmt.Errorf("open the lock on the epics' progress: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	release, err := runstate.LockFile(ctx, lock)
	if err != nil {
		return fmt.Errorf("lock the epics' progress: %w", err)
	}
	defer release()

	view, err := Read(root.Path(), s.product, epicscope.Record{})
	if err != nil {
		return err
	}
	if why := view.Unreadable(naming); why != "" {
		return fmt.Errorf("the progress of epic %s cannot be read: %s", naming.WorkItemID, why)
	}
	progress, begun := view.progress, view.begun
	at := progress.index(naming)
	if at < 0 {
		at = len(progress.Entries)
		progress.Entries = append(progress.Entries, Entry{Naming: naming, BegunAt: s.now().UTC(), NamedBeforeKept: naming.NamedAt.Before(keptSince)})
		if err := write(product, progressName, progress, func(p Progress) error { return p.validate(s.product) }); err != nil {
			return err
		}
	}
	if !begun.has(naming) {
		begun.Namings = append(begun.Namings, naming)
		if err := write(product, begunName, begun, func(b Begun) error { return b.validate(s.product) }); err != nil {
			return err
		}
	}
	if err := apply(&progress.Entries[at]); errors.Is(err, errUnchanged) {
		return nil
	} else if err != nil {
		return err
	}
	return write(product, progressName, progress, func(p Progress) error { return p.validate(s.product) })
}

// write replaces a file whole, and writes nothing that a read would refuse.
func write[T any](product *repowrite.PinnedRoot, name string, content T, validate func(T) error) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(content); err != nil {
		return fmt.Errorf("encode the epics' progress: %w", err)
	}
	if encoded.Len() > maxFileBytes {
		return fmt.Errorf("refused the epics' progress: it is larger than %d bytes", maxFileBytes)
	}
	if err := validate(content); err != nil {
		return fmt.Errorf("refused the epics' progress: %w", err)
	}
	// Both sides of the change, as repository-writes-are-physically-confined asks.
	if err := product.Unchanged(); err != nil {
		return err
	}
	if err := product.WriteFile(name, encoded.Bytes(), 0o600, false); err != nil {
		return fmt.Errorf("write the epics' progress: %w", err)
	}
	return errors.Join(product.Unchanged(), product.Sync())
}
