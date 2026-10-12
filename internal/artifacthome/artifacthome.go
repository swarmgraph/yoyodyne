// Package artifacthome answers, at the door of every directory of documents the
// harness governs, the question a newcomer asks there first: what is filed here,
// whose is it, and may I edit one by hand.
//
// The answers already exist and are scattered across the configuration guide,
// the design document, and the harness's own code. What did not exist was
// anything that put them where somebody standing in the directory would meet
// them, so the first thing an operator did with `docs/decisions` was guess. Three
// sentences at the door cost nothing and are the whole of what this package
// writes.
//
// It writes prose rather than governed documents, and that is what makes the
// prose safe to write at all: a `README.md` in an artifact home is exempt from
// artifact identity by name (internal/artifact's indexFileName), because an index
// describes what is filed beside it rather than stating any intent of its own. So
// nothing here creates an artifact, claims an id, or records a revision — every
// one of which would be a mutation only an owning role may make.
//
// Nothing here decides policy either. The hand-edit answer these files state is
// the one the harness already enforces: a non-owner proposes an amendment rather
// than editing, an operator's own edit is reported rather than refused, and what
// a change leaves stale downstream is reported by `yoyo stale`. A README that
// invented a rule would be a fourth place for the rule to disagree with itself.
package artifacthome

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// FileName is the index every artifact home gets. It is the one Markdown name in
// a home that is not an artifact, which is why this is the file the answers go
// in rather than a document of a kind somebody would then have to own.
const FileName = "README.md"

// The markers a written index states its three answers under. They are checked
// for rather than the whole file being compared, because an operator who added a
// paragraph of their own has not thereby broken the index — and a diagnosis that
// reported every customized README as wrong would be one operators learn to skip
// past. What is actually asked is the question the file exists to answer: does it
// still say what is filed here, whose it is, and whether you may edit it.
const (
	purposeMarker  = "**Purpose.**"
	ownerMarker    = "**Owner.**"
	handEditMarker = "**Editing by hand.**"
)

// Home is one directory of governed documents, and what its index says about it.
type Home struct {
	// Directory is repository-relative, in slash form, as the configuration
	// names it.
	Directory string
	// Owner is the role that may change a document filed here. It is the same
	// table internal/artifact authorizes mutations against rather than a second
	// copy of it: these homes hold those kinds.
	Owner domain.AgentRole
	// Purpose, Ownership, and HandEdit are the three answers, one paragraph
	// each. They are fields rather than a switch inside the renderer so a home
	// whose answer genuinely differs — the invariants, which carry a scheme of
	// their own — states its own rather than a generic one with a caveat.
	Purpose   string
	Ownership string
	HandEdit  string
	// Note is an optional fourth paragraph for what is peculiar to one home, and
	// is empty for most of them.
	Note string
}

// Path is where this home's index lives, repository-relative.
func (h Home) Path() string { return h.Directory + "/" + FileName }

// Homes are the artifact homes a configuration describes, in the order somebody
// reading the repository would meet them: intent first, then the goals under it,
// then how it gets built, then how it was decided, then the constraints extracted
// from those decisions.
//
// They are derived from the configuration rather than fixed, for the reason the
// protected paths are: a project that files its designs somewhere else has moved
// the home rather than stopped having one. The goals directory is the exception
// and is derived from the specifications home, because it is a convention the
// harness itself reads — a document in a directory called `goals` is goals, which
// is how a repository that wrote its goals before anybody mentioned identity
// still has them found.
func Homes(cfg config.Config) []Home {
	specifications := normalize(cfg.Product.Specifications)
	var homes []Home
	add := func(home Home) {
		if home.Directory == "" {
			return
		}
		for _, existing := range homes {
			if existing.Directory == home.Directory {
				return
			}
		}
		homes = append(homes, home)
	}

	add(productHome(specifications))
	if specifications != "" {
		add(goalsHome(specifications + "/goals"))
	}
	add(designsHome(normalize(cfg.Product.Designs)))
	add(decisionsHome(normalize(cfg.Product.Decisions)))
	add(invariantsHome(normalize(cfg.Product.Invariants)))
	return homes
}

func normalize(directory string) string {
	trimmed := strings.Trim(strings.TrimSpace(filepath.ToSlash(directory)), "/")
	if trimmed == "" {
		return ""
	}
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}

func productHome(directory string) Home {
	return Home{
		Directory: directory,
		Owner:     domain.RoleProductManager,
		Purpose: "The product brief and the goals that serve it: what this product is, who it " +
			"is for, and what finished looks like. This is the only directory the harness " +
			"reads product intent from, and work reaches the backlog only with a goal stated " +
			"here named against it.",
		Ownership: ownershipParagraph(domain.RoleProductManager),
		HandEdit:  handEditParagraph(domain.RoleProductManager),
	}
}

