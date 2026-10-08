package runstate

// Which keys each running part of the product can read of the configuration.
//
// A part of the product runs the build it was started from for days, and it
// reads the configuration again and again while it does. A landing that adds a
// key to the configuration therefore reaches the file at once and the parts
// only when each is restarted, and a part whose build predates the key refuses
// the whole file: on 2026-09-28 the dashboard served nothing but "field effort
// not found in type config.agentDocument" for two hours, and nothing said
// which part was behind or why.
//
// So each part records, as it starts, which build it is, which file it reads,
// and the configuration keys its build can read. Any later build — `yoyo
// doctor`, `yoyo config validate`, the read model — can then compare the file
// as it stands against every part still running, without the older build's
// types, and name the part, its build, and the keys. A record whose process
// has gone is a part that is not running and says nothing.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// ConfigReaderSchemaVersion is 1 and has never changed.
const ConfigReaderSchemaVersion = 1

// ConfigReaderSupervisor is the supervisor's name among the parts that record
// what they read. The other parts are named as the services section names
// them.
const ConfigReaderSupervisor = "supervisor"

const configReadersDirectory = "config-readers"

// ConfigReader is one running part's account of what its build reads.
type ConfigReader struct {
	recordPath string

	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// Service is the part: a service the configuration's services section
	// names, or the supervisor.
	Service string `json:"service"`
	PID     int    `json:"pid"`
	// Build is the revision the part was built from, and empty where the build
	// recorded none.
	Build string `json:"build,omitempty"`
	// ConfigPath is the configuration file the part reads, as it resolved it.
	ConfigPath string    `json:"config_path"`
	StartedAt  time.Time `json:"started_at"`
	// Keys is config.SchemaKeys as the part's own build derived it.
	Keys []string `json:"keys"`
}

// InstanceID identifies a process's record without replacing another running
// process of the same service. The start time also separates reused process IDs.
func (r ConfigReader) InstanceID() string {
	return configReaderInstanceID(r.Service, r.PID, r.StartedAt)
}

func configReaderInstanceID(service string, pid int, started time.Time) string {
	return service + "-" + strconv.Itoa(pid) + "-" + started.UTC().Format("20060102T150405.000000000Z")
}

