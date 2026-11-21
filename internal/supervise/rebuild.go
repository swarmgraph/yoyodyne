package supervise

// Rebuilding the product's own binary when its target branch lands.
//
// A harness developing itself runs from a binary built out of the checkout it
// is developing, so a landing is not running anywhere until somebody builds it.
// That somebody was the operator's maintenance launchd job, which ran
// `make build` whenever the target branch had moved past the binary — outside
// the product, and on its own clock, so every redeploy started from something
// the product did not know it had done. The supervisor now retires that job as
// it is installed (internal/maintenancejob), and this is the one duty of it the
// product still needs: the supervisor looks, on its own poll, at where the
// checkout's branch stands and at the revision the binary was built from, and
// builds when a landing touched what the binary is made of. What takes the
// build up is deploy.go: the supervisor moves every part it hosts onto the
// binary, and the watch re-executes itself into it between runs
// (internal/redeploy).
//
// The bounce-when-idle step the job also had is deliberately not carried. It
// compared a process's start time with the binary's, which is wrong for a watch
// that re-executes itself in place and keeps its start time, and it killed the
// watch 32 times on 2026-09-26.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Resident is work the supervisor hosts beside its children: looked at on the
// supervisor's own poll rather than run as a process of its own.
type Resident interface {
	Name() string
	// Look does whatever the resident is due to do at now. It is called on every
	// tick, and paces itself.
	Look(ctx context.Context, now time.Time)
}

const (
	// DefaultRebuildEvery is how often the rebuilder asks where the branch
	// stands. A landing waits at most this long to be built.
	DefaultRebuildEvery = 30 * time.Second
	// DefaultRebuildTimeout bounds one build.
	DefaultRebuildTimeout = 10 * time.Minute
	// rebuildGitTimeout bounds each Git question, which answers at once.
	rebuildGitTimeout = 30 * time.Second
)

// RebuildSources are the paths a landing has to touch for the binary to be
// built again: what `go build ./cmd/yoyo` compiles. A landing that touches
// only documentation or the tracker's export builds nothing, because a build
// that changes nothing is still a redeploy of every part running from it. The
// Slack sink counts how far a session is behind by the same paths, so the two
// never disagree about which landings change the build.
var RebuildSources = []string{"cmd", "internal", "go.mod", "go.sum"}

// Rebuilder builds the product's binary again when the checkout's branch lands
// something the binary is made of.
type Rebuilder struct {
	// Checkout is the primary checkout the binary is built from, and Binary the
	// file inside it the product runs from.
	Checkout string
	Binary   string
	// Runner runs git and the build.
	Runner execution.ProcessRunner
	// Environ is what the build runs with.
	Environ []string
	// Every is how often to look, and Timeout how long one build may take; zero
	// takes the defaults.
	Every   time.Duration
	Timeout time.Duration
	// BuiltFrom reads the revision a binary was built from; nil reads Go's own
	// stamp out of the file.
	BuiltFrom func(path string) (string, error)
	// Log is where what the rebuilder does is said.
	Log func(format string, args ...any)

	lastLook   time.Time
	considered string
	lastSaid   string
	// standing is what the rebuilder's latest look came to, and failed whether
	// it was a build or a reading that did not work. They are what the periodic
	// pass records as its rebuild step.
	standing string
	failed   bool
}

// Standing is what the rebuilder's latest look came to, and whether it failed.
// It is empty before the first look.
func (r *Rebuilder) Standing() (string, bool) { return r.standing, r.failed }

// NewRebuilder is the rebuilder for a product whose binary lives inside the
// checkout that builds it, and nothing where it does not: a binary installed
// from a release or from the module cache is not built from this checkout, so
// rebuilding the checkout would deploy nothing over it.
func NewRebuilder(checkout, binary string, runner execution.ProcessRunner, environ []string, log func(format string, args ...any)) (*Rebuilder, bool) {
	if checkout == "" || binary == "" || runner == nil {
		return nil, false
	}
	relative, err := filepath.Rel(checkout, binary)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, false
	}
	if info, err := os.Stat(filepath.Join(checkout, "cmd", "yoyo")); err != nil || !info.IsDir() {
		return nil, false
	}
	if _, err := os.Stat(filepath.Join(checkout, "Makefile")); err != nil {
		return nil, false
	}
	return &Rebuilder{Checkout: checkout, Binary: binary, Runner: runner, Environ: environ, Log: log}, true
}

// Name is how the resident is named in what the supervisor says.
func (r *Rebuilder) Name() string { return "rebuild" }

