package orchestrator

// A change an agent proposes to a document it does not own leaves the run
// through here. Nothing in this file may change what a run did: a proposal is
// recorded beside the run exactly as a report is, it decides nothing about the
// work, and a proposal the harness cannot read or cannot keep is named on the
// outcome rather than failing the attempt it arrived with.
//
// That is the point of the channel. A developer that had to choose between
// editing the design and being ignored would eventually edit the design; a
// developer whose proposal costs it nothing and reaches the architect has no
// reason to.

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// AmendmentRecorder is where proposed artifact changes are kept. It is
// satisfied by runstate.AmendmentStore.
type AmendmentRecorder interface {
	Append(proposal amendment.Proposal) error
}

// collectAmendments records the changes one agent invocation proposed. Every
// failure here is noted and swallowed, for the same reason a report's is: a run
// that failed because an agent argued with the design would teach every agent to
// stop arguing with it.
func (a *activeRun) collectAmendmentsWithoutToolAudit(role domain.AgentRole, entries []amendment.Entry) {
	if len(entries) == 0 {
		return
	}
	// A pipeline with nowhere to record loses all of them at once, and says so
	// once: one note per lost proposal would describe the same missing store
	// several times over.
	if a.pipeline.Amendments == nil {
		a.noteAmendmentProblem(role, fmt.Errorf("nothing records proposed amendments for this run, so the %d change(s) the %s proposed were not kept", len(entries), role))
		return
	}
	artifacts, err := a.artifacts()
	if err != nil {
		a.noteAmendmentProblem(role, err)
		return
	}
	collected, problem := amendment.Collect(entries, amendment.Attribution{
		Role:         role,
		Agent:        a.pipeline.agentNameForRole(role),
		RunID:        a.state.RunID,
		WorkItemID:   a.state.WorkItemID,
		ProductID:    a.pipeline.Config.Product.ID,
		RepositoryID: string(a.pipeline.Config.Product.RepositoryID),
	}, artifacts, a.pipeline.clock().Now())
	if problem != nil {
		// Collection is per-proposal, so this names the ones that could not be
		// recorded while the ones that could still are.
		a.noteAmendmentProblem(role, problem)
	}
	for _, proposal := range collected {
		// A developer that could not be talked out of its argument makes it again on
		// every repair attempt, and each collection would otherwise mint a fresh id
		// for it: one disagreement would arrive as up to repair_attempts_before_replan
		// separate proposals, and whoever decides would answer the same argument
		// several times to clear the queue. The second and later copies within a run
		// are dropped rather than raised, and the drop is written onto the run's
		// record beside what it was folded into, because a fold the comparison got
		// wrong costs the second argument its decision and nothing else would say so.
		if folded, likeness, ok := a.restatedAmendment(proposal); ok && a.canRecordAmendment() {
			a.recordRunAmendment(runstate.RunAmendment{
				Role:       role,
				Artifact:   proposal.Artifact,
				Change:     proposal.Change,
				FoldedInto: folded,
				Likeness:   likeness,
			})
			continue
		}
		if err := a.pipeline.Amendments.Append(proposal); err != nil {
			a.noteAmendmentProblem(role, err)
			continue
		}
		// Recorded on the run only once it is actually in the log, so a proposal the
		// log refused is not treated as already made: if the developer argues it
		// again on the next attempt and the log has recovered, that attempt keeps it.
		a.recordRunAmendment(runstate.RunAmendment{
			Role:     role,
			Artifact: proposal.Artifact,
			Change:   proposal.Change,
			ID:       proposal.ID,
		})
		a.outcome.Amendments = append(a.outcome.Amendments, proposal)
	}
}

// amendmentArgument is a recorded proposal reduced to what decides whether the
// next one is the same argument: the document it is about, and the content words
// of what it asks for. The reasoning is deliberately no part of it — a developer
// that restates its case differently is making the same request, and treating
// that as new would defeat the whole of this.
//
// What the reduction adds is that the *change* may be reworded too, which the
// literal comparison this replaces could not see. A developer asked for a repair
// writes its block again from scratch rather than copying the one before it, so
// the same request arrives spelled differently: run-62e78d87 proposed five
// changes to one design and two pairs of them were one argument each, one pair
// differing by three words and the other rewritten end to end.
type amendmentArgument struct {
	artifact string
	// words is the change with its function words removed, which is what carries
	// the request. Those words are what any two pieces of English prose share
	// whatever they say, so leaving them in raises every pair's likeness toward
	// every other's and narrows exactly the gap this is being asked to read: on
	// the five proposals above it halves the margin between the pairs that are
	// one argument and the closest pair that is not.
	words map[string]bool
	// folded is the change itself, for the proposal whose wording is function
	// words and nothing else. There is no request left to compare there, so the
	// only safe reading of it is the literal one.
	folded string
}

