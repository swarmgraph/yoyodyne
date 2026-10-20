package runstate

// One workflow instance's position in the graph it is running.
//
// A run record says what the harness is doing to a work item — the worktree, the
// branch, the publication, the verdicts. This says where a compiled workflow
// instance stands: which definition it is pinned to, which state it is in, and
// every boundary it has crossed to get there. They are apart because they are
// facts about different things, and they are in the same store for the reason
// every other record here is: the harness has one durable state root, and a
// record that lives anywhere else is a record a second writer has to keep safe.
//
// The record is the position rather than a copy of one held in memory. An
// executor reloads it before every transition and writes it before beginning the
// next, so a process that dies leaves behind the answer to where its successor
// starts — and that answer is a state boundary, never the middle of an action.
//
// It is pinned to a digest from creation to terminal, which is the whole of what
// "in-flight work is never silently migrated" amounts to: the definition file may
// be edited, replaced, or deleted, and this record still names the one thing this
// instance is running.
//
// What is checked here is the shape a record may hold, held to this package's
// own patterns rather than to the workflow package's constants, for the reason
// the run record is: the durable schema stays independent of the code that
// produces what it stores, so a record is checked against what a record may hold
// rather than against what this version of the harness happens to write.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// WorkflowInstanceSchemaVersion is 1 and has never changed.
const WorkflowInstanceSchemaVersion = 1

// workflowDigestPattern holds a pin to being a workflow content digest, which is
// the only thing it ever is: a definition digests to a prefixed hash, and a field
// that could carry anything is a field an instance could be pinned to a name by.
var workflowDigestPattern = regexp.MustCompile(`^wf-[a-f0-9]{64}$`)

// WorkflowInstance is one run of one compiled workflow graph, durably.
type WorkflowInstance struct {
	SchemaVersion int `json:"schema_version"`
	// InstanceID is what this instance is recorded and resumed under, and the name
	// its file is stored beside the runs under.
	InstanceID string `json:"instance_id"`
	// WorkflowID is the workflow being run, by the name a binding selects it
	// under. It is here beside the digest because the pair is what says what an
	// instance is running and which version of it.
	WorkflowID string `json:"workflow_id"`
	// DefinitionSchema is the workflow format version the pinned definition was
	// read under, which is not this record's own schema version above.
	DefinitionSchema int `json:"definition_schema"`
	// Digest is the content address of the definition this instance is pinned to.
	// It is written once, at creation, and never changes.
	Digest string `json:"digest"`
	// State is where the instance stands: the state whose action is performed
	// next, or the terminal it ended in.
	State string `json:"state"`
	// Terminal is whether State is a terminal, in which case the instance is over
	// and nothing further is performed.
	Terminal bool `json:"terminal"`
	// Checkpoints is every boundary this instance has crossed, oldest first,
	// beginning with the initial state it was created in. It is history rather
	// than a log: each entry is a position that was durable, so reading them in
	// order is reading exactly the path the instance took.
	Checkpoints []WorkflowCheckpoint `json:"checkpoints"`
}

// WorkflowCheckpoint is one state boundary: where the instance arrived, what
// brought it there, and when.
type WorkflowCheckpoint struct {
	// Sequence is this checkpoint's position in the history, counting from zero at
	// the initial state.
	Sequence int `json:"sequence"`
	// State is where the instance stood once this checkpoint was durable.
	State string `json:"state"`
	// Terminal is whether that state is a terminal.
	Terminal bool `json:"terminal,omitempty"`
	// From is the state whose action produced the transition into this one, and is
	// absent on the initial checkpoint, which nothing transitioned into.
	From string `json:"from,omitempty"`
	// Outcome is what the action of From produced, and is absent for the same
	// reason From is.
	Outcome string `json:"outcome,omitempty"`
	// At is when this checkpoint was recorded.
	At time.Time `json:"at"`
}

// checkpointTimestamp is how a checkpoint's time is written: RFC 3339 in UTC
// with the fractional second always nine digits, so one checkpoint costs the
// same number of bytes as the next whatever the clock behind it produced.
//
// Go's own encoding trims trailing zeros off the fraction, which makes an
// instant cost anywhere between twenty and thirty bytes depending on where the
// clock happened to land. That is not cosmetic here. RoomForAnotherCheckpoint
// is asked before the action that produces the checkpoint, with a timestamp
// taken then, and the checkpoint eventually recorded carries a later one: a
// width that moves with the clock makes "the widest boundary this step could
// produce" a claim that can be ten bytes short of what is written. Padding the
// fraction is a narrowing of what this writes rather than of what can be read,
// so a record written by earlier code still reads back the same way.
const checkpointTimestamp = "2006-01-02T15:04:05.000000000Z"

