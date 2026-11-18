package runstate

// When the forge's required checks count as a merge queue generation's full
// check (docs/designs/integration-through-a-merge-queue.md): only for exactly
// the candidate, with every required check passed, the workflow files as they
// were read, and the target never off the candidate's base.

import (
	"strings"
	"testing"
	"time"
)

var ciRequired = []string{"build", "test"}

const ciDigest = "sha256:workflows-v1"

func ciReadGeneration(t *testing.T) MergeQueueGeneration {
	t.Helper()
	generation := verifiedGeneration(t)
	generation.CIChecks = &MergeQueueCIEvidence{
		Binding: generation.Binding(), ReadAt: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC),
		Commit: generation.Candidate, Base: generation.TargetBase, TargetAtRead: generation.TargetBase,
		Required: ciRequired, WorkflowDigest: ciDigest,
	}
	for _, name := range ciRequired {
		generation.CIChecks.Checks = append(generation.CIChecks.Checks, CICheckEvidence{Name: name, Result: "passed", Commit: generation.Candidate})
	}
	return generation
}

func TestTheForgesChecksCountOnlyWhenEveryConditionHolds(t *testing.T) {
	t.Parallel()

	generation := ciReadGeneration(t)
	if err := generation.validate(); err != nil {
		t.Fatalf("validate() = %v, want a whole record", err)
	}
	if counts, why := generation.CIChecksCount(ciRequired, ciDigest, generation.TargetBase); !counts {
		t.Fatalf("CIChecksCount() = false (%s), want the checks to count", why)
	}

	refusals := map[string]struct {
		change func(*MergeQueueGeneration)
		target string
		digest string
		need   []string
		want   string
	}{
		"never read":             {change: func(g *MergeQueueGeneration) { g.CIChecks = nil }, want: "never read"},
		"read for the head":      {change: func(g *MergeQueueGeneration) { g.CIChecks.Commit = g.Heads[0] }, want: "other than the candidate"},
		"another generation":     {change: func(g *MergeQueueGeneration) { g.CIChecks.Binding = strings.Repeat("0", 64) }, want: "another generation"},
		"a check missing":        {change: func(g *MergeQueueGeneration) { g.CIChecks.Checks = g.CIChecks.Checks[:1] }, want: "required checks changed"},
		"a check failed":         {change: func(g *MergeQueueGeneration) { g.CIChecks.Checks[1].Result = "failed" }, want: "test did not pass"},
		"a check pending":        {change: func(g *MergeQueueGeneration) { g.CIChecks.Checks[1].Result = "pending" }, want: "test did not pass"},
		"a check neutral":        {change: func(g *MergeQueueGeneration) { g.CIChecks.Checks[0].Result = "other" }, want: "build did not pass"},
		"a check cancelled":      {change: func(g *MergeQueueGeneration) { g.CIChecks.Checks[0].Result = "cancelled" }, want: "build did not pass"},
		"a check skipped":        {change: func(g *MergeQueueGeneration) { g.CIChecks.Checks[0].Result = "skipped" }, want: "build did not pass"},
		"a check not started":    {change: func(g *MergeQueueGeneration) { g.CIChecks.Checks[0].Result = "not started" }, want: "build did not pass"},
		"a check ran elsewhere":  {change: func(g *MergeQueueGeneration) { g.CIChecks.Checks[0].Commit = g.Heads[0] }, want: "build did not pass"},
		"no workflow digest":     {change: func(g *MergeQueueGeneration) { g.CIChecks.WorkflowDigest = "" }, want: "no digest"},
		"workflows changed":      {digest: "sha256:workflows-v2", want: "workflow files changed"},
		"a new required check":   {need: []string{"build", "test", "lint"}, want: "required checks changed"},
		"a dropped required one": {need: []string{"build"}, want: "required checks changed"},
		"moved before the read":  {change: func(g *MergeQueueGeneration) { g.CIChecks.TargetAtRead = strings.Repeat("e", 40) }, want: "moved off"},
		"moved since the read":   {target: strings.Repeat("e", 40), want: "moved off"},
		"built ahead": {change: func(g *MergeQueueGeneration) {
			g.BuiltAhead = &MergeQueueAhead{EntryID: g.EntryID, Generation: 1, Candidate: g.TargetBase, OnTarget: g.TargetBase}
			g.CIChecks.Binding = g.Binding()
		}, want: "built ahead"},
	}
	for name, refusal := range refusals {
		t.Run(name, func(t *testing.T) {
			g := ciReadGeneration(t)
			if refusal.change != nil {
				refusal.change(&g)
			}
			target, digest, need := g.TargetBase, ciDigest, ciRequired
			if refusal.target != "" {
				target = refusal.target
			}
			if refusal.digest != "" {
				digest = refusal.digest
			}
			if refusal.need != nil {
				need = refusal.need
			}
			counts, why := g.CIChecksCount(need, digest, target)
			if counts || !strings.Contains(why, refusal.want) {
				t.Fatalf("CIChecksCount() = %v, %q; want it refused for %q", counts, why, refusal.want)
			}
		})
	}
}

func TestAForgeReadingNamingAnotherGenerationIsNeverRecorded(t *testing.T) {
	t.Parallel()

	generation := ciReadGeneration(t)
	generation.CIChecks.Binding = strings.Repeat("0", 64)
	if err := generation.validate(); err == nil || !strings.Contains(err.Error(), "forge check reading") {
		t.Fatalf("validate() = %v, want the reading refused", err)
	}
}
