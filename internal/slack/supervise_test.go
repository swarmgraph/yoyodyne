package slack

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
)

// The two tokens every test here uses. They are obviously not credentials, and
// they are distinct strings so that a test asserting one never appears can tell
// which one leaked.
const (
	botToken     = "xoxb-this-products-bot"
	appToken     = "xapp-this-products-app"
	siblingToken = "xoxb-the-other-products-bot"
)

// storedSecrets is a machine's secret store: the names in it are stored, and
// every other name is one nobody stored.
type storedSecrets map[string]string

func (s storedSecrets) Secret(_ context.Context, name string) (string, error) {
	value, stored := s[name]
	if !stored {
		return "", ErrSecretUnavailable
	}
	return value, nil
}

// recordingLauncher stands in for starting a process, so a test can ask what
// the sink would have been started with.
type recordingLauncher struct {
	started []Launch
	pid     int
	err     error
}

func (l *recordingLauncher) Launch(spec Launch) (int, error) {
	l.started = append(l.started, spec)
	if l.err != nil {
		return 0, l.err
	}
	if l.pid == 0 {
		l.pid = 4242
	}
	return l.pid, nil
}

func supervisorFor(t *testing.T, root string, product domain.ProductID, secrets storedSecrets, launcher *recordingLauncher, environ []string) Supervisor {
	t.Helper()
	store, err := NewStore(root, product)
	if err != nil {
		t.Fatalf("NewStore(%s) error = %v", product, err)
	}
	return Supervisor{
		Store:    store,
		Secrets:  secrets,
		Launcher: launcher,
		Program:  "/opt/yoyo/bin/yoyo",
		Config:   "/products/" + string(product) + "/.yoyodyne/config.yaml",
		Dir:      "/products/" + string(product),
		Environ:  environ,
	}
}

func storedPair(product domain.ProductID) storedSecrets {
	return storedSecrets{BotSecret(product): botToken, AppSecret(product): appToken}
}

// holdSink takes a product's sink lease for the rest of the test, which is what
// a running sink looks like to everything else.
func holdSink(t *testing.T, root string, product domain.ProductID) {
	t.Helper()
	store, err := NewStore(root, product)
	if err != nil {
		t.Fatalf("NewStore(%s) error = %v", product, err)
	}
	lease, held, err := store.Lease()
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v, want the lease nobody else holds", held, err)
	}
	t.Cleanup(func() {
		if err := lease.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	})
}

// A pass that runs every few minutes meets a running sink almost every time,
// and starting a second one is the one thing it must never do: two sinks hold
// separate thread maps, so they open separate threads and post everything twice.
func TestASinkAlreadyReportingIsNotStartedAgain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	holdSink(t, root, "yoyodyne")
	launcher := &recordingLauncher{}

	supervision, err := supervisorFor(t, root, "yoyodyne", storedPair("yoyodyne"), launcher, nil).Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if supervision.Outcome != OutcomeRunning {
		t.Fatalf("outcome = %q, want %q", supervision.Outcome, OutcomeRunning)
	}
	if len(launcher.started) != 0 {
		t.Fatalf("started %d sinks over one that was already reporting", len(launcher.started))
	}
}

// Several harnesses on one machine is the ordinary case, and it is where a pass
// that asks the process table gets both answers wrong: `pgrep -f "yoyo slack"`
// matches the sibling's sink, so this product is never started, and a pass that
// did start one from a single hard-coded namespace would start it holding the
// sibling's tokens. The lease is per product and so are the secret names, so
// neither product answers for the other.
func TestASiblingProductsSinkDoesNotAnswerForThisOne(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	holdSink(t, root, "sibling")
	launcher := &recordingLauncher{}

	supervision, err := supervisorFor(t, root, "yoyodyne", storedPair("yoyodyne"), launcher, nil).Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if supervision.Outcome != OutcomeStarted {
		t.Fatalf("outcome = %q, want this product's sink started while the sibling's runs", supervision.Outcome)
	}
	if len(launcher.started) != 1 {
		t.Fatalf("started %d sinks, want exactly one", len(launcher.started))
	}

	// And the sibling is still the one product this pass leaves alone.
	sibling, err := supervisorFor(t, root, "sibling", storedPair("sibling"), launcher, nil).Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure(sibling) error = %v", err)
	}
	if sibling.Outcome != OutcomeRunning {
		t.Fatalf("sibling outcome = %q, want %q", sibling.Outcome, OutcomeRunning)
	}
	if len(launcher.started) != 1 {
		t.Fatalf("started %d sinks, want the sibling's left alone", len(launcher.started))
	}
}

