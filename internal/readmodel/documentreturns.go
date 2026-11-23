package readmodel

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DocumentReturnsWait is one document revision held for the development manager
// because automatic publication of its document stopped after returned runs
// (runstate.DocumentReturnsHold): which document, the role that wrote it, and
// why it is held. It is read off the writer's conversation, so her decision
// takes it off the list by itself.
type DocumentReturnsWait struct {
	// Reference is what her held_document action names: the conversation, a
	// slash, and the write.
	Reference string           `json:"reference"`
	Document  string           `json:"document"`
	Title     string           `json:"title,omitempty"`
	Owner     domain.AgentRole `json:"owner"`
	Why       string           `json:"why"`
	LastRun   string           `json:"last_run,omitempty"`
	Since     time.Time        `json:"since"`
	// Findings is what the last returned run said, Reason the writer's reason
	// for the revision, and Body its text, cut to maxHeldDocumentBody. They are
	// in her list and not in the status line's JSON.
	Findings string `json:"-"`
	Reason   string `json:"-"`
	Section  string `json:"-"`
	Body     string `json:"-"`
	// Line is Says as the reading worded it, for the dashboard.
	Line string `json:"says"`
}

const (
	// maxHeldDocumentBody bounds each revision's text on her list, and
	// maxHeldDocumentsListed how many are listed in full.
	maxHeldDocumentBody    = 6 << 10
	maxHeldDocumentsListed = 6
)

// Says is the held document in the words the status prints.
func (w DocumentReturnsWait) Says() string {
	last := ""
	if w.LastRun != "" {
		last = fmt.Sprintf(" (the last returned run is %s)", w.LastRun)
	}
	return fmt.Sprintf("document %s (%s), the %s's, held since %s: automatic publication stopped after %d returned runs%s, and %s",
		w.Document, w.Reference, w.Owner.Title(), localMoment(w.Since), runstate.MaxDocumentReturns, last, w.Why)
}

// Next is whose move the held document is.
func (w DocumentReturnsWait) Next() string {
	return "the development manager releases it for one reviewed run or declines it, and nothing is asked of anybody else"
}

// HeldDocuments is every revision held for the development manager in the given
// conversations, longest held first.
func HeldDocuments(conversations []runstate.Conversation) []DocumentReturnsWait {
	var held []DocumentReturnsWait
	for _, conversation := range conversations {
		for _, write := range conversation.PendingWrites {
			if !write.AwaitsDevelopmentManager() {
				continue
			}
			keptIn := fmt.Sprintf("conversation %s keeps all of it as %s", conversation.ConversationID, write.ID)
			wait := DocumentReturnsWait{
				Reference: documentApprovalID(conversation.ConversationID, write.ID),
				Document:  strings.TrimSpace(write.Artifact), Title: strings.TrimSpace(write.Title), Owner: conversation.Role,
				Why: write.ReturnsHold.Why, LastRun: write.ReturnsHold.LastRun, Since: write.ReturnsHold.HeldAt,
				Findings: write.ReturnedFindings, Reason: strings.TrimSpace(write.Reason), Section: strings.TrimSpace(write.Section),
				Body: runstate.FitText(write.Body, maxHeldDocumentBody, keptIn),
			}
			wait.Line = wait.Says()
			held = append(held, wait)
		}
	}
	sort.SliceStable(held, func(i, j int) bool { return held[i].Since.Before(held[j].Since) })
	return held
}

// readHeldDocuments reads the held documents from the recorded conversations.
func readHeldDocuments(sources Sources) ([]DocumentReturnsWait, string) {
	if sources.Conversations == nil {
		return nil, ""
	}
	recorded, err := sources.Conversations.Recorded()
	if err != nil {
		return nil, fmt.Sprintf("the documents held for the development manager could not be read: %v", err)
	}
	return HeldDocuments(recorded), ""
}

// RenderHeldDocuments is the development manager's list of held documents, with
// what she judges each by, and empty where there is none.
func RenderHeldDocuments(held []DocumentReturnsWait) string {
	if len(held) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("## Documents held for your decision\n\nEach is a revision its writer wrote after automatic publication of the document stopped. Decide each with \"held_document\": \"release\" for one reviewed run, or \"decline\" with a reason its writer reads.\n")
	for i, wait := range held {
		fmt.Fprintf(&out, "\n- %s\n", wait.Says())
		if i >= maxHeldDocumentsListed {
			out.WriteString("  Its text is left out here for room; it is listed in full once the ones above are decided.\n")
			continue
		}
		if wait.Findings != "" {
			out.WriteString("  What came back last time:\n" + quoted(wait.Findings))
		}
		if wait.Reason != "" {
			out.WriteString("  The writer's reason for this revision:\n" + quoted(wait.Reason))
		}
		if wait.Section != "" {
			fmt.Fprintf(&out, "  It replaces the section %q.\n", wait.Section)
		}
		out.WriteString("  The revision:\n" + quoted(wait.Body))
	}
	return out.String()
}

// quoted sets text apart from the list around it, line by line.
func quoted(text string) string {
	return "  > " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  > ") + "\n"
}
