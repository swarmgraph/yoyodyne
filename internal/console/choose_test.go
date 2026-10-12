package console

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// choosing is one Choose running in a goroutine, so a test can watch the list
// on screen while it is blocked reading keystrokes.
func choosing(ctx context.Context, console *terminal, prompt string, options []string, interrupt <-chan struct{}) prompted {
	answers := make(prompted, 1)
	go func() {
		answer, err := console.Choose(ctx, prompt, options, interrupt)
		answers <- struct {
			line string
			err  error
		}{answer, err}
	}()
	return answers
}

// testOptions are the answers a question of this shape actually carries: a few
// short lines, each one a whole answer.
var testOptions = []string{"Write the goal it serves", "Retire the work"}

// TestAnEnumerableQuestionIsAnsweredByMovingAMarker is the point of the whole
// thing: the operator answers by choosing rather than by composing a sentence,
// and what comes back is the answer they chose, in the words it was offered in.
func TestAnEnumerableQuestionIsAnsweredByMovingAMarker(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	answers := choosing(context.Background(), console, "answer c1.1? ", testOptions, nil)
	out.await(t, "the answers on offer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"1. Write the goal it serves")
	})
	// Every answer is numbered as well as marked, and the last of them is always
	// the operator's own words.
	listed := out.screen().text()
	for _, required := range []string{"answer c1.1?", "2. Retire the work", "3. " + FreeEntryChoice, choiceKeys} {
		if !strings.Contains(listed, required) {
			t.Fatalf("screen =\n%s\nwant it to contain %q", listed, required)
		}
	}

	keys.Write([]byte("\x1b[B"))
	out.await(t, "the marker on the second answer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"2. Retire the work")
	})
	keys.Write([]byte("\r"))
	if answer := answers.line(t); answer != "Retire the work" {
		t.Fatalf("answer = %q", answer)
	}
	// The list comes off the screen with the region it was drawn in, and what
	// was asked and what was answered join the transcript: an answer that
	// scrolls past still says what it was answering.
	if final := out.screen(); final.text() != "answer c1.1? Retire the work" {
		t.Fatalf("screen =\n%s", final.text())
	}
}

// TestTheLastAnswerIsAlwaysTheOperatorsOwnWords covers the operator whose
// answer nobody listed, and the one whose answer is a question back. Both are
// the same choice, and it is on every list there is.
func TestTheLastAnswerIsAlwaysTheOperatorsOwnWords(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	ctx := context.Background()
	finished := make(chan struct{})
	interrupted := choosing(ctx, console, "answer c1.1? ", testOptions, finished)
	out.await(t, "the answers on offer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"1. Write the goal it serves")
	})

	// The numbers are on screen, so typing one moves the marker to it. It moves
	// rather than answers, so a digit is corrected rather than sent.
	keys.Write([]byte("3"))
	out.await(t, "the marker on their own words", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"3. "+FreeEntryChoice)
	})
	keys.Write([]byte("\r"))
	out.await(t, "the prompt for their own words", func(s *screen) bool {
		return s.lastLine() == "answer c1.1?"
	})

	// A run that finishes while they are typing takes the prompt back, and what
	// they were part way through is a line rather than a choice: it resumes as
	// the prompt it had become rather than putting the list to them again.
	keys.Write([]byte("neither; what would"))
	out.await(t, "the typed text", func(s *screen) bool {
		return s.lastLine() == "answer c1.1? neither; what would"
	})
	close(finished)
	if _, err := interrupted.result(t); err != ErrInterrupted {
		t.Fatalf("Choose() error = %v, want ErrInterrupted", err)
	}
	answers := choosing(ctx, console, "answer c1.1? ", testOptions, nil)
	out.await(t, "the resumed line", func(s *screen) bool {
		return s.lastLine() == "answer c1.1? neither; what would"
	})

	keys.Write([]byte(" either of them cost?\n"))
	if answer := answers.line(t); answer != "neither; what would either of them cost?" {
		t.Fatalf("answer = %q", answer)
	}
}

