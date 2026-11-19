// Package intakerules reads the rules document a scheduled intake query names
// and decides which of its rules can be used
// (docs/designs/scheduled-message-intake.md, sections 5 and 6).
//
// A rule is the Lead Product Manager's standing decision to admit one kind of
// work, so everything about the work a message starts — its goal, priority,
// type, label, title, done criteria and workflow — comes from the rule and
// nothing from the message. That is why a rule is held to the same limits
// admission is: a rule naming a goal that is not active, setting priority 0, or
// naming a workflow other than delivery is refused, and so is one written so
// that it cannot be read. One refused rule never takes the others with it.
//
// The document is read as the target branch holds it when a query run starts,
// never from a working tree (read.go), and the commit it was read at goes with
// every decision made under it. A document that cannot be read as rules stops
// that query's readers until it can be, and is reported once to the Lead
// Product Manager (stop.go).
package intakerules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// MaxDocumentBytes bounds the rules document read at a commit. A rules document
// is a short list a person keeps; one past this size is refused as unreadable
// rather than read in part, because a rule cut off half way is a different rule.
const MaxDocumentBytes = 256 << 10

// WorkflowDelivery is the one workflow a rule may name in the first build: the
// configurable workflows design says no other sequence runs until the graph
// takes over (section 5 of the intake design).
const WorkflowDelivery = "delivery"

// Rule is one rule that can be used: every part of the work a message under it
// would start, as the document states it.
type Rule struct {
	// ID is the identifier the rule opens with, which is what the message reader
	// names and what the started work records.
	ID string `json:"id"`
	// Description is the rule's own words for the messages it covers, which is
	// what the reader judges a message against.
	Description string `json:"description"`
	Workflow    string `json:"workflow"`
	// Goal is the identifier of the active goal the work serves.
	Goal     string              `json:"goal"`
	Priority int                 `json:"priority"`
	Type     domain.WorkItemKind `json:"type"`
	// Label is optional: a rule that names none adds no label.
	Label string `json:"label,omitempty"`
	Title string `json:"title"`
	Done  string `json:"done"`
	// From is optional and says where the rule came from.
	From string `json:"from,omitempty"`
	// Line is the line of the document the rule opens on, counted from the top
	// of the file, so what is reported names a place to open.
	Line int `json:"line"`
}

// Refusal is one rule that cannot be used, and why. The other rules still
// apply.
type Refusal struct {
	// ID is the rule's identifier, empty where the entry opens with none.
	ID     string `json:"id,omitempty"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

func (r Refusal) String() string {
	if r.ID == "" {
		return fmt.Sprintf("line %d: %s", r.Line, r.Reason)
	}
	return fmt.Sprintf("line %d, rule %s: %s", r.Line, r.ID, r.Reason)
}

// Parsed is what one rules document holds once read.
type Parsed struct {
	Rules   []Rule    `json:"rules,omitempty"`
	Refused []Refusal `json:"refused,omitempty"`
	// Unreadable is why the document cannot be read as rules at all, and is
	// empty where it can. A document that is unreadable holds no rules, and
	// Rules and Refused are then empty.
	Unreadable string `json:"unreadable,omitempty"`
}

// Rule returns the usable rule with the identifier given.
func (p Parsed) Rule(id string) (Rule, bool) {
	for _, rule := range p.Rules {
		if rule.ID == id {
			return rule, true
		}
	}
	return Rule{}, false
}

// ruleEntryPattern is a top-level list entry. A rule opens at the left margin,
// so an indented entry is one of a rule's parts rather than a rule.
var ruleEntryPattern = regexp.MustCompile(`^[-*+]\s+(.*)$`)

// ruleIdentityPattern is the identifier a rule opens with, in the same form a
// goal's identifier takes.
var ruleIdentityPattern = regexp.MustCompile(`^\[([a-z0-9]+(?:-[a-z0-9]+)*)\]\s*(.*)$`)

// partPattern is one indented part of a rule, such as `  - goal: nearly-autonomous`.
var partPattern = regexp.MustCompile(`^\s+[-*+]\s+(.*)$`)

// headingPattern is an ATX heading.
var headingPattern = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)

// The parts a rule may carry, and which of them it must.
var (
	requiredParts = []string{"workflow", "goal", "priority", "type", "title", "done"}
	optionalParts = []string{"label", "from"}
)

// Parse reads one rules document and judges each of its rules against the goals
// given. It never fails: what is wrong is said in the result, a rule at a time
// where it can be and of the whole document where it cannot.
//
// goals is what a rule's goal is checked against. A rule names a goal by its
// identifier, and only a goal in force that one goal alone carries is active.
func Parse(content []byte, goals goal.Set) Parsed {
	if !utf8.Valid(content) {
		return Parsed{Unreadable: "it is not UTF-8 text"}
	}
	if len(content) > MaxDocumentBytes {
		return Parsed{Unreadable: fmt.Sprintf("it is %d bytes, and a rules document is read only up to %d", len(content), MaxDocumentBytes)}
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(content), "\ufeff"), "\r\n", "\n")
	body, offset, problem := documentBody(text)
	if problem != "" {
		return Parsed{Unreadable: problem}
	}
	entries, found := ruleEntries(body, offset)
	if !found {
		return Parsed{Unreadable: "it has no `## Rules` heading, which is where a rules document states its rules"}
	}
	if len(entries) == 0 {
		return Parsed{Unreadable: "its `## Rules` section states no rules"}
	}

	var parsed Parsed
	counts := map[string]int{}
	for _, entry := range entries {
		if entry.id != "" {
			counts[entry.id]++
		}
	}
	for _, entry := range entries {
		rule, reason := judge(entry, goals)
		if reason == "" && counts[entry.id] > 1 {
			// Two rules sharing one identifier is a defect in the document rather
			// than a choice between them: a reader naming the identifier would pick
			// out neither, so both are refused until one is renamed.
			reason = fmt.Sprintf("%d rules in this document open with [%s]; a rule identifier names one rule, so each of them is refused until it does", counts[entry.id], entry.id)
		}
		if reason != "" {
			parsed.Refused = append(parsed.Refused, Refusal{ID: entry.id, Line: entry.line, Reason: reason})
			continue
		}
		parsed.Rules = append(parsed.Rules, rule)
	}
	if len(parsed.Rules) == 0 {
		// A document none of whose rules can be used would have every message read
		// for nothing, since the reader could only ever ignore or ask. It is
		// reported as a document to fix, with each rule's refusal kept.
		parsed.Unreadable = fmt.Sprintf("none of its %d rules can be used", len(entries))
	}
	return parsed
}

