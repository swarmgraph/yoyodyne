package readiness

import (
	"bytes"
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// sourceExtension is what the symbol read looks in, and testSuffix is what it
// leaves out of that. Both exclusions are the same one: a symbol that is gone
// from the code is regularly still named by the text that records its going, so
// a read taking those in would report a deleted symbol as present by reading its
// own obituary. The diagnosis of yoyodyne-ifd.291 names
// domain.Backend.SupportsRole four times to say it no longer exists, and this
// package's own tests name it to prove the check catches it.
const (
	sourceExtension = ".go"
	testSuffix      = "_test.go"
)

// skippedDirectories are the directories the symbol read does not walk: the
// git store, the tracker's own database, and the build outputs. None of them is
// source, and the first two are large enough that walking them is the whole cost
// of the read.
var skippedDirectories = map[string]struct{}{
	".git": {}, ".beads": {}, "bin": {}, "dist": {}, "node_modules": {},
}

// Repository is the tree as it stands in one checkout, and is the Tree a pull
// reads through.
//
// It caches the source it reads, and is therefore a pointer and one per pull.
// That is the whole of the cost control here: the corpus is read at most once
// per pull, and only where an item actually pinpoints a symbol. A pull whose
// queue pinpoints nothing never opens a source file at all, which is most pulls.
type Repository struct {
	// Root is the checkout the reads are made against. Reads outside it are
	// refused rather than followed, so a citation that walks out of the tree is
	// absent rather than a way to read the machine.
	Root string
	// GoRoot is the Go installation whose standard library a symbol the tree
	// does not declare is read against. Empty is the one the environment names,
	// or the one this binary was built with.
	GoRoot string

	once    sync.Once
	source  []byte
	loaded  error
	library sync.Once
	std     *standardLibrary
}

// Library reports what Go's standard library says of a symbol. It is read only
// for a symbol the tree does not declare, and at most once per pull, so a pull
// whose citations are all repository symbols never opens it.
func (r *Repository) Library(symbol string) (Library, error) {
	r.library.Do(func() {
		root := strings.TrimSpace(r.GoRoot)
		if root == "" {
			root = goRoot()
		}
		r.std = &standardLibrary{root: root}
	})
	return r.std.lookup(symbol)
}

// File is how many lines the file at this repository-relative path has. A path
// that is not under the root — absolute, or climbing out with `..` — is absent
// rather than an error: an item may cite anything at all, and a citation nobody
// can resolve inside the tree is a citation the tree does not hold.
func (r *Repository) File(path string) (int, bool, error) {
	resolved, ok := r.resolve(path)
	if !ok {
		return 0, false, nil
	}
	content, err := os.ReadFile(resolved)
	switch {
	case os.IsNotExist(err):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("read %s: %w", path, err)
	}
	return lines(content), true, nil
}

// lines is how many lines a file has, counting a last line nobody terminated.
func lines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	counted := bytes.Count(content, []byte{'\n'})
	if content[len(content)-1] != '\n' {
		counted++
	}
	return counted
}

// Declares reports the tree's own source naming this symbol. Four forms count,
// and the last three are what keep this from firing on every method a document
// refers to by its package-qualified name.
//
//   - The symbol as the item wrote it. A package-qualified function is written
//     that way wherever it is called, so its absence is the function's absence.
//   - Its type and member. A method is never written package-qualified in Go —
//     it is called on a value — so the qualified form an item uses in prose is
//     absent from the source of every tree, including the ones that have the
//     method.
//   - A method of that name declared on that type.
//   - A field of that name inside a struct of that name.
//
// The last two are why the receiver and the member are asked about together
// rather than one at a time, and that is the case this exists for: a tree can
// hold an unrelated type of the same name and an unrelated method of the same
// name and still not have the method on the type, which is precisely what
// yoyodyne-ifd.291's citation had become.
//
// Comments are not part of the answer; see corpus for why.
func (r *Repository) Declares(symbol string) (bool, error) {
	source, err := r.corpus()
	if err != nil {
		return false, err
	}
	if names(source, symbol) {
		return true, nil
	}
	parts := strings.Split(symbol, ".")
	if len(parts) < 3 {
		return false, nil
	}
	if names(source, strings.Join(parts[1:], ".")) {
		return true, nil
	}
	receiver, member := parts[len(parts)-2], parts[len(parts)-1]
	method := regexp.MustCompile(`func \([A-Za-z_][A-Za-z0-9_]* \*?` + regexp.QuoteMeta(receiver) + `\) ` + regexp.QuoteMeta(member) + `\b`)
	return method.Match(source) || declaresField(source, receiver, member), nil
}

