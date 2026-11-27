package cli

// The ownership registry (internal/ownership) names, on every waiting entry,
// the command that settles it, and `yoyo status --json`, the dashboard, and the
// operator-questions prompt pass that name on to whoever reads the entry. A
// name this build does not have sends the operator or his assistant to a
// failed or mistaken step, so every `yoyo` command line the package's code
// carries is asked for its own usage here, the way the operator-questions
// prompt's commands are (skill_operator_questions_test.go).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const ownershipPackageDir = "../ownership"

var (
	// A command in a registry string starts at the word yoyo, which is never
	// the end of an earlier command's words: "yoyo stop and yoyo start" is two.
	yoyoWordPattern = regexp.MustCompile(`(?:^|\s)yoyo\s+`)
	// A path word may name alternatives, as "approve|decline" does.
	alternativesPattern = regexp.MustCompile(`^[a-z][a-z-]*(?:\|[a-z][a-z-]*)+$`)
)

// ownershipStrings is every string literal in the ownership package's own
// code, tests left out.
func ownershipStrings(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(ownershipPackageDir, "*.go"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	var literals []string
	fileSet := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("ParseFile(%s) error = %v", file, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				if value, err := strconv.Unquote(literal.Value); err == nil {
					literals = append(literals, value)
				}
			}
			return true
		})
	}
	return literals
}

// commandsInRegistryString reads the yoyo command lines out of one string. A
// sentence quotes its commands in code spans, so only those are read; a string
// with none is a capability, which is code as a whole. Each command runs to the
// next yoyo, a comma, or the end, less a joining "and"; a path word naming
// alternatives is each of them.
func commandsInRegistryString(text string) []namedCommand {
	code := []string{text}
	if strings.Contains(text, "`") {
		code = codeIn(text)
	}
	var commands []namedCommand
	for _, piece := range code {
		locations := yoyoWordPattern.FindAllStringIndex(piece, -1)
		for index, location := range locations {
			end := len(piece)
			if index+1 < len(locations) {
				end = locations[index+1][0]
			}
			words, _, _ := strings.Cut(piece[location[1]:end], ",")
			words = strings.TrimSuffix(strings.TrimSpace(words), " and")
			for _, choice := range expandAlternatives(words) {
				commands = append(commands, commandsNamedIn("`yoyo "+choice+"`")...)
			}
		}
	}
	return commands
}

// expandAlternatives is a command's words once for every alternative a word
// names, so "amendment approve|decline" asks after both subcommands.
func expandAlternatives(words string) []string {
	fields := strings.Fields(words)
	for index, field := range fields {
		if !alternativesPattern.MatchString(field) {
			continue
		}
		var expanded []string
		for _, choice := range strings.Split(field, "|") {
			chosen := append(append(append([]string{}, fields[:index]...), choice), fields[index+1:]...)
			expanded = append(expanded, expandAlternatives(strings.Join(chosen, " "))...)
		}
		return expanded
	}
	return []string{words}
}

// TestTheOwnershipRegistryNamesOnlyCommandsThatExist is the guarantee every
// waiting entry's reader depends on: each command the registry names as what
// settles an entry is one this build has, with the flags it names.
func TestTheOwnershipRegistryNamesOnlyCommandsThatExist(t *testing.T) {
	var commands []namedCommand
	for _, literal := range ownershipStrings(t) {
		commands = append(commands, commandsInRegistryString(literal)...)
	}
	// The registry settles entries through a dozen commands or more, so finding
	// few means the reading above stopped finding them.
	if len(commands) < 20 {
		t.Fatalf("read only %d yoyo command lines from %s; the reading has stopped finding them", len(commands), ownershipPackageDir)
	}
	for _, command := range commands {
		if problem := commandProblem(t, command); problem != "" {
			t.Errorf("internal/ownership names %q: %s", command.text, problem)
		}
	}
}

// TestTheRegistryCommandReadingFindsEachForm shows the reading above taking
// apart each form a registry string writes its commands in, and refusing the
// names the registry once gave for answering a concern and deciding a
// document, which no build has had.
func TestTheRegistryCommandReadingFindsEachForm(t *testing.T) {
	for text, want := range map[string][]string{
		"yoyo stop and yoyo start, from an ordinary terminal":                                               {"stop", "start"},
		"yoyo amendment approve|decline":                                                                    {"amendment approve", "amendment decline"},
		`yoyo agent chat <agent> --message "approve <document>" or --message "decline <document> <reason>"`: {"agent chat"},
		"nothing runs until `yoyo resume` lifts it, and `yoyo work --watch` starts one":                     {"resume", "work"},
	} {
		var got []string
		for _, command := range commandsInRegistryString(text) {
			got = append(got, strings.Join(command.path, " "))
		}
		if strings.Join(got, "; ") != strings.Join(want, "; ") {
			t.Errorf("commandsInRegistryString(%q) = %q, want %q", text, got, want)
		}
	}
	for _, text := range []string{"yoyo chat answer", "yoyo agent chat approve|decline", "yoyo restart"} {
		refused := false
		for _, command := range commandsInRegistryString(text) {
			if commandProblem(t, command) != "" {
				refused = true
			}
		}
		if !refused {
			t.Errorf("the check accepted %q, which this build does not have", text)
		}
	}
}
