package orchestrator

// Closing an item a conversation carries when what it asked for lands.
//
// A developer item closes when its change merges: the run claimed it, the
// reviewer approved it, and the reconciler reads the forge and closes the item
// with nobody typing anything. An item the architect's conversation carries had
// nothing of the kind. Its landing is a revision in a document the architect
// owns, and the only thing that ever read that revision back to the tracker was
// the product manager, closing the item on evidence some turns later —
// yoyodyne-ifd.282 at turn 440, yoyodyne-ifd.330 at turn 512, after a developer
// run had been spent on it. On the day this was written five more architect
// items (209.21, 294, 306, 313, 348) stood open with their revisions already in
// the designs; docs/diagnoses/yoyodyne-ifd-367-conversation-item-dispatched.md
// lists them.
//
// So the pass reads the landing. An item whose executor is a role's
// conversation is closed once a document that role owns carries a revision, by
// that role, whose reason opens with the item's identifier: "yoyodyne-ifd.330 -
// side conversations designed". Opening with it is the convention: a revision
// that merely mentions an item in passing — "published under yoyodyne-ifd.280
// after three reviewer reports" — is a revision about something else. The
// architect's revision log is the architect saying the item's work is in the
// document, which is exactly the evidence the product manager was reading by
// hand.
//
// The convention says which item a revision is for, and not that it is the
// revision the item asked for. An item that names the documents it is to revise
// — by path, "docs/designs/artifact-contract.md", or by identifier,
// "artifact-contract" — closes only once every one of those documents the role
// owns carries such a revision. A revision opening with its identifier in some
// other document is written onto the item's notes as related and closes
// nothing: the architect revising a third design under the item's identifier
// left the two passages the item named untouched, and the item closed anyway,
// before this rule. An item that names no document the role owns has nothing
// to check the revision against, so the first such revision closes it as it
// always did, and the close says no named document was checked. Only the
// item's own text — title, description, design, acceptance criteria — is read
// for names, never its notes: the related note this writes names the document
// it is about, and reading it back would make that document named.
//
// It reads the repository the pull is made from, which is the same tree every
// other reader of the artifact homes reads — the invariants delivered to a run,
// the documents a done-condition is checked against — and closes with a reason
// naming the document, the revision, and the convention, so a close that was
// wrong is one the product manager can read and reopen with a note.
//
// The reopen holds because the close is made once per revision rather than once
// per pull. Every pull reads every landing again, so a sweep with no memory
// would close a reopened item again a minute later and the note would be the
// only trace of the reopen. So the revision an item is closed on is written onto
// the item, in the tracker, before the close, and an entry still carrying that
// revision is one the harness already closed on it and somebody put back: it is
// left where they put it. A later revision opening with the same identifier is a
// new landing, and closes it again.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// LandingTracker is the tracker access one landing sweep needs: recording the
// revision an item is closed on, closing it with the reason on it, and adding a
// note about a related revision that did not close it. It is satisfied by
// beads.Client.
type LandingTracker interface {
	RecordLanding(ctx context.Context, id, landing string) (beads.WorkItem, error)
	Complete(ctx context.Context, id, reason string) (beads.WorkItem, error)
	Update(ctx context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error)
}

// ConversationLander closes conversation-executed items whose landing is in
// the repository.
type ConversationLander struct {
	Tracker LandingTracker
	// Repository is the checkout the artifact homes are read from, and Product
	// is where they are.
	Repository string
	Product    config.Product
}

// LandedConversation is one item this sweep closed, and the revision it closed
// it on.
type LandedConversation struct {
	WorkItemID string                  `json:"work_item_id"`
	Executor   domain.WorkItemExecutor `json:"executor"`
	// Document is the repository-relative path of the document the revision is
	// in, and Revision is the revision's own timestamp and reason. Where the item
	// names several documents it is the revision that completed the set.
	Document  string    `json:"document"`
	RevisedAt time.Time `json:"revised_at"`
	Reason    string    `json:"reason"`
	// Named is every revision the close rests on, one per document the item's
	// text names, and empty where the item names no document its role owns.
	Named       []LandingRevision `json:"named,omitempty"`
	CloseReason string            `json:"close_reason"`
}

// LandingRevision is one revision of one document, as the landing sweep reads
// it.
type LandingRevision struct {
	Document  string    `json:"document"`
	RevisedAt time.Time `json:"revised_at"`
	Reason    string    `json:"reason"`
}

