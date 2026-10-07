// Package launchd is the product's launch agent on macOS: the per-user job
// that starts the supervisor with the machine and starts it again if it dies.
//
// The agent runs the supervisor verb and nothing else — `yoyo start
// --foreground`, from the binary that installed the agent, reading the
// configuration it was installed for — so what starts with the machine is the
// resident, and through it every part the services section enables. Nothing
// here is a script: the operator's interim maintenance job ran as one, and its
// history is in the two settings this plist is careful about.
//
// AbandonProcessGroup is true. The children the supervisor starts are sessions
// of their own, but the job's process group is what launchd tears down when
// the job exits, and on 2026-09-03 a job without this setting killed every
// watch session and Slack service it had started at the end of each of its
// passes — 77 kill-restart cycles before anybody read the log. The supervisor
// exits on `yoyo stop` and on a crash, and its children are meant to survive
// both, to be reattached by the next one.
//
// KeepAlive is conditional on an unsuccessful exit rather than unconditional.
// `yoyo stop` asks the supervisor to stop and it exits cleanly; a job launchd
// restarted on every exit would start it again ten seconds later, and the verb
// that stops the product would not. A crash exits otherwise, and launchd starts
// the job again; a machine restart starts it because RunAtLoad is set. A
// supervisor refused because another already holds the product's lease exits
// cleanly too, for the same reason: a refusal read as a failure would be
// restarted every throttle interval for as long as the other supervisor ran.
package launchd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// labelPrefix is what every agent's label starts with; the product's id
// follows, so two products on one machine are two jobs. "supervisor" is in it
// so no product's id can name the operator's retired maintenance job,
// com.yoyodyne.maintenance, whose label the doctor and the supervisor look for.
const labelPrefix = "com.yoyodyne.supervisor."

// agentsDirectory is where launchd reads a user's agents from, under the home
// directory.
const agentsDirectory = "Library/LaunchAgents"

// commandTimeout bounds one launchctl invocation, which answers in well under a
// second when it answers at all.
const commandTimeout = 30 * time.Second

// Label is the job's name for a product.
func Label(product domain.ProductID) string {
	return labelPrefix + string(product)
}

// Spec is what the agent runs and where it says what it did.
type Spec struct {
	Label string
	// Program and Args are the supervisor verb from the installing binary.
	Program string
	Args    []string
	// WorkingDirectory is the project directory, so a relative repository in
	// the configuration resolves as it does from a terminal there.
	WorkingDirectory string
	// Log is where the supervisor's standard output and error go, which is the
	// supervisor log `yoyo start` names.
	Log string
	// Environment is what the job is given. launchd's own environment is a
	// bare PATH, so the operator's is carried across for the tools every part
	// needs: git, bd, the provider, make.
	Environment map[string]string
}

// Render writes the spec as a property list.
func Render(spec Spec) []byte {
	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	out.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	out.WriteString("<plist version=\"1.0\">\n<dict>\n")
	key(&out, "Label")
	str(&out, spec.Label, 1)
	key(&out, "ProgramArguments")
	out.WriteString("  <array>\n")
	str(&out, spec.Program, 2)
	for _, arg := range spec.Args {
		str(&out, arg, 2)
	}
	out.WriteString("  </array>\n")
	if spec.WorkingDirectory != "" {
		key(&out, "WorkingDirectory")
		str(&out, spec.WorkingDirectory, 1)
	}
	key(&out, "RunAtLoad")
	out.WriteString("  <true/>\n")
	key(&out, "KeepAlive")
	out.WriteString("  <dict>\n    <key>SuccessfulExit</key>\n    <false/>\n  </dict>\n")
	key(&out, "AbandonProcessGroup")
	out.WriteString("  <true/>\n")
	if spec.Log != "" {
		key(&out, "StandardOutPath")
		str(&out, spec.Log, 1)
		key(&out, "StandardErrorPath")
		str(&out, spec.Log, 1)
	}
	if len(spec.Environment) > 0 {
		key(&out, "EnvironmentVariables")
		out.WriteString("  <dict>\n")
		names := make([]string, 0, len(spec.Environment))
		for name := range spec.Environment {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			out.WriteString("    <key>" + escape(name) + "</key>\n")
			str(&out, spec.Environment[name], 2)
		}
		out.WriteString("  </dict>\n")
	}
	out.WriteString("</dict>\n</plist>\n")
	return out.Bytes()
}

func key(out *bytes.Buffer, name string) {
	out.WriteString("  <key>" + escape(name) + "</key>\n")
}

func str(out *bytes.Buffer, value string, depth int) {
	out.WriteString(strings.Repeat("  ", depth) + "<string>" + escape(value) + "</string>\n")
}

