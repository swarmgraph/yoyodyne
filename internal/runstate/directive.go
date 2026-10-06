package runstate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// DirectiveStore is where user directives live: in the same operating-system
// state root as runs and collected reports, product-scoped and beside both
// rather than inside either.
//
// That placement is the whole mechanism behind a directive applying regardless
// of which agent received it. A directive kept in the conversation that heard it
// would reach that conversation; one kept in a run's state would reach that run.
// Kept here, it is read by every process that acts on this product's work, and
// the run pipeline consults it before it starts a run, before it resumes one,
// and before it puts a change through the gate.
//
// It is a file per directive rather than an append-only log, which is the one
// way it differs from the reports beside it. A report is written once and never
// revised; a directive is revised by the acts that dispose of it — resolving one
// that paused work, which is what lets that work resume, recording what came of
// one that paused nothing, and the operator withdrawing one they no longer mean.
// Each of those is written at most once, and none of them removes anything the
// record already said.
type DirectiveStore struct {
	root      string
	productID domain.ProductID
}

func NewDirectiveStore(root string, productID domain.ProductID) (*DirectiveStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &DirectiveStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "directives"),
		productID: productID,
	}, nil
}

func (s *DirectiveStore) Root() string { return s.root }

// ErrNoDirective reports a reference that names nothing recorded, which is a
// plain answer rather than a failure to look.
var ErrNoDirective = errors.New("no directive is recorded under that reference")

// Record makes one directive durable. It is an exclusive create: a directive is
// recorded once, and two processes recording at the same moment must not be able
// to overwrite each other's.
func (s *DirectiveStore) Record(recorded directive.Directive) error {
	if err := s.validate(recorded); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create directive directory: %w", err)
	}
	path, err := s.path(recorded.ID)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("directive %s is already recorded", recorded.ID)
		}
		return fmt.Errorf("create directive: %w", err)
	}
	if err := writeJSONFile(file, "directive", recorded); err != nil {
		return cleanupFailedCreate(file, path, err)
	}
	if err := file.Close(); err != nil {
		return cleanupFailedCreate(nil, path, fmt.Errorf("close directive: %w", err))
	}
	if err := syncDirectory(s.root); err != nil {
		return cleanupFailedCreate(nil, path, err)
	}
	return nil
}

// List returns every recorded directive in the order it was received. A
// directory that does not exist yet is a product nobody has directed, which is
// not a failure to read.
//
// It is the tolerant door of the two in tolerantread.go, because its readers —
// the read model's lines, the reporting sink, a listing an operator asked for —
// say what the directives hold and write none of them back: a directive a newer
// build recorded is read without the fields this build does not know rather than
// stopping every one of those readers. What settles a directive, and what asks
// whether one pauses work, reads through the strict door instead.
func (s *DirectiveStore) List() ([]directive.Directive, error) {
	return s.list(true)
}

func (s *DirectiveStore) list(tolerateUnknownFields bool) ([]directive.Directive, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read directive directory: %w", err)
	}
	directives := make([]directive.Directive, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		loaded, err := s.load(strings.TrimSuffix(entry.Name(), ".json"), tolerateUnknownFields)
		if err != nil {
			return nil, err
		}
		directives = append(directives, loaded)
	}
	directive.Sort(directives)
	return directives, nil
}

// Pausing lists the in-force directives that pause one work item. It is the
// question the run pipeline asks, and it is answered from the durable records
// every time rather than from anything a process remembers: a directive recorded
// by one process while another is mid-run has to reach that run.
//
// It reads strictly: whether work may proceed is decided from what this returns,
// and a directive this build can only read part of is refused to the caller —
// which declines to run on it — rather than judged on the part it could read.
func (s *DirectiveStore) Pausing(workItemID string) ([]directive.Directive, error) {
	recorded, err := s.list(false)
	if err != nil {
		return nil, err
	}
	return directive.Pausing(recorded, workItemID), nil
}

// Load reads one directive by its full identifier, strictly: a field this build
// does not know refuses the record, for a caller that may write it back.
func (s *DirectiveStore) Load(id string) (directive.Directive, error) {
	return s.load(id, false)
}

