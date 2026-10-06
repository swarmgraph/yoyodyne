package chat

// A document an owning role wrote, confirmed under policy, and written by the
// harness under that role's authority.
//
// The path a drafted document took to the repository used to leave the harness
// entirely: fenced Markdown in a reply, a "yes" in prose, and then a person or
// an operator's agent working out the path, writing the frontmatter, and
// committing it. Everything in that step is something the harness already knows
// how to do and nothing in it is judgement, which is why it is worth closing:
// the drafted content was never the part that went wrong.
//
// So it runs exactly as a proposal does, and for the same reasons. The role
// emits a typed action carrying what it decided; the harness refuses what the
// role may not write and saves what survives durably. Automatic policy
// confirms it and opens a reviewed run carrying the exact saved content.
// Other policies retain operator confirmation and a write into the checkout.
// Both use the store's Authorize and record confirmation against the revision
// the write produced.
//
// What the role never gets is a way past its own boundary. It cannot write a
// kind it does not own or file a document anywhere but its kind's home. A role that owns no
// document is refused before any of this, and proposing a change to the owner
// remains its only move.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Documents is the narrow capability a conversation writes a canonical document
// through. It is satisfied by artifact.Store, and it is deliberately a list of
// named operations rather than a filesystem: the only writes a conversation can
// make are these, every one of them goes through the store's ownership
// boundary, and there is nothing here that writes a document without recording
// who wrote it and why.
type Documents interface {
	// CheckWrite refuses a write before anything is done about it: its shape, the
	// role's authority over the document it names, and whether the directory is
	// the one its kind is filed in.
	CheckWrite(role domain.AgentRole, write artifact.Write) error
	// Filing is where each kind this role owns is filed, which is what the role
	// has to be told before it can name a directory — and it is per role rather
	// than a list of every home, because an example naming the wrong one steers
	// the document into another role's directory.
	Filing(role domain.AgentRole) ([]artifact.KindHome, error)
	Create(role domain.AgentRole, draft artifact.Draft, now time.Time) (artifact.Artifact, error)
	Amend(role domain.AgentRole, id string, amendment artifact.Amendment, now time.Time) (artifact.Artifact, error)
	// Approve records the operator's approval of a document as it now stands. It
	// is not a role's operation and does not go through the ownership boundary:
	// the operator is not one of the roles, and an approval an owning role could
	// record would be that role approving its own document.
	Approve(id, reason string, now time.Time) (artifact.Artifact, error)
	// Checkout is the repository the writes land in, named when the operator is
	// told the document is now an uncommitted change there.
	Checkout() string
}

// PendingWrite is one document awaiting confirmation or publication, together with
// the turn it was written in, so a document that reaches the repository traces
// back to the conversation that produced it.
type PendingWrite struct {
	// ID identifies the write within its conversation as document-turn.position,
	// so an operator can name one and the record says which turn wrote it. It is
	// deliberately unlike a proposal's bare turn.position: the two are decided by
	// the same words, and an identifier that could mean either would let an
	// approval land on the wrong thing.
	ID             string                        `json:"id"`
	ConversationID string                        `json:"conversation_id"`
	Turn           int                           `json:"turn"`
	Write          artifact.Write                `json:"write"`
	Publication    *runstate.DocumentPublication `json:"publication,omitempty"`
}

// writeRecord is one written document and whether confirmation and delivery finished
// with it.
type writeRecord struct {
	pending PendingWrite
	decided bool
	// lapsed is a confirmation the publisher refused because it no longer holds;
	// the document is put to the operator in its place, and is not confirmed by
	// policy again in this process.
	lapsed bool
	// failure is the last publication failure the owning role was told about in
	// this process, so a cause that persists is said once rather than at every
	// message.
	failure string
}

