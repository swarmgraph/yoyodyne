package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Team mode is off unless the configuration turns it on, and the key is one
// this build says it reads.
func TestTeamModeIsOffUnlessTurnedOn(t *testing.T) {
	t.Parallel()
	load := func(body string) Config {
		t.Helper()
		path := filepath.Join(t.TempDir(), FileName)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		resolved, err := LoadResolved(path)
		if err != nil {
			t.Fatalf("LoadResolved() error = %v", err)
		}
		return resolved.Config
	}
	base := "version: 1\nextends: builtin:v1\nproduct:\n  id: calc\n  repository: /somewhere/calc\n"
	if load(base).Team.Enabled {
		t.Errorf("team mode is on in a configuration that does not mention it")
	}
	if !load(base + "team:\n  enabled: true\n").Team.Enabled {
		t.Errorf("team.enabled: true did not turn team mode on")
	}
	if !slices.Contains(SchemaKeys(), "team.enabled") {
		t.Errorf("SchemaKeys() lacks team.enabled")
	}
}
