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
)

// Refusals is what was not started because it is outside the epics the operator
// named, as the paths that asked Decide recorded it with Note. It decides
// nothing: it is kept so that status can say what was refused.
type Refusals struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Items is one entry for each work item, from its first refusal until it
	// may start.
	Items []RefusedItem `json:"items,omitempty"`
	// Passes is one entry for each role, from its first refusal until a pass of
	// that role may start.
	Passes []RefusedPasses `json:"passes,omitempty"`
}

// RefusedItem is a work item whose start was refused.
type RefusedItem struct {
	WorkItemID string    `json:"work_item_id"`
	Path       Path      `json:"path"`
	Since      time.Time `json:"since"`
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
		if !beads.ValidIssueID(item.WorkItemID) || item.Since.IsZero() {
			problems = append(problems, fmt.Errorf("the refusal of %q needs a work item identifier and when it was first refused", item.WorkItemID))
		}
	}
	for _, passes := range r.Passes {
		if passes.Role == "" || passes.Count < 1 || passes.Since.IsZero() {
			problems = append(problems, fmt.Errorf("the refused passes of %q need a role, a count and when the first was refused", passes.Role))
		}
	}
	return errors.Join(problems...)
}

// Note records what Decide answered about a start. A refusal is added, and a
// start that may go ahead takes away the refusal it ends; anything else writes
// nothing. A path calls it once for each pass that came due, because each call
// counts one; an item refused again is already recorded.
//
// The review of unmet goals and the reading of outside messages are not role
// passes and name no work item, so nothing is kept about them.
func Note(stateRoot string, product domain.ProductID, start Start, decision Decision, at time.Time) error {
	if decision.Answer != May && decision.Answer != MayNot {
		return nil
	}
	if decision.Answer == May {
		// Almost every start is allowed with nothing to take away, so the lock is
		// not taken to find that out.
		current, err := ReadRefusals(stateRoot, product)
		if err != nil || !current.note(start, decision, at) {
			return err
		}
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
	current, err := readRefusals(root.Path(), product, true)
	if err != nil {
		return err
	}
	if !current.note(start, decision, at) {
		return nil
	}
	return writeRefusals(pinned, current)
}

// note applies one answer, and reports whether it changed anything.
func (r *Refusals) note(start Start, decision Decision, at time.Time) bool {
	refused := decision.Answer == MayNot
	changed := false
	switch start.what {
	case workItemRun, documentRun:
		for _, id := range start.items {
			index := slices.IndexFunc(r.Items, func(item RefusedItem) bool { return item.WorkItemID == id })
			switch {
			case refused && index < 0 && beads.ValidIssueID(id):
				r.Items = append(r.Items, RefusedItem{WorkItemID: id, Path: start.path, Since: at.UTC()})
				changed = true
			// A document may start for one of the items it answers, which says
			// nothing about a run of the others.
			case !refused && index >= 0 && (start.what == workItemRun || r.Items[index].Path == start.path):
				r.Items = slices.Delete(r.Items, index, index+1)
				changed = true
			}
		}
	case rolePass:
		index := slices.IndexFunc(r.Passes, func(passes RefusedPasses) bool { return passes.Role == start.role })
		switch {
		case refused && index < 0:
			r.Passes = append(r.Passes, RefusedPasses{Role: start.role, Count: 1, Since: at.UTC()})
			changed = true
		case refused:
			r.Passes[index].Count++
			changed = true
		case index >= 0:
			r.Passes = slices.Delete(r.Passes, index, index+1)
			changed = true
		}
	}
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
