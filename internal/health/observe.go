package health

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runinspect"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The sources an observation reads, by the names it reports them under.
const (
	SourceRuns        = "runs in flight"
	SourceHeld        = "work held after a stoppage"
	SourceFactory     = "factory problems"
	SourceServices    = "the supervisor's record of the services"
	SourceEnvironment = "what each running copy recorded as it started"
	SourcePasses      = "pass records"
	SourceSlots       = "what holds each developer slot"
)

// launchesRoles is the parts that launch a provider's command: the scheduler,
// which runs developers and scheduled passes, and the Slack sink, which holds
// the Lead Product Manager's conversation. The others launch none, so a
// provider command missing from their search path is nothing they need.
var launchesRoles = map[string]bool{string(config.ServiceScheduler): true, string(config.ServiceSlack): true}

// Keeper is where observations are kept for following. It is satisfied by
// *runstate.HealthObservationStore.
type Keeper interface {
	Save(id string, observedAt time.Time, observation []byte) error
	Load(id string) (runstate.HealthObservationRecord, error)
}

// Caller is who an observation is taken for: the role, and the program manager
// instance where it is one. It decides only what "since your last pass" means
// for the maintenance failures; every classification is the same for every
// caller.
type Caller struct {
	Role  domain.AgentRole
	Agent string
}

// Observer takes observations of one product.
type Observer struct {
	Product domain.ProductID
	// Sources is the shared read model's sources. Runs, ConfigReaders, and
	// Passes are read directly; the rest through the standing reading.
	Sources readmodel.Sources
	// Standing is the shared read model's standing reading; nil reads
	// readmodel.ReadStanding over Sources.
	Standing func(context.Context) readmodel.Standing
	// ProviderCommands is every provider command the configured agents launch,
	// as the configuration resolves each.
	ProviderCommands []string
	Keeper           Keeper
	RedactValues     []string
	Now              func() time.Time
	// Stat and Executable are seams over the file system, for a test that
	// arranges a missing file without touching the machine's.
	Stat func(string) (fs.FileInfo, error)
}

func (o Observer) now() time.Time {
	if o.Now == nil {
		return time.Now().UTC()
	}
	return o.Now().UTC()
}

func (o Observer) stat(path string) (fs.FileInfo, error) {
	if o.Stat == nil {
		return os.Stat(path)
	}
	return o.Stat(path)
}

func (o Observer) standing(ctx context.Context) readmodel.Standing {
	if o.Standing != nil {
		return o.Standing(ctx)
	}
	return readmodel.ReadStanding(ctx, o.Sources)
}

// kept is what is kept for following: the observation as first rendered, and
// every condition it found up to MaxKept.
type kept struct {
	Observation Observation `json:"observation"`
	All         []Anomaly   `json:"all"`
}

// reading is one observation as it is assembled.
type reading struct {
	now        time.Time
	product    domain.ProductID
	restricted map[string]bool
	sources    []Source
	failures   []string
	found      []Anomaly
	services   []Service
	slots      []runstate.SlotHolder
}

func (r *reading) source(name, problem string) {
	r.sources = append(r.sources, Source{Name: name, Complete: problem == "", Problem: problem})
	if problem != "" {
		r.failures = append(r.failures, name+": "+problem)
	}
}

func (r *reading) add(anomaly Anomaly) {
	anomaly.Identity.Product = r.product
	anomaly.Actionable = anomaly.Classification.Actionable()
	r.found = append(r.found, anomaly)
}

// keeps is whether a run's condition is inside the observation's restriction.
func (r *reading) keeps(run string) bool {
	return len(r.restricted) == 0 || r.restricted[run]
}

