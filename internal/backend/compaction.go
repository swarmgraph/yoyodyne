package backend

import (
	"strings"
	"time"
)

// ProviderCompaction is a provider's report that it compacted the session an
// invocation was running in: it summarized what the session held and carried on
// from the summary, so detail the session had is gone from it. The harness does
// not decide this and cannot stop it; what it can do is say it happened, on the
// record of the pass or conversation it happened in.
//
// The sizes are the provider's own and are zero where it did not report them.
// Claude Code reports the size before compacting and not after; Codex's
// non-interactive stream reports a compaction only where it says so at all.
type ProviderCompaction struct {
	At time.Time `json:"at"`
	// Trigger is the provider's own word for why it compacted, such as "auto"
	// for a session that reached its threshold, and empty where it named none.
	Trigger      string `json:"trigger,omitempty"`
	BeforeTokens int    `json:"before_tokens,omitempty"`
	AfterTokens  int    `json:"after_tokens,omitempty"`
}

// ContextWindow is an adapter that states how many tokens of context the model
// a request names can hold. A conversation bounds what a recurring turn starts
// from by it. An adapter that states none is bounded by
// DefaultContextWindowTokens.
type ContextWindow interface {
	ContextWindowTokens(RunRequest) int
}

// DefaultContextWindowTokens is the window assumed of an endpoint whose adapter
// states none: the smallest window a supported provider's current models have.
const DefaultContextWindowTokens = 200_000

// ContextWindowTokens is what the adapter states for the request, and the
// default where it states nothing.
func ContextWindowTokens(adapter any, request RunRequest) int {
	if stated, ok := adapter.(ContextWindow); ok {
		if tokens := stated.ContextWindowTokens(request); tokens > 0 {
			return tokens
		}
	}
	return DefaultContextWindowTokens
}

// ModelInFamily reports whether a model selector or provider identifier names
// one of the families given, by the family's word anywhere in it: "opus" and
// "claude-opus-5-5" are both opus.
func ModelInFamily(model string, families ...string) bool {
	model = strings.ToLower(model)
	for _, family := range families {
		if strings.Contains(model, family) {
			return true
		}
	}
	return false
}