// TestAChoiceInterruptedKeepsWhereTheOperatorHadGot is the guarantee a
// composing line already has, for a question being chosen from: a run that
// finishes while they are deciding is reported above the list, and the list
// comes back with the marker where they left it.
func TestAChoiceInterruptedKeepsWhereTheOperatorHadGot(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	ctx := context.Background()
	finished := make(chan struct{})
	interrupted := choosing(ctx, console, "answer c1.1? ", testOptions, finished)
	out.await(t, "the answers on offer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"1. Write the goal it serves")
	})

	keys.Write([]byte("\x1b[B"))
	out.await(t, "the marker on the second answer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"2. Retire the work")
	})
	close(finished)
	if _, err := interrupted.result(t); err != ErrInterrupted {
		t.Fatalf("Choose() error = %v, want ErrInterrupted", err)
	}
	io.WriteString(console, "harness> yoyodyne-1 finished.\n")

	answers := choosing(ctx, console, "answer c1.1? ", testOptions, nil)
	out.await(t, "the list put back where it was", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"2. Retire the work")
	})
	keys.Write([]byte("\r"))
	if answer := answers.line(t); answer != "Retire the work" {
		t.Fatalf("answer = %q", answer)
	}
	if transcript := out.screen().text(); !strings.Contains(transcript, "harness> yoyodyne-1 finished.") {
		t.Fatalf("the report was lost; screen was:\n%s", transcript)
	}
}

// TestADifferentQuestionStartsAtTheTop is the other half of the guarantee
// above. Two concerns can offer the same answers, and the marker is carried
// across an interruption for one question rather than for one list: a different
// question over the same answers starts at the top, and so does a question
// asked after another was answered, however alike the two read.
func TestADifferentQuestionStartsAtTheTop(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	ctx := context.Background()
	finished := make(chan struct{})
	interrupted := choosing(ctx, console, "answer c1.1? ", testOptions, finished)
	out.await(t, "the answers on offer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"1. Write the goal it serves")
	})
	keys.Write([]byte("\x1b[B"))
	out.await(t, "the marker on the second answer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"2. Retire the work")
	})
	close(finished)
	if _, err := interrupted.result(t); err != ErrInterrupted {
		t.Fatalf("Choose() error = %v, want ErrInterrupted", err)
	}

	// The same answers under a different concern are a different question, and
	// the marker the operator moved for the first says nothing about the second.
	answers := choosing(ctx, console, "answer c1.2? ", testOptions, nil)
	out.await(t, "the other question", func(s *screen) bool {
		return strings.Contains(s.text(), "answer c1.2?")
	})
	if listed := out.screen().text(); !strings.Contains(listed, chosenMarker+"1. Write the goal it serves") {
		t.Fatalf("a different question kept the first one's marker:\n%s", listed)
	}
	keys.Write([]byte("\x1b[B\r"))
	if answer := answers.line(t); answer != "Retire the work" {
		t.Fatalf("answer = %q", answer)
	}

	// A question that reads exactly as one already answered is still a new
	// question: what was chosen went with the list it was chosen from.
	answers = choosing(ctx, console, "answer c1.2? ", testOptions, nil)
	out.await(t, "the question asked again", func(s *screen) bool {
		return strings.Contains(s.text(), choiceKeys)
	})
	if listed := out.screen().text(); !strings.Contains(listed, chosenMarker+"1. Write the goal it serves") {
		t.Fatalf("a question asked again started where the last answer was:\n%s", listed)
	}
	keys.Write([]byte("\r"))
	if answer := answers.line(t); answer != "Write the goal it serves" {
		t.Fatalf("answer = %q", answer)
	}
}

