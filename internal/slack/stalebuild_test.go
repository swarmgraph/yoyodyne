package slack

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The cases where a session is behind by design, and where it is behind
// because something stopped, as the supervisor's record and the repository
// describe them. Nothing here is ever said to the operators directly: none of
// the causes is on the closed list of things only the operator can do.

// A morning of published documents lands no change to what gets built, so the
// count is zero and nothing is said, whatever the supervisor's record holds.
// The count itself is the repository's (cli's buildLandings); this is the sink
// keeping silent on it.
func TestDocumentsOnlyLandingsSayNothing(t *testing.T) {
	t.Parallel()
	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(0)
	harness.feed.Installation = &fakeInstallation{installed: Installed{Supervised: true, Build: staleResidentBuild}}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start())
	for hour := 0; hour < 4; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
}

// The session runs the installed build while the next landing is being built:
// the supervisor builds by itself, and a build takes minutes.
func TestASessionOnTheInstalledBuildWhileTheNextIsMadeSaysNothing(t *testing.T) {
	t.Parallel()
	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(2)
	harness.feed.Installation = &fakeInstallation{
		installed: Installed{Supervised: true, Build: staleResidentBuild},
		unbuilt:   Unbuilt{Since: harness.now.Add(-3 * time.Minute)},
	}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	harness.poll(t, harness.start())
}

// A newer build is installed and the session holds its own until the least
// time between two moves is up, which is the rule rather than a fault.
func TestANewerBuildWaitingInsideTheMoveIntervalSaysNothing(t *testing.T) {
	t.Parallel()
	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(27)
	harness.feed.Installation = &fakeInstallation{installed: Installed{
		Supervised: true,
		Build:      currentResidentBuild,
		MoveDue:    harness.now.Add(2 * time.Hour),
		MoveBy:     harness.now.Add(2*time.Hour + 30*time.Minute),
	}}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(time.Hour + time.Minute)
	harness.poll(t, cursors)
}

// A landing left unbuilt past the time a build takes is a build that cannot be
// made. It is said as a warning naming the development manager, who has the
// main line fixed, and not to the operators.
func TestAFailingBuildIsAWarningForTheDevelopmentManager(t *testing.T) {
	t.Parallel()
	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(2)
	harness.feed.Installation = &fakeInstallation{
		installed: Installed{Supervised: true, Build: staleResidentBuild},
		unbuilt:   Unbuilt{Since: harness.now.Add(-time.Hour)},
	}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	said := harness.resident(t, harness.start())
	assertStaleBuild(t, said, report.SeverityWarning, "the development manager's", "fixed")
}

// Uncommitted changes in the checkout stop the supervisor building. They are
// somebody's work in progress that no role may touch, and the closed list has
// no reason for them, so they are the Lead Product Manager's to classify.
func TestUncommittedChangesStoppingTheBuildAreAWarning(t *testing.T) {
	t.Parallel()
	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(2)
	harness.feed.Installation = &fakeInstallation{
		installed: Installed{Supervised: true, Build: staleResidentBuild},
		unbuilt:   Unbuilt{Since: harness.now.Add(-time.Hour), Uncommitted: " M internal/cli/slack.go"},
	}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	said := harness.resident(t, harness.start())
	assertStaleBuild(t, said, report.SeverityWarning, "the Lead Product Manager's", "uncommitted changes")
}

// A move that has fallen due and that the session has not begun is a note
// while the supervisor's own deadline for restarting it stands, and a warning
// for the development manager once that deadline is well past.
func TestAMovePastDueIsANoteAndThenAWarning(t *testing.T) {
	t.Parallel()
	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(4)
	installation := &fakeInstallation{installed: Installed{
		Supervised: true,
		Build:      currentResidentBuild,
		MoveDue:    harness.now.Add(-10 * time.Minute),
		MoveBy:     harness.now.Add(5 * time.Minute),
	}}
	harness.feed.Installation = installation
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.start()
	due := harness.resident(t, cursors)
	assertStaleBuild(t, due, report.SeverityNote, "the harness's", "supervisor restarts")
	cursors.Streams[residentStream] = due.Cursor

	harness.now = harness.now.Add(time.Hour)
	stuck := harness.resident(t, cursors)
	assertStaleBuild(t, stuck, report.SeverityWarning, "the development manager's", "has not moved")
}

