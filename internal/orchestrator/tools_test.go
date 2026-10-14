package orchestrator

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/toolcatalog"
)

// A run reads the report and amendment blocks from its developer's and its
// reviewer's replies and nothing else, so the tools section a run sends may
// name no other block: one it named would be an ability the role is told it has
// and the run silently ignores.
func TestARunsToolsNameOnlyBlocksTheRunReads(t *testing.T) {
	t.Parallel()
	read := []string{strings.TrimPrefix(report.Fence, "```"), strings.TrimPrefix(amendment.Fence, "```")}
	block := regexp.MustCompile("`(yoyodyne-[a-z-]+)`")
	for role, want := range map[domain.AgentRole][]string{
		domain.RoleDeveloper: {"report.file", "amendment.propose"},
		domain.RoleReviewer:  {"report.file"},
	} {
		contract := toolcatalog.RunContract(role)
		for _, match := range block.FindAllStringSubmatch(contract, -1) {
			if !slices.Contains(read, match[1]) {
				t.Errorf("%s run instructions name block %q, which no run reads", role, match[1])
			}
		}
		var got []string
		for _, registered := range toolcatalog.RunGranted(role) {
			got = append(got, registered.Name)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s run tools = %v, want %v", role, got, want)
		}
		if !strings.Contains(toolcatalog.Contract(role), "yoyodyne-tracker") || strings.Contains(contract, "yoyodyne-tracker") {
			t.Errorf("%s: the tracker read belongs to its conversation and not to its run", role)
		}
	}
}
