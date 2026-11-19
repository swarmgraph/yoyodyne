package config

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const slackMCP = `mcp:
  servers:
    slack:
      url: https://mcp.example.com/
      credential: file
      tools:
        post_message: act
        read_thread: read
        search: read
`

const reviewThreadsSection = `review_threads:
  server: slack
  post_tool: post_message
  read_tool: read_thread
  channel: C0123456789
  reviewers: [U0AAAA1111, U0BBBB2222]
  check_every: 30m
`

func reviewThreadsProject(mcp, section string) string {
	return validBootstrapConfig + mcp + section
}

func TestAProjectWithNoReviewThreadsSectionIsUnchanged(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.ReviewThreads.Configured() {
		t.Fatalf("a file with no review_threads section has %#v", cfg.ReviewThreads)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "review_threads") {
		t.Fatalf("a configuration with no section serializes the key: %s", encoded)
	}
	empty, err := Decode(strings.NewReader(validBootstrapConfig + "review_threads: {}\n"))
	if err != nil {
		t.Fatalf("Decode() of an empty section error = %v", err)
	}
	if empty.Revision() != cfg.Revision() {
		t.Fatalf("an empty section moved the revision from %s to %s", cfg.Revision(), empty.Revision())
	}
}

func TestTheReviewThreadsSectionLoads(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(reviewThreadsProject(slackMCP, reviewThreadsSection)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	got := cfg.ReviewThreads
	if got.Server != "slack" || got.PostTool != "post_message" || got.ReadTool != "read_thread" ||
		got.Channel != "C0123456789" || len(got.Reviewers) != 2 || got.CheckEvery != Duration(30*time.Minute) {
		t.Fatalf("the section loaded as %#v", got)
	}
}

func TestCheckEveryDefaultsToFifteenMinutes(t *testing.T) {
	t.Parallel()

	section := strings.Replace(reviewThreadsSection, "  check_every: 30m\n", "", 1)
	cfg, err := Decode(strings.NewReader(reviewThreadsProject(slackMCP, section)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.ReviewThreads.CheckEvery != Duration(15*time.Minute) {
		t.Fatalf("check_every = %s, want 15m0s", cfg.ReviewThreads.CheckEvery)
	}
}

func TestReviewThreadsRefusalsNameTheSetting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, mcp, section, want string
	}{
		{"a server the mcp section does not declare", slackMCP, strings.Replace(reviewThreadsSection, "server: slack", "server: chat", 1), "review_threads.server"},
		{"no mcp section at all", "", reviewThreadsSection, "review_threads.server"},
		{"a post tool declared as read", slackMCP, strings.Replace(reviewThreadsSection, "post_message", "search", 1), "review_threads.post_tool"},
		{"a post tool not declared", slackMCP, strings.Replace(reviewThreadsSection, "post_message", "missing", 1), "review_threads.post_tool"},
		{"a read tool declared as act", slackMCP, strings.Replace(reviewThreadsSection, "read_thread", "post_message", 1), "review_threads.read_tool"},
		{"a read tool not declared", slackMCP, strings.Replace(reviewThreadsSection, "read_thread", "missing", 1), "review_threads.read_tool"},
		{"an empty reviewer list", slackMCP, strings.Replace(reviewThreadsSection, "[U0AAAA1111, U0BBBB2222]", "[]", 1), "review_threads.reviewers"},
		{"no reviewer key", slackMCP, strings.Replace(reviewThreadsSection, "  reviewers: [U0AAAA1111, U0BBBB2222]\n", "", 1), "review_threads.reviewers"},
		{"an interval below five minutes", slackMCP, strings.Replace(reviewThreadsSection, "30m", "4m59s", 1), "review_threads.check_every"},
		{"an interval above a day", slackMCP, strings.Replace(reviewThreadsSection, "30m", "24h1m", 1), "review_threads.check_every"},
		{"no channel", slackMCP, strings.Replace(reviewThreadsSection, "  channel: C0123456789\n", "", 1), "review_threads.channel"},
		{"a section with no server", slackMCP, strings.Replace(reviewThreadsSection, "  server: slack\n", "", 1), "review_threads.server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decode(strings.NewReader(reviewThreadsProject(tc.mcp, tc.section)))
			if err == nil {
				t.Fatalf("Decode() accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal %q does not name %s", err, tc.want)
			}
		})
	}
}

func TestTheIntervalBoundsAreAccepted(t *testing.T) {
	t.Parallel()

	for _, bound := range []string{"5m", "24h"} {
		section := strings.Replace(reviewThreadsSection, "30m", bound, 1)
		if _, err := Decode(strings.NewReader(reviewThreadsProject(slackMCP, section))); err != nil {
			t.Fatalf("check_every %s refused: %v", bound, err)
		}
	}
}

func TestARoleMayNotWriteReviewThreads(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"review_threads", "review_threads.reviewers", "review_threads.check_every"} {
		err := RoleMayApply(key)
		if err == nil {
			t.Fatalf("a role's write to %s was allowed", key)
		}
		if !strings.Contains(err.Error(), "stays the operator's to set") || !strings.Contains(err.Error(), "channel") {
			t.Fatalf("the refusal for %s does not give the section's reason: %v", key, err)
		}
	}
}
