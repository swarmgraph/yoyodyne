package config

// The scheduled queries a project declares: which read tool the harness calls on
// a schedule, how it asks for a time window, where the messages are in the
// answer, whose messages count, which rules judge them, where a question goes,
// and the daily limits (docs/designs/scheduled-message-intake.md, sections 1
// and 2).
//
// Nothing here runs a query, reads a message, or launches a reader. What this
// file settles is what loads. A project that says nothing has no queries, and
// its configuration serializes and digests as it did before the key existed.
//
// All of it is the operator's, as the mcp section is: a role's write to any
// part of it is refused (roleapply.go). It names and narrows and grants
// nothing: a query's tool has to be a tool the mcp section already declares as
// read, so no line here makes a tool callable that was not, or lets a schedule
// take an act.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Intake is the project's intake section: its scheduled queries, by name.
type Intake struct {
	Queries map[string]IntakeQuery `yaml:"queries,omitempty" json:"queries,omitempty"`
}

// Declared reports whether the project declares any scheduled query at all.
func (i Intake) Declared() bool { return len(i.Queries) > 0 }

// IntakeQuery is one scheduled query.
type IntakeQuery struct {
	// Tool is the read tool the query calls, written as server and tool. It has
	// to be declared under mcp as read.
	Tool string `yaml:"tool" json:"tool"`
	// Arguments are literal arguments handed to the tool on every call.
	Arguments map[string]any `yaml:"arguments,omitempty" json:"arguments,omitempty"`
	// SinceArgument and UntilArgument name the two arguments the harness fills
	// with the start and end of the time window it is reading. Both are
	// required: a source that cannot be asked for a window cannot be read.
	SinceArgument string `yaml:"since_argument" json:"since_argument"`
	UntilArgument string `yaml:"until_argument" json:"until_argument"`
	// MoreField optionally names the field saying the source held back more
	// than it returned.
	MoreField string `yaml:"more_field,omitempty" json:"more_field,omitempty"`
	// Every is how often the query runs: an hour unless set, and never less
	// than five minutes.
	Every Duration `yaml:"every" json:"every"`
	// Items is the field of the answer that holds the messages.
	Items string `yaml:"items" json:"items"`
	// Fields says which field of each message holds each of its parts.
	Fields IntakeFields `yaml:"fields" json:"fields"`
	// Senders is the exact identifiers, as the source gives them, whose
	// messages count. Anyone replaces the list; a query sets exactly one.
	Senders []string `yaml:"senders,omitempty" json:"senders,omitempty"`
	Anyone  bool     `yaml:"anyone,omitempty" json:"anyone,omitempty"`
	// Reader is the role that reads each message. The first build has one, the
	// shipped message reader, and it is the default.
	Reader string `yaml:"reader" json:"reader"`
	// Rules is the id of the rules document the reader judges by.
	Rules string `yaml:"rules" json:"rules"`
	// Questions is who a question about a message goes to: the Lead Product
	// Manager, the default, or a person, which only the operator can choose.
	Questions IntakeQuestions `yaml:"questions" json:"questions"`
	// Limits bound what one run and one day may read, start and ask.
	Limits IntakeLimits `yaml:"limits" json:"limits"`
}

// IntakeFields names the field of a message that holds each of its parts.
type IntakeFields struct {
	ID     string `yaml:"id" json:"id"`
	Sender string `yaml:"sender" json:"sender"`
	Text   string `yaml:"text" json:"text"`
	SentAt string `yaml:"sent_at" json:"sent_at"`
	// Link is optional: a source that gives no link is still readable.
	Link string `yaml:"link,omitempty" json:"link,omitempty"`
}

// IntakeQuestions is who a query's questions go to.
type IntakeQuestions string

const (
	// IntakeQuestionsLeadProductManager sends questions to the Lead Product
	// Manager, without the message's text. It is the default.
	IntakeQuestionsLeadProductManager IntakeQuestions = "lead-product-manager"
	// IntakeQuestionsPerson reserves a query's questions for the operator.
	IntakeQuestionsPerson IntakeQuestions = "person"
)

