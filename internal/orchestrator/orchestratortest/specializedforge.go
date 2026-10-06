package orchestratortest

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// RearmForge scripts the request's state and records requested merges.
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

// AnsweringForge scripts publication readings and records every question.
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

// Merge refuses writes during a publication refresh.
func (f *AnsweringForge) Merge(context.Context, publish.MergeRequest) (publish.MergeResult, error) {
	return publish.MergeResult{}, errors.New("answeringForge merges nothing: a refresh only asks")
}

// Close refuses writes during a publication refresh.
func (f *AnsweringForge) Close(context.Context, publish.CloseRequest) (publish.Closure, error) {
	return publish.Closure{}, errors.New("a refresh closes nothing")
}

// NoticingForge filters scripted notices against those already reported.
type NoticingForge struct {
	Notices  []runstate.ForgeNotice
	Reported []map[int]bool
	Err      error
}

func (f *NoticingForge) Notice(_ context.Context, reported map[int]bool) ([]runstate.ForgeNotice, error) {
	f.Reported = append(f.Reported, reported)
	var fresh []runstate.ForgeNotice
	for _, notice := range f.Notices {
		if !reported[notice.Number] {
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

// TargetChecks scripts and records readings of the target branch's checks.
type TargetChecks struct {
	Reading publish.BranchCheckReading
	Refuse  error
	Asked   []string
}

func (c *TargetChecks) BranchChecks(_ context.Context, branch string) (publish.BranchCheckReading, error) {
	c.Asked = append(c.Asked, branch)
	return c.Reading, c.Refuse
}

// RequestChecks scripts and counts readings of a pull request's checks.
type RequestChecks struct {
	Reading publish.CheckReading
	Err     error
	Asked   int
}

func (c *RequestChecks) Checks(context.Context, int, string) (publish.CheckReading, error) {
	c.Asked++
	return c.Reading, c.Err
}

// BatchingForge scripts batch readings and records batch and single requests.
type BatchingForge struct {
	AnsweringForge
	Numbers map[string]int
	Err     error
	Mutex   sync.Mutex
	Batches [][]string
	Single  int
}

func (f *BatchingForge) State(context.Context, string) (publish.PullRequest, error) {
	f.Mutex.Lock()
	defer f.Mutex.Unlock()
	f.Single++
	return publish.PullRequest{}, fmt.Errorf("batchingForge answers only in batches")
}

func (f *BatchingForge) States(_ context.Context, heads []string) (map[string]publish.PullRequest, error) {
	f.Mutex.Lock()
	defer f.Mutex.Unlock()
	f.Batches = append(f.Batches, append([]string(nil), heads...))
	if f.Err != nil {
		return nil, f.Err
	}
	answered := map[string]publish.PullRequest{}
	for _, head := range heads {
		if number, ok := f.Numbers[head]; ok {
			answered[head] = publish.PullRequest{Number: number, URL: fmt.Sprintf("https://example.invalid/pull/%d", number), State: "OPEN"}
		}
	}
	return answered, nil
}

// CheckedForge is a Forge with check state: what it reports about the head's
// checks, the check runs it was asked to run again, and every queued merge it
// was asked to withdraw. Withdrawing one is the forge no longer holding it.
type CheckedForge struct {
	*Forge
	Reading   publish.CheckReading
	ReadError error
	Withdrawn []int
	// Reruns are the check runs it was asked to run again; RefuseRerun, where
	// set, is its answer to every such request.
	Reruns      []int64
	RefuseRerun error
}

func (f *CheckedForge) RerunCheck(_ context.Context, checkRun int64) error {
	if f.RefuseRerun != nil {
		return f.RefuseRerun
	}
	f.Reruns = append(f.Reruns, checkRun)
	return nil
}

func (f *CheckedForge) Checks(_ context.Context, number int, _ string) (publish.CheckReading, error) {
	if f.ReadError != nil {
		return publish.CheckReading{}, f.ReadError
	}
	reading := f.Reading
	if reading.HeadCommit == "" {
		merges := f.MergeRequests()
		reading.HeadCommit = merges[len(merges)-1].HeadCommit
	}
	return reading, nil
}

func (f *CheckedForge) DisableAutoMerge(_ context.Context, number int) error {
	f.Withdrawn = append(f.Withdrawn, number)
	f.DropQueuedMerge()
	return nil
}

// PublicationAnswers answers what the forge holds for each branch and refuses
// to act: a settlement that asks it to merge or close is a test failure.
type PublicationAnswers map[string]publish.PullRequest

func (p PublicationAnswers) State(_ context.Context, branch string) (publish.PullRequest, error) {
	return p[branch], nil
}

func (p PublicationAnswers) Merge(context.Context, publish.MergeRequest) (publish.MergeResult, error) {
	panic("settlement must not merge")
}

func (p PublicationAnswers) Close(context.Context, publish.CloseRequest) (publish.Closure, error) {
	panic("settlement must not close")
}
