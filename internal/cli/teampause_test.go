package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// teamProject is a project whose checkout has a shared remote, with team mode
// on or off as asked. It returns the configuration path and the bare remote.
func teamProject(t *testing.T, team bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	repository := filepath.Join(root, "repository")
	if output, err := exec.Command("git", "init", "--quiet", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, output)
	}
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "init", "--quiet", "-b", "main")
	git(t, repository, "config", "user.name", "Yoyodyne Test")
	git(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	commit(t, repository, "first")
	git(t, repository, "remote", "add", "origin", remote)
	git(t, repository, "push", "--quiet", "origin", "main")
	body := strings.Replace(validConfig, "repository: .", "repository: "+repository, 1)
	if team {
		body += "team:\n  enabled: true\n"
	}
	return writeConfig(t, body), remote
}

func remoteTeamPause(t *testing.T, remote string) string {
	t.Helper()
	output, _ := exec.Command("git", "-C", remote, "rev-parse", "--verify", "--quiet", gitworktree.TeamPauseRef).Output()
	return strings.TrimSpace(string(output))
}

// On a project with team mode off there is no team pause: `yoyo pause --team`
// is refused in plain words, writes nothing to the remote, and pauses nothing
// on this machine either.
func TestPauseTeamIsRefusedWithTeamModeOff(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath, remote := teamProject(t, false)

	_, stderr, code := runCLI(t, "pause", "--team", "--config", configPath)
	if code == 0 {
		t.Fatalf("pause --team code = 0, want a refusal")
	}
	if !strings.Contains(stderr, "no team pause on this project, because its team mode is off") {
		t.Fatalf("pause --team stderr = %q, want the refusal said plainly", stderr)
	}
	if got := remoteTeamPause(t, remote); got != "" {
		t.Fatalf("remote pause = %q, want none written", got)
	}
	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, held, err := holds.Held(); err != nil || held {
		t.Fatalf("Held() = %t, %v, want this machine left unpaused", held, err)
	}
}

// With team mode on, `yoyo pause --team` sets the pause on the shared remote and
// the status line says so and who set it; `--clear` clears it. A machine's own
// `yoyo pause` stays on the machine and writes nothing to the remote.
func TestPauseTeamSetsAndClearsThePauseAndTheMachinePauseStaysLocal(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath, remote := teamProject(t, true)

	stdout, stderr, code := runCLI(t, "pause", "--config", configPath)
	if code != 0 {
		t.Fatalf("pause code = %d, stderr = %q", code, stderr)
	}
	if got := remoteTeamPause(t, remote); got != "" {
		t.Fatalf("remote pause = %q after this machine's own pause, want it kept on the machine", got)
	}
	if strings.Contains(stdout, "team pause") {
		t.Fatalf("pause stdout = %q, want this machine's pause alone", stdout)
	}
	if _, _, code := runCLI(t, "resume", "--config", configPath); code != 0 {
		t.Fatalf("resume code = %d", code)
	}

	stdout, stderr, code = runCLI(t, "pause", "--team", "--config", configPath)
	if code != 0 {
		t.Fatalf("pause --team code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "set the team pause on the shared remote") {
		t.Fatalf("pause --team stdout = %q", stdout)
	}
	if got := remoteTeamPause(t, remote); got == "" {
		t.Fatalf("the team pause was not set on the remote")
	}
	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, held, err := holds.Held(); err != nil || held {
		t.Fatalf("Held() = %t, %v, want the team pause to leave this machine's own pause alone", held, err)
	}

	resolved, err := loadConfiguration(configPath)
	if err != nil {
		t.Fatal(err)
	}
	pause, set, err := standingTeamPause(resolved).Fetched(context.Background())
	if err != nil || !set {
		t.Fatalf("Fetched() = %t, %v, want the pause this machine set", set, err)
	}
	if says := pause.Says(); !strings.Contains(says, "The team pause is set") || !strings.Contains(says, "set it at") || !strings.Contains(says, " (product yoyodyne") {
		t.Fatalf("Says() = %q, want who set it and when", says)
	}

	stdout, stderr, code = runCLI(t, "pause", "--team", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "already set") {
		t.Fatalf("second pause --team = %d, %q, %q; want it already set", code, stdout, stderr)
	}

	stdout, stderr, code = runCLI(t, "pause", "--team", "--clear", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "cleared the team pause") {
		t.Fatalf("pause --team --clear = %d, %q, %q", code, stdout, stderr)
	}
	if got := remoteTeamPause(t, remote); got != "" {
		t.Fatalf("remote pause = %q after clearing, want none", got)
	}
	if _, set, err := standingTeamPause(resolved).Fetched(context.Background()); err != nil || set {
		t.Fatalf("Fetched() after clearing = %t, %v, want no pause", set, err)
	}
}

// A factory with team mode off never reads the team pause: one left on the
// remote by an earlier team does not stop it, and an unreachable remote holds
// back nothing, because no claim, no pause and no status reading is wired to
// ask the remote at all.
func TestTeamModeOffIgnoresALeftoverPauseAndAnUnreachableRemote(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath, remote := teamProject(t, false)

	// A pause an earlier team left behind.
	if output, err := exec.Command("git", "-C", remote, "update-ref", gitworktree.TeamPauseRef, "main").CombinedOutput(); err != nil {
		t.Fatalf("leave a pause on the remote: %v: %s", err, output)
	}

	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	if pipelineTeamClaims(parts) != nil || scheduleTeamClaims(parts) != nil {
		t.Fatalf("team claims were wired with team mode off")
	}
	if _, on := teamPauseOf(parts); on {
		t.Fatalf("the team pause was wired with team mode off")
	}
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if standingTeamPause(resolved) != nil {
		t.Fatalf("the status line was wired to read a team pause with team mode off")
	}

	// And the remote made unreachable changes none of that.
	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}
	parts, err = buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() with the remote gone error = %v", err)
	}
	if pipelineTeamClaims(parts) != nil || standingTeamPause(resolved) != nil {
		t.Fatalf("something was wired to ask an unreachable remote with team mode off")
	}
}
