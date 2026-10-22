package cli

// The tests here read this repository's own goals rather than a fixture,
// because the fixtures cannot fail the way that matters.
//
// Everything the goals gate does rests on one document parsing: the heading
// whose whole text is `Goals`, each goal an unindented entry under it, and an
// attribution matching a statement word for word once case, spacing, and
// trailing punctuation are folded. A document that stops satisfying any of that
// does not fail loudly — it yields no goals, `Known()` goes false, and every
// attribution becomes "uncheckable", which stops admission being refused and
// reports the queue as unchecked rather than untraceable. Synthetic documents
// written by a test satisfy the parser by construction and can never catch it.
//
// What reading them here may fail over is the harness's own record and not their
// prose, which is yoyodyne-ifd.326's decision and the same line
// internal/goal/goal_test.go now draws. A goal written across two lines, a goals
// document that states none, and a repository with no goal in force are things a
// person wrote in a document they own, and a `make test` that goes red on one of
// them makes rewording the document to suit the checker the cheapest way out.
// They are reported instead, by `yoyo goals list` on stderr and in `--json`, and
// carried into a release assessment by internal/conformance's goals check —
// where a goals document nobody can read and a repository with nothing to check
// an attribution against refuse the cut, and a wrapped goal is a note beside it.
// That is a surface that can afford to be right about them: it reads them in
// front of the person who owns the document, at the moment it matters.
//
// So what fails here is what this repository's own code got wrong about those
// documents — a report naming no place to open, a goal the harness collected and
// then cannot resolve, and an attribution the tracker already carries that no
// longer names anything.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// repositoryConfigPath is this repository's own configuration, read from the
// package the test lives in.
const repositoryConfigPath = "../../.yoyodyne/config.yaml"

// backlogAttribution is the goal named by the work items in this repository's
// backlog that carry an attribution, in the words the item records. It is
// checked verbatim because that is the claim the tracker actually holds: the
// parser being healthy is not the same fact as the attributions already written
// still resolving, and a reworded goal breaks the second while leaving the
// first perfectly green.
const backlogAttribution = "Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification."

