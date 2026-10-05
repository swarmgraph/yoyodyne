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
	kept := []string{lines[0]}
	keeping := false
	bulk := false
	for _, line := range lines[1:] {
		keepLine := keeping
		if label, _, field := strings.Cut(line, ":"); field {
			switch label {
			case "Run", "Phase", "Repair attempts", "Failing check", "Waiting out", "Asks again by":
				keepLine = !bulk
				keeping = false
			case "Failure", "Reason", "Round", "Directive", "Refused paths", "Integration stop", "Replay conflict":
				// Matching stop fields inside captured output or a diff are data,
				// not a second account of which run stopped or why.
				keeping = !bulk
				keepLine = keeping
			case "Reviewed against", "Review decision", "Approved as":
				keepLine = true
				keeping = false
			case "Review summary":
				keepLine = true
				keeping = true
			case "Captured output":
				return strings.TrimSpace(strings.Join(kept, "\n"))
			case "Changes when the run ended", "Diff stat when the run ended", "Check stage", "Check":
				bulk = true
				keepLine = false
				keeping = false
			case "Branch", "Worktree", "Base commit", "Claude session", "Developer model", "Developer effort",
				"Reviewer session", "Reviewer model", "Reviewer effort", "Invariants delivered",
				"Pull request", "Pull request merged", "Protected by this project", "Granted by this work item", "Role definitions",
				"Lands on", "Commit to land", "Target commit it was prepared on", "Integrated into", "Integrated commit", "Previous target commit":
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
