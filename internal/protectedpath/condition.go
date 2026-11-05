package protectedpath

// A done-condition no developer run may satisfy.
//
// The gate in protectedpath.go refuses a developer's diff that touches an
// artifact home the item did not grant. What it cannot refuse is the item
// itself: a done-condition that names a design, a decision record, or a product
// artifact — "the design's query list marks the query as existing", "reconcile
// the design document's yoyo status entry", "her ruling is recorded on the
// design" — is a condition the run may not write the satisfaction of, and the
// run finds that out only by spending itself. Three items in one week did
// (yoyodyne-ifd.141.1, .63, and .68.25 before them): each ran, each parked or
// spent review rounds on the one clause no diff could meet, and each was fixed
// afterwards by the architect amending the document through the governed path.
// The development manager's 2026-09-03 checklist said where the fix belongs — a
// precondition or an edit the done-means implies is structure at admission, not
// a finding at review — and this is that structure.
//
// So an item's done-conditions are read where the item is written. A clause of
// the description's done-means or of the acceptance criteria that names a path
// under one of the artifact homes, or a document one of those homes owns, is
// refused unless the item grants that path — with the clause quoted and the fix
// named, which is one of two things: take the clause out and say the document's
// owner amends it, or carry the grant where one is permitted. The run's opening
// check asks the same question of the item text it is handed, so an item that
// acquired such a clause with the tracker's own command, or was admitted before
// this existed, is refused before it is claimed rather than parked after a run.
//
// # What is read, and what deliberately is not
//
// Only the done-conditions are read: the whole of the acceptance criteria, and
// in the description the sentences from a "Done means" (or "Done:", "done
// when") to the end of their paragraph. An item's prose cites these documents
// constantly — as the design the work builds against, the invariant it is held
// to, the decision that rules something out — and a citation is not a
// condition. Measured over the 596 items this repository's tracker held when
// this was written, reading every clause fires on 117 of them, a fifth of the
// backlog; reading the done-conditions as below fires on 10, one of them
// unfinished (yoyodyne-ifd.313, whose done-means records a rule "as a section
// of slack-reporting-design"), and each of the ten names a document as
// something the work leaves in a state. The one unfinished item that names a
// product path in its done-means and carries the grant for it
// (yoyodyne-ifd.262) passes.
//
// A document is named by its path, or by a phrase whose subject is the document
// itself: its id with the word design, document, decision, record, goals, or
// rules beside it — "the machine-home design" — or an id that ends in such a
// word on its own, as "the slack-reporting design" names slack-reporting-design
// and "the v1 goals" names v1-goals. A path names a
// document only where it names a file: a home's own path, or a folder inside
// one, is the home, which a done-condition lists as often as a rule about where
// changes land as a place the run writes. A design's id used as ordinary words
// inside a phrase about something else — "the machine-home project directory",
// "no program manager is configured" — is the words, and so is a file of the
// same name written without the home's path. A single-word id is never looked
// for: the brief's id is "brief", and a done-condition that says "the brief's
// acceptance criteria hold" is citing it, which the 17 items whose done-means
// say something of that kind bear out. Documents under the invariants directory are named by path only, for the
// reason the artifact store excludes them from its identity scheme: an invariant
// is delivered to every run by its id and cited by it in nearly every item, and
// a done-condition that edited one would name its path.
//
// # Who executes the item changes the answer
//
// The question above has a second half, and it was not asked until
// yoyodyne-ifd.330 cost a run: not only which document a done-condition names,
// but who the item says carries it out. An item whose executor is a role's
// conversation is that role's work, and a done-condition naming a document the
// role owns is the condition stated correctly — "the ruling is recorded on the
// slack-reporting design" is exactly what the architect's conversation does —
// so it is admitted, and it is what the harness later reads to close the item
// when the revision lands. The same clause on an item nobody marked is refused,
// because that item is a developer run and no run can write it.
//
// And the shape itself is read, without any document named. 330 was admitted
// with "Done means the design is recorded in the governed documents" and no
// executor, and selection had nothing to go on: no document was named, the
// tracker called the item ready, and a developer run was spent finding out
// that the design had already landed. So a done-condition that says a design
// or a ruling is recorded, published, promoted, or ratified is read as the
// design owner's conversation work, and so is a title whose subject is that
// role — "The architect designs …", "The architect rules …" — because the
// architect executes nothing in a run. Measured over the 596 items this
// tracker held when this was written, the two readings together fire on 27
// items: 24 that carry the executor already, 330, and two admitted before the
// marker existed (yoyodyne-ifd.68.1 and .68.25) whose clause a run indeed
// could not meet. An item that reads so and names no executor is refused with
// the marker named as the fix.

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
)

