// Package workref finds the work item references in text a role wrote, and
// keeps each one as the item's full id and the bytes it covers.
//
// It is the matcher the observability design's "Work item references" section
// fixes (docs/designs/observability-and-dashboard.md): one matcher, used when
// text is recorded and when older text is read, so that every surface shows a
// reference the same way and nothing around a reference changes. Rendering what
// is found is the read model's (internal/readmodel/titles.go); this package
// holds no title, priority, or label, because those are read from the tracker
// each time text is shown.
//
// It imports nothing of the harness's, so a record that keeps references — a
// report, a lane report — can carry them without depending on the read model.
package workref

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Reference is one work item named in a text: the item's full id, and the start
// and end byte of what named it in the stored text. The bytes cover the id as
// it was written and nothing else; for a short id they are the short id, and
// the full id is what it was resolved to when the text was recorded.
type Reference struct {
	ID    string `json:"id"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Index is what the matcher knows of the tracker: its id prefixes, and, where
// it was read from a listing, every id the tracker holds. The zero value and nil
// know no prefix and find nothing.
type Index struct {
	prefixes map[string]bool
	held     map[string]bool
	// suffixes is each held id's endings after its prefix's hyphen or after a
	// dot, with the ids that end in it, which is what a short id is read against.
	suffixes map[string][]string
	listed   bool
	// known is ids a caller named as work items (Knowing), which are references
	// without being held.
	known map[string]bool
}

// NewIndex indexes a listing of every id the tracker holds. The prefixes are
// read from the ids themselves, plus any the caller names, so a tracker holding
// nothing yet still recognises its own ids.
func NewIndex(ids []string, prefixes ...string) *Index {
	index := &Index{prefixes: map[string]bool{}, held: map[string]bool{}, suffixes: map[string][]string{}, listed: true}
	for _, prefix := range prefixes {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			index.prefixes[prefix] = true
		}
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		prefix, rest, ok := splitPrefix(id)
		if !ok || index.held[id] {
			continue
		}
		index.held[id] = true
		index.prefixes[prefix] = true
		index.suffixes[rest] = append(index.suffixes[rest], id)
		for at := strings.IndexByte(rest, '.'); at >= 0; {
			ending := rest[at+1:]
			index.suffixes[ending] = append(index.suffixes[ending], id)
			next := strings.IndexByte(ending, '.')
			if next < 0 {
				break
			}
			at += next + 1
		}
	}
	return index
}

// NewPrefixIndex knows the tracker's prefixes and none of its items: what the
// matcher has when the tracker could not be read. It finds full ids by their
// shape alone and resolves no short id.
func NewPrefixIndex(prefixes ...string) *Index {
	index := &Index{prefixes: map[string]bool{}}
	for _, prefix := range prefixes {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			index.prefixes[prefix] = true
		}
	}
	return index
}

// PrefixOf is the tracker prefix of a full id, "yoyodyne" of
// "yoyodyne-ifd.432.28", and empty for anything that is not one.
func PrefixOf(id string) string {
	prefix, _, ok := splitPrefix(strings.TrimSpace(id))
	if !ok {
		return ""
	}
	return prefix
}

// Prefixes is every prefix the index knows, sorted.
func (x *Index) Prefixes() []string {
	if x == nil {
		return nil
	}
	prefixes := make([]string, 0, len(x.prefixes))
	for prefix := range x.prefixes {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	return prefixes
}

// Knowing is the index with ids the caller already knows are work items —
// a run's item, an entry's item — matched as references whatever their shape,
// so one the tracker could not be read for is still marked rather than taken
// for a word. Their prefixes are learned with them.
func (x *Index) Knowing(ids ...string) *Index {
	var known []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); PrefixOf(id) != "" && (x == nil || !x.held[id]) {
			known = append(known, id)
		}
	}
	if len(known) == 0 {
		return x
	}
	widened := &Index{prefixes: map[string]bool{}, held: map[string]bool{}, suffixes: map[string][]string{}}
	if x != nil {
		for prefix := range x.prefixes {
			widened.prefixes[prefix] = true
		}
		for id := range x.held {
			widened.held[id] = true
		}
		widened.suffixes = x.suffixes
		widened.listed = x.listed
	}
	widened.known = map[string]bool{}
	for _, id := range known {
		widened.prefixes[PrefixOf(id)] = true
		widened.known[id] = true
	}
	return widened
}

// Holds reports whether the tracker listing the index was built from holds id.
func (x *Index) Holds(id string) bool {
	return x != nil && x.held[id]
}

// Find is every reference in text a role has just written, as it is recorded:
// every full id, and every short id exactly one held item ends in. Unresolved
// is how many short ids it left as written because no item, or more than one,
// ends in them. This is the only place a short id is resolved: whether one
// named exactly one item can be known only against the tracker as it stood when
// the text was written.
func (x *Index) Find(text string) (found []Reference, unresolved int) {
	return x.find(text, true)
}

// FindFull is every full id in text recorded before references were kept, as
// it is read now. A short id in such text is never resolved; unresolved counts
// the ones some held item ends in, so a reader is told how many were left as
// written.
func (x *Index) FindFull(text string) (found []Reference, unresolved int) {
	return x.find(text, false)
}

func (x *Index) find(text string, resolveShort bool) ([]Reference, int) {
	if x == nil || len(x.prefixes) == 0 || text == "" {
		return nil, 0
	}
	var found []Reference
	unresolved := 0
	for at := 0; at < len(text); {
		if !idByte(text[at]) || (at > 0 && !boundaryBefore(text, at)) {
			at++
			continue
		}
		end := at
		for end < len(text) && idByte(text[end]) {
			end++
		}
		// A dot it ends with is a full stop, not part of it.
		for end > at && text[end-1] == '.' {
			end--
		}
		token := text[at:end]
		next := end
		if next == at {
			next = at + 1
		}
		if !boundaryAfter(text, end) || insideCode(text, at) {
			at = next
			continue
		}
		if x.fullID(token) {
			found = append(found, Reference{ID: token, Start: at, End: end})
		} else if x.shortCandidate(text, token, end) {
			switch ids := x.suffixes[token]; {
			case resolveShort && len(ids) == 1:
				found = append(found, Reference{ID: ids[0], Start: at, End: end})
			case resolveShort && strings.Contains(token, "."):
				// A dotted short id that names no item, or more than one, is
				// left as written and counted; a word with a digit in it that
				// names nothing is only a word.
				unresolved++
			case resolveShort && len(ids) > 1:
				unresolved++
			case !resolveShort && len(ids) > 0:
				unresolved++
			}
		}
		at = next
	}
	return found, unresolved
}

// fullID reports whether a token is a full id: the tracker's prefix and a
// hyphen, then the item's hash and any dotted children. The tracker holding it
// settles it. One the tracker does not hold is still a reference where it has
// a dotted child or a digit in its hash, which no ordinary hyphenated word has,
// and is a word otherwise: "yoyodyne-specific" names nothing. A match never
// stops early at a shorter id, so "ex-7.2s" is the id "ex-7.2s" whether or not
// the tracker holds "ex-7.2".
func (x *Index) fullID(token string) bool {
	prefix, rest, ok := splitPrefix(token)
	if !ok || !x.prefixes[prefix] {
		return false
	}
	if x.held[token] || x.known[token] {
		return true
	}
	hash, children, dotted := strings.Cut(rest, ".")
	if !hashShaped(hash) {
		return false
	}
	if dotted {
		for _, child := range strings.Split(children, ".") {
			if !hashShaped(strings.ToLower(child)) {
				return false
			}
		}
		return true
	}
	return strings.ContainsAny(hash, "0123456789")
}

// shortCandidate reports whether a token may be a short id: something with a
// digit in it, written without a prefix, that is not a plain number and not
// the amount of a quantity that follows it ("3.5 hours").
func (x *Index) shortCandidate(text, token string, end int) bool {
	if !strings.ContainsAny(token, "0123456789") || strings.Trim(token, "0123456789") == "" {
		return false
	}
	if strings.Contains(token, "-") {
		return false
	}
	following := strings.Fields(text[end:])
	if len(following) > 0 && quantityUnits[strings.ToLower(strings.TrimRight(following[0], ".,;:)"))] {
		return false
	}
	return true
}

// Expand is stored text with each reference's bytes replaced by the full id it
// was resolved to, and every other byte kept. References that do not fit the
// text, overlap one before them, or cover bytes that are not the id or an
// ending of it are passed over rather than trusted: a record whose text and
// references disagree — text a redaction changed after its references were
// found, say — is shown as written.
func Expand(text string, references []Reference) string {
	if len(references) == 0 {
		return text
	}
	ordered := append([]Reference(nil), references...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Start < ordered[j].Start })
	var expanded strings.Builder
	last := 0
	for _, reference := range ordered {
		if reference.Start < last || reference.Start < 0 || reference.End > len(text) || reference.Start >= reference.End || strings.TrimSpace(reference.ID) == "" {
			continue
		}
		if covered := text[reference.Start:reference.End]; covered != reference.ID &&
			!strings.HasSuffix(reference.ID, "."+covered) && !strings.HasSuffix(reference.ID, "-"+covered) {
			continue
		}
		expanded.WriteString(text[last:reference.Start])
		expanded.WriteString(reference.ID)
		last = reference.End
	}
	expanded.WriteString(text[last:])
	return expanded.String()
}

// splitPrefix splits a full id at its prefix's hyphen: "yoyodyne" and
// "ifd.432.28" of "yoyodyne-ifd.432.28". The hyphen is the last one before the
// first dot, since a prefix may itself carry a hyphen.
func splitPrefix(id string) (prefix, rest string, ok bool) {
	root, _, _ := strings.Cut(id, ".")
	cut := strings.LastIndexByte(root, '-')
	if cut <= 0 || cut >= len(root)-1 {
		return "", "", false
	}
	return id[:cut], id[cut+1:], true
}

// hashShaped reports whether a segment has the shape of a tracker hash: short,
// lower-case letters and digits.
func hashShaped(segment string) bool {
	if segment == "" || len(segment) > 12 {
		return false
	}
	for _, r := range segment {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// idByte is a byte an id is made of: a letter, a digit, a hyphen, or a dot.
func idByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '.'
}

// boundaryBefore reports whether an id may start at offset: the character
// before it is not one an id is made of, and not one that makes it part of a
// path, a link, an address, or a name with an underscore in it.
func boundaryBefore(text string, offset int) bool {
	before, _ := utf8.DecodeLastRuneInString(text[:offset])
	if before < utf8.RuneSelf && idByte(byte(before)) {
		return false
	}
	return !strings.ContainsRune(`/_=#$<@\~`, before)
}

