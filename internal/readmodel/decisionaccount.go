package readmodel

// What one entry on the operator's decision list says, so he can decide from
// the entry alone without asking any role what it means.
//
// A decision entry used to name the record — its id, the document, the
// conversation, the date — and the commands that decide it, and nothing of what
// was being decided. The account is the rest: what changes, with a short before
// and after where the change is to a document's text; why, in the writer's own
// words with any work item it serves; what approving and what declining each
// lead to; and why the decision is his rather than a role's. Every surface that
// lists the decision prints the same account, because it is derived here from
// the record the entry carries: the four lines `yoyo status` and the channel
// print, the summary's "Needs your decision", and the dashboard's pop-up.
//
// A record that lacks one of those parts is not left silent about it: the
// account names what its record does not hold, so a missing reason reads as
// missing rather than as there being none.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
)

// DecisionAccount is one decision as the operator decides it.
type DecisionAccount struct {
	// Changes is what changes if he approves, in a sentence.
	Changes string `json:"changes"`
	// Before and After are a short excerpt of the text a document change
	// replaces and the text it puts there, where the change carries its text.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Why is the reason the writer gave, in its own words, and Serves the work
	// items it names as what it serves.
	Why    string   `json:"why"`
	Serves []string `json:"serves,omitempty"`
	// IfApproved and IfDeclined are what each choice leads to.
	IfApproved string `json:"if_approved"`
	IfDeclined string `json:"if_declined"`
	// WhyYours is why the decision is the operator's rather than a role's.
	WhyYours string `json:"why_yours"`
	// Missing names each of those parts the record does not hold.
	Missing []string `json:"missing,omitempty"`
}

// Lines is the account as a terminal prints it, one part to a line, each
// opening with what it is.
func (a DecisionAccount) Lines() []string {
	var lines []string
	add := func(label, text string) {
		if text = strings.TrimSpace(text); text != "" {
			lines = append(lines, label+": "+text)
		}
	}
	add("What changes", a.Changes)
	add("Before", oneLine(a.Before))
	add("After", oneLine(a.After))
	why := a.Why
	if len(a.Serves) > 0 {
		why = strings.TrimSpace(why + " (serves " + strings.Join(a.Serves, ", ") + ")")
	}
	add("Why", why)
	add("If you approve", a.IfApproved)
	add("If you decline", a.IfDeclined)
	add("Why it is yours", a.WhyYours)
	if len(a.Missing) > 0 {
		lines = append(lines, "Not in its record: "+strings.Join(a.Missing, "; "))
	}
	return lines
}

// oneLine is an excerpt on one line, its line breaks shown as " / " so the
// shape of what it quotes survives a terminal line.
func oneLine(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, " / ")
}

// Account is the entry's decision account, for the entries the decision list
// carries: a document waiting for approval, a proposed change to a document,
// and a concern waiting for an answer, each where the operator decides it or a
// role's decision needs his hand. Every other entry carries none.
func (a Attention) Account() (DecisionAccount, bool) {
	if !a.Mover.IsOperator() && !a.Ownership.NeedsHand() {
		return DecisionAccount{}, false
	}
	switch a.Kind {
	case AttentionDocumentApproval:
		if a.DocumentApproval != nil {
			return documentAccount(*a.DocumentApproval, a.Mover.IsOperator(), a.Ownership.ReasonSays()), true
		}
	case AttentionAmendment:
		if a.Amendment != nil {
			return amendmentAccount(*a.Amendment, a.Ownership.ReasonSays()), true
		}
	case AttentionConcern:
		if a.Concern != nil {
			return concernAccount(*a.Concern, a.Ownership.ReasonSays()), true
		}
	}
	return DecisionAccount{}, false
}

// becauseIt closes a why-yours sentence on the closed list's reason, where the
// registry gave one.
func becauseIt(sentence, reason string) string {
	if reason == "" {
		return sentence
	}
	return sentence + ", which makes it " + reason
}

