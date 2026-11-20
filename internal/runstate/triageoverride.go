package runstate

// Crossing one work item's triage caps: the operator's own override, and the
// bounded crossing the development manager takes himself.
//
// The caps beside this exist to stop machines looping, and they do it by
// refusing: a development manager past an item's round budget may escalate or
// re-scope, and may not hand the item back again. That is the whole of what they
// are for and it is right — until the escalation is answered, at which point they
// refuse the answer as well. An item at five rounds of four could have no re-run
// recorded against it, the re-run verb refuses without that record, and the
// escalation entry records neither, so an operator who read the escalation and
// decided the work should be run again had no recorded path to say so. The
// decision was carried out by admitting fresh work in its place, twice, which is
// surgery rather than a path.
//
// So a cap is crossable. An override is the decision written down: the budget it
// lifts, what it lifts it to, who decided and when, and why. An unbounded raise
// and a cleared budget are the operator's own hand and stay there, recorded by a
// terminal command for the reason `yoyo release` is one — the switch that answers
// an escalation has to work with no conversation open.
//
// Two narrow crossings are not the operator's any more, and this is where that
// is enforced. Every override recorded in the week to 2026-09-06 was granted, most
// of them within minutes of the escalation that asked for it, under the
// operator's own standing direction: the operator step was latency rather than
// judgement. So the development manager may cross a cap on his own recorded
// authority, far enough for the one decision it refused, up to
// MaxDelegatedCapCrossings times per item, and only with a justification recorded
// on the item and reported to the operator as it happens. That is a veto by reading rather than a permission to ask for: the
// crossing is in force the moment it is recorded, and what keeps it answerable is
// that the operator sees it at once and can undo the work it bought.
//
// The bound is the whole of the delegation. A sixth crossing of one item is
// refused naming the Lead Product Manager as the one who decides past it, a
// crossing carrying no reason is refused outright, and neither clearing a budget
// nor raising one past the ceiling that refusal named is his at all.
//
// Past the development manager's five, the Lead Product Manager may grant up to
// MaxLeadCapGrants more for the item, each the same size as one of hers and each
// with its reason, and the harness records it as her decision without the operator
// typing anything (`docs/designs/stopped-run-recovery.md`, the ruling on raises
// past the development manager's five). After MaxRoleCapRaises in all no role
// raises an item's limit again. The operator's own override is not a role's and
// is not counted against either bound. CrossedBy is what tells the three apart on
// the record, so an item's overrides say who gave it each piece of room.
//
// It clears or raises, and never lowers. An override that would leave a budget no
// larger than it already stands is refused, so an item's overrides are a
// monotonic account of who gave it more room and why, and the last word on a
// budget is also the largest. Lowering a cap is a judgement about a project's
// pace rather than a decision about one item, and `triage.review_rounds_cap` is
// where that is made.
//
// It lives on the item's own counter record rather than beside it, because the
// guards read the counters under that record's lock and must not have to reach a
// second store — and because an override read from somewhere else could be read
// at a different moment from the counters it crosses.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// TriageCapCleared is the ceiling a cleared budget stands at. Nothing this
// harness counts comes near it, so every guard reading it permits, and the
// arithmetic the counters do against a cap cannot overflow past it: what is
// subtracted from a cap is a count of rounds an item has actually produced.
const TriageCapCleared = math.MaxInt

// The bounds one override is held to. The reason is the whole of what makes an
// override answerable for later, and a bounded line is enough to say it; the
// deciding party is a name rather than an argument.
const (
	MaxTriageOverrideReasonBytes = 4 << 10
	MaxTriageOverrideByBytes     = 256
)

// MaxTriageOverrides bounds how many overrides one item's record carries. It is
// generous rather than tight: each one is an operator sitting down to answer an
// escalation, so an item that has had this many has a problem no further budget
// is going to settle, and the refusal says so.
const MaxTriageOverrides = 16

// MaxDelegatedCapCrossings bounds how many of one item's overrides the
// development manager may record on his own authority. It is the operator's own
// figure rather than one derived here, and it is a bound on the delegation rather
// than on the item: past it a raise is the Lead Product Manager's to grant, up to
// MaxLeadCapGrants more, so that no role takes the item past MaxRoleCapRaises
// (ten) in all.
//
// Five is deliberately more than the escalations one item produced in the week
// that argued for this and deliberately fewer than the sixteen an item's record
// carries in total, so an item that reaches the bound is one where the
// development manager has been wrong about the same work five times and whether
// it is worth more is a judgement of its value against the other work waiting.
const MaxDelegatedCapCrossings = 5

