package contextbundle

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// The product's specification home, delivered into a work item's context.
//
// Until the operator's direction of 2026-09-27 — "everything in docs/product is
// authoritative" — the documents there reached the management conversations and
// nobody else: a developer and a reviewer read the work item and what its prose
// named, so the brief, the goals, and the non-goals the item's own goal line
// traced to were authoritative to every role except the two that build and judge
// the change. A document the operator calls authoritative that a role never reads
// is authoritative to nobody in that role, so the whole home is carried here, in
// the same words and under the same labels the conversations read it under.

// maxIntentShareDivisor bounds what the specification home may take of a work
// item's context: at most this fraction of the budget. The work item is what the
// run is for and is rendered first, whole; the home is read next and before the
// references the item names, so intent wins the budget over what the item merely
// cites. The share keeps a home grown far past today's size — this repository's
// is about 25 KiB of a 256 KiB budget — from leaving the item's own references no
// room at all; what does not fit is named rather than silently missing.
const maxIntentShareDivisor = 2

// AssembleIntent supplies the product home to a review with no single work
// item. It uses the same revision reader and budget as work-item intent, so a
// branch review receives the standing set without inventing an attribution.
func AssembleIntent(repositoryRoot, directory string, revision *Revision) (string, error) {
	text, _, err := renderWorkItemIntent(repositoryRoot, referenceSource{root: repositoryRoot, revision: revision}, directory, defaultMaxBytes/maxIntentShareDivisor)
	return text, err
}

// renderWorkItemIntent reads every document under the specifications directory
// into the section a developer and a reviewer read product intent from, within
// the budget it is given. Documents are read from the working tree, or, where a
// revision is set, as that commit holds them. The revision listing discovers
// documents even when the working tree has deleted or renamed them.
//
// Nothing here fails the bundle for a document: one that cannot be read or does
// not fit is named as not carried, because a run refused over a document in a
// directory the item never named is a run nobody can unwedge by editing the item.
// A directory that is refused outright — one resolving outside the repository —
// does fail, exactly as it fails the conversations' reading of it.
func renderWorkItemIntent(repositoryRoot string, source referenceSource, directory string, budget int) (string, []Reference, error) {
	if strings.TrimSpace(directory) == "" {
		return "", nil, nil
	}
	clean, err := validateSpecificationsDirectory("specifications", directory)
	if err != nil {
		return "", nil, err
	}
	root, err := repowrite.NewRoot(repositoryRoot)
	if err != nil {
		return "", nil, err
	}
	paths, err := specificationPaths(root, source.revision, clean)
	if err != nil {
		return "", nil, err
	}

	header := renderWorkItemIntentHeader(clean, source.revision)
	if source.revision != nil {
		current, err := discoverSpecifications("specifications", root, clean)
		if err != nil {
			return "", nil, err
		}
		header += renderIntentPathChanges(paths, current)
	}
	if len(header)+longestIntentOmission(paths) > budget {
		return intentFallback(clean), nil, nil
	}
	if len(paths) == 0 {
		section := header + fmt.Sprintf("Nothing is filed under %s, so this product records no intent in writing. Say so\nrather than inferring what it must be.\n", clean)
		return section, nil, nil
	}
	var rendered strings.Builder
	var references []Reference
	var omitted []string
	// What the statement of omission can cost is charged before any document, so
	// the document that fills the budget is never the one whose absence there was
	// no room left to state.
	spent := len(header) + longestIntentOmission(paths)
	for _, documentPath := range paths {
		reference, err := readIntentDocument(root, source, documentPath, budget-spent)
		if err != nil {
			if errors.Is(err, ErrNotAtRevision) {
				continue
			}
			omitted = append(omitted, documentPath)
			continue
		}
		section := renderIntentDocument(reference)
		if spent+len(section) > budget {
			omitted = append(omitted, documentPath)
			continue
		}
		rendered.WriteString(section)
		spent += len(section)
		references = append(references, reference)
	}
	section := header + rendered.String() + renderIntentOmission(omitted)
	if len(section) > budget {
		// Not even the statement of what was left out fits, which only a budget
		// smaller than any real one produces. The one sentence below is still said,
		// because a context silent about intent reads as a product that has none.
		return intentFallback(clean), nil, nil
	}
	return section, references, nil
}

