package runstate

// A program manager's lane report: where its lane stands, rewritten whole on
// every pass, and a bounded history of what it said before.
//
// `docs/designs/program-manager.md`, under "The lane report", is the whole of
// the rule, and three of its sentences decide the shape here. The report is
// rewritten whole rather than amended, so what is on the disk is always one
// report a single turn wrote and never a patchwork of several. It lives under
// the state root, beside a history of the last fifty versions, each stamped with
// the pass and the conversation turn that wrote it — and never in the
// repository, which is public, and never in the memory store, which is
// append-only judgement rather than a surface. And it is bounded, redacted
// before it is written, and refused whole where it is malformed, with the
// previous report left standing.
//
// The redaction happens here rather than at the caller, for the reason the
// memory store's does: a field of the store rather than an argument to the
// write is a path onto the disk nothing can skip.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// LaneReportSchemaVersion is versioned on its own, because a lane report is
// neither a conversation nor a memory: it has no turns of its own and it is
// replaced rather than accumulated.
const LaneReportSchemaVersion = 1

// MaxLaneReportBytes bounds the report itself — its summary, what remains, and
// its blockers, as JSON — after redaction, so a report that fits is one the
// store will hold byte for byte. The design fixes it at 16 KiB: an executive
// summary longer than that is not a summary, and a report read on a card beside
// every other instance's must stay readable there.
const MaxLaneReportBytes = 16 << 10

// LaneReportHistory is how many versions are kept, the newest included. The
// fifty-first drops the oldest.
const LaneReportHistory = 50

// maxLaneReportLineBytes bounds one short field that is held to a line: a
// citation, and the pass a version names.
const maxLaneReportLineBytes = 200

// maxEncodedLaneReportVersionBytes bounds one stored version, stamp included.
// The report is bounded above and a stamp is a few hundred bytes, so this is
// generous; the reader and the writer share it, so a version that was written is
// always one that can be read back.
const maxEncodedLaneReportVersionBytes = 64 << 10

// LaneReportMoverCheck refuses a token that is not a mover a blocker may wait
// on. The vocabulary is the read model's, and it stays there: this package holds
// no copy of it, because the read model reads this record and not the other way
// round. The store is handed the read model's own check when it is built, which
// is readmodel.CheckLaneReportMover, so what a report may name is decided in one
// place.
type LaneReportMoverCheck func(token string) error

// LaneReportBlocker is one thing holding the lane back: what it is, who has to
// move for it to clear, and the record the instance already raised about it.
type LaneReportBlocker struct {
	What string `json:"what"`
	// WaitingOn is a mover from the read model's vocabulary, narrowed to those a
	// program manager can be waiting on.
	WaitingOn string `json:"waiting_on"`
	// Cites is the identifier of the request, report, amendment, or exchange the
	// instance raised about it. What the read model makes of the citation — an
	// open ask of the instance's own, or a claim that resolves to nothing — is the
	// status derivation's, not this record's; here it is held to being an
	// identifier at all.
	Cites string `json:"cites"`
}

// LaneReportContent is the report as the role wrote it: an executive summary,
// what remains, and what is blocking.
type LaneReportContent struct {
	Summary   string              `json:"summary"`
	Remaining []string            `json:"remaining"`
	Blockers  []LaneReportBlocker `json:"blockers"`
}

// Validate reports every contract violation in the report at once. A report is
// held to all of it or refused whole, so it says everything that is wrong rather
// than the first thing.
func (c LaneReportContent) Validate() error {
	var problems []error
	if strings.TrimSpace(c.Summary) == "" {
		problems = append(problems, errors.New("summary is required: it is where the lane stands"))
	}
	if c.Remaining == nil {
		problems = append(problems, errors.New("remaining is required, as a list; an empty one says nothing remains"))
	}
	for index, entry := range c.Remaining {
		if strings.TrimSpace(entry) == "" {
			problems = append(problems, fmt.Errorf("remaining[%d] is empty", index))
		}
	}
	if c.Blockers == nil {
		problems = append(problems, errors.New("blockers is required, as a list; an empty one says nothing is blocking"))
	}
	for index, blocker := range c.Blockers {
		if err := blocker.validate(); err != nil {
			problems = append(problems, fmt.Errorf("blockers[%d]: %w", index, err))
		}
	}
	if encoded, err := json.Marshal(c); err != nil {
		problems = append(problems, fmt.Errorf("encode the report: %w", err))
	} else if len(encoded) > MaxLaneReportBytes {
		problems = append(problems, fmt.Errorf("the report is %d bytes, limit is %d", len(encoded), MaxLaneReportBytes))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid lane report: %w", err)
	}
	return nil
}

