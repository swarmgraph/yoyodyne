// Package terms is the register of coined words this project's governed
// documents are allowed to use, and the check that one already swept out has not
// come back with nothing defining it.
//
// The rule it serves is the legibility goal's: user-facing language chooses the
// ordinary, literal word over metaphor, coinage, or term of art — unless the
// term is defined in `docs/terms.md`. Registration is the whole of the
// exception. A word that names a real mechanism an operator meets in command
// output is worth keeping and cheap to define; a word that decorates a sentence
// nothing else in it needed is worth replacing. What is not acceptable is the
// third case, which is what this check exists for: a coinage in an
// operator-facing document with no plain-word definition anywhere, so a reader
// meeting it has nowhere to go.
//
// The vocabulary below is not a guess at what a coinage looks like. It is the
// inventory the yoyodyne-ifd.206 sweep measured, term by term, with the ordinary
// wording that sweep recorded for each. That is deliberately a closed list: no
// deterministic check can tell a word somebody coined this morning from an
// ordinary one, so this is the mechanical floor for the terms already known, and
// a new coinage is caught by a reviewer rather than here. What the floor buys is
// that the sweep does not have to be run twice — a term taken out of these
// documents cannot quietly come back, and a term kept has to say what it means.
//
// The list is closed but the spelling is not. A term is looked for however its
// parts are spaced — hyphenated, spaced, closed up, or broken by a line wrap —
// because those are one word to a reader and a check that knew only the spelling
// the sweep recorded would be a floor a hyphen walks over. `minute-zero` did
// exactly that to the sweep and to the first version of this check, which is why
// pattern is written the way it is.
//
// Where the register decides and the check only reports: adding an entry to
// `docs/terms.md` permits a term, and removing the entry forbids it again,
// neither of which is a change to this package. A term nothing here lists is
// registrable too — the register is the authority, and the vocabulary is only
// the set the check knows to look for.
//
// The documents are not the only place a term reaches a reader. The strings a
// command prints, the help it shows, and the lines the notifier posts are what
// an operator reads most, and a term swept out of the documents and left in
// the help text has not been swept out. So the check reads those strings too
// (Sources), held to the same register: a term with no row is refused there
// exactly as it is in a document, and a term the register lists as replaced is
// refused whatever any document still says.
package terms

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RegisterPath is where the register lives, repository-relative. It sits outside
// the artifact homes on purpose: it is a reference every role reads and no role
// owns as an artifact, and putting it inside one would make each entry an
// amendment to somebody's document.
const RegisterPath = "docs/terms.md"

// RegisterHeading is the heading whose table is the register. The rows are read
// from under one heading rather than from every table in the file, because the
// document also lists the words that were replaced rather than registered, and a
// parser that took every table would register exactly the terms the sweep took
// out.
const RegisterHeading = "## The register"

// InventoryPath is the generated inventory of the harness's coined vocabulary,
// repository-relative. It is not read as a guide for the reason the register is
// not: its whole content is the terms themselves, each named so a decision can
// be made about it, including the ones retired — a document listing every word
// to stop writing has to write every one of them.
const InventoryPath = "docs/vocabulary-inventory.md"

// ReplacedHeading is the heading whose table lists the terms that were replaced
// rather than registered. The check reads it for one thing only: a replaced
// term's row may name the governed documents that still carry it while their
// owner amends them, and those documents — and nothing else — are excused for
// that term until the row stops naming them.
const ReplacedHeading = "## Replaced rather than registered"

// Homes are the document trees this check reads: the artifact homes the sweep
// covered. The guides under `docs/` are operator-facing too and are not read
// for the whole vocabulary — the sweep measured the homes, and holding
// documents to an inventory nobody has taken over them would fail on words
// nobody was asked about. They are read for the few terms marked Guides; see
// Guides.
var Homes = []string{"docs/product", "docs/designs", "docs/decisions"}

// Guides are the operator guides: the README, and every Markdown file under
// `docs/` that is not in a home, not the register, and not under one of the
// record directories in NotGuides. They are read for the terms a Coinage marks
// Guides and for nothing else, because no sweep has been run over them for the
// rest: a term held there is one whose row the guides lean on, so removing the
// row fails on the guide prose that uses it rather than leaving that prose
// using a word nothing defines.
var Guides = []string{"README.md", "docs"}

