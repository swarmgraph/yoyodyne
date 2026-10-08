package domain

import (
	"fmt"
	"regexp"
	"strings"
)

type ProductID string

type RepositoryID string

// AgentRole names one of the harness's fixed roles. The set is closed: what a
// project configures is which agents fill these roles, how many, and each one's
// backend, model selector, and persona. Role authority and per-role tool posture
// are derived from the role in code, so a name outside this set names authority
// nobody wrote, and adding a role is a change to the harness rather than to a
// configuration file.
type AgentRole string

const (
	RoleProductManager     AgentRole = "product-manager"
	RoleArchitect          AgentRole = "architect"
	RoleDevelopmentManager AgentRole = "development-manager"
	RoleDeveloper          AgentRole = "developer"
	RoleReviewer           AgentRole = "reviewer"
	// RoleProgramManager is the program manager, the role type the operator
	// decided on 2026-09-24 (docs/designs/program-manager.md): an agent filling it
	// owns one outcome across the other five, admits work inside one lane, and asks
	// the product manager for everything outside it. It is last rather than placed
	// in the hierarchy because it is not a step of it: it watches the whole line.
	RoleProgramManager AgentRole = "program-manager"
)

// Roles are the harness's roles in the order the hierarchy runs: product intent,
// then design, then decomposition, then the two roles that do the work inside a
// run — and then the program manager, which watches the line rather than
// standing in it. It is the whole set, so a caller that has to name what is
// allowed reads it from here rather than repeating the list.
func Roles() []AgentRole {
	return []AgentRole{
		RoleProductManager,
		RoleArchitect,
		RoleDevelopmentManager,
		RoleDeveloper,
		RoleReviewer,
		RoleProgramManager,
	}
}

// RoleNames are the names prose gives the harness's roles: every role in
// Roles(). It is what a reader of prose asks when a word could be a role or
// something else of the same name — a document whose id is a role's name is the
// case that asked first.
func RoleNames() []string {
	names := make([]string, 0, len(Roles()))
	for _, role := range Roles() {
		names = append(names, string(role))
	}
	return names
}

// Valid reports whether a name is one of the harness's roles. An unrecognized
// name — a typo in an agents block, most often — is refused rather than carried
// as a role nothing knows how to run, because every posture the harness derives
// from a role has no answer for it.
func (r AgentRole) Valid() bool {
	switch r {
	case RoleProductManager, RoleArchitect, RoleDevelopmentManager, RoleDeveloper, RoleReviewer, RoleProgramManager:
		return true
	default:
		return false
	}
}

// LeadProductManagerTitle is the name a person reads for the product-manager
// role. The operator renamed it on 2026-09-26 so that it is not confused with
// the program managers, who answer to it: "PM" had come to mean either. The
// identifier stays product-manager — in configuration, agent names, persona
// paths, and every record already written — and only the name a sentence gives
// the role changed, which is why it is decided here and nowhere else.
const LeadProductManagerTitle = "Lead Product Manager"

// Title names a role the way somebody reading a sentence reads it, which is the
// identifier with the hyphen a name needs taken back out — except for the
// product manager, whose name is the Lead Product Manager's, written as a title.
// A name that is not a role is given as it was written: a sentence that has to
// name something the harness does not recognize is better off printing it than
// dressing it up.
func (r AgentRole) Title() string {
	if !r.Valid() {
		return string(r)
	}
	if r == RoleProductManager {
		return LeadProductManagerTitle
	}
	return strings.ReplaceAll(string(r), "-", " ")
}

type Backend string

const (
	BackendClaudeCode Backend = "claude-code"
	BackendCodex      Backend = "codex"
)

type ApprovalMode string

const (
	ApprovalHuman     ApprovalMode = "human"
	ApprovalAutomatic ApprovalMode = "automatic"
)

// ProviderOutageCause is why a provider is answering nobody: the account it is
// asked under is not logged in, or nothing reaches its API at all. It is the
// vocabulary every surface that names the wait shares — the adapter contract
// that classifies the provider's words, the durable record that says the
// outage is standing, and the lines that tell the operator — and it lives here
// so that none of them redeclares it.
//
// It is closed at two on purpose. Both are a wait no run can end and no reset
// time bounds: a login is the operator's to renew, and a network is nobody's to
// hurry. What separates them is only what the operator is told to do, which is
// the whole reason there are two words rather than one.
type ProviderOutageCause string