// declaresField reports a struct of this name having a member of this name. The
// search is scoped to the struct's own declaration rather than to the file it is
// in: a member name is an ordinary word, and asking whether the tree contains it
// anywhere is asking nothing at all.
//
// The block is taken as far as the closing brace in the first column, which is
// where gofmt puts it and therefore where it is in every file this reads.
func declaresField(source []byte, receiver, member string) bool {
	declaration := regexp.MustCompile(`type ` + regexp.QuoteMeta(receiver) + ` struct \{`)
	field := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(member) + `\b`)
	for _, at := range declaration.FindAllIndex(source, -1) {
		block := source[at[1]:]
		if end := bytes.Index(block, []byte("\n}")); end >= 0 {
			block = block[:end]
		}
		if field.Match(block) {
			return true
		}
	}
	return false
}

// names reports the source naming this symbol as a symbol rather than as the
// beginning of a longer one. The boundary is what keeps `directive.Resolve` from
// being answered by a tree that only has `directive.Resolved` — which is a rename
// the item's citation did not survive, and exactly the thing this is asked about.
func names(source []byte, symbol string) bool {
	wanted := []byte(symbol)
	for at := 0; at+len(wanted) <= len(source); {
		found := bytes.Index(source[at:], wanted)
		if found < 0 {
			return false
		}
		start := at + found
		if !continues(source, start-1) && !continues(source, start+len(wanted)) {
			return true
		}
		at = start + 1
	}
	return false
}

// continues reports the byte at this position being one a symbol could carry, so
// a match with one on either side is part of something longer.
func continues(source []byte, at int) bool {
	if at < 0 || at >= len(source) {
		return false
	}
	character := source[at]
	return character == '.' || character == '_' ||
		(character >= '0' && character <= '9') ||
		(character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z')
}

// corpus is the tree's Go source with its comments blanked out, read once. A
// read that failed is remembered as the failure it was rather than as an empty
// tree: an empty corpus would report every symbol as deleted, which is the one
// wrong answer this must never give.
//
// The comments come out because a comment is where a symbol is named after it is
// gone. This package's own prose names the symbol yoyodyne-ifd.291 cited, to say
// what the incident was; leaving comments in would have that sentence answer the
// question the sentence is about. Blanking rather than deleting keeps every
// remaining byte at the offset it had, so nothing else here has to know this
// happened.
func (r *Repository) corpus() ([]byte, error) {
	r.once.Do(func() {
		root := strings.TrimSpace(r.Root)
		if root == "" {
			r.loaded = fmt.Errorf("reading the tree for a symbol requires the repository it is in")
			return
		}
		var assembled bytes.Buffer
		r.loaded = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if _, skipped := skippedDirectories[entry.Name()]; skipped {
					return fs.SkipDir
				}
				return nil
			}
			if filepath.Ext(entry.Name()) != sourceExtension || strings.HasSuffix(entry.Name(), testSuffix) {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			assembled.Write(uncommented(content))
			assembled.WriteByte('\n')
			return nil
		})
		if r.loaded == nil {
			r.source = assembled.Bytes()
		}
	})
	return r.source, r.loaded
}

// uncommented is one file's source with every comment blanked to spaces. It is
// the standard scanner rather than a search for slashes, because a slash pair
// inside a string literal is not a comment and a scanner is the thing that knows
// the difference. A file that will not scan keeps whatever was read of it: the
// tokens before the error are still the source, and a file this cannot read is
// not a reason to report every symbol in the tree as deleted.
func uncommented(content []byte) []byte {
	stripped := make([]byte, len(content))
	copy(stripped, content)
	files := token.NewFileSet()
	file := files.AddFile("", files.Base(), len(content))
	var source scanner.Scanner
	source.Init(file, content, func(token.Position, string) {}, scanner.ScanComments)
	for {
		position, found, literal := source.Scan()
		if found == token.EOF {
			return stripped
		}
		if found != token.COMMENT {
			continue
		}
		at := files.Position(position).Offset
		for index := at; index < at+len(literal) && index < len(stripped); index++ {
			if stripped[index] != '\n' {
				stripped[index] = ' '
			}
		}
	}
}

// resolve is where a repository-relative citation lands, and false for one that
// does not land inside the tree at all.
func (r *Repository) resolve(path string) (string, bool) {
	root := strings.TrimSpace(r.Root)
	cited := strings.TrimSpace(path)
	if root == "" || cited == "" || filepath.IsAbs(cited) {
		return "", false
	}
	cleaned := filepath.Clean(cited)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(root, cleaned), true
}
