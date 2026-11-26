// Package epicprogress keeps what the harness remembers about each epic the
// operator named: the pieces that joined it, the review comments acted on, the
// checks of the whole epic, its delivery, and each start and paid call reserved
// for it. The operator's own record (epicscope) is another file, which nothing
// here writes.
//
// epic-progress.json holds one entry for each naming. epic-progress-begun.json
// lists the namings whose entry was begun, which shows that an entry once existed.
// docs/designs/epic-mode-second-part.md says what each being missing means.
package epicprogress

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

const (
	// SchemaVersion is 1 for both files and has never changed.
	SchemaVersion = 1

	// maxFileBytes bounds each file, for a write and a read alike.
	maxFileBytes = 8 << 20

	progressName = "epic-progress.json"
	begunName    = "epic-progress-begun.json"
	lockName     = progressName + ".lock"

	judgingLockName = progressName + ".judging.lock"
)

// keptSince is about when the harness first kept progress. An epic named before
// it may have been worked on before anything was kept.
var keptSince = time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)

// Naming is one naming of an epic, as the operator's record holds it. An epic
// that ended and was named again is another naming.
type Naming struct {
	WorkItemID string    `json:"work_item_id"`
	NamedAt    time.Time `json:"named_at"`
}

// Of is the naming an entry of the operator's record is.
func Of(epic epicscope.Epic) Naming {
	return Naming{WorkItemID: epic.WorkItemID, NamedAt: epic.NamedAt.UTC()}
}

func (n Naming) same(other Naming) bool {
	return n.WorkItemID == other.WorkItemID && n.NamedAt.Equal(other.NamedAt)
}

func (n Naming) valid() bool { return beads.ValidIssueID(n.WorkItemID) && !n.NamedAt.IsZero() }

// Progress is epic-progress.json.
type Progress struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Entries       []Entry          `json:"entries"`
}

// Begun is epic-progress-begun.json.
type Begun struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Namings       []Naming         `json:"namings"`
}

// Entry is what is remembered of one naming.
type Entry struct {
	Naming
	BegunAt time.Time `json:"begun_at"`
	// NamedBeforeKept is that the epic was named before the harness kept
	// progress, as far as it can tell (keptSince).
	NamedBeforeKept bool          `json:"named_before_kept,omitempty"`
	Joined          []Joined      `json:"joined,omitempty"`
	Pieces          []Piece       `json:"pieces,omitempty"`
	Comments        []Comment     `json:"comments,omitempty"`
	Checks          []Check       `json:"checks,omitempty"`
	Deliveries      []Delivery    `json:"deliveries,omitempty"`
	Reservations    []Reservation `json:"reservations,omitempty"`
}

// Joined is a piece that joined the epic, the member or check it came from, and
// the judgment that let it join, with that judgment's session.
type Joined struct {
	WorkItemID string    `json:"work_item_id"`
	From       string    `json:"from"`
	Judgment   string    `json:"judgment"`
	Session    string    `json:"session"`
	At         time.Time `json:"at"`
}

// MaxJoined is how many pieces may join one naming. Nothing raises it. A piece the
// operator took out still counts, and an item the operator added does not.
const MaxJoined = 12

// Piece is one made by an act that may let it join, and whether it did. Judgment
// is the reservation of its judgment, and Outcome is nil until it is judged.
type Piece struct {
	WorkItemID string    `json:"work_item_id"`
	From       string    `json:"from"`
	Judgment   string    `json:"judgment"`
	At         time.Time `json:"at"`
	Outcome    *Outcome  `json:"outcome,omitempty"`
}

// Answer is what decided whether a piece joined.
type Answer string

// Every answer but AnswerInside is a question about scope.
const (
	AnswerInside     Answer = "inside"
	AnswerOutside    Answer = "outside"
	AnswerCannotTell Answer = "cannot tell"
	AnswerLimit      Answer = "limit reached"
	AnswerTakenOut   Answer = "taken out"
)

// Valid reports an answer this build knows.
func (a Answer) Valid() bool {
	return slices.Contains([]Answer{AnswerInside, AnswerOutside, AnswerCannotTell, AnswerLimit, AnswerTakenOut}, a)
}

// Outcome is what became of a piece. Session is the judging session's, where one
// answered.
type Outcome struct {
	At      time.Time `json:"at"`
	Answer  Answer    `json:"answer"`
	Why     string    `json:"why"`
	Session string    `json:"session,omitempty"`
}