// Document is one document an artifact home owns, as an item may name it: by
// the path the file is at, or by the id the artifact store gives it, which is
// the file's own name.
type Document struct {
	ID   string
	Path string
}

// Homes are the artifact homes a done-condition may not reach into, and the
// documents they own. It is built from the configuration for the reason Set is:
// a project that keeps its designs somewhere else has not thereby made them a
// run's to satisfy a condition in.
type Homes struct {
	directories []string
	documents   []ownedDocument
	// owners is which role's conversation writes each home, keyed by the home's
	// normalized path. It is read from the artifact ownership table rather than
	// stated here, so "the architect owns designs" stays one sentence in one
	// place; a home whose kind no single role owns is in directories and not
	// here, and a condition naming it is refused whoever executes the item.
	owners map[string]domain.AgentRole
}

// ownedDocument is a document with the shape its id is looked for in, compiled
// once when the homes are built rather than once per clause.
type ownedDocument struct {
	Document
	shape *regexp.Regexp
	// titled marks an id whose own last word says what kind of document it is —
	// slack-reporting-design — so the id written alone is already "the <name>
	// design"; see namesDocument.
	titled bool
}

// ArtifactHomes builds the homes a configuration names — the product artifacts,
// the designs, the decision records, and the invariants — with whichever owned
// documents the caller read. The configuration directory is deliberately not
// among them: it is protected in a developer's diff, but a condition naming it
// is about the harness's own settings rather than about a document another role
// owns, and this gate is about the second.
func ArtifactHomes(cfg config.Config, documents ...Document) Homes {
	homes := Homes{owners: map[string]domain.AgentRole{}}
	for _, home := range []struct {
		directory string
		owner     domain.AgentRole
	}{
		{cfg.Product.Specifications, artifactOwner(artifact.KindBrief)},
		{cfg.Product.Designs, artifactOwner(artifact.KindDesign)},
		{cfg.Product.Decisions, artifactOwner(artifact.KindDecision)},
		{cfg.Product.Invariants, invariantOwner()},
	} {
		clean, ok := normalize(home.directory)
		if !ok {
			continue
		}
		homes.directories = appendUnique(homes.directories, clean)
		if home.owner != "" {
			homes.owners[clean] = home.owner
		}
	}
	sort.Strings(homes.directories)
	for _, document := range documents {
		clean, ok := normalize(document.Path)
		if !ok {
			continue
		}
		id := strings.TrimSpace(document.ID)
		shape, named := idShape(id)
		if !named {
			continue
		}
		homes.documents = append(homes.documents, ownedDocument{Document: Document{ID: id, Path: clean}, shape: shape, titled: documentWord.MatchString(id[strings.LastIndex(id, "-")+1:])})
	}
	sort.Slice(homes.documents, func(i, j int) bool { return homes.documents[i].Path < homes.documents[j].Path })
	return homes
}

// artifactOwner is the role whose conversation writes a kind of artifact, or
// empty where no single role does. It is the ownership table's answer and not
// one made here.
func artifactOwner(kind artifact.Kind) domain.AgentRole {
	owner, placed := artifact.Owner(kind)
	if !placed {
		return ""
	}
	return owner
}

// invariantOwner is the role that may write an invariant, read the same way:
// the one holder of the capability, or nobody where the registry names more or
// fewer than one.
func invariantOwner() domain.AgentRole {
	holders := rolecapability.MustDefault().RolesHolding(capability.InvariantMutate)
	if len(holders) != 1 {
		return ""
	}
	return holders[0]
}

// idShape is the shape an id is looked for in, and whether the id is one that
// is looked for at all. An id is matched by its words in order, joined by a
// hyphen, an underscore, or a space, because prose names "the slack-reporting
// design" as readily as `slack-reporting-design` and both name the same file.
// A one-word id is never looked for: it is as likely to be the word as the
// document, and the path names the document unambiguously.
func idShape(id string) (*regexp.Regexp, bool) {
	words := strings.Split(id, "-")
	if len(words) < 2 {
		return nil, false
	}
	for i, word := range words {
		if word == "" {
			return nil, false
		}
		words[i] = regexp.QuoteMeta(word)
	}
	shape, err := regexp.Compile(`(?i)` + strings.Join(words, `[-_ ]`))
	if err != nil {
		return nil, false
	}
	return shape, true
}

