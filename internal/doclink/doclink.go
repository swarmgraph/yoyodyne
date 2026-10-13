// Package doclink is the links a repository's documentation makes to itself,
// resolved rather than believed.
//
// The artifact layer already reports a reference that names nothing: a
// `supports` entry pointing at an id no document answers to is a broken
// relationship, and it is reported beside the set rather than left to be read
// out of prose. That covers the links documents make in their frontmatter and
// nothing else. The links a reader actually follows are written in the prose —
// `[the product brief](../brief.md)`, `[design invariant
// 1](../../designs/v1-harness-design.md#design-invariants)` — and until now
// nothing resolved one.
//
// Nobody can. A relative path is checkable by eye only by holding the directory
// layout in your head, and a `#fragment` is worse: it names a heading in another
// file, through a slug nobody writes down, so a reviewer looking at a patch can
// see the link and cannot see the target. That is what the reviews of this
// repository kept saying — "the anchor slug could not be verified from the
// evidence available", "a grep for those two anchors across the repo would
// confirm it before merge" — and a check that a reviewer has to end by asking
// somebody else to run is attention spent on nothing.
//
// So both ends are resolved here. A relative target has to be a file that is
// there, and a fragment has to be a heading the target document carries. What is
// deliberately not resolved is anything outside the repository: an absolute URL
// is somebody else's to keep working, and reaching for one would make the check
// depend on the network.
//
// An absolute URL naming this repository's own forge home is not outside it. A
// document that writes `https://github.com/owner/repo#getting-started` names a
// heading in the README of this checkout, and that spelling is what a link has
// to use when it is read somewhere other than in a checkout:
// `.github/release-notes-preamble.md` is appended to every release's notes and
// rendered on the forge, where a relative path leads nowhere. It is the same
// argument the generated configuration below makes — published text this
// repository cannot correct afterwards — and until yoyodyne-ifd.315 nothing
// resolved it, so a README restructure could move that heading and break the
// call to action in every release already out with no check to say so. The home
// is derived from go.mod's module path, which is where this repository already
// says what it is called.
//
// Source files are read for the same fragments, because prose is not the only
// thing that cites a heading. `internal/config/scaffold.go` writes
// `docs/configuration.md#checks` into every `.yoyodyne/config.yaml` `yoyo init`
// has ever generated, and a workflow definition names an anchor in a comment.
// Those are the citations that cost most when they die — a generated
// configuration is on a disk this repository cannot rewrite — and they were the
// ones nothing here resolved, so a documentation split that moved a heading
// could pass every check and break them. yoyodyne-ifd.121 moved twenty-two
// README sections at once, which is the size of that exposure.
package doclink

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MaxFileBytes bounds one document read. It is generous — this repository's
// longest document is well over a hundred kilobytes — and it is here so that a
// walk that meets something enormous reports it rather than reading it into
// memory.
const MaxFileBytes = 4 << 20

// Problem is one link that resolves to nothing.
type Problem struct {
	// Path is the repository-relative document the link is written in and Line
	// the physical line it is written on, because what somebody has to open is the
	// document making the link rather than the one that is missing.
	Path string `json:"path"`
	Line int    `json:"line"`
	// Target is the link as the document writes it, so what is reported can be
	// found by searching for it.
	Target string `json:"target"`
	Reason string `json:"reason"`
}

func (p Problem) String() string {
	return fmt.Sprintf("%s:%d: %s", p.Path, p.Line, p.Reason)
}

// Check reports every link the repository's Markdown documents make to
// themselves that resolves to nothing, and every fragment its source files cite
// that names no heading. Problems are reported in the order the documents are
// walked and the order the links are written, and the source files after them,
// so two runs over one checkout report the same thing in the same order.
//
// A document that cannot be read fails the whole check rather than being
// reported as a document with no links: a walk that quietly skipped what it
// could not open would report a repository with fewer links instead of a named
// problem, which is the failure this exists to prevent arrived at from the other
// side. A source file is treated the other way and skipped when it cannot be
// read, because it is read opportunistically for something it may not contain
// at all, and failing the documentation check over a file nobody was documenting
// would be the check reaching past what it is for.
func Check(root string) ([]Problem, error) {
	documents, err := Documents(root)
	if err != nil {
		return nil, err
	}
	contents := make(map[string]string, len(documents))
	headings := make(map[string]map[string]bool, len(documents))
	for _, document := range documents {
		content, err := read(filepath.Join(root, filepath.FromSlash(document)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", document, err)
		}
		contents[document] = content
		headings[document] = anchorsIn(content)
	}
	home := forgeHome(root)
	var problems []Problem
	for _, document := range documents {
		problems = append(problems, problemsIn(root, home, document, contents[document], headings)...)
	}
	sources, err := Sources(root)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		content, err := read(filepath.Join(root, filepath.FromSlash(source)))
		if err != nil {
			continue
		}
		problems = append(problems, citationsIn(source, content, headings)...)
	}
	return problems, nil
}