const (
	// ProviderUnauthenticated is the provider refusing the account the harness
	// asks under. The remedy is a person logging in.
	ProviderUnauthenticated ProviderOutageCause = "unauthenticated"
	// ProviderUnreachable is nothing answering at the provider's API: the
	// machine is offline, asleep, or behind a network that is not there. The
	// remedy is the network coming back, which the harness finds by asking again.
	ProviderUnreachable ProviderOutageCause = "unreachable"
)

// ProviderOutageCauses is every cause, in the order they are documented.
func ProviderOutageCauses() []ProviderOutageCause {
	return []ProviderOutageCause{ProviderUnauthenticated, ProviderUnreachable}
}

// Valid reports a cause this harness names.
func (c ProviderOutageCause) Valid() bool {
	for _, known := range ProviderOutageCauses() {
		if c == known {
			return true
		}
	}
	return false
}

// ProviderChannel is where a provider said something: on an envelope of the
// event stream it was asked for, on its process's stderr, or as plain text on
// its stdout in place of the envelopes it was asked for. It is the vocabulary
// the adapter contract hands a dialect an event with and the durable record
// says a classification by, and it lives here so that neither redeclares it.
//
// It is closed at three. A provider process has exactly two descriptors to say
// anything on, and the stream it was asked for is one of them, so the channels
// are the envelope and the two kinds of prose: what went to stderr, and what
// went to stdout without being an envelope at all. Which one a refusal arrived
// on is kept because they are read under different rules: an envelope names
// the event and the ending, and the two plain channels are prose with neither,
// read only when the stream ended without an ending of its own. A run waiting
// on a refusal read off either plain channel is a run whose provider died
// before it wrote a single envelope, which is what a reader of the record has
// to know before deciding whether the dialect read it right — and the plain
// stdout one says further that the CLI put its refusal where its stream should
// have been, which is the shape that used to fail the invocation as a stream
// the parser could not decode.
type ProviderChannel string

const (
	// ProviderChannelEnvelope is an envelope of the provider's event stream: the
	// terminal result, a rate-limit report, a retry in progress.
	ProviderChannelEnvelope ProviderChannel = "envelope"
	// ProviderChannelStderr is the provider process's stderr, read whole once
	// the process has ended without a terminal of its own.
	ProviderChannelStderr ProviderChannel = "stderr"
	// ProviderChannelStdout is plain text the provider process wrote to stdout
	// before any envelope — lines that were not envelopes on the stream that
	// was asked for — read whole, under the same rule as stderr, once the
	// process has ended without a terminal of its own.
	ProviderChannelStdout ProviderChannel = "stdout"
)

// ProviderChannels is every channel, in the order they are documented.
func ProviderChannels() []ProviderChannel {
	return []ProviderChannel{ProviderChannelEnvelope, ProviderChannelStderr, ProviderChannelStdout}
}

// Plain reports a channel that is prose rather than an envelope: the two the
// adapter reads only once the process has ended without a terminal, and only
// for the refusals a CLI can make before it writes anything structured.
func (c ProviderChannel) Plain() bool {
	return c == ProviderChannelStderr || c == ProviderChannelStdout
}

// Valid reports a channel this harness names.
func (c ProviderChannel) Valid() bool {
	for _, known := range ProviderChannels() {
		if c == known {
			return true
		}
	}
	return false
}

// StaleBlockClearOutcome is what became of a stale blocked status the harness
// cleared as it claimed an item: whether the tracker read the status back as
// open, and how long that took. It is the vocabulary the tracker client reports
// the clear by and the durable run record says it by, and it lives here so that
// neither redeclares it.
//
// It is closed at three because the read-back has exactly three endings. The
// first read after the write returns open; a later read within the bounded wait
// does; or none does, in which case the item was not claimed. The middle one is
// kept apart from the first because a clear that lands late is a tracker that is
// slower than the claim, which is worth knowing before the day it is slower than
// the wait.
type StaleBlockClearOutcome string

const (
	// StaleBlockClearConfirmed is the first read after the write returning open,
	// and the claim made on it taken.
	StaleBlockClearConfirmed StaleBlockClearOutcome = "confirmed"
	// StaleBlockClearConfirmedLate is a read after the first, within the bounded
	// wait, returning open and the claim made on it taken — later because the
	// earlier reads returned another status, or because the tracker refused the
	// claims made on them.
	StaleBlockClearConfirmedLate StaleBlockClearOutcome = "confirmed_late"
	// StaleBlockClearUnconfirmed is no claim within the bounded wait taken on a
	// read returning open. The item was not claimed and is left for the next pull.
	StaleBlockClearUnconfirmed StaleBlockClearOutcome = "unconfirmed"
)

