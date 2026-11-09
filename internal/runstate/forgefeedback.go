package runstate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// What people say on a published pull request.
//
// A change published for a person to merge sits open on the forge, where
// anybody who can read it can review it or comment on it. Each check of the
// forge reads what was said after the latest head the harness pushed, leaving
// out what a factory of this product wrote (PullRequest.Wrote, and the mark
// factory.Identity.Mark describes), and sorts it by kind:
//
//   - A review that requests changes, or a comment on a line of the change, is
//     somebody asking for the change to be different. It starts a repair round
//     on the same run, and the feedback is that round's repair input
//     (State.ForgeFeedback), handed to the developer as evidence with its author
//     and time.
//   - An ordinary comment on the request is as often a question as a request,
//     so it is put on the development manager's list for her to decide.
//   - An approval is recorded on the publication as information
//     (PullRequestFeedback.Approvals). It never stands in for the independent
//     review, and nothing that gates a promotion reads it.
//
// The design is docs/designs/working-alongside-other-contributors.md, section 2.

// ForgeFeedbackKind is the kind of one piece of feedback left on a pull
// request, as the harness sorts it.
type ForgeFeedbackKind string

const (
	// FeedbackChangesRequested is a review whose verdict asks for changes.
	FeedbackChangesRequested ForgeFeedbackKind = "changes_requested"
	// FeedbackLineComment is a comment left on a line of the change.
	FeedbackLineComment ForgeFeedbackKind = "line_comment"
	// FeedbackComment is a comment on the request as a whole, or a review that
	// only commented.
	FeedbackComment ForgeFeedbackKind = "comment"
	// FeedbackApproval is a review that approved the change.
	FeedbackApproval ForgeFeedbackKind = "approval"
)

// StartsRepair reports feedback that asks for the change to be different, which
// is what starts a repair round on its own.
func (k ForgeFeedbackKind) StartsRepair() bool {
	return k == FeedbackChangesRequested || k == FeedbackLineComment
}

// Says is the kind in ordinary words.
func (k ForgeFeedbackKind) Says() string {
	switch k {
	case FeedbackChangesRequested:
		return "requested changes"
	case FeedbackLineComment:
		return "commented on a line of the change"
	case FeedbackComment:
		return "commented on the pull request"
	case FeedbackApproval:
		return "approved the pull request"
	}
	return string(k)
}

func (k ForgeFeedbackKind) valid() bool {
	switch k {
	case FeedbackChangesRequested, FeedbackLineComment, FeedbackComment, FeedbackApproval:
		return true
	}
	return false
}

const (
	// MaxForgeFeedbackItems bounds the feedback one record carries. A round
	// answering more than this was asked for a different change, and the rest is
	// on the forge.
	MaxForgeFeedbackItems = 20
	// MaxForgeFeedbackBodyBytes bounds what one piece of feedback says.
	MaxForgeFeedbackBodyBytes = 4096
	// maxForgeFeedbackFieldBytes bounds the forge's own short strings: who wrote
	// it, the file it names, and its link.
	maxForgeFeedbackFieldBytes = 400
	// maxFeedbackCursorIDs bounds the identifiers kept for the one moment the
	// cursor stands at.
	maxFeedbackCursorIDs = 50
	// MaxForgeWrites bounds the identifiers a request's record keeps of what the
	// harness wrote on it; the oldest goes first. The harness writes a handful of
	// comments on a request in its life, so the bound is never reached in use.
	MaxForgeWrites = 50
)

// feedbackIDPattern is the shape of a piece of feedback's identifier: what
// the forge recorded it as, and the forge's number for it.
var feedbackIDPattern = regexp.MustCompile(`^(review|line-comment|comment)-[0-9]{1,20}$`)

// FeedbackID is the identifier of a review, a comment on a line, or a comment
// on the request, from the forge's number for it.
func FeedbackID(what string, number int64) string {
	return fmt.Sprintf("%s-%d", what, number)
}

// ForgeWrites is the forge's identifier for each comment and review the
// harness wrote on one pull request (PullRequest.Wrote).
type ForgeWrites struct {
	IDs []string `json:"ids"`
}

