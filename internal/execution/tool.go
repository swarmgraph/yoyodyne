package execution

import (
	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ToolAudit is request metadata and applied bounds, never returned content or
// untrusted request prose. Both conversation and run invocations carry it.
type ToolAudit struct {
	ID         string                `json:"id"`
	Tool       capability.Capability `json:"tool"`
	Role       domain.AgentRole      `json:"role"`
	Turn       int                   `json:"turn"`
	Round      int                   `json:"round"`
	Pass       string                `json:"pass,omitempty"`
	Parameters map[string]any        `json:"parameters,omitempty"`
	Bounds     action.ToolBounds     `json:"bounds"`
	Requests   int                   `json:"requests"`
	Bytes      int                   `json:"bytes"`
	Truncated  bool                  `json:"truncated,omitempty"`
}
