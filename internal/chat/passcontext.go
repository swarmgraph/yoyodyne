package chat

// What a recurring turn starts from.
//
// A turn the operator opens resumes the conversation's provider session, which
// grows by every turn it takes until it is compacted. A recurring turn does not:
// every turn a recurring task takes starts a provider session of its own, built
// from the role's contract, persona and remit, its memories, the product
// briefing with the product intent whole, what the previous recurring turn said,
// and the turn's own message. Further rounds of the same turn — the harness
// handing back what the role asked for, or the one request for a missing closing
// report — continue that session. The conversation stays the record: every turn
// is still recorded in its event log, and only the provider session is new.
//
// The briefing it carries is contextbundle.PassContext's: the product intent
// whole, with the role's own documents and the rest of the documentation left
// out and listed, for the role to read on demand with a repository read. What a
// turn starts from is bounded by a third of the context window of the provider
// serving it, so the provider is left two thirds of its window to work in
// before it would compact anything. Nothing in the start gives way to the
// bound — not the role's instructions, its memories, the product intent, the
// previous turn's account or the turn's message — so a start over it is refused
// before the provider is asked.
//
// The picture a recurring turn carries goes to its own session, so it is not
// recorded as delivered to the conversation's: a refresh it carried is still
// owed to the next turn the operator opens.
//
// The role's memory holds its judgment, and the previous turn's account holds
// what that turn did and said it would do next: it is kept on the conversation
// record (PassCarriedForward) and given to the next recurring turn, and once to
// the next turn the operator opens, whose session never saw it.
//
// The conversation's own session is set aside while the recurring turn runs and
// put back when it ends (EndPass), so a turn the operator opens afterwards
// resumes the session it always has rather than the recurring turn's.
//
// A provider can still compact the session during a recurring turn, where the
// turn's own rounds outgrow the window. That is recorded on the conversation's
// log, on the reply, and from there on the pass's record.

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// passContextShare is the part of the provider's context window a recurring
// turn may start from: one third, so two thirds are left for the turn's work.
const passContextShare = 3

// bytesPerToken converts a provider's window, stated in tokens, into the bytes
// the harness measures. Four bytes a token is the usual rate for English prose.
const bytesPerToken = 4

// PassStart is what one recurring turn started from: how large it was as the
// provider would receive it, the bound it was held to, the window the bound was
// taken from, and the briefing sections that gave way to fit it.
type PassStart struct {
	Bytes        int                           `json:"bytes"`
	BoundBytes   int                           `json:"bound_bytes"`
	WindowTokens int                           `json:"window_tokens"`
	GaveWay      []contextbundle.FittedSection `json:"gave_way,omitempty"`
}

// PassContextTooLarge is a recurring turn refused before sending because what
// never gives way is over the bound on what it may start from. It names each
// part's size, because what has to change is one of them.
type PassContextTooLarge struct {
	Bytes             int
	BoundBytes        int
	WindowTokens      int
	InstructionsBytes int
	BriefingBytes     int
	RestBytes         int
}

func (e *PassContextTooLarge) Error() string {
	return fmt.Sprintf("this recurring turn was not sent: what it starts from is %d bytes, and a recurring turn may start from at most %d, "+
		"a third of the %d tokens the provider's window holds at about %d bytes a token. Nothing left in it gives way: the role's own instructions are %d bytes, "+
		"the product briefing is %d bytes with every document but the product intent already left out, "+
		"and the role's memories, the previous recurring turn's account and this turn's message are %d bytes. None of those is cut to fit, so the turn is not tried again",
		e.Bytes, e.BoundBytes, e.WindowTokens, bytesPerToken, e.InstructionsBytes, e.BriefingBytes, e.RestBytes)
}

// passContextBytes is the bound for an endpoint whose window is window tokens.
func (o Options) passContextBytes(window int) int {
	if o.PassContextBytes > 0 {
		return o.PassContextBytes
	}
	return window * bytesPerToken / passContextShare
}

// startFreshPass sets the conversation's own session aside for the recurring
// turn about to be taken, and has that turn start a session of its own. A
// session already set aside is left as it is: it is the conversation's, and the
// one the record holds now is an earlier recurring turn's.
func (s *Session) startFreshPass() {
	if s.state.SessionAsideForPass == nil {
		held := s.state.HeldSession()
		s.state.SessionAsideForPass = &held
	}
	s.freshPass = true
}