// CheckDocument reports the links one document makes that would resolve to
// nothing once it is filed at document, before it is: what a role writes from
// its conversation is checked here when it is written, rather than by the run
// that lands it. The repository's documents supply the headings a fragment may
// name, with content standing in for whatever document holds now, and each
// problem's line is the line of content the link is written on.
func CheckDocument(root, document, content string) ([]Problem, error) {
	documents, err := Documents(root)
	if err != nil {
		return nil, err
	}
	headings := make(map[string]map[string]bool, len(documents)+1)
	for _, existing := range documents {
		if existing == document {
			continue
		}
		read, err := read(filepath.Join(root, filepath.FromSlash(existing)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", existing, err)
		}
		headings[existing] = anchorsIn(read)
	}
	headings[document] = anchorsIn(content)
	return problemsIn(root, forgeHome(root), document, content, headings), nil
}

// skippedDirectories are the trees a documentation check has no business
// walking: the two databases a checkout carries, the fixtures tests write to be
// malformed on purpose, and the release output. Everything else under the root
// is documentation somebody will read, including the agent instructions and the
// personas, which link into the same documents the guides do.
//
// The stores are named because they are not in the checkout — they are written
// beside it and ignored — and a check that reads whatever a tool happened to
// leave in one is a check that fails on one machine and passes on the next.
var skippedDirectories = map[string]bool{".git": true, ".dolt": true, "testdata": true, "dist": true}

// Documents is every Markdown file under the root, repository-relative and in
// sorted order. It is exported because what was checked is part of what a check
// reports: a walk that found nothing and a repository with no documentation are
// the same result otherwise.
func Documents(root string) ([]string, error) {
	return walkFor(root, "documentation", func(name string) bool {
		return strings.EqualFold(filepath.Ext(name), ".md")
	})
}

// sourceExtensions are the kinds of file that are read for citations of this
// repository's documentation. It is an allow-list rather than "everything that
// is not Markdown" for two reasons: a binary read as text produces matches
// nobody wrote, and the tracker's JSON export carries the anchors of work items
// this repository must not rewrite, so reporting one would be a check nobody can
// clear. These four are the kinds that carry a citation here — Go source, the
// workflow and CI definitions, and the shell beside them. A repository that
// grows a fifth adds it here, which is the same moment internal/composition
// makes it write down what exercises the new class.
var sourceExtensions = map[string]bool{".go": true, ".yaml": true, ".yml": true, ".sh": true}

// Sources is every file under the root whose kind can carry a citation of this
// repository's documentation, repository-relative and in sorted order. It is
// exported beside Documents and for the same reason: what was read is part of
// what the check reports.
func Sources(root string) ([]string, error) {
	return walkFor(root, "citations", func(name string) bool {
		return sourceExtensions[strings.ToLower(filepath.Ext(name))]
	})
}

// walkFor is every file under the root the predicate accepts, skipping the trees
// nothing is checked in.
func walkFor(root, what string, wanted func(name string) bool) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if current != root && skippedDirectories[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !wanted(entry.Name()) {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s for %s: %w", root, what, err)
	}
	sort.Strings(found)
	return found, nil
}

// linkPattern matches a Markdown inline link and captures its target: the text
// in brackets, which may itself contain one level of brackets, then the target
// in parentheses, optionally angle-bracketed and optionally followed by a
// quoted title. An image is matched too — the `!` is simply the character
// before the bracket — because a picture that is not there is as broken as a
// document that is not.
var linkPattern = regexp.MustCompile(`\[(?:[^\[\]]|\[[^\[\]]*\])*\]\(\s*(<[^>]*>|[^()\s]*)(?:\s+"[^"]*")?\s*\)`)

// codeSpanPattern matches a Markdown inline code span. A link inside one is an
// example of a link rather than a link, so the spans are removed before the
// pattern above is run and a document teaching the syntax is not reported for
// what it teaches. Only a span that opens and closes on one line is removed:
// what is left of an unbalanced backtick is prose, and prose is what the rest of
// the line already is.
var codeSpanPattern = regexp.MustCompile("`[^`]*`")

// headingPattern matches an ATX Markdown heading and captures its text, which is
// what a fragment names through its slug.
var headingPattern = regexp.MustCompile(`^#{1,6}\s+(.*)$`)

// fencePattern matches the line that opens or closes a fenced code block.
var fencePattern = regexp.MustCompile("^(?:```|~~~)")

// schemePattern matches a target that names something outside the repository:
// an absolute URL, a `mailto:`, anything with a scheme. None of it is this
// check's to resolve.
var schemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// problemsIn reports the links one document makes that resolve to nothing.
func problemsIn(root, home, document, content string, headings map[string]map[string]bool) []Problem {
	var problems []Problem
	directory := path.Dir(document)
	fenced := false
	for index, raw := range strings.Split(content, "\n") {
		if fencePattern.MatchString(strings.TrimSpace(raw)) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, match := range linkPattern.FindAllStringSubmatch(codeSpanPattern.ReplaceAllString(raw, ""), -1) {
			written := strings.TrimSpace(match[1])
			written = strings.TrimSuffix(strings.TrimPrefix(written, "<"), ">")
			if written == "" {
				continue
			}
			target := written
			if schemePattern.MatchString(written) || strings.HasPrefix(written, "//") {
				// A URL naming this repository is a link into this checkout however it
				// is spelled; anything else absolute is somebody else's to keep working.
				if target = repositoryTarget(home, written); target == "" {
					continue
				}
			}
			if reason := resolve(root, document, directory, target, written, headings); reason != "" {
				problems = append(problems, Problem{Path: document, Line: index + 1, Target: written, Reason: reason})
			}
		}
	}
	return problems
}

// modulePattern matches the module path go.mod declares, which is where this
// repository says what it is called.
var modulePattern = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// forgeHome is the URL this repository is read at on its forge —
// `github.com/mason-bryant/yoyodyne` in go.mod is
// `https://github.com/mason-bryant/yoyodyne` — and the empty string for a root
// that does not say. It is derived rather than configured because the module
// path is already this repository's own name for itself, committed here, and a
// second place to write it down is a second place for it to go stale.
//
// A root carrying no go.mod, or one whose module path is not a forge host and an
// owner and a repository, has no home, and every absolute URL under it stays
// somebody else's — which is the behaviour this package had before any of them
// were resolved.
func forgeHome(root string) string {
	content, err := read(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	module := modulePattern.FindStringSubmatch(content)
	if module == nil {
		return ""
	}
	segments := strings.Split(module[1], "/")
	if len(segments) != 3 || !strings.Contains(segments[0], ".") {
		return ""
	}
	return "https://" + module[1]
}

// blobViewPattern matches what a forge puts in front of a repository-relative
// path in its blob view, once the home has been taken off.
var blobViewPattern = regexp.MustCompile(`^/blob/[^/]+/`)

// repositoryTarget is the repository-relative target an absolute URL names when
// it names this repository, and the empty string for a URL that names anything
// else. Two spellings resolve, and they are the two this repository's own
// documents and generated files write: a file in the forge's blob view, and the
// repository root carrying a fragment, which is a link into the README because
// the root is where the forge renders it.
//
// A root URL with no fragment names the repository rather than a heading in it,
// so there is nothing here to resolve and it is left alone. So is everything
// else under the home — a release, a pull request, an issue — which is the
// forge's own furniture rather than a document this checkout has.
func repositoryTarget(home, written string) string {
	if home == "" || !strings.HasPrefix(written, home) {
		return ""
	}
	rest, fragment, carried := strings.Cut(strings.TrimPrefix(written, home), "#")
	if carried {
		fragment = "#" + fragment
	}
	switch {
	case rest == "" || rest == "/":
		if !carried {
			return ""
		}
		return "/README.md" + fragment
	case blobViewPattern.MatchString(rest):
		return "/" + blobViewPattern.ReplaceAllString(rest, "") + fragment
	}
	return ""
}

// resolve says why one target names nothing, and returns nothing for one that
// resolves. The two halves are separate questions: whether the file is there,
// and — for a Markdown file, which is the only kind whose headings are known —
// whether it carries the heading the fragment names.
//
// The target is resolved and the written form is reported: a link spelled as a
// URL into this repository resolves as the path it names, and what a reader has
// to search the document for is what the document says.
func resolve(root, document, directory, target, written string, headings map[string]map[string]bool) string {
	reference, fragment, _ := strings.Cut(target, "#")
	// A path is written as a URL, so what it says and what the filesystem holds
	// differ wherever a character had to be escaped. A target that is not valid
	// escaping is left as written rather than refused, because the filesystem is
	// what decides and a name containing a stray percent is a name.
	if unescaped, err := url.PathUnescape(reference); err == nil {
		reference = unescaped
	}
	if unescaped, err := url.PathUnescape(fragment); err == nil {
		fragment = unescaped
	}

	// An empty path is a fragment naming a heading in the document making the
	// link, which is how a long guide points at its own sections.
	linked := document
	if reference != "" {
		// A target that opens with a separator is written from the repository root,
		// which is how the forge reads one; anything else is written from the
		// document making the link.
		if strings.HasPrefix(reference, "/") {
			linked = path.Clean(strings.TrimPrefix(reference, "/"))
		} else {
			linked = path.Clean(path.Join(directory, reference))
		}
		if linked == ".." || strings.HasPrefix(linked, "../") {
			return fmt.Sprintf("it links to %q, which leaves the repository, so nothing here can say whether it is there", written)
		}
		// A document naming itself by its path is there however the filesystem
		// answers: one being checked before it is written is not on disk yet.
		if linked == document {
			return resolveFragment(linked, fragment, written, headings)
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(linked)))
		if err != nil {
			return fmt.Sprintf("it links to %q, and %s is not in the repository", written, linked)
		}
		if info.IsDir() {
			// A link to a directory is a link to a place rather than to a heading, so
			// a fragment on one is not something to resolve.
			return ""
		}
	}
	return resolveFragment(linked, fragment, written, headings)
}

// resolveFragment says why a fragment names no heading in the document linked,
// and returns nothing for one that does or for a link carrying none.
func resolveFragment(linked, fragment, written string, headings map[string]map[string]bool) string {
	if fragment == "" {
		return ""
	}
	anchors, known := headings[linked]
	if !known {
		// Only a Markdown document's headings were collected. A fragment on
		// anything else is an anchor in something this does not read, and guessing
		// would be worse than saying nothing.
		return ""
	}
	if !anchors[slug(fragment)] {
		return fmt.Sprintf("it links to %q, and %s carries no heading with that anchor", written, linked)
	}
	return ""
}

// citationPattern matches a Markdown path carrying a fragment, which is how a
// source file names a heading: `docs/configuration.md#checks` bare in a comment
// or a string, the same path spelled from the citing file's own directory, or
// either inside the forge URL for this repository's blob view. It is
// deliberately narrower than the Markdown link pattern above — there is no link
// syntax to key on in Go or YAML, so the path and the `#` are the whole of the
// evidence that a citation is what this is.
var citationPattern = regexp.MustCompile(`[A-Za-z0-9_.\-/]+\.md#[A-Za-z0-9_\-]+`)

// blobPrefixPattern matches what a forge URL puts in front of a
// repository-relative path in its blob view, so the URL form of a citation
// resolves to the same document the bare form does.
var blobPrefixPattern = regexp.MustCompile(`^.*/blob/[^/]+/`)

// citationsIn reports the fragments one source file cites that name no heading.
//
// A citation whose path is not a document this repository has is passed over
// rather than reported. A source file names paths for every reason there is —
// a fixture written to be broken on purpose, a document in somebody else's
// repository, a string assembled from parts — and this is read opportunistically
// rather than because anything declared it a link. What it exists to catch is
// the other case: a path that is here and a fragment that is not, which is a
// heading somebody moved and a citation nobody could see go dead.
func citationsIn(source, content string, headings map[string]map[string]bool) []Problem {
	var problems []Problem
	directory := path.Dir(source)
	for index, raw := range strings.Split(content, "\n") {
		for _, citation := range citationPattern.FindAllString(raw, -1) {
			reference, fragment, _ := strings.Cut(citation, "#")
			document, anchors := citedDocument(directory, reference, headings)
			if document == "" || anchors[slug(fragment)] {
				continue
			}
			problems = append(problems, Problem{
				Path:   source,
				Line:   index + 1,
				Target: citation,
				Reason: fmt.Sprintf("it cites %q, and %s carries no heading with that anchor", citation, document),
			})
		}
	}
	return problems
}

// citedDocument is the document a citation names and the fragments it answers
// to, resolved in the three spellings a citation is written in: the path from
// the repository root, that path inside a forge blob URL, and the path from the
// directory the citing file sits in — `../README.md#further-reading` from a
// script in scripts/ names a document this repository has, and resolving only
// the root form would pass it over as if it named somebody else's. A reference
// naming none of the three returns the empty path, which is how a citation this
// repository has no document for is passed over.
//
// The root form is tried first, because that is the form the citations here are
// written in and it is the one a generated file carries. Where a repository has
// both a root `README.md` and one in the citing file's own directory, a bare
// `README.md#…` therefore resolves to the root one. That is a guess rather than
// a reading, and it is the right way round: passing over a citation that names a
// document twice over would lose the check for the sake of an ambiguity nothing
// in the citation settles.
func citedDocument(directory, reference string, headings map[string]map[string]bool) (string, map[string]bool) {
	for _, candidate := range []string{
		reference,
		blobPrefixPattern.ReplaceAllString(reference, ""),
		path.Join(directory, reference),
	} {
		candidate = path.Clean(candidate)
		if anchors, known := headings[candidate]; known {
			return candidate, anchors
		}
	}
	return "", nil
}

// anchorsIn is every fragment one document answers to: the slug of each of its
// headings, with the suffix a repeated heading gets so that the second `## Why`
// in a document is reachable as `why-1`. That numbering is the forge's, and it
// is followed rather than invented because the links being checked are the ones
// a reader clicks on the forge.
func anchorsIn(content string) map[string]bool {
	anchors := map[string]bool{}
	seen := map[string]int{}
	fenced := false
	for _, raw := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(raw)
		if fencePattern.MatchString(trimmed) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		heading := headingPattern.FindStringSubmatch(trimmed)
		if heading == nil {
			continue
		}
		anchor := slug(heading[1])
		if anchor == "" {
			continue
		}
		if repeated := seen[anchor]; repeated > 0 {
			anchors[fmt.Sprintf("%s-%d", anchor, repeated)] = true
		} else {
			anchors[anchor] = true
		}
		seen[anchor]++
	}
	return anchors
}

