package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestBuiltInExecutableOverridePreservesTheCompiledProviderContract(t *testing.T) {
	t.Parallel()
	for _, named := range []domain.Backend{domain.BackendCodex, domain.BackendClaudeCode} {
		cfg, err := Decode(strings.NewReader(validBootstrapConfig + "providers:\n  " + string(named) + ":\n    binary: '/absolute/path/Desktop App/CLI'\n"))
		if err != nil {
			t.Fatal(err)
		}
		registry, err := cfg.ProviderRegistry()
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := registry.Lookup(named)
		want, _ := backend.BuiltInDescriptor(named)
		want.Binary = "/absolute/path/Desktop App/CLI"
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("override changed the compiled contract: %+v, want %+v", actual, want)
		}
	}
}

func TestBuiltInBinaryOverrideCannotReplaceAnyOtherProviderProperty(t *testing.T) {
	t.Parallel()
	for _, extra := range []string{"    adapter: claude-code\n", "    roles: [developer]\n", "    postures: [worktree-write]\n", "    capabilities: {tool_control: true}\n", "    dialect: {rules: [{answer: retrying, type: retry}]}\n"} {
		_, err := Decode(strings.NewReader(validBootstrapConfig + "providers:\n  codex:\n    binary: '/CLI'\n" + extra))
		if err == nil || !strings.Contains(err.Error(), "only a non-empty binary override") {
			t.Fatalf("property %q accepted: %v", extra, err)
		}
	}
	_, err := Decode(strings.NewReader(validBootstrapConfig + "providers:\n  codex:\n    binary: ' '\n"))
	if err == nil {
		t.Fatal("empty override accepted")
	}
}
