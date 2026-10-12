package console

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// preferences stands in for macOS's preferences system, holding iTerm2's
// global key bindings the way plutil reads them and defaults writes them.
type preferences struct {
	bindings map[string]iTermBinding
	commands []string
}

var dictValue = regexp.MustCompile(`^<dict><key>Action</key><integer>(\d+)</integer><key>Text</key><string>([^<]*)</string></dict>$`)

func (p *preferences) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	p.commands = append(p.commands, command.Name+" "+strings.Join(command.Args, " "))
	switch command.Name {
	case "plutil":
		if p.bindings == nil {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Could not extract value, error: No value at that key path or invalid key path: GlobalKeyMap"}, nil
		}
		encoded, _ := json.Marshal(p.bindings)
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
	case "defaults":
		args := command.Args
		if len(args) != 6 || args[0] != "write" || args[1] != iTermDomain || args[2] != iTermKeyMap || args[3] != "-dict-add" {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "unexpected defaults command"}, nil
		}
		match := dictValue.FindStringSubmatch(args[5])
		if match == nil {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "unreadable value " + args[5]}, nil
		}
		action, _ := strconv.Atoi(match[1])
		if p.bindings == nil {
			p.bindings = map[string]iTermBinding{}
		}
		p.bindings[args[4]] = iTermBinding{Action: action, Text: match[2]}
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
	}
	return execution.ProcessResult{}, os.ErrNotExist
}

func (p *preferences) writes() int {
	count := 0
	for _, command := range p.commands {
		if strings.HasPrefix(command, "defaults write") {
			count++
		}
	}
	return count
}

// hexBytes reads the bytes an iTerm2 binding that sends hexadecimal codes
// names, as iTerm2 does when the key is pressed.
func hexBytes(t *testing.T, text string) []byte {
	t.Helper()
	var sent []byte
	for _, code := range strings.Fields(text) {
		value, err := strconv.ParseUint(strings.TrimPrefix(code, "0x"), 16, 8)
		if err != nil {
			t.Fatalf("%q is not a hexadecimal code: %v", code, err)
		}
		sent = append(sent, byte(value))
	}
	return sent
}

// decodesToNewline checks that what a terminal now sends for shift-return is a
// new line to the message being composed, and that return alone still sends.
func decodesToNewline(t *testing.T, sent []byte) {
	t.Helper()
	pressed, size, complete := decodeKey(sent)
	if !complete || size != len(sent) || pressed.code != keyNewline {
		t.Fatalf("shift-return sends %q, which decodes to %+v (%d of %d bytes, complete %v), not a new line", sent, pressed, size, len(sent), complete)
	}
	for _, alone := range []string{"\r", "\n"} {
		if pressed, _, _ := decodeKey([]byte(alone)); pressed.code != keyEnter {
			t.Fatalf("return alone (%q) decodes to %+v rather than sending", alone, pressed)
		}
	}
}

func TestITermIsSetUpOnceAndShiftReturnBecomesANewLine(t *testing.T) {
	t.Parallel()

	prefs := &preferences{bindings: map[string]iTermBinding{"0xf700-0x300000": {Action: 10, Text: "[1;5A"}}}
	host := SetupHost{Env: environment(map[string]string{"TERM_PROGRAM": "iTerm.app"}), Home: t.TempDir(), Runner: prefs}

	// Before: shift-return has no binding, so iTerm2 sends return for it.
	if _, ok := prefs.bindings[iTermShiftReturn]; ok {
		t.Fatal("the starting settings already bind shift-return")
	}
	said, err := SetUpTerminal(context.Background(), host, false)
	if err != nil {
		t.Fatalf("SetUpTerminal: %v\n%s", err, said)
	}
	for _, want := range []string{"This is iTerm2.", "Changing: iTerm2's global key bindings", "Quit iTerm2"} {
		if !strings.Contains(said, want) {
			t.Fatalf("what the setup said does not include %q:\n%s", want, said)
		}
	}
	// After: shift-return sends escape and return, and nothing else moved.
	got, ok := prefs.bindings[iTermShiftReturn]
	if !ok || got != (iTermBinding{Action: iTermSendHex, Text: "0x1b 0x0d"}) {
		t.Fatalf("shift-return is bound to %+v (present %v)", got, ok)
	}
	if other := prefs.bindings["0xf700-0x300000"]; other != (iTermBinding{Action: 10, Text: "[1;5A"}) {
		t.Fatalf("an unrelated binding was disturbed: %+v", other)
	}
	decodesToNewline(t, hexBytes(t, got.Text))

	// Twice changes nothing more.
	again, err := SetUpTerminal(context.Background(), host, false)
	if err != nil {
		t.Fatalf("second SetUpTerminal: %v", err)
	}
	if prefs.writes() != 1 || !strings.Contains(again, "nothing was changed") {
		t.Fatalf("the second run wrote %d times in all and said:\n%s", prefs.writes(), again)
	}
}

