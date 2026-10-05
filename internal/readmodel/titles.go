package readmodel

// Work item citations are resolved from the tracker when a surface is produced.
// The stored text stays as its author wrote it; every mention a person reads
// carries the current priority, labels, title, and full identifier.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// unavailableWorkItem keeps an unreadable reference visible without inventing
// either a title or a priority.
const unavailableWorkItem = "title unavailable"
const untitledWorkItem = "no title recorded"

// Rendered citations end with their own identifier. Titles can contain other
// parenthesized identifiers, so the final one before a sentence boundary,
// status separator, next citation, or line break identifies the item.
var renderedCitationStart = regexp.MustCompile(`\(P[0-4](?:, [^()\n]*)?\) |title unavailable \(`)
var citationIdentifier = regexp.MustCompile(`\(([A-Za-z0-9][A-Za-z0-9_-]*-[a-z0-9]+(?:\.[0-9]+)*)\)`)
var citationBoundary = regexp.MustCompile(`^(?:[.!?]\s+[A-Z]|\s+—\s+)`)

func renderedCitations(text string) [][]int {
	starts := renderedCitationStart.FindAllStringIndex(text, -1)
	var spans [][]int
	for i, start := range starts {
		end := len(text)
		if newline := strings.IndexByte(text[start[0]:], '\n'); newline >= 0 {
			end = start[0] + newline
		}
		if i+1 < len(starts) && starts[i+1][0] < end {
			end = starts[i+1][0]
		}
		ids := citationIdentifier.FindAllStringSubmatchIndex(text[start[0]:end], -1)
		if len(ids) == 0 {
			continue
		}
		id := ids[len(ids)-1]
		for _, candidate := range ids {
			if citationBoundary.MatchString(text[start[0]+candidate[1] : end]) {
				id = candidate
				break
			}
		}
		if strings.HasPrefix(text[start[0]:], unavailableWorkItem) {
			id = ids[0]
		}
		spans = append(spans, []int{start[0], start[0] + id[1], start[0] + id[2], start[0] + id[3]})
	}
	return spans
}

// candidateToken is a run of the characters an identifier is made of. Each one
// is then classified; most are ordinary words and are passed over.
var candidateToken = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._-]*`)

var (
	dottedNumber = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+$`)
	children     = regexp.MustCompile(`^(\.[0-9]+)*$`)
)

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

// WorkItemTitles indexes the tracker fields used to name work to a person.
// Nil means the tracker could not be read: full identifiers still receive the
// unavailable marker, while ambiguous dotted numbers are left alone.
type WorkItemTitles struct {
	titles map[string]string
	items  map[string]beads.WorkItem
	// roots is each item's identifier up to its first child, "yoyodyne-ifd",
	// which is what a bare number is read against.
	roots map[string]bool
	// prefixes is each root without its hash, "yoyodyne", which is what makes a
	// full identifier recognisable as one the tracker does not hold.
	prefixes map[string]bool
	// hashes is each root's hash, "ifd", with the roots carrying it.
	hashes map[string][]string
}

// NewWorkItemTitles indexes a listing of the tracker's items.
func NewWorkItemTitles(items []beads.WorkItem) *WorkItemTitles {
	index := &WorkItemTitles{
		titles:   make(map[string]string, len(items)),
		items:    make(map[string]beads.WorkItem, len(items)),
		roots:    map[string]bool{},
		prefixes: map[string]bool{},
		hashes:   map[string][]string{},
	}
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		index.titles[id] = strings.TrimSpace(item.Title)
		item.ID = id
		item.Labels = append([]string(nil), item.Labels...)
		index.items[id] = item
		root, _, _ := strings.Cut(id, ".")
		if index.roots[root] {
			continue
		}
		index.roots[root] = true
		if cut := strings.LastIndex(root, "-"); cut > 0 && cut < len(root)-1 {
			index.prefixes[root[:cut]] = true
			hash := root[cut+1:]
			index.hashes[hash] = append(index.hashes[hash], root)
		}
	}
	return index
}

// ReadWorkItemTitles lists every item the tracker holds, closed work included,
// for the titles. It is one tracker command however many numbers are resolved.
func ReadWorkItemTitles(ctx context.Context, sources Sources) (*WorkItemTitles, error) {
	return ReadWorkItemTitlesFrom(ctx, sources.Tracker, sources.TrackerTimeout)
}

