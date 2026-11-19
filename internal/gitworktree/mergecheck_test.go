package gitworktree

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// mergeCheckFixture is a base, a candidate built on it, and the target having
// moved past the base, which is what a pull request the forge merges without
// requiring it to be up to date can find.
type mergeCheckFixture struct {
	manager                    *Manager
	repository                 string
	base, candidate, movedBase string
}

func newMergeCheckFixture(t *testing.T) mergeCheckFixture {
	t.Helper()
	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	base := gitLine(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "checkout", "-q", "-b", "candidate", base)
	writeFile(t, repository, "feature.txt", "the candidate's change\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-q", "-m", "candidate")
	candidate := gitLine(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "checkout", "-q", "-b", "moved", base)
	writeFile(t, repository, "elsewhere.txt", "somebody else's change\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-q", "-m", "elsewhere")
	return mergeCheckFixture{manager: manager, repository: repository, base: base, candidate: candidate, movedBase: gitLine(t, repository, "rev-parse", "HEAD")}
}

// merge makes the forge's merge commit of the candidate on a branch started at
// onto, with whatever extra the test writes before it commits.
func (f mergeCheckFixture) merge(t *testing.T, name, onto string, extra func()) string {
	t.Helper()
	runGit(t, f.repository, "checkout", "-q", "-b", name, onto)
	runGit(t, f.repository, "merge", "-q", "--no-ff", "--no-commit", f.candidate)
	if extra != nil {
		extra()
		runGit(t, f.repository, "add", ".")
	}
	runGit(t, f.repository, "commit", "-q", "-m", "Merge pull request #7 from swarmgraph/yoyodyne/merge-queue/mqe-1")
	return gitLine(t, f.repository, "rev-parse", "HEAD")
}

func TestCheckMergeCommitMatchesAMergeOnTheRecordedBaseWithTheCandidatesFiles(t *testing.T) {
	t.Parallel()
	f := newMergeCheckFixture(t)
	merge := f.merge(t, "target", f.base, nil)
	check, err := f.manager.CheckMergeCommit(context.Background(), merge, f.base, f.candidate)
	if err != nil {
		t.Fatalf("CheckMergeCommit() error = %v", err)
	}
	if !check.Matches() || check.Says() != "" {
		t.Fatalf("CheckMergeCommit() = %+v (%s), want a match", check, check.Says())
	}
}

func TestCheckMergeCommitReportsAMergeOnAnotherBase(t *testing.T) {
	t.Parallel()
	f := newMergeCheckFixture(t)
	merge := f.merge(t, "target", f.movedBase, nil)
	check, err := f.manager.CheckMergeCommit(context.Background(), merge, f.base, f.candidate)
	if err != nil {
		t.Fatalf("CheckMergeCommit() error = %v", err)
	}
	if check.Matches() || !check.BaseDiffers() || check.FirstParent != f.movedBase {
		t.Fatalf("CheckMergeCommit() = %+v, want a first parent of %s that is not the base", check, f.movedBase)
	}
	if len(check.FilesDiffer) != 0 {
		t.Fatalf("FilesDiffer = %v, want none: the merge changed what the candidate changed", check.FilesDiffer)
	}
	if !strings.Contains(check.Says(), f.movedBase) || !strings.Contains(check.Says(), f.base) {
		t.Fatalf("Says() = %q, want both bases named", check.Says())
	}
}

func TestCheckMergeCommitReportsFilesThatAreNotTheCandidates(t *testing.T) {
	t.Parallel()
	f := newMergeCheckFixture(t)
	merge := f.merge(t, "target", f.base, func() {
		writeFile(t, f.repository, "extra.txt", "nobody reviewed this\n")
		writeFile(t, f.repository, "feature.txt", "the candidate's change, altered\n")
	})
	check, err := f.manager.CheckMergeCommit(context.Background(), merge, f.base, f.candidate)
	if err != nil {
		t.Fatalf("CheckMergeCommit() error = %v", err)
	}
	if check.BaseDiffers() {
		t.Fatalf("CheckMergeCommit() = %+v, want the first parent to be the base", check)
	}
	if want := []string{"extra.txt", "feature.txt"}; !reflect.DeepEqual(check.FilesDiffer, want) {
		t.Fatalf("FilesDiffer = %v, want %v", check.FilesDiffer, want)
	}
	if check.Matches() {
		t.Fatal("Matches() = true for a merge with other files")
	}
}

func TestCheckMergeCommitRefusesACommitThatIsNotOne(t *testing.T) {
	t.Parallel()
	f := newMergeCheckFixture(t)
	if _, err := f.manager.CheckMergeCommit(context.Background(), "not-a-commit", f.base, f.candidate); err == nil {
		t.Fatal("CheckMergeCommit() error = nil, want a refusal")
	}
}

func TestLandedPullRequestsNamesEachCandidateAndItsBase(t *testing.T) {
	t.Parallel()
	f := newMergeCheckFixture(t)
	merge := f.merge(t, "target", f.base, nil)
	landings, err := f.manager.LandedPullRequests(context.Background(), "target", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LandedPullRequests() error = %v", err)
	}
	last := landings[len(landings)-1]
	if last.Merge != merge || last.Candidate != f.candidate || last.Base != f.base || last.Unchecked != "" {
		t.Fatalf("last landing = %+v, want merge %s of candidate %s on %s", last, merge, f.candidate, f.base)
	}
	if first := landings[0]; first.Unchecked == "" {
		t.Fatalf("first landing = %+v, want a commit that is no merge listed as not checkable", first)
	}
}