// documentBody is the document below its frontmatter, and the number of lines
// the frontmatter took. A rules document a query names is the project's own
// setup and may be kept anywhere, so frontmatter is not required; where the
// document carries some, it has to say it is a rules document in force,
// because one that says it is something else, or that it has been superseded
// or retired, is not what the query should be judging by.
func documentBody(text string) (string, int, string) {
	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return text, 0, ""
	}
	recorded, err := artifact.ParseDocument([]byte(text))
	if err != nil {
		return "", 0, fmt.Sprintf("its frontmatter cannot be read: %v", err)
	}
	if recorded.Kind != artifact.KindRules {
		return "", 0, fmt.Sprintf("its frontmatter says it is kind %q, and a query's rules have to be a %q document", recorded.Kind, artifact.KindRules)
	}
	if !recorded.InForce() {
		return "", 0, fmt.Sprintf("its frontmatter says its status is %q, and only a document whose status is %q is in force", recorded.Status, artifact.StatusActive)
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return strings.Join(lines[index+1:], "\n"), index + 1, ""
		}
	}
	return "", 0, "its frontmatter is not closed by a `---` line"
}

// entry is one rule as written: its opening line and the indented lines under
// it.
type entry struct {
	id   string
	text string
	// opening is the text after the list marker, kept so an entry with no
	// identifier can be refused naming what it said.
	opening string
	parts   []part
	// loose are indented lines under the entry that are not parts. They extend
	// the description where they come before any part.
	loose []string
	line  int
}

type part struct {
	text string
	line int
}

