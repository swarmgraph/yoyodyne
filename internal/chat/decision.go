package chat

// The grammar a single message decides proposals in. The console asks about
// each proposal on its own (askone.go); a message sent without a question can
// still name several, as "approve 3.1 and decline 3.2 <reason>". Nothing is
// created without an approval that names the item, nothing is declined that the
// answer did not name, and every decision is recorded on its own exactly as an
// answer at the prompt records it.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/console"
)

// The words one answer is written with. An approval is spelled out or answered
// the way a single question has always been answered; a decline has the same
// short forms. Everything is matched case-insensitively, and nothing else is a
// verb at all.
var (
	approveWords = []string{"approve", "y", "yes"}
	declineWords = []string{"decline", "n", "no"}
)

// allSelector names every proposal still on the table. It is accepted on a
// decline and refused on an approval, and the asymmetry is the fail-closed rule
// itself: turning work down wholesale is a decision an operator can make in one
// word, while creating work is not, because an approval has to name what it
// approves.
const allSelector = "all"

// connectorWords join two clauses of one answer, so "approve 1,3 and decline 2"
// reads the way an operator would write it.
var connectorWords = []string{"and", "then", "also"}

// card is one proposal as the operator is asked about it: the number they
// answer with and the proposal it names. The number is the proposal's place in
// the turn that proposed it, so it never changes as decisions are made — a
// number that meant a different item on the second round would be a way to
// approve something nobody read.
type card struct {
	number   int
	proposal PendingProposal
}

// Render describes one proposal as a card the operator decides on. The number
// leads it because that is what they answer with, and the proposal's own
// identifier is beside it because that is what the record calls it.
func (c card) Render(theme console.Theme) string {
	heading := fmt.Sprintf("%d · proposal %s · %s", c.number, c.proposal.ID, strings.TrimSpace(c.proposal.Proposal.Title))
	return theme.Card(heading, strings.Join(c.proposal.body(), "\n"))
}

// decision is one proposal and what the operator said about it. It is the unit
// the session acts on, so a batch answer and a single one reach exactly the
// same recording path.
type decision struct {
	proposalID string
	approve    bool
	// reason is why a declined proposal was turned down. It is empty on an
	// approval, which needs no reason: the item itself records where it came
	// from.
	reason string
}

// DecisionOutcome is what became of one proposal the operator decided. It is
// what a decision made outside a conversation is reported by, where there is no
// prompt to print underneath: the same decision, said once, in a shape a script
// can read.
type DecisionOutcome struct {
	ProposalID string `json:"proposal_id"`
	// Title is the work the decision was about, taken from the created item where
	// there is one and from the proposal where there is not.
	Title    string `json:"title"`
	Approved bool   `json:"approved"`
	// WorkItemID is the item an approval created. It is empty on a decline, and
	// on an approval the tracker would not carry out.
	WorkItemID string `json:"work_item_id,omitempty"`
	// Reason is why a declined proposal was turned down, in the operator's own
	// words where they gave any.
	Reason string `json:"reason,omitempty"`
	// Problem is what stopped the decision landing whole. An approval carries one
	// where nothing was created and where the item exists but is incomplete, and
	// those are different situations, which is what Undecided says.
	Problem string `json:"problem,omitempty"`
	// Undecided says the approval created nothing, so the proposal is still
	// awaiting a decision and can be approved again once whatever refused it
	// answers.
	Undecided bool `json:"undecided,omitempty"`
}

// Render describes one decision for an operator reading what their message did.
// It leads with the proposal, because that is what they named, and says the
// item's identifier where there is one to say.
func (d DecisionOutcome) Render() string {
	switch {
	case !d.Approved:
		return fmt.Sprintf("[%s] declined: %s\n", d.ProposalID, strings.TrimSpace(d.Title)) +
			indent("because: "+d.Reason)
	case d.Undecided:
		return fmt.Sprintf("[%s] not created: %s\n", d.ProposalID, strings.TrimSpace(d.Title)) +
			indent(d.Problem) +
			indent("it is still awaiting a decision; approve it again once the tracker answers")
	case d.Problem != "":
		return fmt.Sprintf("[%s] created %s: %s\n", d.ProposalID, d.WorkItemID, strings.TrimSpace(d.Title)) +
			indent("the item is incomplete: "+d.Problem)
	default:
		return fmt.Sprintf("[%s] created %s: %s\n", d.ProposalID, d.WorkItemID, strings.TrimSpace(d.Title))
	}
}

