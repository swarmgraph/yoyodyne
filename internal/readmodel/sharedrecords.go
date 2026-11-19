package readmodel

// The records filed on this machine that have not yet reached the team's shared
// remote (docs/designs/working-alongside-other-contributors.md, section 7). Every
// surface says them in the words sharedRecordsSays gives, so the status line and
// the dashboard are two renderings of this one sentence.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// SharedRecords is the state of this machine's record sharing, which the forge
// check writes in team mode. *runstate.SharedRecordsStore satisfies it.
type SharedRecords interface {
	Load() (runstate.SharedRecordsState, bool, error)
}

// RecordsNotShared is how many records are waiting to reach the shared remote,
// the sentence saying so, and why the state could not be read where it could
// not. Without a source, or with a state never recorded, none waits and nothing
// is said: a machine not in team mode has no records to share.
func RecordsNotShared(sources Sources) (int, string, string) {
	if sources.SharedRecords == nil {
		return 0, "", ""
	}
	state, recorded, err := sources.SharedRecords.Load()
	if err != nil {
		return 0, "", fmt.Sprintf("the records not yet shared with the team could not be read: %v", err)
	}
	if !recorded || state.Waiting <= 0 {
		return 0, "", ""
	}
	return state.Waiting, sharedRecordsSays(state), ""
}

// sharedRecordsSays is the one sentence for a state with records waiting. The
// problem is the last try's own words, left off where the try reached the remote.
func sharedRecordsSays(state runstate.SharedRecordsState) string {
	said := fmt.Sprintf("Not yet shared with the team's other factories: %d record(s) filed here (reports, their handling and directives), kept on this machine and sent at the next forge check", state.Waiting)
	if problem := strings.TrimSpace(state.Problem); problem != "" {
		said += "; the last try did not get through, because " + problem
	}
	return said + "."
}

// renderRecordsNotShared is the sentence, and nothing where no record waits.
func (s Standing) renderRecordsNotShared() string {
	var rendered strings.Builder
	if s.RecordsNotSharedProblem != "" {
		rendered.WriteString(s.RecordsNotSharedProblem + "\n")
	}
	if s.RecordsNotSharedSays != "" {
		rendered.WriteString(s.RecordsNotSharedSays + "\n")
	}
	return rendered.String()
}