// amendmentRestatementLikeness is how alike two changes to one document must be
// to be one argument: the share of content words they have in common, counted
// over the words in either of them.
//
// It is measured rather than guessed. On run-62e78d87's five proposals the two
// pairs the architect decided as one argument each score 0.47 and 0.97, and the
// closest pair decided as two — both asking for the same fact to be recorded,
// in different sections of the design — scores 0.28. This sits nearer the
// duplicates than the midpoint deliberately, because the two errors do not cost
// the same: letting a restatement through costs the owner a second copy of an
// argument they are already reading, and folding two arguments into one costs
// the second of them its decision, silently. So it leaves 0.12 between itself
// and the closest pair that is not one argument, against 0.07 between itself
// and the closest pair that is.
//
// The evidence is one run's five proposals, which is all there is: those are
// the only amendments anybody has decided. A second run's worth is worth
// re-measuring against.
const amendmentRestatementLikeness = 0.4

func amendmentArgumentOf(proposal amendment.Proposal) amendmentArgument {
	return amendmentArgument{
		artifact: proposal.Artifact,
		words:    amendmentContentWords(proposal.Change),
		folded:   strings.Join(strings.Fields(strings.ToLower(proposal.Change)), " "),
	}
}

// sameAmendmentArgument reports whether a proposal asks for something this run
// has already recorded. Two changes to different documents are never one
// argument however alike they read: the owner decides them one document at a
// time, and a document is the one thing about a proposal the agent does not get
// to assert loosely.
func sameAmendmentArgument(recorded, proposed amendmentArgument) bool {
	return amendmentLikeness(recorded, proposed) >= amendmentRestatementLikeness
}

// amendmentLikeness is how alike two proposals read, from 0 to 1: the share of
// content words their changes have in common, 0 for changes to different
// documents, and for a change with no content words either 1 or 0 as the two
// read literally the same or not. It is what a dropped restatement is recorded
// with, so whoever doubts a fold can see how close to the boundary it was.
func amendmentLikeness(recorded, proposed amendmentArgument) float64 {
	if recorded.artifact != proposed.artifact {
		return 0
	}
	if len(recorded.words) == 0 || len(proposed.words) == 0 {
		if recorded.folded == proposed.folded {
			return 1
		}
		return 0
	}
	shared := 0
	for word := range proposed.words {
		if recorded.words[word] {
			shared++
		}
	}
	union := len(recorded.words) + len(proposed.words) - shared
	return float64(shared) / float64(union)
}

// amendmentContentWords is the change reduced to the words that carry the
// request: lower-cased, split on everything that is not a letter or a digit, and
// with the function words below dropped.
func amendmentContentWords(change string) map[string]bool {
	words := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(change), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if !amendmentFunctionWords[word] {
			words[word] = true
		}
	}
	return words
}

// amendmentFunctionWords are the English words a proposal shares with every other
// proposal because it is a sentence, rather than because it asks for the same
// thing. It is articles, conjunctions, prepositions, pronouns, and auxiliaries,
// and deliberately nothing from this product's own vocabulary: a list that
// dropped "design" or "grant" would be tuning the comparison on the arguments it
// has already seen.
var amendmentFunctionWords = func() map[string]bool {
	words := map[string]bool{}
	for _, word := range strings.Fields(`a about an and any are as at be been being between both but by
can could did do does each every for from had has have if in into is it its may
might must no nor not of on onto or other over own s same should so some such
than that the their them then there these they this those to under until up was
were what when where which while who whom whose will with within would`) {
		words[word] = true
	}
	return words
}()

