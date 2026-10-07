package runstate

// The provider answering nobody, recorded where every surface can read it.
//
// A usage window has a reset and is written once per refusal; the read model
// adds the refusals up. An outage has no reset. A login that expired is expired
// until a person renews it, and a network that is down is down until it is not,
// so what a surface needs is not how many turns were refused but whether the
// provider is answering right now — one standing fact, and the moment it began.
// That is a state file rather than a log, the shape the operator's own switches
// take, and it is placed and lifted by the harness rather than by a person: the
// process that meets the provider refusing everybody writes it, and the first
// process the provider answers again removes it.
//
// What it is for. From 2026-09-17 18:17 local the operator's Claude Code login
// had expired. Every dispatch was refused, every recurring pass recorded 0
// turns, the runs already going spent their relaunch budgets and blocked, three
// of them in a row tripped the intake brake, and the operator's maintenance job
// restarted the watch 158 times. Nothing told him, and the one thing that did
// happen — the brake — prescribed the wrong remedy. The record here is what
// `yoyo status` names the wait from, what the sweep records instead of a turn
// count, what the channel says once when it begins and once when it ends, and
// what the scheduler reads so that the brake never counts a run the provider
// refused.

import (
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
)

// ProviderOutageSchemaVersion is 1 and has never changed.
const ProviderOutageSchemaVersion = 1

// MaxProviderOutageTextBytes bounds the two pieces of prose an outage carries:
// the provider's own words, and what the latest refusal stopped. Both are read
// in a line by somebody deciding what to do, so they are bounded like an
// intake hold's reason is.
const MaxProviderOutageTextBytes = 4 << 10

// ProviderOutage is the recorded fact that the provider is answering nobody on
// this product: why, since when, and what most recently met it.
type ProviderOutage struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Cause is which of the two it is, because it decides what the operator is
	// told to do: log in, or wait for the network.
	Cause domain.ProviderOutageCause `json:"cause"`
	// Provider and AccountAlias are the endpoint that refused, so a project with
	// two providers or two accounts is told which login to renew. Both are
	// optional: a process that met the outage without an account in hand still
	// records that it happened.
	Provider     domain.Backend `json:"provider,omitempty"`
	AccountAlias string         `json:"account_alias,omitempty"`
	// Since is when the outage was first noticed, and LastSeen when it was most
	// recently confirmed. The gap between them is how long the harness has been
	// asking and being refused, which is the age every surface says.
	Since    time.Time `json:"since"`
	LastSeen time.Time `json:"last_seen"`
	// Refusals is how many times the provider has been met refusing inside this
	// outage. It is a count rather than a log because what a reader needs is
	// that the harness kept asking, not each moment it did.
	Refusals int `json:"refusals"`
	// Detail is the provider's own words on the latest refusal, carried as
	// evidence rather than interpreted.
	Detail string `json:"detail,omitempty"`
	// Channel is where the latest refusal was read: the terminal of the
	// provider's stream, or its process's stderr or plain stdout because it
	// refused before writing one. It is empty when the refusal was met
	// somewhere other than an invocation — the availability check before a
	// dispatch reads the provider's own record of being signed in and none of
	// the channels.
	Channel domain.ProviderChannel `json:"channel,omitempty"`
	// Waiting names what the latest refusal stopped, in words: a run of an item,
	// a role's conversation, a dispatch. It is prose because the things that can
	// be waiting do not share a shape.
	Waiting string `json:"waiting,omitempty"`
}

