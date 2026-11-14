package composition

// The gates the ledger's Exercised sentences refer to. Each one is here rather
// than inline in a test because what it holds is a claim about this repository
// that somebody may want to run over another set of files — and because a gate
// written as a test body is one no fixture ever proves works.
//
// None of them is deep. A shell file that parses can still be wrong and a
// workflow with a job and a step can still do the wrong thing. What they buy is
// that a change made entirely of shell, YAML, or JSON cannot reach a reviewer
// with nothing having read it, which is the state yoyodyne-ifd.156 was
// integrated in.

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ShellSyntax parses each file named and reports the ones a shell will not
// accept.
//
// `bash -n` reads a script and does not run it, so this is safe against files
// whose whole purpose is to do something — the release verb tags a repository
// and the getting-started walkthrough builds one. It is deliberately the only thing
// asked of shell that has no suite of its own: parsing is what nothing else in
// this project did, and a syntax error in a script only a release path executes
// would otherwise be found by the release.
func ShellSyntax(root string, paths []string) []Problem {
	var problems []Problem
	for _, path := range paths {
		parse := exec.Command("bash", "-n", filepath.FromSlash(path))
		parse.Dir = root
		report, err := parse.CombinedOutput()
		if err == nil {
			continue
		}
		problems = append(problems, Problem{
			Path:   path,
			Reason: fmt.Sprintf("a shell will not parse it (%v): %s", err, strings.TrimSpace(string(report))),
		})
	}
	return problems
}

// StructuredData decodes each YAML and JSON file named and reports the ones
// that do not decode.
//
// What a file means is the business of whatever reads it, and most of these are
// read by somebody else's tool. What is this repository's business either way is
// that the file is the thing it claims to be: a configuration nothing can parse
// is broken whoever owns its schema, and it is broken in a way no reviewer
// reading a diff reliably sees.
func StructuredData(root string, paths []string) []Problem {
	var problems []Problem
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("it could not be read: %v", err)})
			continue
		}
		var document any
		if Extension(path) == ".json" {
			err = json.Unmarshal(content, &document)
		} else {
			err = yaml.Unmarshal(content, &document)
		}
		if err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("it does not decode: %v", err)})
		}
	}
	return problems
}

// WorkflowDirectory is where a repository's GitHub Actions workflows live.
const WorkflowDirectory = ".github/workflows/"

// Workflows is the workflow files among the paths named.
func Workflows(paths []string) []string {
	var workflows []string
	for _, path := range paths {
		if strings.HasPrefix(path, WorkflowDirectory) {
			workflows = append(workflows, path)
		}
	}
	return workflows
}

// workflowJob is the part of a job that has to be there for the job to do
// anything: something to run it on, and something to run.
type workflowJob struct {
	RunsOn any `yaml:"runs-on"`
	Steps  any `yaml:"steps"`
}

// steps is how many steps the job declares. It is read back out of `any` rather
// than decoded into a typed slice because the claim is only that there are
// some, and a `steps:` written as anything other than a list is a job with none
// of them as surely as one that leaves the key out.
func (j workflowJob) steps() int {
	declared, list := j.Steps.([]any)
	if !list {
		return 0
	}
	return len(declared)
}

// workflowFile is as much of a workflow's shape as is worth holding: enough
// that a file which is valid YAML and not a workflow fails.
type workflowFile struct {
	// A workflow's trigger key is `on`, which YAML 1.2 reads as that string and
	// YAML 1.1 reads as the boolean true. The file is read by parsers of both
	// vintages, so both spellings are the same key and a workflow carrying
	// either has a trigger.
	On   any `yaml:"on"`
	True any `yaml:"true"`

	Jobs map[string]workflowJob `yaml:"jobs"`
}

// workflowRunStep is the part of a step this package reads the shell of: what
// the step calls itself, and what it runs. A step with no `run:` is an action,
// whose version is pinned in the `uses:` reference rather than in a command.
type workflowRunStep struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
}

// workflowRunJob and workflowRunFile are the same file read for its commands
// rather than for its shape. They are separate from the types above because
// `steps:` is decoded here as a list of mappings, and holding the shape gate to
// that would turn a malformed `steps:` into "this does not decode as a
// workflow" instead of the job-shaped complaint it makes today.
type workflowRunJob struct {
	Steps []workflowRunStep `yaml:"steps"`
}

