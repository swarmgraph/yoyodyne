package ownership

import "strings"

// ShortReasonLists returns every paragraph of text that lists the operator's
// reasons and leaves some of them out. It exists for the tests that hold the
// role contracts and the operator's guides to OperatorReasons: a contract or a
// guide that teaches part of the list teaches a reader that the rest are
// refused.
//
// A paragraph is text between blank lines, so a Markdown list and the sentence
// introducing it are one paragraph. A reason counts where it is written as the
// field takes it, in double quotes or backticks; the bare word "credential" in a
// sentence is not one. A paragraph naming two or more reasons is a list, and is
// returned unless it names all of them. The one exception is the person-only
// remedy, whose own closed list (PersonOnlyReason) is shorter by design: a
// paragraph that mentions person_only and names only reasons on that list is
// not a short list of the operator's reasons.
func ShortReasonLists(text string) []string {
	var short []string
	for _, paragraph := range paragraphs(text) {
		named := namedReasons(paragraph)
		if len(named) < 2 || len(named) == len(OperatorReasons()) {
			continue
		}
		if strings.Contains(paragraph, "person_only") && onlyPersonOnly(named) {
			continue
		}
		short = append(short, paragraph)
	}
	return short
}

func paragraphs(text string) []string {
	var out []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			out = append(out, strings.Join(current, "\n"))
			current = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return out
}

func namedReasons(paragraph string) []OperatorReason {
	var named []OperatorReason
	for _, reason := range OperatorReasons() {
		if strings.Contains(paragraph, `"`+string(reason)+`"`) || strings.Contains(paragraph, "`"+string(reason)+"`") {
			named = append(named, reason)
		}
	}
	return named
}

func onlyPersonOnly(named []OperatorReason) bool {
	for _, reason := range named {
		if _, ok := personOnlyReasons[PersonOnlyReason(reason)]; !ok {
			return false
		}
	}
	return true
}
