package doctor

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

const storeHoldersCheck = "tracker-store"

// storeHoldersWait bounds how long doctor waits for the harness's own tracker
// calls to finish before it looks; new ones wait behind it meanwhile.
const storeHoldersWait = 5 * time.Second

// checkStoreHolders names every process outside the harness that has the
// tracker's store files open directly. While the tracker's server runs, every
// bd on this machine is meant to go through it.
func (d *diagnosis) checkStoreHolders(ctx context.Context, repository string) []Finding {
	client := beads.Client{Runner: d.env.Runner, Dir: repository, Timeout: storeHoldersWait, Holders: d.env.StoreHolders}
	reach, found := client.Reach(ctx)
	if !found {
		return nil
	}
	finding := Finding{Check: storeHoldersCheck, Status: StatusOK, Detail: reach.Says()}
	holders, unknown := client.OutsideHolders(ctx)
	switch {
	case unknown != "":
		finding.Status = StatusWarning
		finding.Summary = "whether a process outside the harness holds the tracker's store directly could not be told"
		finding.Detail = unknown
		finding.Remedy = "yoyo doctor"
	case len(holders) > 0:
		finding.Status = StatusWarning
		finding.Summary = fmt.Sprintf("the tracker's store is held directly by a process outside the harness: %s", beads.SayHolders(holders))
		finding.Remedy = fmt.Sprintf("ps -p %d", holders[0].PID)
	default:
		finding.Summary = "no process outside the harness holds the tracker's store directly"
	}
	return []Finding{finding}
}
