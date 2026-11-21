package cli

import (
	"context"
	"runtime"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/intake"
	"github.com/mason-bryant/yoyodyne/internal/mcpcall"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// intakeQueriesFrom is the project's scheduled intake queries as a watching
// session runs them beside its pulls (internal/intake), or nil for a project
// that declares none: such a project's sessions run, record and show exactly
// what they did before intake existed. The queries' calls go through the one
// MCP call path, built here as runMCPCalls builds a developer's, with the same
// credentials, the same operator's pause and the same call record.
//
// It is returned as an interface, nil as a value, so no caller holds a typed
// nil pointer that compares as set.
func intakeQueriesFrom(parts components) orchestrator.ScheduleIntake {
	cfg := parts.config
	if !cfg.Intake.Declared() || parts.holds == nil {
		return nil
	}
	calls, err := mcpcall.NewRecords(parts.stateRoot, cfg.Product.ID)
	if err != nil {
		return nil
	}
	records, err := intake.NewRecords(parts.stateRoot, cfg.Product.ID)
	if err != nil {
		return nil
	}
	stores := mcpcredential.Stores{StateRoot: parts.stateRoot, Product: cfg.Product.ID}
	if runtime.GOOS == "darwin" {
		stores.Keychain = slack.Keychain{Runner: execution.OSProcessRunner{}}
	}
	path := &mcpcall.Path{
		Config:        cfg,
		Stores:        stores,
		Hold:          parts.holds,
		Records:       calls,
		RedactValues:  parts.redactValues,
		ClientVersion: buildinfo.Commit(),
		Attribution:   passFailureAttribution(cfg),
	}
	runner := &intake.Runner{Config: cfg, Records: records, Calls: path, Hold: parts.holds, Attribution: passFailureAttribution(cfg)}
	// The store is assigned only when there is one, since a nil store in the
	// interface reads as set.
	if parts.reports != nil {
		path.Notes = parts.reports
		runner.Notes = parts.reports
	}
	return intakeQueries{runner: runner}
}

// intakeQueries adapts the intake runner to what the session asks of it: how
// each run went is in the intake record, which `yoyo status` counts, so the
// session is told only what the record could not keep.
type intakeQueries struct{ runner *intake.Runner }

func (q intakeQueries) Due(now time.Time) ([]string, error) { return q.runner.Due(now) }

func (q intakeQueries) Run(ctx context.Context, query string) error {
	_, err := q.runner.Run(ctx, query)
	return err
}
