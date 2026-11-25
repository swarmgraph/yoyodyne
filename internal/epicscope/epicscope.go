// Package epicscope keeps the record of the epics the operator has named: the
// one file that says whether epic mode is on, which epics were named, and which
// work items are in each.
//
// The file is epic-scope.json, in the product's state directory beside the hold
// on new work. It is read through Read, which anything may call, and written
// only through the acts of the Operator (operator.go), which only a command the
// operator runs may open: a test here holds the module to the list of files
// that do, and no role's block and no scheduler is on it.
//
// Membership is the recorded list and nothing else. It is never worked out from
// the tracker, so an item moved or added below an epic afterwards changes
// nothing here, and the epic itself is not one of its members.
package epicscope

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
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const (
	// SchemaVersion is 1 and has never changed.
	SchemaVersion = 1

	// MaxWhereBytes bounds where an act was made, which is one line, and
	// MaxWordsBytes what the operator said with it.
	MaxWhereBytes = 1 << 10
	MaxWordsBytes = 16 << 10

	// maxFileBytes bounds the whole file, for a write and a read alike, so that
	// nothing is written that would not be read back.
	maxFileBytes = 8 << 20

	fileName = "epic-scope.json"
	lockName = fileName + ".lock"
)

// Mode is whether the factory works only on the epics the operator named. It is
// written out in full rather than as a flag so that a file that has lost it is
// refused: an absent flag would read as epic mode off, and start everything.
type Mode string

const (
	ModeGoal Mode = "goal" // epic mode is off
	ModeEpic Mode = "epic" // epic mode is on
)

// Act is the operator's act that put an item in an epic's members.
type Act string

const (
	ActStart Act = "start" // it was on the list recorded when the epic was named
	ActAdd   Act = "add"   // the operator added it afterwards
)

// Record is the file.
type Record struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Mode          Mode             `json:"mode"`
	// Epics is one entry for each naming, in the order they were made. An epic
	// that ended may be named again, so it may have several, and at most one of
	// them, the last, is open.
	Epics []Epic `json:"epics"`
}

// Epic is one naming of an epic by the operator.
type Epic struct {
	WorkItemID string    `json:"work_item_id"`
	NamedAt    time.Time `json:"named_at"`
	NamedFrom  string    `json:"named_from"`
	// Words is what the operator said when naming it, if anything.
	Words string `json:"words,omitempty"`
	// Description is the epic's description as it read when it was named, kept
	// whole, so that editing the epic afterwards does not widen what was
	// authorized.
	Description string   `json:"description"`
	Members     []Member `json:"members"`
	// Removed is each item the operator took out, oldest first. One put back is a
	// member again, and its removal is kept here as history.
	Removed []Removal `json:"removed,omitempty"`
	// Ended is absent while the epic is open.
	Ended *Ending `json:"ended,omitempty"`
}

// Member is one work item in an epic's scope.
type Member struct {
	WorkItemID string    `json:"work_item_id"`
	AddedAt    time.Time `json:"added_at"`
	Act        Act       `json:"act"`
}

// Removal is the operator taking one item out of an epic.
type Removal struct {
	WorkItemID string    `json:"work_item_id"`
	RemovedAt  time.Time `json:"removed_at"`
	Words      string    `json:"words"`
	// AddedAt and Act are when and by which act a member had been put in. A piece
	// that joined was in no list of this record and has neither.
	AddedAt *time.Time `json:"added_at,omitempty"`
	Act     Act        `json:"act,omitempty"`
}

// Ending is the operator ending an epic.
type Ending struct {
	At    time.Time `json:"at"`
	Words string    `json:"words,omitempty"`
}

// On reports whether epic mode is on.
func (r Record) On() bool { return r.Mode == ModeEpic }

// Named finds the latest entry for an epic, which is the open one where there is
// one.
func (r Record) Named(epic string) (Epic, bool) {
	if at := r.index(epic); at >= 0 {
		return r.Epics[at], true
	}
	return Epic{}, false
}

