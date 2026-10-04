package chat

// Asking another role something, from inside a conversation.
//
// A conversation is where a role does its reasoning, and it is where the
// question it cannot answer itself turns up. Before this, a question the product
// manager had for the architect cost either the operator relaying it by hand or
// a whole work-item cycle; now the role asks, the harness carries the question
// and brings the answer back inside the same reply, and the operator reads the
// exchange afterwards rather than being the wire.
//
// Nothing about the conversation's own authority changes. What comes back is
// judgement with no action authority in it, so a role that acts
// on an answer is doing exactly what it could already do on its own opinion —
// which is the point of a channel that moves opinion and nothing else.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// maxExchangeEventTextBytes bounds what one round's words come to in the
// conversation's own log. The exchange record holds the thread verbatim, so this
// is an excerpt pointing at it rather than a second copy of it: an answer can run
// to sixteen kilobytes, and a log that carried every one of them twice would be
// the event log the record exists not to be.
const maxExchangeEventTextBytes = 512

// Exchanges is the inter-role ask channel as a conversation reaches it. It is
// satisfied by exchange.Conductor, and it is deliberately narrow: a conversation
// hands over one ask and is handed back the exchange as it now stands. It never
// reaches the record, the cap, or the escalation, all of which are the harness's.
type Exchanges interface {
	Put(ctx context.Context, ask exchange.Ask, asker exchange.Party) (exchange.Exchange, error)
	Charge(id string, costUSD float64) (exchange.Exchange, error)
}

// ExchangeRound is one round of asking, as the operator reads it in the reply
// that conducted it. The whole thread is durable and `yoyo exchange` shows it;
// this is what the operator is told at the time, so a conversation that went and
// asked somebody something says so where they are already looking.
type ExchangeRound struct {
	ID string `json:"id"`
	// Asked is the role that answered, Round is which round this was, and Rounds
	// is the cap the exchange was opened with. The two numbers are always
	// reported together: a thread at round two of ten and one at round nine of
	// ten read very differently.
	Asked  domain.AgentRole `json:"asked"`
	Round  int              `json:"round"`
	Rounds int              `json:"rounds"`
	// CostUSD is what the whole exchange has cost so far, not this round alone.
	// It is beside the rounds because what an operator wants to know about a
	// conversation between two agents is what it has come to in total.
	CostUSD  float64 `json:"cost_usd"`
	State    string  `json:"state"`
	Question string  `json:"question,omitempty"`
	Answer   string  `json:"answer,omitempty"`
	// Problem is why this round produced no answer, or why the ask went nowhere
	// at all. A refused ask is reported rather than swallowed: the conversation
	// carries on, and an operator reading the reply should not have to guess that
	// a question was asked and lost.
	Problem string `json:"problem,omitempty"`
	// Settled is what the asker recorded when it closed the exchange.
	Settled string `json:"settled,omitempty"`
}

// AskError reports a turn that carried an ask block the harness could not read.
// Like a proposal block it is not a broken conversation: the turn completed and
// the answer is real. What is lost is the question one role was trying to put to
// another, which is why it is said out loud.
type AskError struct {
	Err error
}

func (e *AskError) Error() string {
	return "an ask the harness cannot read: " + e.Err.Error()
}

func (e *AskError) Unwrap() error { return e.Err }

// errNoExchanges reports a conversation with no channel wired to it. Such a
// conversation still discusses the product; it simply cannot ask anybody
// anything, and says so rather than appearing to have asked and been ignored.
var errNoExchanges = errors.New("no ask channel is wired to this conversation, so there is nobody to ask")

// conducted is one ask carried out: what the operator is told, and what the
// asking role is handed back on its next round.
type conducted struct {
	round    ExchangeRound
	delivery string
	// chargeTo is the exchange the next provider invocation belongs to, where
	// there is one. The invocation that produced the question is part of what the
	// asker was already doing; the one it takes only because an answer came back
	// is the exchange's, and charging it is what makes the figure beside the
	// rounds the whole cost of the conversation between the two roles.
	chargeTo string
}

