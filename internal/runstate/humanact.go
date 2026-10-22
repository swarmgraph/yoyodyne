package runstate

// A person's recorded act, and the only thing that ever passes a human gate.
//
// Everything else the harness keeps is a record of something the harness did.
// This is the one record of something it cannot do: a named person saying they
// took a step, written by them at the command line. It exists because the
// alternative encoding — an item somebody closes — is one machinery can satisfy,
// and did, which is how a parity soak the operator had reserved for themselves
// was passed by a run closing the item that reserved it.
//
// An act is recorded against the thing that declared the gate — the work item,
// or the workflow instance — and it passes that gate there and nowhere else.
//
// The subject is the whole of what keeps a gate name honest. A name is a word
// somebody chose, and the useful words recur: `release-signed` and
// `soak-reviewed` describe a step taken once per release and once per soak, not
// once ever. If the name alone were the identity, the first recorded act would
// pass every later declaration of that word — the next release's sign-off would
// read satisfied with nobody having signed anything, and the operator could not
// even record the new act, because the gate would already be passed. That is the
// failure this whole mechanism exists to end, arriving through the namespace
// instead of through the tracker.
//
// It is what keeps the workflow half honest too, and more sharply: a definition
// gates a state by name, and every instance of that definition reaches the same
// state. One act against the name would pass that gate for every run the harness
// ever makes afterwards. Against the instance, each run's own step is its own.
//
// The reader already holds this rule inside one item's text — a name declared
// twice saying two different things is unreadable, because one act would pass
// both. The subject is that same rule across time and across work.
//
// Recording is exclusive per subject and gate. An act already recorded there is
// refused rather than overwritten: the first act is the one that passed it, and
// a second write would replace whose signature is on it with whoever typed last.
// A different subject is a different gate to pass, so it is not a second write.
//
// Nothing in the pipeline writes one. No registered action produces one, no
// closure implies one, and the store offers no verb that derives one — which is
// what "satisfiable only by a recorded human act" amounts to once it is code
// rather than a rule somebody remembers. `internal/cli` is the door, and a
// conformance test holds the store to having only that one.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
)

// HumanActSchemaVersion is 1 and has never changed.
const HumanActSchemaVersion = 1

const (
	// MaxHumanActPersonBytes bounds who recorded the act, and
	// MaxHumanActStatementBytes bounds what they say they did. Both are generous
	// for the name and the sentence each actually is, and small enough that
	// nothing can push a document into the record.
	MaxHumanActPersonBytes    = 200
	MaxHumanActStatementBytes = 2 << 10
	// MaxHumanActSubjectBytes bounds what the act was recorded against. It is a
	// work item or a workflow instance identifier and nothing longer.
	MaxHumanActSubjectBytes = 200
)

// humanActSuffix keeps a recorded act apart from the runs and the workflow
// instances it sits beside in the same directory.
const humanActSuffix = ".human-act.json"

// maxHumanActKeyPrefix bounds the readable half of an act's file name, for the
// reason the triage counters' does: what makes the directory legible is the
// rendering, and what makes it correct is the digest after it.
const maxHumanActKeyPrefix = 60

// HumanAct is one person's record that they took the step a gate reserved for
// them, against the thing that reserved it.
type HumanAct struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Subject is what declared the gate this act passes: the work item, or the
	// workflow instance. It is required, and see this file's own comment for why
	// it is what makes a gate name safe to reuse — an act passes the gate on its
	// subject and passes nothing anywhere else.
	Subject string `json:"subject"`
	// Gate is the gate this act passes, by the name the declaration gives it.
	Gate string `json:"gate"`
	// Person is who took the step. It is required, and it is the whole reason
	// this record is worth more than a flag: a gate passed by nobody in
	// particular is a gate passed by whatever wrote the flag.
	Person string `json:"person"`
	// Statement is what they say they did, in their own words. It is required
	// too, because an act with no account of it is a signature on a blank page.
	Statement  string    `json:"statement"`
	RecordedAt time.Time `json:"recorded_at"`
}

