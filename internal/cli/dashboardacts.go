package cli

// The operator's decisions, taken from the dashboard and carried out here by
// the code the command-line verbs run.
//
// The dashboard decides who is acting and whether they may (internal/dashboard,
// acts.go); this is what it hands an allowed act to, and it invents nothing the
// command line cannot do. A proposed change to a document is decided through
// recordAmendmentDecision, which is `yoyo amendment approve` and `decline`. A
// document an owning role wrote, and work an agent proposed, are decided in
// that agent's conversation by the message `yoyo agent chat <agent> --message`
// carries — "approve document-4.1", "decline 3.1 <reason>" — read by the same
// session methods, which record the operator's approval exactly as they do for
// a message typed at a terminal. Unlike that command, a message here that
// turns out to decide nothing is refused rather than said to the agent, so a
// decision made elsewhere in the meantime never becomes a provider turn.
//
// Asking first is the one act that reaches a role: the question goes to the
// conversation of the role that raised the decision, or the Lead Product
// Manager's where that role cannot be reached in one, as the message
// `yoyo agent chat <agent> --message` sends, and decides nothing. It is sent in
// the background, under the dashboard's own lifetime, because a turn takes
// minutes and a page cannot hold a request open that long; the answer is in
// that conversation, where every answer the role gives is.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// dashboardActor is the dashboard's Actor over this product's records.
type dashboardActor struct {
	configPath string
	// lifetime is the dashboard's own, which an ask's turn runs under once the
	// request that asked it has been answered.
	lifetime context.Context
	stderr   io.Writer
	// asking is the conversations a question from the page is in flight in, so
	// a second press waits for the first rather than queuing a second turn.
	mu     sync.Mutex
	asking map[string]bool
	// converse is the conversation one message is sent through; it is converse
	// itself everywhere but a test.
	converse func(ctx context.Context, role domain.AgentRole, request conversationRequest, stdin io.Reader, stdout, stderr io.Writer) int
	// decideInConversation carries out one decision message in an agent's
	// conversation; it is decideInConversation everywhere but a test.
	decideInConversation func(ctx context.Context, configPath string, decision readmodel.OperatorDecision, message string, stderr io.Writer) (string, error)
}

var _ dashboard.Actor = (*dashboardActor)(nil)

func newDashboardActor(lifetime context.Context, configPath string, stderr io.Writer) *dashboardActor {
	return &dashboardActor{
		configPath: configPath, lifetime: lifetime, stderr: stderr, asking: map[string]bool{},
		converse: converse,
		decideInConversation: func(ctx context.Context, configPath string, decision readmodel.OperatorDecision, message string, stderr io.Writer) (string, error) {
			return decideInConversation(ctx, lifetime, configPath, decision, message, stderr)
		},
	}
}

// Find reads the decision fresh, from the same derivation the page's list
// came from.
func (a *dashboardActor) Find(_ context.Context, kind readmodel.DecisionKind, agent, id string) (readmodel.OperatorDecision, error) {
	found, ok, problem := readmodel.FindOperatorDecision(standingSources(a.configPath), kind, agent, id)
	if ok {
		return found, nil
	}
	if problem != "" {
		return readmodel.OperatorDecision{}, errors.New(problem)
	}
	return readmodel.OperatorDecision{}, dashboard.ErrNotWaiting
}

// Decide carries the decision out with the verb that makes it.
func (a *dashboardActor) Decide(ctx context.Context, decision readmodel.OperatorDecision, approve bool, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if decision.Kind == readmodel.DecisionAmendment {
		return a.decideAmendment(decision, approve, reason)
	}
	verb := "decline"
	if approve {
		verb = "approve"
	}
	message := verb + " " + decision.ID
	if !approve && reason != "" {
		message += " " + reason
	}
	return a.decideInConversation(ctx, a.configPath, decision, message, a.stderr)
}