// WriteOutcome is what became of one document the operator decided. It is what
// a decision made outside a conversation is reported by, where there is no
// prompt to print underneath.
type WriteOutcome struct {
	WriteID  string `json:"write_id"`
	Artifact string `json:"artifact"`
	Approved bool   `json:"approved"`
	// Path is where the document landed, empty on a decline and on an approval
	// the store would not carry out.
	Path string `json:"path,omitempty"`
	// Approval says the operator's approval was recorded in the document's own
	// frontmatter. A write that landed and an approval that could not be recorded
	// beside it are different situations, and the second is the one that would
	// otherwise be invisible: the document is in the repository saying nobody
	// approved it.
	Approval bool `json:"approval,omitempty"`
	// PendingCommit is where a document that was written now sits — uncommitted,
	// in the checkout named, holding up every run over that checkout until the
	// operator commits it — in the words `yoyo artifact approve` uses for the same
	// fact. It is set whenever a document was written, approval recorded or not.
	PendingCommit string `json:"pending_commit,omitempty"`
	// Reason is why a declined document was turned down, in the operator's own
	// words where they gave any.
	Reason string `json:"reason,omitempty"`
	// Problem is what stopped the decision landing whole, and Undecided says
	// nothing was written at all, so the document is still awaiting a decision
	// and can be approved again once whatever refused it answers.
	Problem   string `json:"problem,omitempty"`
	Undecided bool   `json:"undecided,omitempty"`
}

// Render describes one decision for an operator reading what their message did.
// The four approved outcomes are said apart because they are four different
// things to do next: nothing, try again later, take it up with whoever owns the
// document, or commit what is now in the working tree.
func (o WriteOutcome) Render() string {
	switch {
	case !o.Approved:
		return fmt.Sprintf("[%s] declined: %s\n", o.WriteID, o.Artifact) + indent("because: "+o.Reason)
	case o.Undecided:
		return fmt.Sprintf("[%s] not written: %s\n", o.WriteID, o.Artifact) +
			indent(o.Problem) +
			indent("it is still awaiting a decision; approve it again once whatever refused it answers, or decline it")
	case o.Path == "":
		// A refusal rather than a failure: the store answered about the document,
		// and the answer will not change. Saying it is still waiting would send the
		// operator back to a prompt that can only refuse them again.
		return fmt.Sprintf("[%s] refused: %s\n", o.WriteID, o.Artifact) +
			indent(o.Problem) +
			indent("nothing was written and it is not waiting on you; the role has to write a document it owns")
	case o.Problem != "":
		return fmt.Sprintf("[%s] wrote %s to %s\n", o.WriteID, o.Artifact, o.Path) +
			indent("the record is incomplete: "+o.Problem) +
			indent(o.PendingCommit)
	default:
		return fmt.Sprintf("[%s] wrote %s to %s, with your approval recorded in it\n", o.WriteID, o.Artifact, o.Path) +
			// Said every time, at the moment of the write, because the write stops
			// at the working tree by design and the price is a checkout every run
			// refuses to start over until the operator commits it. Met later, that
			// is an unexplained refusal from whatever they ran next.
			indent(o.PendingCommit)
	}
}

// DocumentError reports that a turn carried a document the harness will not
// write. Like the errors beside it this is not a broken conversation: the turn
// completed, the answer is real, and nothing was written or recorded — the
// refusal happened at the action layer, before the operator was asked and before
// anything touched the filesystem.
type DocumentError struct {
	Role domain.AgentRole
	Err  error
}

func (e *DocumentError) Error() string {
	return fmt.Sprintf("the %s wrote a document the harness will not record: %s", RoleTitle(e.Role), e.Err.Error())
}

func (e *DocumentError) Unwrap() error { return e.Err }

// artifactFiling is where this role's own kinds of document are filed, which is
// what the write contract has to name before the role can name a directory. A
// conversation with no artifact store behind it has none, and so does a store
// that cannot say where a kind goes or whose homes will not resolve: the role is
// not told it can write a document, which is the truthful answer in every one of
// those cases, rather than being told to guess where one goes.
func (s *Session) artifactFiling() []artifact.KindHome {
	if s.options.Documents == nil {
		return nil
	}
	filing, err := s.options.Documents.Filing(s.state.Role)
	if err != nil {
		return nil
	}
	return filing
}

