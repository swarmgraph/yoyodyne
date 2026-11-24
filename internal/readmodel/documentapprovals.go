package readmodel

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DocumentApproval is one document an owning role wrote in a conversation that
// waits for the operator to approve or decline it: which document, the role and
// agent that wrote it and the conversation it was written in, what the write
// does, and since when it has waited. It is read off the conversation's saved
// record, which holds a document only until it is decided, so an approval or a
// decline takes it off the line by itself.
type DocumentApproval struct {
	WriteID        string           `json:"write_id"`
	ConversationID string           `json:"conversation_id"`
	Agent          string           `json:"agent"`
	Owner          domain.AgentRole `json:"owner"`
	// Action is "create" or "revise", Document the document's identifier, and
	// Kind, Title, and Directory what the write names of it, empty where a
	// revision leaves them alone.
	Action    string `json:"action"`
	Document  string `json:"document"`
	Kind      string `json:"kind,omitempty"`
	Title     string `json:"title,omitempty"`
	Directory string `json:"directory,omitempty"`
	Reason    string `json:"reason"`
	// Intent is what the writer recorded the document does to what the goals
	// admit — "consistent", "fundamental", or empty where it said nothing — and
	// Section the heading a section revision replaces. Serves is the work items
	// the document answers or names.
	Intent  string   `json:"intent,omitempty"`
	Section string   `json:"section,omitempty"`
	Serves  []string `json:"serves,omitempty"`
	// Before and After are the part of the document a revision changes, read
	// against the repository when the line is read (beforeAndAfter), and
	// BeforeUnread why the document as it stands could not be read where it
	// could not.
	Before       string `json:"before,omitempty"`
	After        string `json:"after,omitempty"`
	BeforeUnread string `json:"before_unread,omitempty"`
	// Body is the document's text as the role wrote it. The line says what
	// waits rather than holding it, so it is not in the line's JSON; the
	// decision the dashboard opens from this entry carries it.
	Body string `json:"-"`
	// ProductIntent is a document stating the product's intent: the brief, the
	// goals and non-goals, and the operating rules.
	ProductIntent bool `json:"product_intent,omitempty"`
	// DocumentKind is the kind of the document the write changes: the write's
	// own kind on a creation, and the kind recorded with a revision. It is
	// empty where neither was kept.
	DocumentKind string `json:"document_kind,omitempty"`
	// HarnessConfirms is a document the harness confirms by itself — under its
	// kind's automatic approval setting, or as the Lead Product Manager's change
	// consistent with intent — and has not confirmed yet. It waits on nobody's
	// decision, only on the harness reaching it.
	HarnessConfirms bool `json:"harness_confirms,omitempty"`
	Turn            int  `json:"turn"`
	// WrittenAt is when the role wrote it, absent on a record kept before the
	// moment was.
	WrittenAt time.Time `json:"written_at,omitzero"`
	// Placement is where a document already confirmed has to be put by a
	// person, because the harness cannot reach the repository it is kept in,
	// and Waited how long it has waited for that as of this reading. Such a
	// document waits for nobody's approval: its owning role finished it, and the
	// entry is that role's, carrying the operator's hand for the placement.
	Placement *runstate.DocumentPlacement `json:"placement,omitempty"`
	Waited    time.Duration               `json:"waited,omitempty"`
	// Answers are the work items the document is the design or ruling for.
	Answers []string `json:"answers,omitempty"`
}

// documentApprovalID is what identifies a waiting document across every
// conversation: a write's own identifier is unique only within the
// conversation that wrote it.
func documentApprovalID(conversationID, writeID string) string {
	return conversationID + "/" + writeID
}

// Says is the waiting document in the words the needs-a-human line prints.
func (d DocumentApproval) Says() string {
	what := d.what()
	if d.Placement != nil {
		answers := ""
		if len(d.Answers) > 0 {
			answers = ", the design or ruling for " + strings.Join(d.Answers, ", ") + ","
		}
		return fmt.Sprintf("document %s, %s%s finished by the %s in conversation %s, waits for a person to put it at %s in %s, which the harness cannot reach to land it itself; waiting since %s, %s ago",
			d.WriteID, what, answers, d.Owner.Title(), d.ConversationID, d.Placement.Path, d.Placement.Repository, localMoment(d.Placement.Since), spanSaid(d.Waited))
	}
	since := "since a time its record does not hold"
	if !d.WrittenAt.IsZero() {
		since = "since " + localMoment(d.WrittenAt)
	}
	waits := "waits for the operator's approval"
	if d.HarnessConfirms {
		waits = "waits for the harness to confirm it and send it to be published, which needs nobody's approval"
	}
	return fmt.Sprintf("document %s, %s written by the %s in conversation %s, %s, %s",
		d.WriteID, what, d.Owner.Title(), d.ConversationID, waits, since)
}

