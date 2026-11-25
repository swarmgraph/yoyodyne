package releasecut

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// What a failed cut printed, and what its refusal quotes of it.
//
// The script that runs the release checks is the one the named commit carries,
// which is older than the harness running it. So which check failed and what it
// said are read here, from what that script has always printed, rather than
// from anything a newer script could be made to print.

const (
	// maxOutputBytes bounds what is held of one cut's output. Past it the oldest
	// lines go, because the end is where a failure is.
	maxOutputBytes = 8 << 20
	// maxQuoted, maxQuotedLineBytes and maxQuotedBytes bound what a refusal
	// quotes, so it fits its record beside everything else the record says.
	maxQuoted          = 20
	maxQuotedLineBytes = 190
	maxQuotedBytes     = 2000
	// lastLines is how many lines of each stream stand for a check that named no
	// failure.
	lastLines = 5
	// streamSkew is how far apart the two streams can arrive: a line on standard
	// error is read beside the heading standard output printed before it, not
	// after it.
	streamSkew = time.Second
	// quoteSeparator stands between the lines a refusal quotes.
	quoteSeparator = " | "
)

// releaseSteps are the headings scripts/cut-release.sh prints ahead of its
// checks, with what each is called in a refusal. The walkthrough prints headings
// of its own, so a heading is one of these only whole.
var releaseSteps = []struct{ heading, name string }{
	{"=== gate: this release's notes", "this release's notes"},
	{"=== gate: release readiness", "release readiness"},
	{"=== gate: the getting-started walkthrough", walkthroughStep},
	{"=== gate: make check", "make check"},
	{"=== build: the archives and their checksums", "the release build"},
}

const walkthroughStep = "the getting-started walkthrough"

// cutOutput collects what the release checks print, both streams in the order
// they arrived. It is an observer rather than the process result because the
// result keeps the two apart and keeps the beginning when it is cut.
type cutOutput struct {
	mask  execution.Redactor
	lines []execution.Output
	bytes int
	// dropped counts the lines let go at the bound.
	dropped int
	// step is the check that started last, from where in lines it began, and
	// when.
	step    string
	from    int
	started time.Time
	// where says where the output is kept, or why it is not.
	where string
}

func (o *cutOutput) observe(line execution.Output) {
	if line.LineTruncated {
		// The cut can fall inside a secret, which would leave half of one that
		// masking no longer recognizes, so the half of the line holding it goes.
		line.Text = strings.ToValidUTF8(line.Text[:len(line.Text)/2], "") + " [the rest of this line was not kept]"
	}
	if line.Stream == execution.StreamStdout {
		for _, step := range releaseSteps {
			if line.Text == step.heading || (strings.HasPrefix(step.heading, "=== build") && strings.HasPrefix(line.Text, step.heading+" for ")) {
				o.step, o.from, o.started = step.name, len(o.lines), line.Timestamp
			}
		}
	}
	o.lines = append(o.lines, line)
	o.bytes += len(line.Text) + 1
	for o.bytes > maxOutputBytes && len(o.lines) > 1 {
		o.bytes -= len(o.lines[0].Text) + 1
		o.lines = o.lines[1:]
		o.dropped++
		o.from = max(o.from-1, 0)
	}
}

// keep writes the output into the held directory, over what an earlier failed
// cut of the version left there.
func (o *cutOutput) keep(root *repowrite.PinnedRoot, version, commit string, at time.Time) error {
	var text strings.Builder
	fmt.Fprintf(&text, "What the release checks for %s printed at commit %s. The checks ended at %s. Secrets are masked.\n\n", version, commit, local(at))
	if o.dropped > 0 {
		fmt.Fprintf(&text, "[the first %d lines were not kept]\n", o.dropped)
	}
	for _, line := range o.lines {
		text.WriteString(line.Text)
		text.WriteByte('\n')
	}
	return root.WriteFile(outputName(version), []byte(o.mask.Redact(text.String())), 0o600, false)
}

// outputName is the kept output of a version's newest failed cut, beside where
// its checkout is made.
func outputName(version string) string { return version + ".failed-output.txt" }

