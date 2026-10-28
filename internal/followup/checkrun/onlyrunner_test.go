package checkrun

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The check runner is the only schedule that re-checks a hold's cause
// (docs/designs/follow-ups.md, "What ends a follow-up"). This fails where any
// code outside this package runs a check itself: calls an evaluator's Evaluate,
// or starts xcodebuild, whose license check is the cause the October 24 stop
// waited on. A watch session's holds wait on their follow-ups, which the
// session's own tests hold to: the license hold starts no run to find out, and
// the lapsed login asks the provider nothing itself
// (internal/orchestrator/followups_test.go).
func TestNothingOutsideTheCheckRunnerRunsAHoldsCheck(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	own, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	files := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || path == own) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		importsRunner := ""
		for _, spec := range parsed.Imports {
			if imported, _ := strconv.Unquote(spec.Path.Value); imported == "github.com/mason-bryant/yoyodyne/internal/followup/checkrun" {
				importsRunner = "checkrun"
				if spec.Name != nil {
					importsRunner = spec.Name.Name
				}
			}
		}
		relative, _ := filepath.Rel(root, path)
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.BasicLit:
				if node.Kind == token.STRING && strings.Contains(node.Value, "xcodebuild") && !strings.Contains(node.Value, " ") {
					found = append(found, relative+": starts xcodebuild")
				}
			case *ast.CallExpr:
				selector, ok := node.Fun.(*ast.SelectorExpr)
				if ok && importsRunner != "" && selector.Sel.Name == "Evaluate" {
					found = append(found, relative+": runs a check's Evaluate itself")
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("checks are run outside the check runner, which is the one schedule that re-checks a hold's cause:\n%s", strings.Join(found, "\n"))
	}
}

// The scheduler's package re-checks a hold's cause outside the check runner in
// exactly the places below, each named by its file and function. They are what
// existed when the runner landed, and the list may only shrink: the test fails
// on any re-check that is not on it, on an entry that no longer re-checks
// anything (remove it), and on the list growing past maxExistingReChecks.
//
// A re-check is found by the forms these take: asking a provider about its
// login (CheckAvailability), looking a tool up on the search path (LookPath),
// running a command (exec.Command, exec.CommandContext), waiting out a probe
// interval or a cooldown before trying again (time.Add of anything named for a
// probe or a cooldown), and lifting a hold's record (Clear).
var existingReChecks = map[string]string{
	// The three the Lead Product Manager named on October 27.
	"nocheckran.go:state":                  "a trial run every 30 minutes for checks the machine refused to start, where the harness does not recognise the cause",
	"launchsettings.go:launchSettingsHeld": "the launch-settings hold, asked again after its probe interval",
	"divergedtarget.go:liftDivergences":    "the target-branch divergence hold, lifted by the convergence sweep",
	// Found beside them when this guard was written.
	"schedule.go:providerAway":              "a provider nobody can reach, pulled into again after the probe interval, and a login asked about by a session with no check runner",
	"recurring.go:providerAway":             "a recurring role pass waiting out the same provider outage for the probe interval",
	"followups.go:loginAskedWithoutARunner": "a lapsed login asked about by a session with no check runner",
	"schedule.go:brake":                     "the brake's trial run, started when its cooldown ends",
	"schedule.go:settleLostProbe":           "the brake's trial run, its cooldown restarted when the run was lost",
	"schedule.go:settleRefusedRecovery":     "the brake's trial run, its cooldown restarted when the recovery was refused",
	"schedule.go:recordNoProbe":             "the brake's trial run, its cooldown restarted when nothing could be tried",
	"schedule.go:settleProbe":               "the brake's trial run, its cooldown restarted when the run blocked",
}

// maxExistingReChecks is how many entries existingReChecks had when this guard
// was written. Lower it as entries are removed; it is never raised.
const maxExistingReChecks = 11

