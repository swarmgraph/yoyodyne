package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/epicwake"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// epicWakes starts the three roles' passes from the events of the named epics
// while epic mode is on (docs/designs/epic-mode-second-part.md, "When each role
// runs: piece 13"): which are owed is read from the standing status shows
// (readmodel.EpicWakeEvents), and internal/epicwake paces them. While epic mode
// is off nothing is read or written.
type epicWakes struct {
	parts      components
	configPath string
}

func (e epicWakes) Owed(ctx context.Context) (orchestrator.EpicWakeDue, error) {
	product := e.parts.config.Product.ID
	record, err := epicscope.Read(e.parts.stateRoot, product)
	if err != nil || !record.On() {
		return orchestrator.EpicWakeDue{}, err
	}
	on := orchestrator.EpicWakeDue{On: true}
	view, err := epicprogress.Read(e.parts.stateRoot, product, record)
	if err != nil {
		return on, err
	}
	if e.parts.reports == nil {
		return on, errors.New("the reports are not wired, so what waits on each role cannot be read")
	}
	reports, err := e.parts.reports.List()
	if err != nil {
		return on, fmt.Errorf("read the reports: %w", err)
	}
	handlings, err := e.parts.reports.Handlings()
	if err != nil {
		return on, fmt.Errorf("read what became of the reports: %w", err)
	}
	standing := readmodel.ReadStanding(ctx, standingSources(e.configPath))
	// Where an epic stands not being read is not its steps being settled: the
	// events are kept as they are until a reading can say.
	if standing.Epics == nil {
		return on, errors.New("where the named epics stand could not be read")
	}
	for _, open := range standing.Epics.Open {
		if open.State == "" {
			return on, fmt.Errorf("where epic %s stands could not be worked out: %s", open.WorkItemID, open.Problem)
		}
	}
	store, err := epicwake.Open(e.parts.stateRoot, product, time.Now)
	if err != nil {
		return on, err
	}
	on.Due, err = store.Owed(readmodel.EpicWakeEvents(standing.Epics, record, view, reports, handlings))
	return on, err
}

func (e epicWakes) Woken(role domain.AgentRole, pass string, events []epicwake.Kept) error {
	store, err := epicwake.Open(e.parts.stateRoot, e.parts.config.Product.ID, time.Now)
	if err != nil {
		return err
	}
	return store.Woken(role, pass, events)
}

func (e epicWakes) Ended(pass string, fired orchestrator.Fired) error {
	store, err := epicwake.Open(e.parts.stateRoot, e.parts.config.Product.ID, time.Now)
	if err != nil {
		return err
	}
	return store.Ended(pass, fired.CostUSD, orchestrator.EpicWakeKnown(fired))
}
