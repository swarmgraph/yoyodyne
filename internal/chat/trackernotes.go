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
			rendered.WriteString("Older notes have no recorded append boundaries. All matching excerpts from that history are retained; matching text may be quoted or captured output, so their order alone does not establish separate writes or which decision was latest.\n")
		} else {
			rendered.WriteString("Latest stop and recorded decisions from the cut notes (verbatim extracts, in note order):\n")
		}
		seen := make(map[string]bool)
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
			fmt.Fprintf(&rendered, "\n[notes bytes %d–%d]\n%s\n", record.start, record.end, text)
		}
		rendered.WriteString("\nOnly the latest framed record of each kind is extracted above; unframed history is quoted conservatively. Captured check output and other details may be omitted. These notes do not establish execution beyond what they explicitly record.\n\nContinuous end of notes:\n")
	}
	rendered.WriteString(boundTextTail(notes, budget))
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

// Where each field of a stop note sits, in the order every writer of one puts
// them (renderFailureNotes, the blocker and pause writers in
// internal/orchestrator): what stopped, then where the work is, then the change
// and the checks, then the review, then where the change landed or was
// published. The fields of the first group come in an order that differs from
// writer to writer, so they share a rank; every later field has its own. Fields
// that never appear in the same note can share a rank.
const stopHeadRank, stopCheckRank = 0, 10

var trackerStopFieldRank = map[string]int{
	"Run": stopHeadRank, "Failure": stopHeadRank, "Reason": stopHeadRank, "Repair attempts": stopHeadRank,
	"Phase": stopHeadRank, "Round": stopHeadRank, "Integration stop": stopHeadRank, "Replay conflict": stopHeadRank,
	"Waiting out": stopHeadRank, "Asks again by": stopHeadRank, "Directive": stopHeadRank,

	"Branch": 1, "Worktree": 2,
	"Base commit": 3, "Failing check": 3, "Refused paths": 3,
	"Claude session": 4, "Protected by this project": 4,
	"Developer model": 5, "Role definitions": 5,
	"Developer effort": 6, "Granted by this work item": 6,

	"Changes when the run ended": 7, "Diff stat when the run ended": 8, "Check stage": 9, "Check": stopCheckRank,

	"Invariants delivered": 11, "Reviewer session": 12, "Reviewer model": 13, "Reviewer effort": 14,
	"Reviewed against": 15, "Review decision": 16, "Approved as": 17, "Review summary": 18,

	"Lands on": 19, "Integrated into": 19, "Commit to land": 20, "Integrated commit": 20,
	"Target commit it was prepared on": 21, "Previous target commit": 21,
	"Pull request": 22, "Pull request merged": 23,
}

// trackerStopFields says which lines of a stop note open a field. A failure, a
// reason, or a review summary is free text and can hold a line that begins with
// a field's name, so a name alone does not open a field. What does is where the
// line sits, in three ways the writers guarantee:
//
//   - A writer never goes back to an earlier field (trackerStopFieldRank), so a
//     named line ranked before the field already open is text inside that field.
//   - A writer writes each field once, except a check, so a field's name after
//     that field is already open is text too.
//   - Only some fields run onto further lines (trackerStopFieldRunsOn). A named
//     line straight after a field that does not cannot be inside free text, so
//     it opens a field. One inside a field that does opens a field only where
//     reading it so lets more of the named lines after it open fields in order
//     than reading it as text would; where the two readings are as good, it is
//     text, because the field already open is what it most plainly belongs to.
//
// A check line must also have the shape renderCheckNotes gives it.
func trackerStopFields(lines []string) []bool {
	type named struct {
		line  int
		label string
		rank  int
	}
	var candidates []named
	for index, line := range lines {
		label, value, field := strings.Cut(line, ":")
		rank, known := trackerStopFieldRank[label]
		if !field || !known || (label == "Check" && !trackerCheckLine(value)) {
			continue
		}
		candidates = append(candidates, named{line: index, label: label, rank: rank})
	}
	// follows says whether a writer can put field b after field a.
	follows := func(a, b named) bool {
		if b.rank != a.rank {
			return b.rank > a.rank
		}
		return (a.rank == stopHeadRank && a.label != b.label) || a.label == "Check"
	}
	// longest[i] is how many named lines from candidate i onward can open fields
	// in order with candidate i opening the first.
	longest := make([]int, len(candidates))
	for index := len(candidates) - 1; index >= 0; index-- {
		longest[index] = 1
		for after := index + 1; after < len(candidates); after++ {
			if follows(candidates[index], candidates[after]) {
				longest[index] = max(longest[index], 1+longest[after])
			}
		}
	}
	opens := make([]bool, len(lines))
	opened := make(map[string]bool)
	var open *named
	runsOn := false
	canOpen := func(candidate named) bool {
		return (open == nil || follows(*open, candidate)) && (!opened[candidate.label] || candidate.label == "Check")
	}
	for index, candidate := range candidates {
		if !canOpen(candidate) {
			continue
		}
		if runsOn {
			rival := 0
			for after := index + 1; after < len(candidates); after++ {
				if canOpen(candidates[after]) {
					rival = max(rival, longest[after])
				}
			}
			if longest[index] <= rival {
				continue
			}
		}
		opens[candidate.line] = true
		opened[candidate.label] = true
		open = &candidates[index]
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