// Observe takes one observation. It never fails: what could not be read is a
// source not read whole, and a read failure said, rather than a missing answer.
func (o Observer) Observe(ctx context.Context, caller Caller, runs []string) Observation {
	now := o.now()
	r := &reading{now: now, product: o.Product}
	if len(runs) > 0 {
		r.restricted = make(map[string]bool, len(runs))
		for _, run := range runs {
			r.restricted[run] = true
		}
	}
	o.readRuns(r)
	standing := o.standing(ctx)
	o.readHeld(r, standing)
	r.source(SourceSlots, standing.SlotHoldersProblem)
	r.slots = standing.SlotHolders
	o.readFactory(r, standing)
	o.readServices(r, standing)
	passes := o.readPasses(r)
	maintenance, since := o.readMaintenance(r, caller, passes)

	all := order(dedupe(r.found))
	history(all, passes, now)
	observation := Observation{
		ID: newID(now), Product: o.Product, ObservedAt: now,
		Sources: r.sources, Failures: r.failures, Services: r.services, Slots: r.slots,
		Maintenance: maintenance, MaintenanceSince: since,
	}
	if len(runs) > 0 {
		observation.RestrictedTo = append([]string(nil), runs...)
	}
	observation.Complete = len(r.failures) == 0
	observation.Totals = totals(all, len(r.services))
	observation.Says = verdict(observation)
	redactor := execution.NewRedactor(o.RedactValues...)
	redact(&observation, all, redactor.Redact)

	keep := all
	if len(keep) > MaxKept {
		observation.Unkept = len(keep) - MaxKept
		keep = keep[:MaxKept]
	}
	observation.Anomalies, observation.Omitted, observation.Cursor = page(keep, 0)
	if o.Keeper == nil {
		observation.KeepProblem = "nothing is wired to keep observations, so what this one leaves out cannot be followed"
	} else if encoded, err := json.Marshal(kept{Observation: observation, All: keep}); err != nil {
		observation.KeepProblem = fmt.Sprintf("the observation could not be encoded to be kept: %v", err)
	} else if err := o.Keeper.Save(observation.ID, now, encoded); err != nil {
		observation.KeepProblem = redactor.Redact(fmt.Sprintf("the observation could not be kept, so what it leaves out cannot be followed: %v", err))
	}
	return observation
}

// Follow is the next part of what an observation left out. It is refused for
// an observation older than MaxAge, so the caller observes again.
func (o Observer) Follow(id, cursor string) (Part, error) {
	offset, err := parseCursor(cursor)
	if err != nil {
		return Part{}, err
	}
	if o.Keeper == nil {
		return Part{}, errors.New("nothing is wired to keep observations, so none can be followed")
	}
	record, err := o.Keeper.Load(id)
	if err != nil {
		return Part{}, err
	}
	if age := o.now().Sub(record.ObservedAt); age > MaxAge {
		return Part{}, fmt.Errorf("observation %s was taken at %s, %s ago, and an observation older than %s is not followed: observe again",
			id, record.ObservedAt.Local().Format("2006-01-02 15:04 MST"), age.Round(time.Second), MaxAge)
	}
	var stored kept
	if err := json.Unmarshal(record.Observation, &stored); err != nil {
		return Part{}, fmt.Errorf("observation %s could not be read: %v", id, err)
	}
	if offset >= len(stored.All) {
		return Part{}, fmt.Errorf("observation %s kept %d conditions, so cursor %s is past its end", id, len(stored.All), cursor)
	}
	part := Part{Observation: id, ObservedAt: stored.Observation.ObservedAt, Complete: stored.Observation.Complete, Totals: stored.Observation.Totals, From: offset}
	part.Anomalies, part.Omitted, part.Cursor = page(stored.All, offset)
	return part, nil
}

// page is the conditions from offset that one part carries, the identities of
// those after them, and the cursor for the next part.
func page(all []Anomaly, offset int) ([]Anomaly, []string, string) {
	end := min(offset+MaxRendered, len(all))
	shown := append([]Anomaly{}, all[offset:end]...)
	if end == len(all) {
		return shown, nil, ""
	}
	omitted := make([]string, 0, len(all)-end)
	for _, anomaly := range all[end:] {
		omitted = append(omitted, anomaly.Key)
	}
	return shown, omitted, cursorOf(end)
}

