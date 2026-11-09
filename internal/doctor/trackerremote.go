package doctor

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// remoteCommandTimeout bounds the one Git command this asks. `git remote -v`
// reads the repository's configuration and reaches no remote, so a repository
// that has not answered by now is not going to.
const remoteCommandTimeout = 10 * time.Second

// SharedTrackerRemote is one of the tracker's sync remotes that names one of the
// project's own Git remotes.
//
// Beads moves its data over an ordinary Git remote, under refs of its own
// (refs/dolt/data and a __dolt_remote_info__ branch), so a tracker that syncs
// through the project's own remote publishes the backlog into the project's
// repository when it pushes.
type SharedTrackerRemote struct {
	Name      string `json:"name"`       // the tracker's remote name
	URL       string `json:"url"`        // as bd holds it
	GitRemote string `json:"git_remote"` // the name of the project's Git remote it names
}

// ProjectGitRemotes reads every URL the project's Git remotes fetch from or push
// to, keyed by remote name. A remote with a different push URL has both, and a
// remote whose two URLs are the same has it once.
//
// A repository with no remotes answers an empty map and no error, because that
// is an ordinary state rather than a failure. Git failing to run, or exiting
// non-zero, is an error carrying Git's own words.
func ProjectGitRemotes(ctx context.Context, runner execution.ProcessRunner, repository string) (map[string][]string, error) {
	if runner == nil {
		return nil, fmt.Errorf("no process runner was wired")
	}
	result, err := runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    []string{"-C", repository, "remote", "-v"},
		Env:     execution.GitEnvironment(nil),
		Timeout: remoteCommandTimeout,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("git remote -v: %w", err)
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("git remote -v: %s", describeFailure(result, nil))
	}
	remotes := map[string][]string{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		// Each line is `<name>\t<url> (fetch)` or `<name>\t<url> (push)`.
		name, rest, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found || name == "" {
			continue
		}
		url := strings.TrimSpace(rest)
		for _, suffix := range []string{" (fetch)", " (push)"} {
			url = strings.TrimSuffix(url, suffix)
		}
		if url == "" || slices.Contains(remotes[name], url) {
			continue
		}
		remotes[name] = append(remotes[name], url)
	}
	return remotes, nil
}

// TrackerRemotesOnProject answers the tracker's sync remotes, all of them as bd
// lists them, and the subset that names one of the project's own Git remotes.
//
// Remotes are compared as the place they name (home.SameRemote), so
// git@host:o/r.git, https://host/o/r and the git+ssh://git@host/o/r spelling bd
// stores are one place, and so are a local path and the git+file URL for it.
// Git is asked first and bd second, and either failing is an error, because an
// answer built from half the facts would clear a remote that was never compared.
func TrackerRemotesOnProject(ctx context.Context, runner execution.ProcessRunner, repository string) (shared []SharedTrackerRemote, all []beads.SyncRemote, err error) {
	gitRemotes, err := ProjectGitRemotes(ctx, runner, repository)
	if err != nil {
		return nil, nil, err
	}
	client := beads.Client{Runner: runner, Dir: repository, Timeout: beads.DefaultTimeout}
	all, err = client.SyncRemotes(ctx)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(gitRemotes))
	for name := range gitRemotes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, remote := range all {
		for _, name := range names {
			if slices.ContainsFunc(gitRemotes[name], func(url string) bool { return home.SameRemote(url, remote.URL) }) {
				shared = append(shared, SharedTrackerRemote{Name: remote.Name, URL: remote.URL, GitRemote: name})
				break
			}
		}
	}
	return shared, all, nil
}

// RemoveTrackerRemotesCommand is the command that keeps the tracker on this
// machine: it removes each named sync remote, and says which directory to run
// it in, because bd answers for the project it is run in.
func RemoveTrackerRemotesCommand(names []string, repository string) string {
	removals := make([]string, 0, len(names))
	for _, name := range names {
		removals = append(removals, "bd dolt remote remove "+shellQuote(name))
	}
	return strings.Join(removals, " && ") + inDirectory(repository)
}

// checkTrackerRemote reports whether a configuration kept outside the
// repository sends the backlog into that repository.
//
// An external configuration exists so that nothing of Yoyodyne's is published
// into a repository whose owner has not taken it, and a tracker that syncs
// through the project's own Git remote undoes that on the first push. A
// configuration inside the repository says nothing here: the backlog's being
// published with the project is what that configuration chose. A tracker that
// cannot be read says nothing either, because checkTracker already reports it.
//
// It is a warning rather than a problem because work still runs; what it costs
// is paid at the next push.
func (d *diagnosis) checkTrackerRemote(ctx context.Context, resolved config.Resolved, repository string) []Finding {
	if _, inside := insideRepository(repository, resolved.Path); inside {
		return nil
	}
	shared, _, err := TrackerRemotesOnProject(ctx, d.env.Runner, repository)
	if err != nil {
		return nil
	}
	if len(shared) == 0 {
		return []Finding{{
			Check:   "tracker-remote",
			Status:  StatusOK,
			Summary: "this configuration is kept outside the repository, and the tracker does not sync through this project's own Git remote",
		}}
	}
	names := make([]string, 0, len(shared))
	lines := make([]string, 0, len(shared))
	for _, remote := range shared {
		names = append(names, remote.Name)
		lines = append(lines, fmt.Sprintf("%s -> %s (this project's Git remote %s)", remote.Name, remote.URL, remote.GitRemote))
	}
	first := shared[0]
	return []Finding{{
		Check:  "tracker-remote",
		Status: StatusWarning,
		Summary: fmt.Sprintf("this configuration is kept outside the repository, and the tracker syncs through %s, which is this project's own Git remote %s, "+
			"so a push publishes the backlog there; remove it to keep the tracker on this machine, or point it at a repository of its own with "+
			"`yoyo init --external --force --tracker-remote <url>`", first.Name, first.GitRemote),
		Detail: strings.Join(lines, "\n"),
		Remedy: RemoveTrackerRemotesCommand(names, repository),
	}}
}
