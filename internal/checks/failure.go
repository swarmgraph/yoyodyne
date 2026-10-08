package checks

import (
	"strings"
	"unicode/utf8"
)

const failureOutputLimit = 8 << 10
const failureCut = "[earlier check output truncated]\n"

// failureCapture reads the redacted observer stream, which continues after the
// process runner stops retaining its prefix. Names and the tail stay bounded.
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
}

func (c *failureCapture) add(text string) {
	c.observed = true
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if reason, ok := couldNotRunReason(line); ok {
			c.couldNotRun = reason
		}
		var name string
		switch {
		case strings.HasPrefix(line, "--- FAIL: "):
			fields := strings.Fields(strings.TrimPrefix(line, "--- FAIL: "))
			if len(fields) > 0 {
				name = fields[0]
			}
		case strings.HasPrefix(line, "FAILED "), strings.HasPrefix(line, "FAIL: "):
			name = strings.SplitN(strings.TrimSpace(strings.SplitN(line, " ", 2)[1]), " - ", 2)[0]
		case strings.HasPrefix(line, "FAIL\t"), strings.HasPrefix(line, "FAIL "):
			fields := strings.Fields(line)
			if len(fields) > 1 {
				name = fields[1]
			}
		}
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
	header := "No failing test or package was named in the captured output.\n"
	if len(c.names) > 0 {
		header = "Failing tests or packages named in the captured output (up to 20): " + strings.Join(c.names, ", ") + "\n"
	}
	tail := strings.Trim(c.tail, "\n")
	limit := failureOutputLimit - len(header)
	if c.cut || len(tail) > limit {
		tail = failureCut + cutFailureText(tail, limit-len(failureCut))
	}
	return header + tail
}

// FailureOutput is the bounded evidence a failed check carries to its run,
// tracker note and docket. The fallback supports runners returning only a
// process result, including a check the stage never started.
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