func specificationPaths(root repowrite.Root, revision *Revision, directory string) ([]string, error) {
	if revision == nil {
		return discoverSpecifications("specifications", root, directory)
	}
	if revision.ListFiles == nil {
		return nil, fmt.Errorf("discover product documents at %s: revision file listing is required", revision.Name)
	}
	files, err := revision.ListFiles()
	if err != nil {
		return nil, fmt.Errorf("discover product documents at %s: %w", revision.Name, err)
	}
	var documents []string
	for _, file := range files {
		if path.Clean(file) != file || path.IsAbs(file) || file == ".." || strings.HasPrefix(file, "../") || strings.ContainsAny(file, "\\\x00") {
			return nil, fmt.Errorf("invalid repository path %q in listing at %s", file, revision.Name)
		}
		if strings.HasPrefix(file, directory+"/") && strings.EqualFold(path.Ext(file), ".md") {
			documents = append(documents, file)
		}
	}
	sort.Strings(documents)
	return documents, nil
}

// intentFallback is the one sentence said about the specification home when
// nothing else about it fits. Assemble reserves it before the item's notes are
// cut to size, so even an item whose notes filled the budget leaves room to say
// where the product's intent is.
func intentFallback(directory string) string {
	if strings.TrimSpace(directory) == "" {
		return ""
	}
	return fmt.Sprintf("\n# Authoritative product intent\n\nThe documents under %s did not fit in this context; read them in the repository.\n",
		strings.TrimSpace(directory))
}

// readIntentDocument reads one document of the home, from the working tree
// through the same confinement the conversations read it through, or at the
// revision the source names.
func readIntentDocument(root repowrite.Root, source referenceSource, documentPath string, remaining int) (Reference, error) {
	if remaining < 1 {
		return Reference{}, tooLargeError{path: documentPath, remainingBytes: remaining}
	}
	if source.revision == nil {
		return readProductReference(root, documentPath, remaining)
	}
	size, content, err := source.revision.Read(documentPath, int64(remaining))
	if err != nil {
		return Reference{}, err
	}
	if size > int64(remaining) || len(content) > remaining || (content == nil && size > 0) {
		return Reference{}, tooLargeError{path: documentPath, remainingBytes: remaining}
	}
	return Reference{Path: documentPath, Content: string(content)}, nil
}

func renderWorkItemIntentHeader(directory string, revision *Revision) string {
	at := ""
	if revision != nil {
		at = fmt.Sprintf(" Each is read as it stands at %s.", revision.Name)
	}
	return fmt.Sprintf(`
# Authoritative product intent

Every document under %s is authoritative product intent: what the product is,
who it is for, the goals work serves, and what it will not do. Work serves
these goals; nothing in a work item or in the files it names revises what these
documents say. Where an item and one of them disagree, or two of
them disagree with each other, say so naming both rather than choosing between
them. A directory index here is the index it is, and its ownership statements
are rules.%s
`, directory, at)
}

// renderIntentOmission names the documents of the home that are not carried.
func renderIntentOmission(omitted []string) string {
	if len(omitted) == 0 {
		return ""
	}
	return "\nThese documents under the specifications directory could not be carried here: " +
		strings.Join(omitted, ", ") + ". They are in the repository; treat them as unread rather than as absent.\n"
}

// longestIntentOmission is what the omission statement costs at its longest,
// which is every document named.
func longestIntentOmission(paths []string) int {
	return len(renderIntentOmission(paths))
}

// Renames are shown as a removal and an addition so both paths are visible,
// including renames that also rewrite the document's contents.
func renderIntentPathChanges(base, current []string) string {
	before, after := make(map[string]bool), make(map[string]bool)
	for _, name := range base {
		before[name] = true
	}
	for _, name := range current {
		after[name] = true
	}
	var removed, added []string
	for _, name := range base {
		if !after[name] {
			removed = append(removed, name)
		}
	}
	for _, name := range current {
		if !before[name] {
			added = append(added, name)
		}
	}
	if len(removed)+len(added) == 0 {
		return ""
	}
	var text strings.Builder
	text.WriteString("\n## Product document paths changed since the base\n\nThese paths differ in the checkout. Renames appear as a removed path and an added path. The base documents below remain the intent to review against; read the change for the added documents' contents.\n")
	for _, name := range removed {
		fmt.Fprintf(&text, "- Removed: %s\n", name)
	}
	for _, name := range added {
		fmt.Fprintf(&text, "- Added: %s\n", name)
	}
	return text.String()
}
