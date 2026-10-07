package runstate

import (
	"crypto/rand"
	"encoding/hex"
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
	"github.com/mason-bryant/yoyodyne/internal/home"
)

const RoleActivationSchemaVersion = 1

const roleActivationsDirectory = "role-activations"

// RoleActivation records a person's decision about one exact definition. It
// supplies no authority by itself: an agent binding must still enforce it.
type RoleActivation struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Digest        string           `json:"digest"`
	Source        string           `json:"source"`
	Person        string           `json:"person"`
	ActivatedAt   time.Time        `json:"activated_at"`
}

func (a RoleActivation) Validate() error {
	if a.SchemaVersion != RoleActivationSchemaVersion {
		return fmt.Errorf("role activation schema version %d is not supported", a.SchemaVersion)
	}
	if err := domain.ValidateIdentifier("product id", string(a.ProductID)); err != nil {
		return err
	}
	if err := domain.ValidateIdentifier("role definition name", a.Name); err != nil {
		return err
	}
	if !lowerHex(a.ID, 32) || !lowerHex(a.Digest, 64) {
		return errors.New("role activation requires a 32-character hexadecimal id and a SHA-256 content digest")
	}
	if !filepath.IsAbs(a.Source) || len(a.Source) > 4096 {
		return errors.New("role activation source must be an absolute path of at most 4096 bytes")
	}
	if strings.TrimSpace(a.Person) == "" || len(a.Person) > MaxHumanActPersonBytes || !utf8.ValidString(a.Person) {
		return fmt.Errorf("role activation must name a person in at most %d bytes of UTF-8", MaxHumanActPersonBytes)
	}
	if a.ActivatedAt.IsZero() {
		return errors.New("role activation time is required")
	}
	return nil
}

func lowerHex(value string, length int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(value) == length && len(decoded)*2 == length && strings.ToLower(value) == value
}

// RoleActivationStore retains every activation as an immutable, atomically
// published record. Concurrent activations never overwrite each other.
type RoleActivationStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
}

func NewRoleActivationStore(root string, productID domain.ProductID) (*RoleActivationStore, error) {
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the role activation state root: %w", err)
	}
	return &RoleActivationStore{stateRoot: stateRoot, anchor: anchor, productID: productID}, nil
}

func (s *RoleActivationStore) directory() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), roleActivationsDirectory)
}

func (s *RoleActivationStore) Root() string { return filepath.Join(s.stateRoot, s.directory()) }

// RecordRoleActivation is called only by the person's CLI verb, after the definition loads.
// No workflow action or conversation tool records an activation.
func (s *RoleActivationStore) RecordRoleActivation(name, digest, source, person string) (RoleActivation, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return RoleActivation{}, err
	}
	activation := RoleActivation{
		SchemaVersion: RoleActivationSchemaVersion, ProductID: s.productID,
		ID: hex.EncodeToString(id[:]), Name: name, Digest: digest, Source: source,
		Person: strings.TrimSpace(person), ActivatedAt: time.Now().UTC(),
	}
	if err := activation.Validate(); err != nil {
		return RoleActivation{}, err
	}
	encoded, err := encodeRecord("role activation", activation)
	if err != nil {
		return RoleActivation{}, err
	}
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return RoleActivation{}, fmt.Errorf("pin the role activation state root: %w", err)
	}
	defer root.Close()
	if err := root.MakeDirectory(s.directory(), 0o700); err != nil {
		return RoleActivation{}, fmt.Errorf("create role activation directory: %w", err)
	}
	if err := root.Unchanged(); err != nil {
		return RoleActivation{}, err
	}
	if err := root.CreateFile(filepath.Join(s.directory(), activation.ID+".json"), encoded, 0o600); err != nil {
		return RoleActivation{}, fmt.Errorf("record role activation: %w", err)
	}
	if err := root.Unchanged(); err != nil {
		return RoleActivation{}, err
	}
	return activation, nil
}

// History reads every activation, newest first. Missing state is an empty
// history; unreadable state is an error, never an unactivated definition.
func (s *RoleActivationStore) History() ([]RoleActivation, error) {
	entries, err := os.ReadDir(s.Root())
	if errors.Is(err, os.ErrNotExist) {
		return []RoleActivation{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read role activation history: %w", err)
	}
	history := make([]RoleActivation, 0, len(entries))
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		activation, err := s.read(entry.Name())
		if err != nil {
			return nil, err
		}
		history = append(history, activation)
	}
	sort.Slice(history, func(i, j int) bool {
		if history[i].ActivatedAt.Equal(history[j].ActivatedAt) {
			return history[i].ID > history[j].ID
		}
		return history[i].ActivatedAt.After(history[j].ActivatedAt)
	})
	return history, nil
}

func (s *RoleActivationStore) read(name string) (RoleActivation, error) {
	path := filepath.Join(s.Root(), name)
	file, err := os.Open(path)
	if err != nil {
		return RoleActivation{}, fmt.Errorf("read role activation %s: %w", path, err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes+1))
	if err != nil {
		return RoleActivation{}, err
	}
	if len(encoded) > maxEncodedStateBytes {
		return RoleActivation{}, fmt.Errorf("role activation %s exceeds %d bytes", path, maxEncodedStateBytes)
	}
	var activation RoleActivation
	unknown, err := decodeTolerating(encoded, &activation)
	if err != nil {
		return RoleActivation{}, fmt.Errorf("decode role activation %s: %w", path, err)
	}
	noteUnknownFields("role activation", unknown)
	if err := activation.Validate(); err != nil {
		return RoleActivation{}, fmt.Errorf("role activation %s: %w", path, err)
	}
	if activation.ProductID != s.productID || activation.ID+".json" != name {
		return RoleActivation{}, fmt.Errorf("role activation %s does not match its product or file name", path)
	}
	return activation, nil
}
