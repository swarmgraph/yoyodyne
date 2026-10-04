package artifact

// The typed action an owning role writes one of its own documents with, and the
// gate that refuses one before anything reaches disk.
//
// A drafted document used to cross from a conversation to the repository by
// hand: fenced Markdown in a reply, an approval in prose, and then a person or
// an operator's agent transcribing the path, the frontmatter, the commit. The
// transcription is the seam, and it is where a governance error lives — the
// drafted content is right, and whatever places it invents a shape the store
// would have refused. Nothing about that step needs a human transcriber, and
// every part of it the harness does itself is a part that cannot be invented.
//
// So a document is emitted the way a proposed work item and a proposed
// amendment already are: as a typed action in a fenced block, carrying what the
// role decided and nothing about how it is stored. The harness does the rest —
// it refuses what the role may not write before anything is written, puts the
// document to the operator unless it qualifies under delegated authority,
// performs the write under the role's own authority through Authorize, generates
// the frontmatter the contract requires, and records any operator approval
// against the revision the write produced.
//
// What this is not is a way past ownership. Every write goes through the same
// Authorize the rest of this package's mutations go through, so the action layer
// can only ever refuse earlier than the store would: a role that emits an action
// for a kind it does not own is refused here, and would be refused again by the
// store if it were not. A role that owns no document at all cannot use this
// mechanism, and a proposal to the owner remains its only move.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// WriteFence opens the one block a reply may write a document in. It is a
// distinct language tag rather than plain Markdown so a drafted document can
// never be confused with Markdown an agent happens to be discussing, and a
// distinct one from the proposal and amendment fences because it asks for
// something neither of those does: a change to the repository.
const WriteFence = "```yoyodyne-artifact"

// Bounds on one write. A reply carries one document because a document is what
// an operator reads before approving it, and two in one reply is a reply nobody
// reads to the end. The body is a whole document and is bounded well under the
// file limit the store enforces, so a document that decodes here is one that
// can also be written; the block is bounded a little above the body, which is
// the only part of it that is large.
//
// maxWritesPerReplyText is the same bound as the contract states it; a test
// keeps the number an agent is told equal to the one enforced here.
const (
	MaxWritesPerReply     = 1
	maxWritesPerReplyText = "one"
	MaxWriteBodyBytes     = 64 << 10
	MaxWriteBlockBytes    = MaxWriteBodyBytes + (8 << 10)
)

// WriteAction is what one action does to a document: bring it into existence,
// or replace what an existing one says. They are the two the store performs
// under an owning role's authority, named as the role thinks of them.
//
// Ending a document is deliberately absent. Superseding and retiring say that
// intent stopped applying, which is a decision about the product rather than a
// document to transcribe, and neither is what this exists to close.
type WriteAction string

const (
	WriteCreate WriteAction = "create"
	WriteRevise WriteAction = "revise"
)

func (a WriteAction) Valid() bool {
	return a == WriteCreate || a == WriteRevise
}

// Write is one document exactly as its owning role wrote it: what the document
// is, what it says, and why it is being recorded. Everything the store owns —
// the lifecycle status, the revision log, the frontmatter, the file itself — is
// absent, because that is the half a transcriber used to invent.
type Write struct {
	Action WriteAction `json:"action"`
	// ID is the document's identity, which is also its file name. It is required
	// on both actions: a creation says what the new document is called, and a
	// revision says which document is being replaced.
	ID string `json:"id"`
	// Kind is what sort of document this is, and decides who may write it. It is
	// required on a creation and refused on a revision — the kind is what decides
	// the document's owner, so changing it would be a mutation that reassigns its
	// own authorization, and the store refuses that too.
	Kind Kind `json:"kind,omitempty"`
	// Title is the document's one-line title, required on a creation and
	// optional on a revision, where leaving it out keeps the title the document
	// already has.
	Title string `json:"title,omitempty"`
	// Supports names the documents upstream of this one. It is optional on both:
	// the brief is the root and a decision record is an account rather than a
	// link in the chain, and on a revision leaving it out keeps what the document
	// already declares.
	Supports []string `json:"supports,omitempty"`
	// Directory is the repository-relative directory a new document lands in: an
	// artifact home, or a directory beneath one. It is required on a creation and
	// refused on a revision, where the document is already somewhere and a
	// revision that moved it would break every reference to the file.
	Directory string `json:"directory,omitempty"`
	// Body is the document itself, everything below the frontmatter. It is
	// required on both actions, including a revision: this mechanism exists to
	// carry a drafted document to disk whole, and a revision that carried only a
	// new title would be a metadata edit wearing a document's clothes.
	Body string `json:"body"`
	// Reason is why this document is being recorded, kept as the revision. It is
	// required for the reason every revision's is: a change nobody explained is
	// one nobody can evaluate later.
	Reason string `json:"reason"`
	// Intent is the owning role's claim about what a creation or revision does
	// to intent. See rewording.go for when that claim is delegated.
	Intent Intent `json:"intent,omitempty"`
}

