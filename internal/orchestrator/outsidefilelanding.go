package orchestrator

// Bringing a pinned outside file in (docs/designs/pinned-outside-files.md).
//
// A project declares a file nobody in it wrote with a record beside it. At each
// pull the scheduler reads the target branch's records (NextOutsideFile), and a
// record that is due — its file missing, or its checksum not yet recorded — is
// fetched here, in the harness's own process and outside every agent's
// sandbox, under the rules internal/outsidefile holds whatever the
// configuration says. Only records on the target branch are read, so a record
// a developer run has just written is never fetched: an address nobody has
// reviewed is one no request is sent to.
//
// What the fetch brought is landed by a run of the harness's own
// (BringInOutsideFile): at most one at a time, holding no developer slot, which
// writes the file and the record's harness fields through the confined writer
// and nothing else, runs the full checks, and is reviewed independently before
// it merges through its pull request, and through the merge queue when that is
// switched on (outsidefilequeue.go). Where the license file a record names is
// itself pinned and not yet on the target branch, the same run brings it in
// too, so the file's checks see its license (licenseLanding). A failure that is
// about the record, or a promotion refused for want of evidence that the next
// run would be refused for again, stops
// the record until it changes on the target branch, and files one warning
// report for the development manager; a network that fails is waited out and
// counts for nothing.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/outsidefile"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// OutsideFileStore is what the run store keeps for outside files: the
// reservation that holds at most one run that brings a file in, and what is
// kept about each record between pulls. It is an optional capability of the
// pipeline's StateStore, satisfied by *runstate.Store; a store without it
// brings no file in.
type OutsideFileStore interface {
	ReserveOutsideFileLanding(ctx context.Context, state runstate.State) (*runstate.Lease, error)
	OutsideFileAttempt(record string) (runstate.OutsideFileAttempt, bool, error)
	SaveOutsideFileAttempt(attempt runstate.OutsideFileAttempt) error
}

// OutsideFileFetcher fetches a record's file under the policy. It is satisfied
// by outsidefile.Fetcher, which is what a pipeline with none uses.
type OutsideFileFetcher interface {
	Fetch(ctx context.Context, recordPath string, record outsidefile.Record, policy outsidefile.Policy) (outsidefile.Fetched, error)
}

// targetReader reads the target branch where the records are: its commit, its
// listing, and its files. It is satisfied by *gitworktree.Manager; a pipeline
// whose worktree manager does not read the target brings no file in.
type targetReader interface {
	TargetCommit(ctx context.Context, branch string) (string, error)
	repositoryReader
}

// maxOutsideFileHead is how much of a fetched text file the reviewer is shown.
const maxOutsideFileHead = 2 << 10

func (p Pipeline) outsideFileFetcher() OutsideFileFetcher {
	if p.OutsideFiles != nil {
		return p.OutsideFiles
	}
	return outsidefile.Fetcher{Now: p.clock().Now}
}

// NextOutsideFile is the record on the target branch the harness brings a file
// in for next, and false where none is due. It reads and decides; it fetches
// nothing and starts nothing. Every record it finds wrong on its face — a field
// that does not read, an address the rules refuse, a work item the tracker does
// not hold — is stopped and reported here, before any request is sent.
//
// A project that lists no host has nothing due, whatever records it carries.
func (p Pipeline) NextOutsideFile(ctx context.Context) (runstate.OutsideFileLanding, bool, error) {
	policy := p.Config.OutsideFiles.Policy()
	if !policy.Enabled() {
		return runstate.OutsideFileLanding{}, false, nil
	}
	store, ok := p.Store.(OutsideFileStore)
	if !ok {
		return runstate.OutsideFileLanding{}, false, nil
	}
	tree, ok := p.Worktrees.(targetReader)
	if !ok {
		return runstate.OutsideFileLanding{}, false, nil
	}
	branch, err := p.Worktrees.CurrentBranch(ctx)
	if err != nil {
		return runstate.OutsideFileLanding{}, false, err
	}
	commit, err := tree.TargetCommit(ctx, branch)
	if err != nil {
		return runstate.OutsideFileLanding{}, false, fmt.Errorf("read where %s is: %w", branch, err)
	}
	listing, err := tree.FilesAtCommit(ctx, commit, maxPinnedListingFiles, maxPinnedListingBytes)
	if err != nil {
		return runstate.OutsideFileLanding{}, false, fmt.Errorf("list %s at %s for the records of pinned outside files: %w", branch, commit, err)
	}
	if listing.Omitted > 0 {
		return runstate.OutsideFileLanding{}, false, fmt.Errorf("the listing of %s at %s omitted %d path(s), so not every record of a pinned outside file could be found", branch, commit, listing.Omitted)
	}
	now := p.clock().Now()
	// Every record is decided before one is chosen, because a record due only
	// as the license another due record brings in does not start a run of its
	// own, and the record that brings it in may come later in the listing.
	var problems []error
	var due []runstate.OutsideFileLanding
	licenses := map[string]bool{}
	for _, recordPath := range outsidefile.Records(listing.Files) {
		landing, isDue, err := p.dueOutsideFile(ctx, store, tree, policy, branch, commit, recordPath, now, false)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if isDue {
			due = append(due, landing)
			if landing.License != nil {
				licenses[landing.License.Record] = true
			}
		}
	}
	for _, landing := range due {
		if !licenses[landing.Record] {
			return landing, true, nil
		}
	}
	return runstate.OutsideFileLanding{}, false, errors.Join(problems...)
}

