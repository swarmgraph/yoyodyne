// Package readiness reads what a work item says it needs, and checks each of
// those needs against the tree as it stands — before the item is dispatched
// rather than by the run that spends itself discovering it.
//
// Four items in a fortnight cost a full developer run each to establish
// something a read would have said in milliseconds, and they are one class
// rather than four accidents:
//
//   - yoyodyne-ifd.284 asked for the coherence scan "shipping after the DM
//     hourly sweep and inheriting its machinery". The machinery was whole and
//     reviewer-approved on yoyodyne-ifd.283's unmerged branch, so nothing the
//     item needed compiled on the base its run was cut from.
//   - yoyodyne-ifd.209.14 asked for a conversion whose subject — ifd.142's
//     coordination slice — is on a branch too. Its own text says it is "not
//     decomposable further until the runtime exists and the management-conversion
//     design lands", and it was pulled into a run twice regardless.
//   - yoyodyne-ifd.100.1 states "blocked until the architect's answer exists".
//     The answer came, and it negated every one of the item's done conditions;
//     two runs delivered empty trees before a third wrote the ruling down.
//   - yoyodyne-ifd.291 was admitted citing domain.Backend.SupportsRole, deleted
//     weeks earlier by f07a6ba, for a gap yoyodyne-ifd.97 had already closed.
//
// So this asks two questions of an item, and each is a read.
//
//   - Does the tree still hold what the item points at? An item that pinpoints
//     a `file:line` or a qualified symbol is pointing at code somebody read, and
//     a pinpoint naming code that is no longer there is the cheapest possible
//     already-satisfied signal. It is exact, and it clears by itself: the item
//     becomes pullable the moment the code it names is in the tree.
//   - Does the item say of itself that something has to happen first? Sequencing
//     stated in prose is invisible to everything that pulls — the tracker knows
//     about dependency links and not about a sentence — so an item that says it
//     is gated is one the queue reads as ready forever. What clears this is a
//     person: the product manager amending the item, or the development manager
//     recording the dependency the sentence names. The reading is taken from the
//     item as the tracker holds it at each pull and remembered nowhere, so a
//     sentence that has been removed stops refusing at the next pull.
//
// Nothing here refuses anything, and nothing here is enforcement. It reports
// what one item's own statement asks of the tree, and the caller decides. That
// is deliberate: a readiness reading that could stop work would be a second
// account of what may be pulled, beside the tracker's dependency graph and the
// holds, and two accounts of one rule are two answers an operator has to
// adjudicate.
//
// # What it reads, and what it deliberately does not
//
// It reads the four fields somebody authored — title, description, design
// guidance, acceptance criteria — and never the notes. The notes are where the
// harness appends each run's record and where a development manager writes the
// implementation plan, so they are full of paths and symbols that are the work's
// output rather than its prerequisites. That is measured rather than assumed:
// over the 435 items this repository's tracker held when this was written,
// reading pinpoints out of the notes as well fires on 20 of the 76 unfinished
// items, nearly every one of them a file the item exists to create. Reading the
// authored fields alone fires on 6 items in 435 and on none of the unfinished
// ones, and every one of the 6 is a rename somebody really made. A guard that
// fires on a quarter of the queue is one whoever reads it learns to read past.
//
// A bare path is not a pinpoint for the same reason. An item that names
// `docs/configuration/agents.md` is as likely to be asking for the file as
// citing it, and the three documentation-split items in this backlog are exactly
// that shape. A line number and a qualified symbol are not ambiguous in that
// way: both mean somebody read what is there.
package readiness

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// Kind is one shape of unmet prerequisite, named for the incident that put it
// here. The set is closed: every unmet prerequisite is one of these, because a
// refusal whose kind nobody recognizes is one nobody knows what to do about.
type Kind string