// EndPass puts back the session the conversation's other turns resume, where a
// recurring turn set it aside, and records that it did. It is what a recurring
// task calls once its turn, and any request for its missing report, is over.
func (s *Session) EndPass() error {
	s.freshPass, s.passFresh = false, false
	// What the pass was handed is the pass's: a later turn in the conversation
	// counts no stop.
	s.handedStops = nil
	if s.state.SessionAsideForPass == nil {
		return nil
	}
	s.putBackSessionAside()
	return s.record()
}

// putBackSessionAside makes the session set aside for a recurring turn the one
// the record holds again.
func (s *Session) putBackSessionAside() {
	if aside := s.state.SessionAsideForPass; aside != nil {
		s.state.Hold(*aside)
		s.state.SessionAsideForPass = nil
	}
}

// carryPassForward keeps what a recurring turn said for the next one. A turn
// that started a session of its own replaces the last account; a further turn
// in the same session — the request for a missing closing report — adds to it.
// A longer account keeps its end, where the closing report is.
func (s *Session) carryPassForward(fresh bool, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if !fresh && s.state.PassCarriedForward != "" && s.state.PassCarriedForwardFrom == s.pass {
		text = s.state.PassCarriedForward + "\n\n" + text
	}
	if len(text) > runstate.MaxPassCarriedForwardBytes {
		const cut = "[The start of this account is not carried: it was longer than the harness keeps.]\n\n"
		// Keep the end, starting on a whole character and then on a whole line.
		start := len(text) - (runstate.MaxPassCarriedForwardBytes - len(cut))
		for start < len(text) && !utf8.RuneStart(text[start]) {
			start++
		}
		if at := strings.IndexByte(text[start:], '\n'); at >= 0 && start+at < len(text)-1 {
			start += at + 1
		}
		text = cut + text[start:]
	}
	s.state.PassCarriedForward = text
	s.state.PassCarriedForwardFrom = s.pass
	s.state.PassAccountOwed = true
}

// briefingFirst is a turn's prompt with lead — the reply cuts and memories a
// turn opens with — in front of it, except on a recurring turn starting a
// session of its own, where the briefing goes first and lead after it. The
// briefing is the same from one pass to the next until the documents or the
// work change, and the work items are its last section, so a provider caching
// what its requests open with can serve it again to the next pass's new
// session. Memories change more often, and in front of it they would spoil that.
func (s *Session) briefingFirst(lead, turnPrompt string) string {
	if s.freshPass && s.carried != nil {
		if briefing := s.carried.Text + "\n"; strings.HasPrefix(turnPrompt, briefing) {
			return briefing + lead + turnPrompt[len(briefing):]
		}
	}
	return lead + turnPrompt
}

// renderPassStart tells a recurring turn what it is starting from, and gives it
// the previous recurring turn's account.
func (s *Session) renderPassStart() string {
	var rendered strings.Builder
	rendered.WriteString("# This recurring turn starts afresh\n\n")
	fmt.Fprintf(&rendered, "This turn of conversation %s starts in a provider session of its own. Your instructions, the product briefing above, your memories above, the account of your previous recurring turn below, and this turn's message are the whole of what you have: nothing else said in the conversation reaches you, though all of it stays in the conversation's durable record. Where the briefing says a document gave way, read it with a repository read before relying on it.\n\n", s.state.ConversationID)
	if account := strings.TrimSpace(s.state.PassCarriedForward); account != "" {
		from := s.state.PassCarriedForwardFrom
		if from == "" {
			from = "a recurring turn"
		}
		fmt.Fprintf(&rendered, "## What your previous recurring turn said\n\nThis is the account your previous recurring turn (%s) gave, as the harness recorded it. Your memories hold your judgment; this holds what that turn did and what it said it would do next. Where it said it would do something next, do it or say why not.\n\n", from)
		rendered.WriteString(account)
		rendered.WriteString("\n\n")
	}
	return rendered.String()
}

