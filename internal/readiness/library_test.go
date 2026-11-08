package readiness

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// standardAPI is the release list of the Go installation the tests read
// against, in the shape the Go distribution ships it.
const standardAPI = `pkg os, func Hostname() (string, error)
pkg os, const O_RDONLY int
pkg net/http, method (*Client) Do(*Request) (*Response, error)
pkg net/http, type Client struct
pkg net/http, type Client struct, Jar CookieJar
pkg net/http, type Handler interface, ServeHTTP(ResponseWriter, *Request)
pkg math/rand/v2, func IntN(int) int #61716
pkg crypto/rand, func Read([]uint8) (int, error)
pkg iter, type Seq[$0 interface{}] func(func($0) bool)
pkg syscall (darwin-amd64), const ImplementsGetwd = false
`

// goInstallation writes a Go installation holding only this release list.
func goInstallation(t *testing.T, api string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "api"), 0o755); err != nil {
		t.Fatalf("make the installation's api directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "api", "go1.txt"), []byte(api), 0o644); err != nil {
		t.Fatalf("write the release list: %v", err)
	}
	return root
}

// yoyodyne-ifd.435.14.8 was held for citing os.Hostname, which the tree did not
// call yet and Go's standard library has. A standard name is a correct citation
// whether or not the tree calls it, in each of the forms an item writes one.
func TestAStandardLibraryNameIsMet(t *testing.T) {
	repository := tree(t, map[string]string{"internal/launch/launch.go": "package launch\n\nfunc Recover() {}\n"})
	for _, cited := range []string{"os.Hostname", "os.O_RDONLY", "http.Client", "http.Client.Do", "http.Client.Jar",
		"http.Handler.ServeHTTP", "rand.IntN", "rand.Read", "iter.Seq", "syscall.ImplementsGetwd"} {
		item := beads.WorkItem{ID: "item", Description: "Launch recovery reads " + cited + " to tell the machines apart."}
		if unmet := check(t, item, repository); len(unmet) != 0 {
			t.Errorf("%s: want a standard name met, got %+v", cited, unmet)
		}
	}
}

// The three kinds of citation side by side: a standard name and a repository
// name the tree has are both met, and a repository name nobody has is held and
// said to be one.
func TestOnlyANameNeitherTheTreeNorTheStandardLibraryHasIsHeld(t *testing.T) {
	repository := tree(t, map[string]string{
		"internal/launch/launch.go": "package launch\n\nfunc Recover() {}\n",
		"internal/cli/start.go":     "package cli\n\nfunc start() { launch.Recover() }\n",
	})
	item := beads.WorkItem{
		ID:          "yoyodyne-ifd.435.14.8",
		Description: "launch.Recover compares os.Hostname with the recorded host, as launch.Reconcile did.",
	}

	unmet := check(t, item, repository)

	if len(unmet) != 1 || unmet[0].Kind != KindStalePinpoint {
		t.Fatalf("want one stale pinpoint, got %+v", unmet)
	}
	if !strings.Contains(unmet[0].Missing, "launch.Reconcile") || !strings.Contains(unmet[0].Missing, "a repository name") {
		t.Errorf("want the refusal to name launch.Reconcile as a repository name, got %q", unmet[0].Missing)
	}
	if !strings.Contains(unmet[0].Evidence, "no package called launch") {
		t.Errorf("want the evidence to say the standard library was read too, got %q", unmet[0].Evidence)
	}
}

// A name qualified by a standard package that the package does not have is
// held too, and said to be cited as a name in Go's standard library, with the
// packages read: somebody correcting it looks in the library rather than the
// tree.
func TestANameAStandardPackageDoesNotHaveIsHeldAsOne(t *testing.T) {
	repository := tree(t, map[string]string{"internal/launch/launch.go": "package launch\n"})

	unmet := check(t, beads.WorkItem{ID: "item", Description: "It reads rand.Hostname first."}, repository)

	if len(unmet) != 1 || unmet[0].Kind != KindStalePinpoint {
		t.Fatalf("want one stale pinpoint, got %+v", unmet)
	}
	if !strings.Contains(unmet[0].Missing, "as a name in Go's standard package") ||
		!strings.Contains(unmet[0].Missing, "crypto/rand, math/rand/v2") {
		t.Errorf("want the refusal to name the standard packages it looked in, got %q", unmet[0].Missing)
	}
}

