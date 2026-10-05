package runstate

// Every provider spend, one durable line at the moment it is known.
//
// What the harness spends was already recorded, and only ever inside the record
// of the thing that spent it: a run's event log carries the cost of the
// invocations that run made, and a conversation turn's carries its own. That is
// enough to price one run and nothing else. Asking what a day cost, or what a
// persona costs, or whether one model is worth what it charges over another,
// meant opening every run record there has ever been and knowing in advance
// which of them to open.
//
// So a spend says itself, in the shape every other log here is written in: an
// append-only log per product, one line per priced provider invocation, carrying
// who spent it, how much, on whose account and under which configuration, and
// what it was spent on. A line is written once and never revised, because what
// an invocation cost is a moment that happened rather than a state that moves.
//
// Nothing here aggregates. Adding the lines up is the operator's, and any later
// read model builds on the same lines rather than on a rollup this decided for
// them in advance -- which is also what makes the log evidence for routing a
// model, where the question is what each of them charged rather than what they
// charged together.

import (
	"bufio"
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
)

// SpendSchemaVersion is 1 and has never changed.
const SpendSchemaVersion = 1

// maxEncodedSpendBytes bounds one encoded line, including the trailing newline.
// The writer and the reader share it, so a line that was written is always one
// that can be read back.
const maxEncodedSpendBytes = 8 << 10

// MaxSpendUnknownBytes bounds what a line may say about why an amount is not
// known. It is the provider's or the harness's own words about one invocation,
// so it is bounded the way a failure detail is rather than the way a document
// would be.
const MaxSpendUnknownBytes = 1 << 10

// SpendPhase names the part of the work one invocation served. The three a run
// splits into are the same three a run's own event log is split by, so a line
// and the run it came from say the same thing about where the money went; the
// two beside them are the invocations that belong to no run at all.
type SpendPhase string

const (
	// SpendPhaseDevelopment is the developer's first attempt at a change,
	// including any invocation reissued into it after the provider refused or
	// killed one.
	SpendPhaseDevelopment SpendPhase = "development"
	// SpendPhaseReview is a reviewer invocation, whichever way its verdict went.
	SpendPhaseReview SpendPhase = "review"
	// SpendPhaseRepair is every developer attempt after the first, whatever sent
	// the work back: a failing check, a refused path, or a reviewer's findings.
	SpendPhaseRepair SpendPhase = "repair"
	// SpendPhaseConversation is one turn of a management conversation.
	SpendPhaseConversation SpendPhase = "conversation"
	// SpendPhaseExchange is one round of an inter-role ask, which is an
	// invocation with neither a run nor a conversation behind it.
	SpendPhaseExchange SpendPhase = "exchange"
)

// SpendPhases lists every phase there is, in the order a refusal names them.
func SpendPhases() []SpendPhase {
	return []SpendPhase{
		SpendPhaseDevelopment,
		SpendPhaseReview,
		SpendPhaseRepair,
		SpendPhaseConversation,
		SpendPhaseExchange,
	}
}

func (p SpendPhase) Valid() bool {
	for _, candidate := range SpendPhases() {
		if p == candidate {
			return true
		}
	}
	return false
}

// SpendClassification says whether the amount on a line is a number anybody
// knows. It is carried rather than inferred from the amount, because the two
// things a zero could mean -- an invocation that was free and an invocation
// nobody was told the price of -- are opposite facts, and a total that adds the
// second in as nothing is wrong by however much was really spent.
type SpendClassification string

const (
	// SpendKnown is an amount the provider reported for the invocation.
	SpendKnown SpendClassification = "known"
	// SpendUnknown is an invocation the provider never reported a cost for. The
	// amount on such a line is zero and means nothing; what the line says is
	// beside it, in words.
	SpendUnknown SpendClassification = "unknown"
)

func (c SpendClassification) Valid() bool {
	return c == SpendKnown || c == SpendUnknown
}

