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
// as command.completed with the size of what it returned, so this is the same
// count on either.
//
// A tool call written out as text in the reviewer's reply is counted too. It
// runs nothing and returns nothing, and it is what a reviewer with no way to
// read does in place of reading, before reporting that every read returned
// nothing (docs/diagnoses/yoyodyne-ifd-428-89-reviewer-silences.md).
type readTally struct {
	attempted     int
	answered      int
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
		var result struct {
			IsError      bool `json:"is_error"`
			ContentBytes int  `json:"content_bytes"`
		}
		if json.Unmarshal(event.Payload, &result) == nil && !result.IsError && result.ContentBytes > 0 {
			t.answered++
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

// unread reports a reviewer that tried to read and was given nothing back by
// any read. A reviewer that tried nothing judged from its evidence by choice,
// which the contract permits; one whose reads all came back empty judged from
// it because it had no other choice, and said nothing about the callers, tests,
// and documentation it was asked to check.
func (t readTally) unread() bool {
	return t.attempted+t.writtenAsText > 0 && t.answered == 0
}

// UnreadRepositoryError reports a review whose reads of the repository all
// came back empty. The reviewer answered, and may well be right, but nothing
// it could not have been handed reached it, so its verdict is not recorded as
// an independently judged review. The caller asks once more, in the reviewer's
// own session, and a second review like it ends the run, which hands the change
// to the development manager with this as its cause.
type UnreadRepositoryError struct {
	// Attempted is how many tool calls the reviewer made, none of which returned
	// anything.
	Attempted int
	// WrittenAsText is how many tool calls the reviewer wrote into its reply as
	// text, which runs nothing.
	WrittenAsText int
}

func (e UnreadRepositoryError) Error() string {
	var tried []string
	if e.Attempted > 0 {
		tried = append(tried, fmt.Sprintf("%d read(s) that returned nothing", e.Attempted))
	}
	if e.WrittenAsText > 0 {
		tried = append(tried, fmt.Sprintf("%d tool call(s) written into its reply as text, which run nothing", e.WrittenAsText))
	}
	return "every read the reviewer made of the repository came back empty (" + strings.Join(tried, " and ") +
		"), so it judged only from what it was handed and its verdict is not an independently judged review"
}
