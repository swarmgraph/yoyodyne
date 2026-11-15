package review

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// RepositoryEvidence is the reviewed commit's tree and bounded, whole copies
// of files cited by the item. Base-revision references remain separate: those
// describe what the change was written against, rather than what it produced.
type RepositoryEvidence struct {
	Listing         gitworktree.CommitListing
	Unavailable     string
	Contents        []RepositoryFile
	ContentsOmitted int
}

type RepositoryFile struct {
	Path        string
	Size        int64
	Content     string
	Unavailable string
}

func (e RepositoryEvidence) refute(verdict Verdict, changes gitworktree.ChangeDiff) error {
	deleted := make(map[string]bool)
	for _, file := range changes.DeletedFiles {
		deleted[file.Path] = file.Whole
	}
	for _, file := range changes.Files {
		deleted[file.Path] = file.Status == "D"
	}
	presentPaths := make(map[string]bool, len(e.Listing.Files)+len(changes.Files))
	presentAtTip := func(path string) {
		if path != "" {
			presentPaths[path] = true
		}
	}
	presentAtHead := func(path string) {
		if !deleted[path] {
			presentAtTip(path)
		}
	}
	for _, file := range e.Listing.Files {
		presentAtHead(file)
	}
	// A caller may review uncommitted files above the listed HEAD.
	for _, file := range changes.Files {
		if file.Status != "D" {
			presentAtTip(file.Path)
		}
	}
	// These sections are delivered independently of the changed-file listing's
	// bound. A digest proves even a zero-byte file is present; zero bytes without
	// a digest or an irregular-file marker may instead describe a deletion.
	for _, file := range changes.UntrackedFiles {
		presentAtTip(file)
	}
	for _, file := range changes.OmittedFiles {
		if file.Digest != "" || file.Bytes > 0 || file.Undigestable {
			presentAtTip(file.Path)
		}
	}
	for _, file := range changes.DeletedFiles {
		if !file.Whole {
			presentAtTip(file.Path)
		}
	}
	for _, file := range e.Contents {
		if file.Unavailable == "" {
			presentAtHead(file.Path)
		}
	}
	var problems []error
	for index, finding := range verdict.Findings {
		if finding.Absent == "" {
			continue
		}
		claimed, err := canonicalAbsentPath(finding.Absent)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		holds := presentPaths[claimed]
		if !holds {
			for file := range presentPaths {
				if strings.HasPrefix(file, claimed+"/") {
					holds = true
					break
				}
			}
		}
		switch {
		case holds:
			problems = append(problems, fmt.Errorf("findings[%d] claims %q is absent, but the reviewed repository holds it", index, finding.Absent))
		case e.Unavailable != "" || e.Listing.Commit == "" || e.Listing.Omitted > 0:
			problems = append(problems, fmt.Errorf("findings[%d] claims %q is absent, but this review has no complete repository listing to check it", index, finding.Absent))
		case changes.FilesOmitted > 0:
			problems = append(problems, fmt.Errorf("findings[%d] claims %q is absent, but this review has no complete change listing to check uncommitted additions", index, finding.Absent))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("unsupported review finding: %w", errors.Join(problems...))
	}
	return nil
}

// bounded spends only the input space left after the change, intent, checks,
// and immutable contract. Every loss is made explicit, and the same shortened
// listing is used to validate absence claims.
func (e RepositoryEvidence) bounded(maxBytes int) RepositoryEvidence {
	return e.boundedAs(renderRepository, maxBytes)
}

// boundedAs is bounded for evidence that is rendered by render rather than by
// renderRepository, so the project's code is trimmed by the same rules against
// the text it is actually shown as.
func (e RepositoryEvidence) boundedAs(render func(RepositoryEvidence) string, maxBytes int) RepositoryEvidence {
	e.Contents = append([]RepositoryFile(nil), e.Contents...)
	for index := len(e.Contents) - 1; len(render(e)) > maxBytes && index >= 0; index-- {
		if e.Contents[index].Unavailable == "" {
			e.Contents[index].Content = ""
			e.Contents[index].Unavailable = "the whole file exceeds the remaining review input budget"
		}
	}
	// Reserve the small growth of the omission marker, then trim in one pass.
	needed := len(render(e)) - maxBytes
	if needed > 0 {
		needed += 64
		for needed > 0 && len(e.Listing.Files) > 0 {
			last := len(e.Listing.Files) - 1
			needed -= len(strconv.Quote(e.Listing.Files[last])) + 1
			e.Listing.Files = e.Listing.Files[:last]
			e.Listing.Omitted++
		}
	}
	if len(render(e)) > maxBytes {
		e = RepositoryEvidence{Unavailable: "the review input budget has no room for repository evidence"}
	}
	return e
}

func renderRepository(e RepositoryEvidence) string {
	return renderRepositorySection(e, repositorySection{
		Heading:      "# Repository at the reviewed commit",
		FileHeading:  "File at reviewed commit",
		WholeFile:    "This is the candidate's content, not the base-revision reference above.",
		ListingLabel: "Committed paths at",
	})
}

// repositorySection is the words that differ between the two repositories a
// review can show: the one holding the reviewed change and the project's own
// code. The listing and the file contents are rendered the same way for both.
type repositorySection struct {
	Heading      string
	Lead         string
	FileHeading  string
	WholeFile    string
	ListingLabel string
}

func renderRepositorySection(e RepositoryEvidence, section repositorySection) string {
	var rendered strings.Builder
	rendered.WriteString("\n" + section.Heading + "\n\n")
	if section.Lead != "" {
		rendered.WriteString(section.Lead + "\n\n")
	}
	if e.Unavailable != "" || e.Listing.Commit == "" {
		rendered.WriteString("No repository listing is available; no absence claim can be checked. " + e.Unavailable + "\n")
		return rendered.String()
	}
	rendered.WriteString(section.ListingLabel + " " + e.Listing.Commit + ", one quoted path per line (including binary files):\n")
	if e.Listing.Omitted > 0 {
		rendered.WriteString(fmt.Sprintf("The listing omitted %d path(s) under its bounds. It proves presence only; do not claim an unlisted path is absent.\n", e.Listing.Omitted))
	} else {
		rendered.WriteString("The committed listing is complete. Absence must be checked here, together with additions in the change evidence, rather than inferred from the patch. A bounded change listing cannot establish an unlisted path's absence from the worktree.\n")
	}
	for _, file := range e.Listing.Files {
		rendered.WriteString(strconv.Quote(file) + "\n")
	}
	if e.ContentsOmitted > 0 {
		rendered.WriteString(fmt.Sprintf("\nWhole content of %d further cited or changed file(s) was not supplied under the 32-file bound; no content claim about them is supported by this section.\n", e.ContentsOmitted))
	}
	for _, file := range e.Contents {
		rendered.WriteString(fmt.Sprintf("\n## %s %s: %s\n\n", section.FileHeading, e.Listing.Commit, file.Path))
		if file.Unavailable != "" {
			rendered.WriteString("Content not supplied: " + file.Unavailable + ". Do not make literal content claims about this file from the patch alone.\n")
			continue
		}
		rendered.WriteString(fmt.Sprintf("Whole file, %d bytes. %s\n\n", file.Size, section.WholeFile))
		rendered.WriteString(file.Content)
		rendered.WriteString("\n")
	}
	return rendered.String()
}

// isEmpty reports evidence nothing was read into, which is how a review that
// carries no project code is told from one that does.
func (e RepositoryEvidence) isEmpty() bool {
	return e.Unavailable == "" && e.Listing.Commit == "" && len(e.Listing.Files) == 0 && e.Listing.Omitted == 0 && len(e.Contents) == 0 && e.ContentsOmitted == 0
}

// hasProjectCode reports whether this review is of a change that lands in a
// companion intent repository, with the project's code read for it.
func (r Request) hasProjectCode() bool {
	return !r.ProjectCode.isEmpty() || r.ProjectCodeCommit != ""
}

// renderProjectCode is the project's own repository as the reviewer of a
// document landing in its companion intent repository is shown it. It renders
// nothing when the review carries no project code.
func renderProjectCode(r Request) string {
	if !r.hasProjectCode() {
		return ""
	}
	e := r.ProjectCode
	if e.Listing.Commit == "" && e.Unavailable == "" && r.ProjectCodeCommit != "" {
		e.Listing.Commit = r.ProjectCodeCommit
	}
	return renderRepositorySection(e, projectCodeSection(r.ProjectCodeCommit))
}

func projectCodeSection(commit string) repositorySection {
	return repositorySection{
		Heading:      "# The project's code at commit " + commit,
		Lead:         "This change lands a document in the project's companion intent repository. Below are the project's own repository's file listing and whole copies of the files the document names, at that commit, so a design or an invariant can be checked against the code it constrains. A claim the document makes about that code is judged against this, and a file it names that is listed as unavailable is unread rather than absent.",
		FileHeading:  "Project file at commit",
		WholeFile:    "This is the project's code at that commit, not part of the change under review.",
		ListingLabel: "Project paths at",
	}
}
