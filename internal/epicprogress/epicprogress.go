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
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const (
	// SchemaVersion is 1 for both files and has never changed.
	SchemaVersion = 1

	// maxFileBytes bounds each file, for a write and a read alike.
	maxFileBytes = 8 << 20

	progressName = "epic-progress.json"
	begunName    = "epic-progress-begun.json"
	lockName     = progressName + ".lock"
)

// keptSince is about when the harness first kept progress. An epic named before
// it may have been worked on before anything was kept.
var keptSince = time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)

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

// Comment is a review comment acted on, by the forge's identifier.
type Comment struct {
	ID     string    `json:"id"`
	Became string    `json:"became"`
	At     time.Time `json:"at"`
}

// Check is one check of the whole epic.
type Check struct {
	ID     string    `json:"id"`
	Commit string    `json:"commit"`
	Found  string    `json:"found"`
	At     time.Time `json:"at"`
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
	return len(e.Joined)+len(e.Comments)+len(e.Checks)+len(e.Deliveries)+len(e.Reservations) == 0
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
	for _, c := range e.Comments {
		part("comment", c.ID, c.Became != "" && !c.At.IsZero())
	}
	for _, c := range e.Checks {
		part("check", c.ID, c.Commit != "" && c.Found != "" && !c.At.IsZero())
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

// Holders is every open epic the item is in, as a member or as a piece that
// joined. An ended epic holds nothing.
func (v View) Holders(item string) []epicscope.Epic {
	var holders []epicscope.Epic
	for _, epic := range v.Record.OpenEpics() {
		entry, _ := v.Entry(Of(epic))
		if epic.Has(item) || entry.Has(item) {
			holders = append(holders, epic)
		}
	}
	return holders
}