// dueOutsideFile decides one record on the target branch. asLicense is a record
// asked about as the license another record names, whose own license is not
// looked for again.
func (p Pipeline) dueOutsideFile(ctx context.Context, store OutsideFileStore, tree targetReader, policy outsidefile.Policy, branch, commit, recordPath string, now time.Time, asLicense bool) (runstate.OutsideFileLanding, bool, error) {
	none := runstate.OutsideFileLanding{}
	file, err := tree.FileAtCommit(ctx, commit, recordPath, outsidefile.MaxRecordBytes)
	if errors.Is(err, gitworktree.ErrNotAtCommit) {
		return none, false, nil
	}
	if err != nil {
		return none, false, err
	}
	digest := outsidefile.Checksum(file.Content)
	attempt, found, err := store.OutsideFileAttempt(recordPath)
	if err != nil {
		return none, false, err
	}
	if !found || !attempt.For(digest) {
		attempt = runstate.OutsideFileAttempt{Record: recordPath, RecordDigest: digest}
	}
	if attempt.Stopped != "" && attempt.StoppedBuild != "" && p.Build != "" && p.Build != attempt.StoppedBuild && attempt.Attempt < runstate.MaxOutsideFileRuns {
		// A stop at the merge step was a fault in how the harness ran the checks,
		// not in the record, so a new build is a change that may have fixed it:
		// the record starts again with the attempts it has left, and nobody edits
		// a correct record to start it. A stop for anything else waits for the
		// record to change.
		attempt = runstate.OutsideFileAttempt{Record: recordPath, RecordDigest: digest, Attempt: attempt.Attempt}
		if err := store.SaveOutsideFileAttempt(attempt); err != nil {
			return none, false, err
		}
	}
	if attempt.Stopped != "" {
		return none, false, p.reportStoppedOutsideFile(store, attempt)
	}
	if file.Content == nil {
		return none, false, p.stopOutsideFile(store, attempt, "", fmt.Sprintf("the record is %d bytes, more than the %d a record may be", file.Size, outsidefile.MaxRecordBytes))
	}
	record, err := outsidefile.ParseRecord(recordPath, file.Content)
	if err != nil {
		return none, false, p.stopOutsideFile(store, attempt, "", recordReason(err))
	}
	_, err = tree.FileAtCommit(ctx, commit, outsidefile.PinnedPath(recordPath), 0)
	present := err == nil
	if err != nil && !errors.Is(err, gitworktree.ErrNotAtCommit) {
		return none, false, err
	}
	if !record.Due(present) {
		return none, false, nil
	}
	if attempt.Waiting(now) || attempt.Attempt >= runstate.MaxOutsideFileRuns {
		return none, false, nil
	}
	if _, err := policy.CheckAddress(recordPath, record.URL, record.Version); err != nil {
		return none, false, p.stopOutsideFile(store, attempt, record.For, recordReason(err))
	}
	bounded, cancel := trackerRecordContext(ctx)
	_, err = p.Tracker.Show(bounded, record.For)
	cancel()
	if errors.Is(err, beads.ErrNoSuchWorkItem) {
		return none, false, p.stopOutsideFile(store, attempt, "", fmt.Sprintf("the record's for names %s, and the tracker holds no such work item", record.For))
	}
	if err != nil {
		// A tracker that did not answer decides nothing about the record; the
		// next pull asks again.
		return none, false, nil
	}
	landing := runstate.OutsideFileLanding{
		Record: recordPath, RecordDigest: digest, TargetCommit: commit, For: record.For,
		URL: record.URL, Version: record.Version, Attempt: attempt.Attempt,
	}
	if asLicense {
		return landing, true, nil
	}
	license, ready, err := p.licenseLanding(ctx, store, tree, policy, branch, commit, recordPath, record, now)
	if err != nil || !ready {
		return none, false, err
	}
	landing.License = license
	return landing, true, nil
}

// licenseLanding is the license a due record's file is brought in with: nil
// where the record's license file is the pinned file itself, is already on the
// target branch, or has no record there to bring it in by, and the license's own
// landing where it is a pinned file the target branch does not hold yet. A file
// whose license is brought in separately fails its own checks wherever they
// read the license beside it, so the two are one run.
//
// It is not ready where the license's record is not due itself — stopped,
// waiting out a failed try, or refused on its face — and then the file waits
// with it. Whatever is wrong with the license's record is stopped and reported
// when the scheduler reads that record in its own turn, so the error is not
// returned here as well.
func (p Pipeline) licenseLanding(ctx context.Context, store OutsideFileStore, tree targetReader, policy outsidefile.Policy, branch, commit, recordPath string, record outsidefile.Record, now time.Time) (*runstate.OutsideFileLanding, bool, error) {
	licensePath := record.LicensePath(recordPath)
	if licensePath == outsidefile.PinnedPath(recordPath) {
		return nil, true, nil
	}
	_, err := tree.FileAtCommit(ctx, commit, licensePath, 0)
	if err == nil {
		return nil, true, nil
	}
	if !errors.Is(err, gitworktree.ErrNotAtCommit) {
		return nil, false, err
	}
	licenseRecord := licensePath + outsidefile.RecordSuffix
	if _, err := tree.FileAtCommit(ctx, commit, licenseRecord, 0); errors.Is(err, gitworktree.ErrNotAtCommit) {
		// Nothing would bring the license in, so the file's checks and its
		// reviewer judge it without one, as they always have.
		return nil, true, nil
	} else if err != nil {
		return nil, false, err
	}
	license, due, err := p.dueOutsideFile(ctx, store, tree, policy, branch, commit, licenseRecord, now, true)
	if err != nil || !due {
		return nil, false, nil
	}
	return &license, true, nil
}

// recordReason is a record error's reason without the record's path, which
// every place that says it already names.
func recordReason(err error) string {
	var refused *outsidefile.RecordError
	if errors.As(err, &refused) {
		return refused.Reason
	}
	return err.Error()
}

// stopOutsideFile stops a record for a failure about it, and reports the stop.
// The record is tried again only once its content on the target branch
// changes, which starts what is kept about it afresh.
func (p Pipeline) stopOutsideFile(store OutsideFileStore, attempt runstate.OutsideFileAttempt, workItem, why string) error {
	now := p.clock().Now().UTC()
	attempt.Stopped, attempt.StoppedAt, attempt.Reported = why, &now, false
	attempt.RetryFrom, attempt.Waited = nil, ""
	if err := store.SaveOutsideFileAttempt(attempt); err != nil {
		return fmt.Errorf("record that the record %s is stopped: %w", attempt.Record, err)
	}
	return p.reportStoppedOutsideFile(store, attempt)
}

