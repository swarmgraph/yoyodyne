package console

import (
	"strings"
	"unicode"
)

// Command is one thing the conversation carries out when its name is typed,
// offered in the list a slash opens on an empty prompt. The console knows
// nothing about what any of them does: the conversation hands the list over,
// from the same text /help prints, so the list and /help cannot name different
// commands.
type Command struct {
	// Name is what is typed, slash included.
	Name string
	// Description is the one line /help says about it.
	Description string
	// Argument says the command needs something typed after its name, so
	// completing it leaves a space to type that into.
	Argument bool
}

// maxMenuRows bounds how many commands the list shows at once. More than that
// is a list scrolled with the arrow keys rather than one that takes the window.
const maxMenuRows = 10

// OfferCommands hands the console the commands a slash at the start of an
// empty prompt lists. Typing narrows the list to the names that begin with what
// has been typed, up and down move through it, Tab completes the name under the
// marker, return completes it where it is not already what was typed, and Esc
// puts the list away leaving the text as it is.
//
// Only a terminal lists anything. A stream has no screen to keep a list on and
// no keystrokes to move through one, so everywhere else this does nothing and
// a line is read exactly as it was.
func OfferCommands(c Console, commands ...Command) {
	t, ok := c.(*terminal)
	if !ok {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.commands = append([]Command(nil), commands...)
}

// menuMatches is the commands the list shows now: those whose name begins with
// what has been typed, in the order they were handed over. It is empty unless a
// slash typed on an empty line opened the list and the line is still only a
// name, so a message that happens to begin with a slash, a recalled command,
// and a command with its argument being typed are left alone.
func (t *terminal) menuMatches() []Command {
	if !t.menuOpen || !t.prompting || t.choosing || t.entering || len(t.line) == 0 || t.line[0] != '/' {
		return nil
	}
	for _, character := range t.line {
		if unicode.IsSpace(character) {
			return nil
		}
	}
	typed := strings.ToLower(string(t.line))
	var matches []Command
	for _, command := range t.commands {
		if strings.HasPrefix(command.Name, typed) {
			matches = append(matches, command)
		}
	}
	return matches
}

// openMenu decides, before a key is applied, whether that key opens the list:
// a slash typed into an empty line does, and anything else on an empty line
// leaves it shut. A line that empties shuts it, and so does stepping through
// earlier messages, because up and down are the list's while it is showing.
func (t *terminal) openMenu(pressed key) {
	if len(t.line) == 0 {
		t.menuOpen = pressed.code == keyRune && pressed.value == '/'
	}
	if pressed.code == keyUp || pressed.code == keyDown {
		t.menuOpen = false
	}
}

// pointMenu puts the marker on the command whose name is exactly what has been
// typed, where there is one, and otherwise on the first, so a name typed in
// full is the one return sends even where a longer name begins with it.
func (t *terminal) pointMenu() {
	t.menuChosen = 0
	for index, command := range t.menuMatches() {
		if strings.EqualFold(command.Name, string(t.line)) {
			t.menuChosen = index
			return
		}
	}
}

// steerMenu is one keystroke's effect on the list, and reports whether the list
// took it. Keys it has no use for are left to the line, so typing and deleting
// go on narrowing it.
func (t *terminal) steerMenu(pressed key) bool {
	matches := t.menuMatches()
	if len(matches) == 0 {
		return false
	}
	if t.menuChosen >= len(matches) {
		t.menuChosen = 0
	}
	switch pressed.code {
	case keyEscape:
		t.menuOpen = false
	case keyUp:
		t.menuChosen = (t.menuChosen + len(matches) - 1) % len(matches)
	case keyDown:
		t.menuChosen = (t.menuChosen + 1) % len(matches)
	case keyTab:
		t.complete(matches[t.menuChosen])
	case keyEnter:
		// A name already typed in full is sent as any line is; return only
		// completes one that is not.
		chosen := matches[t.menuChosen]
		if strings.EqualFold(chosen.Name, string(t.line)) {
			return false
		}
		t.complete(chosen)
	default:
		return false
	}
	return true
}

// complete puts a command's name in the line, followed by a space where it
// needs something typed after it, with the cursor at the end.
func (t *terminal) complete(command Command) {
	t.line = []rune(command.Name)
	if command.Argument {
		t.line = append(t.line, ' ')
	}
	t.cursor = len(t.line)
	t.pointMenu()
}

// menuRegion is the rows the list occupies under the line being composed, no
// more of them than room allows: a marker against the command return or Tab
// would take, each name padded to the longest, and its description. Every row
// is cut to one column short of the width, so none of them wraps and the list
// is exactly as many rows as it has lines.
func (t *terminal) menuRegion(width, room int) []string {
	matches := t.menuMatches()
	if len(matches) == 0 || room < 1 || width < 2 {
		return nil
	}
	shown := min(len(matches), maxMenuRows, room)
	chosen := min(t.menuChosen, len(matches)-1)
	// The rows shown are a window over the list that keeps the marker in it.
	first := 0
	if chosen >= shown {
		first = chosen - shown + 1
	}
	longest := 0
	for _, command := range matches {
		longest = max(longest, len([]rune(command.Name)))
	}
	lines := make([]string, 0, shown)
	for index := first; index < first+shown; index++ {
		command := matches[index]
		marker := unchosenMarker
		if index == chosen {
			marker = chosenMarker
		}
		name := marker + command.Name + strings.Repeat(" ", longest-len([]rune(command.Name)))
		description := "  " + command.Description
		nameRunes := []rune(name)
		if len(nameRunes) >= width-1 {
			lines = append(lines, string(nameRunes[:width-1]))
			continue
		}
		descriptionRunes := []rune(description)
		if room := width - 1 - len(nameRunes); len(descriptionRunes) > room {
			descriptionRunes = descriptionRunes[:room]
		}
		lines = append(lines, name+t.theme.Detail(string(descriptionRunes)))
	}
	return lines
}