// Holding finds the epic that has not ended and has the item as a member.
func (r Record) Holding(item string) (Epic, bool) {
	for _, epic := range r.Epics {
		if epic.Open() && epic.Has(item) {
			return epic, true
		}
	}
	return Epic{}, false
}

func (r Record) index(epic string) int {
	for at := len(r.Epics) - 1; at >= 0; at-- {
		if r.Epics[at].WorkItemID == epic {
			return at
		}
	}
	return -1
}

// OpenEpics is every entry the operator has not ended, in the order they were
// named. They are kept open while epic mode is off.
func (r Record) OpenEpics() []Epic {
	var open []Epic
	for _, epic := range r.Epics {
		if epic.Open() {
			open = append(open, epic)
		}
	}
	return open
}

// Open reports an epic the operator has not ended.
func (e Epic) Open() bool { return e.Ended == nil }

// Has reports whether the item is on the epic's recorded list.
func (e Epic) Has(item string) bool {
	for _, member := range e.Members {
		if member.WorkItemID == item {
			return true
		}
	}
	return false
}

// TakenOut reports an item the operator took out and has not put back. A piece
// that joined is in the epic unless it is one.
func (e Epic) TakenOut(item string) bool {
	return !e.Has(item) && slices.ContainsFunc(e.Removed, func(removal Removal) bool { return removal.WorkItemID == item })
}

// Validate refuses a record this build cannot speak for.
func (r Record) Validate() error {
	var problems []error
	if r.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("epic record schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if r.Mode != ModeGoal && r.Mode != ModeEpic {
		problems = append(problems, fmt.Errorf("mode %q is neither %q nor %q", r.Mode, ModeGoal, ModeEpic))
	}
	if len(r.Epics) == 0 {
		problems = append(problems, errors.New("the file names no epic, and it is written only once one has been named"))
	}
	open := map[string]bool{}
	for _, epic := range r.Epics {
		if open[epic.WorkItemID] {
			problems = append(problems, fmt.Errorf("epic %s has another entry after one that has not ended", epic.WorkItemID))
		}
		open[epic.WorkItemID] = open[epic.WorkItemID] || epic.Open()
		if err := epic.validate(); err != nil {
			problems = append(problems, fmt.Errorf("epic %q: %w", epic.WorkItemID, err))
		}
	}
	return errors.Join(problems...)
}

