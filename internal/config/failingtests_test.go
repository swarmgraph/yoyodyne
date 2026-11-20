package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/checks"
)

func TestACheckMayDeclareHowItsOutputNamesItsFailingTests(t *testing.T) {
	resolved := resolveFixture(t, `
checks:
  - make test CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"
  - make vet
failing_tests:
  - checks: [make test]
    test: '^\s*--- FAIL: (?P<test>[^\s/]+)'
    file: '^\s+(?P<file>[^\s/:]+_test\.go):[0-9]+: '
    group: '^FAIL\s+(?P<directory>\S+)'
    strip_prefix: example.com/shop
`)
	readings := resolved.Config.FailingTests
	if len(readings) != 1 || readings[0].StripPrefix != "example.com/shop" {
		t.Fatalf("failing_tests = %#v", readings)
	}
	if origin := resolved.Origins["failing_tests"]; !strings.Contains(origin, "config.yaml") {
		t.Fatalf("failing_tests origin = %q, want the project file", origin)
	}
	if _, ok := checks.ReadingFor(readings, resolved.Config.Checks[0]); !ok {
		t.Fatal("the configured make test check has no reading")
	}
	// A check the configuration declares nothing for is read by nothing.
	if _, ok := checks.ReadingFor(readings, "make vet"); ok {
		t.Fatal("make vet, which no reading names, was given one")
	}
}

func TestNoFailingTestReadingDeclaredReadsNothing(t *testing.T) {
	resolved := resolveFixture(t, `
checks:
  - make test
`)
	if len(resolved.Config.FailingTests) != 0 {
		t.Fatalf("failing_tests = %#v, want none", resolved.Config.FailingTests)
	}
	if _, ok := checks.ReadingFor(resolved.Config.FailingTests, "make test"); ok {
		t.Fatal("a check was given a reading nobody declared")
	}
}

func TestAFailingTestReadingThatCannotBeUsedIsRefused(t *testing.T) {
	_, err := resolveFixtureErr(t, `
checks:
  - make test
failing_tests:
  - checks: [make tset]
    test: '^--- FAIL: (?P<test>\S+)'
    file: '(?P<file>\S+_test\.go)'
  - checks: [make test]
    test: '^--- FAIL: (\S+)'
    file: '(?P<file>\S+_test\.go)'
`)
	if err == nil {
		t.Fatal("unusable readings were accepted")
	}
	for _, want := range []string{
		`failing_tests[0] names "make tset", which is no configured check`,
		`failing_tests[1] has a test expression with no (?P<test>...) group`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to say %q", err, want)
		}
	}
}

// This repository's own declaration reads go test's output: each failing test
// with its file, relative to the repository's top.
func TestThisProjectsGoChecksNameTheirFailingTestsWithTheirFiles(t *testing.T) {
	t.Parallel()
	cfg, err := Load("../../.yoyodyne/config.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, command := range []string{
		`make test CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"`,
		`make race CHANGED_GO_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"`,
		"make race",
		"make test-macos",
	} {
		reading, ok := checks.ReadingFor(cfg.FailingTests, command)
		if !ok {
			t.Fatalf("%s has no reading", command)
		}
		got := checks.ReadFailingTests(reading, "--- FAIL: TestQueue (0.00s)\n    --- FAIL: TestQueue/held (0.00s)\n        queue_test.go:40: held\nFAIL\nFAIL\tgithub.com/mason-bryant/yoyodyne/internal/runstate\t3.1s\n--- FAIL: TestRoot (0.00s)\n    root_test.go:3: no\nFAIL\tgithub.com/mason-bryant/yoyodyne\t0.1s\nFAIL\n")
		want := []checks.FailingTest{{Name: "TestQueue", File: "internal/runstate/queue_test.go"}, {Name: "TestRoot", File: "root_test.go"}}
		if !reflect.DeepEqual(got.Tests, want) || got.Unread != "" {
			t.Fatalf("%s read %#v, want %#v", command, got, want)
		}
	}
	if _, ok := checks.ReadingFor(cfg.FailingTests, "make vet"); ok {
		t.Fatal("make vet, which prints no test, was given a reading")
	}
}
