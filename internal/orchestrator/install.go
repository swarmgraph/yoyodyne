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
// A step that does not pass is sorted by whose failure it is (build piece 4,
// runstate.SortInstallFailure). One on dependency files unchanged from the
// run's starting target that have installed on this machine before is the
// network's or the package registry's: it is tried again
// runstate.InstallRetries times, runstate.InstallRetryInterval apart, with the
// wait on the record for every surface to show, and the run is held for the
// development manager once those tries are spent. One on unchanged files that
// have never installed here is the step's or the machine's, and the run is
// held at once. Both holds block the item with the step's output, keep the
// branch and worktree, and spend no repair attempt (installHeld). A failure on
// files the run changed is the change's to repair, and can only happen once the
// steps run again after the developer's turn, which is build piece 3; before
// the developer starts the files are the starting target's by construction.

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
		if err := a.installSorted(ctx, step, a.worktree.Path, cache); err != nil {
			return err
		}
	}
	return nil
}

// installSorted runs one step until it passes or its failure is sorted to an
// ending: tried again where the cause is outside the work and tries are left,
// held for the development manager where they are spent or the step has never
// worked here, and handed back as a failure where the run changed its inputs.
func (a *activeRun) installSorted(ctx context.Context, step config.InstallStep, directory, cache string) error {
	p := a.pipeline
	for retry := 0; ; retry++ {
		record, err := a.install(ctx, step, directory, cache)
		if err != nil {
			return err
		}
		if record.Result == runstate.InstallPassed {
			if err := p.recordInstalled(record.Name, record.InputDigest); err != nil {
				return fmt.Errorf("record that install step %q installed on this machine: %w", step.Name, err)
			}
			return a.setInstallWait(ctx, nil)
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		installedBefore, err := p.installedBefore(record.Name, record.InputDigest)
		if err != nil {
			return fmt.Errorf("read whether install step %q has installed on this machine before: %w", step.Name, err)
		}
		switch runstate.SortInstallFailure(record, startingInstallDigest(record, a.state.Install), installedBefore) {
		case runstate.InstallCauseChange:
			return installFailure{record: record}
		case runstate.InstallCauseNeverInstalled:
			return a.blockOnInstall(installHeld{cause: runstate.CauseInstallNeverWorked, record: record, tries: retry + 1})
		}
		if retry == runstate.InstallRetries {
			return a.blockOnInstall(installHeld{cause: runstate.CauseInstallUnavailable, record: record, tries: retry + 1})
		}
		if err := a.waitToInstallAgain(ctx, runstate.InstallWait{Name: step.Name, Retry: retry + 1, NextAt: p.clock().Now().Add(runstate.InstallRetryInterval)}); err != nil {
			return err
		}
	}
}

// startingInstallDigest is the digest the step's inputs had when this run first
// ran it in the checkout record ran in: before its developer started, for the
// run's own worktree, which is the starting target's. A step with no earlier
// run there is on the inputs it started from.
func startingInstallDigest(record runstate.InstallRun, history []runstate.InstallRun) string {
	for _, recorded := range history {
		if recorded.Name == record.Name && recorded.Directory == record.Directory {
			return recorded.InputDigest
		}
	}
	return record.InputDigest
}

// installWaitSlice is how long the wait before another try sleeps between
// readings of whether the operator asked the run to stop, so a stop asked
// during the wait is acted on within it.
const installWaitSlice = releaseCheckInterval

// waitToInstallAgain records the wait on the run and sleeps until wait.NextAt.
// The wait stays on the record until the next try passes or the run is held,
// so a surface reading the run between tries says when the next one is.
func (a *activeRun) waitToInstallAgain(ctx context.Context, wait runstate.InstallWait) error {
	p := a.pipeline
	if err := a.setInstallWait(ctx, &wait); err != nil {
		return err
	}
	for {
		remaining := wait.NextAt.Sub(p.clock().Now())
		if remaining <= 0 {
			return nil
		}
		err := p.sleep(ctx, min(remaining, installWaitSlice))
		if err == nil {
			err = a.stopRequested()
		}
		if err != nil {
			// A wait cut short is not a wait any more, so the record the ending
			// is written onto does not say the run is still waiting; one that
			// could not take that is cleared on this process's copy.
			if clearErr := a.setInstallWait(ctx, nil); clearErr != nil {
				a.state.InstallWait = nil
			}
			return err
		}
	}
}

// setInstallWait puts wait on the run's record, or takes the wait there off it
// where wait is nil, and takes the record as stored as the run's.
func (a *activeRun) setInstallWait(ctx context.Context, wait *runstate.InstallWait) error {
	if wait == nil && a.state.InstallWait == nil {
		return nil
	}
	written, err := a.pipeline.Store.Update(context.WithoutCancel(ctx), a.state.RunID, runstate.InstallWaitRecorded{Wait: wait, At: a.pipeline.clock().Now()})
	if err != nil {
		return fmt.Errorf("record the wait to try an install step again: %w", err)
	}
	a.state = written
	return nil
}

// installedEntry is one line of the product's record of what has installed on
// this machine (home.InstalledFileWithin): a step's name and the digest of the
// inputs it installed.
type installedEntry struct {
	Name        string `json:"name"`
	InputDigest string `json:"input_digest"`
}

// installedBefore reports whether the step named name has ever installed inputs
// with digest on this machine, read from the product's record of it. A record
// not yet written is a machine nothing has installed on.
func (p Pipeline) installedBefore(name, digest string) (bool, error) {
	if strings.TrimSpace(p.StateRoot) == "" {
		return false, nil
	}
	root, err := repowrite.NewRoot(p.StateRoot)
	if err != nil {
		return false, err
	}
	file, err := root.Resolve(home.InstalledFileWithin(root.Path(), string(p.Config.Product.ID)))
	if err != nil {
		return false, err
	}
	content, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		var entry installedEntry
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Name == name && entry.InputDigest == digest {
			return true, nil
		}
	}
	return false, nil
}

