package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestSchemaKeysNameWhatTheDecoderReads(t *testing.T) {
	keys := SchemaKeys()
	for _, want := range []string{
		"agents",
		"agents.*",
		"agents.*.effort",
		"agents.*.failover.model",
		"agents.*.failover.effort",
		"execution.work_poll",
		"execution.developer_slots.[]",
		"execution.developer_slots.[].number",
		"execution.developer_slots.[].routing.enabled",
		"execution.developer_slots.[].routing.primary.provider",
		"execution.developer_slots.[].routing.primary.model_version",
		"execution.developer_slots.[].routing.alternate.account",
		"execution.developer_slots.[].routing.alternate.effort",
		"services.dashboard.token",
		"recurring_tasks.*",
	} {
		if !slices.Contains(keys, want) {
			t.Errorf("SchemaKeys() lacks %q", want)
		}
	}
	// A duration is decoded whole, so nothing under it is a key.
	for _, key := range keys {
		if len(key) > len("execution.work_poll.") && key[:len("execution.work_poll.")] == "execution.work_poll." {
			t.Errorf("SchemaKeys() looks inside a duration: %q", key)
		}
	}
}

// The incident this exists for: a file carrying agents.<name>.effort, read
// against a build from before the key.
func TestUnreadableKeysNamesWhatAnOlderBuildWouldRefuse(t *testing.T) {
	older := slices.DeleteFunc(SchemaKeys(), func(key string) bool { return key == "agents.*.effort" })
	source := []byte(`version: 1
product:
  id: example
agents:
  developer:
    role: developer
    model: opus
    effort: medium
  reviewer:
    role: reviewer
    effort: high
    failover:
      enabled: true
      model: sonnet
execution:
  work_poll: 30s
`)
	unreadable, err := UnreadableKeys(source, older)
	if err != nil {
		t.Fatalf("UnreadableKeys() error = %v", err)
	}
	want := []string{"agents.developer.effort", "agents.reviewer.effort"}
	if !reflect.DeepEqual(unreadable, want) {
		t.Fatalf("UnreadableKeys() = %v, want %v", unreadable, want)
	}
	current, err := UnreadableKeys(source, SchemaKeys())
	if err != nil {
		t.Fatalf("UnreadableKeys() error = %v", err)
	}
	if len(current) != 0 {
		t.Fatalf("UnreadableKeys() against this build = %v, want none", current)
	}
}

// A section the older build does not know is named once, as the key the
// decoder stops on, rather than with every key under it.
func TestUnreadableKeysStopsAtTheUnknownSection(t *testing.T) {
	older := slices.DeleteFunc(SchemaKeys(), func(key string) bool {
		return key == "services" || len(key) > len("services.") && key[:len("services.")] == "services."
	})
	source := []byte("services:\n  dashboard:\n    enabled: true\n    port: 8765\n")
	unreadable, err := UnreadableKeys(source, older)
	if err != nil {
		t.Fatalf("UnreadableKeys() error = %v", err)
	}
	if !reflect.DeepEqual(unreadable, []string{"services"}) {
		t.Fatalf("UnreadableKeys() = %v, want [services]", unreadable)
	}
}

func TestUnreadableKeysExpandsMergesAtTheirDestination(t *testing.T) {
	older := slices.DeleteFunc(SchemaKeys(), func(key string) bool { return key == "agents.*.effort" })
	for _, test := range []struct {
		name   string
		source string
		want   []string
	}{
		{"inline merge", "agents: {developer: {<<: {role: developer, model: opus, effort: medium}}}", []string{"agents.developer.effort"}},
		{"alias merge", "agents:\n  developer: &defaults {role: developer, effort: medium}\n  reviewer: {<<: *defaults, role: reviewer}\n", []string{"agents.developer.effort", "agents.reviewer.effort"}},
		{"explicit mapping replaces merged mapping", "<<: {agents: {developer: {effort: medium}}}\nagents: {developer: {role: developer}}\n", nil},
		{"first merge mapping wins", "agents: {<<: [{developer: {role: developer}}, {developer: {effort: medium}}]}", nil},
		{"first merge mapping carries the key", "agents: {<<: [{developer: {effort: medium}}, {developer: {role: developer}}]}", []string{"agents.developer.effort"}},
		{"quoted merge name is an ordinary key", "agents: {developer: {'<<': {effort: medium}}}", []string{"agents.developer.<<"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := UnreadableKeys([]byte(test.source), older)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("UnreadableKeys() = %v, %v; want %v", got, err, test.want)
			}
		})
	}
	if _, err := UnreadableKeys([]byte("agents: {developer: {<<: medium}}"), older); err == nil {
		t.Fatal("an invalid merge was reported as readable")
	}
}

// Every key the shipped template writes and every key this project's own file
// carries is one this build reads: were the derivation to miss a key the
// decoder accepts, every running service would be reported unable to read it.
func TestSchemaKeysReadEveryKeyTheTemplateAndThisProjectWrite(t *testing.T) {
	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: "example", Repository: "."})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	sources := map[string][]byte{"the scaffolded configuration": scaffold.Config.Content}
	if project, err := os.ReadFile(filepath.Join("..", "..", ".yoyodyne", FileName)); err == nil {
		sources["this project's configuration"] = project
	}
	if bundle, err := os.ReadFile(filepath.Join("builtin", "v1", "bundle.yaml")); err == nil {
		sources["the built-in bundle"] = bundle
	}
	for name, source := range sources {
		unreadable, err := UnreadableKeys(source, SchemaKeys())
		if err != nil {
			t.Fatalf("UnreadableKeys(%s) error = %v", name, err)
		}
		if len(unreadable) != 0 {
			t.Errorf("SchemaKeys() does not read %v, which %s carries", unreadable, name)
		}
	}
}
