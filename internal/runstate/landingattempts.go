package runstate

// What happened to an approved change on its way onto a target branch that
// moved under it: each landing attempt other work beat it to, what the replay
// that followed made of it, and the attempts a run gets to bring a change that
// conflicted at landing level with the target.
//
// Losing a landing says nothing about the change, and neither does a replay
// onto the newer target that applies cleanly and passes its checks, so such a
// replay is recorded here and not counted toward the limit on lost landings
// (lostRacesBeforeGivingBackTheSlot in the orchestrator). A conflict is about
// the change, but each of the first MaxReconciliations at landing is answered by
// an attempt of its own rather than out of the repair attempts the review
// rounds draw on, because a run whose reviews spent those attempts would
// otherwise stop with approved work it never got to bring up to date. The limit
// is decision 3 of the design for approved changes that no longer merge
// (docs/designs/approved-changes-that-no-longer-merge.md).

import (
	"errors"
	"fmt"
	"time"
)

// MaxLostLandings bounds how many lost landings one run keeps an account of.
// A replay that applies cleanly is not counted toward any limit, so a target
// that keeps moving can lose a run more landings than this; the oldest
// accounts are dropped first, and IntegrationRetries still says how many there
// were.
const MaxLostLandings = 32

// ReplayOutcome is what became of the replay that answered one lost landing.
type ReplayOutcome string

const (
	// ReplayPending is a replay not yet landed or handed back, or a lost landing
	// that was not replayed because it ended the run.
	ReplayPending ReplayOutcome = ""
	// ReplayClean is a replay that applied cleanly and passed every check on its
	// way to landing. It is not counted as a lost landing.
	ReplayClean ReplayOutcome = "clean"
	// ReplayConflicted is a replay that would not apply onto the newer target.
	ReplayConflicted ReplayOutcome = "conflicted"
	// ReplayStopped is a replay that applied but was then stopped on the change
	// and handed back: a refused path, missing verification, a failing check, or
	// a repair verdict.
	ReplayStopped ReplayOutcome = "stopped"
)

// LostLanding is one landing attempt refused because the target branch had
// moved: other work landed on it first.
type LostLanding struct {
	TargetBranch string `json:"target_branch"`
	// FromCommit is the base the change had been checked and reviewed on, and
	// OntoCommit is where the replay put it: the target as it stood when the
	// landing was lost. OntoCommit is empty for a landing that was not replayed.
	FromCommit string        `json:"from_commit,omitempty"`
	OntoCommit string        `json:"onto_commit,omitempty"`
	Replay     ReplayOutcome `json:"replay,omitempty"`
	LostAt     time.Time     `json:"lost_at"`
}

// Validate reports every contract violation in the record at once.
func (l LostLanding) Validate() error {
	var problems []error
	if !validLocalBranch(l.TargetBranch) {
		problems = append(problems, errors.New("target_branch must be a local branch name"))
	}
	if l.FromCommit != "" && !commitPattern.MatchString(l.FromCommit) {
		problems = append(problems, errors.New("from_commit must be a full commit hash"))
	}
	if l.OntoCommit != "" && !commitPattern.MatchString(l.OntoCommit) {
		problems = append(problems, errors.New("onto_commit must be a full commit hash"))
	}
	switch l.Replay {
	case ReplayPending, ReplayClean, ReplayConflicted, ReplayStopped:
	default:
		problems = append(problems, fmt.Errorf("replay %q is not a known outcome", l.Replay))
	}
	if l.LostAt.IsZero() {
		problems = append(problems, errors.New("lost_at is required"))
	}
	return errors.Join(problems...)
}

// Says is what the run's notes say of this lost landing, numbered as the run
// lost it.
func (l LostLanding) Says(number int) string {
	moved := fmt.Sprintf("Lost landing %d: other work landed on %s first", number, l.TargetBranch)
	if l.OntoCommit == "" {
		return fmt.Sprintf("%s, so the change was not landed from %s; it was not replayed.", moved, commitNamed(l.FromCommit))
	}
	moved = fmt.Sprintf("%s, so the change was moved from %s onto %s", moved, commitNamed(l.FromCommit), commitNamed(l.OntoCommit))
	switch l.Replay {
	case ReplayClean:
		return moved + "; it applied cleanly and passed its checks, so this landing is not counted as lost."
	case ReplayConflicted:
		return moved + "; it conflicted with what other work had changed there."
	case ReplayStopped:
		return moved + "; it applied, then was stopped on the change and handed back."
	default:
		return moved + "; it applied, and the run stopped before it was landed."
	}
}

// commitNamed is how a note names a commit: in full, as the run's other notes
// do, or as the base the change was checked on where the record has none.
func commitNamed(commit string) string {
	if commit == "" {
		return "the base it was checked on"
	}
	return commit
}

