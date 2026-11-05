package invariant

// The on-disk half of an invariant: one Markdown file per constraint, in the
// architect's own directory, reviewed with the code like every other canonical
// document. Reading is open to anything that needs the set; writing goes through
// Authorize, so the ownership boundary is enforced by the only code that can
// change an invariant rather than by the callers that ask it to.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// MaxFileBytes bounds one invariant file. It is a constraint stated tightly
// enough to hold a developer to, not a design document, and the bound is what
// keeps a file nobody noticed growing out of every prompt that carries it.
const MaxFileBytes = 32 << 10

// frontmatterFence opens and closes the machine-readable half of an invariant
// file. The prose below it is the constraint itself, so the metadata is kept
// where every other tool that reads Markdown expects it rather than encoded into
// the prose.
const frontmatterFence = "---"

// The two required body sections. They are headings rather than YAML keys
// because an invariant is read by people as often as by the harness, and a
// constraint quoted out of a YAML string is not a document anybody reviews.
const (
	statementHeading = "must hold"
	rationaleHeading = "why"
)

// Store reads and writes one repository's invariants.
type Store struct {
	// RepositoryRoot is the repository the invariants belong to.
	RepositoryRoot string
	// Directory is the invariants directory, relative to the repository root.
	Directory string
}

// Load reads every invariant the repository records. A directory that does not
// exist is not an error: a project that has recorded no invariant yet gets an
// empty set, and the harness delivers nothing rather than failing every run over
// an absent directory. Anything else that stops the directory being read is an
// error, because a set that silently came back empty would look exactly like a
// repository with no constraints at all.
func (s Store) Load() (Set, error) {
	root, directory, err := s.resolve()
	if err != nil {
		return Set{}, err
	}
	set := Set{Directory: filepath.ToSlash(directory)}
	// Where the configured directory actually is, resolved component by component
	// rather than joined onto the root: `docs/decisions/invariants` is whatever
	// the filesystem has put along the path, and one symlink above it reads a
	// constraint nobody committed and delivers it under exactly that path.
	base, err := root.ResolveDirectory(directory)
	if err != nil {
		return Set{}, err
	}
	info, err := os.Lstat(base)
	if errors.Is(err, os.ErrNotExist) {
		return set, nil
	}
	if err != nil {
		return Set{}, fmt.Errorf("inspect invariants directory %q: %w", s.Directory, err)
	}
	if !info.IsDir() {
		return Set{}, fmt.Errorf("invariants path %q is not a directory", s.Directory)
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		return Set{}, fmt.Errorf("read invariants directory %q: %w", s.Directory, err)
	}
	// Only this directory itself is read. Every invariant lives in it, one file
	// per constraint named by its own identity, so a Markdown file filed a level
	// down is reported rather than read: it would otherwise be an invariant
	// nobody is delivered and nobody knows is missing.
	for _, entry := range entries {
		relative := filepath.ToSlash(filepath.Join(directory, entry.Name()))
		if entry.IsDir() {
			nested, err := nestedMarkdown(filepath.Join(base, entry.Name()))
			if err != nil {
				return Set{}, err
			}
			for _, name := range nested {
				set.Problems = append(set.Problems, Problem{
					Path:   relative + "/" + name,
					Reason: "it is in a subdirectory; every invariant is a file in the invariants directory itself, named by its id",
				})
			}
			continue
		}
		if !entry.Type().IsRegular() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			continue
		}
		// The directory index is not a constraint and is not reported as a
		// malformed one. Every artifact home carries one and `yoyo doctor` reports
		// this one missing, so a file the harness asks for must not also be the
		// file the harness calls unreadable on every load. It is the same
		// exemption by name that the artifacts and the specifications make.
		if strings.EqualFold(entry.Name(), artifacthome.FileName) {
			continue
		}
		loaded, err := s.read(filepath.Join(base, entry.Name()), relative)
		if err != nil {
			var unreadable unreadableError
			if errors.As(err, &unreadable) {
				set.Problems = append(set.Problems, Problem{Path: relative, Reason: unreadable.reason})
				continue
			}
			return Set{}, err
		}
		if loaded.Active() {
			set.Active = append(set.Active, loaded)
			continue
		}
		set.Retired = append(set.Retired, loaded)
	}
	sortByID(set.Active)
	sortByID(set.Retired)
	sort.Slice(set.Problems, func(first, second int) bool { return set.Problems[first].Path < set.Problems[second].Path })
	return set, nil
}