// MaxLeadCapGrants bounds how many raises the Lead Product Manager may grant one
// item once the development manager's own MaxDelegatedCapCrossings are used, and
// MaxRoleCapRaises is the two together: the limit no role may take an item past.
// Both are the architect's ruling of 2026-10-22 rather than figures derived here.
const (
	MaxLeadCapGrants = 5
	MaxRoleCapRaises = MaxDelegatedCapCrossings + MaxLeadCapGrants
)

// ErrTriageOverrideNotARaise is what an override that would not give an item more
// room unwraps to, so a caller can tell "this changes nothing" from a record that
// could not be written without matching on the words of either.
var ErrTriageOverrideNotARaise = errors.New("a triage override clears or raises a cap and never lowers one")

// ErrTriageCrossingsSpent is what a crossing past the delegated bound unwraps to,
// and ErrTriageCrossingUnjustified what one carrying no reason does. They are
// separate sentinels because they are opposite failures with opposite remedies:
// the first is an item the development manager may no longer decide about, and
// the second is a decision he may make and has not argued for.
var (
	ErrTriageCrossingsSpent      = errors.New("the development manager's cap crossings for this item are spent")
	ErrTriageCrossingUnjustified = errors.New("a delegated cap crossing carries the reason it was crossed for")
)

// ErrTriageRaiseLimit is what a raise past MaxRoleCapRaises unwraps to, and
// ErrTriageGrantEarly what a Lead Product Manager's grant does while the
// development manager still has raises of her own to record.
var (
	ErrTriageRaiseLimit = errors.New("no role raises one item's limit past ten raises in all")
	ErrTriageGrantEarly = errors.New("the development manager still has raises of her own for this item")
)

// TriageOverride is one operator decision to cross one of a work item's caps.
//
// Every field is required, which is deliberate and is the point of the record: an
// override nobody is named for is exactly the thing this exists to be an
// alternative to. Who decided and when are what make it attributable, and the
// reason is what makes it answerable — an operator reading an item's budgets six
// weeks later has to be able to see that a cap was crossed, by whom, and on what
// argument.
type TriageOverride struct {
	// Budget is the budget this crosses, named in the vocabulary a refusal names:
	// what an operator reads is "the review round cap", not "the review round
	// counter".
	Budget string `json:"budget"`
	// Cap is the ceiling this puts in force, and Cleared lifts the budget
	// entirely. They are exclusive: a cleared budget has no number, and a record
	// carrying both would leave a reader to guess which one the guards obey.
	Cap     int  `json:"cap,omitempty"`
	Cleared bool `json:"cleared,omitempty"`
	// DecidedBy names the operator who decided this, in whatever form they gave.
	// The harness does not verify it and does not pretend to: what it records is
	// that somebody put their name to crossing a cap, which is what the next
	// reader needs in order to go and ask them.
	DecidedBy string    `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`
	Reason    string    `json:"reason"`
	// CrossedBy is the role that crossed this cap on its own delegated authority,
	// and is empty for the operator's own hand — which is what every override
	// recorded before the delegation existed was, and what every cleared budget and
	// every unbounded raise still is.
	//
	// It is the role rather than a vocabulary of its own, because who may hold a
	// delegation is already a closed set the harness names elsewhere and a second
	// spelling of it here would be a second thing to keep in step. The development
	// manager holds the first five raises of an item and the Lead Product Manager
	// the five after them; any other role on the record is a record nothing that
	// writes them could have written.
	CrossedBy domain.AgentRole `json:"crossed_by,omitempty"`
	// CostSoFar is what the item's runs had cost when the Lead Product Manager
	// granted this raise, carried on every grant and on nothing else. The
	// architect's ruling asks each grant to record it, because a grant is a
	// judgement of the item's value against what it has already spent, and the
	// figure that judgement was made against is what a reader checking it needs.
	CostSoFar *TriageGrantCost `json:"cost_so_far,omitempty"`
}

// TriageGrantCost is one item's provider-reported cost as a grant recorded it:
// the total over every run made for the item, how many runs that is, and how many
// of them could not be priced because their records are gone. While any could
// not, the total is the least the item cost rather than what it cost.
type TriageGrantCost struct {
	TotalUSD    float64 `json:"total_usd"`
	Runs        int     `json:"runs"`
	UnknownRuns int     `json:"unknown_runs,omitempty"`
}

// GrantCostOf is an item's price as a grant records it.
func GrantCostOf(price ItemPrice) TriageGrantCost {
	return TriageGrantCost{TotalUSD: price.TotalUSD, Runs: len(price.Runs), UnknownRuns: price.UnknownRuns}
}