const (
	// KindStalePinpoint is a `file:line` or a qualified symbol the item points at
	// that the tree no longer holds. It is yoyodyne-ifd.291's shape.
	KindStalePinpoint Kind = "stale-pinpoint"
	// KindSubjectNotInRepository is the item's own subject being absent: a file
	// it cites that is not in the tree, or a statement that it waits for
	// something to exist. It is yoyodyne-ifd.209.14's shape.
	KindSubjectNotInRepository Kind = "subject-not-in-repository"
	// KindMachineryOnABranch is the item saying it inherits or follows work that
	// has not landed. It is yoyodyne-ifd.284's shape.
	KindMachineryOnABranch Kind = "machinery-on-a-branch"
	// KindForbiddenByRuling is the item saying it is gated on a decision. It is
	// yoyodyne-ifd.100.1's shape, where the decision came and said no.
	KindForbiddenByRuling Kind = "forbidden-by-ruling"
)

// maxUnmet bounds how many prerequisites one reading reports. Whoever is told
// has to act on the first of them, and the item itself is where the rest are.
// The pinpoints are kept ahead of the sentences when the bound cuts, because a
// pinpoint is exact and clears itself where a sentence needs a person.
const maxUnmet = 5

// maxClauseBytes keeps one quoted clause to its part of one line. What is quoted
// came out of the tracker, so it is folded rather than trusted to be short.
const maxClauseBytes = 160

// admitter is who releases a prerequisite that came from the item's own
// citation. The citation is what was admitted, so correcting it — or retiring
// the item because the work is done — is the product manager's. It also clears
// itself, which is the difference between this and the one below: the item
// becomes pullable the moment the code it names is in the tree.
const admitter = "the Lead Product Manager, who admitted the citation"

// sequencer is who releases a prerequisite the item states about itself. The
// sentence is the item's own, so amending it is the product manager's; recording
// the dependency it names, where the work it waits for is admitted, is the
// development manager's. Nothing clears a sentence on its own.
const sequencer = "the Lead Product Manager, or the development manager who records the dependency"

// Unmet is one prerequisite the item states that the tree does not meet.
type Unmet struct {
	Kind Kind `json:"kind"`
	// Missing is what the item needs and the tree does not have, in the item's
	// own words where the item supplied them.
	Missing string `json:"missing"`
	// Evidence is the read that says so, named so that whoever is told can make
	// the same read. A refusal nobody can check is one nobody can overrule.
	Evidence string `json:"evidence"`
	// Decides is who releases this, because an item held back by nobody in
	// particular is an item held back forever.
	Decides string `json:"decides"`
	// Field is the authored field a sentence was read out of — "title",
	// "description", "design guidance", or "acceptance criteria" — and is empty
	// for a pinpoint, which is read from the item's statement as a whole. It is
	// named because the sentence is what somebody has to remove, and one read out
	// of a field nobody thought to look in outlives the amendment made to the
	// field they did: see FieldOutOfReach.
	Field string `json:"field,omitempty"`
}

// The authored fields a sentence is read out of, by the name a refusal gives
// each. They are the four Statement joins, in the same order.
const (
	FieldTitle              = "title"
	FieldDescription        = "description"
	FieldDesignGuidance     = "design guidance"
	FieldAcceptanceCriteria = "acceptance criteria"
)

// FieldOutOfReach reports an authored field the product manager's own update
// does not rewrite. Her update takes the title and the description and nothing
// else, and her read of an item is bounded, so a long description can push the
// design guidance and acceptance criteria out of what she is shown. A sentence
// in one of those is one she can remove the copy of she was looking at and
// leave standing, which is why a refusal read out of one says so.
func FieldOutOfReach(field string) bool {
	return field == FieldDesignGuidance || field == FieldAcceptanceCriteria
}

// Describe says what one unmet prerequisite is, in one line: what shape it is,
// who releases it, and then what is actually missing.
//
// That order is the one thing about this line that is not arbitrary. Every place
// it is quoted bounds what it prints, and a line cut short must lose the item's
// own words rather than the name of whoever can act — a reader who is told a
// prerequisite is unmet and not told whose it is has been told nothing they can
// do. The evidence is left to the durable entry, which is not bounded to a line.
func (u Unmet) Describe() string {
	return fmt.Sprintf("%s, released by %s: %s", u.Kind, u.Decides, u.Missing)
}