func (s *DirectiveStore) load(id string, tolerateUnknownFields bool) (directive.Directive, error) {
	path, err := s.path(id)
	if err != nil {
		return directive.Directive{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return directive.Directive{}, fmt.Errorf("%w: %s", ErrNoDirective, id)
	}
	if err != nil {
		return directive.Directive{}, fmt.Errorf("open directive: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return directive.Directive{}, fmt.Errorf("read directive %s: %w", id, err)
	}
	var loaded directive.Directive
	if tolerateUnknownFields {
		unknown, err := decodeTolerating(encoded, &loaded)
		if err != nil {
			return directive.Directive{}, fmt.Errorf("decode directive %s: %w", id, err)
		}
		noteUnknownFields("directive record", unknown)
	} else if err := decodeStrictly(encoded, &loaded); err != nil {
		return directive.Directive{}, fmt.Errorf("decode directive %s: %w", id, err)
	}
	if loaded.ID != id {
		return directive.Directive{}, fmt.Errorf("directive file %s holds directive %s", id, loaded.ID)
	}
	if err := s.validate(loaded); err != nil {
		return directive.Directive{}, err
	}
	return loaded, nil
}

// Find resolves a reference an operator typed. A full identifier is matched
// exactly; anything shorter is matched as a prefix and is only an answer when it
// names exactly one directive. An identifier is thirty-two hex digits because
// every other identity here is, and nobody types thirty-two hex digits: an
// ambiguous prefix is reported as ambiguous rather than resolved to whichever
// directive happened to sort first.
//
// It reads strictly, because settling a directive finds it here and writes it
// back.
func (s *DirectiveStore) Find(reference string) (directive.Directive, error) {
	wanted := strings.TrimSpace(reference)
	if wanted == "" {
		return directive.Directive{}, errors.New("name the directive; a listing shows what is recorded")
	}
	recorded, err := s.list(false)
	if err != nil {
		return directive.Directive{}, err
	}
	var matched []directive.Directive
	for _, candidate := range recorded {
		if candidate.ID == wanted {
			return candidate, nil
		}
		if strings.HasPrefix(candidate.ID, wanted) {
			matched = append(matched, candidate)
		}
	}
	switch len(matched) {
	case 0:
		return directive.Directive{}, fmt.Errorf("%w: %s", ErrNoDirective, wanted)
	case 1:
		return matched[0], nil
	default:
		names := make([]string, 0, len(matched))
		for _, candidate := range matched {
			names = append(names, candidate.ID)
		}
		return directive.Directive{}, fmt.Errorf("%q names %d directives: %s", wanted, len(matched), strings.Join(names, ", "))
	}
}

// Resolve settles a directive that pauses work and returns what was recorded.
// Settling is what lifts the pause: the work it affected becomes runnable again
// on the next consultation, in whichever process makes it.
func (s *DirectiveStore) Resolve(reference, resolution string, at time.Time) (directive.Directive, error) {
	return s.settle(reference, func(found directive.Directive) (directive.Directive, error) {
		return found.Resolve(resolution, at)
	})
}

// CarryOut records what came of a directive that pauses nothing. Nothing is
// released by it — an operational directive held nothing up — so what it changes
// is what the record can answer: until it is written, the disposition of the
// commonest kind of directive exists only in whoever carried it out, and
// anything reading these records for what became of one finds an open directive
// forever.
func (s *DirectiveStore) CarryOut(reference, outcome string, at time.Time) (directive.Directive, error) {
	return s.settle(reference, func(found directive.Directive) (directive.Directive, error) {
		return found.CarryOut(outcome, at)
	})
}

// Withdraw takes a directive out of force on the operator's own instruction,
// recording who did it and when. It is the only act that ends a directive that
// pauses nothing: such a directive is in force from the moment it is recorded,
// and without this the store could only ever accumulate standing instructions —
// including ones that were never instructions, which is how a question read as a
// directive stays live and 'met' by every run forever.
//
// What it writes is a record that says the directive no longer applies, not one
// with the directive taken out of it. A run that was held or judged while it
// stood has to stay explicable afterwards, and that needs the words the operator
// used, which is exactly what deleting the file would throw away.
func (s *DirectiveStore) Withdraw(reference, by string, role domain.AgentRole, reason string, at time.Time) (directive.Directive, error) {
	return s.settle(reference, func(found directive.Directive) (directive.Directive, error) {
		return found.Withdraw(by, role, reason, at)
	})
}

// ResolveInto ends a directive by naming the document or work item that now
// carries it, recording who did it and why. It is the Lead Product Manager's act
// on a directive she carried somewhere; the record refuses one already ended.
func (s *DirectiveStore) ResolveInto(reference, became, by string, role domain.AgentRole, reason string, at time.Time) (directive.Directive, error) {
	return s.settle(reference, func(found directive.Directive) (directive.Directive, error) {
		return found.ResolveInto(became, by, role, reason, at)
	})
}

// settle is the one revision path: find what the reference names, let the record
// itself decide whether the act is one it can take, and replace it. Every caller
// goes through it so a revision is never written by one route that another would
// have refused.
func (s *DirectiveStore) settle(reference string, apply func(directive.Directive) (directive.Directive, error)) (directive.Directive, error) {
	found, err := s.Find(reference)
	if err != nil {
		return directive.Directive{}, err
	}
	settled, err := apply(found)
	if err != nil {
		return directive.Directive{}, err
	}
	if err := s.save(settled); err != nil {
		return directive.Directive{}, err
	}
	return settled, nil
}

// save replaces one recorded directive atomically. Every revision a directive
// takes comes through here, so it refuses to write a record for a directive that
// does not already exist rather than creating one by the back door.
func (s *DirectiveStore) save(recorded directive.Directive) error {
	if err := s.validate(recorded); err != nil {
		return err
	}
	path, err := s.path(recorded.ID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("load existing directive before save: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".directive-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary directive: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary directive: %w", err)
	}
	if err := writeJSONFile(temporary, "directive", recorded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary directive: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace directive: %w", err)
	}
	return syncDirectory(s.root)
}

func (s *DirectiveStore) validate(recorded directive.Directive) error {
	if recorded.ProductID != s.productID {
		return fmt.Errorf("directive product %q does not match store product %q", recorded.ProductID, s.productID)
	}
	return recorded.Validate()
}

// path names one directive's file. The identifier is checked against its own
// pattern first, so nothing that came from outside can name a path.
func (s *DirectiveStore) path(id string) (string, error) {
	if !directive.ValidID(id) {
		return "", fmt.Errorf("directive id %q is invalid", id)
	}
	return filepath.Join(s.root, id+".json"), nil
}