func (r ConfigReader) Validate() error {
	var problems []error
	if r.SchemaVersion != ConfigReaderSchemaVersion {
		problems = append(problems, fmt.Errorf("configuration reader schema version %d is not supported", r.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !knownConfigReader(r.Service) {
		problems = append(problems, fmt.Errorf("configuration reader %q is not a part of the product", r.Service))
	}
	if r.PID <= 0 {
		problems = append(problems, fmt.Errorf("configuration reader pid %d names no process", r.PID))
	}
	if !filepath.IsAbs(r.ConfigPath) {
		problems = append(problems, fmt.Errorf("configuration reader's file %q is not an absolute path", r.ConfigPath))
	}
	if r.StartedAt.IsZero() {
		problems = append(problems, errors.New("started at is required"))
	}
	if len(r.Keys) == 0 {
		problems = append(problems, errors.New("a configuration reader names no keys"))
	}
	return errors.Join(problems...)
}

func knownConfigReader(service string) bool {
	if service == ConfigReaderSupervisor {
		return true
	}
	for _, name := range config.ServiceNames {
		if service == string(name) {
			return true
		}
	}
	return false
}

// ConfigMismatch is a running part whose build cannot read keys the file it
// reads now carries.
type ConfigMismatch struct {
	Service    string    `json:"service"`
	PID        int       `json:"pid"`
	Build      string    `json:"build,omitempty"`
	ConfigPath string    `json:"config_path"`
	StartedAt  time.Time `json:"started_at"`
	// Keys are the file's keys the part's build does not read, with the file's
	// own names in them.
	Keys []string `json:"keys"`
}

func (m ConfigMismatch) InstanceID() string {
	return configReaderInstanceID(m.Service, m.PID, m.StartedAt)
}

// ConfigComparison is a landing's saved comparison, including problems reading
// it. Pending means its account has not yet been delivered to the work item.
// The compared revisions and findings survive cleanup and service restarts.
type ConfigComparison struct {
	TargetCommit         string                   `json:"target_commit"`
	PreviousTargetCommit string                   `json:"previous_target_commit"`
	Mismatches           []ConfigMismatch         `json:"mismatches,omitempty"`
	TemplateMismatches   []ConfigTemplateMismatch `json:"template_mismatches,omitempty"`
	ActiveProblem        string                   `json:"active_problem,omitempty"`
	TemplateProblem      string                   `json:"template_problem,omitempty"`
	Pending              bool                     `json:"pending"`
	DeliveryFailure      string                   `json:"delivery_failure,omitempty"`
}

// ConfigTemplateMismatch is prospective: adopting keys introduced in a shipped
// template would make a running part unable to read its configuration. It says
// nothing about whether the file the part currently reads is already broken.
type ConfigTemplateMismatch struct {
	Service      string    `json:"service"`
	PID          int       `json:"pid"`
	Build        string    `json:"build,omitempty"`
	ConfigPath   string    `json:"config_path"`
	StartedAt    time.Time `json:"started_at"`
	TemplatePath string    `json:"template_path"`
	Keys         []string  `json:"keys"`
}

func (m ConfigTemplateMismatch) Says() string {
	build := "a build that recorded no revision"
	if m.Build != "" {
		build = "build " + shortBuild(m.Build)
	}
	return fmt.Sprintf("the %s service, running %s as pid %d since %s, cannot read new template keys %s in %s; adopting those keys in %s would make its configuration reads fail",
		m.Service, build, m.PID, m.StartedAt.Local().Format("2006-01-02 15:04 MST"), strings.Join(m.Keys, ", "), m.TemplatePath, m.ConfigPath)
}

// Says is the mismatch in a sentence: the part, its build, and the keys.
func (m ConfigMismatch) Says() string {
	build := "a build that recorded no revision"
	if m.Build != "" {
		build = "build " + shortBuild(m.Build)
	}
	return fmt.Sprintf("the %s service, running %s as pid %d since %s, cannot read %s in %s, so every read it makes of the configuration fails",
		m.Service, build, m.PID, m.StartedAt.Local().Format("2006-01-02 15:04 MST"), strings.Join(m.Keys, ", "), m.ConfigPath)
}

// ConfigMismatchRemedy says what brings a part onto a build that reads the
// file, in the words every surface uses for it.
func ConfigMismatchRemedy(service string) string {
	switch service {
	case string(config.ServiceScheduler):
		return "the watch restarts itself into the deployed build between runs, once the binary on disk is one that reads every key; `yoyo stop` and `yoyo start` restart it now"
	case string(config.ServiceSlack):
		return "the supervisor restarts the sink into the deployed build between its passes, once the binary on disk is one that reads every key; `yoyo stop` and `yoyo start` restart it now"
	case string(config.ServiceDashboard):
		return "nothing restarts the dashboard onto a newer build until the supervisor adopts it (the dashboard's adoption, yoyodyne-ifd.414), so stop it and start it again with `yoyo dashboard`"
	case ConfigReaderSupervisor:
		return "the supervisor takes up the deployed build once its maintenance pass is recorded, leaving its parts running, once the binary on disk is one that reads every key; `yoyo stop` and `yoyo start` restart it now"
	}
	return "restart it from the binary on disk"
}

func shortBuild(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

// ConfigReaderStore is where the parts' records live: one file per running
// instance under the product.
//
// Writes use the shared confined writer with the state directory pinned for
// the whole operation. A replacement or symlink cannot redirect the record.
type ConfigReaderStore struct {
	root      string
	stateRoot string
	anchor    string
	productID domain.ProductID
	// running answers whether a recorded process is still there; nil asks the
	// operating system.
	running func(pid int) (bool, error)
}

func NewConfigReaderStore(root string, productID domain.ProductID) (*ConfigReaderStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	root, anchor, err := confinedStateRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the configuration reader's state root: %w", err)
	}
	return &ConfigReaderStore{
		root:      filepath.Join(home.ProductDirectory(root, string(productID)), configReadersDirectory),
		stateRoot: root,
		anchor:    anchor,
		productID: productID,
	}, nil
}

// WithProcessCheck replaces the question of whether a recorded process is
// still running, for a test that records a part no process is behind.
func (s *ConfigReaderStore) WithProcessCheck(running func(pid int) (bool, error)) *ConfigReaderStore {
	copied := *s
	copied.running = running
	return &copied
}

// Record writes this startup's account. It supersedes an earlier startup for
// the same service and PID when read, leaving other processes intact.
func (s *ConfigReaderStore) Record(reader ConfigReader) error {
	reader.SchemaVersion = ConfigReaderSchemaVersion
	reader.ProductID = s.productID
	if err := reader.Validate(); err != nil {
		return err
	}
	root, err := s.pinWriteRoot()
	if err != nil {
		return fmt.Errorf("pin the configuration reader's state root: %w", err)
	}
	defer root.Close()
	lock, err := s.lockRecords(root)
	if err != nil {
		return err
	}
	defer func() { unlockStateFile(lock); closeStateFile(lock) }()
	return s.recordIn(root, reader)
}

func (s *ConfigReaderStore) pinWriteRoot() (*repowrite.PinnedRoot, error) {
	return pinStateRoot(s.stateRoot, s.anchor)
}

func (s *ConfigReaderStore) recordIn(root *repowrite.PinnedRoot, reader ConfigReader) error {
	encoded, err := encodeRecord("configuration reader record", reader)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedStateBytes {
		return fmt.Errorf("encoded configuration reader record is %d bytes, limit is %d", len(encoded), maxEncodedStateBytes)
	}
	if err := root.Unchanged(); err != nil {
		return err
	}
	directory := filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), configReadersDirectory)
	if err := root.MakeDirectory(directory, 0o700); err != nil {
		return fmt.Errorf("create configuration reader directory: %w", err)
	}
	if err := root.WriteFile(filepath.Join(directory, reader.InstanceID()+".json"), encoded, 0o600, false); err != nil {
		return fmt.Errorf("record configuration reader: %w", err)
	}
	return root.Unchanged()
}

// Running is every part whose recorded process is still there, in the order
// of their names. Only the latest startup for a service and PID is current:
// watch and supervisor replace their builds with exec, which keeps the PID.
// A record a newer build wrote is read tolerantly, for the reason the
// supervision record is: the reader is often the older build.
//
// A recorded process is still there only while it is the process that wrote
// the record: one that has exited and waits for its parent to collect it, and
// one that started after the record under the same id, are not the part
// (process_look.go says why).
func (s *ConfigReaderStore) Running() ([]ConfigReader, error) {
	processes, err := s.Processes()
	var live []ConfigReader
	for _, process := range processes {
		if process.Running() {
			live = append(live, process.Reader)
		}
	}
	return live, err
}

// RecordedProcess is one part's latest record beside what the operating system
// says now of the process it names. Superseded lists the older records of the
// same process, which a restart in place leaves behind.
type RecordedProcess struct {
	Reader     ConfigReader
	Look       ProcessLook
	Superseded []ConfigReader
}

// Running is the recorded process still running as the part that recorded it.
func (p RecordedProcess) Running() bool {
	return p.Look.Running() && p.Look.Is(p.Reader.StartedAt)
}

// Exited is the recorded process having exited without its parent collecting
// it, which only that parent can end.
func (p RecordedProcess) Exited() bool {
	return p.Look.Exists && p.Look.Exited && p.Look.Is(p.Reader.StartedAt)
}

// Gone is nothing left of the recorded process: no process has the id, or the
// one that has it started after the record was written.
func (p RecordedProcess) Gone() bool {
	return !p.Look.Is(p.Reader.StartedAt)
}

// Processes is the latest record of every recorded process, in the order of
// their names, with what the operating system says of each now. A process
// whose state could not be read is a problem in the returned error rather than
// a process reported either way.
func (s *ConfigReaderStore) Processes() ([]RecordedProcess, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read configuration reader records: %w", err)
	}
	var problems []error
	type process struct {
		service string
		pid     int
	}
	latest := make(map[process]*RecordedProcess)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		reader, err := s.load(filepath.Join(s.root, name))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		// Older builds wrote service.json; either filename can hold the
		// latest startup, and duplicate copies still describe one process.
		key := process{reader.Service, reader.PID}
		previous, exists := latest[key]
		switch {
		case !exists:
			latest[key] = &RecordedProcess{Reader: reader}
		case reader.StartedAt.After(previous.Reader.StartedAt):
			previous.Superseded = append(previous.Superseded, previous.Reader)
			previous.Reader = reader
		default:
			previous.Superseded = append(previous.Superseded, reader)
		}
	}
	processes := make([]RecordedProcess, 0, len(latest))
	for _, recorded := range latest {
		processes = append(processes, *recorded)
	}
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].Reader.InstanceID() < processes[j].Reader.InstanceID()
	})
	looked := processes[:0]
	for _, recorded := range processes {
		look, err := s.look(recorded.Reader)
		if err != nil {
			problems = append(problems, fmt.Errorf("whether the %s service's process %d is running could not be read: %w", recorded.Reader.Service, recorded.Reader.PID, err))
			continue
		}
		recorded.Look = look
		looked = append(looked, recorded)
	}
	return looked, errors.Join(problems...)
}

