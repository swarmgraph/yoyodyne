package execution

import (
	"fmt"
	"unicode/utf8"
)

// DiagnosticTailBytes is how much of the end of a failed session's output is
// kept: enough for the error a process prints before it exits, small enough to
// travel inside a pass record and the finding about it.
const DiagnosticTailBytes = 2560

// DiagnosticTailMarker opens a kept ending whose start was cut, so a reader
// never takes the ending for everything that was printed.
var DiagnosticTailMarker = diagnosticTailMarker(DiagnosticTailBytes)

func diagnosticTailMarker(bound int) string {
	return fmt.Sprintf("[earlier output omitted; retaining the last %d bytes]\n", bound)
}

// DiagnosticTail bounds output to its last DiagnosticTailBytes and declares any
// omitted start.
func DiagnosticTail(output string) string {
	return DiagnosticTailWithin(output, DiagnosticTailBytes)
}

// DiagnosticTailWithin bounds output to its last bound bytes, starting on a
// whole character, and declares any omitted start.
func DiagnosticTailWithin(output string, bound int) string {
	if len(output) <= bound {
		return output
	}
	if bound < 0 {
		bound = 0
	}
	cut := len(output) - bound
	for cut < len(output) && !utf8.RuneStart(output[cut]) {
		cut++
	}
	return diagnosticTailMarker(bound) + output[cut:]
}