// documentWords are the words that say the phrase they end is a document: "the
// slack-reporting design", "the machine-home design document", "the
// merge-affinity decision record", and the product's own kinds, "the v1 goals",
// "the operating rules". Goals and rules are read only in the plural, which is
// how those documents are titled; "a goal" or "a rule" is what a gate enforces
// as often as a document.
const documentWords = `(?:designs?|documents?|decisions?|records?|goals|rules)`

// documentWord is one of documentWords standing alone, for the last word of an
// id; documentAfter and documentBefore are those words beside an id: "the
// program manager design", "the observability-and-dashboard design's query
// list", "the design program-manager", "the decision record merge-affinity". A
// possessive is allowed between, because "the program manager's design" is the
// design too.
var (
	documentWord   = regexp.MustCompile(`(?i)^` + documentWords + `$`)
	documentAfter  = regexp.MustCompile(`(?i)^(?:'s|’s)?\s+` + documentWords + `\b`)
	documentBefore = regexp.MustCompile(`(?i)\b` + documentWords + `\s+$`)
)

// namesDocument reports a match of a document's id that names the document
// rather than something else the words also are. A design's id is very often
// ordinary words, and a done-condition uses them for the thing they describe:
// "the machine-home project directory" is a folder in the machine home, "no
// program manager is configured" is the role, "the pinned outside files section
// of docs/configuration.md" is a section of another file, and
// "developer-sub-agents.md" in the harness's source is a file of the same name
// elsewhere. Each was read as the design of that name and refused work nobody
// meant to touch a design with. So an id names the document only where the
// phrase's subject is the document itself — one of documentWords beside it, or
// an id that ends in one and so says it alone — and anywhere else it is the
// words. A path is read as a path
// whatever is beside it, which is the unambiguous way to name the document.
func (d ownedDocument) namesDocument(text string, at []int) bool {
	if d.titled {
		return true
	}
	return documentAfter.MatchString(text[at[1]:]) || documentBefore.MatchString(text[:at[0]])
}

// Empty reports homes with nothing to check against, which is what a caller
// that was wired none gets: such a caller refuses nothing, exactly as every
// admission did before this existed.
func (h Homes) Empty() bool {
	return len(h.directories) == 0
}

// OwnedDocuments reads the documents a repository's artifact homes own, as the
// artifact store identifies them, so what a done-condition is checked against is
// the same set every other reader of those homes sees. A document the store
// could not read as an artifact is still a document the home owns — a
// done-condition naming it is no more satisfiable for its frontmatter being
// wrong — so the store's problems are read for their paths as well.
func OwnedDocuments(repositoryRoot string, product config.Product) ([]Document, error) {
	set, err := artifact.StoreFor(repositoryRoot, product).Load()
	if err != nil {
		return nil, err
	}
	var documents []Document
	for _, recorded := range set.Artifacts {
		documents = append(documents, Document{ID: recorded.ID, Path: recorded.Path})
	}
	for _, problem := range set.Problems {
		documents = append(documents, Document{ID: idForPath(problem.Path), Path: problem.Path})
	}
	return documents, nil
}

// idForPath is the id a file in an artifact home answers to: its own name. It
// is the artifact store's rule, restated for the files the store refused.
func idForPath(relative string) string {
	base := relative
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	return base
}

// Condition is one done-condition clause naming a document no grant admits.
type Condition struct {
	// Path is the repository-relative path the clause reaches into: the path it
	// wrote, or the path of the document it named by id.
	Path string
	// Named is what the clause wrote, as it wrote it.
	Named string
	// Clause is the clause itself, folded to one line and bounded.
	Clause string
	// Owner is the role whose conversation writes the home the path is under,
	// or empty where no single role does. It is what decides whether an item
	// some conversation carries may state this condition, and what the refusal
	// of one nobody marked names as the executor to mark it with.
	Owner domain.AgentRole
}

