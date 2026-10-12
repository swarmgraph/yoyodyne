package console

// A terminal that will not report shift-return can often be told what to send
// for it instead. The setup below binds shift-return, in the terminal's own
// settings, to the two bytes alt-return already sends — escape, then return —
// which decodeEscape reads as a new line on every terminal and in every mode,
// so nothing has to be negotiated for the binding to work. Return alone is left
// exactly as it was, and still sends.
//
// Every terminal is changed the same way it would be by hand: iTerm2 through
// the preferences system it reads its key bindings from, and VS Code through
// the keybindings file it watches. A binding somebody already put on
// shift-return is theirs and is left alone, and a binding this already made is
// recognised, so running the setup again changes nothing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// newlineSequence is what shift-return is set up to send: alt-return's bytes.
const newlineSequence = "\x1b\r"

// SetupHost is the machine a terminal is set up on. Its zero value is the one
// this process runs on.
type SetupHost struct {
	// Env reads one environment variable. A nil Env reads the process's own.
	Env func(string) string
	// Home is the user's home directory. Empty asks the operating system.
	Home string
	// GOOS is the operating system the paths are chosen for. Empty is this one.
	GOOS string
	// Runner runs the preferences commands iTerm2 is set up with. Nil runs them.
	Runner execution.ProcessRunner
}

func (h SetupHost) env(name string) string {
	if h.Env == nil {
		return os.Getenv(name)
	}
	return h.Env(name)
}

func (h SetupHost) goos() string {
	if h.GOOS == "" {
		return runtime.GOOS
	}
	return h.GOOS
}

func (h SetupHost) home() (string, error) {
	if h.Home != "" {
		return h.Home, nil
	}
	return os.UserHomeDir()
}

func (h SetupHost) runner() execution.ProcessRunner {
	if h.Runner == nil {
		return execution.OSProcessRunner{}
	}
	return h.Runner
}

// ReportsShiftReturn says whether this console's terminal agreed to report
// shift-return, which is what makes setting the terminal up unnecessary.
func ReportsShiftReturn(c Console) bool {
	t, ok := c.(*terminal)
	return ok && t.keyboard != keyboardLegacy
}

// workingKeys is what adds a new line where shift-return does not, said the
// same way wherever the setup cannot help.
const workingKeys = `Alt-return adds a new line, and so does ending a line with \. Return sends.`

// SetUpTerminal sets up the terminal this conversation is typed in so that
// shift-return reaches it as a new line, and says what it found and what it
// changed. reported is whether the terminal already reports shift-return, in
// which case there is nothing to change.
func SetUpTerminal(ctx context.Context, host SetupHost, reported bool) (string, error) {
	kind := identifyTerminal(host)
	var out strings.Builder
	fmt.Fprintf(&out, "This is %s.\n", kind.name)
	if reported {
		fmt.Fprintf(&out, "It already reports shift-return to this console, so shift-return adds a new line and there is nothing to change.\n")
		return out.String(), nil
	}
	if kind.setUp == nil {
		fmt.Fprintf(&out, "It cannot be set up to send anything of yoyo's choosing for shift-return, so shift-return sends like return. %s\n", workingKeys)
		if kind.hint != "" {
			fmt.Fprintln(&out, kind.hint)
		}
		return out.String(), nil
	}
	err := kind.setUp(ctx, host, &out)
	return out.String(), err
}

// terminalKind is one terminal as the setup knows it: what it is called, and
// how it is set up where it can be.
type terminalKind struct {
	name  string
	hint  string
	setUp func(ctx context.Context, host SetupHost, out *strings.Builder) error
}

// identifyTerminal names the terminal from what it says about itself in the
// environment it started this process with.
func identifyTerminal(host SetupHost) terminalKind {
	switch program := host.env("TERM_PROGRAM"); program {
	case "iTerm.app":
		return terminalKind{name: "iTerm2", setUp: setUpITerm}
	case "Apple_Terminal":
		return terminalKind{
			name: "Terminal.app",
			hint: "Alt-return is option and return once Use Option as Meta key is ticked, in Terminal's Settings, under Profiles and then Keyboard.",
		}
	case "vscode":
		if edition, ok := vscodeEditor(host); ok {
			return terminalKind{
				name: "the terminal in " + edition.name,
				setUp: func(_ context.Context, host SetupHost, out *strings.Builder) error {
					return setUpVSCode(host, edition, out)
				},
			}
		}
		return terminalKind{name: "the terminal in an editor built on VS Code that yoyo does not know"}
	case "":
		if host.env("TERM") == "xterm-kitty" || host.env("KITTY_WINDOW_ID") != "" {
			return terminalKind{name: "kitty"}
		}
		return terminalKind{name: "a terminal that does not say what it is"}
	default:
		return terminalKind{name: "a terminal yoyo does not know, which calls itself " + program}
	}
}