// A library that cannot be read — a machine with no Go installed, or a binary
// built with -trimpath that does not know where Go is — is not a reading that
// failed, so a pull reports nothing as failing on its account. A citation only
// the library could answer is passed through and noted as not checked, and one
// qualified by a package of the tree is still held, because the tree alone says
// it is missing.
func TestAStandardLibraryThatCannotBeReadPassesStandardNamesThrough(t *testing.T) {
	for name, goRoot := range map[string]string{
		"an installation with no release lists": t.TempDir(),
		"a nonexistent installation":            filepath.Join(t.TempDir(), "missing"),
	} {
		repository := tree(t, map[string]string{"internal/launch/launch.go": "package launch\n"})
		repository.GoRoot = goRoot

		reading, err := Read(beads.WorkItem{ID: "item", Description: "os.Hostname, rand.Hostname and launch.Reconcile"}, repository)

		if err != nil {
			t.Fatalf("%s: want no failure for a standard library that cannot be read, got %v", name, err)
		}
		if len(reading.Unmet) != 1 || !strings.Contains(reading.Unmet[0].Missing, "launch.Reconcile") {
			t.Fatalf("%s: want only the tree's own missing name held, got %+v", name, reading.Unmet)
		}
		if len(reading.Unchecked) != 2 || reading.Unchecked[0].Cited != "os.Hostname" || reading.Unchecked[1].Cited != "rand.Hostname" {
			t.Fatalf("%s: want both standard names noted as not checked, got %+v", name, reading.Unchecked)
		}
		if !strings.Contains(reading.Unchecked[0].Why, "standard library could not be read") {
			t.Errorf("%s: want the note to say why it was not checked, got %q", name, reading.Unchecked[0].Why)
		}
	}
}

// A binary built with -trimpath, which is how every release is built, carries no
// Go installation path, so the library it is asked to read has no root. That is
// a library that cannot be read, said as one, and not an empty library that
// would answer every standard name as missing.
func TestAStandardLibraryWithNoKnownInstallationCannotBeRead(t *testing.T) {
	library, err := (&standardLibrary{}).lookup("os.Hostname")
	if err == nil || !strings.Contains(err.Error(), "no Go installation is known") {
		t.Fatalf("want a library with no root to be unreadable, got %+v, %v", library, err)
	}

	reading, err := Read(beads.WorkItem{ID: "item", Description: "Launch recovery reads os.Hostname."}, unreadLibrary{tree(t, map[string]string{"internal/launch/launch.go": "package launch\n"})})
	if err != nil || len(reading.Unmet) != 0 || len(reading.Unchecked) != 1 {
		t.Fatalf("want the standard name passed through unchecked and nothing failing, got %+v, %v", reading, err)
	}
}

// unreadLibrary is a checkout whose standard library is the one a -trimpath
// build is given: no installation at all.
type unreadLibrary struct{ *Repository }

func (unreadLibrary) Library(symbol string) (Library, error) {
	return (&standardLibrary{}).lookup(symbol)
}

// The release lists of the Go installation running this test, which is the
// installation a pull reads where none is named: the citation that was held is
// met against it.
func TestTheInstalledStandardLibraryHasHostname(t *testing.T) {
	if _, err := os.Stat(filepath.Join(build.Default.GOROOT, "api")); err != nil {
		t.Skipf("no Go release lists at %s: %v", build.Default.GOROOT, err)
	}
	repository := &Repository{Root: t.TempDir()}

	library, err := repository.Library("os.Hostname")
	if err != nil {
		t.Fatalf("read the installed standard library: %v", err)
	}
	if !library.Declared {
		t.Fatalf("want os.Hostname in the installed standard library, read from %s", library.Source)
	}
	if missing, _ := repository.Library("domain.Backend.SupportsRole"); missing.Declared || len(missing.Packages) != 0 {
		t.Errorf("want a repository name absent from the standard library, got %+v", missing)
	}
}
