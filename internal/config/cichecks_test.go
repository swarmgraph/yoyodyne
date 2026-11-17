package config

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const ciProjectChecks = minimalProjectConfig + "checks:\n  - make fmtcheck\n  - make test\n"

func TestChecksOnCIAreOffByDefaultAndShownWithTheirOrigin(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	if resolved.Config.Execution.CIChecks.Enabled {
		t.Fatal("a project that wrote nothing has checks on CI enabled")
	}
	asYAML, err := yaml.Marshal(resolved.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(asYAML), "ci_checks:") || !strings.Contains(string(asYAML), "enabled: false") {
		t.Errorf("the effective configuration does not show the setting:\n%s", asYAML)
	}
	for _, key := range []string{"enabled", "wait_limit"} {
		if origin := resolved.Origins["execution.ci_checks."+key]; origin != OriginDefault {
			t.Errorf("origin of %s = %q, want %q", key, origin, OriginDefault)
		}
	}
	if got := resolved.Config.Execution.CIChecks.WaitLimitInForce(); got != DefaultCIWaitLimit.Duration() {
		t.Errorf("wait limit in force = %s, want the default", got)
	}
}

func TestADisabledSettingStillRefusesANameNoCheckDeclares(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, ciProjectChecks+"execution:\n  ci_checks:\n    local: [make fmtcheck, make nothing]\n", nil)
	if err == nil || !strings.Contains(err.Error(), `"make nothing"`) {
		t.Fatalf("an undeclared local check was accepted or not named: %v", err)
	}
	ok := loadProject(t, ciProjectChecks+"execution:\n  ci_checks:\n    local: [make fmtcheck]\n    required: [test]\n    wait_limit: 30m\n", nil)
	ci := ok.Config.Execution.CIChecks
	if ci.Enabled || len(ci.Local) != 1 || len(ci.Required) != 1 || ci.WaitLimit.Duration().Minutes() != 30 {
		t.Fatalf("ci_checks = %+v", ci)
	}
	for _, key := range []string{"local", "required", "wait_limit"} {
		if origin := ok.Origins["execution.ci_checks."+key]; origin == "" || origin == OriginDefault {
			t.Errorf("origin of %s = %q, want the file that set it", key, origin)
		}
	}
	if origin := ok.Origins["execution.ci_checks.enabled"]; origin != OriginDefault {
		t.Errorf("origin of enabled = %q, want %q", origin, OriginDefault)
	}
}

func TestEnablingChecksOnCIRefusesAnEmptyRequiredList(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, ciProjectChecks+"execution:\n  ci_checks:\n    enabled: true\n", nil)
	if err == nil || !strings.Contains(err.Error(), "execution.ci_checks.required") {
		t.Fatalf("enabled with no required checks: %v", err)
	}
}

func TestEnablingChecksOnCINamesWhatTheForgeAdapterCannotDo(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, ciProjectChecks+"execution:\n  ci_checks:\n    enabled: true\n    required: [test]\n", nil)
	if err == nil || !strings.Contains(err.Error(), string(ForgeOpenDraftRequest)) {
		t.Fatalf("the missing capability was not named: %v", err)
	}
	for _, present := range []ForgeCapability{ForgePushBranch, ForgeReadCheckResults, ForgeReadCheckOutput} {
		if strings.Contains(err.Error(), string(present)) {
			t.Errorf("the refusal names %q, which the adapter can do: %v", present, err)
		}
	}
}

func TestChecksOnCIPassValidationWhenTheAdapterCanDoEverything(t *testing.T) {
	// Not parallel: it swaps the adapter table.
	saved := forgeAdapterCapabilities["github"]
	defer func() { forgeAdapterCapabilities["github"] = saved }()
	forgeAdapterCapabilities["github"] = map[ForgeCapability]bool{
		ForgePushBranch: true, ForgeOpenDraftRequest: true, ForgeReadCheckResults: true, ForgeReadCheckOutput: true,
	}
	resolved := loadProject(t, ciProjectChecks+"execution:\n  ci_checks:\n    enabled: true\n    local: [make fmtcheck]\n    required: [test]\n", nil)
	if !resolved.Config.Execution.CIChecks.Enabled {
		t.Fatal("enabled did not reach the effective configuration")
	}
	if origin := resolved.Origins["execution.ci_checks.enabled"]; origin == OriginDefault {
		t.Errorf("origin of enabled = %q", origin)
	}
}

// With the setting absent or left at its defaults, the form the configuration
// revision digests carries nothing of it, so every record that stores the
// revision is what it was before the key existed.
func TestADefaultSettingLeavesTheConfigurationRevisionUnchanged(t *testing.T) {
	t.Parallel()

	absent := loadProject(t, minimalProjectConfig, nil).Config
	written := loadProject(t, minimalProjectConfig+"execution:\n  ci_checks:\n    enabled: false\n    wait_limit: 1h\n", nil).Config
	for name, cfg := range map[string]Config{"absent": absent, "written at defaults": written} {
		encoded, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "ci_checks") {
			t.Errorf("%s: the digested form carries ci_checks: %s", name, encoded)
		}
	}
	if absent.Revision() != written.Revision() {
		t.Error("writing the defaults changed the configuration revision")
	}
	set := loadProject(t, ciProjectChecks+"execution:\n  ci_checks:\n    required: [test]\n", nil).Config
	plain := loadProject(t, ciProjectChecks, nil).Config
	if set.Revision() == plain.Revision() {
		t.Error("naming required checks did not change the configuration revision")
	}
}
