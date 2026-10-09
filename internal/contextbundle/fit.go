package contextbundle

// Fitting an assembled product context to the provider it is sent to.
//
// AssembleProduct bounds the context by MaxProductBytes, which is one allowance
// for every provider. An endpoint can accept less than that: Codex takes at most
// 1,048,576 characters of input, role contract and turn included. When a turn
// is over its endpoint's bound with no old conversation left to drop, the
// conversation asks FitProductContext for a shorter briefing rather than
// refusing the turn. The context is read back as text because that is what a
// conversation holds: a picture carried across processes is its text alone.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// GiveWayOrder is the order the sections of a briefing give way when it does
// not fit, least current and least authoritative first. Within one kind the
// longest section goes first, so as few documents as possible are touched.
// Every section not named here never gives way: the header and its rules, the
// recorded-intent section, the work items, the docket, the lists of what was
// omitted or malformed, and any specification carrying the standing goals. The
// role contract and the turn's own evidence are outside the briefing and are
// never offered to this at all.
var GiveWayOrder = []string{
	"Shipped documentation",
	"Command help",
	"Decision record",
	"Design",
	"Architectural invariant",
	"Directory index",
	IntentHeading,
}

// FittedHeading opens the list of what a fitted briefing shortened or left out.
const FittedHeading = "## Left out of this briefing to fit the provider"

// minShortenedBytes is how little of a document is worth keeping rather than
// leaving the whole of it out. Below it, a fragment costs the role more reading
// than it tells it.
const minShortenedBytes = 4 << 10

