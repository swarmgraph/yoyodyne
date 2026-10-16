package config

// execution.merge_queue: one switch, off unless a file turns it on, shown with
// the file that turned it on, and refused for anything but a boolean.

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestTheMergeQueueIsOffUnlessAFileTurnsItOn(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if inherited.Config.Execution.MergeQueue {
		t.Fatal("a project that wrote nothing integrates through the merge queue")
	}
	if origin := inherited.Origins["execution.merge_queue"]; origin != OriginDefault {
		t.Errorf("origin = %q, want %q", origin, OriginDefault)
	}
	if generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config; generated.Execution.MergeQueue {
		t.Error("the configuration yoyo init writes switches the merge queue on")
	}

	on := loadProject(t, minimalProjectConfig+"execution:\n  merge_queue: true\n", nil)
	if !on.Config.Execution.MergeQueue {
		t.Fatal("merge_queue: true did not reach the effective configuration")
	}
	if origin := on.Origins["execution.merge_queue"]; origin == "" || origin == OriginDefault {
		t.Errorf("origin = %q, want the file that switched it on", origin)
	}
	// What `config show --effective` renders names the switch either way.
	for _, cfg := range []Config{inherited.Config, on.Config} {
		encoded, err := yaml.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), "merge_queue: "+map[bool]string{true: "true", false: "false"}[cfg.Execution.MergeQueue]) {
			t.Errorf("the effective configuration does not show merge_queue:\n%s", encoded)
		}
	}
}

func TestTheMergeQueueSwitchIsABoolean(t *testing.T) {
	t.Parallel()

	if _, err := loadProjectError(t, minimalProjectConfig+"execution:\n  merge_queue: github\n", nil); err == nil {
		t.Fatal("merge_queue: github was accepted; the switch is on or off and names no queue")
	}
}
