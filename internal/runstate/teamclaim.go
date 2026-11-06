package runstate

// What this machine knows about the claims it holds on the shared remote, in
// team mode (docs/designs/working-alongside-other-contributors.md, section 5).
//
// The claim itself is a Git reference on the remote, and the remote is the
// authority on who holds an item. What is kept here is what this machine needs
// to go on holding one: which run took it, the commit it last pushed (which
// every renewal and the release name in their lease), and when it last renewed.
// It is kept apart from the run's own record because a live run saves its
// record from memory, and a renewal written into that record by the watch loop
// would be overwritten with a commit the remote no longer holds.
//
// One file per item, replaced whole, and every read-act-write on one is made
// under that item's lock (TeamClaimStore.Lock), so a renewal and a fresh run's
// take cannot interleave.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// TeamClaimSchemaVersion is 1 and has never changed.
const TeamClaimSchemaVersion = 1

const teamClaimsDirectory = "team-claims"

// TeamClaim is one claim this machine took on the shared remote.
type TeamClaim struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	WorkItemID    string           `json:"work_item_id"`
	// RunID is the run the claim was taken for.
	RunID string `json:"run_id"`
	// Factory is who this machine named itself as in the claim.
	Factory string `json:"factory"`
	// Commit is the claim commit this machine last pushed to the remote.
	Commit    string    `json:"commit"`
	TakenAt   time.Time `json:"taken_at"`
	RenewedAt time.Time `json:"renewed_at"`
	// PreviousHolder names the factory whose claim this one took over after it
	// went unrenewed for two hours, and is empty for a claim taken fresh.
	PreviousHolder string `json:"previous_holder,omitempty"`
	// RenewalProblem is why the last renewal did not reach the remote. It is
	// cleared by the next renewal that does, and costs the run nothing.
	RenewalProblem string `json:"renewal_problem,omitempty"`
	// ReleasedAt is when this machine stopped holding the claim: its release
	// reached the remote, or the reference was found to be somebody else's.
	ReleasedAt *time.Time `json:"released_at,omitempty"`
	// Lost says the reference was no longer this machine's when it renewed or
	// released it, and whose it was then. Such a reference is left alone.
	Lost string `json:"lost,omitempty"`
}

// Held reports a claim this machine still holds and still has to renew or
// release.
func (c TeamClaim) Held() bool { return c.ReleasedAt == nil }

func (c TeamClaim) Validate() error {
	var problems []error
	if c.SchemaVersion != TeamClaimSchemaVersion {
		problems = append(problems, fmt.Errorf("team claim schema version %d is not supported", c.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := teamClaimName(c.WorkItemID); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(c.RunID) == "" {
		problems = append(problems, errors.New("run_id is required"))
	}
	if strings.TrimSpace(c.Commit) == "" {
		problems = append(problems, errors.New("commit is required"))
	}
	if c.TakenAt.IsZero() || c.RenewedAt.IsZero() {
		problems = append(problems, errors.New("taken_at and renewed_at are required"))
	}
	return errors.Join(problems...)
}

func teamClaimName(workItemID string) error {
	if strings.TrimSpace(workItemID) == "" || strings.ContainsAny(workItemID, `/\`) || strings.HasPrefix(workItemID, ".") {
		return fmt.Errorf("work item %q cannot name a team claim record", workItemID)
	}
	return nil
}

// TeamClaimStore keeps the claims one product's harness holds on the shared
// remote, in the product's directory under the operating-system state root.
type TeamClaimStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
}

func NewTeamClaimStore(root string, productID domain.ProductID) (*TeamClaimStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the team claims' state root: %w", err)
	}
	return &TeamClaimStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

// directory is the claims' directory relative to the state root.
func (s *TeamClaimStore) directory() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), teamClaimsDirectory)
}

// Directory is where the claims are kept, so a failure can say where.
func (s *TeamClaimStore) Directory() string { return filepath.Join(s.stateRoot, s.directory()) }

// Lock holds one item's claim record against every other writer on this
// machine, until the returned function is called.
func (s *TeamClaimStore) Lock(ctx context.Context, workItemID string) (func(), error) {
	if err := teamClaimName(workItemID); err != nil {
		return nil, err
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, fmt.Errorf("pin the team claims' state root: %w", err)
	}
	defer root.Close()
	if err := root.MakeDirectory(s.directory(), 0o700); err != nil {
		return nil, err
	}
	lock, err := root.OpenLock(filepath.Join(s.directory(), "."+workItemID+".lock"), 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockStateFile(ctx, lock); err != nil {
		closeStateFile(lock)
		return nil, fmt.Errorf("wait for the claim record of %s: %w", workItemID, err)
	}
	return func() { unlockStateFile(lock); closeStateFile(lock) }, nil
}

// Load reads one item's claim record, and false where this machine never
// claimed the item.
func (s *TeamClaimStore) Load(workItemID string) (TeamClaim, bool, error) {
	if err := teamClaimName(workItemID); err != nil {
		return TeamClaim{}, false, err
	}
	encoded, err := os.ReadFile(filepath.Join(s.Directory(), workItemID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return TeamClaim{}, false, nil
	}
	if err != nil {
		return TeamClaim{}, false, err
	}
	var claim TeamClaim
	if err := decodeStrictly(encoded, &claim); err != nil {
		return TeamClaim{}, false, fmt.Errorf("decode the claim record of %s: %w", workItemID, err)
	}
	return claim, true, nil
}

// Save replaces one item's claim record.
func (s *TeamClaimStore) Save(claim TeamClaim) error {
	claim.SchemaVersion = TeamClaimSchemaVersion
	claim.ProductID = s.productID
	if err := claim.Validate(); err != nil {
		return err
	}
	encoded, err := encodeRecord("team claim", claim)
	if err != nil {
		return err
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return fmt.Errorf("pin the team claims' state root: %w", err)
	}
	defer root.Close()
	if err := root.Unchanged(); err != nil {
		return err
	}
	if err := root.WriteFile(filepath.Join(s.directory(), claim.WorkItemID+".json"), encoded, 0o600, false); err != nil {
		return fmt.Errorf("record the claim on %s: %w", claim.WorkItemID, err)
	}
	return nil
}

// List reads every claim record this machine keeps.
func (s *TeamClaimStore) List() ([]TeamClaim, error) {
	entries, err := os.ReadDir(s.Directory())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var claims []TeamClaim
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		claim, found, err := s.Load(strings.TrimSuffix(name, ".json"))
		if err != nil {
			return nil, err
		}
		if found {
			claims = append(claims, claim)
		}
	}
	return claims, nil
}
