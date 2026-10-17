package runstate

// The dashboard's own records: which dashboard processes are serving this
// product, which one the supervisor started, and the token a dashboard that
// generated its own publishes for a person to read.
//
// The observability-and-dashboard design's sections "Under the supervisor" and
// "The generated token" are the rules; this is where they are kept on disk,
// under the product's state directory in `dashboard/`:
//
//   - instances/<start id>.json and .lease: one per dashboard process, written
//     by that process as it starts and removed as it stops cleanly. The lease
//     is held for the life of the process, so whether a recorded dashboard is
//     still serving is the operating system's answer rather than the record's.
//   - supervised.json: the supervisor's own record of the dashboard it started
//     — the start identity a dashboard is taken up against.
//   - starts/<start id>.json: the supervisor's record of every start it made
//     whose dashboard it has not yet seen stop, the latest among them. A
//     dashboard is the supervisor's own because its start identity is one of
//     these and its process the one that start launched — never because of the
//     port it serves on, which a configuration or a build can move — so an
//     earlier copy is still recognised, and stopped, once a later start has
//     replaced supervised.json.
//   - token: the generated token, written whole by rename into a directory of
//     mode 0700, in a file of mode 0600, never through a symbolic link, and
//     refused rather than repaired where either is not as it should be. A token
//     the configuration supplies is never written here.
//
// Every write goes through the pinned root the rest of the state root's
// confined writers use.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

const (
	// DashboardSchemaVersion is the version of the instance and start records.
	DashboardSchemaVersion   = 1
	dashboardDirectory       = "dashboard"
	dashboardInstancesFolder = "instances"
	dashboardStartFile       = "supervised.json"
	dashboardStartsFolder    = "starts"
	// DashboardTokenFile is the generated token's file name in the dashboard
	// directory.
	DashboardTokenFile = "token"
)

// dashboardStartIDPattern is the shape of a start identity: what the supervisor
// passes a dashboard it starts, and what a dashboard started by hand makes for
// itself.
var dashboardStartIDPattern = regexp.MustCompile(`^dashboard-[0-9a-f]{16}$`)

// NewDashboardStartID makes a fresh start identity.
func NewDashboardStartID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("make a dashboard start identity: %w", err)
	}
	return "dashboard-" + hex.EncodeToString(raw), nil
}

// ValidDashboardStartID reports whether a start identity has the shape one is
// made in.
func ValidDashboardStartID(id string) bool { return dashboardStartIDPattern.MatchString(id) }