// Asked is what is kept of a piece that may join, where anything is.
func (e Entry) Asked(item string) (Piece, bool) {
	if at := e.piece(item); at >= 0 {
		return e.Pieces[at], true
	}
	return Piece{}, false
}

func (e Entry) piece(item string) int {
	return slices.IndexFunc(e.Pieces, func(piece Piece) bool { return piece.WorkItemID == item })
}

// Full is why no further piece may join, and empty while one may.
func (e Entry) Full() string {
	if len(e.Joined) < MaxJoined {
		return ""
	}
	return fmt.Sprintf("%d pieces have joined, which is the most that may", len(e.Joined))
}

// Reservation is the one kept under an identifier, where one is.
func (e Entry) Reservation(id string) (Reservation, bool) {
	if at := slices.IndexFunc(e.Reservations, func(r Reservation) bool { return r.ID == id }); at >= 0 {
		return e.Reservations[at], true
	}
	return Reservation{}, false
}

// Comment is a review comment acted on, by the forge's identifier.
type Comment struct {
	ID     string    `json:"id"`
	Became string    `json:"became"`
	At     time.Time `json:"at"`
}

// What a check of the whole epic found. A check that found anything else did not
// pass, and its Found says what could not be told.
const (
	// FoundAllMet is everything met, or taken out by the operator.
	FoundAllMet     = "all met"
	FoundNotMet     = "not met"
	FoundCannotTell = "cannot be told"
	// FoundPersonsStep is something the description itself reserves for a person.
	FoundPersonsStep = "reserved for a person"
)

// Passed reports a check that found everything met or taken out.
func (c Check) Passed() bool { return c.Found == FoundAllMet }

// Check is one check of the whole epic: the commit it read and, for each thing
// the description asks, one answer. Found is worked out from the answers.
type Check struct {
	ID       string    `json:"id"`
	Commit   string    `json:"commit"`
	Found    string    `json:"found"`
	At       time.Time `json:"at"`
	Findings []Finding `json:"findings,omitempty"`
	// Owed is the one correction owed for this check, where one is: for what it
	// found missing, or for the evidence the Lead Product Manager's handling of the
	// question in Asked named, for a check that could not tell.
	Owed *Correction `json:"owed,omitempty"`
	// Asked is the question about the missing evidence the harness put to the Lead
	// Product Manager for a check that could not tell, where it has.
	Asked *Asked `json:"asked,omitempty"`
}

// Asked is the one report the harness files the Lead Product Manager for a check
// that could not tell, asking what evidence would settle it. Its identifier is
// written here before the report is filed. Her recorded handling of it is her
// answer, and nothing else counts.
type Asked struct {
	Report string    `json:"report"`
	At     time.Time `json:"at"`
	// Answered is when her handling was recorded, once the harness has read it.
	Answered *time.Time `json:"answered,omitempty"`
	// Operator is the reason from the operator's closed list her handling gave,
	// where it recorded that only he can act, and Says is what it said he has to
	// do. No correction is owed then, and the epic waits on him.
	Operator ownership.OperatorReason `json:"operator,omitempty"`
	Says     string                   `json:"says,omitempty"`
}

func (a *Asked) valid() bool {
	return a == nil || report.ValidID(a.Report) && !a.At.IsZero() &&
		(a.Operator == "" && a.Says == "" || a.Answered != nil && a.Operator.Valid() && strings.TrimSpace(a.Says) != "")
}

// Correction is the one correcting piece owed for a check of the whole epic. The
// development manager writes it, naming the check, and the harness writes the
// piece's identifier here before the note on the item. A piece is a correction
// only where a correction names it: the note is for people to read.
type Correction struct {
	// Missing is what the piece has to give: what the check found missing, or the
	// evidence it lacked.
	Missing string    `json:"missing"`
	At      time.Time `json:"at"`
	// Answered is when the Lead Product Manager's handling that named the evidence
	// was recorded, for a correction owed for evidence.
	Answered *time.Time `json:"answered,omitempty"`
	Piece    string     `json:"piece,omitempty"`
	// Noted is that the note naming the epic, the check and its commit is on the
	// piece. A stop between the two writes leaves it false, and the note is
	// written at the next reading.
	Noted bool `json:"noted,omitempty"`
}

func (c *Correction) valid(epic string) bool {
	return c == nil || strings.TrimSpace(c.Missing) != "" && !c.At.IsZero() &&
		(c.Piece == "" && !c.Noted || beads.ValidIssueID(c.Piece) && c.Piece != epic)
}

