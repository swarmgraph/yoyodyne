package console

import (
	"context"
	"testing"
)

// editingKeys is what each kind of terminal sends for the editing keys: one in
// its legacy mode, which says nothing about what was held and puts an escape in
// front of a key held with Option or Alt, and the two that report what was held.
var editingKeys = []struct {
	terminal string
	keys     map[string][]string
}{
	{
		terminal: "a legacy terminal",
		keys: map[string][]string{
			"Ctrl-A": {"\x01"},
			"Ctrl-E": {"\x05"},
			"Ctrl-K": {"\x0b"},
			"Ctrl-U": {"\x15"},
			"Ctrl-W": {"\x17"},
			"Ctrl-Y": {"\x19"},
			// Terminal.app sends Option-left as Alt-B; others put the escape in
			// front of the arrow, and xterm reports the arrow held even here.
			"Alt-left":       {"\x1bb", "\x1b\x1b[D", "\x1b[1;3D"},
			"Alt-right":      {"\x1bf", "\x1b\x1b[C", "\x1b[1;3C"},
			"Alt-B":          {"\x1bb"},
			"Alt-F":          {"\x1bf"},
			"Alt-backspace":  {"\x1b\x7f", "\x1b\x08"},
			"Ctrl-left":      {"\x1b[1;5D"},
			"Shift-left":     {"\x1b[1;2D"},
			"Alt-up":         {"\x1b\x1b[A"},
			"Unbound Ctrl-G": {"\x07"},
		},
	},
	{
		terminal: "a terminal speaking the kitty keyboard protocol",
		keys: map[string][]string{
			"Ctrl-A":         {"\x1b[97;5u"},
			"Ctrl-E":         {"\x1b[101;5u"},
			"Ctrl-K":         {"\x1b[107;5u"},
			"Ctrl-U":         {"\x1b[117;5u"},
			"Ctrl-W":         {"\x1b[119;5u"},
			"Ctrl-Y":         {"\x1b[121;5u", "\x1b[121;5:1u"},
			"Alt-left":       {"\x1b[1;3D", "\x1b[1;3:1D"},
			"Alt-right":      {"\x1b[1;3C"},
			"Alt-B":          {"\x1b[98;3u"},
			"Alt-F":          {"\x1b[102;3u"},
			"Alt-backspace":  {"\x1b[127;3u"},
			"Ctrl-left":      {"\x1b[1;5D"},
			"Shift-left":     {"\x1b[1;2D"},
			"Alt-up":         {"\x1b[1;3A"},
			"Unbound Alt-Q":  {"\x1b[113;3u"},
			"Unbound Ctrl-G": {"\x1b[103;5u"},
		},
	},
	{
		terminal: "a terminal speaking xterm's modifyOtherKeys",
		keys: map[string][]string{
			"Ctrl-A":         {"\x1b[27;5;97~"},
			"Ctrl-E":         {"\x1b[27;5;101~"},
			"Ctrl-K":         {"\x1b[27;5;107~"},
			"Ctrl-U":         {"\x1b[27;5;117~"},
			"Ctrl-W":         {"\x1b[27;5;119~"},
			"Ctrl-Y":         {"\x1b[27;5;121~"},
			"Alt-left":       {"\x1b[1;3D"},
			"Alt-right":      {"\x1b[1;3C"},
			"Alt-B":          {"\x1b[27;3;98~"},
			"Alt-F":          {"\x1b[27;3;102~"},
			"Alt-backspace":  {"\x1b[27;3;127~"},
			"Ctrl-left":      {"\x1b[1;5D"},
			"Shift-left":     {"\x1b[1;2D"},
			"Alt-up":         {"\x1b[1;3A"},
			"Unbound Alt-Q":  {"\x1b[27;3;113~"},
			"Unbound Ctrl-G": {"\x1b[27;5;103~"},
		},
	},
}

