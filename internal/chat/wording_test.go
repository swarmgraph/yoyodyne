package chat

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestADecodedLaneReportAndDigestCarryCorrectionsToTheirPass(t *testing.T) {
	t.Parallel()
	session := &Session{options: Options{Repository: "../.."}}
	parsed := parsedReply{
		Prose:      "post-mortem: the idle-bound ended the run",
		LaneReport: &runstate.LaneReportContent{Summary: "the posture changed", Remaining: []string{"the cadence"}, Blockers: []runstate.LaneReportBlocker{{What: "an environmental stop"}}},
		Reports:    []report.Entry{{Message: "digest: a stall continuation"}},
	}
	found := session.replyWording(parsed)
	if len(found) != 5 {
		t.Fatalf("replyWording() = %+v, want all five words from the decoded fields", found)
	}
	if parsed.LaneReport.Summary != "the posture changed" || parsed.Reports[0].Message != "digest: a stall continuation" {
		t.Fatal("checking changed the role's record")
	}
}

func TestConversationPostMortemsAreFlaggedWhetherStreamedOrHeld(t *testing.T) {
	t.Parallel()
	answer := "Post-mortem: the idle\nbound ended the run."
	for _, open := range []func(io.Reader, io.Writer) console.Console{
		testConsole,
		func(in io.Reader, out io.Writer) console.Console { return dressed(in, out) },
	} {
		options := testOptions(t, &replyingBackend{fragments: []string{answer}, reply: answer})
		options.Repository = "../.."
		session := openTestSession(t, options)
		var out strings.Builder
		if err := session.Converse(context.Background(), open(strings.NewReader("what happened?\n/exit\n"), &out)); err != nil {
			t.Fatal(err)
		}
		text := escapes.ReplaceAllString(out.String(), "")
		if !strings.Contains(text, answer) || strings.Count(text, `[wording: "idle bound" was replaced`) != 1 {
			t.Fatalf("conversation = %q, want the wrapped word flagged once beside the post-mortem", text)
		}
	}
}

func TestStreamingFlagsTheWordsShownBeforeATurnIsCutOff(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	stream := newReplyStream(&out, dressedTheme(), readmodel.ReadTextTerms("../.."))
	stream.write("the posture changed\n")
	stream.cutOff()
	stream.end()
	if text := out.String(); strings.Count(text, `[wording: "posture" was replaced`) != 1 || !strings.Contains(text, replyCutOff) {
		t.Fatalf("cut-off reply = %q, want the correction beside the partial reply", text)
	}
}

func TestTheConversationReportsListingFlagsADigestsWords(t *testing.T) {
	t.Parallel()
	digest := report.Report{ID: "report-one", Role: "program-manager", Message: "digest: the posture changed"}
	text := renderCollectedReports(console.NewTheme(func(string) string { return "" }, nil), []report.Report{digest}, nil, nil, nil, nil, fixedClock{}.Now(), readmodel.ReadTextTerms("../.."))
	if !strings.Contains(text, `[wording: "posture" was replaced; write tool access`) {
		t.Fatalf("digest = %q, want the register's correction beside it", text)
	}
}

func TestAnInteractiveConversationFlagsADigestAsSoonAsItIsFiled(t *testing.T) {
	t.Parallel()
	const message = "digest: the posture changed"
	answer := reportReply("A digest was filed.", `{"severity":"note","message":"`+message+`"}`)
	for _, open := range []func(io.Reader, io.Writer) console.Console{
		testConsole,
		func(in io.Reader, out io.Writer) console.Console { return dressed(in, out) },
	} {
		collected := &fakeReports{}
		options := testOptions(t, &replyingBackend{fragments: []string{answer}, reply: answer})
		options.Repository, options.Reports = "../..", collected
		session := openTestSession(t, options)
		var out strings.Builder
		if err := session.Converse(context.Background(), open(strings.NewReader("what changed?\n/exit\n"), &out)); err != nil {
			t.Fatal(err)
		}
		text := escapes.ReplaceAllString(out.String(), "")
		if !strings.Contains(text, message) || strings.Count(text, `[wording: "posture" was replaced; write tool access`) != 1 {
			t.Fatalf("immediate report output = %q, want the report and its replacement flag", text)
		}
		if len(collected.appended) != 1 || collected.appended[0].Message != message {
			t.Fatalf("stored report = %+v, want the author's unchanged message", collected.appended)
		}
	}
}
