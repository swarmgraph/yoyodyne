package governeddoc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func defaults() config.Config {
	return config.Config{Product: config.Product{
		ID:             "example",
		Specifications: "docs/product",
		Designs:        "docs/designs",
		Decisions:      "docs/decisions",
		Invariants:     "docs/decisions/invariants",
	}}
}

// The whole reason the package exists: a defect in a document no developer's
// change may reach does not belong to the change in hand, and saying so is what
// stops a build going red for everybody over a file only one role can edit.
func TestADefectInAGovernedDocumentIsNotTheChangesToFix(t *testing.T) {
	t.Parallel()

	routed := Route(defaults(), Defect{
		Path:   "docs/product/goals/v1-goals.md",
		Detail: "a goal it states is written across 3 physical lines",
	})
	if len(routed) != 1 {
		t.Fatalf("routed = %#v", routed)
	}
	entry := routed[0]
	if entry.Yours {
		t.Fatalf("a goals document was reported as the change's to fix: %#v", entry)
	}
	if entry.Owner != domain.RoleProductManager || entry.Home != "docs/product/goals" {
		t.Fatalf("routed = %#v, want the goals home and its owner", entry)
	}
	// A report that named neither the owner nor the way to reach them would leave
	// whoever read it exactly where the red build left them.
	for _, want := range []string{
		"docs/product/goals/v1-goals.md",
		"written across 3 physical lines",
		domain.RoleProductManager.Title(),
		"yoyo chat",
		"yoyo amendment list",
	} {
		if !strings.Contains(entry.String(), want) {
			t.Errorf("the report never says %q:\n%s", want, entry)
		}
	}
}

// The other half, and the half that keeps the gates worth having: a defect
// anywhere the change may edit still fails, and nothing here softens it.
func TestADefectOutsideTheProtectedHomesStaysTheChangesToFix(t *testing.T) {
	t.Parallel()

	routed := Route(defaults(),
		Defect{Path: "README.md", Detail: "a link resolves to nothing"},
		Defect{Path: "docs/developing-yoyo.md", Detail: "a link resolves to nothing"},
		// A directory whose name merely opens with a protected one is not inside
		// it, which is the comparison the protected paths already make.
		Defect{Path: "docs/products/notes.md", Detail: "a link resolves to nothing"},
	)
	for _, entry := range routed {
		if !entry.Yours || entry.Route != "" {
			t.Errorf("%s was escalated rather than failed: %#v", entry.Path, entry)
		}
	}
}

// The goals sit inside the specifications home and the invariants inside the
// decisions home, so the first home whose directory matches is the wrong answer:
// it would report a goal as the product home's and an invariant as the decision
// records'.
func TestADocumentIsRoutedToTheInnermostHomeThatHoldsIt(t *testing.T) {
	t.Parallel()

	for _, expected := range []struct {
		path  string
		home  string
		owner domain.AgentRole
	}{
		{"docs/product/brief.md", "docs/product", domain.RoleProductManager},
		{"docs/product/goals/v1-goals.md", "docs/product/goals", domain.RoleProductManager},
		{"docs/designs/v1-harness-design.md", "docs/designs", domain.RoleArchitect},
		{"docs/decisions/markdown-source-of-truth.md", "docs/decisions", domain.RoleArchitect},
		{"docs/decisions/invariants/one-promotion.md", "docs/decisions/invariants", domain.RoleArchitect},
	} {
		routed := Route(defaults(), Defect{Path: expected.path, Detail: "something is wrong with it"})
		if routed[0].Home != expected.home || routed[0].Owner != expected.owner {
			t.Errorf("%s routed to %#v, want %s owned by the %s",
				expected.path, routed[0], expected.home, expected.owner)
		}
	}
}

// A project that files its designs somewhere else has moved the home rather
// than made a design a developer's to rewrite, so the routing follows the
// configuration for the same reason the protected paths do.
func TestRoutingFollowsTheConfiguredHomes(t *testing.T) {
	t.Parallel()

	moved := defaults()
	moved.Product.Designs = "architecture/designs"

	if routed := Route(moved, Defect{Path: "architecture/designs/v1.md", Detail: "it supports nothing"}); routed[0].Yours {
		t.Fatalf("the moved designs home was not protected: %#v", routed[0])
	}
	// And the directory it used to be in is now an ordinary one.
	if routed := Route(moved, Defect{Path: "docs/designs/v1.md", Detail: "it supports nothing"}); !routed[0].Yours {
		t.Fatalf("a directory the configuration no longer names was still escalated: %#v", routed[0])
	}
}

// The harness's own configuration directory is protected and is nobody's
// document. Routing it to an owner would invent a role to blame; routing it to
// nobody at all would drop the report.
func TestAProtectedPathThatIsNobodysDocumentRoutesToWhoeverAdmitsTheFix(t *testing.T) {
	t.Parallel()

	routed := Route(defaults(), Defect{Path: ".yoyodyne/config.yaml", Detail: "it names a check nothing can run"})
	entry := routed[0]
	if entry.Yours {
		t.Fatalf("the configuration directory was reported as the change's to fix: %#v", entry)
	}
	if entry.Owner != "" || entry.Home != "" {
		t.Fatalf("routed = %#v, want no owning role for a path no home claims", entry)
	}
	if !strings.Contains(entry.String(), domain.RoleProductManager.Title()) {
		t.Errorf("the report does not say who takes it:\n%s", entry)
	}
}

