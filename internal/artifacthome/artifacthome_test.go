package artifacthome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
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

// The whole point of the file is the three answers, so every home has to carry
// all three: a home whose index answered two of them would leave exactly the
// question the newcomer came with.
func TestEveryHomeStatesTheThreeThings(t *testing.T) {
	t.Parallel()

	homes := Homes(defaults())
	if len(homes) != 5 {
		t.Fatalf("homes = %d, want the product, goals, designs, decisions, and invariants homes", len(homes))
	}
	for _, home := range homes {
		rendered := string(home.README())
		if missing := unanswered(rendered); len(missing) > 0 {
			t.Errorf("%s does not state %s", home.Path(), strings.Join(missing, " or "))
		}
		if !strings.Contains(rendered, home.Owner.Title()) {
			t.Errorf("%s does not name its owner %q", home.Path(), home.Owner.Title())
		}
		if !strings.HasPrefix(rendered, "# "+home.Directory+"\n") {
			t.Errorf("%s does not open by naming the directory it indexes", home.Path())
		}
	}
}

// The hand-edit answer has a second half: whether editing one of these by hand
// means telling an agent. It is the half a reader acts on rather than merely
// notes, because a conversation already open is reasoning from these documents
// as they read when it opened, so an edit nobody mentioned is an agent
// confidently describing a file that no longer says that. Every home answers it,
// and answers it with the command that reaches its own owner.
func TestEveryHomeSaysHowToTellItsOwnerWhatChanged(t *testing.T) {
	t.Parallel()

	for _, home := range Homes(defaults()) {
		reach := ConversationCommand(home.Owner)
		if !strings.Contains(home.HandEdit, reach) {
			t.Errorf("%s says nothing about telling the %s a hand edit happened; it should name `%s`",
				home.Path(), home.Owner.Title(), reach)
		}
	}
	// And the two owners are reached differently, so a home that named the wrong
	// command would be caught rather than passing on the substring alone.
	if ConversationCommand(domain.RoleProductManager) == ConversationCommand(domain.RoleArchitect) {
		t.Fatal("both owners are reached by the same command, so the check above proves nothing")
	}
}

// The hand-edit answer has a third half, and it is the one an owner most needs:
// what notices that the edit left the document malformed. The answer used to be
// everybody's build, which is exactly what nobody could act on, so every home
// states the command that checks it and states that no other run goes red.
func TestEveryHomeSaysWhatChecksWhatWasEditedThere(t *testing.T) {
	t.Parallel()

	for _, home := range Homes(defaults()) {
		if !strings.Contains(home.HandEdit, "yoyo artifact check") {
			t.Errorf("%s does not name the command that checks an edit made here:\n%s", home.Path(), home.HandEdit)
		}
	}
}

// An index is exempt from artifact governance because it is an index. Writing
// one that carried identity frontmatter would claim an id in a home where every
// other file's id is checked, which is the one thing this file must never do.
func TestAWrittenIndexIsNotAnArtifact(t *testing.T) {
	t.Parallel()

	for _, home := range Homes(defaults()) {
		if strings.HasPrefix(string(home.README()), "---") {
			t.Errorf("%s opens with frontmatter, so it claims an identity an index must not have", home.Path())
		}
	}
}

func TestHomesFollowTheConfiguredDirectories(t *testing.T) {
	t.Parallel()

	moved := defaults()
	moved.Product.Specifications = "intent"
	moved.Product.Designs = "architecture/designs"
	moved.Product.Decisions = "architecture/decisions"
	moved.Product.Invariants = "architecture/invariants"

	var directories []string
	for _, home := range Homes(moved) {
		directories = append(directories, home.Directory)
	}
	want := []string{"intent", "intent/goals", "architecture/designs", "architecture/decisions", "architecture/invariants"}
	if strings.Join(directories, ",") != strings.Join(want, ",") {
		t.Fatalf("directories = %v, want %v", directories, want)
	}
}

