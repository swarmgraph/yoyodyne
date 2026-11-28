package doctor

import (
	"fmt"
	"sort"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// retiredSettingSays is what each setting the harness no longer reads did, and
// what took its place, in words that follow "it no longer does anything:".
var retiredSettingSays = map[string]string{
	"execution.build_move_interval": "there is no least time between two moves onto a new build any more, and every part of the product moves onto the newest installed build together, the scheduler within 15 minutes of falling behind, or past the time limit of a merge queue check it waits out",
}

// checkRetiredSettings is a warning for each setting the configuration still
// names that nothing reads. The file loads, so nothing is refused; the
// warning is so whoever wrote the setting does not go on believing it holds.
func (d *diagnosis) checkRetiredSettings(resolved config.Resolved) []Finding {
	keys := make([]string, 0, len(resolved.NoLongerRead))
	for key := range resolved.NoLongerRead {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	findings := make([]Finding, 0, len(keys))
	for _, key := range keys {
		said := retiredSettingSays[key]
		if said == "" {
			said = "nothing in the harness reads it"
		}
		findings = append(findings, Finding{
			Check:   "retired setting",
			Status:  StatusWarning,
			Summary: fmt.Sprintf("%s is set in %s and no longer does anything", key, resolved.NoLongerRead[key]),
			Detail:  fmt.Sprintf("it no longer does anything: %s; the setting can be taken out of the file, and leaving it in changes nothing", said),
			Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(resolved.Path)),
		})
	}
	return findings
}
