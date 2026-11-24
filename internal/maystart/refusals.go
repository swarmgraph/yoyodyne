package maystart

import (
	"bytes"
	"context"
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
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// RefusalsSchemaVersion is 1 and has never changed.
	RefusalsSchemaVersion = 1

	refusalsFile = "not-started.json"
	refusalsLock = refusalsFile + ".lock"

	// maxRefusalsBytes bounds the file for a write and a read alike.
	maxRefusalsBytes = 8 << 20

	refusalsLockWait = 5 * time.Second

	// refusedAgainAfter bounds how often the same refusal moves its time. The
	// scheduler asks about every item outside at every poll, and each move is a
	// synced write.
	refusedAgainAfter = 10 * time.Minute
)

// Refusals is what was not started because of the epics the operator named: what
// is outside them, and what could not be judged. Ask is what keeps it. It decides
// nothing: it is kept so that status can say what was refused.
type Refusals struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Items is one entry for each work item and path, from its first refusal
	// until the item may start, closes, or epic mode ends.
	Items []RefusedItem `json:"items,omitempty"`
	// Passes is one entry for each role, from its first refusal until a pass of
	// that role may start.
	Passes []RefusedPasses `json:"passes,omitempty"`
}

// RefusedItem is a work item whose start by one path was refused.
type RefusedItem struct {
	WorkItemID string `json:"work_item_id"`
	Path       Path   `json:"path"`
	// Why is the reason last given.
	Why string `json:"why"`
	// Since is when it was first refused, and Last when it was last refused, to
	// within refusedAgainAfter.
	Since time.Time `json:"since"`
	Last  time.Time `json:"last"`
}

// RefusedPasses is how many passes of a role were refused.
type RefusedPasses struct {
	Role  domain.AgentRole `json:"role"`
	Count int              `json:"count"`
	Since time.Time        `json:"since"`
}

