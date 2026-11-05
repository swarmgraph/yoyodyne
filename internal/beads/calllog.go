package beads

import (
	"sync/atomic"
	"time"
)

// Every tracker call a yoyo process makes, kept so the harness's own load on
// the store can be counted by who made it.
//
// bd lets one process into the store at a time, reads included, so every call
// waits on every other, and which of the harness's own processes keeps the
// store busiest is answered only by counting all their calls together. A
// CallTally counts one command's calls and forgets them when it ends; this
// keeps them. A process that keeps the count names a log once (LogCallsTo),
// and every client in that process hands it each call once it has ended, with
// who it was for, so no client has to be built with it. A client pinned to a
// measurement's copy of the store logs nothing: those calls are not load on the
// live store.

// LoggedCall is one bd invocation, once it has ended, as the process's call log
// keeps it.
type LoggedCall struct {
	// At is when bd was started.
	At time.Time `json:"at"`
	// For is who it was made for, where its context named somebody
	// (OnBehalfOf), and empty where nothing did.
	For string `json:"for,omitempty"`
	// Call is what it was asked, in words, and Verb the bd verb.
	Call string `json:"call"`
	Verb string `json:"verb"`
	// Took is how long it ran, and StoppedAtLimit says it was stopped at its
	// limit, or at its caller's deadline, without an answer.
	Took           time.Duration `json:"took"`
	StoppedAtLimit bool          `json:"stopped_at_limit,omitempty"`
}

// CallLog keeps every call a process makes. It is satisfied by
// *runstate.TrackerCallLogStore. It returns nothing: a call's answer is its
// caller's, and a log that cannot be written changes nothing about it.
type CallLog interface {
	LogCall(LoggedCall)
}

type callLogHolder struct{ log CallLog }

var processCallLog atomic.Pointer[callLogHolder]

// LogCallsTo has every client in this process hand log each call it makes to
// the live store, from now on. Nil stops it.
func LogCallsTo(log CallLog) {
	if log == nil {
		processCallLog.Store(nil)
		return
	}
	processCallLog.Store(&callLogHolder{log: log})
}

// logCall hands one ended call to the process's log, where one is named and the
// client is not pinned to a measurement's copy.
func (c Client) logCall(call LoggedCall) {
	if c.beadsDir != "" {
		return
	}
	if holder := processCallLog.Load(); holder != nil {
		holder.log.LogCall(call)
	}
}