// Key is the revision as the tracker records it: the document and the
// revision's time.
func (r LandingRevision) Key() string {
	return r.Document + "@" + r.RevisedAt.UTC().Format(time.RFC3339)
}

// Key is the revision as the tracker records it on the item: the document and
// the revision's time, which together name one revision and no other.
func (l LandedConversation) Key() string {
	return l.Document + "@" + l.RevisedAt.UTC().Format(time.RFC3339)
}

// LandingSweep is what one sweep did: the items it closed, the ones it found
// closed once already and reopened, and the ones it found landed and could not
// close.
type LandingSweep struct {
	Landed []LandedConversation `json:"landed,omitempty"`
	// Reopened is the entries whose landing this sweep found and left alone,
	// because the item already carries that revision as the one the harness
	// closed it on: somebody reopened it, and the reopen holds.
	Reopened []string `json:"reopened,omitempty"`
	// Related is the entries this sweep wrote a related revision onto: a
	// revision opening with the item's identifier in a document the item's text
	// does not name, which left the item open.
	Related []string `json:"related,omitempty"`
	// Problems name the items whose landing was found and whose close the
	// tracker refused, or the reading that failed. They are named rather than
	// counted because what a person does about one is specific to the item.
	Problems []string `json:"problems,omitempty"`
}

// Render is the sweep as a person reads it, one line per item.
func (s LandingSweep) Render() string {
	var rendered strings.Builder
	for _, landed := range s.Landed {
		fmt.Fprintf(&rendered, "%s was closed: its %s landed as the %s revision of %s\n",
			landed.WorkItemID, landed.Executor.Role(), landed.RevisedAt.UTC().Format("2006-01-02 15:04:05Z"), landed.Document)
	}
	return rendered.String()
}

func (l ConversationLander) validate() error {
	var problems []error
	if l.Tracker == nil {
		problems = append(problems, errors.New("a landing sweep requires a work tracker"))
	}
	if strings.TrimSpace(l.Repository) == "" {
		problems = append(problems, errors.New("a landing sweep requires the repository the artifact homes are read from"))
	}
	return errors.Join(problems...)
}