func (a HumanAct) Validate() error {
	var problems []error
	if a.SchemaVersion != HumanActSchemaVersion {
		problems = append(problems, fmt.Errorf("human act schema version %d is not supported", a.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(a.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if problem := humangate.NameProblem(a.Gate); problem != nil {
		problems = append(problems, problem)
	}
	if problem := humanActSubjectProblem(a.Subject); problem != nil {
		problems = append(problems, problem)
	}
	if strings.TrimSpace(a.Person) == "" {
		problems = append(problems, errors.New("the act names nobody; a gate is passed by a person and the record says which"))
	} else if len(a.Person) > MaxHumanActPersonBytes {
		problems = append(problems, fmt.Errorf("who recorded the act is %d bytes against a limit of %d", len(a.Person), MaxHumanActPersonBytes))
	}
	if strings.TrimSpace(a.Statement) == "" {
		problems = append(problems, errors.New("the act says nothing about what was done; an act nobody described is one nobody can check afterwards"))
	} else if len(a.Statement) > MaxHumanActStatementBytes {
		problems = append(problems, fmt.Errorf("what was done is %d bytes against a limit of %d", len(a.Statement), MaxHumanActStatementBytes))
	}
	if !utf8.ValidString(a.Person) || !utf8.ValidString(a.Statement) {
		problems = append(problems, errors.New("the act is not valid UTF-8"))
	}
	if a.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded at is required"))
	}
	return errors.Join(problems...)
}

// RecordHumanAct makes one person's act durable, refusing a gate already passed
// on that subject.
//
// The refusal is not tidiness. The record is who passed the gate, so a second
// write would replace one person's account with another's, and the gate would
// afterwards read as having been passed by whoever typed most recently. An
// operator who meant to correct one has to say so out loud, which is a
// conversation rather than a command.
//
// The same gate name on a different subject is not a second write. It is a
// different step, reserved by a different piece of work, and it is untaken until
// somebody takes it.
func (s *Store) RecordHumanAct(act HumanAct) error {
	if act.ProductID != s.productID {
		return fmt.Errorf("human act product %q does not match store product %q", act.ProductID, s.productID)
	}
	if err := act.Validate(); err != nil {
		return err
	}
	path, err := s.humanActPath(act.Subject, act.Gate)
	if err != nil {
		return err
	}
	if existing, recorded, err := s.HumanAct(act.Subject, act.Gate); err != nil {
		return err
	} else if recorded {
		return fmt.Errorf("the gate %q on %s was already passed by %s at %s; the act that passed it is the one on the record, and replacing it would change whose it was",
			act.Gate, act.Subject, existing.Person, existing.RecordedAt.UTC().Format(time.RFC3339))
	}
	return createJSONFile(s.state, path, "human act", act)
}

// HumanAct is the act that passed one gate on one subject, and whether one has
// been recorded at all. No record is the ordinary answer and means the gate still
// holds, which is why it is reported as an absence rather than as a failure to
// look. A record that cannot be read is neither: it is an error, because a gate
// nobody can read the act for must never be treated as passed.
func (s *Store) HumanAct(subject, gate string) (HumanAct, bool, error) {
	path, err := s.humanActPath(subject, gate)
	if err != nil {
		return HumanAct{}, false, err
	}
	act, err := readHumanAct(path)
	if errors.Is(err, os.ErrNotExist) {
		return HumanAct{}, false, nil
	}
	if err != nil {
		return HumanAct{}, false, err
	}
	// The file name is a rendering plus a digest, so a record whose own fields
	// disagree with what was asked for is one nothing here wrote.
	if act.Gate != gate || act.Subject != subject {
		return HumanAct{}, false, fmt.Errorf("the recorded act filed for the gate %q on %s is about %q on %s",
			gate, subject, act.Gate, act.Subject)
	}
	return act, true, nil
}

// HumanActRecorded reports whether the act that passes one gate on one subject
// is on the record. It is the whole of what something deciding whether to proceed
// needs, and deliberately the whole of what it is offered: who acted and what
// they said belong to whoever is reading the gate rather than to whatever is held
// by it.
func (s *Store) HumanActRecorded(subject, gate string) (bool, error) {
	_, recorded, err := s.HumanAct(subject, gate)
	return recorded, err
}

// HumanActs is every act recorded for this product, by subject and then by gate.
// It is what a queue and a status reading ask once rather than asking per gate,
// so that one reading of the backlog sees one set of recorded acts.
func (s *Store) HumanActs() ([]HumanAct, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the recorded human acts: %w", err)
	}
	var acts []HumanAct
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), humanActSuffix) {
			continue
		}
		act, err := readHumanAct(filepath.Join(s.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		acts = append(acts, act)
	}
	sort.Slice(acts, func(i, j int) bool {
		if acts[i].Subject != acts[j].Subject {
			return acts[i].Subject < acts[j].Subject
		}
		return acts[i].Gate < acts[j].Gate
	})
	return acts, nil
}

// DischargedGates is the gates a person has passed, by the subject they were
// passed on, each subject's names in sorted order.
//
// It is keyed by subject rather than flattened into one list because a flat list
// is the bug this record's subject exists to prevent: a queue given only names
// would read one recorded act as passing every later declaration of that word.
// What a gate says the person has to do belongs to whoever declared it; what this
// answers is only which of them have been done, and on what.
func (s *Store) DischargedGates() (map[string][]string, error) {
	acts, err := s.HumanActs()
	if err != nil {
		return nil, err
	}
	discharged := make(map[string][]string, len(acts))
	for _, act := range acts {
		discharged[act.Subject] = append(discharged[act.Subject], act.Gate)
	}
	return discharged, nil
}

// humanActSubjectProblem is what is wrong with what an act was recorded against.
// It is deliberately not held to the identifier pattern: a work item identifier
// is the tracker's to shape and carries dots, and what this needs is a bounded,
// single-line value that a key can be derived from.
func humanActSubjectProblem(subject string) error {
	switch {
	case strings.TrimSpace(subject) == "":
		return errors.New("the act says nothing about what it was recorded against; an act passes the gate on its subject, and one with no subject would pass every declaration of the name")
	case len(subject) > MaxHumanActSubjectBytes:
		return fmt.Errorf("what the act was recorded against is %d bytes against a limit of %d", len(subject), MaxHumanActSubjectBytes)
	case strings.ContainsAny(subject, "\n\r"), !utf8.ValidString(subject):
		return fmt.Errorf("%q is not something an act can be recorded against; a subject is one line naming a work item or a workflow instance", subject)
	default:
		return nil
	}
}

func readHumanAct(path string) (HumanAct, error) {
	file, err := os.Open(path)
	if err != nil {
		// The not-exist case is the ordinary one and is passed through unwrapped so
		// a caller can still recognize it.
		if errors.Is(err, os.ErrNotExist) {
			return HumanAct{}, err
		}
		return HumanAct{}, fmt.Errorf("open human act: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var act HumanAct
	if err := decoder.Decode(&act); err != nil {
		return HumanAct{}, fmt.Errorf("decode human act %s: %w", filepath.Base(path), err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return HumanAct{}, fmt.Errorf("decode human act %s: %w", filepath.Base(path), err)
	}
	if err := act.Validate(); err != nil {
		return HumanAct{}, fmt.Errorf("read human act %s: %w", filepath.Base(path), err)
	}
	return act, nil
}

// humanActPath names where one subject's act on one gate is recorded.
//
// The name is a bounded, lowercased rendering of the pair for whoever reads the
// directory, with a digest of the exact pair after it, which is the same shape
// the triage counters use and for the same reason: a work item identifier is not
// a file name, and two that render alike still need their own file. Deriving it
// rather than joining the values is also what keeps it a file in this directory
// rather than a path reaching out of it.
func (s *Store) humanActPath(subject, gate string) (string, error) {
	if problem := humangate.NameProblem(gate); problem != nil {
		return "", problem
	}
	if problem := humanActSubjectProblem(subject); problem != nil {
		return "", problem
	}
	return filepath.Join(s.root, humanActKey(subject, gate)+humanActSuffix), nil
}

// humanActKey renders one subject-and-gate pair into a file name. The separator
// inside the digested value cannot appear in either half, so no two pairs digest
// alike by running together.
func humanActKey(subject, gate string) string {
	digest := sha256.Sum256([]byte(subject + "\x00" + gate))
	var rendered strings.Builder
	for _, character := range strings.ToLower(subject + "-" + gate) {
		if rendered.Len() >= maxHumanActKeyPrefix {
			break
		}
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			rendered.WriteRune(character)
		default:
			rendered.WriteByte('-')
		}
	}
	return rendered.String() + "-" + hex.EncodeToString(digest[:8])
}