// boundaryAfter reports whether an id may end at offset: what follows does not
// make it a path segment, an address, or a percentage.
func boundaryAfter(text string, offset int) bool {
	if offset >= len(text) {
		return true
	}
	after, _ := utf8.DecodeRuneInString(text[offset:])
	return !strings.ContainsRune(`/_%=@>`, after)
}

// insideCode reports whether the offset falls inside an inline code span on its
// line. What is between backticks is something to be typed, and a title put
// into the middle of a command is a command that no longer runs.
func insideCode(text string, offset int) bool {
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	return strings.Count(text[lineStart:offset], "`")%2 == 1
}

// quantityUnits are the words a dotted number is a quantity of rather than an
// item, when one follows it: "3.5 hours" is a duration whether or not the
// tracker holds a 3.5.
var quantityUnits = map[string]bool{
	"s": true, "ms": true, "sec": true, "secs": true, "second": true, "seconds": true,
	"m": true, "min": true, "mins": true, "minute": true, "minutes": true,
	"h": true, "hr": true, "hrs": true, "hour": true, "hours": true,
	"d": true, "day": true, "days": true, "week": true, "weeks": true,
	"kb": true, "mb": true, "gb": true, "kib": true, "mib": true, "gib": true, "tokens": true,
	"percent": true, "per": true, "times": true, "x": true, "dollars": true, "usd": true,
}
