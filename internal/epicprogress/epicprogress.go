// Package epicprogress keeps what the harness remembers about each epic the
// operator named: the pieces that joined it, the review comments acted on, the
// checks of the whole epic, its delivery, and each start and paid call reserved
// for it. The operator's own record (epicscope) is another file, which nothing
// here writes.
//
// epic-progress.json holds one entry for each naming. epic-progress-begun.json
// lists the namings whose entry was begun, which shows that an entry once existed.
// docs/designs/epic-mode-second-part.md says what each being missing means.
package epicprogress

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

const (
	// SchemaVersion is 1 for both files and has never changed.
	SchemaVersion = 1

	// maxFileBytes bounds each file, for a write and a read alike.
	maxFileBytes = 8 << 20

	progressName = "epic-progress.json"
	begunName    = "epic-progress-begun.json"
	lockName     = progressName + ".lock"

	judgingLockName = progressName + ".judging.lock"
)

// keptSince is about when the harness first kept progress. An epic named before
// it may have been worked on before anything was kept.
var keptSince = time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)

// Naming is one naming of an epic, as the operator's record holds it. An epic
// that ended and was named again is another naming.
type Naming struct {
	WorkItemID string    `json:"work_item_id"`
	NamedAt    time.Time `json:"named_at"`
}

// Of is the naming an entry of the operator's record is.
func Of(epic epicscope.Epic) Naming {
	return Naming{WorkItemID: epic.WorkItemID, NamedAt: epic.NamedAt.UTC()}
}

func (n Naming) same(other Naming) bool {
	return n.WorkItemID == other.WorkItemID && n.NamedAt.Equal(other.NamedAt)
}

func (n Naming) valid() bool { return beads.ValidIssueID(n.WorkItemID) && !n.NamedAt.IsZero() }

// Progress is epic-progress.json.
type Progress struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Entries       []Entry          `json:"entries"`
}

// Begun is epic-progress-begun.json.
type Begun struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	Namings       []Naming         `json:"namings"`
}

// Entry is what is remembered of one naming.
type Entry struct {
	Naming
	BegunAt time.Time `json:"begun_at"`
	// NamedBeforeKept is that the epic was named before the harness kept
	// progress, as far as it can tell (keptSince).
	NamedBeforeKept bool          `json:"named_before_kept,omitempty"`
	Joined          []Joined      `json:"joined,omitempty"`
	Pieces          []Piece       `json:"pieces,omitempty"`
	Comments        []Comment     `json:"comments,omitempty"`
	Checks          []Check       `json:"checks,omitempty"`
	Deliveries      []Delivery    `json:"deliveries,omitempty"`
	Reservations    []Reservation `json:"reservations,omitempty"`
	// CopiedLate is the text of each member put in before the operator's record
	// copied members' texts, copied the first time a piece of it was judged and
	// used from then on.
	CopiedLate []CopiedLate `json:"copied_late,omitempty"`
	// Blockers is each open item outside the epic found able to block a member,
	// with the question about scope filed for it. One item files one question
	// for one naming, and its identifier is written here before it is filed.
	Blockers []Blocker `json:"blockers,omitempty"`
}

// CopiedLate is a member's text copied by the harness, late, rather than by the
// operator's act that put it in.
type CopiedLate struct {
	WorkItemID string         `json:"work_item_id"`
	Text       epicscope.Text `json:"text"`
	At         time.Time      `json:"at"`
}

// MemberText is the text a piece split from a member is judged against: the copy
// the operator's act made, or the one copied late. late says it is the second.
func (e Entry) MemberText(epic epicscope.Epic, item string) (text epicscope.Text, late, found bool) {
	if text, copied := epic.MemberText(item); copied {
		return text, false, true
	}
	if at := slices.IndexFunc(e.CopiedLate, func(c CopiedLate) bool { return c.WorkItemID == item }); at >= 0 {
		return e.CopiedLate[at].Text, true, true
	}
	return epicscope.Text{}, false, false
}

// Blocker is an open item outside the epic that a scheduler pass found able to
// block a member, the member, how it is linked, and the report that asks the
// Lead Product Manager about it (docs/designs/epic-mode-second-part.md, "An item
// outside the epic that could block a member", point 5).
type Blocker struct {
	WorkItemID string    `json:"work_item_id"`
	Member     string    `json:"member"`
	Link       string    `json:"link"`
	Report     string    `json:"report"`
	At         time.Time `json:"at"`
}

// Blocker is the question kept for an item, where one is.
func (e Entry) Blocker(item string) (Blocker, bool) {
	at := slices.IndexFunc(e.Blockers, func(b Blocker) bool { return b.WorkItemID == item })
	if at < 0 {
		return Blocker{}, false
	}
	return e.Blockers[at], true
}

// Joined is a piece that joined the epic, the member or check it came from, and
// the judgment that let it join, with that judgment's session.
type Joined struct {
	WorkItemID string    `json:"work_item_id"`
	From       string    `json:"from"`
	Judgment   string    `json:"judgment"`
	Session    string    `json:"session"`
	At         time.Time `json:"at"`
}

// MaxJoined is how many pieces may join one naming. Nothing raises it. A piece the
// operator took out still counts, and an item the operator added does not.
const MaxJoined = 12

