package terms

import (
	"errors"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/terms/inventory"
)

// ErrNoRegister is what CheckDocument answers for a repository that keeps no
// register. A project that never adopted the register has no coined words for
// the check to hold its documents to, which is a different answer from a
// document with none in it.
var ErrNoRegister = errors.New("this repository keeps no register of coined words at " + RegisterPath)

// CheckDocument is the check run over one governed document before it is
// written, rather than over the repository after: the coined words the
// register does not define and the compounds nothing accounts for, read in the
// document's body exactly as Check and Compounds read it once it is filed at
// documentPath. Each problem's line is the line of content it is written on.
//
// It exists because a role writing a document from its conversation otherwise
// learns of a word the check refuses only when the run landing the document
// fails `make test`, long after the role could have reworded it. The register
// is read where it is now, so a replaced row naming documentPath excuses it here
// exactly as it does in the full check, and a term registered since is allowed.
// What it does not report is anything about the rest of the repository: the
// register's own shape and every other file are the full check's to judge, and
// a document is not refused for somebody else's.
func CheckDocument(root, documentPath, content string) ([]Problem, error) {
	entries, err := Register(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoRegister
	}
	if err != nil {
		return nil, err
	}
	_, registered := checkRegister(entries)
	replacements, err := Replaced(root)
	if err != nil {
		return nil, err
	}
	ordinary, err := Ordinary(root)
	if err != nil {
		return nil, err
	}
	names, err := documentNames(root)
	if err != nil {
		return nil, err
	}
	// The document's own name is one of them once it is filed, and a new
	// document is not filed yet.
	names[strings.ToLower(strings.TrimSuffix(path.Base(documentPath), path.Ext(documentPath)))] = true

	// The body is everything below the frontmatter the harness writes, so none of
	// it is skipped as frontmatter here.
	stretches := passagesFrom(strings.Split(content, "\n"), 1)
	patterns := make([]*regexp.Regexp, len(Vocabulary))
	for index, coinage := range Vocabulary {
		patterns[index] = pattern(coinage)
	}
	vocabulary := scan{
		path:       documentPath,
		patterns:   patterns,
		registered: registered,
		excused:    excusedDocuments(replacements)[documentPath],
		carried:    make(map[string]map[string]bool),
	}
	problems := vocabulary.problems(stretches)
	compounds := newCompoundScan(accountFor(entries, replacements, ordinary, names), inventory.Unread)
	return append(problems, compounds.problems(documentPath, stretches, true)...), nil
}
