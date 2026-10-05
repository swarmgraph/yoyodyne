package backend

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestCodexEffortMatchesTheRecordedCLICatalog(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/codex-cli-0.159.2-effort.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []struct {
			Slug    string `json:"slug"`
			Default string `json:"default_reasoning_level"`
			Levels  []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) == 0 || len(catalog.Models) != len(codexModelEfforts) {
		t.Fatal("the effort policy must cover the recorded catalog")
	}
	descriptor, _ := BuiltInDescriptor(domain.BackendCodex)
	for _, model := range catalog.Models {
		policy := descriptor.ForModel(model.Slug)
		var levels []string
		for _, level := range model.Levels {
			levels = append(levels, level.Effort)
			if !policy.AcceptsEffort(level.Effort) {
				t.Fatalf("%s does not accept advertised level %s", model.Slug, level.Effort)
			}
		}
		if !reflect.DeepEqual(policy.EffortLevels, levels) || policy.DefaultEffort != model.Default || !policy.AcceptsEffort(model.Default) {
			t.Fatalf("%s policy = %+v, want levels %v and default %q", model.Slug, policy, levels, model.Default)
		}
	}
}

func TestCodexInvocationKeepsAnOmittedEffortAndDescribesItsSource(t *testing.T) {
	t.Parallel()
	descriptor, _ := BuiltInDescriptor(domain.BackendCodex)
	for _, test := range []struct {
		requested, resolved string
		reported            bool
		want                string
	}{
		{"high", "", false, "high, from the agent"},
		{"high", "medium", true, "high, from the agent"},
		{"", "high", true, "high, from the Codex configuration"},
		{"", "", false, "not reported, from the Codex configuration"},
		{"", "high", false, "not reported, from the Codex configuration"},
	} {
		if got := descriptor.InvocationEffort("gpt-6.1-sol", test.requested); got != test.requested {
			t.Fatalf("requested %q became %q", test.requested, got)
		}
		if got := descriptor.DescribeInvocationEffort(test.requested, test.resolved, test.reported); got != test.want {
			t.Fatalf("description = %q, want %q", got, test.want)
		}
	}
	claude, _ := BuiltInDescriptor(domain.BackendClaudeCode)
	if claude.InvocationEffort("opus", "") != "" || claude.DescribeInvocationEffort("high", "", false) != "" {
		t.Fatal("Claude's omitted effort and records must stay unchanged")
	}
}
