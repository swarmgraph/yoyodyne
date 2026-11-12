package config

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// intakeServers is an mcp section with one read tool and one act tool for a
// query to name.
const intakeServers = `mcp:
  servers:
    slack:
      command: [slack-mcp]
      tools:
        search: read
        post: act
`

// intakeQuery is the design's example query, under intake.queries.
const intakeQuery = `    slack-bugs:
      tool: slack.search
      arguments: {query: in-bugs, channels: [bugs, support]}
      since_argument: after
      until_argument: before
      more_field: has_more
      every: 1h
      items: messages
      fields: {id: ts, sender: user, text: text, sent_at: ts, link: permalink}
      senders: [U123, U456]
      reader: message-reader
      rules: intake-rules
      questions: lead-product-manager
      limits: {messages_per_run: 20, messages_per_day: 100, starts_per_day: 5, questions_per_day: 5, text_bytes: 4096}
`

const intakeProject = validBootstrapConfig + intakeServers + "intake:\n  queries:\n" + intakeQuery

// intakeWith is the example project with one line of the query replaced, or
// removed where with is empty.
func intakeWith(t *testing.T, line, with string) string {
	t.Helper()
	old := "      " + line + "\n"
	if !strings.Contains(intakeProject, old) {
		t.Fatalf("the example query has no line %q", line)
	}
	if with != "" {
		with = "      " + with + "\n"
	}
	return strings.Replace(intakeProject, old, with, 1)
}

func TestTheIntakeSectionLoadsEveryPartOfAQuery(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(intakeProject))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	query, ok := cfg.Intake.Queries["slack-bugs"]
	if !ok || !cfg.Intake.Declared() {
		t.Fatalf("intake = %#v, want the slack-bugs query", cfg.Intake)
	}
	if query.Tool != "slack.search" || query.Arguments["query"] != "in-bugs" || query.SinceArgument != "after" || query.UntilArgument != "before" || query.MoreField != "has_more" {
		t.Errorf("the call = %#v", query)
	}
	if query.Every.Duration() != time.Hour || query.Items != "messages" {
		t.Errorf("schedule and items = %s, %q", query.Every, query.Items)
	}
	if query.Fields != (IntakeFields{ID: "ts", Sender: "user", Text: "text", SentAt: "ts", Link: "permalink"}) {
		t.Errorf("fields = %#v", query.Fields)
	}
	if strings.Join(query.Senders, ",") != "U123,U456" || query.Anyone {
		t.Errorf("senders = %v, anyone = %v", query.Senders, query.Anyone)
	}
	if query.Reader != IntakeMessageReader || query.Rules != "intake-rules" || query.Questions != IntakeQuestionsLeadProductManager {
		t.Errorf("reader, rules, questions = %q, %q, %q", query.Reader, query.Rules, query.Questions)
	}
	if query.Limits != (IntakeLimits{MessagesPerRun: 20, MessagesPerDay: 100, StartsPerDay: 5, QuestionsPerDay: 5, TextBytes: 4096}) {
		t.Errorf("limits = %#v", query.Limits)
	}
}

// A query that leaves out what has a default runs every hour, is read by the
// message reader, sends its questions to the Lead Product Manager, and runs
// under the design's limits; a limit it does set is kept and the rest default.
func TestAnIntakeQueryTakesTheDefaultsItLeavesOut(t *testing.T) {
	t.Parallel()

	input := intakeProject
	for _, line := range []string{"every: 1h", "reader: message-reader", "questions: lead-product-manager", "more_field: has_more", "fields: {id: ts, sender: user, text: text, sent_at: ts, link: permalink}"} {
		with := ""
		if strings.HasPrefix(line, "fields:") {
			with = "      fields: {id: ts, sender: user, text: text, sent_at: ts}\n"
		}
		input = strings.Replace(input, "      "+line+"\n", with, 1)
	}
	input = strings.Replace(input, "      limits: {messages_per_run: 20, messages_per_day: 100, starts_per_day: 5, questions_per_day: 5, text_bytes: 4096}\n", "      limits: {starts_per_day: 2}\n", 1)
	cfg, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	query := cfg.Intake.Queries["slack-bugs"]
	want := DefaultIntakeLimits
	want.StartsPerDay = 2
	if query.Every != DefaultIntakeEvery || query.Reader != IntakeMessageReader || query.Questions != IntakeQuestionsLeadProductManager || query.Limits != want || query.MoreField != "" || query.Fields.Link != "" {
		t.Fatalf("query = %#v, want the defaults with starts_per_day 2", query)
	}
}

