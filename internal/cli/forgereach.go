package cli

// Whether `yoyo start` may start a supervisor from the shell it was run in.
//
// On macOS, a shell inside an AI session's sandbox cannot read the system
// certificate store, so every GitHub connection made from it fails with an
// x509 OSStatus error. A supervisor started there inherits that, and could never
// notice a pull request merged: its forge check would fail on every look. So
// `yoyo start` asks the same question first, and where the answer is that
// failure it starts the supervisor through the launch agent, which launchd runs
// outside the calling shell, or refuses.
//
// Two answers lead there. The local check (internal/forgetrust) verifies a
// certificate it makes itself and reaches no network; a shell that fails it is
// handled whatever the product publishes, since every part would inherit it. The
// network probe is asked only of a product whose supervisor checks the forge,
// and only its certificate store failure counts.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/forgecheck"
)

// reachGitHub asks the same question the forge check would, from this process,
// before anything is started from here: can a connection to GitHub be made? Any
// HTTP response at all is success, because the answer wanted is whether the
// transport works, so only a transport error is returned. It goes through the
// default transport, which takes its proxy from the environment as the forge
// check's own tools do.
var reachGitHub = func(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://api.github.com", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

// forgeChecked reports whether the supervisor of a product with this
// configuration checks the forge: it publishes automatically, so it has pull
// requests to wait on, or it sets an upstream branch, whose landed changes the
// check finds. Only such a product has a reason to reach GitHub.
func forgeChecked(cfg config.Config) bool {
	return cfg.Approvals.Publishing == domain.ApprovalAutomatic || strings.TrimSpace(cfg.Execution.UpstreamBranch) != ""
}

// outsideShellSentence is said when the supervisor was started through the
// launch agent instead of from this shell, with why.
func outsideShellSentence(cause string) string {
	return "the supervisor was started through the launch agent because " + cause
}

// storeFailureCause is why, when a GitHub connection from this shell failed on
// the system certificate store.
const storeFailureCause = "a GitHub connection from this shell fails on the system certificate store"

// unverifiableCause is why, when this process cannot verify any certificate.
const unverifiableCause = "this shell cannot verify GitHub's certificate"

// setupLaunchAgentCommand is the one command that fixes a start refused for
// want of a launch agent: `yoyo setup --launch-agent` installs the agent and
// starts the supervisor through it.
func setupLaunchAgentCommand(configPath string) string {
	return "yoyo setup --launch-agent --directory " + shellQuote(config.ProjectDirectory(configPath))
}

// startRefusal is the text of a refused start. problem says what was found:
// the certificate store failure from a GitHub connection, or the local check's
// error. bootstrapProblem is non-empty where a launch agent is installed but
// loading it from this shell failed too, and bootstrapCommand the command that
// loads it from an ordinary terminal.
func startRefusal(configPath, problem, bootstrapProblem, bootstrapCommand string) string {
	text := fmt.Sprintf("start refused: %s, which is what happens inside an AI session's sandbox. A supervisor started from here would pass that on to the scheduler and every other part, and none of them could check GitHub for merged pull requests, so pull requests merged there would never be noticed", problem)
	if bootstrapProblem != "" {
		return text + fmt.Sprintf(", and loading the installed launch agent from here failed too (%s). From an ordinary terminal, run: %s", bootstrapProblem, bootstrapCommand)
	}
	return text + ", and no launch agent is installed to start it outside this shell. From an ordinary terminal, run: " + setupLaunchAgentCommand(configPath)
}

// storeFailureProblem is the refusal's account of a GitHub connection that
// failed on the system certificate store, with the line that says so.
func storeFailureProblem(matched string) string {
	return fmt.Sprintf("a GitHub connection from this shell fails on the system certificate store (%s)", matched)
}

// forgeUnreachable reports why a supervisor started from this shell could not
// reach GitHub, as the refusal's problem and the started line's cause, or false
// where nothing found says so.
func (p *product) forgeUnreachable(ctx context.Context) (problem, cause string, found bool) {
	if p.verifies != nil {
		if err := p.verifies(); err != nil {
			return strings.TrimSuffix(err.Error(), "."), unverifiableCause, true
		}
	}
	if p.reachForge == nil || !forgeChecked(p.resolved.Config) {
		return "", "", false
	}
	probe := p.reachForge(ctx)
	if probe == nil {
		return "", "", false
	}
	matched, certificate := forgecheck.CertificateStoreFailure(probe.Error())
	if !certificate {
		return "", "", false
	}
	return storeFailureProblem(matched), storeFailureCause, true
}

// startUnreachableForge decides what start does when the forge cannot be
// reached from this shell, and reports whether it handled the start. A probe
// that fails for any reason other than the certificate store (no network, a
// proxy refusing, DNS) changes nothing and start proceeds as it always did: a
// supervisor in a shell with no network is a different problem, and refusing on
// it would stop a start on any offline laptop.
func (p *product) startUnreachableForge(ctx context.Context, stdout, stderr io.Writer, jsonOutput bool, logRoot, logPath, retired, retireProblem string) (int, bool) {
	problem, cause, found := p.forgeUnreachable(ctx)
	if !found {
		return 0, false
	}
	refuse := func(text string) (int, bool) {
		if jsonOutput {
			writeJSON(stdout, stderr, startReport{Product: p.resolved.Config.Product.ID, Refused: text, Retired: retired, RetireProblem: retireProblem})
		} else {
			fmt.Fprintln(stderr, text)
		}
		return 1, true
	}
	if p.agent == nil {
		return refuse(startRefusal(p.resolved.Path, problem, "", ""))
	}
	if runs, err := p.agent.Runs(p.resolved.Path); err != nil || !runs {
		return refuse(startRefusal(p.resolved.Path, problem, "", ""))
	}
	asked := p.now()
	if err := p.agent.Bootstrap(ctx); err != nil {
		return refuse(startRefusal(p.resolved.Path, problem, err.Error(), p.agent.BootstrapCommand()))
	}
	report := startReport{
		Product:       p.resolved.Config.Product.ID,
		Started:       true,
		Through:       p.agent.Label,
		Log:           filepath.Join(logRoot, filepath.FromSlash(logPath)),
		OutsideShell:  outsideShellSentence(cause),
		Retired:       retired,
		RetireProblem: retireProblem,
	}
	return p.reportAgentStart(ctx, stdout, stderr, jsonOutput, asked, report), true
}