// DashboardInstance is one dashboard process as it recorded itself.
type DashboardInstance struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	StartID       string           `json:"start_id"`
	PID           int              `json:"pid"`
	// Build is the revision the process was built from, and empty where the
	// build recorded none.
	Build     string    `json:"build,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// URL is where a browser is pointed, and Port the port it serves on.
	URL  string `json:"url"`
	Port int    `json:"port"`
	// ReadyURL is the process's own address for the readiness request, which
	// reaches this process and no other even while a dashboard it is replacing
	// still shares its port.
	ReadyURL string `json:"ready_url,omitempty"`
	// Supervised marks a dashboard the supervisor started.
	Supervised bool `json:"supervised,omitempty"`
	// TokenPublished says the generated token is in the token file, and
	// TokenProblem why a token that should have been could not be. Neither
	// carries the token.
	TokenPublished bool   `json:"token_published,omitempty"`
	TokenProblem   string `json:"token_problem,omitempty"`
	// ReadyAt is when the dashboard accepted the readiness request and began
	// serving; zero until then.
	ReadyAt time.Time `json:"ready_at,omitempty"`
}

// Validate refuses a record nothing could use.
func (i DashboardInstance) Validate() error {
	var problems []error
	if i.SchemaVersion != DashboardSchemaVersion {
		problems = append(problems, fmt.Errorf("dashboard instance schema version %d is not supported", i.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(i.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !ValidDashboardStartID(i.StartID) {
		problems = append(problems, fmt.Errorf("dashboard start identity %q is not one", i.StartID))
	}
	if i.PID <= 0 {
		problems = append(problems, fmt.Errorf("dashboard pid %d names no process", i.PID))
	}
	if i.StartedAt.IsZero() {
		problems = append(problems, errors.New("started at is required"))
	}
	if i.Port <= 0 || i.Port > 65535 {
		problems = append(problems, fmt.Errorf("dashboard port %d is not a port", i.Port))
	}
	return errors.Join(problems...)
}

// DashboardStart is the supervisor's record of the dashboard it started: the
// start identity it handed the process, the process, and the deployed build it
// was started from.
type DashboardStart struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	StartID       string           `json:"start_id"`
	PID           int              `json:"pid"`
	Build         string           `json:"build,omitempty"`
	Port          int              `json:"port"`
	StartedAt     time.Time        `json:"started_at"`
}

// Validate refuses a record nothing could use.
func (s DashboardStart) Validate() error {
	var problems []error
	if s.SchemaVersion != DashboardSchemaVersion {
		problems = append(problems, fmt.Errorf("dashboard start schema version %d is not supported", s.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !ValidDashboardStartID(s.StartID) {
		problems = append(problems, fmt.Errorf("dashboard start identity %q is not one", s.StartID))
	}
	if s.PID <= 0 {
		problems = append(problems, fmt.Errorf("dashboard pid %d names no process", s.PID))
	}
	if s.StartedAt.IsZero() {
		problems = append(problems, errors.New("started at is required"))
	}
	return errors.Join(problems...)
}

// DashboardLook is a recorded dashboard and whether its process still holds
// its lease.
type DashboardLook struct {
	Instance DashboardInstance
	Live     bool
}

// DashboardStore is one product's dashboard records.
type DashboardStore struct {
	stateRoot string
	anchor    string
	productID domain.ProductID
	// owned reports whether a file is owned by this process's user; a test
	// replaces it to drive the refusal.
	owned func(fs.FileInfo) bool
}

// NewDashboardStore opens the dashboard records under a state root.
func NewDashboardStore(root string, productID domain.ProductID) (*DashboardStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	stateRoot, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the dashboard's state root: %w", err)
	}
	return &DashboardStore{stateRoot: stateRoot, anchor: anchor, productID: productID, owned: ownedByThisUser}, nil
}

// Product is the product the records belong to.
func (s *DashboardStore) Product() domain.ProductID { return s.productID }

// Directory is the dashboard directory, for a message to name.
func (s *DashboardStore) Directory() string {
	return filepath.Join(home.ProductDirectory(s.stateRoot, string(s.productID)), dashboardDirectory)
}

// TokenPath is the generated token's file, for a message to name. It is the
// path and never the value.
func (s *DashboardStore) TokenPath() string {
	return filepath.Join(s.Directory(), DashboardTokenFile)
}

func (s *DashboardStore) within() string {
	return filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), dashboardDirectory)
}

// open pins the dashboard directory, creating it with mode 0700 where it is
// not there, and refuses one that is a link, is not a directory, belongs to
// another user, or is readable by anybody but its owner. A directory like that
// is refused and left as it is, never repaired.
func (s *DashboardStore) open() (*repowrite.PinnedRoot, error) {
	root, err := pinStateRoot(s.stateRoot, s.anchor)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	product := filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID)))
	if err := root.MakeDirectory(product, 0o700); err != nil {
		return nil, fmt.Errorf("create the product's state directory: %w", err)
	}
	within := s.within()
	info, err := root.Lstat(within)
	if errors.Is(err, fs.ErrNotExist) {
		if err := root.MakeDirectory(within, 0o700); err != nil {
			return nil, fmt.Errorf("create the dashboard directory %s: %w", s.Directory(), err)
		}
		info, err = root.Lstat(within)
	}
	if err != nil {
		return nil, fmt.Errorf("look at the dashboard directory %s: %w", s.Directory(), err)
	}
	if err := s.safe(info, true); err != nil {
		return nil, fmt.Errorf("the dashboard directory %s %w", s.Directory(), err)
	}
	return root.OpenDirectory(within)
}

// existing pins the dashboard directory for a reading, which creates nothing:
// a directory that is not there is fs.ErrNotExist, and one that is there is
// held to the same checks a write is.
func (s *DashboardStore) existing() (*repowrite.PinnedRoot, error) {
	info, err := os.Lstat(s.Directory())
	if err != nil {
		return nil, err
	}
	if err := s.safe(info, true); err != nil {
		return nil, fmt.Errorf("the dashboard directory %s %w", s.Directory(), err)
	}
	parent, err := repowrite.OpenPinnedRoot(filepath.Dir(s.Directory()))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return parent.OpenDirectory(dashboardDirectory)
}

// safe is the check every existing directory and file of the token's is held
// to: no link, the type expected, this user's, and readable by nobody else.
func (s *DashboardStore) safe(info fs.FileInfo, directory bool) error {
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return errors.New("is a symbolic link, which is refused rather than followed")
	case directory && !info.IsDir():
		return errors.New("is not a directory")
	case !directory && !info.Mode().IsRegular():
		return errors.New("is not a regular file")
	case !s.owned(info):
		return errors.New("is owned by another user")
	case info.Mode().Perm()&0o077 != 0:
		return fmt.Errorf("has mode %#o, readable beyond its owner; it is refused rather than repaired", info.Mode().Perm())
	}
	return nil
}

// Register records a dashboard process as it starts and takes its lease, which
// it holds until Unregister or its exit. The record is written under the lease.
func (s *DashboardStore) Register(instance DashboardInstance) (*Lease, error) {
	instance.SchemaVersion = DashboardSchemaVersion
	instance.ProductID = s.productID
	if err := instance.Validate(); err != nil {
		return nil, err
	}
	directory, err := s.open()
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	if err := directory.MakeDirectory(dashboardInstancesFolder, 0o700); err != nil {
		return nil, fmt.Errorf("create the dashboard instances directory: %w", err)
	}
	file, err := directory.OpenLock(filepath.Join(dashboardInstancesFolder, instance.StartID+".lease"), 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the dashboard's lease: %w", err)
	}
	held, err := tryLockStateFile(file)
	if err != nil || !held {
		file.Close()
		if err == nil {
			err = fmt.Errorf("another process holds the lease of dashboard %s", instance.StartID)
		}
		return nil, err
	}
	lease := &Lease{label: "dashboard " + instance.StartID, file: file}
	if err := s.write(directory, instance); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	return lease, nil
}

// Update rewrites a registered dashboard's own record, as it becomes ready.
func (s *DashboardStore) Update(instance DashboardInstance) error {
	instance.SchemaVersion = DashboardSchemaVersion
	instance.ProductID = s.productID
	if err := instance.Validate(); err != nil {
		return err
	}
	directory, err := s.open()
	if err != nil {
		return err
	}
	defer directory.Close()
	return s.write(directory, instance)
}

func (s *DashboardStore) write(directory *repowrite.PinnedRoot, instance DashboardInstance) error {
	encoded, err := encodeRecord("dashboard instance", instance)
	if err != nil {
		return err
	}
	if err := directory.WriteFile(filepath.Join(dashboardInstancesFolder, instance.StartID+".json"), encoded, 0o600, false); err != nil {
		return fmt.Errorf("record dashboard %s: %w", instance.StartID, err)
	}
	return directory.Sync()
}

// Unregister removes a stopping dashboard's record, then lets its lease go.
func (s *DashboardStore) Unregister(lease *Lease, startID string) error {
	var removed error
	if directory, err := s.existing(); err != nil {
		removed = err
	} else {
		if err := directory.Remove(filepath.Join(dashboardInstancesFolder, startID+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			removed = err
		}
		_ = directory.Remove(filepath.Join(dashboardInstancesFolder, startID+".lease"))
		directory.Close()
	}
	return errors.Join(removed, lease.Release())
}

// Instances is every recorded dashboard, each with whether its process still
// holds its lease, oldest start first. The record of one that is gone is
// removed as it is found, so the records are the dashboards serving rather than
// every start the product made. A record a newer build wrote is read
// tolerantly, since its reader is often the older build.
func (s *DashboardStore) Instances() ([]DashboardLook, error) {
	directory, err := s.existing()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDirectory(dashboardInstancesFolder)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the dashboard instances: %w", err)
	}
	var looks []DashboardLook
	var problems []error
	for _, entry := range entries {
		startID, isRecord := strings.CutSuffix(entry.Name(), ".json")
		if !isRecord || !ValidDashboardStartID(startID) {
			continue
		}
		encoded, err := directory.ReadFile(filepath.Join(dashboardInstancesFolder, entry.Name()))
		if err != nil {
			problems = append(problems, fmt.Errorf("read dashboard %s: %w", startID, err))
			continue
		}
		var instance DashboardInstance
		unknown, err := decodeTolerating(encoded, &instance)
		if err == nil {
			noteUnknownFields("dashboard instance", unknown)
			err = instance.Validate()
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("decode dashboard %s: %w", startID, err))
			continue
		}
		live, err := s.held(directory, startID)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !live {
			_ = directory.Remove(filepath.Join(dashboardInstancesFolder, startID+".json"))
			_ = directory.Remove(filepath.Join(dashboardInstancesFolder, startID+".lease"))
			continue
		}
		looks = append(looks, DashboardLook{Instance: instance, Live: true})
	}
	sort.Slice(looks, func(i, j int) bool { return looks[i].Instance.StartedAt.Before(looks[j].Instance.StartedAt) })
	return looks, errors.Join(problems...)
}

// Instance is one recorded dashboard by its start identity, and whether its
// process holds its lease.
func (s *DashboardStore) Instance(startID string) (DashboardLook, bool, error) {
	looks, err := s.Instances()
	for _, look := range looks {
		if look.Instance.StartID == startID {
			return look, true, nil
		}
	}
	return DashboardLook{}, false, err
}

func (s *DashboardStore) held(directory *repowrite.PinnedRoot, startID string) (bool, error) {
	file, err := directory.OpenLock(filepath.Join(dashboardInstancesFolder, startID+".lease"), 0o600)
	if err != nil {
		return false, fmt.Errorf("open the lease of dashboard %s: %w", startID, err)
	}
	defer file.Close()
	free, err := tryLockStateFile(file)
	if err != nil {
		return false, fmt.Errorf("ask whether dashboard %s holds its lease: %w", startID, err)
	}
	if free {
		return false, unlockStateFile(file)
	}
	return true, nil
}

// RecordStart writes the supervisor's record of the dashboard it started, as
// the latest start and among every start it made. The start it replaces as the
// latest is kept among them first, which is how a start recorded by a build
// that kept only the latest is still recognised as the supervisor's after it.
func (s *DashboardStore) RecordStart(start DashboardStart) error {
	start.SchemaVersion = DashboardSchemaVersion
	start.ProductID = s.productID
	if err := start.Validate(); err != nil {
		return err
	}
	encoded, err := encodeRecord("dashboard start", start)
	if err != nil {
		return err
	}
	directory, err := s.open()
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.MakeDirectory(dashboardStartsFolder, 0o700); err != nil {
		return fmt.Errorf("create the dashboard starts directory: %w", err)
	}
	if previous, found, err := s.readStart(directory, dashboardStartFile); err == nil && found && previous.StartID != start.StartID {
		kept := filepath.Join(dashboardStartsFolder, previous.StartID+".json")
		if _, err := directory.Lstat(kept); errors.Is(err, fs.ErrNotExist) {
			if earlier, err := encodeRecord("dashboard start", previous); err == nil {
				if err := directory.WriteFile(kept, earlier, 0o600, false); err != nil {
					return fmt.Errorf("keep the record of the dashboard's earlier start %s: %w", previous.StartID, err)
				}
			}
		}
	}
	if err := directory.WriteFile(filepath.Join(dashboardStartsFolder, start.StartID+".json"), encoded, 0o600, false); err != nil {
		return fmt.Errorf("record the dashboard's start %s: %w", start.StartID, err)
	}
	if err := directory.WriteFile(dashboardStartFile, encoded, 0o600, false); err != nil {
		return fmt.Errorf("record the dashboard's start: %w", err)
	}
	return directory.Sync()
}

// Start is the supervisor's record of the dashboard it last started, reported
// as an absence where it has started none.
func (s *DashboardStore) Start() (DashboardStart, bool, error) {
	directory, err := s.existing()
	if errors.Is(err, fs.ErrNotExist) {
		return DashboardStart{}, false, nil
	}
	if err != nil {
		return DashboardStart{}, false, err
	}
	defer directory.Close()
	return s.readStart(directory, dashboardStartFile)
}

func (s *DashboardStore) readStart(directory *repowrite.PinnedRoot, name string) (DashboardStart, bool, error) {
	encoded, err := directory.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return DashboardStart{}, false, nil
	}
	if err != nil {
		return DashboardStart{}, false, fmt.Errorf("read the dashboard's start: %w", err)
	}
	var start DashboardStart
	unknown, err := decodeTolerating(encoded, &start)
	if err != nil {
		return DashboardStart{}, false, fmt.Errorf("decode the dashboard's start: %w", err)
	}
	noteUnknownFields("dashboard start", unknown)
	if err := start.Validate(); err != nil {
		return DashboardStart{}, false, err
	}
	return start, true, nil
}

// Starts is every start the supervisor recorded and has not forgotten, the
// latest among them, oldest first.
func (s *DashboardStore) Starts() ([]DashboardStart, error) {
	directory, err := s.existing()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	var starts []DashboardStart
	var problems []error
	seen := map[string]bool{}
	latest, found, err := s.readStart(directory, dashboardStartFile)
	if err != nil {
		problems = append(problems, err)
	}
	if found {
		starts = append(starts, latest)
		seen[latest.StartID] = true
	}
	entries, err := directory.ReadDirectory(dashboardStartsFolder)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		problems = append(problems, fmt.Errorf("list the dashboard's starts: %w", err))
	}
	for _, entry := range entries {
		startID, isRecord := strings.CutSuffix(entry.Name(), ".json")
		if !isRecord || !ValidDashboardStartID(startID) || seen[startID] {
			continue
		}
		start, found, err := s.readStart(directory, filepath.Join(dashboardStartsFolder, entry.Name()))
		if err != nil {
			problems = append(problems, fmt.Errorf("the dashboard's start %s: %w", startID, err))
			continue
		}
		if found && start.StartID == startID {
			starts = append(starts, start)
			seen[startID] = true
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].StartedAt.Before(starts[j].StartedAt) })
	return starts, errors.Join(problems...)
}

// ForgetStart removes the record of an earlier start whose dashboard has
// stopped. The latest start is kept in supervised.json whatever this removes.
func (s *DashboardStore) ForgetStart(startID string) error {
	if !ValidDashboardStartID(startID) {
		return fmt.Errorf("dashboard start identity %q is not one", startID)
	}
	directory, err := s.existing()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Remove(filepath.Join(dashboardStartsFolder, startID+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("forget the dashboard's start %s: %w", startID, err)
	}
	return nil
}

// StartedBySupervisor is every running dashboard the supervisor started: each
// whose start identity is among the starts it recorded, run by the process
// that start launched. Which port each serves on has no part in it.
func (s *DashboardStore) StartedBySupervisor() ([]DashboardInstance, error) {
	starts, err := s.Starts()
	launched := make(map[string]int, len(starts))
	for _, start := range starts {
		launched[start.StartID] = start.PID
	}
	looks, lookErr := s.Instances()
	var started []DashboardInstance
	for _, look := range looks {
		if pid, ours := launched[look.Instance.StartID]; ours && look.Live && pid == look.Instance.PID {
			started = append(started, look.Instance)
		}
	}
	return started, errors.Join(err, lookErr)
}

// DashboardTokenReceipt is what publishing the token left: the file it wrote,
// so a stop removes that file and never one a later dashboard published over
// it.
type DashboardTokenReceipt struct {
	info fs.FileInfo
}

// PublishToken writes the generated token into the token file: a new file
// created exclusively in the dashboard directory, flushed, renamed over
// `token`, and the directory flushed, so a reader sees the old token or the
// new one and never part of one. An existing file that is a link, is not a
// regular file, is another user's, or is readable beyond its owner is refused
// and left as it is.
func (s *DashboardStore) PublishToken(token string) (DashboardTokenReceipt, error) {
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return DashboardTokenReceipt{}, errors.New("a dashboard token is one line, and this is not")
	}
	directory, err := s.open()
	if err != nil {
		return DashboardTokenReceipt{}, err
	}
	defer directory.Close()
	if info, err := directory.Lstat(DashboardTokenFile); err == nil {
		if err := s.safe(info, false); err != nil {
			return DashboardTokenReceipt{}, fmt.Errorf("the token file %s %w", s.TokenPath(), err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return DashboardTokenReceipt{}, fmt.Errorf("look at the token file %s: %w", s.TokenPath(), err)
	}
	if err := directory.WriteFile(DashboardTokenFile, []byte(token+"\n"), 0o600, false); err != nil {
		return DashboardTokenReceipt{}, fmt.Errorf("write the token file %s: %w", s.TokenPath(), err)
	}
	if err := directory.Sync(); err != nil {
		return DashboardTokenReceipt{}, fmt.Errorf("flush the dashboard directory %s: %w", s.Directory(), err)
	}
	info, err := directory.Lstat(DashboardTokenFile)
	if err != nil {
		return DashboardTokenReceipt{}, fmt.Errorf("look at the token file %s just written: %w", s.TokenPath(), err)
	}
	if err := s.safe(info, false); err != nil {
		return DashboardTokenReceipt{}, fmt.Errorf("the token file %s %w", s.TokenPath(), err)
	}
	return DashboardTokenReceipt{info: info}, nil
}

// ErrNoDashboardToken is a token file that is not there: no dashboard has
// published one, or the one that did stopped cleanly.
var ErrNoDashboardToken = errors.New("no dashboard token is published")

// ReadToken reads the published token, under the same checks as the write.
func (s *DashboardStore) ReadToken() (string, error) {
	directory, err := s.existing()
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w at %s", ErrNoDashboardToken, s.TokenPath())
	}
	if err != nil {
		return "", err
	}
	defer directory.Close()
	info, err := directory.Lstat(DashboardTokenFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w at %s", ErrNoDashboardToken, s.TokenPath())
	}
	if err != nil {
		return "", fmt.Errorf("look at the token file %s: %w", s.TokenPath(), err)
	}
	if err := s.safe(info, false); err != nil {
		return "", fmt.Errorf("the token file %s %w", s.TokenPath(), err)
	}
	encoded, err := directory.ReadFile(DashboardTokenFile)
	if err != nil {
		return "", fmt.Errorf("read the token file %s: %w", s.TokenPath(), err)
	}
	token := strings.TrimSpace(string(encoded))
	if token == "" {
		return "", fmt.Errorf("%w: the token file %s is empty", ErrNoDashboardToken, s.TokenPath())
	}
	return token, nil
}

// WithdrawToken removes the token file a dashboard published, as it stops
// cleanly, and leaves alone a file a later dashboard published over it.
func (s *DashboardStore) WithdrawToken(receipt DashboardTokenReceipt) error {
	if receipt.info == nil {
		return nil
	}
	directory, err := s.existing()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	current, err := directory.Lstat(DashboardTokenFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(current, receipt.info) {
		return nil
	}
	if err := directory.Remove(DashboardTokenFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return directory.Sync()
}
