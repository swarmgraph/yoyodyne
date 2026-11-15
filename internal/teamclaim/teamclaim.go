// Package teamclaim decides when this machine takes, renews, takes over, and
// releases its claims on the shared remote, in team mode
// (docs/designs/working-alongside-other-contributors.md, section 5).
//
// A claim is the reference refs/yoyodyne/claims/<item id> on the remote every
// factory working the repository shares. It is what stops two factories doing
// the same work: the tracker reaches another machine only once its export is
// committed and fetched, and the remote's compare and swap is immediate. Every
// write here is a push with a lease naming what the reference is expected to be,
// so the remote decides every race and this package only reads its answer.
//
// Only the harness writes these references. Nothing here is reachable from an
// agent: the pushes go through the harness's own Git manager, as its pull request
// branches do.
package teamclaim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RenewEvery is how often a held claim is renewed.
const RenewEvery = 15 * time.Minute

// ReleaseAfter is how long after it was taken a claim may first be released
// for its run no longer holding the item. A run takes its claim before it
// reserves its run record or claims the tracker, so for a moment a fresh claim
// has nothing on this machine behind it; within this time it is renewed as
// held instead. The cost is that an item whose run failed at once stays
// claimed this long.
const ReleaseAfter = RenewEvery

// ExpireAfter is how long a claim may go unrenewed before another factory may
// take it over. It is eight renewals, so only a holder that has been unable to
// reach the remote for two hours, or has gone, loses one.
const ExpireAfter = 2 * time.Hour

// Remote is the shared remote's claim references. It is satisfied by
// *gitworktree.Manager.
type Remote interface {
	FetchClaims(ctx context.Context) error
	FetchedClaim(ctx context.Context, workItemID string) (gitworktree.ClaimReading, error)
	FetchedTeamPause(ctx context.Context) (gitworktree.TeamPauseReading, error)
	PushClaim(ctx context.Context, workItemID, record, parent, expected string, now time.Time) (string, error)
	DeleteClaim(ctx context.Context, workItemID, expected string) error
}

// Store is this machine's record of the claims it holds. It is satisfied by
// *runstate.TeamClaimStore.
type Store interface {
	Lock(ctx context.Context, workItemID string) (func(), error)
	Load(workItemID string) (runstate.TeamClaim, bool, error)
	Save(claim runstate.TeamClaim) error
	List() ([]runstate.TeamClaim, error)
}

// Record is what a claim commit says, as JSON in its message. It is what
// another factory reads to name the holder.
type Record struct {
	// Factory names the factory holding the claim: the product, the machine,
	// and the forge account it acts as, where those are known.
	Factory    string    `json:"factory"`
	WorkItemID string    `json:"work_item"`
	RunID      string    `json:"run"`
	TakenAt    time.Time `json:"taken_at"`
	RenewedAt  time.Time `json:"renewed_at"`
	// PreviousHolder is the factory whose expired claim this one took over.
	PreviousHolder string `json:"previous_holder,omitempty"`
}

// HeldError is an item another factory holds the claim on. It is not a
// failure: the item is skipped this turn and the holder is named.
type HeldError struct {
	WorkItemID string
	// Holder names who holds the claim, or is empty where the claim could not
	// be read back.
	Holder string
	// RenewedAt is when the holder last renewed it, where known.
	RenewedAt time.Time
	// Age is how long before the refusal that was. The sentence a person reads
	// says this rather than the moment, so it needs no time zone.
	Age time.Duration
}

func (e HeldError) Error() string {
	holder := e.Holder
	if holder == "" {
		holder = "another factory"
	}
	if e.RenewedAt.IsZero() {
		return fmt.Sprintf("%s is claimed on the shared remote by %s", e.WorkItemID, holder)
	}
	return fmt.Sprintf("%s is claimed on the shared remote by %s, last renewed %s", e.WorkItemID, holder, renewedAgo(e.Age))
}