// StaleBlockClearOutcomes is every outcome, in the order they are documented.
func StaleBlockClearOutcomes() []StaleBlockClearOutcome {
	return []StaleBlockClearOutcome{StaleBlockClearConfirmed, StaleBlockClearConfirmedLate, StaleBlockClearUnconfirmed}
}

// Valid reports an outcome this harness names.
func (o StaleBlockClearOutcome) Valid() bool {
	for _, known := range StaleBlockClearOutcomes() {
		if o == known {
			return true
		}
	}
	return false
}

// WorkItemClass names a kind of work a project may treat differently at
// admission. It exists because "ask about every work item" turned out to be
// coarser than the operators who set it actually meant: work that only reads and
// reports is not the work a per-item gate was put up against, and a policy that
// cannot say so is one an operator either abandons or works around.
//
// There is one class, deliberately. A class is an exemption from a gate the
// operator put up, so each of them has to be worth the operator's trust on its
// own, and a list that grew by convenience would be the gate coming down by
// instalments.
type WorkItemClass string

// WorkItemClassDiagnosis is work that only looks: it reads what is already
// there, is bounded in what it reads, and produces findings rather than a
// change. Nothing it admits alters the product, which is why an operator can
// hand it over without handing over what the gate was for.
const WorkItemClassDiagnosis WorkItemClass = "diagnosis"

// WorkItemClasses lists every class there is, in the order a refusal names them.
var WorkItemClasses = []WorkItemClass{WorkItemClassDiagnosis}

// Valid reports a class the harness recognizes. The empty class is not one: work
// that names no class is ordinary work, and asking whether that is valid is
// asking the wrong question.
func (c WorkItemClass) Valid() bool {
	for _, known := range WorkItemClasses {
		if c == known {
			return true
		}
	}
	return false
}

// WorkItemKind says whether a work item fixes a bug or adds a feature. It is
// what the rework rate is computed from — the share of merged changes that were
// unplanned bug fixes — so an admission through the harness has to state one,
// and it is stored in the tracker's own type field rather than in harness
// metadata, so bd's listings read the same kind the harness wrote.
//
// It is a different question from WorkItemClass, which says how a project's
// admission gate treats the work; and from the reliability label, which says a
// seat should prefer it.
type WorkItemKind string

const (
	// WorkItemKindBug is work that repairs something that should already have
	// worked.
	WorkItemKindBug WorkItemKind = "bug"
	// WorkItemKindFeature is planned work: anything that is not repairing
	// something broken.
	WorkItemKindFeature WorkItemKind = "feature"
)

// WorkItemKinds lists every kind there is, in the order a refusal names them.
var WorkItemKinds = []WorkItemKind{WorkItemKindBug, WorkItemKindFeature}

// WorkItemKindBugLabel is the label that marked a bug before the type field
// did. An item carrying it reads as a bug whatever its type says, so the items
// labelled that way before kinds were required are counted as what they are.
const WorkItemKindBugLabel = "bug"

// Valid reports a kind the harness recognizes. The empty kind is not one: it
// is an item nobody typed.
func (k WorkItemKind) Valid() bool {
	for _, known := range WorkItemKinds {
		if k == known {
			return true
		}
	}
	return false
}

// WorkItemKindOf reads a work item's kind from the tracker's type field and its
// labels. The bug label wins over the type, because most items carrying it were
// admitted as "task" before anything could say otherwise. An item that is
// neither reads as the empty kind, which a caller reports as untyped rather than
// counting as either.
func WorkItemKindOf(issueType string, labels []string) WorkItemKind {
	for _, label := range labels {
		if strings.TrimSpace(label) == WorkItemKindBugLabel {
			return WorkItemKindBug
		}
	}
	if kind := WorkItemKind(strings.TrimSpace(issueType)); kind.Valid() {
		return kind
	}
	return ""
}

// ReworkTally counts merged changes by the kind of the item each one closed.
// Untyped is kept apart rather than folded into features, because an item with
// no kind is a gap in the record and not a planned change.
type ReworkTally struct {
	Merges   int `json:"merges"`
	Bugs     int `json:"bugs"`
	Features int `json:"features"`
	Untyped  int `json:"untyped"`
}