// NotGuides are the directories under `docs/` that hold records rather than
// guides. A diagnosis or a release note says what was true on a date in the
// words used then, and rewording one to follow the register falsifies it.
var NotGuides = []string{"docs/diagnoses", "docs/experiments", "docs/releases"}

// Sources are the Go packages whose string literals this check reads: the
// command line, the conversation, the notifier and the Slack sink, the read
// model every surface projects, the dashboard, the two packages whose
// refusals `yoyo directive` and `yoyo goals` print word for word, and the
// provider and configuration packages whose refusals configuration loading
// and `yoyo doctor` print. These are the
// strings an operator reads, so a term retired from the documents and still in
// one of them has not been retired. Only string literals are read, and only
// outside test files: a comment is written for whoever reads the code, and a
// test names the wording it refuses as often as the wording it wants.
var Sources = []string{
	"internal/backend",
	"internal/chat",
	"internal/cli",
	"internal/config",
	"internal/dashboard",
	"internal/directive",
	"internal/goal",
	"internal/notify",
	"internal/readmodel",
	"internal/slack",
}

// Assets are the files the dashboard ships to a browser, read whole rather
// than as string literals: what a script says on the page and what it says in
// a comment beside it are one file to the check, because nothing cheap tells
// them apart in a language this package does not parse.
var Assets = []string{"internal/dashboard/assets"}

// MaxFileBytes bounds one document read, for the reason internal/doclink bounds
// its own: a walk that meets something enormous reports it rather than reading
// it into memory.
const MaxFileBytes = 4 << 20

// Coinage is one word the sweep found and wrote an ordinary wording for.
type Coinage struct {
	// Term is the word as the register names it, which is what an entry has to
	// say for this term to be permitted.
	Term string
	// Match is what the scan looks for at a word start, which is the stem rather
	// than the term where a term inflects: `wedge` catches `wedged` and `starv`
	// catches `starving`, the same way the sweep counted them. Where the stem is
	// written in more than one part, every spacing of those parts is looked for
	// rather than only the one written here — see pattern.
	Match string
	// Whole holds the match to a whole word, for a term whose stem also begins an
	// ordinary word that is not it. `seamless` is not `seam`, and reporting it
	// with `seam`'s wording would tell an author to name a boundary in a sentence
	// that has none. The cost is that a plural is missed, which is the cheaper of
	// the two mistakes: a term this check misses a reviewer still catches, and a
	// term it reports wrongly is a check people learn to argue with.
	Whole bool
	// Guides holds the operator guides to the register for this term as well as
	// the homes and the sources. It is set for a term the guides already use
	// under a row, so the row is what keeps them legible: `re-arm` is the verb
	// `yoyo triage rearm`, and the guides that tell an operator when to type it
	// have to say the word the command is called. A term without it is not read
	// in the guides at all, since nobody has swept them for it.
	Guides bool
	// PlainWords is the ordinary wording the audit recorded, carried here so a
	// failure says what to write instead rather than only what is wrong.
	PlainWords string
}