// documentAccount is a document a role wrote as the operator decides it.
func documentAccount(d DocumentApproval, operators bool, reason string) DecisionAccount {
	var account DecisionAccount
	name := d.Document
	if d.Title != "" {
		name = fmt.Sprintf("%s (%q)", d.Document, d.Title)
	}
	switch {
	case d.Document == "":
		account.Missing = append(account.Missing, "which document it is")
	case d.Action == string(artifact.WriteCreate):
		kind := "document"
		if d.Kind != "" {
			kind = d.Kind + " document"
		}
		account.Changes = fmt.Sprintf("adds a new %s, %s", kind, name)
		if d.Directory != "" {
			account.Changes += ", under " + d.Directory
		}
		account.Before = "nothing: the document does not exist yet"
	case d.Section != "":
		account.Changes = fmt.Sprintf("replaces the section %q of %s, and leaves the rest of it as it is", d.Section, name)
	default:
		account.Changes = "replaces the text of " + name
	}
	if strings.TrimSpace(d.Body) == "" {
		account.Missing = append(account.Missing, "the text it would put in the document")
	} else if d.Action == string(artifact.WriteCreate) {
		account.After = excerpt(d.Body)
	} else if d.Before == "" && d.After == "" {
		account.After = excerpt(d.Body)
		missing := "the document as it stands now, so no before is shown"
		if d.BeforeUnread != "" {
			missing += " (" + d.BeforeUnread + ")"
		}
		account.Missing = append(account.Missing, missing)
	} else {
		account.Before, account.After = d.Before, d.After
	}
	account.Why, account.Serves = d.Reason, d.Serves
	if account.Why == "" {
		account.Missing = append(account.Missing, "why it was written")
	}
	account.IfApproved = "it is published through a reviewed run, as a code change lands, and the " + d.Owner.Title() + " is told"
	account.IfDeclined = "nothing reaches the repository, and the " + d.Owner.Title() + " is told your reason"
	switch {
	case operators && d.ProductIntent:
		sentence := "it states the product's intent"
		switch artifact.Intent(d.Intent) {
		case artifact.IntentFundamental:
			sentence += " and is recorded as changing what the goals admit or refuse"
		case artifact.IntentConsistent:
			sentence += " and is recorded as consistent, yet still waits on your approval"
		default:
			sentence += " and does not say whether it changes what the goals admit or refuse"
		}
		account.WhyYours = becauseIt(sentence, reason)
	case operators:
		account.WhyYours = becauseIt("the ownership registry names you as the one who decides it", reason)
	default:
		account.WhyYours = "it is the " + d.Owner.Title() + "'s, but the project's approval setting for its kind asks a person to approve it, which no role may do"
	}
	return account
}

// amendmentAccount is a proposed change to a document as the operator decides
// it. A proposal says what should become true of the document, never the text,
// so it carries no before and after: the owner writes the text once it is
// approved.
func amendmentAccount(p amendment.Proposal, reason string) DecisionAccount {
	var account DecisionAccount
	switch {
	case strings.TrimSpace(p.Change) == "":
		account.Missing = append(account.Missing, "what the change is")
	case p.Artifact == "":
		account.Changes = "a change to a document its record does not name: " + strings.TrimSpace(p.Change)
		account.Missing = append(account.Missing, "which document it changes")
	default:
		account.Changes = "a change to " + p.Artifact + ": " + strings.TrimSpace(p.Change)
	}
	account.Why = strings.TrimSpace(p.Why)
	if account.Why == "" {
		account.Missing = append(account.Missing, "why it was proposed")
	}
	if p.WorkItemID != "" {
		account.Serves = []string{p.WorkItemID}
	}
	document := p.Artifact
	if document == "" {
		document = "the document"
	}
	account.IfApproved = "it is recorded, and the " + p.Owner.Title() + " writes the new text of " + document + " itself; nothing is written for you"
	account.IfDeclined = document + " stays as it is, and the " + p.Role.Title() + " is told your reason"
	sentence := "it changes " + document + ", which states what the product is for"
	switch p.Intent {
	case artifact.IntentFundamental:
		sentence += ", and is said to change what the goals admit or refuse"
	default:
		sentence += ", and does not say it leaves what the goals admit and refuse unchanged"
	}
	account.WhyYours = becauseIt(sentence, reason)
	return account
}