// renewedAgo says how long ago a claim was renewed, in whole minutes.
func renewedAgo(age time.Duration) string {
	minutes := int(age.Round(time.Minute) / time.Minute)
	switch {
	case minutes < 1:
		return "less than a minute ago"
	case minutes == 1:
		return "1 minute ago"
	default:
		return fmt.Sprintf("%d minutes ago", minutes)
	}
}

// UnreachableError is a claim that could not be taken because the remote could
// not be read or written. In team mode no new work starts on a reading of the
// claims that could not be refreshed, so the item is skipped this turn.
type UnreachableError struct {
	WorkItemID string
	Err        error
}

func (e UnreachableError) Error() string {
	return fmt.Sprintf("%s was not claimed because the shared remote could not be reached to claim it: %v", e.WorkItemID, e.Err)
}

func (e UnreachableError) Unwrap() error { return e.Err }

// Claims takes and keeps this machine's claims.
type Claims struct {
	Remote Remote
	Store  Store
	// Factory is who this machine names itself as in a claim, so another
	// factory can say who holds an item. It decides nothing: which claims are
	// this machine's own is read from Store, never from a name another machine
	// could share.
	Factory string
	// Identity reads the factory identity a claim names: the product id, the
	// machine identity kept in the home, and the forge account
	// (factory.Reader.Name). Where it is set, Take names the identity it reads;
	// where that cannot be read, Take names Factory instead, because a forge
	// that will not say which account the harness acts as is no reason on its
	// own to leave an item unclaimed. Like Factory, it decides nothing.
	Identity FactoryIdentity
	Clock    execution.Clock
}

// FactoryIdentity reads the name a claim gives the factory holding it.
type FactoryIdentity interface {
	Name(ctx context.Context) (string, error)
}

// factoryName is the name Take records: the factory identity where it can be
// read, and Factory where it cannot.
func (c Claims) factoryName(ctx context.Context) string {
	if c.Identity != nil {
		if name, err := c.Identity.Name(ctx); err == nil && strings.TrimSpace(name) != "" {
			return name
		}
	}
	return c.Factory
}

func (c Claims) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now()
	}
	return c.Clock.Now()
}

func (c Claims) validate() error {
	var problems []error
	if c.Remote == nil {
		problems = append(problems, errors.New("team claims need the shared remote"))
	}
	if c.Store == nil {
		problems = append(problems, errors.New("team claims need somewhere to record what they hold"))
	}
	if strings.TrimSpace(c.Factory) == "" {
		problems = append(problems, errors.New("team claims need the factory's name"))
	}
	return errors.Join(problems...)
}

// Take claims one item on the shared remote for a run the harness chose to
// start. It fetches the claims and the team pause first, and claims nothing
// while the team pause is set (pause.go). Otherwise it pushes the claim with a
// lease that fails if the reference exists. A claim this factory left from an
// earlier run of the item is taken on with a lease naming its commit, and one
// another factory has not renewed for ExpireAfter is taken over the same way,
// naming that factory as the previous holder.
//
// An item another factory holds ends on HeldError, a team pause on PausedError,
// and a remote that could not be reached on UnreachableError. Whichever it is,
// nothing was claimed.
func (c Claims) Take(ctx context.Context, workItemID, runID string) (runstate.TeamClaim, error) {
	return c.take(ctx, workItemID, runID, true)
}

// TakeNamed is Take for an item the operator named on this machine, which is
// that operator's exception to the team pause as it is to the intake hold. The
// remote must still be reached and the claim still won.
func (c Claims) TakeNamed(ctx context.Context, workItemID, runID string) (runstate.TeamClaim, error) {
	return c.take(ctx, workItemID, runID, false)
}

