package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

// operatorReasonsHeading opens the section of every persona that says how to
// name the operator, and operatorReasonEntry is one reason in its list.
const operatorReasonsHeading = "## Naming the operator's reason"

var operatorReasonEntry = regexp.MustCompile("(?m)^- `([a-z-]+)`: ")

// Every shipped persona, in the template "yoyo init" gives a new project and in
// the copy this repository's own roles read, lists exactly the reasons the
// resolver accepts, in its order, and says the form a role names one in: the
// field a block carries, or the reason and a colon a report's message opens
// with. A reason added to ownership.OperatorReasons and not taught here, or
// taught here and not accepted, fails.
func TestEveryPersonaNamesTheReasonsTheResolverAccepts(t *testing.T) {
	t.Parallel()

	directories := []string{
		filepath.Join("builtin", "v1", bundlePersonaDirectory),
		filepath.Join("..", "..", ".yoyodyne", "personas"),
	}
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("read %s: %v", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			_, section, found := strings.Cut(string(text), operatorReasonsHeading+"\n")
			if !found {
				t.Errorf("%s has no %q section, so its role is not told how to name the operator", path, operatorReasonsHeading)
				continue
			}
			if next := strings.Index(section, "\n## "); next >= 0 {
				section = section[:next]
			}
			var named []ownership.OperatorReason
			for _, match := range operatorReasonEntry.FindAllStringSubmatch(section, -1) {
				named = append(named, ownership.OperatorReason(match[1]))
			}
			if want := ownership.OperatorReasons(); !slices.Equal(named, want) {
				t.Errorf("%s names %v, and the resolver accepts %v", path, named, want)
			}
			if !strings.Contains(section, `"operator_reason"`) && !strings.Contains(section, "and a colon") {
				t.Errorf("%s lists the reasons without saying the form a finding or escalation names one in", path)
			}
		}
	}
}
