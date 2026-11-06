package cli

import (
	"os"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/teamclaim"
)

// teamClaims is the claims this machine takes and keeps on the shared remote,
// and false where the project has team mode off or the claims cannot be kept,
// in which case nothing is wired and no start waits on the remote. A store that
// cannot be opened in team mode is not silently dropped: the pipeline is given
// claims that refuse every take, so no new work starts without a claim.
func teamClaims(parts components) (teamclaim.Claims, bool) {
	if !parts.config.Team.Enabled || parts.worktrees == nil {
		return teamclaim.Claims{}, false
	}
	claims := teamclaim.Claims{Remote: parts.worktrees, Factory: factoryName(parts)}
	if store, err := runstate.NewTeamClaimStore(parts.stateRoot, parts.config.Product.ID); err == nil {
		claims.Store = store
	}
	return claims, true
}

// factoryName is how this machine names itself in a claim, for another factory
// to say who holds an item: the product, the machine's host name, and the
// machine identifier the run store keeps, which is what tells two homes on one
// machine, or two machines that kept the same name, apart. Which claims are
// this machine's own is never read from it (teamclaim.Claims.Factory).
func factoryName(parts components) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "an unnamed machine"
	}
	name := string(parts.config.Product.ID) + " on " + host
	if parts.store != nil {
		if machine, err := parts.store.MachineID(); err == nil {
			name += " (" + machine + ")"
		}
	}
	return name
}

// pipelineTeamClaims is what a run starting in team mode takes its claim
// through, and nil with team mode off.
func pipelineTeamClaims(parts components) orchestrator.TeamClaims {
	claims, on := teamClaims(parts)
	if !on {
		return nil
	}
	return claims
}

// scheduleTeamClaims is what the watch loop renews and releases claims
// through, and nil with team mode off.
func scheduleTeamClaims(parts components) orchestrator.ScheduleTeamClaims {
	claims, on := teamClaims(parts)
	if !on {
		return nil
	}
	return claims
}
