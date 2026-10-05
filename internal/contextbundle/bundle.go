package contextbundle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

const defaultMaxBytes = 256 << 10

// maxExcerptSourceBytes bounds the file an excerpt is chosen from. Choosing
// sections of a document means reading the whole of it, and past this size that
// is no longer the cheap answer the excerpt exists to be, so the reference is
// stated as left out instead.
const maxExcerptSourceBytes = 8 << 20

// minExcerptBytes is how little room leaves an excerpt not worth making. Below
// it what survives is a heading and half a sentence, which reads as the document
// rather than as a fragment of one.
const minExcerptBytes = 512

// maxElisionBytes bounds one marker saying what an excerpt left out, so what the
// markers can cost is charged before the sections are chosen rather than
// discovered once they are rendered.
const maxElisionBytes = 96

// maxOmissionCauseBytes bounds the reason a reference that did not resolve is
// stated with. The path came from prose and the reason came from the
// filesystem, and a statement about a file that is not here must not spend the
// budget the files that are here need.
const maxOmissionCauseBytes = 160

// minTermBytes is how short a word is dropped from what a work item is about.
// There is no stopword list behind it: what makes a common word harmless is that
// it appears in every section, and the weighting below already discounts a term
// for exactly that.
const minTermBytes = 4

// maxNotesTruncationBytes is the allowance the marker standing in for dropped
// notes is charged before any note is kept, so what the marker costs is settled
// before the notes are chosen rather than discovered once they are rendered. The
// counts inside it grow with the item, so it is roughly twice the marker's own
// length; TestNotesTruncationMarkerFitsWhatIsChargedForIt renders it at
// implausible counts and requires the headroom to still be there.
const maxNotesTruncationBytes = 768

// maxTruncatedItemIDBytes keeps the item the marker names to part of a line, so
// what the marker costs is bounded by the sentence rather than by an identifier
// the tracker chose.
const maxTruncatedItemIDBytes = 80

type Request struct {
	RepositoryRoot string
	WorkItem       beads.WorkItem
	References     []string
	MaxBytes       int
	// Revision, where set, is the commit every reference is read at instead of
	// the repository's working tree, and each reference is labelled with it. A
	// reviewer's context sets it to the change's recorded base: the checkout has
	// moved on by the time a review is asked for whenever anything else was
	// promoted meanwhile, and a document read at a later revision than the one the
	// change was written against makes a correct change read as a divergent one.
	Revision *Revision
	// Specifications is the product's specification home, product.specifications,
	// relative to the repository root. Every document in it is carried after the
	// work item, labelled as authoritative product intent, so the developer and
	// the reviewer read the intent the item serves rather than only what the item
	// happened to cite. Empty carries none, which is what a caller that is only
	// validating the item passes.
	Specifications string
}

// Revision is a recorded commit references are read at.
type Revision struct {
	// Name is how the commit is named to the reader — "base commit <id>" — and is
	// what every reference read at it is labelled with.
	Name string
	// ListFiles, when supplied, returns the complete repository-relative file
	// listing at this revision. A partial listing must return an error, because
	// it cannot establish which product documents define the standing set.
	// Older callers without it discover paths from the working tree.
	ListFiles func() ([]string, error)
	// Read answers one repository-relative, slash-separated path as the commit
	// holds it: its size, and its whole content where the size is within
	// maxBytes. A path the commit does not hold as a regular file answers an
	// error wrapping ErrNotAtRevision.
	Read func(path string, maxBytes int64) (size int64, content []byte, err error)
}

// ErrNotAtRevision is what a Revision's Read answers for a path the commit does
// not hold as a regular file.
var ErrNotAtRevision = errors.New("not a file at this revision")

type Reference struct {
	Path    string
	Content string
	// Excerpted says Content is part of the file rather than the whole of it,
	// because the whole of it did not fit the context budget. A reference listed
	// here as if it were the file would be a smaller version of the silence the
	// excerpt exists to avoid.
	Excerpted bool
}

