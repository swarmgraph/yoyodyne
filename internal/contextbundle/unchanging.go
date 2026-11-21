package contextbundle

import "strings"

// What a recurring turn's briefing opens with, and what follows it.
//
// A provider reuses what it cached of an earlier request only up to the end of
// a whole block of the request, so text that is the same from one recurring
// turn to the next is read back from the cache only when a block ends where
// that text ends. SplitUnchanging cuts a briefing at that point: the product
// context's opening, the recorded-intent section, and the product intent
// documents and directory indexes carried whole, and nothing after the first
// section that is anything else. What it returns first carries no time, count,
// commit or listing that moves when work lands, so two turns given the same
// product intent get it byte for byte the same; every section that does move —
// a role's own documents left out with their sizes, the shipped surface, the
// work items, the docket, and the lists of what was left out — is in the rest.

// unchangingHeadings are the fixed sections AssembleProduct opens a briefing
// with that do not move when work lands.
var unchangingHeadings = map[string]bool{
	"# Product context":          true,
	"## Recorded product intent": true,
}

// SplitUnchanging returns the opening of a briefing that does not change while
// the product intent stays the same, and the rest. The two joined are the
// briefing exactly. A briefing that does not open with the product context's
// header comes back whole as the rest.
func SplitUnchanging(text string) (unchanging, rest string) {
	segments := splitBriefing(text)
	if len(segments) == 0 || strings.TrimSuffix(segments[0].heading, "\n") != "# Product context" {
		return "", text
	}
	end := 0
	for _, segment := range segments {
		if !unchangingSegment(segment) {
			break
		}
		end += len(segment.text)
	}
	return text[:end], text[end:]
}

// unchangingSegment reports whether one segment of a briefing is one of the
// sections SplitUnchanging keeps in the opening. A document carrying the
// standing goals is classified as fixed so it never gives way, so the heading
// is read again here rather than the segment's kind.
func unchangingSegment(segment briefingSegment) bool {
	heading := strings.TrimSuffix(segment.heading, "\n")
	if heading == "" {
		return false
	}
	if unchangingHeadings[heading] {
		return true
	}
	label, _ := sectionKind(heading)
	return label == IntentHeading || label == "Directory index"
}