// Draft is a new invariant as its author states it. The lifecycle fields are
// absent because the store owns them: status, revisions, and the file it lands
// in are the record of the mutation rather than something an author asserts.
type Draft struct {
	ID            string
	Title         string
	Statement     string
	Rationale     string
	EstablishedBy []string
	Scope         []string
	// Reason is why this invariant is being created, recorded as the first
	// revision.
	Reason string
}

// Amendment is a bounded change to an invariant that already exists. Every field
// is a pointer so an amendment says exactly what it changes: an invariant whose
// scope is being narrowed must not have its statement replaced by the zero value
// of a struct nobody filled in.
type Amendment struct {
	Title         *string
	Statement     *string
	Rationale     *string
	EstablishedBy *[]string
	Scope         *[]string
	// Reason is why the invariant is changing, recorded as a revision. It is
	// required for the same reason retirement's is: an amendment nobody explained
	// is one nobody can evaluate later.
	Reason string
}

// Create records a new invariant. Only the architect may: see Authorize.
func (s Store) Create(role domain.AgentRole, draft Draft, now time.Time) (Invariant, error) {
	created, err := s.PrepareCreate(role, draft, now)
	if err != nil {
		return Invariant{}, err
	}
	if err := s.write(created.Path, created); err != nil {
		return Invariant{}, err
	}
	return created, nil
}

// PrepareCreate is Create without the write: the invariant a creation would
// record, validated and placed at the file it would land in, refused exactly
// where Create would refuse it. It is what a reviewed run is handed instead of a
// change to the checkout.
func (s Store) PrepareCreate(role domain.AgentRole, draft Draft, now time.Time) (Invariant, error) {
	if err := Authorize(role); err != nil {
		return Invariant{}, err
	}
	created := Invariant{
		ID:            strings.TrimSpace(draft.ID),
		Title:         strings.TrimSpace(draft.Title),
		EstablishedBy: trimmedList(draft.EstablishedBy),
		Scope:         trimmedList(draft.Scope),
		Status:        StatusActive,
		Statement:     strings.TrimSpace(draft.Statement),
		Rationale:     strings.TrimSpace(draft.Rationale),
		Revisions: []Revision{{
			Action: ActionCreated,
			By:     role,
			At:     now.UTC(),
			Reason: strings.TrimSpace(draft.Reason),
		}},
	}
	if err := created.Validate(); err != nil {
		return Invariant{}, err
	}
	path, relative, err := s.path(created.ID)
	if err != nil {
		return Invariant{}, err
	}
	if _, err := os.Lstat(path); err == nil {
		return Invariant{}, fmt.Errorf("invariant %q already exists at %s; amend it instead", created.ID, relative)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Invariant{}, fmt.Errorf("inspect invariant %q: %w", created.ID, err)
	}
	created.Path = relative
	return created, nil
}

// Amend changes an invariant that already exists and records why. An invariant
// is amended in place as the system changes, unlike the decision record it was
// extracted from: the set of active invariants answers what currently holds, and
// a constraint left describing last month's system is one a developer learns to
// discount.
func (s Store) Amend(role domain.AgentRole, id string, amendment Amendment, now time.Time) (Invariant, error) {
	amended, err := s.PrepareAmend(role, id, amendment, now)
	if err != nil {
		return Invariant{}, err
	}
	if err := s.write(amended.Path, amended); err != nil {
		return Invariant{}, err
	}
	return amended, nil
}

