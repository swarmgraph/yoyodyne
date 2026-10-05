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
		if !reflect.DeepEqual(policy.EffortLevels, levels) || policy.InvocationEffort(model.Slug, "") != model.Default || !policy.AcceptsEffort(model.Default) {
			t.Fatalf("%s policy = %+v, want levels %v and default %q", model.Slug, policy, levels, model.Default)
		}
	}
}