// A move the supervisor's restart is held from — the provider answering
// nobody — stays a note for the harness, because the supervisor makes it once
// the hold ends and the outage is said by its own message.
func TestAMoveHeldByTheSupervisorsGuardStaysANote(t *testing.T) {
	t.Parallel()
	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(4)
	harness.feed.Installation = &fakeInstallation{installed: Installed{
		Supervised: true,
		Build:      currentResidentBuild,
		MoveDue:    harness.now.Add(-3 * time.Hour),
		MoveBy:     harness.now.Add(-2 * time.Hour),
		MoveHeld:   "the provider cannot be reached",
	}}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	assertStaleBuild(t, harness.resident(t, harness.start()), report.SeverityNote, "the harness's", "supervisor restarts")
}

// With no supervisor running nothing builds or installs a landing, so how far
// behind decides how loud, and the next step is starting the supervisor.
func TestNoSupervisorIsSaidByTheCount(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		behind int
		want   report.Severity
	}{{2, report.SeverityNote}, {DefaultStaleBuildThreshold, report.SeverityWarning}} {
		harness := newTestHarness(t, time.Time{})
		harness.ready(1)
		harness.deployed(c.behind)
		harness.feed.Installation = &fakeInstallation{}
		harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)
		assertStaleBuild(t, harness.resident(t, harness.start()), c.want, "the harness's", "supervisor")
	}
}

// A supervisor's record that will not be read leaves the sink unable to tell a
// session behind by design from a stuck one, so it guesses neither way: it says
// so in its own log, posts nothing, and asks again at the next interval.
func TestAnInstallationThatCannotBeReadIsNotGuessedAt(t *testing.T) {
	t.Parallel()
	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(9)
	var said []string
	harness.feed.Log = func(format string, args ...any) { said = append(said, format) }
	harness.feed.Installation = &fakeInstallation{err: errors.New("decode supervision record: unexpected end of JSON input")}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start())
	harness.poll(t, cursors)
	if len(said) != 1 {
		t.Fatalf("the sink said %v, want it said once and asked again at the next interval", said)
	}
}

// assertStaleBuild checks one delivery about the session's build: how loud,
// whose move it names, that it never reaches or tags the operators, and that
// the message as rendered never names the operator.
func assertStaleBuild(t *testing.T, said Delivery, severity report.Severity, mover, remedy string) {
	t.Helper()
	if said.Notification.Event.Kind != notify.KindResidentStale {
		t.Fatalf("said %q, want %q", said.Notification.Event.Kind, notify.KindResidentStale)
	}
	if got := said.Notification.Event.Severity; got != severity {
		t.Fatalf("severity = %q, want %q", got, severity)
	}
	if said.Direct || said.Tag {
		t.Fatalf("the operators were told directly (direct %t, tagged %t), and nothing here is theirs", said.Direct, said.Tag)
	}
	message, err := notify.Render(said.Notification.Topic, said.Notification.Speaker, said.Notification.Event)
	if err != nil {
		t.Fatalf("the message could not be said: %v", err)
	}
	if !strings.Contains(message.Body, mover) || !strings.Contains(message.Body, remedy) {
		t.Fatalf("body %q does not name %q with %q", message.Body, mover, remedy)
	}
	if strings.Contains(message.Body, "operator") {
		t.Fatalf("body %q names the operator", message.Body)
	}
}

type fakeInstallation struct {
	installed Installed
	unbuilt   Unbuilt
	err       error
}

func (f *fakeInstallation) Installed(context.Context) (Installed, error) { return f.installed, f.err }

func (f *fakeInstallation) Unbuilt(context.Context, string) (Unbuilt, error) {
	return f.unbuilt, f.err
}
