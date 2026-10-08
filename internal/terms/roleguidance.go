package terms

// Roles copy their words from what they are told, so a term retired from every
// command and document and still written in a role's own guidance comes back
// in the next thing that role writes for a person. This is the check on that
// guidance: the personas the executable ships, the bundle beside them, and the
// Go sources holding the role contracts and the pass prompts are held to
// carrying none of the terms the register lists as replaced.
//
// It is separate from Check because Check reads the packages a command prints
// from, for every term the vocabulary knows, and the packages that build role
// contracts print a great deal besides those contracts; widening Check to them
// is the terms check's own coverage work. What is read here is narrower in
// terms and wider in places: only the replaced terms, everywhere a role reads
// its instructions from.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ShippedGuidance is where the executable's own role guidance lives: the
// personas and the bundle `yoyo init` gives a new project. The copies a
// project binds under .yoyodyne/personas are the project's, and are not read
// here.
var ShippedGuidance = []string{"internal/config/builtin"}

// GuidanceSources are the Go packages and files whose string literals are a
// role's contract or a pass prompt: the developer contract and the recurring
// pass prompts (internal/orchestrator), the conversation contracts
// (internal/chat), the reviewer's (internal/review), the contract fragments
// every role is given (the rest of the packages), the pass prompts `yoyo init`
// writes (internal/config), and the shared sentences this package states once
// for all of them. Every string literal in them is read, contract or not,
// because a printed string in one of these packages is read by somebody too.
var GuidanceSources = []string{
	"internal/amendment",
	"internal/artifact",
	"internal/chat",
	"internal/config",
	"internal/evaluation",
	"internal/exchange",
	"internal/landing",
	"internal/orchestrator",
	"internal/report",
	"internal/repositoryread",
	"internal/research",
	"internal/review",
	"internal/selfcheck",
	"internal/sidestream",
	"internal/sweep",
	"internal/terms/itemnaming.go",
	"internal/terms/livecopy.go",
	"internal/terms/operatorapproval.go",
	"internal/terms/personwriting.go",
	"internal/terms/standinggoals.go",
}

// QuotedExample is the sentence the operator was sent and could not read,
// which every persona and the shared writing rule quote on purpose as what
// not to write. It is the one place a role's guidance may carry the retired
// words it holds, and only as this quotation: any other use of them is refused.
const QuotedExample = "stopped by the harness's idle bound when the provider's stream went silent, settled as an environmental stop"

// quotedExample matches the quotation however a line wrap has spaced it.
var quotedExample = regexp.MustCompile(`(?i)` + strings.Join(strings.Fields(regexp.QuoteMeta(QuotedExample)), `\s+`))

// GuidanceFiles is every file ShippedGuidance and GuidanceSources name: the
// shipped personas and bundle, and the Go files of the sources less their
// tests, repository-relative and sorted. Exported for the reason Documents is:
// a walk that found nothing and guidance with nothing wrong in it are the same
// result otherwise.
func GuidanceFiles(root string) ([]string, error) {
	var files []string
	walk := func(home string, keep func(name string) bool) error {
		return filepath.WalkDir(filepath.Join(root, filepath.FromSlash(home)), func(current string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !keep(entry.Name()) {
				return nil
			}
			relative, err := filepath.Rel(root, current)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(relative))
			return nil
		})
	}
	for _, home := range ShippedGuidance {
		err := walk(home, func(name string) bool {
			extension := strings.ToLower(filepath.Ext(name))
			return extension == ".md" || extension == ".yaml"
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("walk %s for role guidance: %w", home, err)
		}
	}
	for _, source := range GuidanceSources {
		err := walk(source, func(name string) bool {
			return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("walk %s for role guidance: %w", source, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

// Guidance reports every replaced term written in the shipped role guidance,
// naming the file, the line, and the wording the register gives instead. The
// quoted example is not read. A row of the replaced table that excuses a
// governed document excuses nothing here: guidance is not a governed document,
// and the excuse exists only for an owner who has not yet amended one.
func Guidance(root string) ([]Problem, error) {
	replacements, err := Replaced(root)
	if err != nil {
		return nil, err
	}
	type retired struct {
		term, words string
		pattern     *regexp.Regexp
	}
	var looked []retired
	for _, replacement := range replacements {
		// A term the vocabulary knows is looked for in every spelling it lists;
		// one it does not is looked for as the register writes it.
		spellings := []Coinage{{Match: replacement.Term}}
		for _, coinage := range Vocabulary {
			if coinage.Term == replacement.Term {
				if spellings[0].Term == "" {
					spellings = spellings[:0]
				}
				spellings = append(spellings, coinage)
			}
		}
		for _, spelling := range spellings {
			looked = append(looked, retired{term: replacement.Term, words: replacement.PlainWords, pattern: pattern(spelling)})
		}
	}
	files, err := GuidanceFiles(root)
	if err != nil {
		return nil, err
	}
	var problems []Problem
	for _, file := range files {
		var stretches []passage
		if strings.HasSuffix(file, ".go") {
			stretches, err = stringsIn(root, file)
			if err != nil {
				return nil, err
			}
		} else {
			content, err := read(filepath.Join(root, filepath.FromSlash(file)))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", file, err)
			}
			stretches = passagesFrom(strings.Split(content, "\n"), 1)
		}
		for _, stretch := range stretches {
			text := WithoutQuotedExample(stretch.text)
			for _, term := range looked {
				reported := make(map[int]bool)
				for _, match := range term.pattern.FindAllStringIndex(text, -1) {
					line := stretch.lineOf(match[0])
					if reported[line] {
						continue
					}
					reported[line] = true
					problems = append(problems, Problem{Path: file, Line: line, Term: term.term,
						Reason: fmt.Sprintf("%q is listed in %s as replaced, and a role copies what its guidance says; write %s", term.term, RegisterPath, term.words)})
				}
			}
		}
	}
	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].Path != problems[j].Path {
			return problems[i].Path < problems[j].Path
		}
		return problems[i].Line < problems[j].Line
	})
	return problems, nil
}

// WithoutQuotedExample is text with the quoted example blanked out, byte for
// byte, so nothing after it moves. The vocabulary inventory counts with it
// too: the example is quoted on purpose, and counting its words as somewhere
// they are still written would make the model of what not to write look like
// work left to do.
func WithoutQuotedExample(text string) string {
	return quotedExample.ReplaceAllStringFunc(text, blank)
}

// blank is a stretch of text with every byte but a line break turned to a
// space, so what follows it keeps both its offset and its line.
func blank(text string) string {
	blanked := []byte(text)
	for index := range blanked {
		if blanked[index] != '\n' {
			blanked[index] = ' '
		}
	}
	return string(blanked)
}