// Text is the cost in the words a note and a message use: money, the runs it
// was spent over, and "at least" where some of them could not be priced.
func (c TriageGrantCost) Text() string {
	runs := fmt.Sprintf("%d runs", c.Runs)
	if c.Runs == 1 {
		runs = "1 run"
	}
	if c.UnknownRuns > 0 {
		return fmt.Sprintf("at least $%.2f over %s, %d of which could not be priced", c.TotalUSD, runs, c.UnknownRuns)
	}
	return fmt.Sprintf("$%.2f over %s", c.TotalUSD, runs)
}

func (c TriageGrantCost) validate() error {
	var problems []error
	if c.TotalUSD < 0 {
		problems = append(problems, fmt.Errorf("a grant's cost so far is $%.2f, and a cost is never negative", c.TotalUSD))
	}
	if c.Runs < 0 || c.UnknownRuns < 0 || c.UnknownRuns > c.Runs {
		problems = append(problems, fmt.Errorf("a grant's cost so far counts %d runs with %d unpriced, which no item's runs could come to", c.Runs, c.UnknownRuns))
	}
	return errors.Join(problems...)
}

// Delegated reports an override a role recorded on its own authority: the
// development manager's crossing or the Lead Product Manager's grant. The
// operator's own overrides are unbounded by either count and always were.
func (o TriageOverride) Delegated() bool { return o.CrossedBy != "" }

// Granted reports a raise the Lead Product Manager granted past the development
// manager's five.
func (o TriageOverride) Granted() bool { return o.CrossedBy == domain.RoleProductManager }

// TriageOverrideBudgets are the budgets an operator may cross, in the order a
// refusal lists them, so a command that refuses an unknown one names exactly what
// was available.
func TriageOverrideBudgets() []string {
	return []string{TriageReviewRoundBudget, TriageRepairGrantBudget, TriageRerunBudget, TriageMergeRearmBudget}
}

// Limit is the ceiling this override puts in force.
func (o TriageOverride) Limit() int {
	if o.Cleared {
		return TriageCapCleared
	}
	return o.Cap
}

// Validate reports every contract violation in the override at once.
func (o TriageOverride) Validate() error {
	var problems []error
	if err := validTriageBudget(o.Budget); err != nil {
		problems = append(problems, err)
	}
	if o.Cap < 0 {
		problems = append(problems, fmt.Errorf("an override to a cap of %d is not a cap", o.Cap))
	}
	if o.Cleared && o.Cap != 0 {
		problems = append(problems, fmt.Errorf("an override that clears the %s cap states no number, and this one states %d", o.Budget, o.Cap))
	}
	if strings.TrimSpace(o.DecidedBy) == "" {
		problems = append(problems, errors.New("an override names the operator who decided it; one nobody is named for is what this record exists to replace"))
	}
	if len(o.DecidedBy) > MaxTriageOverrideByBytes {
		problems = append(problems, fmt.Errorf("the deciding operator is %d bytes, limit is %d", len(o.DecidedBy), MaxTriageOverrideByBytes))
	}
	if o.DecidedAt.IsZero() {
		problems = append(problems, errors.New("an override records when it was decided"))
	}
	if strings.TrimSpace(o.Reason) == "" {
		problems = append(problems, errors.New("an override records why the cap was crossed, which is what an operator reading the item afterwards has to be able to see"))
	}
	if len(o.Reason) > MaxTriageOverrideReasonBytes {
		problems = append(problems, fmt.Errorf("the override reason is %d bytes, limit is %d", len(o.Reason), MaxTriageOverrideReasonBytes))
	}
	// The delegation is one role's and it is narrow. A record naming any other
	// role, or naming a role over a cleared budget, describes an override the only
	// thing that writes delegated ones could not have produced — and it is the
	// figures the guards obey, so it is refused rather than read.
	if o.Delegated() {
		if o.CrossedBy != domain.RoleDevelopmentManager && o.CrossedBy != domain.RoleProductManager {
			problems = append(problems, fmt.Errorf(
				"only the %s and the %s raise a cap on their own authority, and this override names the %s",
				domain.RoleDevelopmentManager.Title(), domain.RoleProductManager.Title(), o.CrossedBy))
		}
		if o.Cleared {
			problems = append(problems, errors.New("a delegated crossing raises a cap to a number and never clears one; clearing a budget is the operator's"))
		}
	}
	switch {
	case o.Granted() && o.CostSoFar == nil:
		problems = append(problems, fmt.Errorf("a raise the %s grants records what the item had cost so far, and this one carries no cost", domain.RoleProductManager.Title()))
	case o.Granted():
		if err := o.CostSoFar.validate(); err != nil {
			problems = append(problems, err)
		}
	case o.CostSoFar != nil:
		problems = append(problems, fmt.Errorf("only a raise the %s grants records the item's cost so far", domain.RoleProductManager.Title()))
	}
	return errors.Join(problems...)
}