// stopLanding stops a landing's record for a failure about it, and the record
// of the license file it brings in with it, where it brings one, with it: a
// failure about either stops both, so the license is not brought in alone
// while the file that needs it is stopped. One report is filed, by the file's
// record, naming both; the license's stop is recorded as reported by it.
// attempt is the file record's, as the caller has it; its StoppedBuild is
// given to the license's stop too.
func (p Pipeline) stopLanding(store OutsideFileStore, attempt runstate.OutsideFileAttempt, landing runstate.OutsideFileLanding, why string) error {
	if license := landing.License; license != nil {
		now := p.clock().Now().UTC()
		stopped := runstate.OutsideFileAttempt{Record: license.Record, RecordDigest: license.RecordDigest, Attempt: license.Attempt,
			Stopped: fmt.Sprintf("stopped with %s, which brings it in: %s", landing.Record, why), StoppedAt: &now, Reported: true, StoppedBuild: attempt.StoppedBuild}
		if kept, found, err := store.OutsideFileAttempt(license.Record); err != nil {
			return err
		} else if found && kept.For(license.RecordDigest) {
			stopped.Attempt = kept.Attempt
		}
		if err := store.SaveOutsideFileAttempt(stopped); err != nil {
			return fmt.Errorf("record that the record %s is stopped: %w", license.Record, err)
		}
		why += fmt.Sprintf("; the record %s of the license file it brings in is stopped with it", license.Record)
	}
	return p.stopOutsideFile(store, attempt, landing.For, why)
}

// reportStoppedOutsideFile files the one warning report a stopped record is
// owed, for the development manager, and remembers that it did. A report that
// could not be filed is tried again at the next pull.
func (p Pipeline) reportStoppedOutsideFile(store OutsideFileStore, attempt runstate.OutsideFileAttempt) error {
	if attempt.Reported || p.Reports == nil {
		return nil
	}
	next := "It is not fetched again until the record changes on the target branch, so the record has to be corrected, or the file taken out of the project."
	if attempt.StoppedBuild != "" {
		// The merge step's stop is the harness's fault, so nothing in the record
		// is asked to change.
		next = "The record is not at fault: it is tried again once a build of the harness other than " + attempt.StoppedBuild + " is running, or when the record changes on the target branch."
	}
	message := fmt.Sprintf("The harness stopped bringing in the pinned outside file declared by %s: %s. %s",
		attempt.Record, oneline.Fold(strings.TrimSuffix(attempt.Stopped, "."), report.MaxMessageBytes*3/4), next)
	collected, err := report.Collect([]report.Entry{{Severity: report.SeverityWarning, Message: message, For: domain.RoleDevelopmentManager}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "outsidefile@" + attempt.RecordDigest[:16],
		Build:        p.Build,
		ProductID:    p.Config.Product.ID,
		RepositoryID: string(p.Config.Product.RepositoryID),
	}, p.clock().Now())
	if err != nil {
		return fmt.Errorf("report the stopped record %s: %w", attempt.Record, err)
	}
	for _, reported := range collected {
		if err := p.Reports.Append(reported); err != nil {
			return fmt.Errorf("report the stopped record %s: %w", attempt.Record, err)
		}
	}
	attempt.Reported = true
	return store.SaveOutsideFileAttempt(attempt)
}

// waitOutOutsideFile records a try that judged nothing about the record, so the
// record is tried again once outsidefile.RetryAfter has passed. counted is a
// run that stopped, which spends one of the record's attempts; a fetch the
// network stopped is never counted.
func (p Pipeline) waitOutOutsideFile(store OutsideFileStore, landing runstate.OutsideFileLanding, counted bool, why string) error {
	attempt, found, err := store.OutsideFileAttempt(landing.Record)
	if err != nil {
		return err
	}
	if !found || !attempt.For(landing.RecordDigest) {
		attempt = runstate.OutsideFileAttempt{Record: landing.Record, RecordDigest: landing.RecordDigest}
	}
	now := p.clock().Now().UTC()
	attempt.RetryFrom, attempt.Waited = &now, why
	if counted && attempt.Attempt <= landing.Attempt {
		attempt.Attempt = landing.Attempt + 1
		attempt.Unjudged++
	}
	if license := landing.License; license != nil {
		// The license the run brings in waits with the file, so it does not come
		// due alone while the file that needs it waits.
		kept, found, err := store.OutsideFileAttempt(license.Record)
		if err != nil {
			return err
		}
		if !found || !kept.For(license.RecordDigest) {
			kept = runstate.OutsideFileAttempt{Record: license.Record, RecordDigest: license.RecordDigest}
		}
		if kept.Stopped == "" {
			kept.RetryFrom, kept.Waited = &now, "waiting with "+landing.Record+", which brings it in: "+why
			if err := store.SaveOutsideFileAttempt(kept); err != nil {
				return err
			}
		}
	}
	if attempt.Unjudged >= runstate.MaxOutsideFileAttempts {
		return p.stopLanding(store, attempt, landing, fmt.Sprintf("%d runs in a row stopped without anything judging the file, the last because %s", runstate.MaxOutsideFileAttempts, why))
	}
	return store.SaveOutsideFileAttempt(attempt)
}

