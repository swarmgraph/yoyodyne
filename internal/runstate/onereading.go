package runstate

import "sync"

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

type oneReading struct {
	recorded    rememberedListing
	incomplete  rememberedListing
	outstanding rememberedListing
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