type Bundle struct {
	Text       string
	References []Reference
	Bytes      int
	// SpecificationProblems is set by AssembleProduct alone: it names the
	// specifications that were included despite not following the required
	// structure, so a caller can report them to the operator as well as to the
	// product manager.
	SpecificationProblems []SpecificationProblem
	// NotesTruncation says the work item's own notes did not fit and what was
	// dropped to make them, and is nil for the ordinary item whose notes fit
	// whole. It is reported rather than only marked in the rendered text: an item
	// silently carrying half of what was recorded on it is exactly the thing an
	// operator has to be able to see from the run record rather than by reading
	// the context an agent was handed.
	NotesTruncation *NotesTruncation
	// SpecificationsIncluded counts the specifications among the references,
	// which the references alone no longer answer: a role that reads its own
	// documents as well has references whether or not the product records any
	// intent at all, and "this repository has no brief or goals" is exactly the
	// thing a caller must still be able to say. A directory index is carried as
	// a reference and is not counted here for the same reason — it says what
	// would be filed in a directory rather than stating any intent, and `yoyo
	// init` writes one into a specifications directory that is otherwise empty.
	SpecificationsIncluded int
	// ShippedDocumentationBytes is set by AssembleProduct alone: what the shipped
	// documentation the repository actually has adds up to on disk, carried or
	// not. It is reported so every briefing records the set's size where the
	// conversation's own record is kept, and so a caller can warn where the set
	// stands against its ceiling — see ShippedDocumentationStanding — rather
	// than leaving that to the test that fails once the ceiling is reached.
	ShippedDocumentationBytes int
	// TriageDocketPosition is set by AssembleProduct alone, and only where the
	// docket window walked past something: the position the next window resumes
	// past. It is handed back rather than kept, because where it is kept is the
	// caller's, and a caller that does not keep it gets a window that starts from
	// the oldest stoppage every time rather than one that skips anything.
	TriageDocketPosition *triage.WindowPosition
}

var markdownReferencePattern = regexp.MustCompile(`[A-Za-z0-9._/-]+\.md`)

var fileReferencePattern = regexp.MustCompile(`[A-Za-z0-9._/-]+\.[A-Za-z][A-Za-z0-9]*`)
var repositoryPathPattern = regexp.MustCompile(`[A-Za-z0-9._/-]+`)

// ExtractFileReferences names repository-relative files cited by the item,
// including extensionless files resolved against the reviewed commit's listing.
// Filename references with extensions are retained even where they do not
// resolve, so their unavailable content can be stated. Base-revision Markdown
// references remain separate from this whole-file evidence at the candidate's HEAD.
func ExtractFileReferences(item beads.WorkItem, repositoryFiles []string) []string {
	var references []string
	seen := make(map[string]bool)
	committed := make(map[string]bool, len(repositoryFiles))
	for _, file := range repositoryFiles {
		committed[file] = true
	}
	// Acceptance sources have first claim on a bounded review's content budget;
	// old run notes must not displace the sources needed to judge the criteria.
	for _, text := range []string{item.AcceptanceCriteria, item.Description, item.Design, item.Notes} {
		var candidates []string
		add := func(candidate string) {
			clean := filepath.Clean(candidate)
			if filepath.IsAbs(candidate) || clean == ".." || strings.HasPrefix(clean, "../") {
				return
			}
			candidates = append(candidates, filepath.ToSlash(clean))
		}
		for _, candidate := range fileReferencePattern.FindAllString(text, -1) {
			add(candidate)
		}
		for _, candidate := range repositoryPathPattern.FindAllString(text, -1) {
			clean := filepath.ToSlash(filepath.Clean(candidate))
			if committed[clean] {
				add(candidate)
			} else if withoutPeriod := strings.TrimRight(candidate, "."); committed[filepath.ToSlash(filepath.Clean(withoutPeriod))] {
				// A sentence-ending period is punctuation unless the committed
				// filename itself includes it.
				add(withoutPeriod)
			}
		}
		for _, candidate := range uniqueSorted(candidates) {
			if !seen[candidate] {
				references = append(references, candidate)
				seen[candidate] = true
			}
		}
	}
	return references
}