// look asks about one recorded process. A test's process check answers
// whether it runs and nothing else, so a process it calls running is the one
// the record names.
func (s *ConfigReaderStore) look(reader ConfigReader) (ProcessLook, error) {
	if s.running != nil {
		alive, err := s.running(reader.PID)
		return ProcessLook{Exists: alive}, err
	}
	return LookProcess(reader.PID)
}

// Forget removes the records nothing reads any more: every record of a process
// that is gone, and the superseded records of one that restarted in place. The
// directory then holds the parts running rather than every start the product
// has ever made. The latest record of a process that is not gone is kept, and
// so is a record a later startup has replaced since it was read.
func (s *ConfigReaderStore) Forget(process RecordedProcess) error {
	forgotten := process.Superseded
	if process.Gone() {
		forgotten = append([]ConfigReader{process.Reader}, forgotten...)
	}
	if len(forgotten) == 0 {
		return nil
	}
	root, err := s.pinWriteRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := s.lockRecords(root)
	if err != nil {
		return err
	}
	defer func() { unlockStateFile(lock); closeStateFile(lock) }()
	for _, reader := range forgotten {
		relative, err := filepath.Rel(s.stateRoot, reader.recordPath)
		if err != nil {
			return err
		}
		encoded, err := root.ReadFile(relative)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var current ConfigReader
		if _, err := decodeTolerating(encoded, &current); err != nil {
			return err
		}
		if current.InstanceID() != reader.InstanceID() {
			continue
		}
		if err := root.Unchanged(); err != nil {
			return err
		}
		if err := root.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return root.Unchanged()
}

func (s *ConfigReaderStore) load(path string) (ConfigReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return ConfigReader{}, fmt.Errorf("open configuration reader record: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, maxEncodedStateBytes))
	if err != nil {
		return ConfigReader{}, fmt.Errorf("read configuration reader record %s: %w", path, err)
	}
	var reader ConfigReader
	unknown, err := decodeTolerating(encoded, &reader)
	if err != nil {
		return ConfigReader{}, fmt.Errorf("decode configuration reader record %s: %w", path, err)
	}
	noteUnknownFields("configuration reader record", unknown)
	if err := reader.Validate(); err != nil {
		return ConfigReader{}, fmt.Errorf("configuration reader record %s: %w", path, err)
	}
	if reader.ProductID != s.productID {
		return ConfigReader{}, fmt.Errorf("configuration reader record %s belongs to product %q, not %q", path, reader.ProductID, s.productID)
	}
	reader.recordPath = path
	return reader, nil
}

// Mismatches compares the file each running part reads, as it stands now,
// against the keys that part's build reads, and returns every part that
// cannot read something in it. A part whose file cannot be read is a problem
// in the returned error rather than a part reported current. This diagnostic
// comparison never writes state.
func (s *ConfigReaderStore) Mismatches() ([]ConfigMismatch, error) {
	return s.mismatchesIn(os.ReadFile, false)
}

// TemplateMismatches compares newly introduced template keys against every
// running part's schema independently of the file it reads now.
func (s *ConfigReaderStore) TemplateMismatches(templatePath string, added []string) ([]ConfigTemplateMismatch, error) {
	readers, err := s.Running()
	var mismatches []ConfigTemplateMismatch
	for _, reader := range readers {
		keys := config.UnreadableSchemaKeys(added, reader.Keys)
		if len(keys) == 0 {
			continue
		}
		mismatches = append(mismatches, ConfigTemplateMismatch{
			Service: reader.Service, PID: reader.PID, Build: reader.Build,
			ConfigPath: reader.ConfigPath, StartedAt: reader.StartedAt,
			TemplatePath: templatePath, Keys: keys,
		})
	}
	return mismatches, err
}

// MismatchesIn is Mismatches with the file each part reads read by read
// rather than from the working tree: a landing reads it as the commit it
// landed holds it, because the checkout the parts read may not have moved onto
// that commit yet. It removes confirmed stale records through the confined
// writer; diagnostic callers use the read-only Mismatches method.
func (s *ConfigReaderStore) MismatchesIn(read func(configPath string) ([]byte, error)) ([]ConfigMismatch, error) {
	return s.mismatchesIn(read, true)
}

func (s *ConfigReaderStore) mismatchesIn(read func(string) ([]byte, error), cleanup bool) ([]ConfigMismatch, error) {
	readers, err := s.Running()
	problems := []error{err}
	var mismatches []ConfigMismatch
	for _, reader := range readers {
		source, readErr := read(reader.ConfigPath)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				correction := "diagnostic comparison left the record unchanged; the product removes confirmed stale records automatically during its next landing configuration comparison; other parts were still checked"
				if cleanup {
					correction = "the product removed this stale record automatically; other parts were still checked"
					if err := s.removeStale(reader); err != nil {
						correction = "automatic removal could not complete: " + err.Error() + "; the product retries removal at the next landing configuration comparison; other parts were still checked"
					}
				}
				problems = append(problems, fmt.Errorf("stale configuration reader record %s: recorded configuration %s no longer exists; %s", reader.recordPath, reader.ConfigPath, correction))
			} else {
				problems = append(problems, fmt.Errorf("the configuration the %s service reads could not be read: %w", reader.Service, readErr))
			}
			continue
		}
		keys, keysErr := config.UnreadableKeys(source, reader.Keys)
		if keysErr != nil {
			problems = append(problems, fmt.Errorf("the configuration the %s service reads: %w", reader.Service, keysErr))
			continue
		}
		if len(keys) == 0 {
			continue
		}
		mismatches = append(mismatches, ConfigMismatch{
			Service:    reader.Service,
			PID:        reader.PID,
			Build:      reader.Build,
			ConfigPath: reader.ConfigPath,
			StartedAt:  reader.StartedAt,
			Keys:       keys,
		})
	}
	return mismatches, errors.Join(problems...)
}

