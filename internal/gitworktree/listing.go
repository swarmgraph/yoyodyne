package gitworktree

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// CommitListing names the paths a committed tree holds. A partial listing can
// prove presence, but cannot prove absence.
type CommitListing struct {
	Commit  string
	Files   []string
	Omitted int
}

// FilesAtCommit reads the committed tree, including binary files and symlinks
// that a text patch cannot establish the presence of. Both bounds apply to the
// rendered paths, with room for a newline after each one.
func (m *Manager) FilesAtCommit(ctx context.Context, commit string, maxFiles, maxBytes int) (CommitListing, error) {
	if !commitPattern.MatchString(commit) || maxFiles <= 0 || maxBytes <= 0 {
		return CommitListing{}, fmt.Errorf("a full commit and positive listing bounds are required")
	}
	// Git's quoted path format survives the process runner's line splitting,
	// including filenames containing carriage returns or newlines. A NUL-only
	// stream would also be one enormous line in a large repository.
	result, err := m.run(ctx, "-C", m.repositoryRoot, "-c", "core.quotepath=true", "ls-tree", "-r", "--name-only", commit)
	if err != nil {
		return CommitListing{}, err
	}
	if result.Status != execution.ProcessSucceeded {
		return CommitListing{}, fmt.Errorf("list files at %s failed with exit code %d: %s", commit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	if result.OutputTruncation != "" {
		return CommitListing{}, fmt.Errorf("repository listing could not be read whole: %s", result.OutputTruncation)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		file := line
		if strings.HasPrefix(line, "\"") {
			file, err = strconv.Unquote(line)
			if err != nil {
				return CommitListing{}, fmt.Errorf("repository listing contains an unreadable quoted path: %w", err)
			}
		} else {
			// With core.quotepath enabled, non-ASCII bytes are always escaped.
			// This also refuses the runner's Unicode line-truncation marker.
			for _, value := range []byte(line) {
				if value < 32 || value > 126 {
					return CommitListing{}, fmt.Errorf("repository listing contains an unreadable or shortened path")
				}
			}
		}
		files = append(files, file)
	}
	sort.Strings(files)
	listing := CommitListing{Commit: commit}
	bytes := 0
	for _, file := range files {
		size := len(strconv.Quote(file)) + 1
		if len(listing.Files) == maxFiles || bytes+size > maxBytes {
			break
		}
		listing.Files = append(listing.Files, file)
		bytes += size
	}
	listing.Omitted = len(files) - len(listing.Files)
	return listing, nil
}

// FilesUnderAtCommit names every path a committed tree holds under one folder,
// sorted, without any bound on the count: the folder is what the caller needs
// read whole, so a listing cut short is an error naming the folder and never a
// shortened answer. A folder the commit does not hold lists nothing.
func (m *Manager) FilesUnderAtCommit(ctx context.Context, commit, folder string) ([]string, error) {
	folder = strings.Trim(folder, "/")
	if !commitPattern.MatchString(commit) || folder == "" || folder == ".." || strings.HasPrefix(folder, "../") || strings.ContainsAny(folder, "\x00\\") {
		return nil, fmt.Errorf("a full commit and a folder inside the repository are required")
	}
	result, err := m.run(ctx, "-C", m.repositoryRoot, "-c", "core.quotepath=true", "ls-tree", "-r", "--name-only", commit, "--", folder+"/")
	if err != nil {
		return nil, fmt.Errorf("list %s at %s: %w", folder, commit, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list %s at %s failed with exit code %d: %s", folder, commit, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	if result.OutputTruncation != "" {
		return nil, fmt.Errorf("%s could not be read whole at %s: %s", folder, commit, result.OutputTruncation)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		file := line
		if strings.HasPrefix(line, "\"") {
			file, err = strconv.Unquote(line)
			if err != nil {
				return nil, fmt.Errorf("%s holds an unreadable quoted path: %w", folder, err)
			}
		} else {
			for _, value := range []byte(line) {
				if value < 32 || value > 126 {
					return nil, fmt.Errorf("%s holds an unreadable or shortened path", folder)
				}
			}
		}
		files = append(files, file)
	}
	sort.Strings(files)
	return files, nil
}