// what names the document and what the write does to it.
func (d DocumentApproval) what() string {
	what := "a revision of " + d.Document
	if d.Action == string(artifact.WriteCreate) {
		what = "a new document " + d.Document
		if d.Kind != "" {
			what = fmt.Sprintf("a new %s document %s", d.Kind, d.Document)
		}
	}
	if d.Title != "" {
		what += fmt.Sprintf(" (%q)", d.Title)
	}
	return what
}

// waitedSince is since when the document has waited on the step its entry
// names: the placement since the harness found it could not land it, and an
// approval since it was written.
func (d DocumentApproval) waitedSince() time.Time {
	if d.Placement != nil {
		return d.Placement.Since
	}
	return d.WrittenAt
}

// reach is the command that reaches the conversation the document waits in.
func (d DocumentApproval) reach() string {
	if d.Owner == domain.RoleProductManager && d.Agent == string(domain.RoleProductManager) {
		return "yoyo chat"
	}
	return "yoyo agent chat " + d.Agent
}

// command is what the operator types to approve or decline it, as the
// conversation it waits in is reached.
func (d DocumentApproval) command() string {
	reach := d.reach()
	return fmt.Sprintf("`%s --message \"approve %s\"` approves it and `%s --message \"decline %s <reason>\"` declines it", reach, d.WriteID, reach, d.WriteID)
}

// approveCommand is what the operator types to approve it, the hand a waiting
// document of a role's own carries.
func (d DocumentApproval) approveCommand() string {
	return fmt.Sprintf("%s --message \"approve %s\"", d.reach(), d.WriteID)
}

// placementCommand is what a person does to put the finished document in
// place, and how the conversation it waits in is told at once rather than at
// its next message.
func (d DocumentApproval) placementCommand() string {
	return fmt.Sprintf("commit the text confirmed as %s to %s on the target branch of %s, then %s --message \"/landed %s <commit>\" records it at once; otherwise it is found at the conversation's next message",
		d.WriteID, d.Placement.Path, d.Placement.Repository, d.reach(), d.WriteID)
}

// whose is whose move the waiting document is and what settles it.
func (d DocumentApproval) whose(mover Mover) string {
	if d.Placement != nil {
		return mover.Possessive() + " — the document is finished and confirmed, and the harness cannot reach " + d.Placement.Repository + " to land it, so a person puts it there: " + d.placementCommand() + "; it leaves this line once the document is found there"
	}
	if d.HarnessConfirms {
		return mover.Possessive() + " — the project's approval setting lets the harness confirm this document itself, which it does at the next message in conversation " + d.ConversationID + ", or once an earlier revision of " + d.Document + " has finished publishing; nobody has to approve it, and it leaves this line once it is sent"
	}
	if mover.IsOperator() {
		return mover.Possessive() + " — it states the product's intent, and a change to it is his to approve; " + d.command() + ", and it leaves this line once he has"
	}
	return mover.Possessive() + " — the project's approval setting for this kind of document asks for a person's approval, which no role may give, so it waits for the operator: " + d.command() + ", and it leaves this line once he has"
}

// documentApprovalAttention is a waiting document as the attention line carries
// it.
func documentApprovalAttention(waiting DocumentApproval) Attention {
	return resolved(Attention{
		Kind:             AttentionDocumentApproval,
		ID:               documentApprovalID(waiting.ConversationID, waiting.WriteID),
		DocumentApproval: &waiting,
	})
}

// documentReading is one reading of the waiting documents and the unanswered
// concerns beside them, as the line carries them, and what could not be read.
type documentReading struct {
	attention []Attention
	problem   string
}

func readDocuments(sources Sources) documentReading {
	attention, problem := readDocumentApprovals(sources)
	return documentReading{attention: attention, problem: problem}
}

// DocumentConfirmations says whether the harness confirms one waiting document
// by itself. It is satisfied by the artifact store and the project's approval
// and integration settings, asked through chat.HarnessConfirms, which asks the
// question the conversation asks before it confirms the document.
type DocumentConfirmations interface {
	HarnessConfirms(role domain.AgentRole, conversationID string, write runstate.PendingWrite) (bool, error)
}