// iTermDomain is where iTerm2 keeps its preferences, and iTermShiftReturn the
// global key binding for return held with shift: the key's character, the
// modifier flag for shift, and the key's code, as iTerm2 writes them itself.
// iTermLegacyShiftReturn is the same binding written by older versions, which
// leave the key's code out.
const (
	iTermDomain            = "com.googlecode.iterm2"
	iTermKeyMap            = "GlobalKeyMap"
	iTermShiftReturn       = "0xd-0x20000-0x24"
	iTermLegacyShiftReturn = "0xd-0x20000"
	// iTermSendHex is iTerm2's action for sending the bytes a binding names,
	// written as hexadecimal codes.
	iTermSendHex  = 11
	iTermSendText = "0x1b 0x0d"
)

// iTermBinding is one entry in iTerm2's global key bindings.
type iTermBinding struct {
	Action int    `json:"Action"`
	Text   string `json:"Text"`
}

// setupCommandTimeout bounds one preferences command, which answers at once.
const setupCommandTimeout = 10 * time.Second

func setUpITerm(ctx context.Context, host SetupHost, out *strings.Builder) error {
	home, err := host.home()
	if err != nil {
		return fmt.Errorf("the home directory, where iTerm2 keeps its settings, could not be found: %w", err)
	}
	settings := filepath.Join(home, "Library", "Preferences", iTermDomain+".plist")
	bindings, err := readITermBindings(ctx, host.runner(), settings)
	if err != nil {
		return err
	}
	want := iTermBinding{Action: iTermSendHex, Text: iTermSendText}
	for _, name := range []string{iTermShiftReturn, iTermLegacyShiftReturn} {
		existing, ok := bindings[name]
		if !ok {
			continue
		}
		if existing == want {
			fmt.Fprintf(out, "Shift-return in iTerm2 already sends escape and then return, which adds a new line here, so nothing was changed. If it still sends, quit iTerm2 and open it again.\n")
			return nil
		}
		fmt.Fprintf(out, "Shift-return already has a key binding of its own in iTerm2's settings (Settings, then Keys, then Key Bindings), so it was left alone. %s\n", workingKeys)
		return nil
	}
	fmt.Fprintf(out, "Changing: iTerm2's global key bindings (Settings, then Keys, then Key Bindings), to make shift-return send escape and then return, which this console reads as a new line. Return alone still sends.\n")
	value := fmt.Sprintf("<dict><key>Action</key><integer>%d</integer><key>Text</key><string>%s</string></dict>", want.Action, want.Text)
	result, err := host.runner().Run(ctx, execution.Command{
		Name:    "defaults",
		Args:    []string{"write", iTermDomain, iTermKeyMap, "-dict-add", iTermShiftReturn, value},
		Timeout: setupCommandTimeout,
	}, nil)
	if err == nil && result.Status != execution.ProcessSucceeded {
		err = errors.New(strings.TrimSpace(result.Stderr + " " + result.Stdout))
	}
	if err != nil {
		return fmt.Errorf("iTerm2's key bindings could not be changed: %w", err)
	}
	fmt.Fprintf(out, "Done. Quit iTerm2 and open it again for shift-return to add a new line.\n")
	return nil
}

// readITermBindings reads iTerm2's global key bindings from its settings file.
// A file with no global bindings in it has none yet, which is not a failure.
func readITermBindings(ctx context.Context, runner execution.ProcessRunner, settings string) (map[string]iTermBinding, error) {
	result, err := runner.Run(ctx, execution.Command{
		Name:    "plutil",
		Args:    []string{"-extract", iTermKeyMap, "json", "-o", "-", settings},
		Timeout: setupCommandTimeout,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("iTerm2's settings in %s could not be read: %w", settings, err)
	}
	if result.Status != execution.ProcessSucceeded {
		said := strings.TrimSpace(result.Stderr + " " + result.Stdout)
		if strings.Contains(said, "No value at that key path") {
			return map[string]iTermBinding{}, nil
		}
		return nil, fmt.Errorf("iTerm2's settings in %s could not be read: %s", settings, said)
	}
	// A binding this does not recognise may hold something other than an action
	// and a text, so each one is read on its own and one that does not fit is
	// kept as a binding that is not this one rather than refusing the lot.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return nil, fmt.Errorf("iTerm2's key bindings in %s could not be read: %w", settings, err)
	}
	bindings := make(map[string]iTermBinding, len(raw))
	for name, value := range raw {
		var binding iTermBinding
		if json.Unmarshal(value, &binding) != nil {
			binding = iTermBinding{Action: -1}
		}
		bindings[name] = binding
	}
	return bindings, nil
}