// Spend is one priced provider invocation: what it cost, who spent it, and what
// it was spent on. It is written when the invocation ends, which is the moment
// its cost is known, and never revised.
type Spend struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	At            time.Time        `json:"at"`
	// Role is the contract the spender worked under and Agent is the configured
	// agent that filled it. Both are recorded because a project may configure
	// more than one agent for a role, and "what the developer costs" is then a
	// different question from "what this developer costs" -- which is exactly the
	// question a routing decision is.
	Role  domain.AgentRole `json:"role"`
	Agent string           `json:"agent,omitempty"`
	Phase SpendPhase       `json:"phase"`
	// Classification says whether AmountUSD is a number. AmountUSD is always
	// encoded, including on an unknown line: a key left out reads to whatever
	// consumes the log as an amount of nothing, which is the one thing an unknown
	// spend must never be mistaken for.
	//
	// AmountUSD is this invocation's own cost and never the session's running
	// total. The difference is the whole of yoyodyne-ifd.432.10: a provider asked
	// to resume a session reports what that session has cost since it began, so a
	// log recording the reported figure verbatim counts every earlier turn again
	// on every later one -- which is how this product's last seven days came to be
	// recorded at $30,841 against an actual $3,464. What the provider reported is
	// kept beside it rather than thrown away, in ReportedTotalUSD.
	Classification SpendClassification `json:"classification"`
	AmountUSD      float64             `json:"amount_usd"`
	// ReportedTotalUSD is what the provider itself said when this invocation
	// ended, which on a resumed session is the session's running total rather
	// than this invocation's cost. It is kept because the correction above has to
	// be auditable: a reader holding both figures can see what was reported and
	// what was made of it, and a later invocation of the same session is priced
	// against it rather than against an amount that has already been corrected.
	//
	// It is absent where there was nothing to correct -- a line the provider
	// priced at nothing, an unknown line, and every line written before this was
	// carried, whose AmountUSD is the reported figure itself. SpendStore.List
	// re-derives those on the way out, which is what lets a log written across the
	// change be read as one thing.
	ReportedTotalUSD float64 `json:"reported_total_usd,omitempty"`
	// Unknown says why nobody knows what this invocation cost, and is empty on a
	// line that names an amount.
	Unknown string `json:"unknown,omitempty"`
	// The account the invocation ran on and the configuration in force when it
	// did. Both are required: what a spend is attributable to is an account and a
	// set of effective values, and a line that named neither would be a number
	// with nowhere to put it.
	AccountAlias   string `json:"account_alias"`
	ConfigRevision string `json:"config_revision"`
	// Exactly one of these names what the invocation belongs to, and it is
	// the same identifier that invocation's event log is named by. A run also
	// names the work item it served, which is what an item's spend is read by.
	//
	// A branch review has one of its own rather than borrowing run_id. It is not
	// a run and has no run behind it, so a line that put its identifier there
	// would hand anything joining these lines to run records a run id naming no
	// run — and the join is exactly what a later read model over this log is.
	RunID      string `json:"run_id,omitempty"`
	WorkItemID string `json:"work_item_id,omitempty"`
	// A side conversation has one of its own for the reason a branch review does.
	// It is not the main thread it is held beside: a line naming that thread would
	// put a side turn's cost on a conversation that never took it, and the two are
	// separately answerable for what they spend.
	ConversationID string `json:"conversation_id,omitempty"`
	SideStreamID   string `json:"side_stream_id,omitempty"`
	ExchangeID     string `json:"exchange_id,omitempty"`
	BranchReviewID string `json:"branch_review_id,omitempty"`
	// What served the invocation. The backend and the requested model are what
	// the harness asked for; the resolved model is what the provider reported
	// actually serving it, which is the only durable evidence where the requested
	// selector was a floating alias.
	Backend       domain.Backend `json:"backend"`
	Model         string         `json:"model,omitempty"`
	ResolvedModel string         `json:"resolved_model,omitempty"`
	// Effort is the effort level the invocation asked the provider for, and
	// empty where its agent configured none -- which is every line written
	// before the level was configurable, and says the provider resolved its own.
	Effort string `json:"effort,omitempty"`
	// ResolvedEffort is provider-reported; EffortReported is false when not reported.
	ResolvedEffort string `json:"resolved_effort,omitempty"`
	EffortReported bool   `json:"effort_reported"`
	// AdapterVersion is the compiled adapter that reached the provider. With the
	// backend, the account alias, and the model above it, it is the whole of the
	// endpoint identity this line was served by — see backend.Endpoint — which is
	// what lets a reader say which endpoint served a turn rather than only which
	// provider was named.
	//
	// It is what a record needs beyond the provider's own name because the two are
	// separate facts: a provider a project declared is reached by an adapter this
	// build ships, and two harness builds reading one provider differently is
	// exactly the difference a record has to be able to tell apart.
	//
	// Absent means nobody said. That is every line written before this was
	// carried, and a line for an invocation that died before its adapter could
	// report anything on a provider the harness has no built-in description of.
	AdapterVersion string `json:"adapter_version,omitempty"`
	// SessionID is the provider session the invocation ran in, where it reported
	// one. It is evidence about the invocation and never the record of it: what
	// this line says survives the session being gone.
	SessionID string `json:"session_id,omitempty"`
	// Build is the repository revision the harness binary that made the
	// invocation was built from. It completes what the durable-state invariant
	// asks every provider invocation to be pinned to: the backend, the model, the
	// account, the configuration — and, since a long-lived process goes on running
	// what it was started with, which harness actually made the call.
	//
	// It is not required, and that is the one place this differs from the account
	// and the revision beside it. Those the harness always knows; a build is
	// stamped into the binary by whoever built it, so one installed from the
	// module cache carries none. An absence is recorded as one rather than guessed
	// at from the version: a comparison nobody can make is an answer, and a
	// comparison made against the wrong commit is not.
	Build string `json:"build,omitempty"`
}

