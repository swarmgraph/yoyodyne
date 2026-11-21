package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
)

// buildLandings answers, for the Slack sink, how far the repository has moved
// past one build in landings that change what gets built: landings on the
// main line, counted once each however many commits a merge carries, and only
// those touching the paths the supervisor rebuilds for (supervise.RebuildSources).
// A landing of documents builds nothing and moves no part, so it is not counted.
//
// It is the sink's count alone. A report's or a conversation's build is still
// measured in commits by repositoryDeployments, because what those ask is what
// the repository holds that the build did not.
type buildLandings struct {
	repositoryDeployments
}

// landingRange is the main line's own landings after build, each once,
// limited to what gets built.
func landingRange(build string) []string {
	return append([]string{"--first-parent", build + "..HEAD", "--"}, supervise.RebuildSources...)
}

func (d buildLandings) Behind(ctx context.Context, build string) (int, error) {
	if err := d.holds(ctx, build); err != nil {
		return 0, err
	}
	return d.count(ctx, append([]string{"rev-list", "--count"}, landingRange(build)...)...)
}

// Unbuilt is when the earliest landing after build that changes what gets
// built landed, and the checkout's uncommitted changes to what gets built,
// which the supervisor will not build over.
func (d buildLandings) Unbuilt(ctx context.Context, build string) (slack.Unbuilt, error) {
	if err := d.holds(ctx, build); err != nil {
		return slack.Unbuilt{}, err
	}
	listed, err := d.output(ctx, append([]string{"rev-list", "--reverse"}, landingRange(build)...)...)
	if err != nil {
		return slack.Unbuilt{}, err
	}
	first, _, _ := strings.Cut(listed, "\n")
	first = strings.TrimSpace(first)
	if first == "" {
		return slack.Unbuilt{}, nil
	}
	// The committer's date of a landing on the main line is when it landed.
	stamp, err := d.output(ctx, "show", "-s", "--format=%ct", first)
	if err != nil {
		return slack.Unbuilt{}, err
	}
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return slack.Unbuilt{}, fmt.Errorf("git show did not answer with a time for %s: %s", first, singleLine(stamp))
	}
	dirty, err := d.output(ctx, append([]string{"status", "--porcelain", "--"}, supervise.RebuildSources...)...)
	if err != nil {
		return slack.Unbuilt{}, err
	}
	return slack.Unbuilt{Since: time.Unix(seconds, 0), Uncommitted: dirty}, nil
}

// supervisedInstallation is the supervisor's record of the installed binary
// and of the watch session's move onto it, with the repository's account of
// what has landed and not been built.
type supervisedInstallation struct {
	supervision *runstate.SupervisionStore
	landings    buildLandings
}

func (i supervisedInstallation) Installed(context.Context) (slack.Installed, error) {
	running, err := i.supervision.Running()
	if err != nil {
		return slack.Installed{}, fmt.Errorf("whether the product's supervisor is running: %w", err)
	}
	if !running {
		return slack.Installed{}, nil
	}
	recorded, found, err := i.supervision.Load()
	if err != nil {
		return slack.Installed{}, err
	}
	installed := slack.Installed{Supervised: true}
	if !found {
		return installed, nil
	}
	installed.Build = recorded.Deployed
	if scheduler, ok := recorded.Child(config.ServiceScheduler); ok {
		installed.MoveDue, installed.MoveBy, installed.MoveHeld = scheduler.MoveDue, scheduler.MoveBy, scheduler.MoveHeld
	}
	return installed, nil
}

func (i supervisedInstallation) Unbuilt(ctx context.Context, build string) (slack.Unbuilt, error) {
	return i.landings.Unbuilt(ctx, build)
}
