package terms

// Vocabulary, in terms.go, is the terms already found. This is the half of the
// check that finds the next one: it reads the harness's own words wherever a
// person or a role reads them, picks out every compound word in them, and
// refuses one that nothing accounts for — not the register, not the replaced
// table, not the inventory of terms awaiting a decision, and not the list of
// ordinary compounds. So a new coinage is refused the first time it is
// written, naming the file and line, rather than reaching a person who then
// has to ask what it means.
//
// What it looks for is one shape: letters joined by hyphens, `whose-move`,
// `carry-out`, `needs-a-human`. That is the shape this project's coinages have
// most often taken and the one shape a check can pick out of prose without
// mistaking ordinary words for it. An ordinary word given a new sense —
// `docket`, `lane` — has no shape at all, and stays the reviewer's to catch.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/terms/inventory"
)

// OrdinaryHeading is the heading in the register under which the ordinary
// compounds are listed: words with a hyphen in them that are plain English or a
// name, not a term of art, written as code spans. A compound listed there
// passes wherever it is written.
const OrdinaryHeading = "## Ordinary compounds"

// CompoundSources are the trees whose Go string literals are read for
// compounds: the program, every package of it. A literal is read only when it
// holds a space, because one with none is a key, a flag, or an identifier
// rather than words anybody reads — which is the same rule the inventory
// counts by.
var CompoundSources = []string{"cmd", "internal"}

// PersonaHomes are where the personas are read from: the templates the
// executable ships, and the copies this project's roles actually read.
var PersonaHomes = []string{"internal/config/builtin", ".yoyodyne/personas"}

// ordinaryPrefixes are the first parts that make an ordinary English word of
// whatever follows them: `re-run`, `non-zero`, `self-hosting`, `mid-turn`. A
// compound starting with one is a word formed the way English forms words, not
// a coinage.
var ordinaryPrefixes = map[string]bool{
	"all": true, "anti": true, "auto": true, "bi": true, "co": true, "counter": true,
	"cross": true, "de": true, "dis": true, "ex": true, "half": true, "inter": true,
	"mid": true, "multi": true, "non": true, "off": true, "on": true, "out": true,
	"over": true, "per": true, "post": true, "pre": true, "pro": true, "re": true,
	"self": true, "semi": true, "sub": true, "super": true, "un": true, "under": true,
	"well": true, "in": true,
}

// ordinarySuffixes are the last parts English makes an adjective with:
// `read-only`, `repository-wide`, `operator-facing`, `case-insensitive`.
var ordinarySuffixes = map[string]bool{
	"agnostic": true, "appropriate": true, "aware": true, "based": true, "bearing": true,
	"critical": true, "eligible": true, "facing": true, "free": true, "independent": true,
	"insensitive": true, "level": true, "like": true, "local": true, "native": true,
	"only": true, "producing": true, "proof": true, "readable": true, "ready": true,
	"relative": true, "safe": true, "sensitive": true, "shaped": true, "side": true,
	"specific": true, "style": true, "time": true, "visible": true, "wide": true,
}

// names are the first parts that make a compound the name of the harness or
// of a tool it runs: `yoyodyne-report` is a block an agent writes, and
// `claude-code` and `codex-cli` are programs.
var names = map[string]bool{"claude": true, "codex": true, "yoyo": true, "yoyodyne": true}

// numberWords are the parts that make a count or a measure of a compound:
// `two-hour`, `thirty-three`, `first-seen`.
var numberWords = map[string]bool{
	"one": true, "two": true, "three": true, "four": true, "five": true, "six": true,
	"seven": true, "eight": true, "nine": true, "ten": true, "eleven": true, "twelve": true,
	"fifteen": true, "twenty": true, "thirty": true, "forty": true, "fifty": true,
	"sixty": true, "hundred": true, "first": true, "second": true, "third": true,
	"last": true, "half": true, "single": true, "double": true,
}

// compoundPattern is a compound word: two or more runs of letters joined by
// single hyphens. What comes either side of it is checked separately, in
// compoundsIn, because Go's expressions cannot look behind.
var compoundPattern = regexp.MustCompile(`[A-Za-z]+(?:-[A-Za-z]+)+`)

// ordinaryCompound says whether a compound is ordinary English by its form
// alone: a first part that is an English prefix, a last part English makes an
// adjective with or one ending in -ed, a first part that names the harness or a
// tool, a part that is a number word or a single letter (`e-mail`, `x-ray`), or
// every part capitalised, which is a name (`Claude-Code`) rather than a term.
func ordinaryCompound(word string) bool {
	parts := strings.Split(word, "-")
	first, last := strings.ToLower(parts[0]), strings.ToLower(parts[len(parts)-1])
	if ordinaryPrefixes[first] || ordinarySuffixes[last] || names[first] || strings.HasSuffix(last, "ed") {
		return true
	}
	capitalised := true
	for _, part := range parts {
		if len(part) == 1 || numberWords[strings.ToLower(part)] {
			return true
		}
		if part[0] < 'A' || part[0] > 'Z' {
			capitalised = false
		}
	}
	return capitalised
}

