package chat

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

// TestTheCommandMenuAndHelpNameTheSameCommands is the promise the list a slash
// opens makes: every command /help prints is in it, described by the line /help
// gives it, and nothing is in it that /help does not print.
func TestTheCommandMenuAndHelpNameTheSameCommands(t *testing.T) {
	t.Parallel()

	session := &Session{}
	var help bytes.Buffer
	if _, err := session.dispatch(context.Background(), "/help", &help); err != nil {
		t.Fatalf("/help: %v", err)
	}
	var printed []string
	for _, line := range strings.Split(help.String(), "\n") {
		if !strings.HasPrefix(line, "  /") {
			continue
		}
		name, _ := splitCommand(strings.Fields(line)[0])
		if !slices.Contains(printed, name) {
			printed = append(printed, name)
		}
	}
	if len(printed) == 0 {
		t.Fatalf("/help printed no commands:\n%s", help.String())
	}

	menu := commandMenu()
	var listed []string
	for _, command := range menu {
		listed = append(listed, command.Name)
		if command.Description == "" {
			t.Errorf("%s is listed with no description", command.Name)
		}
		if !strings.Contains(help.String(), command.Description) {
			t.Errorf("%s is described as %q, which /help does not say", command.Name, command.Description)
		}
	}
	if !slices.Equal(listed, printed) {
		t.Fatalf("the menu lists %v and /help prints %v", listed, printed)
	}
}

// TestTheCommandMenuListsOnlyWhatTheConversationCarriesOut holds the list to the
// dispatcher too, so a command added to one and not the other is caught: a name
// the menu completes that nothing carries out is refused once sent, and a
// command carried out that the menu and /help never mention is one nobody finds.
// /quit is the one exception, the second spelling of /exit, which /help leaves
// out so the list says it once.
func TestTheCommandMenuListsOnlyWhatTheConversationCarriesOut(t *testing.T) {
	t.Parallel()

	dispatched := dispatchedCommands(t)
	var listed []string
	for _, command := range commandMenu() {
		listed = append(listed, command.Name)
		if !containsCommand(dispatched, command.Name) {
			t.Errorf("the menu lists %s, which the conversation does not carry out", command.Name)
		}
	}
	for _, name := range dispatched {
		if name == "/quit" {
			continue
		}
		if !slices.Contains(listed, name) {
			t.Errorf("the conversation carries out %s, which neither /help nor the menu lists", name)
		}
	}
}

// TestTheCommandMenuDescribesEachCommandByItsOwnLine covers the forms of an entry
// /help has: a description on the same line, one on the line under a long form,
// and a command shown in several forms, listed once by the first. A command
// whose form needs an argument completes with a space to type it into, and one
// whose argument may be left out completes without.
func TestTheCommandMenuDescribesEachCommandByItsOwnLine(t *testing.T) {
	t.Parallel()

	menu := commandMenu()
	find := func(name string) (int, bool) {
		for index, command := range menu {
			if command.Name == name {
				return index, true
			}
		}
		return 0, false
	}
	cases := []struct {
		name, description string
		argument          bool
	}{
		{"/status", "what is in flight, claimed, blocked, available, and done, with what the done work cost", false},
		{"/redirect", "record direction on an item, stopping it first if it is running", true},
		{"/directive", "record a directive that takes effect now", true},
		{"/stop", "stop one item's run wherever it is running, and settle what it left", false},
		{"/show", "one bounded work item read, as the tracker holds it, and what each run for it cost", true},
	}
	for _, want := range cases {
		index, ok := find(want.name)
		if !ok {
			t.Errorf("%s is not listed", want.name)
			continue
		}
		got := menu[index]
		if got.Description != want.description || got.Argument != want.argument {
			t.Errorf("%s = %+v, want description %q and argument %v", want.name, got, want.description, want.argument)
		}
	}
	count := 0
	for _, command := range menu {
		if command.Name == "/directive" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("/directive is listed %d times, want once", count)
	}
}