// Asking is the last check of the naming where it could not tell and owes no
// correction yet: the one whose question about evidence the harness puts to the
// Lead Product Manager, or whose answer it reads. An earlier check was made again,
// so it asks nothing.
func (e Entry) Asking() (Check, bool) {
	if len(e.Checks) == 0 {
		return Check{}, false
	}
	last := e.Checks[len(e.Checks)-1]
	if last.Found != FoundCannotTell || last.Owed != nil || last.Asked != nil && last.Asked.Answered != nil {
		return Check{}, false
	}
	return last, true
}

// CouldNotTell is what a check could not tell, each thing asked with why, leaving
// out a step the description reserves for a person.
func CouldNotTell(check Check) string {
	var said []string
	for _, finding := range check.Findings {
		if finding.Answer == CannotBeTold && !finding.ForAPerson {
			said = append(said, finding.Asks+": "+finding.Says)
		}
	}
	return strings.Join(said, "; ")
}

// FromCheck is what a correcting piece is recorded as made from: the check, in
// the place a split records the member.
func FromCheck(check string) string { return "the check " + check }

// Correcting is the check whose owed correction names the item, where one does.
func (e Entry) Correcting(item string) (Check, bool) {
	if at := slices.IndexFunc(e.Checks, func(check Check) bool { return check.Owed != nil && check.Owed.Piece == item }); item != "" && at >= 0 {
		return e.Checks[at], true
	}
	return Check{}, false
}

// correctsFrom reports a piece made from a check whose owed correction names it.
func (e Entry) correctsFrom(item, from string) bool {
	check, correcting := e.Correcting(item)
	return correcting && FromCheck(check.ID) == from
}

// Superseded reports a check that did not pass and was made again because the
// operator took an item out after it, with nothing joined in between. It is not a
// round of correction.
func (e Entry) Superseded(epic epicscope.Epic, check Check) bool {
	return !check.Passed() && slices.ContainsFunc(epic.Removed, func(removal epicscope.Removal) bool {
		return removal.RemovedAt.After(check.At) && !slices.ContainsFunc(e.Joined, func(joined Joined) bool {
			return joined.At.After(check.At) && joined.At.Before(removal.RemovedAt)
		})
	})
}

// Rounds is how many checks of the naming found something missing, each a round
// of correction.
func (e Entry) Rounds(epic epicscope.Epic) int {
	rounds := 0
	for _, check := range e.Checks {
		if check.Found == FoundNotMet && !e.Superseded(epic, check) {
			rounds++
		}
	}
	return rounds
}

// MaxRounds is how many rounds of correction the harness starts by itself. A
// check that finds something missing after them goes to the Lead Product Manager.
const MaxRounds = 2

// CorrectionNote is the note of fixed form written on a correcting piece, for
// people to read. Nothing reads it to decide anything.
func CorrectionNote(epic string, check Check) string {
	return fmt.Sprintf("Written as the correction owed by the check of the whole epic %s, %s, which read commit %s.", epic, check.ID, check.Commit)
}

// CheckAnswer is the answer a check gives one thing the description asks.
type CheckAnswer string

const (
	Met          CheckAnswer = "met"
	TakenOut     CheckAnswer = "taken out"
	NotMet       CheckAnswer = "not met"
	CannotBeTold CheckAnswer = "cannot be told"
)

// Finding is one thing the description asks and the answer to it. Says is where
// the session looked for met, what is missing for not met, and why for cannot be
// told. Removal is the item the operator took out, for taken out, which is an
// answer only for what that item would have given. ForAPerson is a cannot be told
// because the description itself reserves the step for a person.
type Finding struct {
	Asks       string      `json:"asks"`
	Answer     CheckAnswer `json:"answer"`
	Says       string      `json:"says,omitempty"`
	Removal    string      `json:"removal,omitempty"`
	ForAPerson bool        `json:"for_a_person,omitempty"`
}

func (f Finding) valid() bool {
	if strings.TrimSpace(f.Asks) == "" || f.ForAPerson && f.Answer != CannotBeTold {
		return false
	}
	switch f.Answer {
	case TakenOut:
		return beads.ValidIssueID(f.Removal)
	case Met, NotMet, CannotBeTold:
		return strings.TrimSpace(f.Says) != "" && f.Removal == ""
	}
	return false
}

