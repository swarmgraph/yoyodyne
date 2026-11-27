package cli

// The tests here read the shipped operator-questions prompt —
// skills/yoyo-operator-questions/SKILL.md — against the commands it tells an
// assistant to run and the status fields it tells it to read.
//
// Like the setup prompt (skill_repository_test.go), nothing executes this
// document: an assistant reads it and runs what it names. A command renamed, a
// subcommand that never existed, or a flag dropped leaves the prompt wrong in a
// way that costs the operator a failed or mistaken step rather than a red test,
// so every `yoyo` command line it names is asked for its own usage here, and the
// flags it names are looked for in that usage.

import (
	"bytes"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

const operatorQuestionsSkillPath = "../../skills/yoyo-operator-questions/SKILL.md"

// namedCommand is one `yoyo` command line the prompt names: the words of the
// command itself, and the flags given to it.
type namedCommand struct {
	text  string
	path  []string
	flags []string
}

var (
	// A command starts at the word yoyo, at the start of a code span or line or
	// after a space, and runs to the end of the span or line.
	yoyoCommandPattern = regexp.MustCompile("(?:^|[\\s`(])yoyo\\s+([^`\\n]*)")
	// A quoted argument is a message or a reason, never a flag.
	quotedArgumentPattern = regexp.MustCompile(`"[^"]*"`)
	commandWordPattern    = regexp.MustCompile(`^[a-z][a-z-]*$`)
	flagPattern           = regexp.MustCompile(`^--([a-z][a-z-]*)(?:=.*)?$`)
)

// commandsNamedIn reads every yoyo command line out of a document's code spans
// and code blocks. The command is the run of plain words after yoyo, so the
// document writes a positional argument as a placeholder in angle brackets —
// `yoyo amendment show <id>` — and a bare word there is read as a subcommand.
func commandsNamedIn(document string) []namedCommand {
	var commands []namedCommand
	for _, code := range codeIn(document) {
		for _, match := range yoyoCommandPattern.FindAllStringSubmatch(code, -1) {
			rest := quotedArgumentPattern.ReplaceAllString(match[1], " ")
			command := namedCommand{text: "yoyo " + strings.TrimSpace(match[1])}
			inPath := true
			for _, token := range strings.Fields(rest) {
				if inPath && commandWordPattern.MatchString(token) {
					command.path = append(command.path, token)
					continue
				}
				inPath = false
				if flag := flagPattern.FindStringSubmatch(token); flag != nil {
					command.flags = append(command.flags, flag[1])
				}
			}
			if len(command.path) > 0 {
				commands = append(commands, command)
			}
		}
	}
	return commands
}

// codeIn is every code span's text and every line of every code block.
func codeIn(document string) []string {
	var code []string
	inBlock := false
	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock {
			code = append(code, line)
			continue
		}
		spans := strings.Split(line, "`")
		for index := 1; index < len(spans); index += 2 {
			code = append(code, spans[index])
		}
	}
	return code
}

// commandProblem asks the command for its own usage and says what of the named
// command line it does not recognize, or "" where it recognizes all of it. A
// command recognizes itself by opening its usage with its own name: a word
// yoyo does not know as a subcommand is read as a positional argument of the
// words before it, whose usage then names only them.
func commandProblem(t *testing.T, command namedCommand) string {
	t.Helper()

	var stdout, stderr bytes.Buffer
	Run(append(append([]string{}, command.path...), "--help"), &stdout, &stderr, "test")
	usage := stdout.String() + stderr.String()
	name := strings.Join(command.path, " ")
	if !strings.HasPrefix(usage, "Usage of "+name+":") && !strings.HasPrefix(usage, "Usage: yoyo "+name+" ") {
		first, _, _ := strings.Cut(usage, "\n")
		return "`yoyo " + name + "` is not a command; asked for its usage, yoyo answered " + strconv.Quote(first)
	}
	for _, flag := range command.flags {
		if !regexp.MustCompile(`(?m)^\s+--?` + regexp.QuoteMeta(flag) + `(\s|$)`).MatchString(usage) {
			return "`yoyo " + name + "` has no --" + flag + " flag"
		}
	}
	return ""
}

