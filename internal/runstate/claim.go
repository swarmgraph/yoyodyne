package runstate

// A claim the harness gave back, written down so that it survives the pass that
// gave it back.
//
// A claim is the tracker's record that something is working on an item, and the
// harness makes one at the start of every run. Nothing gives it back when the
// process holding it dies: the run's own record stops moving, the item stays
// claimed, and the scheduler — which chooses only from work the tracker calls
// ready — passes over it forever. Four nights of throughput went that way in the
// week of 2026-09-01, and what made it expensive was not the stuck item but the
// silence: an idle line that looks busy is the one failure every surface here
// reports as the harness working.
//
// So the audit that gives the claim back records having done it, and it has to
// be a record rather than a message for the two reasons the stall log is one. It
// is the dedup: a claim is released once, and a tracker that has not caught up
// must not produce a second release and a second message. And it is the history:
// an item that was started twice with nothing between the two runs is otherwise
// a mystery, and the release is the whole of the answer.
//
// It is one append-only log per product. Nothing here is ever rewritten: a
// release happened or it did not, and two processes auditing at once must not
// overwrite each other's record.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// ReleasedClaimSchemaVersion is 1 and has never changed.
const ReleasedClaimSchemaVersion = 1

// MaxReleasedClaimDetailBytes bounds the sentence a release keeps about the run
// that left the claim behind. It is the harness's own words about itself rather
// than a provider's, so it takes the bound a stall record's detail takes.
const MaxReleasedClaimDetailBytes = 4 << 10

// maxEncodedReleasedClaimBytes bounds one encoded record, including the trailing
// newline. The writer and the reader share it, so a record that was written is
// always one that can be read back.
const maxEncodedReleasedClaimBytes = 16 << 10

// ReleasedClaim is one claim the harness audited against the runs it actually
// has, found nothing alive behind, and gave back to the queue.
type ReleasedClaim struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	WorkItemID    string           `json:"work_item_id"`
	// WorkItemTitle is what the item is called, copied here for the reason a run
	// copies it: everything that reads this afterwards reads only this, and a
	// release nobody can name the work of is one nobody will look at.
	WorkItemTitle string `json:"work_item_title,omitempty"`
	// RunID is the run whose death left the claim behind.
	RunID string `json:"run_id,omitempty"`
	// Since is the last moment that run recorded anything, which is what the age
	// of the dead claim is measured from. It is kept apart from ReleasedAt because
	// the two answer different questions: one is how long the item sat unworked,
	// and the other is how long it took anybody to notice.
	Since time.Time `json:"since,omitempty"`
	// Because is what the record said about the run that left the claim — that it
	// ended, or that it stopped saying anything. It is the one field that tells a
	// run the harness finished from a process somebody killed, which is the first
	// thing whoever reads this has to decide between.
	Because    string    `json:"because,omitempty"`
	ReleasedAt time.Time `json:"released_at"`
	// Found is what the repository held of that run's branch and worktree when
	// the claim was given back, looked for rather than read off the run's
	// removal flags. The release says it on the item, because a release that
	// said only that nothing was working on the item was read on 2026-09-23 as
	// run-838ffc48 having preserved nothing while its branch held the approved
	// change. A record written before this existed carries none, which is what
	// the convergence sweep's correction reads.
	Found *triage.Found `json:"found,omitempty"`
}

func (c ReleasedClaim) Validate() error {
	var problems []error
	if c.SchemaVersion != ReleasedClaimSchemaVersion {
		problems = append(problems, fmt.Errorf("released claim schema version %d is not supported", c.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(c.WorkItemID) == "" {
		problems = append(problems, errors.New("work_item_id is required"))
	}
	if c.ReleasedAt.IsZero() {
		problems = append(problems, errors.New("released_at is required"))
	}
	if len(c.Because) > MaxReleasedClaimDetailBytes {
		problems = append(problems, fmt.Errorf("because is %d bytes, which exceeds the %d byte bound", len(c.Because), MaxReleasedClaimDetailBytes))
	}
	if c.Found != nil {
		if err := c.Found.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("found: %w", err))
		}
	}
	return errors.Join(problems...)
}

// ClaimStore is where the claims one product's harness gave back are collected,
// in the same operating-system state root as the runs and the stalls and beside
// them.
type ClaimStore struct {
	root      string
	productID domain.ProductID
}

func NewClaimStore(root string, productID domain.ProductID) (*ClaimStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &ClaimStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *ClaimStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the account of the
// released claims actually is.
func (s *ClaimStore) Path() string { return filepath.Join(s.root, "released-claims.jsonl") }

// Append records one release durably.
func (s *ClaimStore) Append(released ReleasedClaim) error {
	released.Because = boundedClaimDetail(released.Because, MaxReleasedClaimDetailBytes)
	if err := s.validate(released); err != nil {
		return err
	}
	encoded, err := encodeReleasedClaim(released)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedReleasedClaimBytes {
		return fmt.Errorf("encoded released claim is %d bytes, limit is %d", len(encoded), maxEncodedReleasedClaimBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create released claim directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect released claim log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open released claim log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append released claim: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append released claim: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync released claim log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close released claim log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded release in the order it was recorded. A log that
// does not exist yet is a product no claim has ever been given back on, which is
// not a failure to read. A line that will not decode is, for the reason it is on
// the reports pile: a listing that quietly dropped a release is one nobody can
// trust to be complete. The sink reads past such a line by position with Scan,
// and says so.
func (s *ClaimStore) List() ([]ReleasedClaim, error) {
	released, skipped, err := s.Scan()
	if err != nil {
		return nil, err
	}
	if err := firstSkipped("released claim log", skipped); err != nil {
		return nil, err
	}
	return released, nil
}

// Scan returns every release that decoded, in the order it was recorded, and
// beside them the lines that would not, each at the position it holds among the
// records. It is the read a positional cursor is kept against, so one bad line
// costs the reader that line and nothing behind it.
func (s *ClaimStore) Scan() ([]ReleasedClaim, []SkippedLine, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open released claim log: %w", err)
	}
	defer file.Close()

	var released []ReleasedClaim
	skipped, err := scanLog(file, maxEncodedReleasedClaimBytes, func(line []byte) error {
		decoded, err := decodeReleasedClaim(line)
		if err != nil {
			return err
		}
		if err := s.validate(decoded); err != nil {
			return err
		}
		released = append(released, decoded)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read released claim log: %w", err)
	}
	return released, skipped, nil
}

func decodeReleasedClaim(data []byte) (ReleasedClaim, error) {
	var decoded ReleasedClaim
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ReleasedClaim{}, err
	}
	if err := decoded.Validate(); err != nil {
		return ReleasedClaim{}, err
	}
	return decoded, nil
}

func encodeReleasedClaim(released ReleasedClaim) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(released); err != nil {
		return nil, fmt.Errorf("encode released claim: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *ClaimStore) validate(released ReleasedClaim) error {
	if released.ProductID != s.productID {
		return fmt.Errorf("released claim product %q does not match store product %q", released.ProductID, s.productID)
	}
	return released.Validate()
}

// boundedClaimDetail folds the harness's own sentence about a dead run to a line
// and cuts it to the bound, so a record that was assembled is always one that can
// be written. It cuts on a rune boundary: a sentence truncated mid-rune is not
// text.
func boundedClaimDetail(text string, limit int) string {
	return oneline.Bound(text, limit)
}
