package orchestrator

// Which backend a run's developer is invoked on, once the run exists.
//
// A run records the backend its developer was reserved on (runstate.State's
// Backend), and every developer invocation the run makes after that goes to the
// same backend: the first attempt, a repair, a resumed attempt, a stall the
// harness carries on, and a fresh session a decided repair starts. The
// developer slot's configuration is read once, when the run is reserved, for
// the same reason the account and the model are (see activeRun.account): a run
// that followed the configuration as it changed would hand one provider's
// session to another, which no provider can resume. That is what happened to
// the Codex runs repaired after the developer moved to Claude Code
// (docs/diagnoses/yoyodyne-hfi-decided-repairs-fail-at-developing.md).
//
// Where this build cannot launch the backend a run recorded, or the project no
// longer describes it, the run's developer cannot be invoked at all. That is
// refused before any provider call and before a decided repair spends its grant
// (RecordedBackendError), with the session id left on the record, so the
// development manager can choose a re-run instead.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// ErrRecordedBackendUnavailable is what a refusal for a run whose recorded
// backend cannot be invoked here unwraps to.
var ErrRecordedBackendUnavailable = errors.New("the backend the run's developer worked on cannot be invoked here")

// RecordedBackendError refuses to invoke a run's developer on a backend other
// than the one the run recorded. No provider was called, and the run's session
// id is still on its record.
type RecordedBackendError struct {
	RunID string
	// Recorded is the backend the run's developer worked on, and Configured the
	// one the developer slot is configured for now.
	Recorded   domain.Backend
	Configured domain.Backend
}

func (e RecordedBackendError) Error() string {
	return fmt.Sprintf(
		"run %s's developer worked on %s, and the developer is now configured for %s; this harness cannot start %s, because the project no longer describes it or this build cannot launch it, and %s cannot carry on a %s session, so the developer was not invoked and no provider was called. The run's session is still on its record. A re-run starts the item again on %s",
		e.RunID, e.Recorded, e.Configured, e.Recorded, e.Configured, e.Recorded, e.Configured)
}

func (e RecordedBackendError) Unwrap() error { return ErrRecordedBackendUnavailable }

// DeveloperBackends is what a continuation needs to know to tell whether a
// stopped run's developer can be invoked again: the backend the developer slot
// is configured for now, and the adapters this harness can build for any other
// backend a run recorded.
//
// The zero value asks nothing, and a continuation wired with it leaves the
// question to the pipeline, which still refuses before any provider call but
// only after a decided repair has spent its grant.
type DeveloperBackends struct {
	Configured domain.Backend
	// Other builds the adapter for a backend other than Configured, and reports
	// false for one the project no longer describes or this build cannot launch.
	Other func(named domain.Backend) (backend.Backend, bool)
	// Adapter names the compiled adapter that launches a backend, which is the
	// source its events carry. Nil reads every backend as its own adapter, which
	// is what every built-in is.
	Adapter func(named domain.Backend) domain.Backend
}

// refuse reports why the developer of a run with this record cannot be invoked
// here, and nil where it can.
func (b DeveloperBackends) refuse(state runstate.State) error {
	recorded := state.Backend
	if b.Configured == "" || recorded == "" || recorded == b.Configured {
		return nil
	}
	if b.Other != nil {
		if _, ok := b.Other(recorded); ok {
			return nil
		}
	}
	return permanentCarryOut(triage.CarryOutBackendUnavailable,
		RecordedBackendError{RunID: state.RunID, Recorded: recorded, Configured: b.Configured})
}

// restoreSession puts back on a run's record the developer session a failed
// attempt erased from it, read from the run's own event log (erasedSession),
// and reports the session restored. A log that cannot be read restores
// nothing: the record is left as it was, and the repair goes ahead as it would
// have.
func (b DeveloperBackends) restoreSession(events RunEvents, state *runstate.State) string {
	if events == nil || strings.TrimSpace(state.ProviderSessionID) != "" || state.Backend == "" {
		return ""
	}
	logged, err := events.LoadEvents(state.RunID)
	if err != nil {
		return ""
	}
	adapter := state.Backend
	if b.Adapter != nil {
		adapter = b.Adapter(state.Backend)
	}
	session := erasedSession(*state, logged, adapter)
	state.ProviderSessionID = session
	return session
}

