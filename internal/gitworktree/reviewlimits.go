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

// MaxReviewDiffAndContextBytes is the most the diff limit and the work item
// context limit may come to together, which leaves 256 KiB of
// MaxReviewInputBytes for every other part of the reviewer's copy. It is a rule
// on the two settings together, beside the bounds each one has.
const MaxReviewDiffAndContextBytes = 512 << 10

// WorkItemContextBytes is the limit on the work item context, which no project
// sets: internal/contextbundle assembles the context under it, and the diff
// limit is added to it here. With it fixed, the highest diff limit a project may
// set is 256 KiB, the default.
const WorkItemContextBytes = 256 << 10

// The bounds on each limit a project may set.
const (
	MinReviewDiffBytes = 64 << 10
	MaxReviewDiffBytes = 512 << 10
	MinReviewFileBytes = 16 << 10
	MaxReviewFileBytes = 128 << 10
	MinReviewFiles     = 50
	MaxReviewFiles     = 400
)

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

// WithDefaults is the limits with every one left unset at its default, as the
// rendering applies them, for a reader that has to state the numbers a change is
// held to. Negative limits, which the rendering refuses, are returned as given.
func (l DiffLimits) WithDefaults() DiffLimits {
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
// the bound it was held to, and what is wrong with the diff limit, set or not,
// beside the work item context limit. No bound is held on a limit left unset.
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
	if l.DiffKiB <= MaxReviewDiffBytes>>10 {
		if problem := ReviewDiffAndContextProblem(prefix, l.DiffLimits().WithDefaults().MaxTotalBytes, WorkItemContextBytes); problem != "" {
			problems = append(problems, problem)
		}
	}
	return problems
}

// ReviewDiffAndContextProblem says what is wrong with a diff limit and a work
// item context limit that together leave too little of the reviewer's copy for
// everything else, naming both values and what they may come to, or nothing
// where the pair is allowed.
func ReviewDiffAndContextProblem(prefix string, diffBytes, contextBytes int) string {
	if diffBytes+contextBytes <= MaxReviewDiffAndContextBytes {
		return ""
	}
	return fmt.Sprintf("%s.%s is %d KiB and the work item context limit is %d KiB, and together they may come to at most %d KiB, so the rest of what the reviewer is shown has room",
		prefix, ReviewLimitDiffKey, diffBytes>>10, contextBytes>>10, MaxReviewDiffAndContextBytes>>10)
}

// ReviewLimitProblem says what is wrong with one limit's value, naming the
// limit and its bound, or nothing where the value is inside the bound. Unlike
// Problems it treats zero as a value, so a reader that can tell a limit written
// as zero from one left out refuses the first.
func ReviewLimitProblem(prefix, key string, value int) string {
	lowest, highest, unit := 0, 0, " KiB"
	switch key {
	case ReviewLimitDiffKey:
		lowest, highest = MinReviewDiffBytes>>10, MaxReviewDiffBytes>>10
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
	return fmt.Sprintf("%s.%s is %d, and it must be between %d and %d%s", prefix, key, value, lowest, highest, unit)
}
