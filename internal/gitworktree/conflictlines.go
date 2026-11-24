package gitworktree

// How large a conflict is: the number of lines the two sides disagree about,
// counted while the conflict still stands in a checkout's index, or from what a
// merge in memory left unmerged.
//
// The count is made from the versions Git left unmerged rather than from the
// markers it wrote in the files, because a file can hold lines that look like
// markers without being any. Each conflicting file is merged again in memory
// with markers far longer than any text holds, and every line of either side
// between them is counted; a common ancestor's lines, which Git also writes
// under some conflict styles, are not.
//
// A file one side deleted, or that is not text, has no such lines. Counting it
// as none would make the conflict look smaller than it is, so the whole count
// is then not known, and says which file it stopped on.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const (
	conflictMarkerSize = 64
	// maxCountedConflicts bounds how many files are merged again to be counted.
	maxCountedConflicts = 200
	// emptyBlob stands for the common ancestor of a file both sides added. Git
	// answers it whether or not the repository holds it.
	emptyBlob = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
)

// ConflictLines is how many lines a conflict covers. Lines is nil where that
// is not known, and Unknown then says why.
type ConflictLines struct {
	Lines   *int
	Unknown string
}

func unknownConflictLines(format string, args ...any) ConflictLines {
	return ConflictLines{Unknown: fmt.Sprintf(format, args...)}
}

// conflictLines counts the conflict standing in the checkout at path.
func (m *Manager) conflictLines(ctx context.Context, path string) ConflictLines {
	listed, err := m.run(ctx, "-C", path, "ls-files", "--unmerged", "-z")
	if err != nil {
		return unknownConflictLines("the conflicting files could not be listed: %v", err)
	}
	if listed.Status != execution.ProcessSucceeded || listed.OutputTruncation != "" {
		return unknownConflictLines("the conflicting files could not be listed")
	}
	order, stages := unmergedFiles(listed.Stdout)
	return m.countConflictLines(ctx, path, order, stages)
}

// unmergedFiles reads Git's NUL-ended listing of unmerged files: each file once,
// in the order listed, and the blob of each of its stages.
func unmergedFiles(listing string) ([]string, map[string]*[4]string) {
	stages := map[string]*[4]string{}
	var order []string
	for _, entry := range strings.Split(listing, "\x00") {
		described, name, found := strings.Cut(entry, "\t")
		fields := strings.Fields(described)
		if !found || len(fields) != 3 || len(fields[2]) != 1 || fields[2] < "1" || fields[2] > "3" {
			continue
		}
		if stages[name] == nil {
			stages[name] = &[4]string{}
			order = append(order, name)
		}
		stages[name][fields[2][0]-'0'] = fields[1]
	}
	return order, stages
}

// countConflictLines counts the lines in conflict across the unmerged files,
// asking Git in the repository or checkout at path.
func (m *Manager) countConflictLines(ctx context.Context, path string, order []string, stages map[string]*[4]string) ConflictLines {
	switch {
	case len(order) == 0:
		return unknownConflictLines("Git listed no conflicting file")
	case len(order) > maxCountedConflicts:
		return unknownConflictLines("%d files conflict, which is more than the %d that are counted", len(order), maxCountedConflicts)
	}
	total := 0
	for _, name := range order {
		base, ours, theirs := stages[name][1], stages[name][2], stages[name][3]
		if ours == "" || theirs == "" {
			return unknownConflictLines("%s is kept by one side and not the other", name)
		}
		if base == "" {
			base = emptyBlob
		}
		merged, err := m.run(ctx, "-C", path, "merge-file", "--stdout", "--object-id", fmt.Sprintf("--marker-size=%d", conflictMarkerSize), ours, base, theirs)
		if err != nil {
			return unknownConflictLines("%s could not be counted: %v", name, err)
		}
		// Git exits with the number of conflicts, and above 127 on a failure.
		conflicted := merged.Status == execution.ProcessFailed && merged.ExitCode > 0 && merged.ExitCode < 128
		if (merged.Status != execution.ProcessSucceeded && !conflicted) || merged.OutputTruncation != "" {
			return unknownConflictLines("%s could not be counted", name)
		}
		total += linesInConflict(merged.Stdout)
	}
	if total == 0 {
		return unknownConflictLines("the files conflict in something other than their lines")
	}
	return ConflictLines{Lines: &total}
}

// linesInConflict counts the lines of either side inside the conflicts of one
// file merged with markers of conflictMarkerSize.
func linesInConflict(merged string) int {
	marker := func(line, mark string) bool { return strings.HasPrefix(line, strings.Repeat(mark, conflictMarkerSize)) }
	total, inside, ancestor := 0, false, false
	for _, line := range strings.Split(merged, "\n") {
		switch {
		case marker(line, "<"):
			inside, ancestor = true, false
		case marker(line, "|"):
			ancestor = true
		case marker(line, "="):
			ancestor = false
		case marker(line, ">"):
			inside = false
		case inside && !ancestor:
			total++
		}
	}
	return total
}
