package beads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// The harness's own bd calls take turns at the store, in the order they asked.
//
// bd lets one process into its embedded store at a time, and a bd that finds
// the store taken does not wait in line: it sleeps and tries again, sleeping
// longer each time. When the holder finishes, whichever bd happens to try next
// gets in, usually one that has only just arrived, while the one that has
// waited longest is the one sleeping longest. So under steady use the oldest
// caller keeps losing, however quick each call is, and runs to its limit. On a
// copy of the live store, where every call took under two seconds alone, six
// callers making single-item reads together waited up to 48 seconds, and three
// reads started behind one listing got in 0.5, 1.1 and 3.1 seconds after it
// ended (docs/diagnoses/yoyodyne-ifd-428-126-4-24-slow-listings.md). On the
// live factory, with two to four calls waiting at any moment, that was the
// two-minute timeouts.
//
// So every bd call the harness makes on the live store waits here for the calls
// that asked before it, and starts bd only when none of them is still waiting
// or running. Then at most one of the harness's bd processes is ever waiting on
// bd's own lock, and only against processes the harness does not start, such
// as a person's own bd; the harness's calls are served first come, first
// served.
//
// The line is a directory of tickets inside the store's own directory, beside
// the write locks, so every harness process reaching one store shares it. A
// ticket is a file named for when it was taken, held under an advisory lock for
// as long as its call waits and runs, and removed when the call ends. The
// operating system drops the lock when its holder dies, so a ticket left behind
// by a process that died is one nobody holds, and the next caller to find it
// removes it rather than waiting on it.
//
// The rebuild of the store, which holds the store lock exclusive, makes its
// calls without a ticket (storelock.go): no other harness call can be in the
// line while it holds that lock. A client running bd in server mode makes its
// calls without one too, because a server does not admit one process at a time.

// turnDirectory is where the tickets are, relative to the store.
const turnDirectory = writeLockDirectory + "/turns"

// turnSuffix ends every ticket's name, so nothing else in the directory is read
// as one.
const turnSuffix = ".turn"

// turnPoll is how often a waiting call looks whether its turn has come.
const turnPoll = 10 * time.Millisecond

// turnTakingGrace is how long a ticket may stand unheld before it is taken to
// be one its process left behind: the moment between a ticket being made and
// its lock being taken is far shorter.
const turnTakingGrace = 2 * time.Second

// turnSequence tells apart two tickets one process takes in the same
// nanosecond.
var turnSequence atomic.Uint64

// turnBusy is a call that waited out its bound behind the harness's other
// calls, and so was never made. Like a call bd's own bound stopped, it reads as
// ErrUnanswered.
type turnBusy struct{ error }

func (b turnBusy) Unwrap() error        { return b.error }
func (b turnBusy) Is(target error) bool { return target == ErrUnanswered }

// takeTurn waits until no bd call the harness asked to make on this client's
// store before this one is still waiting or running, for at most one call's
// bound, and returns what ends the turn. Where no store is found, or the call
// is the rebuild's or runs against a server, it returns at once with nothing to
// end.
func (c Client) takeTurn(ctx context.Context) (func(), error) {
	if c.Dir == "" || c.storeLocked || c.serverMode || c.UnorderedCalls || !turnsSupported {
		return func() {}, nil
	}
	store := storeDirectory(c.Dir, c.beadsDirVariable())
	if store == "" {
		return func() {}, nil
	}
	root, err := repowrite.NewRoot(store)
	if err != nil {
		return nil, fmt.Errorf("take a turn at the tracker's store: %w", err)
	}
	name := turnName(time.Now(), os.Getpid(), turnSequence.Add(1))
	ticket, err := root.OpenAppend(turnDirectory+"/"+name, 0o600, 0o700)
	if err != nil {
		return nil, fmt.Errorf("take a turn at the tracker's store: %w", err)
	}
	leave := func() {
		_, _ = root.RemoveFile(turnDirectory + "/" + name)
		_ = unlockWriteFile(ticket)
		_ = ticket.Close()
	}
	waiting, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	// A process looking whether this ticket is held takes it shared for a
	// moment, so taking it here may have to wait that moment out.
	if err := lockWriteFile(waiting, ticket); err != nil {
		_, _ = root.RemoveFile(turnDirectory + "/" + name)
		_ = ticket.Close()
		return nil, fmt.Errorf("take a turn at the tracker's store: %w", err)
	}
	directory := filepath.Join(store, turnDirectory)
	for {
		ahead := turnAhead(root, directory, name, time.Now())
		if ahead == "" {
			return leave, nil
		}
		select {
		case <-waiting.Done():
			leave()
			if errors.Is(waiting.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				held := c.whoHeld("the turn ahead of it", filepath.Join(directory, ahead))
				return nil, turnBusy{fmt.Errorf("the harness's tracker calls ahead of this one did not finish within %s, so this call was not made (%s): %w", c.timeout(), held, waiting.Err())}
			}
			return nil, waiting.Err()
		case <-time.After(turnPoll):
		}
	}
}

// turnName is the name of a ticket taken at taken by process pid: the time
// first, padded so that names sort in the order the tickets were taken.
func turnName(taken time.Time, pid int, sequence uint64) string {
	return fmt.Sprintf("%020d-%010d-%020d%s", taken.UnixNano(), pid, sequence, turnSuffix)
}

// turnTaken is when the ticket named name was taken, and false for a name that
// is not a ticket's.
func turnTaken(name string) (time.Time, bool) {
	stamp, _, found := strings.Cut(strings.TrimSuffix(name, turnSuffix), "-")
	if !found || !strings.HasSuffix(name, turnSuffix) {
		return time.Time{}, false
	}
	nanos, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}

// turnAhead is the first ticket taken before mine that a live call still
// holds, and empty where there is none. A ticket nobody holds that is older
// than turnTakingGrace is one whose process died; it is removed on the way
// past. A ticket that cannot be looked at is taken to be held, so a call never
// jumps the line on a doubt about one ticket; a directory that cannot be read at
// all, which holds this call's own ticket, lets the call go, as every call went
// before there was a line.
func turnAhead(root repowrite.Root, directory, mine string, now time.Time) string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		name := entry.Name()
		if name >= mine {
			break
		}
		taken, ok := turnTaken(name)
		if !ok {
			continue
		}
		held, err := turnHeld(filepath.Join(directory, name))
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil || held:
			return name
		case now.Sub(taken) < turnTakingGrace:
			return name
		}
		_, _ = root.RemoveFile(turnDirectory + "/" + name)
	}
	return ""
}