// vscodeEdition is one of the editors whose terminal is set up through a
// keybindings file, and the directory under the user's application settings
// that file lives in.
type vscodeEdition struct {
	name      string
	directory string
}

// vscodeEditor says which edition of VS Code the terminal is running in. Every
// editor built on VS Code calls its terminal vscode, so on a Mac the
// application's own identifier decides, and an editor that is not one of these
// is not given another editor's file.
func vscodeEditor(host SetupHost) (vscodeEdition, bool) {
	edition := vscodeEdition{name: "VS Code", directory: "Code"}
	if strings.HasSuffix(host.env("TERM_PROGRAM_VERSION"), "-insider") {
		edition = vscodeEdition{name: "VS Code Insiders", directory: "Code - Insiders"}
	}
	switch host.env("__CFBundleIdentifier") {
	case "":
	case "com.microsoft.VSCode":
		edition = vscodeEdition{name: "VS Code", directory: "Code"}
	case "com.microsoft.VSCodeInsiders":
		edition = vscodeEdition{name: "VS Code Insiders", directory: "Code - Insiders"}
	default:
		return vscodeEdition{}, false
	}
	return edition, true
}

// vscodeBinding is the keybinding that makes shift-return in VS Code's terminal
// send alt-return's bytes. It applies only while the terminal has the focus, so
// shift-return in an editor tab is untouched.
type vscodeBinding struct {
	Key     string            `json:"key"`
	Command string            `json:"command"`
	Args    map[string]string `json:"args"`
	When    string            `json:"when"`
}

var vscodeShiftReturn = vscodeBinding{
	Key:     "shift+enter",
	Command: "workbench.action.terminal.sendSequence",
	Args:    map[string]string{"text": newlineSequence},
	When:    "terminalFocus",
}

// vscodeKeybindings is where an edition keeps its keybindings file, relative to
// the home directory.
func vscodeKeybindings(goos string, edition vscodeEdition) string {
	if goos == "darwin" {
		return filepath.Join("Library", "Application Support", edition.directory, "User", "keybindings.json")
	}
	return filepath.Join(".config", edition.directory, "User", "keybindings.json")
}

func setUpVSCode(host SetupHost, edition vscodeEdition, out *strings.Builder) error {
	home, err := host.home()
	if err != nil {
		return fmt.Errorf("the home directory, where %s keeps its keybindings, could not be found: %w", edition.name, err)
	}
	relative := vscodeKeybindings(host.goos(), edition)
	path := filepath.Join(home, relative)
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s's keybindings in %s could not be read: %w", edition.name, path, err)
	}
	updated, state, err := addVSCodeBinding(string(content))
	if err != nil {
		return fmt.Errorf("%s's keybindings in %s could not be read: %w", edition.name, path, err)
	}
	switch state {
	case bindingPresent:
		fmt.Fprintf(out, "Shift-return in %s's terminal already sends escape and then return, which adds a new line here, so nothing was changed.\n", edition.name)
		return nil
	case bindingTaken:
		fmt.Fprintf(out, "Shift-return in %s's terminal already has a keybinding of its own in %s, so it was left alone. %s\n", edition.name, path, workingKeys)
		return nil
	}
	fmt.Fprintf(out, "Changing: %s, adding a keybinding that makes shift-return in %s's terminal send escape and then return, which this console reads as a new line. Return alone still sends, and shift-return outside the terminal is untouched.\n", path, edition.name)
	// The file is confined to the home directory it was found under, as every
	// write the harness makes is to the root it declares.
	root, err := repowrite.NewRoot(home)
	if err != nil {
		return err
	}
	if _, err := root.WriteFile(filepath.ToSlash(relative), []byte(updated)); err != nil {
		return fmt.Errorf("%s's keybindings in %s could not be changed: %w", edition.name, path, err)
	}
	fmt.Fprintf(out, "Done. %s reads the change at once, so shift-return in its terminal adds a new line from now on.\n", edition.name)
	return nil
}

// bindingState is what a settings file already says about shift-return.
type bindingState int

