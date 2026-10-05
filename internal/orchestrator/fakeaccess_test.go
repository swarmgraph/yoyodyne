package orchestrator

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
)

// The fixtures and helpers several test files share are typed on what they ask
// of orchestratortest's fakes. These interfaces include what a test reads or
// arranges beyond the interfaces the orchestrator asks of them.

// recordingTracker is a work tracker whose record a test can read and move.
type recordingTracker interface {
	WorkTracker
	Record() orchestratortest.TrackerRecord
	SetItemStatus(status string)
	ForgetSettlement()
}

// recordingBackend is a provider that remembers every invocation it served and
// the session it served the developer under.
type recordingBackend interface {
	backend.Backend
	RequestsMade() []backend.RunRequest
	DeveloperSessionID() string
}

// queuedForge is a forge a test reads what it was asked and arranges how it
// answers, including the merges it queues and later performs or drops.
type queuedForge interface {
	PullRequests
	SupersededPublications
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

var (
	_ recordingTracker = (*orchestratortest.Tracker)(nil)
	_ recordingBackend = (*orchestratortest.Backend)(nil)
	_ queuedForge      = (*orchestratortest.Forge)(nil)
)