func newID(now time.Time) string {
	var random [4]byte
	_, _ = rand.Read(random[:])
	return "health-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(random[:])
}

func (o Observer) readRuns(r *reading) {
	if o.Sources.Runs == nil {
		r.source(SourceRuns, "nothing was wired to read the runs in flight")
		return
	}
	states, err := o.Sources.Runs.Incomplete()
	if err != nil {
		r.source(SourceRuns, fmt.Sprintf("the runs in flight could not be read: %v", err))
		return
	}
	presence, asked := o.Sources.Runs.(readmodel.RunPresence)
	var problems []string
	if !asked {
		problems = append(problems, "the run store cannot be asked whether a process is behind a run, so no run's worker could be established")
	}
	for _, state := range states {
		if !state.Status.InFlight() || !r.keeps(state.RunID) {
			continue
		}
		base := Anomaly{Identity: Identity{Run: state.RunID}, WorkItem: state.WorkItemID, Title: state.WorkItemTitle}
		// What the development manager's run.recover requests left on the run
		// comes first: it is her account of the run, and it is what says what
		// anything else about it means.
		if pending := state.PendingRecoveryRequests(); len(pending) > 0 {
			last := pending[len(pending)-1]
			anomaly := base
			anomaly.Classification, anomaly.Identity.Condition = ClassRecoveryUnknown, "recovery-unknown"
			anomaly.Says = fmt.Sprintf("run.recover request %s (%s) on run %s of %s was written and its writer could not read it back", last.ID, last.Action, state.RunID, state.WorkItemID)
			anomaly.Owner = ownership.MoverDevelopmentManager
			anomaly.Needs = "nothing is decided on this run until a run.recover request for it — a repeat of the same one is the plainest — has the harness read the write back under the run's lease and say what it holds"
			r.add(anomaly)
			continue
		}
		if hold := state.RecoveryHold; hold != nil {
			anomaly := base
			anomaly.Classification, anomaly.Identity.Condition = ClassRecoveryHold, "recovery-hold"
			anomaly.Says = fmt.Sprintf("run %s of %s is %s at %s and carries an unresolved hold recorded at %s", state.RunID, state.WorkItemID, state.Status, state.Phase, hold.At.Local().Format("2006-01-02 15:04 MST"))
			anomaly.Owner = ownership.MoverOf(hold.Owner)
			anomaly.Needs = fmt.Sprintf("%s; looked at again %s", hold.NextAction, hold.FollowUp)
			r.add(anomaly)
			continue
		}
		if wait := runinspect.WaitOf(state); wait != nil {
			anomaly := base
			anomaly.Classification, anomaly.Identity.Condition = ClassWait, "wait"
			if state.DependencyPause != nil {
				anomaly.Classification, anomaly.Identity.Condition = ClassDependencyWait, "dependency-wait"
			}
			anomaly.Says = fmt.Sprintf("run %s of %s is parked on %s", state.RunID, state.WorkItemID, wait.Kind)
			anomaly.Owner = ownership.MoverHarness
			anomaly.Needs = "released by " + wait.Release + "; " + runstate.DeadRunRemedy(state, readmodel.DefaultDeadClaimThreshold)
			r.add(anomaly)
			continue
		}
		if release := state.SlotRelease; state.SlotReleased() {
			anomaly := base
			anomaly.Classification, anomaly.Identity.Condition = ClassSlotReleased, "slot-released"
			anomaly.Says = fmt.Sprintf("run %s of %s is %s at %s with no live worker. %s", state.RunID, state.WorkItemID, state.Status, state.Phase, release.Says)
			anomaly.Owner = ownership.MoverDevelopmentManager
			anomaly.Needs = runstate.DeadRunRemedy(state, readmodel.DefaultDeadClaimThreshold)
			r.add(anomaly)
			continue
		}
		if !asked {
			anomaly := base
			anomaly.Classification, anomaly.Identity.Condition = ClassWorkerUncertain, "process-unknown"
			anomaly.Says = fmt.Sprintf("whether a process is behind run %s of %s could not be asked", state.RunID, state.WorkItemID)
			anomaly.Owner, anomaly.Needs = ownership.MoverDevelopmentManager, "run.inspect shows the run whole; nothing is decided on it until its worker is established"
			r.add(anomaly)
			continue
		}
		found, err := presence.Presence(state, readmodel.DefaultDeadClaimThreshold, r.now)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("whether a process is behind run %s could not be read: %v", state.RunID, err))
			anomaly := base
			anomaly.Classification, anomaly.Identity.Condition = ClassWorkerUncertain, "process-unknown"
			anomaly.Says = fmt.Sprintf("whether a process is behind run %s of %s could not be read: %v", state.RunID, state.WorkItemID, err)
			anomaly.Owner, anomaly.Needs = ownership.MoverDevelopmentManager, "run.inspect shows the run whole; nothing is decided on it until its worker is established"
			r.add(anomaly)
		case !found.Found:
			anomaly := base
			anomaly.Classification, anomaly.Identity.Condition = ClassWorkerless, "no-process"
			anomaly.Says = fmt.Sprintf("run %s of %s is %s at %s and %s", state.RunID, state.WorkItemID, state.Status, state.Phase, found.Says)
			anomaly.Owner = ownership.MoverDevelopmentManager
			anomaly.Needs = runstate.DeadRunRemedy(state, readmodel.DefaultDeadClaimThreshold)
			r.add(anomaly)
		}
	}
	r.source(SourceRuns, strings.Join(problems, "; "))
}