// Writes returns the documents from this conversation the operator has not
// decided on yet.
func (s *Session) Writes() []PendingWrite {
	pending := make([]PendingWrite, 0, len(s.writes))
	for _, record := range s.writes {
		if !record.decided && record.pending.Publication == nil {
			pending = append(pending, record.pending)
		}
	}
	return pending
}

// refuseWrites is the action layer's own gate: everything about a written
// document that can be refused without doing anything is refused here, before
// the operator is asked and before the filesystem is touched. One refused
// document refuses the whole block, the way an unreadable one already does — a
// block is one request, and half-carrying it out would leave the repository in a
// state nobody asked for.
func (s *Session) refuseWrites(writes []artifact.Write) error {
	if len(writes) == 0 {
		return nil
	}
	// Ownership is enforced here even without a configured artifact store.
	// Its refusal belongs to the document action, just like a filing refusal.
	authority := s.authority()
	for _, write := range writes {
		if err := write.Authorize(authority.Role); err != nil {
			return &AuthorityError{
				Role:    authority.Role,
				Refused: "a document to be written",
				Reason:  err.Error(),
			}
		}
	}
	if s.options.Documents == nil {
		return errors.New("no artifact store is configured, so no document can be written from this conversation")
	}
	// What is counted is what is still waiting. A document the operator has
	// already decided costs the record nothing, so a conversation that has
	// written and filed two documents is not thereby a conversation that may
	// never write a third.
	waiting := 0
	for _, record := range s.writes {
		if !record.decided {
			waiting++
		}
	}
	if waiting+len(writes) > runstate.MaxPendingWrites {
		return fmt.Errorf("%d document(s) are already waiting for confirmation or publication and the limit is %d; finish those before writing another",
			waiting, runstate.MaxPendingWrites)
	}
	for _, write := range writes {
		if err := s.options.Documents.CheckWrite(s.state.Role, write); err != nil {
			return err
		}
	}
	return nil
}

// renderDocumentRefusal names what was refused and what is still waiting.
// It grants no confirmation and leaves every pending document untouched.
func (s *Session) renderDocumentRefusal(problem error) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Document submission refused\n\n%s\n\nNothing in the document block was recorded. Other actions in the reply continue.\n", problem)
	for _, pending := range s.Writes() {
		fmt.Fprintf(&out, "- %s: %s (%s)\n", pending.ID, pending.Write.Title, pending.Write.ID)
	}
	return out.String() + "\n"
}

// recordWrites gives each document an identity within the conversation and makes
// it durable before the operator is asked about it. A document that lived only
// in the process that wrote it was undecidable the moment that process exited,
// which for a single message is immediately — and the operator's approval then
// arrived at a conversation that had never heard of what they were approving,
// which is the failure that put the transcription back in a person's hands.
func (s *Session) recordWrites(writes []artifact.Write) ([]PendingWrite, error) {
	pending := make([]PendingWrite, 0, len(writes))
	for index, write := range writes {
		record := &writeRecord{pending: PendingWrite{
			ID:             fmt.Sprintf("document-%d.%d", s.state.Turns, index+1),
			ConversationID: s.state.ConversationID,
			Turn:           s.state.Turns,
			Write:          write,
		}}
		if err := s.emit(execution.EventDocumentDrafted, record.pending); err != nil {
			return pending, fmt.Errorf("record a written document: %w", err)
		}
		s.writes = append(s.writes, record)
		pending = append(pending, record.pending)
	}
	if len(pending) > 0 {
		if err := s.record(); err != nil {
			return pending, err
		}
	}
	return pending, nil
}