// MaxReconciliations is how many attempts one run gets to bring its approved
// change level with the target after it conflicted at landing, one for each
// hand-back for a conflict. The next conflict stops the run as about the work,
// for the development manager. It is held here and nowhere in configuration,
// and it counts every attempt on the run's record, so neither a restart nor a
// new build of the harness gives a run more.
const MaxReconciliations = 3

// HoldMissed is why holding the entries that change the same files did not
// keep a conflict from happening again, recorded on every attempt after a run's
// first. The merge queue holds no entries for a returned change yet, so the
// only reason there is to record is that no hold applied; the reasons a
// hold can fail are added with the hold itself (build piece 2 of the design).
type HoldMissed string

const (
	// HoldMissedNoHold is a conflict met while no hold applied to the
	// change.
	HoldMissedNoHold HoldMissed = "no_hold"
)

// Says is the reason in words, for a sentence.
func (h HoldMissed) Says() string {
	switch h {
	case HoldMissedNoHold:
		return "the merge queue held no entries that change the same files for it"
	default:
		return string(h)
	}
}

// Reconciliation is one attempt a run had to bring its approved change level
// with the target branch after it conflicted there at landing. It is recorded
// when the change is handed to its developer, before the developer is invoked
// (State.UseReconciliation), so a process that dies part-way through comes back
// to an attempt already used rather than a fresh one. It is never counted
// against the repair attempts.
type Reconciliation struct {
	TargetBranch string `json:"target_branch"`
	// TargetCommit is the commit on the target the change conflicted with.
	TargetCommit string    `json:"target_commit,omitempty"`
	Paths        []string  `json:"paths,omitempty"`
	Omitted      int       `json:"omitted,omitempty"`
	UsedAt       time.Time `json:"used_at"`
	// Handback is the hand-back for a conflict that caused this attempt
	// (ConflictHandback).
	Handback string `json:"handback,omitempty"`
	// Attempt is this attempt's number on the run, from 1. A record written
	// before attempts were numbered has none, and is the run's first.
	Attempt int `json:"attempt,omitempty"`
	// HoldMissed is why holding the entries on the same files did not prevent
	// this conflict, on every attempt after the first.
	HoldMissed HoldMissed `json:"hold_missed,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (r Reconciliation) Validate() error {
	var problems []error
	if !validLocalBranch(r.TargetBranch) {
		problems = append(problems, errors.New("target_branch must be a local branch name"))
	}
	if r.TargetCommit != "" && !commitPattern.MatchString(r.TargetCommit) {
		problems = append(problems, errors.New("target_commit must be a full commit hash"))
	}
	if len(r.Paths) > MaxConflictedPaths {
		problems = append(problems, fmt.Errorf("%d conflicted paths are recorded, which exceeds the bound of %d", len(r.Paths), MaxConflictedPaths))
	}
	if r.Omitted < 0 {
		problems = append(problems, errors.New("omitted cannot be negative"))
	}
	if r.UsedAt.IsZero() {
		problems = append(problems, errors.New("used_at is required"))
	}
	if r.Attempt < 0 || r.Attempt > MaxReconciliations {
		problems = append(problems, fmt.Errorf("attempt %d is outside 1 to %d", r.Attempt, MaxReconciliations))
	}
	switch r.HoldMissed {
	case "", HoldMissedNoHold:
	default:
		problems = append(problems, fmt.Errorf("hold_missed %q is not a known reason", r.HoldMissed))
	}
	return errors.Join(problems...)
}

// number is the attempt's number on the run, reading an unnumbered record as
// the first.
func (r Reconciliation) number() int {
	return max(r.Attempt, 1)
}

// Says is what the run's notes say of the reconciliation attempt.
func (r Reconciliation) Says() string {
	on := ""
	if r.TargetCommit != "" {
		on = " at " + commitNamed(r.TargetCommit)
	}
	paths := ""
	if len(r.Paths) > 0 {
		paths = fmt.Sprintf(" on %d path(s)", len(r.Paths)+r.Omitted)
	}
	said := fmt.Sprintf("Reconciliation attempt %d of %d: used. The change conflicted with %s%s%s, and its developer was asked to bring it level with %s; that attempt was not counted against the repair attempts.",
		r.number(), MaxReconciliations, r.TargetBranch, on, paths, r.TargetBranch)
	if r.HoldMissed != "" {
		said += " It was not prevented because " + r.HoldMissed.Says() + "."
	}
	return said
}

// RecordLostLanding adds the account of one lost landing, dropping the oldest
// past MaxLostLandings.
func (s *State) RecordLostLanding(lost LostLanding) {
	s.LostLandings = append(s.LostLandings, lost)
	if extra := len(s.LostLandings) - MaxLostLandings; extra > 0 {
		s.LostLandings = append([]LostLanding(nil), s.LostLandings[extra:]...)
	}
}

// pendingReplay is the latest lost landing whose replay applied and has not yet
// been judged, or nil.
func (s *State) pendingReplay() *LostLanding {
	if len(s.LostLandings) == 0 {
		return nil
	}
	last := &s.LostLandings[len(s.LostLandings)-1]
	if last.OntoCommit == "" || last.Replay != ReplayPending {
		return nil
	}
	return last
}

// JudgeReplay settles the latest replay's outcome, and reports whether
// anything changed. A clean replay is counted in CleanReplays, which is what
// takes it off the count of lost landings. Only a replay still pending is
// settled, so a change that is handed back and then passes is never read as a
// clean replay.
func (s *State) JudgeReplay(outcome ReplayOutcome) bool {
	pending := s.pendingReplay()
	if pending == nil || outcome == ReplayPending {
		return false
	}
	pending.Replay = outcome
	if outcome == ReplayClean {
		s.CleanReplays++
	}
	return true
}

// CountedLostLandings is how many of the landings this run lost since it last
// started again count toward the limit on lost landings: every one but those
// whose replay applied cleanly and passed its checks.
func (s State) CountedLostLandings() int {
	return max(s.IntegrationRetries-s.CleanReplays, 0)
}

// ReconciliationsUsed is how many attempts this run has had to bring a change
// that conflicted at landing level with the target.
func (s State) ReconciliationsUsed() int {
	if s.Reconciliation == nil {
		return len(s.EarlierReconciliations)
	}
	return max(len(s.EarlierReconciliations)+1, s.Reconciliation.number())
}

// ReconciliationsSpent reports whether this run has had every attempt it gets
// (MaxReconciliations), so that the next conflict at landing stops it.
func (s State) ReconciliationsSpent() bool {
	return s.ReconciliationsUsed() >= MaxReconciliations
}

// AllReconciliations is every attempt this run had, oldest first.
func (s State) AllReconciliations() []Reconciliation {
	all := append([]Reconciliation(nil), s.EarlierReconciliations...)
	if s.Reconciliation != nil {
		all = append(all, *s.Reconciliation)
	}
	return all
}

// UseReconciliation records the run's next attempt to bring its change level
// with the target, numbered after those already used, and keeps the one it
// replaces as the latest among the earlier ones. An attempt after the first
// says why holding the entries on the same files did not prevent its conflict,
// which today is always that no hold applied. The caller saves the record
// before the developer is invoked, and never calls this once every attempt is
// used (ReconciliationsSpent).
func (s *State) UseReconciliation(attempt Reconciliation) {
	attempt.Attempt = s.ReconciliationsUsed() + 1
	if attempt.Attempt > 1 && attempt.HoldMissed == "" {
		attempt.HoldMissed = HoldMissedNoHold
	}
	if s.Reconciliation != nil {
		s.EarlierReconciliations = append(append([]Reconciliation(nil), s.EarlierReconciliations...), *s.Reconciliation)
	}
	s.Reconciliation = &attempt
}

func (s State) validateLandingAttempts() []error {
	var problems []error
	if s.CleanReplays < 0 {
		problems = append(problems, errors.New("clean_replays cannot be negative"))
	}
	if s.CleanReplays > s.IntegrationRetries {
		problems = append(problems, fmt.Errorf("clean_replays %d exceeds the %d landing(s) lost", s.CleanReplays, s.IntegrationRetries))
	}
	if len(s.LostLandings) > MaxLostLandings {
		problems = append(problems, fmt.Errorf("lost_landings holds %d entries, which exceeds the %d entry bound", len(s.LostLandings), MaxLostLandings))
	}
	for index, lost := range s.LostLandings {
		if err := lost.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("lost_landings[%d]: %w", index, err))
		}
	}
	if s.Reconciliation != nil {
		if err := s.Reconciliation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("reconciliation: %w", err))
		}
	}
	if len(s.EarlierReconciliations) > 0 && s.Reconciliation == nil {
		problems = append(problems, errors.New("earlier_reconciliations is recorded without a latest reconciliation"))
	}
	if len(s.EarlierReconciliations) >= MaxReconciliations {
		problems = append(problems, fmt.Errorf("earlier_reconciliations holds %d entries, which exceeds the %d entry bound", len(s.EarlierReconciliations), MaxReconciliations-1))
	}
	for index, earlier := range s.EarlierReconciliations {
		if err := earlier.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("earlier_reconciliations[%d]: %w", index, err))
		}
	}
	return problems
}
