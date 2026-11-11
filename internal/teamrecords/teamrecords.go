// Package teamrecords shares a team's reports, what was done about them,
// directives and changes to directives between its factories, through the
// records reference on the shared remote, in team mode only
// (docs/designs/working-alongside-other-contributors.md, section 7).
//
// Only the harness writes records. The pushes go through the harness's own Git
// code in the machine's state directory, and nothing here is reachable from an
// agent. Every surface goes on reading the machine's own report and directive
// stores through the read model: sharing copies what other factories wrote into
// those stores and pushes what this machine filed, and no surface reads Git.
//
// A record is one file, named by its own identifier, and is never rewritten. A
// change to something already recorded is a new file naming the earlier one: a
// directive resolved after it was shared is a directive-changes file that every
// factory folds into its copy of the directive.
package teamrecords

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// EnvelopeSchemaVersion is 1 and has never changed.
const EnvelopeSchemaVersion = 1

// The kinds of record a file holds.
const (
	KindReport          = "report"
	KindReportHandling  = "report-handling"
	KindDirective       = "directive"
	KindDirectiveChange = "directive-change"
)

// maxAttempts is how many times one check builds and pushes before it leaves
// the records on the machine for the next turn.
const maxAttempts = 5

// Envelope is the JSON a record file holds. The inner record is written with
// its Factory cleared: the envelope names the factory that wrote it.
type Envelope struct {
	SchemaVersion int                  `json:"schema_version"`
	Kind          string               `json:"kind"`
	Factory       string               `json:"factory"`
	Report        *report.Report       `json:"report,omitempty"`
	Handling      *report.Handling     `json:"handling,omitempty"`
	Directive     *directive.Directive `json:"directive,omitempty"`
	// Names is the directive a directive-change changes.
	Names string `json:"names,omitempty"`
}

// Remote is the records repository: the shared remote's records reference,
// fetched into this machine and built on there (*gitworktree.RecordsRepository).
type Remote interface {
	Fetch(ctx context.Context) (string, error)
	Paths(ctx context.Context, commit string) (map[string]bool, error)
	Read(ctx context.Context, commit string, paths []string) (map[string][]byte, error)
	Build(ctx context.Context, parent string, files map[string][]byte, now time.Time) (string, error)
	Push(ctx context.Context, commit, expected string) error
}

// Reports is this machine's report store (*runstate.ReportStore).
type Reports interface {
	List() ([]report.Report, error)
	Handlings() ([]report.Handling, error)
	Append(report.Report) error
	Handle(report.Handling) error
}

// Directives is this machine's directive store (*runstate.DirectiveStore).
type Directives interface {
	List() ([]directive.Directive, error)
	Load(id string) (directive.Directive, error)
	Record(directive.Directive) error
	Adopt(directive.Directive) error
}

// State is the state of this machine's record sharing
// (*runstate.SharedRecordsStore). Lock holds it against every other process on
// the machine that shares records for the product.
type State interface {
	Load() (runstate.SharedRecordsState, bool, error)
	Save(runstate.SharedRecordsState) error
	Lock(ctx context.Context) (func(), error)
}

// Sharer shares one product's records for one factory.
type Sharer struct {
	Remote     Remote
	Reports    Reports
	Directives Directives
	State      State
	// Factory is this factory's identity line, written into every record it
	// shares and compared with a record's to tell this factory's own records from
	// another's.
	Factory string
	Now     func() time.Time
}

// Result says what one check did.
type Result struct {
	// Fetched is the records commit the check read, and empty where the remote
	// held none or could not be reached.
	Fetched string `json:"fetched,omitempty"`
	// Copied is how many records were copied in from other factories.
	Copied int `json:"copied,omitempty"`
	// Shared is how many of this machine's records reached the remote.
	Shared int `json:"shared,omitempty"`
	// Waiting is how many of this machine's records are not yet on the remote.
	Waiting int `json:"waiting,omitempty"`
	// Retries is how many times another factory pushed first.
	Retries int `json:"retries,omitempty"`
	// Problem is why this machine's records did not reach the remote.
	Problem string `json:"problem,omitempty"`
	// Unreadable names each record file on the remote that could not be used, and
	// why, as "<path>: <why>".
	Unreadable []string `json:"unreadable,omitempty"`
}

