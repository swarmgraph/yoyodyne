package runstate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The recorded slow tracker calls read together, so nobody has to open the
// files one by one: which callers ran out of time most often, how long their
// calls ran, and what held the store while they waited.

// maxSlowCallRowsSaid is how many callers, and how many holders, a summary
// names.
const maxSlowCallRowsSaid = 10

// SlowCaller is one caller's calls that ran out of time.
type SlowCaller struct {
	Caller string
	Calls  int
	// Shortest and Longest are the least and most time one of its calls ran,
	// and Took how long they ran in all.
	Shortest, Longest, Took time.Duration
}

// SlowHolder is one kind of process found holding the store when calls ran out
// of time, and how many of those calls found it.
type SlowHolder struct {
	Process string
	Calls   int
}

// SlowCallSummary is every recorded slow call read together.
type SlowCallSummary struct {
	Calls int
	// First and Last are when the earliest and the latest started.
	First, Last time.Time
	// Callers is each caller, most calls first, and Holders each kind of
	// process that held the store, most calls first.
	Callers []SlowCaller
	Holders []SlowHolder
	// Held is how many calls found another process with the store open, Alone
	// how many found none, Unknown how many could not tell, and ByCaller how
	// many were stopped by their caller's own shorter deadline.
	Held, Alone, Unknown, ByCaller int
	// LargestStore is the largest store any of them saw on disk.
	LargestStore int64
}

// SummarizeSlowCalls reads every recorded slow call together.
func SummarizeSlowCalls(calls []TrackerSlowCall) SlowCallSummary {
	summary := SlowCallSummary{Calls: len(calls)}
	callers := map[string]*SlowCaller{}
	holders := map[string]int{}
	for _, call := range calls {
		if summary.First.IsZero() || call.StartedAt.Before(summary.First) {
			summary.First = call.StartedAt
		}
		if call.StartedAt.After(summary.Last) {
			summary.Last = call.StartedAt
		}
		name := CallerGroup(call.For, "")
		if call.For == "" {
			name = "a call that named no caller (bd " + call.Verb + ")"
		}
		caller := callers[name]
		if caller == nil {
			caller = &SlowCaller{Caller: name, Shortest: call.Ran}
			callers[name] = caller
		}
		caller.Calls++
		caller.Took += call.Ran
		caller.Shortest, caller.Longest = min(caller.Shortest, call.Ran), max(caller.Longest, call.Ran)
		switch {
		case call.CallerDeadline > 0:
			summary.ByCaller++
		case call.HoldersUnread != "":
			summary.Unknown++
		case call.Held():
			summary.Held++
		default:
			summary.Alone++
		}
		// Each kind of process once per call, so a call held by three bd
		// listings counts one call against bd list.
		seen := map[string]bool{}
		for _, holder := range call.Holders {
			process := holderProcess(holder.Command)
			if !seen[process] {
				seen[process] = true
				holders[process]++
			}
		}
		summary.LargestStore = max(summary.LargestStore, call.StoreBytes)
	}
	for _, caller := range callers {
		summary.Callers = append(summary.Callers, *caller)
	}
	sort.Slice(summary.Callers, func(i, j int) bool {
		if summary.Callers[i].Calls != summary.Callers[j].Calls {
			return summary.Callers[i].Calls > summary.Callers[j].Calls
		}
		return summary.Callers[i].Caller < summary.Callers[j].Caller
	})
	for process, count := range holders {
		summary.Holders = append(summary.Holders, SlowHolder{Process: process, Calls: count})
	}
	sort.Slice(summary.Holders, func(i, j int) bool {
		if summary.Holders[i].Calls != summary.Holders[j].Calls {
			return summary.Holders[i].Calls > summary.Holders[j].Calls
		}
		return summary.Holders[i].Process < summary.Holders[j].Process
	})
	return summary
}

// holderProcess is what kind of process a holder was: its program and, where
// it was given one, the command it was asked to run, such as "bd list" or
// "yoyo watch", without the arguments that make every call different.
func holderProcess(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "a process whose command could not be read"
	}
	process := filepath.Base(fields[0])
	if len(fields) > 1 && !strings.HasPrefix(fields[1], "-") {
		process += " " + fields[1]
	}
	return process
}

// Says is the summary as lines a person reads: how many calls ran out of time
// and when, which callers they were for and how long they ran, and what held
// the store.
func (s SlowCallSummary) Says() []string {
	if s.Calls == 0 {
		return []string{"No tracker call has been recorded as running out of time."}
	}
	lines := []string{fmt.Sprintf("%d tracker calls ran out of time between %s and %s; the largest store any of them saw was %s on disk.",
		s.Calls, s.First.Local().Format("2006-01-02 15:04 MST"), s.Last.Local().Format("2006-01-02 15:04 MST"), SayBytes(s.LargestStore))}
	lines = append(lines, fmt.Sprintf("%d of them found another process holding the store, so they were most likely waiting behind it; %d found none, so the tracker itself was too slow; %d were stopped by their caller's own shorter deadline; for %d, what held the store could not be told.",
		s.Held, s.Alone, s.ByCaller, s.Unknown))
	lines = append(lines, "Who they were for, most often first:")
	for index, caller := range s.Callers {
		if index == maxSlowCallRowsSaid {
			lines = append(lines, fmt.Sprintf("  and %d other callers", len(s.Callers)-maxSlowCallRowsSaid))
			break
		}
		ran := "it ran " + sayTook(caller.Longest)
		if caller.Calls > 1 {
			ran = fmt.Sprintf("they ran from %s to %s, %s on average", sayTook(caller.Shortest), sayTook(caller.Longest), sayTook(caller.Took/time.Duration(caller.Calls)))
		}
		lines = append(lines, fmt.Sprintf("  %s: %d %s; %s", caller.Caller, caller.Calls, callsWord(caller.Calls), ran))
	}
	if len(s.Holders) == 0 {
		return append(lines, "No call found another process holding the store.")
	}
	lines = append(lines, "What held the store while they waited, most often first:")
	for index, holder := range s.Holders {
		if index == maxSlowCallRowsSaid {
			lines = append(lines, fmt.Sprintf("  and %d other kinds of process", len(s.Holders)-maxSlowCallRowsSaid))
			break
		}
		lines = append(lines, fmt.Sprintf("  %s, while %d %s waited", holder.Process, holder.Calls, callsWord(holder.Calls)))
	}
	return lines
}

func callsWord(count int) string {
	if count == 1 {
		return "call"
	}
	return "calls"
}
