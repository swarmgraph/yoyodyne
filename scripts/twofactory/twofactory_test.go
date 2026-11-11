package twofactory

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

// RootVariable names the temporary root scripts/validate-two-factories.sh makes
// for a validation and removes afterwards. Without it the validation does not
// run, so nothing here is ever written anywhere the script did not make.
const RootVariable = "YOYODYNE_TWO_FACTORY_ROOT"

// product is the product id both factories work.
const product = "shared"

// check is one assertion the validation work item names. A check whose piece
// has not landed names the work it waits on instead of a function, and is
// reported as not yet built: counted as neither passed nor failed.
type check struct {
	name    string
	waitsOn string
	run     func(t *testing.T, w *world)
}

// checks are the validation work item's checks, in its order. Each subtest's
// name is its number and what it asserts, which is how the script prints it.
var checks = []check{
	{name: "01 an item waiting for a person names its pull request, closes on merge, and goes to the Lead Product Manager when closed unmerged", run: checkWaitingForAPerson},
	{name: "02 requested changes start a repair round, and an ordinary comment reaches the development manager's list", run: checkFeedback},
	{name: "03 each run records the target it started from and whether it was fetched", run: checkStartingTarget},
	{name: "04 a conflict while bringing a pull request up to date is a repair round under its own limit", run: checkUpdateConflict},
	{name: "05 at the limit on waiting pull requests no new work starts and the status line names them", run: checkWaitingLimit},
	{name: "06 two factories racing for one item, exactly one claim push succeeds", run: checkClaimRace},
	{name: "07 a claim unrenewed for two hours is taken over, naming the earlier holder", run: checkClaimTakeover},
	{name: "08 a report filed on one factory shows on the other naming its factory, and its handling travels back", waitsOn: "sharing reports, their handling and directives through the records reference (yoyodyne-ifd.429.108)"},
	{name: "09 one Slack reply seen by two factories produces one directive", waitsOn: "sharing reports, their handling and directives through the records reference (yoyodyne-ifd.429.108)"},
	{name: "10 with a repair cap of one, two factories spend one repair between them", waitsOn: "counting each item's budgets across factories (yoyodyne-ifd.429.109)"},
	{name: "11 a spend whose push fails is not acted on", waitsOn: "counting each item's budgets across factories (yoyodyne-ifd.429.109)"},
	{name: "12 with the team pause set no factory starts new work, and runs in progress finish", waitsOn: "the team pause shared through the remote (yoyodyne-ifd.429.105)"},
}

// TestTwoFactories runs every check, each against a world of its own.
func TestTwoFactories(t *testing.T) {
	root := os.Getenv(RootVariable)
	if root == "" {
		t.Skipf("the two-factory validation runs only from scripts/validate-two-factories.sh, which sets %s", RootVariable)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("%s is %q, which is not an absolute path", RootVariable, root)
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.waitsOn != "" {
				// The script reads this line by its number to say what the
				// check waits on.
				t.Skipf("not yet built: check %s waits on %s", strings.SplitN(c.name, " ", 2)[0], c.waitsOn)
			}
			c.run(t, newWorld(t, filepath.Join(root, "check-"+strings.SplitN(c.name, " ", 2)[0])))
		})
	}
}

// world is one check's two factories and the remote they share.
type world struct {
	// root is the directory this check's world lives in, inside the script's
	// temporary root.
	root string
	// remote is the throwaway bare repository both factories push to and
	// fetch from, with one commit on main.
	remote string
	a, b   *factoryHome
}

// factoryHome is one factory: a machine home of its own, with the product's
// state and worktrees under it as the machine home design lays them out, a
// clone of the shared remote, and the machine identity kept at the top of the
// home. Two homes on one machine are two factories, because each keeps its own
// machine identity.
type factoryHome struct {
	name string
	// home is the machine home, the directory ~/.yoyodyne would be.
	home string
	// clone is this factory's checkout of the shared remote.
	clone string
	// worktreeRoot is where this factory cuts its runs' worktrees.
	worktreeRoot string
	// machine is the machine identity kept in home.
	machine string
}

// stateRoot is the state root a store of this factory's is opened on.
func (f *factoryHome) stateRoot() string { return f.home }

func newWorld(t *testing.T, root string) *world {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("make the check's directory: %v", err)
	}
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	git(t, root, "init", "--quiet", "--bare", "-b", "main", remote)
	git(t, root, "init", "--quiet", "-b", "main", seed)
	writeFile(t, filepath.Join(seed, "README.md"), "a throwaway repository two factories share\n")
	git(t, seed, "add", "README.md")
	git(t, seed, "commit", "--quiet", "-m", "first")
	git(t, seed, "push", "--quiet", remote, "main")
	w := &world{root: root, remote: remote}
	w.a = newFactoryHome(t, root, "factory-a", remote)
	w.b = newFactoryHome(t, root, "factory-b", remote)
	if w.a.machine == w.b.machine {
		t.Fatalf("both homes name machine %s; two homes must be two factories", w.a.machine)
	}
	return w
}

func newFactoryHome(t *testing.T, root, name, remote string) *factoryHome {
	t.Helper()
	machineHome := filepath.Join(root, name, "home", ".yoyodyne")
	clone := filepath.Join(root, name, "repository")
	git(t, root, "clone", "--quiet", remote, clone)
	worktreeRoot := filepath.Join(home.ProjectDirectory(machineHome, product), "worktrees")
	if err := os.MkdirAll(worktreeRoot, 0o700); err != nil {
		t.Fatalf("make %s's worktree directory: %v", name, err)
	}
	machine, err := home.MachineIdentity(machineHome)
	if err != nil {
		t.Fatalf("make %s's machine identity: %v", name, err)
	}
	return &factoryHome{name: name, home: machineHome, clone: clone, worktreeRoot: worktreeRoot, machine: machine}
}

// git runs one Git command in dir with no user or system configuration, so
// the operator's own Git settings never reach the validation.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Two Factory Validation", "GIT_AUTHOR_EMAIL=validation@example.invalid",
		"GIT_COMMITTER_NAME=Two Factory Validation", "GIT_COMMITTER_EMAIL=validation@example.invalid")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make the directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