// PrepareAmend is Amend without the write, as PrepareCreate is Create without
// it.
func (s Store) PrepareAmend(role domain.AgentRole, id string, amendment Amendment, now time.Time) (Invariant, error) {
	if err := Authorize(role); err != nil {
		return Invariant{}, err
	}
	existing, err := s.loadOne(id)
	if err != nil {
		return Invariant{}, err
	}
	// Amending a retired invariant would revive a constraint by editing it, which
	// is not a decision anybody made. Bringing one back is a new invariant, or an
	// explicit amendment of the retirement itself, and neither is this.
	if existing.Status == StatusRetired {
		return Invariant{}, fmt.Errorf("invariant %q is retired; a retired constraint is not amended back into force", id)
	}
	amended := existing
	if amendment.Title != nil {
		amended.Title = strings.TrimSpace(*amendment.Title)
	}
	if amendment.Statement != nil {
		amended.Statement = strings.TrimSpace(*amendment.Statement)
	}
	if amendment.Rationale != nil {
		amended.Rationale = strings.TrimSpace(*amendment.Rationale)
	}
	if amendment.EstablishedBy != nil {
		amended.EstablishedBy = trimmedList(*amendment.EstablishedBy)
	}
	if amendment.Scope != nil {
		amended.Scope = trimmedList(*amendment.Scope)
	}
	if amendment.changesNothing() {
		return Invariant{}, fmt.Errorf("amending invariant %q changes nothing; name what is being amended", id)
	}
	amended.Revisions = append(append([]Revision(nil), existing.Revisions...), Revision{
		Action: ActionAmended,
		By:     role,
		At:     now.UTC(),
		Reason: strings.TrimSpace(amendment.Reason),
	})
	if err := amended.Validate(); err != nil {
		return Invariant{}, err
	}
	return amended, nil
}

// Find reads the one invariant an id names, active or retired, and reports
// whether one is recorded under it. A file that is there and cannot be read as
// an invariant is an error rather than an absent invariant: a write that took it
// for absent would create a second constraint over the first.
func (s Store) Find(id string) (Invariant, bool, error) {
	path, relative, err := s.path(id)
	if err != nil {
		return Invariant{}, false, err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return Invariant{}, false, nil
	} else if err != nil {
		return Invariant{}, false, fmt.Errorf("inspect invariant %q: %w", id, err)
	}
	found, err := s.loadOne(strings.TrimSpace(id))
	if err != nil {
		return Invariant{}, false, err
	}
	found.Path = relative
	return found, true, nil
}

// ResolvedDirectory is the invariants directory as this store resolves it, relative to
// the repository root.
func (s Store) ResolvedDirectory() (string, error) {
	_, directory, err := s.resolve()
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(directory), nil
}

// changesNothing reports an amendment that names no change at all, which is a
// recorded revision with nothing to record.
func (a Amendment) changesNothing() bool {
	return a.Title == nil && a.Statement == nil && a.Rationale == nil && a.EstablishedBy == nil && a.Scope == nil
}

// Retire lifts an invariant and records that it was lifted. The file stays: an
// invariant that vanished leaves a developer who read it last week with no way
// to find out that it stopped applying, and leaves the reviewer's evidence with
// nothing to explain why a change that contradicts it is now correct.
func (s Store) Retire(role domain.AgentRole, id, reason string, now time.Time) (Invariant, error) {
	if err := Authorize(role); err != nil {
		return Invariant{}, err
	}
	existing, err := s.loadOne(id)
	if err != nil {
		return Invariant{}, err
	}
	if existing.Status == StatusRetired {
		retired, _ := existing.Retirement()
		return Invariant{}, fmt.Errorf("invariant %q was already retired on %s: %s", id, retired.At.Format(time.RFC3339), retired.Reason)
	}
	existing.Status = StatusRetired
	existing.Revisions = append(existing.Revisions, Revision{
		Action: ActionRetired,
		By:     role,
		At:     now.UTC(),
		Reason: strings.TrimSpace(reason),
	})
	if err := existing.Validate(); err != nil {
		return Invariant{}, err
	}
	if err := s.write(existing.Path, existing); err != nil {
		return Invariant{}, err
	}
	return existing, nil
}

