package mcpcall

// The block an agent asks with (docs/designs/mcp-tools-through-the-harness.md,
// sections 2 and 3). It is read here, beside the path it feeds, so a
// conversation role and a developer run read the same block by the same rules.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// Fence opens the block a reply asks for MCP calls in.
const Fence = "```yoyodyne-mcp"

// MaxBlockBytes bounds the block's payload: as many calls as any reply may ask
// for, each with its largest arguments, and room for the rest of each request.
const MaxBlockBytes = 128 << 10

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

type block struct {
	Requests []Request `json:"requests"`
}

// ReadAsk reads the one block a reply asks with. A reply with no block asks for
// nothing. A block that cannot be read is still an ask: the reply was not an
// answer, and what it asked for is refused whole with the reason, rather than
// half-read.
func ReadAsk(reply string) Ask {
	split, err := fenced.Split(reply, Fence, "MCP")
	if err != nil {
		return Ask{Found: true, Problem: err.Error()}
	}
	if !split.Found {
		return Ask{}
	}
	requests, err := decodeBlock(split.Payload)
	if err != nil {
		return Ask{Found: true, Problem: err.Error()}
	}
	return Ask{Found: true, Requests: requests}
}

func decodeBlock(payload string) ([]Request, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("the MCP block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return nil, fmt.Errorf("the MCP block is %d bytes, over its limit of %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var decoded block
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("the MCP block is not the shape it takes: %v", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the MCP block carries something after its one object")
	}
	if len(decoded.Requests) == 0 {
		return nil, errors.New("the MCP block asks for no calls")
	}
	for index, request := range decoded.Requests {
		if strings.TrimSpace(request.Tool) == "" {
			return nil, fmt.Errorf("request %d in the MCP block names no tool", index+1)
		}
	}
	return decoded.Requests, nil
}