// FoundOf is what a check found from its answers: something missing first,
// because a correction is written for it; then what cannot be told; then a step
// the description reserves for a person; and all met only where every answer is
// met or taken out.
func FoundOf(findings []Finding) string {
	found := FoundAllMet
	for _, finding := range findings {
		switch {
		case finding.Answer == NotMet:
			return FoundNotMet
		case finding.Answer == CannotBeTold && !finding.ForAPerson:
			found = FoundCannotTell
		case finding.Answer == CannotBeTold && found == FoundAllMet:
			found = FoundPersonsStep
		}
	}
	return found
}

// Delivery is the epic having been delivered, and on what evidence.
type Delivery struct {
	At       time.Time `json:"at"`
	Evidence string    `json:"evidence"`
}

// Reservation is a start or a paid call written down before it is made.
type Reservation struct {
	ID  string    `json:"id"`
	For string    `json:"for"`
	At  time.Time `json:"at"`
	// HeldBy names the durable record that already holds this reservation and its
	// result, such as a run's. Where it is set nothing more is kept here.
	HeldBy string  `json:"held_by,omitempty"`
	Result *Result `json:"result,omitempty"`
}

// Result is what became of a reservation. Unknown is that it cannot be known.
type Result struct {
	At      time.Time `json:"at"`
	Says    string    `json:"says"`
	Unknown bool      `json:"unknown,omitempty"`
}

func (e Entry) empty() bool {
	return len(e.Joined)+len(e.Pieces)+len(e.Comments)+len(e.Checks)+len(e.Deliveries)+len(e.Reservations) == 0
}

// Has reports whether the item joined.
func (e Entry) Has(item string) bool {
	return slices.ContainsFunc(e.Joined, func(joined Joined) bool { return joined.WorkItemID == item })
}

func (p Progress) index(naming Naming) int {
	return slices.IndexFunc(p.Entries, func(entry Entry) bool { return entry.same(naming) })
}

func (b Begun) has(naming Naming) bool {
	return slices.ContainsFunc(b.Namings, naming.same)
}

func heading(schema int, product, want domain.ProductID) error {
	if schema != SchemaVersion || product != want {
		return fmt.Errorf("it has schema version %d and belongs to product %q, where %d and %q are read", schema, product, SchemaVersion, want)
	}
	return nil
}

func (p Progress) validate(product domain.ProductID) error {
	if err := heading(p.SchemaVersion, p.ProductID, product); err != nil {
		return err
	}
	if len(p.Entries) == 0 {
		return errors.New("it holds no entry, and it is written only once one has been begun")
	}
	for at, entry := range p.Entries {
		if !entry.valid() || entry.BegunAt.IsZero() {
			return fmt.Errorf("an entry for %q needs a work item, when it was named and when the entry was begun", entry.WorkItemID)
		}
		if p.index(entry.Naming) != at {
			return fmt.Errorf("epic %s has two entries for one naming", entry.WorkItemID)
		}
		if err := entry.validate(); err != nil {
			return fmt.Errorf("epic %s: %w", entry.WorkItemID, err)
		}
	}
	return nil
}

// validate refuses an entry with a part that is not whole or is listed twice.
func (e Entry) validate() error {
	var problems []error
	seen := map[string]bool{}
	part := func(kind, id string, whole bool) {
		if key := kind + " " + id; !whole || id == "" || seen[key] {
			problems = append(problems, fmt.Errorf("the %s %q is missing something it needs, or is listed twice", kind, id))
		} else {
			seen[key] = true
		}
	}
	for _, j := range e.Joined {
		part("joined piece", j.WorkItemID, beads.ValidIssueID(j.WorkItemID) && j.WorkItemID != e.WorkItemID && j.From != "" && j.Judgment != "" && j.Session != "" && !j.At.IsZero())
	}
	for _, p := range e.Pieces {
		part("piece", p.WorkItemID, beads.ValidIssueID(p.WorkItemID) && p.WorkItemID != e.WorkItemID && p.From != "" && p.Judgment != "" && !p.At.IsZero() &&
			(p.Outcome == nil || p.Outcome.Answer.Valid() && p.Outcome.Why != "" && !p.Outcome.At.IsZero()))
	}
	for _, c := range e.Comments {
		part("comment", c.ID, c.Became != "" && !c.At.IsZero())
	}
	for _, c := range e.Checks {
		part("check", c.ID, c.Commit != "" && c.Found != "" && !c.At.IsZero() &&
			(len(c.Findings) == 0 || c.Found == FoundOf(c.Findings) && !slices.ContainsFunc(c.Findings, func(f Finding) bool { return !f.valid() })) &&
			c.Owed.valid(e.WorkItemID) && (c.Owed == nil || c.Found == FoundNotMet || c.Found == FoundCannotTell) &&
			c.Asked.valid() && (c.Asked == nil || c.Found == FoundCannotTell) && (c.Asked == nil || c.Asked.Operator == "" || c.Owed == nil))
	}
	for _, c := range e.Checks {
		if c.Owed != nil && c.Owed.Piece != "" {
			part("correcting piece", c.Owed.Piece, true)
		}
	}
	for _, d := range e.Deliveries {
		part("delivery", d.At.String(), d.Evidence != "" && !d.At.IsZero())
	}
	for _, r := range e.Reservations {
		part("reservation", r.ID, r.For != "" && !r.At.IsZero())
		if r.Result != nil && (r.HeldBy != "" || r.Result.At.IsZero()) {
			problems = append(problems, fmt.Errorf("the result of reservation %q needs when, and is not kept where another record holds it", r.ID))
		}
	}
	return errors.Join(problems...)
}

