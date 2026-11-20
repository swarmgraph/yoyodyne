package intake

// Reading a query's answer (docs/designs/scheduled-message-intake.md, section
// 1). The configuration says, by plain field names, where the messages are and
// where each of their parts is. No expression language is read and no service
// is known by name: the answer is one JSON object, items names its field that
// holds the messages, and each message is an object whose fields the query's
// fields name.
//
// An answer that does not hold that shape is a failed run, never a partial
// read: a message that could not be read might be one of many, and marking
// its window as read would hide them. So every problem here names the field
// that was wrong and nothing a message said.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// sourceMessage is one message as an answer held it.
type sourceMessage struct {
	ID, Sender, Text, Link string
	SentAt                 time.Time
}

// structuredPrefix is how the call path renders a result's structured content
// after its text (mcpcall's render).
const structuredPrefix = "structured content: "

// readAnswer finds the messages in what a call returned, and whether the
// source said it held more back. problem is set, and nothing else, when the
// answer does not have the shape the query declares.
func readAnswer(result string, query config.IntakeQuery) (messages []sourceMessage, more bool, problem string) {
	object, problem := answerObject(result)
	if problem != "" {
		return nil, false, problem
	}
	if query.MoreField != "" {
		if raw, ok := object[query.MoreField]; ok && !isNull(raw) {
			var flag bool
			if err := json.Unmarshal(raw, &flag); err != nil {
				return nil, false, fmt.Sprintf("the answer's %q field, which more_field names, is not true or false", query.MoreField)
			}
			more = flag
		}
	}
	raw, ok := object[query.Items]
	if !ok {
		return nil, false, fmt.Sprintf("the answer has no %q field, which items names as holding the messages", query.Items)
	}
	if isNull(raw) {
		return nil, more, ""
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, false, fmt.Sprintf("the answer's %q field, which items names, is not a list", query.Items)
	}
	for index, item := range items {
		message, problem := readMessage(item, query.Fields)
		if problem != "" {
			return nil, false, fmt.Sprintf("message %d of the answer's %q list %s", index+1, query.Items, problem)
		}
		messages = append(messages, message)
	}
	return messages, more, ""
}

// answerObject is the one JSON object an answer holds: its structured content
// where it has one, and otherwise its text.
func answerObject(result string) (map[string]json.RawMessage, string) {
	var candidates []string
	if before, after, found := strings.Cut(result, structuredPrefix); found && (before == "" || strings.HasSuffix(before, "\n")) {
		candidates = append(candidates, after, strings.TrimSuffix(before, "\n"))
	}
	candidates = append(candidates, result)
	for _, candidate := range candidates {
		var object map[string]json.RawMessage
		if err := decode([]byte(candidate), &object); err == nil && object != nil {
			return object, ""
		}
	}
	return nil, "the answer is not one JSON object"
}

func readMessage(item json.RawMessage, fields config.IntakeFields) (sourceMessage, string) {
	var object map[string]json.RawMessage
	if err := decode(item, &object); err != nil || object == nil {
		return sourceMessage{}, "is not an object"
	}
	var message sourceMessage
	var problem string
	if message.ID, problem = identifier(object, fields.ID, "id"); problem != "" {
		return sourceMessage{}, problem
	}
	if message.Sender, problem = identifier(object, fields.Sender, "sender"); problem != "" {
		return sourceMessage{}, problem
	}
	raw, ok := object[fields.Text]
	if !ok {
		return sourceMessage{}, fmt.Sprintf("has no %q field, which fields.text names", fields.Text)
	}
	if !isNull(raw) {
		if err := json.Unmarshal(raw, &message.Text); err != nil {
			return sourceMessage{}, fmt.Sprintf("has a %q field, which fields.text names, that is not text", fields.Text)
		}
	}
	if message.SentAt, problem = sentAt(object, fields.SentAt); problem != "" {
		return sourceMessage{}, problem
	}
	if fields.Link != "" {
		if raw, ok := object[fields.Link]; ok && !isNull(raw) {
			if err := json.Unmarshal(raw, &message.Link); err != nil {
				return sourceMessage{}, fmt.Sprintf("has a %q field, which fields.link names, that is not text", fields.Link)
			}
		}
	}
	return message, ""
}

// identifier is a field holding a non-empty string or a number, as text.
func identifier(object map[string]json.RawMessage, field, part string) (string, string) {
	raw, ok := object[field]
	if !ok || isNull(raw) {
		return "", fmt.Sprintf("has no %q field, which fields.%s names", field, part)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return "", fmt.Sprintf("has an empty %q field, which fields.%s names", field, part)
		}
		return text, ""
	}
	var number json.Number
	if err := decode(raw, &number); err == nil {
		return number.String(), ""
	}
	return "", fmt.Sprintf("has a %q field, which fields.%s names, that is neither text nor a number", field, part)
}

// sentAt reads when a message was sent: a time written in RFC 3339, or a count
// of seconds since 1970, written as a number or as text, with or without a
// fraction, which is how several services write their message times.
func sentAt(object map[string]json.RawMessage, field string) (time.Time, string) {
	raw, ok := object[field]
	if !ok || isNull(raw) {
		return time.Time{}, fmt.Sprintf("has no %q field, which fields.sent_at names", field)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		var number json.Number
		if err := decode(raw, &number); err != nil {
			return time.Time{}, fmt.Sprintf("has a %q field, which fields.sent_at names, that is not a time", field)
		}
		text = number.String()
	}
	text = strings.TrimSpace(text)
	if when, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return when.UTC(), ""
	}
	if seconds, err := strconv.ParseFloat(text, 64); err == nil && seconds >= 0 && seconds < math.MaxInt32*4 {
		whole, fraction := math.Modf(seconds)
		return time.Unix(int64(whole), int64(fraction*1e9)).UTC(), ""
	}
	return time.Time{}, fmt.Sprintf("has a %q field, which fields.sent_at names, that is not a time written in RFC 3339 or as seconds since 1970", field)
}

func decode(data []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(data)))
	decoder.UseNumber()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if decoder.More() {
		return fmt.Errorf("more than one JSON value")
	}
	return nil
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }
