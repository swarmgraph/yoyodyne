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

// A library that cannot be read is a reading that failed, said once however
// many symbols needed it, and the symbols the tree does not have are still held
// as they were before the library was consulted, saying it went unchecked.
func TestAStandardLibraryThatCannotBeReadStillHoldsAndSaysSo(t *testing.T) {
	repository := tree(t, map[string]string{"internal/launch/launch.go": "package launch\n"})
	repository.GoRoot = t.TempDir()

	unmet, err := Check(beads.WorkItem{ID: "item", Description: "os.Hostname and launch.Reconcile"}, repository)

	if err == nil {
		t.Fatal("want the unread library reported as a reading that failed")
	}
	if strings.Count(err.Error(), "read the standard library") != 1 {
		t.Errorf("want the failure said once, got %v", err)
	}
	if len(unmet) != 2 {
		t.Fatalf("want both undeclared symbols held, got %+v", unmet)
	}
	for _, one := range unmet {
		if !strings.Contains(one.Missing, "could not be checked") {
			t.Errorf("want the refusal to say the library went unchecked, got %q", one.Missing)
		}
	}
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