// readHeld lists every item the harness holds after a stopped run: those the
// refusals list as held, and every other one, whatever its tracker status, its
// assignee, or the reason the not-startable line gives for it first
// (readmodel.Standing.AlsoHeld). Each of the others says why that line does not
// show it as held.
func (o Observer) readHeld(r *reading, standing readmodel.Standing) {
	r.source(SourceHeld, standing.NotStartableProblem)
	held := make([]readmodel.Refused, 0, len(standing.NotStartable)+len(standing.AlsoHeld))
	held = append(held, standing.NotStartable...)
	held = append(held, standing.AlsoHeld...)
	for _, refused := range held {
		if refused.Kind != backlog.HeldForAPerson {
			continue
		}
		if !r.keeps(refused.RunID) {
			continue
		}
		anomaly := Anomaly{Classification: ClassHeldStoppage, WorkItem: refused.WorkItemID, Title: refused.Title,
			Identity: Identity{Run: refused.RunID, Condition: "held-after-stoppage"}, Owner: ownership.MoverDevelopmentManager}
		if refused.RunID == "" {
			anomaly.Identity.Condition += ":" + refused.WorkItemID
		}
		anomaly.Says = fmt.Sprintf("%s is held after a stopped run", refused.WorkItemID)
		if refused.RunID != "" {
			anomaly.Says = fmt.Sprintf("%s is held after run %s stopped", refused.WorkItemID, refused.RunID)
		}
		anomaly.Says += heldElsewhere(refused)
		anomaly.Needs = refused.Reason
		r.add(anomaly)
	}
}

// heldElsewhere is why a held item is not shown as held on the not-startable
// line, in the words that close the health check's sentence about it, and
// empty for an item that line does show as held.
func heldElsewhere(refused readmodel.Refused) string {
	switch {
	case refused.ListedFor != "":
		return "; the list of work that cannot start gives another reason for it: " + refused.ListedFor
	case refused.Status != "":
		return ", though the tracker marks it " + strings.ReplaceAll(refused.Status, "_", " ")
	default:
		return ""
	}
}