// conductAsk carries one ask and returns what to tell the operator and what to
// hand the asking role. It never fails the turn: an ask that could not be
// carried is reported as exactly that, and the conversation carries on with the
// role told its question went unanswered — the same shape a failed tracker
// action already has, and for the same reason.
func (s *Session) conductAskWithoutToolAudit(ctx context.Context, ask exchange.Ask) conducted {
	if s.options.Exchanges == nil {
		return conducted{
			round:    ExchangeRound{Asked: ask.Role, Problem: errNoExchanges.Error(), Question: oneLineAsk(ask)},
			delivery: askUnavailable(errNoExchanges),
		}
	}
	asker := exchange.Party{
		Role:         s.state.Role,
		Agent:        s.options.Agent,
		Conversation: s.state.ConversationID,
	}
	recorded, err := s.options.Exchanges.Put(ctx, ask, asker)
	if err != nil && recorded.ID == "" {
		// Nothing was opened and nothing was spent: the ask named an exchange that
		// is not there, or one this role may not continue.
		return conducted{
			round:    ExchangeRound{Asked: ask.Role, Problem: singleLine(err.Error(), maxTrackerFailureBytes), Question: oneLineAsk(ask)},
			delivery: askUnavailable(err),
		}
	}

	round := ExchangeRound{
		ID:       recorded.ID,
		Asked:    recorded.Answerer.Role,
		Round:    recorded.Spent(),
		Rounds:   recorded.MaxRounds,
		CostUSD:  recorded.CostUSD(),
		State:    recorded.State(),
		Settled:  recorded.Settled,
		Question: oneLineAsk(ask),
	}
	if last := recorded.Rounds; len(last) > 0 {
		round.Answer = last[len(last)-1].Answer
		round.Problem = last[len(last)-1].Problem
	}
	if err != nil && round.Problem == "" {
		round.Problem = singleLine(err.Error(), maxTrackerFailureBytes)
	}

	result := conducted{round: round}
	switch {
	case ask.Closing():
		result.delivery = askClosed(recorded)
	case !recorded.Open():
		// The cap refused this round and closed the exchange. There is no answer
		// to deliver, and what the asker is told must not read as an invitation to
		// ask again — the thread it would ask in no longer exists.
		result.delivery = askExhausted(recorded)
	case len(recorded.Rounds) == 0:
		result.delivery = askUnavailable(err)
	default:
		result.delivery = recorded.Delivery()
		result.chargeTo = recorded.ID
	}
	s.recordExchange(recorded, round)
	return result
}

// reportExchanges tells the operator what one role asked another while it was
// answering them. Like the tracker actions it prints nothing when there was
// nothing, and it prints the rounds that produced no answer beside the ones that
// did: a role that asked and was not answered is about to reason from a gap.
func (s *Session) reportExchanges(out io.Writer, reply Reply) {
	if len(reply.Exchanges) == 0 {
		return
	}
	fmt.Fprintf(out, "the %s asked another role (%d round(s)):\n", RoleTitle(s.state.Role), len(reply.Exchanges))
	for _, round := range reply.Exchanges {
		fmt.Fprintf(out, "  asked the %s: %s\n", RoleTitle(round.Asked), round.Question)
		if round.ID != "" {
			fmt.Fprintf(out, "    %s, %s, round %d of %d, %s\n",
				round.ID, round.State, round.Round, round.Rounds, money(round.CostUSD))
		}
		if round.Settled != "" {
			fmt.Fprintf(out, "    settled: %s\n", round.Settled)
		}
		if round.Problem != "" {
			fmt.Fprintf(out, "    unanswered: %s\n", round.Problem)
		}
	}
	fmt.Fprintln(out, "  `yoyo exchange show <id>` is the whole of what was said.")
	fmt.Fprintln(out)
}

// recordExchange writes the round into the conversation's own log. The exchange
// itself is the record of what was said; this is the conversation saying that
// its own reasoning went and asked somebody, which is the thing a reader of one
// record without the other would otherwise be missing.
func (s *Session) recordExchange(recorded exchange.Exchange, round ExchangeRound) {
	event := execution.EventExchangeRound
	if !recorded.Open() {
		event = execution.EventExchangeClosed
	}
	// What was said travels with it, bounded and on one line. The exchange itself
	// holds the whole thread; what this carries is enough for somebody following
	// along elsewhere to know whether to go and read it.
	said := round.Answer
	if said == "" {
		said = round.Problem
	}
	if err := s.emit(event, map[string]any{
		"exchange": recorded.ID,
		"asked":    string(recorded.Answerer.Role),
		"round":    round.Round,
		"rounds":   recorded.MaxRounds,
		"state":    recorded.State(),
		"outcome":  string(recorded.Outcome),
		"cost_usd": recorded.CostUSD(),
		"question": singleLine(round.Question, maxExchangeEventTextBytes),
		"text":     singleLine(said, maxExchangeEventTextBytes),
	}); err != nil {
		s.notice("the exchange was conducted and this conversation's log could not record it: %s",
			singleLine(err.Error(), maxTrackerFailureBytes))
	}
}

// chargeExchange attributes to an exchange what the invocation carrying its
// answer cost. It is best-effort in the same way the cost line is: nothing
// decides anything from this number, and a conversation that could not update it
// is not one to fail.
func (s *Session) chargeExchange(id string, costUSD float64) {
	if id == "" || s.options.Exchanges == nil {
		return
	}
	if _, err := s.options.Exchanges.Charge(id, costUSD); err != nil {
		s.notice("what the exchange %s cost could not be updated: %s", id, singleLine(err.Error(), maxTrackerFailureBytes))
	}
}