func Assemble(request Request) (Bundle, error) {
	if request.WorkItem.ID == "" {
		return Bundle{}, errors.New("work item is required")
	}
	root, err := filepath.Abs(request.RepositoryRoot)
	if err != nil {
		return Bundle{}, fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Bundle{}, fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	maxBytes := request.MaxBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxBytes
	}
	if maxBytes < 1 {
		return Bundle{}, errors.New("max context bytes must be greater than zero")
	}

	referencePaths := append([]string(nil), request.References...)
	source := referenceSource{root: root, revision: request.Revision}
	referencePaths = append(referencePaths, implicitReferenceCandidates(source, ExtractMarkdownReferences(request.WorkItem))...)
	plans, err := planReferences(source, uniqueSorted(referencePaths), request.References)
	if err != nil {
		return Bundle{}, err
	}

	// What the statements of omission can cost is reserved before any reference is
	// read and released as each one is decided. Without it the reference that
	// filled the budget would be the one whose omission there was no room left to
	// state, which is the one thing this context must never be silent about.
	pendingNotes := 0
	for _, plan := range plans {
		pendingNotes += len(plan.note())
	}
	// The least the specification home can say is reserved the same way, so an
	// item whose notes are cut to fit still leaves room to say where the
	// product's intent is.
	intentReserve := len(intentFallback(request.Specifications))
	pendingNotes += intentReserve

	base := renderWorkItem(request.WorkItem)
	var truncation *NotesTruncation
	if len(base) > maxBytes {
		// The notes are cut against what is left after that reservation rather than
		// against the whole budget: an item truncated to the last byte would leave
		// nothing to say a reference with, and these are the items whose notes name
		// the most of them.
		base, truncation = truncateNotes(request.WorkItem, maxBytes-pendingNotes)
		if truncation == nil {
			return Bundle{}, runstate.StopError{Class: runstate.StopContextBound, Cause: fmt.Errorf("work item context is %d bytes before any of its notes, exceeding limit %d",
				len(renderWorkItem(withoutNotes(request.WorkItem))), maxBytes)}
		}
	}
	bundle := Bundle{Bytes: len(base), NotesTruncation: truncation}
	var output bytes.Buffer
	output.WriteString(base)

	// The product's specification home is read after the item and before what
	// the item names, so intent wins the budget over a citation, and it takes at
	// most its share so a large home still leaves the item's references room.
	pendingNotes -= intentReserve
	intentBudget := max(min(maxBytes/maxIntentShareDivisor, maxBytes-bundle.Bytes-pendingNotes), intentReserve)
	intent, intentReferences, err := renderWorkItemIntent(root, source, request.Specifications, intentBudget)
	if err != nil {
		return Bundle{}, err
	}
	output.WriteString(intent)
	bundle.Bytes += len(intent)
	carried := make(map[string]bool, len(intentReferences))
	for _, reference := range intentReferences {
		carried[reference.Path] = true
		bundle.References = append(bundle.References, reference)
		if !directoryIndex(reference.Path) {
			bundle.SpecificationsIncluded++
		}
	}

	for _, plan := range plans {
		pendingNotes -= len(plan.note())
		// A document the item names that the specification home already carried
		// is not carried twice.
		if plan.omission == "" && carried[plan.resolved.path] {
			continue
		}
		if plan.omission != "" {
			output.WriteString(plan.omission)
			bundle.Bytes += len(plan.omission)
			continue
		}
		section, reference, err := referenceSection(plan.resolved, plan.path, request.WorkItem, maxBytes-bundle.Bytes-pendingNotes)
		if err != nil {
			return Bundle{}, err
		}
		output.WriteString(section)
		bundle.Bytes += len(section)
		if reference.Path != "" {
			bundle.References = append(bundle.References, reference)
		}
	}
	bundle.Text = output.String()
	bundle.Bytes = len(bundle.Text)
	return bundle, nil
}

func ExtractMarkdownReferences(item beads.WorkItem) []string {
	text := strings.Join([]string{item.Description, item.Design, item.AcceptanceCriteria, item.Notes}, "\n")
	matches := markdownReferencePattern.FindAllString(text, -1)
	references := make([]string, 0, len(matches))
	for _, match := range matches {
		// URL matches begin at the authority separator (for example,
		// //example.com/design.md). Absolute paths are not valid implicit
		// repository references either, so exclude both before resolution.
		if strings.HasPrefix(match, "/") {
			continue
		}
		references = append(references, match)
	}
	return uniqueSorted(references)
}

// implicitReferenceCandidates narrows what a work item's own prose named to what
// this context has anything to say about. A path that names no file at all is
// dropped: work-item prose names Markdown deliverables that do not exist yet, and
// stating each of those as omitted would report the work still to be done as
// something missing. Everything else is kept, including a path that is not a
// repository reference — planReferences states those as left out, which is what
// tells the developer their item named something this context could not carry.
//
// Read at a revision, a path the commit does not hold is dropped for the same
// reason: it is a deliverable the change itself creates, and the patch is where
// it is read.
func implicitReferenceCandidates(source referenceSource, referencePaths []string) []string {
	candidates := make([]string, 0, len(referencePaths))
	for _, referencePath := range referencePaths {
		clean, err := validateReferencePath(referencePath)
		if err != nil {
			candidates = append(candidates, referencePath)
			continue
		}
		if !source.exists(clean) {
			continue
		}
		candidates = append(candidates, referencePath)
	}
	return candidates
}

// referenceSource is where references are read from: the repository's working
// tree, or, where a revision is set, the commit it names.
type referenceSource struct {
	root     string
	revision *Revision
}