// queueRefusedOutsideFile records a run whose change the merge queue refused:
// the queue's combined change failed its checks or its review, or would not
// apply to the target as it then stood. Nothing is handed to a developer; the
// next pull starts a fresh run from the record as the target branch then holds
// it. After MaxOutsideFileQueueFailures such runs in a row for one record's
// content, the record is stopped and reported once with every reason.
func (p Pipeline) queueRefusedOutsideFile(store OutsideFileStore, landing runstate.OutsideFileLanding, why string) error {
	attempt, found, err := store.OutsideFileAttempt(landing.Record)
	if err != nil {
		return err
	}
	if !found || !attempt.For(landing.RecordDigest) {
		attempt = runstate.OutsideFileAttempt{Record: landing.Record, RecordDigest: landing.RecordDigest}
	}
	if attempt.Attempt > landing.Attempt {
		// This run's refusal is already counted.
		return nil
	}
	attempt.Attempt = landing.Attempt + 1
	attempt.QueueFailures = append(attempt.QueueFailures, why)
	if len(attempt.QueueFailures) >= runstate.MaxOutsideFileQueueFailures {
		reasons := make([]string, len(attempt.QueueFailures))
		for i, reason := range attempt.QueueFailures {
			reasons[i] = fmt.Sprintf("(%d) %s", i+1, strings.TrimSuffix(oneline.Fold(reason, 700), "."))
		}
		return p.stopLanding(store, attempt, landing, fmt.Sprintf("the merge queue refused the change of %d runs in a row: %s", len(attempt.QueueFailures), strings.Join(reasons, "; ")))
	}
	return store.SaveOutsideFileAttempt(attempt)
}

// queueRefused reports a run that brings a file in whose change was admitted
// to the merge queue and handed back by it, rather than landed.
func queueRefused(state runstate.State) bool {
	return state.OutsideFile != nil && state.MergeQueue != nil && state.Integration == nil && state.Status.Terminal() && state.Status != runstate.StatusSucceeded
}

// BringInOutsideFile fetches the file a due record declares and lands it
// through a run of the harness's own, as NextOutsideFile chose it. A run of the
// same record and attempt already in flight is continued rather than started
// again.
func (p Pipeline) BringInOutsideFile(ctx context.Context, landing runstate.OutsideFileLanding) (Outcome, error) {
	store, ok := p.Store.(OutsideFileStore)
	if !ok {
		return Outcome{}, errors.New("this run store cannot keep a run that brings a file in")
	}
	if err := landing.Validate(); err != nil {
		return Outcome{}, err
	}
	// The real tracker is kept for the notes on the item the record is for;
	// the run itself is recorded under an item of its own, like a document's.
	record := outsideFileRecorder{tracker: p.Tracker, landing: landing}
	p.Docket, p.Prices = nil, nil
	item := outsideFileItem(landing)
	p.Tracker = &documentTracker{item: item}
	p.Selection = runstate.Selection{By: runstate.SelectedByScheduler, Reason: fmt.Sprintf("The record %s is on the target branch at %s, and its file is missing or its checksum is not yet recorded; bring in %s at version %s for %s through a reviewed run of the harness's own.", landing.Record, landing.TargetCommit, landing.URL, landing.Version, landing.For)}
	if err := p.validateDispatch(); err != nil {
		return Outcome{}, err
	}
	if hold, held, err := p.operatorHold(); err != nil || held {
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{WorkItemID: item.ID, Summary: fmt.Sprint(hold)}, nil
	}
	// At most one runs at a time: one already in flight, whichever record it is
	// for, is continued where nobody holds it and left to whoever does.
	inFlight, err := p.Store.Incomplete()
	if err != nil {
		return Outcome{}, err
	}
	for _, other := range inFlight {
		if other.HoldsOutsideFileLanding() {
			return p.continueOutsideFile(ctx, store, other, outsideFileRecorder{tracker: record.tracker, landing: *other.OutsideFile})
		}
	}
	runID := landing.RunID()
	if existing, err := p.Store.Load(runID); err == nil {
		return p.continueOutsideFile(ctx, store, existing, record)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Outcome{}, err
	}
	if outcome, held, err := p.holdIntake(item.ID); err != nil || held {
		return outcome, err
	}
	if err := p.Worktrees.ValidateReady(ctx); err != nil {
		return Outcome{}, err
	}
	// The fetch is made before the run is reserved, so a network that fails
	// leaves no run behind it to settle, and is waited out.
	declared, err := p.readTargetRecord(ctx, landing)
	if err != nil {
		return Outcome{}, err
	}
	fetched, err := p.outsideFileFetcher().Fetch(ctx, landing.Record, declared, p.Config.OutsideFiles.Policy())
	if err != nil {
		if outsidefile.IsRecordError(err) {
			return Outcome{WorkItemID: item.ID, Summary: err.Error()}, p.stopLanding(store, runstate.OutsideFileAttempt{Record: landing.Record, RecordDigest: landing.RecordDigest, Attempt: landing.Attempt}, landing, recordReason(err))
		}
		return Outcome{WorkItemID: item.ID, Summary: err.Error()}, p.waitOutOutsideFile(store, landing, false, oneline.Fold(err.Error(), 600))
	}
	landing.Fetched = &runstate.OutsideFileFetch{FinalURL: fetched.FinalURL, SHA256: fetched.SHA256, Size: fetched.Size, Given: fetched.Given, At: fetched.At}
	var license *outsidefile.Fetched
	if landing.License != nil {
		declaredLicense, err := p.readTargetRecord(ctx, *landing.License)
		if err != nil {
			return Outcome{}, err
		}
		fetchedLicense, err := p.outsideFileFetcher().Fetch(ctx, landing.License.Record, declaredLicense, p.Config.OutsideFiles.Policy())
		if err != nil {
			// Neither file is brought in. A failure about the license's record stops
			// both records (stopLanding); one the network caused is waited out.
			if outsidefile.IsRecordError(err) {
				return Outcome{WorkItemID: item.ID, Summary: err.Error()}, p.stopLanding(store, runstate.OutsideFileAttempt{Record: landing.Record, RecordDigest: landing.RecordDigest, Attempt: landing.Attempt}, landing, "the license file it brings in could not be fetched: "+err.Error())
			}
			return Outcome{WorkItemID: item.ID, Summary: err.Error()}, p.waitOutOutsideFile(store, landing, false, oneline.Fold(err.Error(), 600))
		}
		landing.License.Fetched = &runstate.OutsideFileFetch{FinalURL: fetchedLicense.FinalURL, SHA256: fetchedLicense.SHA256, Size: fetchedLicense.Size, Given: fetchedLicense.Given, At: fetchedLicense.At}
		license = &fetchedLicense
	}
	target, err := p.Worktrees.CurrentBranch(ctx)
	if err != nil {
		return Outcome{}, err
	}
	state := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: p.Config.Product.ID, RepositoryID: string(p.Config.Product.RepositoryID), WorkItemID: item.ID, WorkItemTitle: item.Title, Backend: p.reviewer().Backend, ConfigRevision: p.Config.Revision(), Build: p.Build, Status: runstate.StatusPending, OutsideFile: &landing, TargetBranch: target}
	state, lease, err := p.reserveRun(ctx, state)
	var capacity runstate.CapacityError
	var existing runstate.ExistingWorkItemError
	switch {
	case errors.As(err, &capacity), errors.As(err, &existing):
		// Another run that brings a file in holds the place, or another process
		// reserved this one; the record is looked at again at the next pull.
		return Outcome{WorkItemID: item.ID, Summary: err.Error()}, err
	case err != nil:
		return Outcome{}, err
	}
	defer lease.Release()
	withLicense := ""
	if landing.License != nil {
		withLicense = fmt.Sprintf(", with its license file %s from %s", outsidefile.PinnedPath(landing.License.Record), landing.License.Fetched.FinalURL)
	}
	record.note(ctx, fmt.Sprintf("Yoyodyne started run %s to bring in %s from %s%s, as the record %s asks; it is reviewed independently and merges through a pull request.", runID, outsidefile.PinnedPath(landing.Record), fetched.FinalURL, withLicense, landing.Record))
	a, err := p.outsideFileRun(ctx, state, item)
	if err != nil {
		return Outcome{}, err
	}
	worktree, err := p.Worktrees.Create(ctx, gitworktree.CreateRequest{ResumeCreation: true, RunID: state.RunID, WorkItemID: item.ID, BaseRef: state.TargetBranch, TargetBranch: state.TargetBranch})
	if err != nil {
		_, failErr := a.fail(stoppedBy(runstate.StopHarness, fmt.Errorf("cut the checkout for the run that brings a file in: %w", err)), runstate.StatusFailed)
		return p.settleOutsideFile(ctx, store, state.RunID, record, failErr)
	}
	a.recordWorktree(worktree)
	a.state.Status, a.state.Phase = runstate.StatusRunning, runstate.PhaseDeveloping
	if err := p.Store.Save(a.state); err != nil {
		return Outcome{}, err
	}
	if err := a.writeOutsideFile(ctx, fetched, license); err != nil {
		_, failErr := a.fail(err, runstate.StatusFailed)
		return p.settleOutsideFile(ctx, store, state.RunID, record, failErr)
	}
	a.observe(ctx, deliveryDevelop, "produced")
	_, err = a.verifyReviewAndFinish(ctx)
	return p.settleOutsideFile(ctx, store, state.RunID, record, err)
}