// ApproveWrite writes the document the operator approved, under the authority of
// the role that wrote it, and records their approval in the document itself.
//
// The order is what makes the record honest. The approval is emitted before
// anything is written, so a write that failed still shows the operator decided;
// the document is then written through the store, which judges the role's
// ownership again against the document it is actually changing; and the approval
// in the frontmatter is recorded last, against the revision that write produced,
// because until the revision exists there is nothing for it to name.
func (s *Session) ApproveWrite(writeID string) (WriteOutcome, error) {
	record, err := s.awaitingWriteDecision(writeID)
	if err != nil {
		return WriteOutcome{}, err
	}
	write := record.pending.Write
	outcome := WriteOutcome{WriteID: record.pending.ID, Artifact: strings.TrimSpace(write.ID), Approved: true}
	if s.options.Documents == nil {
		outcome.Problem = "no artifact store is configured; the document cannot be written"
		outcome.Undecided = true
		return outcome, errors.New(outcome.Problem)
	}
	if err := s.emit(execution.EventDocumentApproved, record.pending.recordedSummary()); err != nil {
		return outcome, fmt.Errorf("record document approval: %w", err)
	}
	now := s.options.clock().Now()
	var written artifact.Artifact
	switch write.Action {
	case artifact.WriteCreate:
		written, err = s.options.Documents.Create(s.state.Role, write.Draft(), now)
	default:
		written, err = s.options.Documents.Amend(s.state.Role, strings.TrimSpace(write.ID), write.Amendment(), now)
	}
	if err != nil {
		outcome.Problem = err.Error()
		// An ownership refusal is the store answering about the document rather
		// than failing at it, and the answer will be the same however often it is
		// asked. So it decides the write rather than parking it: a document left
		// waiting on an approval that can never succeed would hold a pending slot
		// the role needs for a document it may actually write, and it would tell
		// the operator to try again at something that will never work. The action
		// layer refuses this before the operator is ever asked; this is what
		// happens if the document changed hands between the two.
		if errors.Is(err, artifact.ErrUnauthorized) {
			record.decided = true
			s.notice("the operator approved document %s, and the harness refused it: %v", record.pending.ID, err)
			return outcome, fmt.Errorf("write document %s: %w", strings.TrimSpace(write.ID), err)
		}
		// Anything else is the store failing rather than answering, so nothing was
		// written and the document is still awaiting a decision: approving it again
		// asks for the same document rather than losing it to a store that refused
		// this once.
		outcome.Undecided = true
		return outcome, fmt.Errorf("write document %s: %w", strings.TrimSpace(write.ID), err)
	}
	record.decided = true
	outcome.Path = written.Path
	outcome.PendingCommit = artifact.PendingCommit(s.options.Documents.Checkout(), written.Path)
	s.notice("the operator approved document %s, and the harness wrote %s to %s under the %s's authority",
		record.pending.ID, written.ID, written.Path, s.state.Role)
	if err := s.emit(execution.EventDocumentWritten, map[string]any{
		"write_id": record.pending.ID,
		"turn":     record.pending.Turn,
		"artifact": written.ID,
		"kind":     string(written.Kind),
		"path":     written.Path,
		"action":   string(write.Action),
	}); err != nil {
		return outcome, fmt.Errorf("record written document %s: %w", written.ID, err)
	}
	// The operator's approval goes into the document's own frontmatter, against
	// the revision this write just recorded. It is the last step because it is the
	// only one that needs the revision to exist, and it is a separate failure
	// because a document in the repository saying nobody approved it is a
	// different thing to be told than a document that was never written.
	approved, err := s.options.Documents.Approve(written.ID, s.approvalReason(record.pending), now)
	if err != nil {
		outcome.Problem = fmt.Sprintf("your approval could not be recorded in the document: %v", err)
		return outcome, fmt.Errorf("record the operator's approval of %s: %w", written.ID, err)
	}
	outcome.Approval = true
	outcome.Path = approved.Path
	return outcome, nil
}