// renderPassAccountForOperator gives a turn the operator opened the account of
// the recurring turn that ran since its session last served, once.
func (s *Session) renderPassAccountForOperator() string {
	account := strings.TrimSpace(s.state.PassCarriedForward)
	if !s.state.PassAccountOwed || account == "" {
		return ""
	}
	s.passAccountInPrompt = true
	from := s.state.PassCarriedForwardFrom
	if from == "" {
		from = "a recurring turn"
	}
	return fmt.Sprintf("# What your last recurring turn said\n\nYour last recurring turn (%s) ran in a provider session of its own, so this session did not see it. This is the account it gave, as the harness recorded it; it is evidence of what that turn did, not something the operator said.\n\n%s\n\n", from, account)
}

// boundPassStart makes the briefing a recurring turn starts from the one
// contextbundle.PassContext leaves: the product intent whole and every other
// document left out, to be read on demand. It then holds the start to its bound
// and refuses the turn where it is over, since nothing left in it gives way.
// reply is told what the turn started from.
func (s *Session) boundPassStart(systemPrompt, prompt string, reply *Reply) (string, error) {
	start := PassStart{}
	briefingBytes, keptBytes := 0, 0
	if full := s.briefingIn(prompt); full != "" {
		kept := contextbundle.PassContext(full)
		at := strings.Index(prompt, full)
		prompt = prompt[:at] + kept.Text + prompt[at+len(full):]
		briefingBytes, keptBytes, start.GaveWay = len(full), len(kept.Text), kept.Sections
	}
	// Only measured, never sent: the turn's own request is built in takeTurn.
	var request backend.RunRequest
	request.Role, request.WorkingDirectory, request.Model = s.state.Role, s.options.Repository, s.options.Model
	request.Prompt, request.SystemPrompt = prompt, systemPrompt
	start.WindowTokens = backend.ContextWindowTokens(s.options.Backend, request)
	start.BoundBytes = s.options.passContextBytes(start.WindowTokens)
	start.Bytes = s.measureStart(request)
	if start.Bytes > start.BoundBytes {
		return prompt, errors.Join(ErrTurnUnassembled, &PassContextTooLarge{
			Bytes: start.Bytes, BoundBytes: start.BoundBytes, WindowTokens: start.WindowTokens,
			InstructionsBytes: len(systemPrompt), BriefingBytes: keptBytes,
			RestBytes: max(len(prompt)-keptBytes, 0),
		})
	}
	payload := map[string]any{
		"pass": s.pass, "start_bytes": start.Bytes, "bound_bytes": start.BoundBytes, "window_tokens": start.WindowTokens,
		"briefing_bytes": briefingBytes, "briefing_kept_bytes": keptBytes,
	}
	if len(start.GaveWay) > 0 {
		payload["briefing_sections_left_out"] = start.GaveWay
	}
	if err := s.emit(execution.EventSessionPassStarted, payload); err != nil {
		return prompt, err
	}
	if reply != nil {
		reply.PassStart = &start
	}
	return prompt, nil
}

// measureStart is how large a request is as the adapter would send it, and
// its text where the adapter does not say.
func (s *Session) measureStart(request backend.RunRequest) int {
	if sizer, ok := s.options.Backend.(backend.RequestSizer); ok {
		if size, _ := sizer.RequestSize(request); size > 0 {
			return size
		}
	}
	return len(request.SystemPrompt) + len(request.Prompt)
}

// recordProviderCompactions writes each compaction the provider reported for
// this invocation into the conversation's log, and onto the reply.
func (s *Session) recordProviderCompactions(result backend.RunResult, reply *Reply) error {
	for _, compaction := range result.Compactions {
		payload := map[string]any{
			"at":            compaction.At,
			"session_id":    result.SessionID,
			"trigger":       compaction.Trigger,
			"before_tokens": compaction.BeforeTokens,
			"after_tokens":  compaction.AfterTokens,
		}
		if s.pass != "" {
			payload["pass"] = s.pass
		}
		if err := s.emit(execution.EventSessionProviderCompacted, payload); err != nil {
			return err
		}
		if reply != nil {
			reply.ProviderCompactions = append(reply.ProviderCompactions, compaction)
		}
	}
	return nil
}