const (
	bindingAbsent bindingState = iota
	bindingPresent
	bindingTaken
)

// addVSCodeBinding returns the keybindings file with the shift-return binding
// added, and what the file already said about shift-return in the terminal.
// The file is JSON with comments and trailing commas allowed, as VS Code reads
// it, and it is the operator's: the binding is put in front of the list's
// closing bracket, and every comment and line already there is left as it was.
func addVSCodeBinding(content string) (string, bindingState, error) {
	blanked := blankComments(content)
	if strings.TrimSpace(blanked) == "" {
		// A file that is missing, empty, or holds only comments has no list yet.
		entry, err := json.MarshalIndent(vscodeShiftReturn, "    ", "    ")
		if err != nil {
			return "", bindingAbsent, err
		}
		prefix := content
		if prefix != "" && !strings.HasSuffix(prefix, "\n") {
			prefix += "\n"
		}
		return prefix + "[\n    " + string(entry) + "\n]\n", bindingAbsent, nil
	}
	var bindings []map[string]any
	if err := json.Unmarshal([]byte(dropTrailingCommas(blanked)), &bindings); err != nil {
		return "", bindingAbsent, fmt.Errorf("it is not a list of keybindings: %w", err)
	}
	for _, binding := range bindings {
		key, _ := binding["key"].(string)
		when, _ := binding["when"].(string)
		if strings.ReplaceAll(strings.ToLower(key), " ", "") != vscodeShiftReturn.Key || !strings.Contains(when, "terminalFocus") {
			continue
		}
		command, _ := binding["command"].(string)
		args, _ := binding["args"].(map[string]any)
		if text, _ := args["text"].(string); command == vscodeShiftReturn.Command && text == newlineSequence {
			return content, bindingPresent, nil
		}
		return content, bindingTaken, nil
	}
	closing := strings.LastIndexByte(strings.TrimRight(blanked, " \t\r\n"), ']')
	if closing < 0 {
		return "", bindingAbsent, errors.New("it does not end its list of keybindings with a closing bracket")
	}
	// The binding goes straight after the last thing in the list, so a comment
	// between that and the bracket stays where it was, after it.
	last := len(strings.TrimRight(blanked[:closing], " \t\r\n"))
	separator := ","
	if previous := blanked[last-1]; previous == '[' || previous == ',' {
		separator = ""
	}
	entry, err := json.MarshalIndent(vscodeShiftReturn, "    ", "    ")
	if err != nil {
		return "", bindingAbsent, err
	}
	return content[:last] + separator + "\n    " + string(entry) + content[last:], bindingAbsent, nil
}

// blankComments returns the text with every comment replaced by spaces, newlines
// kept, so a position in what is left is the same position in the original.
func blankComments(text string) string {
	blanked := []byte(text)
	for index := 0; index < len(blanked); index++ {
		switch {
		case blanked[index] == '"':
			index = stringEnd(blanked, index)
		case blanked[index] == '/' && index+1 < len(blanked) && blanked[index+1] == '/':
			for ; index < len(blanked) && blanked[index] != '\n'; index++ {
				blanked[index] = ' '
			}
		case blanked[index] == '/' && index+1 < len(blanked) && blanked[index+1] == '*':
			end := strings.Index(string(blanked[index+2:]), "*/")
			stop := len(blanked)
			if end >= 0 {
				stop = index + 2 + end + 2
			}
			for ; index < stop; index++ {
				if blanked[index] != '\n' {
					blanked[index] = ' '
				}
			}
			index--
		}
	}
	return string(blanked)
}

// dropTrailingCommas replaces a comma that closes a list or an object with a
// space, which is the one other thing VS Code reads that JSON does not.
func dropTrailingCommas(text string) string {
	cleaned := []byte(text)
	for index := 0; index < len(cleaned); index++ {
		switch cleaned[index] {
		case '"':
			index = stringEnd(cleaned, index)
		case ',':
			next := strings.TrimLeft(string(cleaned[index+1:]), " \t\r\n")
			if strings.HasPrefix(next, "]") || strings.HasPrefix(next, "}") {
				cleaned[index] = ' '
			}
		}
	}
	return string(cleaned)
}

// stringEnd is the index of the quote closing the string opened at start, or
// the end of the text for a string that never closes.
func stringEnd(text []byte, start int) int {
	for index := start + 1; index < len(text); index++ {
		switch text[index] {
		case '\\':
			index++
		case '"':
			return index
		}
	}
	return len(text)
}