// approvalReason says how the approval was given, which is what the record in
// the document has to carry: an approval nobody can trace back to how a person
// gave it is a claim the harness made on their behalf.
func (s *Session) approvalReason(pending PendingWrite) string {
	return fmt.Sprintf("approved by the operator in conversation %s, turn %d, for the document the %s wrote there (%s)",
		pending.ConversationID, pending.Turn, s.state.Role, pending.ID)
}

// DeclineWrite records that the operator turned a document down. Nothing is
// written, and the document stays in the conversation's record: what was drafted
// and that it was refused are both evidence, and neither is dropped for being
// unwelcome.
func (s *Session) DeclineWrite(writeID, reason string) error {
	record, err := s.awaitingWriteDecision(writeID)
	if err != nil {
		return err
	}
	trimmed := declineReason(reason)
	if len(trimmed) > MaxOperatorMessageBytes {
		return fmt.Errorf("rejection reason is %d bytes, limit is %d", len(trimmed), MaxOperatorMessageBytes)
	}
	summary := record.pending.recordedSummary()
	summary["reason"] = trimmed
	if err := s.emit(execution.EventDocumentDeclined, summary); err != nil {
		return fmt.Errorf("record document rejection: %w", err)
	}
	record.decided = true
	s.notice("the operator declined document %s (%s), because: %s",
		record.pending.ID, strings.TrimSpace(record.pending.Write.ID), trimmed)
	return nil
}

func (s *Session) awaitingWriteDecision(writeID string) (*writeRecord, error) {
	trimmed := strings.TrimSpace(writeID)
	for _, record := range s.writes {
		if record.pending.ID != trimmed {
			continue
		}
		if record.decided {
			return nil, fmt.Errorf("document %s has already been decided", trimmed)
		}
		if record.pending.Publication != nil {
			return nil, fmt.Errorf("document %s was already confirmed under policy and is waiting for its reviewed run", trimmed)
		}
		return record, nil
	}
	return nil, fmt.Errorf("no document %q is awaiting a decision in this conversation", writeID)
}

// DecideWrites applies one operator answer to the documents this conversation is
// still waiting on, and reports whether the answer was a decision at all.
//
// It is deliberately stricter than the proposal grammar it borrows its verbs
// from: an answer decides a document only when it names one. A bare "y" sent as
// a message names nothing, and a message is not an answer to a question that was
// just asked — it may be hours and several messages after the document was
// written. Reading loose prose as an approval would file a document into the
// repository that nobody meant to approve, which is the one failure this whole
// path exists to prevent.
func (s *Session) DecideWrites(answer string) ([]WriteOutcome, bool, error) {
	trimmed := strings.TrimSpace(answer)
	if len(trimmed) > MaxOperatorMessageBytes {
		return nil, false, nil
	}
	verb, rest := nextWord(trimmed)
	approve := matches(verb, approveWords)
	decline := matches(verb, declineWords)
	if !approve && !decline {
		return nil, false, nil
	}
	named, names := namesAWrite(rest)
	if !names {
		return nil, false, nil
	}
	record, err := s.awaitingWriteDecision(named)
	if err != nil {
		return nil, true, fmt.Errorf("%w. Nothing was decided, and nothing was said to the %s", err, RoleTitle(s.state.Role))
	}
	outcome, err := s.decideWrite(decision{
		proposalID: record.pending.ID,
		approve:    approve,
		reason:     strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), named)),
	})
	saved := s.record()
	if err != nil {
		return []WriteOutcome{outcome}, true, errors.Join(err, saved)
	}
	return []WriteOutcome{outcome}, true, saved
}

// namesAWrite reports the pending write an answer names, if it names one. The
// identifier is the whole of the rule: a card number would be a position in a
// listing that is no longer on the screen, and "no" on its own is somebody
// talking.
func namesAWrite(text string) (string, bool) {
	for _, word := range strings.Fields(text) {
		for _, part := range splitSelectors(word) {
			if strings.HasPrefix(part, "document-") && len(part) > len("document-") {
				return part, true
			}
		}
	}
	return "", false
}