// identifierBefore and identifierAfter are the characters that make a compound
// part of something longer: a path, a flag, a file name, an address, a key. A
// compound touching one is an identifier, which the register does not govern.
const (
	identifierBefore = "_-./@#$=:~\\"
	identifierAfter  = "_-/@=\\"
)

// compoundsIn is every compound in one stretch of text that is not part of an
// identifier, with the offset each starts at.
func compoundsIn(text string) [][2]int {
	var found [][2]int
	for _, at := range compoundPattern.FindAllStringIndex(text, -1) {
		start, end := at[0], at[1]
		if start > 0 && (isWordByte(text[start-1]) || strings.IndexByte(identifierBefore, text[start-1]) >= 0) {
			continue
		}
		if end < len(text) {
			next := text[end]
			if isWordByte(next) || strings.IndexByte(identifierAfter, next) >= 0 {
				continue
			}
			// A full stop or a colon with something straight after it is a file
			// name, a host and port, or a key and its value — `run-stops.md` —
			// rather than the end of a sentence.
			if (next == '.' || next == ':') && end+1 < len(text) && (isWordByte(text[end+1]) || text[end+1] == '/') {
				continue
			}
		}
		// A compound between quotes of one kind is a value being named — a
		// field's, an action's, a reason's — rather than a word in a sentence.
		if start > 0 && end < len(text) && (text[start-1] == '"' || text[start-1] == '\'') && text[end] == text[start-1] {
			continue
		}
		found = append(found, [2]int{start, end})
	}
	return found
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// codeSpanOrLink is what Markdown prose carries that is not prose: a code span,
// a link's target, an autolink, and an HTML comment or tag. A compound in one
// of those is a command, a path, an anchor, or markup.
var codeSpanOrLink = regexp.MustCompile("`[^`]*`|\\]\\([^)]*\\)|<[^>]*>|https?://\\S+")

// Ordinary reads the ordinary compounds the register lists, lowercased.
func Ordinary(root string) (map[string]bool, error) {
	content, err := read(filepath.Join(root, filepath.FromSlash(RegisterPath)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", RegisterPath, err)
	}
	return ordinaryIn(content), nil
}

func ordinaryIn(content string) map[string]bool {
	ordinary := make(map[string]bool)
	within := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			within = trimmed == OrdinaryHeading
			continue
		}
		if !within {
			continue
		}
		for _, span := range codeSpan.FindAllStringSubmatch(trimmed, -1) {
			ordinary[strings.ToLower(span[1])] = true
		}
	}
	return ordinary
}

