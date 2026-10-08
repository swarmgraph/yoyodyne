// Package admission judges whether work about to be admitted is work the
// tracker already holds, so the duplicate is caught where catching it is free
// rather than by the run that spends itself on it.
//
// Two shapes cost a run each in one week, and they are not the same mistake.
// yoyodyne-ifd.274 duplicated the closed yoyodyne-ifd.229: both were admitted
// from the same developer report, the second after the first had landed, so the
// second run's diff against the target branch could not contain work the target
// branch already carried. yoyodyne-ifd.241 was decomposed twice into parallel
// pairs — 241.1/241.2, then 241.3/241.4 — and the second occurrence cost a full
// run, both of its repair attempts, and a confident review demanding code that
// was already merged.
//
// So there are two questions here rather than one general one, and they are
// asked differently because the evidence for them is different.
//
//   - The source is exact. Work admitted from a report or a directive says so in
//     its own record, so a second admission citing the same source is matched by
//     an identifier rather than by a resemblance, and the answer holds however
//     the second item was worded. It is asked over every item the tracker holds,
//     open and closed alike: the 274 shape is a duplicate of work that had
//     already landed, and an admitted-work check that only looked at the open
//     queue would have seen nothing.
//   - The scope is a judgement, so it is asked only where the population is
//     small enough for a judgement to be worth making: among the children of one
//     parent, which is where decomposition happens and where the 241 shape is. A
//     resemblance measured across a whole backlog fires on the vocabulary a
//     backlog shares — "run", "change", "the harness" — and a guard that fires
//     on a quarter of admissions is one whoever admits learns to read past.
//
// Nothing here refuses anything. It reports what one candidate looks like and
// leaves the deciding to the caller, because a duplicate the admitter is not
// shown is a duplicate nobody can rule out.
package admission

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// scopeThreshold is how much of the smaller of two titles' distinctive wording
// they have to share before they are the same scope.
//
// It is calibrated against this repository's own tracker rather than chosen for
// how it reads. Over the 842 sibling pairs the backlog held when this was
// written, three pairs reach it, and the two the incidents name are the top two:
// 241.1/241.3 at 1.00 and 241.2/241.4 at 0.88. Lowering it buys the third pair
// nothing and costs the guard the thing that makes it worth reading, which is
// that it almost never fires.
const scopeThreshold = 0.70

// minimumScopeWords is how many distinctive words a title needs before its scope
// is a thing this will compare at all. A two-word title shares all of itself
// with anything containing it, so measuring one says more about its brevity than
// about the work.
const minimumScopeWords = 3

// minimumWordRunes drops the short words a title is held together with. They
// carry no scope, and the ones that survive the common-word list below are the
// ones nobody thought to write down.
const minimumWordRunes = 3

// maxDescribedMatches bounds how many matches one sentence names. Whoever is
// being told has to act on the first of them; a list is what the tracker is for.
const maxDescribedMatches = 3

// maxDescribedTitleBytes keeps one tracker-supplied title to its part of one
// line. What is being described came out of the tracker, so it is folded rather
// than trusted to be the single line a title is supposed to be.
const maxDescribedTitleBytes = 120

// Candidate is the work an admission would put in the tracker, in the terms this
// judges it by. It is deliberately not a work item: what is being judged does not
// exist yet, and half of what an item carries is written by the tracker.
type Candidate struct {
	// Title is what the work would be called. It is the whole of what the scope
	// judgement reads: a description is where the same work is written up two
	// different ways, and a title is where it is written up twice.
	Title string
	// Parent is the item this work would be created under, and is empty for work
	// being admitted rather than decomposed. The scope judgement is asked only
	// where it is set, because that is what makes the population small.
	Parent string
	// Sources are the reports and directives this admission cites, as the records
	// hold their identifiers rather than as anybody typed them. A candidate citing
	// none is the ordinary case.
	Sources []string
	// Corrects are the closed items a standing-goal correction says it corrects.
	// A correction already admitted for any of them is the same violation, so it
	// is matched exactly, as a source is: one violation found across several
	// closed items or several passes is one item, widened rather than filed again.
	Corrects []string
}

