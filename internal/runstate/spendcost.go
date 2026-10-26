package runstate

// What a recorded spend used and what kind of cost it is, and how a later
// estimate is appended to the ledger beside the line it re-prices.
//
// The rules are the provider usage and cost design's
// (docs/designs/provider-usage-and-cost.md): every line written from it on
// carries the five counters the shared usage code settled and one cost figure
// of exactly one kind, computed by the one pricing service. A line written
// before carries neither, and is read with every counter absent and with the
// kind its classification already says -- reported where the provider priced
// it, unknown where it did not -- never as zero.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/pricing"
	"github.com/mason-bryant/yoyodyne/internal/usage"
)

// NewInvocationID is a fresh identifier for one recorded provider call, which
// is what a later estimate names the call by.
func NewInvocationID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("make an invocation id: %w", err)
	}
	return "call-" + hex.EncodeToString(raw[:]), nil
}

// Revision reports a line that is a later estimate of another rather than a
// provider call of its own.
func (s Spend) Revision() bool { return strings.TrimSpace(s.Revises) != "" }

// Figure is the line's cost, as the kind it is. A line written before figures
// were carried is reported where the provider priced it and unknown where it
// did not, with the line's own reason.
func (s Spend) Figure() pricing.Figure {
	if s.Cost != nil {
		return *s.Cost
	}
	if s.Known() {
		return pricing.Figure{Kind: pricing.KindReported, USD: s.AmountUSD}
	}
	return pricing.Figure{Kind: pricing.KindUnknown, Unknown: s.Unknown}
}

// UsageCounters is what the call used, with every counter absent on a line
// that recorded none.
func (s Spend) UsageCounters() usage.Counters {
	if s.Counters == nil {
		return usage.Counters{}
	}
	return *s.Counters
}

// PricedCall is the line as the pricing service reads it.
func (s Spend) PricedCall() pricing.Call {
	model := strings.TrimSpace(s.ResolvedModel)
	if model == "" {
		model = strings.TrimSpace(s.Model)
	}
	call := pricing.Call{
		Provider: s.Backend,
		Model:    model,
		Billing:  s.Billing,
		At:       s.At,
		Counters: s.UsageCounters(),
		Tier:     s.Tier,
	}
	if s.Known() {
		reported := s.AmountUSD
		call.ReportedUSD = &reported
	}
	return call
}

// costProblem reports a cost figure that disagrees with the rest of the line.
func (s Spend) costProblem() error {
	var problems []error
	if !s.Billing.Valid() {
		problems = append(problems, fmt.Errorf("billing_mode %q is not a billing mode", s.Billing))
	}
	if s.Revision() {
		if s.Cost == nil {
			problems = append(problems, errors.New("a later estimate carries the figure it estimates"))
		}
		if s.Known() {
			problems = append(problems, errors.New("a provider-reported cost is never recalculated"))
		}
		if strings.TrimSpace(s.InvocationID) != "" && s.InvocationID == s.Revises {
			problems = append(problems, errors.New("a later estimate is not the call it estimates"))
		}
	}
	if s.Cost == nil {
		return errors.Join(problems...)
	}
	figure := *s.Cost
	switch {
	case !figure.Kind.Valid():
		problems = append(problems, fmt.Errorf("cost kind %q is not one of the four", figure.Kind))
	case s.Known() != (figure.Kind == pricing.KindReported):
		problems = append(problems, errors.New("a cost is reported exactly when the provider priced the call"))
	case figure.Kind == pricing.KindUnknown && strings.TrimSpace(figure.Unknown) == "":
		problems = append(problems, errors.New("an unknown cost says why"))
	case figure.Kind == pricing.KindUnknown && figure.USD != 0:
		problems = append(problems, errors.New("an unknown cost carries no figure"))
	case (figure.Kind == pricing.KindEstimated || figure.Kind == pricing.KindComparison) && (figure.Entry == "" || figure.Schedule == ""):
		problems = append(problems, errors.New("a computed cost names the rate entry and the rate data version it was computed from"))
	case figure.USD < 0:
		problems = append(problems, errors.New("a cost is not below nothing"))
	}
	return errors.Join(problems...)
}

// foldRevisions puts every later estimate onto the line it estimates: the line
// is shown at its latest figure, and the figures before it stay readable in
// Earlier. A later estimate naming a call this log does not hold is kept as it
// stands rather than dropped, because dropping it would hide a figure somebody
// wrote.
func foldRevisions(lines []Spend) []Spend {
	index := make(map[string]int, len(lines))
	folded := make([]Spend, 0, len(lines))
	for _, line := range lines {
		if !line.Revision() {
			if id := strings.TrimSpace(line.InvocationID); id != "" {
				index[id] = len(folded)
			}
			folded = append(folded, line)
			continue
		}
		at, found := index[line.Revises]
		if !found {
			folded = append(folded, line)
			continue
		}
		original := &folded[at]
		original.Earlier = append(original.Earlier, original.Figure())
		figure := *line.Cost
		original.Cost = &figure
	}
	return folded
}