// PersonaFiles is every persona under the persona homes, repository-relative
// and sorted. A home that does not exist holds none: a project that has not
// written its own copies reads the templates.
func PersonaFiles(root string) ([]string, error) {
	var files []string
	for _, home := range PersonaHomes {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(home)), func(current string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				return nil
			}
			relative, err := filepath.Rel(root, current)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(relative))
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("walk %s for personas: %w", home, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

// compoundSourceFiles is every Go file under the compound sources that is not
// a test and not a fixture, repository-relative and sorted.
func compoundSourceFiles(root string) ([]string, error) {
	var files []string
	for _, home := range CompoundSources {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(home)), func(current string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, current)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(relative))
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("walk %s for sources: %w", home, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

// accounted is what a compound may be accounted for by: a term the register
// lists either way, a term the check already looks for, or a term in the
// inventory, whatever its decision. A compound a pattern matches anywhere in is
// that term's business rather than a new one — `whose-move` is refused as the
// replaced term it is, and `lane-label` waits on `lane`'s decision.
type accounted struct {
	patterns []*regexp.Regexp
	ordinary map[string]bool
	// documents are the names of this repository's own Markdown documents: a
	// compound that is one, `observability-and-dashboard`, is the name of a
	// design or a decision written where a reader can look it up.
	documents map[string]bool
}

func accountFor(entries []Entry, replacements []Replacement, ordinary, documents map[string]bool) accounted {
	var known accounted
	known.ordinary = ordinary
	known.documents = documents
	for _, entry := range entries {
		known.patterns = append(known.patterns, pattern(Coinage{Match: entry.Term}))
	}
	for _, replacement := range replacements {
		known.patterns = append(known.patterns, pattern(Coinage{Match: replacement.Term}))
	}
	for _, coinage := range Vocabulary {
		known.patterns = append(known.patterns, pattern(coinage))
	}
	for _, term := range inventory.Inventory {
		known.patterns = append(known.patterns, inventory.Pattern(term))
	}
	return known
}

func (known accounted) covers(word string) bool {
	lower := strings.ToLower(word)
	if known.ordinary[lower] || known.documents[lower] || ordinaryCompound(word) {
		return true
	}
	for _, pattern := range known.patterns {
		if pattern.MatchString(word) {
			return true
		}
	}
	return false
}

// Compounds is the refusal for every compound word the harness uses where a
// person or a role reads it that nothing accounts for — in the string literals
// of the program, the personas, the guides, and the governed documents — and
// for every word the inventory lists as unread that nothing writes any more.
// Each compound is reported once per line it is written on, in the order the
// files are walked.
func Compounds(root string) ([]Problem, error) {
	return compoundProblems(root, inventory.Unread)
}

// compoundProblems is Compounds with the unread words given, so a test can
// hold a fixture to a list of its own.
func compoundProblems(root string, unread []string) ([]Problem, error) {
	entries, err := Register(root)
	if err != nil {
		return nil, err
	}
	replacements, err := Replaced(root)
	if err != nil {
		return nil, err
	}
	ordinary, err := Ordinary(root)
	if err != nil {
		return nil, err
	}
	documentNames, err := documentNames(root)
	if err != nil {
		return nil, err
	}
	known := accountFor(entries, replacements, ordinary, documentNames)
	pending := make(map[string]bool, len(unread))
	for _, word := range unread {
		pending[strings.ToLower(word)] = true
	}
	written := make(map[string]bool)

	var problems []Problem
	report := func(path string, stretches []passage, markdown bool) {
		for _, stretch := range stretches {
			text := stretch.text
			if markdown {
				text = blankOut(text)
			}
			seen := make(map[string]bool)
			for _, at := range compoundsIn(text) {
				spelled := text[at[0]:at[1]]
				word := strings.ToLower(spelled)
				line := stretch.lineOf(at[0])
				key := fmt.Sprintf("%d %s", line, word)
				if seen[key] || known.covers(spelled) {
					continue
				}
				seen[key] = true
				if pending[word] {
					written[word] = true
					continue
				}
				problems = append(problems, Problem{
					Path: path, Line: line, Term: word,
					Reason: fmt.Sprintf("%q is a compound word nothing defines; write the ordinary words it stands for, "+
						"or register it with a row in %s, or, if it is ordinary English or a name, list it under %q there",
						word, RegisterPath, strings.TrimPrefix(OrdinaryHeading, "## ")),
				})
			}
		}
	}

	files, err := compoundSourceFiles(root)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		stretches, err := stringsIn(root, file)
		if err != nil {
			return nil, err
		}
		var spoken []passage
		for _, stretch := range stretches {
			if strings.ContainsAny(stretch.text, " \n") {
				spoken = append(spoken, stretch)
			}
		}
		report(file, spoken, false)
	}

	personas, err := PersonaFiles(root)
	if err != nil {
		return nil, err
	}
	guides, err := GuideFiles(root)
	if err != nil {
		return nil, err
	}
	documents, err := Documents(root)
	if err != nil {
		return nil, err
	}
	for _, group := range [][]string{personas, guides, documents} {
		for _, file := range group {
			content, err := read(filepath.Join(root, filepath.FromSlash(file)))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", file, err)
			}
			report(file, passages(strings.Split(content, "\n")), true)
		}
	}

	for _, word := range unread {
		if !written[strings.ToLower(word)] {
			problems = append(problems, Problem{
				Path: UnreadPath, Term: word,
				Reason: fmt.Sprintf("%q is listed as unread and nothing reads it as an unaccounted compound any more; take it off the list", word),
			})
		}
	}
	return problems, nil
}

// UnreadPath is where the unread compounds are listed, so a word taken out of
// every text can be taken off the list too.
const UnreadPath = "internal/terms/inventory/inventory.go"

// documentNames is the name of every Markdown file under `docs/`, without its
// extension: the identifier a governed document is cited by, and the name a
// guide is linked by.
func documentNames(root string) (map[string]bool, error) {
	named := make(map[string]bool)
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			named[strings.ToLower(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))] = true
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("walk docs for document names: %w", err)
	}
	return named, nil
}

// blankOut replaces what Markdown carries that is not prose with spaces of the
// same length, so the offsets of what is left still fall on the lines they were
// written on.
func blankOut(text string) string {
	return codeSpanOrLink.ReplaceAllStringFunc(text, func(span string) string {
		blank := []byte(span)
		for index := range blank {
			if blank[index] != '\n' {
				blank[index] = ' '
			}
		}
		return string(blank)
	})
}