// exists says whether a validated path names anything at all in this source,
// which is the question implicitReferenceCandidates asks before a path is
// planned.
func (s referenceSource) exists(clean string) bool {
	if s.revision != nil {
		_, _, err := s.revision.Read(filepath.ToSlash(clean), 0)
		return !errors.Is(err, ErrNotAtRevision)
	}
	_, err := os.Lstat(filepath.Join(s.root, clean))
	return !errors.Is(err, os.ErrNotExist)
}

// resolve proves a reference names a regular file in this source.
func (s referenceSource) resolve(referencePath string) (resolvedReference, error) {
	if s.revision == nil {
		return resolveReference(s.root, referencePath)
	}
	clean, err := validateReferencePath(referencePath)
	if err != nil {
		return resolvedReference{}, err
	}
	path := filepath.ToSlash(clean)
	// Read whole up to the largest file an excerpt is chosen from, so the one
	// read decides the size and the content together rather than two reads that
	// could see two different things.
	size, content, err := s.revision.Read(path, maxExcerptSourceBytes)
	if err != nil {
		return resolvedReference{}, fmt.Errorf("read reference %q at %s: %w", referencePath, s.revision.Name, err)
	}
	return resolvedReference{path: path, size: size, revision: s.revision.Name, content: content}, nil
}

// plannedReference is one reference decided before any of the budget is spent:
// either a file this context can read, or the statement that stands in for one
// it cannot. Deciding it up front is what lets an omission be charged against
// the budget before the references that do fit have spent it.
type plannedReference struct {
	// path is the reference as it was written, which is what an omission names:
	// that is what a developer will look for in the worktree.
	path     string
	resolved resolvedReference
	// omission is what is carried in place of a reference that did not resolve,
	// and is empty for one that did.
	omission string
}

// note is what this reference may still cost the bundle after it is decided: the
// statement of its omission, whether that omission was settled here or is
// decided later by what the budget has left.
func (p plannedReference) note() string {
	if p.omission != "" {
		return p.omission
	}
	return renderOmittedReference(p.path, p.resolved.revision)
}

// planReferences resolves every reference before any of them is read.
//
// A reference the work item's own text named is never a failure. That text is
// append-only — a triage note quoting a reviewer's citation of a path outside
// the repository cannot be edited back out again — so an item naming a reference
// that cannot be resolved would otherwise be an item no run could ever start,
// and no human could unwedge. It is stated as left out instead, which is the
// same answer size already gets: the developer reads what was named and that
// this context does not hold it.
//
// An explicit request reference is the caller's own required input rather than
// anything an item accumulated, so it still fails here. Nothing appended to a
// work item can add one, so there is nothing for a run to be wedged by.
func planReferences(source referenceSource, referencePaths, required []string) ([]plannedReference, error) {
	requiredPaths := make(map[string]struct{}, len(required))
	for _, referencePath := range required {
		requiredPaths[referencePath] = struct{}{}
	}
	plans := make([]plannedReference, 0, len(referencePaths))
	for _, referencePath := range referencePaths {
		resolved, err := source.resolve(referencePath)
		if err != nil {
			if _, isRequired := requiredPaths[referencePath]; isRequired {
				return nil, err
			}
			plans = append(plans, plannedReference{path: referencePath, omission: renderUnresolvedReference(referencePath, err)})
			continue
		}
		plans = append(plans, plannedReference{path: referencePath, resolved: resolved})
	}
	return plans, nil
}

func readReference(root, referencePath string, remainingBytes int) (Reference, error) {
	resolved, err := resolveReference(root, referencePath)
	if err != nil {
		return Reference{}, err
	}
	if resolved.size > int64(remainingBytes) {
		return Reference{}, tooLargeError{path: referencePath, remainingBytes: remainingBytes}
	}
	data, err := readBounded(resolved, referencePath, remainingBytes)
	if err != nil {
		return Reference{}, err
	}
	if len(data) > remainingBytes {
		return Reference{}, tooLargeError{path: referencePath, remainingBytes: remainingBytes}
	}
	return Reference{Path: resolved.path, Content: string(data)}, nil
}

// resolvedReference is a reference that has been proven to be a regular file
// inside the repository, and how big it is. It is separated from reading one
// because a reference that does not fit is still read — as an excerpt — and
// deciding that twice from two resolutions is deciding it about two files.
type resolvedReference struct {
	// path is the repository-relative path, which is what the reader is shown.
	path string
	// location is the resolved path on disk, which is what is opened.
	location string
	size     int64
	// revision names the commit the reference was read at, and is empty for one
	// read from the working tree. Where it is set, content is what the commit
	// holds — nil for a file larger than any excerpt is chosen from — and
	// location is unused.
	revision string
	content  []byte
}