// Piece is one made by an act that may let it join, and whether it did. Judgment
// is the reservation of its latest judgment, and Outcome is nil until it is
// judged. docs/designs/epic-mode-second-part.md says under piece 9 how a piece
// edited after it was judged is judged again.
type Piece struct {
	WorkItemID string    `json:"work_item_id"`
	From       string    `json:"from"`
	Judgment   string    `json:"judgment"`
	At         time.Time `json:"at"`
	Outcome    *Outcome  `json:"outcome,omitempty"`
	// Digest is of the text the latest judgment judges (TextDigest). A piece
	// recorded before texts were kept has none.
	Digest string `json:"digest,omitempty"`
	// Question is the report that put the latest judgment, of outside or cannot
	// tell, to the Lead Product Manager as a question about scope.
	Question string `json:"question,omitempty"`
	// Answered is the Lead Product Manager's answer to Question, once the harness
	// has read her recorded handling of it.
	Answered *ScopeAnswer `json:"answered,omitempty"`
	// Seen is when each text that no judgment judged was first seen while the
	// piece waited on its question, so that only an edit seen after her answer
	// is judged.
	Seen []Seen `json:"seen,omitempty"`
	// Earlier is each judgment of an earlier text, oldest first.
	Earlier []EarlierJudgment `json:"earlier,omitempty"`
	// WithSource is that the latest judgment was given the text of what the piece
	// came from beside the epic's description. A judgment made before that was not.
	WithSource bool `json:"with_source,omitempty"`
	// Reread is the question about scope the latest judgment answers, where it is
	// the one judgment made once more, with that text, of a piece judged outside or
	// cannot tell before that text was given and whose question had no handling.
	Reread *Reread `json:"reread,omitempty"`
}

// Reread is a question about scope that a judgment made once more answers. Judged
// inside, Answered is set: the judgment answered the question, which the Lead
// Product Manager's next pass is shown unhandled. Judged otherwise, the piece's
// Question is that same report again, so no second one is filed.
type Reread struct {
	Question string `json:"question"`
	Answered bool   `json:"answered,omitempty"`
}

// MayReread reports a piece whose latest judgment, of outside or cannot tell, was
// made without the text of what it came from, and whose question has no recorded
// handling: it is judged once more with that text.
func (p Piece) MayReread() bool {
	return !p.WithSource && p.Reread == nil && p.Outcome != nil && p.Outcome.Answer.Asks() && p.Question != "" && p.Answered == nil
}

// ScopeAnswer is what the Lead Product Manager's handling of a question about
// scope recorded: when, and, where it recorded that only the operator can act,
// the reason from his closed list and her words. Naming him decides only that he
// is asked: the harness puts one question to him (Asked), and the piece waits and
// is judged no more. Any other answer has the development manager act on it: a
// piece rewritten is judged again at the first edit seen after it, and one she
// leaves out stays as it is.
type ScopeAnswer struct {
	At       time.Time                `json:"at"`
	Operator ownership.OperatorReason `json:"operator,omitempty"`
	Says     string                   `json:"says,omitempty"`
	// Asked is the one question the harness put to the operator, where her
	// handling named him: its identifier is written here before it is filed, so a
	// restart files that same question and no second one.
	Asked *OperatorQuestion `json:"asked,omitempty"`
}

// OperatorQuestion is the question about scope the harness put to the operator
// once the Lead Product Manager's handling named him. It is built from the
// identifiers and offers what where the piece stands allows: a piece that never
// joined is offered the add, and one that joined, whose edited text was judged
// outside, the add, which keeps it as it now reads, or the remove, which lets the
// epic be delivered without it. docs/designs/epic-mode-second-part.md, "What
// records the answer on scope", points 5 to 7, states the rule.
type OperatorQuestion struct {
	Report string    `json:"report"`
	At     time.Time `json:"at"`
	// Joined is that the piece had joined the epic when it was asked.
	Joined bool `json:"joined,omitempty"`
}

func (a *ScopeAnswer) valid() bool {
	return a == nil || !a.At.IsZero() && (a.Operator == "" && a.Says == "" && a.Asked == nil || a.Operator.Valid() && strings.TrimSpace(a.Says) != "") &&
		(a.Asked == nil || report.ValidID(a.Asked.Report) && !a.Asked.At.IsZero())
}

// Offers is each command the operator is offered for a piece, in the order the
// question names them: the add, and the remove where the piece joined.
func (q OperatorQuestion) Offers(epic, piece string) []string {
	offers := []string{fmt.Sprintf("yoyo epic add %s %s", epic, piece)}
	if q.Joined {
		offers = append(offers, fmt.Sprintf("yoyo epic remove %s %s --words <text>", epic, piece))
	}
	return offers
}

// Seen is a text first seen at a time.
type Seen struct {
	Digest string    `json:"digest"`
	At     time.Time `json:"at"`
}

// EarlierJudgment is a judgment of a piece's text that a later one followed.
type EarlierJudgment struct {
	Judgment string  `json:"judgment"`
	Digest   string  `json:"digest,omitempty"`
	Outcome  Outcome `json:"outcome"`
	Question string  `json:"question,omitempty"`
}

