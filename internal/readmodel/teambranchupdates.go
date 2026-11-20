package readmodel

// The record of bringing the target branch up to date with the team's branch
// (execution.upstream_branch), which the forge check keeps
// (runstate.TeamBranchUpdate). Every surface says it in the words
// TeamBranchUpdate.Says gives, so the status line and the dashboard are two
// renderings of this one list. Only a project with an upstream branch set has a
// record, and a reading with none says nothing.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// TeamBranchUpdates is the update records the read model projects. It is an
// optional source, satisfied by *runstate.TeamBranchUpdateStore.
type TeamBranchUpdates interface {
	All() ([]runstate.TeamBranchUpdate, error)
}

// ReadTeamBranchUpdates is every record the product has, and why they could not
// be read where they could not. No source reads as none.
func ReadTeamBranchUpdates(source TeamBranchUpdates) ([]runstate.TeamBranchUpdate, string) {
	if source == nil {
		return nil, ""
	}
	updates, err := source.All()
	if err != nil {
		return nil, fmt.Sprintf("the record of bringing the target branch up to date with the team's branch could not be read: %v", err)
	}
	return updates, ""
}

// renderTeamBranchUpdates is the records' sentences, one per line, and nothing
// where there are none.
func (s Standing) renderTeamBranchUpdates() string {
	var rendered strings.Builder
	if s.TeamBranchUpdateProblem != "" {
		rendered.WriteString(s.TeamBranchUpdateProblem + "\n")
	}
	for _, said := range s.TeamBranchUpdateSays {
		rendered.WriteString(said + "\n")
	}
	return rendered.String()
}