// IntakeLimits bound one query's reading, starting and asking.
type IntakeLimits struct {
	MessagesPerRun  int `yaml:"messages_per_run" json:"messages_per_run"`
	MessagesPerDay  int `yaml:"messages_per_day" json:"messages_per_day"`
	StartsPerDay    int `yaml:"starts_per_day" json:"starts_per_day"`
	QuestionsPerDay int `yaml:"questions_per_day" json:"questions_per_day"`
	TextBytes       int `yaml:"text_bytes" json:"text_bytes"`
}

// IntakeMessageReader is the one reader the first build ships.
const IntakeMessageReader = "message-reader"

// The schedule's default and its shortest allowed interval.
const (
	DefaultIntakeEvery = Duration(time.Hour)
	MinimumIntakeEvery = Duration(5 * time.Minute)
)

// DefaultIntakeLimits are the limits a query that names none runs under.
var DefaultIntakeLimits = IntakeLimits{
	MessagesPerRun:  20,
	MessagesPerDay:  100,
	StartsPerDay:    5,
	QuestionsPerDay: 5,
	TextBytes:       4096,
}

// intakeDocument is the intake section as one layer writes it. The section
// replaces an inherited one entirely, as mcp does, because a query assembled
// from two layers is one nobody wrote; the pointers are only so a value left
// out takes its default rather than zero.
type intakeDocument struct {
	Queries map[string]intakeQueryDocument `yaml:"queries"`
}

type intakeQueryDocument struct {
	Tool          string                `yaml:"tool"`
	Arguments     map[string]any        `yaml:"arguments"`
	SinceArgument string                `yaml:"since_argument"`
	UntilArgument string                `yaml:"until_argument"`
	MoreField     string                `yaml:"more_field"`
	Every         *Duration             `yaml:"every"`
	Items         string                `yaml:"items"`
	Fields        IntakeFields          `yaml:"fields"`
	Senders       []string              `yaml:"senders"`
	Anyone        bool                  `yaml:"anyone"`
	Reader        *string               `yaml:"reader"`
	Rules         string                `yaml:"rules"`
	Questions     *IntakeQuestions      `yaml:"questions"`
	Limits        *intakeLimitsDocument `yaml:"limits"`
}

type intakeLimitsDocument struct {
	MessagesPerRun  *int `yaml:"messages_per_run"`
	MessagesPerDay  *int `yaml:"messages_per_day"`
	StartsPerDay    *int `yaml:"starts_per_day"`
	QuestionsPerDay *int `yaml:"questions_per_day"`
	TextBytes       *int `yaml:"text_bytes"`
}

// resolveIntake is a layer's section with every default filled in, copied
// rather than aliased so nothing later edits what the document holds. An empty
// section is no section, so `queries: {}` digests as a file that never wrote
// the key.
func resolveIntake(document intakeDocument) Intake {
	if len(document.Queries) == 0 {
		return Intake{}
	}
	resolved := Intake{Queries: make(map[string]IntakeQuery, len(document.Queries))}
	for name, written := range document.Queries {
		query := IntakeQuery{
			Tool:          strings.TrimSpace(written.Tool),
			Arguments:     copyIntakeArguments(written.Arguments),
			SinceArgument: strings.TrimSpace(written.SinceArgument),
			UntilArgument: strings.TrimSpace(written.UntilArgument),
			MoreField:     strings.TrimSpace(written.MoreField),
			Every:         DefaultIntakeEvery,
			Items:         strings.TrimSpace(written.Items),
			Fields:        written.Fields,
			Senders:       append([]string(nil), written.Senders...),
			Anyone:        written.Anyone,
			Reader:        IntakeMessageReader,
			Rules:         strings.TrimSpace(written.Rules),
			Questions:     IntakeQuestionsLeadProductManager,
			Limits:        DefaultIntakeLimits,
		}
		if written.Every != nil {
			query.Every = *written.Every
		}
		if written.Reader != nil {
			query.Reader = strings.TrimSpace(*written.Reader)
		}
		if written.Questions != nil {
			query.Questions = *written.Questions
		}
		if limits := written.Limits; limits != nil {
			for _, limit := range []struct {
				written *int
				into    *int
			}{
				{limits.MessagesPerRun, &query.Limits.MessagesPerRun},
				{limits.MessagesPerDay, &query.Limits.MessagesPerDay},
				{limits.StartsPerDay, &query.Limits.StartsPerDay},
				{limits.QuestionsPerDay, &query.Limits.QuestionsPerDay},
				{limits.TextBytes, &query.Limits.TextBytes},
			} {
				if limit.written != nil {
					*limit.into = *limit.written
				}
			}
		}
		resolved.Queries[name] = query
	}
	return resolved
}

