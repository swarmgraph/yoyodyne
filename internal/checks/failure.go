package checks

import (
	"strings"
	"unicode/utf8"
)

const failureOutputLimit = 8 << 10
const failureCut = "[earlier check output truncated]\n"

// failureWindowLimit bounds the output kept around the first line reporting a
// failure, and failureWindowLead is how many lines before that line it keeps,
// within failureWindowLeadLimit bytes. The rest of failureOutputLimit is the
// end of the output.
const (
	failureWindowLimit     = 3 << 10
	failureWindowLead      = 5
	failureWindowLeadLimit = 512
)

const (
	failureWindowTitle   = "Output around the first line reporting a failure:\n"
	failureWindowLeadCut = "[earlier lines before the failure cut]\n"
	failureWindowRestCut = "[later lines after the failure cut]\n"
	failureTailTitle     = "End of the output:\n"
)

// failureMarker is one way a test runner opens a line reporting a failure, and
// how the failing test or package is named on it.
type failureMarker struct {
	prefix string
	name   func(line string) string
}

// failureMarkers are the failure lines the test runners a project's declared
// checks drive are known to print: Go's "--- FAIL:" and "FAIL\tpackage",
// pytest's "FAILED", and unittest's "FAIL:". They are data rather than an
// understanding of any runner, so a check printing none of them keeps the end
// of its output only, as it always did.
var failureMarkers = []failureMarker{
	{"--- FAIL: ", func(line string) string {
		fields := strings.Fields(strings.TrimPrefix(line, "--- FAIL: "))
		if len(fields) > 0 {
			return fields[0]
		}
		return ""
	}},
	{"FAILED ", unittestFailureName},
	{"FAIL: ", unittestFailureName},
	{"FAIL\t", packageFailureName},
	{"FAIL ", packageFailureName},
}

func unittestFailureName(line string) string {
	return strings.SplitN(strings.TrimSpace(strings.SplitN(line, " ", 2)[1]), " - ", 2)[0]
}

func packageFailureName(line string) string {
	fields := strings.Fields(line)
	if len(fields) > 1 {
		return fields[1]
	}
	return ""
}

// failingName is the test or package a line of output reports failing, and
// empty for a line that reports none.
func failingName(line string) string {
	for _, marker := range failureMarkers {
		if strings.HasPrefix(line, marker.prefix) {
			return marker.name(line)
		}
	}
	return ""
}

// failureCapture reads the redacted observer stream, which continues after the
// process runner stops retaining its prefix. Names, the tail and the window
// around the first failure stay bounded.
type failureCapture struct {
	tail      string
	names     []string
	nameBytes int
	cut       bool
	observed  bool
	// couldNotRun is the reason the latest could-not-run line gave, read from
	// every line the check printed rather than from the bounded tail, so a
	// line followed by a long wrapper's output is still found.
	couldNotRun string
	// total is how many bytes of output tail has been given, so a position in
	// the output can be compared with where the kept tail starts.
	total int
	// lead is the last few lines before any failure has been reported. Once
	// one has, window is that lead, the failure line and what follows it up to
	// failureWindowLimit, starting windowStart bytes into the output.
	lead        []string
	leadCut     bool
	window      string
	windowStart int
	windowFound bool
	windowFull  bool
	windowCut   bool
}

func (c *failureCapture) add(text string) {
	c.observed = true
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if reason, ok := couldNotRunReason(line); ok {
			c.couldNotRun = reason
		}
		name := failingName(line)
		c.keepAround(raw, name != "")
		c.total += len(raw) + 1
		if name != "" && len(c.names) < 20 {
			name = cutFailureText(name, 100)
			found := false
			for _, prior := range c.names {
				if prior == name {
					found = true
				}
			}
			if !found && c.nameBytes+len(name) <= 2000 {
				c.names = append(c.names, name)
				c.nameBytes += len(name)
			}
		}
	}
	c.tail += text + "\n"
	if len(c.tail) > failureOutputLimit {
		c.tail = cutFailureText(c.tail, failureOutputLimit)
		c.cut = true
	}
}

// keepAround builds the window around the first line reporting a failure, from
// a line read at c.total bytes into the output.
func (c *failureCapture) keepAround(line string, failure bool) {
	switch {
	case !c.windowFound && !failure:
		c.lead = append(c.lead, line+"\n")
		if len(c.lead) > failureWindowLead {
			c.lead = c.lead[1:]
			c.leadCut = true
		}
	case !c.windowFound:
		c.windowFound = true
		lead := strings.Join(c.lead, "")
		if len(lead) > failureWindowLeadLimit {
			lead = cutFailureText(lead, failureWindowLeadLimit)
			c.leadCut = true
		}
		c.lead = nil
		c.windowStart = c.total - len(lead)
		c.appendWindow(lead + line + "\n")
	case !c.windowFull:
		c.appendWindow(line + "\n")
	}
}

func (c *failureCapture) appendWindow(text string) {
	room := failureWindowLimit - len(c.window)
	if len(text) <= room {
		c.window += text
		return
	}
	cut := room
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	c.window += text[:cut]
	c.windowFull = true
	c.windowCut = true
}

func cutFailureText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := len(text) - limit
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return text[cut:]
}

func (c failureCapture) render() string {
	header := "No failing test or package was named in the captured output, so only its end is kept.\n"
	if len(c.names) > 0 {
		header = "Failing tests or packages named in the captured output (up to 20): " + strings.Join(c.names, ", ") + "\n"
	}
	window := c.renderWindow()
	trailing := len(c.tail) - len(strings.TrimRight(c.tail, "\n"))
	tail := strings.Trim(c.tail, "\n")
	if window == "" {
		return header + c.renderTail(tail, failureOutputLimit-len(header))
	}
	// The window is left out when the end kept below already holds it, which
	// is every output short enough to keep whole.
	rendered := c.renderTail(tail, failureOutputLimit-len(header)-len(window)-len(failureTailTitle))
	kept := strings.TrimPrefix(rendered, failureCut)
	if c.windowStart >= c.total-trailing-len(kept) {
		return header + c.renderTail(tail, failureOutputLimit-len(header))
	}
	return header + window + failureTailTitle + rendered
}

func (c failureCapture) renderWindow() string {
	if !c.windowFound {
		return ""
	}
	var window strings.Builder
	window.WriteString(failureWindowTitle)
	if c.leadCut {
		window.WriteString(failureWindowLeadCut)
	}
	window.WriteString(c.window)
	if c.windowCut {
		if !strings.HasSuffix(c.window, "\n") {
			window.WriteString("\n")
		}
		window.WriteString(failureWindowRestCut)
	}
	return window.String()
}

func (c failureCapture) renderTail(tail string, limit int) string {
	if c.cut || len(tail) > limit {
		return failureCut + cutFailureText(tail, limit-len(failureCut))
	}
	return tail
}

// FailureOutput is the bounded evidence a failed check carries to its run,
// tracker note and docket: the failing tests named, the output around the
// first line reporting a failure, and the end of the output. The fallback
// supports runners returning only a process result, including a check the
// stage never started.
func FailureOutput(result Result) string {
	if result.FailureOutput != "" {
		return result.FailureOutput
	}
	var capture failureCapture
	for _, text := range []string{result.Process.Stdout, result.Process.Stderr} {
		if text != "" {
			capture.add(text)
		}
	}
	return capture.render()
}
