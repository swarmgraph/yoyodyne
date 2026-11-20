package dashboard

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

var updateFixtureProblems = flag.Bool("update-fixture-problems", false, "rewrite the problems every standing fixture carries from the rest of that fixture")

// problemsKey is where a standing fixture carries its problems: last, so a
// rewrite replaces the end of the file and leaves the rest of it as written.
const problemsKey = ",\n  \"problems\": "

// Every standing fixture carries the problems the read model derives from the
// rest of it, and no other, so the problems section is rendered from what the
// server would actually send for that standing; and its operator entries are
// already on its needs-a-human line, as the read model keeps them.
// -update-fixture-problems rewrites them.
func TestEveryStandingFixtureCarriesItsDerivedProblems(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(filepath.Join("testdata", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".json")
		if !strings.HasPrefix(name, "standing-") {
			continue
		}
		body := fixture(t, name)
		var standing readmodel.Standing
		strict(t, name, body, &standing)
		line := len(standing.NeedsHuman)
		standing.SetProblems(nil)
		if len(standing.NeedsHuman) != line {
			t.Errorf("fixture %s has a problem of the operator's its needs-a-human line does not carry", name)
		}
		want := problemMembers(t, standing.Problems, standing.ProblemsProblem)
		if strings.HasSuffix(strings.TrimRight(string(body), " \n"), ",\n"+want+"\n}") {
			continue
		}
		if !*updateFixtureProblems {
			t.Errorf("fixture %s does not carry the problems the read model derives from it; run `go test ./internal/dashboard -run TestEveryStandingFixtureCarriesItsDerivedProblems -update-fixture-problems` and review the change", name)
			continue
		}
		text := strings.TrimRight(string(body), " \n")
		if at := strings.Index(text, problemsKey); at >= 0 {
			text = text[:at]
		} else {
			text = strings.TrimRight(strings.TrimSuffix(text, "}"), " \n")
		}
		text += ",\n" + want + "\n}\n"
		if err := os.WriteFile(filepath.Join("testdata", "fixtures", name+".json"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// zonedKinds are the attention kinds whose sentences say a moment in the
// reader's own zone. A fixture cannot carry those sentences, because a fixture
// is read under whatever zone the machine running the tests is in and the read
// model refuses a sentence that disagrees with the one it derives; so their
// problems carry the record without them, as the factory problems in the same
// fixtures do, and render.js supplies the words the page shows.
var zonedKinds = map[string]bool{"failing-task": true, "capacity-degraded": true, "heavy-check-kept": true}

// problemMembers is the problems and the problem with them as the members a
// standing fixture ends with, indented as the fixtures are. The fixture's own
// text is held to it, so a fixture that leaves the list out, or carries a
// sentence the list does not, differs.
func problemMembers(t *testing.T, problems []readmodel.Problem, problem string) string {
	t.Helper()
	encoded, err := json.Marshal(problems)
	if err != nil {
		t.Fatal(err)
	}
	var generic []map[string]any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatal(err)
	}
	for _, each := range generic {
		if entry, ok := each["entry"].(map[string]any); ok && zonedKinds[fmt.Sprint(entry["kind"])] {
			for _, sentence := range []string{"what", "whose", "said_what", "said_whose"} {
				delete(entry, sentence)
			}
		}
	}
	indented, err := json.MarshalIndent(struct {
		Problems        []map[string]any `json:"problems"`
		ProblemsProblem string           `json:"problems_problem,omitempty"`
	}{generic, problem}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(strings.TrimPrefix(string(indented), "{\n"), "\n}")
}

// The kinds of problem the page groups by are the read model's four, in the
// model's order with broken first, and the page decides nobody's move: an entry
// is marked as the operator's from the owner the model carries on it, and from
// nothing the page reads in its sentences.
func TestThePageGroupsProblemsByTheModelsKinds(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	_, script := w.get("/assets/dashboard.js", nil)
	var named []readmodel.ProblemKind
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, `{ kind: "`) && strings.Contains(line, "heading:") {
			named = append(named, readmodel.ProblemKind(strings.Split(line, `"`)[1]))
		}
	}
	kinds := readmodel.ProblemKinds()
	if len(named) != len(kinds) {
		t.Fatalf("the script names the kinds %v, and the model's are %v", named, kinds)
	}
	for i := range kinds {
		if named[i] != kinds[i] {
			t.Fatalf("the script's kind %d is %q, and the model's is %q", i, named[i], kinds[i])
		}
	}
	_, row, found := strings.Cut(script, "function problemRow(")
	if !found {
		t.Fatal("the script has no problemRow")
	}
	row, _, _ = strings.Cut(row, "\n  }")
	if !strings.Contains(row, `entry.mover === "operator"`) || strings.Contains(row, "whose.indexOf") || strings.Contains(row, "what.indexOf") {
		t.Fatalf("the row does not mark the operator's entries from the owner the model carries:\n%s", row)
	}
	if strings.Contains(script, "renderCapacity") || strings.Contains(script, `"capacity-runs"`) {
		t.Fatal("the script still draws a provider capacity section of its own")
	}
}