func TestITermWithNoGlobalBindingsYetIsSetUp(t *testing.T) {
	t.Parallel()

	prefs := &preferences{}
	host := SetupHost{Env: environment(map[string]string{"TERM_PROGRAM": "iTerm.app"}), Home: t.TempDir(), Runner: prefs}
	if said, err := SetUpTerminal(context.Background(), host, false); err != nil {
		t.Fatalf("SetUpTerminal: %v\n%s", err, said)
	}
	if got := prefs.bindings[iTermShiftReturn]; got.Text != iTermSendText {
		t.Fatalf("shift-return is bound to %+v", got)
	}
}

func TestAShiftReturnBindingSomebodyMadeIsLeftAlone(t *testing.T) {
	t.Parallel()

	for _, name := range []string{iTermShiftReturn, iTermLegacyShiftReturn} {
		prefs := &preferences{bindings: map[string]iTermBinding{name: {Action: 12, Text: "\\n"}}}
		host := SetupHost{Env: environment(map[string]string{"TERM_PROGRAM": "iTerm.app"}), Home: t.TempDir(), Runner: prefs}
		said, err := SetUpTerminal(context.Background(), host, false)
		if err != nil {
			t.Fatalf("SetUpTerminal: %v", err)
		}
		if prefs.writes() != 0 || !strings.Contains(said, "left alone") || !strings.Contains(said, "Alt-return") {
			t.Fatalf("a binding under %s was not left alone (%d writes):\n%s", name, prefs.writes(), said)
		}
	}
}