// Validate reports every contract violation in the write at once, so a block
// that is wrong in three ways is corrected once rather than three times.
//
// It judges the shape of the action alone. Whether the role may write this kind
// is Authorize, and whether the directory is one this project files documents
// in needs the configured homes, which one action does not carry.
func (w Write) Validate() error {
	var problems []error
	if !w.Action.Valid() {
		problems = append(problems, fmt.Errorf("action %q must be %q or %q", w.Action, WriteCreate, WriteRevise))
	}
	if err := domain.ValidateIdentifier("id", strings.TrimSpace(w.ID)); err != nil {
		problems = append(problems, err)
	}
	problems = append(problems, w.perActionProblems()...)
	if w.Intent != "" && !w.Intent.Valid() {
		problems = append(problems, fmt.Errorf("intent %q must be %q or %q, or left out", w.Intent, IntentConsistent, IntentFundamental))
	}
	switch body := strings.TrimSpace(w.Body); {
	case body == "":
		problems = append(problems, errors.New("body is required; an artifact is an identity attached to a document, and there is nothing to identify without one"))
	case len(body) > MaxWriteBodyBytes:
		problems = append(problems, fmt.Errorf("body is %d bytes, limit is %d", len(body), MaxWriteBodyBytes))
	}
	switch reason := strings.TrimSpace(w.Reason); {
	case reason == "":
		problems = append(problems, errors.New("reason is required, saying why this document is being recorded"))
	case len(reason) > MaxReasonBytes:
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(reason), MaxReasonBytes))
	}
	if len(w.Supports) > MaxSupportsCount {
		problems = append(problems, fmt.Errorf("supports names %d artifacts, limit is %d", len(w.Supports), MaxSupportsCount))
	}
	for index, reference := range w.Supports {
		if err := domain.ValidateIdentifier(fmt.Sprintf("supports[%d]", index), strings.TrimSpace(reference)); err != nil {
			problems = append(problems, err)
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid artifact write: %w", err)
	}
	return nil
}

// perActionProblems reports what one action requires and what it refuses. The
// two actions take different arguments, and an argument that means nothing for
// the action it was sent with is refused rather than ignored: a revision
// carrying a kind is a role that believes it is changing one.
func (w Write) perActionProblems() []error {
	var problems []error
	title := strings.TrimSpace(w.Title)
	if len(title) > MaxTitleBytes {
		problems = append(problems, fmt.Errorf("title is %d bytes, limit is %d", len(title), MaxTitleBytes))
	}
	switch w.Action {
	case WriteCreate:
		if !w.Kind.Valid() {
			problems = append(problems, fmt.Errorf("kind %q must be one of %s", w.Kind, renderKinds()))
		}
		if title == "" {
			problems = append(problems, errors.New("title is required on a creation"))
		}
		if strings.TrimSpace(w.Directory) == "" {
			problems = append(problems, errors.New("directory is required on a creation, naming the artifact home the document is filed in"))
		}
	case WriteRevise:
		if strings.TrimSpace(string(w.Kind)) != "" {
			problems = append(problems, errors.New("kind is not revisable; it decides who owns the document, so changing it would be a mutation that reassigns its own authorization"))
		}
		if strings.TrimSpace(w.Directory) != "" {
			problems = append(problems, errors.New("directory is not revisable; the document is already filed, and moving it would break every reference to the file"))
		}
	}
	return problems
}

// Authorize refuses a write the role may not make, from the action alone.
//
// A creation names its kind, so the boundary is the same Authorize every other
// mutation goes through. A revision does not — the kind is the document's, and
// what the action names is an id — so what is judged here is that the role owns
// some document at all, and which document it named is judged by CheckWrite,
// which can read the set the id resolves against. Both refuse before anything is
// written, and both consult the one ownership table rather than a second one.
func (w Write) Authorize(role domain.AgentRole) error {
	if w.Action == WriteCreate {
		return Authorize(role, w.Kind)
	}
	if len(Owned(role)) == 0 {
		return fmt.Errorf("%w; the %s owns no document, and proposes a change to one instead", ErrUnauthorized, roleName(role))
	}
	return nil
}