// Vocabulary is the sweep's inventory: the thirteen words it called decoration,
// `cadence` which it named in passing as one more, and the six mechanism names
// it measured for the architect. Every one is recorded in
// `docs/diagnoses/yoyodyne-ifd-206-coined-terms-sweep.md` with the evidence
// behind it. Three more joined after it, under yoyodyne-ifd.437.17: the
// harness's own names for a run ended by something outside the work, for the
// harness ending a run whose session went quiet, and for resuming such a run in
// its session, which reached the operator in one sentence with nothing saying
// what any of them meant. They are held in the guides as well, because the
// guides are where they had been written.
var Vocabulary = []Coinage{
	{Term: "brake", Match: "brake", PlainWords: "the automatic stop after a set number of blocked runs in a row"},
	{Term: "cadence", Match: "cadence", PlainWords: "how often it repeats"},
	{Term: "docket", Match: "docket", PlainWords: "the list of stopped runs waiting on the development manager"},
	// Every form of the word is the one coinage: `environmental refusal`,
	// `environmental cause`, and `refused environmentally` reached readers as
	// often as `environmental stop` did, and a match on the phrase alone left
	// them all standing.
	{Term: "environmental stop", Match: "environmental", Guides: true, PlainWords: "that the run was ended by something outside the work, naming what it was"},
	{Term: "handback", Match: "handback", PlainWords: "handing the work back to the developer that made it"},
	{Term: "heartbeat", Match: "heartbeat", PlainWords: "how often to repeat"},
	{Term: "idle bound", Match: "idle bound", Whole: true, Guides: true, PlainWords: "that the AI session produced no output for too long, so the harness ended the run"},
	{Term: "in force", Match: "in force", Whole: true, PlainWords: "active, or still applies"},
	{Term: "minute zero", Match: "minute zero", PlainWords: "before development begins"},
	{Term: "pane of glass", Match: "pane of glass", PlainWords: "one window"},
	{Term: "posture", Match: "posture", PlainWords: "tool access, meaning the tools a role may use"},
	{Term: "re-arm", Match: "re-arm", Guides: true, PlainWords: "repeat the merge request"},
	{Term: "seam", Match: "seam", Whole: true, PlainWords: "name the boundary instead — what attaches to what"},
	{Term: "sidecar", Match: "sidecar", PlainWords: "a separate directory outside the repository"},
	{Term: "sink", Match: "sink", PlainWords: "the process that posts to Slack"},
	{Term: "soak", Match: "soak", PlainWords: "a trial run kept alongside the old path for comparison"},
	{Term: "starving", Match: "starv", PlainWords: "stopping"},
	{Term: "steer", Match: "steer", PlainWords: "direct, or change what is being worked on"},
	{Term: "stall continuation", Match: "stall continuation", Guides: true, PlainWords: "that the development manager resumed the run in the same AI session"},
	{Term: "supersession pile", Match: "supersession pile", PlainWords: "the list of superseded pull requests"},
	{Term: "tranche", Match: "tranche", PlainWords: "stage, or part 1 of 4"},
	{Term: "wedged", Match: "wedge", PlainWords: "stuck, or say the condition outright"},
	{Term: "whose-move", Match: "whose-move", PlainWords: "waiting on you"},
}

// Entry is one row of the register: a term, what it means in ordinary words, and
// where a reader meets it.
type Entry struct {
	Term       string `json:"term"`
	PlainWords string `json:"plainWords"`
	Used       string `json:"used"`
	// Line is where the row is written, so a malformed one can be opened rather
	// than searched for.
	Line int `json:"line"`
}

// Replacement is one row of the replaced table: a term that was retired, the
// wording to write instead, and the governed documents that still carry it
// while their owner amends them.
//
// StillIn is what lets a term be retired before every document has caught up.
// A governed document is its owner's alone to reword, and a term the operator
// has asked to lose cannot wait on that to be refused everywhere else — the
// help text, the notifier, the guides. So the row names the documents that are
// excused, the check refuses the term anywhere the row does not name, and it
// refuses the row itself once a named document no longer carries the term, so
// the excuse cannot outlive its reason.
type Replacement struct {
	Term       string   `json:"term"`
	PlainWords string   `json:"plainWords"`
	StillIn    []string `json:"stillIn,omitempty"`
	Line       int      `json:"line"`
}

// Problem is one thing wrong: a coinage no entry defines, or an entry that does
// not define one.
type Problem struct {
	// Path is the repository-relative document and Line the physical line, because
	// what somebody has to open is the place the word is written.
	Path string `json:"path"`
	Line int    `json:"line"`
	// Term is the word this is about, so what is reported can be searched for.
	Term   string `json:"term"`
	Reason string `json:"reason"`
}

func (p Problem) String() string {
	return fmt.Sprintf("%s:%d: %s", p.Path, p.Line, p.Reason)
}