// RecordWrite keeps the forge's identifier for a comment or review the harness
// wrote on the request, once, newest last, within MaxForgeWrites. An identifier
// that is not one is not kept.
func (p *PullRequest) RecordWrite(id string) {
	if p == nil || !feedbackIDPattern.MatchString(id) || p.WroteIt(id) {
		return
	}
	// A new list each time, so a copy of the record taken before this write
	// keeps the list it had.
	ids := []string{id}
	if p.Wrote != nil {
		ids = append(append([]string(nil), p.Wrote.IDs...), id)
	}
	if len(ids) > MaxForgeWrites {
		ids = ids[len(ids)-MaxForgeWrites:]
	}
	p.Wrote = &ForgeWrites{IDs: ids}
}

// WroteIt reports a comment or review the harness recorded writing on the
// request.
func (p *PullRequest) WroteIt(id string) bool {
	if p == nil || p.Wrote == nil {
		return false
	}
	for _, wrote := range p.Wrote.IDs {
		if wrote == id {
			return true
		}
	}
	return false
}

func (p PullRequest) validateWrote() error {
	if p.Wrote == nil {
		return nil
	}
	var problems []error
	if len(p.Wrote.IDs) > MaxForgeWrites {
		problems = append(problems, fmt.Errorf("at most %d are kept", MaxForgeWrites))
	}
	for _, id := range p.Wrote.IDs {
		if !feedbackIDPattern.MatchString(id) {
			problems = append(problems, fmt.Errorf("%q is not a forge review or comment identifier", id))
		}
	}
	return errors.Join(problems...)
}

// ForgeFeedbackItem is one piece of feedback as the forge recorded it: who
// wrote it, when, what it said, and where on the change, where it named a line.
type ForgeFeedbackItem struct {
	// ID is the forge's identifier, prefixed with what kind of thing it is, so a
	// review and a comment with the same number are told apart
	// (feedbackIDPattern).
	ID     string            `json:"id"`
	Kind   ForgeFeedbackKind `json:"kind"`
	Author string            `json:"author"`
	At     time.Time         `json:"at"`
	Body   string            `json:"body,omitempty"`
	Path   string            `json:"path,omitempty"`
	Line   int               `json:"line,omitempty"`
	URL    string            `json:"url,omitempty"`
}

// Validate checks one piece of feedback names its kind, author and time.
func (f ForgeFeedbackItem) Validate() error {
	var problems []error
	if !feedbackIDPattern.MatchString(f.ID) {
		problems = append(problems, fmt.Errorf("id %q is not a forge review or comment identifier", f.ID))
	}
	if !f.Kind.valid() {
		problems = append(problems, fmt.Errorf("kind %q is not a kind of feedback", f.Kind))
	}
	if strings.TrimSpace(f.Author) == "" {
		problems = append(problems, errors.New("author is required"))
	}
	if f.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if f.Line < 0 {
		problems = append(problems, errors.New("line cannot be negative"))
	}
	return errors.Join(problems...)
}

// ForgeFeedback is the repair input of a round a person's feedback on the
// published pull request started: the request, the head the feedback was left
// on, and what was asked. It stays on the run until the repaired change is
// approved and published, so a round interrupted part-way hands the developer
// the same feedback when it is picked up.
//
// DispatchPending is set as the check of the forge makes the run live, and
// cleared by the process that takes the round up, so the check hosts only the
// rounds it started and nothing takes one up twice.
type ForgeFeedback struct {
	PullRequest     int                 `json:"pull_request"`
	HeadCommit      string              `json:"head_commit"`
	FoundAt         time.Time           `json:"found_at"`
	Items           []ForgeFeedbackItem `json:"items"`
	DispatchPending bool                `json:"dispatch_pending,omitempty"`
}

