package runstate

// A provider refusing the harness for want of capacity, recorded wherever it
// happened rather than only inside a run.
//
// A run that meets an exhausted usage limit already writes one down: the pause
// it records carries the limit, the deadline, and the item that is waiting, and
// the sink reads it off the run's own state. Nothing else in the harness had
// that. A conversation turn and an independent branch review are provider
// invocations with no run record to cross, so a limit that stopped one of them
// failed at whatever terminal asked for it and left the durable record silent —
// which is the same silence a healthy queue makes, at the exact moment somebody
// most needs to be told the difference.
//
// So the refusal says itself, in the shape every other log here is written in:
// an append-only log per product, one entry per refusal, carrying what was
// stopped and — where the provider named one — when it lifts. It is deliberately
// not the run pause: a pause is a run's own state, revised as the run serves it,
// and this is a moment that happened and is never revised.

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
)

// UsageLimitSchemaVersion is 1 and has never changed.
const UsageLimitSchemaVersion = 1

// MaxUsageLimitWaitingBytes bounds what one refusal says is waiting on it. It
// is a phrase somebody reads in the morning rather than a record they study, so
// it is bounded like a watch transition's reason is.
const MaxUsageLimitWaitingBytes = 4 << 10

// maxEncodedUsageLimitBytes bounds one encoded refusal, including the trailing
// newline. The writer and the reader share it, so a refusal that was written is
// always one that can be read back.
const maxEncodedUsageLimitBytes = 16 << 10