func (a *dashboardActor) decideAmendment(decision readmodel.OperatorDecision, approve bool, reason string) (string, error) {
	resolved, err := loadConfiguration(a.configPath)
	if err != nil {
		return "", err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return "", err
	}
	store, err := runstate.NewAmendmentStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return "", err
	}
	verdict, how := amendment.VerdictDeclined, "the dashboard's Decline"
	if approve {
		verdict, how = amendment.VerdictApproved, "the dashboard's Approve"
	}
	found, _, err := recordAmendmentDecision(store, a.configPath, decision.ID, verdict, reason, how, a.stderr)
	if err != nil {
		return "", err
	}
	if approve {
		return fmt.Sprintf("approved the proposed change %s to %s, under the %s's authority; nothing was written to %s, and the %s makes the change",
			found.ID, found.Artifact, found.Owner.Title(), found.Artifact, found.Owner.Title()), nil
	}
	return fmt.Sprintf("declined the proposed change %s to %s, under the %s's authority, because: %s", found.ID, found.Artifact, found.Owner.Title(), reason), nil
}

// decideInConversation opens the agent's conversation and carries out one
// decision message there, as `yoyo agent chat <agent> --message` does before
// it would speak to the agent. A conversation taking a turn is not waited
// for: the page is told to try again, rather than held open for minutes.
//
// An approval that hands a document to a reviewed run is followed by that
// run's publication, as the command line follows it (reportChatWriteDecisions).
// The run takes as long as its checks and review do, so it is started in the
// background under the dashboard's own lifetime, still holding the
// conversation, and the page is answered at once with what was started.
func decideInConversation(ctx, lifetime context.Context, configPath string, decision readmodel.OperatorDecision, message string, stderr io.Writer) (string, error) {
	prepared, err := prepareChat(ctx, decision.Role, decision.Agent, configPath, stderr)
	if err != nil {
		return "", err
	}
	hold, err := prepared.store.TryClaim(prepared.identity)
	if errors.Is(err, runstate.ErrConversationHeld) {
		return "", fmt.Errorf("the %s's conversation is taking a turn right now; decide again once it has ended", decision.Role.Title())
	}
	if err != nil {
		return "", err
	}
	held := true
	defer func() {
		if held {
			hold.Release()
		}
	}()
	session, err := prepared.open(ctx, hold, false, true, stderr)
	if err != nil {
		return "", err
	}
	said, publish, err := decideInSession(ctx, session, decision, message)
	if err != nil || !publish {
		return said, err
	}
	held = false
	go func() {
		defer hold.Release()
		publishFromDashboard(lifetime, session, decision, stderr)
	}()
	return said + " " + publishingStarted(decision), nil
}

// decideInSession carries out one decision message in an open conversation,
// and says whether a document it approved waits on its reviewed run, which
// whoever took the decision starts straight after (publishFromDashboard). A
// document is decided by DecideWrites, which saves the conversation's record
// as it decides, so a declined document leaves the record and with it the
// attention line and the page.
func decideInSession(ctx context.Context, session *chat.Session, decision readmodel.OperatorDecision, message string) (string, bool, error) {
	if decision.Kind == readmodel.DecisionDocument {
		outcomes, decided, err := session.DecideWrites(message)
		if !decided {
			return "", false, dashboard.ErrNotWaiting
		}
		var rendered strings.Builder
		for _, outcome := range outcomes {
			rendered.WriteString(outcome.Render())
		}
		if err != nil {
			return "", false, fmt.Errorf("%w%s", err, prefixed(rendered.String()))
		}
		publish := slices.ContainsFunc(outcomes, func(outcome chat.WriteOutcome) bool { return outcome.Publishing != "" })
		return strings.TrimSpace(rendered.String()), publish, nil
	}
	decided, settled, err := session.Decide(ctx, message)
	if !settled {
		return "", false, dashboard.ErrNotWaiting
	}
	if err != nil {
		return "", false, fmt.Errorf("%w%s", err, prefixed(renderDecisions(decided)))
	}
	return strings.TrimSpace(renderDecisions(decided)), false, nil
}

