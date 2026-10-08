// Command vocabulary writes the inventory of the harness's own vocabulary:
// every term of art in the inventory, with where a person or a role reads it, how
// often, what it means, and the decision made on it.
//
//	go run ./scripts/vocabulary > docs/vocabulary-inventory.md
//	go run ./scripts/vocabulary -candidates
//
// The terms, their meanings, and the decisions are data in internal/terms/inventory,
// which the terms check reads too. The counts
// are measured every time it runs, so the document is re-run rather than
// edited: a term whose decision lands is changed there, and the next run says
// so. -candidates lists the hyphenated compounds no term covers, which is where
// a later reading for new terms starts; nothing mechanical tells a coinage from
// an ordinary compound, so it lists them for a person to read.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/terms"
	"github.com/mason-bryant/yoyodyne/internal/terms/inventory"
)

// OutputPath is where the inventory is kept, repository-relative. The guides
// are read for the counts, and this one is left out of them so the inventory
// does not count itself.
const OutputPath = "docs/vocabulary-inventory.md"

// Surface is one of the places a person or a role reads the harness's words.
type Surface int

const (
	// Printed is the string literals in the Go source, and the dashboard's own
	// files: what commands print, what surfaces show, and the role contracts
	// and pass prompts, which are Go strings as well.
	Printed Surface = iota
	// Personas is the shipped personas and bundle.
	Personas
	// Guides is the README and the guides under docs/.
	Guides
	// Governed is the governed documents: product intent, designs, decisions.
	Governed
	surfaces
)

func (s Surface) String() string {
	return [...]string{"Printed strings", "Personas", "Guides", "Governed documents"}[s]
}

// PersonaHome is where the shipped personas and bundle live.
const PersonaHome = "internal/config/builtin"

// skippedDirectories are never read for printed strings: repository metadata,
// test fixtures, and this command, whose own data names every term.
var skippedDirectories = map[string]bool{
	".git": true, ".beads": true, "testdata": true, "node_modules": true, "vendor": true,
	"scripts/vocabulary": true,
}

// Text is one body of words and where it is read.
type Text struct {
	Surface Surface
	// Where is the package directory for a Go string, and the file otherwise.
	Where string
	Body  string
}

// literalSeparator joins string literals from one package so a match cannot
// run from the end of one literal into the start of the next: a spacing
// pattern matches whitespace and hyphens, and never this.
const literalSeparator = "\x00"

// Collect reads every text the inventory counts, under root.
func Collect(root string) ([]Text, error) {
	var texts []Text
	printed, err := printedStrings(root)
	if err != nil {
		return nil, err
	}
	texts = append(texts, printed...)

	personas, err := filesUnder(root, PersonaHome, func(name string) bool {
		ext := strings.ToLower(filepath.Ext(name))
		return ext == ".md" || ext == ".yaml" || ext == ".yml"
	})
	if err != nil {
		return nil, err
	}
	for _, file := range personas {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		texts = append(texts, Text{Surface: Personas, Where: file, Body: string(body)})
	}

	guides, err := terms.GuideFiles(root)
	if err != nil {
		return nil, err
	}
	for _, file := range guides {
		if file == OutputPath {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		texts = append(texts, Text{Surface: Guides, Where: file, Body: string(body)})
	}

	documents, err := terms.Documents(root)
	if err != nil {
		return nil, err
	}
	for _, file := range documents {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		texts = append(texts, Text{Surface: Governed, Where: file, Body: withoutFrontmatter(string(body))})
	}
	// The example of what not to write is quoted on purpose wherever it
	// appears, and its words are not debt.
	for index := range texts {
		texts[index].Body = terms.WithoutQuotedExample(texts[index].Body)
	}
	return texts, nil
}

// printedStrings is every string literal holding a space in the Go source
// outside tests, one text per package, and the dashboard's assets read whole. A
// literal with no space is a key, an identifier, or a format fragment rather
// than words anybody reads, and counting it would count the field names the
// inventory says keep their names.
func printedStrings(root string) ([]Text, error) {
	byPackage := make(map[string][]string)
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if skippedDirectories[entry.Name()] || skippedDirectories[relative] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		literals, err := stringLiterals(current)
		if err != nil {
			return fmt.Errorf("read strings from %s: %w", relative, err)
		}
		directory := filepath.ToSlash(filepath.Dir(relative))
		byPackage[directory] = append(byPackage[directory], literals...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var texts []Text
	for directory, literals := range byPackage {
		if len(literals) > 0 {
			texts = append(texts, Text{Surface: Printed, Where: directory, Body: strings.Join(literals, literalSeparator)})
		}
	}

	assets, err := filesUnder(root, "internal/dashboard/assets", func(string) bool { return true })
	if err != nil {
		return nil, err
	}
	for _, file := range assets {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		texts = append(texts, Text{Surface: Printed, Where: file, Body: string(body)})
	}
	sort.Slice(texts, func(i, j int) bool { return texts[i].Where < texts[j].Where })
	return texts, nil
}

func stringLiterals(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var literals []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.ContainsAny(value, " \n") {
			literals = append(literals, value)
		}
		return true
	})
	return literals, nil
}