// Owned returns the kinds a role may write, in the order the kinds are
// declared. It is the ownership table read the other way round — Owner answers
// "who may write this", and this answers "what may this role write" — derived
// from the same table rather than restated beside it, so a kind that changed
// hands cannot be owned by one role and writable by another.
func Owned(role domain.AgentRole) []Kind {
	owned := make([]Kind, 0, len(Kinds()))
	for _, kind := range Kinds() {
		if owner, known := Owner(kind); known && owner == role {
			owned = append(owned, kind)
		}
	}
	if len(owned) == 0 {
		return nil
	}
	return owned
}

// ExtractWrites splits a reply into what the agent said and the document it
// wrote. A document comes only from the fenced block: Markdown in prose is
// something the agent is showing the operator, and treating it as a document to
// file is exactly the transcription this exists to end.
//
// What the agent said comes back without the block whichever way that goes, for
// the same reason a report's does: the reply is the role's actual output, and a
// block the harness could not read must not cost the operator the answer.
func ExtractWrites(reply string) (string, []Write, error) {
	block, err := fenced.Split(reply, WriteFence, "artifact")
	if err != nil {
		return block.Before, nil, err
	}
	if !block.Found {
		return block.Before, nil, nil
	}
	writes, err := DecodeWrites(block.Payload)
	if err != nil {
		return block.Before, nil, err
	}
	return block.Rest, writes, nil
}

// writeDocument is the payload shape of the fenced block. It carries a list
// even though only one is accepted, so the block reads as the others do and a
// role that sends two is told the bound rather than silently having one written.
type writeDocument struct {
	Documents []Write `json:"documents"`
}

// DecodeWrites strictly decodes the block payload. Unknown fields, trailing
// content, and oversized input are refused rather than tolerated: what the
// operator is asked to approve, and what is then written into the repository,
// has to be exactly what the role wrote.
func DecodeWrites(payload string) ([]Write, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode artifact writes: the artifact block is empty")
	}
	if len(trimmed) > MaxWriteBlockBytes {
		return nil, fmt.Errorf("decode artifact writes: block is %d bytes, limit is %d", len(trimmed), MaxWriteBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var decoded writeDocument
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode artifact writes: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode artifact writes: unexpected trailing content after the documents")
	}
	if len(decoded.Documents) == 0 {
		return nil, errors.New("decode artifact writes: an artifact block must carry at least one document")
	}
	if len(decoded.Documents) > MaxWritesPerReply {
		return nil, fmt.Errorf("decode artifact writes: %d documents in one reply, limit is %d",
			len(decoded.Documents), MaxWritesPerReply)
	}
	var problems []error
	for index, write := range decoded.Documents {
		if err := write.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("documents[%d]: %w", index, err))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid artifact writes: %w", errors.Join(problems...))
	}
	return decoded.Documents, nil
}

// Describe says what one write would do, in the words the operator reads before
// approving it. It is the action and the document rather than the document's
// prose, which is shown beside it.
func (w Write) Describe() string {
	switch w.Action {
	case WriteCreate:
		return fmt.Sprintf("create %s (%s) in %s", strings.TrimSpace(w.ID), w.Kind, strings.TrimSpace(w.Directory))
	default:
		return "revise " + strings.TrimSpace(w.ID)
	}
}

// Draft is what a creation asks the store for.
func (w Write) Draft() Draft {
	return Draft{
		ID:        strings.TrimSpace(w.ID),
		Kind:      w.Kind,
		Title:     strings.TrimSpace(w.Title),
		Supports:  trimmedList(w.Supports),
		Directory: strings.TrimSpace(w.Directory),
		Body:      strings.TrimSpace(w.Body),
		Reason:    strings.TrimSpace(w.Reason),
		Intent:    w.Intent,
	}
}

// Amendment is what a revision asks the store for. The title and what the
// document supports are sent only where the role named them, so a revision that
// mentions neither keeps both rather than replacing them with nothing.
func (w Write) Amendment() Amendment {
	body := strings.TrimSpace(w.Body)
	amendment := Amendment{Body: &body, Reason: strings.TrimSpace(w.Reason), Intent: w.Intent}
	if title := strings.TrimSpace(w.Title); title != "" {
		amendment.Title = &title
	}
	if len(w.Supports) > 0 {
		supports := trimmedList(w.Supports)
		amendment.Supports = &supports
	}
	return amendment
}