// TestTheOperatorQuestionsSkillNamesOnlyCommandsThatExist is the guarantee the
// prompt's reader depends on: every command it says to run is one this build
// has, with the flags it says to give it.
func TestTheOperatorQuestionsSkillNamesOnlyCommandsThatExist(t *testing.T) {
	commands := commandsNamedIn(readOperatorQuestionsSkill(t))
	// The prompt gathers from five sources and records through six commands, so
	// finding few command lines means the reading above stopped finding them,
	// not that the prompt stopped naming them.
	if len(commands) < 15 {
		t.Fatalf("read only %d yoyo command lines from %s; the reading has stopped finding them", len(commands), operatorQuestionsSkillPath)
	}
	for _, command := range commands {
		if problem := commandProblem(t, command); problem != "" {
			t.Errorf("%s names %q: %s", operatorQuestionsSkillPath, command.text, problem)
		}
	}
}

// TestTheCommandCheckRefusesWhatDoesNotExist shows the check above failing on
// the three ways a named command can be wrong: a command yoyo does not have, a
// subcommand it does not have under a command it does, and a flag it does not
// take. The second is the case the harness's own ownership registry once named
// as a capability, `yoyo chat answer`, where a concern is really answered by a
// message to the conversation.
func TestTheCommandCheckRefusesWhatDoesNotExist(t *testing.T) {
	for _, text := range []string{
		"`yoyo questions list`",
		"`yoyo chat answer <id>`",
		"`yoyo status --waiting-on-me`",
	} {
		commands := commandsNamedIn(text)
		if len(commands) != 1 {
			t.Fatalf("read %d commands from %s, want 1", len(commands), text)
		}
		if commandProblem(t, commands[0]) == "" {
			t.Errorf("the check accepted %s, which this build does not have", text)
		}
	}
	accepted := commandsNamedIn("`yoyo amendment approve <id> --reason \"his --words\"` and `yoyo status --json`")
	if len(accepted) != 2 {
		t.Fatalf("read %d commands, want 2", len(accepted))
	}
	for _, command := range accepted {
		if problem := commandProblem(t, command); problem != "" {
			t.Errorf("the check refused %q, which exists: %s", command.text, problem)
		}
	}
}

// TestTheOperatorQuestionsSkillNamesWhatADecisionCarries holds the prompt to
// the status fields it decides from: every part of a decision's account, the
// mover that marks an entry as the operator's, and the whole closed list of
// reasons an entry is his — a part of the list left out teaches the assistant
// that the rest are not reasons.
func TestTheOperatorQuestionsSkillNamesWhatADecisionCarries(t *testing.T) {
	t.Parallel()

	document := readOperatorQuestionsSkill(t)
	for _, field := range jsonFieldNames(reflect.TypeOf(readmodel.DecisionAccount{})) {
		if !strings.Contains(document, "`"+field+"`") {
			t.Errorf("%s does not name the %q part of a decision's account", operatorQuestionsSkillPath, field)
		}
	}
	if !strings.Contains(document, "`"+string(ownership.MoverOperator)+"`") {
		t.Errorf("%s does not name the mover %q that marks an entry as the operator's", operatorQuestionsSkillPath, ownership.MoverOperator)
	}
	for _, reason := range ownership.OperatorReasons() {
		if !strings.Contains(document, `"`+string(reason)+`"`) {
			t.Errorf("%s does not name the reason %q", operatorQuestionsSkillPath, reason)
		}
	}
	if short := ownership.ShortReasonLists(document); len(short) != 0 {
		t.Errorf("%s lists only some of the operator's reasons in: %q", operatorQuestionsSkillPath, short)
	}
}

// TestTheOperatorQuestionsSkillIsReachableFromTheReadme: a prompt nothing in
// the README points at is one nobody installs.
func TestTheOperatorQuestionsSkillIsReachableFromTheReadme(t *testing.T) {
	t.Parallel()

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(readme), "skills/yoyo-operator-questions/SKILL.md") {
		t.Fatalf("the README does not point at %s", operatorQuestionsSkillPath)
	}
}

func readOperatorQuestionsSkill(t *testing.T) string {
	t.Helper()

	document, err := os.ReadFile(operatorQuestionsSkillPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return string(document)
}