// restatedAmendment reports the raised proposal on this run's record that a new
// one restates, with how alike the two read. Where several are alike enough it
// names the likeliest, since that is the one a reader checking the fold should be
// sent to. The record is the run's durable state rather than anything this
// process holds, so a run continued in another process compares against every
// proposal the run has raised, wherever it was raised.
func (a *activeRun) restatedAmendment(proposal amendment.Proposal) (string, float64, bool) {
	argument := amendmentArgumentOf(proposal)
	folded, best := "", 0.0
	for _, recorded := range a.state.Amendments {
		if recorded.Dropped() {
			continue
		}
		likeness := amendmentLikeness(amendmentArgumentOf(amendment.Proposal{Artifact: recorded.Artifact, Change: recorded.Change}), argument)
		if likeness >= amendmentRestatementLikeness && likeness > best {
			folded, best = recorded.ID, likeness
		}
	}
	return folded, best, folded != ""
}

// canRecordAmendment reports whether the run's record has room for one more
// proposal. A restatement is only dropped where it can be: a drop the record
// cannot hold is an argument lost with nothing to find it by, so past the bound
// the restatement is raised instead, which costs its owner a second copy at
// worst.
func (a *activeRun) canRecordAmendment() bool {
	return len(a.state.Amendments) < runstate.MaxRunAmendments
}

// recordRunAmendment writes one proposal onto the run's record, or nothing once
// the record is full. A raised proposal the record has no room for is still in
// the amendment log and still reaches its owner; what it costs is that a later
// restatement of it is raised too rather than folded.
func (a *activeRun) recordRunAmendment(proposed runstate.RunAmendment) {
	if !a.canRecordAmendment() {
		return
	}
	a.state.Amendments = append(a.state.Amendments, proposed)
}

// artifacts is the recorded artifact set a proposal's document is resolved
// against, read once per run and only when something actually proposes a
// change. Loading it is deliberately not part of starting a run: a repository
// with no artifact homes yet runs work exactly as it always did, and only a run
// that proposes a change to a document is told there are none.
func (a *activeRun) artifacts() (artifact.Set, error) {
	if a.artifactSet != nil {
		return *a.artifactSet, nil
	}
	// The homes and what is excluded from them are assembled in one place rather
	// than restated here, so a proposal resolves against exactly the set every
	// other reader sees. The invariants are excluded there: they carry the
	// identity scheme this one was modeled on rather than this one, and an
	// invariant is amended through its own lifecycle either way.
	store := artifact.StoreFor(a.pipeline.Repository, a.pipeline.Config.Product)
	set, err := store.Load()
	if err != nil {
		return artifact.Set{}, fmt.Errorf("load the recorded artifacts to resolve the proposal against: %w", err)
	}
	a.artifactSet = &set
	return set, nil
}

// maxAmendmentProblemBytes keeps one lost proposal to a readable line of the
// outcome.
const maxAmendmentProblemBytes = 512

// noteAmendmentProblem records a proposal that did not reach the durable log. It
// accumulates rather than replaces, because losing the first proposal and then
// losing a second is two facts.
//
// It goes to three places. The outcome is what reaches the operator, and it was
// for a long time the only place it reached: the agent that wrote the block was
// never told, so a developer whose proposal was refused carried on believing it
// was waiting on somebody, and the outcome itself was gone with the process, so
// afterwards a refused proposal read exactly as one never made. The other two
// are on the run's own state, and they answer those two failures one each: the
// carried refusal is what the proposing role's next invocation opens with, and
// the recorded problem is what an auditor reads once the run is over.
func (a *activeRun) noteAmendmentProblem(role domain.AgentRole, cause error) {
	problem := fmt.Sprintf("a change the %s proposed was not recorded: %s", role, singleLine(cause.Error(), maxAmendmentProblemBytes))
	a.carryAmendmentRefusal(role, problem)
	if a.outcome.AmendmentProblem == "" {
		a.outcome.AmendmentProblem = problem
	} else {
		a.outcome.AmendmentProblem += "; " + problem
	}
	a.state.AmendmentProblem = runstate.RecordChannelProblem(a.outcome.AmendmentProblem)
}

