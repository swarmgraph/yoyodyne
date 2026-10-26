package beads

import (
	"errors"
	"fmt"
	"time"
)

// What a tracker call stopped at its limit found, kept as well as said.
//
// A call stopped at its limit says what it was asked, for whom, how long it
// ran, and which processes had the store open (timeoutAccount, whoHeld). Said
// is not enough to find out why the tracker is slow: an export that timed out
// with nothing else holding the store is the tracker itself being slow, one
// that timed out behind three other calls is a queue, and telling the two
// apart over a week means reading every place a call failed. So the account is
// also a value, SlowCall, with the store's size on disk beside the holders; it
// is handed to the client's recorder where one is set, and a caller that has to
// say why its own step failed reads it back out of the error (SlowCallOf).

// SlowCall is one bd invocation that ran until a limit stopped it.
type SlowCall struct {
	// Call is what it was asked, in words, and Verb the bd verb.
	Call string `json:"call"`
	Verb string `json:"verb"`
	// For is who it was made for, where its context named somebody.
	For string `json:"for,omitempty"`
	// StartedAt is when bd was started, and Ran how long it ran before it was
	// stopped.
	StartedAt time.Time     `json:"started_at"`
	Ran       time.Duration `json:"ran"`
	// Limit is the limit the client gives the call. CallerDeadline is the
	// caller's own shorter deadline where that is what stopped it, and zero
	// where the client's limit did.
	Limit          time.Duration `json:"limit"`
	CallerDeadline time.Duration `json:"caller_deadline,omitempty"`
	// Store is the store's database directory, and StoreBytes its size on disk
	// when the call was stopped; StoreUnmeasured is why the size is not known,
	// where it is not.
	Store           string `json:"store,omitempty"`
	StoreBytes      int64  `json:"store_bytes,omitempty"`
	StoreUnmeasured string `json:"store_unmeasured,omitempty"`
	// Holders is the processes that had the store open when the call was
	// stopped; HoldersUnread is why they could not be told, where they could
	// not.
	Holders       []StoreHolder `json:"holders,omitempty"`
	HoldersUnread string        `json:"holders_unread,omitempty"`
}

// SlowCallRecorder is where a client keeps each call stopped at a limit. It is
// satisfied by *runstate.TrackerSlowCallStore.
type SlowCallRecorder interface {
	RecordSlowCall(call SlowCall) error
}

// Held reports a call that found other processes with the store open.
func (s SlowCall) Held() bool { return len(s.Holders) > 0 }

// Says is what the call found, as a clause to follow its account: the store's
// size on disk, and what held it.
func (s SlowCall) Says() string {
	size := "the size of the tracker's store could not be measured: " + s.StoreUnmeasured
	if s.StoreUnmeasured == "" {
		size = "the tracker's store was " + SayBytes(s.StoreBytes) + " on disk"
	}
	held := ""
	switch {
	case s.HoldersUnread != "":
		held = "which process held the tracker's store could not be told: " + s.HoldersUnread
	case s.Held():
		held = "when it was stopped, the tracker's store was held open by " + SayHolders(s.Holders)
	default:
		held = "no process had the tracker's store open when it was stopped"
	}
	return held + "; " + size
}

// Cause is the plain finding the account comes to: whether the call waited
// behind other processes or the tracker was slow on its own, and the store's
// size, which is what makes it slow on its own.
func (s SlowCall) Cause() string {
	ran := fmt.Sprintf("%s ran %s", s.Call, roundedSpan(s.Ran))
	if s.CallerDeadline > 0 {
		return fmt.Sprintf("%s and was stopped by its caller's own deadline of %s, shorter than the %s the tracker is given",
			ran, roundedSpan(s.CallerDeadline), s.Limit)
	}
	size := "a store whose size could not be measured"
	if s.StoreUnmeasured == "" {
		size = "a store of " + SayBytes(s.StoreBytes) + " on disk"
	}
	switch {
	case s.HoldersUnread != "":
		return fmt.Sprintf("%s without finishing, on %s; whether another process held the store could not be told", ran, size)
	case s.Held():
		return fmt.Sprintf("%s without finishing, on %s, while %d other process(es) had the store open, so it was most likely waiting behind them",
			ran, size, len(s.Holders))
	default:
		return fmt.Sprintf("%s without finishing, on %s, while no other process had the store open, so the tracker itself was too slow", ran, size)
	}
}

// SlowCallOf is the slow call err reports, where it reports one: the last bd
// invocation stopped at a limit that err wraps.
func SlowCallOf(err error) (SlowCall, bool) {
	var failure processFailure
	if !errors.As(err, &failure) || failure.slow == nil {
		return SlowCall{}, false
	}
	return *failure.slow, true
}

// slowCall measures what a call stopped at its limit found: the store's size,
// and which processes had it open. It never fails, since a measurement that
// cannot be taken is said as such.
func (c Client) slowCall(args []string, who string, started time.Time, ran, limit time.Duration, deadline time.Time, deadlined bool) SlowCall {
	call := SlowCall{Call: describeCall(args), Verb: args[0], For: who, StartedAt: started, Ran: ran, Limit: limit}
	if allowed := deadline.Sub(started); deadlined && allowed < limit {
		call.CallerDeadline = allowed
	}
	store, size, err := c.StoreSize()
	call.Store, call.StoreBytes = store, size
	if err != nil {
		call.StoreUnmeasured = err.Error()
	}
	if store == "" {
		call.HoldersUnread = "no store was found from " + describeDir(c.Dir)
		return call
	}
	holders, err := c.findHolders(store)
	call.Holders = holders
	if err != nil {
		call.HoldersUnread = err.Error()
	}
	return call
}

// recordSlowCall hands call to the recorder, where one is set, and says what
// went wrong where the record could not be kept, so the account carries it
// rather than the record being lost unsaid.
func (c Client) recordSlowCall(call SlowCall) string {
	if c.SlowCalls == nil {
		return ""
	}
	if err := c.SlowCalls.RecordSlowCall(call); err != nil {
		return "; the record of this slow call could not be kept: " + err.Error()
	}
	return ""
}

// SayBytes is a size as a person reads one: kilobytes under a megabyte, and
// megabytes above.
func SayBytes(size int64) string {
	const kilobyte, megabyte = 1 << 10, 1 << 20
	switch {
	case size < kilobyte:
		return fmt.Sprintf("%d bytes", size)
	case size < megabyte:
		return fmt.Sprintf("%d KB", (size+kilobyte/2)/kilobyte)
	case size < 10*megabyte:
		return fmt.Sprintf("%.1f MB", float64(size)/megabyte)
	default:
		return fmt.Sprintf("%d MB", (size+megabyte/2)/megabyte)
	}
}