func TestVSCodeIsSetUpOnceAndKeepsWhatTheFileHad(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path := filepath.Join(home, vscodeKeybindings("darwin", vscodeEdition{directory: "Code"}))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `// Place your key bindings in this file to override the defaults
[
    {
        "key": "cmd+k cmd+t", // "a comment, with a comma",
        "command": "workbench.action.selectTheme",
    },
    /* the end of the list */
]
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	host := SetupHost{
		Env:  environment(map[string]string{"TERM_PROGRAM": "vscode", "__CFBundleIdentifier": "com.microsoft.VSCode"}),
		Home: home,
		GOOS: "darwin",
	}
	said, err := SetUpTerminal(context.Background(), host, false)
	if err != nil {
		t.Fatalf("SetUpTerminal: %v\n%s", err, said)
	}
	for _, want := range []string{"This is the terminal in VS Code.", "Changing: " + path} {
		if !strings.Contains(said, want) {
			t.Fatalf("what the setup said does not include %q:\n%s", want, said)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `// Place your key bindings in this file to override the defaults
[
    {
        "key": "cmd+k cmd+t", // "a comment, with a comma",
        "command": "workbench.action.selectTheme",
    },
    {
        "key": "shift+enter",
        "command": "workbench.action.terminal.sendSequence",
        "args": {
            "text": "\u001b\r"
        },
        "when": "terminalFocus"
    }
    /* the end of the list */
]
`
	if string(after) != want {
		t.Fatalf("keybindings after setup:\n%s\nwant:\n%s", after, want)
	}
	var bindings []vscodeBinding
	if err := json.Unmarshal([]byte(dropTrailingCommas(blankComments(string(after)))), &bindings); err != nil {
		t.Fatalf("the file no longer reads as keybindings: %v", err)
	}
	decodesToNewline(t, []byte(bindings[len(bindings)-1].Args["text"]))

	again, err := SetUpTerminal(context.Background(), host, false)
	if err != nil {
		t.Fatalf("second SetUpTerminal: %v", err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != want || !strings.Contains(again, "nothing was changed") {
		t.Fatalf("the second run changed the file or said:\n%s", again)
	}
}

func TestVSCodeKeybindingsAreMadeWhereThereAreNone(t *testing.T) {
	t.Parallel()

	for _, before := range []string{"", "[]", "// only a comment\n", "[\n    {\"key\": \"f5\", \"command\": \"a\"}\n]"} {
		updated, state, err := addVSCodeBinding(before)
		if err != nil || state != bindingAbsent {
			t.Fatalf("addVSCodeBinding(%q) = %v, %v", before, state, err)
		}
		var bindings []vscodeBinding
		if err := json.Unmarshal([]byte(dropTrailingCommas(blankComments(updated))), &bindings); err != nil {
			t.Fatalf("addVSCodeBinding(%q) made a file that does not read:\n%s\n%v", before, updated, err)
		}
		last := bindings[len(bindings)-1]
		if last.Key != "shift+enter" || last.Args["text"] != newlineSequence || last.When != "terminalFocus" {
			t.Fatalf("addVSCodeBinding(%q) added %+v", before, last)
		}
		if again, state, _ := addVSCodeBinding(updated); again != updated || state != bindingPresent {
			t.Fatalf("a second pass over %q changed it, or reads it as %v", before, state)
		}
	}
	// A file with no home directory under it yet is made, as VS Code would.
	home := t.TempDir()
	host := SetupHost{Env: environment(map[string]string{"TERM_PROGRAM": "vscode"}), Home: home, GOOS: "linux"}
	if said, err := SetUpTerminal(context.Background(), host, false); err != nil {
		t.Fatalf("SetUpTerminal: %v\n%s", err, said)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "Code", "User", "keybindings.json")); err != nil {
		t.Fatalf("the keybindings file was not made: %v", err)
	}
}

func TestAVSCodeShiftReturnBindingSomebodyMadeIsLeftAlone(t *testing.T) {
	t.Parallel()

	before := `[{"key": "shift+enter", "command": "workbench.action.terminal.sendSequence", "args": {"text": "\\\r\n"}, "when": "terminalFocus"}]`
	updated, state, err := addVSCodeBinding(before)
	if err != nil || state != bindingTaken || updated != before {
		t.Fatalf("addVSCodeBinding = %v, %v, changed %v", state, err, updated != before)
	}
	// Shift-return bound in the editor rather than the terminal is not in the way.
	if _, state, _ := addVSCodeBinding(`[{"key": "shift+enter", "command": "editor.action.insertLineAfter", "when": "editorTextFocus"}]`); state != bindingAbsent {
		t.Fatalf("an editor binding reads as %v", state)
	}
}

func TestATerminalThatCannotBeSetUpIsNamedWithTheKeysThatWork(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		env  map[string]string
		name string
	}{
		{env: map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, name: "Terminal.app"},
		{env: map[string]string{"TERM_PROGRAM": "vscode", "__CFBundleIdentifier": "com.example.editor"}, name: "an editor built on VS Code that yoyo does not know"},
		{env: map[string]string{"TERM_PROGRAM": "Hyper"}, name: "which calls itself Hyper"},
		{env: map[string]string{}, name: "a terminal that does not say what it is"},
	} {
		prefs := &preferences{}
		host := SetupHost{Env: environment(test.env), Home: t.TempDir(), Runner: prefs}
		said, err := SetUpTerminal(context.Background(), host, false)
		if err != nil {
			t.Fatalf("SetUpTerminal: %v", err)
		}
		if !strings.Contains(said, test.name) || !strings.Contains(said, "Alt-return") || !strings.Contains(said, `\`) || !strings.Contains(said, "cannot be set up") {
			t.Fatalf("%v: the setup said:\n%s", test.env, said)
		}
		if len(prefs.commands) != 0 {
			t.Fatalf("%v: a terminal that cannot be set up had commands run: %v", test.env, prefs.commands)
		}
	}
}

func TestATerminalThatAlreadyReportsShiftReturnIsLeftAlone(t *testing.T) {
	t.Parallel()

	prefs := &preferences{}
	host := SetupHost{Env: environment(map[string]string{"TERM_PROGRAM": "iTerm.app"}), Home: t.TempDir(), Runner: prefs}
	said, err := SetUpTerminal(context.Background(), host, true)
	if err != nil || !strings.Contains(said, "already reports shift-return") || len(prefs.commands) != 0 {
		t.Fatalf("SetUpTerminal = %v, %v commands:\n%s", err, prefs.commands, said)
	}
}
