package runstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// The forge's answers to the reads the forge check asks conditionally, kept
// between checks with the tag the forge gave each one, so the next check can
// ask whether anything changed rather than for the whole answer again
// (publish's conditional.go).
//
// It is a cache and nothing more: no decision is read from it, and an answer
// it has lost, or a file it cannot read, costs one full read. So a file that
// cannot be read is started afresh rather than refused, two checks writing it
// at once leave whichever wrote last, and nothing here is a record anybody is
// owed.

const (
	// ForgeResponsesSchemaVersion is the version of the kept answers' file.
	ForgeResponsesSchemaVersion = 1
	// forgeResponsesFile is the file's one fixed name under the product.
	forgeResponsesFile = "forge-responses.json"
	// maxForgeResponsesBytes bounds the whole file: past it, the answers used
	// longest ago are dropped until it fits.
	maxForgeResponsesBytes = 8 << 20
	// forgeResponseUnusedFor is how long an answer nobody asked about is kept.
	// A pull request settled a day ago is not asked about again.
	forgeResponseUnusedFor = 24 * time.Hour
)

// ForgeResponse is one kept answer.
type ForgeResponse struct {
	ETag   string    `json:"etag"`
	Body   string    `json:"body"`
	UsedAt time.Time `json:"used_at"`
}

type forgeResponsesFileContent struct {
	SchemaVersion int                      `json:"schema_version"`
	ProductID     domain.ProductID         `json:"product_id"`
	Responses     map[string]ForgeResponse `json:"responses"`
}

// ForgeResponseStore is the kept answers, read once when opened and written
// back by Save. It satisfies publish.ResponseCache.
type ForgeResponseStore struct {
	root      string
	productID domain.ProductID
	now       func() time.Time

	mu        sync.Mutex
	responses map[string]ForgeResponse
}

// OpenForgeResponses reads the kept answers of one product. A file that is
// missing or cannot be read starts with none, for the reason the file comment
// gives; the second value says why it was started afresh, where it was.
func OpenForgeResponses(root string, productID domain.ProductID, now func() time.Time) (*ForgeResponseStore, string, error) {
	if !filepath.IsAbs(root) {
		return nil, "", errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, "", err
	}
	if now == nil {
		now = time.Now
	}
	store := &ForgeResponseStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
		now:       now,
		responses: map[string]ForgeResponse{},
	}
	afresh := store.load()
	return store, afresh, nil
}

func (s *ForgeResponseStore) load() string {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("the forge's kept answers could not be opened, so every read is asked in full this time: %v", err)
	}
	defer file.Close()
	var content forgeResponsesFileContent
	if err := json.NewDecoder(io.LimitReader(file, maxForgeResponsesBytes+1)).Decode(&content); err != nil {
		return fmt.Sprintf("the forge's kept answers could not be read, so every read is asked in full this time: %v", err)
	}
	if content.SchemaVersion != ForgeResponsesSchemaVersion || content.ProductID != s.productID {
		return "the forge's kept answers are of another version or product, so every read is asked in full this time"
	}
	for key, response := range content.Responses {
		s.responses[key] = response
	}
	return ""
}

// Path is where the kept answers are.
func (s *ForgeResponseStore) Path() string { return filepath.Join(s.root, forgeResponsesFile) }

// Cached is the answer kept for one read, and marks it used.
func (s *ForgeResponseStore) Cached(key string) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	response, found := s.responses[key]
	if !found {
		return "", "", false
	}
	response.UsedAt = s.now().UTC()
	s.responses[key] = response
	return response.ETag, response.Body, true
}

// Keep keeps one answer with its tag, in place of what was kept for the read.
func (s *ForgeResponseStore) Keep(key, etag, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[key] = ForgeResponse{ETag: etag, Body: body, UsedAt: s.now().UTC()}
}

// Len is how many answers are kept.
func (s *ForgeResponseStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.responses)
}

// Save writes the kept answers back, without the ones unused for a day and,
// past the file's bound, without the ones used longest ago.
func (s *ForgeResponseStore) Save() error {
	s.mu.Lock()
	cutoff := s.now().UTC().Add(-forgeResponseUnusedFor)
	content := forgeResponsesFileContent{SchemaVersion: ForgeResponsesSchemaVersion, ProductID: s.productID, Responses: map[string]ForgeResponse{}}
	keys := make([]string, 0, len(s.responses))
	for key, response := range s.responses {
		if response.UsedAt.Before(cutoff) {
			delete(s.responses, key)
			continue
		}
		keys = append(keys, key)
	}
	// Most recently used first, so what the bound drops is what was used
	// longest ago.
	sort.Slice(keys, func(i, j int) bool { return s.responses[keys[i]].UsedAt.After(s.responses[keys[j]].UsedAt) })
	size := 0
	for _, key := range keys {
		response := s.responses[key]
		size += len(key) + len(response.ETag) + len(response.Body) + 64
		if size > maxForgeResponsesBytes {
			break
		}
		content.Responses[key] = response
	}
	s.mu.Unlock()

	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".forge-responses-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary forge answers: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary forge answers: %w", err)
	}
	if err := json.NewEncoder(temporary).Encode(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write forge answers: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary forge answers: %w", err)
	}
	if err := os.Rename(temporaryPath, s.Path()); err != nil {
		return fmt.Errorf("replace forge answers: %w", err)
	}
	return syncDirectory(s.root)
}