// namesAProposal reports an answer that decides a proposal by name, and which
// one it names first. It exists for the case where nothing is awaiting a
// decision at all, which is the one case the grammar above cannot be asked:
// there are no cards to resolve a selector against, and the difference between
// somebody deciding and somebody talking has to be read from the answer itself.
//
// The rule is a decision verb followed by a proposal's own identifier. Both
// halves matter. Without the verb, an answer that merely mentions a number is
// prose; without the identifier, a bare "yes" with nothing on the table names no
// proposal and cannot be treated as deciding one. A card number deliberately
// does not count either: a number means a position in a listing, the listing it
// meant is gone, and "no 2 of those are worth doing" is a sentence.
func namesAProposal(answer string) (string, bool) {
	verb, rest := nextWord(strings.TrimSpace(answer))
	if !matches(verb, approveWords) && !matches(verb, declineWords) {
		return "", false
	}
	for _, word := range strings.Fields(rest) {
		for _, part := range splitSelectors(word) {
			if isProposalID(part) {
				return part, true
			}
		}
	}
	return "", false
}

// decidesAsAMessage reports an answer that decides proposals when nobody has
// just asked a question. The grammar below reads a clause that trails off into
// prose after a decline as the reason for it, which is safe only where the
// operator is plainly deciding.
//
// A message is not that. The proposals it would decide may be hours and several
// messages old, and the operator is usually talking rather than answering. So an
// ordinary reply that happens to open with a decision word — "no, let's look at
// the resolver instead", "yes, and can you also check X" — must reach the agent
// as what it is, rather than quietly turning down work nobody mentioned.
//
// Two shapes are decisions here, and both are shapes prose does not take:
//
//   - the answer names a proposal by its own identifier, as "approve 3.1" or
//     "decline 3.1 too vague" — the identifier is what the harness prints and
//     what nobody writes by accident, so the words after it are a reason;
//   - the answer is nothing but decision vocabulary, as "y", "decline all", or
//     "approve 1,3" — there is no prose in it to lose.
//
// Everything else is speech, including "decline 2 too vague": a bare number is a
// position in a listing rather than a name, and that ambiguity is the one this
// package already resolves toward the reason. Deciding it from a message would
// resolve it toward a proposal the operator may not have been looking at.
func decidesAsAMessage(answer string) bool {
	if _, names := namesAProposal(answer); names {
		return true
	}
	return onlyDecisionWords(answer)
}

// IsDecision reports a single message that Decide would take as an answer to
// something this conversation is waiting on — a proposal decided, or a concern
// answered — rather than as speech. It is the same two rules Decide reads,
// asked without a session, for a caller that has to know before it opens one:
// a decision has to reach the main thread, which holds what is being decided,
// and a caller that routed one somewhere the main thread is not would spend the
// operator's "y" on a question nobody asked.
func IsDecision(message string) bool {
	trimmed := strings.TrimSpace(message)
	if _, _, names := namesAConcern(trimmed); names {
		return true
	}
	return decidesAsAMessage(trimmed)
}

// onlyDecisionWords reports an answer with no prose in it at all: a decision
// verb, and after it nothing but more verbs, the words that join them, and the
// proposals they name.
func onlyDecisionWords(answer string) bool {
	words := strings.Fields(strings.TrimSpace(answer))
	if len(words) == 0 || !isDecisionVerb(words[0]) {
		return false
	}
	for _, word := range words[1:] {
		trimmed := strings.Trim(word, ",;")
		switch {
		case trimmed == "":
			// Punctuation the operator separated selectors with, as in "1 , 3".
		case isDecisionVerb(trimmed) || matches(trimmed, connectorWords):
		case isSelectorList(trimmed):
		default:
			return false
		}
	}
	return true
}