// UsageLimitExhaustion is one moment a provider declined the harness for want
// of capacity. It carries what the refusal stopped rather than what the harness
// did about it: what an operator does with hours of silence depends entirely on
// which of their work is inside it.
type UsageLimitExhaustion struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	At            time.Time        `json:"at"`
	// Waiting names what the refusal stopped, in words: a conversation with a
	// role, a review of a branch. It is prose because the things that can be
	// waiting do not share a shape, and because the answer an operator needs is
	// to "what is not happening", which is a sentence rather than an identifier.
	Waiting string `json:"waiting"`
	// Kind is the provider's own name for the exhausted limit, carried as
	// evidence rather than interpreted. Empty where the provider named none.
	Kind string `json:"kind,omitempty"`
	// ResetsAt is when the provider said the limit lifts. It is absent where the
	// provider named no reset time, which is a different fact from a wait of
	// unknown length: the harness asks again rather than being told when.
	ResetsAt *time.Time `json:"resets_at,omitempty"`
	// The correlation identifiers, so a refusal can be read back to what it
	// stopped. Both are optional, because a refusal that stopped something
	// belonging to no work item and no conversation is still one somebody has to
	// be told about.
	WorkItemID     string `json:"work_item_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	// Model is the model selector the refused invocation asked for. It is
	// optional because most refusals are recorded by processes that have nothing
	// to do about which model was refused, and it is here because failover does:
	// a window is a fact about one model rather than about the account, so a
	// refusal that does not say which model was refused cannot be read back as a
	// window that has since reopened.
	Model string `json:"model,omitempty"`
	// AccountAlias is the configured account the refused invocation ran under,
	// and empty where the process that recorded it did not say — which is every
	// refusal recorded before the account was carried. It is what a served turn
	// is matched against: a window is closed on one account and one model, and a
	// turn served on another account says nothing about it.
	AccountAlias string `json:"account,omitempty"`
	// ServedBy is the permitted alternate that took the turn instead, and empty
	// where nothing did. It is what makes this entry a substitution rather than a
	// stoppage: the same refusal happened either way, and what an operator needs
	// to know is whether the work carried on.
	ServedBy string `json:"served_by,omitempty"`
	// Provider and ServedByProvider are the providers the two models above were
	// asked of, and are written only where a substitution crossed from one to the
	// other. Both are empty on every entry that stayed on one provider, which is
	// every entry written before an alternate could name a second one.
	//
	// They are here because a turn that crossed providers is not the same news as
	// a turn that changed model: the second provider held no session, so its
	// context was rebuilt from the durable record rather than resumed. An operator
	// reading a substitution needs to know which of the two happened, and the model
	// selectors alone cannot say — two providers can spell one model name.
	Provider         domain.Backend `json:"provider,omitempty"`
	ServedByProvider domain.Backend `json:"served_by_provider,omitempty"`
	// Substitution is why the turn was moved off the model above. It is empty on
	// every entry that is not a substitution at all, and empty on one written
	// before there was more than one reason — which reads back as capacity,
	// because an exhausted window was the only reason a turn moved until a pinned
	// version could be one the provider has not got.
	//
	// It is here rather than inferred from the other fields because the two
	// reasons are told apart by nothing else on the record and they must not be
	// read as each other: a window closes and reopens on the provider's clock, and
	// a model the provider has not got is not waiting for anything. Whoever reads
	// this log back to decide what to ask for next turn reads this field to know
	// which question the entry answers.
	Substitution SubstitutionReason `json:"substitution,omitempty"`
}

// SubstitutionReason is why a turn was served by a model other than the one it
// asked for. The set is closed: a record naming anything else is refused where
// it is written rather than met later as an entry nothing knows what to do with.
type SubstitutionReason string

const (
	// SubstitutedForCapacity is the model's usage window closed, which is what
	// failover answers. It is what an entry with no reason recorded means.
	SubstitutedForCapacity SubstitutionReason = "capacity"
	// SubstitutedForAvailability is the provider not having the model at all,
	// which is what a pinned version falling back to its family answers. Nothing
	// about the account is exhausted and nothing is waiting for a window.
	SubstitutedForAvailability SubstitutionReason = "availability"
	// SubstitutedForExecutable is the provider's executable not being found, or
	// not starting, in the environment the turn was made in, which is what a
	// configured alternate on another provider answers. Nothing about the account
	// or the model is exhausted, and no reset time exists: the executable is
	// looked for again once the harness's unknown-reset interval has passed.
	SubstitutedForExecutable SubstitutionReason = "executable"
)

// SubstitutionReasons is every reason a substitution may name, for a refusal
// that shows the choices.
var SubstitutionReasons = []SubstitutionReason{SubstitutedForCapacity, SubstitutedForAvailability, SubstitutedForExecutable}

// Reason is why this entry's turn moved, with an entry that names none reading
// as capacity — the only reason there was when such an entry could be written.
// It answers the empty reason for every caller so none of them has to remember
// which vintage of the log it is reading.
func (e UsageLimitExhaustion) Reason() SubstitutionReason {
	if e.Substitution == "" {
		return SubstitutedForCapacity
	}
	return e.Substitution
}

func (e UsageLimitExhaustion) Validate() error {
	var problems []error
	if e.SchemaVersion != UsageLimitSchemaVersion {
		problems = append(problems, fmt.Errorf("usage limit schema version %d is not supported", e.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if e.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if strings.TrimSpace(e.Waiting) == "" {
		problems = append(problems, errors.New("waiting is required: a refusal nobody can say what it stopped is not worth recording"))
	}
	if len(e.Waiting) > MaxUsageLimitWaitingBytes {
		problems = append(problems, fmt.Errorf("what is waiting is %d bytes, which exceeds the %d byte bound", len(e.Waiting), MaxUsageLimitWaitingBytes))
	}
	if e.ResetsAt != nil && e.ResetsAt.IsZero() {
		problems = append(problems, errors.New("resets_at is present but names no moment; a provider that named none records none"))
	}
	// A substitution says what was refused as well as what served, because the
	// pair is the whole of what it claims: an alternate named beside no model is
	// a record of a turn being moved off nothing.
	if strings.TrimSpace(e.ServedBy) != "" && strings.TrimSpace(e.Model) == "" {
		problems = append(problems, errors.New("served_by names an alternate and model names nothing; a substitution says which model was refused"))
	}
	// The same selector asked of two providers is a real substitution — the turn
	// moved endpoints, and two providers can spell one model name — so what has to
	// differ is the pair rather than the model alone.
	if strings.TrimSpace(e.ServedBy) != "" && strings.TrimSpace(e.ServedBy) == strings.TrimSpace(e.Model) &&
		strings.TrimSpace(string(e.Provider)) == strings.TrimSpace(string(e.ServedByProvider)) {
		problems = append(problems, errors.New("served_by and model name the same model on the same provider; a substitution is a turn served by the endpoint that was not refused"))
	}
	// A reason belongs to a substitution and to nothing else: an entry that names
	// why a turn moved without naming what moved it is a reason for something
	// that did not happen.
	if e.Substitution != "" && strings.TrimSpace(e.ServedBy) == "" {
		problems = append(problems, fmt.Errorf("substitution %q names why a turn was moved and served_by names nothing that took it", e.Substitution))
	}
	if e.Substitution != "" && !e.Substitution.known() {
		problems = append(problems, fmt.Errorf("substitution %q is not one of %s", e.Substitution, describeSubstitutionReasons()))
	}
	// An availability substitution is the provider not having a model rather than
	// a window that will lift, so a reset time on one describes a wait that is not
	// happening. It is refused rather than ignored, because a reader that took it
	// at its word would hold a pinned version unasked-for until a moment nothing
	// was ever going to change at.
	if e.Substitution == SubstitutedForAvailability && e.ResetsAt != nil {
		problems = append(problems, errors.New("an availability substitution names a reset time; a model the provider has not got is not waiting for a window"))
	}
	// The same holds of an executable this environment could not run: nothing
	// quoted a time it would be back.
	if e.Substitution == SubstitutedForExecutable && e.ResetsAt != nil {
		problems = append(problems, errors.New("an executable substitution names a reset time; a provider executable that could not run is not waiting for a window"))
	}
	// A crossing is stated as a pair or not at all. One provider named without the
	// other is a record saying a turn moved between one place and nowhere, which
	// nothing reading it back could act on.
	crossed := strings.TrimSpace(string(e.Provider)) != "" || strings.TrimSpace(string(e.ServedByProvider)) != ""
	if crossed {
		if err := domain.ValidateIdentifier("provider", string(e.Provider)); err != nil {
			problems = append(problems, err)
		}
		if err := domain.ValidateIdentifier("served_by provider", string(e.ServedByProvider)); err != nil {
			problems = append(problems, err)
		}
		if strings.TrimSpace(e.ServedBy) == "" {
			problems = append(problems, errors.New("a provider pair names where a turn crossed to and served_by names nothing that took it"))
		}
		if e.Provider == e.ServedByProvider {
			problems = append(problems, errors.New("provider and served_by provider name the same provider; a crossing is a turn served by the provider that did not refuse it"))
		}
	}
	return errors.Join(problems...)
}

func (r SubstitutionReason) known() bool {
	for _, named := range SubstitutionReasons {
		if r == named {
			return true
		}
	}
	return false
}

func describeSubstitutionReasons() string {
	named := make([]string, 0, len(SubstitutionReasons))
	for _, reason := range SubstitutionReasons {
		named = append(named, string(reason))
	}
	return strings.Join(named, ", ")
}

// Substituted reports a refusal a permitted alternate served through. It is the
// difference between an hour of silence and an hour of work carrying on under a
// different model, which is the whole of what separates a warning here from a
// note.
func (e UsageLimitExhaustion) Substituted() bool {
	return strings.TrimSpace(e.ServedBy) != ""
}

// CrossedProviders reports a substitution that left the provider it was refused
// by, which is the one that rebuilt its context instead of resuming a session.
func (e UsageLimitExhaustion) CrossedProviders() bool {
	return strings.TrimSpace(string(e.Provider)) != "" &&
		strings.TrimSpace(string(e.ServedByProvider)) != "" &&
		e.Provider != e.ServedByProvider
}

// DescribeModel and DescribeServedBy name the two models a substitution moved
// between, each qualified by its provider where the turn crossed from one to the
// other. Within one provider they are the selectors themselves, which is what
// every reader has always been shown.
//
// The qualification is here rather than in whatever displays them so that one
// derivation answers for every surface: two providers can spell one model name,
// and a reader shown "opus rather than opus" would be shown a substitution that
// reads as no substitution at all.
func (e UsageLimitExhaustion) DescribeModel() string {
	if !e.CrossedProviders() {
		return strings.TrimSpace(e.Model)
	}
	return DescribeServingModel(e.Provider, e.Model)
}

func (e UsageLimitExhaustion) DescribeServedBy() string {
	if !e.CrossedProviders() {
		return strings.TrimSpace(e.ServedBy)
	}
	return DescribeServingModel(e.ServedByProvider, e.ServedBy)
}

// DescribeServingModel names one model selector qualified by the provider it was
// asked of. It is exported because the same phrase is owed to every surface that
// says a turn was served somewhere other than where it was configured — the
// conversation's own evidence line as well as this record — and two spellings of
// it would be two answers to one question.
//
// A provider nobody named leaves the selector as it is, which is the answer for
// every turn that never left the provider it was configured for.
func DescribeServingModel(provider domain.Backend, model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" || strings.TrimSpace(string(provider)) == "" {
		return trimmed
	}
	return string(provider) + "'s " + trimmed
}

// WindowClosed reports a refusal still standing at the given moment.
//
// A reset time the provider named and that is in the future is the window, and
// the refusal stands until it. Where the provider named none — and where it
// named one that was already past when it refused, which describes no wait at
// all — what stands in for it is unknownResetPause, the caller's configured
// interval between probes. That is the same substitution the run path makes for
// the same case: a limit reported without a reset is not the absence of
// capacity, it is capacity nobody was given a deadline for, so the harness waits
// its configured interval and asks again rather than either guessing a deadline
// or asking on every turn.
//
// A caller with no interval to offer passes zero, and then only a named reset
// time can make a refusal stand — which is the honest answer for a caller that
// has no polling discipline of its own to apply.
//
// An availability substitution never names a reset time, so it always stands for
// the caller's interval and no longer. That is the same answer for the same
// reason: a provider that has not got a model has quoted no deadline for getting
// it, and a version that arrives — or comes back — is one the harness finds by
// asking again rather than by being told when.
func (e UsageLimitExhaustion) WindowClosed(at time.Time, unknownResetPause time.Duration) bool {
	standsUntil := e.At.Add(unknownResetPause)
	if e.ResetsAt != nil && e.ResetsAt.After(e.At) {
		standsUntil = *e.ResetsAt
	}
	return standsUntil.After(at)
}

// Describe says what the refusal was, as the object of "waiting out". It is the
// same sentence a paused run's cause is written in, taken from there rather than
// restated, so one exhausted limit does not read two ways depending on which
// process met it.
//
// An availability substitution is the exception, and it is a different sentence
// because it is a different fact: nothing is being waited out, so describing it
// as an exhausted limit would tell an operator to expect a window that is never
// going to lift.
func (e UsageLimitExhaustion) Describe() string {
	if e.Substituted() && e.Reason() == SubstitutedForAvailability {
		return "a model version this provider has not got"
	}
	if e.Substituted() && e.Reason() == SubstitutedForExecutable {
		described := "a provider executable that could not be found or started here"
		if e.CrossedProviders() {
			described += "; the turn went to " + string(e.ServedByProvider) +
				" and rebuilt its context from the durable record rather than resuming a session"
		}
		return described
	}
	described := DescribePause(PauseUsageLimit, e.Kind)
	if e.ResetsAt != nil {
		described += ", until " + e.ResetsAt.UTC().Format(time.RFC3339)
	}
	// A crossing says so in the cause rather than only in the two models, because
	// the cost of it is the part a reader would otherwise have to infer: the second
	// provider held no session, so the turn carried on from what the record holds
	// rather than from where the first provider had got to.
	if e.CrossedProviders() {
		described += "; the turn crossed to " + string(e.ServedByProvider) +
			" and rebuilt its context from the durable record rather than resuming a session"
	}
	return described
}

// UsageLimitStore is where a product's refusals are collected, in the same
// operating-system state root as the runs and the reports and beside them rather
// than among them. It is one append-only log per product, because a refusal
// belongs to the account rather than to any run, and because the processes that
// meet one — a conversation, a review, a scheduler — have nothing else in common
// to write it on.
type UsageLimitStore struct {
	root      string
	productID domain.ProductID
}

func NewUsageLimitStore(root string, productID domain.ProductID) (*UsageLimitStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &UsageLimitStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *UsageLimitStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the account of the
// refusals actually is.
func (s *UsageLimitStore) Path() string { return filepath.Join(s.root, "usage-limits.jsonl") }

// Record appends one refusal. It is an append rather than a rewrite for the
// reason every other log here is: a refusal is written once and never revised,
// and two processes refused at the same moment must not overwrite each other's
// account.
func (s *UsageLimitStore) Record(exhaustion UsageLimitExhaustion) error {
	if err := s.validate(exhaustion); err != nil {
		return err
	}
	encoded, err := encodeUsageLimitExhaustion(exhaustion)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedUsageLimitBytes {
		return fmt.Errorf("encoded usage limit refusal is %d bytes, limit is %d", len(encoded), maxEncodedUsageLimitBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create usage limit directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect usage limit log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open usage limit log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append usage limit refusal: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append usage limit refusal: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync usage limit log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close usage limit log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded refusal in the order it happened. A log that does
// not exist yet is a product no provider has refused, which is not a failure to
// read. A line that will not decode is: a hold derived from a listing that
// quietly dropped a refusal is a hold nobody can trust. The sink reads past such
// a line by position with Scan, and says so.
func (s *UsageLimitStore) List() ([]UsageLimitExhaustion, error) {
	exhaustions, skipped, err := s.Scan()
	if err != nil {
		return nil, err
	}
	if err := firstSkipped("usage limit log", skipped); err != nil {
		return nil, err
	}
	return exhaustions, nil
}

// Scan returns every refusal that decoded, in the order it happened, and beside
// them the lines that would not, each at the position it holds among the
// records. It is the read a positional cursor is kept against, so one bad line
// costs the reader that line and nothing behind it.
func (s *UsageLimitStore) Scan() ([]UsageLimitExhaustion, []SkippedLine, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open usage limit log: %w", err)
	}
	defer file.Close()

	var exhaustions []UsageLimitExhaustion
	skipped, err := scanLog(file, maxEncodedUsageLimitBytes, func(line []byte) error {
		decoded, err := decodeUsageLimitExhaustion(line)
		if err != nil {
			return err
		}
		if err := s.validate(decoded); err != nil {
			return err
		}
		exhaustions = append(exhaustions, decoded)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read usage limit log: %w", err)
	}
	return exhaustions, skipped, nil
}

func decodeUsageLimitExhaustion(data []byte) (UsageLimitExhaustion, error) {
	var decoded UsageLimitExhaustion
	if err := json.Unmarshal(data, &decoded); err != nil {
		return UsageLimitExhaustion{}, err
	}
	if err := decoded.Validate(); err != nil {
		return UsageLimitExhaustion{}, err
	}
	return decoded, nil
}

func encodeUsageLimitExhaustion(exhaustion UsageLimitExhaustion) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(exhaustion); err != nil {
		return nil, fmt.Errorf("encode usage limit refusal: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *UsageLimitStore) validate(exhaustion UsageLimitExhaustion) error {
	if exhaustion.ProductID != s.productID {
		return fmt.Errorf("usage limit refusal product %q does not match store product %q", exhaustion.ProductID, s.productID)
	}
	return exhaustion.Validate()
}