// Says is the result in one plain line.
func (r Result) Says() string {
	var did []string
	if r.Copied > 0 {
		did = append(did, fmt.Sprintf("copied %d from other factories", r.Copied))
	}
	if r.Shared > 0 {
		did = append(did, fmt.Sprintf("shared %d", r.Shared))
	}
	line := "shared records: "
	if len(did) == 0 {
		line += "nothing new"
	} else {
		line += strings.Join(did, ", ")
	}
	if r.Waiting > 0 {
		verb := "are"
		if r.Waiting == 1 {
			verb = "is"
		}
		line += fmt.Sprintf("; %d filed here %s not yet shared because %s", r.Waiting, verb, r.Problem)
	} else if r.Problem != "" {
		line += "; " + r.Problem
	}
	if len(r.Unreadable) > 0 {
		line += fmt.Sprintf("; %d on the shared remote could not be read", len(r.Unreadable))
	}
	return line
}

// Sharing is what became of a directive offered to the shared records.
type Sharing string

const (
	// SharingShared says this machine's directive is now the one that stands.
	SharingShared Sharing = "shared"
	// SharingAlreadyRecorded says the directive was already on the shared remote,
	// and the version that stands there is the one now recorded here.
	SharingAlreadyRecorded Sharing = "already-recorded"
	// SharingNotShared says the remote could not be reached, so the directive is
	// kept here and pushed on a later turn.
	SharingNotShared Sharing = "not-yet-shared"
)