func (c Claims) take(ctx context.Context, workItemID, runID string, honourPause bool) (runstate.TeamClaim, error) {
	if err := c.validate(); err != nil {
		return runstate.TeamClaim{}, err
	}
	unlock, err := c.Store.Lock(ctx, workItemID)
	if err != nil {
		return runstate.TeamClaim{}, err
	}
	defer unlock()
	if err := c.Remote.FetchClaims(ctx); err != nil {
		return runstate.TeamClaim{}, UnreachableError{WorkItemID: workItemID, Err: err}
	}
	if honourPause {
		if err := readPause(ctx, c.Remote, workItemID); err != nil {
			return runstate.TeamClaim{}, err
		}
	}
	reading, err := c.Remote.FetchedClaim(ctx, workItemID)
	if err != nil {
		return runstate.TeamClaim{}, err
	}
	now := c.now()
	name := c.factoryName(ctx)
	record := Record{Factory: name, WorkItemID: workItemID, RunID: runID, TakenAt: now, RenewedAt: now}
	expected := ""
	if reading.Exists {
		own, err := c.ownClaim(workItemID, reading.Commit)
		if err != nil {
			return runstate.TeamClaim{}, err
		}
		holder, renewed := readRecord(reading)
		switch {
		case own:
			// This machine's own claim, left by an earlier run of the item that
			// has not released it yet: the run starting now holds it from here.
		case now.Sub(renewed) >= ExpireAfter:
			record.PreviousHolder = holderName(holder)
		default:
			return runstate.TeamClaim{}, HeldError{WorkItemID: workItemID, Holder: holder.Factory, RenewedAt: renewed, Age: now.Sub(renewed)}
		}
		expected = reading.Commit
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return runstate.TeamClaim{}, err
	}
	commit, err := c.Remote.PushClaim(ctx, workItemID, string(encoded), "", expected, now)
	switch {
	case errors.Is(err, gitworktree.ErrClaimLeaseRefused):
		return runstate.TeamClaim{}, c.heldAfterRace(ctx, workItemID)
	case err != nil:
		return runstate.TeamClaim{}, UnreachableError{WorkItemID: workItemID, Err: err}
	}
	claim := runstate.TeamClaim{
		WorkItemID:     workItemID,
		RunID:          runID,
		Factory:        name,
		Commit:         commit,
		TakenAt:        now,
		RenewedAt:      now,
		PreviousHolder: record.PreviousHolder,
	}
	if err := c.Store.Save(claim); err != nil {
		// A claim the remote holds that this machine cannot remember would never
		// be renewed or released, so it is given back at once.
		if releaseErr := c.Remote.DeleteClaim(ctx, workItemID, commit); releaseErr != nil {
			return runstate.TeamClaim{}, fmt.Errorf("record the claim on %s: %w (and giving it back on the remote failed: %v)", workItemID, err, releaseErr)
		}
		return runstate.TeamClaim{}, fmt.Errorf("record the claim on %s: %w", workItemID, err)
	}
	return claim, nil
}

// ownClaim reports whether the claim the remote holds on an item, at commit,
// is this machine's: its own record of the item is still held and names that
// same commit. Nothing else counts, so another factory under the same name,
// such as a second home on this machine, never takes a live claim over, and a
// machine whose host name changed still knows its own claims.
func (c Claims) ownClaim(workItemID, commit string) (bool, error) {
	local, found, err := c.Store.Load(workItemID)
	if err != nil {
		return false, err
	}
	return found && local.Held() && local.Commit == commit, nil
}

// heldAfterRace names whoever won the push this claim lost.
func (c Claims) heldAfterRace(ctx context.Context, workItemID string) error {
	held := HeldError{WorkItemID: workItemID}
	if err := c.Remote.FetchClaims(ctx); err != nil {
		return held
	}
	reading, err := c.Remote.FetchedClaim(ctx, workItemID)
	if err != nil || !reading.Exists {
		return held
	}
	holder, renewed := readRecord(reading)
	held.Holder, held.RenewedAt, held.Age = holder.Factory, renewed, c.now().Sub(renewed)
	return held
}