// failure is what a refusal quotes: the check that was running, and either the
// failures it named or the last lines it printed.
func (o *cutOutput) failure() string {
	var stdout, stderr []string
	for index, line := range o.lines {
		switch {
		case line.Stream == execution.StreamStdout && index >= o.from:
			stdout = append(stdout, line.Text)
		case line.Stream == execution.StreamStderr && o.step != walkthroughStep && !line.Timestamp.Before(o.started.Add(-streamSkew)):
			// The walkthrough's own output is all on standard output; the script
			// repeats the end of it on standard error.
			stderr = append(stderr, line.Text)
		}
	}
	if o.step != "" && len(stdout) > 0 && strings.HasPrefix(stdout[0], "=== ") {
		// The heading itself says nothing the step's name does not.
		stdout = stdout[1:]
	}
	var failed, last []string
	// One stream and then the other, so the same failure reads the same however
	// the two interleaved.
	for _, stream := range [][]string{stdout, stderr} {
		var said []string
		for _, line := range strings.Split(o.mask.Redact(strings.Join(stream, "\n")), "\n") {
			folded := oneline.Fold(line, maxQuotedLineBytes-len(oneline.Marker))
			if folded == "" || strings.HasPrefix(folded, "cut-release: ") {
				continue
			}
			said = append(said, folded)
			// A walkthrough claim or a Go test that failed, and a Go package that did.
			if (strings.Contains(line, "FAIL:") || strings.HasPrefix(line, "FAIL\t")) && !slices.Contains(failed, folded) {
				failed = append(failed, folded)
			}
		}
		last = append(last, said[max(len(said)-lastLines, 0):]...)
	}
	where := "before the first release check"
	if o.step != "" {
		where = "during " + o.step
	}
	switch {
	case len(failed) > 0:
		return fmt.Sprintf("; what failed, %s: %s", where, quote(failed))
	case len(last) > 0:
		return fmt.Sprintf("; it stopped %s without naming a failure, and its last lines are: %s", where, quote(last))
	}
	return fmt.Sprintf("; it stopped %s and printed nothing", where)
}

// quote joins lines inside the bounds, saying how many it left out.
func quote(lines []string) string {
	var quoted []string
	size := 0
	for _, line := range lines {
		if len(quoted) == maxQuoted || size+len(line)+len(quoteSeparator) > maxQuotedBytes {
			break
		}
		quoted = append(quoted, line)
		size += len(line) + len(quoteSeparator)
	}
	joined := strings.Join(quoted, quoteSeparator)
	if left := len(lines) - len(quoted); left > 0 {
		joined += fmt.Sprintf("%sand %d more", quoteSeparator, left)
	}
	return joined
}

// varying is what differs between two runs of the same failure: temporary
// paths, times and durations, process and port numbers, and commits.
var varying = []*regexp.Regexp{
	regexp.MustCompile(`(?:/private)?(?:/var/folders|/tmp)/[^\s"'` + "`" + `,;)]+`),
	regexp.MustCompile(`\b\d{4}-\d\d-\d\d\b`),
	regexp.MustCompile(`\b\d\d?:\d\d(?::\d\d(?:\.\d+)?)?\b`),
	regexp.MustCompile(`\b(?:\d+h)?(?:\d+m)?\d+(?:\.\d+)?(?:ms|µs|s)\b`),
	regexp.MustCompile(`\b[0-9a-f]{12,64}\b`),
	regexp.MustCompile(`(?i)\bpid:? \d+\b`),
	regexp.MustCompile(`\d{5,}`),
}

// comparableText is a refusal's text with what varies between runs taken out and
// the lines it quotes in one order, since packages finish in any.
func comparableText(text string) string {
	for _, pattern := range varying {
		text = pattern.ReplaceAllString(text, "#")
	}
	start := quotedLines.FindStringIndex(text)
	if start == nil {
		return text
	}
	lines := strings.Split(text[start[1]:], quoteSeparator)
	slices.Sort(lines)
	return text[:start[1]] + strings.Join(lines, quoteSeparator)
}

// quotedLines is how failure introduces the lines it quotes.
var quotedLines = regexp.MustCompile(`; what failed, [^:]*: |its last lines are: `)

// ended says in words how the checks ended where the script made no refusal.
func ended(result execution.ProcessResult) string {
	switch result.Status {
	case execution.ProcessTimedOut:
		return fmt.Sprintf("they ran longer than the %s they are given and were stopped", CutTimeout)
	case execution.ProcessCancelled:
		return "they were stopped because the harness was stopping"
	}
	return fmt.Sprintf("they ended with exit status %d and gave no reason", result.ExitCode)
}
