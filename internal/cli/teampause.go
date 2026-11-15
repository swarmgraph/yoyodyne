package cli

// `yoyo pause --team`: the team pause on the shared remote, in team mode
// (internal/teamclaim/pause.go says what it stops). On a project with team
// mode off there is no team pause, so the flag is refused rather than writing a
// reference no factory would read.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/user"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/teamclaim"
)

type teamPauseOutput struct {
	Pause *teamclaim.Pause `json:"pause,omitempty"`
	// Set is whether the team pause stands after the command, and Changed
	// whether the command set or cleared it rather than finding it already so.
	Set     bool   `json:"set"`
	Changed bool   `json:"changed"`
	Error   string `json:"error,omitempty"`
}

// errTeamModeOff is `yoyo pause --team` on a project that works its repository
// alone.
var errTeamModeOff = errors.New("there is no team pause on this project, because its team mode is off: a factory working the repository alone never reads one. `yoyo pause` without --team pauses this machine; a project shared by several factories turns team mode on with team.enabled: true in its configuration")

func pauseTeam(parts components, clear, jsonOutput bool, stdout, stderr io.Writer) int {
	report := func(err error) int {
		if jsonOutput {
			if code := writeJSON(stdout, stderr, teamPauseOutput{Error: err.Error()}); code != 0 {
				return code
			}
			return 1
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	pause, on := teamPauseOf(parts)
	if !on {
		return report(errTeamModeOff)
	}
	ctx := context.Background()
	if clear {
		cleared, was, err := pause.Clear(ctx)
		if err != nil {
			return report(err)
		}
		if jsonOutput {
			output := teamPauseOutput{Set: false, Changed: was}
			if was {
				output.Pause = &cleared
			}
			return writeJSON(stdout, stderr, output)
		}
		if !was {
			fmt.Fprintln(stdout, "the team pause was not set; nothing changed")
			return 0
		}
		fmt.Fprintf(stdout, "cleared the team pause %s set at %s\n", cleared.Setter(), localTime(cleared.SetAt))
		fmt.Fprintln(stdout, "every factory working this repository starts new work again at its next claim")
		return 0
	}
	set, already, err := pause.Set(ctx, time.Now())
	if err != nil {
		return report(err)
	}
	if jsonOutput {
		return writeJSON(stdout, stderr, teamPauseOutput{Pause: &set, Set: true, Changed: !already})
	}
	if already {
		fmt.Fprintf(stdout, "the team pause was already set, by %s at %s; nothing changed\n", set.Setter(), localTime(set.SetAt))
	} else {
		fmt.Fprintf(stdout, "set the team pause on the shared remote at %s\n", localTime(set.SetAt))
	}
	fmt.Fprintln(stdout, "no factory working this repository starts new work the harness chose while it is set; each reads it before it claims an item")
	fmt.Fprintln(stdout, "runs in progress finish, and repair rounds and waiting pull requests carry on; an item an operator names on their own machine is still started")
	fmt.Fprintln(stdout, "it does not pause any machine's spending: `yoyo pause` without --team does that, for this machine only")
	fmt.Fprintln(stdout, "`yoyo pause --team --clear` clears it")
	return 0
}

// teamPauseOf is the team pause for this project, and false where its team mode
// is off or there is no repository to reach the shared remote through.
func teamPauseOf(parts components) (teamclaim.TeamPause, bool) {
	if !parts.config.Team.Enabled || parts.worktrees == nil {
		return teamclaim.TeamPause{}, false
	}
	return teamclaim.TeamPause{Remote: parts.worktrees, SetBy: personName(), Factory: factoryName(parts)}, true
}

// personName is who is at this terminal, as this machine's account names them.
func personName() string {
	current, err := user.Current()
	if err != nil {
		return ""
	}
	if name := strings.TrimSpace(current.Username); name != "" {
		return name
	}
	return strings.TrimSpace(current.Name)
}

func localTime(moment time.Time) string {
	return moment.Local().Format("2006-01-02 15:04 MST")
}

// standingTeamPause is the team pause the status line reads, from this
// machine's last fetched copy, and nil where the project's team mode is off: a
// factory working its repository alone reads no team pause, so one left on the
// remote by an earlier team is never said as stopping it.
func standingTeamPause(resolved config.Resolved) readmodel.TeamPause {
	if !resolved.Config.Team.Enabled {
		return nil
	}
	roots, err := resolveRoots(resolved)
	if err != nil {
		return nil
	}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: roots.repository,
		WorktreeRoot:   roots.worktreeRoot,
		Remote:         resolved.Config.Execution.Remote,
	})
	if err != nil {
		return nil
	}
	return teamclaim.TeamPause{Remote: worktrees}
}
