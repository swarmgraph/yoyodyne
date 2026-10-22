// Package governeddoc decides where a defect in a governed document surfaces.
//
// The repository-coupled gates read this project's own brief, goals, designs,
// decision records, and invariants, and fail when one of them is malformed.
// That is worth having: the class they catch — a goal hard-wrapped across two
// physical lines, a `supports` entry naming a document nobody wrote — is
// mechanical, invisible to a reader, and has occurred. What it cost is the other
// half of the same arrangement. Those documents live in the artifact homes, and
// every one of those homes is refused in a developer's diff
// (internal/protectedpath), so one malformed document turned every developer
// run red over a defect no developer could fix, with the only route out an
// amendment proposal made while every run stayed red.
//
// So a defect is routed rather than merely reported. What decides the route is
// where the document lives: a defect in a directory the harness protects is the
// owning role's, and one anywhere else belongs to the change in hand. The first
// is escalated — named in full, with the role that owns it and the way to reach
// them, and failing nothing — and the second still fails, because it is a defect
// the change in hand can put right.
//
// The ownership is read from the artifact homes rather than restated here, for
// the reason every other reading of that table is: a project that files its
// designs somewhere else has moved the home rather than changed whose it is, and
// a second copy of the table is a second answer to who owns a document.
//
// One class is not routed here, and it is worth naming rather than discovering.
// A file the loader refuses to read at all looks the same whether the loader was
// tightened or the document was broken, and the gates fail on it rather than
// escalating it (internal/cli/governed_documents_repository_test.go), because a
// loader that stopped accepting what this repository ships is code and would
// otherwise reach nobody. What passes through here is what a successful read
// reported beside the set it produced.
package governeddoc

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
)

// Defect is one thing a check found wrong with one document, in the words the
// check found it in. It carries no kind: each reader upstream of here has a
// problem type of its own, and folding them into one enumeration would be a
// further place for them to disagree about what they found.
type Defect struct {
	// Path is the file somebody has to open, repository-relative and in slash
	// form. For a broken relationship that is the document writing it rather
	// than the one it misses, which is what every reader here already reports.
	Path string
	// Detail is what is wrong with it.
	Detail string
}

// Routed is one defect with the role that may put it right and the way to reach
// them.
type Routed struct {
	Defect
	// Home is the artifact home the document is filed in, and is empty for a
	// document no home claims.
	Home string
	// Owner is the role that may change the document. It is empty for a
	// protected path that is nobody's document — the harness's own configuration
	// directory is one — where the route names the role that admits the fix.
	Owner domain.AgentRole
	// Yours reports the defect being in a document the change in hand may edit,
	// which is the whole of what decides whether it fails. A change that was
	// granted one of these paths meets the same defect through the reviewer and
	// through the run's own refusal evidence rather than through here: the grant
	// lives in the work item, which a check running in a worktree cannot read.
	Yours bool
	// Route is what somebody meeting this defect does about it, and is empty on
	// one that is theirs to fix — there the fix is the defect itself.
	Route string
}

// String is the whole of what a report says: the file, what is wrong with it,
// and — where it is not the reader's to fix — whose it is and how to raise it.
// The route is on its own line because it is the line somebody acts on, and one
// long sentence carrying both buries it.
func (r Routed) String() string {
	reported := r.Path + ": " + r.Detail
	if r.Yours {
		return reported
	}
	return reported + "\n\t" + r.Route
}

// Route sorts defects into the ones the change in hand may put right and the
// ones only an owning role may. Every defect comes back, in the order it was
// given: what changes is what is said about it, and dropping the ones nobody
// here can fix is how a defect stops being reported at all.
func Route(cfg config.Config, defects ...Defect) []Routed {
	homes := artifacthome.Homes(cfg)
	routed := make([]Routed, 0, len(defects))
	for _, defect := range defects {
		entry := Routed{Defect: Defect{Path: normalize(defect.Path), Detail: defect.Detail}, Yours: true}
		if home, found := homeOf(homes, entry.Path); found {
			entry.Home, entry.Owner = home.Directory, home.Owner
		}
		// Protected rather than an equivalent test written out here, so a caller
		// that has to ask the same question ahead of time is asking this code and
		// not a second copy of it.
		if Protected(cfg, entry.Path) {
			entry.Yours = false
			entry.Route = route(entry.Home, entry.Owner)
		}
		routed = append(routed, entry)
	}
	return routed
}

// Protected reports whether a defect in this document would be escalated rather
// than failed — which is to say whether the change in hand may edit the file at
// all. It is the predicate Route decides Yours by, asked on its own.
//
// It is exported for the caller that has to decide which documents are its to
// answer for before it reads them rather than after: `yoyo artifact check` walks
// the whole repository for links, because resolving one needs the document at
// its other end, and answers only for the links written in a document no
// developer may edit. Asking here rather than comparing paths of its own is what
// keeps one answer to that question, so the gate that escalates a defect and the
// gate that fails on it cannot come to disagree about which defects those are.
//
// It is the protected paths rather than the artifact homes deliberately. The
// homes are where the owned documents live, but the harness's own configuration
// directory is protected and no home claims it — and it holds Markdown, the
// personas among it. Filtering by home would leave a broken link there escalated
// by every gate and failed by none, which is the hole this whole arrangement is
// built to avoid.
func Protected(cfg config.Config, candidate string) bool {
	// Nothing is granted, on purpose: a check runs in a worktree and has no work
	// item to read a grant out of, and inventing one would be the gate granting
	// itself the path the gate exists to hold.
	return len(protectedpath.Protect(cfg).Refused([]string{normalize(candidate)}, nil)) > 0
}