// filesUnder is every file under home that keep accepts, repository-relative
// and sorted. A home that does not exist holds nothing.
func filesUnder(root, home string, keep func(name string) bool) ([]string, error) {
	var files []string
	err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(home)), func(current string, entry fs.DirEntry, err error) error {
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
	if os.IsNotExist(err) {
		return nil, nil
	}
	sort.Strings(files)
	return files, err
}

// withoutFrontmatter drops a governed document's frontmatter, which is identity
// and revision history: a revision's recorded reason is what somebody decided on
// a date, and no decision here rewords it.
func withoutFrontmatter(body string) string {
	if !strings.HasPrefix(body, "---\n") {
		return body
	}
	end := strings.Index(body[4:], "\n---")
	if end < 0 {
		return body
	}
	rest := body[4+end+4:]
	return strings.TrimPrefix(rest, "\n")
}

// Place is one location a term occurs in and how often.
type Place struct {
	Surface Surface
	Where   string
	Count   int
}

// Measurement is one term's occurrences.
type Measurement struct {
	Term     inventory.Term
	Totals   [surfaces]int
	Places   []Place
	Register string
}

// Total is every occurrence across the surfaces.
func (m Measurement) Total() int {
	total := 0
	for _, count := range m.Totals {
		total += count
	}
	return total
}

// Measure counts every term in inventory across texts.
func Measure(known []inventory.Term, texts []Text) []Measurement {
	var measurements []Measurement
	for _, term := range known {
		measurement := Measurement{Term: term}
		for _, text := range texts {
			count := inventory.Count(term, text.Body)
			if count == 0 {
				continue
			}
			measurement.Totals[text.Surface] += count
			measurement.Places = append(measurement.Places, Place{Surface: text.Surface, Where: text.Where, Count: count})
		}
		sort.SliceStable(measurement.Places, func(i, j int) bool {
			return measurement.Places[i].Count > measurement.Places[j].Count
		})
		measurements = append(measurements, measurement)
	}
	return measurements
}

// registerStates says, for each term, whether docs/terms.md registers it or
// lists it as replaced today.
func registerStates(root string) (map[string]string, error) {
	states := make(map[string]string)
	entries, err := terms.Register(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		states[strings.ToLower(entry.Term)] = "registered"
	}
	replaced, err := terms.Replaced(root)
	if err != nil {
		return nil, err
	}
	for _, replacement := range replaced {
		states[strings.ToLower(replacement.Term)] = "replaced"
	}
	return states, nil
}

func main() {
	root := flag.String("root", ".", "the repository to measure")
	candidates := flag.Bool("candidates", false, "list hyphenated compounds no term covers, instead of writing the inventory")
	flag.Parse()

	texts, err := Collect(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vocabulary:", err)
		os.Exit(1)
	}
	if *candidates {
		WriteCandidates(os.Stdout, inventory.Inventory, texts, 5)
		return
	}
	states, err := registerStates(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vocabulary:", err)
		os.Exit(1)
	}
	measurements := Measure(inventory.Inventory, texts)
	for i := range measurements {
		measurements[i].Register = states[strings.ToLower(measurements[i].Term.Term)]
	}
	Render(os.Stdout, measurements, inventory.StopWords, inventory.Unread, time.Now())
}

var compound = regexp.MustCompile(`(?i)\b[a-z]+(?:-[a-z]+)+\b`)

// WriteCandidates lists the hyphenated compounds in the Go strings and the
// personas that no term in inventory covers and that occur at least minimum
// times, most frequent first.
func WriteCandidates(w io.Writer, known []inventory.Term, texts []Text, minimum int) {
	counts := make(map[string]int)
	for _, text := range texts {
		// The dashboard's style and script are full of compounds that are
		// property names and classes, so only the Go strings are read here.
		if text.Surface == Printed && filepath.Ext(text.Where) != "" || text.Surface == Guides || text.Surface == Governed {
			continue
		}
		for _, word := range compound.FindAllString(text.Body, -1) {
			counts[strings.ToLower(word)]++
		}
	}
	var words []string
	for word, count := range counts {
		if count < minimum || covered(known, word) {
			continue
		}
		words = append(words, word)
	}
	sort.Slice(words, func(i, j int) bool {
		if counts[words[i]] != counts[words[j]] {
			return counts[words[i]] > counts[words[j]]
		}
		return words[i] < words[j]
	})
	for _, word := range words {
		fmt.Fprintf(w, "%6d  %s\n", counts[word], word)
	}
}