func (b Begun) validate(product domain.ProductID) error {
	if err := heading(b.SchemaVersion, b.ProductID, product); err != nil {
		return err
	}
	if len(b.Namings) == 0 {
		return errors.New("it lists no naming, and it is written only once one has been begun")
	}
	for at, naming := range b.Namings {
		if !naming.valid() || slices.IndexFunc(b.Namings, naming.same) != at {
			return fmt.Errorf("the naming of %q needs a work item and when it was named, listed once", naming.WorkItemID)
		}
	}
	return nil
}

// View is the three files as one reading.
type View struct {
	Record   epicscope.Record
	progress Progress
	begun    Begun
}

// Read reads the two files beside a reading of the operator's record. A file
// that is there and cannot be read whole is an error, and never read as no file.
//
// The list is read on both sides of the entries. An entry is begun with one write
// and listed with another, so two readings of the list that agree are of the
// entries read between them.
func Read(stateRoot string, product domain.ProductID, record epicscope.Record) (View, error) {
	if err := place(stateRoot, product); err != nil {
		return View{}, err
	}
	directory := home.ProductDirectory(stateRoot, string(product))
	readBegun := func() (Begun, error) {
		return readFile(filepath.Join(directory, begunName), Begun{SchemaVersion: SchemaVersion, ProductID: product}, func(b Begun) error { return b.validate(product) })
	}
	begun, err := readBegun()
	if err != nil {
		return View{}, err
	}
	for range 5 {
		progress, problem := readFile(filepath.Join(directory, progressName), Progress{SchemaVersion: SchemaVersion, ProductID: product}, func(p Progress) error { return p.validate(product) })
		again, err := readBegun()
		if problem != nil || err != nil {
			return View{}, errors.Join(problem, err)
		}
		if slices.EqualFunc(begun.Namings, again.Namings, Naming.same) {
			return View{Record: record, progress: progress, begun: begun}, nil
		}
		begun = again
	}
	return View{}, errors.New("the files that keep the epics' progress changed while they were read")
}

func place(stateRoot string, product domain.ProductID) error {
	if !filepath.IsAbs(stateRoot) {
		return errors.New("state root must be an absolute path")
	}
	return domain.ValidateIdentifier("product id", string(product))
}

// readFile reads one file whole, and answers missing where there is none. It is
// strict because every read either precedes a write of the whole file or decides
// whether something may start.
func readFile[T any](path string, missing T, validate func(T) error) (T, error) {
	var none, read T
	// Only the name's own absence is the absence of a file.
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return missing, nil
	}
	if err != nil {
		return none, fmt.Errorf("look for the epics' progress: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return none, fmt.Errorf("the epics' progress, %s, is not a regular file of at most %d bytes", path, maxFileBytes)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return none, fmt.Errorf("read the epics' progress: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&read)
	if _, after := decoder.Token(); err == nil && !errors.Is(after, io.EOF) {
		err = errors.New("something follows the record")
	}
	if err == nil {
		err = validate(read)
	}
	if err != nil {
		return none, fmt.Errorf("the epics' progress, %s: %w", path, err)
	}
	return read, nil
}

// Entry is what is kept of a naming, where anything is.
func (v View) Entry(naming Naming) (Entry, bool) {
	if at := v.progress.index(naming); at >= 0 {
		return v.progress.Entries[at], true
	}
	return Entry{}, false
}