// Two homes pointed at one directory is one index rather than two renderings of
// one file, and the first one wins so the answer does not depend on which of
// them was read last.
func TestOneDirectoryNamedTwiceGetsOneIndex(t *testing.T) {
	t.Parallel()

	shared := defaults()
	shared.Product.Decisions = "docs/designs"
	shared.Product.Invariants = "docs/designs"

	seen := map[string]int{}
	for _, home := range Homes(shared) {
		seen[home.Directory]++
	}
	if seen["docs/designs"] != 1 {
		t.Fatalf("docs/designs indexed %d times, want once", seen["docs/designs"])
	}
	for _, home := range Homes(shared) {
		if home.Directory == "docs/designs" && home.Owner != domain.RoleArchitect {
			t.Fatalf("owner = %q, want the architect", home.Owner)
		}
	}
}

// A directory the configuration does not name has no index rather than one at
// the repository root, which is where an empty path would otherwise put it.
func TestAnUnnamedHomeIsNotIndexed(t *testing.T) {
	t.Parallel()

	none := config.Config{}
	if homes := Homes(none); len(homes) != 0 {
		t.Fatalf("homes = %v, want none for a configuration that names no directory", homes)
	}
}

func TestInspectTellsMissingFromIncompleteFromWritten(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	root, err := repowrite.NewRoot(repository)
	if err != nil {
		t.Fatalf("NewRoot() error = %v", err)
	}
	cfg := defaults()

	// Nothing written yet: every home is missing, and a home whose directory
	// does not exist is still reported rather than skipped.
	for _, status := range Inspect(root, cfg) {
		if status.State != StateMissing {
			t.Fatalf("%s = %q, want missing", status.Path, status.State)
		}
	}

	homes := Homes(cfg)
	if _, err := Write(root, homes[0]); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	// An index somebody replaced with prose of their own that no longer answers
	// the questions is incomplete rather than missing: the two are not put right
	// the same way, because one of them has somebody's writing in it.
	partial := filepath.Join(repository, filepath.FromSlash(homes[1].Path()))
	if err := os.MkdirAll(filepath.Dir(partial), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(partial, []byte("# goals\n\n**Purpose.** The goals.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	states := map[string]State{}
	for _, status := range Inspect(root, cfg) {
		states[status.Path] = status.State
	}
	if states[homes[0].Path()] != StateWritten {
		t.Errorf("%s = %q, want written", homes[0].Path(), states[homes[0].Path()])
	}
	if states[homes[1].Path()] != StateIncomplete {
		t.Errorf("%s = %q, want incomplete", homes[1].Path(), states[homes[1].Path()])
	}
	if states[homes[2].Path()] != StateMissing {
		t.Errorf("%s = %q, want missing", homes[2].Path(), states[homes[2].Path()])
	}
}

// What a report leads with names the files rather than counting them, and it
// keeps the two states apart: replacing what somebody wrote and writing what
// nobody did are different things to ask for.
func TestDescribeNamesWhatIsMissingApartFromWhatIsIncomplete(t *testing.T) {
	t.Parallel()

	described := Describe([]Status{
		{Path: "docs/designs/README.md", State: StateIncomplete},
		{Path: "docs/product/README.md", State: StateMissing},
		{Path: "docs/decisions/README.md", State: StateWritten},
	})
	if !strings.Contains(described, "missing: docs/product/README.md") {
		t.Errorf("described = %q, want the missing index named", described)
	}
	if !strings.Contains(described, "incomplete: docs/designs/README.md") {
		t.Errorf("described = %q, want the incomplete index named apart from it", described)
	}
	if strings.Contains(described, "docs/decisions") {
		t.Errorf("described = %q, want a written index left out of it", described)
	}
	if Describe(nil) != "" {
		t.Errorf("Describe(nil) = %q, want nothing to report", Describe(nil))
	}
}

// A home reached through a symlink out of the repository is refused rather than
// written, which is the containment every repository-scoped write is held to
// rather than something this package decides for itself.
func TestAnIndexNeverLandsOutsideTheRepository(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, "docs"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repository, "docs", "designs")); err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}
	root, err := repowrite.NewRoot(repository)
	if err != nil {
		t.Fatalf("NewRoot() error = %v", err)
	}

	escaping := designsHome("docs/designs")
	if _, err := Write(root, escaping); err == nil {
		t.Fatal("Write() wrote an index through a symlink out of the repository")
	}
	if _, err := os.Stat(filepath.Join(outside, FileName)); err == nil {
		t.Fatal("an index landed outside the repository")
	}
	if status := inspect(root, escaping); status.State != StateUnreadable {
		t.Fatalf("state = %q, want unreadable rather than missing", status.State)
	}
}