// carryAmendmentRefusal puts one refusal where the role that earned it reads it
// next. The words are the harness's own and are carried verbatim: what is wrong
// with the block is the whole of what its author needs to write a different one,
// and a paraphrase is the harness guessing at that.
//
// The role is recorded with them because every reader of this list is a
// particular agent being told what it itself proposed. Only the developer's reply
// is scanned for proposals today, so only the developer can earn one — but this
// is called with whatever role collected, and a refusal that arrived without its
// proposer would be shown to whoever was invoked next under a heading claiming
// they wrote it, while its actual author went on believing it had landed. That is
// the failure this exists to end, moved one role over, so what decides who is
// shown a refusal is recorded rather than assumed.
//
// Past the bound the refusal is kept on the outcome and dropped from what is
// carried, which is the direction that cannot cost an agent anything it has not
// already been told: a role's own reply drops what that role was shown, so a run
// that fills this has proposed more changes than one block may carry several
// times over, and the first ones say the same thing as the rest.
func (a *activeRun) carryAmendmentRefusal(role domain.AgentRole, problem string) {
	if len(a.state.RefusedAmendments) >= runstate.MaxCarriedAmendmentRefusals {
		return
	}
	a.state.RefusedAmendments = append(a.state.RefusedAmendments, runstate.AmendmentRefusal{
		Role:    role,
		Problem: singleLine(problem, runstate.MaxAmendmentRefusalBytes),
	})
}

// clearCarriedAmendmentRefusals spends the refusals one role's invocation was
// shown, and leaves every other role's where they are: what the clear records is
// that this role has been told, which says nothing about anybody else.
//
// It is called where the reply to that invocation is recorded rather than where
// the prompt is built, so a prompt rebuilt after an interrupted attempt still
// carries them — only a reply proves the agent was actually told.
func (a *activeRun) clearCarriedAmendmentRefusals(role domain.AgentRole) {
	kept := make([]runstate.AmendmentRefusal, 0, len(a.state.RefusedAmendments))
	for _, refused := range a.state.RefusedAmendments {
		if refused.Role != role {
			kept = append(kept, refused)
		}
	}
	if len(kept) == 0 {
		a.state.RefusedAmendments = nil
		return
	}
	a.state.RefusedAmendments = kept
}

// openWithDeveloperRefusals puts the developer's own refused proposals in front
// of a prompt the developer is about to be sent. The role is named here rather
// than left implicit: what is prepended is the developer's refusals, and another
// role's would be this developer told it wrote something it never wrote. It is
// applied once per prompt, where the prompt is built, and never to a prompt
// already built: a reissue after a refused or killed attempt sends the same
// prompt again, and the refusals it opened with are still carried because only
// a reply spends them.
func (a *activeRun) openWithDeveloperRefusals(prompt string) string {
	return carriedAmendmentRefusals(domain.RoleDeveloper, a.state.RefusedAmendments) + prompt
}

// carriedAmendmentRefusals is what one role's next turn opens with, ahead of
// everything else it is handed. It renders that role's own refusals and no
// others: the whole of what it says is "you proposed this and it was not
// recorded", which is false of anything another agent proposed.
//
// It says the same three things a refused tracker block says to the role that
// sent it: nothing happened, here is why in the harness's own words, and ask
// again if you still want it. The one it adds is the durable-record half, because
// that is the failure this exists to end — a developer that believed its proposal
// had landed wrote into a checked-in document that it had raised one, and the
// claim outlived the run that made it.
//
// That half is worded twice on purpose. Telling the agent not to write the claim
// only binds what it does next, and the refusal is discoverable no earlier than
// the reply that carried it: by the time this is read, the attempt that proposed
// has already had its turn, and in run-6ff896ba that is exactly the turn the false
// claim was written in. So it also asks for the claim to be taken back out, which
// is the only thing that repairs a record already made.
func carriedAmendmentRefusals(role domain.AgentRole, refused []runstate.AmendmentRefusal) string {
	var mine []string
	for _, refusal := range refused {
		if refusal.Role == role {
			mine = append(mine, refusal.Problem)
		}
	}
	if len(mine) == 0 {
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString("# A change you proposed was not recorded\n\n")
	rendered.WriteString("The harness refused it, so nothing is waiting on anybody: no owner was asked, and no decision will ever come back. ")
	rendered.WriteString("Do not describe the proposal as raised, and do not write into your change — or into anything else that outlives this run — that you have raised one.\n\n")
	rendered.WriteString("The refusal, in the harness's own words:\n\n")
	for _, problem := range mine {
		rendered.WriteString("- " + problem + "\n")
	}
	rendered.WriteString("\nIf the attempt that proposed it already wrote anywhere that you had raised this proposal — into the change, a comment, or a note — take that back out now. It was never true, and left there it is the record this run leaves behind.\n\n")
	rendered.WriteString("If the change is still worth proposing, propose it again in a block that answers what the refusal says was wrong with the one before it. If it is not, say so in your summary and leave it at that.\n\n")
	return rendered.String()
}
