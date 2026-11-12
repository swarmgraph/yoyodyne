package runstate

import (
	"sort"
	"sync"
)

// One reading's listings, made once.
//
// A status reading asks the run store the same few questions from many places:
// the running line, the slots, the holds, the reconcile findings, the unrun
// checks, and the stall each list the runs in flight or every run kept. Each
// asking read the records again, so one reading of the dashboard read every run
// record once per section. A store opened for one reading (OneReading) answers
// each listing once and gives every later asker the same answer, so a reading
// builds one listing of each kind however many sections it has, and every
// section is reading the same moment.

// OneReading is a store over the same records that remembers each listing it
// makes for as long as it is used. It is for one reading of the status and
// nothing longer: a store kept past its reading would answer from a moment that
// has gone. A write through it forgets what it remembered.
func (s *Store) OneReading() *Store {
	reading := *s
	reading.reading = &oneReading{}
	return &reading
}

// OneReadingPast is OneReading for a surface that shows runs and acts on none
// of them — the standing the dashboard and `yoyo status` read. Every listing it
// makes reads past a record that will not read rather than refusing the whole
// listing, as IncompleteReadable does, and remembers each record it read past,
// which ReadPast returns. One record nobody can read then costs that record,
// said by name, rather than every section of the reading.
//
// Nothing that acts on runs opens one: a record read past may be a run still
// going, and only a listing that refuses over it never treats its absence as
// the run having finished.
func (s *Store) OneReadingPast() *Store {
	reading := s.OneReading()
	reading.reading.readPast = true
	return reading
}

// ReadPast is every record a reading opened by OneReadingPast has read past so
// far, in run order, and nothing on any other store.
func (s *Store) ReadPast() []Unreadable {
	if s.reading == nil {
		return nil
	}
	s.reading.pastLock.Lock()
	defer s.reading.pastLock.Unlock()
	ids := make([]string, 0, len(s.reading.past))
	for runID := range s.reading.past {
		ids = append(ids, runID)
	}
	sort.Strings(ids)
	past := make([]Unreadable, 0, len(ids))
	for _, runID := range ids {
		past = append(past, s.reading.past[runID])
	}
	return past
}

// readingPast reports a store opened by OneReadingPast.
func (s *Store) readingPast() bool { return s.reading != nil && s.reading.readPast }

// notePast remembers the records a listing of a reading read past.
func (s *Store) notePast(unreadable []Unreadable) {
	if s.reading == nil || len(unreadable) == 0 {
		return
	}
	s.reading.pastLock.Lock()
	defer s.reading.pastLock.Unlock()
	if s.reading.past == nil {
		s.reading.past = map[string]Unreadable{}
	}
	for _, record := range unreadable {
		s.reading.past[record.Record] = record
	}
}

type oneReading struct {
	readPast bool
	pastLock sync.Mutex
	past     map[string]Unreadable

	recorded           rememberedListing
	incomplete         rememberedListing
	outstanding        rememberedListing
	incompleteReadable rememberedReadable
}

// rememberedReadable is a listing that reads past what it cannot read, made
// once, with the records it read past beside it.
type rememberedReadable struct {
	sync.Mutex
	done       bool
	states     []State
	unreadable []Unreadable
	err        error
}

func (l *rememberedReadable) once(ask func() ([]State, []Unreadable, error)) ([]State, []Unreadable, error) {
	l.Lock()
	defer l.Unlock()
	if !l.done {
		l.states, l.unreadable, l.err = ask()
		l.done = true
	}
	if l.err != nil {
		return nil, nil, l.err
	}
	return append([]State(nil), l.states...), append([]Unreadable(nil), l.unreadable...), nil
}

func (l *rememberedReadable) forget() {
	l.Lock()
	defer l.Unlock()
	l.done, l.states, l.unreadable, l.err = false, nil, nil, nil
}

// readingOf is the reading's memory, or a fresh one that remembers nothing
// beyond the call on a store no reading opened.
func (s *Store) readingOf() *oneReading {
	if s.reading == nil {
		return &oneReading{}
	}
	return s.reading
}

// forgetReading drops what the reading remembered, because the store is about
// to change a record it may have listed.
func (s *Store) forgetReading() {
	if s.reading == nil {
		return
	}
	s.reading.recorded.forget()
	s.reading.incomplete.forget()
	s.reading.outstanding.forget()
	s.reading.incompleteReadable.forget()
}

// remembered answers a listing that is a filter of every record: from the full
// listing where the reading already made one, from its own memory where it
// made this one, and otherwise by asking.
func (s *Store) remembered(listing *rememberedListing, ask func() ([]State, error), keep func(State) bool) ([]State, error) {
	if recorded, ok := s.readingOf().recorded.held(); ok {
		return filterStates(recorded, keep), nil
	}
	return listing.once(ask)
}

type rememberedListing struct {
	sync.Mutex
	done   bool
	states []State
	err    error
}

// once makes the listing the first time and answers every later call with a
// copy of it, so a caller sorting its answer sorts nobody else's.
func (l *rememberedListing) once(ask func() ([]State, error)) ([]State, error) {
	l.Lock()
	defer l.Unlock()
	if !l.done {
		l.states, l.err = ask()
		l.done = true
	}
	if l.err != nil {
		return nil, l.err
	}
	return append([]State(nil), l.states...), nil
}

// held is the listing where it has been made and read whole.
func (l *rememberedListing) held() ([]State, bool) {
	l.Lock()
	defer l.Unlock()
	if !l.done || l.err != nil {
		return nil, false
	}
	return l.states, true
}

func (l *rememberedListing) forget() {
	l.Lock()
	defer l.Unlock()
	l.done, l.states, l.err = false, nil, nil
}

func filterStates(states []State, keep func(State) bool) []State {
	kept := make([]State, 0)
	for _, state := range states {
		if keep(state) {
			kept = append(kept, state)
		}
	}
	return kept
}