func isDecisionVerb(word string) bool {
	return matches(word, approveWords) || matches(word, declineWords)
}

// declineReason is what the record keeps about why a proposal was turned down.
// An operator who declined it without saying anything still declined it, so the
// record says that rather than recording no reason at all.
func declineReason(reason string) string {
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		return trimmed
	}
	return "the operator declined it without giving a reason"
}

// errNotADecision reports an answer that is not a decision at all: it names no
// proposal and begins with nothing the harness recognizes. It is separate from
// every other failure here because it is not a decision that failed: a message
// that reads as one is said to the role as speech, and decides nothing.
var errNotADecision = errors.New("that answer does not decide anything")

// readDecisions turns one answer into the decisions it asks for, resolved
// against the proposals the operator was shown.
//
// It is deliberately strict in one direction only. An answer it cannot read
// whole decides nothing rather than partly something: an approval that names an
// item that is not there, or a clause that trails off into words, leaves every
// proposal awaiting a decision and is put to the operator again. Nothing here
// can create anything, so being asked twice is the entire cost of a typo, while
// acting on half of a misread answer would create work nobody asked for.
func readDecisions(answer string, cards []card) ([]decision, error) {
	rest := strings.TrimSpace(answer)
	// Nothing on the table is nothing to decide, and saying so here is what lets
	// everything below assume there is at least one card to name.
	if rest == "" || len(cards) == 0 {
		return nil, errNotADecision
	}
	var decisions []decision
	named := make(map[string]bool, len(cards))
	for rest != "" {
		clause := rest
		verb, after := nextWord(rest)
		approve := matches(verb, approveWords)
		if !approve && !matches(verb, declineWords) {
			if len(decisions) == 0 {
				return nil, errNotADecision
			}
			return nil, fmt.Errorf("%q is not part of a decision; say approve or decline before the proposals you mean", verb)
		}
		// An approval is followed only by more clauses, so spaces separate the
		// proposals it names. A decline is followed by the operator's words, so
		// they do not.
		selectors, remainder := takeSelectors(after, approve)
		if approve {
			chosen, err := resolveApproved(selectors, cards, named)
			if err != nil {
				return nil, err
			}
			for _, id := range chosen {
				decisions = append(decisions, decision{proposalID: id, approve: true})
			}
			rest = skipConnector(remainder)
			continue
		}
		// A declined proposal keeps the operator's own words, so the reason runs
		// to the end of the line rather than stopping at the next thing that looks
		// like a verb. That makes a decline the last clause of an answer, which is
		// stated in the prompt: an approval written after one is never carried out,
		// and the proposal it named is put again rather than quietly created.
		chosen, err := resolveDeclined(selectors, cards, named)
		if err != nil {
			return nil, err
		}
		// A clause that names no proposals is entirely the operator's words about
		// the ones it declines, so the whole of it is kept — the word they turned
		// them down with included. That is what "n" recorded when proposals were
		// answered one at a time, and "no thanks" recorded as "thanks" would be
		// the harness editing what somebody said about work it then refused.
		reason := strings.TrimSpace(remainder)
		if len(selectors) == 0 {
			reason = strings.TrimSpace(clause)
		}
		for _, id := range chosen {
			decisions = append(decisions, decision{proposalID: id, reason: reason})
		}
		rest = ""
	}
	if len(decisions) == 0 {
		return nil, errNotADecision
	}
	return inCardOrder(decisions, cards), nil
}

// resolveApproved names the proposals an approval clause approves. An approval
// naming nothing is accepted only where there is exactly one proposal on the
// table, because there it does name it — that is the single question an
// operator has always answered with a bare yes. Where there is more than one it
// is refused, since the harness would otherwise be choosing which item to
// create.
func resolveApproved(selectors []string, cards []card, named map[string]bool) ([]string, error) {
	if len(selectors) == 0 {
		if len(cards) != 1 {
			return nil, fmt.Errorf("say which of the %d proposals to approve, as %s; an approval has to name what it creates", len(cards), approveExample(cards))
		}
		// The card in front of the operator is taken directly rather than by the
		// number it happens to carry. A batch decided down to its last proposal
		// keeps that proposal's original number, so a bare yes to the last of
		// three is a yes to card 3, and reading it as card 1 would refuse the very
		// answer the prompt asks for.
		found, err := claim(cards[0], named)
		if err != nil {
			return nil, err
		}
		return []string{found}, nil
	}
	var chosen []string
	for _, selector := range selectors {
		if strings.EqualFold(selector, allSelector) {
			return nil, fmt.Errorf("an approval names the proposals it creates rather than all of them; say %s", approveExample(cards))
		}
		found, err := resolve(selector, cards, named)
		if err != nil {
			return nil, err
		}
		chosen = append(chosen, found)
	}
	return chosen, nil
}