// developerBackends is this pipeline's answer to the same question.
func (p Pipeline) developerBackends() DeveloperBackends {
	return DeveloperBackends{Configured: p.developer().Backend, Other: p.RecordedBackends}
}

// restoredSessionSays is what a continuation's reason says about a session it
// put back on the run's record.
func restoredSessionSays(session string) string {
	return fmt.Sprintf("The run's record had lost its developer session %s to an earlier attempt that failed before the provider opened one; the session was restored from the run's event log, where it was recorded when it opened.", session)
}

// developerBackendFor is the adapter a run's developer is invoked through, and
// the backend it runs. A run that recorded no backend, or the configured one,
// runs on the pipeline's own adapter.
func (p Pipeline) developerBackendFor(state runstate.State) (backend.Backend, domain.Backend, error) {
	backends := p.developerBackends()
	if state.Backend == "" || state.Backend == backends.Configured {
		return p.Backend, backends.Configured, nil
	}
	if backends.Other != nil {
		if provider, ok := backends.Other(state.Backend); ok && provider != nil {
			return provider, state.Backend, nil
		}
	}
	return nil, state.Backend, stoppedBy(runstate.StopHarness, RecordedBackendError{RunID: state.RunID, Recorded: state.Backend, Configured: backends.Configured})
}

// RunEvents reads a run's event log. It is satisfied by runstate.Store.
type RunEvents interface {
	LoadEvents(runID string) ([]execution.Event, error)
}

// erasedSession is the developer session a run's record lost and its event log
// still names, and empty where the record holds one or the log names none.
//
// Until a failed attempt stopped overwriting it (carrySession), an attempt that
// reported no session erased the one the record held, and a resume that failed
// before the provider opened a session — the wrong provider asked to resume it,
// for one — is such an attempt. The session itself was not lost: the provider
// that opened it reported it as it opened, as a run.started event in the run's
// own log. The latest one the developer opened on the run's recorded backend is
// the session the run was working in.
//
// A log holds the reviewer's sessions too, so each session is attributed to the
// role named on the terminal that ended it, and, for a terminal written before
// terminals named their role, to whether it opened inside a review.
func erasedSession(state runstate.State, events []execution.Event, adapter domain.Backend) string {
	if strings.TrimSpace(state.ProviderSessionID) != "" || state.Backend == "" {
		return ""
	}
	source := string(adapter)
	if source == "" {
		source = string(state.Backend)
	}
	type opened struct {
		session  string
		source   string
		inReview bool
	}
	var pending *opened
	latest := ""
	inReview := false
	settle := func(role string) {
		if pending == nil {
			return
		}
		developer := role == string(domain.RoleDeveloper) || (role == "" && !pending.inReview)
		if developer && pending.source == source {
			latest = pending.session
		}
		pending = nil
	}
	for _, event := range events {
		switch event.Type {
		case execution.EventReviewStarted:
			inReview = true
		case execution.EventReviewCompleted:
			inReview = false
		case execution.EventRunStarted:
			// A session that opened and never reached a terminal is still one the
			// developer opened, read as the review window says.
			settle("")
			var payload struct {
				SessionID string `json:"session_id"`
			}
			if json.Unmarshal(event.Payload, &payload) != nil || strings.TrimSpace(payload.SessionID) == "" {
				continue
			}
			pending = &opened{session: payload.SessionID, source: event.Source, inReview: inReview}
		case execution.EventRunCompleted, execution.EventRunFailed:
			if pending == nil || event.Source != pending.source {
				continue
			}
			var payload struct {
				Role string `json:"role"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			settle(payload.Role)
		}
	}
	settle("")
	return latest
}