// Known reports a line carrying an amount somebody can add up.
func (s Spend) Known() bool { return s.Classification == SpendKnown }

// ReportedTotal is the provider's own figure for this invocation, which is what
// a later invocation of the same session is priced against. It is the amount
// itself on a line that carried no correction, which is both a line the
// correction left alone and every line written before there was one.
func (s Spend) ReportedTotal() float64 {
	if s.ReportedTotalUSD != 0 {
		return s.ReportedTotalUSD
	}
	return s.AmountUSD
}

// OwnCostUSD is one invocation's own cost, from what the provider reported for
// it and what the provider had already reported for the same session.
//
// A provider resuming a session reports that session's running total, so what
// this invocation cost is what the total moved by. Two cases record the whole
// reported figure instead: a session's first invocation, which has no earlier
// total to have moved, and a figure below the one before it, which is a provider
// reporting this invocation's own cost rather than a running total -- or one
// whose total restarted mid-session, where the first figure after the restart is
// again a beginning. Both are the same rule stated once: a total that did not
// rise is not a total this figure is an increment over.
//
// It is never negative, which is what makes it safe to apply to a log whose
// provider changed its reporting partway through -- as this product's did on
// 2026-09-19, mid-conversation.
func OwnCostUSD(reported, previouslyReported float64, seen bool) float64 {
	if !seen || reported < previouslyReported {
		return reported
	}
	return reported - previouslyReported
}

// SessionCosts turns each invocation's reported figure into that invocation's
// own cost, over a sequence of invocations read in the order they were recorded.
// It is what a scan of one event log or one cost log keeps while it reads: the
// rule above needs the session's previous figure, and the only thing that has it
// is whatever is walking the record.
//
// An invocation that named no session is its own cost as reported. Nothing can
// say otherwise about it, and a provider that reports no session is one whose
// figures were never running totals to begin with.
type SessionCosts struct {
	reported map[string]float64
}

// Own is what this invocation cost, and records its reported figure as the one
// the session's next invocation is priced against.
func (c *SessionCosts) Own(sessionID string, reported float64) float64 {
	session := strings.TrimSpace(sessionID)
	if session == "" {
		return reported
	}
	if c.reported == nil {
		c.reported = make(map[string]float64)
	}
	previous, seen := c.reported[session]
	c.reported[session] = reported
	return OwnCostUSD(reported, previous, seen)
}