// Report hands each defect to whichever reporter it belongs to: fail for one the
// change in hand may put right, and escalate for one only an owner may. They are
// passed in rather than decided here because what "fail" means belongs to the
// caller — at every call site today it is a test's Errorf, and the package
// holding the routing rule is not the package that should know that. Escalate
// below is what every one of them passes for the other half.
func Report(cfg config.Config, defects []Defect, fail, escalate func(string, ...any)) {
	for _, entry := range Route(cfg, defects...) {
		// Reported one at a time rather than as a tally: what somebody has to open
		// is one place in one file, and a count sends them looking for it.
		if entry.Yours {
			fail("%s", entry)
			continue
		}
		escalate("%s", entry)
	}
}

// IndexDefects is one defect for each artifact home whose index is not what it
// should be, each made against that home's own index file.
//
// The path is the whole of what Route decides the owner by, so each defect has to
// name the index it is about: one defect against a single home for every index
// would send a missing designs index to the product manager. It is here rather
// than at each caller so that `yoyo artifact check`, which fails on these, and
// the doctor's repository gate, which escalates them, build the same defects.
func IndexDefects(statuses []artifacthome.Status) []Defect {
	var defects []Defect
	for _, status := range statuses {
		if status.Written() {
			continue
		}
		detail := "its index is " + string(status.State)
		if status.Detail != "" {
			detail += ": " + status.Detail
		}
		defects = append(defects, Defect{Path: status.Path, Detail: detail + " (`yoyo setup` writes it)"})
	}
	return defects
}

// EscalationPrefix opens every escalated defect. It is a fixed, searchable
// string rather than prose so that the step in `make test` that prints the
// escalations can pick them out of a test's output without reading the wording.
const EscalationPrefix = "defect in a governed document (escalated; not this change's to fix):"

// Escalate is where an escalated defect is written: the process's own standard
// error, at the start of a line, rather than the test log, which would indent
// it behind a file name.
//
// Writing it is not the same as anybody seeing it. `go test ./...` discards
// everything a passing package printed, its standard error included, and every
// gate that routes through here is meant to pass on a defect it found. So
// `make test` runs those gates once more with `-v` after the suite and prints
// each escalation, or says there was none (the Makefile's
// GOVERNED_DOCUMENT_GATES), and repository_test.go here fails if that step and
// the gates that call this stop matching. Without that step an escalation would
// be the silence a red build was traded for.
func Escalate(format string, args ...any) {
	escalateTo(os.Stderr, format, args...)
}

// escalateTo is Escalate with its writer named, so what an escalation looks
// like can be held without capturing the process's standard error.
func escalateTo(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, EscalationPrefix+" "+format+"\n", args...)
}

// route is what a defect nobody here may fix says to do about it. It names the
// owning role, says plainly that editing the document is not the way, and gives
// the command that reaches the owner — which is the same route the index at the
// door of the home states, from the same place, so the two cannot drift apart.
func route(home string, owner domain.AgentRole) string {
	if strings.TrimSpace(string(owner)) == "" {
		// A protected path that is no role's document: the harness's own
		// configuration directory. No role owns it to amend, and a change may edit
		// it only where its work item grants it, so the fix is work for the Lead
		// Product Manager to admit with that grant.
		return fmt.Sprintf("this path is protected and is no role's document to amend: a change may edit it only where its work item grants it, "+
			"so name the defect in the run's summary for the %s to admit the fix, and leave the file alone", domain.RoleProductManager.Title())
	}
	return fmt.Sprintf("the %s owns %s, so this is not a developer's to fix: propose the amendment and leave the document alone, "+
		"`yoyo amendment list` is what is waiting, and `%s` is where the owner acts",
		owner.Title(), home, artifacthome.ConversationCommand(owner))
}

// homeOf finds the artifact home a document is filed in, longest directory
// first: the goals sit inside the specifications home and the invariants inside
// the decisions home, so the first prefix that matches is not the right answer.
func homeOf(homes []artifacthome.Home, candidate string) (artifacthome.Home, bool) {
	var found artifacthome.Home
	matched := false
	for _, home := range homes {
		if !within(candidate, home.Directory) {
			continue
		}
		if !matched || len(home.Directory) > len(found.Directory) {
			found, matched = home, true
		}
	}
	return found, matched
}

// within reports a path being the directory or sitting inside it. The separator
// is required, so "docs/products" is not inside "docs/product" — the same
// comparison the protected paths make, for the same reason.
func within(candidate, directory string) bool {
	if directory == "" {
		return false
	}
	return candidate == directory || strings.HasPrefix(candidate, directory+"/")
}

// normalize is the repository-relative slash form every comparison here is made
// in. A path it cannot make sense of is returned trimmed rather than dropped:
// this is a reporting path, and a defect silently discarded over the shape of
// its own file name is a defect nobody is told about.
func normalize(value string) string {
	trimmed := strings.Trim(strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")), "/")
	if trimmed == "" {
		return value
	}
	return path.Clean(trimmed)
}
