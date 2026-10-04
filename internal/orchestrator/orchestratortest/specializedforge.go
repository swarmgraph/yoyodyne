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