// continueOutsideFile takes up a run of this record and attempt that is
// already recorded: one another process holds is left to it, one that ended is
// settled, and one in flight with nobody behind it is continued from its
// record where its change was committed, and otherwise ended so the next
// attempt fetches again.
func (p Pipeline) continueOutsideFile(ctx context.Context, store OutsideFileStore, existing runstate.State, record outsideFileRecorder) (Outcome, error) {
	if existing.Status.Terminal() {
		if existing.Status == runstate.StatusSucceeded {
			// Landed, or waiting in the merge queue: the run said so as it ended,
			// and nothing more is owed until the record changes.
			return Outcome{RunID: existing.RunID, WorkItemID: existing.WorkItemID, Status: existing.Status, Phase: existing.Phase, PullRequest: existing.PullRequest}, nil
		}
		return p.settleOutsideFile(ctx, store, existing.RunID, record, nil)
	}
	state, lease, err := p.Store.AdoptRun(ctx, existing.RunID)
	if errors.Is(err, runstate.ErrRunHeld) {
		return Outcome{RunID: existing.RunID, WorkItemID: existing.WorkItemID, Status: existing.Status, Phase: existing.Phase}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	defer lease.Release()
	if state.OutsideFile == nil {
		return Outcome{}, fmt.Errorf("run %s is not a run that brings a file in", state.RunID)
	}
	item := outsideFileItem(*state.OutsideFile)
	item.Status = "in_progress"
	p.Tracker = &documentTracker{item: item}
	a, err := p.outsideFileRun(ctx, state, item)
	if err != nil {
		return Outcome{}, err
	}
	a.resumeDeliveryTrial()
	a.carryReviewEvidence()
	switch {
	case state.Integration != nil:
		a.outcome.Integration = &gitworktree.Integration{Branch: state.Branch, TargetBranch: state.Integration.TargetBranch, SourceCommit: state.Integration.SourceCommit, TargetCommit: state.Integration.TargetCommit, PreviousTargetCommit: state.Integration.PreviousTargetCommit, ThroughPullRequest: state.Integration.ThroughPullRequest}
		_, err = a.finish(ctx)
	case state.Phase == runstate.PhaseIntegrating:
		if err = a.recoverDocumentIntegration(ctx); err == nil {
			if a.state.Integration != nil {
				_, err = a.finish(ctx)
			} else {
				_, err = a.verifyReviewAndFinish(ctx)
			}
		}
	case state.HarnessCommit != "" && state.WorktreePath != "":
		_, err = a.verifyReviewAndFinish(ctx)
	default:
		// Nothing of the fetch was committed, so nothing is left to continue; the
		// next attempt fetches again.
		_, err = a.fail(stoppedBy(runstate.StopOutside, errors.New("the run stopped before the file it fetched was committed, so the next attempt fetches it again")), runstate.StatusFailed)
	}
	return p.settleOutsideFile(ctx, store, state.RunID, record, err)
}

// outsideFileRun is the active run a run that brings a file in is carried by.
func (p Pipeline) outsideFileRun(ctx context.Context, state runstate.State, item beads.WorkItem) (*activeRun, error) {
	publishing, skipped, err := p.resolvePublishing(ctx)
	if err != nil {
		return nil, err
	}
	invariants, err := p.loadInvariants()
	if err != nil {
		return nil, err
	}
	landing := state.OutsideFile
	a := &activeRun{pipeline: p, state: state, item: item, claimed: true, publishing: publishing, invariants: invariants,
		context: fmt.Sprintf("Land the pinned outside file %s exactly as the harness fetched it from %s, with its record %s. No developer may change it.\n", outsidefile.PinnedPath(landing.Record), landing.URL, landing.Record),
		outcome: Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: state.Status, Phase: state.Phase, PublishSkipped: skipped, Summary: "Bring in a pinned outside file from a record on the target branch.", PullRequest: state.PullRequest}}
	a.worktree = gitworktree.Worktree{RunID: state.RunID, WorkItemID: state.WorkItemID, Path: state.WorktreePath, Branch: state.Branch, BaseCommit: state.BaseCommit, TargetBranch: state.TargetBranch, HarnessCommit: state.HarnessCommit}
	a.outcome.Branch, a.outcome.WorktreePath, a.outcome.BaseCommit = state.Branch, state.WorktreePath, state.BaseCommit
	if err := a.claim(ctx); err != nil {
		return nil, err
	}
	if state.WorktreePath == "" {
		if err := a.beginDeliveryTrial(); err != nil {
			return nil, err
		}
		a.observe(ctx, deliveryClaim, "claimed")
	}
	return a, nil
}

