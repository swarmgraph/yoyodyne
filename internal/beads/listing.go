package beads

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// listingCutWarning reads the hints bd writes even when its process succeeds.
// bd ready says "Showing N of M ready issues. Use --limit 0 for all"; bd list
// also points to --limit when it cuts its output. Other diagnostics are not
// evidence that a listing was cut.
func listingCutWarning(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "--limit") && (strings.Contains(lower, "showing ") || strings.Contains(lower, "truncat")) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// completeWorkItems counts only complete, readable items in the retained JSON
// array. It also works on a byte-cut array whose last item is incomplete. This
// is evidence about how much arrived, never a list callers may schedule from.
func completeWorkItems(stdout string) int {
	decoder := json.NewDecoder(strings.NewReader(stdout))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('[') {
		return 0
	}
	count := 0
	for decoder.More() {
		var raw rawWorkItem
		if err := decoder.Decode(&raw); err != nil {
			break
		}
		if _, err := convertWorkItem(raw); err != nil {
			break
		}
		count++
	}
	return count
}

// decodeWorkListing also names a JSON array that ends early without a warning
// from bd or the runner. Other decoding failures keep their original cause.
func decodeWorkListing(data []byte, verb string) ([]WorkItem, error) {
	items, err := decodeWorkItems(data)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("bd %s output was cut: read %d complete work item(s); work may be missing: %w", verb, completeWorkItems(string(data)), err)
	}
	return items, err
}