func resolveReference(root, referencePath string) (resolvedReference, error) {
	clean, err := validateReferencePath(referencePath)
	if err != nil {
		return resolvedReference{}, err
	}
	path := filepath.Join(root, clean)
	location, err := filepath.EvalSymlinks(path)
	if err != nil {
		return resolvedReference{}, fmt.Errorf("resolve reference %q: %w", referencePath, err)
	}
	relative, err := filepath.Rel(root, location)
	if err != nil {
		return resolvedReference{}, fmt.Errorf("verify reference %q: %w", referencePath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return resolvedReference{}, fmt.Errorf("reference %q resolves outside the repository", referencePath)
	}
	info, err := os.Stat(location)
	if err != nil {
		return resolvedReference{}, fmt.Errorf("stat reference %q: %w", referencePath, err)
	}
	if !info.Mode().IsRegular() {
		return resolvedReference{}, fmt.Errorf("reference %q is not a regular file", referencePath)
	}
	return resolvedReference{path: filepath.ToSlash(relative), location: location, size: info.Size()}, nil
}

// readBounded reads at most one byte past the budget, so a file that grew
// between being sized and being read is caught by the caller rather than
// spending whatever it has become.
func readBounded(reference resolvedReference, referencePath string, limitBytes int) ([]byte, error) {
	if reference.revision != "" {
		if reference.content == nil && reference.size > 0 {
			return nil, fmt.Errorf("read reference %q at %s: it is %d bytes, larger than was read", referencePath, reference.revision, reference.size)
		}
		if len(reference.content) > limitBytes {
			return reference.content[:limitBytes+1], nil
		}
		return reference.content, nil
	}
	file, err := os.Open(reference.location)
	if err != nil {
		return nil, fmt.Errorf("open reference %q: %w", referencePath, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limitBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read reference %q: %w", referencePath, err)
	}
	return data, nil
}

// referenceSection renders one reference into a work-item context and reports
// what of it was carried. A reference that fits is carried whole; one that does
// not is excerpted to the sections that look most relevant to the work item, or,
// where not even an excerpt fits, stated as left out.
//
// Nothing here fails the bundle for size. The file is in the worktree whatever
// this context carries, so a reference that does not fit is unread rather than
// unreachable — and failing would fail every work item whose prose or
// append-only notes happen to name a document that has since grown past the
// budget, which is not something the item can be edited out of.
//
// The returned Reference has no path when nothing of the file was carried.
func referenceSection(resolved resolvedReference, referencePath string, item beads.WorkItem, available int) (string, Reference, error) {
	header := renderReferenceHeader(resolved, "")
	// The trailing newline the section is terminated with is part of what it
	// costs, so it is charged here rather than discovered afterwards.
	budget := available - len(header) - 1
	if resolved.size <= int64(budget) {
		data, err := readBounded(resolved, referencePath, budget)
		if err != nil {
			return "", Reference{}, err
		}
		if len(data) <= budget {
			reference := Reference{Path: resolved.path, Content: string(data)}
			return header + terminated(reference.Content), reference, nil
		}
	}
	return excerptSection(resolved, referencePath, item, available)
}

// excerptSection renders what a reference too large for the budget can still
// contribute: an excerpt of the sections most relevant to the work item, or the
// statement that it was left out. The omission names the path the work item
// wrote rather than the resolved one, because that is what a developer will look
// for in the worktree.
func excerptSection(resolved resolvedReference, referencePath string, item beads.WorkItem, available int) (string, Reference, error) {
	omission := renderOmittedReference(referencePath, resolved.revision)
	if resolved.size > maxExcerptSourceBytes {
		return omission, Reference{}, nil
	}
	header := renderReferenceHeader(resolved, "excerpt")
	notice := renderExcerptNotice(resolved.path, resolved.size, resolved.revision)
	budget := available - len(header) - len(notice) - 1
	if budget < minExcerptBytes {
		return omission, Reference{}, nil
	}
	data, err := readBounded(resolved, referencePath, maxExcerptSourceBytes)
	if err != nil {
		return "", Reference{}, err
	}
	body := excerpt(string(data), item, budget)
	if body == "" {
		return omission, Reference{}, nil
	}
	reference := Reference{Path: resolved.path, Content: body, Excerpted: true}
	return header + notice + terminated(body), reference, nil
}

func terminated(content string) string {
	if strings.HasSuffix(content, "\n") {
		return content
	}
	return content + "\n"
}

// renderReferenceHeader heads one carried reference. A reference read at a
// revision says which one in the heading itself and again in a sentence under
// it, because the heading is what a reader scanning the context sees and the
// sentence is what says the file may since have changed.
func renderReferenceHeader(resolved resolvedReference, kind string) string {
	if resolved.revision == "" {
		if kind == "" {
			return fmt.Sprintf("\n## Referenced file: %s\n\n", resolved.path)
		}
		return fmt.Sprintf("\n## Referenced file: %s (%s)\n\n", resolved.path, kind)
	}
	qualifier := "at " + resolved.revision
	if kind != "" {
		qualifier = kind + ", " + qualifier
	}
	return fmt.Sprintf("\n## Referenced file: %s (%s)\n\n%s as it stands at %s, which may differ from the file as it stands now.\n\n",
		resolved.path, qualifier, resolved.path, resolved.revision)
}

func renderExcerptNotice(path string, size int64, revision string) string {
	where := "is in the\nworktree"
	if revision != "" {
		where = "is at\n" + revision
	}
	return fmt.Sprintf(`%s is %d bytes and does not fit in this context. What follows is an excerpt:
the sections of it that look most relevant to this work item, in the file's own
order, with a marker wherever something was left out. The whole file %s — read it there rather than reading this as all of it.

`, path, size, where)
}

func renderOmittedReference(referencePath, revision string) string {
	if revision != "" {
		return fmt.Sprintf(`
## Referenced file: %s (omitted, at %s)

%s was referenced but exceeds the context budget; consult it at %s.
`, referencePath, revision, referencePath, revision)
	}
	return fmt.Sprintf(`
## Referenced file: %s (omitted)

%s was referenced but exceeds the context budget; consult it in the worktree.
`, referencePath, referencePath)
}

// renderUnresolvedReference states a reference that named no file this
// repository holds. It says what was named and why nothing was read for it, so
// the developer reads a gap they can act on rather than a document they never
// learn was named at all.
func renderUnresolvedReference(referencePath string, cause error) string {
	return fmt.Sprintf(`
## Referenced file: %s (omitted)

%s was named by this work item but does not resolve to a file in this repository
(%s), so nothing was read for it. Treat it as unread rather than as absent.
`, referencePath, referencePath, singleLine(cause.Error(), maxOmissionCauseBytes))
}

// tooLargeError reports a reference that did not fit in the remaining budget.
// It is a distinct type because a product document that does not fit is
// reported to the reader as omitted rather than as an error. A work-item
// reference that does not fit is neither: it is excerpted or stated as left
// out, and never reaches an error at all.
type tooLargeError struct {
	path           string
	remainingBytes int
}

func (e tooLargeError) Error() string {
	return fmt.Sprintf("reference %q exceeds remaining context limit of %d bytes", e.path, e.remainingBytes)
}

// excerpt chooses as much of an oversized document as the budget holds: whole
// sections, ranked by how much of what the work item is about they carry, and
// rendered back in the document's own order so what survives still reads as part
// of the file. It returns "" when there is nothing worth carrying, and the
// reference is stated as left out instead.
//
// The excerpt is bounded by construction rather than by measuring afterwards:
// every kept section is charged one elision marker, and one more is reserved for
// a document that ends in a gap, so the markers can never push the rendering
// past what was budgeted for it.
func excerpt(content string, item beads.WorkItem, budget int) string {
	sections := splitSections(content)
	if len(sections) < 2 {
		// A document with no headings is one section, and one section that did not
		// fit is nothing to choose between. Cutting it at an arbitrary byte would
		// hand the reader a document that stops mid-sentence and says so nowhere,
		// so it is stated as left out instead.
		return ""
	}
	budget -= maxElisionBytes
	scores := sectionScores(sections, workItemTerms(item))
	ranked := make([]int, len(sections))
	for index := range ranked {
		ranked[index] = index
	}
	// Stable, so sections nothing distinguishes stay in the document's order
	// rather than in whichever one the sort happened to leave them.
	sort.SliceStable(ranked, func(i, j int) bool { return scores[ranked[i]] > scores[ranked[j]] })

	kept := make([]bool, len(sections))
	spent, carried := 0, 0
	// The opening of the document is offered first whatever it scores: it says
	// what the file is, and an excerpt that starts mid-document reads as a
	// different document rather than as part of one.
	for _, index := range append([]int{0}, ranked...) {
		if kept[index] {
			continue
		}
		cost := len(sections[index]) + maxElisionBytes
		if spent+cost > budget {
			continue
		}
		kept[index] = true
		spent += cost
		carried += len(sections[index])
	}
	if carried < minExcerptBytes {
		// What fit is the document's title and little else, which tells the reader
		// nothing while looking like something. Saying the file was left out is the
		// more useful of the two.
		return ""
	}
	return renderExcerpt(sections, kept)
}

// splitSections cuts a Markdown document at its headings. Each section carries
// its own heading, and whatever precedes the first one is a section of its own.
// Headings inside a fenced block are not headings: a configuration example
// showing a commented line would otherwise cut the section describing it in two.
func splitSections(content string) []string {
	var sections []string
	var current strings.Builder
	inFence := false
	for _, raw := range strings.SplitAfter(content, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "```"), strings.HasPrefix(line, "~~~"):
			inFence = !inFence
		case !inFence && headingPattern.MatchString(line) && current.Len() > 0:
			sections = append(sections, current.String())
			current.Reset()
		}
		current.WriteString(raw)
	}
	if current.Len() > 0 {
		sections = append(sections, current.String())
	}
	return sections
}

