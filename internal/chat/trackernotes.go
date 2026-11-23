package chat

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// renderTrackerNotes keeps the continuous tail and, outside its byte bound,
// quotes the latest stop and each kind of recorded decision that the tail lost.
// A blocker can carry pages of check output after its reason; a later report
// mapping can follow a repair grant. Neither makes those records dispensable.
// These are extracts of notes, not evidence minted from their words: in
// particular, a follow-up asking about a continuation proves no execution.
// Appends with recorded boundaries are read atomically. Older prose without
// those boundaries is quoted conservatively and identified as ambiguous.
//
// The continuous tail starts at the first recorded append that fits whole in
// the budget, so the newest notes are returned whole rather than opening
// mid-sentence; only a newest append larger than the budget is itself cut. The
// excerpts are held to maxTrackerExcerptBytes, the newest kept and the oldest
// cut first, with a line saying how many and how many bytes were cut: an item
// with years of older notes would otherwise carry enough excerpts to push the
// newest notes past whatever bounds the read further on.
func renderTrackerNotes(notes string, budget int) string {
	parts := beads.NoteRecords(notes)
	var plain strings.Builder
	type record struct {
		start  int
		end    int
		kind   string
		framed bool
		review bool
	}
	var records []record
	legacy := false
	for _, part := range parts {
		start := plain.Len()
		plain.WriteString(part.Text)
		if part.Framed {
			line, _, _ := strings.Cut(part.Text, "\n")
			kind, _ := trackerNoteKind(line)
			records = append(records, record{start: start, end: plain.Len(), kind: kind, framed: true,
				review: strings.HasPrefix(line, "Yoyodyne ") && trackerHasReview(part.Text)})
			continue
		}
		// Old notes have no recorded append boundaries. Keep all possible stop
		// and decision excerpts, rather than allowing a later matching sentence
		// in output or quoted prose to replace the genuine one as "latest".
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		legacy = true
		offset := start
		first := len(records)
		for _, line := range strings.SplitAfter(part.Text, "\n") {
			if kind, boundary := trackerNoteKind(strings.TrimSuffix(line, "\n")); boundary {
				if len(records) > first {
					records[len(records)-1].end = offset
				}
				records = append(records, record{start: offset, end: plain.Len(), kind: kind})
			}
			offset += len(line)
		}
	}
	notes = plain.String()
	for index := range records {
		record := &records[index]
		if !record.framed {
			text := notes[record.start:record.end]
			record.review = strings.HasPrefix(text, "Yoyodyne ") && trackerHasReview(text)
		}
	}
	if len(notes) <= budget {
		return notes
	}
	cut := len(notes) - budget
	for cut < len(notes) && !utf8.RuneStart(notes[cut]) {
		cut++
	}
	for _, record := range records {
		if record.framed && record.start >= cut && strings.TrimSpace(notes[record.start:]) != "" {
			cut = record.start
			break
		}
	}
	latest := make(map[string]int)
	for index, record := range records {
		if record.framed && record.kind != "" {
			latest[record.kind] = index
		}
		if record.framed && record.review {
			latest["review"] = index
		}
	}
	var kept []int
	for index, record := range records {
		if !record.framed && (record.kind != "" || record.review) && record.start < cut {
			kept = append(kept, index)
		}
	}
	for _, index := range latest {
		if records[index].start < cut {
			kept = append(kept, index)
		}
	}
	slices.Sort(kept)
	kept = slices.Compact(kept)
	var rendered strings.Builder
	if len(kept) > 0 {
		if legacy {
			rendered.WriteString("Stop and decision excerpts from the cut notes (in note order):\n")
			rendered.WriteString("Older notes have no recorded append boundaries. Matching excerpts from that history are quoted within the read's limit, and where they do not all fit the oldest are cut first; matching text may be quoted or captured output, so their order alone does not establish separate writes or which decision was latest.\n")
		} else {
			rendered.WriteString("Latest stop and recorded decisions from the cut notes (verbatim extracts, in note order):\n")
		}
		seen := make(map[string]bool)
		var excerpts []string
		for _, index := range kept {
			record := records[index]
			text := strings.TrimSpace(notes[record.start:record.end])
			if record.kind == "stop" || record.review {
				text = trackerStopExcerpt(text)
			}
			if !record.framed && seen[text] {
				continue
			}
			seen[text] = true
			excerpts = append(excerpts, fmt.Sprintf("\n[notes bytes %d–%d]\n%s\n", record.start, record.end, text))
		}
		rendered.WriteString(boundTrackerExcerpts(excerpts))
		rendered.WriteString("\nOnly the latest framed record of each kind is extracted above; unframed history is quoted conservatively. Captured check output and other details may be omitted. These notes do not establish execution beyond what they explicitly record.\n\nContinuous end of notes:\n")
	}
	if cut > 0 {
		fmt.Fprintf(&rendered, "[the first %d bytes are cut; treat them as unread rather than absent]\n\n", cut)
	}
	rendered.WriteString(strings.TrimSpace(notes[cut:]))
	return rendered.String()
}