// Describe says what one override did, for whoever is reading an item's budgets.
func (o TriageOverride) Describe() string {
	crossed := fmt.Sprintf("raised the %s cap to %d", o.Budget, o.Cap)
	if o.Cleared {
		crossed = fmt.Sprintf("cleared the %s cap", o.Budget)
	}
	// Who crossed it is said as what it was rather than only as a name, because
	// the two are answerable to different people: an operator's override is the
	// operator answering an escalation, and a delegated crossing is the
	// development manager acting on standing direction with the operator reading
	// it afterwards.
	decided := fmt.Sprintf("decided by %s", strings.TrimSpace(o.DecidedBy))
	switch {
	case o.Granted():
		decided = fmt.Sprintf("granted by the %s past the %s's own raises", o.CrossedBy.Title(), domain.RoleDevelopmentManager.Title())
	case o.Delegated():
		decided = fmt.Sprintf("crossed by the %s on delegated authority", o.CrossedBy.Title())
	}
	if o.Granted() && o.CostSoFar != nil {
		decided += fmt.Sprintf(" when the item had cost %s", o.CostSoFar.Text())
	}
	return fmt.Sprintf("%s, %s at %s: %s",
		crossed, decided, o.DecidedAt.UTC().Format(time.RFC3339), strings.TrimSpace(o.Reason))
}

// Overridden is these caps as one item's recorded overrides leave them. It is
// what every guard refuses against and what every view reports, so the cap that
// refuses a decision and the cap an operator is shown are one number.
//
// It takes the larger of the configured ceiling and the override, rather than the
// override, and that is the whole of what makes "never lowers" true rather than
// only true on the day the override was written. An override is a raise measured
// against what stood when it was recorded, and what stood is a configured cap that
// can move afterwards: raise `triage.review_rounds_cap` from 4 to 10 over an item
// carrying an override to 8, and an assignment would drop that one item to 8 while
// every other item went to 10 — an override lowering a cap, reported as a raise by
// every view, which is exactly the thing this record is not allowed to do.
//
// The overrides are folded in order and each takes the larger, so nothing has to
// sort them and a later, smaller one cannot undo an earlier one. That the store
// only ever records a raise is a separate guarantee and this does not lean on it.
func (c TriageCaps) Overridden(overrides []TriageOverride) TriageCaps {
	for _, override := range overrides {
		switch override.Budget {
		case TriageReviewRoundBudget:
			c.ReviewRounds = larger(c.ReviewRounds, override.Limit())
		case TriageRepairGrantBudget:
			c.RepairGrants = larger(c.RepairGrants, override.Limit())
		case TriageRerunBudget:
			c.Reruns = larger(c.Reruns, override.Limit())
		case TriageMergeRearmBudget:
			c.MergeRearms = larger(c.MergeRearms, override.Limit())
		}
	}
	return c
}

// larger is the ceiling in force where a configured one and an override disagree:
// an override gives an item room and never takes it away.
func larger(configured, override int) int {
	if override > configured {
		return override
	}
	return configured
}

// OverrideOf is the last override recorded against one budget, and whether there
// is one. It answers who crossed this budget and when, which is what an
// attribution needs; what ceiling is actually in force is Overridden's answer and
// can be the configured one, where that has since been raised above the override.
func (c TriageCounters) OverrideOf(budget string) (TriageOverride, bool) {
	for index := len(c.Overrides) - 1; index >= 0; index-- {
		if c.Overrides[index].Budget == budget {
			return c.Overrides[index], true
		}
	}
	return TriageOverride{}, false
}

// DelegatedCrossings is how many of this item's overrides the development
// manager recorded on her own authority, which is the figure her bound is refused
// against. It counts every budget together rather than each one separately,
// because what is bounded is how often she may decide this item deserves more
// room, not which room she asked for.
func (c TriageCounters) DelegatedCrossings() int {
	return countRaisesBy(c.Overrides, domain.RoleDevelopmentManager)
}

// LeadGrants is how many raises the Lead Product Manager has granted this item
// past the development manager's own, counted over every budget together for the
// same reason.
func (c TriageCounters) LeadGrants() int {
	return countRaisesBy(c.Overrides, domain.RoleProductManager)
}

// RoleRaises is every raise a role recorded on this item, which is what
// MaxRoleCapRaises is refused against.
func (c TriageCounters) RoleRaises() int {
	return c.DelegatedCrossings() + c.LeadGrants()
}