func TestAQueryMayTakeMessagesFromAnyoneAndSendQuestionsToAPerson(t *testing.T) {
	t.Parallel()

	input := strings.Replace(intakeWith(t, "senders: [U123, U456]", "anyone: true"), "questions: lead-product-manager", "questions: person", 1)
	cfg, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if query := cfg.Intake.Queries["slack-bugs"]; !query.Anyone || query.Senders != nil || query.Questions != IntakeQuestionsPerson {
		t.Fatalf("query = %#v", query)
	}
}

// Every refusal the item names, and the others the section is held to, is made
// as the file loads and says in plain words what is wrong and what to write.
func TestTheIntakeSectionIsRefusedInPlainWords(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, input string
		want        []string
	}{
		{"an act tool", intakeWith(t, "tool: slack.search", "tool: slack.post"), []string{`intake.queries.slack-bugs.tool "slack.post"`, `declared "act"`, `only a "read" tool`, "nobody watching"}},
		{"an undeclared tool", intakeWith(t, "tool: slack.search", "tool: slack.history"), []string{`"slack.history"`, `does not name "history"`, "read tool declared there"}},
		{"an undeclared server", intakeWith(t, "tool: slack.search", "tool: mail.search"), []string{`"mail.search"`, `declares no server "mail"`}},
		{"a tool not written as server.tool", intakeWith(t, "tool: slack.search", "tool: search"), []string{`"search" is not written as server.tool`}},
		{"no tool", intakeWith(t, "tool: slack.search", ""), []string{"names no tool"}},
		{"no since argument", intakeWith(t, "since_argument: after", ""), []string{"names no since_argument", "one time window at a time", "window's start"}},
		{"no until argument", intakeWith(t, "until_argument: before", ""), []string{"names no until_argument", "window's end"}},
		{"neither time argument", strings.Replace(intakeWith(t, "since_argument: after", ""), "      until_argument: before\n", "", 1), []string{"names neither since_argument nor until_argument"}},
		{"one argument for both ends", intakeWith(t, "until_argument: before", "until_argument: after"), []string{`"after" as both`}},
		{"a literal for a filled argument", intakeWith(t, "arguments: {query: in-bugs, channels: [bugs, support]}", "arguments: {query: in-bugs, after: '0'}"), []string{`sets "after"`, "fills with the time window"}},
		{"no senders and no anyone", intakeWith(t, "senders: [U123, U456]", ""), []string{"lists no senders and does not say anyone: true"}},
		{"an empty senders list", intakeWith(t, "senders: [U123, U456]", "senders: []"), []string{"lists no senders and does not say anyone: true"}},
		{"anyone: false and no senders", intakeWith(t, "senders: [U123, U456]", "anyone: false"), []string{"lists no senders and does not say anyone: true"}},
		{"senders and anyone", intakeWith(t, "reader: message-reader", "anyone: true"), []string{"sets both senders and anyone: true"}},
		{"a sender twice", intakeWith(t, "senders: [U123, U456]", "senders: [U123, U123]"), []string{`lists "U123" twice`}},
		{"a schedule under five minutes", intakeWith(t, "every: 1h", "every: 4m59s"), []string{"intake.queries.slack-bugs.every is 4m59s", "at most every 5m0s"}},
		{"no items", intakeWith(t, "items: messages", ""), []string{"names no items"}},
		{"a missing field", intakeWith(t, "fields: {id: ts, sender: user, text: text, sent_at: ts, link: permalink}", "fields: {id: ts, text: text, sent_at: ts}"), []string{"fields names no sender"}},
		{"another reader", intakeWith(t, "reader: message-reader", "reader: pm"), []string{`reader is "pm"`, `"message-reader"`}},
		{"no rules", intakeWith(t, "rules: intake-rules", ""), []string{"names no rules"}},
		{"a rules id that is not an id", intakeWith(t, "rules: intake-rules", "rules: ../rules.md"), []string{"intake.queries.slack-bugs.rules"}},
		{"a questions setting nobody has", intakeWith(t, "questions: lead-product-manager", "questions: operator"), []string{`questions is "operator"`, `"lead-product-manager", the default, or "person"`}},
		{"a zero limit", intakeWith(t, "limits: {messages_per_run: 20, messages_per_day: 100, starts_per_day: 5, questions_per_day: 5, text_bytes: 4096}", "limits: {starts_per_day: 0}"), []string{"limits.starts_per_day is 0; it must be at least 1"}},
		{"a key nobody has", intakeWith(t, "items: messages", "items: messages\n      cursor: next"), []string{"field cursor not found"}},
		{"a bad query name", strings.Replace(intakeProject, "    slack-bugs:\n", "    Slack_Bugs:\n", 1), []string{"intake query name"}},
		{"team mode", intakeProject + "team:\n  enabled: true\n", []string{"intake is set while team.enabled is on", "both start work from one message", "turn team mode off"}},
	} {
		_, err := Decode(strings.NewReader(test.input))
		if err == nil {
			t.Errorf("%s: Decode() = nil, want it refused", test.name)
			continue
		}
		for _, want := range test.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: Decode() error = %v, want it to say %q", test.name, err, want)
			}
		}
	}
}

