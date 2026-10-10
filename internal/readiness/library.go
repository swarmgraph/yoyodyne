package readiness

import (
	"bufio"
	"bytes"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Library is what Go's standard library says of one cited name. A symbol the
// tree does not declare may still be a correct citation: `os.Hostname` is named
// by no file in a tree that never calls it, and is exactly where the item says it
// is. The symbol pinpoint this package reads is Go-shaped, so Go's is the
// standard library it is read against.
type Library struct {
	// Declared is the standard library having the name as it was cited: a
	// package-level function, type, constant, or variable, or a method or struct
	// field on a standard type.
	Declared bool
	// Packages are the standard packages whose name is the one the citation is
	// qualified by, by import path, so a refusal can say where it looked. It is
	// empty where no standard package has that name, which is the ordinary
	// repository symbol.
	Packages []string
	// Source is where the library was read from, so whoever is told can make the
	// same read.
	Source string
}

// apiFile is the release lists the Go distribution ships under GOROOT/api,
// one per release, each line one exported name that release added. They are
// the standard library's own record of what it exports, kept by the Go project
// for its API compatibility check, so reading them is reading the library rather
// than parsing its source.
var apiFile = regexp.MustCompile(`^go1(?:\.[0-9]+)?\.txt$`)

// majorVersion is the final element of an import path like math/rand/v2, which
// names a version of the package rather than the package.
var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// standardLibrary reads the API lists of one Go installation, once.
type standardLibrary struct {
	root string

	once     sync.Once
	names    map[string]struct{}
	packages map[string][]string
	loaded   error
}

// lookup is what the library says of one cited symbol.
func (l *standardLibrary) lookup(symbol string) (Library, error) {
	l.once.Do(l.load)
	source := filepath.Join(l.root, "api")
	if l.loaded != nil {
		return Library{Source: source}, l.loaded
	}
	_, declared := l.names[symbol]
	pkg, _, _ := strings.Cut(symbol, ".")
	return Library{Declared: declared, Packages: l.packages[pkg], Source: source}, nil
}

// load reads every release list. A failure is remembered as the failure it was
// rather than as an empty library, for the reason corpus gives: an empty library
// would answer every standard name as missing, which is the false refusal this
// exists to end.
func (l *standardLibrary) load() {
	root := strings.TrimSpace(l.root)
	if root == "" {
		l.loaded = fmt.Errorf("the Go standard library was not found: no Go installation is known to this process")
		return
	}
	directory := filepath.Join(root, "api")
	entries, err := os.ReadDir(directory)
	if err != nil {
		l.loaded = fmt.Errorf("read the Go standard library from %s: %w", directory, err)
		return
	}
	names := make(map[string]struct{})
	paths := make(map[string]map[string]struct{})
	read := 0
	for _, entry := range entries {
		if entry.IsDir() || !apiFile.MatchString(entry.Name()) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			l.loaded = fmt.Errorf("read the Go standard library from %s: %w", directory, err)
			return
		}
		read++
		lines := bufio.NewScanner(bytes.NewReader(content))
		lines.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for lines.Scan() {
			path, name, ok := apiName(lines.Text())
			if !ok {
				continue
			}
			names[name] = struct{}{}
			pkg, _, _ := strings.Cut(name, ".")
			if paths[pkg] == nil {
				paths[pkg] = make(map[string]struct{})
			}
			paths[pkg][path] = struct{}{}
		}
		if err := lines.Err(); err != nil {
			l.loaded = fmt.Errorf("read %s: %w", filepath.Join(directory, entry.Name()), err)
			return
		}
	}
	if read == 0 {
		l.loaded = fmt.Errorf("the Go standard library was not found: %s holds no release list", directory)
		return
	}
	l.names = names
	l.packages = make(map[string][]string, len(paths))
	for pkg, set := range paths {
		sorted := make([]string, 0, len(set))
		for path := range set {
			sorted = append(sorted, path)
		}
		sort.Strings(sorted)
		l.packages[pkg] = sorted
	}
}

// apiName is the import path one release-list line is about, and the name it
// declares in the form an item cites it: package name, then the type where there
// is one, then the member. The lines read
//
//	pkg os, func Hostname() (string, error)
//	pkg net/http, method (*Client) Do(*Request) (*Response, error)
//	pkg net/http, type Client struct, Jar CookieJar
//	pkg syscall (darwin-amd64), const ImplementsGetwd = false
//
// and give os.Hostname, http.Client.Do, http.Client.Jar, and
// syscall.ImplementsGetwd. Any line in another shape is not a name.
func apiName(line string) (string, string, bool) {
	rest, ok := strings.CutPrefix(line, "pkg ")
	if !ok {
		return "", "", false
	}
	header, declaration, ok := strings.Cut(rest, ", ")
	if !ok {
		return "", "", false
	}
	path, _, _ := strings.Cut(header, " ")
	pkg := packageName(path)
	kind, declaration, ok := strings.Cut(declaration, " ")
	if !ok {
		return "", "", false
	}
	switch kind {
	case "func", "const", "var":
		return path, pkg + "." + identifier(declaration), true
	case "method":
		receiver, member, ok := strings.Cut(declaration, ") ")
		if !ok {
			return "", "", false
		}
		receiver = strings.TrimLeft(strings.TrimPrefix(receiver, "("), "*")
		return path, pkg + "." + identifier(receiver) + "." + identifier(member), true
	case "type":
		named, member, hasMember := strings.Cut(declaration, ", ")
		name := pkg + "." + identifier(named)
		if hasMember {
			// A struct's field or an interface's method, written after the type.
			// An embedded field is written "embedded T" and is cited by its type,
			// which the type's own line already gives.
			if strings.HasPrefix(member, "embedded ") {
				return path, name, true
			}
			name += "." + identifier(member)
		}
		return path, name, true
	}
	return "", "", false
}

// packageName is the name a package is cited by: the last element of its import
// path, or the one before a major-version element.
func packageName(path string) string {
	elements := strings.Split(path, "/")
	last := elements[len(elements)-1]
	if majorVersion.MatchString(last) && len(elements) > 1 {
		return elements[len(elements)-2]
	}
	return last
}

// identifier is the name a declaration begins with, up to whatever follows it:
// a parameter list, a type parameter list, or the type.
func identifier(declaration string) string {
	if end := strings.IndexAny(declaration, " ([,"); end >= 0 {
		return declaration[:end]
	}
	return declaration
}

// goRoot is the Go installation the library is read from where the Repository
// names none: the one the environment names, or the one this binary was built
// with.
func goRoot() string {
	return build.Default.GOROOT
}