// ruleEntries finds the `## Rules` section and splits it into entries. found
// is false where the document has no such heading.
func ruleEntries(body string, offset int) ([]entry, bool) {
	lines := strings.Split(body, "\n")
	start := -1
	for index, line := range lines {
		if match := headingPattern.FindStringSubmatch(line); match != nil && len(match[1]) == 2 && strings.EqualFold(strings.TrimSpace(match[2]), "rules") {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return nil, false
	}
	var entries []entry
	fenced := false
	for index := start; index < len(lines); index++ {
		line := lines[index]
		number := offset + index + 1
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if match := headingPattern.FindStringSubmatch(line); match != nil && len(match[1]) <= 2 {
			break
		}
		if match := ruleEntryPattern.FindStringSubmatch(line); match != nil {
			opening := strings.TrimSpace(match[1])
			written := entry{opening: opening, line: number}
			if identity := ruleIdentityPattern.FindStringSubmatch(opening); identity != nil {
				written.id = identity[1]
				written.text = strings.TrimSpace(identity[2])
			}
			entries = append(entries, written)
			continue
		}
		if trimmed == "" || len(entries) == 0 {
			continue
		}
		current := &entries[len(entries)-1]
		if match := partPattern.FindStringSubmatch(line); match != nil {
			current.parts = append(current.parts, part{text: strings.TrimSpace(match[1]), line: number})
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(current.parts) == 0 {
				current.text = strings.TrimSpace(current.text + " " + trimmed)
			} else {
				current.loose = append(current.loose, fmt.Sprintf("line %d", number))
			}
			continue
		}
		// Prose at the margin between rules says nothing about the rule above
		// it, and is left alone.
	}
	return entries, true
}

// judge turns one written entry into a rule, or says why it cannot be one.
func judge(written entry, goals goal.Set) (Rule, string) {
	if written.id == "" {
		return Rule{}, fmt.Sprintf("the entry %q does not open with a rule identifier in square brackets", clip(written.opening))
	}
	if written.text == "" {
		return Rule{}, "it says nothing after its identifier about which messages it covers"
	}
	if len(written.loose) > 0 {
		return Rule{}, fmt.Sprintf("%s under it is not a part written as `- name: value`", strings.Join(written.loose, " and "))
	}
	values := map[string]string{}
	for _, part := range written.parts {
		name, value, ok := strings.Cut(part.text, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if !ok || name == "" {
			return Rule{}, fmt.Sprintf("line %d, %q, is not a part written as `- name: value`", part.line, clip(part.text))
		}
		if !known(name) {
			return Rule{}, fmt.Sprintf("line %d names a part %q; a rule's parts are %s", part.line, name, strings.Join(append(append([]string(nil), requiredParts...), optionalParts...), ", "))
		}
		if _, repeated := values[name]; repeated {
			return Rule{}, fmt.Sprintf("it states its %s twice; a rule states each part once", name)
		}
		if value == "" {
			return Rule{}, fmt.Sprintf("line %d states an empty %s", part.line, name)
		}
		values[name] = value
	}
	var missing []string
	for _, name := range requiredParts {
		if _, ok := values[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Rule{}, fmt.Sprintf("it states no %s; every rule states its %s", strings.Join(missing, ", "), strings.Join(requiredParts, ", "))
	}

	rule := Rule{
		ID:          written.id,
		Description: written.text,
		Workflow:    values["workflow"],
		Goal:        strings.ToLower(values["goal"]),
		Type:        domain.WorkItemKind(values["type"]),
		Label:       values["label"],
		Title:       values["title"],
		Done:        values["done"],
		From:        values["from"],
		Line:        written.line,
	}
	if rule.Workflow != WorkflowDelivery {
		return Rule{}, fmt.Sprintf("it names the workflow %q; a rule may start only %q work in this build, because no other sequence runs yet", rule.Workflow, WorkflowDelivery)
	}
	if reason := goalRefusal(rule.Goal, goals); reason != "" {
		return Rule{}, reason
	}
	priority, err := strconv.Atoi(values["priority"])
	switch {
	case err != nil:
		return Rule{}, fmt.Sprintf("its priority %q is not a whole number from 1 to 4", values["priority"])
	case priority == 0:
		return Rule{}, "it sets priority 0; work a message starts may not go ahead of reliability work without somebody deciding that, so a rule's priority is 1 to 4"
	case priority < 0 || priority > 4:
		return Rule{}, fmt.Sprintf("its priority %d is not from 1 to 4", priority)
	}
	rule.Priority = priority
	if !rule.Type.Valid() {
		return Rule{}, fmt.Sprintf("its type %q is not one of %s", rule.Type, kinds())
	}
	if strings.ContainsAny(rule.Label, " \t,") {
		return Rule{}, fmt.Sprintf("its label %q holds a space or a comma; a rule names one label", rule.Label)
	}
	return rule, ""
}

// goalRefusal says why a rule cannot serve the goal it names, and nothing where
// it can: the goal has to be one the goals in force state under that
// identifier, and only one of them.
func goalRefusal(named string, goals goal.Set) string {
	if err := domain.ValidateIdentifier("goal", named); err != nil {
		return fmt.Sprintf("its goal %q is not a goal identifier: lower-case letters and digits joined by single hyphens", named)
	}
	for _, problem := range goals.IdentityProblems {
		if problem.Identity == named {
			return fmt.Sprintf("its goal %q is carried by more than one goal in force (%s), so it names neither", named, strings.Join(problem.Stated, ", "))
		}
	}
	stated := false
	for _, candidate := range goals.Goals {
		if candidate.Identity != named {
			continue
		}
		if candidate.InForce {
			return ""
		}
		stated = true
	}
	if stated {
		return fmt.Sprintf("its goal %q is stated only in a goals document that is not in force, so it is not an active goal", named)
	}
	return fmt.Sprintf("its goal %q is not an active goal; a rule may start only work an active goal admits", named)
}

func known(name string) bool {
	for _, part := range requiredParts {
		if part == name {
			return true
		}
	}
	for _, part := range optionalParts {
		if part == name {
			return true
		}
	}
	return false
}

func kinds() string {
	names := make([]string, 0, len(domain.WorkItemKinds))
	for _, kind := range domain.WorkItemKinds {
		names = append(names, string(kind))
	}
	return strings.Join(names, " or ")
}

// clip keeps a quoted piece of the document short enough to read in a report.
func clip(text string) string {
	const limit = 80
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}