func copyIntakeArguments(arguments map[string]any) map[string]any {
	if len(arguments) == 0 {
		return nil
	}
	copied := make(map[string]any, len(arguments))
	for name, value := range arguments {
		copied[name] = copyIntakeValue(value)
	}
	return copied
}

func copyIntakeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return copyIntakeArguments(typed)
	case []any:
		copied := make([]any, len(typed))
		for index, element := range typed {
			copied[index] = copyIntakeValue(element)
		}
		return copied
	}
	return value
}

// intakeProblems reports everything wrong with the intake section at once.
func (c Config) intakeProblems() []string {
	if !c.Intake.Declared() {
		return nil
	}
	var problems []string
	if c.Team.Enabled {
		// Each machine keeps its own message records, so two factories sharing a
		// repository could both start work from one message.
		problems = append(problems, "intake is set while team.enabled is on; each machine keeps its own record of the messages it has read, so two machines working this repository could both start work from one message. Take out the intake section or turn team mode off")
	}
	names := make([]string, 0, len(c.Intake.Queries))
	for name := range c.Intake.Queries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		problems = append(problems, c.intakeQueryProblems(name, c.Intake.Queries[name])...)
	}
	return problems
}

func (c Config) intakeQueryProblems(name string, query IntakeQuery) []string {
	var problems []string
	if err := domain.ValidateIdentifier("intake query name", name); err != nil {
		problems = append(problems, err.Error())
	}
	key := "intake.queries." + name
	problems = append(problems, c.intakeToolProblems(key, query.Tool)...)

	switch {
	case query.SinceArgument == "" && query.UntilArgument == "":
		problems = append(problems, fmt.Sprintf("%s names neither since_argument nor until_argument; the harness reads a source one time window at a time, so a query has to name the two arguments it fills with the window's start and end", key))
	case query.SinceArgument == "":
		problems = append(problems, fmt.Sprintf("%s names no since_argument; the harness reads a source one time window at a time, so a query has to name the argument it fills with the window's start", key))
	case query.UntilArgument == "":
		problems = append(problems, fmt.Sprintf("%s names no until_argument; the harness reads a source one time window at a time, so a query has to name the argument it fills with the window's end", key))
	case query.SinceArgument == query.UntilArgument:
		problems = append(problems, fmt.Sprintf("%s names %q as both since_argument and until_argument; the start and end of a window are two arguments", key, query.SinceArgument))
	}
	for _, filled := range []string{query.SinceArgument, query.UntilArgument} {
		if _, written := query.Arguments[filled]; filled != "" && written {
			problems = append(problems, fmt.Sprintf("%s.arguments sets %q, which is the argument the harness fills with the time window; leave it out of arguments", key, filled))
		}
	}

	if query.Every < MinimumIntakeEvery {
		problems = append(problems, fmt.Sprintf("%s.every is %s; a query runs at most every %s", key, query.Every, MinimumIntakeEvery))
	}
	if query.Items == "" {
		problems = append(problems, fmt.Sprintf("%s names no items, the field of the answer that holds the messages", key))
	}
	for _, field := range []struct{ name, value string }{
		{"id", query.Fields.ID},
		{"sender", query.Fields.Sender},
		{"text", query.Fields.Text},
		{"sent_at", query.Fields.SentAt},
	} {
		if strings.TrimSpace(field.value) == "" {
			problems = append(problems, fmt.Sprintf("%s.fields names no %s, the field of each message that holds it", key, field.name))
		}
	}

	switch {
	case query.Anyone && len(query.Senders) > 0:
		problems = append(problems, fmt.Sprintf("%s sets both senders and anyone: true; anyone: true takes messages from everybody and replaces the list, so set one or the other", key))
	case !query.Anyone && len(query.Senders) == 0:
		problems = append(problems, fmt.Sprintf("%s lists no senders and does not say anyone: true; list the exact sender identifiers whose messages count, or say anyone: true to take messages from everybody", key))
	}
	seen := map[string]bool{}
	for _, sender := range query.Senders {
		switch {
		case strings.TrimSpace(sender) == "":
			problems = append(problems, fmt.Sprintf("%s.senders holds an empty entry", key))
		case seen[sender]:
			problems = append(problems, fmt.Sprintf("%s.senders lists %q twice", key, sender))
		}
		seen[sender] = true
	}

	if query.Reader != IntakeMessageReader {
		problems = append(problems, fmt.Sprintf("%s.reader is %q; the one reader this build has is %q, a role with no tools", key, query.Reader, IntakeMessageReader))
	}
	if query.Rules == "" {
		problems = append(problems, fmt.Sprintf("%s names no rules, the id of the rules document its messages are judged by", key))
	} else if err := domain.ValidateIdentifier(key+".rules", query.Rules); err != nil {
		problems = append(problems, err.Error())
	}
	switch query.Questions {
	case IntakeQuestionsLeadProductManager, IntakeQuestionsPerson:
	default:
		problems = append(problems, fmt.Sprintf("%s.questions is %q; it is %q, the default, or %q", key, query.Questions, IntakeQuestionsLeadProductManager, IntakeQuestionsPerson))
	}
	for _, limit := range []struct {
		name  string
		value int
	}{
		{"messages_per_run", query.Limits.MessagesPerRun},
		{"messages_per_day", query.Limits.MessagesPerDay},
		{"starts_per_day", query.Limits.StartsPerDay},
		{"questions_per_day", query.Limits.QuestionsPerDay},
		{"text_bytes", query.Limits.TextBytes},
	} {
		if limit.value < 1 {
			problems = append(problems, fmt.Sprintf("%s.limits.%s is %d; it must be at least 1", key, limit.name, limit.value))
		}
	}
	return problems
}

// intakeToolProblems refuses a query's tool unless the mcp section declares it
// as read: a scheduled call runs with nobody watching, so it may only read.
func (c Config) intakeToolProblems(key, name string) []string {
	said := fmt.Sprintf("%s.tool %q", key, name)
	if name == "" {
		return []string{fmt.Sprintf("%s names no tool; a query calls one read tool declared under mcp, written as server.tool", key)}
	}
	server, tool, written := strings.Cut(name, ".")
	if !written || server == "" || tool == "" || strings.Contains(name, "*") {
		return []string{said + " is not written as server.tool"}
	}
	declared, ok := c.MCP.Servers[server]
	if !ok {
		return []string{fmt.Sprintf("%s: the mcp section declares no server %q, and a query may call only a read tool declared there", said, server)}
	}
	class, ok := declared.Tools[tool]
	if !ok {
		return []string{fmt.Sprintf("%s: mcp.servers.%s.tools does not name %q, and a query may call only a read tool declared there", said, server, tool)}
	}
	if class != MCPRead {
		return []string{fmt.Sprintf("%s is declared %q, and a query may call only a %q tool, because it runs on a schedule with nobody watching and must change nothing outside the harness", said, class, MCPRead)}
	}
	return nil
}