// TestEachEditingKeyDecodesWholeAsEveryTerminalSendsIt checks the decoder alone:
// every spelling of a key is read as that key, whole, so no byte of it is left
// over to be typed into the message.
func TestEachEditingKeyDecodesWholeAsEveryTerminalSendsIt(t *testing.T) {
	t.Parallel()

	want := map[string]keyCode{
		"Ctrl-A":         keyHome,
		"Ctrl-E":         keyEnd,
		"Ctrl-K":         keyKillToEnd,
		"Ctrl-U":         keyKillLine,
		"Ctrl-W":         keyKillWord,
		"Ctrl-Y":         keyYank,
		"Alt-left":       keyWordLeft,
		"Alt-right":      keyWordRight,
		"Alt-B":          keyWordLeft,
		"Alt-F":          keyWordRight,
		"Alt-backspace":  keyKillWord,
		"Ctrl-left":      keyWordLeft,
		"Shift-left":     keyLeft,
		"Alt-up":         keyUp,
		"Unbound Alt-Q":  keyIgnored,
		"Unbound Ctrl-G": keyIgnored,
	}
	for _, kind := range editingKeys {
		for name, spellings := range kind.keys {
			for _, bytes := range spellings {
				pressed, size, complete := decodeKey([]byte(bytes))
				if !complete || size != len(bytes) {
					t.Errorf("%s: %s %q read %d bytes (whole = %v), want all %d", kind.terminal, name, bytes, size, complete, len(bytes))
					continue
				}
				if pressed.code != want[name] {
					t.Errorf("%s: %s %q = %v, want %v", kind.terminal, name, bytes, pressed.code, want[name])
				}
			}
		}
	}
}

// TestEditingKeysChangeTheMessageAsClaudeCodesDo feeds each key, as each kind of
// terminal sends it, through the decoder and the line editor, starting from a
// message with the cursor at its end, and shows the message and the cursor that
// result.
func TestEditingKeysChangeTheMessageAsClaudeCodesDo(t *testing.T) {
	t.Parallel()

	const start = "plan the brief"
	for _, test := range []struct {
		name   string
		keys   []string
		line   string
		cursor int
	}{
		{name: "a word left", keys: []string{"Alt-left"}, line: start, cursor: 9},
		{name: "two words left and one right", keys: []string{"Alt-left", "Alt-left", "Alt-right"}, line: start, cursor: 8},
		{name: "Alt-B is a word left", keys: []string{"Alt-B", "Alt-B"}, line: start, cursor: 5},
		{name: "Alt-F is a word right", keys: []string{"Ctrl-A", "Alt-F"}, line: start, cursor: 4},
		{name: "Ctrl-left is a word left too", keys: []string{"Ctrl-left"}, line: start, cursor: 9},
		{name: "shift-left is still one character", keys: []string{"Shift-left"}, line: start, cursor: 13},
		{name: "Alt-backspace deletes the word before the cursor", keys: []string{"Alt-backspace"}, line: "plan the ", cursor: 9},
		{name: "Ctrl-K deletes to the end", keys: []string{"Alt-left", "Alt-left", "Ctrl-K"}, line: "plan ", cursor: 5},
		{name: "Ctrl-K at the end deletes nothing", keys: []string{"Ctrl-K"}, line: start, cursor: 14},
		{name: "Ctrl-Y puts back what Ctrl-K took", keys: []string{"Alt-left", "Ctrl-K", "Ctrl-A", "Ctrl-Y"}, line: "briefplan the ", cursor: 5},
		{name: "Ctrl-Y puts back what Ctrl-U took", keys: []string{"Alt-left", "Ctrl-U", "Ctrl-E", "Ctrl-Y"}, line: "briefplan the ", cursor: 14},
		{name: "Ctrl-Y puts back what Ctrl-W took", keys: []string{"Ctrl-W", "Ctrl-A", "Ctrl-Y"}, line: "briefplan the ", cursor: 5},
		{name: "Ctrl-Y puts back what Alt-backspace took", keys: []string{"Alt-backspace", "Ctrl-A", "Ctrl-Y"}, line: "briefplan the ", cursor: 5},
		{name: "kills one after another are put back together", keys: []string{"Ctrl-W", "Alt-backspace", "Ctrl-Y"}, line: start, cursor: 14},
		{name: "a kill after another key starts again", keys: []string{"Ctrl-W", "Alt-left", "Ctrl-W", "Ctrl-E", "Ctrl-Y"}, line: "the plan ", cursor: 9},
		{name: "Ctrl-Y can be pressed more than once", keys: []string{"Ctrl-W", "Ctrl-Y", "Ctrl-Y"}, line: "plan the briefbrief", cursor: 19},
		{name: "Ctrl-Y with nothing killed does nothing", keys: []string{"Ctrl-Y"}, line: start, cursor: 14},
		{name: "a key nothing is bound to types nothing", keys: []string{"Unbound Ctrl-G"}, line: start, cursor: 14},
		{name: "Alt with a key nothing is bound to types nothing", keys: []string{"Unbound Ctrl-G", "Unbound Alt-Q"}, line: start, cursor: 14},
	} {
	terminals:
		for _, kind := range editingKeys {
			// Every spelling a terminal has for a key is pressed in its turn, so a
			// key with two spellings is tested as both.
			for spelling := 0; ; spelling++ {
				var bytes string
				more := false
				for _, name := range test.keys {
					spellings, ok := kind.keys[name]
					if !ok {
						// A legacy terminal has no Alt-Q of its own: what it sends is
						// an escape and then a q, which is what pressing escape and
						// then typing a q sends too, so the q is typed.
						continue terminals
					}
					bytes += spellings[min(spelling, len(spellings)-1)]
					more = more || spelling+1 < len(spellings)
				}
				line, cursor := editing(t, start, bytes)
				if line != test.line || cursor != test.cursor {
					t.Errorf("%s, %s (%q): %q with the cursor at %d, want %q at %d", kind.terminal, test.name, bytes, line, cursor, test.line, test.cursor)
				}
				if !more {
					break
				}
			}
		}
	}
}