// Match is one item the tracker already holds that a candidate looks like.
type Match struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Status is the state the tracker holds the matched item in. It is carried
	// because it decides what whoever is told does next: a duplicate of open work
	// is work to fold into, and a duplicate of closed work is work that is done.
	Status string `json:"status"`
	// Because is why this item matched, in words that name the evidence rather
	// than assert the conclusion. A match nobody can check is one nobody can
	// overrule.
	Because string `json:"because"`
	// Source is the report or directive both this item and the candidate cite,
	// and is empty on a match found by scope. It is carried apart from the
	// sentence because what to do about the two differs: a scope match is work
	// already carved out, and a source match may be a second piece of work one
	// record genuinely prompted — which is admitted without the citation rather
	// than not admitted.
	Source string `json:"source,omitempty"`
	// Corrected is the closed item both this item and the candidate correct, and
	// is empty on every other match. What to do about it differs again: the
	// correction already admitted is widened to name the closed items it also
	// covers, rather than a second one filed beside it.
	Corrected string `json:"corrected,omitempty"`
}

// correctionPrefix opens the line a standing-goal correction records for each
// closed item it corrects. It is a line of its own, written by the harness, so
// the guard reads an identifier the harness wrote rather than one somebody
// mentioned in prose.
const correctionPrefix = "Corrects closed item "

// CorrectionLine is the line a correction's notes carry for one closed item it
// corrects, which is what a later correction naming the same item is matched
// against.
func CorrectionLine(id, title string) string {
	return correctionPrefix + strings.TrimSpace(id) + ": " + singleLine(title, maxDescribedTitleBytes)
}

// Corrected reports the closed items an item's notes record it as correcting,
// in the order the lines appear. An update that widens a correction appends the
// line for each further item, so this reads every one of them.
func Corrected(item beads.WorkItem) []string {
	var corrected []string
	for _, line := range strings.Split(item.Notes, "\n") {
		rest, found := strings.CutPrefix(strings.TrimSpace(line), correctionPrefix)
		if !found {
			continue
		}
		id, _, _ := strings.Cut(rest, ":")
		if id = strings.TrimSpace(id); id != "" {
			corrected = append(corrected, id)
		}
	}
	return corrected
}

// correctedBy reports which of a candidate's corrected items this item already
// corrects, and is empty for the item that corrects none of them.
func correctedBy(item beads.WorkItem, corrects []string) string {
	if len(corrects) == 0 {
		return ""
	}
	already := Corrected(item)
	for _, wanted := range corrects {
		wanted = strings.TrimSpace(wanted)
		for _, held := range already {
			if wanted != "" && held == wanted {
				return wanted
			}
		}
	}
	return ""
}

// Resembling reports the work the tracker already holds that one candidate looks
// like, most certain first: the items admitted from a source this admission also
// cites, then the siblings already carrying its scope.
//
// admitted is every item the tracker holds rather than the open queue. Closed
// work is exactly what the source question is about, and a scope already carved
// out of a parent is still carved out of it after the child closes.
func Resembling(candidate Candidate, admitted []beads.WorkItem) []Match {
	var (
		fromSource []Match
		fromScope  []Match
	)
	measure := newScope(admitted)
	wording := scopeWords(candidate.Title)
	parent := strings.TrimSpace(candidate.Parent)
	for _, item := range admitted {
		if corrected := correctedBy(item, candidate.Corrects); corrected != "" {
			fromSource = append(fromSource, Match{
				ID:        item.ID,
				Title:     strings.TrimSpace(item.Title),
				Status:    strings.TrimSpace(item.Status),
				Because:   "it already corrects " + corrected + ", which this correction names too",
				Corrected: corrected,
			})
			continue
		}
		if cited := citedBy(item, candidate.Sources); cited != "" {
			fromSource = append(fromSource, Match{
				ID:      item.ID,
				Title:   strings.TrimSpace(item.Title),
				Status:  strings.TrimSpace(item.Status),
				Because: "it was admitted from " + cited + ", which this admission cites too",
				Source:  cited,
			})
			continue
		}
		if parent == "" || strings.TrimSpace(item.Parent) != parent {
			continue
		}
		if !measure.sameScope(wording, item.Title) {
			continue
		}
		fromScope = append(fromScope, Match{
			ID:      item.ID,
			Title:   strings.TrimSpace(item.Title),
			Status:  strings.TrimSpace(item.Status),
			Because: "it is already a child of " + parent + " carrying this scope",
		})
	}
	// Each group is ordered by identifier rather than by how strongly it matched.
	// The strength is a number this does not report and whoever reads it cannot
	// check, and ordering by it would present a ranking as though it were one.
	sortByID(fromSource)
	sortByID(fromScope)
	return append(fromSource, fromScope...)
}

// closedStatus is the state a distinction may be recorded against. It is the
// tracker's own word for work that has left the backlog, finished or retired.
const closedStatus = "closed"