// CountRework tallies the kinds of the items a set of merges closed, one kind
// per merge.
func CountRework(kinds []WorkItemKind) ReworkTally {
	tally := ReworkTally{Merges: len(kinds)}
	for _, kind := range kinds {
		switch kind {
		case WorkItemKindBug:
			tally.Bugs++
		case WorkItemKindFeature:
			tally.Features++
		default:
			tally.Untyped++
		}
	}
	return tally
}

// Rate is the share of merges that were bug fixes, and false where there were
// no merges to take a share of. While Untyped is non-zero it is a floor: an
// untyped merge may have been a bug fix, and is counted here as if it were not.
func (t ReworkTally) Rate() (float64, bool) {
	if t.Merges == 0 {
		return 0, false
	}
	return float64(t.Bugs) / float64(t.Merges), true
}

// WorkItemExecutor names what actually carries a work item's execution. It
// exists because nothing in the queue said, and the harness's own selection is
// what paid for that: an item whose execution is a conversation with the
// architect was chosen for a developer run, which spent the run and two review
// rounds producing a correctly refused empty diff. The subtler cost is the one
// that made a marker worth having — those rounds count against the item's cap,
// so an item mis-selected twice reaches its cap having done nothing and forces
// an escalation about work nobody ever started.
//
// The absent executor is a developer run. That is what almost all admitted work
// is, and it is what every item admitted before this existed is, so this says
// which work is not that rather than restating what the ordinary case already
// is. An item naming no executor is ordinary work.
type WorkItemExecutor string

// WorkItemExecutorConversation is work a persona conversation carries out: a
// promotion the architect makes to a document it owns, a decomposition the
// development manager settles, a decision the product manager records. No
// developer run can do any of it — the documents are default-deny for a
// developer's diff and the decision is not a change to a file at all — so
// selecting one for a run buys a refusal at best.
//
// On its own it says only that much, and that turned out to be one role short of
// the question it is asked: a handoff that cannot say whose conversation carries
// the item leaves the whole gap between the handoff and somebody picking the work
// up unattributed, which is exactly the anonymous silence the channel is narrated
// to end. So it is the prefix of a marker rather than the whole of one now — see
// ConversationWith — and an item may no longer be marked with it bare. It is
// still read: everything marked before the marker named a role carries it, and
// reading it as anything but a conversation would put that work back in front of
// a run that cannot do it.
const WorkItemExecutorConversation WorkItemExecutor = "conversation"

// executorRoleSeparator divides the conversation marker from the role whose
// conversation carries the work.
const executorRoleSeparator = ":"

// ConversationWith is the marker for work a named role's conversation carries.
// Naming the role is what makes a handoff addressed to somebody rather than
// announced to nobody: the pickup already says who started, so the role named
// here is what accounts for the wait before it.
func ConversationWith(role AgentRole) WorkItemExecutor {
	return WorkItemExecutor(string(WorkItemExecutorConversation) + executorRoleSeparator + string(role))
}

// WorkItemExecutors lists every executor an item may be marked with, in the
// order a refusal names them, which is the order the hierarchy runs in. The bare
// conversation marker is deliberately not among them: it is readable and it is
// not writable, because a marker written from here on has a role to name and
// nothing forces one but the refusal.
var WorkItemExecutors = conversationExecutors()

// The program manager is not among them. Its role is filled by instances, each
// owning one lane, so a marker naming the role names no conversation in
// particular; and its work reaches it through its own passes over its lane
// rather than by an item handed to it.
func conversationExecutors() []WorkItemExecutor {
	executors := make([]WorkItemExecutor, 0, len(Roles()))
	for _, role := range Roles() {
		if role == RoleProgramManager {
			continue
		}
		executors = append(executors, ConversationWith(role))
	}
	return executors
}

// Valid reports an executor an item may be marked with. The empty executor is
// not one: work that names no executor is a developer run, and asking whether
// that is valid is asking the wrong question.
//
// It is asked only where a marker is being written — the tracker action that
// names one, and the client that writes it — and never of a marker being read,
// which is what lets the bare marker already on an item go on meaning what it
// meant while nothing writes another one.
func (e WorkItemExecutor) Valid() bool {
	for _, known := range WorkItemExecutors {
		if e == known {
			return true
		}
	}
	return false
}

// Role is the role whose conversation carries the work, and is empty where the
// marker names none — the bare marker on work marked before this, and anything
// the harness does not recognize. Empty is the honest answer for both: what
// reads it says the role is unnamed rather than guessing one, and a guess here
// would be a thread telling an operator to wait on somebody nobody asked.
func (e WorkItemExecutor) Role() AgentRole {
	marker, named, qualified := strings.Cut(strings.TrimSpace(string(e)), executorRoleSeparator)
	if !qualified || WorkItemExecutor(marker) != WorkItemExecutorConversation {
		return ""
	}
	role := AgentRole(strings.TrimSpace(named))
	if !role.Valid() {
		return ""
	}
	return role
}