// Unreadable is why a naming's progress cannot be read, and empty where it can.
// A naming that was never begun can, and so can an empty entry that a stop left
// out of the list.
func (v View) Unreadable(naming Naming) string {
	entry, kept := v.Entry(naming)
	listed := v.begun.has(naming)
	switch {
	case listed && !kept:
		return fmt.Sprintf("%s says its progress was begun, and %s holds no entry for it", begunName, progressName)
	case kept && !listed && !entry.empty():
		return fmt.Sprintf("%s holds its entry, and %s does not say it was begun", progressName, begunName)
	}
	return ""
}

// UnknownResults is each reservation of a naming whose result cannot be known.
func (v View) UnknownResults(naming Naming) []Reservation {
	entry, _ := v.Entry(naming)
	return slices.DeleteFunc(slices.Clone(entry.Reservations), func(r Reservation) bool { return r.Result == nil || !r.Result.Unknown })
}

// Precedes reports a run, by when it started, that is older than anything kept
// of a naming's progress: the naming was not begun, or was begun after it and
// does not point to it. A run from before progress was kept is not one.
func (v View) Precedes(naming Naming, runID string, startedAt time.Time) bool {
	if startedAt.Before(keptSince) {
		return false
	}
	entry, kept := v.Entry(naming)
	return !kept || startedAt.Before(entry.BegunAt) &&
		!slices.ContainsFunc(entry.Reservations, func(r Reservation) bool { return r.ID == runReservation(runID) })
}

// RunsReserved is each run whose start was reserved for a naming, once, in the
// order they were reserved. A run's record holds its reservation, so these are
// the runs made for the epic, those of items taken out since included.
func (e Entry) RunsReserved() []string {
	var runs []string
	for _, reserved := range e.Reservations {
		if id, isRun := strings.CutPrefix(reserved.ID, runReservation("")); isRun && reserved.HeldBy != "" && !slices.Contains(runs, id) {
			runs = append(runs, id)
		}
	}
	return runs
}

// CallsReserved is each paid call reserved for a naming that is not a run's
// start: a joining judgment or a check of the whole epic.
func (e Entry) CallsReserved() []Reservation {
	return slices.DeleteFunc(slices.Clone(e.Reservations), func(reserved Reservation) bool {
		return strings.HasPrefix(reserved.ID, runReservation("")) && reserved.HeldBy != ""
	})
}

// RunSharedBy is how many namings a run's start was reserved for: a document
// may answer items of two epics, and its cost is then shared between them.
func (v View) RunSharedBy(runID string) int {
	shared := 0
	for _, entry := range v.progress.Entries {
		if slices.Contains(entry.RunsReserved(), runID) {
			shared++
		}
	}
	return shared
}

// Items is every item in an epic, the members first, each once.
func (v View) Items(epic epicscope.Epic) []string {
	var items []string
	for _, member := range epic.Members {
		items = append(items, member.WorkItemID)
	}
	entry, _ := v.Entry(Of(epic))
	for _, joined := range entry.Joined {
		if !epic.Has(joined.WorkItemID) && !epic.TakenOut(joined.WorkItemID) {
			items = append(items, joined.WorkItemID)
		}
	}
	return items
}

// Files is where the two files are, for a person.
func Files(stateRoot string, product domain.ProductID) string {
	directory := home.ProductDirectory(stateRoot, string(product))
	return filepath.Join(directory, progressName) + " and " + filepath.Join(directory, begunName)
}

// Holding is what the operator's acts ask about an item while they hold the
// record's lock: the open epics it is in, the one it joined included.
func Holding(stateRoot string, product domain.ProductID) epicscope.JoinedReader {
	return func(record epicscope.Record) (epicscope.Joins, error) {
		view, err := Read(stateRoot, product, record)
		return epicscope.Joins{Holders: view.Holders, Unreadable: func(epic epicscope.Epic) string { return view.Unreadable(Of(epic)) }}, err
	}
}

// Holders is every open epic the item is in, as a member or as a piece that
// joined and was not taken out. An ended epic holds nothing.
func (v View) Holders(item string) []epicscope.Epic {
	var holders []epicscope.Epic
	for _, epic := range v.Record.OpenEpics() {
		entry, _ := v.Entry(Of(epic))
		if epic.Has(item) || entry.Has(item) && !epic.TakenOut(item) {
			holders = append(holders, epic)
		}
	}
	return holders
}