// workItemTerms is the vocabulary the work item is about, which is what decides
// which sections of an oversized document are worth carrying.
func workItemTerms(item beads.WorkItem) map[string]struct{} {
	text := strings.ToLower(strings.Join([]string{item.Title, item.Description, item.Design, item.AcceptanceCriteria, item.Notes}, "\n"))
	terms := make(map[string]struct{})
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(word) < minTermBytes {
			continue
		}
		terms[word] = struct{}{}
	}
	return terms
}

// sectionScores weighs each section by the work item's terms it carries, and
// weighs each term by how few sections hold it. That weighting is what stands in
// for a list of words to ignore: a term in every section says nothing about
// which section to keep, and is discounted to almost nothing without anybody
// having to have written it down as common.
func sectionScores(sections []string, terms map[string]struct{}) []float64 {
	lowered := make([]string, len(sections))
	for index, section := range sections {
		lowered[index] = strings.ToLower(section)
	}
	scores := make([]float64, len(sections))
	for term := range terms {
		var carrying []int
		for index, section := range lowered {
			if strings.Contains(section, term) {
				carrying = append(carrying, index)
			}
		}
		if len(carrying) == 0 {
			continue
		}
		weight := math.Log(float64(len(sections)+1) / float64(len(carrying)))
		for _, index := range carrying {
			scores[index] += weight
		}
	}
	return scores
}

