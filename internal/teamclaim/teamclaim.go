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

// ExpireAfter is how long a claim may go unrenewed before another factory may
// take it over. It is eight renewals, so only a holder that has been unable to
// reach the remote for two hours, or has gone, loses one.
const ExpireAfter = 2 * time.Hour

// Remote is the shared remote's claim references. It is satisfied by
// *gitworktree.Manager.
type Remote interface {
	FetchClaims(ctx context.Context) error
	FetchedClaim(ctx context.Context, workItemID string) (gitworktree.ClaimReading, error)
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
}

func (e HeldError) Error() string {
	holder := e.Holder
	if holder == "" {
		holder = "another factory"
	}
	if e.RenewedAt.IsZero() {
		return fmt.Sprintf("%s is claimed on the shared remote by %s", e.WorkItemID, holder)
	}
	return fmt.Sprintf("%s is claimed on the shared remote by %s, last renewed at %s", e.WorkItemID, holder, e.RenewedAt.UTC().Format(time.RFC3339))
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
	// Factory is who this machine names itself as in a claim. A claim found on
	// the remote under the same name is this machine's own, left by an earlier
	// run of the item, and a later run takes it on rather than waiting for it.
	Factory string
	Clock   execution.Clock
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

// Take claims one item on the shared remote for a run that is about to start
// it. It fetches the claims first, then pushes the claim with a lease that
// fails if the reference exists. A claim this factory left from an earlier run
// of the item is taken on with a lease naming its commit, and one another
// factory has not renewed for ExpireAfter is taken over the same way, naming
// that factory as the previous holder.
//
// An item another factory holds ends on HeldError, and a remote that could not
// be reached ends on UnreachableError. Either way nothing was claimed.
func (c Claims) Take(ctx context.Context, workItemID, runID string) (runstate.TeamClaim, error) {
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
	reading, err := c.Remote.FetchedClaim(ctx, workItemID)
	if err != nil {
		return runstate.TeamClaim{}, err
	}
	now := c.now()
	record := Record{Factory: c.Factory, WorkItemID: workItemID, RunID: runID, TakenAt: now, RenewedAt: now}
	expected := ""
	if reading.Exists {
		holder, renewed := readRecord(reading)
		switch {
		case holder.Factory == c.Factory:
			// This factory's own claim, left by an earlier run of the item that
			// has not released it yet: the run starting now holds it from here.
		case now.Sub(renewed) >= ExpireAfter:
			record.PreviousHolder = holderName(holder)
		default:
			return runstate.TeamClaim{}, HeldError{WorkItemID: workItemID, Holder: holder.Factory, RenewedAt: renewed}
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
		Factory:        c.Factory,
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
	held.Holder, held.RenewedAt = holder.Factory, renewed
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