// Settle closes every entry among those given whose landing is in the
// repository, and reports what it did. Entries a developer run carries, and
// entries whose conversation's role owns no document, are passed over without
// the repository being read at all: a pull with nothing of the kind in its
// queue costs exactly what it cost before this existed.
//
// One reading of the artifact homes serves every entry. A reading that fails
// is reported and closes nothing, in the direction every other optional part
// of a pull fails in: an item closed on a document nobody could read would be
// the one thing worse than an item left open.
//
// Items is the tracker's reading of the same entries, by identifier, which is
// where each item's text is read for the documents it names. An entry missing
// from it is left open and named as a problem, for the same reason: which
// documents it asked for is not known, so neither is whether they landed.
func (l ConversationLander) Settle(ctx context.Context, entries []backlog.Entry, items map[string]beads.WorkItem) (LandingSweep, error) {
	if err := l.validate(); err != nil {
		return LandingSweep{}, err
	}
	var candidates []backlog.Entry
	for _, entry := range entries {
		if landsInADocument(entry.Executor) {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return LandingSweep{}, nil
	}
	set, err := artifact.StoreFor(l.Repository, l.Product).Load()
	if err != nil {
		return LandingSweep{}, fmt.Errorf("read the artifact homes the landings are in: %w", err)
	}
	var sweep LandingSweep
	for _, entry := range candidates {
		// An entry carrying a landing is one the harness closed on that revision
		// and somebody reopened. The reopen is their decision and this is not a
		// wait: what closes it again is a later revision, and nothing else. So
		// only revisions after the recorded one are read for such an entry.
		closedOn, reopened := recordedLanding(entry.Landing)
		if reopened && closedOn.IsZero() {
			sweep.Reopened = append(sweep.Reopened, entry.ID)
			continue
		}
		item, known := items[entry.ID]
		if !known {
			sweep.Problems = append(sweep.Problems, fmt.Sprintf("%s was not read from the tracker with the queue, so which documents it names is not known and it was not checked for a landing; it stays open", entry.ID))
			continue
		}
		judged := judgeLanding(set, entry, item, closedOn)
		if note := judged.relatedNote(entry, item); note != "" {
			// A related revision is said on the item whether or not the item closes,
			// so whoever reads it sees what the role revised under its identifier.
			// A note that could not be written is reported and costs the close
			// nothing: the close rests on the named documents, not on the note.
			if _, err := l.Tracker.Update(ctx, entry.ID, beads.WorkItemChange{AppendNotes: note}); err != nil {
				sweep.Problems = append(sweep.Problems, fmt.Sprintf("%s has a revision opening with its identifier in a document its text does not name, and the note saying so could not be written: %v", entry.ID, err))
			} else {
				sweep.Related = append(sweep.Related, entry.ID)
			}
		}
		landing, found := judged.landing, judged.found
		if !found {
			if reopened {
				sweep.Reopened = append(sweep.Reopened, entry.ID)
			}
			continue
		}
		// The revision is written onto the item before the close, so that an item
		// the close reaches carries what it was closed on whatever the tracker
		// does next. A record that could not be written closes nothing: a close
		// with no record behind it is exactly the close a reopen cannot hold
		// against.
		if _, err := l.Tracker.RecordLanding(ctx, entry.ID, landing.Key()); err != nil {
			sweep.Problems = append(sweep.Problems, fmt.Sprintf("%s landed as the %s revision of %s and the revision could not be recorded on it, so it was not closed and stays in the queue: %v",
				entry.ID, landing.RevisedAt.UTC().Format(time.RFC3339), landing.Document, err))
			continue
		}
		if _, err := l.Tracker.Complete(ctx, entry.ID, landing.CloseReason); err != nil {
			problem := fmt.Sprintf("%s landed as the %s revision of %s and could not be closed, so it stays in the queue until it is closed by hand: %v",
				entry.ID, landing.RevisedAt.UTC().Format(time.RFC3339), landing.Document, err)
			// The record is taken back off an item the close never reached, so the
			// next pull tries again rather than reading the item as reopened. Where
			// even that fails the item is open and marked, which the next pull reads
			// as a reopen and leaves alone — so it is said here, once, in words that
			// name the marker somebody would have to clear.
			if _, err := l.Tracker.RecordLanding(ctx, entry.ID, ""); err != nil {
				problem += fmt.Sprintf("; and the revision recorded on it ahead of the close could not be cleared, so every later pull reads the item as reopened and leaves it: clear its %s metadata to let the harness try again: %v", beads.LandingKey, err)
			}
			sweep.Problems = append(sweep.Problems, problem)
			continue
		}
		sweep.Landed = append(sweep.Landed, landing)
	}
	return sweep, nil
}

// landsInADocument reports an executor whose conversation's landing could be a
// revision at all: a conversation with a role that owns some kind of artifact.
// The development manager's conversation owns none — its work is decomposition
// in the tracker — so an item it carries is never read for one.
func landsInADocument(executor domain.WorkItemExecutor) bool {
	role := executor.Role()
	if role == "" {
		return false
	}
	for _, kind := range artifact.Kinds() {
		if owner, placed := artifact.Owner(kind); placed && owner == role {
			return true
		}
	}
	return false
}

// recordedLanding reads the time of the revision an entry was already closed
// on, from the key RecordLanding wrote, and whether it carries one at all. A key
// that does not parse is reported as carried with no time, and the caller
// leaves such an item alone: something the harness wrote is on it, and closing
// over a record nobody can read is the wrong guess.
func recordedLanding(key string) (time.Time, bool) {
	if strings.TrimSpace(key) == "" {
		return time.Time{}, false
	}
	_, stamp, split := strings.Cut(key, "@")
	if !split {
		return time.Time{}, true
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}, true
	}
	return at, true
}

// judgedLanding is what one entry's documents say about it: the landing, where
// there is one, and the revisions opening with its identifier in documents its
// text does not name.
type judgedLanding struct {
	landing LandedConversation
	found   bool
	// named is the documents the item's text names that its role owns, and
	// unlanded those of them with no revision opening with its identifier yet.
	named    []string
	unlanded []string
	related  []LandingRevision
}