// Register reads the entries the register states, in the order it states them.
//
// A register that cannot be read is an error rather than an empty set: every
// term is undefined against an empty register, so a missing file would otherwise
// be reported as the documents being full of undefined coinage, which sends
// whoever reads it to the wrong place entirely.
func Register(root string) ([]Entry, error) {
	content, err := read(filepath.Join(root, filepath.FromSlash(RegisterPath)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", RegisterPath, err)
	}
	return entriesIn(content), nil
}

func entriesIn(content string) []Entry {
	var entries []Entry
	for _, row := range tableUnder(content, RegisterHeading) {
		entries = append(entries, Entry{
			Term:       strings.ToLower(strings.Trim(row.cells[0], "`")),
			PlainWords: cell(row.cells, 1),
			Used:       cell(row.cells, 2),
			Line:       row.line,
		})
	}
	return entries
}

// Replaced reads the terms the register lists as replaced rather than
// registered, in the order it lists them. The documents a row says still carry
// its term are read from the third column as the code spans written there, so
// a row that names none excuses nothing.
func Replaced(root string) ([]Replacement, error) {
	content, err := read(filepath.Join(root, filepath.FromSlash(RegisterPath)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", RegisterPath, err)
	}
	return replacementsIn(content), nil
}

func replacementsIn(content string) []Replacement {
	var replacements []Replacement
	for _, row := range tableUnder(content, ReplacedHeading) {
		replacement := Replacement{
			Term:       strings.ToLower(strings.Trim(row.cells[0], "`")),
			PlainWords: cell(row.cells, 1),
			Line:       row.line,
		}
		for _, span := range codeSpan.FindAllStringSubmatch(cell(row.cells, 2), -1) {
			replacement.StillIn = append(replacement.StillIn, span[1])
		}
		replacements = append(replacements, replacement)
	}
	return replacements
}

// codeSpan is one `path` in a table cell.
var codeSpan = regexp.MustCompile("`([^`]+)`")

// row is one table row under a heading: its cells and the physical line it is
// written on.
type row struct {
	cells []string
	line  int
}

// tableUnder is the rows of the table under one heading, and only that one: the
// document has more than one table, and a parser that took every table would
// register exactly the terms the sweep took out. The header names the columns
// and the separator draws the rule under them; neither is a row, and neither
// writes its first cell as code.
func tableUnder(content, heading string) []row {
	var rows []row
	within := false
	for index, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			within = line == heading
			continue
		}
		if !within || !strings.HasPrefix(line, "|") {
			continue
		}
		cells := tableCells(line)
		if len(cells) == 0 || !strings.HasPrefix(cells[0], "`") {
			continue
		}
		rows = append(rows, row{cells: cells, line: index + 1})
	}
	return rows
}

// cell is one cell of a row, or nothing where the row is short of it.
func cell(cells []string, index int) string {
	if index < len(cells) {
		return cells[index]
	}
	return ""
}

// Documents is every Markdown file under the homes, repository-relative and in
// sorted order. It is exported for the reason internal/doclink exports its own:
// a walk that found nothing and a set of documents with nothing wrong in them
// are the same result otherwise.
func Documents(root string) ([]string, error) {
	var documents []string
	for _, home := range Homes {
		directory := filepath.Join(root, filepath.FromSlash(home))
		err := filepath.WalkDir(directory, func(current string, entry fs.DirEntry, err error) error {
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
			documents = append(documents, filepath.ToSlash(relative))
			return nil
		})
		if err != nil {
			if os.IsNotExist(err) {
				// A home a project has not created is intent not yet written, not a
				// defect. The same judgement the goals check makes about an absent
				// artifact home.
				continue
			}
			return nil, fmt.Errorf("walk %s for documents: %w", home, err)
		}
	}
	sort.Strings(documents)
	return documents, nil
}

// GuideFiles is every guide, repository-relative and in sorted order: the
// Markdown files under Guides less the homes, the register, the vocabulary
// inventory, and the record directories. Exported for the reason Documents is.
func GuideFiles(root string) ([]string, error) {
	skipped := make(map[string]bool)
	for _, directory := range append(append([]string{}, Homes...), NotGuides...) {
		skipped[directory] = true
	}
	var guides []string
	for _, guide := range Guides {
		start := filepath.Join(root, filepath.FromSlash(guide))
		err := filepath.WalkDir(start, func(current string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, current)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if entry.IsDir() {
				if skipped[relative] {
					return filepath.SkipDir
				}
				return nil
			}
			if relative == RegisterPath || relative == InventoryPath || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				return nil
			}
			guides = append(guides, relative)
			return nil
		})
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("walk %s for guides: %w", guide, err)
		}
	}
	sort.Strings(guides)
	return guides, nil
}