// The sink gets this product's pair and this product's namespace, over an
// environment that had the sibling's pair exported into it. Inheriting instead
// is how a sink connects, authenticates, and posts this product's work through
// another project's Slack app.
func TestTheSinkIsStartedWithThisProductsOwnTokensAndNothingElse(t *testing.T) {
	t.Parallel()

	launcher := &recordingLauncher{}
	environ := []string{
		"PATH=/usr/bin",
		BotTokenVariable + "=" + siblingToken,
		AppTokenVariable + "=" + siblingToken,
		SecretNamespaceVariable + "=sibling",
	}

	if _, err := supervisorFor(t, t.TempDir(), "yoyodyne", storedPair("yoyodyne"), launcher, environ).Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	started := launcher.started[0]

	want := []string{
		"PATH=/usr/bin",
		BotTokenVariable + "=" + botToken,
		AppTokenVariable + "=" + appToken,
		SecretNamespaceVariable + "=yoyodyne",
	}
	if !slices.Equal(started.Env, want) {
		t.Fatalf("the sink's environment is %q, want %q", started.Env, want)
	}
	for _, entry := range started.Env {
		if strings.Contains(entry, siblingToken) {
			t.Fatalf("the sink was started holding the sibling product's token: %q", entry)
		}
	}
}

// The sink is told which configuration to read and which directory to read it
// from, because a pass runs from wherever it was installed: a sink left to find
// a configuration would find whichever project is nearest that directory.
func TestTheSinkIsToldWhichProjectItIsReportingFor(t *testing.T) {
	t.Parallel()

	launcher := &recordingLauncher{}
	supervisor := supervisorFor(t, t.TempDir(), "yoyodyne", storedPair("yoyodyne"), launcher, nil)

	if _, err := supervisor.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	started := launcher.started[0]
	if started.Program != supervisor.Program {
		t.Fatalf("started %q, want the binary supervising it, %q", started.Program, supervisor.Program)
	}
	if want := []string{"slack", "--config", supervisor.Config}; !slices.Equal(started.Args, want) {
		t.Fatalf("started with %q, want %q", started.Args, want)
	}
	if started.Dir != supervisor.Dir {
		t.Fatalf("started in %q, want %q", started.Dir, supervisor.Dir)
	}
}

// One log per product, beside that product's own sink state. One file every
// product on the machine appends to is a log that answers no question about
// either of them.
func TestEachProductsSinkLogsBesideItsOwnState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	launcher := &recordingLauncher{}
	for _, product := range []domain.ProductID{"yoyodyne", "sibling"} {
		supervision, err := supervisorFor(t, root, product, storedPair(product), launcher, nil).Ensure(context.Background())
		if err != nil {
			t.Fatalf("Ensure(%s) error = %v", product, err)
		}
		want := filepath.Join(home.ProductDirectory(root, string(product)), "slack", sinkLogFile)
		if supervision.Log != want {
			t.Fatalf("%s logs to %q, want %q", product, supervision.Log, want)
		}
	}
	if launcher.started[0].Log == launcher.started[1].Log {
		t.Fatal("two products' sinks were started logging to one file")
	}
}

// A pair nobody stored is the operator's to settle, so both names are said at
// once: told about one, they store it, and the next pass tells them about the
// other.
func TestEveryUnreadableSecretIsNamedAndNothingIsStarted(t *testing.T) {
	t.Parallel()

	launcher := &recordingLauncher{}
	supervision, err := supervisorFor(t, t.TempDir(), "yoyodyne", storedSecrets{}, launcher, nil).Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if supervision.Outcome != OutcomeSecretsUnavailable {
		t.Fatalf("outcome = %q, want %q", supervision.Outcome, OutcomeSecretsUnavailable)
	}
	want := []string{BotSecret("yoyodyne"), AppSecret("yoyodyne")}
	if !slices.Equal(supervision.Secrets, want) {
		t.Fatalf("named %q, want both of %q", supervision.Secrets, want)
	}
	if len(launcher.started) != 0 {
		t.Fatalf("started %d sinks with no tokens to start one with", len(launcher.started))
	}
}

// Half a pair is no pair: a sink started with one token authenticates with
// neither, so the missing one is named and nothing is started.
func TestOneStoredTokenIsNotEnoughToStartASink(t *testing.T) {
	t.Parallel()

	launcher := &recordingLauncher{}
	secrets := storedSecrets{BotSecret("yoyodyne"): botToken}
	supervision, err := supervisorFor(t, t.TempDir(), "yoyodyne", secrets, launcher, nil).Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if supervision.Outcome != OutcomeSecretsUnavailable {
		t.Fatalf("outcome = %q, want %q", supervision.Outcome, OutcomeSecretsUnavailable)
	}
	if want := []string{AppSecret("yoyodyne")}; !slices.Equal(supervision.Secrets, want) {
		t.Fatalf("named %q, want %q", supervision.Secrets, want)
	}
	if len(launcher.started) != 0 {
		t.Fatalf("started %d sinks on half a pair", len(launcher.started))
	}
}