// judgeLanding reads one entry's landing out of the set. A revision counts
// where it is in a document the entry's role owns, made by that role, with a
// reason that opens with the entry's identifier.
//
// Where the item's text names no document the role owns, the landing is the
// earliest such revision later than the one the entry was already closed on,
// because the item landed when the first one did. Where it names some, each of
// them needs one, and the landing is the revision that completed the set: the
// latest of each document's earliest. For an entry already closed once and
// reopened, the set has to be completed again by a revision after the one it
// was closed on, and the earliest such revision is the new landing.
func judgeLanding(set artifact.Set, entry backlog.Entry, item beads.WorkItem, after time.Time) judgedLanding {
	role := entry.Executor.Role()
	text := strings.Join([]string{item.Title, item.Description, item.Design, item.AcceptanceCriteria}, "\n")
	var (
		judged   judgedLanding
		earliest = map[string]LandingRevision{}
		latest   = map[string]LandingRevision{}
		opening  []LandingRevision
	)
	named := map[string]bool{}
	for _, document := range set.Artifacts {
		if owner, placed := artifact.Owner(document.Kind); !placed || owner != role {
			continue
		}
		if namesDocument(text, document) {
			named[document.Path] = true
			judged.named = append(judged.named, document.Path)
		}
		for _, revision := range document.Revisions {
			if revision.By != role || !opensWith(revision.Reason, entry.ID) {
				continue
			}
			read := LandingRevision{Document: document.Path, RevisedAt: revision.At, Reason: revision.Reason}
			opening = append(opening, read)
			if first, seen := earliest[document.Path]; !seen || read.RevisedAt.Before(first.RevisedAt) {
				earliest[document.Path] = read
			}
			if last, seen := latest[document.Path]; !seen || read.RevisedAt.After(last.RevisedAt) {
				latest[document.Path] = read
			}
		}
	}
	if len(judged.named) == 0 {
		var first LandingRevision
		for _, revision := range opening {
			if revision.RevisedAt.After(after) && (!judged.found || revision.RevisedAt.Before(first.RevisedAt)) {
				first, judged.found = revision, true
			}
		}
		if judged.found {
			judged.landing = landedOn(entry, first, nil)
		}
		return judged
	}
	for _, revision := range opening {
		if !named[revision.Document] {
			judged.related = append(judged.related, revision)
		}
	}
	var completed LandingRevision
	for _, path := range judged.named {
		first, landed := earliest[path]
		if !landed {
			judged.unlanded = append(judged.unlanded, path)
			continue
		}
		if completed.Document == "" || first.RevisedAt.After(completed.RevisedAt) {
			completed = first
		}
	}
	if len(judged.unlanded) > 0 {
		return judged
	}
	if !completed.RevisedAt.After(after) {
		// Closed on this set once and reopened: what closes it again is a later
		// revision of a document it names, the earliest of them.
		completed = LandingRevision{}
		for _, revision := range opening {
			if named[revision.Document] && revision.RevisedAt.After(after) && (completed.Document == "" || revision.RevisedAt.Before(completed.RevisedAt)) {
				completed = revision
			}
		}
		if completed.Document == "" {
			return judged
		}
	}
	rests := make([]LandingRevision, 0, len(judged.named))
	for _, path := range judged.named {
		revision := earliest[path]
		if path == completed.Document {
			revision = completed
		} else if last := latest[path]; last.RevisedAt.After(after) && after.After(revision.RevisedAt) {
			revision = last
		}
		rests = append(rests, revision)
	}
	judged.landing, judged.found = landedOn(entry, completed, rests), true
	return judged
}

// landedOn is the landing of an entry on one revision, with the revisions of
// every document its text names where it names some.
func landedOn(entry backlog.Entry, revision LandingRevision, named []LandingRevision) LandedConversation {
	landing := LandedConversation{
		WorkItemID: entry.ID,
		Executor:   entry.Executor,
		Document:   revision.Document,
		RevisedAt:  revision.RevisedAt,
		Reason:     revision.Reason,
		Named:      named,
	}
	landing.CloseReason = landingCloseReason(landing)
	return landing
}

// namesDocument reports item text naming a document by its repository-relative
// path or by its identifier as a whole word: "artifact-contract" names the
// document with that identifier, and "artifact-contract-notes" does not.
func namesDocument(text string, document artifact.Artifact) bool {
	if path := strings.TrimSpace(document.Path); path != "" && strings.Contains(text, path) {
		return true
	}
	id := strings.TrimSpace(document.ID)
	if id == "" {
		return false
	}
	for offset := 0; ; {
		found := strings.Index(text[offset:], id)
		if found < 0 {
			return false
		}
		start := offset + found
		end := start + len(id)
		if (start == 0 || !artifactIDByte(text[start-1])) && (end == len(text) || !artifactIDByte(text[end])) {
			return true
		}
		offset = start + 1
	}
}