// TestCtrlKStopsAtTheEndOfItsLine checks Ctrl-K in a message of more than one
// line: it deletes to the end of the line the cursor is on, and at the end of a
// line it takes the newline, as the shell's own line editors do.
func TestCtrlKStopsAtTheEndOfItsLine(t *testing.T) {
	t.Parallel()

	console := &terminal{line: []rune("first\nsecond"), cursor: 2}
	console.apply(key{code: keyKillToEnd})
	if line := string(console.line); line != "fi\nsecond" || console.cursor != 2 {
		t.Fatalf("after one Ctrl-K: %q at %d, want %q at 2", line, console.cursor, "fi\nsecond")
	}
	console.apply(key{code: keyKillToEnd})
	if line := string(console.line); line != "fisecond" || console.cursor != 2 {
		t.Fatalf("after two: %q at %d, want %q at 2", line, console.cursor, "fisecond")
	}
	console.apply(key{code: keyYank})
	if line := string(console.line); line != "first\nsecond" || console.cursor != 6 {
		t.Fatalf("after Ctrl-Y: %q at %d, want the message back with the cursor at 6", line, console.cursor)
	}
}

// TestEditingKeysAtThePrompt is the same keys at a real prompt: typed, edited,
// and sent, with nothing of their escape sequences landing in the message, and
// what was cut from one message put into the next.
func TestEditingKeysAtThePrompt(t *testing.T) {
	t.Parallel()

	console, keys, _ := terminalUnderTest(t, 40)
	ctx := context.Background()

	lines := prompting(ctx, console, "you> ", nil)
	// The last word cut and put at the front, and the first word then cut and
	// put at the end, with the keys spelled by all three kinds of terminal.
	keys.Write([]byte("goals the review" +
		"\x1b[1;3D" + "\x1b[107;5u" + "\x01" + "\x1b[27;5;121~" + " " +
		"\x1bf" + "\x1b[127;3u" + "\x1b[3~" + "\x1b[101;5u" + "\x19" + pressEnter))
	if line := lines.line(t); line != "review the goals" {
		t.Fatalf("line = %q, want %q", line, "review the goals")
	}

	lines = prompting(ctx, console, "you> ", nil)
	keys.Write([]byte("and " + "\x19" + pressEnter))
	if line := lines.line(t); line != "and goals" {
		t.Fatalf("line = %q, want what was cut from the last message put into this one", line)
	}
}

// editing presses the keys in bytes, as a terminal sends them, on a message
// that starts as start with the cursor at its end.
func editing(t *testing.T, start, bytes string) (string, int) {
	t.Helper()

	console := &terminal{line: []rune(start), cursor: len([]rune(start))}
	buffer := []byte(bytes)
	for len(buffer) > 0 {
		pressed, size, complete := decodeKey(buffer)
		if !complete {
			t.Fatalf("%q was left part of a keystroke: %q", bytes, buffer)
		}
		buffer = buffer[size:]
		console.apply(pressed)
	}
	return string(console.line), console.cursor
}