// Look builds the binary again where the checkout's branch has landed
// something the binary is made of since the binary was built.
func (r *Rebuilder) Look(ctx context.Context, now time.Time) {
	every := r.Every
	if every <= 0 {
		every = DefaultRebuildEvery
	}
	if !r.lastLook.IsZero() && now.Before(r.lastLook.Add(every)) {
		return
	}
	r.lastLook = now

	branch, err := r.git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		r.say("not rebuilding %s: the checkout at %s is on no branch, so no landing is checked out there (%v)", r.Binary, r.Checkout, err)
		return
	}
	tip, err := r.git(ctx, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		r.fail("not rebuilding %s: where %s stands could not be read: %v", r.Binary, branch, err)
		return
	}
	if tip == r.considered {
		return
	}
	built, err := r.builtFrom(r.Binary)
	if err == nil && built == tip {
		r.considered = tip
		r.standing, r.failed = fmt.Sprintf("%s is built from %s's tip %s", r.Binary, branch, short(tip)), false
		return
	}
	if err == nil && built != "" {
		changed, diffErr := r.git(ctx, append([]string{"diff", "--name-only", built, tip, "--"}, RebuildSources...)...)
		if diffErr == nil && changed == "" {
			r.considered = tip
			r.say("%s landed at %s touching nothing %s is built from, so it is not rebuilt", branch, short(tip), r.Binary)
			return
		}
	}
	// A build compiles the working tree, not the commit, so uncommitted changes
	// to what the binary is made of would be deployed with the landing. Those are
	// somebody's work in progress, and the build waits for them.
	dirty, err := r.git(ctx, append([]string{"status", "--porcelain", "--"}, RebuildSources...)...)
	if err != nil {
		r.fail("not rebuilding %s: whether the checkout has uncommitted changes could not be read: %v", r.Binary, err)
		return
	}
	if dirty != "" {
		r.say("not rebuilding %s after %s landed at %s: the checkout has uncommitted changes to what it is built from, which a build would deploy: %s", r.Binary, branch, short(tip), oneLine(dirty))
		return
	}
	r.build(ctx, branch, tip, built)
}

func (r *Rebuilder) build(ctx context.Context, branch, tip, built string) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultRebuildTimeout
	}
	result, err := r.Runner.Run(ctx, execution.Command{
		Name:    "make",
		Args:    []string{"build", "BINARY=" + r.Binary},
		Dir:     r.Checkout,
		Env:     r.Environ,
		Timeout: timeout,
	}, nil)
	// A build that failed is not tried again until the branch moves: the same
	// tree fails the same way, and a build every poll is load for nothing.
	r.considered = tip
	if err != nil || result.Status != execution.ProcessSucceeded {
		r.fail("could not rebuild %s after %s landed at %s: %s; it is tried again when %s next moves", r.Binary, branch, short(tip), describeFailure(result, err), branch)
		return
	}
	from := "a build that carries no revision"
	if built != "" {
		from = short(built)
	}
	r.say("rebuilt %s at %s after %s landed there, replacing %s; every part running from it takes the build up", r.Binary, short(tip), branch, from)
}

func (r *Rebuilder) git(ctx context.Context, args ...string) (string, error) {
	result, err := r.Runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    append([]string{"-C", r.Checkout}, args...),
		Timeout: rebuildGitTimeout,
	}, nil)
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), describeFailure(result, nil))
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (r *Rebuilder) builtFrom(path string) (string, error) {
	if r.BuiltFrom != nil {
		return r.BuiltFrom(path)
	}
	return Revision(path)
}

// say logs a line, and the same line once: a rebuilder that cannot build says
// why every poll otherwise.
func (r *Rebuilder) say(format string, args ...any) {
	r.settle(false, format, args...)
}

// fail is say for a look that did not work.
func (r *Rebuilder) fail(format string, args ...any) {
	r.settle(true, format, args...)
}

func (r *Rebuilder) settle(failed bool, format string, args ...any) {
	said := fmt.Sprintf(format, args...)
	r.standing, r.failed = said, failed
	if said == r.lastSaid {
		return
	}
	r.lastSaid = said
	if r.Log != nil {
		r.Log("%s", said)
	}
}

func describeFailure(result execution.ProcessResult, err error) string {
	if err != nil {
		return err.Error()
	}
	said := oneLine(lastLines(strings.TrimSpace(result.Stderr+"\n"+result.Stdout), 3))
	if said == "" {
		return fmt.Sprintf("exited %d", result.ExitCode)
	}
	return fmt.Sprintf("exited %d: %s", result.ExitCode, said)
}

func lastLines(text string, count int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func short(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}