// renderExcerpt writes the kept sections in the document's order, saying at each
// gap how much of the file is not there. A gap left unmarked is what would let
// an excerpt be read as the whole document.
func renderExcerpt(sections []string, kept []bool) string {
	var rendered strings.Builder
	skipped := 0
	for index, section := range sections {
		if !kept[index] {
			skipped++
			continue
		}
		if skipped > 0 {
			rendered.WriteString(renderElision(skipped))
			skipped = 0
		}
		rendered.WriteString(section)
	}
	if skipped > 0 && rendered.Len() > 0 {
		rendered.WriteString(renderElision(skipped))
	}
	return rendered.String()
}

func renderElision(sections int) string {
	return fmt.Sprintf("\n[%d section(s) of this file are not included in this excerpt]\n\n", sections)
}

func validateReferencePath(referencePath string) (string, error) {
	clean := filepath.Clean(referencePath)
	if filepath.IsAbs(referencePath) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("reference %q must be a repository-relative path", referencePath)
	}
	if strings.ToLower(filepath.Ext(clean)) != ".md" {
		return "", fmt.Errorf("reference %q must be a Markdown file", referencePath)
	}
	return clean, nil
}

func renderWorkItem(item beads.WorkItem) string {
	return fmt.Sprintf(`# Assigned work item

ID: %s
Title: %s
Status: %s
Depends on: %s
Relevant goals — goals the change must not break: %s

## Description

%s

## Design guidance

%s

## Acceptance criteria

%s

## Notes

%s
`, item.ID, item.Title, item.Status, renderDependencies(item), relevantGoals(item.RelevantGoals),
		emptyFallback(item.Description), emptyFallback(item.Design), emptyFallback(item.AcceptanceCriteria), emptyFallback(item.Notes))
}

// NotesTruncation is what an item's notes lost to the context budget: how many
// of the oldest of them were dropped, how much of the notes that was, and how
// much of them the context still carries.
type NotesTruncation struct {
	DroppedNotes int
	DroppedBytes int
	KeptBytes    int
}

