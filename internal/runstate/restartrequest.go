package runstate

// Where a program manager's requests to restart a part of the product are kept.
//
// The program manager's design gives the role one request to the supervisor,
// `service.request-restart`: an instance that believes a part of the product
// should be restarted writes one durable request naming the part, and the
// supervisor's periodic pass is the only thing that ever acts on one. No
// program manager restarts, kills, or invokes anything itself — noticing
// surfaces do not restart processes, and the harness is the only invoker — so
// this store writes a record and nothing else.
//
// The supervisor's periodic pass is what answers a request: it reads the open
// ones, acts on each within the supervisor's own bounds, and appends the answer
// with Answer. Nothing else writes one.
//
// It is one append-only log per product, like the stalls: a request and the
// answer to it are both appends, folded to one entry per request when read, so
// two readers never see a half-written record and a crash between the two
// leaves a request that is open rather than one that is neither.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
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

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// RestartRequestSchemaVersion is 1 and has never changed.
const RestartRequestSchemaVersion = 1

// MaxRestartReasonBytes bounds why an instance asked, for the reason every
// other reason here is bounded: it is read in a line by whoever decides about
// the part.
const MaxRestartReasonBytes = 1 << 10

// maxEncodedRestartRequestBytes bounds one encoded record, shared by the writer
// and the reader so a record that was written is one that can be read back.
const maxEncodedRestartRequestBytes = 16 << 10

// restartRequestLockWait bounds the wait for the log's lock. What it waits on is
// another process's one small append; longer than this is a lock somebody died
// holding, and the request should fail and be asked again rather than hang.
const restartRequestLockWait = 5 * time.Second

// RestartRequest is one instance asking the supervisor to restart one part.
type RestartRequest struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	ID            string           `json:"id"`
	// Agent is the program manager instance that asked: the configured agent's
	// name, which is the whole of an instance's identity.
	Agent string `json:"agent"`
	// Part is the part of the product the request names, one of the four the
	// configuration's services section declares.
	Part config.ServiceName `json:"part"`
	// Reason is why the instance asked, in its own words.
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requested_at"`
	// ConversationID and Turn are where the request was made, so the reasoning
	// behind it can be read back from the conversation's own record.
	ConversationID string `json:"conversation_id,omitempty"`
	Turn           int    `json:"turn,omitempty"`
	// AnsweredAt and Answer are what the supervisor's pass records once it has
	// acted on the request: what it did, or why it did nothing.
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
	Answer     string     `json:"answer,omitempty"`
}

// Open reports whether nothing has answered the request yet.
func (r RestartRequest) Open() bool { return r.AnsweredAt == nil }

func (r RestartRequest) Validate() error {
	var problems []error
	if r.SchemaVersion != RestartRequestSchemaVersion {
		problems = append(problems, fmt.Errorf("restart request schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(r.ID) == "" {
		problems = append(problems, errors.New("restart request id is required"))
	}
	if err := domain.ValidateIdentifier("agent", r.Agent); err != nil {
		problems = append(problems, err)
	}
	if err := ValidateRestartPart(string(r.Part)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(r.Reason) == "" {
		problems = append(problems, errors.New("a restart request says why it was made"))
	}
	if len(r.Reason) > MaxRestartReasonBytes {
		problems = append(problems, fmt.Errorf("restart reason is %d bytes, limit is %d", len(r.Reason), MaxRestartReasonBytes))
	}
	if r.RequestedAt.IsZero() {
		problems = append(problems, errors.New("requested at is required"))
	}
	if r.AnsweredAt != nil && r.AnsweredAt.IsZero() {
		problems = append(problems, errors.New("answered_at is present and unset"))
	}
	if r.AnsweredAt == nil && strings.TrimSpace(r.Answer) != "" {
		problems = append(problems, errors.New("an answer requires the time it was answered"))
	}
	return errors.Join(problems...)
}

// ValidateRestartPart refuses a part the services section does not declare,
// naming the four it does.
func ValidateRestartPart(part string) error {
	for _, declared := range config.ServiceNames {
		if part == string(declared) {
			return nil
		}
	}
	named := make([]string, 0, len(config.ServiceNames))
	for _, declared := range config.ServiceNames {
		named = append(named, fmt.Sprintf("%q", declared))
	}
	return fmt.Errorf("%q is not a part the services section declares; the parts are %s", part, strings.Join(named, ", "))
}

// NewRestartRequestID names one request.
func NewRestartRequestID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate restart request id: %w", err)
	}
	return "restart-" + hex.EncodeToString(raw), nil
}

// OpenRestartRequestError is a second request refused because the instance
// already has one open for the same part. It carries the open one, so the
// refusal can name it.
type OpenRestartRequestError struct {
	Open RestartRequest
}

func (e *OpenRestartRequestError) Error() string {
	return fmt.Sprintf("%s already asked for the %s to be restarted as %s at %s, and nothing has answered it; a second request for the same part is refused while the first is open",
		e.Open.Agent, e.Open.Part, e.Open.ID, e.Open.RequestedAt.UTC().Format(time.RFC3339))
}

// RestartRequestStore is the log of restart requests, under the product's state
// beside the program managers' other records.
type RestartRequestStore struct {
	root      string
	productID domain.ProductID
}

func NewRestartRequestStore(root string, productID domain.ProductID) (*RestartRequestStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &RestartRequestStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "program-managers"),
		productID: productID,
	}, nil
}