// ConditionInstruction is what a refused role is told to do about it. It names
// both fixes because both are real: a condition that belongs to the document's
// owner comes out of the item, and a change somebody already decided is granted.
// It says who owns what only as far as the artifact ownership table does, and
// says the run's summary is how the owner learns what to record, because that
// is the path the three incidents above were each fixed along.
const ConditionInstruction = "No developer run may write there, so a run handed this condition spends itself and parks on it. " +
	"Either take the clause out of the done-condition and say that the document's owner — the architect for a design or a decision record, the Lead Product Manager for a product artifact — amends the document through the governed path, with the run's summary naming what there is to record; " +
	"or, where a grant is permitted for that path because the change behind it is already decided, carry one on a line beginning \"" + GrantMarker + "\" that names it."

// executorInstruction is the third fix, named where the document has an owner
// whose conversation is what the item is really for: mark the item as that
// conversation's, and it is never selected for a run and closes when the
// owner's revision naming it lands. It is said only where there is a role to
// name, because the instruction is the marker's value and the marker is refused
// without a role.
func executorInstruction(owner domain.AgentRole) string {
	if owner == "" {
		return ""
	}
	return fmt.Sprintf(" Or, where the condition is that role's own work rather than a run's, mark the item as carried by its conversation, with executor %q: an item so marked keeps its place in the order, is never selected for a developer run, and is closed by the harness once a revision of a document the %s owns opens with the item's identifier.",
		domain.ConversationWith(owner), owner)
}

// Refusal is what admission says about one condition it will not admit: what the
// clause names and where that is, the clause itself, and what to do instead.
func (c Condition) Refusal() string {
	where := c.Path
	if c.Named != c.Path {
		where = fmt.Sprintf("%s (%s)", c.Named, c.Path)
	}
	return fmt.Sprintf("a done-condition names %s, which is under a protected artifact home and no %q line in the item admits: %q. %s%s",
		where, GrantMarker, c.Clause, ConditionInstruction, executorInstruction(c.Owner))
}

// anotherRolesRefusal is what is said of a condition on an item some
// conversation does carry, naming a document that conversation's role does not
// own. The marker is not the fix here — the item already carries one — so what
// is named is whose document it is, and the two fixes that remain.
func (c Condition) anotherRolesRefusal(executor domain.WorkItemExecutor) string {
	where := c.Path
	if c.Named != c.Path {
		where = fmt.Sprintf("%s (%s)", c.Named, c.Path)
	}
	whose := "no single role's"
	if c.Owner != "" {
		whose = "the " + string(c.Owner) + "'s"
	}
	return fmt.Sprintf("a done-condition names %s, which is %s to write and not the %s conversation's this item is carried by, and no %q line in the item admits it: %q. %s",
		where, whose, executor.Role(), GrantMarker, c.Clause, ConditionInstruction)
}

// maxConditions bounds how many conditions one reading reports. Whoever is told
// has to rewrite the first of them, and the item is where the rest are.
const maxConditions = 5

// maxClauseBytes keeps one quoted clause to its part of one line. What is quoted
// came out of a role's reply, so it is folded rather than trusted to be short.
const maxClauseBytes = 200

// Ungranted reports the done-conditions in an item's description and acceptance
// criteria that name a path under these homes, or a document they own, without
// a grant covering it. An empty result is the ordinary answer: nearly every item
// names nothing of the kind in its done-conditions, and one whose every named
// document is granted is as clear as one that named none.
//
// The grants are passed rather than read here, because which of an item's
// fields they are read from is the caller's decision — the same four fields
// Grants is given, which the harness never writes into.
func (h Homes) Ungranted(description, acceptanceCriteria string, granted []string) []Condition {
	if h.Empty() {
		return nil
	}
	grants := normalizeAll(granted)
	var (
		conditions []Condition
		seen       = map[string]bool{}
	)
	note := func(path, named, clause string) {
		if within(path, grants) {
			return
		}
		key := path + "\x00" + clause
		if seen[key] {
			return
		}
		seen[key] = true
		conditions = append(conditions, Condition{Path: path, Named: named, Clause: fold(clause, maxClauseBytes), Owner: h.ownerOf(path)})
	}
	for _, span := range doneConditions(description, acceptanceCriteria) {
		for _, clause := range clauses(span) {
			for _, cited := range standingAlone(clause, writtenPath) {
				path, ok := normalize(strings.TrimRight(cited, trailingDecoration))
				if !ok || !h.documentPath(path) {
					continue
				}
				note(path, path, clause)
			}
			for _, document := range h.documents {
				for _, at := range standingAloneAt(clause, document.shape) {
					if !document.namesDocument(clause, at) {
						continue
					}
					note(document.Path, clause[at[0]:at[1]], clause)
					break
				}
			}
		}
	}
	if len(conditions) > maxConditions {
		conditions = conditions[:maxConditions]
	}
	return conditions
}

