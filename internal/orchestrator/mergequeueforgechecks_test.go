package orchestrator

// The merge queue reads GitHub's required checks for the exact candidate commit
// and counts them as the full check only when every condition of the design
// holds (docs/designs/integration-through-a-merge-queue.md). The decision
// itself is runstate's; these tests run the harness's own reading against a
// fake forge and a real candidate.

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// forgeReadFixture is a verified candidate with checks on CI switched on and
// a fake forge wired in.
func forgeReadFixture(t *testing.T, result publish.CheckResult) (*queueFixture, *fakeCIForge, runstate.MergeQueueGeneration) {
	t.Helper()
	f := newQueueFixture(t)
	verification, err := f.work()
	if err != nil || !verification.Verified {
		t.Fatalf("Work() = %#v, %v, want the candidate verified", verification, err)
	}
	forge := &fakeCIForge{result: result}
	p := f.worker.Pipeline
	p.CIForge = forge
	p.Config.Execution.CIChecks.Enabled = true
	p.Config.Execution.CIChecks.Required = []string{"test", "race"}
	return f, forge, verification.Generation
}

func readForgeChecks(t *testing.T, f *queueFixture, generation runstate.MergeQueueGeneration) (runstate.MergeQueueGeneration, error) {
	t.Helper()
	ctx := context.Background()
	lease, held, err := f.records.LeaseWorker(ctx, queueKey)
	if err != nil || !held {
		t.Fatalf("LeaseWorker() = %v, %v", held, err)
	}
	defer lease.Release()
	return f.worker.readCIChecks(ctx, lease, queueKey, generation)
}

func TestTheForgesChecksOnTheExactCandidateCountAsTheFullCheck(t *testing.T) {
	t.Parallel()

	f, _, generation := forgeReadFixture(t, publish.CheckPassed)
	read, err := readForgeChecks(t, f, generation)
	if err != nil {
		t.Fatalf("readCIChecks() = %v", err)
	}
	evidence := read.CIChecks
	if evidence == nil || evidence.Commit != generation.Candidate || evidence.Base != generation.TargetBase ||
		evidence.TargetAtRead != generation.TargetBase || evidence.WorkflowDigest != "sha256:workflows-v1" || len(evidence.Checks) != 2 {
		t.Fatalf("evidence = %#v, want the exact candidate, its base, both checks and the workflow digest", evidence)
	}
	for _, check := range evidence.Checks {
		if check.Commit != generation.Candidate || check.Result != "passed" {
			t.Fatalf("check = %#v, want it passed on the candidate, not the head %s", check, f.head)
		}
	}
	// What was recorded is what a later process reads.
	stored := f.generations()[0]
	if stored.CIChecks == nil || stored.CIChecks.Commit != generation.Candidate {
		t.Fatalf("stored generation = %#v, want the reading kept", stored.CIChecks)
	}
	counts, why, err := f.worker.ciChecksCount(context.Background(), stored)
	if err != nil || !counts {
		t.Fatalf("ciChecksCount() = %v, %q, %v, want the checks to count", counts, why, err)
	}
}

func TestAnyRequiredCheckShortOfAPassNeverCounts(t *testing.T) {
	t.Parallel()

	for _, result := range []publish.CheckResult{publish.CheckFailed, publish.CheckPending, publish.CheckCancelled, publish.CheckSkipped, publish.CheckNotStarted, publish.CheckOther} {
		t.Run(string(result), func(t *testing.T) {
			f, _, generation := forgeReadFixture(t, result)
			read, err := readForgeChecks(t, f, generation)
			if err != nil {
				t.Fatalf("readCIChecks() = %v", err)
			}
			if read.CIChecks.WorkflowDigest != "" {
				t.Fatalf("workflow digest = %q, want none recorded for a reading that did not pass", read.CIChecks.WorkflowDigest)
			}
			counts, why, err := f.worker.ciChecksCount(context.Background(), read)
			if err != nil || counts || !strings.Contains(why, "did not pass") {
				t.Fatalf("ciChecksCount() = %v, %q, %v, want it refused as not passed", counts, why, err)
			}
		})
	}
}