// Release gives back the claim on one item, if this machine holds it, without
// waiting for the next upkeep. It is what a run that never started calls.
func (c Claims) Release(ctx context.Context, workItemID, runID string) error {
	if err := c.validate(); err != nil {
		return err
	}
	unlock, err := c.Store.Lock(ctx, workItemID)
	if err != nil {
		return err
	}
	defer unlock()
	claim, found, err := c.Store.Load(workItemID)
	if err != nil || !found || !claim.Held() || claim.RunID != runID {
		return err
	}
	_, err = c.release(ctx, claim)
	return err
}

// Upkeep is what one pass over the held claims did.
type Upkeep struct {
	Renewed  []string `json:"renewed,omitempty"`
	Released []string `json:"released,omitempty"`
	// Lost are the claims found to be another factory's when this machine came
	// to renew or release them. They were left alone.
	Lost []string `json:"lost,omitempty"`
	// Problems are renewals and releases that did not reach the remote. Each is
	// tried again on the next pass and charges nothing.
	Problems []string `json:"problems,omitempty"`
}

// Holding says whether the run behind a claim still holds its item on this
// machine: it is in flight, or its item is still claimed or blocked here, which
// is a run waiting for a merge or stopped and kept for a decision.
type Holding func(claim runstate.TeamClaim) (bool, error)

// Tend renews every claim this machine holds that is due, and releases every
// claim whose run no longer holds its item. It is called on every pass of the
// watch loop, and nothing it fails at stops the pass.
func (c Claims) Tend(ctx context.Context, holding Holding) (Upkeep, error) {
	if err := c.validate(); err != nil {
		return Upkeep{}, err
	}
	claims, err := c.Store.List()
	if err != nil {
		return Upkeep{}, fmt.Errorf("read the claims this machine holds: %w", err)
	}
	var upkeep Upkeep
	for _, listed := range claims {
		if !listed.Held() {
			continue
		}
		c.tendOne(ctx, listed.WorkItemID, holding, &upkeep)
	}
	return upkeep, nil
}

func (c Claims) tendOne(ctx context.Context, workItemID string, holding Holding, upkeep *Upkeep) {
	unlock, err := c.Store.Lock(ctx, workItemID)
	if err != nil {
		upkeep.Problems = append(upkeep.Problems, fmt.Sprintf("the claim on %s could not be looked after: %v", workItemID, err))
		return
	}
	defer unlock()
	// Read again under the lock: a run may have taken the claim on since the
	// listing, and what is renewed or released is what the record says now.
	claim, found, err := c.Store.Load(workItemID)
	if err != nil {
		upkeep.Problems = append(upkeep.Problems, fmt.Sprintf("the claim on %s could not be read: %v", workItemID, err))
		return
	}
	if !found || !claim.Held() {
		return
	}
	held, err := holding(claim)
	if !held && err == nil && c.now().Sub(claim.TakenAt) < ReleaseAfter {
		// A claim this new may belong to a run that took it after the caller
		// read which runs and items are held, and before it reserved its run or
		// claimed the tracker: releasing it would leave that run working with
		// no claim. So it is kept until it is older than any such gap.
		held = true
	}
	if err != nil {
		// Whether the run still holds its item could not be read, so the claim
		// is kept and renewed: releasing on a reading that failed would let
		// another factory start work this one may still be doing.
		held = true
	}
	if !held {
		lost, err := c.release(ctx, claim)
		switch {
		case err != nil:
			upkeep.Problems = append(upkeep.Problems, err.Error())
		case lost != "":
			upkeep.Lost = append(upkeep.Lost, lost)
		default:
			upkeep.Released = append(upkeep.Released, workItemID)
		}
		return
	}
	if c.now().Sub(claim.RenewedAt) < RenewEvery {
		return
	}
	lost, err := c.renew(ctx, claim)
	switch {
	case err != nil:
		upkeep.Problems = append(upkeep.Problems, err.Error())
	case lost != "":
		upkeep.Lost = append(upkeep.Lost, lost)
	default:
		upkeep.Renewed = append(upkeep.Renewed, workItemID)
	}
}