func escape(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

// Environment picks out of a process environment what the job is given: the
// PATH, the state home overrides where the operator set one, so the job reads
// the same state the operator's own commands do, and XDG_CONFIG_HOME, which the
// tools the job runs read their own settings by. Nothing else is carried, a Slack token least of
// all.
func Environment(environ []string) map[string]string {
	carried := map[string]string{}
	for _, entry := range environ {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		switch name {
		case "PATH", "YOYODYNE_STATE_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME":
			carried[name] = value
		}
	}
	return carried
}

// pathEntry is the PATH the plist carries, which Same leaves out of the
// comparison.
var pathEntry = regexp.MustCompile(`\s*<key>PATH</key>\s*<string>[^<]*</string>`)

// Same reports whether an installed property list is the one wanted, apart from
// the PATH each carries. The PATH is the installing shell's, and a walk run from
// another shell — an agent session, a script — has another one; replacing a
// working agent over that alone would restart the supervisor and could leave it
// without the tools its parts need at the next login. So a PATH-only difference
// is the agent already installed, and replacing its PATH is removing the agent
// and installing it again from the shell whose PATH it should carry.
func Same(installed, wanted []byte) bool {
	return bytes.Equal(pathEntry.ReplaceAll(installed, nil), pathEntry.ReplaceAll(wanted, nil))
}

// Controller is how the agent is read and driven: launchctl, run as this user.
type Controller struct {
	Runner      execution.ProcessRunner
	UserHomeDir func() (string, error)
	Getuid      func() int
}

// Agent is one product's launch agent as this machine holds it: its label,
// and the file launchd reads it from.
type Agent struct {
	Label string
	// Path is the property list under the user's launch agents directory, and
	// empty where the home directory could not be resolved.
	Path string
	// home is the user's home directory, which the property list is written
	// inside: the write is confined to it, launch agents directory included.
	home       string
	controller Controller
}

// AgentFor names a product's agent on this machine.
func AgentFor(product domain.ProductID, controller Controller) *Agent {
	agent := &Agent{Label: Label(product), controller: controller}
	if controller.UserHomeDir != nil {
		if home, err := controller.UserHomeDir(); err == nil && home != "" {
			agent.home = home
			agent.Path = filepath.Join(home, filepath.FromSlash(agentsDirectory), agent.Label+".plist")
		}
	}
	return agent
}

// Installed reads the property list as it is on disk, reporting whether one is
// there.
func (a *Agent) Installed() ([]byte, bool, error) {
	if a.Path == "" {
		return nil, false, errors.New("the home directory could not be resolved, so where the launch agent would be is not known")
	}
	content, err := os.ReadFile(a.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return content, true, nil
}

// Runs reports whether the installed agent runs the supervisor for the given
// configuration file: a product id names one job per user, and a second
// checkout of the same product on one machine is a job for the other
// checkout, which `yoyo start` here must neither start nor read as its own.
func (a *Agent) Runs(configPath string) (bool, error) {
	content, found, err := a.Installed()
	if err != nil || !found {
		return false, err
	}
	return bytes.Contains(content, []byte("<string>--config</string>\n    <string>"+escape(configPath)+"</string>")), nil
}

// Install writes the property list, confined to the user's launch agents
// directory: a path string proves nothing about where a write lands, and this
// one is under the home directory where a symlink is ordinary.
func (a *Agent) Install(content []byte) error {
	if a.Path == "" {
		return errors.New("the home directory could not be resolved, so the launch agent has nowhere to be written")
	}
	// Confined to the home directory rather than to the launch agents directory,
	// so creating that directory where it does not exist yet is part of the same
	// confined write: every existing component is resolved before anything is
	// made, and one that points out of the home is refused rather than followed.
	root, err := repowrite.NewRoot(a.home)
	if err != nil {
		return err
	}
	if _, err := root.WriteFile(agentsDirectory+"/"+a.Label+".plist", content); err != nil {
		return err
	}
	return nil
}

// Loaded reports whether launchd holds the job, whether or not it is running.
func (a *Agent) Loaded(ctx context.Context) (bool, error) {
	result, err := a.launchctl(ctx, "print", a.target())
	if err != nil {
		return false, err
	}
	return result.Status == execution.ProcessSucceeded, nil
}

// Bootstrap loads the job, which starts it because RunAtLoad is set.
func (a *Agent) Bootstrap(ctx context.Context) error {
	if a.Path == "" {
		return errors.New("the launch agent has no property list to load")
	}
	return a.expectSuccess(ctx, "bootstrap", a.domain(), a.Path)
}

// Bootout unloads the job, stopping the supervisor it runs; the supervisor's
// children survive it, by the plist's setting and by design.
func (a *Agent) Bootout(ctx context.Context) error {
	return a.expectSuccess(ctx, "bootout", a.target())
}

// Kickstart starts the job's process if it is not running, and does nothing
// to one that is.
func (a *Agent) Kickstart(ctx context.Context) error {
	return a.expectSuccess(ctx, "kickstart", a.target())
}

// BootstrapCommand and UninstallCommand are the two things an operator types
// about the agent, for a remedy and for the documentation.
func (a *Agent) BootstrapCommand() string {
	return fmt.Sprintf("launchctl bootstrap gui/$(id -u) %s", shellQuote(a.Path))
}

func (a *Agent) UninstallCommand() string {
	return fmt.Sprintf("launchctl bootout gui/$(id -u)/%s; rm %s", a.Label, shellQuote(a.Path))
}

func (a *Agent) domain() string {
	uid := 0
	if a.controller.Getuid != nil {
		uid = a.controller.Getuid()
	}
	return "gui/" + strconv.Itoa(uid)
}

func (a *Agent) target() string { return a.domain() + "/" + a.Label }

func (a *Agent) expectSuccess(ctx context.Context, args ...string) error {
	result, err := a.launchctl(ctx, args...)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		if message == "" {
			message = string(result.Status)
		}
		return fmt.Errorf("launchctl %s: %s (exit %d)", strings.Join(args, " "), message, result.ExitCode)
	}
	return nil
}

func (a *Agent) launchctl(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	if a.controller.Runner == nil {
		return execution.ProcessResult{}, errors.New("nothing is wired to run launchctl")
	}
	return a.controller.Runner.Run(ctx, execution.Command{Name: "launchctl", Args: args, Timeout: commandTimeout}, nil)
}

// shellQuote makes a path safe to paste, the way the setup remedies are.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\"'\\$`*?[]{}()|&;<>#~!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
