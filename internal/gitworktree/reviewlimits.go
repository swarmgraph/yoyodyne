package gitworktree

import "fmt"

// The limits on the reviewer's copy of a change, and the bounds a project's
// setting of them is held to (docs/decisions/review-size-limits.md).
//
// A project may set three of the limits DiffLimits carries: the diff in all,
// one new file, and the number of new files. The bounds below are held here,
// beside the defaults, and no configuration moves them: they are what keeps a
// review one session can read whole, so raising them is a change to this code
// and not to a project's file.

// MaxReviewInputBytes is the limit on everything one review is shown together:
// the reviewer's contract, the change, and the item, intent, rules and other
// evidence around it. It is fixed, and no project sets it. review.MaxReviewInputBytes
// is this value; it is held here, beside the limits on the diff, where the
// configuration can read it.
const MaxReviewInputBytes = 768 << 10

// The diff limit a project may set is held to DefaultMaxDiffBytes at most, below
// the 512 KiB (MaxReviewDiffBytes) the ruling allows, because nothing larger
// leaves the rest of the review evidence whole beside it, and today's default
// does not either. The review package's
// TestTheReviewEvidenceBesideTheHighestDiffLimitIsMeasured rebuilds the largest
// review this repository would send: the evidence that does not depend on the
// change measures about 516 KiB, most of it the work item context at its own
// 256 KiB limit, which leaves room for a one-file diff of about 250 KiB; and a
// change of many small files is shown with a status listing, diff stat and file
// listing that add about half a byte for every byte of patch and have no limit
// of their own, which leaves room for about 173 KiB. Both are below the 256 KiB
// default the ruling sets, so the ruling's figures cannot all hold in this
// repository, and which of them gives way is the architect's to decide. Until
// then a project may lower the diff limit and raise the other two, and no
// project can make a review larger than every project's review already is. The
// test fails if this ceiling is ever raised above the default while those
// measurements do not fit.

// The bounds on each limit a project may set.
const (
	MinReviewDiffBytes = 64 << 10
	MaxReviewDiffBytes = 512 << 10
	MinReviewFileBytes = 16 << 10
	MaxReviewFileBytes = 128 << 10
	MinReviewFiles     = 50
	MaxReviewFiles     = 400
)

// ReviewDiffCeiling is the highest diff limit a project may set: the lesser of
// MaxReviewDiffBytes and what the measurement above leaves room for, which is
// the default for now.
func ReviewDiffCeiling() int {
	return min(MaxReviewDiffBytes, DefaultMaxDiffBytes)
}

// ReviewLimits are the limits a project sets on the reviewer's copy of a
// change, in the units the configuration writes them: KiB for the two byte
// limits, a count for the files. A zero field is one the project did not set,
// and takes the default.
//
// They are the project's, read from its configuration by both the code that
// renders the reviewer's copy and the size check in front of the checks, and
// never a run's: nothing gives one run limits of its own.
type ReviewLimits struct {
	DiffKiB int `yaml:"diff_kib,omitempty" json:"diff_kib,omitempty"`
	FileKiB int `yaml:"file_kib,omitempty" json:"file_kib,omitempty"`
	Files   int `yaml:"files,omitempty" json:"files,omitempty"`
}

// DiffLimits is the limits as the rendering of a change reads them. A field the
// project did not set stays zero, which DiffLimits resolves to its default.
func (l ReviewLimits) DiffLimits() DiffLimits {
	return DiffLimits{MaxTotalBytes: l.DiffKiB << 10, MaxFileBytes: l.FileKiB << 10, MaxFiles: l.Files}
}

// Resolved is the limits with every one left unset at its default, as the
// rendering applies them, for a reader that has to state the numbers a change is
// held to. Negative limits, which the rendering refuses, are returned as given.
func (l DiffLimits) Resolved() DiffLimits {
	resolved, err := l.resolve()
	if err != nil {
		return l
	}
	return resolved
}

// The keys the three limits are written under, inside the section that holds
// them.
const (
	ReviewLimitDiffKey  = "diff_kib"
	ReviewLimitFileKey  = "file_kib"
	ReviewLimitFilesKey = "files"
)

// Problems says what is wrong with each limit set outside its bound, naming
// the limit by the key it is written under (prefix and the field's name) and
// the bound it was held to. Nothing is said of a limit left unset.
func (l ReviewLimits) Problems(prefix string) []string {
	var problems []string
	for _, limit := range []struct {
		key   string
		value int
	}{{ReviewLimitDiffKey, l.DiffKiB}, {ReviewLimitFileKey, l.FileKiB}, {ReviewLimitFilesKey, l.Files}} {
		if limit.value == 0 {
			continue
		}
		if problem := ReviewLimitProblem(prefix, limit.key, limit.value); problem != "" {
			problems = append(problems, problem)
		}
	}
	return problems
}

// ReviewLimitProblem says what is wrong with one limit's value, naming the
// limit and its bound, or nothing where the value is inside the bound. Unlike
// Problems it treats zero as a value, so a reader that can tell a limit written
// as zero from one left out refuses the first.
func ReviewLimitProblem(prefix, key string, value int) string {
	lowest, highest, unit, why := 0, 0, " KiB", ""
	switch key {
	case ReviewLimitDiffKey:
		lowest, highest = MinReviewDiffBytes>>10, ReviewDiffCeiling()>>10
		if ReviewDiffCeiling() < MaxReviewDiffBytes {
			why = fmt.Sprintf(", because the %d KiB the reviewer is shown in all leaves no room for a larger diff beside the rest of the review evidence", MaxReviewInputBytes>>10)
		}
	case ReviewLimitFileKey:
		lowest, highest = MinReviewFileBytes>>10, MaxReviewFileBytes>>10
	case ReviewLimitFilesKey:
		lowest, highest, unit = MinReviewFiles, MaxReviewFiles, " files"
	default:
		return fmt.Sprintf("%s.%s is not a limit on the reviewer's copy of a change", prefix, key)
	}
	if value >= lowest && value <= highest {
		return ""
	}
	return fmt.Sprintf("%s.%s is %d, and it must be between %d and %d%s%s", prefix, key, value, lowest, highest, unit, why)
}