// Describe names what a reading found, in one line, so a refusal recorded
// against a pass and one docketed for the development manager say it in the same
// words. It is empty for the ordinary item, which meets everything it states.
func Describe(unmet []Unmet) string {
	if len(unmet) == 0 {
		return ""
	}
	described := unmet
	if len(described) > maxUnmet {
		described = described[:maxUnmet]
	}
	said := make([]string, 0, len(described))
	for _, one := range described {
		said = append(said, one.Describe())
	}
	line := strings.Join(said, "; ")
	if remaining := len(unmet) - len(described); remaining > 0 {
		line += fmt.Sprintf("; and %d further prerequisite(s) are unmet and are not named here", remaining)
	}
	return line
}

// Kinds are the kinds one reading found, deduplicated and in a stable order. It
// is what a durable record is keyed by: two readings that found the same kinds
// of thing about one item are the same fact, however the wording moved.
func Kinds(unmet []Unmet) []string {
	seen := make(map[Kind]struct{}, len(unmet))
	kinds := make([]string, 0, len(unmet))
	for _, one := range unmet {
		if _, met := seen[one.Kind]; met {
			continue
		}
		seen[one.Kind] = struct{}{}
		kinds = append(kinds, string(one.Kind))
	}
	sort.Strings(kinds)
	return kinds
}

// Tree is the repository as it stands, in the reads this makes of it, and the
// standard library its source is written against.
type Tree interface {
	// File is how many lines the file at this repository-relative path has, and
	// whether the tree has the file at all. A path that leaves the tree is
	// absent rather than an error: an item may cite anything, and a citation
	// nobody can resolve is not a reading that failed.
	File(path string) (lines int, present bool, err error)
	// Declares reports the tree's own source naming this symbol — as it is
	// written, as its type and member, or as a method declared on that type.
	Declares(symbol string) (bool, error)
	// Library reports what the language's standard library says of a symbol
	// the tree does not declare. A citation of a standard name is correct
	// whether or not the tree happens to call it.
	Library(symbol string) (Library, error)
}

// Check reports the prerequisites one item states that the tree does not meet,
// pinpoints first and then the sentences, each with the read that says so.
//
// The error is a reading that failed rather than a prerequisite that is unmet,
// and the two are deliberately separate returns: a tree that could not be read
// says nothing about the item, and a caller must not be able to mistake one for
// the other. Whatever could be read is still returned beside it.
func Check(item beads.WorkItem, tree Tree) ([]Unmet, error) {
	if tree == nil {
		return nil, errors.New("checking an item's prerequisites requires the tree to check them against")
	}
	unmet, problems := stalePinpoints(Statement(item), tree)
	unmet = append(unmet, statedPreconditions(item)...)
	if len(unmet) > maxUnmet {
		unmet = unmet[:maxUnmet]
	}
	return unmet, errors.Join(problems...)
}

// Statement is the item as somebody authored it: the four fields a person wrote,
// and not the notes the harness appends each run's record to.
//
// It is the same four fields the conflict surface is read from and the same four
// a protected-path grant is read from, for the same reason each of them gives:
// the harness writes into the notes, so a reading that took them in would let a
// run's own record answer a question about the item somebody admitted. It is
// exported so that what counts as the item's own words is a thing this package
// states rather than a convention three packages happen to share.
func Statement(item beads.WorkItem) string {
	fields := authored(item)
	texts := make([]string, 0, len(fields))
	for _, one := range fields {
		texts = append(texts, one.text)
	}
	return strings.Join(texts, "\n")
}

// authoredField is one of the fields Statement joins, with the name a refusal
// gives it.
type authoredField struct {
	name string
	text string
}

