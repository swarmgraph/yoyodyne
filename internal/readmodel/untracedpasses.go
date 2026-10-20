package readmodel

// A pass that found something and left no trace of it.
//
// A role's memory writes are voluntary, and a pass that reports findings in its
// account and writes nothing else leaves them in two places: the account in
// the sweep log, which the role never reads back, and its conversation, which
// is compacted. On the operator's direction of 2026-09-28 such a pass is
// marked untraced on its own record, and that mark is said here, with the
// role as the one to move: the problem is the role's to fix on its next pass,
// not a request to a person.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// UntracedPass is one task whose last pass that took a turn reported findings
// and left no trace of them: which task, which role, when, and how many.
type UntracedPass struct {
	Task string           `json:"task"`
	Role domain.AgentRole `json:"role"`
	// StartedAt is when the untraced pass began, which is how long its findings
	// have gone untraced.
	StartedAt time.Time `json:"started_at"`
	// Findings is how many findings the pass reported; First is the first of
	// them, in the role's own words, so the entry says what was found.
	Findings int    `json:"findings"`
	First    string `json:"first,omitempty"`
}

// Says is the entry as a sentence.
func (u UntracedPass) Says() string {
	what := fmt.Sprintf("the pass of %s at %s reported %d finding(s) and left no trace of them: no memory written, no lane report changed, no report filed, no work admitted",
		u.Task, u.StartedAt.UTC().Format(time.RFC3339), u.Findings)
	if first := strings.TrimSpace(u.First); first != "" {
		what += "; the first: " + singleLine(first, maxRefusalBytes)
	}
	return what
}

// UntracedPassesOf reads, for each task, whether its last pass that took a
// turn is marked untraced. A record that took no turn — a miss, a firing
// refused before its turn, a wait on the provider — asked the role nothing and
// neither raises nor clears it; the next pass that takes a turn clears it,
// because that pass was told the findings and its own record says whether it
// left a trace this time. The result is in task name order.
func UntracedPassesOf(passes []runstate.Sweep) []UntracedPass {
	latest := map[string]runstate.Sweep{}
	for _, pass := range passes {
		if pass.Turns == 0 {
			continue
		}
		if held, ok := latest[pass.Task]; !ok || !pass.StartedAt.Before(held.StartedAt) {
			latest[pass.Task] = pass
		}
	}
	var untraced []UntracedPass
	for task, pass := range latest {
		if !pass.Untraced {
			continue
		}
		entry := UntracedPass{Task: task, Role: pass.Role, StartedAt: pass.StartedAt}
		if pass.Result != nil {
			entry.Findings = len(pass.Result.Findings)
			if entry.Findings > 0 {
				entry.First = pass.Result.Findings[0].Issue
			}
		}
		untraced = append(untraced, entry)
	}
	sort.Slice(untraced, func(first, second int) bool { return untraced[first].Task < untraced[second].Task })
	return untraced
}

// ReadUntracedPasses reads the sweep log and derives the untraced passes from
// it. A reading with no log wired says nothing; a log that cannot be read says
// so rather than reporting every pass traced.
func ReadUntracedPasses(sources Sources) ([]UntracedPass, string) {
	if sources.Passes == nil {
		return nil, ""
	}
	passes, _, err := sources.Passes.List()
	if err != nil && len(passes) == 0 {
		return nil, fmt.Sprintf("the recurring tasks' passes could not be read to say which left no trace: %v", err)
	}
	var problem string
	if err != nil {
		problem = fmt.Sprintf("the recurring tasks' passes could only be read in part to say which left no trace: %v", err)
	}
	return UntracedPassesOf(passes), problem
}

// untracedPassAttention is an untraced pass as the attention line carries it.
func untracedPassAttention(untraced UntracedPass) Attention {
	return resolved(Attention{Kind: AttentionUntracedPass, ID: untraced.Task, UntracedPass: &untraced})
}
