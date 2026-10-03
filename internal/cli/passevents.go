package cli

// The event streams a program manager instance's pass is woken by, read the way
// the harness already reads them.
//
// Two streams, one position each. The run records say when a run landed its
// change and when one failed, timed out, was cancelled, or stopped for a
// decision, read by when each run ended as the read model reads it for
// throughput. The tracker says when work was admitted, read from the export the
// tracker writes of every item it holds, by the moment each was created.
//
// The tracker's interactions log — the export the freshness measurement reads —
// is not where admissions are read from, although the design names it: the
// tracker writes an entry there for a status, priority, or assignee that moved,
// and none for an item created, so an admission read from it would be a
// trigger that never fires. The item export records every item with the moment
// it was created, which is what an admission is.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// issuesExport is the tracker's export of every item it holds, one per line.
const issuesExport = beads.ExportPath

// maxIssuesExportBytes bounds how much of the export a pass reads. It is far
// above this project's own export, which is under ten megabytes after a
// thousand items, and a read that reaches it fails rather than handing a pass
// the older half of the backlog as though it were the whole.
const maxIssuesExportBytes = 256 << 20

// programManagerPasses is the program manager instances a pull considers for a
// pass.
func programManagerPasses(parts components) map[string]config.AgentConfig {
	passes := parts.config.ProgramManagerPasses()
	if len(passes) == 0 {
		return nil
	}
	return passes
}

// passEvents reads the streams an instance's triggers watch.
type passEvents struct {
	runs       *runstate.Store
	repository string
	refresh    func(context.Context) error
}

// Events reads one stream's events after one moment and at or before another.
func (p passEvents) Events(ctx context.Context, stream string, after, until time.Time) ([]orchestrator.PassEvent, error) {
	switch stream {
	case runstate.PassStreamRuns:
		return runEvents(p.runs, after, until)
	case runstate.PassStreamTracker:
		if p.refresh != nil {
			if err := p.refresh(ctx); err != nil {
				return nil, fmt.Errorf("refresh the tracker admission snapshot: %w", err)
			}
		}
		return admissionEvents(p.repository, after, until)
	default:
		return nil, fmt.Errorf("stream %q is not one a pass reads", stream)
	}
}

// runEvents is every run that ended inside the window having landed its change
// or stopped. A record that cannot be read is set aside rather than failing the
// stream, as every reader of the run records sets one aside.
func runEvents(store *runstate.Store, after, until time.Time) ([]orchestrator.PassEvent, error) {
	if store == nil {
		return nil, errors.New("no run store is wired")
	}
	recorded, _, err := store.RecordedReadable()
	if err != nil {
		return nil, fmt.Errorf("read the run records: %w", err)
	}
	var events []orchestrator.PassEvent
	for _, state := range recorded {
		if !state.Status.Terminal() {
			continue
		}
		ended := readmodel.EndedAt(state)
		if !ended.After(after) || ended.After(until) {
			continue
		}
		event := orchestrator.PassEvent{Stream: runstate.PassStreamRuns, At: ended, Subject: state.WorkItemID}
		switch outcome := state.Outcome(); {
		case state.Escalated():
			event.Class = config.TriggerStoppages
			event.Detail = runDetail(state, "stopped: "+strings.TrimSpace(state.EscalationReason()))
		case outcome == runstate.OutcomeSucceeded && state.Integration != nil:
			event.Class = config.TriggerLandings
			event.Detail = runDetail(state, "landed")
		case outcome == runstate.OutcomeStopped || outcome == runstate.OutcomeFailed ||
			outcome == runstate.OutcomeTimedOut || outcome == runstate.OutcomeCancelled:
			event.Class = config.TriggerStoppages
			event.Detail = runDetail(state, "stopped: "+state.Reason())
		default:
			continue
		}
		event.Key = state.RunID + "/" + string(event.Class)
		events = append(events, event)
	}
	return events, nil
}

func runDetail(state runstate.State, what string) string {
	detail := fmt.Sprintf("run %s %s", state.RunID, what)
	if title := strings.TrimSpace(state.WorkItemTitle); title != "" {
		detail = title + " (" + detail + ")"
	}
	return detail
}

// admissionEvents is every item the tracker's export records as created inside
// the window. A repository with no tracker has admitted nothing; one with a
// tracker and no export is a stream that could not be read, rather than one
// that is quiet.
func admissionEvents(repository string, after, until time.Time) ([]orchestrator.PassEvent, error) {
	file, err := os.Open(filepath.Join(repository, issuesExport))
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Stat(filepath.Join(repository, filepath.Dir(issuesExport))); statErr == nil {
			return nil, errors.New("the tracker has not exported its items")
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the tracker's item export: %w", err)
	}
	defer file.Close()

	counted := &countingReader{reader: io.LimitReader(file, maxIssuesExportBytes+1)}
	scanner := bufio.NewScanner(counted)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	var events []orchestrator.PassEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item struct {
			Type      string    `json:"_type"`
			ID        string    `json:"id"`
			Title     string    `json:"title"`
			CreatedAt time.Time `json:"created_at"`
		}
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			// One unreadable entry is not an export that could not be read.
			continue
		}
		if (item.Type != "" && item.Type != "issue") || strings.TrimSpace(item.ID) == "" {
			continue
		}
		if !item.CreatedAt.After(after) || item.CreatedAt.After(until) {
			continue
		}
		events = append(events, orchestrator.PassEvent{
			Stream:  runstate.PassStreamTracker,
			Class:   config.TriggerAdmissions,
			At:      item.CreatedAt,
			Key:     item.ID,
			Subject: item.ID,
			Detail:  item.Title,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the tracker's item export: %w", err)
	}
	if counted.read > maxIssuesExportBytes {
		return nil, fmt.Errorf("the tracker's item export is larger than the %d bytes a pass reads", maxIssuesExportBytes)
	}
	return events, nil
}

// instanceConversations asks the read model whether a turn is in flight on an
// instance's conversation, as `yoyo agent list` and the standing status ask it.
type instanceConversations struct {
	store *runstate.ConversationStore
}

func (c instanceConversations) InFlight(agent string) (bool, error) {
	inFlight, problem := readmodel.InFlight(c.store, runstate.ConversationIdentity{Agent: agent, Role: domain.RoleProgramManager})
	if problem != "" {
		return false, errors.New(problem)
	}
	return inFlight, nil
}