func countRaisesBy(overrides []TriageOverride, role domain.AgentRole) int {
	raises := 0
	for _, override := range overrides {
		if override.CrossedBy == role {
			raises++
		}
	}
	return raises
}

// TriageCrossing is what one delegated cap crossing came to: the budget it
// crossed, the ceiling now in force for it, and which of the item's permitted
// crossings this was against how many there are.
//
// The count travels with the crossing rather than being read back afterwards,
// because it is half of what the crossing has to say for itself: what the
// operator is told at the moment of a crossing is which one of the five it was,
// and a surface counting the record again is a second chance to say a different
// number than the guard used.
type TriageCrossing struct {
	Budget   string
	Cap      int
	Number   int
	Bound    int
	Reason   string
	Counters TriageCounters
	// CostSoFar is the item's cost the grant recorded, and nil on the development
	// manager's crossing, which records none.
	CostSoFar *TriageGrantCost
}

// TriageCrossingError refuses a crossing past the delegated bound, and names the
// Lead Product Manager as the one who decides a raise past it. It is the
// escalation the bound exists to force, said where the refusal happens rather
// than left for the caller to word: a refusal that did not say who decides next
// would read as a budget that had simply run out.
type TriageCrossingError struct {
	Budget     string
	WorkItemID string
	Crossings  int
	Bound      int
}

func (e TriageCrossingError) Error() string {
	return fmt.Sprintf(
		"%s already carries %d of %d cap crossing(s) the %s may record herself, so this one is refused and nothing was recorded: past the bound a raise of the %s cap is the %s's to grant, up to %d more for this item, so escalate to her with \"to\": %q, saying which cap and why",
		e.WorkItemID, e.Crossings, e.Bound, domain.RoleDevelopmentManager.Title(), e.Budget,
		domain.RoleProductManager.Title(), MaxLeadCapGrants, string(domain.RoleProductManager))
}

func (e TriageCrossingError) Unwrap() error { return ErrTriageCrossingsSpent }

// TriageRaiseLimitError refuses any role's raise of an item that already has
// MaxRoleCapRaises of them. It says the limit is one no role may pass and what
// becomes of the item instead, because an item that has failed through ten
// raises is more likely wrong than unlucky.
type TriageRaiseLimitError struct {
	Budget     string
	WorkItemID string
	Raises     int
}

func (e TriageRaiseLimitError) Error() string {
	return fmt.Sprintf(
		"%s already has %d raises of its limits recorded by the %s and the %s, and ten is the limit no role may pass, so this raise of the %s cap is refused and nothing was recorded: the item is not working as written, so the %s rewrites or splits it, or the %s closes it",
		e.WorkItemID, e.Raises, domain.RoleDevelopmentManager.Title(), domain.RoleProductManager.Title(), e.Budget,
		domain.RoleDevelopmentManager.Title(), domain.RoleProductManager.Title())
}

func (e TriageRaiseLimitError) Unwrap() error { return ErrTriageRaiseLimit }

// TriageGrantEarlyError refuses a Lead Product Manager's grant on an item whose
// development manager still has raises of her own: those are hers to decide,
// and a grant spent before them would take one of the item's ten for a raise
// that needed nobody past her.
type TriageGrantEarlyError struct {
	Budget     string
	WorkItemID string
	Crossings  int
}

func (e TriageGrantEarlyError) Error() string {
	return fmt.Sprintf(
		"%s carries %d of the %d raises the %s records herself, so a raise of its %s cap is still hers to record; nothing was recorded. Your grants begin once her %d are used",
		e.WorkItemID, e.Crossings, MaxDelegatedCapCrossings, domain.RoleDevelopmentManager.Title(), e.Budget, MaxDelegatedCapCrossings)
}

func (e TriageGrantEarlyError) Unwrap() error { return ErrTriageGrantEarly }

// TriageCrossingUnjustifiedError refuses a crossing that argued nothing. The
// justification is the whole of what makes the delegation answerable — it is what
// the operator reads in the channel and what the item carries afterwards — so a
// crossing without one is not a weaker crossing but no crossing at all.
type TriageCrossingUnjustifiedError struct {
	Budget     string
	WorkItemID string
}

func (e TriageCrossingUnjustifiedError) Error() string {
	return fmt.Sprintf(
		"crossing the %s cap for %s on delegated authority requires the reason it is being crossed for, and none was given; nothing was recorded and the cap stands where it did",
		e.Budget, e.WorkItemID)
}

func (e TriageCrossingUnjustifiedError) Unwrap() error { return ErrTriageCrossingUnjustified }