// CostKinds is what a set of recorded calls cost, by kind, never added
// together. It is the one summation every surface showing cost by kind reads,
// so two of them cannot add the same lines to different figures.
type CostKinds struct {
	Calls         int     `json:"calls"`
	ReportedUSD   float64 `json:"reported_usd"`
	ReportedCalls int     `json:"reported_calls"`
	EstimatedUSD  float64 `json:"estimated_usd"`
	EstimateCalls int     `json:"estimated_calls"`
	// ComparisonUSD is what calls paid for by a subscription or credits would
	// have cost at API prices. It is not money spent.
	ComparisonUSD   float64 `json:"comparison_usd"`
	ComparisonCalls int     `json:"comparison_calls"`
	// Credits is what calls paid for with purchased credits came to in credits,
	// where a credits rate entry priced them.
	Credits      float64 `json:"credits,omitempty"`
	CreditsCalls int     `json:"credits_calls,omitempty"`
	UnknownCalls int     `json:"unknown_calls"`
	// RateVersions are the rate data versions the estimates and comparisons
	// were computed from.
	RateVersions []string `json:"rate_versions,omitempty"`
	// UnknownReasons counts the unknown calls by why, so a surface can name the
	// commonest reason without enumerating every call.
	UnknownReasons map[string]int `json:"unknown_reasons,omitempty"`
}

// Add counts one recorded call. A later estimate that names no call this log
// holds is not a call and is not counted.
func (k *CostKinds) Add(line Spend) {
	if line.Revision() {
		return
	}
	figure := line.Figure()
	k.Calls++
	switch figure.Kind {
	case pricing.KindReported:
		k.ReportedCalls++
		k.ReportedUSD += figure.USD
	case pricing.KindEstimated:
		k.EstimateCalls++
		k.EstimatedUSD += figure.USD
		k.version(figure.Schedule)
	case pricing.KindComparison:
		k.ComparisonCalls++
		k.ComparisonUSD += figure.USD
		k.version(figure.Schedule)
	default:
		k.UnknownCalls++
		if k.UnknownReasons == nil {
			k.UnknownReasons = make(map[string]int)
		}
		reason := strings.TrimSpace(figure.Unknown)
		if reason == "" {
			reason = "no reason was recorded"
		}
		k.UnknownReasons[reason]++
	}
	if figure.Credits != nil {
		k.CreditsCalls++
		k.Credits += *figure.Credits
	}
}

// Merge adds another set's counts into this one.
func (k *CostKinds) Merge(other CostKinds) {
	k.Calls += other.Calls
	k.ReportedUSD += other.ReportedUSD
	k.ReportedCalls += other.ReportedCalls
	k.EstimatedUSD += other.EstimatedUSD
	k.EstimateCalls += other.EstimateCalls
	k.ComparisonUSD += other.ComparisonUSD
	k.ComparisonCalls += other.ComparisonCalls
	k.Credits += other.Credits
	k.CreditsCalls += other.CreditsCalls
	k.UnknownCalls += other.UnknownCalls
	for _, version := range other.RateVersions {
		k.version(version)
	}
	for reason, count := range other.UnknownReasons {
		if k.UnknownReasons == nil {
			k.UnknownReasons = make(map[string]int)
		}
		k.UnknownReasons[reason] += count
	}
}

func (k *CostKinds) version(version string) {
	if version == "" {
		return
	}
	for _, have := range k.RateVersions {
		if have == version {
			return
		}
	}
	k.RateVersions = append(k.RateVersions, version)
	sort.Strings(k.RateVersions)
}

// UnknownShare is the part of the calls whose cost is unknown, between 0 and 1.
func (k CostKinds) UnknownShare() float64 {
	if k.Calls == 0 {
		return 0
	}
	return float64(k.UnknownCalls) / float64(k.Calls)
}

// CommonestUnknown is the reason most unknown calls share, and empty where
// none is unknown.
func (k CostKinds) CommonestUnknown() string {
	best, count := "", 0
	for reason, seen := range k.UnknownReasons {
		if seen > count || (seen == count && reason < best) {
			best, count = reason, seen
		}
	}
	return best
}

// Text is the figures in words, each kind apart: the reported subtotal, the
// estimated subtotal with the rate data it came from, the comparison figure
// said not to be money spent, credits apart from dollars, and the unknown share
// as a count and a percentage.
func (k CostKinds) Text() string {
	if k.Calls == 0 {
		return "no calls recorded in the spend ledger"
	}
	parts := []string{fmt.Sprintf("$%.2f reported by the provider (%d %s)", k.ReportedUSD, k.ReportedCalls, calls(k.ReportedCalls))}
	estimated := fmt.Sprintf("$%.2f estimated (%d %s)", k.EstimatedUSD, k.EstimateCalls, calls(k.EstimateCalls))
	if len(k.RateVersions) > 0 {
		estimated += " at rate data " + strings.Join(k.RateVersions, ", ")
	}
	parts = append(parts, estimated)
	if k.ComparisonCalls > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f at API prices for %d subscription or credit %s, for comparison only and not money spent", k.ComparisonUSD, k.ComparisonCalls, calls(k.ComparisonCalls)))
	}
	if k.CreditsCalls > 0 {
		parts = append(parts, fmt.Sprintf("%.2f credits used on %d %s", k.Credits, k.CreditsCalls, calls(k.CreditsCalls)))
	}
	unknown := fmt.Sprintf("cost unknown for %d of %d %s (%.0f%%)", k.UnknownCalls, k.Calls, calls(k.Calls), 100*k.UnknownShare())
	if reason := k.CommonestUnknown(); reason != "" {
		unknown += "; most often because " + reason
	}
	parts = append(parts, unknown)
	return strings.Join(parts, "; ")
}

