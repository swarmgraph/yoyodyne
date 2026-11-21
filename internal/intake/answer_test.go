package intake

import (
	"strings"
	"testing"
	"time"
)

func TestAnAnswerIsReadByTheFieldNamesTheQueryDeclares(t *testing.T) {
	query := testQuery()
	messages, more, problem := readAnswer(`{"messages":[{"id":17,"user":"U-listed","text":"broken","ts":"1763542800.000100","permalink":"https://x.test/1"},{"id":"b","user":"U2","text":null,"ts":"2026-11-19T09:00:00Z"}],"has_more":true}`, query)
	if problem != "" || !more || len(messages) != 2 {
		t.Fatalf("messages %+v, more %v, problem %q", messages, more, problem)
	}
	if first := messages[0]; first.ID != "17" || first.Text != "broken" || first.Link != "https://x.test/1" || first.SentAt.Unix() != 1763542800 {
		t.Fatalf("first = %+v", first)
	}
	if second := messages[1]; second.Text != "" || !second.SentAt.Equal(time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("second = %+v", second)
	}
	// Structured content is preferred to the text beside it.
	if messages, _, problem := readAnswer("a caption\nstructured content: {\"messages\":[]}", query); problem != "" || len(messages) != 0 {
		t.Fatalf("structured content: %v, %q", messages, problem)
	}
	if messages, more, problem := readAnswer(`{"messages":null}`, query); problem != "" || more || len(messages) != 0 {
		t.Fatalf("an empty answer: %v, %v, %q", messages, more, problem)
	}
}

func TestAnAnswerOfTheWrongShapeSaysWhichFieldAndNothingAMessageSaid(t *testing.T) {
	query := testQuery()
	for answer, want := range map[string]string{
		`not json at all`:                  "not one JSON object",
		`{"results":[]}`:                   `no "messages" field`,
		`{"messages":{}}`:                  "is not a list",
		`{"messages":[],"has_more":"yes"}`: "more_field",
		`{"messages":["secret words"]}`:    "is not an object",
		`{"messages":[{"user":"U","text":"secret words","ts":"1"}]}`:                `no "id" field`,
		`{"messages":[{"id":"a","text":"secret words","ts":"1"}]}`:                  `no "user" field`,
		`{"messages":[{"id":"a","user":"U","ts":"1"}]}`:                             `no "text" field`,
		`{"messages":[{"id":"a","user":"U","text":"secret words"}]}`:                `no "ts" field`,
		`{"messages":[{"id":"a","user":"U","text":"secret words","ts":"Tuesday"}]}`: "not a time",
		`{"messages":[{"id":"a","user":"U","text":7,"ts":"1"}]}`:                    "not text",
	} {
		_, _, problem := readAnswer(answer, query)
		if !strings.Contains(problem, want) {
			t.Errorf("%s: problem %q, want it to say %q", answer, problem, want)
		}
		if strings.Contains(problem, "secret") || strings.Contains(problem, "Tuesday") {
			t.Errorf("%s: the problem repeats what the message said: %q", answer, problem)
		}
	}
}