// loadOne reads the single invariant an amendment or a retirement names. What it
// returns carries the file it came from in its Path, so the mutation replaces
// exactly what it read.
func (s Store) loadOne(id string) (Invariant, error) {
	path, relative, err := s.path(id)
	if err != nil {
		return Invariant{}, err
	}
	loaded, err := s.read(path, relative)
	if err != nil {
		var unreadable unreadableError
		if errors.As(err, &unreadable) {
			return Invariant{}, fmt.Errorf("invariant %q at %s cannot be read: %s", id, relative, unreadable.reason)
		}
		if errors.Is(err, os.ErrNotExist) {
			return Invariant{}, fmt.Errorf("no invariant %q is recorded at %s", id, relative)
		}
		return Invariant{}, err
	}
	return loaded, nil
}

// unreadableError is a file that is not a usable invariant, as opposed to a
// filesystem failure. The two are told apart because the first is a problem
// reported alongside a set that still loads, and the second means the set itself
// is not known.
type unreadableError struct {
	reason string
}

func (e unreadableError) Error() string { return e.reason }

// read parses one invariant file. Everything wrong with the file is reported as
// unreadable rather than fatal, so one malformed invariant never hides the rest.
func (s Store) read(path, relative string) (Invariant, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Invariant{}, err
	}
	if !info.Mode().IsRegular() {
		return Invariant{}, unreadableError{reason: "it is not a regular file"}
	}
	if info.Size() > MaxFileBytes {
		return Invariant{}, unreadableError{reason: fmt.Sprintf("it is %d bytes, limit is %d", info.Size(), MaxFileBytes)}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Invariant{}, fmt.Errorf("read invariant %q: %w", relative, err)
	}
	parsed, err := parse(string(content))
	if err != nil {
		return Invariant{}, unreadableError{reason: err.Error()}
	}
	// The file's name is the invariant's identity, so a file whose frontmatter
	// claims another id is refused rather than read as either: two names for one
	// constraint is how a delivered invariant and a retired one end up being the
	// same file.
	expected := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	if parsed.ID != expected {
		return Invariant{}, unreadableError{reason: fmt.Sprintf("its id %q does not match its file name; an invariant lives in %s.md", parsed.ID, parsed.ID)}
	}
	parsed.Path = relative
	if err := parsed.Validate(); err != nil {
		return Invariant{}, unreadableError{reason: err.Error()}
	}
	return parsed, nil
}

// write replaces one invariant file, named by where it lives in the repository
// rather than by an absolute path. Going through repowrite is what keeps the
// write inside the repository: the directory it lands in comes from
// configuration, and the lexical check that configuration passed says nothing
// about what the filesystem has put along the path by the time anything writes
// there. A constraint written outside the repository is one no developer is ever
// delivered and no reviewer ever sees.
func (s Store) write(relative string, recorded Invariant) error {
	root, err := repowrite.NewRoot(s.RepositoryRoot)
	if err != nil {
		return err
	}
	rendered, err := render(recorded)
	if err != nil {
		return err
	}
	if len(rendered) > MaxFileBytes {
		return fmt.Errorf("invariant %q renders to %d bytes, limit is %d", recorded.ID, len(rendered), MaxFileBytes)
	}
	if _, err := root.WriteFile(relative, []byte(rendered)); err != nil {
		return fmt.Errorf("write invariant %q: %w", recorded.ID, err)
	}
	return nil
}

// frontmatter is the machine-readable half of an invariant file. It is a
// separate type from Invariant so the prose halves stay in the body: a statement
// serialized into a YAML string is not a document anybody reviews.
type frontmatter struct {
	ID            string     `yaml:"id"`
	Title         string     `yaml:"title"`
	Status        Status     `yaml:"status"`
	EstablishedBy []string   `yaml:"established_by"`
	Scope         []string   `yaml:"scope,omitempty"`
	Revisions     []Revision `yaml:"revisions"`
}