func TestAReadingTheForgeGaveForAnotherCommitIsNeverRecorded(t *testing.T) {
	t.Parallel()

	f, forge, generation := forgeReadFixture(t, publish.CheckPassed)
	forge.readCommit = f.head
	read, err := readForgeChecks(t, f, generation)
	if err == nil || read.CIChecks != nil || f.generations()[0].CIChecks != nil {
		t.Fatalf("readCIChecks() = %v, evidence %#v; want the head's result refused and nothing recorded", err, read.CIChecks)
	}
}

func TestAnUnreadableForgeRecordsNothingAndCountsForNothing(t *testing.T) {
	t.Parallel()

	f, forge, generation := forgeReadFixture(t, publish.CheckPassed)
	forge.readErr = context.DeadlineExceeded
	if _, err := readForgeChecks(t, f, generation); err == nil {
		t.Fatal("readCIChecks() = nil, want the unread forge to be an error")
	}
	counts, _, err := f.worker.ciChecksCount(context.Background(), f.generations()[0])
	if err != nil || counts {
		t.Fatalf("ciChecksCount() = %v, %v, want nothing to count", counts, err)
	}
}

func TestTheForgesChecksAreNotReadWhereChecksOnCIIsOff(t *testing.T) {
	t.Parallel()

	f, forge, generation := forgeReadFixture(t, publish.CheckPassed)
	f.worker.Pipeline.Config.Execution.CIChecks.Enabled = false
	read, err := readForgeChecks(t, f, generation)
	if err != nil || read.CIChecks != nil || forge.reads != 0 {
		t.Fatalf("readCIChecks() = %v, evidence %#v, reads %d; want the forge left alone", err, read.CIChecks, forge.reads)
	}
	if counts, _, _ := f.worker.ciChecksCount(context.Background(), generation); counts {
		t.Fatal("ciChecksCount() = true with the switch off")
	}
}

func TestAWorkflowChangeOrATargetMoveWithdrawsTheForgesChecks(t *testing.T) {
	t.Parallel()

	t.Run("workflow files changed", func(t *testing.T) {
		f, forge, generation := forgeReadFixture(t, publish.CheckPassed)
		read, err := readForgeChecks(t, f, generation)
		if err != nil {
			t.Fatal(err)
		}
		forge.digest = "sha256:workflows-v2"
		if counts, why, err := f.worker.ciChecksCount(context.Background(), read); err != nil || counts || !strings.Contains(why, "workflow files changed") {
			t.Fatalf("ciChecksCount() = %v, %q, %v, want the changed workflow files to withdraw it", counts, why, err)
		}
	})
	t.Run("target moved after the read", func(t *testing.T) {
		f, _, generation := forgeReadFixture(t, publish.CheckPassed)
		read, err := readForgeChecks(t, f, generation)
		if err != nil {
			t.Fatal(err)
		}
		moveTarget(t, f.repository, "landed-after.txt")
		if counts, why, err := f.worker.ciChecksCount(context.Background(), read); err != nil || counts || !strings.Contains(why, "moved off") {
			t.Fatalf("ciChecksCount() = %v, %q, %v, want the moved target to withdraw it", counts, why, err)
		}
	})
	t.Run("target moved before the read finished", func(t *testing.T) {
		f, _, generation := forgeReadFixture(t, publish.CheckPassed)
		moveTarget(t, f.repository, "landed-during.txt")
		read, err := readForgeChecks(t, f, generation)
		if err != nil {
			t.Fatal(err)
		}
		if counts, why, err := f.worker.ciChecksCount(context.Background(), read); err != nil || counts || !strings.Contains(why, "moved off") {
			t.Fatalf("ciChecksCount() = %v, %q, %v, want the reading taken off the base to refuse", counts, why, err)
		}
	})
}