// documentPath reports a written path that names a document under one of these
// homes: a document the homes own, or a file — a last segment with an extension
// — inside a home. A home's own path, or a folder inside one, names no document:
// a done-condition listing "docs/product, docs/designs, and docs/decisions" as
// the rule for which changes take a route is describing the homes, not asking a
// run to change something in them.
func (h Homes) documentPath(path string) bool {
	if !within(path, h.directories) {
		return false
	}
	for _, document := range h.documents {
		if document.Path == path {
			return true
		}
	}
	for _, directory := range h.directories {
		if within(directory, []string{path}) {
			return false
		}
	}
	name := path[strings.LastIndex(path, "/")+1:]
	return strings.LastIndex(name, ".") > 0
}

// ownerOf is the role whose conversation writes the home a path is under, or
// empty where the path is under no home or under one no single role owns.
func (h Homes) ownerOf(path string) domain.AgentRole {
	for directory, owner := range h.owners {
		if within(path, []string{directory}) {
			return owner
		}
	}
	return ""
}

// Subject is one item as its done-conditions are judged: the fields a condition
// is read from, the fields a grant is honoured from, and what the item says
// carries it out. The executor is part of the question rather than a filter on
// the answer, because the same clause is right on an item a conversation
// carries and unmeetable on one a run does.
type Subject struct {
	Title              string
	Description        string
	AcceptanceCriteria string
	// Granted is the paths the item admits, as Grants reads them from whichever
	// of its fields the caller honours a grant from.
	Granted []string
	// Executor is what carries the item, and empty for a developer run.
	Executor domain.WorkItemExecutor
}

// ConditionProblems is the errors an admission joins about an item's
// done-conditions, one per condition, each carrying its refusal. It is one
// predicate every door into the queue asks rather than each deciding for itself,
// for the reason GrantProblems is: a door that asked a weaker question is the
// door such an item would arrive through.
//
// An item a conversation carries is judged as that conversation's work: a
// condition naming a document the executor's role owns is admitted, and one
// naming another role's document is refused as another role's. An item nobody
// marked is a developer run, and is refused both for a condition naming a
// document — as it always was — and for reading as conversation work with no
// executor to say so, which is the shape a run was spent on.
func (h Homes) ConditionProblems(subject Subject) []error {
	var problems []error
	if subject.Executor.DeveloperRun() || subject.Executor.Role() == "" {
		// A clause refused for the document it names is refused once: the
		// document refusal already names the marker as a fix, and the same clause
		// quoted twice with two instructions is one instruction too many.
		refused := map[string]bool{}
		for _, condition := range h.Ungranted(subject.Description, subject.AcceptanceCriteria, subject.Granted) {
			refused[condition.Clause] = true
			problems = append(problems, errors.New(condition.Refusal()))
		}
		for _, work := range h.ConversationWork(subject.Title, subject.Description, subject.AcceptanceCriteria, subject.Granted) {
			if work.Field == "done-condition" && refused[work.Text] {
				continue
			}
			problems = append(problems, errors.New(work.Refusal()))
		}
		return problems
	}
	role := subject.Executor.Role()
	for _, condition := range h.Ungranted(subject.Description, subject.AcceptanceCriteria, subject.Granted) {
		if condition.Owner == role {
			continue
		}
		problems = append(problems, errors.New(condition.anotherRolesRefusal(subject.Executor)))
	}
	return problems
}

// ConversationWork is one reading of an item that says a role's conversation
// carries it: a done-condition whose subject is the role's judgement recorded,
// or a title whose subject is the role itself.
type ConversationWork struct {
	// Role is whose conversation the reading says the work is.
	Role domain.AgentRole
	// Field is where it was read — "title" or "done-condition".
	Field string
	// Text is what was read, folded to one line and bounded.
	Text string
}

