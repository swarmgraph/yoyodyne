package action

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/capability"
)

// Tool describes the conversation door onto a trusted action. Grants are the
// action's capabilities; descriptions and configured personas grant nothing.
type Tool struct {
	ID         capability.Capability
	Block      string
	Class      ToolClass
	Parameters string
	Bounds     ToolBounds
	Scope      string
	Gates      string
	Framing    string
}

type ToolClass string

const (
	ToolRead  ToolClass = "read"
	ToolSpeak ToolClass = "speak"
	ToolAct   ToolClass = "act"
)

// Zero byte bounds mean the tool returns no evidence. Requests and rounds must
// always be bounded, including tools whose result is only an acknowledgement.
type ToolBounds struct {
	RequestsPerReply int `json:"requests_per_reply"`
	RoundsPerMessage int `json:"rounds_per_message"`
	BytesPerRequest  int `json:"bytes_per_request"`
	BytesPerReply    int `json:"bytes_per_reply"`
}

func (t Tool) Validate(name string, requires []capability.Capability) error {
	var problems []error
	if !t.ID.Known() || name != string(t.ID) || !slices.Contains(requires, t.ID) {
		problems = append(problems, errors.New("tool identity must be a known required capability and the action name"))
	}
	if !strings.HasPrefix(t.Block, "yoyodyne-") || strings.ContainsAny(t.Block, " \n\r\t`") {
		problems = append(problems, errors.New("tool block must be a yoyodyne fence name"))
	}
	if t.Class != ToolRead && t.Class != ToolSpeak && t.Class != ToolAct {
		problems = append(problems, errors.New("tool class must be read, speak, or act"))
	}
	b := t.Bounds
	if b.RequestsPerReply < 1 || b.RoundsPerMessage < 1 || b.BytesPerRequest < 0 || b.BytesPerReply < b.BytesPerRequest {
		problems = append(problems, errors.New("tool bounds must bound requests and rounds and contain the per-request byte limit"))
	}
	if t.Class == ToolRead && b.BytesPerRequest == 0 {
		problems = append(problems, errors.New("a read tool must bound returned evidence"))
	}
	if strings.TrimSpace(t.Parameters) == "" || strings.TrimSpace(t.Scope) == "" || strings.TrimSpace(t.Gates) == "" || strings.TrimSpace(t.Framing) == "" {
		problems = append(problems, errors.New("tool parameters, scope, gates, and framing are required"))
	}
	return errors.Join(problems...)
}

// Contract is generated, so changing a descriptor changes the next turn's
// instructions without changing a persona or another copy of the bounds.
//
// A role copies its instructions' wording into what people read, so every
// label and limit here is said in ordinary words rather than in the names the
// descriptor's fields carry.
func (t Tool) Contract(summary string) string {
	return fmt.Sprintf("## %s (%s)\n\n%s Use the `%s` block. %s\n\nLimits: %s\n\nWhat it reaches: %s.\n\nWhat the harness still checks: %s\n\n%s\n", t.ID, classWords[t.Class], summary, t.Block, t.Parameters, t.Bounds.words(), t.Scope, t.Gates, t.Framing)
}

// classWords says what each class of tool does, for the heading a role reads.
var classWords = map[ToolClass]string{
	ToolRead:  "reads, and returns what it found",
	ToolSpeak: "records what you say",
	ToolAct:   "makes a change",
}

// words is the bounds as a sentence a role can repeat to a person.
func (b ToolBounds) words() string {
	said := fmt.Sprintf("at most %s in one reply, and at most %s of requests and results for one message.",
		counted(b.RequestsPerReply, "request", "requests"), counted(b.RoundsPerMessage, "round", "rounds"))
	if b.BytesPerRequest == 0 {
		return said + " It returns nothing for you to read."
	}
	return said + fmt.Sprintf(" Each request returns at most %d bytes, and all the requests in one reply at most %d bytes together.", b.BytesPerRequest, b.BytesPerReply)
}

func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
