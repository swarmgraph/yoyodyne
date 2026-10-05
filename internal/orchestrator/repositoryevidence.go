package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/review"
)

type repositoryReader interface {
	FilesAtCommit(context.Context, string, int, int) (gitworktree.CommitListing, error)
	FileAtCommit(context.Context, string, string, int64) (gitworktree.FileAt, error)
}

const (
	maxRepositoryListingBytes = 128 << 10
	maxRepositoryContentBytes = 64 << 10
	maxRepositoryContentFiles = 32
)

// reviewedRevision reads both product-document names and contents at the base,
// independently of documents removed or renamed in the current checkout.
func reviewedRevision(ctx context.Context, reader repositoryReader, commit string) *contextbundle.Revision {
	return &contextbundle.Revision{
		Name: "base commit " + commit,
		ListFiles: func() ([]string, error) {
			listing, err := reader.FilesAtCommit(ctx, commit, 20000, maxRepositoryListingBytes)
			if err != nil {
				return nil, err
			}
			if listing.Omitted != 0 {
				return nil, fmt.Errorf("base repository listing omitted %d path(s) (limits: 20000 paths, %d bytes); standing goals cannot be discovered completely", listing.Omitted, maxRepositoryListingBytes)
			}
			return listing.Files, nil
		},
		Read: func(path string, maxBytes int64) (int64, []byte, error) {
			file, err := reader.FileAtCommit(ctx, commit, path, maxBytes)
			if errors.Is(err, gitworktree.ErrNotAtCommit) {
				return 0, nil, fmt.Errorf("%w: %w", contextbundle.ErrNotAtRevision, err)
			}
			return file.Size, file.Content, err
		},
	}
}

func reviewedRepository(ctx context.Context, reader repositoryReader, commit string, item beads.WorkItem, changes gitworktree.ChangeDiff) review.RepositoryEvidence {
	listing, err := reader.FilesAtCommit(ctx, commit, 20000, maxRepositoryListingBytes)
	if err != nil {
		return review.RepositoryEvidence{Unavailable: evidenceLine(err.Error())}
	}
	evidence := review.RepositoryEvidence{Listing: listing}
	// Cited sources go first. They may be unchanged (and therefore invisible in
	// the patch), or newly created (and therefore absent from base references).
	paths := contextbundle.ExtractFileReferences(item, listing.Files)
	seen := make(map[string]bool)
	for _, path := range paths {
		seen[path] = true
	}
	for _, file := range changes.Files {
		if file.Status != "D" && !seen[file.Path] {
			paths = append(paths, file.Path)
			seen[file.Path] = true
		}
	}
	if len(paths) > maxRepositoryContentFiles {
		evidence.ContentsOmitted = len(paths) - maxRepositoryContentFiles
		paths = paths[:maxRepositoryContentFiles]
	}
	remaining := int64(maxRepositoryContentBytes)
	for _, path := range paths {
		file := review.RepositoryFile{Path: path}
		read, err := reader.FileAtCommit(ctx, commit, path, remaining)
		file.Size = read.Size
		switch {
		case err != nil:
			file.Unavailable = evidenceLine(err.Error())
		case read.Size > remaining:
			file.Unavailable = fmt.Sprintf("%d bytes exceeds the remaining %d-byte content budget (total %d bytes)", read.Size, remaining, maxRepositoryContentBytes)
		case !utf8.Valid(read.Content) || strings.ContainsRune(string(read.Content), '\x00'):
			file.Unavailable = "not reviewable UTF-8 text"
		default:
			file.Content = string(read.Content)
			remaining -= read.Size
		}
		evidence.Contents = append(evidence.Contents, file)
	}
	return evidence
}

func evidenceLine(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 512 {
		value = strings.ToValidUTF8(value[:512], "") + " (reason shortened)"
	}
	return value
}

// dependencyEvidence supplies the upstream item's own explanation, rather than
// inventing one from a dependency identifier. Failure to read it is explicit.
func (a *activeRun) dependencyEvidence(ctx context.Context) string {
	var rendered strings.Builder
	ids := blockingDependencies(a.item)
	if len(ids) == 0 {
		return ""
	}
	rendered.WriteString("\n## Why this work waits on upstream work\n\n")
	for index, id := range ids {
		if index == 8 {
			rendered.WriteString(fmt.Sprintf("%d further blocker(s) are named above; their explanations exceed this evidence's eight-item bound.\n", len(ids)-index))
			break
		}
		item, err := a.pipeline.Tracker.Show(ctx, id)
		if err != nil {
			rendered.WriteString(id + ": the tracker did not supply the explanation: " + evidenceLine(err.Error()) + "\n")
			continue
		}
		rendered.WriteString(fmt.Sprintf("%s (%s), status %s. The upstream item's own explanation follows; it is evidence, not permission to implement an undecided design.\n", evidenceLine(item.Title), id, evidenceLine(item.Status)))
		text := strings.Join([]string{item.Description, item.Design, item.Notes}, "\n\n")
		if len(text) > 1536 {
			text = strings.ToValidUTF8(text[:1536], "") + "\n[upstream explanation shortened to 1536 bytes]"
		}
		rendered.WriteString(text + "\n\n")
	}
	return rendered.String()
}