// concernAccount is a concern as the operator answers it. A concern is
// answered rather than approved: going ahead and not going ahead are the two
// answers the account says the results of.
func concernAccount(c ConcernWait, reason string) DecisionAccount {
	var account DecisionAccount
	role := c.Role.Title()
	switch {
	case c.Subject == "" && c.Question == "":
		account.Missing = append(account.Missing, "what the work is and what it asks")
	case c.Question == "":
		account.Changes = "whether the " + role + " goes on to propose: " + c.Subject
		account.Missing = append(account.Missing, "the question it asks")
	default:
		subject := c.Subject
		if subject == "" {
			subject = "work its record does not name"
			account.Missing = append(account.Missing, "what the work is")
		}
		account.Changes = "whether the " + role + " goes on to propose: " + subject + "; it asks: " + c.Question
	}
	why := c.headline()
	if c.Goal != "" {
		why += ", about the goal " + c.Goal
	}
	if c.Detail != "" {
		why += ": " + c.Detail
	} else {
		account.Missing = append(account.Missing, "the "+role+"'s reason for asking")
	}
	account.Why = why
	answer := "`" + c.answerCommand() + "`"
	if len(c.Options) > 0 {
		var offered []string
		for index, option := range c.Options {
			offered = append(offered, fmt.Sprintf("%d. %s", index+1, strings.TrimSpace(option)))
		}
		answer += ", or with an offered answer's number (" + strings.Join(offered, "; ") + ")"
	}
	account.IfApproved = "answer that it should go ahead, with " + answer + "; the " + role + " acts on your answer at its next turn"
	account.IfDeclined = "answer that it should not; the work is not proposed, and the " + role + " is told"
	var sentence string
	switch c.Kind {
	case "unplaceable":
		sentence = "the work serves no goal the " + role + " can find, so going ahead widens what the goals admit"
	case "conflict":
		sentence = "the work would cut against a goal, and settling that changes what the goals admit or refuse"
	case "judgement":
		sentence = "the work fits the goals as written, and overruling or accepting the " + role + "'s judgement against it decides what the product is for"
	default:
		sentence = "a concern is a question about what the goals admit, which the " + role + " may not settle itself"
	}
	account.WhyYours = becauseIt(sentence, reason)
	return account
}

// The bounds on a before or after excerpt: enough to see what changes, never
// the document.
const (
	maxExcerptLines = 6
	maxExcerptBytes = 600
)

// excerpt is the opening of a text, cut to the excerpt's bounds and saying
// where it was cut.
func excerpt(text string) string {
	lines := nonBlankLines(text)
	cut := len(lines) > maxExcerptLines
	if cut {
		lines = lines[:maxExcerptLines]
	}
	said := strings.Join(lines, "\n")
	if len(said) > maxExcerptBytes {
		// The excerpt keeps its line breaks, so it is cut here rather than
		// folded onto one line by oneline.Bound, stepping back to a rune start.
		end := maxExcerptBytes
		for end > 0 && !utf8.RuneStart(said[end]) {
			end--
		}
		said = said[:end]
		cut = true
	}
	if cut {
		said += " …"
	}
	return said
}

func nonBlankLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, " \t"))
		}
	}
	return lines
}

// beforeAndAfter is the part of a document a revision changes: the lines of
// the text as it stands that the revision replaces, and the lines it puts in
// their place, found by setting aside the lines the two share at either end.
// Blank lines are not compared, so a change of spacing alone is no change.
func beforeAndAfter(current, revised string) (string, string) {
	old, updated := nonBlankLines(current), nonBlankLines(revised)
	start := 0
	for start < len(old) && start < len(updated) && old[start] == updated[start] {
		start++
	}
	oldEnd, newEnd := len(old), len(updated)
	for oldEnd > start && newEnd > start && old[oldEnd-1] == updated[newEnd-1] {
		oldEnd--
		newEnd--
	}
	if start == oldEnd && start == newEnd {
		return "the text is the same as the document now", "the same text"
	}
	before, after := excerpt(strings.Join(old[start:oldEnd], "\n")), excerpt(strings.Join(updated[start:newEnd], "\n"))
	if before == "" {
		before = "nothing: the revision only adds text"
	}
	if after == "" {
		after = "nothing: the revision only removes text"
	}
	return before, after
}