// truncateNotes renders a work item too large for the budget by dropping the
// oldest of its notes until it fits, and reports what it dropped.
//
// The notes are the one part of a work item that grows without bound: every run
// appends to them and nothing removes them, so a long-lived item eventually
// renders past any budget — and in exactly the way nobody can edit it back out
// of, because the notes are append-only. That is why they are what is cut. The
// description and the acceptance criteria are what the item is for and are never
// dropped, so an item that does not fit with none of its notes is refused
// instead: this returns no truncation for one, and the caller refuses on that.
//
// The oldest go first because the newest are what the item is being worked from:
// the reviewer's last findings, the operator's most recent guidance, the round
// that just stopped.
func truncateNotes(item beads.WorkItem, maxBytes int) (string, *NotesTruncation) {
	// What the marker costs is charged before any note is kept, so keeping one
	// more note can never be what leaves no room to say the rest were dropped —
	// the one thing this rendering must never be silent about.
	available := maxBytes - len(renderWorkItem(withoutNotes(item))) - maxNotesTruncationBytes
	if available < 0 {
		return "", nil
	}
	notes := splitNotes(item.Notes)
	kept, keptBytes := 0, 0
	for index := len(notes) - 1; index >= 0; index-- {
		if keptBytes+len(notes[index]) > available {
			break
		}
		kept++
		keptBytes += len(notes[index])
	}
	dropped := notes[:len(notes)-kept]
	truncation := &NotesTruncation{DroppedNotes: len(dropped), KeptBytes: keptBytes}
	for _, note := range dropped {
		truncation.DroppedBytes += len(note)
	}
	truncated := item
	truncated.Notes = renderNotesTruncation(item.ID, *truncation) + strings.Join(notes[len(notes)-kept:], "")
	return renderWorkItem(truncated), truncation
}

// splitNotes cuts an item's notes into the notes they were appended as. A note
// is appended with a blank line in front of it, so a blank line is where one note
// ends and the next begins; the blank lines themselves stay with the note above
// them, which is what leaves what survives starting on a note rather than on the
// gap after a dropped one.
func splitNotes(notes string) []string {
	var split []string
	var current strings.Builder
	blank := false
	for _, raw := range strings.SplitAfter(notes, "\n") {
		if strings.TrimSpace(raw) == "" {
			blank = true
			current.WriteString(raw)
			continue
		}
		if blank && current.Len() > 0 {
			split = append(split, current.String())
			current.Reset()
		}
		blank = false
		current.WriteString(raw)
	}
	if current.Len() > 0 {
		split = append(split, current.String())
	}
	return split
}

// renderNotesTruncation says what was dropped and where the whole of it is, in
// the place the dropped notes would have been. A gap left unmarked is what would
// let notes that were cut for size be read as notes nobody ever wrote.
func renderNotesTruncation(itemID string, truncation NotesTruncation) string {
	return fmt.Sprintf(`[the oldest %d note(s) on %s, %d bytes of them, are not included here: this item's
notes do not fit in this context, so the most recent of them were kept and the
rest were dropped. Nothing else about the item was: the description and the
acceptance criteria above are whole. The notes are held whole on the work item
itself — read them there, or in the tracker's export in this worktree, rather
than reading what follows as all of them.]

`, truncation.DroppedNotes, singleLine(itemID, maxTruncatedItemIDBytes), truncation.DroppedBytes)
}

func withoutNotes(item beads.WorkItem) beads.WorkItem {
	item.Notes = ""
	return item
}

// blocksDependency is the tracker relation that makes one item wait for another.
const blocksDependency = "blocks"

// renderDependencies states what the item waits on, on the same line whether it
// waits on anything or not.
//
// Saying "nothing" is the half that matters. This context is what both the
// developer and the reviewer read the item from, and a line that appeared only
// when there were dependencies would leave them unable to tell an item nothing
// blocks from an item whose blockers this context happens not to carry. A
// reviewer judging a change against an item that waits on unfinished work needs
// to be able to see that it does — a change written as if the work it depends on
// were settled is judged differently from one written after it was.
//
// A dependency whose own item is closed is not something anybody is waiting for,
// and it is left out rather than listed as satisfied: what this line is for is
// what is still outstanding.
func renderDependencies(item beads.WorkItem) string {
	var waiting []string
	var states []string
	for _, dependency := range item.Dependencies {
		if dependency.Type == blocksDependency && dependency.Status != "closed" {
			waiting = append(waiting, dependency.ID)
			status := dependency.Status
			if strings.TrimSpace(status) == "" {
				status = "not supplied by the tracker"
			}
			states = append(states, dependency.ID+": "+status)
		}
	}
	if len(waiting) == 0 {
		return "nothing; no unfinished work blocks this item"
	}
	sort.Strings(waiting)
	sort.Strings(states)
	return strings.Join(waiting, ", ") + " (unfinished work this item waits on)\nBlocker states: " + strings.Join(states, ", ") +
		".\nStopping for an undecided upstream can be correct; do not require the developer to invent the decision as a repair."
}

func emptyFallback(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Not provided."
	}
	return value
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func relevantGoals(named []string) string {
	if len(named) == 0 {
		return "none recorded"
	}
	return strings.Join(named, "; ")
}
