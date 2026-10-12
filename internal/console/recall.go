package console

// maxRecalled bounds how many earlier messages up can step back through. A
// conversation that has run for months would otherwise hold every message it
// was ever sent in memory for the sake of a key that is pressed a few times.
const maxRecalled = 1000

// Remember adds messages to the ones up recalls, oldest first, each after any
// already remembered. The conversation hands over what was sent before this
// console opened and then each message as it is sent, so the earlier messages
// come back whichever process sent them. A message that repeats the one before
// it is remembered once, as is an empty one never: pressing up past either
// would be a keystroke that changed nothing on screen.
//
// Only a terminal recalls anything. A stream has no keys to recall with, so
// everywhere else this does nothing.
func Remember(c Console, messages ...string) {
	t, ok := c.(*terminal)
	if !ok {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.remember(messages...)
}

func (t *terminal) remember(messages ...string) {
	writing := t.recalled == len(t.recall)
	for _, message := range messages {
		if message == "" || (len(t.recall) > 0 && t.recall[len(t.recall)-1] == message) {
			continue
		}
		t.recall = append(t.recall, message)
	}
	if dropped := len(t.recall) - maxRecalled; dropped > 0 {
		t.recall = append([]string(nil), t.recall[dropped:]...)
		t.recalled -= dropped
		if t.recalled < 0 {
			t.recalled = 0
		}
	}
	// What is on screen stays what it was: the message being written is still
	// the one being written, rather than becoming the last one remembered.
	if writing {
		t.recalled = len(t.recall)
	}
}

// forget puts the line back to the message being written, with nothing
// recalled into it and nothing kept aside, which is where every prompt starts
// once a message has been sent or abandoned.
func (t *terminal) forget() {
	t.recalled = len(t.recall)
	t.draft = nil
	t.draftCursor = 0
}

// up moves the cursor to the line above in a message of more than one, keeping
// its column where that line is long enough. On the first line there is no line
// above, so it recalls the message sent before the one on screen instead.
func (t *terminal) up() {
	start := lineStart(t.line, t.cursor)
	if start == 0 {
		t.recallEarlier()
		return
	}
	column := t.cursor - start
	above := lineStart(t.line, start-1)
	t.cursor = min(above+column, start-1)
}

// down is up the other way: the line below, or on the last line the message
// sent after the one on screen, and after the last of those the message that
// was being written before up was first pressed.
func (t *terminal) down() {
	end := lineEnd(t.line, t.cursor)
	if end == len(t.line) {
		t.recallLater()
		return
	}
	column := t.cursor - lineStart(t.line, t.cursor)
	below := end + 1
	t.cursor = min(below+column, lineEnd(t.line, below))
}

// recallEarlier puts the message before the one on screen into the line, with
// the cursor at its end, which is on its last line: another up from there
// climbs through it rather than past it. What was being written is kept aside
// the first time, exactly as it stood, so coming back down finds it intact.
func (t *terminal) recallEarlier() {
	if t.recalled == 0 {
		return
	}
	if t.recalled == len(t.recall) {
		t.draft = append([]rune(nil), t.line...)
		t.draftCursor = t.cursor
	}
	t.recalled--
	t.line = []rune(t.recall[t.recalled])
	t.cursor = len(t.line)
}

// recallLater puts the message after the one on screen into the line, with the
// cursor at the end of its first line, so another down walks through it the
// way up walked through the one before. Past the last message sent is the one
// that was being written. A recalled message the operator changed and then
// moved away from is not kept: the history is what was sent, and only the
// message they had not sent yet is theirs to lose.
func (t *terminal) recallLater() {
	if t.recalled >= len(t.recall) {
		return
	}
	t.recalled++
	if t.recalled == len(t.recall) {
		t.line = t.draft
		t.cursor = t.draftCursor
		t.draft = nil
		t.draftCursor = 0
		return
	}
	t.line = []rune(t.recall[t.recalled])
	t.cursor = lineEnd(t.line, 0)
}

// lineStart is where the line holding index begins: just after the newline
// before it, or the start of the message.
func lineStart(line []rune, index int) int {
	for index > 0 && line[index-1] != '\n' {
		index--
	}
	return index
}

// lineEnd is where the line holding index ends: at the newline after it, or the
// end of the message.
func lineEnd(line []rune, index int) int {
	for index < len(line) && line[index] != '\n' {
		index++
	}
	return index
}