func (s *RestartRequestStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the requests are.
func (s *RestartRequestStore) Path() string { return filepath.Join(s.root, "restart-requests.jsonl") }

// Request records one request, refusing it with an *OpenRestartRequestError
// where the same instance already has one open for the same part. The read and
// the append are made under one lock across processes, so two turns asking at
// the same moment cannot each find nothing open and each record one.
//
// It writes the record and does nothing else: no process is started, stopped,
// or signalled here; the supervisor's pass is what acts on it.
func (s *RestartRequestStore) Request(request RestartRequest) (RestartRequest, error) {
	if request.ProductID != s.productID {
		return RestartRequest{}, fmt.Errorf("restart request product %q does not match store product %q", request.ProductID, s.productID)
	}
	request.AnsweredAt = nil
	request.Answer = ""
	request.RequestedAt = request.RequestedAt.UTC()
	if err := request.Validate(); err != nil {
		return RestartRequest{}, err
	}
	unlock, err := s.lock()
	if err != nil {
		return RestartRequest{}, err
	}
	defer unlock()
	open, err := s.Open()
	if err != nil {
		return RestartRequest{}, err
	}
	for _, standing := range open {
		if standing.Agent == request.Agent && standing.Part == request.Part {
			return RestartRequest{}, &OpenRestartRequestError{Open: standing}
		}
	}
	if err := s.append(request); err != nil {
		return RestartRequest{}, err
	}
	return request, nil
}

// Answer records what the supervisor's pass did about an open request. It is
// an append of the request with its answer, under the same lock as Request, so
// a request answered twice keeps the first answer and says so.
func (s *RestartRequestStore) Answer(id, answer string, at time.Time) (RestartRequest, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return RestartRequest{}, errors.New("an answer to a restart request says what was done")
	}
	if len(answer) > MaxRestartReasonBytes {
		answer = oneline.Fold(answer, MaxRestartReasonBytes-len(oneline.Marker))
	}
	unlock, err := s.lock()
	if err != nil {
		return RestartRequest{}, err
	}
	defer unlock()
	requests, err := s.List()
	if err != nil {
		return RestartRequest{}, err
	}
	for _, request := range requests {
		if request.ID != id {
			continue
		}
		if !request.Open() {
			return request, fmt.Errorf("restart request %s was already answered at %s", id, request.AnsweredAt.UTC().Format(time.RFC3339))
		}
		answered := at.UTC()
		request.AnsweredAt = &answered
		request.Answer = answer
		if err := request.Validate(); err != nil {
			return RestartRequest{}, err
		}
		if err := s.append(request); err != nil {
			return RestartRequest{}, err
		}
		return request, nil
	}
	return RestartRequest{}, fmt.Errorf("no restart request %s is recorded", id)
}

// List returns every request, folded to one entry each and oldest first. A log
// that does not exist yet is a product no instance has asked anything of the
// supervisor for, which is not a failure to read.
func (s *RestartRequestStore) List() ([]RestartRequest, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open restart request log: %w", err)
	}
	defer file.Close()

	folded := map[string]RestartRequest{}
	var order []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedRestartRequestBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var decoded RestartRequest
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			return nil, fmt.Errorf("decode restart request log: %w", err)
		}
		if err := decoded.Validate(); err != nil {
			return nil, fmt.Errorf("decode restart request log: %w", err)
		}
		if decoded.ProductID != s.productID {
			return nil, fmt.Errorf("decode restart request log: request product %q does not match store product %q", decoded.ProductID, s.productID)
		}
		if _, seen := folded[decoded.ID]; !seen {
			order = append(order, decoded.ID)
		}
		// The last entry for a request is the whole of it: an answer carries
		// everything the request did, plus the answer.
		folded[decoded.ID] = decoded
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read restart request log: %w", err)
	}
	requests := make([]RestartRequest, 0, len(order))
	for _, id := range order {
		requests = append(requests, folded[id])
	}
	sort.SliceStable(requests, func(first, second int) bool {
		if !requests[first].RequestedAt.Equal(requests[second].RequestedAt) {
			return requests[first].RequestedAt.Before(requests[second].RequestedAt)
		}
		return requests[first].ID < requests[second].ID
	})
	return requests, nil
}

// Open is every request nothing has answered, oldest first.
func (s *RestartRequestStore) Open() ([]RestartRequest, error) {
	requests, err := s.List()
	if err != nil {
		return nil, err
	}
	open := make([]RestartRequest, 0, len(requests))
	for _, request := range requests {
		if request.Open() {
			open = append(open, request)
		}
	}
	return open, nil
}

func (s *RestartRequestStore) lock() (func(), error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create program manager directory: %w", err)
	}
	file, err := os.OpenFile(s.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open restart request log lock: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), restartRequestLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the restart request log: %w", err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

func (s *RestartRequestStore) append(request RestartRequest) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(request); err != nil {
		return fmt.Errorf("encode restart request: %w", err)
	}
	encoded := buffer.Bytes()
	if len(encoded) > maxEncodedRestartRequestBytes {
		return fmt.Errorf("encoded restart request is %d bytes, limit is %d", len(encoded), maxEncodedRestartRequestBytes)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect restart request log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open restart request log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append restart request: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append restart request: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync restart request log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close restart request log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}
