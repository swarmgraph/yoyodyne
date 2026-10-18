package console

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// testCommands is a list shaped like the conversation's: names that share a
// beginning, one that is the beginning of another, and one that needs an
// argument typed after it.
var testCommands = []Command{
	{Name: "/status", Description: "what is in flight"},
	{Name: "/show", Description: "one work item", Argument: true},
	{Name: "/stop", Description: "stop one item's run"},
	{Name: "/stop-everything", Description: "stop every run"},
	{Name: "/help", Description: "this list"},
}

// commandsUnderTest is a terminal offering testCommands, with a prompt open.
func commandsUnderTest(t *testing.T) (*terminal, func(string), *recorder, prompted) {
	t.Helper()

	console, keys, out := terminalUnderTest(t, 60)
	OfferCommands(console, testCommands...)
	lines := prompting(context.Background(), console, "you> ", nil)
	// The screen drops the space a row ends in, so the empty prompt reads "you>".
	out.await(t, "the prompt", func(s *screen) bool { return s.lastLine() == "you>" })
	return console, func(typed string) { keys.Write([]byte(typed)) }, out, lines
}

// listed is the names the list shows on screen, under the prompt, and which of
// them the marker is against.
func listed(s *screen) (names []string, chosen string) {
	rows := s.lines()
	prompt := -1
	for index, row := range rows {
		if strings.HasPrefix(row, "you>") {
			prompt = index
		}
	}
	if prompt < 0 {
		return nil, ""
	}
	for _, row := range rows[prompt+1:] {
		marked := strings.HasPrefix(row, chosenMarker)
		if !marked && !strings.HasPrefix(row, unchosenMarker) {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(strings.TrimPrefix(row, chosenMarker), unchosenMarker))
		if len(fields) == 0 {
			continue
		}
		names = append(names, fields[0])
		if marked {
			chosen = fields[0]
		}
	}
	return names, chosen
}

func showing(names ...string) func(*screen) bool {
	return func(s *screen) bool {
		got, _ := listed(s)
		return strings.Join(got, " ") == strings.Join(names, " ")
	}
}

// TestASlashOnAnEmptyPromptListsTheCommands is the list opening: every command,
// each with its description, under the line being typed, the first marked.
func TestASlashOnAnEmptyPromptListsTheCommands(t *testing.T) {
	t.Parallel()

	_, typed, out, _ := commandsUnderTest(t)
	typed("/")
	shown := out.await(t, "every command listed", showing("/status", "/show", "/stop", "/stop-everything", "/help"))
	if !strings.Contains(shown.text(), "/stop-everything  stop every run") {
		t.Fatalf("a command is listed without its description; screen was:\n%s", shown.text())
	}
	if _, chosen := listed(shown); chosen != "/status" {
		t.Fatalf("the marker is against %q, want the first command; screen was:\n%s", chosen, shown.text())
	}
	// The line being typed is still where the operator is typing: above the list.
	if !strings.Contains(shown.text(), "you> /\n") {
		t.Fatalf("the prompt is not above the list; screen was:\n%s", shown.text())
	}
}

// TestTheListNarrowsAsTheNameIsTyped covers narrowing, and widening again when
// what was typed is deleted.
func TestTheListNarrowsAsTheNameIsTyped(t *testing.T) {
	t.Parallel()

	_, typed, out, _ := commandsUnderTest(t)
	typed("/st")
	out.await(t, "the commands beginning /st", showing("/status", "/stop", "/stop-everything"))
	typed("o")
	out.await(t, "the commands beginning /sto", showing("/stop", "/stop-everything"))
	typed("x")
	out.await(t, "nothing listed for a name nothing begins with", showing())
	typed("\x7f\x7f\x7f")
	out.await(t, "the list widened again", showing("/status", "/show", "/stop", "/stop-everything"))
}