func (o Observer) readFactory(r *reading, standing readmodel.Standing) {
	r.source(SourceFactory, standing.FactoryProblemsProblem)
	if len(r.restricted) > 0 {
		return
	}
	for _, entry := range standing.FactoryProblems {
		// More than one copy of a part is said per copy below, with its build.
		if entry.Kind == readmodel.AttentionServiceCopies {
			continue
		}
		r.add(Anomaly{Classification: ClassFactoryProblem, Identity: Identity{Condition: "factory-problem:" + string(entry.Kind) + ":" + entry.ID},
			WorkItem: entry.WorkItemID, Says: entry.What(), Owner: entry.Mover, Needs: entry.Whose()})
	}
}

func (o Observer) readServices(r *reading, standing readmodel.Standing) {
	services := standing.Services
	switch {
	case services == nil && standing.ServicesProblem == "":
		r.source(SourceServices, "nothing was wired to read the supervisor's record")
	default:
		r.source(SourceServices, standing.ServicesProblem)
	}
	var record runstate.Supervision
	if services != nil && services.Recorded {
		record = services.Record
	}
	byName := map[string]*Service{}
	var names []string
	service := func(name string) *Service {
		if found, seen := byName[name]; seen {
			return found
		}
		byName[name] = &Service{Name: name, Deployed: record.Deployed, Copies: []Copy{}}
		names = append(names, name)
		return byName[name]
	}
	supervised := map[string]int{}
	if services != nil && services.Recorded {
		service(runstate.ConfigReaderSupervisor).State = "running as pid " + fmt.Sprint(record.PID)
		supervised[runstate.ConfigReaderSupervisor] = record.PID
		for _, child := range record.Children {
			found := service(string(child.Service))
			found.State = string(child.State)
			if child.State == runstate.ChildRunning {
				supervised[string(child.Service)] = child.PID
			}
		}
		if len(r.restricted) == 0 {
			for _, entry := range services.Attention() {
				r.add(Anomaly{Classification: ClassServiceDown, Identity: Identity{Condition: "service-down:" + entry.ID},
					Says: entry.What(), Owner: entry.Mover, Needs: entry.Whose()})
			}
		}
	}
	defer func() {
		sort.Strings(names)
		for _, name := range names {
			r.services = append(r.services, *byName[name])
		}
	}()
	if o.Sources.ConfigReaders == nil {
		r.source(SourceEnvironment, "nothing was wired to read what the running copies recorded")
		return
	}
	readers, err := o.Sources.ConfigReaders.Running()
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("the running copies could not all be read: %v", err))
	}
	counts := map[string]int{}
	for _, reader := range readers {
		counts[reader.Service]++
	}
	for _, reader := range readers {
		found := service(reader.Service)
		copied := Copy{PID: reader.PID, Build: reader.Build, StartedAt: reader.StartedAt, ConfigFile: reader.ConfigPath,
			Supervised: supervised[reader.Service] > 0 && supervised[reader.Service] == reader.PID}
		switch {
		case reader.Build == "" || record.Deployed == "":
			copied.BuildIs = "unknown"
		case reader.Build == record.Deployed:
			copied.BuildIs = "current"
		default:
			copied.BuildIs = "old"
		}
		copyName := fmt.Sprintf("pid-%d-%d", reader.PID, reader.StartedAt.Unix())
		who := fmt.Sprintf("the %s copy with pid %d, started %s", reader.Service, reader.PID, reader.StartedAt.Local().Format("2006-01-02 15:04 MST"))
		if len(r.restricted) == 0 {
			switch {
			case copied.BuildIs == "old":
				needs := "the supervisor moves the copy it runs onto the build on disk when it restarts it"
				if !copied.Supervised {
					needs = "the supervisor did not start this copy and leaves it running: find what started it and stop it, keeping the copy the supervisor runs"
				}
				r.add(Anomaly{Classification: ClassOldBuild, Identity: Identity{Condition: "old-build:" + reader.Service + ":" + copyName},
					Says:  fmt.Sprintf("%s runs build %s, and the build on disk is %s", who, short(reader.Build), short(record.Deployed)),
					Owner: ownership.MoverDevelopmentManager, Needs: needs})
			case !copied.Supervised && counts[reader.Service] > 1:
				r.add(Anomaly{Classification: ClassExtraCopy, Identity: Identity{Condition: "extra-copy:" + reader.Service + ":" + copyName},
					Says:  fmt.Sprintf("%s is running beside the copy the supervisor runs", who),
					Owner: ownership.MoverDevelopmentManager, Needs: "the supervisor did not start this copy and leaves it running: find what started it and stop it"})
			}
		}
		if reader.SearchPath == "" {
			copied.SearchPathUnknown = true
			problems = append(problems, fmt.Sprintf("%s was started by a build that records no search path, so whether it finds the provider commands is not known until it restarts", who))
		} else {
			copied.SearchPath = reader.SearchPath
			if launchesRoles[reader.Service] {
				for _, name := range o.ProviderCommands {
					command := o.lookOn(reader.SearchPath, name)
					copied.Commands = append(copied.Commands, command)
					if !command.Found && len(r.restricted) == 0 {
						r.add(Anomaly{Classification: ClassMissingCommand, Identity: Identity{Condition: "missing-command:" + reader.Service + ":" + name},
							Says:  fmt.Sprintf("%s runs with a search path on which the provider command %s is not found", who, name),
							Owner: ownership.MoverDevelopmentManager, Needs: "the part is started with a search path that finds the command, or the provider's configuration names the command by its full path; a search path set by hand in a person's shell does not reach the part"})
					}
				}
			}
		}
		if reader.ConfigPath != "" {
			_, err := o.stat(reader.ConfigPath)
			switch {
			case err == nil:
				copied.ConfigExists = true
			case errors.Is(err, fs.ErrNotExist):
				if len(r.restricted) == 0 {
					r.add(Anomaly{Classification: ClassMissingConfig, Identity: Identity{Condition: "missing-config:" + reader.Service + ":" + reader.ConfigPath},
						Says:  fmt.Sprintf("%s names the configuration file %s, which does not exist", who, reader.ConfigPath),
						Owner: ownership.MoverDevelopmentManager, Needs: "the part is restarted on the configuration file the product reads, or the file it names is put back"})
				}
			default:
				copied.ConfigProblem = err.Error()
				problems = append(problems, fmt.Sprintf("whether the configuration file %s that %s names exists could not be read: %v", reader.ConfigPath, who, err))
			}
		}
		found.Copies = append(found.Copies, copied)
	}
	for _, name := range names {
		sort.Slice(byName[name].Copies, func(i, j int) bool { return byName[name].Copies[i].StartedAt.Before(byName[name].Copies[j].StartedAt) })
	}
	r.source(SourceEnvironment, strings.Join(problems, "; "))
}