func goalsHome(directory string) Home {
	return Home{
		Directory: directory,
		Owner:     domain.RoleProductManager,
		Purpose: "The goals derived from the product brief above, and the non-goals that bound " +
			"them. Every entry under a goals document's `Goals` heading is a goal work can be " +
			"attributed to, and an entry opening with an identifier in square brackets — " +
			"`- [traceable-chain] Maintain a traceable chain ...` — carries that identifier as " +
			"its stable identity. An attribution resolves by naming the identity, so re-wording " +
			"a goal orphans no item that named it that way; a goal stating no identity is matched " +
			"on its words instead, and both that goal and an attribution quoting an older wording " +
			"are broken by a re-wording. `yoyo goals list` is what they resolve to.",
		Ownership: ownershipParagraph(domain.RoleProductManager),
		HandEdit:  handEditParagraph(domain.RoleProductManager),
	}
}

func designsHome(directory string) Home {
	return Home{
		Directory: directory,
		Owner:     domain.RoleArchitect,
		Purpose: "The designs and specifications: how what the goals ask for gets built. A design " +
			"serves the intent upstream of it and never revises it, so a design that has " +
			"outgrown its goal is an amendment to propose rather than a goal to reinterpret.",
		Ownership: ownershipParagraph(domain.RoleArchitect),
		HandEdit:  handEditParagraph(domain.RoleArchitect),
	}
}

func decisionsHome(directory string) Home {
	return Home{
		Directory: directory,
		Owner:     domain.RoleArchitect,
		Purpose: "The decision records: how something was decided, what was decided against, and " +
			"what that cost. A record is an account of a decision rather than a statement of " +
			"intent, so approvals.designs governs it as it governs a design.",
		Ownership: ownershipParagraph(domain.RoleArchitect),
		HandEdit:  handEditParagraph(domain.RoleArchitect),
	}
}

func invariantsHome(directory string) Home {
	return Home{
		Directory: directory,
		Owner:     domain.RoleArchitect,
		Purpose: "The architectural invariants: one Markdown file per durable constraint, named " +
			"by its id, carrying what must hold, why, what established it, and its revision " +
			"history. The harness selects the ones relevant to a work item and delivers them " +
			"into the developer's context and the reviewer's evidence, so a constraint holds " +
			"even where the work item never mentions it.",
		Ownership: "The architect, and no other role at all. A developer or a reviewer that " +
			"believes an invariant is wrong leaves it as it stands and proposes the amendment in " +
			"what it reports, for the architect to decide.",
		HandEdit: "You may, and nothing refuses it. What `yoyo invariant` does that an editor " +
			"does not is record who changed the constraint and why, so an edit made by hand " +
			"leaves a history that has stopped accounting for itself. Retiring one is " +
			"`yoyo invariant retire` rather than deleting the file: the file stays, the " +
			"constraint stops being delivered, and the reason it was lifted stays readable. " +
			"Then say what you changed in `" + conversationCommand(domain.RoleArchitect) + "`, because a " +
			"conversation that is already open is working from these constraints as they read " +
			"when it opened — and every run started since is being handed the ones you edited.",
		Note: "These carry a scheme of their own rather than artifact identity frontmatter — " +
			"the file name is the id — which is why this directory is skipped when the " +
			"artifacts are loaded even though it usually sits inside the decisions home.",
	}
}

// ownershipParagraph is the same answer for every home an artifact kind lives in,
// because it is one rule: the owning role changes the document and every other
// role proposes. Naming the role is the whole of what differs.
func ownershipParagraph(owner domain.AgentRole) string {
	return fmt.Sprintf("The %s. It is the only role that changes a document filed here. Every other "+
		"role proposes an amendment and waits for the owner to decide rather than editing one, "+
		"and `yoyo amendment list` is what is waiting.", owner.Title())
}

// handEditParagraph says what happens when the operator edits one of these
// documents themselves. It is deliberately an account of what the harness already
// does rather than a permission this file is granting: the operator's authority
// over their own documents is not this file's to hand out or withhold.
func handEditParagraph(owner domain.AgentRole) string {
	return fmt.Sprintf("You may, and nothing here refuses it — you are the operator, and these "+
		"documents are yours. Two things follow rather than nothing. Record the change in the "+
		"document's own revision log under the %s: a revision recorded under any other role is "+
		"reported as unauthorized every time the artifacts load, which is something to look at "+
		"rather than a refusal. And whatever downstream traced to what you changed is reported "+
		"by `yoyo stale` until its own owner revises it. Then say what you changed in `%s`, "+
		"because a conversation that is already open is working from these documents as they "+
		"read when it opened.", owner.Title(), conversationCommand(owner))
}

// conversationCommand is how an operator reaches the role that owns a home.
// `yoyo chat` names no agent and so takes the one filling the product-manager
// role, which is why that role's command is the short one.
func conversationCommand(owner domain.AgentRole) string {
	if owner == domain.RoleProductManager {
		return "yoyo chat"
	}
	return "yoyo agent chat " + string(owner)
}

// README renders this home's index.
func (h Home) README() []byte {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "# %s\n", h.Directory)
	writeParagraph(&rendered, purposeMarker+" "+h.Purpose)
	writeParagraph(&rendered, ownerMarker+" "+h.Ownership)
	writeParagraph(&rendered, handEditMarker+" "+h.HandEdit)
	if h.Note != "" {
		writeParagraph(&rendered, h.Note)
	}
	writeParagraph(&rendered, "This file is a directory index rather than an artifact: it carries no "+
		"identity frontmatter, nothing refers to it by id, and artifact governance skips it. "+
		"`yoyo init` writes it and `yoyo doctor` reports it missing, so editing it is safe and "+
		"deleting it is noticed.")
	return []byte(rendered.String())
}