// readTargetRecord reads the record as the target branch held it when it was
// chosen, and refuses one whose content is no longer what was chosen.
func (p Pipeline) readTargetRecord(ctx context.Context, landing runstate.OutsideFileLanding) (outsidefile.Record, error) {
	tree, ok := p.Worktrees.(targetReader)
	if !ok {
		return outsidefile.Record{}, errors.New("this worktree manager cannot read the target branch's records")
	}
	file, err := tree.FileAtCommit(ctx, landing.TargetCommit, landing.Record, outsidefile.MaxRecordBytes)
	if err != nil {
		return outsidefile.Record{}, err
	}
	if outsidefile.Checksum(file.Content) != landing.RecordDigest {
		return outsidefile.Record{}, fmt.Errorf("the record %s at %s is not the one chosen", landing.Record, landing.TargetCommit)
	}
	return outsidefile.ParseRecord(landing.Record, file.Content)
}

// writeOutsideFile writes what the fetch brought and the record's harness
// fields into the run's worktree through the confined writer — the license's
// too, where license is what the fetch of the license the run brings in with
// the file brought — and commits and publishes them as the run's one attempt.
func (a *activeRun) writeOutsideFile(ctx context.Context, fetched outsidefile.Fetched, license *outsidefile.Fetched) error {
	landing := a.state.OutsideFile
	root, err := repowrite.NewRoot(a.worktree.Path)
	if err != nil {
		return err
	}
	if err := writePinnedFile(root, *landing, fetched); err != nil {
		return err
	}
	if landing.License != nil {
		if license == nil {
			return errors.New("the run brings in a license file with its file and has nothing fetched for it")
		}
		if err := writePinnedFile(root, *landing.License, *license); err != nil {
			return err
		}
	}
	if err := a.commitAttempt(ctx); err != nil {
		return err
	}
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return err
	}
	summary, err := a.pipeline.Worktrees.SummarizeChanges(ctx, a.worktree)
	if err != nil {
		return err
	}
	a.recordChanges(summary)
	return a.publishAttempt(ctx)
}

// writePinnedFile writes one fetched file and its record's harness fields. The
// worktree's record has to be the one that was chosen: a target branch that
// moved on and changed it is a new landing, and this one stops without judging
// anything.
func writePinnedFile(root repowrite.Root, landing runstate.OutsideFileLanding, fetched outsidefile.Fetched) error {
	current, present, err := worktreeReader(root)(landing.Record, outsidefile.MaxRecordBytes)
	if err != nil {
		return err
	}
	if !present || outsidefile.Checksum(current) != landing.RecordDigest {
		return stoppedBy(runstate.StopOutside, fmt.Errorf("the record %s on the target branch changed after it was chosen, so this landing stops and the changed record is looked at afresh", landing.Record))
	}
	written, err := outsidefile.WithHarnessFields(landing.Record, current, fetched)
	if err != nil {
		return err
	}
	if _, err := root.WriteFile(outsidefile.PinnedPath(landing.Record), fetched.Content); err != nil {
		return err
	}
	_, err = root.WriteFile(landing.Record, written)
	return err
}

// gateOutsideFile refuses a run that brings a file in whose change is anything
// but its pinned file and its record, and the license file and its record
// where it brings one in with them. It is settled before any check runs.
func (a *activeRun) gateOutsideFile(changed []string) error {
	landing := a.state.OutsideFile
	allowed := landing.Paths()
	var outside []string
	for _, path := range changed {
		if !slices.Contains(allowed, path) {
			outside = append(outside, path)
		}
	}
	if len(changed) == 0 || len(outside) > 0 {
		return fmt.Errorf("a run that brings a file in may change only %s; changed paths: %v", strings.Join(allowed, " and "), changed)
	}
	return nil
}