// closingHashesPattern matches the optional trailing hashes of a closed ATX
// heading, which are punctuation rather than part of what the heading says.
var closingHashesPattern = regexp.MustCompile(`\s+#+$`)

// inlineLinkTextPattern matches a link inside a heading, capturing the text: a
// heading that links somewhere is slugged from what it reads as, not from where
// it points.
var inlineLinkTextPattern = regexp.MustCompile(`\[([^\[\]]*)\]\([^()]*\)`)

// emphasisPattern matches the markers a heading's words may be wrapped in.
// Underscores are deliberately not among them: the forge keeps an underscore in
// a slug, so stripping one would make `check_timeout` unreachable as itself.
var emphasisPattern = regexp.MustCompile(`\*+`)

// droppedPattern matches what a slug does not keep. Letters, digits, marks,
// underscores, hyphens, and spaces survive; everything else is dropped rather
// than replaced, which is why `what fails, closed` and `what fails closed` are
// the same anchor.
var droppedPattern = regexp.MustCompile(`[^\p{L}\p{N}\p{M}_\- ]`)

// slug is the anchor a heading answers to, derived the way the forge derives it:
// the heading's text with its markup taken off, lowercased, punctuation dropped,
// and spaces turned into hyphens.
func slug(heading string) string {
	text := closingHashesPattern.ReplaceAllString(strings.TrimSpace(heading), "")
	text = inlineLinkTextPattern.ReplaceAllString(text, "$1")
	text = strings.ReplaceAll(text, "`", "")
	text = emphasisPattern.ReplaceAllString(text, "")
	text = droppedPattern.ReplaceAllString(strings.ToLower(text), "")
	return strings.ReplaceAll(strings.TrimSpace(text), " ", "-")
}

// read reads one document, bounded, so that a file too large to be
// documentation is reported rather than loaded.
func read(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("it is not a regular file")
	}
	if info.Size() > MaxFileBytes {
		return "", fmt.Errorf("it is %d bytes, limit is %d", info.Size(), MaxFileBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