// maxTrackerExcerptBytes bounds the excerpts quoted from cut notes. With the
// item read's own bound it keeps a whole read well inside the 32 KiB the
// results carried into a role's next turn may hold (maxPendingResultBytes), so
// nothing after the excerpts, the newest notes least of all, is cut there.
const maxTrackerExcerptBytes = 12 << 10

// boundTrackerExcerpts keeps the newest excerpts that fit maxTrackerExcerptBytes,
// in note order, and says how many older ones were cut and how many bytes they
// held. A newest excerpt larger than the bound is kept, cut at its end.
func boundTrackerExcerpts(excerpts []string) string {
	from, used := len(excerpts), 0
	for from > 0 && used+len(excerpts[from-1]) <= maxTrackerExcerptBytes {
		from--
		used += len(excerpts[from])
	}
	kept := excerpts[from:]
	if from == len(excerpts) && from > 0 {
		from--
		kept = []string{boundText(excerpts[from], maxTrackerExcerptBytes) + "\n"}
	}
	var rendered strings.Builder
	if from > 0 {
		cutBytes := 0
		for _, excerpt := range excerpts[:from] {
			cutBytes += len(excerpt)
		}
		fmt.Fprintf(&rendered, "\n[the %d oldest excerpts, %d bytes, are cut to keep this read within its bound; treat them as unread rather than absent]\n", from, cutBytes)
	}
	for _, excerpt := range kept {
		rendered.WriteString(excerpt)
	}
	return rendered.String()
}

// Captured output is the final field of a check blocker. It cannot supply a
// review verdict; renderFailureNotes carries the review in its separate append.
func trackerHasReview(note string) bool {
	metadata, _, _ := strings.Cut(note, "\nCaptured output:")
	return strings.Contains(metadata, "\nReview decision:")
}

// trackerNoteKind classifies the opening sentence of an append. In older notes
// it identifies only possible records, since a matching sentence can be quoted.
// A report mapping or a recovery follow-up gets its own kind and cannot replace
// the triage decision or the harness's account of carrying it out.
func trackerNoteKind(line string) (kind string, boundary bool) {
	switch {
	case strings.HasPrefix(line, "Yoyodyne "):
		if strings.HasPrefix(line, "Yoyodyne stopped ") || strings.HasPrefix(line, "Yoyodyne blocked ") ||
			strings.HasPrefix(line, "Yoyodyne paused ") || strings.HasPrefix(line, "Yoyodyne parked ") ||
			strings.HasPrefix(line, "Yoyodyne bootstrap run failed") || strings.HasPrefix(line, "Yoyodyne run failed") ||
			strings.HasPrefix(line, "Yoyodyne run raised ") {
			return "stop", true
		}
		return "", true
	case strings.HasPrefix(line, "Triaged: the development manager's triage decided "),
		strings.HasPrefix(line, "Continued at its checks:"),
		strings.HasPrefix(line, "The harness did not continue "):
		return "continuation", true
	case strings.HasPrefix(line, "Triaged: the ") && strings.Contains(line, " cap crossed to "):
		cap, _, _ := strings.Cut(line, " cap crossed to ")
		return cap, true
	}
	for _, verb := range triageVerbs {
		if strings.HasPrefix(line, verb+", ") {
			return verb, true
		}
	}
	if strings.Contains(line, " by the ") && strings.Contains(line, " in conversation ") {
		// The first word is stable across values: a new priority, label, or report
		// identifier supersedes the previous note of that kind.
		verb, _, _ := strings.Cut(line, " ")
		_, by, _ := strings.Cut(line, " by the ")
		role, _, _ := strings.Cut(by, " in conversation ")
		return verb + " by the " + role, true
	}
	return "", false
}