// Refusal is what admission says about conversation-shaped work that names no
// executor: what was read and where, and the two fixes — mark the item, or
// rewrite it as the change a run makes.
func (w ConversationWork) Refusal() string {
	switch w.Field {
	case "title":
		return fmt.Sprintf("its title says the %s does the work, which no developer run carries, and the item names no executor: %q. Either mark the item as carried by that conversation, with executor %q, so it is never selected for a developer run and is closed by the harness once a revision of a document the %s owns opens with the item's identifier; or retitle it as the change a run makes.",
			w.Role, w.Text, domain.ConversationWith(w.Role), w.Role)
	default:
		return fmt.Sprintf("a done-condition says a design or a ruling is recorded, which is the %s's conversation's work and no developer run's, and the item names no executor: %q. Either mark the item as carried by that conversation, with executor %q, so it is never selected for a developer run and is closed by the harness once a revision of a document the %s owns opens with the item's identifier; or take the clause out of the done-condition and say that the %s amends the document through the governed path, with the run's summary naming what there is to record.",
			w.Role, w.Text, domain.ConversationWith(w.Role), w.Role, w.Role)
	}
}

// recordedJudgement is a done-condition whose subject is a design or a ruling
// and whose predicate is that it is recorded: "the design is recorded in the
// governed documents", "her ruling is recorded on the design", "the baseline
// ratified". The subjects are the two words this tracker's conversation items
// are written with, and only those two: "decision" is also what triage records
// on an item and "rule" is what a gate enforces, and both fire on developer
// items, so they are left out. The verbs are the ones a revision performs.
var recordedJudgement = regexp.MustCompile(`(?i)\b(?:designs?|rulings?)\b[^.;\n]{0,60}?\b(?:is|are|be|being|was|were|gets?)\s+(?:recorded|published|promoted|ratified)\b`)

// architectSubject is a title whose subject is the architect doing something:
// "The architect designs …", "The architect rules …", "The architect
// ratifies …". The verb is what makes it the architect acting rather than the
// architect's — "The architect's ruling is enforced" is a developer item about
// the ruling, and the apostrophe keeps it out of this. Only the architect is
// read this way: it is the one role that executes nothing in a run, where "the
// product manager" and "the development manager" open developer items about
// those roles' machinery as often as not.
var architectSubject = regexp.MustCompile(`(?i)^\s*the architect\s+[a-z]+s\b`)

// ConversationWork reads an item for the shape that says a role's conversation
// carries it, without any document being named. It reads the same
// done-conditions Ungranted reads, and the title, and it says which role the
// shape names: the design owner for a recorded design or ruling, and the
// architect for a title with the architect as its subject. An item that reads
// so and carries the executor is that role's work stated correctly; one that
// reads so and carries none is what ConditionProblems refuses.
//
// An item that grants a path under one of these homes is read as nothing of
// the kind. A grant is somebody's decision that a run writes there — the change
// behind it already decided — so "the ruling is recorded on the design" on
// such an item is the run's work, exactly as the named-document reading treats
// it.
func (h Homes) ConversationWork(title, description, acceptanceCriteria string, granted []string) []ConversationWork {
	if h.Empty() {
		return nil
	}
	for _, grant := range normalizeAll(granted) {
		for _, directory := range h.directories {
			if within(grant, []string{directory}) || within(directory, []string{grant}) {
				return nil
			}
		}
	}
	var found []ConversationWork
	if designer := artifactOwner(artifact.KindDesign); h.writes(designer) {
		for _, span := range doneConditions(description, acceptanceCriteria) {
			for _, clause := range clauses(span) {
				if !recordedJudgement.MatchString(clause) {
					continue
				}
				found = append(found, ConversationWork{Role: designer, Field: "done-condition", Text: fold(clause, maxClauseBytes)})
			}
		}
	}
	if h.writes(domain.RoleArchitect) && architectSubject.MatchString(title) {
		found = append(found, ConversationWork{Role: domain.RoleArchitect, Field: "title", Text: fold(title, maxClauseBytes)})
	}
	if len(found) > maxConditions {
		found = found[:maxConditions]
	}
	return found
}

// writes reports a role whose conversation writes one of these homes. A project
// configured without any home that role owns has nowhere its work could land,
// and nothing on such a project is read as that role's.
func (h Homes) writes(role domain.AgentRole) bool {
	if role == "" {
		return false
	}
	for _, holder := range h.owners {
		if holder == role {
			return true
		}
	}
	return false
}

