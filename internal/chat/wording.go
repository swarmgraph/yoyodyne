package chat

import (
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// RenderReply projects a reply for a person without changing the text kept in
// the conversation or the structured reply returned to a script.
func (s *Session) RenderReply(text string) string {
	return readmodel.ReadTextTerms(s.options.Repository).Render(s.workItemTitles().Cite(text))
}

func (s *Session) replyStream(out console.Console) *replyStream {
	stream := newReplyStream(out, s.theme, readmodel.ReadTextTerms(s.options.Repository))
	if stream != nil {
		stream.cite = s.RenderReply
	}
	return stream
}

// replyWording checks the person-readable parts after the reply's structured
// blocks have been decoded, so a lane report's JSON fence does not hide its
// summary. Findings travel beside the text to the pass; no author record is
// rewritten, and a finding refuses no action.
func (s *Session) replyWording(parsed parsedReply) []terms.Finding {
	words := readmodel.ReadTextTerms(s.options.Repository)
	found := words.Find(parsed.Prose)
	if parsed.LaneReport != nil {
		found = terms.MergeFindings(found, words.LaneReport(*parsed.LaneReport))
	}
	for _, reported := range parsed.Reports {
		found = terms.MergeFindings(found, words.Find(reported.Message))
	}
	return found
}