// lookOn looks for a command on a search path as the part would: a command
// named by its path is looked for there, and a bare name in each directory of
// the path in turn, as a regular file anybody may execute.
func (o Observer) lookOn(searchPath, name string) Command {
	command := Command{Name: name}
	executable := func(path string) bool {
		info, err := o.stat(path)
		return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
	}
	if strings.ContainsRune(name, filepath.Separator) {
		command.Found = executable(name)
		if command.Found {
			command.Path = name
		}
		return command
	}
	for _, directory := range filepath.SplitList(searchPath) {
		if directory == "" {
			continue
		}
		candidate := filepath.Join(directory, name)
		if executable(candidate) {
			command.Found, command.Path = true, candidate
			return command
		}
	}
	return command
}

func (o Observer) readPasses(r *reading) []runstate.Sweep {
	if o.Sources.Passes == nil {
		r.source(SourcePasses, "nothing was wired to read the pass records, so no condition's history and no maintenance failure is known")
		return nil
	}
	passes, unreadable, err := o.Sources.Passes.List()
	switch {
	case err != nil:
		r.source(SourcePasses, fmt.Sprintf("the pass records could not be read: %v", err))
	case len(unreadable) > 0:
		r.source(SourcePasses, fmt.Sprintf("%d pass record line(s) could not be read", len(unreadable)))
	default:
		r.source(SourcePasses, "")
	}
	return passes
}