// notReChecks are the same forms where they re-check no hold: a run's own
// start, routing or waiting, a hold lifted because work proved its cause gone,
// and the check runner's own release.
var notReChecks = map[string]string{
	"provideroutage.go:requireBackendReady":    "the check before one run starts, which holds nothing",
	"developerrouting.go:endpointProblem":      "the routing of one run to an endpoint",
	"pipeline.go:awaitRecordedUsageLimit":      "a running run waiting out its own usage limit",
	"provideroutage.go:pauseForProviderOutage": "a running run waiting out a provider refusal",
	"provideroutage.go:noticeProviderServed":   "a provider that just served a turn, which proves the outage over",
	"launchsettings.go:requireLaunchSettings":  "a run's own start finding the launch settings in force",
	"followups.go:followUpRound":               "the release of a hold whose follow-up the check runner found passed",
}

func reCheckForm(file *ast.File, fset *token.FileSet, call *ast.CallExpr) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	switch selector.Sel.Name {
	case "CheckAvailability", "LookPath", "Command", "CommandContext", "Clear":
		return selector.Sel.Name
	case "Add":
		if len(call.Args) != 1 {
			return ""
		}
		var argument strings.Builder
		ast.Inspect(call.Args[0], func(node ast.Node) bool {
			if identifier, ok := node.(*ast.Ident); ok {
				argument.WriteString(strings.ToLower(identifier.Name) + " ")
			}
			return true
		})
		if said := argument.String(); strings.Contains(said, "probe") || strings.Contains(said, "cooldown") {
			return "waits out " + strings.TrimSpace(said)
		}
	}
	return ""
}

// Nothing in the scheduler's package re-checks a hold's cause except what
// existingReChecks names, and that list only shrinks.
func TestTheSchedulerReChecksNoHoldsCauseBeyondTheExistingOnes(t *testing.T) {
	t.Parallel()
	found, err := reChecksIn(filepath.Join("..", "..", "orchestrator"))
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range unlisted(found) {
		t.Error(problem)
	}
	for name := range existingReChecks {
		if !found[name] {
			t.Errorf("%s no longer re-checks a hold's cause; remove it from existingReChecks and lower maxExistingReChecks", name)
		}
	}
	if len(existingReChecks) > maxExistingReChecks {
		t.Errorf("existingReChecks has %d entries and may only shrink from %d: a new re-check belongs in the check runner", len(existingReChecks), maxExistingReChecks)
	}
}

// The guard fails on a planted fourth kind of re-check: a new timer that
// starts a trial run once a probe interval has passed.
func TestTheGuardFailsOnANewReCheck(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	planted := "package orchestrator\n\nimport \"time\"\n\nfunc retryTheHold(since time.Time, holdProbe time.Duration) bool {\n\treturn time.Now().After(since.Add(holdProbe))\n}\n"
	if err := os.WriteFile(filepath.Join(directory, "planted.go"), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := reChecksIn(directory)
	if err != nil {
		t.Fatal(err)
	}
	if problems := unlisted(found); len(problems) != 1 || !strings.Contains(problems[0], "planted.go:retryTheHold") {
		t.Fatalf("the guard found %v in a planted re-check, want it named", problems)
	}
}

func unlisted(found map[string]bool) []string {
	var problems []string
	for name := range found {
		if _, existing := existingReChecks[name]; existing {
			continue
		}
		if _, exempt := notReChecks[name]; exempt {
			continue
		}
		problems = append(problems, name+" re-checks a hold's cause outside the check runner; write a follow-up whose check the runner runs instead")
	}
	sort.Strings(problems)
	return problems
}

// reChecksIn is every file:function in a directory's non-test Go files that
// takes one of the forms reCheckForm recognises.
func reChecksIn(directory string) (map[string]bool, error) {
	entries, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	files := token.NewFileSet()
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && reCheckForm(parsed, files, call) != "" {
					found[filepath.Base(path)+":"+function.Name.Name] = true
				}
				return true
			})
		}
	}
	return found, nil
}