// CrossCap raises one of a work item's caps to one more than the item has spent
// against it, on the development manager's own delegated authority, and reports
// what the crossing came to.
//
// It is deliberately not Override with a different name on it. What the operator
// records is any ceiling they like, or none at all; what is delegated is one more
// of the thing that was refused, which is exactly what the refusal's own
// arithmetic already offers — so the new ceiling is computed here, under the
// item's lock, rather than taken from a caller who could name any number. An
// unbounded raise and a cleared budget stay the operator's, and nothing here can
// produce either.
//
// Everything that refuses does so before anything is written: the bound on how
// many crossings this item has had, the justification, and a budget already
// standing at a ceiling nothing counts up to. So a refused crossing costs the item
// nothing and leaves its record exactly as it found it.
//
// Like Override it spends nothing and carries nothing out. What it changes is what
// the guards permit next, and the decision the crossing was for is still a
// separate decision the development manager records afterwards.
func (s *TriageStore) CrossCap(ctx context.Context, workItemID string, by domain.AgentRole, budget, reason string, at time.Time, caps TriageCaps) (TriageCrossing, error) {
	if by != domain.RoleDevelopmentManager {
		return TriageCrossing{}, fmt.Errorf("only the %s crosses a cap on delegated authority, and this crossing names %q",
			domain.RoleDevelopmentManager.Title(), by)
	}
	return s.raiseCap(ctx, workItemID, by, budget, reason, nil, at, caps)
}

// GrantCap raises one of a work item's caps by one step on the Lead Product
// Manager's authority, once the development manager has used her own
// MaxDelegatedCapCrossings for the item, and reports what the grant came to.
//
// A grant is the same size as one of the development manager's crossings — one
// more than the item has spent against the budget — and it is refused before
// anything is written where her raises are not yet used, where the Lead Product
// Manager has granted MaxLeadCapGrants already, and where the item already
// carries MaxRoleCapRaises raises in all. Like a crossing it spends nothing and
// carries nothing out: the decision it makes recordable is still the development
// manager's to record afterwards.
//
// cost is what the item's runs have cost so far, which the grant records and the
// operator is told, so the judgement and the figure it was made against stay
// together.
func (s *TriageStore) GrantCap(ctx context.Context, workItemID, budget, reason string, cost TriageGrantCost, at time.Time, caps TriageCaps) (TriageCrossing, error) {
	if err := cost.validate(); err != nil {
		return TriageCrossing{}, err
	}
	return s.raiseCap(ctx, workItemID, domain.RoleProductManager, budget, reason, &cost, at, caps)
}