// approveExample shows how an approval names what it creates, using numbers
// that are on the table. A refusal that told the operator to "say approve 1,3"
// when the cards in front of them are 4 and 5 would be telling them to type
// something the harness refuses for naming proposals that are not there.
func approveExample(cards []card) string {
	if len(cards) == 1 {
		return fmt.Sprintf("approve %d", cards[0].number)
	}
	return fmt.Sprintf("approve %d,%d", cards[0].number, cards[len(cards)-1].number)
}

// resolveDeclined names the proposals a decline clause turns down. A decline
// naming nothing is accepted only where exactly one proposal is waiting, as an
// approval is: where several are, a bare "no" has not said which, and declining
// them all on its account would decide work the operator did not name. "decline
// all" names every one of them, and is accepted.
func resolveDeclined(selectors []string, cards []card, named map[string]bool) ([]string, error) {
	if len(selectors) == 0 && len(cards) != 1 {
		return nil, fmt.Errorf("say which of the %d proposals to decline, as decline %d <reason>, or decline all <reason>; nothing is declined that you did not name", len(cards), cards[0].number)
	}
	if len(selectors) == 0 || (len(selectors) == 1 && strings.EqualFold(selectors[0], allSelector)) {
		var chosen []string
		for _, entry := range cards {
			if named[entry.proposal.ID] {
				continue
			}
			named[entry.proposal.ID] = true
			chosen = append(chosen, entry.proposal.ID)
		}
		if len(chosen) == 0 {
			return nil, errors.New("every proposal in that answer was already decided by it")
		}
		return chosen, nil
	}
	var chosen []string
	for _, selector := range selectors {
		if strings.EqualFold(selector, allSelector) {
			return nil, errors.New("say either all or the proposals you mean, not both")
		}
		found, err := resolve(selector, cards, named)
		if err != nil {
			return nil, err
		}
		chosen = append(chosen, found)
	}
	return chosen, nil
}

// resolve finds the one proposal a selector names, by the number on its card or
// by its own identifier. A selector naming nothing on the table, and one naming
// a proposal the same answer already decided, are both refused: an answer that
// contradicts itself is exactly the kind nobody can be sure of.
func resolve(selector string, cards []card, named map[string]bool) (string, error) {
	number, numeric := cardNumber(selector)
	for _, entry := range cards {
		if entry.proposal.ID != selector && !(numeric && entry.number == number) {
			continue
		}
		return claim(entry, named)
	}
	return "", fmt.Errorf("%s is not one of the proposals you are being asked about", selector)
}

// claim records that one answer has decided a card, and refuses a second
// decision about it: an answer that contradicts itself is exactly the kind
// nobody can be sure of.
func claim(entry card, named map[string]bool) (string, error) {
	if named[entry.proposal.ID] {
		return "", fmt.Errorf("proposal %s is decided twice in that answer", entry.proposal.ID)
	}
	named[entry.proposal.ID] = true
	return entry.proposal.ID, nil
}

// cardNumber reads a selector as the number on a card. A proposal identifier is
// turn.position, so it is never mistaken for one of these.
func cardNumber(selector string) (int, bool) {
	number, err := strconv.Atoi(selector)
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}

// inCardOrder puts the decisions in the order the operator was shown them, so
// what is recorded and what is printed follow the cards rather than the order
// the clauses happened to be written in.
func inCardOrder(decisions []decision, cards []card) []decision {
	ordered := make([]decision, 0, len(decisions))
	for _, entry := range cards {
		for _, made := range decisions {
			if made.proposalID == entry.proposal.ID {
				ordered = append(ordered, made)
			}
		}
	}
	return ordered
}