// What a pass says about itself goes to a terminal, a log, and whatever
// collects them, so no outcome it produces may carry a token — including the
// one whose whole subject is the tokens.
func TestNothingSupervisionSaysCarriesAToken(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	launcher := &recordingLauncher{}
	refusing := storedSecrets{}

	started, err := supervisorFor(t, root, "yoyodyne", storedPair("yoyodyne"), launcher, nil).Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	unavailable, err := supervisorFor(t, root, "sibling", refusing, launcher, nil).Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure(sibling) error = %v", err)
	}

	for _, supervision := range []Supervision{started, unavailable} {
		said, err := json.Marshal(supervision)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		for _, token := range []string{botToken, appToken} {
			if strings.Contains(string(said), token) {
				t.Fatalf("what supervision said carries a token: %s", said)
			}
		}
	}
}

// keychainRunner answers the one command this reads with, and records how it
// was asked.
type keychainRunner struct {
	asked     []string
	observed  bool
	stdout    string
	stderr    string
	failed    bool
	runFailed error
}

func (r *keychainRunner) Run(_ context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.asked = append(r.asked, strings.Join(append([]string{command.Name}, command.Args...), " "))
	r.observed = r.observed || observer != nil
	if r.runFailed != nil {
		return execution.ProcessResult{}, r.runFailed
	}
	status := execution.ProcessSucceeded
	if r.failed {
		status = execution.ProcessFailed
	}
	return execution.ProcessResult{Status: status, Stdout: r.stdout, Stderr: r.stderr}, nil
}

// The keychain is asked for this product's own items, under the account the
// setup document stores them beside, and the command that produces the value is
// given nothing to log it with.
func TestTheKeychainIsAskedForThisProductsItemAndNeverObserved(t *testing.T) {
	t.Parallel()

	runner := &keychainRunner{stdout: botToken + "\n"}
	keychain := Keychain{Runner: runner, Timeout: time.Second}

	token, err := keychain.Secret(context.Background(), BotSecret("yoyodyne"))
	if err != nil {
		t.Fatalf("Secret() error = %v", err)
	}
	if token != botToken {
		t.Fatalf("Secret() = %q, want the stored value with the keychain's newline off", token)
	}
	want := "security find-generic-password -s yoyo-slack-bot.yoyodyne -a " + KeychainAccount + " -w"
	if !slices.Equal(runner.asked, []string{want}) {
		t.Fatalf("asked %q, want %q", runner.asked, []string{want})
	}
	if runner.observed {
		t.Fatal("the command that produces a token was given an observer to log it with")
	}
}

// A keychain that will not answer is not a keychain that has nothing: a refused
// item and an item nobody stored are both the operator's to settle, so both
// read as unavailable and what the keychain said is carried through.
func TestAKeychainRefusalSaysWhatTheKeychainSaid(t *testing.T) {
	t.Parallel()

	runner := &keychainRunner{failed: true, stderr: "security: User interaction is not allowed.\n"}
	_, err := Keychain{Runner: runner}.Secret(context.Background(), BotSecret("yoyodyne"))
	if !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("Secret() error = %v, want it reported as unavailable", err)
	}
	if !strings.Contains(err.Error(), "User interaction is not allowed") {
		t.Fatalf("Secret() error = %v, want it to carry what the keychain said", err)
	}
}

// An item stored with no value would otherwise start a sink that authenticates
// with an empty token, which fails in Slack rather than here.
func TestAnEmptyStoredValueIsNotAToken(t *testing.T) {
	t.Parallel()

	_, err := Keychain{Runner: &keychainRunner{stdout: "\n"}}.Secret(context.Background(), AppSecret("yoyodyne"))
	if !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("Secret() error = %v, want it reported as unavailable", err)
	}
}

