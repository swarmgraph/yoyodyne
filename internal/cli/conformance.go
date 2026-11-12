package cli

// Checking the system against what it records about itself, before a version is
// tagged.
//
// `make check` says the code does what its tests say. This says the repository
// still means what its documents say: that every canonical artifact is one, that
// every reference between them and every link in the prose reaches something,
// that every architectural invariant is one a run would actually be delivered,
// and that every admitted work item traces to a goal the product states. Tagging
// then means the system demonstrably matches its recorded intent rather than
// only that its tests pass.
//
// The sequence is not written here. It is a workflow definition — this build
// ships one and a project may keep its own — validated and compiled before a
// single check runs, and walked by the workflow runtime one durable transition
// at a time. What this file does is find the definition, read what the checks
// judge, and render the answer for the two readers it has: an operator at a
// terminal, and the release notes the passing result ships in.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/conformance"
)

func runConformance(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("conformance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	definitionPath := flags.String("workflow", "", "workflow definition to run (default: the project's own copy, or the built-in one)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	notes := flags.Bool("notes", false, "render the result as the Markdown section a release's notes carry")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "conformance does not accept positional arguments: it checks the whole product against what it records about itself")
		printConformanceUsage(stderr)
		return 2
	}
	if *jsonOutput && *notes {
		fmt.Fprintln(stderr, "conformance renders one result at a time: pass --json or --notes, not both")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportConformanceError(stdout, stderr, *jsonOutput, err)
	}
	definition, err := conformance.Compile(conformanceDefinition(*definitionPath, parts.configPath))
	if err != nil {
		return reportConformanceError(stdout, stderr, *jsonOutput, err)
	}

	// A tracker that could not be read costs the goals check its answer rather
	// than costing the command its run: the repository half is readable without
	// it, and the gate says outright that nothing was judged instead of reporting
	// a backlog with nothing wrong in it.
	trackerUnreadable := ""
	admitted, err := admittedWorkItems(ctx, parts.tracker())
	if err != nil {
		trackerUnreadable = err.Error()
		admitted = nil
	}
	sources := conformance.Gather(parts.repository, parts.config.Product, admitted, trackerUnreadable)

	result, err := conformance.Assess(ctx, definition, parts.store, sources, time.Now)
	if err != nil {
		return reportConformanceError(stdout, stderr, *jsonOutput, err)
	}

	switch {
	case *jsonOutput:
		if code := writeJSON(stdout, stderr, result); code != 0 {
			return code
		}
	case *notes:
		fmt.Fprint(stdout, conformanceNotes(result))
	default:
		printConformance(stdout, result)
	}
	if !result.Conforms {
		return 1
	}
	return 0
}

// conformanceDefinition is the definition the gate runs: the one named on the
// command line, otherwise the project's own copy where it keeps one, otherwise
// the definition this build ships.
//
// Nothing is merged between them. A project that ejects a copy owns the whole
// sequence from then on, which is the only arrangement where reading the file
// tells somebody what actually ran.
func conformanceDefinition(named, configPath string) string {
	if named != "" {
		return named
	}
	for _, directory := range config.ConfigurationDirectories(configPath) {
		candidate := filepath.Join(directory, filepath.FromSlash(conformance.ProjectDefinitionPath))
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

// conformanceStep is how one finding is labelled: the state the definition
// performed it in, and the check behind that state where the two differ.
//
// They differ exactly when a project named a state something other than the
// check it selects, which its own definition is free to do. Naming the state
// alone would leave a renamed sequence unreadable against this build, and naming
// the check alone would leave it unreadable against the file the operator wrote,
// so where they disagree the report carries both.
func conformanceStep(finding conformance.Finding) string {
	if finding.Check == "" || finding.Check == finding.Step {
		return finding.Step
	}
	return finding.Step + " (" + finding.Check + ")"
}

// printConformance is the reading an operator gets: one line per check, and then
// what the whole of it came to.
func printConformance(stdout io.Writer, result conformance.Result) {
	if result.Conforms {
		fmt.Fprintln(stdout, "release readiness: the system matches what it records about itself")
	} else {
		fmt.Fprintln(stdout, "release readiness: something the system records about itself is no longer true")
	}
	width := 0
	for _, finding := range result.Findings {
		if named := conformanceStep(finding); len(named) > width {
			width = len(named)
		}
	}
	fmt.Fprintln(stdout)
	for _, finding := range result.Findings {
		fmt.Fprintf(stdout, "%-*s  %-8s  %s\n", width, conformanceStep(finding), finding.Outcome, finding.Summary)
		for _, mismatch := range finding.Mismatches {
			fmt.Fprintf(stdout, "  %s\n", mismatch)
		}
		if finding.Truncated > 0 {
			fmt.Fprintf(stdout, "  and %d more, not listed\n", finding.Truncated)
		}
		for _, note := range finding.Notes {
			fmt.Fprintf(stdout, "  %s\n", note)
		}
	}
	fmt.Fprintf(stdout, "\n%s (schema %d, definition %s) ended in %q\n", result.Workflow, result.Schema, result.Definition, result.Terminal)
	fmt.Fprintf(stdout, "digest %s, instance %s\n", result.Digest, result.Instance)
	if !result.Conforms {
		// The refusal names what to do rather than only what is wrong: everything
		// here is a document somebody has to reconcile, and none of it is fixed by
		// running this again. What it claims is scoped to the checkout, because the
		// walk itself was recorded — see the usage text.
		fmt.Fprintln(stdout, "the tag is refused until each mismatch above is reconciled; nothing in this repository was changed")
	}
}

// conformanceNotes is the result as a release's notes carry it: one Markdown
// section, headed and delimited so a cut can put a fresh result in place of an
// older one without disturbing anything the product manager wrote around it.
// The "ended in" line and the "Pinned to" line are what scripts/cut-release.sh
// reads back to decide whether the result the notes already carry is current:
// the verdict and the definition, not the counts, which move every day.
func conformanceNotes(result conformance.Result) string {
	var rendered strings.Builder
	rendered.WriteString(conformanceNotesBegin + "\n")
	rendered.WriteString("## Release readiness\n\n")
	verdict := "the system matches what it records about itself"
	if !result.Conforms {
		verdict = "something the system records about itself is no longer true"
	}
	fmt.Fprintf(&rendered, "The `%s` workflow (schema %d, definition %s) ended in **%s** on %s: %s.\n\n",
		result.Workflow, result.Schema, result.Definition, result.Terminal, result.At.UTC().Format("2006-01-02"), verdict)
	for _, finding := range result.Findings {
		fmt.Fprintf(&rendered, "- **%s** — %s — %s\n", conformanceStep(finding), finding.Outcome, finding.Summary)
		for _, mismatch := range finding.Mismatches {
			fmt.Fprintf(&rendered, "  - %s\n", mismatch)
		}
		if finding.Truncated > 0 {
			fmt.Fprintf(&rendered, "  - and %d more, not listed\n", finding.Truncated)
		}
		for _, note := range finding.Notes {
			fmt.Fprintf(&rendered, "  - %s\n", note)
		}
	}
	fmt.Fprintf(&rendered, "\nPinned to `%s`.\n", result.Digest)
	rendered.WriteString(conformanceNotesEnd + "\n")
	return rendered.String()
}

// The markers the notes section is written between. They are HTML comments so a
// reader never sees them, and they are exported to the release cut through the
// rendered output itself rather than duplicated in shell: the script looks for
// these lines and replaces what is between them, so a result stamped twice
// leaves one section rather than two.
const (
	conformanceNotesBegin = "<!-- yoyodyne:release-readiness -->"
	conformanceNotesEnd   = "<!-- /yoyodyne:release-readiness -->"
)

func reportConformanceError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, map[string]string{"error": err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printConformanceUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo conformance [options]

Check this product against what it records about itself, which is what a release
is tagged behind. Every canonical artifact is one, every reference between them
and every link in the documentation resolves, every architectural invariant is
one a run would be delivered, and every admitted work item traces to a goal the
product states. What a change upstream left unanswered downstream is reported
too, and refuses nothing -- staleness is a condition to look at rather than a
broken build.

It writes nothing to this repository or to the tracker, so it cannot be what
changed what it was about to judge. A divergence exits 1 and names every mismatch
the check that found it collected, which is what refuses a tag in
`+"`make release`"+`.

One record is written outside both: the run of the workflow itself, under the
harness's own state root, so what a release had to pass can be read back rather
than only having been printed. Nothing prunes those, and one is written per
invocation.

The sequence is a workflow definition rather than code. This build ships one;
a project that wants its own copies it to
.yoyodyne/workflows/release-readiness.yaml and edits it there. Either way it is
validated and compiled before a single check runs, and the result says which of
the two it ran.

Options:
  --config <path>     configuration file (default: the nearest .yoyodyne/config.yaml)
  --workflow <path>   workflow definition to run (default: the project's own copy, or the built-in one)
  --json              emit machine-readable JSON
  --notes             render the result as the Markdown section a release's notes carry`)
}