// standingGoalsHeading finds the Standing goals section a goals document
// carries. A document holding it never gives way, because those goals govern
// every role's output whatever the turn is about.
var standingGoalsHeading = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+Standing goals[ \t]*$`)

// briefingTrailers open the notes AssembleProduct writes straight after the
// shipped documentation, with no heading of their own. Splitting on headings
// would attach them to the last document above them, so a section is cut short
// where one begins and the note stays with the fixed part of the briefing.
var briefingTrailers = []string{omittedDocumentationLead, shippedDocumentationSizeLead, noShippedDocumentation, noShippedDocumentationNamed}

// FittedSection is one section a fitted briefing shortened or left out.
type FittedSection struct {
	// Kind is the section's entry in GiveWayOrder.
	Kind string `json:"kind"`
	// Path is the repository path of the document, empty for the command help.
	Path string `json:"path,omitempty"`
	// Bytes is the section's size in full, heading included.
	Bytes int `json:"bytes"`
	// KeptBytes is how much of the document's text is still carried; zero is a
	// section left out.
	KeptBytes int `json:"kept_bytes"`
}

// Fit is what FitProductContext made of a briefing.
type Fit struct {
	Text string
	// Fits is false where every section that may give way has and the text is
	// still past the target. Text is then the shortest briefing available.
	Fits     bool
	Sections []FittedSection
}

// briefingSegment is one consecutive run of a briefing: a section that may give
// way, or fixed text that may not.
type briefingSegment struct {
	text    string
	heading string
	kind    string
	path    string
	rank    int
	// fitted is what replaces the section once it has given way.
	fitted *FittedSection
	order  int
}

// SectionSize is one section of a briefing as it stands, for reporting what a
// briefing is made of.
type SectionSize struct {
	Heading string
	// Kind is the section's entry in GiveWayOrder, empty for one that never gives
	// way.
	Kind  string
	Bytes int
}

// BriefingSections lists a briefing's sections in the order they appear, with
// the kind each gives way as.
func BriefingSections(text string) []SectionSize {
	segments := splitBriefing(text)
	sizes := make([]SectionSize, 0, len(segments))
	for _, segment := range segments {
		heading := strings.TrimSuffix(segment.heading, "\n")
		if heading == "" {
			heading = strings.TrimSpace(firstLine(segment.text))
		}
		sizes = append(sizes, SectionSize{Heading: heading, Kind: segment.kind, Bytes: len(segment.text)})
	}
	return sizes
}

// FitProductContext shortens an assembled product context to at most target
// bytes, by letting sections give way in GiveWayOrder. A section that has to
// lose only part of itself to fit keeps its opening; one that would keep less
// than minShortenedBytes is left out whole. Each one keeps its heading, says in
// place what became of it, and is listed under FittedHeading with how to read it
// in full. A context already within the target comes back unchanged.
func FitProductContext(text string, target int) Fit {
	if len(text) <= target {
		return Fit{Text: text, Fits: true}
	}
	segments := splitBriefing(text)
	candidates := make([]int, 0, len(segments))
	for index, segment := range segments {
		if segment.kind != "" {
			candidates = append(candidates, index)
		}
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		first, second := segments[candidates[a]], segments[candidates[b]]
		if first.rank != second.rank {
			return first.rank < second.rank
		}
		return len(first.text) > len(second.text)
	})
	given := 0
	fit := Fit{Text: text}
	for _, index := range candidates {
		excess := len(fit.Text) - target
		if excess <= 0 {
			break
		}
		segment := &segments[index]
		// Charged at its longest, so a section that keeps part of itself always
		// saves what it was cut for.
		overhead := len(shortenedNote(len(segment.text), len(segment.text))) +
			len(renderFittedEntry(FittedSection{Kind: segment.kind, Path: segment.path, Bytes: len(segment.text), KeptBytes: len(segment.text)}))
		if given == 0 {
			overhead += len(renderFittedList(nil))
		}
		keep := len(segment.text) - len(segment.heading) - excess - overhead
		if keep < minShortenedBytes {
			keep = 0
		}
		given++
		segment.order = given
		segment.fitted = &FittedSection{Kind: segment.kind, Path: segment.path, Bytes: len(segment.text)}
		segment.text = renderGivenWay(*segment, keep)
		fit.Text = joinBriefing(segments)
	}
	fit.Sections = fittedSections(segments)
	fit.Fits = len(fit.Text) <= target
	return fit
}

// splitBriefing cuts a briefing into segments at the headings SectionHeading
// recognizes. Text before the first heading, and every section not in
// GiveWayOrder, is fixed.
func splitBriefing(text string) []briefingSegment {
	var segments []briefingSegment
	start := 0
	heading := ""
	flush := func(end int) {
		if end > start {
			segments = append(segments, classify(text[start:end], heading)...)
		}
	}
	for offset := 0; offset < len(text); {
		next := strings.IndexByte(text[offset:], '\n')
		lineEnd := len(text)
		if next >= 0 {
			lineEnd = offset + next
		}
		line := text[offset:lineEnd]
		if SectionHeading(line) {
			flush(offset)
			start, heading = offset, line
		}
		if next < 0 {
			break
		}
		offset = lineEnd + 1
	}
	flush(len(text))
	return segments
}

// classify says what one heading-delimited run of a briefing gives way as, and
// keeps any trailing note AssembleProduct wrote under it apart from it.
func classify(section, heading string) []briefingSegment {
	kind, path := sectionKind(heading)
	if kind == IntentHeading || kind == "Directory index" {
		if standingGoalsHeading.MatchString(section) {
			kind = ""
		}
	}
	if kind == "" {
		return []briefingSegment{{text: section, heading: heading}}
	}
	cut := len(section)
	for _, trailer := range briefingTrailers {
		if at := strings.Index(section, trailer); at > len(heading) && at < cut {
			cut = at
		}
	}
	segments := []briefingSegment{{text: section[:cut], heading: heading + "\n", kind: kind, path: path, rank: giveWayRank(kind)}}
	if !strings.HasPrefix(section, heading+"\n") {
		segments[0].heading = heading
	}
	if cut < len(section) {
		segments = append(segments, briefingSegment{text: section[cut:]})
	}
	return segments
}

// sectionKind reads which GiveWayOrder kind a heading opens, and the document
// it names.
func sectionKind(heading string) (kind, path string) {
	if heading == "### Command help" {
		return "Command help", ""
	}
	if rest, ok := strings.CutPrefix(heading, "### Shipped documentation: "); ok {
		return "Shipped documentation", rest
	}
	rest, ok := strings.CutPrefix(heading, "## ")
	if !ok {
		return "", ""
	}
	label, path, found := strings.Cut(rest, ": ")
	if !found || giveWayRank(label) < 0 || label == "Shipped documentation" || label == "Command help" {
		return "", ""
	}
	return label, path
}

func giveWayRank(kind string) int {
	for index, candidate := range GiveWayOrder {
		if candidate == kind {
			return index
		}
	}
	return -1
}

// renderGivenWay is a section after it has given way: its heading, the first
// keep bytes of its text cut back to a line, and a note saying what is missing.
func renderGivenWay(segment briefingSegment, keep int) string {
	body := strings.TrimPrefix(segment.text, segment.heading)
	var kept string
	if keep > 0 && keep < len(body) {
		if cut := strings.LastIndexByte(body[:keep], '\n'); cut > 0 {
			kept = body[:cut+1]
		}
	}
	segment.fitted.KeptBytes = len(kept)
	if kept == "" {
		return segment.heading + leftOutNote(segment.fitted.Bytes)
	}
	return segment.heading + kept + shortenedNote(len(kept), segment.fitted.Bytes)
}

func leftOutNote(bytes int) string {
	return fmt.Sprintf("\n[Left out of this briefing to fit the provider serving this turn: this section is %d bytes. The list under %q, at the end of the briefing, says how to read it.]\n",
		bytes, strings.TrimPrefix(FittedHeading, "## "))
}

func shortenedNote(kept, bytes int) string {
	return fmt.Sprintf("\n[Shortened to fit the provider serving this turn: only the first %d of this section's %d bytes are above. The list under %q, at the end of the briefing, says how to read it in full.]\n",
		kept, bytes, strings.TrimPrefix(FittedHeading, "## "))
}

func joinBriefing(segments []briefingSegment) string {
	var joined strings.Builder
	for _, segment := range segments {
		joined.WriteString(segment.text)
	}
	return joined.String() + renderFittedList(fittedSections(segments))
}

func fittedSections(segments []briefingSegment) []FittedSection {
	var given []briefingSegment
	for _, segment := range segments {
		if segment.fitted != nil {
			given = append(given, segment)
		}
	}
	sort.SliceStable(given, func(a, b int) bool { return given[a].order < given[b].order })
	sections := make([]FittedSection, 0, len(given))
	for _, segment := range given {
		sections = append(sections, *segment.fitted)
	}
	return sections
}

// renderFittedList is the account a role is given of what its briefing lost,
// with how to read each in full. Rendered for no sections, it is what the
// account costs before its entries.
func renderFittedList(sections []FittedSection) string {
	var rendered strings.Builder
	rendered.WriteString("\n" + FittedHeading + "\n\n")
	rendered.WriteString("This briefing did not fit beside your instructions and this turn's evidence in what the provider serving this turn accepts, so the harness shortened it. Your own instructions, the standing goals, the recorded intent section, the work items and docket, and this turn's evidence and message are carried whole. These sections gave way, in this order:\n\n")
	for _, section := range sections {
		rendered.WriteString(renderFittedEntry(section))
	}
	rendered.WriteString("\nEach document named here is in the repository at the path given. To read one, name that path in a repository read, where this conversation has one: the harness returns the file, and a file longer than one read returns comes back cut with the cut said, so ask for what you need rather than reading a long document through. The command help is what the product's commands print when asked for help, and nothing here can show it to you. Until you have read what you need, say that you have not read it rather than reasoning from what you would expect it to say.\n")
	return rendered.String()
}

func renderFittedEntry(section FittedSection) string {
	name := "the command help"
	if section.Path != "" {
		name = section.Path
	}
	what := "left out"
	if section.KeptBytes > 0 {
		what = fmt.Sprintf("shortened to its first %d bytes", section.KeptBytes)
	}
	return fmt.Sprintf("- %s (%s, %d bytes): %s\n", name, strings.ToLower(section.Kind), section.Bytes, what)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimLeft(text, "\n"), "\n")
	return line
}