func (b LaneReportBlocker) validate() error {
	var problems []error
	if strings.TrimSpace(b.What) == "" {
		problems = append(problems, errors.New("what is required"))
	}
	// Which movers are admitted is the read model's to say, and the store asks it
	// as it writes; here the field is held to being a token at all.
	if err := validateLaneReportLine("waiting_on", b.WaitingOn); err != nil {
		problems = append(problems, err)
	}
	if err := validateLaneReportLine("cites", b.Cites); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// validateLaneReportLine holds an identifier-shaped field to one short line.
func validateLaneReportLine(name, value string) error {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		return fmt.Errorf("%s is required", name)
	case trimmed != value:
		return fmt.Errorf("%s carries surrounding space", name)
	case len(trimmed) > maxLaneReportLineBytes:
		return fmt.Errorf("%s is %d bytes, limit is %d", name, len(trimmed), maxLaneReportLineBytes)
	case strings.ContainsFunc(trimmed, unicode.IsSpace):
		return fmt.Errorf("%s is an identifier, so it carries no spaces", name)
	}
	return nil
}

// LaneReportStamp is what wrote one version: the conversation and its turn, and
// the pass where a pass was what woke the turn. An operator's own turn in the
// instance's conversation writes a version with no pass, and says so by leaving
// it out rather than naming a pass that did not happen.
type LaneReportStamp struct {
	Pass           string `json:"pass,omitempty"`
	ConversationID string `json:"conversation_id"`
	Turn           int    `json:"turn"`
}

func (s LaneReportStamp) validate() error {
	var problems []error
	if s.Pass != "" {
		if err := validateLaneReportPass(s.Pass); err != nil {
			problems = append(problems, err)
		}
	}
	if !conversationIDPattern.MatchString(s.ConversationID) {
		problems = append(problems, errors.New("the stamp names no conversation"))
	}
	if s.Turn < 1 {
		problems = append(problems, errors.New("a conversation turn is numbered from one"))
	}
	return errors.Join(problems...)
}

// validateLaneReportPass holds a pass to one readable line. A pass is named by
// the recurring task that fired it and which firing it was, so it is not an
// identifier in the strict sense, and what it is held to is legibility.
func validateLaneReportPass(pass string) error {
	trimmed := strings.TrimSpace(pass)
	if trimmed == "" || trimmed != pass {
		return errors.New("the pass is named, with no surrounding space")
	}
	if len(trimmed) > maxLaneReportLineBytes {
		return fmt.Errorf("the pass is %d bytes, limit is %d", len(trimmed), maxLaneReportLineBytes)
	}
	if strings.ContainsFunc(trimmed, unicode.IsControl) {
		return errors.New("the pass is one line, so it carries no control characters")
	}
	return nil
}

// LaneReport is one version of one instance's report, as it is stored: the
// report itself, which version it is, and what wrote it.
type LaneReport struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Agent is the instance, which is an agent filling the program manager role.
	// Two instances are two agents, and each has its own report.
	Agent string `json:"agent"`
	// Version is where this report falls in the instance's history, from one. The
	// store assigns it, so it goes on counting past the fifty the history keeps.
	Version    int               `json:"version"`
	Report     LaneReportContent `json:"report"`
	Stamp      LaneReportStamp   `json:"stamp"`
	RecordedAt time.Time         `json:"recorded_at"`
}

// Validate reports every contract violation in a stored version at once.
func (r LaneReport) Validate() error {
	var problems []error
	if r.SchemaVersion != LaneReportSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", LaneReportSchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("agent", r.Agent); err != nil {
		problems = append(problems, err)
	}
	if r.Version < 1 {
		problems = append(problems, errors.New("a version is numbered from one"))
	}
	if err := r.Report.Validate(); err != nil {
		problems = append(problems, err)
	}
	if err := r.Stamp.validate(); err != nil {
		problems = append(problems, err)
	}
	if r.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid lane report version: %w", err)
	}
	return nil
}

// LaneReportStore keeps every program manager instance's report, one directory
// per instance under the product: `report.json`, the current version whole, and
// `history.jsonl`, the last fifty versions oldest first.
type LaneReportStore struct {
	root      string
	productID domain.ProductID
	// redactor is applied to everything the role wrote before it is measured or
	// stored. It is a field of the store so there is no write that skips it.
	redactor execution.Redactor
	// movers is the read model's check on what a blocker waits on, asked of
	// every blocker before anything is written.
	movers LaneReportMoverCheck
}

// NewLaneReportStore builds the store for one product. The values are the ones
// every durable record in this harness is redacted against; a store built with
// none redacts nothing, which is what a reader wants.
//
// The mover check is required: a store that could not say which movers a
// blocker may wait on would write whatever it was handed.
func NewLaneReportStore(root string, productID domain.ProductID, movers LaneReportMoverCheck, redactValues ...string) (*LaneReportStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if movers == nil {
		return nil, errors.New("a lane report store is built with the read model's check on what a blocker may wait on")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &LaneReportStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "program-managers"),
		productID: productID,
		redactor:  execution.NewRedactor(redactValues...),
		movers:    movers,
	}, nil
}

