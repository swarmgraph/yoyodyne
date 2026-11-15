package chat

// The console puts every proposal and every document to the operator as its own
// question, one at a time and in order, and each question has four answers:
// approve it, decline it, ask the role that made it something first, or leave
// it waiting. Only the first two decide anything. An answer the harness does not
// recognise is the last of them rather than a decline, because an answer nobody
// can be sure of has not decided anything, and turning work down on its account
// would be the harness deciding in the operator's place.

import (
	"fmt"
	"strings"
)

// askWords begin an answer that asks about the item before deciding it.
var askWords = []string{"ask", "?"}

// answerKind is what one answer to one question did.
type answerKind int

const (
	// answerUndecided leaves the item waiting: an empty answer, or one the
	// harness does not recognise.
	answerUndecided answerKind = iota
	answerApprove
	answerDecline
	// answerAsk leaves the item waiting and sends a question about it to the
	// role that made it.
	answerAsk
)

// oneAnswer is one answer to one question about one item.
type oneAnswer struct {
	kind answerKind
	// words is the reason on a decline, the question on an ask, and on an answer
	// that decided nothing what was said, so it can be said back.
	words string
}

// readOneAnswer reads the operator's answer to the question about one item.
//
// A decline keeps the whole answer as its reason, the word it was turned down
// with included, because the record keeps what somebody said about work it then
// refused rather than an edited copy; a bare "n" says nothing more than that it
// was declined, and declineReason records that. A question is anything that
// starts with "ask" or ends with a question mark, since that is how a person
// writes one.
func readOneAnswer(line string) oneAnswer {
	answer := strings.TrimSpace(line)
	if answer == "" {
		return oneAnswer{kind: answerUndecided}
	}
	verb, rest := nextWord(answer)
	switch {
	case matches(verb, approveWords) && strings.TrimSpace(rest) == "":
		return oneAnswer{kind: answerApprove}
	case matches(verb, declineWords):
		if strings.TrimSpace(rest) == "" {
			return oneAnswer{kind: answerDecline}
		}
		return oneAnswer{kind: answerDecline, words: answer}
	case matches(verb, askWords):
		return oneAnswer{kind: answerAsk, words: strings.TrimSpace(rest)}
	case strings.HasPrefix(answer, "?"):
		return oneAnswer{kind: answerAsk, words: strings.TrimSpace(strings.TrimPrefix(answer, "?"))}
	case strings.HasSuffix(answer, "?"):
		return oneAnswer{kind: answerAsk, words: answer}
	}
	return oneAnswer{kind: answerUndecided, words: answer}
}

// oneQuestionPrompt is what the operator answers one item under: which item,
// where it stands among those waiting, and every answer it takes, including the
// one that decides nothing.
func oneQuestionPrompt(item string, position, count int, role string) string {
	return fmt.Sprintf("%s (%d of %d)? [y approves it; n <reason> declines it; ask <question> asks the %s about it first; anything else leaves it waiting] ",
		item, position, count, role)
}

// proposalUnblocks says what deciding one proposal unblocks.
func proposalUnblocks(proposal PendingProposal) string {
	line := fmt.Sprintf("Approving proposal %s creates the work item %q, so it can be started", proposal.ID, strings.TrimSpace(proposal.Proposal.Title))
	if parent := strings.TrimSpace(proposal.Proposal.Parent); parent != "" {
		line += fmt.Sprintf(" under %s", parent)
	}
	return line + "; declining it records why, and nothing is created."
}

// writeUnblocks says what deciding one document unblocks.
func writeUnblocks(pending PendingWrite) string {
	line := fmt.Sprintf("Approving %s records your approval in it and lets the harness %s in the repository", pending.ID, pending.Write.Describe())
	if answers := trimmedItems(pending.Write.Answers); len(answers) > 0 {
		line += fmt.Sprintf(", which the work item(s) %s are waiting on", strings.Join(answers, ", "))
	}
	return line + "; declining it records why, and nothing is written."
}

// followUpMessage is the operator's question as it reaches the role: which item
// it is about, so the role answers about that one, and that it is still
// undecided, so the role does not take the question for an answer.
func followUpMessage(item, title, question string) string {
	if strings.TrimSpace(question) == "" {
		question = "Tell me more about it: what it is for and what happens if I decline it."
	}
	return fmt.Sprintf("A question about %s (%s) before I decide it; it is still undecided: %s", item, strings.TrimSpace(title), question)
}

// leaveWaiting marks an item to be put to the operator again after their next
// message.
func (s *Session) leaveWaiting(id string) {
	if s.putAgain == nil {
		s.putAgain = make(map[string]bool)
	}
	s.putAgain[id] = true
}

// takeFollowUp is the question the operator chose to ask before deciding, which
// is the next thing said to the role, taken once.
func (s *Session) takeFollowUp() (string, bool) {
	question := s.followUp
	s.followUp = ""
	return question, question != ""
}

// proposalsToPut is what the operator is asked about after a turn: the
// proposals left waiting earlier, oldest first, then the ones the turn made.
func (s *Session) proposalsToPut(fresh []PendingProposal) []PendingProposal {
	var put []PendingProposal
	seen := make(map[string]bool)
	for _, waiting := range s.Proposals() {
		if s.putAgain[waiting.ID] {
			put = append(put, waiting)
			seen[waiting.ID] = true
		}
	}
	for _, proposal := range fresh {
		if !seen[proposal.ID] {
			put = append(put, proposal)
		}
	}
	return put
}

// writesToPut is proposalsToPut for documents.
func (s *Session) writesToPut(fresh []PendingWrite) []PendingWrite {
	var put []PendingWrite
	seen := make(map[string]bool)
	for _, waiting := range s.Writes() {
		if s.putAgain[waiting.ID] {
			put = append(put, waiting)
			seen[waiting.ID] = true
		}
	}
	for _, write := range fresh {
		if !seen[write.ID] {
			put = append(put, write)
		}
	}
	return put
}

// leftWaiting says that an answer decided nothing, and what becomes of the item.
// item is how the operator was shown it, such as "proposal 1.1" or
// "document-1.1".
func leftWaiting(item string, answer oneAnswer) string {
	if answer.words == "" {
		return fmt.Sprintf("%s is left undecided; nothing was approved or declined. It is put to you again after your next message.\n\n", item)
	}
	return fmt.Sprintf("%q is not an answer the harness recognises, so %s is left undecided; nothing was approved or declined. It is put to you again after your next message.\n\n", answer.words, item)
}

// askedFirst says that the operator's question goes to the role and the item,
// with any after it, waits for the answer.
func askedFirst(item, role string, after int) string {
	line := fmt.Sprintf("%s stays undecided. Your question goes to the %s now, and %s is put to you again after its answer", item, role, item)
	if after > 0 {
		line += fmt.Sprintf(", with the %d after it", after)
	}
	return line + ".\n\n"
}