func calls(count int) string {
	if count == 1 {
		return "call"
	}
	return "calls"
}

// KindsOf sums the recorded lines the filter keeps. A nil filter keeps every
// line.
func KindsOf(lines []Spend, keep func(Spend) bool) CostKinds {
	var kinds CostKinds
	for _, line := range lines {
		if keep == nil || keep(line) {
			kinds.Add(line)
		}
	}
	return kinds
}

// LastRunningUsage is the last running usage total recorded for a session, and
// nil where none is. It is what the meter settles the session's next call
// against, and it is read from the log for the reason ReportedSessionTotal is:
// a session outlives the process that opened it.
func (s *SpendStore) LastRunningUsage(sessionID string) (*usage.Counters, error) {
	session := strings.TrimSpace(sessionID)
	if session == "" {
		return nil, nil
	}
	lines, err := s.List()
	if err != nil {
		return nil, err
	}
	var last *usage.Counters
	for _, line := range lines {
		if line.SessionID == session && !line.Revision() && line.RunningCounters != nil {
			running := *line.RunningCounters
			last = &running
		}
	}
	return last, nil
}

// Reprice appends a later estimate for every call whose latest figure the
// pricing service would now give differently, and reports how many it
// appended. It never rewrites a line: the figure a call was shown at before
// stays readable beside the new one.
//
// A provider-reported figure is never recalculated. Nor is a line written
// before figures were carried: it records neither its counters nor how its
// account was paid for, so no estimate could be made from it that was not a
// guess, and it stays as it was recorded.
func (s *SpendStore) Reprice(service pricing.Service, now func() time.Time) (int, error) {
	lines, err := s.List()
	if err != nil {
		return 0, err
	}
	appended := 0
	for _, line := range lines {
		if line.Revision() || line.Cost == nil || line.Known() || strings.TrimSpace(line.InvocationID) == "" {
			continue
		}
		current := line.Figure()
		repriced := service.Price(line.PricedCall())
		if sameFigure(current, repriced) {
			continue
		}
		id, err := NewInvocationID()
		if err != nil {
			return appended, err
		}
		revision := line
		revision.Earlier = nil
		revision.InvocationID = id
		revision.Revises = line.InvocationID
		revision.Replaces = figureName(current)
		revision.Cost = &repriced
		revision.At = now().UTC()
		if err := s.Append(revision); err != nil {
			return appended, fmt.Errorf("append a later estimate of %s: %w", line.InvocationID, err)
		}
		appended++
	}
	return appended, nil
}

func sameFigure(a, b pricing.Figure) bool {
	if a.Kind != b.Kind || a.USD != b.USD || a.Entry != b.Entry || a.Schedule != b.Schedule || a.Unknown != b.Unknown {
		return false
	}
	if (a.Credits == nil) != (b.Credits == nil) {
		return false
	}
	return a.Credits == nil || *a.Credits == *b.Credits
}

// figureName names a figure by what produced it, which is what a later
// estimate says it replaces.
func figureName(figure pricing.Figure) string {
	entry := figure.Entry
	if entry == "" {
		entry = string(figure.Kind)
	}
	if figure.Schedule == "" {
		return entry
	}
	return entry + " at rate data " + figure.Schedule
}

// StreamKind is the kind of record the call belongs to, in the terms a spend
// report selects by.
func (s Spend) StreamKind() StreamKind {
	switch {
	case s.RunID != "":
		return StreamRun
	case s.BranchReviewID != "":
		return StreamReview
	case s.SideStreamID != "":
		return StreamSide
	case s.ExchangeID != "":
		return StreamExchange
	}
	return StreamConversation
}

// Covers reports a recorded call the query selects: by kind, by the one thing
// it names -- the run, the work item, the conversation, the side thread, the
// exchange, or the branch review -- and by the local days it covers.
func (q SpendQuery) Covers(line Spend) bool {
	if !q.covers(line.StreamKind()) {
		return false
	}
	if match := strings.TrimSpace(q.Match); match != "" {
		named := false
		for _, id := range []string{line.RunID, line.WorkItemID, line.ConversationID, line.SideStreamID, line.ExchangeID, line.BranchReviewID} {
			if id == match {
				named = true
				break
			}
		}
		if !named {
			return false
		}
	}
	if q.Days > 0 && q.Match == "" {
		return LocalDay(line.At) >= oldestLocalDay(q.Now, q.Days)
	}
	return true
}