// recordInstalled adds a step that passed, and the digest of the inputs it
// installed, to the product's record of what has installed on this machine,
// through the confined write primitive rooted at the state directory. A pair
// already there is not added again.
func (p Pipeline) recordInstalled(name, digest string) error {
	if strings.TrimSpace(p.StateRoot) == "" {
		return nil
	}
	if known, err := p.installedBefore(name, digest); err != nil || known {
		return err
	}
	root, err := repowrite.NewRoot(p.StateRoot)
	if err != nil {
		return err
	}
	line, err := json.Marshal(installedEntry{Name: name, InputDigest: digest})
	if err != nil {
		return err
	}
	file, err := root.OpenAppend(home.InstalledFileWithin(root.Path(), string(p.Config.Product.ID)), 0o600, 0o700)
	if err != nil {
		return err
	}
	_, err = file.Write(append(line, '\n'))
	return errors.Join(err, file.Close())
}

// installHeld is an install step whose failure is not the change's and that
// the harness will not try again: the run is held for the development manager.
type installHeld struct {
	cause  runstate.EnvironmentalCause
	record runstate.InstallRun
	// tries is how many times the step ran on these inputs in this run.
	tries int
}

func (h installHeld) Error() string {
	var message string
	if h.cause == runstate.CauseInstallNeverWorked {
		message = fmt.Sprintf("the install step %q failed on dependency files that have never installed on this machine, so the step or the machine needs fixing rather than the change; the run is held for the development manager before any developer turn: %s", h.record.Name, h.record.Describe())
	} else {
		message = fmt.Sprintf("the install step %q failed %s on dependency files that installed on this machine before, %s apart, most likely because the network or the package registry could not be reached; the run is held for the development manager with its work kept: %s", h.record.Name, timesSaid(h.tries), runstate.InstallRetryInterval, h.record.Describe())
	}
	if output := strings.TrimSpace(h.record.Output); output != "" {
		message += "; it ended on:\n" + lastLines(output, 20)
	}
	return message
}

// timesSaid is a count of tries in words.
func timesSaid(count int) string {
	switch count {
	case 1:
		return "once"
	case 2:
		return "twice"
	default:
		return fmt.Sprintf("%d times", count)
	}
}

// blockOnInstall holds a run whose install step failed for a cause outside the
// change. The cause is recorded as the machine's before the item is blocked,
// so the round is given back where it settles and no repair attempt is spent;
// the blocker names the step, which case it is, and what it printed, which is
// what the development manager's docket entry carries; and the branch and
// worktree are kept for whatever she decides.
func (a *activeRun) blockOnInstall(held installHeld) error {
	// The run is held rather than waiting, so the wait is taken off the record
	// the ending is written onto, before anything else is put on this process's
	// copy of it; a record that could not take it is ended all the same, with
	// the wait cleared on that copy.
	if err := a.setInstallWait(context.Background(), nil); err != nil {
		a.state.InstallWait = nil
	}
	a.recordEnvironmentalRefusal(held.cause, held.Error(), nothingRan)
	class := held.cause.StopClass()
	if err := a.block(renderInstallHeldNotes(a.outcome, held)); err != nil {
		return stoppedBy(class, withFailedRecord(held, fmt.Errorf("record the install step that could not install as a blocker: %w", err)))
	}
	return stoppedBy(class, held)
}

// renderInstallHeldNotes describes a run held because one of the project's
// install steps could not install its dependencies.
func renderInstallHeldNotes(outcome Outcome, held installHeld) string {
	lines := []string{"Yoyodyne held this item for the development manager: " + held.record.Describe() + "."}
	if held.cause == runstate.CauseInstallNeverWorked {
		lines = append(lines,
			"The dependency files are the ones the run started from, and no run on this machine has ever installed them, so the step as the project declares it, or the machine, needs fixing rather than the change.",
			"It was held before any developer turn. Nothing was handed back to the developer and no repair attempt was spent.",
			"Changing the project's install step is the operator's to write; what the machine lacks may be something only a person can supply.")
	} else {
		lines = append(lines,
			fmt.Sprintf("The dependency files are the ones the run started from and have installed on this machine before, so the cause is outside the change, most likely the network or the package registry. The harness tried the step %s, %s apart, and every try failed.", timesSaid(held.tries), runstate.InstallRetryInterval),
			"Nothing was handed back to the developer and no repair attempt was spent.")
	}
	lines = append(lines,
		"Run: "+outcome.RunID,
		"Branch: "+outcome.Branch,
		"Worktree: "+outcome.WorktreePath,
		"The branch and worktree are preserved. Once the step can install, the item can be run again.")
	if output := strings.TrimSpace(held.record.Output); output != "" {
		lines = append(lines, "The step ended on:", lastLines(output, 20))
	}
	return strings.Join(lines, "\n")
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
