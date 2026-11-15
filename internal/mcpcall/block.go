package mcpcall

// The block a developer run asks with (docs/designs/mcp-tools-through-the-harness.md,
// section 3). It is read by Extract and Decode in describe.go, the same rules a
// conversation role's block is read by, so the two read one block one way.

// Ask is what a reply's block asked for.
type Ask struct {
	// Found says the reply carried a block at all. Most replies carry none.
	Found bool
	// Requests is what the block asked for, in its order. It is empty when the
	// block could not be read.
	Requests []Request
	// Problem says why a block that was found could not be read; nothing in it
	// is called.
	Problem string
}

// ReadAsk reads the one block a reply asks with. A reply with no block asks for
// nothing. A block that cannot be read is still an ask: the reply was not an
// answer, and what it asked for is refused whole with the reason, rather than
// half-read.
func ReadAsk(reply string) Ask {
	_, requests, found, err := Extract(reply)
	if !found && err == nil {
		return Ask{}
	}
	if err != nil {
		return Ask{Found: true, Problem: err.Error()}
	}
	return Ask{Found: true, Requests: requests}
}