// Team mode with no intake section loads as it always did.
func TestTeamModeWithNoIntakeSectionLoads(t *testing.T) {
	t.Parallel()
	if _, err := Decode(strings.NewReader(validBootstrapConfig + intakeServers + "team:\n  enabled: true\n")); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
}

// A project with no intake section has no queries, and a configuration that
// serializes and digests exactly as it did before the key existed; an empty
// section is no section.
func TestAProjectWithNoIntakeSectionIsUnchanged(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.Intake.Declared() || cfg.Intake.Queries != nil {
		t.Fatalf("a file with no intake section has %#v", cfg.Intake)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"intake"`) || strings.Contains(string(encoded), `"queries"`) {
		t.Fatalf("a configuration with no intake section serializes an intake key: %s", encoded)
	}
	for _, empty := range []string{"intake: {}\n", "intake:\n  queries: {}\n"} {
		decoded, err := Decode(strings.NewReader(validBootstrapConfig + empty))
		if err != nil {
			t.Fatalf("Decode(%q) error = %v", empty, err)
		}
		if decoded.Revision() != cfg.Revision() {
			t.Fatalf("%q moved the revision from %s to %s", empty, cfg.Revision(), decoded.Revision())
		}
	}
	declared, err := Decode(strings.NewReader(intakeProject))
	if err != nil {
		t.Fatal(err)
	}
	withServers, err := Decode(strings.NewReader(validBootstrapConfig + intakeServers))
	if err != nil {
		t.Fatal(err)
	}
	if declared.Revision() == withServers.Revision() {
		t.Fatal("declaring a query left the revision where it was, so a run record could not tell the two configurations apart")
	}
}

// The section records the layer it came from, as every other effective value
// does, and a project's section replaces an inherited one whole.
func TestTheIntakeSectionRecordsItsOrigin(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(intakeProject))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	if resolved.Origins["intake"] == "" {
		t.Fatalf("the intake section has no origin: %v", resolved.Origins)
	}
}

// Resolving copies a query's arguments, so the resolved configuration never
// shares a map or list with the document it came from.
func TestResolvingCopiesAQuerysArguments(t *testing.T) {
	t.Parallel()

	channels := []any{"bugs"}
	document := intakeDocument{Queries: map[string]intakeQueryDocument{"q": {Arguments: map[string]any{"channels": channels}}}}
	resolved := resolveIntake(document)
	resolved.Queries["q"].Arguments["channels"].([]any)[0] = "changed"
	if channels[0] != "bugs" {
		t.Fatal("editing the resolved arguments edited the document's")
	}
}

// No part of the section is a role's to write.
func TestARoleMayNotApplyTheIntakeSection(t *testing.T) {
	t.Parallel()
	for _, key := range []string{
		"intake",
		"intake.queries",
		"intake.queries.slack-bugs.senders",
		"intake.queries.slack-bugs.anyone",
		"intake.queries.slack-bugs.questions",
		"intake.queries.slack-bugs.limits.starts_per_day",
		"intake.queries.slack-bugs.every",
	} {
		err := RoleMayApply(key)
		if err == nil || !strings.Contains(err.Error(), "stays the operator's") || !strings.Contains(err.Error(), "whose messages can start work") || !strings.Contains(err.Error(), key) {
			t.Errorf("RoleMayApply(%q) = %v, want it refused as the operator's, saying why", key, err)
		}
	}
}

// intakeReaders is every file outside tests that reads the intake section: the
// configuration that declares, loads and checks it, and `yoyo config show`. None of them
// builds a role's launch, writes a record, or feeds a surface other than that
// listing, so a project that declares nothing - whose revision is unchanged too
// (TestAProjectWithNoIntakeSectionIsUnchanged) - launches, records and shows
// everything exactly as it did before the key existed.
//
// A later piece that runs a query or reads the section anywhere else adds its
// file here, which is where a reviewer sees that it does.
var intakeReaders = map[string]bool{
	"internal/config/config.go":   true,
	"internal/config/document.go": true,
	"internal/config/intake.go":   true,
	"internal/config/resolve.go":  true,
	"internal/cli/cli.go":         true,
	"internal/cli/intake.go":      true,
}

// intakeNames is every exported name the section's types and values go by.
var intakeNames = map[string]bool{
	"Intake": true, "IntakeQuery": true, "IntakeFields": true, "IntakeQuestions": true, "IntakeLimits": true,
	"IntakeQuestionsLeadProductManager": true, "IntakeQuestionsPerson": true, "IntakeMessageReader": true,
	"DefaultIntakeEvery": true, "MinimumIntakeEvery": true, "DefaultIntakeLimits": true,
}

// The selector Intake also names the intake hold across the orchestrator, chat,
// Slack and the watchdog, so outside the configuration package this looks for
// what only the section is: one of its type or value names from the config
// package, the section reached through a resolved configuration
// (`<x>.Config.Intake`), or its queries (`<x>.Intake.Queries` and
// `<x>.Intake.Declared`). Inside the configuration package nothing else is
// called Intake, so every reference counts.
func TestNothingOnTheLaunchRecordOrSurfacePathReadsTheIntakeSection(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	files := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if intakeReaders[relative] {
			return nil
		}
		parsed, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		inConfig := parsed.Name.Name == "config" && strings.HasPrefix(relative, "internal/config/")
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.Ident:
				if inConfig && intakeNames[typed.Name] {
					found = append(found, relative+": names "+typed.Name)
				}
			case *ast.SelectorExpr:
				if inConfig {
					return true
				}
				if pkg, ok := typed.X.(*ast.Ident); ok && pkg.Name == "config" && intakeNames[typed.Sel.Name] {
					found = append(found, relative+": names config."+typed.Sel.Name)
				}
				if typed.Sel.Name == "Intake" {
					if inner, ok := typed.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "Config" {
						found = append(found, relative+": reads Config.Intake")
					}
				}
				if typed.Sel.Name == "Queries" || typed.Sel.Name == "Declared" {
					if inner, ok := typed.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "Intake" {
						found = append(found, relative+": reads Intake."+typed.Sel.Name)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Fatalf("code outside the files that load and show the intake section reads it:\n%s", strings.Join(found, "\n"))
	}
}