func covered(known []inventory.Term, word string) bool {
	for _, term := range known {
		if inventory.Pattern(term).MatchString(word) {
			return true
		}
	}
	return false
}

// Render writes the inventory document.
func Render(w io.Writer, measurements []Measurement, stopWords []inventory.StopWord, unread []string, at time.Time) {
	fmt.Fprintf(w, header, at.Format("2006-01-02"))

	fmt.Fprintln(w, "## Summary")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Term | Decided | In the register today | Printed strings | Personas | Guides | Governed documents |")
	fmt.Fprintln(w, "|---|---|---|---:|---:|---:|---:|")
	for _, m := range measurements {
		register := m.Register
		if register == "" {
			register = "—"
		}
		fmt.Fprintf(w, "| [`%s`](#%s) | %s | %s | %d | %d | %d | %d |\n",
			m.Term.Term, anchor(m.Term.Term), m.Term.Decision, register,
			m.Totals[Printed], m.Totals[Personas], m.Totals[Guides], m.Totals[Governed])
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "## The terms")
	for _, m := range measurements {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "### %s\n\n", m.Term.Term)
		fmt.Fprintf(w, "- **Means:** %s\n", m.Term.Meaning)
		switch m.Term.Decision {
		case inventory.Replace:
			fmt.Fprintf(w, "- **Decided:** replace it. Write instead: %s.\n", m.Term.Words)
		case inventory.Register:
			fmt.Fprintf(w, "- **Decided:** register it, with the meaning above: %s.\n", m.Term.Words)
		case inventory.Keep:
			fmt.Fprintf(w, "- **Decided:** no change. It is %s.\n", m.Term.Words)
		}
		fmt.Fprintf(w, "- **Where:** %s\n", places(m))
		if m.Term.Note != "" {
			fmt.Fprintf(w, "- **Note:** %s\n", m.Term.Note)
		}
	}
	fmt.Fprintln(w)

	fmt.Fprint(w, stopWordsIntro)
	fmt.Fprintln(w, "| Word | Means | Decided |")
	fmt.Fprintln(w, "|---|---|---|")
	for _, word := range stopWords {
		proposed := "print instead: " + word.Words
		if word.Decision == inventory.Keep {
			proposed = word.Words
		}
		fmt.Fprintf(w, "| `%s` | %s | %s |\n", word.Word, cellText(word.Meaning), cellText(proposed))
	}
	fmt.Fprintln(w)
	fmt.Fprint(w, footer)
	fmt.Fprintln(w)
	fmt.Fprint(w, unreadIntro)
	for _, word := range unread {
		fmt.Fprintf(w, "- `%s`\n", word)
	}
}

// maxPlaces is how many places one term's entry names before summing the rest.
const maxPlaces = 6

func places(m Measurement) string {
	if len(m.Places) == 0 {
		return "nowhere counted."
	}
	var named []string
	for i, place := range m.Places {
		if i == maxPlaces {
			break
		}
		named = append(named, fmt.Sprintf("`%s` %d", place.Where, place.Count))
	}
	noun := "places"
	if len(m.Places) == 1 {
		noun = "place"
	}
	text := fmt.Sprintf("%d in all, in %d %s: %s", m.Total(), len(m.Places), noun, strings.Join(named, ", "))
	if rest := len(m.Places) - maxPlaces; rest > 0 {
		text += fmt.Sprintf(", and %d more", rest)
	}
	return text + "."
}

// anchor is the heading anchor a Markdown renderer gives a term's heading.
func anchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

func cellText(text string) string {
	return strings.ReplaceAll(text, "|", `\|`)
}

