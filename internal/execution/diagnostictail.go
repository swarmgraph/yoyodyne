package execution

import "unicode/utf8"

const DiagnosticTailBytes = 2560

const DiagnosticTailMarker = "[earlier output omitted; retaining the last 2560 bytes]\n"

// DiagnosticTail bounds the retained ending and declares any omitted prefix.
func DiagnosticTail(output string) string {
	if len(output) <= DiagnosticTailBytes {
		return output
	}
	cut := len(output) - DiagnosticTailBytes
	for cut < len(output) && !utf8.RuneStart(output[cut]) {
		cut++
	}
	return DiagnosticTailMarker + output[cut:]
}