// SourceFiles is every Go file under the sources that is not a test, and every
// file under the assets, repository-relative and in sorted order. Exported for
// the reason Documents is: a walk that found nothing and a set of files with
// nothing wrong in them are the same result otherwise.
func SourceFiles(root string) ([]string, error) {
	var files []string
	walk := func(home string, keep func(name string) bool) error {
		directory := filepath.Join(root, filepath.FromSlash(home))
		err := filepath.WalkDir(directory, func(current string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !keep(entry.Name()) {
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
			return fmt.Errorf("walk %s for sources: %w", home, err)
		}
		return nil
	}
	for _, source := range Sources {
		if err := walk(source, func(name string) bool {
			return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
		}); err != nil {
			return nil, err
		}
	}
	for _, asset := range Assets {
		if err := walk(asset, func(string) bool { return true }); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

// Check reports every coinage in the governed homes and the source strings that
// the register does not define, every register entry that defines nothing, and
// every replaced term still excused in a document that no longer carries it.
// Problems come out in the order the register is written, then the order the
// documents are walked, then the guides, then the sources, so two runs over one
// checkout report the same thing in the same order.
func Check(root string) ([]Problem, error) {
	entries, err := Register(root)
	if err != nil {
		return nil, err
	}
	problems, registered := checkRegister(entries)
	replacements, err := Replaced(root)
	if err != nil {
		return nil, err
	}
	excused := excusedDocuments(replacements)
	documents, err := Documents(root)
	if err != nil {
		return nil, err
	}
	patterns := make([]*regexp.Regexp, len(Vocabulary))
	for index, coinage := range Vocabulary {
		patterns[index] = pattern(coinage)
	}
	// Which excused documents were seen to carry their term, so a row excusing a
	// document that has since been amended is reported rather than kept.
	carried := make(map[string]map[string]bool)
	for _, document := range documents {
		content, err := read(filepath.Join(root, filepath.FromSlash(document)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", document, err)
		}
		found := scan{path: document, patterns: patterns, registered: registered, excused: excused[document], carried: carried}
		problems = append(problems, found.problems(passages(strings.Split(content, "\n")))...)
	}
	guides, err := GuideFiles(root)
	if err != nil {
		return nil, err
	}
	for _, guide := range guides {
		content, err := read(filepath.Join(root, filepath.FromSlash(guide)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", guide, err)
		}
		found := scan{path: guide, patterns: patterns, registered: registered, guide: true}
		problems = append(problems, found.problems(passages(strings.Split(content, "\n")))...)
	}
	files, err := SourceFiles(root)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		found := scan{path: file, patterns: patterns, registered: registered}
		stretches, err := stringsIn(root, file)
		if err != nil {
			return nil, err
		}
		problems = append(problems, found.problems(stretches)...)
	}
	for _, replacement := range replacements {
		for _, document := range replacement.StillIn {
			if !carried[replacement.Term][document] {
				problems = append(problems, Problem{Path: RegisterPath, Line: replacement.Line, Term: replacement.Term,
					Reason: fmt.Sprintf("%q is recorded as still written in %s, and it is not; take the document off the row so the term is refused there too",
						replacement.Term, document)})
			}
		}
	}
	return problems, nil
}

// excusedDocuments is, for each document, the replaced terms whose rows excuse
// it, keyed the way the vocabulary keys them.
func excusedDocuments(replacements []Replacement) map[string]map[string]bool {
	excused := make(map[string]map[string]bool)
	for _, replacement := range replacements {
		for _, document := range replacement.StillIn {
			if excused[document] == nil {
				excused[document] = make(map[string]bool)
			}
			excused[document][replacement.Term] = true
		}
	}
	return excused
}

// stringsIn is the string literals of one source file as passages, each at the
// physical line it starts on, or the whole of an asset file read as one
// document would be.
//
// A raw literal is read line by line, because the help text and the personas
// are written that way and a term wraps across their lines as it does in a
// document. An interpreted literal is one physical line whatever escapes it
// carries, so its newlines are read as spaces and every match is reported on
// the line the literal starts. Import paths and the like are literals too and
// are read like any other. A struct tag is not read: it names the key a field
// is written under in a configuration file or a record, and a key keeps its
// name when the words around it change — `postures` is a provider
// configuration key, not a sentence.
func stringsIn(root, file string) ([]passage, error) {
	full := filepath.Join(root, filepath.FromSlash(file))
	if !strings.HasSuffix(file, ".go") {
		content, err := read(full)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		return passagesFrom(strings.Split(content, "\n"), 1), nil
	}
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, full, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	tags := make(map[*ast.BasicLit]bool)
	ast.Inspect(parsed, func(node ast.Node) bool {
		if field, ok := node.(*ast.Field); ok && field.Tag != nil {
			tags[field.Tag] = true
		}
		return true
	})
	var stretches []passage
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING || tags[literal] {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			// The parser accepted it, so this cannot happen; a literal it did not
			// accept is a parse error reported above.
			return true
		}
		first := fileSet.Position(literal.Pos()).Line
		if literal.Value[0] != '`' {
			value = strings.ReplaceAll(value, "\n", " ")
		}
		stretches = append(stretches, passagesFrom(strings.Split(value, "\n"), first)...)
		return true
	})
	return stretches, nil
}

// checkRegister holds the register to its own shape and reports which terms it
// permits. An entry missing either half of what an entry is for — the plain
// words, or where the word is met — is reported rather than silently permitting
// the term: an entry that defines nothing is the coinage with the appearance of
// having been registered, which is worse than no entry at all.
func checkRegister(entries []Entry) ([]Problem, map[string]bool) {
	var problems []Problem
	registered := make(map[string]bool, len(entries))
	for _, entry := range entries {
		switch {
		case entry.Term == "":
			problems = append(problems, Problem{Path: RegisterPath, Line: entry.Line, Reason: "a register entry names no term"})
			continue
		case registered[entry.Term]:
			problems = append(problems, Problem{Path: RegisterPath, Line: entry.Line, Term: entry.Term,
				Reason: fmt.Sprintf("%q is registered twice; the register holds one entry per term", entry.Term)})
			continue
		case entry.PlainWords == "":
			problems = append(problems, Problem{Path: RegisterPath, Line: entry.Line, Term: entry.Term,
				Reason: fmt.Sprintf("%q is registered with no plain-word definition, which is the whole of what an entry is for", entry.Term)})
		case entry.Used == "":
			problems = append(problems, Problem{Path: RegisterPath, Line: entry.Line, Term: entry.Term,
				Reason: fmt.Sprintf("%q is registered without saying where it is used, so a reader cannot tell whether it still is", entry.Term)})
		}
		registered[entry.Term] = true
	}
	return problems, registered
}

// fencePattern matches the line that opens or closes a fenced code block. A term
// inside one is a command, an identifier, or a script the sweep's own document
// quotes, rather than prose somebody reads for its meaning.
var fencePattern = regexp.MustCompile("^(?:```|~~~)")

// termParts splits a match into the parts a writer can space differently.
var termParts = regexp.MustCompile(`[-\s]+`)

// pattern is what one term is looked for as: its parts, at a word start, spaced
// however whoever wrote the sentence spaced them.
//
// A term written in more than one part is one word to a reader however it was
// typed. `minute zero`, `minute-zero`, and a `minute` that a line wrap left at
// the end of one line with its `zero` at the start of the next are the same
// coinage, and a check that found only the spelling the register happens to use
// is a floor with a hole in it — `minute-zero` is written in an active
// invariant and escaped the sweep and the first version of this check alike.
// Whichever spelling is found, the failure names the term as the register
// spells it, because what the reader needs is the wording to write rather than
// the respelling they used.
//
// The tolerance is only where the term is already divided. A term the register
// writes as one word is looked for as one word: `hand back` is ordinary English
// in sentences that have nothing to do with `handback`, and reporting those is
// the mistake Whole already exists to avoid — a term this check misses a
// reviewer still catches, and a term it reports wrongly is a check people learn
// to argue with.
func pattern(coinage Coinage) *regexp.Regexp {
	parts := termParts.Split(coinage.Match, -1)
	for index, part := range parts {
		parts[index] = regexp.QuoteMeta(part)
	}
	// The separator is optional rather than required, so the parts closed up into
	// one word — `rearm` for `re-arm` — is found too.
	expression := `(?i)\b` + strings.Join(parts, `[-\s]*`)
	if coinage.Whole {
		expression += `\b`
	}
	return regexp.MustCompile(expression)
}

// passage is one stretch of a document that is read for coinage: consecutive
// body lines, trimmed and joined, with the line number the stretch starts at.
//
// The document is read in stretches rather than a line at a time because a term
// of more than one word is as easily broken by a line wrap as by a hyphen, and
// neither makes it a different word. What ends a stretch is what a term cannot
// wrap across: a blank line, a fence, the fenced lines themselves, and the
// frontmatter. So `minute` at the end of one line and `zero` at the start of the
// next is found, and the last word of one paragraph followed by the first word
// of the next is not.
type passage struct {
	first int
	text  string
}

// lineOf is the physical line a match at this offset falls on.
func (p passage) lineOf(offset int) int {
	return p.first + strings.Count(p.text[:offset], "\n")
}

// passages is the stretches of one document's body, in the order they are
// written.
//
// Two parts of a document are deliberately not read. The frontmatter carries the
// identity and the revision history, and a revision's recorded reason is what
// somebody decided in their own words on a date — rewriting one to change a word
// falsifies a record rather than clarifying a sentence. Fenced blocks are code.
func passages(lines []string) []passage {
	skipped := skipFrontmatter(lines)
	return passagesFrom(lines[skipped:], skipped+1)
}

// passagesFrom is the stretches of some lines, the first of which is the
// physical line numbered first.
func passagesFrom(lines []string, first int) []passage {
	var stretches []passage
	var current []string
	start := 0
	fenced := false
	end := func() {
		if len(current) > 0 {
			stretches = append(stretches, passage{first: start, text: strings.Join(current, "\n")})
			current = nil
		}
	}
	for index, raw := range lines {
		line := strings.TrimSpace(raw)
		if fencePattern.MatchString(line) {
			fenced = !fenced
			end()
			continue
		}
		if fenced || line == "" {
			end()
			continue
		}
		if len(current) == 0 {
			start = first + index
		}
		current = append(current, line)
	}
	end()
	return stretches
}

// scan is one file being read for coinage: what is looked for, what the
// register permits, and — for a governed document — which replaced terms a
// row excuses it for while its owner amends it.
type scan struct {
	path       string
	patterns   []*regexp.Regexp
	registered map[string]bool
	excused    map[string]bool
	// carried records, for an excused term, that this file was seen to carry it,
	// so the row excusing a document that no longer does is reported.
	carried map[string]map[string]bool
	// guide is set for an operator guide, which is read only for the terms
	// marked Guides.
	guide bool
}

// problems reports the unregistered coinage in some passages of one file.
func (s scan) problems(stretches []passage) []Problem {
	var problems []Problem
	for _, stretch := range stretches {
		// Collected by line first and reported afterwards, so problems come out in
		// the order the file is written whichever term was searched for first,
		// and a term written twice on one line is one problem rather than two.
		found := make(map[int]map[int]bool)
		for position, coinage := range Vocabulary {
			if s.registered[coinage.Term] || (s.guide && !coinage.Guides) {
				continue
			}
			matches := s.patterns[position].FindAllStringIndex(stretch.text, -1)
			if s.excused[coinage.Term] {
				if len(matches) > 0 {
					if s.carried[coinage.Term] == nil {
						s.carried[coinage.Term] = make(map[string]bool)
					}
					s.carried[coinage.Term][s.path] = true
				}
				continue
			}
			for _, match := range matches {
				line := stretch.lineOf(match[0])
				if found[line] == nil {
					found[line] = make(map[int]bool)
				}
				found[line][position] = true
			}
		}
		for line := stretch.first; line <= stretch.first+strings.Count(stretch.text, "\n"); line++ {
			for position, coinage := range Vocabulary {
				if !found[line][position] {
					continue
				}
				problems = append(problems, Problem{
					Path: s.path, Line: line, Term: coinage.Term,
					Reason: fmt.Sprintf("%q is a coined term with no entry in %s; write %s, or register the term with a plain-word definition",
						coinage.Term, RegisterPath, coinage.PlainWords),
				})
			}
		}
	}
	return problems
}

// skipFrontmatter is the first line of the document proper: the line after the
// closing `---` where a document opens with frontmatter, and the first line
// otherwise. A document that opens a frontmatter block and never closes it is
// read whole, because the alternative is a check that reads nothing at all in a
// document whose first line is a horizontal rule.
func skipFrontmatter(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return index + 1
		}
	}
	return 0
}

// tableCells is one Markdown table row's cells, trimmed, without the empty
// strings the leading and trailing pipes produce.
func tableCells(line string) []string {
	cells := strings.Split(strings.Trim(line, "|"), "|")
	for index, cell := range cells {
		cells[index] = strings.TrimSpace(cell)
	}
	return cells
}

// read is one document, refused rather than loaded if it is larger than a
// document has any business being.
func read(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > MaxFileBytes {
		return "", fmt.Errorf("%d bytes is larger than the %d this check reads", info.Size(), MaxFileBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