// TestASignalKeyDuringAChoiceIsRaised covers the terminal that has agreed to
// report Ctrl-\ rather than raise it. A list on screen is answered with the
// same keys a line is read with, so the key it reports has to reach the
// process exactly as it does while a line is being composed: an operator who
// cannot quit from a question is one the question has trapped.
func TestASignalKeyDuringAChoiceIsRaised(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	raised := make(chan signalKey, 1)
	console.raise = func(pressed signalKey) { raised <- pressed }
	answers := choosing(context.Background(), console, "answer c1.1? ", testOptions, nil)
	out.await(t, "the answers on offer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"1. Write the goal it serves")
	})

	// Ctrl-\, as a negotiated keyboard reports it rather than as a signal the
	// terminal raised.
	keys.Write([]byte("\x1b[92;5u"))
	if pressed := <-raised; pressed != signalQuit {
		t.Fatalf("raised %v, want the quit", pressed)
	}
	// The key was a signal and not a keystroke: the list is still there, still
	// answerable, and the marker has not moved.
	if listed := out.screen().text(); !strings.Contains(listed, chosenMarker+"1. Write the goal it serves") {
		t.Fatalf("the list was disturbed by a signal key:\n%s", listed)
	}
	keys.Write([]byte("\r"))
	if answer := answers.line(t); answer != "Write the goal it serves" {
		t.Fatalf("answer = %q", answer)
	}
}

// TestSuspendingDuringAChoiceTakesTheListDownAndPutsItBack is the stop from a
// question rather than from a line: the shell that takes the foreground finds
// no list on the screen, and the conversation that comes back finds the list
// where it was, with the marker where the operator had moved it.
func TestSuspendingDuringAChoiceTakesTheListDownAndPutsItBack(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	// The resumed negotiation is answered below, so it waits for the answer.
	console.keyboardWait = answeredNegotiation
	stopped := make(chan string, 1)
	console.raise = func(pressed signalKey) {
		if pressed != signalSuspend {
			t.Errorf("raised %v, want the suspend", pressed)
		}
		// What the screen holds at the moment the process stops: this is what the
		// shell is about to be handed.
		stopped <- out.screen().text()
	}
	// The terminal answers the negotiation the resumed conversation makes.
	go func() {
		for !strings.Contains(out.raw(), kittyQuery) {
			time.Sleep(time.Millisecond)
		}
		keys.Write([]byte("\x1b[?0u\x1b[?62;c"))
	}()
	answers := choosing(context.Background(), console, "answer c1.1? ", testOptions, nil)
	out.await(t, "the answers on offer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"1. Write the goal it serves")
	})
	keys.Write([]byte("\x1b[B"))
	out.await(t, "the marker on the second answer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"2. Retire the work")
	})

	// Ctrl-Z, as a negotiated keyboard reports it.
	keys.Write([]byte("\x1b[122;5u"))
	if screen := <-stopped; strings.Contains(screen, "Retire the work") || strings.Contains(screen, choiceKeys) {
		t.Fatalf("the list was still on screen when the process stopped:\n%s", screen)
	}

	// Resumed: the list is drawn back with the marker where it was left.
	out.await(t, "the list drawn again", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"2. Retire the work")
	})
	keys.Write([]byte("\r"))
	if answer := answers.line(t); answer != "Retire the work" {
		t.Fatalf("answer = %q", answer)
	}
}

// TestOutputArrivingUnderAListKeepsBothWhole is the region arithmetic with a
// list in it: erasing has to climb back over every row the answers occupy, or
// the conversation above loses a line to them.
func TestOutputArrivingUnderAListKeepsBothWhole(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	io.WriteString(console, "product-manager> Two goals, then.\n")
	answers := choosing(context.Background(), console, "answer c1.1? ", testOptions, nil)
	out.await(t, "the answers on offer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"1. Write the goal it serves")
	})
	io.WriteString(console, "harness> yoyodyne-1 finished.\n")

	rendered := out.screen()
	lines := rendered.lines()
	if len(lines) < 2 || lines[0] != "product-manager> Two goals, then." || lines[1] != "harness> yoyodyne-1 finished." {
		t.Fatalf("the conversation lost a line to the list:\n%s", rendered.text())
	}
	if !strings.Contains(rendered.text(), chosenMarker+"1. Write the goal it serves") {
		t.Fatalf("the list was not drawn again under the output:\n%s", rendered.text())
	}
	keys.Write([]byte("\r"))
	if answer := answers.line(t); answer != "Write the goal it serves" {
		t.Fatalf("answer = %q", answer)
	}
}

