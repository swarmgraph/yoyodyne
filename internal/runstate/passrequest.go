package runstate

// Where requests for a program manager instance's pass are kept.
//
// "Passes on request" in docs/designs/program-manager.md: any instance may be
// asked for a pass, and an instance whose triggers name neither a schedule nor
// events runs only when asked. Two people may ask — the operator, with `yoyo
// agent pass`, and the Lead Product Manager, from her conversation — and the
// scheduler takes an open request at its next pull. This store is the record
// between the two: who asked, when, for what, and what became of it.
//
// It is one append-only log per instance, under the product's state beside the
// instance's other records, at program-managers/<agent>/requests.jsonl. A
// request, a wait recorded on it, and the pass that closes it are each an
// append, folded to one entry per request when read, so two readers never see a
// half-written record and a crash between two appends leaves a request open
// rather than lost.
//
// At most one request per instance is open. A request made while one is open is
// recorded as joined to it, and its focus is added to the open one's, so two
// people asking at once get one pass that answers both, and each can read which
// pass that was. A request is never dropped for age: it closes only when a pass
// taken from it ends.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// PassRequestSchemaVersion is 1 and has never changed.
const PassRequestSchemaVersion = 1

// MaxPassFocusPaths bounds the repository paths one request may name, and the
// paths an open request carries once others have joined it.
const MaxPassFocusPaths = 10

// MaxPassFocusAreas bounds the areas an open request carries once others have
// joined it. One request names at most one.
const MaxPassFocusAreas = 16

// MaxPassFocusBytes bounds one area or one path: it is a name, read in a line.
const MaxPassFocusBytes = 200

// MaxPassRequestTextBytes bounds a wait's reason and a closing pass's problem.
const MaxPassRequestTextBytes = 1 << 10

// maxEncodedPassRequestBytes bounds one encoded record, shared by the writer and
// the reader so a record that was written is one that can be read back.
const maxEncodedPassRequestBytes = 16 << 10

// passRequestLockWait bounds the wait for the log's lock, for the reason the
// restart requests' wait is bounded.
const passRequestLockWait = 5 * time.Second

const passRequestFile = "requests.jsonl"

// PassAsker is who asked for a pass. The set is closed at two: the design names
// the operator and the Lead Product Manager, and another role that wants a pass
// asks her for one.
type PassAsker string

const (
	// PassAskerOperator is the operator, at `yoyo agent pass`.
	PassAskerOperator PassAsker = "operator"
	// PassAskerProductManager is the Lead Product Manager, from her conversation.
	PassAskerProductManager PassAsker = PassAsker(domain.RoleProductManager)
)

// Valid reports whether the asker is one of the two.
func (a PassAsker) Valid() bool {
	return a == PassAskerOperator || a == PassAskerProductManager
}

// Describe is the asker in the words a sentence uses.
func (a PassAsker) Describe() string {
	switch a {
	case PassAskerOperator:
		return "the operator"
	case PassAskerProductManager:
		return "the Lead Product Manager"
	default:
		return string(a)
	}
}