// TextDigest is the digest of what a joining judgment judges of an item: its
// title, description and acceptance criteria. Its notes, priority and labels are
// not part of it.
func TextDigest(item beads.WorkItem) string {
	sum := sha256.New()
	for _, part := range []string{item.Title, item.Description, item.AcceptanceCriteria} {
		fmt.Fprintf(sum, "%d:%s\n", len(part), part)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// JudgedAs is the answer a judgment gave the text with this digest, where one
// did. A piece recorded before texts were kept has judged no text.
func (p Piece) JudgedAs(digest string) (Answer, bool) {
	if p.Outcome != nil && p.Digest == digest && digest != "" && p.Outcome.Answer.judges() {
		return p.Outcome.Answer, true
	}
	for _, earlier := range slices.Backward(p.Earlier) {
		if earlier.Digest == digest && digest != "" && earlier.Outcome.Answer.judges() {
			return earlier.Outcome.Answer, true
		}
	}
	return "", false
}

// SeenAt is when a text was first seen while the piece waited on its question.
func (p Piece) SeenAt(digest string) (time.Time, bool) {
	at := slices.IndexFunc(p.Seen, func(seen Seen) bool { return seen.Digest == digest })
	if at < 0 {
		return time.Time{}, false
	}
	return p.Seen[at].At, true
}

// TextWaits reads the records and answers TextWait for one reading of an item.
// While epic mode is off nothing waits, and records that cannot be read are an
// error, on which nothing starts.
func TextWaits(stateRoot string, product domain.ProductID, item beads.WorkItem) (string, error) {
	record, err := epicscope.Read(stateRoot, product)
	if err != nil || !record.On() {
		return "", err
	}
	view, err := Read(stateRoot, product, record)
	if err != nil {
		return "", err
	}
	return view.TextWait(item), nil
}

// TextWait is why a piece that joined an open epic starts no developer on its
// text as it reads now, and empty where it may. An item the operator named or
// added is never compared, and one that did not join is the one decision's.
func (v View) TextWait(item beads.WorkItem) string {
	for _, epic := range v.Record.OpenEpics() {
		entry, _ := v.Entry(Of(epic))
		if epic.Has(item.ID) || !entry.Has(item.ID) || epic.TakenOut(item.ID) {
			continue
		}
		// Every piece the harness lets join was asked about first. One the record
		// says joined without that has nothing a judgment could be added to.
		piece, asked := entry.Asked(item.ID)
		if !asked {
			continue
		}
		digest := TextDigest(item)
		answer, judged := piece.JudgedAs(digest)
		switch {
		case judged && answer == AnswerInside:
			return ""
		case piece.Outcome != nil && piece.Outcome.Answer.Asks() && piece.Answered != nil && piece.Answered.Operator != "":
			return fmt.Sprintf("the Lead Product Manager's answer to %s names the operator, so it waits until he keeps it with `yoyo epic add` or takes it out of epic %s with `yoyo epic remove`", piece.Question, epic.WorkItemID)
		case judged:
			question := ""
			if piece.Digest == digest && piece.Question != "" {
				question = " (report " + piece.Question + ")"
			}
			return fmt.Sprintf("its text as it reads now was judged not to be asked for by epic %s, so it waits as a question about scope for the Lead Product Manager%s", epic.WorkItemID, question)
		case piece.Outcome != nil && !piece.Outcome.Answer.judges():
			return fmt.Sprintf("it is kept in epic %s as %s, and no judgment of its text was made", epic.WorkItemID, piece.Outcome.Answer)
		}
		return fmt.Sprintf("its title, description or acceptance criteria changed since a reviewer judged it inside epic %s, so it waits for the new text to be judged", epic.WorkItemID)
	}
	return ""
}

// Answer is what decided whether a piece joined.
type Answer string

// Every answer but AnswerInside is a question about scope.
const (
	AnswerInside     Answer = "inside"
	AnswerOutside    Answer = "outside"
	AnswerCannotTell Answer = "cannot tell"
	AnswerLimit      Answer = "limit reached"
	AnswerTakenOut   Answer = "taken out"
)

// Valid reports an answer this build knows.
func (a Answer) Valid() bool {
	return slices.Contains([]Answer{AnswerInside, AnswerOutside, AnswerCannotTell, AnswerLimit, AnswerTakenOut}, a)
}

// judges reports an answer a reviewer session gave a piece's text, as opposed to
// one the harness kept without a judgment.
func (a Answer) judges() bool {
	return a == AnswerInside || a == AnswerOutside || a == AnswerCannotTell
}

// Asks reports an answer that is a question about scope.
func (a Answer) Asks() bool { return a == AnswerOutside || a == AnswerCannotTell }

// Outcome is what became of a piece. Session is the judging session's, where one
// answered.
type Outcome struct {
	At      time.Time `json:"at"`
	Answer  Answer    `json:"answer"`
	Why     string    `json:"why"`
	Session string    `json:"session,omitempty"`
}

func (o Outcome) valid() bool { return o.Answer.Valid() && o.Why != "" && !o.At.IsZero() }

// Asked is what is kept of a piece that may join, where anything is.
func (e Entry) Asked(item string) (Piece, bool) {
	if at := e.piece(item); at >= 0 {
		return e.Pieces[at], true
	}
	return Piece{}, false
}

func (e Entry) piece(item string) int {
	return slices.IndexFunc(e.Pieces, func(piece Piece) bool { return piece.WorkItemID == item })
}

// Full is why no further piece may join, and empty while one may.
func (e Entry) Full() string {
	if len(e.Joined) < MaxJoined {
		return ""
	}
	return fmt.Sprintf("%d pieces have joined, which is the most that may", len(e.Joined))
}

// Reservation is the one kept under an identifier, where one is.
func (e Entry) Reservation(id string) (Reservation, bool) {
	if at := slices.IndexFunc(e.Reservations, func(r Reservation) bool { return r.ID == id }); at >= 0 {
		return e.Reservations[at], true
	}
	return Reservation{}, false
}

// Comment is a review comment acted on, by the forge's identifier. A change
// request on a member's open pull request is written here when it is first read,
// with the identifier its judgment is reserved under, before that judgment is
// made; so the same comment read twice, or again after a restart, is judged once.
type Comment struct {
	ID     string    `json:"id"`
	Became string    `json:"became"`
	At     time.Time `json:"at"`
	// Member is the item whose pull request, PullRequest, the comment is on.
	Member      string `json:"member,omitempty"`
	PullRequest int    `json:"pull_request,omitempty"`
	// Judgment is the reservation of the judgment, and Outcome its answer once
	// known. Question is the report that puts an answer of outside or cannot tell
	// to the Lead Product Manager, written before it is filed.
	Judgment string   `json:"judgment,omitempty"`
	Outcome  *Outcome `json:"outcome,omitempty"`
	Question string   `json:"question,omitempty"`
	// Owed is the one correcting piece owed for feedback on a member's merged pull
	// request, read while the epic was open and not delivered: the development
	// manager writes it, naming the comment, and the harness writes the piece's
	// identifier here before the note on the item. Such a comment is written with
	// Owed in the one write that first records it, and is never judged as a change
	// request is.
	Owed *Correction `json:"owed,omitempty"`
	// Later is feedback on a member's merged pull request read once the epic was
	// delivered, integrated or handed over. It owes no piece and is never judged: it
	// becomes one parked work item the harness files for the Lead Product Manager,
	// who asks the operator whether to add it. The comment is written with Later in
	// the one write that first records it, and the item's identifier after the
	// item is filed.
	Later *Later `json:"later,omitempty"`
}

// Later is what feedback after delivery became.
type Later struct {
	// Says is who said what, as the item and the Lead Product Manager are shown it.
	Says string `json:"says"`
	// Item is the parked work item filed for it, once it is filed.
	Item string `json:"item,omitempty"`
}

// LaterMarker is the line written into the notes of the item filed for feedback
// after delivery, by which the harness finds that item again where a stop came
// between filing it and writing its identifier here.
func LaterMarker(epic string, comment string) string {
	return fmt.Sprintf("Filed for feedback after delivery: epic %s, review comment %s.", epic, comment)
}

// FromComment is what a piece written for the correction a review comment owes
// is recorded as made from, in the place a split records the member.
func FromComment(id string) string { return "the review comment " + id }

// CorrectingComment is the review comment whose owed correction names the item,
// where one does.
func (e Entry) CorrectingComment(item string) (Comment, bool) {
	if at := slices.IndexFunc(e.Comments, func(c Comment) bool { return c.Owed != nil && c.Owed.Piece == item }); item != "" && at >= 0 {
		return e.Comments[at], true
	}
	return Comment{}, false
}

// CommentCorrectionNote is the note of fixed form written on a piece correcting a
// review comment, for people to read. Nothing reads it to decide anything.
func CommentCorrectionNote(epic string, comment Comment) string {
	return fmt.Sprintf("Written as the correction owed by review comment %s on merged pull request #%d of %s, in the epic %s.", comment.ID, comment.PullRequest, comment.Member, epic)
}

// Comment is the review comment kept under the forge's identifier, where one is.
func (e Entry) Comment(id string) (Comment, bool) {
	if at := slices.IndexFunc(e.Comments, func(c Comment) bool { return c.ID == id }); at >= 0 {
		return e.Comments[at], true
	}
	return Comment{}, false
}

// Repaired reports a check of the whole epic that is owed again, not being a
// round of correction: the latest check passed, a change still handed over for a
// person to merge has an approved head it did not read, because a change request
// on its pull request was repaired since, and nothing joined or was added after
// it. heads is every approved head handed over now.
func (e Entry) Repaired(epic epicscope.Epic, heads []string) bool {
	if len(e.Checks) == 0 {
		return false
	}
	last := e.Checks[len(e.Checks)-1]
	read := strings.Fields(last.Commit)
	if !last.Passed() || !slices.ContainsFunc(heads, func(head string) bool { return head != "" && !slices.Contains(read, head) }) {
		return false
	}
	return !slices.ContainsFunc(e.Joined, func(j Joined) bool { return j.At.After(last.At) }) &&
		!slices.ContainsFunc(epic.Members, func(m epicscope.Member) bool { return m.AddedAt.After(last.At) })
}

// What a check of the whole epic found. A check that found anything else did not
// pass, and its Found says what could not be told.
const (
	// FoundAllMet is everything met, or taken out by the operator.
	FoundAllMet     = "all met"
	FoundNotMet     = "not met"
	FoundCannotTell = "cannot be told"
	// FoundPersonsStep is something the description itself reserves for a person.
	FoundPersonsStep = "reserved for a person"
)

// Passed reports a check that found everything met or taken out.
func (c Check) Passed() bool { return c.Found == FoundAllMet }

// Check is one check of the whole epic: the commit it read and, for each thing
// the description asks, one answer. Found is worked out from the answers.
type Check struct {
	ID       string    `json:"id"`
	Commit   string    `json:"commit"`
	Found    string    `json:"found"`
	At       time.Time `json:"at"`
	Findings []Finding `json:"findings,omitempty"`
	// Owed is the one correction owed for this check, where one is: for what it
	// found missing, or for the evidence the Lead Product Manager's handling of the
	// question in Asked named, for a check that could not tell.
	Owed *Correction `json:"owed,omitempty"`
	// Asked is the question about the missing evidence the harness put to the Lead
	// Product Manager for a check that could not tell, where it has.
	Asked *Asked `json:"asked,omitempty"`
	// Again is a check made again on the new approved heads after a change handed
	// over was repaired (Entry.Repaired). It is not a round of correction.
	Again bool `json:"again,omitempty"`
}

// Asked is the one report the harness files the Lead Product Manager for a check
// that could not tell, asking what evidence would settle it. Its identifier is
// written here before the report is filed. Her recorded handling of it is her
// answer, and nothing else counts.
type Asked struct {
	Report string    `json:"report"`
	At     time.Time `json:"at"`
	// Answered is when her handling was recorded, once the harness has read it.
	Answered *time.Time `json:"answered,omitempty"`
	// Operator is the reason from the operator's closed list her handling gave,
	// where it recorded that only he can act, and Says is what it said he has to
	// do. No correction is owed then, and the epic waits on him.
	Operator ownership.OperatorReason `json:"operator,omitempty"`
	Says     string                   `json:"says,omitempty"`
}

func (a *Asked) valid() bool {
	return a == nil || report.ValidID(a.Report) && !a.At.IsZero() &&
		(a.Operator == "" && a.Says == "" || a.Answered != nil && a.Operator.Valid() && strings.TrimSpace(a.Says) != "")
}

// Correction is the one correcting piece owed for a check of the whole epic. The
// development manager writes it, naming the check, and the harness writes the
// piece's identifier here before the note on the item. A piece is a correction
// only where a correction names it: the note is for people to read.
type Correction struct {
	// Missing is what the piece has to give: what the check found missing, or the
	// evidence it lacked.
	Missing string    `json:"missing"`
	At      time.Time `json:"at"`
	// Answered is when the Lead Product Manager's handling that named the evidence
	// was recorded, for a correction owed for evidence.
	Answered *time.Time `json:"answered,omitempty"`
	Piece    string     `json:"piece,omitempty"`
	// Noted is that the note naming the epic, the check and its commit is on the
	// piece. A stop between the two writes leaves it false, and the note is
	// written at the next reading.
	Noted bool `json:"noted,omitempty"`
}

func (c *Correction) valid(epic string) bool {
	return c == nil || strings.TrimSpace(c.Missing) != "" && !c.At.IsZero() &&
		(c.Piece == "" && !c.Noted || beads.ValidIssueID(c.Piece) && c.Piece != epic)
}

// Asking is the last check of the naming where it could not tell and owes no
// correction yet: the one whose question about evidence the harness puts to the
// Lead Product Manager, or whose answer it reads. An earlier check was made again,
// so it asks nothing.
func (e Entry) Asking() (Check, bool) {
	if len(e.Checks) == 0 {
		return Check{}, false
	}
	last := e.Checks[len(e.Checks)-1]
	if last.Found != FoundCannotTell || last.Owed != nil || last.Asked != nil && last.Asked.Answered != nil {
		return Check{}, false
	}
	return last, true
}

// CouldNotTell is what a check could not tell, each thing asked with why, leaving
// out a step the description reserves for a person.
func CouldNotTell(check Check) string {
	var said []string
	for _, finding := range check.Findings {
		if finding.Answer == CannotBeTold && !finding.ForAPerson {
			said = append(said, finding.Asks+": "+finding.Says)
		}
	}
	return strings.Join(said, "; ")
}

// PersonsSteps is each step a check found the description reserves for a person,
// in the order it answered them. A check can owe these beside what it could not
// tell for another reason, and the epic waits on the operator for each of them
// whatever else it owes.
func PersonsSteps(check Check) []Finding {
	var steps []Finding
	for _, finding := range check.Findings {
		if finding.Answer == CannotBeTold && finding.ForAPerson {
			steps = append(steps, finding)
		}
	}
	return steps
}

// FromCheck is what a correcting piece is recorded as made from: the check, in
// the place a split records the member.
func FromCheck(check string) string { return "the check " + check }

// Correcting is the check whose owed correction names the item, where one does.
func (e Entry) Correcting(item string) (Check, bool) {
	if at := slices.IndexFunc(e.Checks, func(check Check) bool { return check.Owed != nil && check.Owed.Piece == item }); item != "" && at >= 0 {
		return e.Checks[at], true
	}
	return Check{}, false
}

// correctsFrom reports a piece made from a check whose owed correction names it.
func (e Entry) correctsFrom(item, from string) bool {
	if comment, correcting := e.CorrectingComment(item); correcting {
		return FromComment(comment.ID) == from
	}
	check, correcting := e.Correcting(item)
	return correcting && FromCheck(check.ID) == from
}

// Superseded reports a check that did not pass and was made again because the
// operator took an item out after it, with nothing joined in between. It is not a
// round of correction.
func (e Entry) Superseded(epic epicscope.Epic, check Check) bool {
	return !check.Passed() && slices.ContainsFunc(epic.Removed, func(removal epicscope.Removal) bool {
		return removal.RemovedAt.After(check.At) && !slices.ContainsFunc(e.Joined, func(joined Joined) bool {
			return joined.At.After(check.At) && joined.At.Before(removal.RemovedAt)
		})
	})
}

// Rounds is how many checks of the naming found something missing, each a round
// of correction. A check made again after a repair of a change handed over is not
// one.
func (e Entry) Rounds(epic epicscope.Epic) int {
	rounds := 0
	for _, check := range e.Checks {
		if check.Found == FoundNotMet && !check.Again && !e.Superseded(epic, check) {
			rounds++
		}
	}
	return rounds
}

// MaxRounds is how many rounds of correction the harness starts by itself. A
// check that finds something missing after them goes to the Lead Product Manager.
const MaxRounds = 2

// CorrectionNote is the note of fixed form written on a correcting piece, for
// people to read. Nothing reads it to decide anything.
func CorrectionNote(epic string, check Check) string {
	return fmt.Sprintf("Written as the correction owed by the check of the whole epic %s, %s, which read commit %s.", epic, check.ID, check.Commit)
}

// CheckAnswer is the answer a check gives one thing the description asks.
type CheckAnswer string

const (
	Met          CheckAnswer = "met"
	TakenOut     CheckAnswer = "taken out"
	NotMet       CheckAnswer = "not met"
	CannotBeTold CheckAnswer = "cannot be told"
)

// Finding is one thing the description asks and the answer to it. Says is where
// the session looked for met, what is missing for not met, and why for cannot be
// told. Removal is the item the operator took out, for taken out, which is an
// answer only for what that item would have given. ForAPerson is a cannot be told
// because the description itself reserves the step for a person.
type Finding struct {
	Asks       string      `json:"asks"`
	Answer     CheckAnswer `json:"answer"`
	Says       string      `json:"says,omitempty"`
	Removal    string      `json:"removal,omitempty"`
	ForAPerson bool        `json:"for_a_person,omitempty"`
}

func (f Finding) valid() bool {
	if strings.TrimSpace(f.Asks) == "" || f.ForAPerson && f.Answer != CannotBeTold {
		return false
	}
	switch f.Answer {
	case TakenOut:
		return beads.ValidIssueID(f.Removal)
	case Met, NotMet, CannotBeTold:
		return strings.TrimSpace(f.Says) != "" && f.Removal == ""
	}
	return false
}

// FoundOf is what a check found from its answers: something missing first,
// because a correction is written for it; then what cannot be told; then a step
// the description reserves for a person; and all met only where every answer is
// met or taken out.
func FoundOf(findings []Finding) string {
	found := FoundAllMet
	for _, finding := range findings {
		switch {
		case finding.Answer == NotMet:
			return FoundNotMet
		case finding.Answer == CannotBeTold && !finding.ForAPerson:
			found = FoundCannotTell
		case finding.Answer == CannotBeTold && found == FoundAllMet:
			found = FoundPersonsStep
		}
	}
	return found
}

// Delivery is the epic having been delivered, and on what evidence.
type Delivery struct {
	At       time.Time `json:"at"`
	Evidence string    `json:"evidence"`
}

// Reservation is a start or a paid call written down before it is made.
type Reservation struct {
	ID  string    `json:"id"`
	For string    `json:"for"`
	At  time.Time `json:"at"`
	// HeldBy names the durable record that already holds this reservation and its
	// result, such as a run's. Where it is set nothing more is kept here.
	HeldBy string  `json:"held_by,omitempty"`
	Result *Result `json:"result,omitempty"`
}

// Result is what became of a reservation. Unknown is that it cannot be known.
type Result struct {
	At      time.Time `json:"at"`
	Says    string    `json:"says"`
	Unknown bool      `json:"unknown,omitempty"`
}

func (e Entry) empty() bool {
	return len(e.Joined)+len(e.Pieces)+len(e.Comments)+len(e.Checks)+len(e.Deliveries)+len(e.Reservations)+len(e.CopiedLate)+len(e.Blockers) == 0
}

// Has reports whether the item joined.
func (e Entry) Has(item string) bool {
	return slices.ContainsFunc(e.Joined, func(joined Joined) bool { return joined.WorkItemID == item })
}

func (p Progress) index(naming Naming) int {
	return slices.IndexFunc(p.Entries, func(entry Entry) bool { return entry.same(naming) })
}

func (b Begun) has(naming Naming) bool {
	return slices.ContainsFunc(b.Namings, naming.same)
}

func heading(schema int, product, want domain.ProductID) error {
	if schema != SchemaVersion || product != want {
		return fmt.Errorf("it has schema version %d and belongs to product %q, where %d and %q are read", schema, product, SchemaVersion, want)
	}
	return nil
}

func (p Progress) validate(product domain.ProductID) error {
	if err := heading(p.SchemaVersion, p.ProductID, product); err != nil {
		return err
	}
	if len(p.Entries) == 0 {
		return errors.New("it holds no entry, and it is written only once one has been begun")
	}
	for at, entry := range p.Entries {
		if !entry.valid() || entry.BegunAt.IsZero() {
			return fmt.Errorf("an entry for %q needs a work item, when it was named and when the entry was begun", entry.WorkItemID)
		}
		if p.index(entry.Naming) != at {
			return fmt.Errorf("epic %s has two entries for one naming", entry.WorkItemID)
		}
		if err := entry.validate(); err != nil {
			return fmt.Errorf("epic %s: %w", entry.WorkItemID, err)
		}
	}
	return nil
}

// validate refuses an entry with a part that is not whole or is listed twice.
func (e Entry) validate() error {
	var problems []error
	seen := map[string]bool{}
	part := func(kind, id string, whole bool) {
		if key := kind + " " + id; !whole || id == "" || seen[key] {
			problems = append(problems, fmt.Errorf("the %s %q is missing something it needs, or is listed twice", kind, id))
		} else {
			seen[key] = true
		}
	}
	for _, j := range e.Joined {
		part("joined piece", j.WorkItemID, beads.ValidIssueID(j.WorkItemID) && j.WorkItemID != e.WorkItemID && j.From != "" && j.Judgment != "" && j.Session != "" && !j.At.IsZero())
	}
	for _, p := range e.Pieces {
		part("piece", p.WorkItemID, beads.ValidIssueID(p.WorkItemID) && p.WorkItemID != e.WorkItemID && p.From != "" && p.Judgment != "" && !p.At.IsZero() &&
			(p.Outcome == nil || p.Outcome.valid()) && (p.Question == "" || p.Outcome != nil && p.Outcome.Answer.Asks()) &&
			p.Answered.valid() && (p.Answered == nil || p.Question != "") &&
			!slices.ContainsFunc(p.Earlier, func(earlier EarlierJudgment) bool { return earlier.Judgment == "" || !earlier.Outcome.valid() }) &&
			!slices.ContainsFunc(p.Seen, func(seen Seen) bool { return seen.Digest == "" || seen.At.IsZero() }) &&
			(p.Reread == nil || p.Reread.Question != "" && p.WithSource && (!p.Reread.Answered || p.Outcome != nil && p.Outcome.Answer == AnswerInside)))
	}
	for _, c := range e.CopiedLate {
		part("late copy of", c.WorkItemID, beads.ValidIssueID(c.WorkItemID) && !c.At.IsZero())
	}
	for _, c := range e.Comments {
		part("comment", c.ID, c.Became != "" && !c.At.IsZero() && (c.Outcome == nil || c.Outcome.valid() && c.Judgment != "") &&
			(c.Question == "" || c.Outcome != nil && c.Outcome.Answer.Asks()) &&
			c.Owed.valid(e.WorkItemID) && (c.Owed == nil || c.Outcome == nil && c.Judgment == "" && beads.ValidIssueID(c.Member)) &&
			(c.Later == nil || c.Owed == nil && c.Outcome == nil && c.Judgment == "" && beads.ValidIssueID(c.Member) && strings.TrimSpace(c.Later.Says) != "" &&
				(c.Later.Item == "" || beads.ValidIssueID(c.Later.Item) && c.Later.Item != e.WorkItemID)))
		if c.Owed != nil && c.Owed.Piece != "" {
			part("correcting piece", c.Owed.Piece, true)
		}
	}
	for _, c := range e.Checks {
		part("check", c.ID, c.Commit != "" && c.Found != "" && !c.At.IsZero() &&
			(len(c.Findings) == 0 || c.Found == FoundOf(c.Findings) && !slices.ContainsFunc(c.Findings, func(f Finding) bool { return !f.valid() })) &&
			c.Owed.valid(e.WorkItemID) && (c.Owed == nil || c.Found == FoundNotMet || c.Found == FoundCannotTell) &&
			c.Asked.valid() && (c.Asked == nil || c.Found == FoundCannotTell) && (c.Asked == nil || c.Asked.Operator == "" || c.Owed == nil))
	}
	for _, c := range e.Checks {
		if c.Owed != nil && c.Owed.Piece != "" {
			part("correcting piece", c.Owed.Piece, true)
		}
	}
	for _, d := range e.Deliveries {
		part("delivery", d.At.String(), d.Evidence != "" && !d.At.IsZero())
	}
	for _, b := range e.Blockers {
		part("outside blocker", b.WorkItemID, beads.ValidIssueID(b.WorkItemID) && b.WorkItemID != e.WorkItemID && beads.ValidIssueID(b.Member) && b.Link != "" && b.Report != "" && !b.At.IsZero())
	}
	for _, r := range e.Reservations {
		part("reservation", r.ID, r.For != "" && !r.At.IsZero())
		if r.Result != nil && (r.HeldBy != "" || r.Result.At.IsZero()) {
			problems = append(problems, fmt.Errorf("the result of reservation %q needs when, and is not kept where another record holds it", r.ID))
		}
	}
	return errors.Join(problems...)
}

func (b Begun) validate(product domain.ProductID) error {
	if err := heading(b.SchemaVersion, b.ProductID, product); err != nil {
		return err
	}
	if len(b.Namings) == 0 {
		return errors.New("it lists no naming, and it is written only once one has been begun")
	}
	for at, naming := range b.Namings {
		if !naming.valid() || slices.IndexFunc(b.Namings, naming.same) != at {
			return fmt.Errorf("the naming of %q needs a work item and when it was named, listed once", naming.WorkItemID)
		}
	}
	return nil
}

// View is the three files as one reading.
type View struct {
	Record   epicscope.Record
	progress Progress
	begun    Begun
}

// Read reads the two files beside a reading of the operator's record. A file
// that is there and cannot be read whole is an error, and never read as no file.
//
// The list is read on both sides of the entries. An entry is begun with one write
// and listed with another, so two readings of the list that agree are of the
// entries read between them.
func Read(stateRoot string, product domain.ProductID, record epicscope.Record) (View, error) {
	if err := place(stateRoot, product); err != nil {
		return View{}, err
	}
	directory := home.ProductDirectory(stateRoot, string(product))
	readBegun := func() (Begun, error) {
		return readFile(filepath.Join(directory, begunName), Begun{SchemaVersion: SchemaVersion, ProductID: product}, func(b Begun) error { return b.validate(product) })
	}
	begun, err := readBegun()
	if err != nil {
		return View{}, err
	}
	for range 5 {
		progress, problem := readFile(filepath.Join(directory, progressName), Progress{SchemaVersion: SchemaVersion, ProductID: product}, func(p Progress) error { return p.validate(product) })
		again, err := readBegun()
		if problem != nil || err != nil {
			return View{}, errors.Join(problem, err)
		}
		if slices.EqualFunc(begun.Namings, again.Namings, Naming.same) {
			return View{Record: record, progress: progress, begun: begun}, nil
		}
		begun = again
	}
	return View{}, errors.New("the files that keep the epics' progress changed while they were read")
}

func place(stateRoot string, product domain.ProductID) error {
	if !filepath.IsAbs(stateRoot) {
		return errors.New("state root must be an absolute path")
	}
	return domain.ValidateIdentifier("product id", string(product))
}

// readFile reads one file whole, and answers missing where there is none. It is
// strict because every read either precedes a write of the whole file or decides
// whether something may start.
func readFile[T any](path string, missing T, validate func(T) error) (T, error) {
	var none, read T
	// Only the name's own absence is the absence of a file.
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return missing, nil
	}
	if err != nil {
		return none, fmt.Errorf("look for the epics' progress: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return none, fmt.Errorf("the epics' progress, %s, is not a regular file of at most %d bytes", path, maxFileBytes)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return none, fmt.Errorf("read the epics' progress: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&read)
	if _, after := decoder.Token(); err == nil && !errors.Is(after, io.EOF) {
		err = errors.New("something follows the record")
	}
	if err == nil {
		err = validate(read)
	}
	if err != nil {
		return none, fmt.Errorf("the epics' progress, %s: %w", path, err)
	}
	return read, nil
}

// Entry is what is kept of a naming, where anything is.
func (v View) Entry(naming Naming) (Entry, bool) {
	if at := v.progress.index(naming); at >= 0 {
		return v.progress.Entries[at], true
	}
	return Entry{}, false
}

// Unreadable is why a naming's progress cannot be read, and empty where it can.
// A naming that was never begun can, and so can an empty entry that a stop left
// out of the list.
func (v View) Unreadable(naming Naming) string {
	entry, kept := v.Entry(naming)
	listed := v.begun.has(naming)
	switch {
	case listed && !kept:
		return fmt.Sprintf("%s says its progress was begun, and %s holds no entry for it", begunName, progressName)
	case kept && !listed && !entry.empty():
		return fmt.Sprintf("%s holds its entry, and %s does not say it was begun", progressName, begunName)
	}
	return ""
}

// UnknownResults is each reservation of a naming whose result cannot be known.
func (v View) UnknownResults(naming Naming) []Reservation {
	entry, _ := v.Entry(naming)
	return slices.DeleteFunc(slices.Clone(entry.Reservations), func(r Reservation) bool { return r.Result == nil || !r.Result.Unknown })
}

// Precedes reports a run, by when it started, that is older than anything kept
// of a naming's progress: the naming was not begun, or was begun after it and
// does not point to it. A run from before progress was kept is not one.
func (v View) Precedes(naming Naming, runID string, startedAt time.Time) bool {
	if startedAt.Before(keptSince) {
		return false
	}
	entry, kept := v.Entry(naming)
	return !kept || startedAt.Before(entry.BegunAt) &&
		!slices.ContainsFunc(entry.Reservations, func(r Reservation) bool { return r.ID == runReservation(runID) })
}

// RunsReserved is each run whose start was reserved for a naming, once, in the
// order they were reserved. A run's record holds its reservation, so these are
// the runs made for the epic, those of items taken out since included.
func (e Entry) RunsReserved() []string {
	var runs []string
	for _, reserved := range e.Reservations {
		if id, isRun := strings.CutPrefix(reserved.ID, runReservation("")); isRun && reserved.HeldBy != "" && !slices.Contains(runs, id) {
			runs = append(runs, id)
		}
	}
	return runs
}

// CallsReserved is each paid call reserved for a naming that is not a run's
// start: a joining judgment or a check of the whole epic. A run of the declared
// checks calls no provider and is not one (ChecksRunsReserved).
func (e Entry) CallsReserved() []Reservation {
	return slices.DeleteFunc(slices.Clone(e.Reservations), func(reserved Reservation) bool {
		return strings.HasPrefix(reserved.ID, runReservation("")) && reserved.HeldBy != "" || strings.HasPrefix(reserved.ID, checksRunPrefix)
	})
}

// CheckFor begins what the reservation of a check of the whole epic is for.
const CheckFor = "the check of the whole epic"

const checksRunPrefix = "checks:"

// ChecksRun is the reservation of the project's declared checks, run on one
// commit for the check of the whole epic with the identifier check.
func ChecksRun(check, commit string) string { return checksRunPrefix + check + ":" + commit }

// ChecksRunsReserved is each run of the declared checks reserved for a check of
// the whole epic. It is charged to the epic and calls no provider.
func (e Entry) ChecksRunsReserved() []Reservation {
	return slices.DeleteFunc(slices.Clone(e.Reservations), func(reserved Reservation) bool {
		return !strings.HasPrefix(reserved.ID, checksRunPrefix)
	})
}

// CheckCommits is each commit whose declared checks were reserved for the check
// of the whole epic with the identifier check, in the order they were reserved.
func (e Entry) CheckCommits(check string) []string {
	var commits []string
	for _, reserved := range e.ChecksRunsReserved() {
		if commit, mine := strings.CutPrefix(reserved.ID, checksRunPrefix+check+":"); mine {
			commits = append(commits, commit)
		}
	}
	return commits
}

// CheckUnderway is the identifier of the check of the whole epic last begun that
// is still under way: its declared checks or its call are reserved, nothing is
// kept of it, and its call has no result. A call answered and not kept was
// refused, so the check is owed again under a new identifier.
func (e Entry) CheckUnderway() (string, bool) {
	for _, reserved := range slices.Backward(e.Reservations) {
		id := ""
		switch {
		case strings.HasPrefix(reserved.For, CheckFor):
			id = reserved.ID
		case strings.HasPrefix(reserved.ID, checksRunPrefix):
			id, _, _ = strings.Cut(strings.TrimPrefix(reserved.ID, checksRunPrefix), ":")
		default:
			continue
		}
		call, called := e.Reservation(id)
		if slices.ContainsFunc(e.Checks, func(check Check) bool { return check.ID == id }) || called && call.Result != nil {
			return "", false
		}
		return id, true
	}
	return "", false
}

// RunSharedBy is how many namings a run's start was reserved for: a document
// may answer items of two epics, and its cost is then shared between them.
func (v View) RunSharedBy(runID string) int {
	shared := 0
	for _, entry := range v.progress.Entries {
		if slices.Contains(entry.RunsReserved(), runID) {
			shared++
		}
	}
	return shared
}

// Items is every item in an epic, the members first, each once.
func (v View) Items(epic epicscope.Epic) []string {
	var items []string
	for _, member := range epic.Members {
		items = append(items, member.WorkItemID)
	}
	entry, _ := v.Entry(Of(epic))
	for _, joined := range entry.Joined {
		if !epic.Has(joined.WorkItemID) && !epic.TakenOut(joined.WorkItemID) {
			items = append(items, joined.WorkItemID)
		}
	}
	return items
}

// Files is where the two files are, for a person.
func Files(stateRoot string, product domain.ProductID) string {
	directory := home.ProductDirectory(stateRoot, string(product))
	return filepath.Join(directory, progressName) + " and " + filepath.Join(directory, begunName)
}

// Holding is what the operator's acts ask about an item while they hold the
// record's lock: the open epics it is in, the one it joined included.
func Holding(stateRoot string, product domain.ProductID) epicscope.JoinedReader {
	return func(record epicscope.Record) (epicscope.Joins, error) {
		view, err := Read(stateRoot, product, record)
		return epicscope.Joins{Holders: view.Holders, Unreadable: func(epic epicscope.Epic) string { return view.Unreadable(Of(epic)) }}, err
	}
}

// Holders is every open epic the item is in, as a member or as a piece that
// joined and was not taken out. An ended epic holds nothing.
func (v View) Holders(item string) []epicscope.Epic {
	var holders []epicscope.Epic
	for _, epic := range v.Record.OpenEpics() {
		entry, _ := v.Entry(Of(epic))
		if epic.Has(item) || entry.Has(item) && !epic.TakenOut(item) {
			holders = append(holders, epic)
		}
	}
	return holders
}

// AnsweredByRereading is, by report identifier, why each question about scope
// that the one judgment made once more answered needs nothing more: the piece was
// judged inside with the text of what it came from and joined. The Lead Product
// Manager's pass is shown it beside the report, so she can record that nothing
// more is needed (the design's "A piece split from a member", point 5). A question
// whose judgment made once more was outside again is not one.
func (v View) AnsweredByRereading() map[string]string {
	answered := map[string]string{}
	for _, entry := range v.progress.Entries {
		for _, piece := range entry.Pieces {
			if piece.Reread == nil || !piece.Reread.Answered || !entry.Has(piece.WorkItemID) {
				continue
			}
			answered[piece.Reread.Question] = fmt.Sprintf("This question about scope is already answered: %s was judged once more, given the text of %s, which it was made from, found inside epic %s, and joined it. Nothing more is needed; record that with a \"handle\" action naming this report.",
				piece.WorkItemID, piece.From, entry.WorkItemID)
		}
	}
	return answered
}

// AnsweredByRereading reads the records and answers View.AnsweredByRereading.
// Where nothing is kept there is nothing answered.
func (s *Store) AnsweredByRereading() (map[string]string, error) {
	record, err := epicscope.Read(s.stateRoot, s.product)
	if err != nil {
		return nil, err
	}
	view, err := Read(s.stateRoot, s.product, record)
	if err != nil {
		return nil, err
	}
	return view.AnsweredByRereading(), nil
}