// readBefore reads the text a revision replaces from the repository: the
// document's prose without its identity block, or the one section a section
// revision replaces. It says why where it cannot.
func readBefore(repository, path, section string) (string, string) {
	switch {
	case repository == "":
		return "", "the repository was not read here"
	case path == "":
		return "", "its record does not say which file the document is"
	case !filepath.IsLocal(path):
		return "", fmt.Sprintf("its record names %q, which is not a file inside the repository", path)
	}
	content, err := os.ReadFile(filepath.Join(repository, path))
	if err != nil {
		return "", fmt.Sprintf("%s could not be read: %v", path, err)
	}
	text := withoutFrontmatter(string(content))
	if section == "" {
		return text, ""
	}
	found, ok := sectionText(text, section)
	if !ok {
		return "", fmt.Sprintf("%s has no section headed %q", path, section)
	}
	return found, ""
}

// withoutFrontmatter is a document's text after its identity block.
func withoutFrontmatter(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return text
	}
	if end := strings.Index(text[4:], "\n---\n"); end >= 0 {
		return text[4+end+5:]
	}
	return text
}

// sectionText is one section of a Markdown document: its heading line, which
// names the heading as a section revision does, and everything under it up to
// the next heading at its level or above.
func sectionText(text, heading string) (string, bool) {
	lines := strings.Split(text, "\n")
	want := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(heading), "#"))
	for index, line := range lines {
		level := headingLevel(line)
		if level == 0 || strings.TrimSpace(strings.TrimLeft(line, "#")) != want {
			continue
		}
		end := slices.IndexFunc(lines[index+1:], func(next string) bool {
			nextLevel := headingLevel(next)
			return nextLevel > 0 && nextLevel <= level
		})
		if end < 0 {
			return strings.Join(lines[index:], "\n"), true
		}
		return strings.Join(lines[index:index+1+end], "\n"), true
	}
	return "", false
}

func headingLevel(line string) int {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0
	}
	return level
}

// decisionKey is how the dashboard addresses the decision an entry puts to
// the operator (OperatorDecision.Key), and empty for an entry that is not one
// he decides there.
func (a Attention) decisionKey() string {
	if !a.Mover.IsOperator() {
		return ""
	}
	switch {
	case a.Kind == AttentionDocumentApproval && a.DocumentApproval != nil:
		return OperatorDecision{Kind: DecisionDocument, Agent: a.DocumentApproval.Agent, ID: a.DocumentApproval.WriteID}.Key()
	case a.Kind == AttentionAmendment && a.Amendment != nil:
		return OperatorDecision{Kind: DecisionAmendment, ID: a.Amendment.ID}.Key()
	case a.Kind == AttentionConcern && a.Concern != nil:
		return OperatorDecision{Kind: DecisionConcern, Agent: a.Concern.Agent, ID: a.Concern.ConcernID}.Key()
	}
	return ""
}

// decisionSubject is what the decision is about, as a summary line names it.
func (a Attention) decisionSubject() string {
	switch {
	case a.DocumentApproval != nil:
		if a.DocumentApproval.Title != "" {
			return a.DocumentApproval.Title + " (" + a.DocumentApproval.WriteID + ")"
		}
		return a.DocumentApproval.Document + " (" + a.DocumentApproval.WriteID + ")"
	case a.Amendment != nil:
		return a.Amendment.Artifact + " (" + a.Amendment.ID + ")"
	case a.Concern != nil:
		return a.Concern.Subject + " (" + a.Concern.ConcernID + ")"
	}
	return a.ID
}