// Distinguish sets aside the one item the admitter named as separate work, and
// returns the matches still standing beside what was set aside.
//
// It exists for the match that is right about the words and wrong about the
// work: a design closed by the architect and the build of that design share a
// parent and most of a title, and on 2026-09-28 the build of the configurable
// state root was refused against yoyodyne-ifd.434.2, the closed design it was
// built from. Before this the only way past was a proposal, which put a decided
// admission in front of the operator as an approval.
//
// Only closed work may be named. A duplicate of open work is work to fold this
// into or to widen, and a way to name it and carry on would be the guard
// switched off by whoever it was guarding against. The named item has to be one
// the tracker holds; it need not be among the matches, and what is set aside
// says whether it was, so the record does not claim the check matched an item it
// never looked at twice.
func Distinguish(matches []Match, admitted []beads.WorkItem, named string) ([]Match, Match, error) {
	named = strings.TrimSpace(named)
	if named == "" {
		return matches, Match{}, nil
	}
	var (
		held  beads.WorkItem
		found bool
	)
	for _, item := range admitted {
		if strings.TrimSpace(item.ID) == named {
			held, found = item, true
			break
		}
	}
	if !found {
		return matches, Match{}, fmt.Errorf("the tracker holds no item %s, so there is nothing for this work to be distinct from", named)
	}
	distinct := Match{
		ID:     named,
		Title:  strings.TrimSpace(held.Title),
		Status: strings.TrimSpace(held.Status),
	}
	if distinct.Status != closedStatus {
		return matches, Match{}, fmt.Errorf("%s is %s rather than closed, and a distinction is recorded only against closed work", named, distinct.state())
	}
	remaining := make([]Match, 0, len(matches))
	for _, match := range matches {
		if match.ID == named {
			distinct.Because = match.Because
			continue
		}
		remaining = append(remaining, match)
	}
	return remaining, distinct, nil
}

// Describe names what a candidate looks like, in one line, so a refusal and a
// proposal put to an operator say it in the same words. It is empty for the
// ordinary candidate that looks like nothing.
func Describe(matches []Match) string {
	if len(matches) == 0 {
		return ""
	}
	described := matches
	if len(described) > maxDescribedMatches {
		described = described[:maxDescribedMatches]
	}
	said := make([]string, 0, len(described))
	for _, match := range described {
		said = append(said, fmt.Sprintf("%s (%s) %q, because %s",
			match.ID, match.state(), singleLine(match.Title, maxDescribedTitleBytes), match.Because))
	}
	line := strings.Join(said, "; ")
	if remaining := len(matches) - len(described); remaining > 0 {
		line += fmt.Sprintf("; and %d further item(s) match and are not named here", remaining)
	}
	return line
}

// state is the status the tracker gave, or plainly that it gave none. An item
// whose state is unknown must not read as open, which is the one state that
// would have whoever is told go looking for it in the queue.
func (m Match) state() string {
	if m.Status == "" {
		return "state unrecorded"
	}
	return m.Status
}

// singleLine folds a value into one bounded line, so a title the tracker holds
// stays part of the sentence describing it whatever it contains. It is cut on a
// rune boundary: a line truncated mid-rune is not text.
func singleLine(value string, limit int) string {
	return oneline.Fold(value, limit)
}

func sortByID(matches []Match) {
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
}

// citedBy reports which of a candidate's sources this item's own record names,
// and is empty for the item that names none.
func citedBy(item beads.WorkItem, sources []string) string {
	for _, source := range sources {
		cited := strings.TrimSpace(source)
		if cited == "" {
			continue
		}
		if cites(item.Notes, cited) {
			return cited
		}
	}
	return ""
}

// cites reports the text naming one identifier as an identifier, rather than as
// the beginning of a longer one. Report and directive identifiers are hex, so a
// bare substring test would have one report's identifier cite another's whenever
// one happened to be a prefix.
//
// The boundary is judged a byte at a time, which is exact for the identifiers
// this is asked about: they are ASCII, and so is every character that could
// extend one.
func cites(text, identifier string) bool {
	lowered := strings.ToLower(text)
	wanted := strings.ToLower(identifier)
	for at := 0; at+len(wanted) <= len(lowered); {
		found := strings.Index(lowered[at:], wanted)
		if found < 0 {
			return false
		}
		start := at + found
		end := start + len(wanted)
		if !extendsIdentifier(lowered, start-1) && !extendsIdentifier(lowered, end) {
			return true
		}
		at = start + 1
	}
	return false
}

// extendsIdentifier reports the byte at this position being one an identifier
// could carry, so a match that has one on either side is part of something
// longer.
//
// A full stop is deliberately not one of them. A source identifier is a prefix
// and thirty-two hex characters, so nothing that could extend one is punctuation
// — while an item's record is prose, and a citation at the end of a sentence has
// a full stop after it. Counting that as an extension is a guard that reads every
// citation somebody wrote a sentence around as no citation at all.
func extendsIdentifier(text string, at int) bool {
	if at < 0 || at >= len(text) {
		return false
	}
	character := rune(text[at])
	return character == '-' || character == '_' ||
		unicode.IsLetter(character) || unicode.IsDigit(character)
}