// DelegatedWrite checks whether a typed write records a consistent change under
// the Lead Product Manager's authority. Revisions use the recorded document's
// kind, so an action cannot claim ownership by supplying another kind.
func (s Store) DelegatedWrite(role domain.AgentRole, write Write) (bool, error) {
	if err := s.CheckWrite(role, write); err != nil {
		return false, err
	}
	document := Artifact{Kind: write.Kind}
	action := ActionCreated
	if write.Action == WriteRevise {
		existing, _, err := s.loadOne(strings.TrimSpace(write.ID))
		if err != nil {
			return false, err
		}
		document = existing
		action = ActionAmended
	}
	delegated, _ := document.Rewording(Revision{
		Action: action, By: role, Reason: write.Reason, Intent: write.Intent,
	})
	return delegated, nil
}

// CheckWrite refuses everything about a write that a refusal now would spare
// somebody later: its shape, the role's authority over the document it names,
// whether the directory a new document lands in is one this project files
// documents in, and whether the document a revision names is one that can be
// revised at all.
//
// It writes nothing, which is the point of it existing beside the store's own
// mutations rather than only inside them: the refusal happens before the
// operator is asked, so nobody is ever asked to approve a document the harness
// was always going to refuse, and no half-written state has to be undone. The
// store repeats each of these when it writes, because a check that holds only
// where a caller remembered to make it is not one.
//
// It reads the set, because the id is the only thing a revision names and the
// kind that decides its owner lives in the document. Refusing these here rather
// than at the write is what keeps a permanent refusal from being asked about: a
// role cannot revise a document it does not own, an id one document already
// answers to is not a second document's, and nothing an operator does at a
// prompt would ever change either answer. What that leaves for the write to
// refuse is a race — the document changing under the decision — which is the
// only refusal there that is worth telling somebody to try again after.
func (s Store) CheckWrite(role domain.AgentRole, write Write) error {
	if err := write.Validate(); err != nil {
		return err
	}
	if err := write.Authorize(role); err != nil {
		return err
	}
	id := strings.TrimSpace(write.ID)
	if write.Action == WriteCreate {
		if err := s.checkFiling(write.Kind, write.Directory); err != nil {
			return err
		}
	}
	set, err := s.Load()
	if err != nil {
		return err
	}
	found, exists := set.Find(id)
	if write.Action == WriteCreate {
		// One id names one artifact, so a creation over a document that already
		// answers to the id is refused — here rather than at the write, because it
		// is another answer that will not change however often it is asked.
		if exists {
			return fmt.Errorf("artifact %q already exists at %s; revise it instead", id, found.Path)
		}
		return nil
	}
	if !exists {
		return fmt.Errorf("no artifact %q is recorded in %s; a document that does not exist yet is created rather than revised",
			id, strings.Join(set.Homes, ", "))
	}
	if err := Authorize(role, found.Kind); err != nil {
		return err
	}
	// Reviving replaced intent by editing it is not a decision anybody made, and
	// the store refuses it too. It is refused here for the same reason the
	// ownership is: the answer will be the same whenever it is asked.
	if ended, hasEnding := found.Ended(); hasEnding {
		return fmt.Errorf("artifact %q was %s on %s and is not revised back into force: %s",
			id, ended.Action, ended.At.UTC().Format(time.RFC3339), ended.Reason)
	}
	return nil
}

// checkFiling refuses a new document filed anywhere but the home its kind
// belongs in. Being inside some artifact home is not enough: three homes in one
// configuration are three different sorts of document, and a design filed under
// the product manager's home is exactly the wrong-home mistake this gate exists
// to catch — it loads, it validates, and it sits in another role's directory
// where nothing but a reader would notice.
//
// A store that was not told where kinds are filed refuses the write rather than
// falling back to any home. The role is not offered the write in the first place
// on such a store, so reaching here means something asked for one anyway, and
// filing it on a guess is the outcome this whole path exists to prevent.
func (s Store) checkFiling(kind Kind, directory string) error {
	target, err := s.resolveDirectory(directory)
	if err != nil {
		return err
	}
	home, filed := s.homeFor(kind)
	if !filed {
		return fmt.Errorf("this project does not say where a %s artifact is filed, so there is nowhere to write one", kind)
	}
	expected, err := s.resolveDirectory(home)
	if err != nil {
		return err
	}
	if !within(target, []string{expected}) {
		return fmt.Errorf("artifact directory %q is not where a %s artifact is filed, which is %s", directory, kind, expected)
	}
	return nil
}

// Directories reports the artifact homes this store reads, which is what a
// refusal names when it says where documents live. Where a document is written
// is the narrower question Filing answers, per kind.
func (s Store) Directories() ([]string, error) {
	return resolveDirectories("artifact home", s.Homes)
}