// maxMaintenanceListed bounds the maintenance failures one observation lists;
// the anomalies for the undelivered ones are not bounded by it.
const maxMaintenanceListed = 20

// readMaintenance is the maintenance pass's failures since the caller's last
// pass, newest last, each said with whether a finding has carried it to a
// role. A failure is carried once its run of failures reached the threshold a
// finding is raised at (runstate.PassFailuresOf), because that finding is put
// into the watching and the resolving roles' passes from then on.
func (o Observer) readMaintenance(r *reading, caller Caller, passes []runstate.Sweep) ([]MaintenanceFailure, *time.Time) {
	var since time.Time
	for _, pass := range passes {
		if pass.Missed != nil || pass.Role == "" || pass.Role != caller.Role || pass.Agent != caller.Agent {
			continue
		}
		if pass.StartedAt.After(since) {
			since = pass.StartedAt
		}
	}
	var raised []runstate.PassFailure
	for _, failure := range runstate.PassFailuresOf(passes) {
		if failure.Task == config.MaintenanceTaskName {
			raised = append(raised, failure)
		}
	}
	var failures []MaintenanceFailure
	for _, pass := range passes {
		if pass.Task != config.MaintenanceTaskName || !pass.StartedAt.After(since) || !runstate.PassFailed(pass) {
			continue
		}
		delivered := false
		for _, failure := range raised {
			if !pass.StartedAt.Before(failure.FirstAt) && (failure.ClearedAt.IsZero() || pass.StartedAt.Before(failure.ClearedAt)) {
				delivered = true
			}
		}
		problem := oneLine(pass.Problem)
		for _, step := range pass.Steps {
			if step.Outcome == runstate.StepFailed {
				problem = oneLine(step.Name + ": " + step.Detail)
			}
		}
		failures = append(failures, MaintenanceFailure{At: pass.StartedAt, Problem: problem, Delivered: delivered})
		if !delivered && len(r.restricted) == 0 {
			r.add(Anomaly{Classification: ClassUndeliveredMaintenance,
				Identity: Identity{Condition: "maintenance-failure:" + pass.StartedAt.UTC().Format(time.RFC3339Nano)},
				Says:     fmt.Sprintf("the maintenance pass failed at %s: %s", pass.StartedAt.Local().Format("2006-01-02 15:04 MST"), problem),
				Owner:    ownership.MoverDevelopmentManager,
				Needs:    fmt.Sprintf("no finding has carried it to a role: one is raised once the pass has failed %d times in a row, and until then this observation is where it is said; the development manager resolves its cause", runstate.PassFailureThreshold)})
		}
	}
	sort.SliceStable(failures, func(i, j int) bool { return failures[i].At.Before(failures[j].At) })
	if len(failures) > maxMaintenanceListed {
		failures = failures[len(failures)-maxMaintenanceListed:]
	}
	if since.IsZero() {
		return failures, nil
	}
	return failures, &since
}

// history sets when each condition was first seen and how many passes it has
// been put to, from the pass records, and when it was last seen: now.
func history(all []Anomaly, passes []runstate.Sweep, now time.Time) {
	first := map[string]time.Time{}
	count := map[string]int{}
	for _, pass := range passes {
		if pass.Health == nil {
			continue
		}
		for _, key := range pass.Health.Delivered {
			count[key]++
			if seen, ok := first[key]; !ok || pass.Health.ObservedAt.Before(seen) {
				first[key] = pass.Health.ObservedAt
			}
		}
	}
	for i := range all {
		key := all[i].Key
		all[i].FirstSeen, all[i].LastSeen, all[i].Recurrence = now, now, count[key]+1
		if seen, ok := first[key]; ok && seen.Before(now) {
			all[i].FirstSeen = seen
		}
	}
}