func authored(item beads.WorkItem) []authoredField {
	return []authoredField{
		{FieldTitle, item.Title},
		{FieldDescription, item.Description},
		{FieldDesignGuidance, item.Design},
		{FieldAcceptanceCriteria, item.AcceptanceCriteria},
	}
}

// pathPinpoint is a repository path with a line number on it. The extension list
// is closed and the line number is required: a bare path is as likely to be what
// an item asks for as what it cites, and a line number is not.
var pathPinpoint = regexp.MustCompile(`(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+\.(?:go|md|yaml|yml|json|sh):[0-9]{1,7}`)

// symbolPinpoint is a package-qualified Go symbol: a lower-case package name and
// one to three exported members after it. The exported first member is what
// keeps this off ordinary prose — `e.g.` and `docs.md` are not symbols — and the
// bound keeps it off a sentence that happens to run identifiers together.
var symbolPinpoint = regexp.MustCompile(`[a-z][a-z0-9]*(?:\.[A-Z][A-Za-z0-9_]*){1,3}`)

// stalePinpoints reads every pinpoint the item makes against the tree. A path
// the tree does not have is the item's subject being absent; a path it has whose
// line is past the end, and a symbol nothing declares, are the citation having
// gone stale under the item. A symbol the tree does not declare is then read
// against the standard library, because a standard name is a correct citation
// whether or not the tree calls it.
func stalePinpoints(statement string, tree Tree) ([]Unmet, []error) {
	var (
		unmet         []Unmet
		problems      []error
		libraryUnread bool
	)
	for _, cited := range matches(statement, pathPinpoint) {
		path, line, ok := splitPinpoint(cited)
		if !ok {
			continue
		}
		lines, present, err := tree.File(path)
		if err != nil {
			problems = append(problems, fmt.Errorf("read %s to check the pinpoint %s: %w", path, cited, err))
			continue
		}
		switch {
		case !present:
			unmet = append(unmet, Unmet{
				Kind:     KindSubjectNotInRepository,
				Missing:  fmt.Sprintf("it cites %s, and the tree has no %s", cited, path),
				Evidence: "the file is not in the tree",
				Decides:  admitter,
			})
		case line > lines:
			unmet = append(unmet, Unmet{
				Kind:     KindStalePinpoint,
				Missing:  fmt.Sprintf("it cites %s, and %s has %d line(s)", cited, path, lines),
				Evidence: "the file is in the tree and the line is past its end",
				Decides:  admitter,
			})
		}
	}
	for _, symbol := range dedupe(matches(statement, symbolPinpoint)) {
		declared, err := tree.Declares(symbol)
		if err != nil {
			problems = append(problems, fmt.Errorf("read the tree for %s: %w", symbol, err))
			continue
		}
		if declared {
			continue
		}
		library, err := tree.Library(symbol)
		if err != nil {
			// The tree was read and does not have the symbol, and that is still
			// acted on, as it was before the library was consulted at all; the
			// refusal says the library went unchecked, so nobody corrects a
			// citation on its account without checking it. The failure is the
			// same one for every symbol after it, so it is reported once.
			if !libraryUnread {
				problems = append(problems, fmt.Errorf("read the standard library for %s: %w", symbol, err))
				libraryUnread = true
			}
			unmet = append(unmet, Unmet{
				Kind:     KindStalePinpoint,
				Missing:  fmt.Sprintf("it cites %s, which nothing in the tree declares; whether Go's standard library has it could not be checked", symbol),
				Evidence: fmt.Sprintf("%s, and Go's standard library could not be read: %v", treeEvidence, err),
				Decides:  admitter,
			})
			continue
		}
		if library.Declared {
			continue
		}
		unmet = append(unmet, undeclared(symbol, library))
	}
	return unmet, problems
}

// treeEvidence is the read that says the tree does not declare a symbol.
const treeEvidence = "no source file names the symbol, its type and member, or a method of that name on that type"

