package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

const intakeCLIConfig = validConfig + `mcp:
  servers:
    slack:
      command: [slack-mcp]
      tools:
        search: read
intake:
  queries:
    slack-bugs:
      tool: slack.search
      since_argument: after
      until_argument: before
      items: messages
      fields: {id: ts, sender: user, text: text, sent_at: ts}
      senders: [U123, U456]
      rules: intake-rules
    open-reports:
      tool: slack.search
      since_argument: after
      until_argument: before
      every: 15m
      items: messages
      fields: {id: ts, sender: user, text: text, sent_at: ts}
      anyone: true
      rules: intake-rules
      questions: person
`

// `yoyo config show` shows each query, says in words where one takes messages
// from anyone and prints anyone: true where it is set, and a project with no
// intake section prints nothing about intake at all.
func TestConfigShowShowsTheIntakeQueriesAndAnyone(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runCLI(t, "config", "show", "--config", writeConfig(t, intakeCLIConfig))
	if code != 0 {
		t.Fatalf("config show: code %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"# intake query open-reports: calls slack.search every 15m0s, messages from anyone (anyone: true), judged by the rules in intake-rules, questions to person\n",
		"# intake query slack-bugs: calls slack.search every 1h0m0s, messages from 2 listed senders, judged by the rules in intake-rules, questions to lead-product-manager\n",
		"intake:\n",
		"anyone: true\n",
		"- U123\n",
		"messages_per_day: 100\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("config show does not say %q:\n%s", want, stdout)
		}
	}
	if strings.Count(stdout, "anyone:") != 2 {
		t.Errorf("config show prints anyone for a query that did not set it:\n%s", stdout)
	}

	stdout, _, code = runCLI(t, "config", "show", "--json", "--config", writeConfig(t, intakeCLIConfig))
	var payload struct {
		Effective struct {
			Intake struct {
				Queries map[string]struct {
					Anyone  bool     `json:"anyone"`
					Senders []string `json:"senders"`
				} `json:"queries"`
			} `json:"intake"`
		} `json:"effective"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &payload) != nil {
		t.Fatalf("config show --json: code %d\n%s", code, stdout)
	}
	if queries := payload.Effective.Intake.Queries; !queries["open-reports"].Anyone || queries["slack-bugs"].Anyone || len(queries["slack-bugs"].Senders) != 2 {
		t.Fatalf("config show --json intake = %#v", queries)
	}

	stdout, _, code = runCLI(t, "config", "show", "--config", writeConfig(t, validConfig))
	if code != 0 || strings.Contains(stdout, "\nintake:") || strings.Contains(stdout, "intake query") || strings.Contains(stdout, "anyone") {
		t.Fatalf("config show with no intake section mentions intake:\n%s", stdout)
	}
	stdout, _, _ = runCLI(t, "config", "show", "--json", "--config", writeConfig(t, validConfig))
	if strings.Contains(stdout, `"intake"`) {
		t.Fatalf("config show --json with no intake section mentions intake:\n%s", stdout)
	}
}
