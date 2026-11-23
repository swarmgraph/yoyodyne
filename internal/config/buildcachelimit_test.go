package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestTheBuildCacheLimitIsFortyGiBUnlessTheProjectSetsIt(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if got := inherited.Config.Execution.BuildCacheLimitBytes(); got != 40<<30 {
		t.Fatalf("inherited limit = %d bytes, want 40 GiB", got)
	}
	if origin := inherited.Origins["execution.build_cache_limit_gib"]; origin != BuiltinV1 {
		t.Fatalf("origin = %q, want %q", origin, BuiltinV1)
	}
	if got := (Execution{}).BuildCacheLimitBytes(); got != 40<<30 {
		t.Fatalf("a configuration built in code reads the limit as %d bytes, want 40 GiB", got)
	}
	for _, want := range []int{5, 100, 1000} {
		set := loadProject(t, fmt.Sprintf("%sexecution:\n  build_cache_limit_gib: %d\n", minimalProjectConfig, want), nil)
		if got := set.Config.Execution.BuildCacheLimitBytes(); got != int64(want)<<30 {
			t.Fatalf("set to %d: limit = %d bytes", want, got)
		}
	}
	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	if got := generated.Execution.BuildCacheLimitGiB; got != 40 {
		t.Fatalf("generated limit = %d, want 40", got)
	}
}

func TestABuildCacheLimitOutsideItsBoundsIsRefused(t *testing.T) {
	t.Parallel()

	for _, value := range []int{-1, 0, 4, 1001} {
		_, err := loadProjectError(t, fmt.Sprintf("%sexecution:\n  build_cache_limit_gib: %d\n", minimalProjectConfig, value), nil)
		if err == nil || !strings.Contains(err.Error(), "must be at least 5 and at most 1000 GiB") {
			t.Fatalf("%d: error = %v, want it refused naming the bounds", value, err)
		}
	}
}

func TestARecurringTaskNamedForTheBuildCacheTrimIsRefused(t *testing.T) {
	t.Parallel()

	for _, name := range []string{BuildCacheTaskName, BuildCacheWarningName} {
		problems := RecurringTask{}.problems(name, nil)
		if !strings.Contains(strings.Join(problems, "\n"), "trims of the shared build cache") {
			t.Fatalf("a task named %q: problems = %v", name, problems)
		}
	}
}