// wrapWidth keeps a generated document inside the width the repository's own
// Markdown is written to, so an index the harness wrote and one somebody wrote
// diff against each other rather than reflowing.
const wrapWidth = 79

func writeParagraph(builder *strings.Builder, text string) {
	builder.WriteString("\n")
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) > wrapWidth:
			builder.WriteString(line + "\n")
			line = word
		default:
			line += " " + word
		}
	}
	if line != "" {
		builder.WriteString(line + "\n")
	}
}

// State is what one home's index is.
type State string

const (
	// StateWritten is an index that is there and still answers the three
	// questions.
	StateWritten State = "written"
	// StateMissing is a home with no index at all.
	StateMissing State = "missing"
	// StateIncomplete is an index that is there and has stopped answering one of
	// them. It is kept apart from missing because the two are not put right the
	// same way: nothing is lost by writing a file that is not there, and
	// somebody's own prose is lost by replacing one that is.
	StateIncomplete State = "incomplete"
	// StateUnreadable is an index that could not be read or a path that could
	// not be resolved. It is reported as itself rather than as missing, because
	// writing over what could not be read is how an index nobody could open
	// becomes an index nobody has.
	StateUnreadable State = "unreadable"
)

// Status is one home and what became of its index.
type Status struct {
	Home Home
	// Path is repository-relative, which is how a finding names it.
	Path  string
	State State
	// Detail carries what was actually observed for a state that has something
	// to observe, and is empty otherwise.
	Detail string
}

// Written reports whether this index needs nothing.
func (s Status) Written() bool { return s.State == StateWritten }

// Inspect reads what every home's index is. A home directory that does not exist
// is not skipped: a project that has not written its designs down yet is exactly
// the project the index is worth writing for, since the index is what says what
// would go there.
func Inspect(root repowrite.Root, cfg config.Config) []Status {
	homes := Homes(cfg)
	statuses := make([]Status, 0, len(homes))
	for _, home := range homes {
		statuses = append(statuses, inspect(root, home))
	}
	return statuses
}

func inspect(root repowrite.Root, home Home) Status {
	status := Status{Home: home, Path: home.Path()}
	resolved, err := root.Resolve(status.Path)
	if err != nil {
		status.State = StateUnreadable
		status.Detail = err.Error()
		return status
	}
	content, err := os.ReadFile(resolved)
	if os.IsNotExist(err) {
		status.State = StateMissing
		return status
	}
	if err != nil {
		status.State = StateUnreadable
		status.Detail = err.Error()
		return status
	}
	if missing := unanswered(string(content)); len(missing) > 0 {
		status.State = StateIncomplete
		status.Detail = "it does not state " + strings.Join(missing, " or ")
		return status
	}
	status.State = StateWritten
	return status
}

// unanswered names the questions an index has stopped answering, in the order it
// answers them, so a report says which one to write rather than that something is
// wrong with the file.
func unanswered(content string) []string {
	var missing []string
	if !strings.Contains(content, purposeMarker) {
		missing = append(missing, "what is filed here")
	}
	if !strings.Contains(content, ownerMarker) {
		missing = append(missing, "which agent owns it")
	}
	if !strings.Contains(content, handEditMarker) {
		missing = append(missing, "whether you may edit it by hand")
	}
	return missing
}

// Write puts one home's index on disk and returns where it landed. The write goes
// through the confined root every other repository write goes through, so an
// index cannot land outside the repository through a symlinked home — which is
// the same escape the artifact writers were proven vulnerable to.
func Write(root repowrite.Root, home Home) (string, error) {
	written, err := root.WriteFile(home.Path(), home.README())
	if err != nil {
		return "", fmt.Errorf("write %s: %w", home.Path(), err)
	}
	return written, nil
}

// Paths names every index a set of statuses is about, in the order the homes are
// read, so a healthy report can say which files it actually looked at rather than
// only how many.
func Paths(statuses []Status) []string {
	paths := make([]string, 0, len(statuses))
	for _, status := range statuses {
		paths = append(paths, status.Path)
	}
	return paths
}

// Describe folds a set of statuses into the one line a report leads with, naming
// the paths rather than counting them: there are at most a handful of homes, and
// which one is missing is the whole of what somebody would act on.
func Describe(statuses []Status) string {
	byState := map[State][]string{}
	for _, status := range statuses {
		if status.Written() {
			continue
		}
		byState[status.State] = append(byState[status.State], status.Path)
	}
	var parts []string
	for _, state := range []State{StateMissing, StateIncomplete, StateUnreadable} {
		paths := byState[state]
		if len(paths) == 0 {
			continue
		}
		sort.Strings(paths)
		parts = append(parts, string(state)+": "+strings.Join(paths, ", "))
	}
	return strings.Join(parts, "; ")
}