// Validate reports every contract violation in the line at once.
func (s Spend) Validate() error {
	var problems []error
	if s.SchemaVersion != SpendSchemaVersion {
		problems = append(problems, fmt.Errorf("spend schema version %d is not supported", s.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if s.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if !s.Role.Valid() {
		problems = append(problems, fmt.Errorf("role %q is not one of the harness's roles", s.Role))
	}
	if s.Agent != "" {
		if err := domain.ValidateIdentifier("agent name", s.Agent); err != nil {
			problems = append(problems, err)
		}
	}
	if !s.Phase.Valid() {
		problems = append(problems, fmt.Errorf("phase %q must be one of %s", s.Phase, joinSpendPhases()))
	}
	if !s.Backend.Valid() {
		problems = append(problems, fmt.Errorf("backend %q is not a backend identifier", s.Backend))
	}
	// The account and the configuration are the attribution, so a line missing
	// either is refused rather than stored as a spend nobody can attribute. Their
	// shapes are checked against the same patterns a run's are, because a line
	// naming an account nothing configured reads as evidence.
	if !accountAliasPattern.MatchString(s.AccountAlias) {
		problems = append(problems, errors.New("account_alias is not an account alias"))
	}
	if !configRevisionPattern.MatchString(s.ConfigRevision) {
		problems = append(problems, errors.New("config_revision is not a configuration revision"))
	}
	// The build is absent from a line an unstamped binary wrote and from every
	// line written before it was carried, so what is checked is the shape of one
	// that is there: a line naming a revision nothing could have produced says
	// less than one naming none, because it reads as evidence.
	if s.Build != "" && !buildPattern.MatchString(s.Build) {
		problems = append(problems, errors.New("build is not a revision"))
	}
	problems = append(problems, s.subjectProblem(), s.amountProblem())
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid spend: %w", err)
	}
	return nil
}

// subjectProblem reports a line that does not name exactly one thing it was
// spent on. Naming none is a spend nothing can ever be read back to, and naming
// two is a spend that would be counted twice by whoever reads it by either.
func (s Spend) subjectProblem() error {
	named := 0
	for _, id := range []string{s.RunID, s.ConversationID, s.SideStreamID, s.ExchangeID, s.BranchReviewID} {
		if strings.TrimSpace(id) != "" {
			named++
		}
	}
	if named != 1 {
		return errors.New("a spend names exactly one of run_id, conversation_id, side_stream_id, exchange_id, and branch_review_id")
	}
	// A work item belongs to a run and to nothing else: an invocation with no run
	// behind it served no assigned work, and saying it did would put money on an
	// item nothing was ever run for.
	if strings.TrimSpace(s.WorkItemID) != "" && strings.TrimSpace(s.RunID) == "" {
		return errors.New("only a run's spend names a work item")
	}
	return nil
}

// amountProblem reports an amount that disagrees with its classification. The
// two halves have to say one thing: an unknown amount that carried a number
// would be added up, and a known amount with no reason to doubt it that also
// carried one would leave a reader deciding which to believe.
func (s Spend) amountProblem() error {
	if !s.Classification.Valid() {
		return fmt.Errorf("classification %q must be %q or %q", s.Classification, SpendKnown, SpendUnknown)
	}
	switch s.Classification {
	case SpendKnown:
		if s.AmountUSD < 0 {
			return errors.New("a known amount is not negative")
		}
		if strings.TrimSpace(s.Unknown) != "" {
			return errors.New("a known amount does not also say why it is unknown")
		}
		// The reported figure is the session's running total and the amount beside
		// it is what this invocation added to it, so the amount can equal it and
		// never exceed it. A line where it does is a correction applied backwards,
		// which would understate the session it came from and overstate this
		// invocation at once.
		if s.ReportedTotalUSD != 0 && s.ReportedTotalUSD < s.AmountUSD {
			return errors.New("a reported session total is not below the amount this invocation is recorded at")
		}
		return nil
	default:
		if s.ReportedTotalUSD != 0 {
			return errors.New("an invocation nobody was told the cost of has no reported session total either")
		}
		if s.AmountUSD != 0 {
			return errors.New("an unknown amount is not a number, so it is recorded as zero and read by its classification")
		}
		switch unknown := strings.TrimSpace(s.Unknown); {
		case unknown == "":
			return errors.New("an unknown amount says why nobody knows it")
		case len(unknown) > MaxSpendUnknownBytes:
			return fmt.Errorf("unknown is %d bytes, limit is %d", len(unknown), MaxSpendUnknownBytes)
		}
		return nil
	}
}

func joinSpendPhases() string {
	names := make([]string, 0, len(SpendPhases()))
	for _, phase := range SpendPhases() {
		names = append(names, string(phase))
	}
	return strings.Join(names, ", ")
}

// SpendStore is one product's cost log, in the same operating-system state root
// as runs and conversations and beside them rather than inside either. That
// placement is what the log is for: a spend outlives the run or conversation
// that made it, and a run whose state has been cleaned up still spent what it
// spent.
type SpendStore struct {
	root      string
	productID domain.ProductID
}

func NewSpendStore(root string, productID domain.ProductID) (*SpendStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &SpendStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID)),
		productID: productID,
	}, nil
}