// raiseCap is the one step both roles' raises take, with each role's own bound.
func (s *TriageStore) raiseCap(ctx context.Context, workItemID string, by domain.AgentRole, budget, reason string, cost *TriageGrantCost, at time.Time, caps TriageCaps) (TriageCrossing, error) {
	if err := caps.Validate(); err != nil {
		return TriageCrossing{}, err
	}
	budget = strings.TrimSpace(budget)
	if err := validTriageBudget(budget); err != nil {
		return TriageCrossing{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return TriageCrossing{}, TriageCrossingUnjustifiedError{Budget: budget, WorkItemID: strings.TrimSpace(workItemID)}
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	bound := MaxDelegatedCapCrossings
	if by == domain.RoleProductManager {
		bound = MaxLeadCapGrants
	}
	crossing := TriageCrossing{Budget: budget, Reason: reason, Bound: bound, CostSoFar: cost}
	counters, err := s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		// Ten in all is checked first, because it is the one refusal that holds
		// whichever role asks and whatever either has left.
		if raised := counters.RoleRaises(); raised >= MaxRoleCapRaises {
			return TriageRaiseLimitError{Budget: budget, WorkItemID: counters.WorkItemID, Raises: raised}
		}
		crossed := counters.DelegatedCrossings()
		number := crossed + 1
		switch by {
		case domain.RoleDevelopmentManager:
			if crossed >= MaxDelegatedCapCrossings {
				return TriageCrossingError{
					Budget:     budget,
					WorkItemID: counters.WorkItemID,
					Crossings:  crossed,
					Bound:      MaxDelegatedCapCrossings,
				}
			}
		case domain.RoleProductManager:
			if crossed < MaxDelegatedCapCrossings {
				return TriageGrantEarlyError{Budget: budget, WorkItemID: counters.WorkItemID, Crossings: crossed}
			}
			// Her grants and the development manager's crossings together are bounded
			// above, so this is reached only by a record somebody edited by hand; it
			// is refused in the same words all the same.
			granted := counters.LeadGrants()
			if granted >= MaxLeadCapGrants {
				return TriageRaiseLimitError{Budget: budget, WorkItemID: counters.WorkItemID, Raises: counters.RoleRaises()}
			}
			number = granted + 1
		default:
			return fmt.Errorf("only the %s and the %s raise a cap on their own authority, and this raise names %q",
				domain.RoleDevelopmentManager.Title(), domain.RoleProductManager.Title(), by)
		}
		if len(counters.Overrides) >= MaxTriageOverrides {
			return fmt.Errorf(
				"%s already carries %d recorded cap override(s), which is the bound: an item that has been given more room this many times has something no further budget settles",
				counters.WorkItemID, len(counters.Overrides))
		}
		standing, err := triageBudgetCap(caps.Overridden(counters.Overrides), budget)
		if err != nil {
			return err
		}
		// A budget already cleared has no room left to give, and one past the cleared
		// ceiling is not a number. Both are the same refusal a raise to what already
		// stands gets, which is what the caller already handles.
		if standing >= TriageCapCleared {
			return TriageOverrideError{
				Budget:     budget,
				WorkItemID: counters.WorkItemID,
				Standing:   standing,
				Asked:      standing,
			}
		}
		// One more than the item has spent, which is exactly the ceiling the refusal
		// says would permit the decision — and never less, because that is the whole
		// of what the raise is for.
		//
		// It is measured from the spend rather than from the ceiling, and the two
		// differ on every item that is past its cap rather than level with it. Rounds
		// are counted whatever a cap says, so six rounds against a cap of four is an
		// ordinary state and a documented one; a raise that stepped from the ceiling
		// would move that cap to five, meet the same refusal, and cost another of the
		// item's raises and the operator another message for each step of the gap.
		// What bounds a raise is the count and the justification each one carries,
		// not how far behind the item the cap had fallen.
		crossing.Cap = larger(standing, triageBudgetSpend(*counters, budget)) + 1
		crossing.Number = number
		counters.Overrides = append(append([]TriageOverride{}, counters.Overrides...), TriageOverride{
			Budget: budget,
			Cap:    crossing.Cap,
			// The name is the role, because there is no person to name: what put its
			// hand up is the role's conversation, and CrossedBy beside it is what says
			// the authority was the role's rather than the operator's own.
			DecidedBy: by.Title(),
			DecidedAt: when.UTC(),
			Reason:    reason,
			CrossedBy: by,
			CostSoFar: cost,
		})
		return nil
	})
	if err != nil {
		return TriageCrossing{}, err
	}
	crossing.Counters = counters
	return crossing, nil
}

// Override records the operator's decision to cross one of a work item's caps,
// and reports what the item's record now says.
//
// caps are the configured ceilings, which is what the override is measured
// against together with whatever this item's earlier overrides already put in
// force. That comparison happens under the item's own lock, beside the counters,
// so two operators recording overrides of one item at the same instant cannot
// both raise a cap against the same standing figure.
//
// Nothing is spent here and nothing is carried out. What this changes is what the
// guards will permit next: the development manager can then record the decision
// their escalation was about, and the verb that carries it out finds the record
// it has always required. That separation is the same one every other decision
// here keeps — recording is a role's, causing work is the harness's hand
// afterwards — and it is what keeps an override from being a way to start a run.
func (s *TriageStore) Override(ctx context.Context, workItemID string, override TriageOverride, at time.Time, caps TriageCaps) (TriageCounters, error) {
	if err := caps.Validate(); err != nil {
		return TriageCounters{}, err
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	// This is the operator's verb and records the operator's decision. A delegated
	// crossing is CrossCap's and is bounded there, so one arriving here is a caller
	// reaching for the bounded path through the unbounded one.
	if override.Delegated() {
		return TriageCounters{}, fmt.Errorf(
			"an override recorded here is the operator's own, and this one names the %s: a delegated crossing is bounded and is recorded by crossing the cap rather than overriding it",
			override.CrossedBy)
	}
	override.Budget = strings.TrimSpace(override.Budget)
	override.DecidedBy = strings.TrimSpace(override.DecidedBy)
	override.Reason = strings.TrimSpace(override.Reason)
	// When it was decided is this update's own clock rather than a second one the
	// caller supplies, so the override and the record that carries it can never
	// disagree about when it happened.
	override.DecidedAt = when.UTC()
	if err := override.Validate(); err != nil {
		return TriageCounters{}, fmt.Errorf("invalid triage override: %w", err)
	}
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		if len(counters.Overrides) >= MaxTriageOverrides {
			return fmt.Errorf(
				"%s already carries %d recorded cap override(s), which is the bound: an item that has been given more room this many times has something no further budget settles",
				counters.WorkItemID, len(counters.Overrides))
		}
		standing, err := triageBudgetCap(caps.Overridden(counters.Overrides), override.Budget)
		if err != nil {
			return err
		}
		if override.Limit() <= standing {
			return TriageOverrideError{
				Budget:     override.Budget,
				WorkItemID: counters.WorkItemID,
				Standing:   standing,
				Asked:      override.Limit(),
			}
		}
		counters.Overrides = append(append([]TriageOverride{}, counters.Overrides...), override)
		return nil
	})
}

