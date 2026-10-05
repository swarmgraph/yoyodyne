// Package spend turns one provider invocation into one line in the cost log.
//
// It is a wrapper around a provider rather than a function beside one on
// purpose. What the harness has to guarantee is that every priced invocation
// lands in the log exactly once at the moment its cost is known, and a recording
// step a caller makes after invoking is a step a caller can forget, take twice,
// or skip on the path where the invocation failed -- which is the path where the
// money was spent and nothing came back. Invoking through this makes the
// invocation and the line the same statement.
//
// The classification is decided here for the same reason. A provider that ends
// an invocation without saying what it cost has not said the invocation was
// free, and every caller working that out for itself is every caller getting a
// chance to record a zero.
package spend

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// processBuild is the revision this binary was built from, read once because a
// process does not change binary while it lives — and that is exactly the
// problem it answers: a long-lived one goes on making invocations from what it
// was started with while the harness moves on underneath it.
//
// It is taken here rather than passed in with the rest of the attribution for
// the reason the recording itself is not left to callers: a build a call site
// could supply is one a call site could forget, and the line would then say
// which account paid for an invocation without saying which harness made it. A
// binary that carries no revision leaves it empty, which reads as a comparison
// nobody can make.
var processBuild = buildinfo.Commit()

// Provider is the invocation half of a provider backend. It is the narrow view
// every role already takes of one, so a metered provider drops in wherever an
// unmetered one was wired.
type Provider interface {
	Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error)
}

// Log is where a line is appended, and what says how much of a session has
// already been paid for. It is satisfied by runstate.SpendStore.
//
// The second half is on the interface rather than beside it because a line
// cannot be written without it. A provider resuming a session reports what that
// session has cost since it began, so what this invocation cost is knowable only
// against what the session was last reported at -- and the only record of that
// is the log itself, since a session outlives the process that opened it. An
// implementation that could not answer would be one every line written through
// it recorded the whole session again.
type Log interface {
	Append(line runstate.Spend) error
	// ReportedSessionTotal is the last figure the provider reported for a
	// session, and whether anything has been recorded for it at all. A session
	// nothing has recorded is the first invocation of one, which records what the
	// provider reported whole.
	ReportedSessionTotal(sessionID string) (float64, bool, error)
}

// Attribution is what the harness knows about one invocation and the provider
// does not: which work it served, under whose account, and under which
// configuration. Everything else on a line -- the role, the requested model,
// what it cost -- is on the request or the result and is never asserted here.
type Attribution struct {
	ProductID domain.ProductID
	// Agent is the configured agent filling the role, which is the persona the
	// spend is attributable to where a project configures more than one agent for
	// a role.
	Agent          string
	Phase          runstate.SpendPhase
	AccountAlias   string
	ConfigRevision string
	// Backend is the provider being invoked, taken from configuration so that an
	// invocation which died before returning anything still names what it died
	// on.
	Backend domain.Backend
	// Exactly one of these names what the invocation belongs to, and a run also
	// names the work item it served. The store refuses a line that names none or
	// more than one. A branch review takes the last of them rather than the run
	// identifier: it is not a run, and a line saying it was would be a run id
	// naming no run to whatever later reads these lines back.
	RunID          string
	WorkItemID     string
	ConversationID string
	SideStreamID   string
	ExchangeID     string
	BranchReviewID string
}