func (s *SpendStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the recorded spends
// actually are.
func (s *SpendStore) Path() string { return filepath.Join(s.root, "spend.jsonl") }

// Append records one spend durably. It is an append rather than a rewrite
// because a line is written once and never revised, and because several
// processes spend at once: two runs and a conversation all record here, and none
// of them may overwrite another's line.
func (s *SpendStore) Append(line Spend) error {
	if err := s.validate(line); err != nil {
		return err
	}
	encoded, err := encodeSpend(line)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedSpendBytes {
		return fmt.Errorf("encoded spend is %d bytes, limit is %d", len(encoded), maxEncodedSpendBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create spend directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect spend log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open spend log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append spend: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append spend: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync spend log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close spend log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded spend in the order it was recorded. A log that
// does not exist yet is a product nothing has been spent on, which is not a
// failure to read.
//
// It returns the lines and nothing derived from them. What they add up to is the
// operator's question and belongs to whoever asks it.
//
// One thing it does derive, and it is a correction rather than an aggregate: a
// line written before an amount was an invocation's own cost carries the
// provider's reported figure, which on a resumed session is the session's
// running total. Those lines are re-derived here by the same rule a new one is
// written by, from the session identifier every line already carries, with the
// figure as recorded kept in ReportedTotalUSD -- so the file on disk stays what
// was written and every reader of it sees one kind of amount. A line that
// already carries a reported total was corrected when it was written and is
// returned as it stands.
func (s *SpendStore) List() ([]Spend, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open spend log: %w", err)
	}
	defer file.Close()

	var (
		lines []Spend
		costs SessionCosts
	)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedSpendBytes)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var decoded Spend
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil, fmt.Errorf("decode spend log: %w", err)
		}
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode spend log: %w", err)
		}
		lines = append(lines, correctSpend(&costs, decoded))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read spend log: %w", err)
	}
	return lines, nil
}

// correctSpend is one recorded line as its amount should now be read. A line
// that already says what the provider reported was corrected when it was
// written and only advances the session; a line that does not is corrected here
// and keeps the figure it was recorded with.
//
// An unknown line advances nothing. Its amount is a zero standing for an
// invocation nobody was told the price of, and treating that as a session total
// would make the session's next invocation cost its whole running total again.
func correctSpend(costs *SessionCosts, line Spend) Spend {
	if !line.Known() {
		return line
	}
	reported := line.ReportedTotal()
	own := costs.Own(line.SessionID, reported)
	if line.ReportedTotalUSD != 0 || own == reported {
		return line
	}
	line.AmountUSD = own
	line.ReportedTotalUSD = reported
	return line
}

// ReportedSessionTotal is the last figure the provider reported for a session,
// and whether this log has recorded one at all. It is what the meter prices the
// session's next invocation against, and it is asked of the log rather than
// carried in memory because a session outlives the process that opened it: a
// management conversation resumes one session across days of separate
// invocations, and a run's repair attempts resume the developer's across
// processes.
//
// A session nothing has recorded is not an error and not a zero: the two are
// opposite facts to the rule that prices the next invocation, so the answer says
// which. Lines that could not be read fail rather than being skipped, for the
// reason List fails on one -- a total taken over a log with a hole in it is a
// figure nobody can attribute.
func (s *SpendStore) ReportedSessionTotal(sessionID string) (float64, bool, error) {
	session := strings.TrimSpace(sessionID)
	if session == "" {
		return 0, false, nil
	}
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("open spend log: %w", err)
	}
	defer file.Close()

	var (
		total float64
		found bool
	)
	needle := []byte(session)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedSpendBytes)
	for scanner.Scan() {
		// The cheap test over-matches -- a session identifier could appear in any
		// string on the line -- and the decoded line rejects the rest. What it must
		// never do is skip a line that names the session, which is why it matches
		// the identifier anywhere rather than in a key it assumes the shape of.
		line := scanner.Bytes()
		if !bytes.Contains(line, needle) {
			continue
		}
		var decoded Spend
		if err := json.Unmarshal(line, &decoded); err != nil {
			return 0, false, fmt.Errorf("decode spend log: %w", err)
		}
		if decoded.SessionID != session || !decoded.Known() {
			continue
		}
		total = decoded.ReportedTotal()
		found = true
	}
	if err := scanner.Err(); err != nil {
		return 0, false, fmt.Errorf("read spend log: %w", err)
	}
	return total, found, nil
}

func encodeSpend(line Spend) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(line); err != nil {
		return nil, fmt.Errorf("encode spend: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *SpendStore) validate(line Spend) error {
	if line.ProductID != s.productID {
		return fmt.Errorf("spend product %q does not match store product %q", line.ProductID, s.productID)
	}
	return line.Validate()
}
