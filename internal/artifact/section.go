package artifact

// Replacing one section of a document, named by its heading, and leaving every
// other byte of the document as it was.
//
// A revision otherwise carries the document whole, and a role reads at most
// the first 48 KiB of a file, so a design longer than that could only be
// revised by cutting it short. A revision naming a section carries that
// section's new text alone; the harness reads the rest from the document as the
// checkout holds it, and the candidate's Before digest binds the result to the
// exact file it was spliced into, so a document changed underneath is caught
// the way any other revision's is.

import (
	"errors"
	"fmt"
	"strings"
)

// MaxSectionHeadingBytes bounds the heading a revision names.
const MaxSectionHeadingBytes = MaxTitleBytes

// ReplaceSection returns body with the text under the heading named replaced
// by replacement, and nothing else changed. The heading line itself stays; the
// section runs to the next heading at the same level or above, so its own
// subsections are part of what is replaced. Headings inside fenced code are not
// headings.
//
// The heading is named by its text, with or without its leading '#' marks. A
// heading the document does not have, and one it has more than once, are
// refused, because either way there is no one section to replace. So is a
// replacement carrying a heading at the section's level or above: it would end
// the section partway through and change the document's shape, which is a whole
// revision rather than this.
func ReplaceSection(body, heading, replacement string) (string, error) {
	wanted := headingText(strings.TrimSpace(heading))
	if wanted == "" {
		return "", errors.New("section names no heading")
	}
	trimmed := strings.TrimSpace(replacement)
	if trimmed == "" {
		return "", fmt.Errorf("section %q would be left empty; say what belongs under it, or revise the whole document to remove it", wanted)
	}
	headings := documentHeadings(body)
	match := -1
	for index, found := range headings {
		if found.text != wanted {
			continue
		}
		if match >= 0 {
			return "", fmt.Errorf("the document has more than one heading %q, so the section to replace is not one section; revise the whole document instead", wanted)
		}
		match = index
	}
	if match < 0 {
		return "", fmt.Errorf("the document has no heading %q; name a heading exactly as the document writes it", wanted)
	}
	section := headings[match]
	for _, inner := range documentHeadings(trimmed) {
		if inner.level <= section.level {
			return "", fmt.Errorf("the replacement for section %q carries the heading %q at its level or above, which would end the section partway through; the text replaces what is under the heading, its subsections included, and the heading itself stays", wanted, inner.text)
		}
	}
	end := len(body)
	for _, next := range headings[match+1:] {
		if next.level <= section.level {
			end = next.start
			break
		}
	}
	prefix := body[:section.end]
	if !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	content := "\n" + trimmed + "\n"
	if end < len(body) {
		content += "\n"
	}
	return prefix + content + body[end:], nil
}

// documentHeading is one ATX heading: its level, its text, and the byte range
// of its line, the line ending included.
type documentHeading struct {
	level      int
	text       string
	start, end int
}

// documentHeadings lists a document's ATX headings outside fenced code, in
// order.
func documentHeadings(body string) []documentHeading {
	var headings []documentHeading
	fence := ""
	for start := 0; start < len(body); {
		end := strings.IndexByte(body[start:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += start + 1
		}
		line := strings.TrimRight(body[start:end], "\r\n")
		indented := strings.TrimLeft(line, " ")
		if len(line)-len(indented) <= 3 {
			if marker := fenceMarker(indented); marker != "" {
				switch {
				case fence == "":
					fence = marker
				case strings.HasPrefix(marker, fence[:1]) && len(marker) >= len(fence) && strings.TrimSpace(indented[len(marker):]) == "":
					fence = ""
				}
			} else if fence == "" {
				if level, text, ok := atxHeading(indented); ok {
					headings = append(headings, documentHeading{level: level, text: text, start: start, end: end})
				}
			}
		}
		start = end
	}
	return headings
}

// fenceMarker is the run of backticks or tildes a code fence line opens with,
// or empty where the line is not one.
func fenceMarker(line string) string {
	for _, mark := range []byte{'`', '~'} {
		count := 0
		for count < len(line) && line[count] == mark {
			count++
		}
		if count >= 3 {
			return line[:count]
		}
	}
	return ""
}

// atxHeading reads one '#' heading line.
func atxHeading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || (level < len(line) && line[level] != ' ' && line[level] != '\t') {
		return 0, "", false
	}
	return level, headingText(line), true
}

// headingText is a heading's words without its opening and closing '#' marks.
func headingText(line string) string {
	text := strings.TrimSpace(strings.TrimLeft(line, "#"))
	if closing := strings.TrimRight(text, "#"); closing != text && (closing == "" || strings.HasSuffix(closing, " ") || strings.HasSuffix(closing, "\t")) {
		text = strings.TrimSpace(closing)
	}
	return text
}