func (s *LaneReportStore) Root() string { return s.root }

// ReportPath names one instance's current report, so a failure can say where it
// is and a reader can be pointed at it.
func (s *LaneReportStore) ReportPath(agent string) string {
	return filepath.Join(s.root, agent, laneReportFile)
}

const (
	laneReportFile        = "report.json"
	laneReportHistoryFile = "history.jsonl"
	laneReportLockFile    = ".report.lock"
)

// Write replaces one instance's report and adds it to the history, and returns
// the version as it was stored: redacted, numbered, and stamped.
//
// It is refused whole before anything is written where the report is malformed
// or past its bound once redacted, so a refusal leaves the previous report
// exactly as it stood. The history is written before the report, so a process
// that dies between the two leaves a history one version ahead of the report
// rather than a report the history never recorded; the next write puts them back
// in step.
func (s *LaneReportStore) Write(ctx context.Context, version LaneReport) (LaneReport, error) {
	if version.Version != 0 {
		return LaneReport{}, fmt.Errorf("the store numbers a lane report, so it arrived already numbered %d", version.Version)
	}
	if version.ProductID != s.productID {
		return LaneReport{}, fmt.Errorf("lane report product %q does not match store product %q", version.ProductID, s.productID)
	}
	if err := domain.ValidateIdentifier("agent", version.Agent); err != nil {
		return LaneReport{}, err
	}
	version.SchemaVersion = LaneReportSchemaVersion
	version.Report = s.redact(version.Report)
	if version.RecordedAt.IsZero() {
		version.RecordedAt = time.Now()
	}
	version.RecordedAt = version.RecordedAt.UTC()
	// Checked before the lock and the history are touched, against everything
	// but the number, so a malformed report costs nothing and names every
	// problem it has. The number is the one thing only the history can supply.
	numbered := version
	numbered.Version = 1
	if err := numbered.Validate(); err != nil {
		return LaneReport{}, err
	}
	var refused []error
	for index, blocker := range version.Report.Blockers {
		if err := s.movers(blocker.WaitingOn); err != nil {
			refused = append(refused, fmt.Errorf("blockers[%d]: %w", index, err))
		}
	}
	if err := errors.Join(refused...); err != nil {
		return LaneReport{}, fmt.Errorf("invalid lane report: %w", err)
	}

	release, err := s.lock(ctx, version.Agent)
	if err != nil {
		return LaneReport{}, err
	}
	defer release()

	history, err := s.readHistory(version.Agent, true)
	if err != nil {
		return LaneReport{}, err
	}
	version.Version = 1
	if len(history) > 0 {
		version.Version = history[len(history)-1].Version + 1
	}
	history = append(history, version)
	if len(history) > LaneReportHistory {
		history = history[len(history)-LaneReportHistory:]
	}

	var lines bytes.Buffer
	for _, kept := range history {
		encoded, err := encodeLaneReport(kept)
		if err != nil {
			return LaneReport{}, err
		}
		lines.Write(encoded)
	}
	current, err := encodeLaneReport(version)
	if err != nil {
		return LaneReport{}, err
	}
	confined, err := repowrite.NewRoot(filepath.Dir(s.root))
	if err != nil {
		return LaneReport{}, fmt.Errorf("resolve the product's state directory: %w", err)
	}
	directory := filepath.Join(filepath.Base(s.root), version.Agent)
	if _, err := confined.WriteFile(filepath.Join(directory, laneReportHistoryFile), lines.Bytes()); err != nil {
		return LaneReport{}, fmt.Errorf("record the %s lane report history: %w", version.Agent, err)
	}
	if _, err := confined.WriteFile(filepath.Join(directory, laneReportFile), current); err != nil {
		return LaneReport{}, fmt.Errorf("record the %s lane report: %w", version.Agent, err)
	}
	return version, nil
}

// redact applies the store's redactor to every piece of prose the role wrote.
// The mover is a closed token and is left alone; a citation is an identifier the
// role chose and could carry anything, so it is redacted like the prose.
func (s *LaneReportStore) redact(content LaneReportContent) LaneReportContent {
	redacted := LaneReportContent{Summary: s.redactor.Redact(content.Summary)}
	if content.Remaining != nil {
		redacted.Remaining = make([]string, len(content.Remaining))
		for index, entry := range content.Remaining {
			redacted.Remaining[index] = s.redactor.Redact(entry)
		}
	}
	if content.Blockers != nil {
		redacted.Blockers = make([]LaneReportBlocker, len(content.Blockers))
		for index, blocker := range content.Blockers {
			redacted.Blockers[index] = LaneReportBlocker{
				What:      s.redactor.Redact(blocker.What),
				WaitingOn: blocker.WaitingOn,
				Cites:     s.redactor.Redact(blocker.Cites),
			}
		}
	}
	return redacted
}