// parse reads one invariant file: YAML frontmatter, then the required prose
// sections. Unknown frontmatter keys are refused rather than ignored, so a
// mistyped field fails visibly instead of leaving a constraint quietly missing
// half of what its author wrote.
func parse(content string) (Invariant, error) {
	metadata, body, err := splitFrontmatter(content)
	if err != nil {
		return Invariant{}, err
	}
	decoder := yaml.NewDecoder(strings.NewReader(metadata))
	decoder.KnownFields(true)
	var decoded frontmatter
	if err := decoder.Decode(&decoded); err != nil {
		return Invariant{}, fmt.Errorf("its frontmatter could not be read: %w", err)
	}
	statement, rationale, err := ParseBody(body)
	if err != nil {
		return Invariant{}, err
	}
	return Invariant{
		ID:            strings.TrimSpace(decoded.ID),
		Title:         strings.TrimSpace(decoded.Title),
		EstablishedBy: trimmedList(decoded.EstablishedBy),
		Scope:         trimmedList(decoded.Scope),
		Status:        decoded.Status,
		Statement:     statement,
		Rationale:     rationale,
		Revisions:     decoded.Revisions,
	}, nil
}

// splitFrontmatter separates the fenced metadata from the prose below it.
func splitFrontmatter(content string) (metadata, body string, err error) {
	// A byte-order mark ahead of the fence is still frontmatter; an editor that
	// wrote one must not turn an invariant into an unreadable file.
	lines := strings.Split(strings.TrimPrefix(content, "\ufeff"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterFence {
		return "", "", errors.New("it does not open with `---` frontmatter naming its id, title, status, origin, and revisions")
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) != frontmatterFence {
			continue
		}
		return strings.Join(lines[1:index], "\n"), strings.Join(lines[index+1:], "\n"), nil
	}
	return "", "", errors.New("its frontmatter is not closed by a `---` line")
}

// splitSections collects the body's level-two sections by their lowercased
// heading. A heading level is part of the format rather than a detail: the two
// required sections are peers, and prose filed under a deeper heading belongs to
// whichever of them it follows.
func splitSections(body string) map[string]string {
	sections := map[string]string{}
	heading := ""
	var current strings.Builder
	flush := func() {
		if heading == "" {
			return
		}
		if text := strings.TrimSpace(current.String()); text != "" {
			sections[heading] = text
		}
		current.Reset()
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			heading = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "## "), ":")))
			continue
		}
		if heading == "" {
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	flush()
	return sections
}

// Parse reads one invariant file's content as Load would, without the check
// that its id matches a file name, since the content alone does not say where
// it was read from.
func Parse(content string) (Invariant, error) {
	return parse(content)
}

// ParseBody reads the prose half of an invariant file — the `## Must hold` and
// `## Why` sections — and returns what each says. It is how an invariant written
// as a document is held to the same two sections a file is.
func ParseBody(body string) (statement, rationale string, err error) {
	sections := splitSections(body)
	statement, hasStatement := sections[statementHeading]
	rationale, hasRationale := sections[rationaleHeading]
	if !hasStatement {
		return "", "", errors.New("it has no `## Must hold` section stating what must hold")
	}
	if !hasRationale {
		return "", "", errors.New("it has no `## Why` section stating why the constraint holds")
	}
	return statement, rationale, nil
}

// Render is the file an invariant is written as.
func Render(recorded Invariant) (string, error) {
	return render(recorded)
}

