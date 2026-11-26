package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicjoin"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// epicJoins judges the pieces that may join a named epic (internal/epicjoin) with
// the reviewer agent's own model, account and provider, in a session of its own.
type epicJoins struct {
	parts  components
	runner execution.ProcessRunner
}

func epicJoinsFrom(parts components) orchestrator.ScheduleEpicJoins {
	if parts.branchReviews == nil || agentNameForRole(parts.config, domain.RoleReviewer) == "" {
		return nil
	}
	return epicJoins{parts, parts.runner}
}

// Owed answers no while the operator has paused what the harness spends.
func (e epicJoins) Owed(ctx context.Context) (bool, error) {
	if paused, err := e.paused(); err != nil || paused {
		return false, err
	}
	joiner, err := e.joiner(nil)
	if err != nil {
		return false, err
	}
	return joiner.Owed(ctx)
}

func (e epicJoins) paused() (bool, error) {
	_, held, err := e.parts.holds.Held()
	return held, err
}

func (e epicJoins) Judge(ctx context.Context) (cost float64, err error) {
	joiner, err := e.joiner(func(line runstate.Spend) { cost += line.AmountUSD })
	if err != nil {
		return 0, err
	}
	err = joiner.Round(ctx)
	return cost, err
}

func (e epicJoins) joiner(spent func(runstate.Spend)) (epicjoin.Joiner, error) {
	store, err := e.parts.epicProgress()
	if err != nil {
		return epicjoin.Joiner{}, err
	}
	// The questions about evidence and scope go in the collected reports; without
	// them nothing is asked.
	evidence := epicjoin.Evidence{Build: buildinfo.Commit(), RepositoryID: string(e.parts.config.Product.RepositoryID)}
	if e.parts.reports != nil {
		evidence.Reports = e.parts.reports
	}
	return epicjoin.Joiner{
		Evidence:  evidence,
		StateRoot: e.parts.stateRoot,
		Product:   e.parts.config.Product.ID,
		Progress:  store,
		// A judgment is kept as a review that belongs to no run is: its events under
		// its own identifier, which its cost line names too.
		Log:       e.parts.branchReviews,
		NewID:     runstate.NewBranchReviewID,
		SplitFrom: chat.SplitByDevelopmentManager,
		Note: func(ctx context.Context, item, note string) error {
			_, err := e.parts.tracker().Update(ctx, item, beads.WorkItemChange{AppendNotes: note})
			return err
		},
		Items: func(ctx context.Context) ([]beads.WorkItem, error) {
			if err := e.parts.tracker().RefreshExportIfDue(ctx); err != nil {
				return nil, fmt.Errorf("refresh the tracker's item export: %w", err)
			}
			return beads.ReadExport(e.parts.repository, nil)
		},
		Judge: func(ctx context.Context, request epicjoin.Request) (backend.RunResult, error) {
			return e.judge(ctx, request, spent)
		},
		MayStart: e.parts.wouldStart(),
		Paused:   e.paused,
		Now:      time.Now,
	}, nil
}

// judge makes one judgment, with no session to resume and nothing it may write. It
// asks the budget's permission of the request, as a turn of a conversation does.
func (e epicJoins) judge(ctx context.Context, request epicjoin.Request, spent func(runstate.Spend)) (backend.RunResult, error) {
	if !request.Permission.Granted() {
		return backend.RunResult{}, errors.New("nothing permitted this call")
	}
	parts, cfg := e.parts, e.parts.config
	name := agentNameForRole(cfg, domain.RoleReviewer)
	reviewer := cfg.Agents[name]
	account, err := cfg.AgentAccountEndpoint(parts.stateRoot, name)
	if err != nil {
		return backend.RunResult{}, err
	}
	provider := spend.Metered{
		Provider: providerBackendIn(cfg, parts.stateRoot, reviewer.Backend, e.runner, account.Directory),
		Log:      parts.spend,
		ToolUse:  parts.toolUse(),
		Recorded: spent,
		Attribution: spend.Attribution{
			ProductID:      cfg.Product.ID,
			Agent:          name,
			Phase:          runstate.SpendPhaseReview,
			AccountAlias:   account.Alias,
			ConfigRevision: cfg.Revision(),
			Backend:        reviewer.Backend,
			BranchReviewID: request.ID,
			Billing:        cfg.AccountBilling(account.Alias),
		},
	}
	result, err := provider.Run(ctx, backend.RunRequest{
		RunID:            request.ID,
		Role:             domain.RoleReviewer,
		WorkingDirectory: parts.repository,
		Prompt:           execution.NewRedactor(parts.redactValues...).Redact(request.Prompt),
		SystemPrompt:     request.System,
		Model:            reviewer.Model,
		Effort:           cfg.InvocationEffort(reviewer, reviewer.Model),
		AllowedTools:     []string{},
		Timeout:          reviewer.TimeLimits.ReviewLimit(),
		LastSequence:     request.LastSequence,
		RedactValues:     parts.redactValues,
		EventSink:        request.Sink,
		AccountAlias:     account.Alias,
		AccountConfigDir: account.Directory,
	})
	if limit := result.UsageLimit; limit != nil && (err != nil || result.IsError) && parts.usageLimits != nil {
		refusal := runstate.UsageLimitExhaustion{
			SchemaVersion: runstate.UsageLimitSchemaVersion,
			ProductID:     cfg.Product.ID,
			At:            time.Now().UTC(),
			Waiting:       "the judgment of a piece that may join a named epic, " + request.ID,
			Kind:          limit.Kind,
			AccountAlias:  account.Alias,
			Model:         strings.TrimSpace(reviewer.Model),
		}
		if !limit.ResetsAt.IsZero() {
			resets := limit.ResetsAt.UTC()
			refusal.ResetsAt = &resets
		}
		if recordErr := parts.usageLimits.Record(refusal); recordErr != nil {
			err = errors.Join(err, fmt.Errorf("record the provider's refusal: %w", recordErr))
		}
	}
	return result, err
}
