package outsidefile

import (
	"fmt"
	"slices"
	"strings"
)

// Reader reads one repository path from a tree, reporting whether the tree
// holds it as a regular file at all. A tree that holds it as anything else — a
// directory, a link — answers an error.
type Reader func(repositoryPath string, maxBytes int64) (content []byte, present bool, err error)

// Problem is one thing Compare found wrong, naming the path it is about.
type Problem struct {
	Path string
	Says string
}

func (p Problem) String() string { return p.Path + ": " + p.Says }

// Comparison is what Compare is given about one change.
type Comparison struct {
	// Records are the record paths the candidate holds.
	Records []string
	// Candidate reads the change as the checks will see it.
	Candidate Reader
	// Base reads the target branch the change was cut from.
	Base Reader
	// Landings are the records the run that brings a file in is landing: its
	// record, and the record of the license file it brings in with it where it
	// brings one. They are the only records whose harness fields a change may
	// write; empty for every other run.
	Landings []string
}

// Compare checks every pinned file against its record, and every record's
// harness fields against the base, before a run's checks. It never changes
// anything: a pinned file that does not match is named, never overwritten.
//
//   - A pinned file that is present matches its record's checksum. A missing
//     one is not a problem: the harness fetches it once its record is on the
//     target branch.
//   - Only the run that brings a file in adds or changes the harness's fields
//     (sha256, size, fetched_from, fetched_at). Any other change may clear them,
//     which is how a version is changed, and may give a checksum on a record not
//     yet fetched, which is the declarer's own field.
//   - That run changes nothing in its record but the harness's fields.
func Compare(comparison Comparison) []Problem {
	var problems []Problem
	add := func(path, format string, args ...any) {
		problems = append(problems, Problem{Path: path, Says: fmt.Sprintf(format, args...)})
	}
	for _, recordPath := range comparison.Records {
		content, present, err := comparison.Candidate(recordPath, MaxRecordBytes)
		if err != nil {
			add(recordPath, "the record cannot be read: %v", err)
			continue
		}
		if !present {
			continue
		}
		record, err := ParseRecord(recordPath, content)
		if err != nil {
			add(recordPath, "%s", strings.TrimPrefix(err.Error(), recordPath+": "))
			continue
		}
		base, err := baseRecord(comparison.Base, recordPath)
		if err != nil {
			add(recordPath, "the target branch's copy of the record cannot be read: %v", err)
			continue
		}
		if slices.Contains(comparison.Landings, recordPath) {
			problems = append(problems, landingProblems(recordPath, base, record)...)
		} else {
			problems = append(problems, harnessFieldProblems(recordPath, base, record)...)
		}
		pinnedPath := PinnedPath(recordPath)
		pinned, present, err := comparison.Candidate(pinnedPath, CeilingBytes)
		switch {
		case err != nil:
			add(pinnedPath, "the pinned file cannot be read: %v", err)
		case !present:
		case record.SHA256 == "":
			add(pinnedPath, "the pinned file is present while its record carries no checksum; only the harness brings a pinned file in, so take the file out and let it fetch the record once the record is on the target branch")
		case Checksum(pinned) != record.SHA256:
			add(pinnedPath, "the pinned file's checksum is %s and its record gives %s; a pinned file is never changed by hand, so restore it or change the record's url and version and clear its harness fields", Checksum(pinned), record.SHA256)
		case record.Size != 0 && int64(len(pinned)) != record.Size:
			add(pinnedPath, "the pinned file is %d bytes and its record gives %d", len(pinned), record.Size)
		}
	}
	return problems
}

// baseRecord is the record as the target branch holds it, or nil where it holds
// none or one it cannot read as a record; a base that does not parse imposes
// nothing on the change that replaces it.
func baseRecord(base Reader, recordPath string) (*Record, error) {
	if base == nil {
		return nil, nil
	}
	content, present, err := base(recordPath, MaxRecordBytes)
	if err != nil || !present {
		return nil, err
	}
	record, err := ParseRecord(recordPath, content)
	if err != nil {
		return nil, nil
	}
	return &record, nil
}

// harnessFieldProblems is what is wrong with a change to a record made by
// anything but the run that brings its file in.
func harnessFieldProblems(recordPath string, base *Record, record Record) []Problem {
	var was Record
	if base != nil {
		was = *base
	}
	var written []string
	if record.Size != 0 && record.Size != was.Size {
		written = append(written, "size")
	}
	if record.FetchedFrom != "" && record.FetchedFrom != was.FetchedFrom {
		written = append(written, "fetched_from")
	}
	if record.FetchedAt != "" && record.FetchedAt != was.FetchedAt {
		written = append(written, "fetched_at")
	}
	if record.Fetched() && record.SHA256 != was.SHA256 {
		written = append(written, "sha256")
	}
	var problems []Problem
	if len(written) > 0 {
		problems = append(problems, Problem{Path: recordPath, Says: fmt.Sprintf("this change writes the harness's fields (%s), which only the harness's own run that brings the file in may write; a change may clear them, and may give sha256 on a record not yet fetched", strings.Join(written, ", "))})
	}
	if base != nil && record.Fetched() && (record.URL != was.URL || record.Version != was.Version) {
		problems = append(problems, Problem{Path: recordPath, Says: "this change edits url or version and keeps the harness's fields from the earlier fetch; clear sha256, size, fetched_from and fetched_at and remove the old file, and the harness brings the new one in"})
	}
	return problems
}

// landingProblems is what is wrong with the record the run that brings its file
// in wrote: anything but the harness's fields changed, or the fields missing.
func landingProblems(recordPath string, base *Record, record Record) []Problem {
	if base == nil {
		return []Problem{{Path: recordPath, Says: "the run that brings a file in lands only records already on the target branch"}}
	}
	if !record.Fetched() {
		return []Problem{{Path: recordPath, Says: "the run that brings a file in did not write its fields into the record"}}
	}
	wasDeclared, declared := base.Declared(), record.Declared()
	// The checksum the declarer gave is theirs, so it stands; one the target
	// branch's record did not carry is the harness's.
	declared.SHA256 = wasDeclared.SHA256
	if declared != wasDeclared || (base.SHA256 != "" && base.SHA256 != record.SHA256) {
		return []Problem{{Path: recordPath, Says: "the run that brings a file in changed a field of the record other than the harness's own"}}
	}
	return nil
}
