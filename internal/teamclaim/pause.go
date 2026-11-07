package teamclaim

// The team pause: the shared form of the intake hold, in team mode
// (docs/designs/working-alongside-other-contributors.md, section 5).
//
// It is the reference refs/yoyodyne/pause on the shared remote, set and cleared
// with `yoyo pause --team`. While it is set no factory in team mode claims new
// work the harness chose for itself; runs already holding their claim carry on,
// because renewing and releasing a claim never read it. It is read from the
// same fetch that comes before every claim, so a factory that cannot reach the
// remote takes no claim and starts nothing on a reading it could not refresh.
//
// An item the operator named on a machine is that operator's exception, as the
// intake hold already allows (TakeNamed).
//
// Each machine's own pause, set by `yoyo pause` alone, is not this: it stops
// that machine's spending and never travels, so one collaborator cannot stop
// another's machine.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// PauseRemote is the shared remote's team pause. It is satisfied by
// *gitworktree.Manager.
type PauseRemote interface {
	FetchClaims(ctx context.Context) error
	FetchedTeamPause(ctx context.Context) (gitworktree.TeamPauseReading, error)
	PushTeamPause(ctx context.Context, record, expected string, now time.Time) (string, error)
	DeleteTeamPause(ctx context.Context, expected string) error
}

// PauseRecord is what a team pause commit says, as JSON in its message.
type PauseRecord struct {
	// SetBy is the person who set it, as their machine names them.
	SetBy string `json:"set_by,omitempty"`
	// Factory is the factory they set it from.
	Factory string    `json:"factory,omitempty"`
	SetAt   time.Time `json:"set_at"`
}

// Pause is the team pause as one reading found it.
type Pause struct {
	// SetBy and Factory name who set it and where, and are empty where the pause
	// does not say, such as one a person pushed by hand.
	SetBy   string    `json:"set_by,omitempty"`
	Factory string    `json:"factory,omitempty"`
	SetAt   time.Time `json:"set_at"`
	// Commit is the pause commit, which clearing it names.
	Commit string `json:"commit"`
}

// Setter says who set the pause, in the words a person reads.
func (p Pause) Setter() string {
	switch {
	case p.SetBy != "" && p.Factory != "":
		return p.SetBy + " (" + p.Factory + ")"
	case p.SetBy != "":
		return p.SetBy
	case p.Factory != "":
		return p.Factory
	default:
		return "somebody the pause does not name"
	}
}

// Says is the team pause in one sentence, for the status line, with the time it
// was set in this machine's local time and zone.
func (p Pause) Says() string {
	return fmt.Sprintf("The team pause is set on the shared remote, as this machine last read it, so no factory working this repository starts new work until it is cleared. %s set it at %s; `yoyo pause --team --clear` clears it.",
		p.Setter(), p.SetAt.Local().Format("2006-01-02 15:04 MST"))
}

// PauseOf reads a pause from a fetched reading, and false where none is set.
func PauseOf(reading gitworktree.TeamPauseReading) (Pause, bool) {
	if !reading.Set {
		return Pause{}, false
	}
	pause := Pause{Commit: reading.Commit, SetAt: reading.CommittedAt}
	var record PauseRecord
	if err := json.Unmarshal([]byte(reading.Record), &record); err == nil {
		pause.SetBy, pause.Factory = strings.TrimSpace(record.SetBy), strings.TrimSpace(record.Factory)
		if !record.SetAt.IsZero() {
			pause.SetAt = record.SetAt
		}
	}
	return pause, true
}

// PausedError is new work not claimed because the team pause is set. It is not
// a failure: nothing was claimed, and the item is tried again once the pause is
// cleared.
type PausedError struct {
	WorkItemID string
	Pause      Pause
}

func (e PausedError) Error() string {
	return fmt.Sprintf("%s was not started because the team pause is set on the shared remote, by %s", e.WorkItemID, e.Pause.Setter())
}