// renew pushes a new claim commit on top of the one this machine last pushed,
// with a lease naming it. A refused lease is a claim another factory took over,
// which is recorded and reported rather than fought for. A remote that could
// not be reached is recorded on the claim and tried again on the next pass.
func (c Claims) renew(ctx context.Context, claim runstate.TeamClaim) (string, error) {
	now := c.now()
	record := Record{Factory: claim.Factory, WorkItemID: claim.WorkItemID, RunID: claim.RunID, TakenAt: claim.TakenAt, RenewedAt: now, PreviousHolder: claim.PreviousHolder}
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	commit, err := c.Remote.PushClaim(ctx, claim.WorkItemID, string(encoded), claim.Commit, claim.Commit, now)
	switch {
	case errors.Is(err, gitworktree.ErrClaimLeaseRefused):
		return c.lose(ctx, claim, "renewing")
	case err != nil:
		claim.RenewalProblem = err.Error()
		if saveErr := c.Store.Save(claim); saveErr != nil {
			err = errors.Join(err, saveErr)
		}
		return "", fmt.Errorf("the claim on %s could not be renewed and will be tried again: %w", claim.WorkItemID, err)
	}
	claim.Commit = commit
	claim.RenewedAt = now
	claim.RenewalProblem = ""
	if err := c.Store.Save(claim); err != nil {
		return "", fmt.Errorf("the claim on %s was renewed and the renewal could not be recorded: %w", claim.WorkItemID, err)
	}
	return "", nil
}

// release deletes the claim reference with a lease naming this machine's own
// commit. A reference that is no longer its own is left alone and reported.
func (c Claims) release(ctx context.Context, claim runstate.TeamClaim) (string, error) {
	err := c.Remote.DeleteClaim(ctx, claim.WorkItemID, claim.Commit)
	switch {
	case errors.Is(err, gitworktree.ErrClaimLeaseRefused):
		return c.lose(ctx, claim, "releasing")
	case err != nil:
		return "", fmt.Errorf("the claim on %s could not be released and will be tried again: %w", claim.WorkItemID, err)
	}
	released := c.now()
	claim.ReleasedAt = &released
	if err := c.Store.Save(claim); err != nil {
		return "", fmt.Errorf("the claim on %s was released and the release could not be recorded: %w", claim.WorkItemID, err)
	}
	return "", nil
}

// lose records a claim found to be somebody else's, and says whose.
func (c Claims) lose(ctx context.Context, claim runstate.TeamClaim, doing string) (string, error) {
	holder := "another factory, or nobody"
	if err := c.Remote.FetchClaims(ctx); err == nil {
		if reading, err := c.Remote.FetchedClaim(ctx, claim.WorkItemID); err == nil {
			if reading.Exists {
				record, _ := readRecord(reading)
				holder = holderName(record)
			} else {
				holder = "nobody: the reference had already been removed"
			}
		}
	}
	lost := fmt.Sprintf("the claim on %s was no longer this machine's when it came to %s it, so it was left alone; it is held by %s", claim.WorkItemID, doing, holder)
	released := c.now()
	claim.ReleasedAt = &released
	claim.Lost = lost
	if err := c.Store.Save(claim); err != nil {
		return "", fmt.Errorf("%s, and that could not be recorded: %w", lost, err)
	}
	return lost, nil
}

// readRecord reads the claim a reference points at, and when it was last
// renewed. A record that does not decode, such as one a person pushed by hand,
// names nobody and counts as renewed when its commit was made.
func readRecord(reading gitworktree.ClaimReading) (Record, time.Time) {
	var record Record
	if err := json.Unmarshal([]byte(reading.Record), &record); err != nil {
		return Record{}, reading.CommittedAt
	}
	renewed := record.RenewedAt
	if renewed.IsZero() {
		renewed = reading.CommittedAt
	}
	return record, renewed
}

func holderName(record Record) string {
	if record.Factory == "" {
		return "a holder whose claim does not name it"
	}
	return record.Factory
}
