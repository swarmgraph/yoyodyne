package config

import (
	"strings"
	"testing"
	"time"
)

// The reading helper's bounds: the defaults a project stating nothing gets,
// values inside the bounds loading as written, and each value outside them
// refused at load.

func TestAProjectStatingNothingGetsTheReadingHelpersDefaults(t *testing.T) {
	t.Parallel()

	ask := loadProject(t, minimalProjectConfig, nil).Config.Execution.RepositoryAsk
	if got := ask.Limit(); got != 5*time.Minute {
		t.Fatalf("time limit = %s, want 5 minutes", got)
	}
	if got := ask.AnswerLimit(); got != 4096 {
		t.Fatalf("answer limit = %d, want 4 KiB", got)
	}
}

func TestTheReadingHelpersBoundsLoadAtTheirLimits(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		yaml   string
		limit  time.Duration
		answer int
	}{
		{yaml: "time_limit: 15m\n    answer_bytes: 8192", limit: 15 * time.Minute, answer: 8192},
		{yaml: "time_limit: 30s\n    answer_bytes: 1024", limit: 30 * time.Second, answer: 1024},
		{yaml: "time_limit: 2m", limit: 2 * time.Minute, answer: 4096},
	} {
		resolved := loadProject(t, minimalProjectConfig+"execution:\n  repository_ask:\n    "+test.yaml+"\n", nil)
		ask := resolved.Config.Execution.RepositoryAsk
		if ask.Limit() != test.limit || ask.AnswerLimit() != test.answer {
			t.Fatalf("%q loaded as %s and %d bytes, want %s and %d", test.yaml, ask.Limit(), ask.AnswerLimit(), test.limit, test.answer)
		}
	}
}

func TestTheReadingHelpersValuesOutsideTheirBoundsAreRefusedAtLoad(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		yaml string
		want string
	}{
		{yaml: "time_limit: 16m", want: "execution.repository_ask.time_limit is 16m0s; it must be between 30s and 15m0s"},
		{yaml: "time_limit: 10s", want: "execution.repository_ask.time_limit is 10s; it must be between 30s and 15m0s"},
		{yaml: "time_limit: -1m", want: "execution.repository_ask.time_limit is -1m0s"},
		{yaml: "answer_bytes: 8193", want: "execution.repository_ask.answer_bytes is 8193; it must be between 1024 and 8192"},
		{yaml: "answer_bytes: 512", want: "execution.repository_ask.answer_bytes is 512; it must be between 1024 and 8192"},
		{yaml: "answer_bytes: -4", want: "execution.repository_ask.answer_bytes is -4"},
	} {
		_, err := loadProjectError(t, minimalProjectConfig+"execution:\n  repository_ask:\n    "+test.yaml+"\n", nil)
		if err == nil {
			t.Fatalf("%q loaded, want it refused", test.yaml)
		}
		if !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%q was refused with %v\nwant it to say %q", test.yaml, err, test.want)
		}
	}
}

func TestTheDevelopmentManagerMayApplyTheReadingHelpersBounds(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"execution.repository_ask.time_limit", "execution.repository_ask.answer_bytes"} {
		if err := RoleMayApply(key); err != nil {
			t.Fatalf("RoleMayApply(%q) = %v, want it applicable", key, err)
		}
	}
}