// artifactIDByte reports a byte that can continue an artifact identifier, so a
// match running into one is part of a longer word.
func artifactIDByte(b byte) bool {
	return b == '-' || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// relatedNote is the note written onto an item for the related revisions it
// does not yet carry, or empty where there are none. Each revision is written
// once: the note names it by the same key a close records, and a revision
// whose key the item's notes already hold was said at an earlier pull.
func (j judgedLanding) relatedNote(entry backlog.Entry, item beads.WorkItem) string {
	var fresh []LandingRevision
	for _, revision := range j.related {
		if !strings.Contains(item.Notes, revision.Key()) {
			fresh = append(fresh, revision)
		}
	}
	if len(fresh) == 0 {
		return ""
	}
	role := entry.Executor.Role()
	var note strings.Builder
	note.WriteString("Related revision, recorded by the harness: ")
	for index, revision := range fresh {
		if index > 0 {
			note.WriteString("; ")
		}
		fmt.Fprintf(&note, "the %s revision %s opens with this item's identifier (%s)", role, revision.Key(), singleLine(revision.Reason, maxLandingReasonBytes))
	}
	fmt.Fprintf(&note, ". This item's text does not name that document, so the revision did not close it and it stays open. It closes once each document it names has a revision by the %s opening with its identifier: %s.",
		role, strings.Join(j.named, ", "))
	if len(j.unlanded) > 0 {
		fmt.Fprintf(&note, " Still without one: %s.", strings.Join(j.unlanded, ", "))
	}
	return note.String()
}

// opensWith reports a revision reason that begins with the item's identifier
// as a whole word: "yoyodyne-ifd.330 - side conversations designed" opens with
// yoyodyne-ifd.330, and "yoyodyne-ifd.330.1 - the record and lease" does not.
// A quote or bracket ahead of the identifier is skipped, because the reason is
// YAML and a reason with a colon in it is written quoted.
func opensWith(reason, id string) bool {
	trimmed := strings.TrimLeft(strings.TrimSpace(reason), `"'([`)
	if !strings.HasPrefix(trimmed, id) {
		return false
	}
	rest := trimmed[len(id):]
	if rest == "" {
		return true
	}
	next := rest[0]
	return !(next == '.' && len(rest) > 1 && rest[1] >= '0' && rest[1] <= '9') &&
		next != '-' && next != '_' &&
		!(next >= '0' && next <= '9') && !(next >= 'a' && next <= 'z') && !(next >= 'A' && next <= 'Z')
}

// maxLandingReasonBytes bounds how much of the revision's own reason the close
// carries. The revision is the record; the close names it.
const maxLandingReasonBytes = 400

// landingCloseReason is what the tracker records about a close made here: which
// documents, which revisions, the rule it was read by, and what the revision
// said, so a close somebody disagrees with is one they can read and reopen with
// a note rather than one they have to reconstruct.
func landingCloseReason(landing LandedConversation) string {
	role := landing.Executor.Role()
	if len(landing.Named) == 0 {
		return fmt.Sprintf("Closed by the harness: the %s revision of %s at %s opens with this item's identifier, which is the %s recording that the item's work is in the document. The item's text names no document the %s owns, so no named document was checked. The revision's reason: %s",
			role, landing.Document, landing.RevisedAt.UTC().Format(time.RFC3339), role, role, singleLine(landing.Reason, maxLandingReasonBytes))
	}
	revisions := make([]string, 0, len(landing.Named))
	for _, revision := range landing.Named {
		revisions = append(revisions, fmt.Sprintf("%s at %s", revision.Document, revision.RevisedAt.UTC().Format(time.RFC3339)))
	}
	return fmt.Sprintf("Closed by the harness: every document this item's text names has a %s revision that opens with this item's identifier (%s), which is the %s recording that the item's work is in those documents. The last of them, the revision of %s at %s, closed it. Its reason: %s",
		role, strings.Join(revisions, "; "), role, landing.Document, landing.RevisedAt.UTC().Format(time.RFC3339), singleLine(landing.Reason, maxLandingReasonBytes))
}