// Validate checks the round names its request and carries feedback that asks
// for a repair.
func (f ForgeFeedback) Validate() error {
	var problems []error
	if f.PullRequest <= 0 {
		problems = append(problems, errors.New("pull_request must be positive"))
	}
	if f.HeadCommit != "" && !commitPattern.MatchString(f.HeadCommit) {
		problems = append(problems, errors.New("head_commit is invalid"))
	}
	if f.FoundAt.IsZero() {
		problems = append(problems, errors.New("found_at is required"))
	}
	if len(f.Items) == 0 {
		problems = append(problems, errors.New("at least one piece of feedback is required"))
	}
	if len(f.Items) > MaxForgeFeedbackItems {
		problems = append(problems, fmt.Errorf("at most %d pieces of feedback are kept", MaxForgeFeedbackItems))
	}
	asks := false
	for index, item := range f.Items {
		if err := item.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("items[%d]: %w", index, err))
		}
		asks = asks || item.Kind.StartsRepair()
	}
	if len(f.Items) > 0 && !asks {
		problems = append(problems, errors.New("a repair round needs feedback that requests changes or comments on a line"))
	}
	return errors.Join(problems...)
}

// PullRequestFeedback is what the checks of the forge have read of the
// feedback on one published pull request: how far they have read, and the
// approvals people gave, kept as information.
//
// Through is the time of the latest feedback acted on, and ThroughIDs the
// feedback at exactly that time, so a check never acts on one piece twice and
// never misses one left in the same second as one it read. Feedback left before
// the latest head the harness pushed (PullRequest.PushedAt) is about a change
// that is no longer there, and is never acted on either.
type PullRequestFeedback struct {
	ReadAt     time.Time           `json:"read_at"`
	ReadError  string              `json:"read_error,omitempty"`
	Through    *time.Time          `json:"through,omitempty"`
	ThroughIDs []string            `json:"through_ids,omitempty"`
	Approvals  []ForgeFeedbackItem `json:"approvals,omitempty"`
}

// Handled reports feedback a check has already acted on.
func (f *PullRequestFeedback) Handled(item ForgeFeedbackItem) bool {
	if f == nil || f.Through == nil {
		return false
	}
	if item.At.Before(*f.Through) {
		return true
	}
	if item.At.Equal(*f.Through) {
		for _, id := range f.ThroughIDs {
			if id == item.ID {
				return true
			}
		}
	}
	return false
}

// Advance moves the cursor past the feedback a check has just acted on.
func (f *PullRequestFeedback) Advance(items []ForgeFeedbackItem) {
	for _, item := range items {
		switch {
		case f.Through == nil || item.At.After(*f.Through):
			at := item.At
			f.Through = &at
			f.ThroughIDs = []string{item.ID}
		case item.At.Equal(*f.Through) && len(f.ThroughIDs) < maxFeedbackCursorIDs:
			f.ThroughIDs = append(f.ThroughIDs, item.ID)
		}
	}
}

// RecordApproval keeps an approval as information, once, newest last, within
// the bound.
func (f *PullRequestFeedback) RecordApproval(item ForgeFeedbackItem) {
	for _, kept := range f.Approvals {
		if kept.ID == item.ID {
			return
		}
	}
	f.Approvals = append(f.Approvals, item)
	if len(f.Approvals) > MaxForgeFeedbackItems {
		f.Approvals = f.Approvals[len(f.Approvals)-MaxForgeFeedbackItems:]
	}
}

// Validate checks the record's approvals are approvals.
func (f PullRequestFeedback) Validate() error {
	var problems []error
	if len(f.Approvals) > MaxForgeFeedbackItems {
		problems = append(problems, fmt.Errorf("at most %d approvals are kept", MaxForgeFeedbackItems))
	}
	for index, approval := range f.Approvals {
		if err := approval.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("approvals[%d]: %w", index, err))
		}
		if approval.Kind != FeedbackApproval {
			problems = append(problems, fmt.Errorf("approvals[%d] is %s, not an approval", index, approval.Kind))
		}
	}
	if len(f.ThroughIDs) > maxFeedbackCursorIDs {
		problems = append(problems, fmt.Errorf("at most %d through_ids are kept", maxFeedbackCursorIDs))
	}
	for _, id := range f.ThroughIDs {
		if !feedbackIDPattern.MatchString(id) {
			problems = append(problems, fmt.Errorf("through_ids holds %q, which is not a forge review or comment identifier", id))
		}
	}
	return errors.Join(problems...)
}