// MarshalJSON writes the checkpoint with that fixed-width timestamp and changes
// nothing else about it. The rest of the record is encoded from the struct
// itself rather than restated here, so a field added above is carried without
// this method being remembered.
func (c WorkflowCheckpoint) MarshalJSON() ([]byte, error) {
	type checkpoint WorkflowCheckpoint // without this method, so encoding it is not recursive
	return json.Marshal(struct {
		checkpoint
		At string `json:"at"`
	}{checkpoint(c), c.At.UTC().Format(checkpointTimestamp)})
}

// Done reports whether this instance has reached a terminal, which is the one
// question a caller asks before deciding whether to step it again.
func (i WorkflowInstance) Done() bool { return i.Terminal }

// Position is the checkpoint the instance currently stands on: the last one
// recorded. A record with no checkpoints is a record nothing created here, so
// the caller is told so rather than handed a zero checkpoint.
func (i WorkflowInstance) Position() (WorkflowCheckpoint, bool) {
	if len(i.Checkpoints) == 0 {
		return WorkflowCheckpoint{}, false
	}
	return i.Checkpoints[len(i.Checkpoints)-1], true
}

// Path is every state and terminal this instance has stood in, in the order it
// stood in them. It is what somebody asking what an instance did reads, and a
// state it went round a loop into appears once per visit.
func (i WorkflowInstance) Path() []string {
	walked := make([]string, 0, len(i.Checkpoints))
	for _, checkpoint := range i.Checkpoints {
		walked = append(walked, checkpoint.State)
	}
	return walked
}

// RoomForAnotherCheckpoint reports whether one more boundary would still fit
// inside the bound every record here is held to.
//
// It exists to be asked before the step that would produce that checkpoint
// rather than after. A record is bounded and a history only grows, so an
// instance whose action has already been performed and whose record will not
// take the result is one nothing can move: every later attempt performs the same
// action again and fails to record it again. Asked first, the same limit is a
// refusal that has cost nothing and that names what is wrong.
func (i WorkflowInstance) RoomForAnotherCheckpoint(next WorkflowCheckpoint) error {
	trial := i
	trial.Checkpoints = append(slices.Clone(i.Checkpoints), next)
	encoded, err := encodeRecord("workflow instance", trial)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedStateBytes {
		return fmt.Errorf("workflow instance %s has recorded %d checkpoints, and one more would make its record %d bytes against a limit of %d; it cannot be stepped any further",
			i.InstanceID, len(i.Checkpoints), len(encoded), maxEncodedStateBytes)
	}
	return nil
}