// trackerStopExcerpt quotes the reason and the run it is about, keeping a
// multiline failure intact. It also keeps the review's verdict, approval scope,
// summary and findings, which the writer can put after bulky output and diffs.
func trackerStopExcerpt(note string) string {
	lines := strings.Split(note, "\n")
	// Captured output is the last field a check blocker writes, so nothing after
	// it is another field.
	for index, line := range lines[1:] {
		if strings.HasPrefix(line, "Captured output:") {
			lines = lines[:index+1]
			break
		}
	}
	opens := trackerStopFields(lines[1:])
	kept := []string{lines[0]}
	keeping := false
	for index, line := range lines[1:] {
		keepLine := keeping
		if opens[index] {
			label, _, _ := strings.Cut(line, ":")
			switch label {
			case "Run", "Phase", "Repair attempts", "Failing check", "Waiting out", "Asks again by",
				"Reviewed against", "Review decision", "Approved as":
				keepLine = true
				keeping = false
			case "Failure", "Reason", "Round", "Directive", "Refused paths", "Integration stop", "Replay conflict",
				"Review summary":
				keepLine = true
				keeping = true
			default:
				keepLine = false
				keeping = false
			}
		}
		if keepLine {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// The parts of a stop note, in the order every writer of one puts them
// (renderFailureNotes, renderOutcomeNotes, the blocker, pause and reconcile
// writers in internal/orchestrator): what stopped and where the work is, then
// the change and the checks, then the review, which renderReviewNotes always
// writes last. Within a part the order differs from writer to writer. Where a
// change landed or was published is written in different places by different
// writers, so those fields belong to no part and may sit anywhere.
const (
	stopAnywhere = iota - 1
	stopOpening
	stopChecks
	stopReview
)

var trackerStopFieldPart = map[string]int{
	"Run": stopOpening, "Failure": stopOpening, "Reason": stopOpening, "Repair attempts": stopOpening,
	"Phase": stopOpening, "Round": stopOpening, "Integration stop": stopOpening, "Replay conflict": stopOpening,
	"Waiting out": stopOpening, "Asks again by": stopOpening, "Directive": stopOpening,
	"Branch": stopOpening, "Worktree": stopOpening, "Base commit": stopOpening, "Failing check": stopOpening,
	"Refused paths": stopOpening, "Claude session": stopOpening, "Protected by this project": stopOpening,
	"Developer model": stopOpening, "Role definitions": stopOpening, "Developer effort": stopOpening,
	"Granted by this work item": stopOpening,

	"Changes when the run ended": stopChecks, "Diff stat when the run ended": stopChecks,
	"Check stage": stopChecks, "Check": stopChecks,

	"Invariants delivered": stopReview, "Reviewer session": stopReview, "Reviewer model": stopReview,
	"Reviewer effort": stopReview, "Reviewed against": stopReview, "Review decision": stopReview,
	"Approved as": stopReview, "Review summary": stopReview,

	"Lands on": stopAnywhere, "Commit to land": stopAnywhere, "Target commit it was prepared on": stopAnywhere,
	"Integrated into": stopAnywhere, "Integrated commit": stopAnywhere, "Previous target commit": stopAnywhere,
	"Pull request": stopAnywhere, "Pull request merged": stopAnywhere,
}

// trackerStopRepeatsTracked bounds how many field names that occur more than
// once in one note are told apart exactly; any further ones are read as if
// they occurred once. A note the writers made never repeats a field, so only a
// note whose free text quotes several field names reaches it.
const trackerStopRepeatsTracked = 6

// trackerStopFields says which lines of a stop note open a field. A failure, a
// reason, or a review summary is free text and can hold a line that begins with
// a field's name, so a name alone does not open a field. What does is where the
// line sits, in three ways the writers guarantee:
//
//   - A writer never goes back to an earlier part of the note
//     (trackerStopFieldPart), so a named line from an earlier part than the
//     field already open is text inside that field.
//   - A writer writes each field once, except a check, so a field's name after
//     that field is already open is text too.
//   - Only some fields run onto further lines (trackerStopFieldRunsOn). A named
//     line straight after a field that does not cannot be inside free text, so
//     it opens a field. One inside a field that does opens a field only where
//     reading it so lets more of the named lines after it open fields, under the
//     two rules above, than reading it as text would; where the two readings are
//     as good, it is text, because the field already open is what it most
//     plainly belongs to.
//
// A check line must also have the shape renderCheckNotes gives it.
func trackerStopFields(lines []string) []bool {
	type named struct {
		line  int
		label string
		part  int
		bit   int
	}
	var candidates []named
	occurrences := make(map[string]int)
	for index, line := range lines {
		label, value, field := strings.Cut(line, ":")
		part, known := trackerStopFieldPart[label]
		if !field || !known || (label == "Check" && !trackerCheckLine(value)) {
			continue
		}
		candidates = append(candidates, named{line: index, label: label, part: part})
		occurrences[label]++
	}
	// A field name that occurs more than once gets a bit, so the count below can
	// say which of its occurrences has already opened.
	bits := make(map[string]int)
	for index := range candidates {
		candidate := &candidates[index]
		bit, seen := bits[candidate.label]
		if !seen && candidate.label != "Check" && occurrences[candidate.label] > 1 && len(bits) < trackerStopRepeatsTracked {
			bit = 1 << len(bits)
			bits[candidate.label] = bit
		}
		candidate.bit = bit
	}
	states := (stopReview + 1) << len(bits)
	// most[i][state] is how many of candidates i onward can open fields, given
	// the part already reached and which repeated names have already opened.
	most := make([][]int32, len(candidates)+1)
	most[len(candidates)] = make([]int32, states)
	open := func(candidate named, part, mask int) (int, int, bool) {
		if (candidate.part != stopAnywhere && candidate.part < part) || mask&candidate.bit != 0 {
			return 0, 0, false
		}
		return max(part, candidate.part), mask | candidate.bit, true
	}
	state := func(part, mask int) int { return part<<len(bits) | mask }
	for index := len(candidates) - 1; index >= 0; index-- {
		most[index] = make([]int32, states)
		for part := stopOpening; part <= stopReview; part++ {
			for mask := 0; mask < 1<<len(bits); mask++ {
				best := most[index+1][state(part, mask)]
				if after, masked, ok := open(candidates[index], part, mask); ok {
					best = max(best, 1+most[index+1][state(after, masked)])
				}
				most[index][state(part, mask)] = best
			}
		}
	}
	opens := make([]bool, len(lines))
	opened := make(map[string]bool)
	part, mask, runsOn := stopOpening, 0, false
	for index, candidate := range candidates {
		after, masked, ok := open(candidate, part, mask)
		if !ok || (opened[candidate.label] && candidate.label != "Check") {
			continue
		}
		if runsOn && 1+most[index+1][state(after, masked)] <= most[index+1][state(part, mask)] {
			continue
		}
		opens[candidate.line] = true
		opened[candidate.label] = true
		part, mask = after, masked
		runsOn = trackerStopFieldRunsOn(lines[candidate.line])
	}
	return opens
}

// trackerStopFieldRunsOn reports whether a field can carry lines after its own:
// free text, the change and diff listings, and a failed check's output.
func trackerStopFieldRunsOn(line string) bool {
	label, value, _ := strings.Cut(line, ":")
	switch label {
	case "Failure", "Reason", "Round", "Directive", "Refused paths", "Integration stop", "Replay conflict", "Waiting out",
		"Review summary", "Changes when the run ended", "Diff stat when the run ended":
		return true
	case "Check":
		return strings.Contains(value, " (passed=false,")
	}
	return false
}

// trackerCheckLine reports whether a "Check:" value has the shape
// renderCheckNotes writes: the command with its result in parentheses at the
// end, or the command and why it could not run.
func trackerCheckLine(value string) bool {
	return strings.HasSuffix(value, ")") &&
		(strings.Contains(value, " (passed=") || strings.Contains(value, " could not run, so it judged nothing"))
}
