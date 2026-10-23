package chat

// The needs-design label in a conversation (docs/designs/needs-design-gate.md).
// The Lead Product Manager, the development manager, and a program manager in
// its lane put it on, through the label action and a creation's labels, which
// they already hold. Only the architect takes it off, through a capability that
// lets her do that and nothing else to an item she does not own, and only once
// the design the item waits on is readable by the developer who will pull it:
// written into the item's own design field, or landed in the designs home in a
// revision whose reason names the item. A ruling that exists only in the notes
// is not readable in that sense, and does not lift the label.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// needsDesignLabelClause is what each role that may put the needs-design label
// on is told about it, in one constant so the three contracts cannot come to
// say different things.
const needsDesignLabelClause = `The label "` + domain.NeedsDesignLabel + `" holds an item from every developer run the harness would start, as a parking does, until the architect's design for it is readable: put it on an item whose conditions for done are choices about behaviour nobody has made yet, rather than letting a developer guess them, and say in the reason the question the design must answer, because that sentence is what the architect reads. Only the architect takes it off, once the design is in the item's design field or landed in the designs home naming the item, and a "label" removing it from any other role is refused.`

// MaxDesignFieldBytes is the largest design the item's own design field may
// carry and still lift the label. A larger one belongs in the designs home,
// which the design says in as many words; a field past it is not refused, it
// just does not count as the readable design.
const MaxDesignFieldBytes = 8 << 10

// refuseDesignHoldLabel refuses, whole and before anything runs, a label action
// that puts the needs-design label where this role may not or takes it off where
// this role may not. It reads only the block: whether the design is readable yet
// is asked as the removal runs (designHoldLifted).
func refuseDesignHoldLabel(authority Authority, action TrackerAction) error {
	if action.Action != actionLabel {
		return nil
	}
	removing := strings.TrimSpace(action.Remove) == domain.NeedsDesignLabel
	if authority.LabelsOnlyDesignHold && !removing {
		return &AuthorityError{
			Role:    authority.Role,
			Refused: "a label action other than taking the " + domain.NeedsDesignLabel + " label off an item",
			Reason:  "the one change this role makes to an item it does not own is removing " + domain.NeedsDesignLabel + " once the design is readable; putting a label on, " + domain.NeedsDesignLabel + " included, is the Lead Product Manager's, the development manager's, or a program manager's in its lane",
		}
	}
	if removing && !authority.RemovesDesignHold {
		return &AuthorityError{
			Role:    authority.Role,
			Refused: "the " + domain.NeedsDesignLabel + " label taken off an item",
			Reason:  "only the architect removes it, and only once the design the item waits on is in the item's design field or landed in the designs home; ask her, through the ask channel or in prose, rather than removing it",
		}
	}
	return nil
}

// LandedDesign is a design committed in the designs home whose recorded
// revision names a work item: the larger of the two forms that lift the label.
type LandedDesign struct {
	// Path is the design's repository-relative file, and ID its identity.
	Path string
	ID   string
	// Commit is the commit it was read at, which is what "landed" means here.
	Commit string
	// At and Reason are the revision that names the item.
	At     time.Time
	Reason string
}

// LandedDesigns finds a design committed in the designs home whose revision
// reason names the item. Nothing found is not an error.
type LandedDesigns interface {
	NamingItem(ctx context.Context, workItemID string) (LandedDesign, bool, error)
}

// designHoldLifted says how the item's design is readable, as the sentence the
// removal records, or why it is not, as the refusal. Exactly one is non-empty.
func (s *Session) designHoldLifted(ctx context.Context, id string, item *beads.WorkItem, unread string) (string, string) {
	if item == nil {
		if unread == "" {
			unread = "the tracker did not answer"
		}
		return "", fmt.Sprintf("the tracker would not show %s (%s), so whether its design is readable could not be checked and the %s label was left on", id, unread, domain.NeedsDesignLabel)
	}
	design := strings.TrimSpace(item.Design)
	if design != "" && len(design) <= MaxDesignFieldBytes {
		return fmt.Sprintf("the design is in the item's own design field (%d bytes), which every run of the item receives", len(design)), ""
	}
	fieldNote := "its design field is empty"
	if design != "" {
		fieldNote = fmt.Sprintf("its design field holds %d bytes, past the %d a design field may carry, so that design belongs in the designs home", len(design), MaxDesignFieldBytes)
	}
	if s.options.LandedDesigns == nil {
		return "", fmt.Sprintf("%s and this conversation has no way to read the designs home, so the %s label was left on", fieldNote, domain.NeedsDesignLabel)
	}
	landed, found, err := s.options.LandedDesigns.NamingItem(ctx, id)
	if err != nil {
		return "", fmt.Sprintf("%s and the designs home could not be read (%s), so the %s label was left on", fieldNote, singleLine(err.Error(), maxTrackerFailureBytes), domain.NeedsDesignLabel)
	}
	if !found {
		return "", fmt.Sprintf("%s and no design committed in the designs home has a revision whose reason names %s, so the developer who pulls it would have nothing to read; write the design into the item's design field or land it naming the item, then remove the label. A ruling in the notes alone does not lift it, and a design written but not yet landed does not either", fieldNote, id)
	}
	return fmt.Sprintf("the design is landed in %s (%s) at commit %s, whose revision of %s names this item", landed.Path, landed.ID, shortDesignCommit(landed.Commit), landed.At.UTC().Format("2006-01-02")), ""
}

func shortDesignCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// GitLandedDesigns answers LandedDesigns from the commit HEAD names in a
// repository, never its working tree, so a design written and not yet landed
// does not count.
type GitLandedDesigns struct {
	Process execution.ProcessRunner
	// Git is the executable, "git" when empty.
	Git string
	// Directory is the repository the designs home is in: the product's own
	// checkout, or its companion intent repository where it has one.
	Directory string
	// Home is the designs home, relative to Directory.
	Home    string
	Timeout time.Duration
}

const (
	landedDesignTimeout  = 30 * time.Second
	landedDesignMaxBytes = 4 << 20
)

// NamingItem lists the files under the designs home that mention the item at
// HEAD, and reads each one's revision record for a reason that names it.
func (g GitLandedDesigns) NamingItem(ctx context.Context, workItemID string) (LandedDesign, bool, error) {
	id := strings.TrimSpace(workItemID)
	home := strings.Trim(strings.TrimSpace(g.Home), "/")
	if id == "" || home == "" {
		return LandedDesign{}, false, errors.New("no work item or no designs home to look in")
	}
	if g.Process == nil {
		return LandedDesign{}, false, errors.New("no process runner is configured to read the designs home")
	}
	head, err := g.git(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return LandedDesign{}, false, err
	}
	commit := strings.TrimSpace(head.Stdout)
	listed, err := g.git(ctx, "grep", "-l", "-F", "-e", id, commit, "--", home)
	if err != nil {
		// git grep answers 1 for "nothing matched", which is an answer.
		if listed.Status == execution.ProcessFailed && listed.ExitCode == 1 && strings.TrimSpace(listed.Stderr) == "" {
			return LandedDesign{}, false, nil
		}
		return LandedDesign{}, false, err
	}
	for _, line := range strings.Split(listed.Stdout, "\n") {
		file := strings.TrimPrefix(strings.TrimSpace(line), commit+":")
		if file == "" || path.Ext(file) != ".md" {
			continue
		}
		shown, err := g.git(ctx, "show", commit+":"+file)
		if err != nil {
			return LandedDesign{}, false, err
		}
		document, err := artifact.ParseDocument([]byte(shown.Stdout))
		if err != nil {
			// A file in the home that is not an artifact is not a design.
			continue
		}
		for index := len(document.Revisions) - 1; index >= 0; index-- {
			revision := document.Revisions[index]
			if NamesWorkItem(revision.Reason, id) {
				return LandedDesign{Path: file, ID: document.ID, Commit: commit, At: revision.At, Reason: revision.Reason}, true, nil
			}
		}
	}
	return LandedDesign{}, false, nil
}

func (g GitLandedDesigns) git(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	name := strings.TrimSpace(g.Git)
	if name == "" {
		name = "git"
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = landedDesignTimeout
	}
	result, err := g.Process.Run(ctx, execution.Command{
		Name:           name,
		Args:           args,
		Dir:            g.Directory,
		Env:            execution.GitEnvironment(nil),
		Timeout:        timeout,
		MaxOutputBytes: landedDesignMaxBytes,
	}, nil)
	if err != nil {
		return execution.ProcessResult{}, fmt.Errorf("run git %s: %w", args[0], err)
	}
	if result.Status != execution.ProcessSucceeded {
		detail := singleLine(strings.TrimSpace(result.Stderr), maxTrackerFailureBytes)
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", result.ExitCode)
		}
		return result, fmt.Errorf("git %s failed: %s", args[0], detail)
	}
	return result, nil
}

// NamesWorkItem reports text naming the item as a whole identifier: the item
// itself and not a child of it or a longer identifier it is the start of, so a
// revision naming yoyodyne-2kr.21 or yoyodyne-2kr.2.1 does not lift the label on
// yoyodyne-2kr.2. A full stop after the identifier is the end of a sentence.
func NamesWorkItem(text, id string) bool {
	if id == "" {
		return false
	}
	for start := 0; ; {
		found := strings.Index(text[start:], id)
		if found < 0 {
			return false
		}
		at := start + found
		end := at + len(id)
		before := at == 0 || !identifierByte(text[at-1])
		after := end == len(text) || !identifierByte(text[end]) ||
			(text[end] == '.' && (end+1 == len(text) || !alphanumericByte(text[end+1])))
		if before && after {
			return true
		}
		start = at + 1
	}
}

func identifierByte(b byte) bool {
	return alphanumericByte(b) || b == '.' || b == '_' || b == '-'
}

func alphanumericByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