// scope measures how much of one title's distinctive wording another carries.
//
// The weighting is what makes that mean anything here. Every title in this
// backlog says "run", "change", or "the harness", so counting shared words alone
// would call any two of them alike; a word is weighted by how rare it is among
// the titles the tracker actually holds, so "export" and "skip-worktree" decide
// the answer and "run" barely moves it. The corpus is the tracker's own listing
// rather than anything stored, so the weighting is always the vocabulary of the
// backlog as it stands.
type scope struct {
	weight map[string]float64
	// titles is how many titles the weighting was measured over, and is what a
	// word nobody else used is weighted by.
	titles int
}

func newScope(admitted []beads.WorkItem) scope {
	titles := 0
	frequency := make(map[string]int)
	for _, item := range admitted {
		words := scopeWords(item.Title)
		if len(words) == 0 {
			continue
		}
		titles++
		for word := range words {
			frequency[word]++
		}
	}
	weight := make(map[string]float64, len(frequency))
	for word, count := range frequency {
		weight[word] = wordWeight(titles, count)
	}
	return scope{weight: weight, titles: titles}
}

// wordWeight is how much one shared word says about two titles being the same
// work. It is the ordinary inverse document frequency, shifted so that it stays
// positive however small the corpus is: a weighting that went negative on a
// tracker holding three items would score two titles as less alike for agreeing.
func wordWeight(titles, count int) float64 {
	return math.Log(1 + float64(titles)/float64(count+1))
}

// weightOf is what one word counts for, including a word the backlog has never
// used. Such a word is weighted as the rarest there is, which is the conservative
// direction: it can only make a candidate's own wording count for more, and so
// can only make this find fewer matches.
func (s scope) weightOf(word string) float64 {
	if weight, known := s.weight[word]; known {
		return weight
	}
	return wordWeight(s.titles, 0)
}

func (s scope) mass(words map[string]struct{}) float64 {
	total := 0.0
	for word := range words {
		total += s.weightOf(word)
	}
	return total
}

// sameScope reports two titles describing the same piece of work, as far as
// their wording can say. It compares the shared weight against the smaller of the
// two rather than against their union, because a title that says everything
// another says and then some is the same scope with more words around it — which
// is what a second decomposition of one parent looks like.
func (s scope) sameScope(candidate map[string]struct{}, title string) bool {
	existing := scopeWords(title)
	if len(candidate) < minimumScopeWords || len(existing) < minimumScopeWords {
		return false
	}
	shared := 0.0
	for word := range candidate {
		if _, both := existing[word]; both {
			shared += s.weightOf(word)
		}
	}
	smaller := math.Min(s.mass(candidate), s.mass(existing))
	if smaller <= 0 {
		return false
	}
	return shared/smaller >= scopeThreshold
}

// scopeWords is a title reduced to the words that say what the work is: folded to
// one case, split on everything that is not a letter or a digit, and stripped of
// the short and the ubiquitous.
func scopeWords(title string) map[string]struct{} {
	words := make(map[string]struct{})
	fields := strings.FieldsFunc(strings.ToLower(title), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
	for _, field := range fields {
		if len([]rune(field)) < minimumWordRunes {
			continue
		}
		if _, common := commonWords[field]; common {
			continue
		}
		words[field] = struct{}{}
	}
	return words
}

// commonWords are the words that survive the length cut and still say nothing
// about what a piece of work is. They are the ordinary English joins rather than
// this project's vocabulary: a word this repository happens to overuse is held
// down by the weighting, which measures the backlog it is actually judging, and a
// list of such words written here would be one nobody updates as the product
// changes.
var commonWords = map[string]struct{}{
	"and": {}, "are": {}, "can": {}, "cannot": {}, "could": {}, "does": {},
	"done": {}, "for": {}, "had": {}, "has": {}, "have": {}, "her": {}, "his": {},
	"into": {}, "its": {}, "must": {}, "nor": {}, "not": {}, "our": {},
	"she": {}, "should": {}, "than": {}, "that": {}, "the": {}, "their": {},
	"them": {}, "then": {}, "there": {}, "they": {}, "this": {}, "was": {},
	"were": {}, "what": {}, "when": {}, "where": {}, "which": {}, "will": {},
	"with": {}, "would": {}, "you": {}, "your": {},
}