// Serialize startup records and stale-record cleanup so a legacy filename
// replaced by a new startup cannot be removed after it was inspected.
func (s *ConfigReaderStore) lockRecords(root *repowrite.PinnedRoot) (*os.File, error) {
	directory := filepath.Join(filepath.FromSlash(home.ProductDirectoryWithin(s.stateRoot, string(s.productID))), configReadersDirectory)
	if err := root.MakeDirectory(directory, 0700); err != nil {
		return nil, err
	}
	lock, err := root.OpenLock(filepath.Join(directory, ".records.lock"), 0600)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := lockStateFile(ctx, lock); err != nil {
		lock.Close()
		return nil, fmt.Errorf("wait for configuration reader record update: %w", err)
	}
	return lock, nil
}

func (s *ConfigReaderStore) removeStale(reader ConfigReader) error {
	root, err := s.pinWriteRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := s.lockRecords(root)
	if err != nil {
		return err
	}
	defer func() { unlockStateFile(lock); closeStateFile(lock) }()
	relative, err := filepath.Rel(s.stateRoot, reader.recordPath)
	if err != nil {
		return err
	}
	encoded, err := root.ReadFile(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var current ConfigReader
	if _, err := decodeTolerating(encoded, &current); err != nil {
		return err
	}
	if current.InstanceID() != reader.InstanceID() || current.ConfigPath != reader.ConfigPath {
		return errors.New("the record changed after comparison; it was preserved")
	}
	if _, err := os.Stat(reader.ConfigPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("the configuration path is no longer known to be missing; the record was preserved")
	}
	if err := root.Unchanged(); err != nil {
		return err
	}
	if err := root.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return root.Unchanged()
}
