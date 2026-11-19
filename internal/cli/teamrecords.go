package cli

import (
	"context"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamrecords"
)

// teamRecords is the sharing of reports, directives and what was done about
// them with the team's other factories, through the records reference on the
// shared remote, and false where the project has team mode off or no worktree
// manager, in which case nothing is wired and nothing reads the remote. It is
// the one place the records repository is opened: the forge check and the Slack
// connection reach the records through the Sharer it builds, and nothing an
// agent is given does.
func teamRecords(ctx context.Context, parts components) (teamrecords.Sharer, bool, error) {
	if !parts.config.Team.Enabled || parts.worktrees == nil {
		return teamrecords.Sharer{}, false, nil
	}
	state, err := runstate.NewSharedRecordsStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return teamrecords.Sharer{}, false, err
	}
	remote, err := parts.worktrees.RecordsRepository(ctx, state.RepositoryDirectory())
	if err != nil {
		return teamrecords.Sharer{}, false, err
	}
	directives, err := runstate.NewDirectiveStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return teamrecords.Sharer{}, false, err
	}
	reports := parts.reports
	if reports == nil {
		if reports, err = runstate.NewReportStore(parts.stateRoot, parts.config.Product.ID); err != nil {
			return teamrecords.Sharer{}, false, err
		}
	}
	identities := factoryIdentities(parts)
	factory, err := identities.Name(ctx)
	if err != nil || strings.TrimSpace(factory) == "" {
		factory = factoryName(parts)
	}
	return teamrecords.Sharer{
		Remote:     remote,
		Reports:    reports,
		Directives: directives,
		State:      state,
		Factory:    factory,
	}, true, nil
}