// TestChooseReportsTheEndOfInputAndLeavesTheQuestionOnScreen covers the
// operator who closes the input rather than answering. There is no answer to
// return, and what they were asked stays where they can read it.
func TestChooseReportsTheEndOfInputAndLeavesTheQuestionOnScreen(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	answers := choosing(context.Background(), console, "answer c1.1? ", testOptions, nil)
	out.await(t, "the answers on offer", func(s *screen) bool {
		return strings.Contains(s.text(), chosenMarker+"1. Write the goal it serves")
	})
	keys.Close()
	if _, err := answers.result(t); err != io.EOF {
		t.Fatalf("Choose() error = %v, want io.EOF", err)
	}
	if rendered := out.screen(); rendered.text() != "answer c1.1?" {
		t.Fatalf("screen = %q", rendered.text())
	}
}

// TestAStreamAsksTheSameQuestionAsNumbers is the degraded path, which is every
// conversation that is not held on a terminal: the same answers, in the same
// order, chosen by typing the number instead of moving a marker.
func TestAStreamAsksTheSameQuestionAsNumbers(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	stream := newPlain(strings.NewReader("2\n"), &out)
	answer, err := stream.Choose(context.Background(), "answer c1.1? ", testOptions, nil)
	if err != nil {
		t.Fatalf("Choose() error = %v", err)
	}
	if answer != "Retire the work" {
		t.Fatalf("answer = %q", answer)
	}
	for _, required := range []string{
		"  1. Write the goal it serves",
		"  2. Retire the work",
		"  3. " + FreeEntryChoice,
		choiceHint,
		"answer c1.1?",
	} {
		if !strings.Contains(out.String(), required) {
			t.Fatalf("stream =\n%s\nwant it to contain %q", out.String(), required)
		}
	}
	// Nothing a terminal draws reaches a stream: what is written here is the
	// same text a redirected conversation has always held.
	if strings.ContainsAny(out.String(), "\x1b") || strings.Contains(out.String(), chosenMarker) {
		t.Fatalf("a stream was drawn on: %q", out.String())
	}
}

func TestAStreamTakesTheOperatorsOwnWordsWhereANumberWasOffered(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		typed string
		want  string
	}{
		{
			// Their answer is not on the list, so the line they typed is the
			// answer: a numbered prompt never costs them a sentence they had
			// already written.
			name:  "words instead of a number",
			typed: "neither; what would either of them cost?\n",
			want:  "neither; what would either of them cost?",
		},
		{
			// They named their own words from the list, so the prompt is put
			// again and what they say to it is the answer.
			name:  "the number of their own words",
			typed: "3\nneither; what would either of them cost?\n",
			want:  "neither; what would either of them cost?",
		},
		{
			// A number nobody offered is not a selection anyone can be sure of,
			// so it is what they said rather than a guess at what they meant.
			name:  "a number that is not on offer",
			typed: "9\n",
			want:  "9",
		},
		{
			// An empty line at a question is what it has always been: the
			// question left open, which the caller says out loud.
			name:  "nothing at all",
			typed: "\n",
			want:  "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var out strings.Builder
			stream := newPlain(strings.NewReader(test.typed), &out)
			answer, err := stream.Choose(context.Background(), "answer c1.1? ", testOptions, nil)
			if err != nil {
				t.Fatalf("Choose() error = %v", err)
			}
			if answer != test.want {
				t.Fatalf("answer = %q, want %q", answer, test.want)
			}
		})
	}
}

func TestAStreamReportsTheEndOfInputRatherThanGuessingAnAnswer(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	stream := newPlain(strings.NewReader(""), &out)
	if _, err := stream.Choose(context.Background(), "answer c1.1? ", testOptions, nil); err != io.EOF {
		t.Fatalf("Choose() error = %v, want io.EOF", err)
	}
}