// Current is one instance's report as it stands, and whether it has one. An
// instance that has never written a report is the ordinary answer rather than a
// failure; a report that will not decode is a failure, because a surface that
// showed it as absent would show a lane that reported as one that never had.
func (s *LaneReportStore) Current(agent string) (LaneReport, bool, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return LaneReport{}, false, err
	}
	stored, err := os.ReadFile(s.ReportPath(agent))
	if errors.Is(err, os.ErrNotExist) {
		return LaneReport{}, false, nil
	}
	if err != nil {
		return LaneReport{}, false, fmt.Errorf("read the %s lane report: %w", agent, err)
	}
	report, err := s.decode(agent, bytes.TrimSpace(stored), false)
	if err != nil {
		return LaneReport{}, false, fmt.Errorf("decode the %s lane report: %w", agent, err)
	}
	return report, true, nil
}

// History is one instance's kept versions, oldest first, the current one last.
func (s *LaneReportStore) History(agent string) ([]LaneReport, error) {
	if err := domain.ValidateIdentifier("agent", agent); err != nil {
		return nil, err
	}
	return s.readHistory(agent, false)
}

// readHistory reads the kept versions. The writer reads them strictly, because
// it numbers the next version from them and writes them back: a field it stepped
// over would be one it then silently dropped. A reader reads them tolerantly, as
// every listing in this package does, so a version a newer build wrote is still
// shown.
func (s *LaneReportStore) readHistory(agent string, strict bool) ([]LaneReport, error) {
	path := filepath.Join(s.root, agent, laneReportHistoryFile)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the %s lane report history: %w", agent, err)
	}
	defer file.Close()
	var history []LaneReport
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), maxEncodedLaneReportVersionBytes)
	line := 0
	for scanner.Scan() {
		line++
		text := bytes.TrimSpace(scanner.Bytes())
		if len(text) == 0 {
			continue
		}
		version, err := s.decode(agent, text, strict)
		if err != nil {
			return nil, fmt.Errorf("the %s lane report history line %d will not decode: %w", agent, line, err)
		}
		if len(history) > 0 && version.Version <= history[len(history)-1].Version {
			return nil, fmt.Errorf("the %s lane report history line %d is version %d, which does not follow version %d",
				agent, line, version.Version, history[len(history)-1].Version)
		}
		history = append(history, version)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the %s lane report history: %w", agent, err)
	}
	return history, nil
}

// decode is one stored version. strict is the writer's door, which refuses a
// field it does not know; the tolerant door steps over one and says so.
func (s *LaneReportStore) decode(agent string, encoded []byte, strict bool) (LaneReport, error) {
	var version LaneReport
	if strict {
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&version); err != nil {
			return LaneReport{}, err
		}
		if err := ensureJSONEOF(decoder); err != nil {
			return LaneReport{}, err
		}
	} else {
		unknown, err := decodeTolerating(encoded, &version)
		if err != nil {
			return LaneReport{}, err
		}
		noteUnknownFields("lane report", unknown)
	}
	if err := version.Validate(); err != nil {
		return LaneReport{}, err
	}
	if version.Agent != agent || version.ProductID != s.productID {
		return LaneReport{}, fmt.Errorf("the version belongs to agent %s of product %s", version.Agent, version.ProductID)
	}
	return version, nil
}

// encodeLaneReport is one version as one line, newline included.
func encodeLaneReport(version LaneReport) ([]byte, error) {
	encoded, err := json.Marshal(version)
	if err != nil {
		return nil, fmt.Errorf("encode the lane report: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxEncodedLaneReportVersionBytes {
		return nil, fmt.Errorf("the encoded lane report is %d bytes, limit is %d", len(encoded), maxEncodedLaneReportVersionBytes)
	}
	return encoded, nil
}

// lock serializes one instance's read of its history and the write that
// follows, across every process, so two turns finishing at once cannot number
// the same version twice or drop each other's.
func (s *LaneReportStore) lock(ctx context.Context, agent string) (func(), error) {
	directory := filepath.Join(s.root, agent)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the %s lane report directory: %w", agent, err)
	}
	file, err := os.OpenFile(filepath.Join(directory, laneReportLockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the %s lane report lock: %w", agent, err)
	}
	if err := lockStateFile(ctx, file); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock the %s lane report: %w", agent, err)
	}
	return func() { _ = releaseStateFile(file) }, nil
}