// Protected is the same question Route decides Yours by, asked on its own by a
// caller that has to know which documents are its to answer for before it reads
// them. The two have to agree everywhere, or a defect would be escalated by one
// gate and answered for by neither — the configuration directory most of all,
// since it is protected, holds Markdown, and no artifact home claims it.
func TestProtectedAgreesWithWhatRouteEscalatesEverywhere(t *testing.T) {
	t.Parallel()

	for _, candidate := range []string{
		"docs/product/brief.md",
		"docs/product/goals/v1-goals.md",
		"docs/designs/v1-harness.md",
		"docs/decisions/invariants/one-promotion.md",
		".yoyodyne/config.yaml",
		".yoyodyne/personas/developer.md",
		"README.md",
		"docs/developing-yoyo.md",
		"docs/products/notes.md",
	} {
		routed := Route(defaults(), Defect{Path: candidate, Detail: "something is wrong with it"})
		if protected := Protected(defaults(), candidate); protected == routed[0].Yours {
			t.Errorf("%s: Protected() = %t, and Route() reported it as the change's to fix = %t",
				candidate, protected, routed[0].Yours)
		}
	}
}

// Report is what every gate calls, and the whole of what it decides is which of
// two reporters a defect goes to. Both are exercised in one pass, because a
// version that sent everything to one of them would pass either half alone.
func TestReportSendsEachDefectToTheReporterItBelongsTo(t *testing.T) {
	t.Parallel()

	var failed, escalated []string
	Report(defaults(), []Defect{
		{Path: "docs/product/goals/v1-goals.md", Detail: "a goal is wrapped"},
		{Path: "README.md", Detail: "a link resolves to nothing"},
	},
		func(format string, args ...any) { failed = append(failed, fmt.Sprintf(format, args...)) },
		func(format string, args ...any) { escalated = append(escalated, fmt.Sprintf(format, args...)) },
	)
	if len(failed) != 1 || !strings.Contains(failed[0], "README.md") {
		t.Fatalf("failed = %v, want the one defect the change may fix", failed)
	}
	if len(escalated) != 1 || !strings.Contains(escalated[0], "docs/product/goals/v1-goals.md") {
		t.Fatalf("escalated = %v, want the one defect only the owner may fix", escalated)
	}
}

// Nothing is dropped. A defect the routing could make no sense of is still a
// defect somebody has to be told about, and silence here is the failure mode the
// whole arrangement is guarding against.
func TestEveryDefectComesBack(t *testing.T) {
	t.Parallel()

	defects := []Defect{
		{Path: "", Detail: "a reader reported no file at all"},
		{Path: "docs/product/brief.md", Detail: "it could not be read"},
		{Path: "internal/cli/cli.go", Detail: "not a document at all"},
	}
	routed := Route(defaults(), defects...)
	if len(routed) != len(defects) {
		t.Fatalf("routed %d of %d defects", len(routed), len(defects))
	}
	for index, entry := range routed {
		if entry.Detail != defects[index].Detail {
			t.Errorf("routed[%d] = %#v, want the defect it was given in the order it was given", index, entry)
		}
	}
}

// An index that is missing or has stopped answering is the defect of the home it
// stands at the door of, and of no other. Each one is reported against its own
// index file, so a designs or decisions index reaches the architect and the
// command that reaches the architect, rather than the specifications home's
// owner — which is what reporting them all against one home did.
func TestEachUnwrittenIndexIsRoutedToItsOwnHomesOwner(t *testing.T) {
	t.Parallel()

	cfg := defaults()
	var statuses []artifacthome.Status
	for _, home := range artifacthome.Homes(cfg) {
		state := artifacthome.StateMissing
		if home.Directory == "docs/product" {
			// A written index is not a defect, and is left out.
			state = artifacthome.StateWritten
		}
		statuses = append(statuses, artifacthome.Status{Home: home, Path: home.Path(), State: state})
	}
	routed := Route(cfg, IndexDefects(statuses)...)

	want := map[string]domain.AgentRole{
		"docs/product/goals":        domain.RoleProductManager,
		"docs/designs":              domain.RoleArchitect,
		"docs/decisions":            domain.RoleArchitect,
		"docs/decisions/invariants": domain.RoleArchitect,
	}
	if len(routed) != len(want) {
		t.Fatalf("routed = %#v, want one defect for each unwritten index", routed)
	}
	for _, entry := range routed {
		owner, found := want[entry.Home]
		if !found {
			t.Errorf("a defect was routed to %q, which has no unwritten index: %#v", entry.Home, entry)
			continue
		}
		if entry.Path != entry.Home+"/"+artifacthome.FileName {
			t.Errorf("path = %q, want the index of %s", entry.Path, entry.Home)
		}
		if entry.Yours || entry.Owner != owner {
			t.Errorf("%s was routed to %q (yours=%t), want %q", entry.Path, entry.Owner, entry.Yours, owner)
		}
		if command := artifacthome.ConversationCommand(owner); !strings.Contains(entry.Route, "`"+command+"`") {
			t.Errorf("the route for %s does not name %q:\n%s", entry.Path, command, entry.Route)
		}
	}
}