// Metered is one provider with the cost log wired behind it. Every invocation it
// serves appends exactly one line, whichever way the invocation went: an
// invocation the provider refused, killed, or answered badly spent money exactly
// as one that succeeded did.
type Metered struct {
	Provider    Provider
	Log         Log
	Attribution Attribution
	Clock       execution.Clock
	// RecordFailure, where a caller sets it, is handed a line that could not be
	// made durable instead of the invocation failing with it.
	//
	// It exists for the one caller whose answer is not reproducible from its own
	// record. A run's answer is a change in a worktree the next attempt starts
	// from, so failing the invocation costs an attempt and loses nothing; a
	// conversation turn's answer is prose the provider has already written and
	// already charged for, and failing it throws that away to report that the
	// bookkeeping behind it did not land. The operator would lose the answer as
	// well as the record, which is a worse trade than the one this makes.
	//
	// A caller that leaves it nil takes the failure, which is what everything but
	// the conversation does.
	RecordFailure func(error)
	// Recorded, where a caller sets it, is handed the line as it was appended.
	//
	// It is how a caller that needs to know what an invocation cost gets the
	// figure, and the reason it exists rather than the caller reading
	// RunResult.CostUSD is that the two are different numbers: the provider
	// reports what a resumed session has cost since it began, and the amount on
	// the line is what this invocation added to it. A caller summing the former
	// over a conversation's turns counts the whole conversation once per turn.
	//
	// It is handed the line rather than the amount so that a caller which needs
	// to tell an unpriced invocation from a free one can, and it is called as
	// soon as the amount is settled rather than once the line is safely stored:
	// a log that refused a line has already said so through RecordFailure, and a
	// budget that also stopped counting over it would be the operator's cap
	// leaking silently behind a failure they were told about. The one thing it is
	// not called for is a line whose amount could not be worked out at all.
	Recorded func(line runstate.Spend)
}

// Run makes the invocation and records what it spent.
//
// A line that cannot be made durable is reported, joined to whatever the
// invocation itself reported. That is the same weight the harness already gives
// a run's event log -- an invocation whose events could not be recorded fails
// rather than carrying on unrecorded -- and it is the weight the cost log needs
// for the same reason: a spend nothing wrote down is money the operator is never
// shown, and the silence looks exactly like not having spent it.
//
// The cost of that choice is stated rather than hidden: an invocation the
// provider already served and already charged for comes back as a failure when
// the bookkeeping behind it fails. Two things bound it. The store refuses only a
// line nobody could attribute, and every call site's attribution comes from
// configuration the loader has already validated or from a run's own validated
// state -- each of those sites has a test that the line it produces satisfies
// the durable contract, so the refusal is not a path a working harness takes.
// What is left is a state root that cannot be written, and that is a root whose
// run state and event log are failing in the same breath; the run is lost either
// way, and the alternative is losing the money silently as well.
//
// A caller for which that trade comes out the other way sets RecordFailure and
// is handed the failure instead of it being joined on.
func (m Metered) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	result, err := m.Provider.Run(ctx, request)
	line := m.line(request, result, err)
	// A provider with nowhere to record what it spends is one nothing is
	// metering. The harness always wires the log; this is what keeps a test
	// harness that does not care about money from having to.
	//
	// The line is still built and still handed back, because a caller counting
	// what it spent is not the same thing as a log. What it cannot be told
	// without one is what a resumed session's figure means: correcting that needs
	// the record of what the session was last reported at, and an unmetered
	// harness has none, so the amount is the provider's figure as it stands.
	if m.Log == nil {
		if m.Recorded != nil {
			m.Recorded(line)
		}
		return result, err
	}
	line, recordErr := m.ownCost(line)
	if recordErr == nil {
		// The amount is settled, so a caller counting what it spent is told even if
		// what follows cannot keep the line. Its figure and the log's are the same
		// number, and a log that refused one line must not also quietly stop a
		// budget counting -- which is the operator's cap leaking rather than a
		// bookkeeping failure they were already told about.
		if m.Recorded != nil {
			m.Recorded(line)
		}
		recordErr = m.Log.Append(line)
	}
	if recordErr != nil {
		recordErr = fmt.Errorf("record what the %s invocation spent: %w", request.Role, recordErr)
		// Either way the failure is reported and never swallowed. What the caller
		// chooses is whether it costs the invocation as well as the record.
		if m.RecordFailure != nil {
			m.RecordFailure(recordErr)
			return result, err
		}
		return result, errors.Join(err, recordErr)
	}
	return result, err
}

