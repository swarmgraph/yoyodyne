package runstate

// The provider sessions that wrote a run's change, beyond the one its developer
// is working in now (State.ProviderSessionID). A conflict met by an approved
// change is resolved in a new session, so one change can have several authors, and no
// reviewer of it may be any of them (State.AuthoredBy).

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// AuthorWork is what a session did to the change.
type AuthorWork string

const (
	// AuthorWroteChange is a session that worked on the change itself.
	AuthorWroteChange AuthorWork = "wrote-change"
	// AuthorResolvedConflict is a session that resolved a conflict handed back
	// to the run.
	AuthorResolvedConflict AuthorWork = "resolved-conflict"
)

// AuthorSession is one session that wrote part of the run's change.
type AuthorSession struct {
	SessionID string     `json:"session_id"`
	Work      AuthorWork `json:"work"`
	// Handback is the hand-back whose conflict the session resolved
	// (ConflictHandback), and empty for a session that wrote the change.
	Handback   string    `json:"handback,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Validate reports every contract violation in the record at once.
func (a AuthorSession) Validate() error {
	var problems []error
	if strings.TrimSpace(a.SessionID) == "" {
		problems = append(problems, errors.New("session_id is required"))
	}
	if a.Work != AuthorWroteChange && a.Work != AuthorResolvedConflict {
		problems = append(problems, fmt.Errorf("work %q is not something a session does to a change", a.Work))
	}
	if a.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	return errors.Join(problems...)
}

// RecordWritingSession records a session that worked on the change. One already
// recorded, for whatever it did, changes nothing.
func (s *State) RecordWritingSession(session string, at time.Time) {
	session = strings.TrimSpace(session)
	if session == "" || slices.ContainsFunc(s.AuthorSessions, func(a AuthorSession) bool { return a.SessionID == session }) {
		return
	}
	s.AuthorSessions = append(slices.Clone(s.AuthorSessions), AuthorSession{SessionID: session, Work: AuthorWroteChange, RecordedAt: at.UTC()})
}

// RecordResolvingSession records the session making the attempt at the
// outstanding conflict. It does nothing for a run with no such attempt.
func (s *State) RecordResolvingSession(session string, at time.Time) {
	session = strings.TrimSpace(session)
	if session == "" || !s.ResolvingConflict() || s.resolvedBy(session) {
		return
	}
	s.AuthorSessions = append(slices.Clone(s.AuthorSessions), AuthorSession{
		SessionID: session, Work: AuthorResolvedConflict, Handback: s.ReplayConflict.Handback, RecordedAt: at.UTC(),
	})
}

// ResolvingConflict reports a run whose outstanding developer attempt is its one
// attempt to bring a handed-back change up to date.
func (s State) ResolvingConflict() bool {
	return s.ReplayConflict != nil && s.ReplayConflict.Reconciliation
}

// ResolvingInNewSession reports such an attempt on an approved change, which is
// made in a session of its own. A conflict found before review, while the run
// waited for the forge's checks, is resolved in the session that wrote the
// change.
func (s State) ResolvingInNewSession() bool {
	return s.ResolvingConflict() && !s.ReplayConflict.BeforeReview()
}

// ResolvingSession is the session already making the attempt at the outstanding
// conflict, which an interrupted attempt resumes. It is empty before that
// session has reported itself, whatever session wrote the change.
func (s State) ResolvingSession() string {
	if session := strings.TrimSpace(s.ProviderSessionID); s.ResolvingConflict() && s.resolvedBy(session) {
		return s.ProviderSessionID
	}
	return ""
}

func (s State) resolvedBy(session string) bool {
	return slices.ContainsFunc(s.AuthorSessions, func(a AuthorSession) bool {
		return a.SessionID == session && a.Work == AuthorResolvedConflict && a.Handback == s.ReplayConflict.Handback
	})
}

// AuthoredBy reports a session that wrote any part of the run's change.
func (s State) AuthoredBy(session string) bool {
	session = strings.TrimSpace(session)
	if session == "" {
		return false
	}
	return session == strings.TrimSpace(s.ProviderSessionID) || slices.Contains(s.OtherAuthorSessions(), session)
}

// OtherAuthorSessions are the recorded authors other than the developer's
// current session, each once.
func (s State) OtherAuthorSessions() []string {
	var others []string
	for _, author := range s.AuthorSessions {
		if author.SessionID != strings.TrimSpace(s.ProviderSessionID) && !slices.Contains(others, author.SessionID) {
			others = append(others, author.SessionID)
		}
	}
	return others
}

func (s State) validateAuthorSessions() []error {
	var problems []error
	for index, author := range s.AuthorSessions {
		if err := author.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("author_sessions[%d]: %w", index, err))
		}
	}
	return problems
}