// decideWrite carries out one decision and says what became of it. It is the one
// place a document is written or turned down, whether the operator answered a
// prompt inside a conversation or sent the decision as a single message.
func (s *Session) decideWrite(made decision) (WriteOutcome, error) {
	if !made.approve {
		outcome := WriteOutcome{WriteID: made.proposalID, Reason: declineReason(made.reason)}
		if record, err := s.awaitingWriteDecision(made.proposalID); err == nil {
			outcome.Artifact = strings.TrimSpace(record.pending.Write.ID)
		}
		if err := s.DeclineWrite(made.proposalID, made.reason); err != nil {
			return outcome, err
		}
		return outcome, nil
	}
	return s.ApproveWrite(made.proposalID)
}

// decideWrites puts the documents this conversation is waiting on to the
// operator, one at a time. They are asked one at a time deliberately: a document
// is read before it is approved, and a batch prompt is an invitation to approve
// what was not read.
func (s *Session) decideWrites(ctx context.Context, writes []PendingWrite, screen console.Console) error {
	if len(writes) == 0 {
		return nil
	}
	// Everything below writes to the console as an ordinary writer: on a terminal
	// that puts it above the composing region, and anywhere else it is the stream
	// it always was.
	var out io.Writer = screen
	fmt.Fprint(out, s.theme.Proposal(fmt.Sprintf(
		"The %s wrote %d document(s). Nothing is written to the repository unless you approve it.\n\n",
		RoleTitle(s.state.Role), len(writes))))
	for _, pending := range writes {
		if s.isWriteDecided(pending.ID) {
			continue
		}
		fmt.Fprint(out, s.theme.Proposal(pending.Render(s.theme)))
		fmt.Fprintln(out)
		line, err := s.ask(ctx, screen, writeDecisionPrompt(pending))
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(out, "input ended before you decided; nothing was written.")
			return nil
		}
		if err != nil {
			return fmt.Errorf("read document decision: %w", err)
		}
		answer := strings.TrimSpace(line)
		verb, rest := nextWord(answer)
		// The contract's own rule, which is the fail-closed one: an answer nobody
		// can be sure of declines, and is kept as the reason it was declined.
		made := decision{proposalID: pending.ID, approve: matches(verb, approveWords)}
		if !made.approve {
			made.reason = answer
			if matches(verb, declineWords) {
				made.reason = strings.TrimSpace(rest)
			}
		}
		outcome, err := s.decideWrite(made)
		saved := s.record()
		// A store that would not write the document is reported and the
		// conversation carries on, whether it refused permanently or failed once.
		// What ends a conversation is the other kind of error — a decision the
		// harness could not record at all — because after that nothing it says
		// about what is decided can be trusted.
		if err != nil && outcome.Problem == "" {
			return errors.Join(err, saved)
		}
		if saved != nil {
			return saved
		}
		fmt.Fprint(out, outcome.Render())
		fmt.Fprintln(out)
	}
	return nil
}

func (s *Session) isWriteDecided(writeID string) bool {
	for _, record := range s.writes {
		if record.pending.ID == writeID {
			return record.decided
		}
	}
	return true
}

// writeDecisionPrompt is what the operator decides one document under. It names
// what would happen to the repository rather than asking a bare yes: this is the
// one prompt in a conversation whose answer changes a file.
func writeDecisionPrompt(pending PendingWrite) string {
	return fmt.Sprintf("%s? [y or yes writes it and records your approval in it; anything else declines, and is kept as the reason] ",
		pending.Write.Describe())
}

// Render describes one written document as the operator decides on it: what
// would be done, and the document itself, because a document approved unread is
// the transcription problem with an extra step.
func (p PendingWrite) Render(theme console.Theme) string {
	heading := fmt.Sprintf("document %s · %s", p.ID, p.Write.Describe())
	return theme.Card(heading, strings.Join(p.body(), "\n"))
}

