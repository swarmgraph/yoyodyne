package beads_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/composition"
)

// repositoryRoot is the checkout these tests run in, reached from the package
// directory. The files are read where they live rather than from a copy,
// because a copy is exactly what cannot carry the defect.
const repositoryRoot = "../.."

// beadsHomeReference finds every place a file names a Beads repository -- the
// repository itself, a release under it, or the raw view its installer is
// fetched from -- and captures the owner the name is under.
var beadsHomeReference = regexp.MustCompile(`(?:github\.com|raw\.githubusercontent\.com)/([A-Za-z0-9_.-]+)/beads\b`)

// canonicalOwner is what beads.Home says, read back from it by the same pattern
// rather than restated, so the test holds the repository to the constant
// rather than to its own copy of the same answer.
func canonicalOwner(t *testing.T) string {
	t.Helper()

	match := beadsHomeReference.FindStringSubmatch(beads.Home)
	if match == nil {
		t.Fatalf("beads.Home = %q is not a Beads repository URL the sweep would recognize", beads.Home)
	}
	return match[1]
}

// Files that legitimately name the other owner, and why. Nothing else may.
var namesTheOtherOwnerOnPurpose = map[string]string{
	// The one place the module path is written, with the reason beside it.
	"internal/beads/home.go": "beads.ModulePath names the module path the tracker's tags still declare",
}

// Directories whose files are records of something rather than the install
// path: what the tracker itself writes, diagnoses of what was, and a release's
// notes, which carry each closed work item's description verbatim -- the item
// that asked for the home to be settled names both homes in its own.
var recordsRatherThanInstallPath = []string{".beads/", "docs/diagnoses/", "docs/releases/"}

// TestEveryBeadsHomeThisRepositoryNamesIsTheCanonicalOne is the gate on the
// install path naming one Beads home. It sweeps every file this repository
// carries for a Beads URL and fails on any that is not under beads.Home,
// outside the files that record the other name on purpose -- so the README,
// the install script, the operations guide, the getting-started walkthrough,
// the CI workflow, and the two remedies in Go cannot disagree about where bd comes
// from, and a newcomer following any one of them is sent to the same place.
func TestEveryBeadsHomeThisRepositoryNamesIsTheCanonicalOne(t *testing.T) {
	t.Parallel()

	files := repositoryFiles(t)
	owner := canonicalOwner(t)
	named := 0
	for _, path := range files {
		if isRecord(path) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range beadsHomeReference.FindAllStringSubmatch(string(content), -1) {
			named++
			if match[1] == owner {
				continue
			}
			if reason, allowed := namesTheOtherOwnerOnPurpose[path]; allowed {
				t.Logf("%s names %s on purpose: %s", path, match[0], reason)
				continue
			}
			t.Errorf("%s sends somebody to %s; the one Beads home is %s (docs/diagnoses/yoyodyne-ifd-125-6-beads-home.md says why)", path, match[0], beads.Home)
		}
	}
	// A sweep that matched nothing would pass while checking none of what it is
	// for; the README alone names the home twice.
	if named < 2 {
		t.Fatalf("the sweep found %d Beads URL(s) in the repository, which is fewer than the README alone carries", named)
	}
}

// TestTheReadmeAndTheInstallScriptAgreeOnTheBeadsHome is the pairwise claim the
// sweep above implies, stated on its own for the two documents a newcomer
// actually reads: the README, which every route starts from, and the install
// script the README's first line fetches, which names bd as the one
// prerequisite it cannot install. Both name beads.Home and neither names
// anything else.
func TestTheReadmeAndTheInstallScriptAgreeOnTheBeadsHome(t *testing.T) {
	t.Parallel()

	readme := homesNamedBy(t, "README.md")
	if len(readme) == 0 {
		t.Fatalf("README.md names no Beads repository at all, and it is where a newcomer is told bd is a prerequisite")
	}
	for home := range readme {
		if home != beads.Home {
			t.Errorf("README.md names %s, want %s", home, beads.Home)
		}
	}

	// The script is on its way in under yoyodyne-ifd.125.2; a base without it
	// has nothing to compare, and says so rather than passing over it.
	script := "scripts/install.sh"
	if _, err := os.Stat(filepath.Join(repositoryRoot, script)); err != nil {
		t.Logf("%s is not in this checkout (%v), so the README is held to the home on its own", script, err)
		return
	}
	installer := homesNamedBy(t, script)
	if len(installer) == 0 {
		t.Fatalf("%s names no Beads repository, and it is what sends a newcomer with no bd to one", script)
	}
	for home := range installer {
		if _, inReadme := readme[home]; !inReadme {
			t.Errorf("%s names %s, which README.md does not; both must send a newcomer to %s", script, home, beads.Home)
		}
	}
}

// TestTheDocumentsStateTheRemedyDoctorPrints holds the README and the
// operations guide to the command `yoyo doctor` and `yoyo setup` actually hand
// somebody with no bd: each quotes beads.InstallCommand verbatim, so what a
// newcomer reads and what the harness prints are one command from one home.
func TestTheDocumentsStateTheRemedyDoctorPrints(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"README.md", "docs/operations.md"} {
		content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), beads.InstallCommand) {
			t.Errorf("%s does not quote the remedy doctor prints for a missing bd: %q", path, beads.InstallCommand)
		}
	}
}

// homesNamedBy is the set of Beads repository URLs one file names, each cut to
// the repository itself so a link to a release, a document, or an anchor under
// the home counts as naming the home.
func homesNamedBy(t *testing.T, path string) map[string]struct{} {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	homes := map[string]struct{}{}
	for _, match := range beadsHomeReference.FindAllStringSubmatch(string(content), -1) {
		homes["https://github.com/"+match[1]+"/beads"] = struct{}{}
	}
	return homes
}

func isRecord(path string) bool {
	for _, prefix := range recordsRatherThanInstallPath {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// repositoryFiles is the census composition keeps: every file this checkout
// carries, tracked or untracked and unignored, which is what a reviewer is
// shown and what a newcomer clones.
func repositoryFiles(t *testing.T) []string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not on PATH, and the census is git's own answer: %v", err)
	}
	files, err := composition.Files(repositoryRoot)
	if err != nil {
		t.Fatalf("composition.Files() error = %v", err)
	}
	// A census that found nothing would report a repository with nothing wrong
	// with it.
	found := false
	for _, path := range files {
		if path == "README.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the census did not find this repository; it collected %d files", len(files))
	}
	return files
}