// WaitingOnAPersonsMerge reports a run whose approved change is published and
// left for a person to merge: it succeeded without promoting anything because
// the merge setting leaves the merge to a person, its pull request is open and
// unmerged, and its branch and checkout are still there to repair the change
// in. These are the runs whose feedback a check of the forge reads.
func (s State) WaitingOnAPersonsMerge() bool {
	if s.Status != StatusSucceeded || s.Integration != nil || s.Retirement != nil || s.StopClass != StopIntegrationPolicy {
		return false
	}
	published := s.PullRequest
	if published == nil || published.Number <= 0 || published.Merged || published.Superseded != "" || published.HandedBack != nil {
		return false
	}
	if published.State != "" && !strings.EqualFold(published.State, "open") {
		return false
	}
	return s.WorktreePath != "" && s.Branch != "" && !s.WorktreeRemoved && !s.BranchRemoved
}

// AnsweringForgeFeedback reports a run a check of the forge put back at its
// developer to answer feedback on its pull request, and that nothing has taken
// up yet.
func (s State) AnsweringForgeFeedback() bool {
	return s.Status == StatusRunning && s.Phase == PhaseDeveloping && s.ForgeFeedback != nil && s.ForgeFeedback.DispatchPending
}

// forgeFeedbackTexts names the free text the feedback records carry, each
// bounded where the records are validated.
func (s *State) forgeFeedbackTexts(unstated func(key, path string, text *string, limit int)) {
	item := func(key, path string, feedback *ForgeFeedbackItem) {
		unstated(key+".author", path+".author", &feedback.Author, maxForgeFeedbackFieldBytes)
		unstated(key+".body", path+".body", &feedback.Body, MaxForgeFeedbackBodyBytes)
		unstated(key+".path", path+".path", &feedback.Path, maxForgeFeedbackFieldBytes)
		unstated(key+".url", path+".url", &feedback.URL, maxForgeFeedbackFieldBytes)
	}
	if s.ForgeFeedback != nil {
		for index := range s.ForgeFeedback.Items {
			item("forge_feedback.items[]", fmt.Sprintf("forge_feedback.items[%d]", index), &s.ForgeFeedback.Items[index])
		}
	}
	if s.PullRequest != nil && s.PullRequest.Feedback != nil {
		unstated("pull_request.feedback.read_error", "pull_request.feedback.read_error", &s.PullRequest.Feedback.ReadError, MaxRecordedTextBytes)
		for index := range s.PullRequest.Feedback.Approvals {
			item("pull_request.feedback.approvals[]", fmt.Sprintf("pull_request.feedback.approvals[%d]", index), &s.PullRequest.Feedback.Approvals[index])
		}
	}
}

// withoutEarlierRound clears what the round that published the head
// established about it: its checks and its approval were for the head the
// feedback is about, and the repaired change earns both again before anything
// is published. Repair inputs left from that round are answered already.
func (s State) withoutEarlierRound() State {
	cleared := s
	cleared.ChecksPassed = nil
	cleared.KeptChecks = nil
	cleared.CheckFailure = nil
	cleared.PathRefusal = nil
	cleared.ReplayConflict = nil
	cleared.ReviewSessionID = ""
	cleared.ReviewModel = ""
	cleared.ReviewResolvedModel = ""
	cleared.ReviewEffort = ""
	cleared.ReviewResolvedEffort = ""
	cleared.ReviewEffortDescription = ""
	cleared.ReviewEffortReported = false
	cleared.ReviewLoaded = nil
	cleared.ReviewBaseCommit = ""
	cleared.ReviewHeadCommit = ""
	cleared.ReviewDecision = ""
	cleared.ReviewApproves = ""
	cleared.ReviewSummary = ""
	cleared.ReviewFindings = 0
	cleared.ReviewFindingDetails = nil
	return cleared
}

// readFeedback moves the publication's cursor past the feedback a check acted
// on, and keeps the approvals among it as information.
func (s *State) readFeedback(read []ForgeFeedbackItem, at time.Time) {
	published := *s.PullRequest
	feedback := PullRequestFeedback{}
	if published.Feedback != nil {
		feedback = *published.Feedback
		feedback.Approvals = append([]ForgeFeedbackItem(nil), feedback.Approvals...)
		feedback.ThroughIDs = append([]string(nil), feedback.ThroughIDs...)
	}
	feedback.ReadAt = at
	feedback.ReadError = ""
	for _, item := range read {
		if item.Kind == FeedbackApproval {
			feedback.RecordApproval(item)
		}
	}
	feedback.Advance(read)
	published.Feedback = &feedback
	s.PullRequest = &published
}