// doneMarker is where a description's done-conditions begin. The phrasings are
// this repository's own: "Done means" opens the done-conditions of 418 of the
// 596 items the tracker held when this was written, and the other two are the
// forms the remainder used. A phrasing not here is a done-condition this does
// not read, which is the safe way round — the acceptance criteria field is read
// whole, and an item written with neither is one nothing here refuses.
var doneMarker = regexp.MustCompile(`(?i)\bdone means\b|\bdone:|\bdone when\b`)

// paragraphEnd is where a done-means paragraph stops: a blank line.
var paragraphEnd = regexp.MustCompile(`\n[ \t]*\n`)

// DoneConditions is doneConditions for a reader outside this package: the
// review evidence reads the same spans for what a criterion quotes, so what a
// reviewer is shown matched against and what admission reads as a condition
// cannot come apart.
func DoneConditions(description, acceptanceCriteria string) []string {
	return doneConditions(description, acceptanceCriteria)
}

// doneConditions is the spans of an item's text that state what done means:
// each done-means paragraph of the description, and the acceptance criteria
// whole, because every clause of that field is a condition by construction.
func doneConditions(description, acceptanceCriteria string) []string {
	var spans []string
	for _, at := range doneMarker.FindAllStringIndex(description, -1) {
		rest := description[at[0]:]
		if end := paragraphEnd.FindStringIndex(rest); end != nil {
			rest = rest[:end[0]]
		}
		spans = append(spans, rest)
	}
	if strings.TrimSpace(acceptanceCriteria) != "" {
		spans = append(spans, acceptanceCriteria)
	}
	return spans
}

// clauses splits a span at the boundaries a clause is written with: a
// semicolon, a line break, or a full stop that ends a sentence. A full stop
// inside a path or an id — "v1-harness-design.md", "yoyodyne-ifd.63" — is not a
// boundary, which is why the stop has to be followed by space or the end.
func clauses(span string) []string {
	var (
		found []string
		start int
	)
	for i := 0; i < len(span); i++ {
		switch span[i] {
		case ';', '\n':
		case '.':
			if i+1 < len(span) && span[i+1] != ' ' && span[i+1] != '\t' {
				continue
			}
		default:
			continue
		}
		if clause := strings.TrimSpace(span[start : i+1]); clause != "" {
			found = append(found, clause)
		}
		start = i + 1
	}
	if clause := strings.TrimSpace(span[start:]); clause != "" {
		found = append(found, clause)
	}
	return found
}

// writtenPath is a path as prose writes one: at least one directory and a
// slash, and whatever name follows, which may be empty for a home written with
// its trailing slash. The character class is what a repository path is made of,
// so a Markdown link's brackets and a sentence's closing punctuation fall
// outside it — except the full stop, which is inside paths and is trimmed from
// the end of a match by the caller.
var writtenPath = regexp.MustCompile(`(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]*`)

// standingAlone is the matches of one shape in the text that are not part of
// something longer: a path inside a longer path is the longer path's, and an id
// inside a longer identifier is not the id.
func standingAlone(text string, shape *regexp.Regexp) []string {
	var found []string
	for _, at := range standingAloneAt(text, shape) {
		found = append(found, text[at[0]:at[1]])
	}
	return found
}

// standingAloneAt is standingAlone's matches as positions in the text, for a
// caller that has to read what stands beside each one.
func standingAloneAt(text string, shape *regexp.Regexp) [][]int {
	var found [][]int
	for _, at := range shape.FindAllStringIndex(text, -1) {
		if continues(text, at[0]-1) || continues(text, at[1]) {
			continue
		}
		found = append(found, at)
	}
	return found
}

// continues reports the byte at this position being one a path or an id could
// carry, so a match with one beside it is part of something longer.
func continues(text string, at int) bool {
	if at < 0 || at >= len(text) {
		return false
	}
	character := text[at]
	return character == '_' || character == '/' || character == '-' ||
		(character >= '0' && character <= '9') ||
		(character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z')
}

// fold turns a clause into one bounded line, cut on a rune boundary, so what is
// quoted back is text whatever the clause carried.
func fold(value string, limit int) string {
	return oneline.Fold(value, limit)
}