// outsideFileAccount is what the reviewer is shown in place of a developer's
// account: the record, where the file came from, its size and checksum and
// whether the record gave the checksum, the license file, and the head of the
// file where it is text. What it asks of the reviewer is said with it.
func (a *activeRun) outsideFileAccount() (string, error) {
	landing := a.state.OutsideFile
	root, err := repowrite.NewRoot(a.worktree.Path)
	if err != nil {
		return "", err
	}
	read := worktreeReader(root)
	recordText, _, err := read(landing.Record, outsidefile.MaxRecordBytes)
	if err != nil {
		return "", err
	}
	pinnedPath := outsidefile.PinnedPath(landing.Record)
	content, present, err := read(pinnedPath, outsidefile.CeilingBytes)
	if err != nil {
		return "", err
	}
	var account strings.Builder
	fmt.Fprintf(&account, "The harness brought in the pinned outside file %s, as the record %s on the target branch at %s declares, for %s.\n\n", pinnedPath, landing.Record, landing.TargetCommit, landing.For)
	fmt.Fprintf(&account, "The record as this change leaves it:\n\n%s\n", strings.TrimRight(string(recordText), "\n"))
	if fetched := landing.Fetched; fetched != nil {
		fmt.Fprintf(&account, "\nFetched from: %s\nFinal address after any redirects: %s\nSize: %d bytes\nSHA-256: %s\n", landing.URL, fetched.FinalURL, fetched.Size, fetched.SHA256)
		if fetched.Given {
			account.WriteString("The record gave this checksum before the fetch, and the file matched it.\n")
		} else {
			account.WriteString("The record gave no checksum, so this file is trusted on its first fetch and the checksum above is the one this fetch recorded.\n")
		}
	}
	if license := landing.License; license != nil {
		licenseRecord, _, err := read(license.Record, outsidefile.MaxRecordBytes)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&account, "\nThe same change brings in the license file %s, as the record %s on the target branch declares, because the target branch does not hold it yet and the file's checks read it. Its record as this change leaves it:\n\n%s\n", outsidefile.PinnedPath(license.Record), license.Record, strings.TrimRight(string(licenseRecord), "\n"))
		if fetched := license.Fetched; fetched != nil {
			fmt.Fprintf(&account, "\nFetched from: %s\nFinal address after any redirects: %s\nSize: %d bytes\nSHA-256: %s\n", license.URL, fetched.FinalURL, fetched.Size, fetched.SHA256)
			if fetched.Given {
				account.WriteString("The record gave this checksum before the fetch, and the license file matched it.\n")
			} else {
				account.WriteString("The record gave no checksum, so the license file is trusted on its first fetch and the checksum above is the one this fetch recorded.\n")
			}
		}
		account.WriteString("\n")
	}
	declared, err := outsidefile.ParseRecord(landing.Record, recordText)
	if err == nil {
		licensePath := declared.LicensePath(landing.Record)
		if _, there, err := read(licensePath, outsidefile.CeilingBytes); err == nil && there {
			fmt.Fprintf(&account, "License: %s, with its text in %s, which is in the repository.\n", declared.License, licensePath)
		} else {
			fmt.Fprintf(&account, "License: %s, with its text to be in %s, which is not in the repository.\n", declared.License, licensePath)
		}
	}
	if present {
		head := content
		if len(head) > maxOutsideFileHead {
			head = head[:maxOutsideFileHead]
		}
		if utf8.Valid(head) || utf8.Valid(head[:max(0, len(head)-3)]) {
			fmt.Fprintf(&account, "\nThe first %d bytes of the file:\n\n%s\n", len(head), strings.ToValidUTF8(string(head), ""))
		} else {
			account.WriteString("\nThe file is not text, so none of it is shown.\n")
		}
	}
	account.WriteString("\nRefuse this change where the version the file names for itself differs from the record's, where the license file is missing, or where the file is not what the record says it is. The patch lists the pinned file whole with its digest rather than showing it, because nobody reviews a fetched file line by line; the checksum every run compares is what holds it.\n")
	return account.String(), nil
}

