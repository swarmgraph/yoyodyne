package runstate

// Where inter-role ask exchanges live: in the same operating-system state root
// as runs, conversations, and collected reports, product-scoped and beside all
// three rather than inside any of them.
//
// That placement is the durability the channel's first property needs. An
// exchange is between two roles and belongs to neither of their conversations:
// kept in the asker's record it would vanish from the answerer's account of
// itself, and kept in a run it would be settled and cleaned up while the
// question it asked was still worth reading. Kept here, it outlives both, and
// every process that can read this product can read what two of its roles said
// to each other.
//
// It is a file per exchange rather than an append-only log, because an exchange
// is revised as it goes: each round is written before it is taken and again when
// the answer is in, and the record is closed exactly once. The revision is a
// temporary file and a rename, as every other revised record here is, so a
// process that dies mid-write leaves the previous state rather than a truncated
// file nothing can read.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// ErrNoExchange reports an identifier that names nothing recorded, which is a
// plain answer rather than a failure to look.
var ErrNoExchange = exchange.ErrNoExchange

// ExchangeStore is one product's exchanges.
type ExchangeStore struct {
	root      string
	productID domain.ProductID
}

func NewExchangeStore(root string, productID domain.ProductID) (*ExchangeStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &ExchangeStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), "exchanges"),
		productID: productID,
	}, nil
}

func (s *ExchangeStore) Root() string { return s.root }

// Save makes one exchange durable, whether it is being opened, taken another
// round, or closed. It is a replacement rather than an append because the record
// is one thread rather than a stream of events about one: what a reader wants is
// the exchange as it now stands, and what makes that safe is that only the
// conductor writes it and only one round of one exchange is ever in flight —
// which is Hold's guarantee below rather than a convention, so a second process
// putting a round on the same thread is refused the lease instead of writing
// over the first one's round.
func (s *ExchangeStore) Save(recorded exchange.Exchange) error {
	if err := s.validate(recorded); err != nil {
		return err
	}
	path, err := s.path(recorded.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create exchange directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".exchange-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary exchange: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary exchange: %w", err)
	}
	if err := writeJSONFile(temporary, "exchange", recorded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary exchange: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace exchange: %w", err)
	}
	return syncDirectory(s.root)
}

// Load reads one exchange by its full identifier, for a caller about to act on
// it — answer it, settle it, carry it forward — which is the strict door of the
// two in tolerantread.go: the exchange is written back, and a field stepped over
// here is a field lost there.
func (s *ExchangeStore) Load(id string) (exchange.Exchange, error) {
	return s.load(id, false)
}

// Read reads one exchange for a reader that only says what it holds — a listing,
// a spend total — which is the tolerant door: a field this build does not know
// is stepped over and named once, rather than turning an exchange a newer build
// wrote into one nobody can price or list.
func (s *ExchangeStore) Read(id string) (exchange.Exchange, error) {
	return s.load(id, true)
}

func (s *ExchangeStore) load(id string, tolerateUnknownFields bool) (exchange.Exchange, error) {
	path, err := s.path(id)
	if err != nil {
		return exchange.Exchange{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return exchange.Exchange{}, fmt.Errorf("%w: %s", ErrNoExchange, id)
	}
	if err != nil {
		return exchange.Exchange{}, fmt.Errorf("open exchange: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return exchange.Exchange{}, fmt.Errorf("read exchange %s: %w", id, err)
	}
	var loaded exchange.Exchange
	if tolerateUnknownFields {
		unknown, err := decodeTolerating(encoded, &loaded)
		if err != nil {
			return exchange.Exchange{}, fmt.Errorf("decode exchange %s: %w", id, err)
		}
		noteUnknownFields("exchange record", unknown)
	} else if err := decodeStrictly(encoded, &loaded); err != nil {
		return exchange.Exchange{}, fmt.Errorf("decode exchange %s: %w", id, err)
	}
	if loaded.ID != id {
		return exchange.Exchange{}, fmt.Errorf("exchange file %s holds exchange %s", id, loaded.ID)
	}
	if err := s.validate(loaded); err != nil {
		return exchange.Exchange{}, err
	}
	return loaded, nil
}

// List returns every recorded exchange, the ones still open first. A directory
// that does not exist yet is a product whose roles have never asked each other
// anything, which is not a failure to read.
//
// One record it cannot read fails the whole listing, because this answers "show
// me what the roles said" and an answer quietly missing a thread is worse than
// no answer. A caller that is totalling rather than reading — where losing the
// records beside the broken one would lose real money from a total — reads them
// one at a time through Records and Read instead.
func (s *ExchangeStore) List() ([]exchange.Exchange, error) {
	ids, err := s.Records()
	if err != nil {
		return nil, err
	}
	if ids == nil {
		return nil, nil
	}
	exchanges := make([]exchange.Exchange, 0, len(ids))
	for _, id := range ids {
		loaded, err := s.Read(id)
		if err != nil {
			return nil, err
		}
		exchanges = append(exchanges, loaded)
	}
	exchange.Sort(exchanges)
	return exchanges, nil
}

// Records names every exchange recorded for this product, by identifier and in
// directory order. It is the enumeration List is built on, separated from it so
// that reading the records and surviving one that cannot be read are the same
// walk: a caller that must not lose the readable exchanges to an unreadable one
// loads them itself and counts what failed.
//
// It answers nil for a product whose roles have never asked each other
// anything, which is how a directory that does not exist yet is told from one
// that holds no exchanges.
func (s *ExchangeStore) Records() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read exchange directory: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), ".json"))
	}
	return ids, nil
}