// ReadWorkItemTitlesFrom is the same bounded reading for a conversation whose
// tracker capability lists work but does not select ready work.
func ReadWorkItemTitlesFrom(ctx context.Context, tracker interface {
	List(context.Context, string) ([]beads.WorkItem, error)
}, timeout time.Duration) (*WorkItemTitles, error) {
	if tracker == nil {
		return nil, nil
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	items, err := tracker.List(ctx, "")
	if err != nil {
		return nil, err
	}
	return NewWorkItemTitles(items), nil
}

// Title is what the tracker calls one item, and whether it holds the item.
func (w *WorkItemTitles) Title(id string) (string, bool) {
	if w == nil {
		return "", false
	}
	title, known := w.titles[id]
	return title, known
}

// Name is the complete citation for an identifier supplied by a durable
// record. It also works without a readable tracker.
func (w *WorkItemTitles) Name(id string) string {
	if w == nil {
		return unavailableWorkItem + " (" + id + ")"
	}
	item, known := w.items[id]
	if !known {
		return unavailableWorkItem + " (" + id + ")"
	}
	title := strings.Join(strings.Fields(item.Title), " ")
	if title == "" {
		title = untitledWorkItem
	}
	labels := ""
	for _, label := range item.Labels {
		if label = strings.TrimSpace(label); label != "" {
			labels += ", " + label
		}
	}
	return fmt.Sprintf("(P%d%s) %s (%s)", item.Priority, labels, title, id)
}

// WorkItemReference preserves a known root reference until the outgoing surface
// reads the tracker. Dotted identifiers are already unambiguous in prose.
func WorkItemReference(id string) string {
	if strings.Contains(id, ".") {
		return id
	}
	var titles *WorkItemTitles
	return titles.Name(id)
}

// CitedText carries the prose a card shows beside its raw record. Identifiers
// used to navigate or query remain raw; a surface projects this map when it
// displays a field, so role-written reasons and notes use the same resolver.
func (w *WorkItemTitles) CitedText(record any) map[string]string {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil
	}
	var fields any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil
	}
	texts := map[string]string{}
	var collect func(any)
	collect = func(value any) {
		switch value := value.(type) {
		case string:
			if cited := w.Cite(value); cited != value {
				texts[value] = cited
			}
		case []any:
			for _, entry := range value {
				collect(entry)
			}
		case map[string]any:
			for _, entry := range value {
				collect(entry)
			}
		}
	}
	collect(fields)
	if len(texts) == 0 {
		return nil
	}
	return texts
}

// Cite expands identifiers in prose, including every later mention of an item.
// Commands, paths, links, quantities, and explicit version numbers stay intact.
func (w *WorkItemTitles) Cite(text string) string {
	if text == "" {
		return text
	}
	var cited strings.Builder
	last := 0
	rendered := renderedCitations(text)
	protected := 0
	for _, match := range candidateToken.FindAllStringIndex(text, -1) {
		start, end := match[0], match[1]
		for protected < len(rendered) && rendered[protected][1] <= start {
			protected++
		}
		if protected < len(rendered) && start >= rendered[protected][0] && start < rendered[protected][1] {
			span := rendered[protected]
			if span[0] >= last && !insideCode(text, span[0]) {
				id := text[span[2]:span[3]]
				if full, _ := w.identify(id); full != "" {
					id = full
				}
				cited.WriteString(text[last:span[0]])
				cited.WriteString(w.Name(id))
				last = span[1]
			}
			continue
		}
		if start < last {
			continue
		}
		for end > start && strings.ContainsRune(".-_", rune(text[end-1])) {
			end--
		}
		id, _ := w.identify(text[start:end])
		if id == "" && explicitWorkItemReference(text, start) {
			root, rest, _ := strings.Cut(text[start:end], ".")
			if cut := strings.LastIndex(root, "-"); cut > 0 && isHash(root[cut+1:]) && rest == "" {
				id = root
			}
		}
		if id == "" || !w.standsAlone(text, start, end) || insideCode(text, start) {
			continue
		}
		// Older surfaces wrote either "id (title)" or "title (id)".
		// Remove that adjacent title when it matches the tracker; titles
		// elsewhere in the prose never suppress a citation.
		if w != nil {
			title := strings.Join(strings.Fields(w.titles[id]), " ")
			if title != "" {
				for _, echo := range []string{title, singleLine(title, 100), singleLine(title, 160)} {
					if strings.HasPrefix(text[end:], " ("+echo+")") {
						end += len(echo) + 3
					}
					if start > 0 && text[start-1] == '(' && end < len(text) && text[end] == ')' && strings.HasSuffix(text[:start-1], echo+" ") {
						start -= len(echo) + 2
						end++
						break
					}
					if start > 0 && text[start-1] == '[' && strings.HasPrefix(text[end:], "] "+echo) {
						start--
						end += len(echo) + 2
						break
					}
				}
			}
		}
		if start < last {
			continue
		}
		cited.WriteString(text[last:start])
		cited.WriteString(w.Name(id))
		last = end
	}
	cited.WriteString(text[last:])
	return cited.String()
}