func (s Sharer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Sync copies in the records other factories wrote and pushes this machine's
// own that the remote does not hold. A remote that cannot be reached or will
// not take the records is not a failure of the check: it is said in the result
// and the records stay here for the next turn. An error is a failure to read or
// write this machine's own stores or repository.
func (s Sharer) Sync(ctx context.Context) (Result, error) {
	release, err := s.State.Lock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	result, _, err := s.sync(ctx)
	return result, err
}

// ShareDirective offers a directive recorded from a Slack reply. Its identifier
// names the reply, so two factories offering the same reply offer the same
// directive: the first push stands, and the other factory takes it as its own
// copy. The returned directive is this machine's copy afterwards. The error is
// only for a failure to record locally.
func (s Sharer) ShareDirective(ctx context.Context, recorded directive.Directive) (directive.Directive, Sharing, error) {
	release, err := s.State.Lock(ctx)
	if err != nil {
		return directive.Directive{}, "", err
	}
	defer release()

	fetched, err := s.Remote.Fetch(ctx)
	if err != nil {
		local, err2 := s.recordLocally(recorded)
		if err2 != nil {
			return directive.Directive{}, "", err2
		}
		previous, _, err2 := s.State.Load()
		if err2 != nil {
			return directive.Directive{}, "", err2
		}
		if _, _, err2 := s.unreachable(ctx, previous, Result{}, "the shared remote could not be reached: "+err.Error()); err2 != nil {
			return directive.Directive{}, "", err2
		}
		return local, SharingNotShared, nil
	}
	paths, err := s.Remote.Paths(ctx, fetched)
	if err != nil {
		return directive.Directive{}, "", err
	}
	if paths[directivePath(recorded.ID)] {
		// The version on the remote stands: copy it in, then let a check push
		// whatever else this machine has waiting.
		if _, _, _, err := s.copyIn(ctx, fetched, paths); err != nil {
			return directive.Directive{}, "", err
		}
		if _, _, err := s.sync(ctx); err != nil {
			return directive.Directive{}, "", err
		}
		local, err := s.Directives.Load(recorded.ID)
		if err != nil {
			return directive.Directive{}, "", err
		}
		return local, SharingAlreadyRecorded, nil
	}
	if _, err := s.recordLocally(recorded); err != nil {
		return directive.Directive{}, "", err
	}
	_, reached, err := s.sync(ctx)
	if err != nil {
		return directive.Directive{}, "", err
	}
	local, err := s.Directives.Load(recorded.ID)
	if err != nil {
		return directive.Directive{}, "", err
	}
	switch {
	case !reached:
		return local, SharingNotShared, nil
	case local.Factory != "":
		return local, SharingAlreadyRecorded, nil
	}
	return local, SharingShared, nil
}

func (s Sharer) recordLocally(recorded directive.Directive) (directive.Directive, error) {
	if err := s.Directives.Record(recorded); err != nil && !errors.Is(err, runstate.ErrDirectiveRecorded) {
		return directive.Directive{}, err
	}
	return s.Directives.Load(recorded.ID)
}

// sync is the check with the lock held. reached says the remote answered and
// took everything this machine had to share.
func (s Sharer) sync(ctx context.Context) (Result, bool, error) {
	previous, _, err := s.State.Load()
	if err != nil {
		return Result{}, false, err
	}
	var result Result
	waiting := 0
	for attempt := 0; attempt < maxAttempts; attempt++ {
		fetched, err := s.Remote.Fetch(ctx)
		if err != nil {
			return s.unreachable(ctx, previous, result, "the shared remote could not be reached: "+err.Error())
		}
		result.Fetched = fetched
		paths, err := s.Remote.Paths(ctx, fetched)
		if err != nil {
			return Result{}, false, err
		}
		copied, unreadable, remoteDirectives, err := s.copyIn(ctx, fetched, paths)
		if err != nil {
			return Result{}, false, err
		}
		result.Copied += copied
		result.Unreadable = unreadable
		files, err := s.unshared(paths, remoteDirectives)
		if err != nil {
			return Result{}, false, err
		}
		if len(files) == 0 {
			return result, true, s.saveReached(result)
		}
		commit, err := s.Remote.Build(ctx, fetched, files, s.now())
		if err != nil {
			return Result{}, false, err
		}
		err = s.Remote.Push(ctx, commit, fetched)
		switch {
		case err == nil:
			result.Shared = len(files)
			return result, true, s.saveReached(result)
		case errors.Is(err, gitworktree.ErrRecordsLeaseRefused):
			result.Retries++
			waiting = len(files)
		default:
			return s.waiting(previous, result, len(files), "the shared remote did not take the records: "+err.Error())
		}
	}
	return s.waiting(previous, result, waiting, "another factory kept pushing first")
}

// saveReached records a check that reached the remote with nothing left to
// share.
func (s Sharer) saveReached(result Result) error {
	now := s.now()
	return s.State.Save(runstate.SharedRecordsState{
		CheckedAt: now,
		Fetched:   result.Fetched,
		SharedAt:  &now,
		Copied:    result.Copied,
	})
}

// waiting records a check that left records on this machine, and says so in
// the result. It is not an error.
func (s Sharer) waiting(previous runstate.SharedRecordsState, result Result, waiting int, problem string) (Result, bool, error) {
	result.Waiting, result.Problem = waiting, problem
	fetched := result.Fetched
	if fetched == "" {
		fetched = previous.Fetched
	}
	err := s.State.Save(runstate.SharedRecordsState{
		CheckedAt: s.now(),
		Fetched:   fetched,
		SharedAt:  previous.SharedAt,
		Waiting:   waiting,
		Problem:   problem,
		Copied:    result.Copied,
	})
	return result, false, err
}

// unreachable is a check whose fetch failed. What is waiting is counted against
// the records the last fetch read, which a change to a directive already shared
// is not counted in, since its remote copy is not read again.
func (s Sharer) unreachable(ctx context.Context, previous runstate.SharedRecordsState, result Result, problem string) (Result, bool, error) {
	paths, err := s.Remote.Paths(ctx, previous.Fetched)
	if err != nil {
		paths = map[string]bool{}
	}
	files, err := s.unshared(paths, nil)
	if err != nil {
		return Result{}, false, err
	}
	return s.waiting(previous, result, len(files), problem)
}

// ---- reading the remote ----

type fetchedRecord struct {
	path     string
	envelope Envelope
}

// copyIn copies every record of the commit this machine does not hold into its
// own stores, and returns how many it recorded, the files it could not use, and
// each remote directive as it stands with the changes folded in.
func (s Sharer) copyIn(ctx context.Context, commit string, paths map[string]bool) (int, []string, map[string]directive.Directive, error) {
	localReports, err := s.Reports.List()
	if err != nil {
		return 0, nil, nil, err
	}
	haveReport := map[string]bool{}
	for _, r := range localReports {
		haveReport[r.ID] = true
	}
	localHandlings, err := s.Reports.Handlings()
	if err != nil {
		return 0, nil, nil, err
	}
	haveHandling := map[string]bool{}
	for _, h := range localHandlings {
		haveHandling[report.HandlingID(h)] = true
	}
	localDirectives, err := s.Directives.List()
	if err != nil {
		return 0, nil, nil, err
	}
	haveDirective := map[string]directive.Directive{}
	for _, d := range localDirectives {
		haveDirective[d.ID] = d
	}

	var wanted []string
	for path := range paths {
		directory, name, ok := splitRecordPath(path)
		if !ok {
			continue
		}
		switch directory {
		case "reports":
			if report.ValidID(name) && !haveReport[name] {
				wanted = append(wanted, path)
			}
		case "report-handling":
			if report.ValidHandlingID(name) && !haveHandling[name] {
				wanted = append(wanted, path)
			}
		case "directives":
			if directive.ValidID(name) {
				wanted = append(wanted, path)
			}
		case "directive-changes":
			wanted = append(wanted, path)
		}
	}
	sort.Strings(wanted)
	contents := map[string][]byte{}
	if len(wanted) > 0 {
		if contents, err = s.Remote.Read(ctx, commit, wanted); err != nil {
			return 0, nil, nil, err
		}
	}

	var unreadable []string
	refuse := func(path string, why error) { unreadable = append(unreadable, path+": "+why.Error()) }
	copied := 0
	directives := map[string]fetchedRecord{}
	var changes []fetchedRecord
	for _, path := range wanted {
		directory, name, _ := splitRecordPath(path)
		kind := map[string]string{"reports": KindReport, "report-handling": KindReportHandling, "directives": KindDirective, "directive-changes": KindDirectiveChange}[directory]
		envelope, err := decodeEnvelope(kind, contents[path])
		if err != nil {
			refuse(path, err)
			continue
		}
		factory := envelope.Factory
		if factory == s.Factory {
			factory = ""
		}
		switch kind {
		case KindReport:
			if envelope.Report.ID != name {
				refuse(path, errors.New("the report's id is not the file's name"))
				continue
			}
			recorded := *envelope.Report
			recorded.Factory = factory
			if err := s.Reports.Append(recorded); err != nil {
				refuse(path, err)
				continue
			}
			copied++
		case KindReportHandling:
			if report.HandlingID(*envelope.Handling) != name {
				refuse(path, errors.New("the handling's id is not the file's name"))
				continue
			}
			recorded := *envelope.Handling
			recorded.Factory = factory
			if err := s.Reports.Handle(recorded); err != nil {
				refuse(path, err)
				continue
			}
			copied++
		case KindDirective:
			if envelope.Directive.ID != name {
				refuse(path, errors.New("the directive's id is not the file's name"))
				continue
			}
			directives[name] = fetchedRecord{path, envelope}
		case KindDirectiveChange:
			if envelope.Names != envelope.Directive.ID {
				refuse(path, errors.New("the change names a different directive than it carries"))
				continue
			}
			changes = append(changes, fetchedRecord{path, envelope})
		}
	}

	remote := map[string]directive.Directive{}
	ids := make([]string, 0, len(directives))
	for id := range directives {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		record := directives[id]
		folded := *record.envelope.Directive
		folded.Factory = ""
		for _, change := range changes { // sorted by path
			if change.envelope.Names == id {
				folded = directive.Merge(folded, *change.envelope.Directive)
			}
		}
		remote[id] = folded
		factory := record.envelope.Factory
		if factory == s.Factory {
			factory = ""
		}
		local, held := haveDirective[id]
		if !held {
			folded.Factory = factory
			if err := s.Directives.Record(folded); err != nil {
				if !errors.Is(err, runstate.ErrDirectiveRecorded) {
					refuse(record.path, err)
				}
				continue
			}
			copied++
			continue
		}
		want := directive.Merge(folded, local)
		want.Factory = factory
		same, err := sameEncoding(want, local)
		if err != nil {
			return 0, nil, nil, err
		}
		if same {
			continue
		}
		if err := s.Directives.Adopt(want); err != nil {
			refuse(record.path, err)
			continue
		}
		copied++
	}
	return copied, unreadable, remote, nil
}

func sameEncoding(a, b directive.Directive) (bool, error) {
	x, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	y, err := json.Marshal(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(x, y), nil
}

// splitRecordPath splits "<directory>/<name>.json" and refuses anything else.
func splitRecordPath(path string) (directory, name string, ok bool) {
	directory, file, found := strings.Cut(path, "/")
	if !found || strings.Contains(file, "/") || !strings.HasSuffix(file, ".json") {
		return "", "", false
	}
	name = strings.TrimSuffix(file, ".json")
	return directory, name, name != ""
}

func decodeEnvelope(kind string, data []byte) (Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("it is not a record: %w", err)
	}
	switch {
	case envelope.SchemaVersion != EnvelopeSchemaVersion:
		return Envelope{}, fmt.Errorf("schema version %d is not supported", envelope.SchemaVersion)
	case envelope.Kind != kind:
		return Envelope{}, fmt.Errorf("it holds a %q record, not a %q one", envelope.Kind, kind)
	case strings.TrimSpace(envelope.Factory) == "" || strings.ContainsAny(envelope.Factory, "\r\n") || len(envelope.Factory) > report.MaxFactoryBytes:
		return Envelope{}, errors.New("the factory that wrote it is missing or unusable")
	}
	var err error
	switch kind {
	case KindReport:
		if envelope.Report == nil {
			return Envelope{}, errors.New("it holds no report")
		}
		err = envelope.Report.Validate()
	case KindReportHandling:
		if envelope.Handling == nil {
			return Envelope{}, errors.New("it holds no handling")
		}
		err = envelope.Handling.Validate()
	case KindDirective, KindDirectiveChange:
		if envelope.Directive == nil {
			return Envelope{}, errors.New("it holds no directive")
		}
		err = envelope.Directive.Validate()
	}
	return envelope, err
}

// ---- writing ----

func directivePath(id string) string { return "directives/" + id + ".json" }

// dispositions is the three groups of a directive that a change carries.
type dispositions struct {
	Resolution    string           `json:"resolution,omitempty"`
	ResolvedAt    *time.Time       `json:"resolved_at,omitempty"`
	Withdrawal    string           `json:"withdrawal,omitempty"`
	WithdrawnBy   string           `json:"withdrawn_by,omitempty"`
	WithdrawnRole domain.AgentRole `json:"withdrawn_role,omitempty"`
	WithdrawnAt   *time.Time       `json:"withdrawn_at,omitempty"`
	Became        string           `json:"became,omitempty"`
	BecameReason  string           `json:"became_reason,omitempty"`
	BecameBy      string           `json:"became_by,omitempty"`
	BecameRole    domain.AgentRole `json:"became_role,omitempty"`
	BecameAt      *time.Time       `json:"became_at,omitempty"`
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// changePath names a change by the directive and the dispositions it carries,
// so the same change recorded on two machines is one file.
func changePath(d directive.Directive) (string, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(dispositions{
		Resolution: d.Resolution, ResolvedAt: utc(d.ResolvedAt),
		Withdrawal: d.Withdrawal, WithdrawnBy: d.WithdrawnBy, WithdrawnRole: d.WithdrawnRole, WithdrawnAt: utc(d.WithdrawnAt),
		Became: d.Became, BecameReason: d.BecameReason, BecameBy: d.BecameBy, BecameRole: d.BecameRole, BecameAt: utc(d.BecameAt),
	}); err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(d.ID+"\x00"), encoded.Bytes()...))
	return "directive-changes/" + hex.EncodeToString(sum[:])[:32] + ".json", nil
}

