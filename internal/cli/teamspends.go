package cli

import (
	"context"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamspends"
)

// teamSpends is what counts and records each item's budgets across the team's
// factories, through spend records in the shared records reference, and false
// where the project has team mode off, in which case nothing is wired and no
// spend waits on the remote (docs/designs/working-alongside-other-contributors.md,
// section 8).
func teamSpends(parts components) (teamspends.Spender, *runstate.SharedRecordsStore, bool, error) {
	if !parts.config.Team.Enabled || parts.worktrees == nil {
		return teamspends.Spender{}, nil, false, nil
	}
	state, err := runstate.NewSharedRecordsStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return teamspends.Spender{}, nil, false, err
	}
	claims, err := runstate.NewTeamClaimStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return teamspends.Spender{}, nil, false, err
	}
	// The identity is read when a record is written, not when the command starts,
	// because reading it asks the forge.
	identities := factoryIdentities(parts)
	return teamspends.Spender{
		Open: func(ctx context.Context) (teamspends.Remote, error) {
			return openRecordsRepository(ctx, parts, state)
		},
		Claims: claims,
		Lock:   state,
		Factory: func(ctx context.Context) string {
			if name, err := identities.Name(ctx); err == nil && strings.TrimSpace(name) != "" {
				return name
			}
			return factoryName(parts)
		},
	}, state, true, nil
}

// wireTeamSpends gives the run store the team's spends, so every grant, re-run,
// merge re-arm and review round is counted across factories and recorded before
// it is made. With team mode off it does nothing.
func wireTeamSpends(parts components) error {
	spender, _, on, err := teamSpends(parts)
	if err != nil || !on {
		return err
	}
	parts.store.UseSharedSpends(spender)
	return nil
}

// joinTeamSpends pushes the spends this machine had counted before it joined
// team mode, once. A machine that cannot reach the remote tries again at the
// next check, and one that has pushed them is not asked again.
func joinTeamSpends(ctx context.Context, parts components) error {
	spender, state, on, err := teamSpends(parts)
	if err != nil || !on {
		return err
	}
	if _, done, err := state.SpendsJoined(); err != nil || done {
		return err
	}
	items, err := parts.store.Triage().All()
	if err != nil {
		return err
	}
	if _, err := spender.Join(ctx, items); err != nil {
		return err
	}
	return state.MarkSpendsJoined(time.Now())
}
