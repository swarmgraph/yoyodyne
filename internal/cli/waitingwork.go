package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/releasecut"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// waitingWork reads, for the Lead Product Manager's pass, the latest release
// tag, the open release items with what each waits on, and the open priority 0
// work, each with when its latest developer run started
// (orchestrator/overdue.go). It changes nothing.
type waitingWork struct {
	items interface {
		ListUnfinished(ctx context.Context) ([]beads.WorkItem, error)
	}
	runs interface {
		Latest(workItemID string) (runstate.State, error)
	}
	runner     execution.ProcessRunner
	repository string
}

const releaseTagTimeout = time.Minute

func (w waitingWork) Waiting(ctx context.Context) (orchestrator.WaitingWork, error) {
	reading := orchestrator.WaitingWork{}
	tag, at, err := w.latestTag(ctx)
	if err != nil {
		return reading, err
	}
	reading.LatestTag, reading.TaggedAt = tag, at
	open, err := w.items.ListUnfinished(ctx)
	if err != nil {
		return reading, fmt.Errorf("list the open work: %w", err)
	}
	unfinished := make(map[string]struct{}, len(open))
	byID := make(map[string]beads.WorkItem, len(open))
	for _, item := range open {
		unfinished[item.ID] = struct{}{}
		byID[item.ID] = item
	}
	release := map[string]bool{}
	for _, item := range releasecut.Decided(open) {
		release[item.ID] = true
	}
	// Every release item, then what each waits on, then every priority 0 item,
	// each once.
	var wanted []string
	seen := map[string]bool{}
	want := func(id string) {
		if !seen[id] {
			seen[id] = true
			wanted = append(wanted, id)
		}
	}
	for _, item := range open {
		if release[item.ID] {
			want(item.ID)
			for _, id := range item.WaitingOn(unfinished) {
				want(id)
			}
		}
	}
	for _, item := range open {
		if item.Priority == 0 {
			want(item.ID)
		}
	}
	for _, id := range wanted {
		item, found := byID[id]
		if !found {
			// A blocker the listing named but does not hold, such as one closed
			// since: the section says it could not be read.
			continue
		}
		waiting := orchestrator.WaitingItem{
			ID: item.ID, Title: item.Title, Priority: item.Priority, Release: release[item.ID],
			Parking: item.Parking.Reason(), WaitsOn: item.WaitingOn(unfinished),
			AdmittedAt: item.CreatedAt, ClosedAt: item.ClosedAt,
		}
		latest, err := w.runs.Latest(item.ID)
		switch {
		case err == nil:
			waiting.LastRun = latest.StartedAt
		case !errors.Is(err, runstate.ErrNoRecordedRun):
			return reading, fmt.Errorf("read the latest run of %s: %w", item.ID, err)
		}
		reading.Items = append(reading.Items, waiting)
	}
	return reading, nil
}

// latestTag is the most recently made release tag and when it was made, and
// empty where the repository has none.
func (w waitingWork) latestTag(ctx context.Context) (string, time.Time, error) {
	result, err := w.runner.Run(ctx, execution.Command{
		Name: "git",
		Args: []string{"for-each-ref", "--sort=-creatordate", "--count=1", "--format=%(refname:short)%09%(creatordate:iso-strict)", "refs/tags/v*"},
		Dir:  w.repository, Env: execution.GitEnvironment(nil), Timeout: releaseTagTimeout,
	}, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read the latest release tag: %w", err)
	}
	if result.Status != execution.ProcessSucceeded {
		return "", time.Time{}, fmt.Errorf("read the latest release tag: git exited %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	line := strings.TrimSpace(result.Stdout)
	if line == "" {
		return "", time.Time{}, nil
	}
	name, stamp, _ := strings.Cut(line, "\t")
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read the latest release tag: %s was made at %q, which is not a time: %w", name, stamp, err)
	}
	return strings.TrimSpace(name), at, nil
}
