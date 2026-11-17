package config

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const ciProjectChecks = minimalProjectConfig + "checks:\n  - make fmtcheck\n  - make test\n"

func TestChecksOnCIAreOffAndAbsentFromTheSerializedConfigurationByDefault(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	if resolved.Config.Execution.CIChecks.Enabled {
		t.Fatal("a project that wrote nothing has checks on CI enabled")
	}
	// Absent means today's records: the serialized form, and so the revision
	// that names it, carries nothing of the setting.
	asJSON, _ := json.Marshal(resolved.Config)
	asYAML, err := yaml.Marshal(resolved.Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(asJSON), "ci_checks") || strings.Contains(string(asYAML), "ci_checks") {
		t.Errorf("a disabled setting is serialized:\n%s\n%s", asJSON, asYAML)
	}
	for _, key := range []string{"enabled", "local", "required", "wait_limit"} {
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