// TestUpAndDownPickACommandAndTabCompletesIt is moving through the list and
// completing the one under the marker; the line sent is the completed name.
func TestUpAndDownPickACommandAndTabCompletesIt(t *testing.T) {
	t.Parallel()

	_, typed, out, lines := commandsUnderTest(t)
	typed("/st")
	out.await(t, "the commands beginning /st", showing("/status", "/stop", "/stop-everything"))
	typed("\x1b[B\x1b[B")
	shown := out.await(t, "the marker moved down twice", func(s *screen) bool {
		_, chosen := listed(s)
		return chosen == "/stop-everything"
	})
	typed("\x1b[A")
	out.await(t, "the marker moved up once", func(s *screen) bool {
		_, chosen := listed(s)
		return chosen == "/stop"
	})
	// Up from the first wraps to the last, as down from the last wraps to the first.
	typed("\x1b[A\x1b[A")
	out.await(t, "the marker wrapped to the last", func(s *screen) bool {
		_, chosen := listed(s)
		return chosen == "/stop-everything"
	})
	typed("\x1b[B\x1b[B")
	out.await(t, "the marker back on /stop", func(s *screen) bool {
		_, chosen := listed(s)
		return chosen == "/stop"
	})
	typed("\t")
	out.await(t, "the name completed", func(s *screen) bool {
		return strings.Contains(s.text(), "you> /stop\n")
	})
	typed("\r")
	if line := lines.line(t); line != "/stop" {
		t.Fatalf("line = %q, want the completed name; screen was:\n%s", line, shown.text())
	}
	if names, _ := listed(out.screen()); len(names) != 0 {
		t.Fatalf("the list is still on screen after sending; screen was:\n%s", out.screen().text())
	}
}

// TestReturnCompletesANameAndSendsOneTypedInFull is return with the list
// showing: a partial name is completed rather than sent, and a name that is
// already whole is sent, even where a longer one begins with it.
func TestReturnCompletesANameAndSendsOneTypedInFull(t *testing.T) {
	t.Parallel()

	_, typed, out, lines := commandsUnderTest(t)
	typed("/he\r")
	out.await(t, "the name completed by return", func(s *screen) bool {
		return strings.Contains(s.text(), "you> /help\n")
	})
	typed("\r")
	if line := lines.line(t); line != "/help" {
		t.Fatalf("line = %q", line)
	}
}

// TestANameTypedInFullIsSentWhereALongerOneBeginsWithIt is /stop typed whole
// with /stop-everything still listed: return sends what was typed.
func TestANameTypedInFullIsSentWhereALongerOneBeginsWithIt(t *testing.T) {
	t.Parallel()

	_, typed, out, lines := commandsUnderTest(t)
	typed("/stop")
	out.await(t, "both names beginning /stop", showing("/stop", "/stop-everything"))
	typed("\r")
	if line := lines.line(t); line != "/stop" {
		t.Fatalf("line = %q, want what was typed", line)
	}
}

// TestCompletingACommandThatTakesAnArgumentLeavesRoomForIt is the space after a
// name that needs something typed after it, which also puts the list away.
func TestCompletingACommandThatTakesAnArgumentLeavesRoomForIt(t *testing.T) {
	t.Parallel()

	_, typed, out, lines := commandsUnderTest(t)
	typed("/sh\t")
	out.await(t, "the name completed with a space and the list gone", func(s *screen) bool {
		names, _ := listed(s)
		return len(names) == 0 && s.lastLine() == "you> /show"
	})
	typed("yoyodyne-1\r")
	if line := lines.line(t); line != "/show yoyodyne-1" {
		t.Fatalf("line = %q", line)
	}
}

// TestEscapePutsTheListAwayLeavingTheText is Esc with the list showing: the
// text stays exactly as typed, typing on does not bring the list back, and
// return then sends what was typed rather than completing it.
func TestEscapePutsTheListAwayLeavingTheText(t *testing.T) {
	t.Parallel()

	_, typed, out, lines := commandsUnderTest(t)
	typed("/st")
	out.await(t, "the commands beginning /st", showing("/status", "/stop", "/stop-everything"))
	typed("\x1b[27u")
	out.await(t, "the list put away", func(s *screen) bool {
		names, _ := listed(s)
		return len(names) == 0 && s.lastLine() == "you> /st"
	})
	typed("o")
	out.await(t, "the text typed on", func(s *screen) bool { return s.lastLine() == "you> /sto" })
	if names, _ := listed(out.screen()); len(names) != 0 {
		t.Fatalf("typing brought the list back; screen was:\n%s", out.screen().text())
	}
	typed("\r")
	if line := lines.line(t); line != "/sto" {
		t.Fatalf("line = %q, want what was typed", line)
	}
}

