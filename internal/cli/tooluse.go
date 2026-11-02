package cli

import (
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/spend"
	"github.com/mason-bryant/yoyodyne/internal/tooluse"
)

// toolUse is what every provider invocation these parts make is handed, so the
// tool calls it made are compared with the tool access it was launched under
// and what does not match is filed in the product's report pile, where the
// status listing, the dashboard, and the Slack channel already read it.
func (parts components) toolUse() spend.ToolUseObserver {
	watch := tooluse.Watch{
		RepositoryID: string(parts.config.Product.RepositoryID),
		Build:        buildinfo.Commit(),
	}
	// A nil store is left as no pile rather than handed on inside the
	// interface, so a finding with nowhere to go is said as that.
	if parts.reports != nil {
		watch.Reports = parts.reports
	}
	return watch
}