// CiteAfter retains the common rendering entry point for adjacent text. Every
// mention receives its own complete citation, regardless of the prior text.
func (w *WorkItemTitles) CiteAfter(prior, text string) string {
	return w.Cite(text)
}

// An unreadable root has the same spelling as an ordinary hyphenated word.
// Work-item wording makes that reference explicit without guessing from the
// spelling alone. Durable fields already identified as work use Name instead.
func explicitWorkItemReference(text string, start int) bool {
	before := strings.ToLower(strings.TrimSpace(text[:start]))
	for _, cue := range []string{"work item", "item", "epic", "blocked on", "waiting on", "admitted as", "filed:"} {
		if before == cue || strings.HasSuffix(before, " "+cue) || strings.HasSuffix(before, "\n"+cue) {
			return true
		}
	}
	return false
}

// identify reads one token as a work item identifier: the identifier it names,
// and whether its shape alone makes it one — so that one the tracker does not
// hold is said to be unknown — rather than only a number that happens to name
// an item.
func (w *WorkItemTitles) identify(token string) (string, bool) {
	if w != nil {
		if _, known := w.titles[token]; known {
			return token, true
		}
	}
	root, rest, _ := strings.Cut(token, ".")
	if rest != "" {
		rest = "." + rest
	}
	if !children.MatchString(rest) {
		return "", false
	}
	// The full form: a prefix the tracker uses, a hash, and children. A root with
	// no children the tracker does not hold is a hyphenated word far more often
	// than a missing epic, so it takes a child to be said to be unknown.
	if cut := strings.LastIndex(root, "-"); cut > 0 && isHash(root[cut+1:]) && (w == nil || len(w.roots) == 0 || w.prefixes[root[:cut]]) {
		if rest != "" {
			return token, true
		}
		return "", false
	}
	if w == nil {
		return "", false
	}
	// The form without the product: a hash exactly one root carries.
	if rest != "" {
		if roots := w.hashes[root]; len(roots) == 1 {
			return roots[0] + rest, true
		}
	}
	// The bare dotted number, read only where exactly one item answers to it.
	if dottedNumber.MatchString(token) {
		var found string
		for candidate := range w.roots {
			if _, known := w.titles[candidate+"."+token]; known {
				if found != "" {
					return "", false
				}
				found = candidate + "." + token
			}
		}
		return found, false
	}
	return "", false
}

// standsAlone reports whether the token at [start, end) is a word of its own
// rather than part of a path, a link, an amount, or a quantity.
func (w *WorkItemTitles) standsAlone(text string, start, end int) bool {
	if start > 0 {
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		if strings.ContainsRune(`/=#$<@\~≥≤+`, before) {
			return false
		}
	}
	if end < len(text) {
		after, _ := utf8.DecodeRuneInString(text[end:])
		if strings.ContainsRune(`/%=@>`, after) {
			return false
		}

	}
	if dottedNumber.MatchString(text[start:end]) {
		preceding := strings.Fields(strings.ToLower(text[:start]))
		if len(preceding) > 0 {
			switch strings.Trim(preceding[len(preceding)-1], "():,;") {
			case "go", "golang", "python", "node", "node.js", "java", "ruby", "rust", "version", "release", "v":
				return false
			}
		}
		following := strings.Fields(text[end:])
		if len(following) > 0 {
			unit := strings.ToLower(strings.TrimRight(following[0], ".,;:)"))
			if quantityUnits[unit] {
				return false
			}
		}
	}
	return true
}

// insideCode reports whether the offset falls inside an inline code span on its
// line. What is between backticks is something to be typed, and a title put
// into the middle of a command is a command that no longer runs.
func insideCode(text string, offset int) bool {
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	var fence string
	for _, line := range strings.Split(text[:lineStart], "\n") {
		line = strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
		} else if strings.HasPrefix(line, "```") {
			fence = "```"
		} else if strings.HasPrefix(line, "~~~") {
			fence = "~~~"
		}
	}
	if fence != "" {
		return true
	}
	return strings.Count(text[lineStart:offset], "`")%2 == 1
}

// isHash reports whether a segment has the shape of a tracker hash: short,
// lower-case letters and digits.
func isHash(segment string) bool {
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