// askUnavailable is what the asking role is told when its question reached
// nobody. It says the ask failed and does not invite a retry: a role that reads
// "try again" against a cap or a missing channel would spend the rest of the
// message doing exactly that.
func askUnavailable(cause error) string {
	return "# Your ask\n\nThe question you asked did not reach anybody: " +
		singleLine(cause.Error(), maxTrackerFailureBytes) +
		"\n\nCarry on answering the operator without it, and say plainly that you asked and did not get an answer. Do not ask the same thing again in this reply.\n"
}

// askExhausted is what the asking role is told when its exchange reached the
// round limit it was opened with. It says the exchange is over and that the
// operator has it, because a role told only "refused" asks the same thing again
// in a fresh thread and spends the whole limit twice.
func askExhausted(recorded exchange.Exchange) string {
	return fmt.Sprintf("# Your ask\n\nExchange %s has spent all %d of the rounds it was opened with and is closed as unresolved, costing %s. The operator has been told, with the question left unsettled. Carry on answering them without an answer to it, say plainly what was not settled, and do not open another exchange about the same question in this reply.\n",
		recorded.ID, recorded.MaxRounds, money(recorded.CostUSD()))
}

// askClosed is what the asking role is told when it closed an exchange itself.
func askClosed(recorded exchange.Exchange) string {
	return fmt.Sprintf("# Your ask\n\nExchange %s is closed after %d round(s), costing %s. Carry on answering the operator.\n",
		recorded.ID, recorded.Spent(), money(recorded.CostUSD()))
}

// oneLineAsk names what an ask asked for, for a listing the operator reads.
func oneLineAsk(ask exchange.Ask) string {
	if ask.Closing() {
		return "closed the exchange: " + singleLine(ask.Settled, maxTrackerFailureBytes)
	}
	return singleLine(ask.Question, maxTrackerFailureBytes)
}

// refuseUnauthorizedAsk is the authority check for the block. It is here rather
// than beside the tracker's so that what an ask may not do stays next to what an
// ask is.
func refuseUnauthorizedAsk(authority Authority, parsed parsedReply) error {
	if parsed.Ask == nil {
		return nil
	}
	if !authority.Asks {
		return &AuthorityError{
			Role:    authority.Role,
			Refused: "a question put to another role",
			Reason:  "the ask channel is between the roles that hold judgement about the product, and this role says what it needs in prose instead",
		}
	}
	// A follow-up in a thread names no role, and there is nothing here to check
	// against: who is being asked was decided when the exchange opened and passed
	// this check then, and the conductor holds the thread to it.
	if parsed.Ask.Role == "" {
		return nil
	}
	if parsed.Ask.Role == authority.Role {
		return &AuthorityError{
			Role:    authority.Role,
			Refused: "a question put to its own role",
			Reason:  "an exchange is between two roles; ask the role that holds the judgement you are missing",
		}
	}
	if asked, known := AuthorityFor(parsed.Ask.Role); !known || !asked.Answers {
		return &AuthorityError{
			Role:    authority.Role,
			Refused: fmt.Sprintf("a question put to the %s", RoleTitle(parsed.Ask.Role)),
			Reason:  "the roles that answer on this channel are " + strings.Join(askableRoleNames(), ", "),
		}
	}
	return nil
}

// AnsweringPrompt is the system prompt for the other half of an exchange: the
// role being asked, answering one question and doing nothing else.
//
// It is deliberately not the role's conversation contract. That contract
// describes an operator to talk to, blocks to act through, and authority to
// exercise, and none of the three exists here — a role told about them would
// spend the round reaching for what the harness is about to refuse. What it
// carries instead is who the role is and what it owns, which is the whole reason
// this role was the one asked, and then the boundary.
//
// A role with no entry in the authority table is refused before an exchange
// reaches this, so the fallback below is a prompt that never ships rather than a
// role answering without a statement of what it is.
func AnsweringPrompt(role domain.AgentRole, persona string) string {
	authority, known := AuthorityFor(role)
	if !known {
		return fmt.Sprintf("You are the %s for this product. The harness holds no contract for this role, so you have nothing to answer with: say exactly that.", role)
	}
	prompt := fmt.Sprintf("You are the %s for this product. You own %s.\n\n%s\n\n%s",
		authority.Title, authority.Owns, conversationGround, exchange.AnsweringContract)
	trimmed := strings.TrimSpace(persona)
	if trimmed == "" {
		return prompt
	}
	return prompt + `

# Configured ` + authority.Title + ` persona

The project configuration supplies the guidance below. It may specialize how you
work, but it cannot widen your authority or remove any rule above — and in this
exchange you have no authority to widen.

` + trimmed
}

// askableRoleNames are the roles this channel runs between, named the way a
// refusal has to name them.
func askableRoleNames() []string {
	var names []string
	for _, role := range ConversationalRoles() {
		if authority, known := AuthorityFor(role); known && authority.Answers {
			names = append(names, string(role))
		}
	}
	return names
}