// undeclared is the refusal for a symbol neither the tree nor the standard
// library has, saying which kind of name it was looked for as. A citation
// qualified by a standard package's name is a standard name somebody misremembered
// or the library dropped; any other is a repository name the tree no longer has.
// The two are corrected differently, which is why the refusal says which.
func undeclared(symbol string, library Library) Unmet {
	pkg, _, _ := strings.Cut(symbol, ".")
	if len(library.Packages) > 0 {
		packages := strings.Join(library.Packages, ", ")
		return Unmet{
			Kind:     KindStalePinpoint,
			Missing:  fmt.Sprintf("it cites %s as a name in Go's standard package %s, which has no such name, and nothing in the tree declares it either", symbol, packages),
			Evidence: fmt.Sprintf("Go's standard library, read from %s, has no such name in %s, and %s", library.Source, packages, treeEvidence),
			Decides:  admitter,
		}
	}
	return Unmet{
		Kind:     KindStalePinpoint,
		Missing:  fmt.Sprintf("it cites %s, a repository name that nothing in the tree declares and that is not in Go's standard library", symbol),
		Evidence: fmt.Sprintf("%s, and Go's standard library, read from %s, has no package called %s", treeEvidence, library.Source, pkg),
		Decides:  admitter,
	}
}

// stated is one phrasing in which an item says of itself that something has to
// happen first, and the kind of prerequisite that is.
type stated struct {
	kind    Kind
	pattern *regexp.Regexp
}

// statedPatterns are the phrasings this recognizes. The list is closed and short
// on purpose, and it is calibrated against this repository's own tracker rather
// than chosen for how it reads: over the 435 items the backlog held when this was
// written it fires on 13, six of them unfinished, and each of those six states a
// real gate — a design answer that has not come, a link somebody said would be
// recorded and never was, an anchor whose activation conditions are in prose.
// Every phrasing that fired more widely was dropped, and what it cost to drop
// them is stated rather than hidden: "pending the" fires on prose about anything
// pending, "builds on the" on any item that describes what it builds on, and a
// bare "until" on almost every item in the backlog. This finds sentences an item
// wrote about itself, not sentences containing a word.
var statedPatterns = []stated{
	{KindMachineryOnABranch, regexp.MustCompile(`(?i)inherit(?:s|ing)? (?:its|the|their) machinery`)},
	{KindMachineryOnABranch, regexp.MustCompile(`(?i)\bship(?:s|ping)? after\b`)},
	{KindForbiddenByRuling, regexp.MustCompile(`(?i)\bblocked until\b`)},
	{KindForbiddenByRuling, regexp.MustCompile(`(?i)\bgated on\b`)},
	{KindSubjectNotInRepository, regexp.MustCompile(`(?i)\bnot decomposable further until\b`)},
	{KindSubjectNotInRepository, regexp.MustCompile(`(?i)\buntil the [^.;\n]{0,40}\bexists\b`)},
	{KindSubjectNotInRepository, regexp.MustCompile(`(?i)\bwhen it activates\b`)},
	{KindSubjectNotInRepository, regexp.MustCompile(`(?i)\b(?:this item|it) does not start\b`)},
}

// statedPreconditions reads the sentences in which the item says it cannot be
// started yet, quoting each one so that what is refused is the item's own words
// rather than this package's paraphrase of them.
//
// Overlapping matches are collapsed to the first, because two phrasings inside
// one clause are one gate said twice: "blocked until the architect's answer
// exists" is matched by two of the patterns above, and reporting it twice would
// make one sentence look like two things to settle.
//
// Each field is read on its own, and the refusal names the field the sentence
// is in. yoyodyne-ifd.298 is why: its product manager removed the sentence from
// the description, and whoever reads a refusal that says only "it says of
// itself" has to go looking for which copy is left when one is. A sentence in a
// field her update cannot rewrite says that as well, because amending the item
// is then not something she can do from her own conversation.
func statedPreconditions(item beads.WorkItem) []Unmet {
	var unmet []Unmet
	for _, field := range authored(item) {
		unmet = append(unmet, statedIn(field)...)
	}
	return unmet
}