const header = `# The harness's own vocabulary

Every term of art the harness uses where a person or a role reads it: where it
appears, how often, what it means in one plain sentence, and the decision made
on it — replace it with ordinary words, or register it in
[the register](terms.md) as a term that names something real. The operator
asked for it on 2026-09-27, after a program manager's report reached him saying
"environmental stop" and "idle bound" and nothing told him what either meant.
It is the inventory of the harness's coined vocabulary (yoyodyne-ifd.437.18).
The Lead Product Manager made the decisions on every term in it on 2026-10-08.

**This document is generated. Edit the script, not this file.** Run
` + "`go run ./scripts/vocabulary > docs/vocabulary-inventory.md`" + ` from the
repository root. The terms, what they mean, and what was decided for each are
in ` + "`internal/terms/inventory`" + `; the counts are measured each time it
runs. These were measured on %s.

## The decisions

The decisions below were made on 2026-10-08. The inventory began as a list of
proposals; the Lead Product Manager recorded on the inventory's work item
(yoyodyne-ifd.437.18) that every one of them stands as decided, unchanged, and
the items admitted beside it carry the decisions out:

- replacing the coined vocabulary in printed strings, role contracts, shipped
  personas, and pass prompts (yoyodyne-ifd.437.19);
- replacing it in the shipped guides (yoyodyne-ifd.437.20);
- the architect replacing it in the governed documents (yoyodyne-ifd.437.21);
- the terms check, which refuses a compound word that is neither registered,
  replaced, a term below, ordinary English, nor on the list at the end of this
  document (yoyodyne-ifd.437.22).

Three terms are already being replaced on their own item — 'environmental
stop', 'idle bound', and 'stall continuation' (yoyodyne-ifd.437.17) — and
what roles write for a person is being held to the register on its own item
too (yoyodyne-ifd.437.16). When a decision changes, change the term's entry in
` + "`internal/terms/inventory`" + ` and run the script again, so this document
says what stands.

The "In the register today" column says how far each decision has been carried
out: a term decided for registration reads "registered" once its row is
written, and a term decided for replacement reads "replaced" once
[the register](terms.md) lists it as replaced, which is when the terms check
begins refusing it.

A term decided for replacement keeps its name wherever it is an identifier: a
type, a field in a stored record, a configuration key, a package. What changes
is the words a person or a role reads.

## What was counted

- **Printed strings:** every string literal holding a space in the Go source,
  outside tests and test data, counted by package. That is what the commands
  print and their help, ` + "`yoyo status`" + `, the development manager's list of
  stopped runs, Slack messages, and the read model the dashboard shows — and the
  role contracts and pass prompts too, which are Go strings. The dashboard's own
  files under ` + "`internal/dashboard/assets`" + ` are read whole.
- **Personas:** the shipped personas and bundle under
  ` + "`internal/config/builtin`" + `.
- **Guides:** the README and every Markdown file under ` + "`docs/`" + ` that is
  not a governed document, the register, a record under ` + "`docs/diagnoses`" + `,
  ` + "`docs/experiments`" + ` or ` + "`docs/releases`" + `, or this document.
- **Governed documents:** every Markdown file under ` + "`docs/product`" + `,
  ` + "`docs/designs`" + `, and ` + "`docs/decisions`" + `, less its frontmatter.

The example of what not to write that the personas and the role contracts
quote on purpose — *stopped by the harness's idle bound when the provider's
stream went silent, settled as an environmental stop* — is not counted: it is
meant to stay where it is.

A count is of occurrences, matched at the start of a word and ignoring case, so
a stem counts its inflections: ` + "`docket`" + ` counts ` + "`docketed`" + `. A
term written in parts is matched however its parts are spaced. Where a term's
ordinary use cannot be told from its use as a term, the entry says so, and its
count is an upper bound.

Not counted: the tracker's own items, which are the Lead Product Manager's to
reword; the records under ` + "`docs/`" + `, which say what was true on a date in
the words used then; code comments; and a string literal with no space in it,
which is a key or an identifier rather than words anybody reads.

`

const stopWordsIntro = `## The stop-cause words

The run record naming what stopped it (yoyodyne-ifd.409) prints one of ten words
in front of every stopped run's reason, as in ` + "`provider: …`" + `. That item
asked for whoever owns their wording to confirm them before anything depends on
them, and the Lead Product Manager put the question here. They are listed and
not counted: that item's change is not on the main branch yet, and most of the
words are ordinary words a count could not tell from their ordinary use. What
was decided is to print the plain phrase in place of the bare word, keeping the
word as the value the record stores.

`

const footer = `## Finding terms this list does not have

` + "`go run ./scripts/vocabulary -candidates`" + ` lists the hyphenated compounds in
the Go strings and the personas that no term above covers, most frequent first.
Nothing mechanical tells a coinage from an ordinary compound, so the list is for
a person to read: a term found there is added to ` + "`internal/terms/inventory`" + `
with its meaning and a proposed decision, the Lead Product Manager decides it,
and the document is run again.

Between readings, the terms check keeps the vocabulary from growing unnoticed:
it refuses a compound word written anywhere a person or a role reads the
harness's words unless the register, this inventory, or the rules for ordinary
English account for it. [Working on yoyo
itself](developing-yoyo.md#what-the-terms-check-counts-as-a-new-term) states
the rule.
`

const unreadIntro = `## Compounds still to be decided

The terms check began reading for new compound words on 2026-10-06. These are
the ones it found already written that nothing accounted for: no row in the
register, no term above, not ordinary English by the check's rules, and not on
the register's list of ordinary compounds. Some are terms of art and some are
ordinary English the rules do not recognise; nobody has decided yet
which they are. The check allows each by name until somebody does.

Deciding one takes it off the list in ` + "`internal/terms/inventory`" + `: an
ordinary compound goes on the register's list of ordinary compounds, a term
worth keeping gets a row in the register, and a term worth replacing is added
above with its meaning and its plain words. A word nothing writes any more is
refused by the check until it is taken off.

`