// body is what the card says about one document: what it supports, why it is
// being recorded, and the document itself.
func (p PendingWrite) body() []string {
	lines := []string{}
	if title := strings.TrimSpace(p.Write.Title); title != "" {
		lines = append(lines, "title: "+title)
	}
	if len(p.Write.Supports) > 0 {
		lines = append(lines, "supports: "+strings.Join(p.Write.Supports, ", "))
	}
	lines = append(lines, "because: "+strings.TrimSpace(p.Write.Reason), "", strings.TrimSpace(p.Write.Body))
	return lines
}

// recordedSummary is what the conversation's event log keeps about a document
// besides the document itself. The prose is deliberately not repeated into every
// event: it is in the record once, where the write is, and an event log that
// carried a whole Markdown file three times over would be one nobody can read.
func (p PendingWrite) recordedSummary() map[string]any {
	return map[string]any{
		"write_id":  p.ID,
		"turn":      p.Turn,
		"action":    string(p.Write.Action),
		"artifact":  strings.TrimSpace(p.Write.ID),
		"kind":      string(p.Write.Kind),
		"title":     strings.TrimSpace(p.Write.Title),
		"directory": strings.TrimSpace(p.Write.Directory),
		"reason":    strings.TrimSpace(p.Write.Reason),
		"bytes":     len(strings.TrimSpace(p.Write.Body)),
	}
}

// recorded is the write as the durable conversation keeps it, so a later process
// can put it back on the table. Everything the write would do is carried,
// including the document, because a document approved in a later process has to
// be the one the operator was shown in an earlier one.
func (p PendingWrite) recorded() runstate.PendingWrite {
	return runstate.PendingWrite{
		Publication: p.Publication,
		Intent:      string(p.Write.Intent),
		ID:          p.ID,
		Turn:        p.Turn,
		Action:      string(p.Write.Action),
		Artifact:    strings.TrimSpace(p.Write.ID),
		Kind:        string(p.Write.Kind),
		Title:       strings.TrimSpace(p.Write.Title),
		Supports:    p.Write.Supports,
		Directory:   strings.TrimSpace(p.Write.Directory),
		Body:        p.Write.Body,
		Reason:      strings.TrimSpace(p.Write.Reason),
	}
}

// restoredWrite is one recorded document put back on the table, in the
// conversation it was written in. It is the exact inverse of recorded: what the
// operator approves in a later process has to be the document they were shown in
// an earlier one, down to the reason it was being recorded.
func restoredWrite(conversationID string, recorded runstate.PendingWrite) PendingWrite {
	return PendingWrite{
		Publication:    recorded.Publication,
		ID:             recorded.ID,
		ConversationID: conversationID,
		Turn:           recorded.Turn,
		Write: artifact.Write{
			Intent:    artifact.Intent(recorded.Intent),
			Action:    artifact.WriteAction(recorded.Action),
			ID:        recorded.Artifact,
			Kind:      artifact.Kind(recorded.Kind),
			Title:     recorded.Title,
			Supports:  recorded.Supports,
			Directory: recorded.Directory,
			Body:      recorded.Body,
			Reason:    recorded.Reason,
		},
	}
}

// undecidedWrites is what a later process may still be asked to carry out. It is
// bounded where it is written as well as where documents are taken from a reply,
// because the two bounds answer different questions: a reply is refused for
// writing too many, and this keeps a record that grew some other way inside what
// a state file may hold.
func (s *Session) undecidedWrites() []runstate.PendingWrite {
	var pending []runstate.PendingWrite
	for _, record := range s.writes {
		if record.decided {
			continue
		}
		pending = append(pending, record.pending.recorded())
	}
	if len(pending) > runstate.MaxPendingWrites {
		pending = pending[len(pending)-runstate.MaxPendingWrites:]
	}
	return pending
}