// takeSelectors consumes the proposals a clause names, and returns what is left
// of the answer. Commas and a joining word both separate them, because an
// operator writes "1,3", "1, 3", and "1 and 3" and means the same thing by all
// three.
//
// A bare space separates them only where whatever follows the clause cannot be
// prose, which is to say on an approval. A decline is followed by the operator's
// own words, and those words start with a number often enough to matter:
// "decline 2 3 weeks out" is one proposal turned down for being three weeks
// out, not two proposals turned down for being "weeks out". Reading it the
// second way would record a decision about a proposal nobody named and never
// put that proposal to them again, so the ambiguity is resolved towards the
// reason and the unnamed proposal is asked about again.
func takeSelectors(text string, spaceSeparates bool) ([]string, string) {
	var selectors []string
	rest := text
	// The first selector needs no separator before it; every later one does,
	// unless bare spaces are separators in this clause.
	separated := true
	for rest != "" {
		word, after := nextWord(rest)
		if matches(word, connectorWords) && len(selectors) > 0 {
			// A joining word only separates selectors when a selector follows it.
			// Otherwise it belongs to whatever comes next, which is the second
			// clause of the answer.
			if next, _ := nextWord(after); !isSelectorList(next) {
				break
			}
			rest = after
			separated = true
			continue
		}
		if !separated && !spaceSeparates && !startsSeparated(word) {
			break
		}
		if !isSelectorList(word) {
			break
		}
		selectors = append(selectors, splitSelectors(word)...)
		separated = endsSeparated(word)
		rest = after
	}
	return selectors, rest
}

// startsSeparated and endsSeparated report the punctuation that joins one
// selector to the next across a space, so "1, 2" and "1 ,2" are the one list an
// operator wrote and "1 2" is a selector followed by something else.
func startsSeparated(word string) bool {
	return strings.HasPrefix(word, ",") || strings.HasPrefix(word, ";")
}

func endsSeparated(word string) bool {
	return strings.HasSuffix(word, ",") || strings.HasSuffix(word, ";")
}

// isSelectorList reports a word that is nothing but proposals, so a clause
// stops consuming at the first word that is prose.
func isSelectorList(word string) bool {
	parts := splitSelectors(word)
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if strings.EqualFold(part, allSelector) {
			continue
		}
		if _, numeric := cardNumber(part); numeric {
			continue
		}
		if !isProposalID(part) {
			return false
		}
	}
	return true
}

// isProposalID reports the turn.position shape a recorded proposal is named by.
func isProposalID(value string) bool {
	turn, position, found := strings.Cut(value, ".")
	if !found {
		return false
	}
	if _, numeric := cardNumber(position); !numeric {
		return false
	}
	number, err := strconv.Atoi(turn)
	return err == nil && number >= 0
}

// splitSelectors breaks one word into the proposals it names, dropping the
// commas and semicolons an operator separates them with.
func splitSelectors(word string) []string {
	var parts []string
	for _, part := range strings.FieldsFunc(word, func(r rune) bool { return r == ',' || r == ';' }) {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// skipConnector steps over whatever joins two clauses, so the next word an
// answer is read from is the verb that begins the clause after it.
func skipConnector(text string) string {
	rest := strings.TrimLeft(text, " \t,;")
	if word, after := nextWord(rest); matches(word, connectorWords) {
		return strings.TrimLeft(after, " \t,;")
	}
	return rest
}

// nextWord splits the first word off some text and returns what follows it.
func nextWord(text string) (string, string) {
	trimmed := strings.TrimLeft(text, " \t")
	at := strings.IndexAny(trimmed, " \t")
	if at < 0 {
		return trimmed, ""
	}
	return trimmed[:at], strings.TrimLeft(trimmed[at:], " \t")
}

// matches reports one of a small set of words, whatever case it was typed in.
func matches(word string, words []string) bool {
	for _, candidate := range words {
		if strings.EqualFold(word, candidate) {
			return true
		}
	}
	return false
}
