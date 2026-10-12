package console

import (
	"sync"
	"time"
)

// WatchForStop reads the keyboard while a turn is being answered, when nothing
// else is reading it, so the operator can stop the turn without ending the
// conversation: Esc stops it, and so does Ctrl-C, which is not raised as an
// interrupt while this watches. The account of work in progress says so while
// it lasts.
//
// Every other key is left exactly where it was, in the order it was typed, for
// the next prompt to apply: typing ahead of an answer is as it always was. A
// stop key is taken out of what is waiting, so it does nothing more at that
// prompt than it did here.
//
// What closes the input — the operator's end of it going away, or a read that
// failed — is left for the next prompt to find, which ends the conversation
// there exactly as before.
func (t *terminal) WatchForStop() (<-chan struct{}, func()) {
	stop := make(chan struct{})
	quit := make(chan struct{})
	finished := make(chan struct{})
	t.claimInterrupt()
	t.mu.Lock()
	t.watching = true
	t.redraw()
	t.mu.Unlock()
	go t.watchForStop(stop, quit, finished)
	var once sync.Once
	return stop, func() {
		once.Do(func() {
			close(quit)
			<-finished
			t.mu.Lock()
			t.watching = false
			t.redraw()
			t.mu.Unlock()
			t.releaseInterrupt()
		})
	}
}

// watchForStop is the reading itself, until done is called. It closes stop the
// first time a stop key arrives and keeps reading after that, so a second press
// while the turn is winding down is taken out of what is waiting as the first
// one was.
func (t *terminal) watchForStop(stop chan<- struct{}, quit <-chan struct{}, finished chan<- struct{}) {
	defer close(finished)
	input := t.input
	var escape <-chan time.Time
	stopped := false
	for {
		late := false
		select {
		case <-quit:
			return
		case piece, open := <-input:
			if !open || piece.err != nil {
				// The next prompt reads the same closed input and ends there.
				input = nil
				continue
			}
			t.mu.Lock()
			t.keys = append(t.keys, piece.data...)
			t.mu.Unlock()
		case <-escape:
			// Input that arrived as the wait ran out may be the rest of the
			// sequence the escape began, so it is read before the escape is taken
			// for the key (feedLate).
			late = true
			select {
			case piece, open := <-input:
				if open && piece.err == nil {
					t.mu.Lock()
					t.keys = append(t.keys, piece.data...)
					t.mu.Unlock()
					late = false
				}
			default:
			}
		}
		found, waiting := t.takeStopKeys(late)
		if found && !stopped {
			stopped = true
			close(stop)
		}
		escape = nil
		if waiting {
			escape = t.escapeAfter()
		}
	}
}

// takeStopKeys takes Esc and Ctrl-C out of what the operator has typed ahead and
// reports whether there were any, and whether an escape byte at the end of it is
// still waiting to be known as the escape key or the start of a sequence.
func (t *terminal) takeStopKeys(late bool) (bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	found := false
	for index := 0; index < len(t.keys); {
		pressed, size, complete := decodeKey(t.keys[index:])
		if !complete {
			if !late || !lateEscape(t.keys[index:]) {
				return found, lateEscape(t.keys[index:])
			}
			pressed, size = key{code: keyEscape}, 1
		}
		if pressed.code == keyEscape || pressed.code == keyInterrupt {
			t.keys = append(t.keys[:index], t.keys[index+size:]...)
			found = true
			continue
		}
		index += size
	}
	return found, false
}
