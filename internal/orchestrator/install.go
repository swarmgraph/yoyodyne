package orchestrator

// A project's declared install steps, run in a run's worktree before its
// developer starts (docs/designs/project-dependencies-in-runs.md, build piece
// 2). The step is run the way a declared check is, through the pipeline's check
// runner (internal/checks/install.go says what that means and what differs), so
// it is the harness that reaches the network and never an agent session. The
// project's package cache folder is kept among the project's records under the
// state directory, created through the confined write primitive, and named only
// to the step's command; no developer sandbox grants it, because each grants its
// own worktree and the repository's Git directory and this folder is in neither.
//
// Running the steps again before a run's later checks and repair rounds, and
// sorting a failure by whose it is, are later pieces of the same design. Until
// they land, a step that does not pass here stops the run before any developer
// turn is spent, with the step's output on the record.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// installBeforeDeveloper runs each declared install step, in the order the
// project declares them, in this run's worktree, and records each on the run.
// A project that declares none runs nothing and records nothing.
func (a *activeRun) installBeforeDeveloper(ctx context.Context) error {
	p := a.pipeline
	if len(p.Config.Install) == 0 {
		return nil
	}
	cache, err := p.installCache()
	if err != nil {
		return err
	}
	for _, step := range p.Config.Install {
		record, err := a.install(ctx, step, a.worktree.Path, cache)
		if err != nil {
			return err
		}
		if record.Result != runstate.InstallPassed {
			return installFailure{record: record}
		}
	}
	return nil
}

// installFailure is a declared install step that did not pass. Its message is
// the whole of what the run's failure note says about it.
type installFailure struct {
	record runstate.InstallRun
}

func (f installFailure) Error() string {
	message := "the project's dependencies were not installed before the developer started: " + f.record.Describe()
	if output := strings.TrimSpace(f.record.Output); output != "" {
		message += "; it ended on:\n" + lastLines(output, 20)
	}
	return message
}

// installCache is the product's package cache folder, created through the
// confined write primitive rooted at the state directory.
func (p Pipeline) installCache() (string, error) {
	if strings.TrimSpace(p.StateRoot) == "" {
		return "", errors.New("the project declares an install step, and this harness was started with no state directory to keep its package cache folder in")
	}
	root, err := repowrite.NewRoot(p.StateRoot)
	if err != nil {
		return "", fmt.Errorf("open the state directory for the install cache: %w", err)
	}
	cache, err := root.MakeDirectory(home.InstallCacheDirectoryWithin(root.Path(), string(p.Config.Product.ID)), 0o700)
	if err != nil {
		return "", fmt.Errorf("create the install cache folder: %w", err)
	}
	return cache, nil
}

// install runs one step once in directory and records it on the run.
func (a *activeRun) install(ctx context.Context, step config.InstallStep, directory, cache string) (runstate.InstallRun, error) {
	p := a.pipeline
	digest, err := installDigest(directory, step.Inputs)
	if err != nil {
		return runstate.InstallRun{}, fmt.Errorf("read the inputs of install step %q: %w", step.Name, err)
	}
	var output []string
	sink := func(event execution.Event) error {
		if event.Type == execution.EventProcessOutput {
			var payload struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(event.Payload, &payload) == nil {
				output = append(output, payload.Text)
				if len(output) > 2*runstate.InstallOutputLines {
					output = append(output[:0], output[len(output)-runstate.InstallOutputLines:]...)
				}
			}
		}
		return a.sink(event)
	}
	started := p.clock().Now()
	result, _, runErr := checks.RunInstallWith(ctx, p.Checks, checks.InstallRequest{
		RunID:        a.state.RunID,
		Directory:    directory,
		Command:      step.Run,
		LastSequence: a.state.LastSequence,
		TimeLimit:    step.Limit(),
		Cache:        cache,
		Permit: checks.PermitRequest{
			WorkItemID: a.state.WorkItemID,
			Commit:     a.state.BaseCommit,
			Priority:   heavyCheckPriority(a.state),
		},
	}, sink)
	finished := p.clock().Now()
	record := runstate.InstallRun{
		Name:                 step.Name,
		Directory:            directory,
		InputDigest:          digest,
		StartedAt:            started,
		DurationMilliseconds: max(finished.Sub(started).Milliseconds(), 0),
		Result:               installResult(result, runErr),
		ExitCode:             result.Process.ExitCode,
		Output:               runstate.InstallOutputTail(output),
	}
	if runErr != nil && strings.TrimSpace(record.Output) == "" {
		record.Output = runstate.InstallOutputTail([]string{runErr.Error()})
	}
	// Written onto the record as stored, and that record taken as the run's, so
	// the run's own later saves are made from a copy in step with it.
	written, err := p.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.InstallRecorded{Install: record, At: finished})
	if err != nil {
		return record, fmt.Errorf("record install step %q: %w", step.Name, err)
	}
	a.state = written
	if runErr != nil && ctx.Err() != nil {
		return record, runErr
	}
	return record, nil
}

func installResult(result checks.Result, err error) runstate.InstallResult {
	switch {
	case err == nil && result.Passed:
		return runstate.InstallPassed
	case result.StoppedByCaller || result.NotLaunched:
		return runstate.InstallStopped
	case result.Process.Status == execution.ProcessTimedOut:
		return runstate.InstallTimedOut
	case err != nil && result.Process.Status == "":
		return runstate.InstallStopped
	default:
		return runstate.InstallFailed
	}
}

// installDigest is the digest of a step's inputs as they stand in directory:
// each input's path and content, in the order the step declares them, read
// without following a link out of the checkout. An input that is missing is
// digested as missing rather than refused, because a change may remove one and
// what the step makes of that is the step's to say.
func installDigest(directory string, inputs []string) (string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	hash := sha256.New()
	for _, input := range inputs {
		name := path.Clean(strings.TrimSpace(input))
		fmt.Fprintf(hash, "%d:%s\n", len(name), name)
		file, err := root.Open(name)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprint(hash, "missing\n")
			continue
		}
		if err != nil {
			return "", err
		}
		content := sha256.New()
		_, err = io.Copy(content, file)
		file.Close()
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		fmt.Fprintf(hash, "%x\n", content.Sum(nil))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// lastLines is the last count lines of text.
func lastLines(text string, count int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}

// installSection is what a developer is told about the project's install
// steps, and nothing for a project that declares none.
func installSection(steps []config.InstallStep) string {
	if len(steps) == 0 {
		return ""
	}
	var section strings.Builder
	section.WriteString("# This project's dependencies\n\n")
	section.WriteString("The project declares the steps below to install its dependencies. The harness ran each of them in your worktree before you started, so what they install is already there, and your checks run against it without reaching the network.\n\n")
	for _, step := range steps {
		fmt.Fprintf(&section, "- %s: installs %s from %s.\n", step.Name, strings.Join(step.Outputs, ", "), strings.Join(step.Inputs, ", "))
	}
	section.WriteString("\nYour session cannot fetch packages itself. If you change one of those input files, the new dependencies are not installed within the same turn: the harness installs them before the checks that judge your change and before your next repair round. Say in your summary that you changed one, and do not try to install packages yourself.\n")
	return section.String()
}

// withInstallSection is a developer prompt with installSection after it.
func withInstallSection(prompt string, steps []config.InstallStep) string {
	section := installSection(steps)
	if section == "" {
		return prompt
	}
	return strings.TrimRight(prompt, "\n") + "\n\n" + section
}