// TestEscapeBesideATurnPutsTheListAwayBeforeItStopsTheTurn: with the list
// showing, Esc is pressed to put the list away, and only a second press is the
// key that stops the turn being answered.
func TestEscapeBesideATurnPutsTheListAwayBeforeItStopsTheTurn(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 60)
	OfferCommands(console, testCommands...)
	end := console.BesideTurn()
	defer end()
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("/st"))
	out.await(t, "the commands beginning /st", showing("/status", "/stop", "/stop-everything"))
	keys.Write([]byte("\x1b[27u"))
	out.await(t, "the list put away", func(s *screen) bool {
		names, _ := listed(s)
		return len(names) == 0
	})
	keys.Write([]byte("\x1b[27u"))
	if _, err := lines.result(t); err != ErrStopTurn {
		t.Fatalf("Prompt() error = %v, want ErrStopTurn", err)
	}
}

// TestOnlyASlashOnAnEmptyLineOpensTheList: a slash later in a message, and a
// command brought back with up from earlier messages, open nothing, and up
// goes on stepping through what was sent.
func TestOnlyASlashOnAnEmptyLineOpensTheList(t *testing.T) {
	t.Parallel()

	console, typed, out, lines := commandsUnderTest(t)
	typed("and/or")
	out.await(t, "the text", func(s *screen) bool { return s.lastLine() == "you> and/or" })
	if names, _ := listed(out.screen()); len(names) != 0 {
		t.Fatalf("a slash inside a message opened the list; screen was:\n%s", out.screen().text())
	}
	typed("\r")
	lines.line(t)

	console.mu.Lock()
	console.remember("/status", "/stop")
	console.mu.Unlock()
	lines = prompting(context.Background(), console, "you> ", nil)
	typed("\x1b[A")
	out.await(t, "the last message recalled", func(s *screen) bool { return s.lastLine() == "you> /stop" })
	if names, _ := listed(out.screen()); len(names) != 0 {
		t.Fatalf("a recalled command opened the list; screen was:\n%s", out.screen().text())
	}
	typed("\x1b[A")
	out.await(t, "the one before it recalled", func(s *screen) bool { return s.lastLine() == "you> /status" })
	typed("\r")
	if line := lines.line(t); line != "/status" {
		t.Fatalf("line = %q", line)
	}
}

// TestTheListFitsTheWindow bounds the list to the rows the window has under the
// line being typed and to the width, so it never wraps or scrolls off.
func TestTheListFitsTheWindow(t *testing.T) {
	t.Parallel()

	console := &terminal{width: func() int { return 20 }, height: func() int { return 3 }, prompting: true, menuOpen: true, line: []rune("/"), commands: testCommands}
	rows := console.menuRegion(20, 2)
	if len(rows) != 2 {
		t.Fatalf("menu rows = %q, want the two the window has room for", rows)
	}
	for _, row := range rows {
		if visibleWidth(row) >= 20 {
			t.Fatalf("row %q fills the width and would wrap", row)
		}
	}
	// The marker moved past the rows shown brings the rows with it.
	console.menuChosen = 3
	rows = console.menuRegion(20, 2)
	if !strings.HasPrefix(rows[1], chosenMarker+"/stop-everything") {
		t.Fatalf("menu rows = %q, want the marked command shown", rows)
	}
}

// TestAStreamListsNothing is a conversation that is not on a terminal: offering
// commands changes nothing, and what is typed is read as the line it was.
func TestAStreamListsNothing(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	stream := newPlain(strings.NewReader("/st\t\n"), &out)
	OfferCommands(stream, testCommands...)
	line, err := stream.Prompt(context.Background(), "you> ", nil)
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if line != "/st\t" {
		t.Fatalf("line = %q, want what was typed", line)
	}
	if out.String() != "you> " {
		t.Fatalf("the stream was written %q, want only the prompt", out.String())
	}
}