func (o ProviderOutage) Validate() error {
	var problems []error
	if o.SchemaVersion != ProviderOutageSchemaVersion {
		problems = append(problems, fmt.Errorf("provider outage schema version %d is not supported", o.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(o.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !o.Cause.Valid() {
		problems = append(problems, fmt.Errorf("provider outage cause %q is not one this harness names", o.Cause))
	}
	if o.Channel != "" && !o.Channel.Valid() {
		problems = append(problems, fmt.Errorf("provider outage channel %q is not one this harness names", o.Channel))
	}
	if o.Since.IsZero() {
		problems = append(problems, errors.New("since is required"))
	}
	if o.LastSeen.IsZero() {
		problems = append(problems, errors.New("last seen is required"))
	}
	if !o.Since.IsZero() && !o.LastSeen.IsZero() && o.LastSeen.Before(o.Since) {
		problems = append(problems, errors.New("last seen is before since; an outage is not confirmed before it began"))
	}
	if o.Refusals < 1 {
		problems = append(problems, fmt.Errorf("refusals is %d, and a recorded outage was met at least once", o.Refusals))
	}
	if len(o.Detail) > MaxProviderOutageTextBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, which exceeds the %d byte bound", len(o.Detail), MaxProviderOutageTextBytes))
	}
	if len(o.Waiting) > MaxProviderOutageTextBytes {
		problems = append(problems, fmt.Errorf("what is waiting is %d bytes, which exceeds the %d byte bound", len(o.Waiting), MaxProviderOutageTextBytes))
	}
	return errors.Join(problems...)
}

// DescribeProviderOutage is the one sentence every surface names the wait in,
// as the object of "paused for" or "waiting on". It says what the operator
// does about it, because that is the whole difference between the two causes
// and the whole reason the wait is named rather than counted: the intake brake
// that tripped on this in September prescribed `yoyo release`, which lifts
// nothing here.
func DescribeProviderOutage(cause domain.ProviderOutageCause) string {
	if cause == domain.ProviderUnreachable {
		return "a provider that cannot be reached"
	}
	return "a provider that is not authenticated; the operator must log in"
}

// Says is the outage as the one sentence every surface states it in: the cause
// first, in the shape the operator asked a pause to be said in, and what ends
// it, because a wait nobody can end is not what this is.
func (o ProviderOutage) Says() string {
	var head string
	switch o.Cause {
	case domain.ProviderUnreachable:
		head = "The provider cannot be reached"
	default:
		head = "The provider is not authenticated; the operator must log in"
	}
	said := fmt.Sprintf("%s: every role is waiting on it, and the harness asks again on its own until it answers; %s refused since %s",
		head, count(o.Refusals, "turn"), o.Since.UTC().Format(time.RFC3339))
	if endpoint := o.endpoint(); endpoint != "" {
		said += " (" + endpoint + ")"
	}
	return said
}

// Mark names the outage durably, so a surface that says it once can say which
// one it said and say a different one afresh. It is the cause and the moment it
// began: a login that expired twice in a week is two things to say.
func (o ProviderOutage) Mark() string {
	return "provider:" + string(o.Cause) + ":" + o.Since.UTC().Format(time.RFC3339)
}

// ParseProviderOutageMark reads a mark back into the cause and the moment it
// names, for a surface that remembered which outage it said and now has to say
// that one ended after the record of it is gone. Anything that is not a mark
// this harness wrote is reported as none.
func ParseProviderOutageMark(mark string) (domain.ProviderOutageCause, time.Time, bool) {
	rest, marked := strings.CutPrefix(mark, "provider:")
	if !marked {
		return "", time.Time{}, false
	}
	cause, since, found := strings.Cut(rest, ":")
	if !found || !domain.ProviderOutageCause(cause).Valid() {
		return "", time.Time{}, false
	}
	began, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return "", time.Time{}, false
	}
	return domain.ProviderOutageCause(cause), began.UTC(), true
}

// endpoint names the provider and account that refused, where either was
// recorded, so a project with two logins is told which one to renew.
func (o ProviderOutage) endpoint() string {
	provider := strings.TrimSpace(string(o.Provider))
	alias := strings.TrimSpace(o.AccountAlias)
	switch {
	case provider != "" && alias != "":
		return provider + ", account " + alias
	case provider != "":
		return provider
	case alias != "":
		return "account " + alias
	default:
		return ""
	}
}

// count says a number of things in words, so a line reads "1 turn" rather than
// "1 turn(s)".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ProviderOutageStore is where the outage is recorded: one file under the
// product, beside the switches, because the provider a product's agents share
// is a fact about the product.
//
// It writes with the same temporary-file-and-rename the intake hold, the
// operator hold, and the run records beside it use, directly rather than
// through the repository's confined-write primitive. That primitive confines
// writes into the repository the harness works on, whose layout comes from a
// project's configuration; the state root is the harness's own directory
// outside every repository, named by nothing a project configures, and every
// store in this package writes there the same way.
type ProviderOutageStore struct {
	root      string
	productID domain.ProductID
}

func NewProviderOutageStore(root string, productID domain.ProductID) (*ProviderOutageStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &ProviderOutageStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *ProviderOutageStore) Root() string { return s.root }

// ProviderOutageObservation is one process meeting the provider refusing
// everybody: what it met, and what that stopped.
type ProviderOutageObservation struct {
	Cause        domain.ProviderOutageCause
	Provider     domain.Backend
	AccountAlias string
	Detail       string
	// Channel is where the refusal was read, and empty where it was met
	// somewhere other than an invocation.
	Channel domain.ProviderChannel
	Waiting string
	At      time.Time
}

// Notice records that the provider was met refusing, and reports the outage as
// it now stands. A second sighting of the same cause is the same outage: it
// keeps when it began, counts the refusal, and takes the latest words and the
// latest thing stopped, because the age is what every surface says and the
// latest sighting is what confirms it is still standing. A sighting of the
// other cause is a different wait — a machine that came back online to find
// its login expired — and begins afresh.
func (s *ProviderOutageStore) Notice(observed ProviderOutageObservation) (ProviderOutage, error) {
	at := observed.At
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()
	standing, found, err := s.Standing()
	if err != nil {
		return ProviderOutage{}, err
	}
	recorded := ProviderOutage{
		SchemaVersion: ProviderOutageSchemaVersion,
		ProductID:     s.productID,
		Cause:         observed.Cause,
		Provider:      observed.Provider,
		AccountAlias:  strings.TrimSpace(observed.AccountAlias),
		Since:         at,
		LastSeen:      at,
		Refusals:      1,
		Detail:        boundOutageText(observed.Detail),
		Channel:       observed.Channel,
		Waiting:       boundOutageText(observed.Waiting),
	}
	if found && standing.Cause == observed.Cause {
		recorded.Since = standing.Since
		recorded.Refusals = standing.Refusals + 1
		// A clock that went backwards between two processes must not record an
		// outage confirmed before it began.
		if at.Before(recorded.Since) {
			recorded.LastSeen = recorded.Since
		}
		if recorded.Provider == "" {
			recorded.Provider = standing.Provider
		}
		if recorded.AccountAlias == "" {
			recorded.AccountAlias = standing.AccountAlias
		}
	}
	if err := recorded.Validate(); err != nil {
		return ProviderOutage{}, err
	}
	if err := s.write(recorded); err != nil {
		return ProviderOutage{}, err
	}
	return recorded, nil
}

// Standing reports whether the provider is recorded as answering nobody. No
// record is the ordinary answer and means the provider is answering, which is
// why it is reported as an absence rather than as a failure to look. A record
// that cannot be read is neither: it is an error, because an outage nobody can
// read must never be started through as though it were absent.
func (s *ProviderOutageStore) Standing() (ProviderOutage, bool, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return ProviderOutage{}, false, nil
	}
	if err != nil {
		return ProviderOutage{}, false, fmt.Errorf("open provider outage: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var standing ProviderOutage
	if err := decoder.Decode(&standing); err != nil {
		return ProviderOutage{}, false, fmt.Errorf("decode provider outage: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ProviderOutage{}, false, fmt.Errorf("decode provider outage: %w", err)
	}
	if err := standing.Validate(); err != nil {
		return ProviderOutage{}, false, err
	}
	if standing.ProductID != s.productID {
		return ProviderOutage{}, false, fmt.Errorf("provider outage belongs to product %q, not %q", standing.ProductID, s.productID)
	}
	return standing, true, nil
}

// Clear records that the provider answered, and reports what was standing.
// Clearing what is not standing is not an error: the provider is answering,
// which is what the caller found.
func (s *ProviderOutageStore) Clear() (ProviderOutage, bool, error) {
	standing, found, err := s.Standing()
	if err != nil {
		return ProviderOutage{}, false, err
	}
	if !found {
		return ProviderOutage{}, false, nil
	}
	if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ProviderOutage{}, false, fmt.Errorf("clear provider outage: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return ProviderOutage{}, false, err
	}
	return standing, true, nil
}

func (s *ProviderOutageStore) write(recorded ProviderOutage) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".provider-outage-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary provider outage: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary provider outage: %w", err)
	}
	if err := writeJSONFile(temporary, "provider outage", recorded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary provider outage: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path()); err != nil {
		return fmt.Errorf("replace provider outage: %w", err)
	}
	return syncDirectory(s.root)
}

// path names the outage's file. It is one fixed name under the product:
// nothing about it is derived from anything a caller supplies.
func (s *ProviderOutageStore) path() string {
	return filepath.Join(s.root, "provider-outage.json")
}

// boundOutageText folds one piece of prose to a line inside the record's bound.
func boundOutageText(text string) string {
	return oneline.Fold(text, MaxProviderOutageTextBytes-len(oneline.Marker))
}
