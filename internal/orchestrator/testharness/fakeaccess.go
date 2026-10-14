package testharness

import (
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
)

// RecordingTracker is a work tracker whose record a test can read and move.
type RecordingTracker interface {
	orchestrator.WorkTracker
	Record() orchestratortest.TrackerRecord
	SetItemStatus(status string)
	ForgetSettlement()
}