// PassFocus is what a request asks the pass to look at in place of the next
// area its remit's rotation names: an area, or repository paths.
type PassFocus struct {
	Areas []string `json:"areas,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// Empty reports a focus that names nothing, which is a request for the next
// area in the rotation.
func (f PassFocus) Empty() bool { return len(f.Areas) == 0 && len(f.Paths) == 0 }

// Describe is the focus in one line.
func (f PassFocus) Describe() string {
	var parts []string
	if len(f.Areas) > 0 {
		parts = append(parts, "the area "+strings.Join(quoteAll(f.Areas), " and the area "))
	}
	if len(f.Paths) > 0 {
		parts = append(parts, "the paths "+strings.Join(f.Paths, ", "))
	}
	return strings.Join(parts, "; ")
}

func quoteAll(values []string) []string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return quoted
}

// NewPassFocus builds the focus one request names: at most one area, or up to
// MaxPassFocusPaths repository paths, each relative to the repository root and
// inside it. A focus naming both is refused, because the design gives a request
// one or the other.
func NewPassFocus(area string, paths []string) (PassFocus, error) {
	var focus PassFocus
	area = strings.TrimSpace(area)
	if area != "" && len(paths) > 0 {
		return PassFocus{}, errors.New("a pass request names an area or repository paths as its focus, not both")
	}
	if area != "" {
		focus.Areas = []string{area}
	}
	for _, named := range paths {
		cleaned, err := cleanFocusPath(named)
		if err != nil {
			return PassFocus{}, err
		}
		focus.Paths = appendUnique(focus.Paths, cleaned)
	}
	return focus, focus.validate()
}

// cleanFocusPath is one named path as the repository read takes it: slashed,
// relative to the repository root, and not climbing out of it.
func cleanFocusPath(named string) (string, error) {
	named = strings.TrimSpace(named)
	if named == "" {
		return "", errors.New("a focus path is empty")
	}
	slashed := filepath.ToSlash(named)
	if path.IsAbs(slashed) || filepath.IsAbs(named) {
		return "", fmt.Errorf("the focus path %q is absolute; name it relative to the repository root", named)
	}
	cleaned := path.Clean(slashed)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("the focus path %q climbs out of the repository", named)
	}
	return cleaned, nil
}

func (f PassFocus) validate() error {
	var problems []error
	if len(f.Areas) > MaxPassFocusAreas {
		problems = append(problems, fmt.Errorf("a pass request carries %d areas, limit is %d", len(f.Areas), MaxPassFocusAreas))
	}
	if len(f.Paths) > MaxPassFocusPaths {
		problems = append(problems, fmt.Errorf("a pass request names %d paths, limit is %d", len(f.Paths), MaxPassFocusPaths))
	}
	for _, area := range f.Areas {
		if strings.TrimSpace(area) == "" {
			problems = append(problems, errors.New("a focus area is empty"))
		}
		if len(area) > MaxPassFocusBytes {
			problems = append(problems, fmt.Errorf("the focus area is %d bytes, limit is %d", len(area), MaxPassFocusBytes))
		}
	}
	for _, named := range f.Paths {
		if len(named) > MaxPassFocusBytes {
			problems = append(problems, fmt.Errorf("the focus path is %d bytes, limit is %d", len(named), MaxPassFocusBytes))
		}
		if cleaned, err := cleanFocusPath(named); err != nil {
			problems = append(problems, err)
		} else if cleaned != named {
			problems = append(problems, fmt.Errorf("the focus path %q is not in its clean form %q", named, cleaned))
		}
	}
	return errors.Join(problems...)
}

// merged is this focus with another's added, keeping the bounds: what does not
// fit is left off, and how much is said by the second result.
func (f PassFocus) merged(other PassFocus) (PassFocus, int) {
	merged := PassFocus{Areas: append([]string(nil), f.Areas...), Paths: append([]string(nil), f.Paths...)}
	dropped := 0
	for _, area := range other.Areas {
		if len(merged.Areas) >= MaxPassFocusAreas && !contains(merged.Areas, area) {
			dropped++
			continue
		}
		merged.Areas = appendUnique(merged.Areas, area)
	}
	for _, named := range other.Paths {
		if len(merged.Paths) >= MaxPassFocusPaths && !contains(merged.Paths, named) {
			dropped++
			continue
		}
		merged.Paths = appendUnique(merged.Paths, named)
	}
	return merged, dropped
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if contains(values, value) {
		return values
	}
	return append(values, value)
}

// PassRequestOutcome is how the pass taken from a request ended.
type PassRequestOutcome string

const (
	PassRequestCompleted PassRequestOutcome = "completed"
	PassRequestFailed    PassRequestOutcome = "failed"
)

// PassRequest is one request for one instance's pass, folded to its latest
// entry.
type PassRequest struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	ID            string           `json:"id"`
	// Agent is the program manager instance asked: the configured agent's name.
	Agent       string    `json:"agent"`
	AskedBy     PassAsker `json:"asked_by"`
	RequestedAt time.Time `json:"requested_at"`
	// Focus is what the pass is asked to look at, and empty for the next area in
	// the rotation. On an open request others have joined, it is every joined
	// request's focus added to its own.
	Focus PassFocus `json:"focus,omitempty"`
	// ConversationID and Turn are where the Lead Product Manager asked, so her
	// reasoning can be read back from her conversation's own record.
	ConversationID string `json:"conversation_id,omitempty"`
	Turn           int    `json:"turn,omitempty"`
	// JoinedTo is the open request this one was added to, where one was open
	// when it was made. A joined request is answered by that request's pass and
	// is never open itself.
	JoinedTo string `json:"joined_to,omitempty"`
	// Joined is the requests added to this one, by identifier.
	Joined []string `json:"joined,omitempty"`
	// Waiting is why the request could not be taken at the last pull that tried,
	// and WaitingSince when that reason was first recorded. Both are cleared when
	// a pass is taken from it.
	Waiting      string     `json:"waiting,omitempty"`
	WaitingSince *time.Time `json:"waiting_since,omitempty"`
	// Pass is the pass taken from the request, by its name in the sweep log, and
	// TakenAt when it was taken; ClosedAt, Outcome, and Problem are how that pass
	// ended. A request closed before passes were recorded as taken has a pass and
	// no TakenAt.
	Pass     string             `json:"pass,omitempty"`
	TakenAt  *time.Time         `json:"taken_at,omitempty"`
	ClosedAt *time.Time         `json:"closed_at,omitempty"`
	Outcome  PassRequestOutcome `json:"outcome,omitempty"`
	Problem  string             `json:"problem,omitempty"`
}

// Open reports a request still waiting for its pass: not joined to another,
// and not closed.
func (r PassRequest) Open() bool { return r.JoinedTo == "" && r.ClosedAt == nil }

// PassRequestState is what became of a request, as a person asking reads it.
type PassRequestState string

const (
	// PassRequestStateWaiting is a request no pass has been taken from yet.
	PassRequestStateWaiting PassRequestState = "waiting"
	// PassRequestStateTaken is a request a pass was taken from that has not
	// ended.
	PassRequestStateTaken PassRequestState = "taken"
	// PassRequestStateJoined is a request added to one already open, answered by
	// that one's pass.
	PassRequestStateJoined PassRequestState = "joined"
	// PassRequestStateCompleted and PassRequestStateFailed are a request whose
	// pass ended, in the words of its Outcome.
	PassRequestStateCompleted PassRequestState = PassRequestState(PassRequestCompleted)
	PassRequestStateFailed    PassRequestState = PassRequestState(PassRequestFailed)
)

// State is what became of the request, and since when: waiting since it was
// asked, taken since its pass was, joined since it was asked, or the outcome
// its pass ended with, since it ended.
func (r PassRequest) State() (PassRequestState, time.Time) {
	switch {
	case r.JoinedTo != "":
		return PassRequestStateJoined, r.RequestedAt
	case r.ClosedAt != nil:
		return PassRequestState(r.Outcome), *r.ClosedAt
	case r.TakenAt != nil:
		return PassRequestStateTaken, *r.TakenAt
	default:
		return PassRequestStateWaiting, r.RequestedAt
	}
}

func (r PassRequest) Validate() error {
	var problems []error
	if r.SchemaVersion != PassRequestSchemaVersion {
		problems = append(problems, fmt.Errorf("pass request schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(r.ID) == "" {
		problems = append(problems, errors.New("pass request id is required"))
	}
	if err := domain.ValidateIdentifier("agent", r.Agent); err != nil {
		problems = append(problems, err)
	}
	if !r.AskedBy.Valid() {
		problems = append(problems, fmt.Errorf("a pass is asked for by %q or %q, not %q", PassAskerOperator, PassAskerProductManager, r.AskedBy))
	}
	if r.RequestedAt.IsZero() {
		problems = append(problems, errors.New("requested at is required"))
	}
	if err := r.Focus.validate(); err != nil {
		problems = append(problems, err)
	}
	if len(r.Waiting) > MaxPassRequestTextBytes || len(r.Problem) > MaxPassRequestTextBytes {
		problems = append(problems, fmt.Errorf("a pass request's wait or problem is limited to %d bytes", MaxPassRequestTextBytes))
	}
	if (r.Waiting == "") != (r.WaitingSince == nil) {
		problems = append(problems, errors.New("a wait on a pass request names both its reason and since when"))
	}
	if r.TakenAt != nil && strings.TrimSpace(r.Pass) == "" {
		problems = append(problems, errors.New("a pass request recorded as taken names the pass that took it"))
	}
	if r.ClosedAt != nil {
		if r.Outcome != PassRequestCompleted && r.Outcome != PassRequestFailed {
			problems = append(problems, fmt.Errorf("a closed pass request ended %q or %q, not %q", PassRequestCompleted, PassRequestFailed, r.Outcome))
		}
		if strings.TrimSpace(r.Pass) == "" {
			problems = append(problems, errors.New("a closed pass request names the pass that closed it"))
		}
	} else if r.Outcome != "" {
		problems = append(problems, errors.New("an outcome requires the time the request closed"))
	}
	return errors.Join(problems...)
}

// NewPassRequestID names one request.
func NewPassRequestID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate pass request id: %w", err)
	}
	return "pass-request-" + hex.EncodeToString(raw), nil
}

// PassRequestStore is the program manager instances' pass requests, one log per
// instance.
type PassRequestStore struct {
	// state is the state root every write this store makes is confined to.
	state     stateRoot
	root      string
	productID domain.ProductID
}

func NewPassRequestStore(root string, productID domain.ProductID) (*PassRequestStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	state, err := newStateRoot(root)
	if err != nil {
		return nil, err
	}
	return &PassRequestStore{
		state:     state,
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "program-managers"),
		productID: productID,
	}, nil
}

// PassRequests is the store under this product's state.
func (s *Store) PassRequests() *PassRequestStore {
	return &PassRequestStore{
		state:     s.state,
		root:      filepath.Join(filepath.Dir(s.root), "program-managers"),
		productID: s.productID,
	}
}

// Path names one instance's log, so a failure can say where its requests are.
func (s *PassRequestStore) Path(agent string) string {
	return filepath.Join(s.root, agent, passRequestFile)
}

// Request records one request. Where the instance already has one open, the new
// one is recorded as joined to it and its focus is added to the open one's; the
// request returned says which, through JoinedTo. The read and the appends are
// made under one lock across processes, so two people asking at the same moment
// cannot each find nothing open and each open one.
func (s *PassRequestStore) Request(request PassRequest) (PassRequest, error) {
	if request.ProductID != s.productID {
		return PassRequest{}, fmt.Errorf("pass request product %q does not match store product %q", request.ProductID, s.productID)
	}
	request.RequestedAt = request.RequestedAt.UTC()
	request.JoinedTo, request.Joined = "", nil
	request.Waiting, request.WaitingSince = "", nil
	request.Pass, request.TakenAt, request.ClosedAt, request.Outcome, request.Problem = "", nil, nil, "", ""
	if err := request.Validate(); err != nil {
		return PassRequest{}, err
	}
	unlock, err := s.lock(request.Agent)
	if err != nil {
		return PassRequest{}, err
	}
	defer unlock()
	open, found, err := s.Open(request.Agent)
	if err != nil {
		return PassRequest{}, err
	}
	if !found {
		if err := s.append(request); err != nil {
			return PassRequest{}, err
		}
		return request, nil
	}
	request.JoinedTo = open.ID
	if err := s.append(request); err != nil {
		return PassRequest{}, err
	}
	open.Joined = append(open.Joined, request.ID)
	open.Focus, _ = open.Focus.merged(request.Focus)
	if err := s.append(open); err != nil {
		return PassRequest{}, err
	}
	return request, nil
}

// Wait records why an open request could not be taken. The same reason again
// writes nothing, so a request that waits out a long outage is one entry rather
// than one a pull; a new reason replaces the old and restarts its time.
func (s *PassRequestStore) Wait(agent, id, reason string, at time.Time) (PassRequest, error) {
	reason = boundRequestText(reason)
	if reason == "" {
		return PassRequest{}, errors.New("a wait on a pass request says what it waits on")
	}
	return s.update(agent, id, func(request *PassRequest) (bool, error) {
		if !request.Open() {
			return false, fmt.Errorf("pass request %s is not open", id)
		}
		if request.Waiting == reason {
			return false, nil
		}
		since := at.UTC()
		request.Waiting, request.WaitingSince = reason, &since
		// A pass taken earlier that never ended is not what the request waits
		// on now, so it no longer reads as taken.
		request.Pass, request.TakenAt = "", nil
		return true, nil
	})
}

// Take records that a pass was taken from an open request: the pass, by its
// name in the sweep log, and when, with any wait cleared. The request stays
// open until that pass ends and closes it, so a process stopped mid-pass leaves
// it for the next pull to take again; that take names the later pass.
func (s *PassRequestStore) Take(agent, id, pass string, at time.Time) (PassRequest, error) {
	pass = strings.TrimSpace(pass)
	if pass == "" {
		return PassRequest{}, errors.New("a pass request is taken by a named pass")
	}
	return s.update(agent, id, func(request *PassRequest) (bool, error) {
		if !request.Open() {
			return false, fmt.Errorf("pass request %s is not open", id)
		}
		taken := at.UTC()
		request.Pass, request.TakenAt = pass, &taken
		request.Waiting, request.WaitingSince = "", nil
		return true, nil
	})
}

// Close records how the pass taken from an open request ended. A request is
// closed once; a second close is refused and the first stands.
func (s *PassRequestStore) Close(agent, id, pass string, outcome PassRequestOutcome, problem string, at time.Time) (PassRequest, error) {
	return s.update(agent, id, func(request *PassRequest) (bool, error) {
		if !request.Open() {
			return false, fmt.Errorf("pass request %s is not open", id)
		}
		closed := at.UTC()
		request.Pass, request.ClosedAt, request.Outcome = strings.TrimSpace(pass), &closed, outcome
		request.Problem = boundRequestText(problem)
		request.Waiting, request.WaitingSince = "", nil
		return true, nil
	})
}

func (s *PassRequestStore) update(agent, id string, change func(*PassRequest) (bool, error)) (PassRequest, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return PassRequest{}, err
	}
	unlock, err := s.lock(agent)
	if err != nil {
		return PassRequest{}, err
	}
	defer unlock()
	requests, err := s.List(agent)
	if err != nil {
		return PassRequest{}, err
	}
	for _, request := range requests {
		if request.ID != id {
			continue
		}
		changed, err := change(&request)
		if err != nil || !changed {
			return request, err
		}
		if err := request.Validate(); err != nil {
			return PassRequest{}, err
		}
		if err := s.append(request); err != nil {
			return PassRequest{}, err
		}
		return request, nil
	}
	return PassRequest{}, fmt.Errorf("no pass request %s is recorded for %s", id, agent)
}

func boundRequestText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > MaxPassRequestTextBytes {
		text = oneline.Fold(text, MaxPassRequestTextBytes-len(oneline.Marker))
	}
	return text
}

// List returns every request made of one instance, folded to one entry each and
// oldest first. A log that does not exist yet is an instance nobody has asked
// for a pass, which is not a failure to read.
func (s *PassRequestStore) List(agent string) ([]PassRequest, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return nil, err
	}
	file, err := os.Open(s.Path(agent))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the pass requests of %s: %w", agent, err)
	}
	defer file.Close()

	folded := map[string]PassRequest{}
	var order []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedPassRequestBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var decoded PassRequest
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			return nil, fmt.Errorf("decode the pass requests of %s: %w", agent, err)
		}
		if err := decoded.Validate(); err != nil {
			return nil, fmt.Errorf("decode the pass requests of %s: %w", agent, err)
		}
		if decoded.ProductID != s.productID || decoded.Agent != agent {
			return nil, fmt.Errorf("decode the pass requests of %s: a request for product %q and instance %q is in this log", agent, decoded.ProductID, decoded.Agent)
		}
		if _, seen := folded[decoded.ID]; !seen {
			order = append(order, decoded.ID)
		}
		// The last entry for a request is the whole of it.
		folded[decoded.ID] = decoded
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the pass requests of %s: %w", agent, err)
	}
	requests := make([]PassRequest, 0, len(order))
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

// Open is the instance's one open request, where it has one.
func (s *PassRequestStore) Open(agent string) (PassRequest, bool, error) {
	requests, err := s.List(agent)
	if err != nil {
		return PassRequest{}, false, err
	}
	for _, request := range requests {
		if request.Open() {
			return request, true, nil
		}
	}
	return PassRequest{}, false, nil
}

func (s *PassRequestStore) lock(agent string) (func(), error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return nil, err
	}
	file, err := s.state.openLock(s.Path(agent) + ".lock")
	if err != nil {
		return nil, fmt.Errorf("open the pass request lock of %s: %w", agent, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), passRequestLockWait)
	defer cancel()
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the pass requests of %s: %w", agent, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}

func (s *PassRequestStore) append(request PassRequest) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(request); err != nil {
		return fmt.Errorf("encode pass request: %w", err)
	}
	encoded := buffer.Bytes()
	if len(encoded) > maxEncodedPassRequestBytes {
		return fmt.Errorf("encoded pass request is %d bytes, limit is %d", len(encoded), maxEncodedPassRequestBytes)
	}
	logPath := s.Path(request.Agent)
	// Appended in checked batches through the confined writer, which syncs the
	// directory when it creates the log.
	if err := s.state.appendTo(logPath, encoded); err != nil {
		return fmt.Errorf("append pass request: %w", err)
	}
	return nil
}