// DeveloperRun reports work a developer run carries out, which is work that
// names no executor at all.
//
// Anything else answers false, including a marker the harness does not
// recognize. That is the safe direction and the only one worth defaulting: a
// marker nobody can read was still put there by somebody who meant the work was
// not a developer run, and reading it as ordinary work would spend exactly the
// run this marker exists to save. A marker that is refused where it is written
// is what keeps the unrecognized case rare.
func (e WorkItemExecutor) DeveloperRun() bool {
	return strings.TrimSpace(string(e)) == ""
}

// WorkItemParking is why admitted work is deliberately not to be pulled. It is
// a status the harness reads rather than a place in the order, and it is a
// separate axis from the executor above: parked work may be perfectly ordinary
// developer work, and what makes it unschedulable is a decision somebody took
// about when rather than anything about what could carry it.
//
// It exists because the decision was being expressed as a priority, and a
// priority is not a decision anything can read that way. The product manager's
// own convention put deferred-by-decision work at the bottom of the order, which
// meant "parked" to whoever set it and "last" to everything that read it — and a
// queue that drains, which watch mode makes routine, reaches the bottom. On
// 2026-08-27 it did, and a run of work that had been deferred by a scope
// decision started, failed, and cost $34.38. Nothing was wrong with the
// selection: the scheduler pulled the highest-priority ready item there was.
// What was wrong was that the deferral lived somewhere selection could not look.
//
// So it is the reason itself rather than a flag. A flag would record that
// somebody parked the work and lose why, which is the half that decides whether
// releasing it is right — and a parked item nobody can account for is the same
// unaccountable state the recorded selection reason exists to prevent on the
// other side of the choice. The absent parking is ordinary queued work, which is
// nearly all of it.
type WorkItemParking string

// MaxWorkItemParkingBytes bounds the reason. It is generous enough to say what
// decided the parking and what would release it, and small enough to stay one
// line of a listing and one value of tracker metadata.
const MaxWorkItemParkingBytes = 480

// Parked reports admitted work the harness must not select, whatever the queue
// depth. It is asked wherever work is chosen, and the empty parking answers
// false, which is what every item admitted before this existed carries.
func (p WorkItemParking) Parked() bool {
	return strings.TrimSpace(string(p)) != ""
}

// Reason is why the work is parked, and is empty for work that is not. It is
// what a listing shows beside a parked item and what a deferral names, because
// "parked" on its own sends whoever reads it looking for the decision.
func (p WorkItemParking) Reason() string {
	return strings.TrimSpace(string(p))
}

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func ValidateIdentifier(kind, value string) error {
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s %q must match %s", kind, value, identifierPattern.String())
	}
	return nil
}

// memoryNamePattern is identifierPattern with a leading digit allowed. A role
// names a memory after what it is about, and what it is about is often a work
// item, whose number comes first: on 2026-09-26 the development manager's sweep
// named one "372-owes-rerun-decision", the reply was refused whole, and the
// triage decisions it carried were lost with it. A memory name is a key in the
// role's own store and never a configured identifier, so nothing needs it to
// start with a letter.
var memoryNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidateMemoryName checks the name a role gives one of its memories.
func ValidateMemoryName(value string) error {
	if !memoryNamePattern.MatchString(value) {
		return fmt.Errorf("memory %q must be lowercase letters and digits in hyphen-separated words, matching %s", value, memoryNamePattern.String())
	}
	return nil
}

// Valid reports a well-formed backend identifier. Which backends a project may
// actually name, and which roles and tool postures each of them serves, is not
// this package's to say: a project may declare a provider of its own, and the
// registry in internal/backend is where the built-ins and the declared ones are
// checked the same way, when the configuration is validated and before any work
// is assigned.
//
// What is checked here is the shape, because that is what a durable record
// needs. A run, a conversation, or a line of spend names the provider it was
// served by, and that record has to stay readable whether or not the provider
// that served it is still configured, still declared, or still compiled into
// this build — a fact about what was spent does not stop being true because
// somebody deleted a plugin.
func (b Backend) Valid() bool {
	return ValidateIdentifier("backend", string(b)) == nil
}

func (m ApprovalMode) Valid() bool {
	return m == ApprovalHuman || m == ApprovalAutomatic
}