type workflowRunFile struct {
	Jobs map[string]workflowRunJob `yaml:"jobs"`
}

// floatingReference is one way a command names a version that moves, and what
// it actually installs when it runs.
type floatingReference struct {
	Marker string
	Gets   string
}

// floatingReferences are the spellings a workflow command uses to fetch
// whatever the upstream has now. They are literals rather than a parse of the
// command, because the claim is not that this understands shell: it is that the
// module install this repository was actually broken by, and the two neighbours
// somebody reaches for next, cannot come back without saying so.
var floatingReferences = []floatingReference{
	{Marker: "@latest", Gets: "whatever the upstream released most recently"},
	{Marker: "@main", Gets: "the upstream's branch tip"},
	{Marker: "@master", Gets: "the upstream's branch tip"},
	{Marker: "releases/latest/", Gets: "whatever the upstream released most recently"},
}

// WorkflowVersionPins reports every workflow command that installs a tool at a
// version somebody else decides.
//
// A workflow step is the one place in this repository where a stranger's
// release lands in a pull request nobody wrote: `go install …@latest` in the
// job that walks the README's getting-started steps moved on 2026-09-05 and
// turned every open pull request red at a step none of them had touched, where
// it read as a broken getting-started walkthrough rather than as an upstream
// release. Pinning the version ended that instance; this ends the class,
// because the pin is one edit away from being a floating reference again and
// no reviewer reliably sees which of the two they are reading. What the pin costs in return — somebody has to move it deliberately —
// is docs/developing-yoyo.md#the-tracker-version-ci-pins.
func WorkflowVersionPins(root string, paths []string) []Problem {
	var problems []Problem
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("it could not be read: %v", err)})
			continue
		}
		var workflow workflowRunFile
		if err := yaml.Unmarshal(content, &workflow); err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("its commands could not be read: %v", err)})
			continue
		}
		// Sorted for the same reason the shape gate sorts: a map ranges in a
		// different order every run, and what a gate reports has to be the same
		// twice over one checkout.
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			for index, step := range workflow.Jobs[name].Steps {
				for _, floating := range floatingReferences {
					if !strings.Contains(step.Run, floating.Marker) {
						continue
					}
					problems = append(problems, Problem{
						Path:   path,
						Reason: fmt.Sprintf("its %q job runs %s, which installs %s: name a version instead", name, describeStep(step, index), floating.Gets),
					})
				}
			}
		}
	}
	return problems
}

// describeStep is how a problem names the step it found, which is by the step's
// own name where it has one -- that is what somebody scrolls the file for.
func describeStep(step workflowRunStep, index int) string {
	if step.Name != "" {
		return fmt.Sprintf("%q", step.Name)
	}
	return fmt.Sprintf("step %d", index+1)
}

// WorkflowShape holds each workflow to the shape a workflow needs.
//
// Decoding alone is not enough for these: workflow YAML is only ever executed by
// GitHub, and the release workflow's trigger is a tag push, so a mistyped
// top-level key there is valid YAML that first misbehaves during a real
// publication. A trigger, at least one job, and a runner and steps in each is
// the part of "this is a workflow" that can be checked without GitHub.
func WorkflowShape(root string, paths []string) []Problem {
	var problems []Problem
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("it could not be read: %v", err)})
			continue
		}
		var workflow workflowFile
		if err := yaml.Unmarshal(content, &workflow); err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("it does not decode as a workflow: %v", err)})
			continue
		}
		if workflow.On == nil && workflow.True == nil {
			problems = append(problems, Problem{Path: path, Reason: "it declares no `on:` trigger, so nothing ever runs it"})
		}
		if len(workflow.Jobs) == 0 {
			problems = append(problems, Problem{Path: path, Reason: "it declares no jobs, so a trigger it does have has nothing to do"})
		}
		// Sorted, because a map ranges in a different order every run and what a
		// gate reports has to be the same twice over one checkout.
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if job.RunsOn == nil {
				problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("its %q job names no `runs-on:`, so there is nothing to run it on", name)})
			}
			if job.steps() == 0 {
				problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("its %q job has no steps", name)})
			}
		}
	}
	return problems
}