// classOrder is the order conditions are listed in: what needs a decision on a
// run first, then the factory around the runs, then the waits.
var classOrder = map[Classification]int{
	ClassSlotReleased: 0, ClassWorkerless: 1, ClassWorkerUncertain: 2, ClassHeldStoppage: 3, ClassUndeliveredMaintenance: 4,
	ClassMissingCommand: 5, ClassMissingConfig: 6, ClassServiceDown: 7, ClassOldBuild: 8, ClassExtraCopy: 9,
	ClassFactoryProblem: 10, ClassDependencyWait: 11, ClassWait: 12,
}

func dedupe(found []Anomaly) []Anomaly {
	seen := map[string]bool{}
	var unique []Anomaly
	for _, anomaly := range found {
		anomaly.Key = anomaly.Identity.Key()
		if seen[anomaly.Key] {
			continue
		}
		seen[anomaly.Key] = true
		unique = append(unique, anomaly)
	}
	return unique
}

func order(all []Anomaly) []Anomaly {
	sort.SliceStable(all, func(i, j int) bool {
		if classOrder[all[i].Classification] != classOrder[all[j].Classification] {
			return classOrder[all[i].Classification] < classOrder[all[j].Classification]
		}
		return all[i].Key < all[j].Key
	})
	if all == nil {
		return []Anomaly{}
	}
	return all
}

func totals(all []Anomaly, services int) Totals {
	t := Totals{Conditions: len(all), Services: services}
	for _, anomaly := range all {
		if anomaly.Actionable {
			t.Actionable++
		} else {
			t.Waits++
		}
	}
	return t
}

// verdict is the observation in a sentence. One with a source not read whole
// is never said to be healthy, and never said to have found nothing.
func verdict(o Observation) string {
	found := fmt.Sprintf("%d condition(s): %d needing a decision or an action, and %d legitimate wait(s)", o.Totals.Conditions, o.Totals.Actionable, o.Totals.Waits)
	if !o.Complete {
		var names []string
		for _, source := range o.Sources {
			if !source.Complete {
				names = append(names, source.Name)
			}
		}
		return fmt.Sprintf("Partial: %s could not be read whole, so this observation is not a healthy result whatever it lists. It found %s.", strings.Join(names, ", "), found)
	}
	if o.Totals.Conditions == 0 {
		return "Every source was read whole, and no condition was found."
	}
	return "Every source was read whole. It found " + found + "."
}

func redact(o *Observation, all []Anomaly, redact func(string) string) {
	for i := range o.Sources {
		o.Sources[i].Problem = redact(o.Sources[i].Problem)
	}
	for i := range o.Failures {
		o.Failures[i] = redact(o.Failures[i])
	}
	for i := range all {
		all[i].Says, all[i].Needs = cut(redact(all[i].Says), MaxTextBytes), cut(redact(all[i].Needs), MaxTextBytes)
	}
	for i := range o.Services {
		for j := range o.Services[i].Copies {
			c := &o.Services[i].Copies[j]
			c.SearchPath, c.ConfigFile, c.ConfigProblem = redact(c.SearchPath), redact(c.ConfigFile), redact(c.ConfigProblem)
		}
	}
	for i := range o.Maintenance {
		o.Maintenance[i].Problem = redact(o.Maintenance[i].Problem)
	}
}

func short(build string) string {
	if len(build) > 12 {
		return build[:12]
	}
	if build == "" {
		return "unrecorded"
	}
	return build
}

func oneLine(text string) string {
	return cut(strings.Join(strings.Fields(text), " "), 400)
}

// cut keeps at most limit bytes of text, ending on a whole character, and
// marks the cut.
func cut(text string, limit int) string {
	const marker = " […]"
	if len(text) <= limit {
		return text
	}
	end := limit - len(marker)
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + marker
}