// Checkout is the repository this store writes into, which is what a surface
// names when it says where a write landed: it is the configuration's answer
// rather than wherever the operator happens to be standing, and PendingCommit
// says why that difference matters.
func (s Store) Checkout() string {
	return s.RepositoryRoot
}

// roleName names a role in a refusal, and says something usable about a write
// that named none rather than printing an empty pair of quotes.
func roleName(role domain.AgentRole) string {
	if trimmed := strings.TrimSpace(string(role)); trimmed != "" {
		return trimmed
	}
	return "unnamed role"
}

// WriteContract is what a role that owns documents is told about writing one.
// It is generated from the ownership table and this project's own filing rather
// than written out per role, so a role is never told it may write a kind it does
// not own, and never asked to guess where a document of that kind goes.
//
// The filing is the role's own, per kind, rather than a list of every artifact
// home: an example that named the first configured home would hand the architect
// the product manager's directory, and a role that copies its own example would
// file the document in the wrong home — which is the mistake this whole path
// exists to stop, arriving through the instruction rather than around it.
//
// A role that owns nothing gets nothing, and so does a project whose filing is
// unknown: telling a role about a mechanism every one of its attempts would be
// refused by is an invitation to attempt it.
func WriteContract(role domain.AgentRole, filing []KindHome) string {
	if len(Owned(role)) == 0 || len(filing) == 0 {
		return ""
	}
	kinds := make([]string, 0, len(filing))
	directories := make([]string, 0, len(filing))
	for _, filed := range filing {
		kinds = append(kinds, `"`+string(filed.Kind)+`" in `+filed.Directory)
		if !slices.Contains(directories, filed.Directory) {
			directories = append(directories, filed.Directory)
		}
	}
	approval := `The operator approves the action before the harness writes it, and the harness records that approval against the revision it produces.`
	if role == domain.RoleProductManager {
		approval = `A creation or revision of any document you own in the product home may carry "intent":"consistent" with a reason opening with the work item that directed it. That records a consistent rewording or existing intent, such as rules the operator already gave: the harness writes it under your delegated authority without asking the operator or inventing their approval. A creation recorded that way starts approved under its reason; a revision preserves the approval already standing. A change that would make the goals admit work they refused or refuse work they admitted is fundamental: use "intent":"fundamental". Fundamental and unmarked writes still await the operator's approval, as does a consistent mark without the directing item. Say what existing intent a consistent creation records in its reason.`
	}
	return `# Writing a document you own

The documents you own are yours to write, and this is how one reaches the repository. You still have no tools: what you emit is an action, and the harness performs the write under your authority — it generates the frontmatter, appends the revision saying you made the change, and files the document. Nothing about the transcription is yours to get right. ` + approval + `

To write one, end your reply with exactly one block of this shape:

` + "```" + `yoyodyne-artifact
{"documents":[{"action":"create","id":"artifact-id","kind":"` + string(filing[0].Kind) + `","title":"one line","supports":["upstream-artifact-id"],"directory":"` + filing[0].Directory + `","body":"the whole document, in Markdown, below the frontmatter","reason":"why you are recording it"}]}
` + "```" + `

A revision replaces what an existing document says: ` + "`" + `{"action":"revise","id":"artifact-id","body":"the whole document","reason":"why it is changing"}` + "`" + `. It carries the document whole rather than the part that changed, and it takes "title" and "supports" only where those change too. "kind" and "directory" are refused on a revision — the kind decides who owns the document, and the file has already been referred to by where it is.

The kinds you may write, and where this project files each of them, are ` + strings.Join(kinds, ", ") + `. Any other kind is refused and nothing is written, and so is a document filed anywhere but the directory its own kind belongs in — being inside one of this project's ` + strings.Join(directories, " or ") + ` is not enough. The id is the file name without its extension, so it also has to be one: an id that already answers to a document is a revision rather than a creation.

A document you write is placed in the operator's working tree. Nothing here commits it, pushes it, or opens a pull request for it, so do not say that it did.

The body is a JSON string, so the document's own newlines and quotes are escaped in it — a Markdown document with code fences in it is carried perfectly well, and a block that is not valid JSON writes nothing at all. One reply carries ` + maxWritesPerReplyText + ` document, at most ` + fmt.Sprintf("%d", MaxWriteBodyBytes) + ` bytes of it, and leaves the block out entirely when you are not writing one, which is most replies. Never describe a document as written before the harness has told you it was.`
}