// TriageOverrideError refuses an override that would give an item no more room
// than it already has. It names what the budget stands at as well as what was
// asked for, because the two most ordinary causes read identically otherwise: an
// override already recorded that did the same thing, and a configured cap that is
// already larger than the number being typed.
type TriageOverrideError struct {
	Budget     string
	WorkItemID string
	Standing   int
	Asked      int
}

func (e TriageOverrideError) Error() string {
	if e.Standing == TriageCapCleared {
		return fmt.Sprintf("the %s cap for %s is already cleared, so this override would give it no more room; nothing was recorded",
			e.Budget, e.WorkItemID)
	}
	return fmt.Sprintf("the %s cap for %s already stands at %d, so an override to %d would give it no more room; nothing was recorded",
		e.Budget, e.WorkItemID, e.Standing, e.Asked)
}

func (e TriageOverrideError) Unwrap() error { return ErrTriageOverrideNotARaise }

// triageBudgetCap is one budget's ceiling under the caps given, and is what
// refuses a budget name nothing bounds. The names are the ones a refusal prints,
// so an operator answering an escalation types the words the refusal used.
func triageBudgetCap(caps TriageCaps, budget string) (int, error) {
	if err := validTriageBudget(budget); err != nil {
		return 0, err
	}
	switch budget {
	case TriageReviewRoundBudget:
		return caps.ReviewRounds, nil
	case TriageRepairGrantBudget:
		return caps.RepairGrants, nil
	case TriageRerunBudget:
		return caps.Reruns, nil
	default:
		return caps.MergeRearms, nil
	}
}

// validTriageBudget refuses a budget name nothing bounds, and says what the names
// are: an operator answering a refusal types the words the refusal used.
func validTriageBudget(budget string) error {
	switch budget {
	case TriageReviewRoundBudget, TriageRepairGrantBudget, TriageRerunBudget, TriageMergeRearmBudget:
		return nil
	default:
		return fmt.Errorf("%q is not a triage budget; the budgets are %s",
			budget, strings.Join(TriageOverrideBudgets(), ", "))
	}
}

// validateTriageOverrides reports every contract violation in one record's
// overrides at once.
//
// The monotonicity check is the one that is about the record rather than about
// any single override: they are only ever appended where they raise what stands,
// so a budget whose overrides do not strictly increase describes a record the only
// thing that writes them could not have written. It matters because these are the
// figures the guards obey — a hand-edited record that quietly lowered a cap would
// refuse decisions with nothing saying why.
func validateTriageOverrides(overrides []TriageOverride) []error {
	var problems []error
	if len(overrides) > MaxTriageOverrides {
		problems = append(problems, fmt.Errorf("%d cap overrides are recorded, which exceeds the bound of %d", len(overrides), MaxTriageOverrides))
	}
	// The delegated bound is checked over the whole record for the same reason the
	// monotonicity below is: it is what the guard enforces as a crossing is
	// recorded, so a record carrying more than the bound is one the only thing that
	// writes them could not have written, and reading it would hand an item room
	// nobody was permitted to give it.
	if crossings := countRaisesBy(overrides, domain.RoleDevelopmentManager); crossings > MaxDelegatedCapCrossings {
		problems = append(problems, fmt.Errorf(
			"%d cap crossings are recorded on delegated authority, which exceeds the bound of %d", crossings, MaxDelegatedCapCrossings))
	}
	if grants := countRaisesBy(overrides, domain.RoleProductManager); grants > MaxLeadCapGrants {
		problems = append(problems, fmt.Errorf(
			"%d raises are recorded as granted by the %s, which exceeds the bound of %d", grants, domain.RoleProductManager.Title(), MaxLeadCapGrants))
	}
	standing := map[string]int{}
	for index, override := range overrides {
		if err := override.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("overrides[%d]: %w", index, err))
			continue
		}
		if previous, recorded := standing[override.Budget]; recorded && override.Limit() <= previous {
			problems = append(problems, fmt.Errorf(
				"overrides[%d]: the %s cap already stood at %d, and an override recorded after it may only raise one",
				index, override.Budget, previous))
		}
		standing[override.Budget] = override.Limit()
	}
	return problems
}
