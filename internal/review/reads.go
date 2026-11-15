package review

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// readTally is what a reviewer tried to read of the repository during one
// invocation, and how much of it came back, read off the events its provider
// reported. Both adapters report a tool call as command.started and its result
// as command.completed, saying whether it was refused or failed, so this is the
// same count on either.
//
// A completed call carries content_bytes, the size of what it returned, where
// the provider's stream reported its output. Claude Code always does; Codex
// does when the event carries the command's output, and an MCP call's end or
// an item that leaves its output out does not. A read with no size is counted
// as unmeasured, never as empty: whether a read was answered is decided by
// whether it ran, never by its size, so a missing figure cannot refuse a
// review (unread).
//
// A tool call written out as text in the reviewer's reply is counted too. It
// runs nothing and returns nothing, and it is what a reviewer with no way to
// read does in place of reading, before reporting that every read returned
// nothing (docs/diagnoses/yoyodyne-ifd-428-89-reviewer-silences.md).
type readTally struct {
	attempted int
	answered  int
	// unmeasured is how many of the answered reads came back with no size.
	unmeasured    int
	writtenAsText int
}

// writtenToolCallMarkers are how a tool call reads when a model writes it as
// text rather than making it: the opening of an invocation block, as written
// and as escaped by anything that rendered it. Only a line that opens with one,
// outside a fenced code block, is counted (writtenToolCalls), so a reviewer
// quoting code that mentions the markers is not taken to have tried a read.
var writtenToolCallMarkers = []string{"<invoke name=", "&lt;invoke name=", "<function_calls>", "&lt;function_calls&gt;"}

func (t *readTally) observe(event execution.Event) {
	switch event.Type {
	case execution.EventCommandStarted:
		t.attempted++
	case execution.EventCommandCompleted:
		// A call that ran is an answer even when what it printed was nothing:
		// `git status --short` on a clean worktree, or a search with no match,
		// has told the reviewer something true about the repository. A call that
		// was refused or failed told it nothing.
		var result struct {
			IsError      bool `json:"is_error"`
			ContentBytes *int `json:"content_bytes"`
		}
		if json.Unmarshal(event.Payload, &result) == nil && !result.IsError {
			t.answered++
			if result.ContentBytes == nil {
				t.unmeasured++
			}
		}
	case execution.EventAgentMessage:
		var message struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(event.Payload, &message) != nil {
			return
		}
		t.writtenAsText += writtenToolCalls(message.Text)
	}
}

// writtenToolCalls counts the lines of text that open with a tool call written
// out as text, leaving out every line inside a fenced code block.
func writtenToolCalls(text string) int {
	count := 0
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, marker := range writtenToolCallMarkers {
			if strings.HasPrefix(line, marker) {
				count++
				break
			}
		}
	}
	return count
}

// unread reports a reviewer that tried to read and had no read answered: every
// call it made was refused or failed, or it made none and wrote its calls out
// as text. A reviewer that tried nothing judged from its evidence by choice,
// which the contract permits; one whose reads all came back empty judged from
// it because it had no other choice, and said nothing about the callers, tests,
// and documentation it was asked to check.
//
// It reads no sizes, so a read whose size its provider did not report counts
// exactly as one that reported it.
func (t readTally) unread() bool {
	return t.attempted+t.writtenAsText > 0 && t.answered == 0
}

// record writes what the reviewer read into a review's record: how many reads
// it tried and had answered, and how many of those answers came back with no
// size, so a reader can tell a review whose reads were not measured from one
// whose reads were.
func (t readTally) record(fields map[string]any) {
	fields["attempted_reads"] = t.attempted
	fields["answered_reads"] = t.answered
	fields["unmeasured_reads"] = t.unmeasured
}

// UnreadRepositoryError reports a review whose reads of the repository all
// came back empty. The reviewer answered, and may well be right, but nothing
// it could not have been handed reached it, so its verdict is not recorded as
// an independently judged review. The caller asks once more, in the reviewer's
// own session, and a second review like it ends the run, which hands the change
// to the development manager with this as its cause.
type UnreadRepositoryError struct {
	// Attempted is how many tool calls the reviewer made, every one of which was
	// refused or failed.
	Attempted int
	// WrittenAsText is how many tool calls the reviewer wrote into its reply as
	// text, which runs nothing.
	WrittenAsText int
}

func (e UnreadRepositoryError) Error() string {
	var tried []string
	if e.Attempted > 0 {
		tried = append(tried, fmt.Sprintf("%d read(s) that were refused or failed", e.Attempted))
	}
	if e.WrittenAsText > 0 {
		tried = append(tried, fmt.Sprintf("%d tool call(s) written into its reply as text, which run nothing", e.WrittenAsText))
	}
	return "every read the reviewer made of the repository came back empty (" + strings.Join(tried, " and ") +
		"), so it judged only from what it was handed and its verdict is not an independently judged review. " +
		"Read again with the tools your session lists and only the commands it says it allows, then give the verdict"
}