// The sink has to still be reporting after the pass that started it has exited,
// so this starts a real process and reads back what it wrote: the environment it
// was constructed with, in the log beside that product's state.
func TestAStartedSinkOutlivesThePassAndWritesToItsOwnLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the launcher is exercised on the Unix hosts Yoyodyne supports")
	}
	t.Parallel()

	root := t.TempDir()
	relative := "projects/yoyodyne/state/slack/" + sinkLogFile
	log := filepath.Join(root, filepath.FromSlash(relative))
	pid, err := DetachedLauncher{}.Launch(Launch{
		Program: "/bin/sh",
		Args:    []string{"-c", "printenv " + SecretNamespaceVariable},
		Dir:     t.TempDir(),
		Env:     Environment(nil, "yoyodyne", botToken, appToken),
		LogRoot: root,
		Log:     relative,
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if pid <= 0 {
		t.Fatalf("Launch() = %d, want the process it started", pid)
	}

	if got := startedSinkWrote(t, pid, log); got != "yoyodyne" {
		t.Fatalf("the started process recorded %q, want the namespace it was launched with", got)
	}
	if info, err := os.Stat(log); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the sink log is mode %v (%v), want it readable only by its owner", info.Mode().Perm(), err)
	}
}

// The sink outlives the pass that started it and everything it runs inherits
// the environment it was started with, so a sink outside the Git maintenance
// fence is a long-lived source of exactly the prune that fence exists to stop —
// one that deletes a worktree registration another run is still creating.
func TestAStartedSinkCarriesTheGitMaintenanceFence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the launcher is exercised on the Unix hosts Yoyodyne supports")
	}
	t.Parallel()

	root := t.TempDir()
	relative := "projects/yoyodyne/state/slack/" + sinkLogFile
	// The constructed environment carries no Git configuration of its own, so
	// the fence is the whole of what the sink was given: two settings, first.
	pid, err := DetachedLauncher{}.Launch(Launch{
		Program: "/bin/sh",
		Args:    []string{"-c", "printenv GIT_CONFIG_COUNT; printenv GIT_CONFIG_KEY_0; printenv GIT_CONFIG_KEY_1"},
		Dir:     t.TempDir(),
		Env:     Environment(nil, "yoyodyne", botToken, appToken),
		LogRoot: root,
		Log:     relative,
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}

	want := "2\ngc.auto\nmaintenance.auto"
	if got := startedSinkWrote(t, pid, filepath.Join(root, filepath.FromSlash(relative))); got != want {
		t.Fatalf("the started process recorded %q, want %q", got, want)
	}
}

// startedSinkWrote reads back what a launched sink said, once it has said all
// of it: the launcher returns without the process having run, which is the
// point of it, and a log read while it is still writing is a partial answer
// rather than a wrong one. The process is waited for rather than the log
// polled against a clock — it is this process's child, in a session of its own
// but reaped here all the same — so what is read is what it wrote by the time
// it exited, however long a loaded machine took to run it.
func startedSinkWrote(t *testing.T, pid int, log string) string {
	t.Helper()

	started, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess(%d) error = %v", pid, err)
	}
	if _, err := started.Wait(); err != nil {
		t.Fatalf("Wait() on the started process error = %v", err)
	}
	written, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", log, err)
	}
	return strings.TrimSpace(string(written))
}

// A sink's log is a write like any other the harness owns, and the one thing
// that makes it different is that the bytes keep coming for as long as the sink
// runs: a symlink anywhere along the path would send every line of what the
// harness is doing outside the root it was told to stay in, and nothing would
// report it. So the launcher is held to the same topology matrix as every other
// confined writer rather than to a test of its own.
func TestTheSinkLogIsConfinedToTheStateRootItWasGiven(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the launcher is exercised on the Unix hosts Yoyodyne supports")
	}

	writertest.Run(t, writertest.Writer{
		Name:      "the Slack sink launcher",
		Directory: "projects/yoyodyne/state/slack",
		File:      sinkLogFile,
		Write: func(_ *testing.T, root string) error {
			// A process that exits immediately: what is under test is where its
			// output was pointed, not what it wrote.
			_, err := DetachedLauncher{}.Launch(Launch{
				Program: "/bin/sh",
				Args:    []string{"-c", ":"},
				Env:     Environment(nil, "yoyodyne", botToken, appToken),
				LogRoot: root,
				Log:     "projects/yoyodyne/state/slack/" + sinkLogFile,
			})
			return err
		},
	})
}

// A supervisor assembled without what it needs says so rather than starting
// something with half an arrangement.
func TestSupervisionRefusesWithoutTheProductsOwnState(t *testing.T) {
	t.Parallel()

	if _, err := (Supervisor{Secrets: storedSecrets{}, Launcher: &recordingLauncher{}}).Ensure(context.Background()); err == nil {
		t.Fatal("Ensure() with no store started work against a product it could not name")
	}
	store, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := (Supervisor{Store: store}).Ensure(context.Background()); err == nil {
		t.Fatal("Ensure() with nothing to read secrets from or start a sink with did not say so")
	}
}
