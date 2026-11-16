package testharness

// ContinuedRun is one continuation the carry-out dispatched: the item, and the
// run it named to be re-entered. The run is recorded because naming it is what
// keeps a repair from being carried out as a fresh run of the same item.
type ContinuedRun struct {
	WorkItemID string
	RunID      string
}