// readDocumentApprovals is every document waiting for the operator's approval,
// across every recorded conversation, longest waiting first, and after them
// every concern a conversation raised that nobody has answered, which waits on
// his answer for the same reason a document waits on his approval. A document the
// conversation keeps is waiting on him unless a reviewed run is carrying it,
// which is the harness's, its owner has held it for sending, which is the
// owner's until the item its hold names closes, or the harness holds it back
// behind another copy of the same document still publishing, a wait that ends
// by itself and is nobody's decision (runstate.PendingWrite.AwaitsDecision;
// chat.Session.Writes reads the same three the same way).
//
// A document not yet confirmed otherwise waits for the conversation's next
// message before the harness confirms it or puts it to the operator. One the
// harness confirms by itself is listed as the harness's, so it is never shown
// as his decision. Where the answer cannot be had — no DocumentConfirmations,
// or the store cannot read the document — it is listed as his, as it was
// before the question was asked.
//
// A confirmed document the harness cannot land, because it cannot reach the
// repository the document is kept in, is listed too: what is left of it is a
// person putting it there (runstate.PendingWrite.AwaitsPlacement). It leaves
// the list once its conversation finds it in place, which settles it.
func readDocumentApprovals(sources Sources) ([]Attention, string) {
	if sources.Conversations == nil {
		return nil, ""
	}
	recorded, err := sources.Conversations.Recorded()
	if err != nil {
		return nil, fmt.Sprintf("the documents waiting for the operator's approval could not be read: %v", err)
	}
	now := sources.now()
	var waiting []DocumentApproval
	var concerns []Attention
	for _, conversation := range recorded {
		agent := conversation.Agent
		if agent == "" {
			agent = string(conversation.Role)
		}
		for _, write := range conversation.PendingWrites {
			if !write.AwaitsDecision() && write.AwaitsPlacement == nil {
				continue
			}
			// A document waiting to be put in place is already confirmed, so nobody
			// is asked whether the harness confirms it.
			harness := false
			if write.AwaitsPlacement == nil && sources.DocumentConfirmations != nil {
				if confirms, err := sources.DocumentConfirmations.HarnessConfirms(conversation.Role, conversation.ConversationID, write); err == nil {
					harness = confirms
				}
			}
			var waited time.Duration
			if write.AwaitsPlacement != nil {
				waited = max(now.Sub(write.AwaitsPlacement.Since), 0)
			}
			document := DocumentApproval{
				WriteID: write.ID, ConversationID: conversation.ConversationID, Agent: agent, Owner: conversation.Role,
				Action: write.Action, Document: strings.TrimSpace(write.Artifact), Kind: write.Kind,
				Title: strings.TrimSpace(write.Title), Directory: strings.TrimSpace(write.Directory),
				Reason: strings.TrimSpace(write.Reason), Intent: strings.TrimSpace(write.Intent), Section: strings.TrimSpace(write.Section),
				Serves: servedItems(write), Body: write.Body, ProductIntent: productIntent(conversation.Role, write),
				DocumentKind:    documentKind(write),
				HarnessConfirms: harness,
				Turn:            write.Turn, WrittenAt: write.WrittenAt,
				Placement: write.AwaitsPlacement, Waited: waited, Answers: write.Answers,
			}
			if document.Action != string(artifact.WriteCreate) && strings.TrimSpace(write.Body) != "" {
				path := ""
				if revised := write.WithRelations().Revises; len(revised) > 0 {
					path = revised[0].Path
				}
				current, unread := readBefore(sources.Repository, path, document.Section)
				if unread != "" {
					document.BeforeUnread = unread
				} else {
					document.Before, document.After = beforeAndAfter(current, write.Body)
				}
			}
			waiting = append(waiting, document)
		}
		// The concerns it raised that nobody has answered wait on the operator
		// beside its documents, in the order it raised them.
		for _, concern := range conversation.PendingConcerns {
			concerns = append(concerns, concernAttention(concernWait(conversation, agent, concern)))
		}
	}
	// A record holding no moment sorts last rather than first: it is not known
	// to be the oldest.
	sort.SliceStable(waiting, func(i, j int) bool {
		first, second := waiting[i].waitedSince(), waiting[j].waitedSince()
		if first.IsZero() != second.IsZero() {
			return second.IsZero()
		}
		if !first.Equal(second) {
			return first.Before(second)
		}
		return documentApprovalID(waiting[i].ConversationID, waiting[i].WriteID) < documentApprovalID(waiting[j].ConversationID, waiting[j].WriteID)
	})
	attention := make([]Attention, 0, len(waiting)+len(concerns))
	for _, each := range waiting {
		attention = append(attention, documentApprovalAttention(each))
	}
	return append(attention, concerns...), ""
}

// servedItems is the work items a written document serves: the ones it is the
// design or ruling for, then any other it names, each once.
func servedItems(write runstate.PendingWrite) []string {
	var served []string
	for _, id := range append(slices.Clone(write.Answers), write.WithRelations().Names...) {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(served, id) {
			served = append(served, id)
		}
	}
	return served
}

// documentKind is the kind of the document a write changes, as far as its
// record says.
func documentKind(write runstate.PendingWrite) string {
	if kind := strings.TrimSpace(write.Kind); kind != "" {
		return kind
	}
	return strings.TrimSpace(write.DocumentKind)
}

// productIntent reports a write of the product's intent. A role files a
// document only in its kind's home, so the kind decides; a revision may name
// none, and then it is the product's intent where the role that wrote it is
// the one that owns the brief, since that role owns no other kind.
func productIntent(role domain.AgentRole, write runstate.PendingWrite) bool {
	if kind := documentKind(write); kind != "" {
		return artifact.IntentKind(artifact.Kind(kind))
	}
	owner, known := artifact.Owner(artifact.KindBrief)
	return known && owner == role
}