func encodeEnvelope(envelope Envelope) ([]byte, error) {
	envelope.SchemaVersion = EnvelopeSchemaVersion
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(envelope); err != nil {
		return nil, fmt.Errorf("encode the %s record: %w", envelope.Kind, err)
	}
	return encoded.Bytes(), nil
}

// unshared builds the files for every record of this machine the remote does
// not hold, keyed by path. remote is each remote directive with its changes
// folded in, and nil where they were not read.
func (s Sharer) unshared(paths map[string]bool, remote map[string]directive.Directive) (map[string][]byte, error) {
	files := map[string][]byte{}
	add := func(path string, envelope Envelope, own string) error {
		envelope.Factory = own
		if envelope.Factory == "" {
			envelope.Factory = s.Factory
		}
		encoded, err := encodeEnvelope(envelope)
		if err != nil {
			return err
		}
		files[path] = encoded
		return nil
	}
	reports, err := s.Reports.List()
	if err != nil {
		return nil, err
	}
	for _, r := range reports {
		path := "reports/" + r.ID + ".json"
		if paths[path] {
			continue
		}
		own := r.Factory
		r.Factory = ""
		if err := add(path, Envelope{Kind: KindReport, Report: &r}, own); err != nil {
			return nil, err
		}
	}
	handlings, err := s.Reports.Handlings()
	if err != nil {
		return nil, err
	}
	for _, h := range handlings {
		path := "report-handling/" + report.HandlingID(h) + ".json"
		if paths[path] {
			continue
		}
		own := h.Factory
		h.Factory = ""
		if err := add(path, Envelope{Kind: KindReportHandling, Handling: &h}, own); err != nil {
			return nil, err
		}
	}
	directives, err := s.Directives.List()
	if err != nil {
		return nil, err
	}
	for _, d := range directives {
		own := d.Factory
		d.Factory = ""
		path := directivePath(d.ID)
		if !paths[path] {
			if err := add(path, Envelope{Kind: KindDirective, Directive: &d}, own); err != nil {
				return nil, err
			}
			continue
		}
		stands, known := remote[d.ID]
		if !known || directive.SameDispositions(directive.Merge(stands, d), stands) {
			continue
		}
		change, err := changePath(d)
		if err != nil {
			return nil, err
		}
		if paths[change] {
			continue
		}
		if err := add(change, Envelope{Kind: KindDirectiveChange, Directive: &d, Names: d.ID}, own); err != nil {
			return nil, err
		}
	}
	return files, nil
}