// readPause reads the team pause from the fetch just made. A pause that is set
// and cannot be read is not taken as clear: it stops the claim, as a remote that
// could not be reached does.
func readPause(ctx context.Context, remote Remote, workItemID string) error {
	reading, err := remote.FetchedTeamPause(ctx)
	if err != nil {
		return UnreachableError{WorkItemID: workItemID, Err: fmt.Errorf("the team pause could not be read: %w", err)}
	}
	if pause, set := PauseOf(reading); set {
		return PausedError{WorkItemID: workItemID, Pause: pause}
	}
	return nil
}

// TeamPause sets and clears the team pause for `yoyo pause --team`.
type TeamPause struct {
	Remote PauseRemote
	// SetBy and Factory are written into the pause, so every factory can say
	// who set it.
	SetBy   string
	Factory string
}

// ErrPauseMoved is a set or clear the remote refused because somebody else set
// or cleared the pause between this reading and the push.
var ErrPauseMoved = errors.New("the team pause was changed by somebody else while this was being done; nothing was changed here, so try again")

// Fetched reads the team pause as this machine last fetched it, asking the
// remote nothing. It is what the status line reads.
func (t TeamPause) Fetched(ctx context.Context) (Pause, bool, error) {
	reading, err := t.Remote.FetchedTeamPause(ctx)
	if err != nil {
		return Pause{}, false, err
	}
	pause, set := PauseOf(reading)
	return pause, set, nil
}

// Read fetches and reads the team pause.
func (t TeamPause) Read(ctx context.Context) (Pause, bool, error) {
	if err := t.Remote.FetchClaims(ctx); err != nil {
		return Pause{}, false, fmt.Errorf("the shared remote could not be reached to read the team pause: %w", err)
	}
	reading, err := t.Remote.FetchedTeamPause(ctx)
	if err != nil {
		return Pause{}, false, fmt.Errorf("the team pause could not be read: %w", err)
	}
	pause, set := PauseOf(reading)
	return pause, set, nil
}

// Set sets the team pause, and reports the pause that stands and whether it was
// already set, in which case it is left as it was.
func (t TeamPause) Set(ctx context.Context, now time.Time) (Pause, bool, error) {
	current, set, err := t.Read(ctx)
	if err != nil || set {
		return current, set, err
	}
	record := PauseRecord{SetBy: t.SetBy, Factory: t.Factory, SetAt: now.UTC()}
	encoded, err := json.Marshal(record)
	if err != nil {
		return Pause{}, false, err
	}
	commit, err := t.Remote.PushTeamPause(ctx, string(encoded), "", now)
	switch {
	case errors.Is(err, gitworktree.ErrClaimLeaseRefused):
		return Pause{}, false, ErrPauseMoved
	case err != nil:
		return Pause{}, false, fmt.Errorf("the team pause could not be set on the shared remote: %w", err)
	}
	t.refresh(ctx)
	return Pause{SetBy: record.SetBy, Factory: record.Factory, SetAt: record.SetAt, Commit: commit}, false, nil
}

// refresh fetches again after a set or clear, so this machine's own copy, which
// the status line reads, says what was just done. A fetch that fails changes
// nothing that was done, and the next claim's fetch brings the copy up to date.
func (t TeamPause) refresh(ctx context.Context) {
	_ = t.Remote.FetchClaims(ctx)
}

// Clear clears the team pause, and reports the pause it cleared and whether one
// was set.
func (t TeamPause) Clear(ctx context.Context) (Pause, bool, error) {
	current, set, err := t.Read(ctx)
	if err != nil || !set {
		return current, set, err
	}
	err = t.Remote.DeleteTeamPause(ctx, current.Commit)
	switch {
	case errors.Is(err, gitworktree.ErrClaimLeaseRefused):
		return current, true, ErrPauseMoved
	case err != nil:
		return current, true, fmt.Errorf("the team pause could not be cleared on the shared remote: %w", err)
	}
	t.refresh(ctx)
	return current, true, nil
}