// settleOutsideFile reads back what a run that brings a file in left, writes
// its end on the item its record is for, and decides what becomes of the
// record: brought in, stopped for something about the record, or tried again
// after a wait for a stop that judged nothing.
func (p Pipeline) settleOutsideFile(ctx context.Context, store OutsideFileStore, runID string, record outsideFileRecorder, err error) (Outcome, error) {
	saved, loadErr := p.Store.Load(runID)
	if loadErr != nil {
		return Outcome{}, errors.Join(err, loadErr)
	}
	outcome := Outcome{RunID: saved.RunID, WorkItemID: saved.WorkItemID, Status: saved.Status, Phase: saved.Phase, PullRequest: saved.PullRequest}
	if !saved.Status.Terminal() {
		return outcome, err
	}
	landing := *saved.OutsideFile
	pinnedPath := outsidefile.PinnedPath(landing.Record)
	if landing.License != nil {
		pinnedPath += " with its license file " + outsidefile.PinnedPath(landing.License.Record)
	}
	stop := runstate.StopReason(saved.RecordedStopClass(), saved.Failure)
	switch {
	case saved.Status == runstate.StatusSucceeded && saved.MergeQueue != nil && saved.Integration == nil:
		record.note(ctx, fmt.Sprintf("Yoyodyne's run %s fetched %s from %s (%d bytes, SHA-256 %s); its checks and an independent review passed and the change is admitted to the merge queue, which lands it once the queue's combined change passes its own checks and review.", saved.RunID, pinnedPath, landing.Fetched.FinalURL, landing.Fetched.Size, landing.Fetched.SHA256))
		outcome.Summary = fmt.Sprintf("Admitted %s to the merge queue through run %s.", pinnedPath, saved.RunID)
	case queueRefused(saved):
		why := nonEmpty(oneline.Fold(strings.TrimPrefix(saved.Failure, "the merge queue handed the change back: "), 900), "the merge queue refused it")
		record.note(ctx, fmt.Sprintf("The merge queue did not land %s from run %s: %s. No developer is asked to repair it; a later pull starts a fresh run from the record as the target branch then holds it.", pinnedPath, saved.RunID, why))
		if queueErr := p.queueRefusedOutsideFile(store, landing, why); queueErr != nil {
			return outcome, queueErr
		}
		outcome.Summary = fmt.Sprintf("The merge queue refused run %s: %s", saved.RunID, why)
	case saved.Status == runstate.StatusSucceeded:
		where := "it merged"
		if saved.Integration == nil {
			where = "it waits for its pull request to be merged"
		}
		record.note(ctx, fmt.Sprintf("Yoyodyne's run %s brought in %s from %s (%d bytes, SHA-256 %s), reviewed independently; %s.", saved.RunID, pinnedPath, landing.Fetched.FinalURL, landing.Fetched.Size, landing.Fetched.SHA256, where))
		outcome.Summary = fmt.Sprintf("Brought in %s through run %s.", pinnedPath, saved.RunID)
	case saved.DocumentJudged():
		// A check that failed, a reviewer who refused, or a path the change may
		// not touch is a judgement of the file the record brings in.
		why := fmt.Sprintf("run %s did not land the file: %s", saved.RunID, nonEmpty(oneline.Fold(stop, 900), "it was refused"))
		record.note(ctx, fmt.Sprintf("Yoyodyne's run %s did not bring in %s: %s. The record is stopped until it changes on the target branch.", saved.RunID, pinnedPath, nonEmpty(oneline.Fold(stop, 900), "it was refused")))
		if stopErr := p.stopLanding(store, runstate.OutsideFileAttempt{Record: landing.Record, RecordDigest: landing.RecordDigest, Attempt: landing.Attempt}, landing, why); stopErr != nil {
			return outcome, stopErr
		}
		outcome.Summary = why
	case refusedForGood(err):
		// The change passed its checks and its review, and the promotion found the
		// record does not show it earned them, for a cause a later run would meet
		// again (lastingRefusal): no developer changes what the next run does. The
		// record is stopped rather than tried again, and the report says which
		// check the promotion found missing. The fault is the harness's rather
		// than the record's, so the stop names the build that made it and lifts
		// once another build is running (dueOutsideFile), and the next run is a
		// fresh attempt rather than this one again. A refusal for a passing cause — the
		// change could not be named, or moved under its evidence — takes the wait
		// below like any stop that judged nothing. So does a run another process
		// ended, whose error is gone; the attempts it is given still bound it.
		why := fmt.Sprintf("run %s passed its checks and its review, and the merge step refused it because %s", saved.RunID, strings.TrimPrefix(oneline.Fold(lastingCause(err), 900), ErrIntegrationUnearned.Error()+": "))
		record.note(ctx, fmt.Sprintf("Yoyodyne's run %s did not bring in %s: %s. Another run of this build would be refused the same way, so the record is stopped until it changes on the target branch or a new build of the harness is running.", saved.RunID, pinnedPath, strings.TrimPrefix(why, "run "+saved.RunID+" ")))
		if stopErr := p.stopLanding(store, runstate.OutsideFileAttempt{Record: landing.Record, RecordDigest: landing.RecordDigest, Attempt: min(landing.Attempt+1, runstate.MaxOutsideFileRuns), StoppedBuild: p.Build}, landing, why); stopErr != nil {
			return outcome, stopErr
		}
		outcome.Summary = why
	default:
		why := fmt.Sprintf("run %s stopped without judging the file: %s", saved.RunID, nonEmpty(oneline.Fold(stop, 600), "it recorded no reason"))
		record.note(ctx, fmt.Sprintf("Yoyodyne's run %s stopped bringing in %s without judging it: %s. It is tried again after a wait.", saved.RunID, pinnedPath, nonEmpty(oneline.Fold(stop, 600), "it recorded no reason")))
		if waitErr := p.waitOutOutsideFile(store, landing, true, why); waitErr != nil {
			return outcome, waitErr
		}
		outcome.Summary = why
	}
	// A run that ended is settled whatever error came with ending it; the
	// record says what happened.
	return outcome, nil
}

// lastingCause is what a lasting refusal says (refusedForGood), without what
// the error that carried it to the run's end added around it.
func lastingCause(err error) string {
	var refused lastingRefusal
	if errors.As(err, &refused) {
		return refused.Error()
	}
	return err.Error()
}

// outsideFileItem is the bookkeeping item a run that brings a file in is
// recorded under. Its grant names the paths the run may write: the file and
// its record, and the license file and its record where it brings one in.
func outsideFileItem(landing runstate.OutsideFileLanding) beads.WorkItem {
	pinnedPath := outsidefile.PinnedPath(landing.Record)
	description := fmt.Sprintf("Bring in the pinned outside file %s exactly as fetched, as the record %s declares, for %s.", pinnedPath, landing.Record, landing.For)
	if landing.License != nil {
		description += fmt.Sprintf(" Bring in its license file %s with it, as the record %s declares.", outsidefile.PinnedPath(landing.License.Record), landing.License.Record)
	}
	for _, path := range landing.Paths() {
		description += "\nprotected-path grant: " + path
	}
	return beads.WorkItem{ID: landing.WorkItemID(), Title: "Bringing in " + pinnedPath, Status: "open", Description: description}
}

// outsideFileRecorder writes a run's start and end onto the item its record is
// for, so a reader of that item sees the file being brought in without finding
// the run. The writes are bounded and never stop the run.
type outsideFileRecorder struct {
	tracker WorkTracker
	landing runstate.OutsideFileLanding
}

func (r outsideFileRecorder) note(ctx context.Context, note string) {
	if r.tracker == nil || r.landing.For == "" {
		return
	}
	bounded, cancel := trackerRecordContext(ctx)
	defer cancel()
	if _, err := r.tracker.Show(bounded, r.landing.For); err != nil {
		return
	}
	_, _ = r.tracker.RecordOutcome(bounded, r.landing.For, note)
}
