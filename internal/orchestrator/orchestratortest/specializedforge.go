package orchestratortest

import (
	"context"
	"errors"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RearmForge is the forge a re-arm speaks to: what it says about the request now,
// what it says is unmet, and every merge it was asked for. The requests are kept
// because the whole of what this action promises is which request it makes.
type RearmForge struct {
	Observed   publish.PullRequest
	ObserveErr error
	Status     string
	StatusErr  error
	Result     publish.MergeResult
	MergeErr   error
	Requested  []publish.MergeRequest
}

func (f *RearmForge) State(context.Context, string) (publish.PullRequest, error) {
	return f.Observed, f.ObserveErr
}

func (f *RearmForge) MergeState(context.Context, int) (string, error) {
	return f.Status, f.StatusErr
}

func (f *RearmForge) Merge(_ context.Context, request publish.MergeRequest) (publish.MergeResult, error) {
	f.Requested = append(f.Requested, request)
	return f.Result, f.MergeErr
}

// AnsweringForge is the forge reduced to the one question this sweep asks. It
// is separate from orchestratortest.Forge because a refresh needs answers no run
// of the harness produces — a request somebody closed unmerged, a branch some
// other request answers for — and because counting the questions is how a test
// proves a settled record is never asked about twice.
type AnsweringForge struct {
	Answer publish.PullRequest
	Err    error
	Asked  int
	Heads  []string
}

func (f *AnsweringForge) State(_ context.Context, head string) (publish.PullRequest, error) {
	f.Asked++
	f.Heads = append(f.Heads, head)
	if f.Err != nil {
		return publish.PullRequest{}, f.Err
	}
	return f.Answer, nil
}

// Merge is never reached from a refresh or a finish: only the recovery of a
// promotion that recorded no request arms a merge, and none of the records these
// tests write is one.
func (f *AnsweringForge) Merge(context.Context, publish.MergeRequest) (publish.MergeResult, error) {
	return publish.MergeResult{}, errors.New("answeringForge merges nothing: a refresh only asks")
}

// Close is the write the refresh never makes. A refresh that reached it would
// be closing a request on the strength of an answer, which is the orphan
// sweep's decision and not this one's.
func (f *AnsweringForge) Close(context.Context, publish.CloseRequest) (publish.Closure, error) {
	return publish.Closure{}, errors.New("a refresh closes nothing")
}

// NoticingForge is the harness's forge reading as a test drives it: a fixed set
// of requests held open for nothing, and a record of what it was told had
// already been reported.
type NoticingForge struct {
	Notices  []runstate.ForgeNotice
	Reported []map[int]bool
	Err      error
}

func (f *NoticingForge) Notice(_ context.Context, Reported map[int]bool) ([]runstate.ForgeNotice, error) {
	f.Reported = append(f.Reported, Reported)
	var fresh []runstate.ForgeNotice
	for _, notice := range f.Notices {
		if !Reported[notice.Number] {
			fresh = append(fresh, notice)
		}
	}
	return fresh, f.Err
}

// JobLogs is the forge's log of a job, as the harness reads its tail.
type JobLogs struct {
	Err   error
	Tail  string
	Asked []int64
}

func (l *JobLogs) JobLogTail(_ context.Context, checkRun int64, _ int) (string, error) {
	l.Asked = append(l.Asked, checkRun)
	return l.Tail, l.Err
}

// TargetChecks is the forge's reading of the target branch's own head, or its
// refusal to give one.
type TargetChecks struct {
	Reading publish.BranchCheckReading
	Refuse  error
	Asked   []string
}

func (c *TargetChecks) BranchChecks(_ context.Context, branch string) (publish.BranchCheckReading, error) {
	c.Asked = append(c.Asked, branch)
	return c.Reading, c.Refuse
}

// RequestChecks is the forge's reading of a request's checks, which gates arming a
// request nothing ever asked the forge to merge.
type RequestChecks struct {
	Reading publish.CheckReading
	Err     error
	Asked   int
}

func (c *RequestChecks) Checks(context.Context, int, string) (publish.CheckReading, error) {
	c.Asked++
	return c.Reading, c.Err
}