// Find resolves a reference an operator typed, exactly as a directive's is: a
// full identifier matches exactly, and anything shorter is a prefix that is only
// an answer when it names one exchange. Nobody types thirty-two hex digits, and
// an ambiguous prefix is reported as ambiguous rather than resolved to whichever
// exchange happened to sort first.
func (s *ExchangeStore) Find(reference string) (exchange.Exchange, error) {
	wanted := strings.TrimSpace(reference)
	if wanted == "" {
		return exchange.Exchange{}, errors.New("name the exchange; a listing shows what is recorded")
	}
	recorded, err := s.List()
	if err != nil {
		return exchange.Exchange{}, err
	}
	var matched []exchange.Exchange
	for _, candidate := range recorded {
		if candidate.ID == wanted {
			return candidate, nil
		}
		if strings.HasPrefix(candidate.ID, wanted) {
			matched = append(matched, candidate)
		}
	}
	switch len(matched) {
	case 0:
		return exchange.Exchange{}, fmt.Errorf("%w: %s", ErrNoExchange, wanted)
	case 1:
		return matched[0], nil
	default:
		names := make([]string, 0, len(matched))
		for _, candidate := range matched {
			names = append(names, candidate.ID)
		}
		return exchange.Exchange{}, fmt.Errorf("%q names %d exchanges: %s", wanted, len(matched), strings.Join(names, ", "))
	}
}

// Hold takes the exclusive lease on one exchange without waiting, reporting
// whether it got it.
//
// It is what makes an exchange take its rounds one at a time. Two processes
// putting a round on the same thread would each load the record, each append a
// round numbered the same, and the second write would take the first away —
// one round the operator paid for and nothing recorded, and a cap counting one
// where two were spent.
//
// It is also how a restart tells an interrupted round from one being taken. A
// round on disk with no answer looks identical either way; the lease is the
// difference, because the operating system drops it when its holder exits. So a
// killed harness leaves nothing for anybody to clear and the next pass simply
// finds the exchange free. Asking whether somebody holds it and taking it
// afterwards would leave a window between the two, so taking it is the question.
//
// The lease lives in a directory beside the records rather than among them,
// which the enumeration skips: a lease is about who is working on an exchange
// and not part of what the exchange says.
// It answers the lease as the interface the exchange package states, because
// that package cannot import this one: the durable home depends on the contract
// and never the other way round.
func (s *ExchangeStore) Hold(id string) (exchange.Release, bool, error) {
	if !exchange.ValidID(id) {
		return nil, false, fmt.Errorf("exchange id %q is invalid", id)
	}
	lease, taken, err := TryLeasePath(filepath.Join(s.root, "leases", id+".lease"), "exchange "+id)
	if err != nil || !taken {
		// A typed nil handed back as an interface is not nil, and a caller checking
		// what it got would find a lease it does not have.
		return nil, taken, err
	}
	return lease, true, nil
}

func (s *ExchangeStore) validate(recorded exchange.Exchange) error {
	if recorded.ProductID != s.productID {
		return fmt.Errorf("exchange product %q does not match store product %q", recorded.ProductID, s.productID)
	}
	return recorded.Validate()
}

// path names one exchange's file. The identifier is checked against its own
// pattern first, so nothing that came from outside can name a path.
func (s *ExchangeStore) path(id string) (string, error) {
	if !exchange.ValidID(id) {
		return "", fmt.Errorf("exchange id %q is invalid", id)
	}
	return filepath.Join(s.root, id+".json"), nil
}