// publishFromDashboard publishes the conversation's approved documents, as
// the command line does after an approval, and says where what came of it is
// recorded when it could not finish: the owning role is told in its
// conversation either way.
func publishFromDashboard(lifetime context.Context, session *chat.Session, decision readmodel.OperatorDecision, stderr io.Writer) {
	if _, err := session.PublishApproved(lifetime); err != nil {
		fmt.Fprintf(stderr, "the reviewed run for document %s, approved from the dashboard, could not start or finish: %v; it is kept and tried again at the next message to the %s\n",
			decision.ID, err, decision.Role.Title())
	}
}

// publishingStarted is what the page is told once the reviewed run is asked
// for.
func publishingStarted(decision readmodel.OperatorDecision) string {
	return fmt.Sprintf("Its reviewed run has been started; `yoyo status` shows it until it merges, and the %s's conversation says how it went.", decision.Role.Title())
}

func prefixed(rendered string) string {
	if strings.TrimSpace(rendered) == "" {
		return ""
	}
	return "; " + strings.TrimSpace(rendered)
}

// Ask sends the person's question to the role that raised the decision.
func (a *dashboardActor) Ask(_ context.Context, decision readmodel.OperatorDecision, question string) (string, error) {
	question = strings.TrimSpace(question)
	message := fmt.Sprintf("Before deciding %s %s (%s), the operator asks from the dashboard: %s", decision.Label, decision.ID, decision.Subject, question)
	if len(message) > chat.MaxOperatorMessageBytes {
		return "", fmt.Errorf("the question is %d bytes with what it is about, and a message is at most %d", len(message), chat.MaxOperatorMessageBytes)
	}
	role, agent, fallback := a.askWhom(decision)
	key := string(role) + ":" + agent
	a.mu.Lock()
	if a.asking[key] {
		a.mu.Unlock()
		return "", fmt.Errorf("a question from this dashboard to the %s is still being answered; ask again once it has been", role.Title())
	}
	a.asking[key] = true
	a.mu.Unlock()
	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.asking, key)
			a.mu.Unlock()
		}()
		code := a.converse(a.lifetime, role, conversationRequest{agentName: agent, configPath: a.configPath, message: message, jsonOutput: true}, strings.NewReader(""), io.Discard, a.stderr)
		if code != 0 {
			fmt.Fprintf(a.stderr, "the question from the dashboard about %s %s was not answered by the %s; its conversation says why\n", decision.Label, decision.ID, role.Title())
		}
	}()
	reads := "yoyo chat"
	if role != domain.RoleProductManager {
		reads = "yoyo agent chat " + agent
	}
	said := fmt.Sprintf("asked the %s about %s %s in its conversation; its answer is recorded there and read with `%s`, and nothing was decided", role.Title(), decision.Label, decision.ID, reads)
	if fallback != "" {
		said += "; " + fallback
	}
	return said, nil
}

// askWhom is whose conversation a question goes to: the agent that raised the
// decision where the configuration names it and the harness holds a
// conversation contract for its role, and the Lead Product Manager otherwise,
// with the reason.
func (a *dashboardActor) askWhom(decision readmodel.OperatorDecision) (domain.AgentRole, string, string) {
	resolved, err := loadConfiguration(a.configPath)
	if err == nil && decision.AskAgent != "" {
		if name, role, err := resolveAgent(resolved.Config, decision.AskAgent); err == nil {
			if _, known := chat.AuthorityFor(role); known {
				return role, name, ""
			}
		}
	}
	if decision.AskRole == domain.RoleProductManager {
		return domain.RoleProductManager, "", ""
	}
	return domain.RoleProductManager, "", fmt.Sprintf("the %s that raised it cannot be reached in a conversation here, so the Lead Product Manager was asked instead", decision.AskRole.Title())
}