func TestThisRepositoryOwnGoalsAreReadableByTheHarness(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runCLI(t, "goals", "list", "--config", repositoryConfigPath, "--json")
	if code != 0 {
		t.Fatalf("goals list code = %d, stderr = %q", code, stderr)
	}
	// A goals document that could not be read is reported rather than refused,
	// so a green exit status is not on its own evidence that anything was read.
	// It is logged rather than failed: the listing is the report, and what makes
	// it refuse anything is the release assessment reading the same set.
	if strings.Contains(stderr, "goals not read") {
		t.Logf("a goals document in this repository could not be read: %s", stderr)
	}
	var listed struct {
		Goals []struct {
			Statement  string `json:"statement"`
			ArtifactID string `json:"artifact_id"`
			InForce    bool   `json:"in_force"`
		} `json:"goals"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(listed.Goals) == 0 {
		t.Logf("this repository records no goal work can be attributed to, so every attribution reads as unchecked; `yoyo release` refuses a cut on that and this does not")
	}
	// Each goal the listing did carry says which document states it and that the
	// document is in force. That is what the harness put in the report rather
	// than anything the document says, so it is held to rather than logged.
	for _, recorded := range listed.Goals {
		if !recorded.InForce || strings.TrimSpace(recorded.ArtifactID) == "" {
			t.Fatalf("goal = %#v", recorded)
		}
	}
}

func TestThisRepositoryOwnGoalsResolveAsAttributions(t *testing.T) {
	t.Parallel()

	goals := repositoryGoals(t)
	if reason, uncheckable := goals.Uncheckable(); uncheckable {
		// There is nothing to round-trip, so this says so and stops rather than
		// failing: a repository that has not written its goals down yet is what
		// `yoyo release` refuses a cut over, not what reddens a build here.
		t.Skipf("nothing in this repository can check an attribution: %s", reason)
	}
	// Every goal the document states can be named on a work item. Reading a
	// statement and resolving one go through different code, and a statement this
	// collected but could not match would be a goal the harness offers and then
	// refuses.
	for _, recorded := range goals.Goals {
		if attribution := goals.Attribute(recorded.Statement); !attribution.Resolved() {
			t.Fatalf("the goal %q does not resolve as an attribution: %s", recorded.Statement, attribution.Reason)
		}
	}
	// The attribution the backlog already carries still resolves. If this fails,
	// a goal was reworded and the items attributed to the old wording now read as
	// naming a goal the goals do not state: re-attribute them with the tracker's
	// "attribute" action, which appends and rewrites nothing, and check the rest
	// with `yoyo goals attribution`.
	if attribution := goals.Attribute(backlogAttribution); !attribution.Resolved() {
		t.Fatalf("the goal this repository's attributed work items name no longer resolves: %s", attribution.Reason)
	}
}

// TestEveryWrappedGoalInThisRepositoryIsReportedWithAPlaceToOpen runs the wrap
// report over the documents it is for, and holds the report rather than the
// documents.
//
// It used to fail on every wrapped goal, on the argument that a report nobody is
// made to read is a convention that does not hold — reading one by eye is how
// six items stayed orphaned once already. yoyodyne-ifd.326 took that back:
// whether a goal is hard-wrapped is how somebody typed a document they own, and
// a build that reddens on it teaches rewriting the document to suit the checker.
// The convention is still worth holding, and what holds it now is a surface that
// reads it in front of the person who can act on it — `yoyo goals list` on
// stderr, and internal/conformance's goals check as a note on a release
// assessment.
//
// What is checked here instead is that each of those reports names a place to
// open. A wrapped goal reported with no file, or with a line nobody can turn to,
// is this repository's own defect rather than the product manager's, and it is
// the half of the old check that never depended on what any document said.
func TestEveryWrappedGoalInThisRepositoryIsReportedWithAPlaceToOpen(t *testing.T) {
	t.Parallel()

	for _, problem := range repositoryGoals(t).WrapProblems {
		// Reported one at a time, because what somebody has to open is one place in
		// one file and a tally sends them looking for it.
		if problem.Path == "" || problem.Line < 1 || problem.ArtifactID == "" {
			t.Errorf("a wrapped goal is reported with nowhere to open: %#v", problem)
			continue
		}
		t.Logf("goal not written on one line: %s", problem)
	}
}

// The same check for the same reason, over the identity two goals in force
// cannot both carry. What is held to is the report rather than the documents: a
// duplicated identity is fixed by editing one of them, and a report that does not
// say which files state it sends the person who owns them looking.
func TestEveryDuplicatedGoalIdentityInThisRepositoryIsReportedWithAPlaceToOpen(t *testing.T) {
	t.Parallel()

	for _, problem := range repositoryGoals(t).IdentityProblems {
		if problem.Identity == "" || len(problem.Stated) < 2 {
			t.Errorf("an identity two goals carry is reported without saying which: %#v", problem)
			continue
		}
		for _, stating := range problem.Stated {
			if !strings.Contains(stating, "(") {
				t.Errorf("a duplicated identity names no file to open: %#v", problem)
			}
		}
		t.Logf("goal identity stated twice: %s", problem)
	}
}

// repositoryGoals collects the goals from this repository as the harness reads
// them, through the configuration rather than a guessed set of artifact homes:
// a test that hardcoded the directories would keep passing after the project
// moved them, which is one of the ways the gate can quietly stop running.
func repositoryGoals(t *testing.T) goal.Set {
	t.Helper()

	repository, cfg := repositoryConfiguration(t)
	goals, err := loadGoals(repository, cfg.Product)
	if err != nil {
		t.Fatalf("loadGoals() error = %v", err)
	}
	// A goals document whose goals could not be read is reported and not failed:
	// a document that legitimately states none is a correct report rather than a
	// defect, and the two arrive here as the same Problem. What is held to is
	// that the report names the file, because a reader sent nowhere is ours.
	for _, problem := range goals.Problems {
		if problem.Path == "" {
			t.Errorf("a goals document in %s was not read and the report does not say which: %q",
				filepath.Base(repository), problem.Reason)
			continue
		}
		t.Logf("goals not read: %s", problem)
	}
	return goals
}

// repositoryConfiguration is this repository read the way every command reads
// it: the configuration, and the repository the product's homes are relative
// to.
func repositoryConfiguration(t *testing.T) (string, config.Config) {
	t.Helper()

	resolved, err := loadConfiguration(repositoryConfigPath)
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		t.Fatalf("resolve product repository: %v", err)
	}
	return repository, resolved.Config
}