// Validate refuses a record this build cannot speak for.
func (r Refusals) Validate() error {
	var problems []error
	if r.SchemaVersion != RefusalsSchemaVersion {
		problems = append(problems, fmt.Errorf("schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	for _, item := range r.Items {
		if !beads.ValidIssueID(item.WorkItemID) || item.Path == "" || item.Why == "" || item.Since.IsZero() || item.Last.IsZero() {
			problems = append(problems, fmt.Errorf("the refusal of %q needs a work item identifier, a path, a reason and when it was first and last refused", item.WorkItemID))
		}
	}
	for _, passes := range r.Passes {
		if passes.Role == "" || passes.Count < 1 || passes.Since.IsZero() {
			problems = append(problems, fmt.Errorf("the refused passes of %q need a role, a count and when the first was refused", passes.Role))
		}
	}
	return errors.Join(problems...)
}

// Ask decides about a start and keeps the answer: the one way a path that is
// wired asks, and the one writer of refused starts. An answer that could not be
// kept is the same answer, and says so in NotKept and, where it is a refusal, in
// its reason.
func Ask(stateRoot string, product domain.ProductID, start Start, at time.Time) Decision {
	decision := Decide(stateRoot, product, start)
	decision.NotKept = Note(stateRoot, product, start, decision, at)
	if decision.NotKept != nil && !decision.MayStart() {
		decision.Why += fmt.Sprintf(" (and this refusal could not be recorded for yoyo status: %v)", decision.NotKept)
	}
	return decision
}

// Note records what Decide answered about a start, and takes away every refusal
// that no longer holds. A refusal already recorded for the same item and path is
// updated. A pass is noted once each time it came due, because each call counts
// one.
//
// The review of unmet goals and the reading of outside messages are not role
// passes and name no work item, so nothing is kept about them.
func Note(stateRoot string, product domain.ProductID, start Start, decision Decision, at time.Time) error {
	return change(stateRoot, product, func(current *Refusals, record epicscope.Record, readable bool) bool {
		noted := current.note(start, decision, at)
		return readable && current.settle(record) || noted
	})
}

// Settle takes away every refusal that no longer holds. A command that changed
// the record of named epics calls it, so what it lets start is not left as
// refused until a path happens to ask again.
func Settle(stateRoot string, product domain.ProductID) error {
	return change(stateRoot, product, func(current *Refusals, record epicscope.Record, readable bool) bool {
		return readable && current.settle(record)
	})
}

// ItemsClosed takes away every refusal of a work item that has closed.
func ItemsClosed(stateRoot string, product domain.ProductID, closed func(item string) bool) error {
	return change(stateRoot, product, func(current *Refusals, _ epicscope.Record, _ bool) bool {
		before := len(current.Items)
		current.Items = slices.DeleteFunc(current.Items, func(item RefusedItem) bool { return closed(item.WorkItemID) })
		return len(current.Items) != before
	})
}

// change applies one change under the lock and writes the record where it
// changed anything. Almost every call changes nothing, so that is found out
// first, without the lock. The record of named epics is read again under it, so
// a refusal decided before the operator's act is not kept after it.
func change(stateRoot string, product domain.ProductID, apply func(current *Refusals, record epicscope.Record, readable bool) bool) error {
	changes := func(strict bool) (Refusals, bool, error) {
		current, err := readRefusals(stateRoot, product, strict)
		if err != nil {
			return Refusals{}, false, err
		}
		record, err := epicscope.Read(stateRoot, product)
		return current, apply(&current, record, err == nil), nil
	}
	if _, changed, err := changes(false); err != nil || !changed {
		return err
	}
	root, err := runstate.PinStateRoot(stateRoot)
	if err != nil {
		return fmt.Errorf("pin the state root: %w", err)
	}
	defer root.Close()
	directory := filepath.FromSlash(home.ProductDirectoryWithin(root.Path(), string(product)))
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return fmt.Errorf("create the product's state directory: %w", err)
	}
	pinned, err := root.OpenDirectory(directory)
	if err != nil {
		return fmt.Errorf("open the product's state directory: %w", err)
	}
	defer pinned.Close()
	lock, err := pinned.OpenLock(refusalsLock, 0o600)
	if err != nil {
		return fmt.Errorf("open the lock on the record of what was not started: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), refusalsLockWait)
	defer cancel()
	release, err := runstate.LockFile(ctx, lock)
	if err != nil {
		return fmt.Errorf("lock the record of what was not started: %w", err)
	}
	defer release()
	current, changed, err := changes(true)
	if err != nil || !changed {
		return err
	}
	return writeRefusals(pinned, current)
}

// note applies one answer, and reports whether it changed anything.
func (r *Refusals) note(start Start, decision Decision, at time.Time) bool {
	changed := false
	switch start.what {
	case workItemRun, documentRun:
		if decision.MayStart() {
			before := len(r.Items)
			// A document may start for one of the items it answers, which says
			// nothing about a run of the others.
			r.Items = slices.DeleteFunc(r.Items, func(item RefusedItem) bool {
				return slices.Contains(start.items, item.WorkItemID) && (start.what == workItemRun || item.Path == start.path)
			})
			return len(r.Items) != before
		}
		for _, id := range start.items {
			if !beads.ValidIssueID(id) || start.path == "" {
				continue
			}
			index := slices.IndexFunc(r.Items, func(item RefusedItem) bool { return item.WorkItemID == id && item.Path == start.path })
			if index < 0 {
				r.Items = append(r.Items, RefusedItem{WorkItemID: id, Path: start.path, Why: decision.Why, Since: at.UTC(), Last: at.UTC()})
				changed = true
				continue
			}
			if entry := &r.Items[index]; entry.Why != decision.Why || at.Sub(entry.Last) >= refusedAgainAfter {
				entry.Why, entry.Last = decision.Why, at.UTC()
				changed = true
			}
		}
	case rolePass:
		index := slices.IndexFunc(r.Passes, func(passes RefusedPasses) bool { return passes.Role == start.role })
		switch {
		case !decision.MayStart() && index >= 0:
			r.Passes[index].Count++
			changed = true
		case !decision.MayStart() && start.role != "":
			r.Passes = append(r.Passes, RefusedPasses{Role: start.role, Count: 1, Since: at.UTC()})
			changed = true
		case decision.MayStart() && index >= 0:
			r.Passes = slices.Delete(r.Passes, index, index+1)
			changed = true
		}
	}
	return changed
}

// settle takes away each refusal of what the record now lets start, and reports
// whether there was one. One made while the record could not be read is given
// the reason the record now gives.
func (r *Refusals) settle(record epicscope.Record) bool {
	changed := false
	kept := make([]RefusedItem, 0, len(r.Items))
	for _, item := range r.Items {
		start := Run(item.Path, item.WorkItemID)
		if item.Path.starts() == startsDocument {
			start = DocumentRun(item.Path, item.WorkItemID)
		}
		now := decide(record, start)
		if now.MayStart() {
			changed = true
			continue
		}
		if now.Answer == MayNot && now.Why != item.Why {
			item.Why, changed = now.Why, true
		}
		kept = append(kept, item)
	}
	r.Items = kept
	r.Passes = slices.DeleteFunc(r.Passes, func(passes RefusedPasses) bool {
		allowed := decide(record, Pass(RecurringPass, passes.Role)).MayStart()
		changed = changed || allowed
		return allowed
	})
	return changed
}

// ReadRefusals reads a product's record. No file means nothing was refused.
func ReadRefusals(stateRoot string, product domain.ProductID) (Refusals, error) {
	return readRefusals(stateRoot, product, false)
}

// readRefusals is strict before a write, where a field stepped over is a field
// lost, and steps over one a newer build added everywhere else.
func readRefusals(stateRoot string, product domain.ProductID, strict bool) (Refusals, error) {
	if !filepath.IsAbs(stateRoot) {
		return Refusals{}, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(product)); err != nil {
		return Refusals{}, err
	}
	file, err := os.Open(filepath.Join(home.ProductDirectory(stateRoot, string(product)), refusalsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Refusals{SchemaVersion: RefusalsSchemaVersion, ProductID: product}, nil
	}
	if err != nil {
		return Refusals{}, fmt.Errorf("open the record of what was not started: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxRefusalsBytes+1))
	if err != nil {
		return Refusals{}, fmt.Errorf("read the record of what was not started: %w", err)
	}
	if len(encoded) > maxRefusalsBytes {
		return Refusals{}, fmt.Errorf("the record of what was not started is larger than %d bytes", maxRefusalsBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if strict {
		decoder.DisallowUnknownFields()
	}
	var record Refusals
	if err := decoder.Decode(&record); err != nil {
		return Refusals{}, fmt.Errorf("decode the record of what was not started: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Refusals{}, errors.New("decode the record of what was not started: something follows the record")
	}
	if err := record.Validate(); err != nil {
		return Refusals{}, fmt.Errorf("the record of what was not started: %w", err)
	}
	if record.ProductID != product {
		return Refusals{}, fmt.Errorf("the record of what was not started belongs to product %q, not %q", record.ProductID, product)
	}
	return record, nil
}

func writeRefusals(product *repowrite.PinnedRoot, record Refusals) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("the record of what was not started: %w", err)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the record of what was not started: %w", err)
	}
	if len(encoded) > maxRefusalsBytes {
		return fmt.Errorf("the record of what was not started would be larger than %d bytes", maxRefusalsBytes)
	}
	// Both sides of the change, as repository-writes-are-physically-confined asks.
	if err := product.Unchanged(); err != nil {
		return err
	}
	if err := product.WriteFile(refusalsFile, encoded, 0o600, false); err != nil {
		return fmt.Errorf("write the record of what was not started: %w", err)
	}
	return errors.Join(product.Unchanged(), product.Sync())
}