// line is what the invocation spent, said once.
func (m Metered) line(request backend.RunRequest, result backend.RunResult, err error) runstate.Spend {
	line := runstate.Spend{
		SchemaVersion:     runstate.SpendSchemaVersion,
		ProductID:         m.Attribution.ProductID,
		At:                m.clock().Now().UTC(),
		Role:              request.Role,
		Agent:             m.Attribution.Agent,
		Phase:             m.Attribution.Phase,
		AccountAlias:      m.Attribution.AccountAlias,
		ConfigRevision:    m.Attribution.ConfigRevision,
		RunID:             m.Attribution.RunID,
		WorkItemID:        m.Attribution.WorkItemID,
		ConversationID:    m.Attribution.ConversationID,
		SideStreamID:      m.Attribution.SideStreamID,
		ExchangeID:        m.Attribution.ExchangeID,
		BranchReviewID:    m.Attribution.BranchReviewID,
		Backend:           m.Attribution.Backend,
		Model:             request.Model,
		ResolvedModel:     result.ResolvedModel,
		Effort:            request.Effort,
		ResolvedEffort:    result.ResolvedEffort,
		EffortDescription: result.EffortDescription,
		EffortReported:    result.EffortReported,
		SessionID:         result.SessionID,
		Build:             processBuild,
	}
	// The provider names the backend that served the invocation, which is the
	// same one the configuration named. It is preferred where it is there because
	// it is what actually ran, and the configured one is what a result that never
	// arrived is left with.
	if result.Backend != "" {
		line.Backend = result.Backend
	}
	// The adapter that reached the provider completes the endpoint identity this
	// line already carries in the account, the backend, and the model. The
	// adapter's own word is preferred for the reason the backend's is — it is what
	// actually ran — and an invocation that died before it could say anything
	// falls back to what this build knows of the backend it was configured for. A
	// provider the project declared has no built-in description, so that line
	// names the provider and no adapter rather than a version nobody established.
	line.AdapterVersion = result.AdapterVersion
	if line.AdapterVersion == "" {
		line.AdapterVersion = backend.AdapterVersionFor(line.Backend)
	}
	if result.CostReported {
		line.Classification = runstate.SpendKnown
		// What the provider said, which ownCost turns into what this invocation
		// cost. The two are the same number on an invocation that opened its
		// session and different ones on every invocation that resumed it.
		line.AmountUSD = result.CostUSD
		return line
	}
	line.Classification = runstate.SpendUnknown
	line.Unknown = unknownReason(err)
	return line
}

// ownCost turns the figure the provider reported into what this invocation
// cost. A provider asked to resume a session reports what the session has cost
// since it began, so the amount recorded is what that total moved by, and the
// reported figure is kept beside it for the session's next invocation to be
// priced against.
//
// A log that cannot say what the session was last reported at stops the line
// being written, and is reported the way a log that cannot be appended to is.
// That is not a hedge about which failure is worse: it is the same failure. The
// total is read from the log this line is about to be appended to, so a log that
// will not answer is a log that is about to refuse the append as well -- and the
// alternative, writing the reported figure as though it were this invocation's
// cost, is the overstatement the amount exists to avoid.
//
// Reading the total and appending the line are not one atomic step, and they do
// not need to be. What they race with is another invocation of the same session
// finishing between them, and a session is a conversation or a run's developer
// taking one turn at a time: the processes that append here concurrently are
// different runs and different conversations, each in a session of its own.
func (m Metered) ownCost(line runstate.Spend) (runstate.Spend, error) {
	if !line.Known() || line.SessionID == "" {
		return line, nil
	}
	reported := line.AmountUSD
	previous, seen, err := m.Log.ReportedSessionTotal(line.SessionID)
	if err != nil {
		return line, fmt.Errorf("read what session %s has already been reported at: %w", line.SessionID, err)
	}
	own := runstate.OwnCostUSD(reported, previous, seen)
	if own == reported {
		return line, nil
	}
	line.AmountUSD = own
	line.ReportedTotalUSD = reported
	return line, nil
}

// unknownReason says why nobody knows what an invocation cost, in as much of the
// provider's own account of it as the line will hold. An invocation that failed
// before the provider reported anything and one that reported everything except
// the cost are different accidents, and which of them happened is what somebody
// reconciling a bill has to know.
func unknownReason(err error) string {
	if err == nil {
		return "the provider ended the invocation without reporting what it cost"
	}
	// The bound cuts the tail of a message, and never a rune in half: a line the
	// store would refuse for being unreadable is a spend lost to a long error.
	return oneline.Bound("the invocation failed before the provider reported what it cost: "+err.Error(), runstate.MaxSpendUnknownBytes)
}

func (m Metered) clock() execution.Clock {
	if m.Clock == nil {
		return execution.RealClock{}
	}
	return m.Clock
}