// waitingFor refuses an update about a request the run is not waiting on a
// person to merge.
func (s State) waitingFor(number int, what string) error {
	if !s.WaitingOnAPersonsMerge() || s.PullRequest.Number != number {
		return fmt.Errorf("run %s is not waiting for a person to merge pull request %d, so %s is not recorded on it", s.RunID, number, what)
	}
	return nil
}

// FeedbackRead records feedback a check acted on without starting a repair
// round — ordinary comments put on the development manager's list, and
// approvals kept as information — by moving the publication's cursor past it.
type FeedbackRead struct {
	Number int
	Read   []ForgeFeedbackItem
	At     time.Time
}

func (u FeedbackRead) apply(state *State) (bool, error) {
	if err := state.waitingFor(u.Number, "what was read of it"); err != nil {
		return false, err
	}
	for _, item := range u.Read {
		if item.Kind.StartsRepair() {
			return false, fmt.Errorf("feedback %s asks for a repair, which a read alone does not answer", item.ID)
		}
	}
	state.readFeedback(u.Read, u.At)
	state.UpdatedAt = u.At
	return true, nil
}

// FeedbackRepairRefused stops a run waiting for a person to merge its pull
// request because feedback asked for a repair and its repair budget is spent:
// the ordinary stop on a spent budget, with the feedback as the repair input a
// decided repair hands back, and the cursor moved past what was read.
type FeedbackRepairRefused struct {
	Feedback ForgeFeedback
	Read     []ForgeFeedbackItem
	Limit    int
	Blocker  string
	At       time.Time
}

func (u FeedbackRepairRefused) apply(state *State) (bool, error) {
	if err := state.waitingFor(u.Feedback.PullRequest, "a repair its budget cannot answer"); err != nil {
		return false, err
	}
	if state.RepairAttempts < u.Limit {
		return false, fmt.Errorf("run %s has %d of %d repair attempt(s) spent, so the feedback starts a round rather than stopping it", state.RunID, state.RepairAttempts, u.Limit)
	}
	stopped := state.withoutEarlierRound()
	feedback := u.Feedback
	feedback.DispatchPending = false
	stopped.ForgeFeedback = &feedback
	stopped.readFeedback(u.Read, u.At)
	stopped.Status = StatusFailed
	stopped.Phase = PhaseDeveloping
	stopped.StopClass = StopRepairBudget
	stopped.Failure = fmt.Sprintf("changes were asked for on pull request %d after %d of %d permitted repair attempt(s) were spent", feedback.PullRequest, state.RepairAttempts, u.Limit)
	stopped.Blocker = RecordBlocker(u.Blocker)
	at := u.At
	stopped.CompletedAt = &at
	stopped.UpdatedAt = u.At
	*state = stopped
	return true, nil
}

// PutBackForFeedback is a run waiting for a person to merge its pull request
// made live again at its developer, to answer feedback that asked for a
// repair: the feedback is the round's repair input, one repair attempt is
// charged as every round is charged before its developer is invoked, and the
// cursor is moved past what was read. The result is for Store.ReserveLive,
// which takes the run's developer slot in the same write.
func (s State) PutBackForFeedback(feedback ForgeFeedback, read []ForgeFeedbackItem, limit int, at time.Time) (State, error) {
	if err := s.waitingFor(feedback.PullRequest, "a repair round"); err != nil {
		return State{}, err
	}
	if s.RepairAttempts >= limit {
		return State{}, fmt.Errorf("run %s has spent %d of %d repair attempt(s), so the feedback cannot start a round", s.RunID, s.RepairAttempts, limit)
	}
	live := s.withoutEarlierRound()
	feedback.DispatchPending = true
	live.ForgeFeedback = &feedback
	live.readFeedback(read, at)
	live.RepairAttempts++
	live.Status = StatusRunning
	live.Phase = PhaseDeveloping
	live.StopClass = ""
	live.Failure = ""
	live.Blocker = ""
	live.CompletedAt = nil
	live.SettledQuietSince = nil
	live.UpdatedAt = at
	return live, nil
}