// Validate refuses a record that could not have been written by an executor.
//
// It is checked on the way out as well as on the way in, because a record read
// back is a record something is about to act on: an instance whose history
// disagrees with its position would resume somewhere neither of them says.
func (i WorkflowInstance) Validate() error {
	var problems []error
	if i.SchemaVersion != WorkflowInstanceSchemaVersion {
		problems = append(problems, fmt.Errorf("workflow instance schema version %d is not supported", i.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("workflow instance id", i.InstanceID); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(i.WorkflowID) == "" {
		problems = append(problems, errors.New("the instance names no workflow; a record has to say what it is running"))
	}
	if i.DefinitionSchema < 1 {
		problems = append(problems, fmt.Errorf("the instance was pinned to a definition of schema version %d; a definition declares which version it was written against", i.DefinitionSchema))
	}
	if !workflowDigestPattern.MatchString(i.Digest) {
		problems = append(problems, fmt.Errorf("the instance is pinned to %q, which is not a workflow digest; an instance pinned to nothing is one any definition could claim", i.Digest))
	}
	if strings.TrimSpace(i.State) == "" {
		problems = append(problems, errors.New("the instance stands in no state; a record has to say where it is"))
	}
	if len(i.Checkpoints) == 0 {
		problems = append(problems, errors.New("the instance has no checkpoints; every instance is created standing on its initial state"))
		return errors.Join(problems...)
	}
	for index, checkpoint := range i.Checkpoints {
		if checkpoint.Sequence != index {
			problems = append(problems, fmt.Errorf("the checkpoint at position %d is numbered %d; checkpoints are the boundaries crossed, in the order they were crossed", index, checkpoint.Sequence))
		}
		if strings.TrimSpace(checkpoint.State) == "" {
			problems = append(problems, fmt.Errorf("the checkpoint at position %d stands in no state", index))
		}
		if index == 0 && (checkpoint.From != "" || checkpoint.Outcome != "") {
			problems = append(problems, fmt.Errorf("the initial checkpoint arrived from %q on %q; nothing transitioned into it", checkpoint.From, checkpoint.Outcome))
		}
		if index > 0 && (checkpoint.From == "" || checkpoint.Outcome == "") {
			problems = append(problems, fmt.Errorf("the checkpoint at position %d does not say what state and what outcome brought the instance there", index))
		}
		if index > 0 && checkpoint.From != i.Checkpoints[index-1].State {
			problems = append(problems, fmt.Errorf("the checkpoint at position %d arrived from %q and the one before it stood in %q; the history skips a boundary", index, checkpoint.From, i.Checkpoints[index-1].State))
		}
	}
	// The position and the history are two statements of one fact, and a record
	// whose two disagree is one nothing can safely resume.
	last := i.Checkpoints[len(i.Checkpoints)-1]
	if last.State != i.State || last.Terminal != i.Terminal {
		problems = append(problems, fmt.Errorf("the instance stands in %q (terminal %t) and its last checkpoint stands in %q (terminal %t)", i.State, i.Terminal, last.State, last.Terminal))
	}
	return errors.Join(problems...)
}

// CreateWorkflowInstance records an instance that does not exist yet, refusing
// one that does. It is exclusive because two instances under one identifier are
// two positions in one workflow, and a resume would not say which of them it
// meant.
func (s *Store) CreateWorkflowInstance(instance WorkflowInstance) error {
	if err := instance.Validate(); err != nil {
		return fmt.Errorf("create workflow instance: %w", err)
	}
	path, err := s.workflowInstancePath(instance.InstanceID)
	if err != nil {
		return err
	}
	return createJSONFile(s.state, path, "workflow instance", instance)
}

// SaveWorkflowInstance replaces the record of an instance, which is what makes
// one boundary durable before the next is begun.
func (s *Store) SaveWorkflowInstance(instance WorkflowInstance) error {
	if err := instance.Validate(); err != nil {
		return fmt.Errorf("save workflow instance: %w", err)
	}
	path, err := s.workflowInstancePath(instance.InstanceID)
	if err != nil {
		return err
	}
	return replaceJSONFile(s.state, path, "workflow instance", instance)
}

// LoadWorkflowInstance reads one record back, refusing anything this build does
// not recognize: an unknown field is a record written by different code, and
// reading it as though the field were not there is resuming an instance whose
// state was partly ignored.
//
// It is the strict door of the two in tolerantread.go, and there is no tolerant
// one beside it: an instance is only ever read to be advanced and saved back, so
// every read of it is a read that precedes a write. The refusal reaches whoever
// asked to resume the instance as an error naming the record, which is the one
// answer that cannot be mistaken for an instance with nothing in it.
func (s *Store) LoadWorkflowInstance(instanceID string) (WorkflowInstance, error) {
	path, err := s.workflowInstancePath(instanceID)
	if err != nil {
		return WorkflowInstance{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return WorkflowInstance{}, fmt.Errorf("open workflow instance %s: %w", instanceID, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var instance WorkflowInstance
	if err := decoder.Decode(&instance); err != nil {
		return WorkflowInstance{}, fmt.Errorf("decode workflow instance %s: %w", instanceID, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return WorkflowInstance{}, fmt.Errorf("decode workflow instance %s: %w", instanceID, err)
	}
	if instance.InstanceID != instanceID {
		return WorkflowInstance{}, fmt.Errorf("the workflow instance file for %s holds the instance %s", instanceID, instance.InstanceID)
	}
	if err := instance.Validate(); err != nil {
		return WorkflowInstance{}, fmt.Errorf("read workflow instance %s: %w", instanceID, err)
	}
	return instance, nil
}

// workflowInstancePath names where one instance is recorded. The suffix keeps it
// apart from the runs it sits beside, the way an operator's release of a wait is
// kept apart from the run it is about, and the identifier is held to the shape
// every identifier in this harness has — which is also what keeps it a file name
// in this directory rather than a path reaching out of it.
func (s *Store) workflowInstancePath(instanceID string) (string, error) {
	if err := domain.ValidateIdentifier("workflow instance id", instanceID); err != nil {
		return "", err
	}
	return filepath.Join(s.root, instanceID+".instance.json"), nil
}
