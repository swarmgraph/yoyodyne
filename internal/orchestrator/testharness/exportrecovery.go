package testharness

import (
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// LastSaved remembers the last state a run saved, which is what a process that
// died at that moment leaves on disk.
type LastSaved struct {
	orchestrator.StateStore
	Last runstate.State
}

func (s *LastSaved) Save(state runstate.State) error {
	if err := s.StateStore.Save(state); err != nil {
		return err
	}
	s.Last = state
	return nil
}