// statedIn is the sentences one authored field states about the item.
func statedIn(field authoredField) []Unmet {
	type found struct {
		at   int
		kind Kind
	}
	var matched []found
	for _, pattern := range statedPatterns {
		for _, at := range pattern.pattern.FindAllStringIndex(field.text, -1) {
			matched = append(matched, found{at: at[0], kind: pattern.kind})
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].at < matched[j].at })
	where := "its " + field.name
	if FieldOutOfReach(field.name) {
		where += ", which the Lead Product Manager's update does not rewrite,"
	}
	var (
		unmet []Unmet
		past  int
	)
	for _, one := range matched {
		if one.at < past {
			continue
		}
		clause := clauseAt(field.text, one.at)
		past = one.at + len(clause)
		unmet = append(unmet, Unmet{
			Kind:     one.kind,
			Missing:  fmt.Sprintf("%s says of it: %q", where, singleLine(clause, maxClauseBytes)),
			Evidence: fmt.Sprintf("the sentence is in the item's %s as the tracker holds it at this pull, and nothing in the tracker records it as a dependency", field.name),
			Decides:  sequencer,
			Field:    field.name,
		})
	}
	return unmet
}

// clauseAt is the clause beginning at this position: everything up to the
// sentence or clause that follows it, bounded. The bound is what keeps a
// paragraph with no full stop in it from becoming the whole of a refusal.
func clauseAt(statement string, at int) string {
	rest := statement[at:]
	if len(rest) > maxClauseBytes*2 {
		rest = rest[:maxClauseBytes*2]
	}
	if end := strings.IndexAny(rest, ".;\n"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// matches are the pinpoints of one shape in the text, taken only where they
// stand on their own. The boundary test is what keeps a symbol out of the middle
// of a longer identifier and a path out of the middle of a longer path, which
// Go's regular expressions cannot express directly.
func matches(text string, shape *regexp.Regexp) []string {
	var found []string
	for _, at := range shape.FindAllStringIndex(text, -1) {
		if extends(text, at[0]-1, -1) || extends(text, at[1], 1) {
			continue
		}
		found = append(found, text[at[0]:at[1]])
	}
	return found
}

// extends reports the byte at this position continuing a pinpoint in the given
// direction, so a match with one on either side is part of something longer.
//
// The full stop is the whole of why this takes a direction. It is both the
// separator inside every symbol this reads and the end of every sentence one is
// written in, so treating it as a continuation outright would drop every
// citation somebody put at the end of a sentence — which, in a tracker whose
// items are written in prose, is most of them. It continues a pinpoint only when
// something a pinpoint could carry is on the far side of it.
func extends(text string, at, direction int) bool {
	if at < 0 || at >= len(text) {
		return false
	}
	character := text[at]
	if character == '.' {
		return extends(text, at+direction, direction)
	}
	return character == '_' || character == '/' || character == '-' ||
		(character >= '0' && character <= '9') ||
		(character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z')
}

// splitPinpoint separates a `path:line` citation into its two halves. A line
// number that will not parse is not a pinpoint, which is what the false return
// is for.
func splitPinpoint(cited string) (string, int, bool) {
	colon := strings.LastIndex(cited, ":")
	if colon < 0 {
		return "", 0, false
	}
	line, err := strconv.Atoi(cited[colon+1:])
	if err != nil || line < 1 {
		return "", 0, false
	}
	return cited[:colon], line, true
}

// dedupe keeps the first of each repeated value, so an item that names one
// symbol three times is told about it once.
func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if _, met := seen[value]; met {
			continue
		}
		seen[value] = struct{}{}
		kept = append(kept, value)
	}
	return kept
}

// singleLine folds a value into one bounded line, so tracker prose stays part of
// the sentence describing it whatever it contains. It is cut on a rune boundary:
// a line truncated mid-rune is not text.
func singleLine(value string, limit int) string {
	return oneline.Fold(value, limit)
}