func (e Epic) validate() error {
	var problems []error
	if !beads.ValidIssueID(e.WorkItemID) {
		problems = append(problems, errors.New("that is not a work item identifier"))
	}
	if e.NamedAt.IsZero() {
		problems = append(problems, errors.New("when it was named is required"))
	}
	if strings.TrimSpace(e.NamedFrom) == "" {
		problems = append(problems, errors.New("where it was named from is required"))
	}
	if len(e.NamedFrom) > MaxWhereBytes {
		problems = append(problems, fmt.Errorf("where it was named from is %d bytes, limit is %d", len(e.NamedFrom), MaxWhereBytes))
	}
	if len(e.Words) > MaxWordsBytes {
		problems = append(problems, fmt.Errorf("the words it was named with are %d bytes, limit is %d", len(e.Words), MaxWordsBytes))
	}
	if e.Members == nil {
		problems = append(problems, errors.New("its list of members is required, even when it is empty"))
	}
	seen := map[string]bool{}
	for _, member := range e.Members {
		switch {
		case !beads.ValidIssueID(member.WorkItemID):
			problems = append(problems, fmt.Errorf("member %q is not a work item identifier", member.WorkItemID))
		case member.WorkItemID == e.WorkItemID:
			problems = append(problems, errors.New("an epic is not one of its own members"))
		case seen[member.WorkItemID]:
			problems = append(problems, fmt.Errorf("member %s is listed twice", member.WorkItemID))
		}
		seen[member.WorkItemID] = true
		if member.AddedAt.IsZero() {
			problems = append(problems, fmt.Errorf("when member %q was added is required", member.WorkItemID))
		}
		if member.Act != ActStart && member.Act != ActAdd {
			problems = append(problems, fmt.Errorf("member %q was added by %q, which is not an act this build records", member.WorkItemID, member.Act))
		}
	}
	for _, removal := range e.Removed {
		if !beads.ValidIssueID(removal.WorkItemID) || removal.WorkItemID == e.WorkItemID || removal.RemovedAt.IsZero() {
			problems = append(problems, fmt.Errorf("removed item %q needs a work item identifier that is not the epic's and when it was taken out", removal.WorkItemID))
		}
		if strings.TrimSpace(removal.Words) == "" || len(removal.Words) > MaxWordsBytes {
			problems = append(problems, fmt.Errorf("the words %q was taken out with are required and may be at most %d bytes", removal.WorkItemID, MaxWordsBytes))
		}
		wasMember := removal.AddedAt != nil && !removal.AddedAt.IsZero() && (removal.Act == ActStart || removal.Act == ActAdd)
		if !wasMember && (removal.AddedAt != nil || removal.Act != "") {
			problems = append(problems, fmt.Errorf("removed item %q needs both when and by which act it had been put in, or neither", removal.WorkItemID))
		}
	}
	if e.Ended != nil {
		if e.Ended.At.IsZero() {
			problems = append(problems, errors.New("when it was ended is required"))
		}
		if len(e.Ended.Words) > MaxWordsBytes {
			problems = append(problems, fmt.Errorf("the words it was ended with are %d bytes, limit is %d", len(e.Ended.Words), MaxWordsBytes))
		}
	}
	return errors.Join(problems...)
}

// ChangedAt is when an operator's act last changed a product's record, read from
// the file, which nothing else writes. It is zero where there is no file.
func ChangedAt(stateRoot string, product domain.ProductID) time.Time {
	info, err := os.Lstat(filepath.Join(home.ProductDirectory(stateRoot, string(product)), fileName))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// Read reads a product's record. No file means epic mode is off and no epic was
// ever named. A file that is there and cannot be read whole is an error, which
// a caller starts nothing on, and is never read as no file.
func Read(stateRoot string, product domain.ProductID) (Record, error) {
	if !filepath.IsAbs(stateRoot) {
		return Record{}, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(product)); err != nil {
		return Record{}, err
	}
	path := filepath.Join(home.ProductDirectory(stateRoot, string(product)), fileName)
	// Only the name's own absence is the absence of a file: opening a link that
	// points at nothing says "not found" too.
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{SchemaVersion: SchemaVersion, ProductID: product, Mode: ModeGoal}, nil
	}
	if err != nil {
		return Record{}, fmt.Errorf("look for the record of named epics: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Record{}, fmt.Errorf("the record of named epics, %s, is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return Record{}, fmt.Errorf("open the record of named epics: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return Record{}, fmt.Errorf("read the record of named epics: %w", err)
	}
	record, err := decode(encoded, product)
	if err != nil {
		return Record{}, fmt.Errorf("the record of named epics, %s: %w", path, err)
	}
	return record, nil
}

// decode is strict because every read either precedes a write of the whole file
// or decides whether something may start, and neither may go on with part of it.
func decode(encoded []byte, product domain.ProductID) (Record, error) {
	if len(encoded) > maxFileBytes {
		return Record{}, fmt.Errorf("it is larger than %d bytes", maxFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var record Record
	if err := decoder.Decode(&record); err != nil {
		return Record{}, fmt.Errorf("decode: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Record{}, errors.New("decode: something follows the record")
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	if record.ProductID != product {
		return Record{}, fmt.Errorf("it belongs to product %q, not %q", record.ProductID, product)
	}
	return record, nil
}