// render writes an invariant back out in the shape parse reads, so an amendment
// produces a file the next load accepts and a hand-written one and a generated
// one are the same document.
func render(recorded Invariant) (string, error) {
	metadata, err := yaml.Marshal(frontmatter{
		ID:            recorded.ID,
		Title:         recorded.Title,
		Status:        recorded.Status,
		EstablishedBy: recorded.EstablishedBy,
		Scope:         recorded.Scope,
		Revisions:     recorded.Revisions,
	})
	if err != nil {
		return "", fmt.Errorf("render invariant %q frontmatter: %w", recorded.ID, err)
	}
	var rendered strings.Builder
	rendered.WriteString(frontmatterFence + "\n")
	rendered.Write(metadata)
	rendered.WriteString(frontmatterFence + "\n\n")
	rendered.WriteString("## Must hold\n\n")
	rendered.WriteString(strings.TrimSpace(recorded.Statement))
	rendered.WriteString("\n\n## Why\n\n")
	rendered.WriteString(strings.TrimSpace(recorded.Rationale))
	rendered.WriteString("\n")
	return rendered.String(), nil
}

// resolve validates the store's roots and returns them. The directory is
// confined to the repository here rather than only where it is configured,
// because this package is what actually reads and writes the filesystem and a
// confinement that holds only when a caller remembered to check is not one.
//
// What comes back is the root itself rather than its path, because the reads
// below go through the same walk the writes do: the lexical check this performs
// says what the configured string is, and only the walk says where it lands.
func (s Store) resolve() (root repowrite.Root, directory string, err error) {
	// The same root the writes are confined to rather than a second reading of it,
	// so a path this package reads from and the path it would write to cannot
	// disagree about where the repository is.
	root, err = repowrite.NewRoot(s.RepositoryRoot)
	if err != nil {
		return repowrite.Root{}, "", err
	}
	directory, err = validateDirectory(s.Directory)
	if err != nil {
		return repowrite.Root{}, "", err
	}
	return root, directory, nil
}

// validateDirectory keeps the invariants directory inside the repository.
// Configuration refuses the same setting when it loads, and this repeats the
// rule rather than relying on it, because this package is what actually reads
// and writes the filesystem: a confinement that holds only when a caller
// remembered to check is not one. A path that escaped the repository would put
// text nobody reviewed with the code in front of every developer as a
// constraint on their change.
func validateDirectory(directory string) (string, error) {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" {
		return "", errors.New("invariants directory is required")
	}
	clean := filepath.Clean(trimmed)
	if filepath.IsAbs(trimmed) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invariants directory %q resolves outside the repository", directory)
	}
	return clean, nil
}

// path returns where an invariant lives, absolute and repository-relative. The
// id is validated first: it becomes a file name, so anything that is not an
// identifier is refused before it reaches the filesystem.
func (s Store) path(id string) (absolute, relative string, err error) {
	trimmed := strings.TrimSpace(id)
	if err := domain.ValidateIdentifier("invariant id", trimmed); err != nil {
		return "", "", err
	}
	// The one name a constraint cannot have, because Load skips it: an invariant
	// written there would be a constraint the harness holds nobody to and nobody
	// can see is missing.
	if strings.EqualFold(trimmed+".md", artifacthome.FileName) {
		return "", "", fmt.Errorf("%s is the directory index rather than an invariant, and is not read as one", artifacthome.FileName)
	}
	root, directory, err := s.resolve()
	if err != nil {
		return "", "", err
	}
	relative = filepath.ToSlash(filepath.Join(directory, trimmed+".md"))
	// Resolved the way the write that follows will resolve it, so what is
	// inspected here for an existing invariant is the file the write would
	// replace rather than whatever the same string reaches through a symlink.
	absolute, err = root.Resolve(relative)
	if err != nil {
		return "", "", err
	}
	return absolute, relative, nil
}

// nestedMarkdown lists the Markdown filed below the invariants directory, which
// is reported rather than read.
func nestedMarkdown(directory string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(directory, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		if strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			return nil
		}
		relative, err := filepath.Rel(directory, candidate)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inspect invariants subdirectory %q: %w", directory, err)
	}
	sort.Strings(found)
	return found, nil
}

func sortByID(invariants []Invariant) {
	sort.Slice(invariants, func(first, second int) bool { return invariants[first].ID < invariants[second].ID })
}

func trimmedList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			trimmed = append(trimmed, cleaned)
		}
	}
	if len(trimmed) == 0 {
		return nil
	}
	return trimmed
}
