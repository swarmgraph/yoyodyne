package testharness

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// RecordingTracker is a work tracker whose record a test can read and move.
type RecordingTracker interface {
	orchestrator.WorkTracker
	Record() orchestratortest.TrackerRecord
	SetItemStatus(status string)
	ForgetSettlement()
}

// QueuedForge is a forge a test reads what it was asked and arranges how it
// answers, including the merges it queues and later performs or drops.
type QueuedForge interface {
	orchestrator.PullRequests
	orchestrator.SupersededPublications
	OpenedRequests() []publish.Request
	MergeRequests() []publish.MergeRequest
	HoldsQueuedMerge() bool
	HoldQueuedMerge()
	PerformQueuedMerge(t *testing.T)
	DropQueuedMerge()
	ForgetMerges()
	MergeByHand(base, head string) error
	Git(arguments ...string) (string, error)
	SetQueueMerge(queue bool)
	SetReplayMerge(replay bool)
	SetMergeErr(err error)
	SetHeadCommit(commit string)
	SetTargetProtection(protection publish.BranchProtection)
	SetOnMerge(onMerge func())
	Hold(branch string, request publish.PullRequest)
	ClosedRequests() []publish.CloseRequest
}
