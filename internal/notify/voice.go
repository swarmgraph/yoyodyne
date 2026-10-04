package notify

// How a persona sounds, and how it appears.
//
// One thread per topic draws the boundary between subjects; this draws the
// boundary between speakers. A channel where five roles and the harness all
// report in the same flat sentence is one where the only thing separating them
// is a header, and a header is the first thing a reader stops reading. So each
// persona has its own line for each reportable kind: the developer speaks in the
// first person about the change and the evidence, the reviewer judges what it
// did not write, the development manager talks about the queue, the product
// manager about intent, the architect about the shape of the thing, and the
// harness says only what happened, with no first person and no opinion, because
// what it reports is what no persona did.
//
// The lines are data and nothing renders them but substitution. There is no
// provider call anywhere on this path: voicing an event through a model would
// price visibility at a call per event, make the channel nondeterministic, and
// put a model between a fact and its reporting. What an agent actually wrote is
// substituted in whole, so the genuine voice is carried rather than described.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Identity is how a persona appears on its own messages: the name a reader sees
// and the avatar beside it. It is per message rather than per connection because
// one sink posts for every persona, and a workspace where they all appear as one
// app is a workspace where the voice is the only thing telling them apart.
type Identity struct {
	Name string `json:"name"`
	// Avatar is an emoji shortcode, or the URL of an image where a project
	// configured one. It is decoration in the strict sense: everything it
	// distinguishes is already distinguished by the name beside it and the voice
	// below it, so a reader whose client will not render it loses nothing but the
	// picture. Which of the two it is, and how a surface carries each, is that
	// surface's business rather than this table's.
	Avatar string `json:"avatar"`
}

func (i Identity) Validate() error {
	var problems []error
	if strings.TrimSpace(i.Name) == "" {
		problems = append(problems, errors.New("identity name is required"))
	}
	if strings.TrimSpace(i.Avatar) == "" {
		problems = append(problems, errors.New("identity avatar is required"))
	}
	return errors.Join(problems...)
}

// Identity is how this speaker appears as the harness ships it, before any one
// project's configuration is applied to it. The configured agent is named only
// where it says something the role does not, which is a project that configured
// more than one agent for a role — the same rule a collected report is rendered
// under, so the same speaker is named the same way wherever it is read.
//
// What it does not carry is the product, because the voice table is the same
// table whichever product is running it. That is Appearance's, and Appearance is
// what a posted identity comes from.
func (s Speaker) Identity() Identity {
	spoken, ok := voices[s.Key()]
	if !ok {
		return Identity{}
	}
	name := spoken.title
	if agent := strings.TrimSpace(s.Agent); agent != "" && agent != string(s.Role) {
		name += " (" + agent + ")"
	}
	return Identity{Name: name, Avatar: spoken.avatar}
}

// Avatars is a project's override of the picture beside a speaker's name, keyed
// exactly as an envelope names a speaker: a role, or the harness.
//
// It is the only part of an identity a project chooses, and that boundary is the
// point. Who speaks, and the name that says so, stay the voice table's: whose
// account a message is is a claim about who did the work, and a project that
// could rewrite it could attribute a promotion to a developer. The avatar is
// what the design calls strict decoration, so it is the part that can be a
// preference without any of that riding on it. The product a name is qualified
// by is not a preference either — it is read from the product's own id, the same
// for every speaker — so it is Appearance's rather than this table's.
type Avatars map[string]string

// avatar is this project's picture for one speaker, or empty where it named
// none. An entry that is blank is the same as no entry: configuring one
// persona's avatar does not quietly un-decorate the rest.
func (a Avatars) avatar(speaker Speaker) string {
	return strings.TrimSpace(a[speaker.Key()])
}

// Appearance is how the speakers of one product appear: the product they speak
// for, and that project's overrides of the pictures beside their names. It is
// one type rather than two arguments because it is the single door a posted
// identity comes through — a surface that could reach the shipped name directly
// is a surface that could post a name saying nothing about which harness said
// it.
type Appearance struct {
	// Product is the id of the product this harness runs, taken from the
	// configuration and never authored per message. Every speaker's name carries
	// it, the harness's included: an operator develops more than one product, and
	// where two of them are read in one channel the name is the only thing a
	// message carries that says which harness is talking. A configuration that
	// names no product leaves the shipped names alone, which is the appearance
	// there was before there was a second product to tell apart.
	Product domain.ProductID
	// Avatars is this project's override of the picture beside each name, and is
	// nil for a project that configured none.
	Avatars Avatars
}

// Identity is how a speaker appears once this product and its overrides are
// applied: the shipped name with the product it speaks for after it, and the
// picture the project chose for it. The product is last and always in the same
// shape, so two harnesses in one channel are told apart at a glance rather than
// by reading a message.
func (a Appearance) Identity(speaker Speaker) Identity {
	identity := speaker.Identity()
	if override := a.Avatars.avatar(speaker); override != "" {
		identity.Avatar = override
	}
	// A speaker with no voice has no name to qualify, and a product suffix on its
	// own would be a name for nobody rather than the empty identity a caller has
	// to notice.
	if product := strings.TrimSpace(string(a.Product)); product != "" && identity.Name != "" {
		identity.Name += " (" + product + ")"
	}
	return identity
}

// voice is one persona's own way of saying what the record holds: how it
// appears, and its line for every reportable kind. Every persona has a line for
// every kind, with no shared fallback anywhere, because a persona falling back
// to the harness's flat sentence would be a message attributed to somebody who
// does not sound like that.
type voice struct {
	title  string
	avatar string
	lines  map[Kind]string
}

// The harness: what no persona did. Flat, no first person, no judgment. It is
// the voice a promotion is reported in, and a promotion is the harness's own act
// by invariant — no agent performs one.
var harnessVoice = voice{
	title:  "Yoyodyne",
	avatar: ":gear:",
	lines: map[Kind]string{
		KindItemAdmitted:             "Admitted to the backlog: {title}. It serves: {goal}",
		KindItemDecomposed:           "Decomposed out of a larger item: {title}",
		KindItemAttributed:           "{item} was attributed to a goal: {goal}",
		KindItemReprioritized:        "{item} was set to {priority}.",
		KindTrackerBlockRefused:      "The {asking} asked for {refused} in one reply and the harness refused the block whole, so none of them happened: {why}",
		KindTrackerRefusalUnresolved: "A block from the {asking} was refused and the turn after it did not put the actions back — {cause} — so {refused} are still lost and nothing further will be tried: {why}",
		KindWorkApproved:             "The operator approved proposed work, and it was admitted: {title}. It serves: {goal}",
		KindWorkDeclined:             "The operator declined proposed work — {title} — because: {why}",
		KindWorkHandedOff:            "{item} was handed to {executor} rather than to a developer run: {why}",
		KindWorkPickedUp:             "Taken up in conversation: {title}",
		KindWorkCarriedOut:           "{item} was closed by the conversation carrying it: {why}",
		KindCapCrossed:               "The {budget} cap for {item} was crossed to {cap} by the development manager on delegated authority, which is {crossing} for this item: {why}",
		KindRunStarted:               "{item} claimed and started as {run}, on account {account} at configuration {config}. Selected by {by}: {reason}",
		KindChecksPassed:             "Checks passed on {item} in {checks}.",
		KindChecksFailed:             "Checks failed on {item}: {command} exited {exit}.",
		KindPathRefused:              "The change on {item} was refused before any check ran: it touches {paths}, and the item grants {grants}. A line reading {grant} in the item's own text is what would admit it.",
		KindReviewApproved:           "Review of {item} approved.",
		KindReviewRepairs:            "Review of {item} asked for repairs: {findings}.\n\n{requested}",
		KindRaceLost:                 "{item} lost its race for {branch}: the target moved under the approved change, so it was replayed onto where the target went and goes through the checks and a fresh review again. That is {races}; a replay that passes costs the item nothing.",
		KindPromoted:                 "{item} promoted onto {branch} at {commit}.",
		KindPublished:                "{item} published as {pr}.",
		KindMergeQueued:              "The merge of {pr} is queued on the forge.",
		KindMergeCompleted:           "{pr} merged.",
		KindMergeDropped:             "The merge of {pr} is not going to happen: {cause}. The change is promoted; the publication is not.",
		KindMergeWaitingOnTarget:     "The merge of {pr} waits on the target rather than on the change: {cause}.",
		KindLandingGreen:             "{item} landed green: {landing}.",
		KindLandingRed:               "{item} landed red: {landing}.",
		KindLandingUnverified:        "{item} landed unverified: {landing}.",
		KindRunParked:                "{run} stopped part-way through {item}, waiting on {cause}.",
		KindRunContinued:             "{run} continued on {item}.",
		KindBlockerRecorded:          "{item} is blocked, and {remains}: {text}",
		KindRunEnded:                 "The run on {item} {ending}, and {remains}: {text}",
		KindUsageLimitExhausted:      "The provider refused {waiting}: {cause}.",
		KindModelSubstituted:         "{waiting} is being served by {servedby} rather than {model}: {cause}. Nothing has stopped; the model behind the work has changed.",
		KindReportFiled:              "Reported on {item}: {text}",
		KindProposalRaised:           "A change to {artifact} is proposed: {text}",
		KindExchangeTurn:             "{exchange}, {rounds}: {text}",
		KindExchangeClosed:           "{exchange} closed {outcome}.",
		KindDirectiveRecorded:        "Recorded, for the {receiver}: {text} — {effect}",
		KindDirectiveResolved:        "That is settled: {text}",
		KindDirectiveCarriedOut:      "That was carried out: {text}",
		KindDirectiveRefused:         "Nothing was recorded from that reply: {why}",
		KindDirectiveWithdrawn:       "That was withdrawn, and no longer applies: {text}",
		KindQuestionHeard:            "Heard as a question rather than an instruction, so nothing was recorded against this item; the Lead Product Manager's answer follows here.",
		KindIntakeHeld:               "Intake is held for this product: {why}{stops}{lifts}",
		KindIntakeReleased:           "Intake is released for this product, {released}.",
		KindIntakeEscalated:          "The brake's hold on intake is escalated to the operator by the harness: {why}",
		KindOperatorAction:           "This needs your hand: {needs} Found by {foundby}; recorded in {recordedin}. Nothing here changes it, and this is not said again — `yoyo status` names it until it is done, and {ends}.",
		KindHoldPlaced:               "All harness activity is held.",
		KindHoldLifted:               "The hold on harness activity is lifted.",
		KindWatchStarted:             "A watch session is open on this product: {why}",
		KindWatchIdle:                "The watch session started nothing at this poll and is polling again: {why}",
		KindWatchBraked:              "The watch session is choosing nothing while intake is held: {why}",
		KindWatchBlocked:             "The watch session can start nothing until the machine is put right: {why}",
		KindWatchResumed:             "The watch session is choosing work again: {why}",
		KindWatchStopped:             "The watch session ended: {why}",
		KindWatchRedeploying:         "The watch session is restarting into the build deployed over it, having waited out every run it started: {why}",
		KindWatchReadRetrying:        "The watch session could not read the harness's store at this poll and is reading it again, so nothing is known about the queue from it: {why}",
		KindLineWaiting:              "Nothing is being chosen on this product: {stopped}, for {age} now, with {ready} ready to pull and {publications}.\n\n{standing}",
		KindStallNoticed:             "Nothing at all has started on this product for {age}, with {ready} ready to pull: {cause}. {stopped}.\n\n{standing}",
		KindProviderWindow:           "{stopped}. Nothing has been chosen on this product for {age}; nothing has stopped and nothing is waiting on anybody, and the harness asks again when the window lifts.\n\n{standing}",
		KindCapacityHold:             "{stopped}. Nothing has moved on this product for {age}: every turn every role would take asks a model the provider is refusing, and nothing any of them may fail over to is being served either. The window lifts on the provider's clock; failover on the agents is what would move the work before it does.\n\n{standing}",
		KindProviderOutage:           "{stopped}. Every run in flight is waiting on it with its claim, its branch, and its worktree kept, no relaunch or repair attempt is being spent, and the intake brake is not tripping on it. The harness asks again on its own; nothing to release, nothing to restart.\n\n{standing}",
		KindProviderRestored:         "The provider is answering again after {age}: {stopped}. Every run that was waiting has resumed where it stopped, and the queue is being pulled from again. Nothing was released and nothing was restarted.",
		KindRecurringTaskFailing:     "{stopped}. Each of those is a failed firing rather than a partial pass: nothing was asked of the role and nothing was spent, and the next firing meets the same refusal until its cause is fixed. It has stood for {age}.",
		KindClaimReleased:            "{item} was claimed with nothing working on it, so the harness gave it back to the queue: {stopped}. Nothing had moved on it for {age}.",
		KindResidentStale:            "The watch session on this product is running a build from before {behind} landed, made at {commit}. It restarts itself into a build installed over it, between the runs it is carrying.",
		KindBundleImprovement:        "{improvement}. Nothing has changed and nothing is waiting on anybody: `yoyo config drift` shows {setting} beside everything else the template moved, and it is adopted by hand or not at all.",
		KindBundleImprovements:       "{improvement}. Nothing has changed and nothing is waiting on anybody: `yoyo config drift` shows what each one was and is, and each is adopted by hand or not at all.",
		KindCatchUpDigest:            "{events} were recorded here over {age} while nothing was posting them. Every one of them is in the durable record.",
		KindLogLineSkipped:           "A line of the {log} log could not be read and was read past — {line}: {cause}. Whatever it recorded is not said here; everything recorded after it is.",
	},
}

// The developer: one bounded item, first person, evidence somebody else can
// verify.
var developerVoice = voice{
	title:  "Developer",
	avatar: ":hammer_and_wrench:",
	lines: map[Kind]string{
		KindItemAdmitted:             "{title} is in the backlog and could reach me one day, admitted for {goal}.",
		KindItemDecomposed:           "A larger item was broken down, and one of the pieces I could be handed is {title}.",
		KindItemAttributed:           "{item} now says what it is for: {goal}. That is the intent I'd be building against.",
		KindItemReprioritized:        "{item} sits at {priority} now. What I build doesn't change with the order it is queued in.",
		KindTrackerBlockRefused:      "A block the {asking} sent was refused together — {refused} — and nothing in the queue moved for any of it: {why}",
		KindTrackerRefusalUnresolved: "The {asking} lost a block and the turn after it put nothing back — {refused} still lost, and {cause} — so this one needs a person: {why}",
		KindWorkApproved:             "The operator approved that one, so it is work somebody will be given: {title}, for {goal}.",
		KindWorkDeclined:             "Proposed work was turned down before it reached anybody — {title} — because: {why}",
		KindWorkHandedOff:            "{item} will never reach me: it is carried by {executor} rather than by a run, because {why}",
		KindWorkPickedUp:             "Somebody has started {title} in conversation. There is no worktree and no diff in that.",
		KindWorkCarriedOut:           "{item} is finished without a change of mine ever being written: {why}",
		KindCapCrossed:               "The {budget} cap on {item} was crossed to {cap} without the operator being asked first — {crossing} — so what I was refused is something I can be handed now: {why}",
		KindRunStarted:               "I've picked up {item} as {run}, on account {account} at configuration {config}. It came to me from {by}: {reason}",
		KindChecksPassed:             "Checks are green on {item} in {checks}. I ran them before calling anything done.",
		KindChecksFailed:             "Checks are red on {item}: {command} exited {exit}. That is my next attempt, not somebody else's problem.",
		KindPathRefused:              "My change on {item} was refused before any check ran: it touches {paths}, and the item grants {grants}. I'm taking that back out of the change. If the work genuinely needs it, a line reading {grant} in the item's own text is what would admit it, and that line is not mine to write.",
		KindReviewApproved:           "The reviewer approved my change on {item}.",
		KindReviewRepairs:            "{item} came back to me with {findings}. I'll take them as written.\n\n{requested}",
		KindRaceLost:                 "{branch} moved under my approved change on {item}, so it is being replayed onto where the branch went and checked and reviewed again. That is {races}; nothing about the change is in question.",
		KindPromoted:                 "My change on {item} is on {branch}, at {commit}.",
		KindPublished:                "I've published the change on {item} as {pr}.",
		KindMergeQueued:              "{pr} is queued to merge; there is nothing more from me on {item}.",
		KindMergeCompleted:           "{pr} is merged, so {item} is out of my hands.",
		KindMergeDropped:             "{pr} was never merged and will not be by itself: {cause}. My change is on the target branch; what is on the forge is not.",
		KindMergeWaitingOnTarget:     "{pr} is waiting on the target branch, not on my change: {cause}.",
		KindLandingGreen:             "The landing checks over what I landed for {item} are green: {landing}.",
		KindLandingRed:               "The landing checks over what I landed for {item} are red: {landing}. My change passed its own gate; what broke is over the whole suite, and the item it filed is where that gets fixed.",
		KindLandingUnverified:        "The landing checks over what I landed for {item} did not run: {landing}. Nothing says the target branch is green after my change.",
		KindRunParked:                "I've stopped mid-change on {item}, waiting on {cause}. The worktree keeps everything.",
		KindRunContinued:             "Back on {item}, picking the change up where I left it.",
		KindBlockerRecorded:          "I could not finish {item}, and {remains}: {text}",
		KindRunEnded:                 "My attempt at {item} {ending} rather than stopping on anything anybody has to decide, and {remains}: {text}",
		KindUsageLimitExhausted:      "The provider has nothing left for {waiting}: {cause}. None of my work moves until it lifts.",
		KindModelSubstituted:         "{waiting} carries on under {servedby} rather than {model}: {cause}. My work keeps moving, and what produced it while it did is written down.",
		KindReportFiled:              "Noticed while working on {item}: {text}",
		KindProposalRaised:           "{artifact} isn't mine to edit, so I'm proposing the change instead: {text}",
		KindExchangeTurn:             "On {exchange}, {rounds}: {text}",
		KindExchangeClosed:           "{exchange} closed {outcome}. Back to the change.",
		KindDirectiveRecorded:        "I've been told something about this item, and it is written down rather than left in a thread: {text} — {effect}",
		KindDirectiveResolved:        "That is settled: {text}. I pick the change up from where it stopped.",
		KindDirectiveCarriedOut:      "What was asked for is done: {text}. Nothing about this item was waiting on it.",
		KindDirectiveRefused:         "That reply changed nothing about what I'm building: {why}",
		KindDirectiveWithdrawn:       "That was taken back, so I'm no longer building under it and nothing about this item waits on it: {text}",
		KindQuestionHeard:            "That was a question rather than direction, so nothing about what I'm building changed by it; the Lead Product Manager answers it here.",
		KindIntakeHeld:               "Intake is held, so nothing new reaches me: {why}{stops}{lifts}",
		KindIntakeReleased:           "Intake is open again, {released}; I'll take what I'm given.",
		KindIntakeEscalated:          "The harness has stopped probing the line with runs like mine and handed the hold to the operator: {why}",
		KindOperatorAction:           "Something only you can change is recorded against my work: {needs} Found by {foundby}; recorded in {recordedin}. I can't make that change from a run, nothing here asks you twice, and {ends}.",
		KindHoldPlaced:               "Held before my next provider call. Nothing of the change is lost.",
		KindHoldLifted:               "The hold is lifted; I'm carrying on.",
		KindWatchStarted:             "Work can reach me without anybody typing an identifier now: {why}",
		KindWatchIdle:                "Nothing was handed to me at this poll, so I'm waiting rather than working: {why}",
		KindWatchBraked:              "Nothing is being handed to me while intake is held: {why}",
		KindWatchBlocked:             "There is work queued for me and nothing can be started on this machine: {why}",
		KindWatchResumed:             "Work is being handed out again, and I'll take what I'm given: {why}",
		KindWatchStopped:             "Nothing more will be handed to me until somebody starts it again: {why}",
		KindWatchRedeploying:         "The session that hands me work is restarting into a newer build of itself; work will keep arriving once it is back: {why}",
		KindWatchReadRetrying:        "Nothing was handed to me because the harness's store could not be read, not because the queue is empty; it is being read again: {why}",
		KindLineWaiting:              "Nothing has been handed to me for {age}: {stopped}, with {ready} in the queue that could have been and {publications} from work already done.\n\n{standing}",
		KindStallNoticed:             "Nothing has been handed to me for {age} — {ready} sat ready the whole time: {cause}. {stopped}.\n\n{standing}",
		KindProviderWindow:           "{stopped}. Nothing has been handed to me for {age}, and that is not a queue nobody is reading — there is nothing to pick up until the provider serves again.\n\n{standing}",
		KindCapacityHold:             "{stopped}. Nothing has reached me for {age}, and nothing will: every role here is refused on the model it asks for, mine included, and nothing my turns could be moved onto is being served either.\n\n{standing}",
		KindProviderOutage:           "{stopped}. My runs are holding where they stopped — claim, branch, worktree, session, all kept — and I am asking again on my own; nothing I was doing is being counted against me.\n\n{standing}",
		KindProviderRestored:         "The provider is answering again after {age}: {stopped}. I have picked my runs up exactly where they were waiting.",
		KindRecurringTaskFailing:     "{stopped}. Nothing I would be handed by that pass is being decided while it fails, and waiting will not end it: every firing meets the same refusal. It has stood for {age}.",
		KindClaimReleased:            "{item} was sitting claimed with no run alive behind it for {age} — {stopped} — so it is back in the queue and can be handed to me again.",
		KindResidentStale:            "What hands me work was built at {commit}, before {behind} landed. A fix already on the main line is not in what runs me until that build is installed over it, and I'd spend the round finding that out.",
		KindBundleImprovement:        "{improvement}. What the template says about {setting} is what I'd be run under if this project took it up, and until somebody does I go on being run under what it holds now.",
		KindBundleImprovements:       "{improvement}. What the template says about each of them is what I'd be run under if this project took them up, and until somebody does I go on being run under what it holds now; `yoyo config drift` shows what each one was and is.",
		KindCatchUpDigest:            "There are {events} here from {age} nobody was watching. I'm not replaying the work message by message; the record kept all of it.",
		KindLogLineSkipped:           "One line of the {log} log would not read back — {line}: {cause}. I can't say what it held; what was recorded after it is still being said.",
	},
}

// The reviewer: judges one change against the item that asked for it, did not
// write it, does not fix it.
var reviewerVoice = voice{
	title:  "Reviewer",
	avatar: ":mag:",
	lines: map[Kind]string{
		KindItemAdmitted:             "Admitted: {title}. What it serves — {goal} — is what I will judge a change against.",
		KindItemDecomposed:           "Cut out of a larger item: {title}. Narrower work is work I can judge as finished or not.",
		KindItemAttributed:           "{item} records the goal it serves: {goal}. Intent I can read beats intent I have to infer.",
		KindItemReprioritized:        "{item} moved to {priority}. The order work arrives in changes nothing about the standard it meets.",
		KindTrackerBlockRefused:      "The {asking} asked for {refused} and the harness read none of them, so what the queue says now is what it said before: {why}",
		KindTrackerRefusalUnresolved: "A block from the {asking} was refused and what followed did not restore it — {cause} — so {refused} are lost and what the queue says now is what it said before: {why}",
		KindWorkApproved:             "Approved and admitted: {title}, serving {goal}. I'll see it when a change comes back from it.",
		KindWorkDeclined:             "{title} was declined, so there is no change coming and nothing for me to judge: {why}",
		KindWorkHandedOff:            "{item} left the run queue for {executor}, so no change on it will come to me: {why}",
		KindWorkPickedUp:             "{title} is under way in conversation. Nothing is coming to me for a verdict on it.",
		KindWorkCarriedOut:           "{item} is done and was never judged, because there was no change to judge: {why}",
		KindCapCrossed:               "{item} has been given more room than the project configured: the {budget} cap now stands at {cap}, on {crossing}. I judge the change and not the budget, and the budget is why it comes back to me again: {why}",
		KindRunStarted:               "{item} is under way as {run}, on account {account} at configuration {config}, chosen by {by}: {reason}. I'll judge what comes back rather than how it got here.",
		KindChecksPassed:             "The checks behind {item} pass, in {checks}. Passing checks are evidence, not a verdict.",
		KindChecksFailed:             "The checks behind {item} fail: {command} exited {exit}. There is nothing for me to judge yet.",
		KindPathRefused:              "The change on {item} was refused before it could reach me: it touches {paths}, and the item grants {grants}. Whether the work needs a line reading {grant} in the item is a decision about the item, not a finding of mine.",
		KindReviewApproved:           "I approve {item}: correct and complete against the criteria it was given.",
		KindReviewRepairs:            "I'm asking for repairs on {item}: {findings}, each one specific enough to act on.\n\n{requested}",
		KindRaceLost:                 "{branch} moved before the change I approved on {item} could land, so my approval is set aside and I will judge the replayed change afresh. That is {races}.",
		KindPromoted:                 "The change I approved on {item} is on {branch} at {commit}.",
		KindPublished:                "{item} is published as {pr}. My verdict was on the change, not on the request.",
		KindMergeQueued:              "{pr} is queued to merge with my approval behind it.",
		KindMergeCompleted:           "{pr} is merged, so what I approved is what landed.",
		KindMergeDropped:             "{pr} did not merge: {cause}. What I approved is on the target branch, and the request carrying it is still open.",
		KindMergeWaitingOnTarget:     "{pr} is held for the target's red check, not for anything I reviewed: {cause}.",
		KindLandingGreen:             "The whole suite passes over what landed for {item}: {landing}. That is the evidence the narrowed gate could not give.",
		KindLandingRed:               "The whole suite fails over what landed for {item}: {landing}. I approved the change on the gate it was given; the suite says something the gate did not.",
		KindLandingUnverified:        "The whole suite was not run over what landed for {item}: {landing}. What I approved stands on the narrowed gate alone.",
		KindRunParked:                "{item} stopped before there was a verdict, waiting on {cause}.",
		KindRunContinued:             "{item} is moving again; I'll see the change when it is ready.",
		KindBlockerRecorded:          "{item} is blocked and there is no change to judge, though {remains}: {text}",
		KindRunEnded:                 "The run on {item} {ending} before I was given anything to judge, and {remains}: {text}",
		KindUsageLimitExhausted:      "{waiting} was refused for want of provider capacity: {cause}. Nothing arrives for a verdict until it lifts.",
		KindModelSubstituted:         "{waiting} is answering on {servedby} rather than {model}: {cause}. What reaches me for a verdict was produced by a different model, and I judge it the same way.",
		KindReportFiled:              "Noticed while reviewing {item}: {text}",
		KindProposalRaised:           "{artifact} is not mine to change, so this is an argument about it: {text}",
		KindExchangeTurn:             "In {exchange}, at {rounds}: {text}",
		KindExchangeClosed:           "{exchange} closed {outcome}; I judge the change against what that leaves.",
		KindDirectiveRecorded:        "This is recorded against the item, and I judge the change against it exactly as against one typed at a terminal: {text} — {effect}",
		KindDirectiveResolved:        "That is settled: {text}. What I judge against is settled with it.",
		KindDirectiveCarriedOut:      "That was carried out: {text}. It stood while I judged this and it stands now.",
		KindDirectiveRefused:         "Nothing in that reply reaches what I judge this against: {why}",
		KindDirectiveWithdrawn:       "That was taken back, so I no longer judge the change against it; what I judged while it stood was judged against it: {text}",
		KindQuestionHeard:            "That was a question rather than something I judge the change against, so nothing was recorded; the Lead Product Manager answers it here.",
		KindIntakeHeld:               "Intake is held, so nothing new will arrive for review: {why}{stops}{lifts}",
		KindIntakeReleased:           "Intake is open, {released}; work will reach me again.",
		KindIntakeEscalated:          "The harness has handed the brake's hold to the operator rather than probe the line again; nothing new reaches me until it is released: {why}",
		KindOperatorAction:           "A finding here is yours rather than a verdict's: {needs} Found by {foundby}; recorded in {recordedin}. No review changes it, it is said to you once, and {ends}.",
		KindHoldPlaced:               "Held before my next review. Nothing already judged changes.",
		KindHoldLifted:               "The hold is lifted; reviews resume.",
		KindWatchStarted:             "Changes will keep arriving for a verdict without anybody starting them: {why}",
		KindWatchIdle:                "Nothing new was started at this poll, so no further change is coming to me from it: {why}",
		KindWatchBraked:              "Nothing new is being started while intake is held, so nothing is coming for a verdict: {why}",
		KindWatchBlocked:             "No change can be started for me to judge, and it is the machine rather than the queue: {why}",
		KindWatchResumed:             "Work is starting again, so changes will come back to me: {why}",
		KindWatchStopped:             "No more changes will arrive from this session; what I judged already stands: {why}",
		KindWatchRedeploying:         "The session sending me changes is restarting into a newer build of itself, so what arrives next was chosen by the build that was deployed: {why}",
		KindWatchReadRetrying:        "The session sending me changes could not read the harness's store, so nothing new reaches me until it answers again: {why}",
		KindLineWaiting:              "No change has come to me for a verdict in {age}: {stopped}, with {ready} waiting behind it and {publications} I have already approved.\n\n{standing}",
		KindStallNoticed:             "No change has reached me for a verdict in {age}, and none was written — {ready} ready and no run started: {cause}. {stopped}.\n\n{standing}",
		KindProviderWindow:           "{stopped}. No change has reached me for a verdict in {age}: nothing was written for me to judge, and nothing was going to be.\n\n{standing}",
		KindCapacityHold:             "{stopped}. No change has reached me for a verdict in {age}, and none was written to reach me: the provider is refusing every role at once, so nobody writes and nobody judges.\n\n{standing}",
		KindProviderOutage:           "{stopped}. Nothing reaches me for a verdict and nothing is being written to: every invocation is waiting on the same provider, and none of it is a failure of the work.\n\n{standing}",
		KindProviderRestored:         "The provider is answering again after {age}: {stopped}. Changes reach me for a verdict again.",
		KindRecurringTaskFailing:     "{stopped}. None of that pass's work reaches anybody while it fails, and waiting will not end it: every firing meets the same refusal. It has stood for {age}.",
		KindClaimReleased:            "{item} was claimed with nothing working on it for {age}, so it is back in the queue: {stopped}. Nothing has been judged either way; the change that run left, if any, is still where it left it.",
		KindResidentStale:            "What sends me changes was built at {commit}, before {behind} landed. A repair round I grant against a bug that is already dead on the main line is a round nobody gets back, and installing that build is what stops me granting one — the session takes it up itself between runs.",
		KindBundleImprovement:        "{improvement}. It changes nothing about the standard I hold a change to today, and it would change {setting} for every change judged after somebody adopts it.",
		KindBundleImprovements:       "{improvement}. None of them changes the standard I hold a change to today, and each would change it for every change judged after somebody adopts it; `yoyo config drift` shows what each one was and is.",
		KindCatchUpDigest:            "{events} went unreported here across {age}. I judge changes rather than backlogs of messages, and the record holds each of them.",
		KindLogLineSkipped:           "The {log} log has a line nothing can decode, {line}: {cause}. I cannot judge what it recorded; the records after it are read as before.",
	},
}

// The development manager: the queue, its order, and what is honestly done.
var developmentManagerVoice = voice{
	title:  "Development Manager",
	avatar: ":clipboard:",
	lines: map[Kind]string{
		KindItemAdmitted:             "In the queue now: {title}, admitted for {goal}.",
		KindItemDecomposed:           "I've broken a larger item down, and a bounded piece of it is {title}.",
		KindItemAttributed:           "{item} now carries the goal it serves: {goal}. Work I cannot say the purpose of is work I cannot order honestly.",
		KindItemReprioritized:        "{item} is at {priority} now, so that is where it gets pulled from.",
		KindTrackerBlockRefused:      "A block from the {asking} never reached the queue I pull from — {refused} — so its order and its contents are unchanged: {why}",
		KindTrackerRefusalUnresolved: "Actions from the {asking} never reached the queue I pull from and the turn after did not put them there — {refused} still lost, and {cause} — and nothing else is going to: {why}",
		KindWorkApproved:             "Approved and in my queue: {title}, for {goal}.",
		KindWorkDeclined:             "{title} was declined, so nothing about it ever reaches my queue: {why}",
		KindWorkHandedOff:            "{item} is out of what I pull: it is carried by {executor}, so a run would only spend itself on it — {why}",
		KindWorkPickedUp:             "{title} is being carried in conversation now. It stays in flight until whoever holds it closes it.",
		KindWorkCarriedOut:           "{item} is done, closed by the conversation that carried it rather than by a run: {why}",
		KindCapCrossed:               "I crossed the {budget} cap on {item} to {cap} myself rather than waiting to be granted it — {crossing} of the ones that are mine to take. Overrule me if I have this wrong: {why}",
		KindRunStarted:               "I've pulled {item} off the queue, and it is claimed and started as {run}, on account {account} at configuration {config}: {reason}",
		KindChecksPassed:             "{item} cleared its checks in {checks} and is on to review.",
		KindChecksFailed:             "{item} came back from its checks: {command} exited {exit}. It routes to repair with that intact.",
		KindPathRefused:              "{item} was turned back before its checks: the change touches {paths}, and the item grants {grants}. It routes to repair, spending from the same budget; if the item should have granted that, a line reading {grant} in its text is what does.",
		KindReviewApproved:           "{item} is approved and clear to integrate.",
		KindReviewRepairs:            "{item} routes back to the developer with {findings} intact.\n\n{requested}",
		KindRaceLost:                 "{item} lost its race for {branch} and is being replayed, checked, and reviewed again — {races}. Nothing is docketed for it; a replay that passes costs no budget.",
		KindPromoted:                 "{item} is integrated into {branch}. That is one item actually done.",
		KindPublished:                "{item} is published as {pr} and still counts as in flight.",
		KindMergeQueued:              "{pr} is queued to merge, so {item} stays in flight until the forge says otherwise.",
		KindMergeCompleted:           "{pr} merged; {item} is done.",
		KindMergeDropped:             "{pr} will not merge on its own: {cause}. {item} is promoted, and its publication is now somebody's to settle by hand.",
		KindMergeWaitingOnTarget:     "{pr} waits on the target being fixed rather than on a decision: {cause}. The item filed for it is at the front of the queue.",
		KindLandingGreen:             "{item} landed and the whole suite is green over it: {landing}.",
		KindLandingRed:               "{item} landed and the whole suite is red over it: {landing}. The item that filed is at the front of the queue, and everything behind it is cut from that commit.",
		KindLandingUnverified:        "{item} landed and the whole suite could not be run over it: {landing}. Nobody knows whether the target branch is green.",
		KindRunParked:                "{item} is stopped part-way, waiting on {cause}. It keeps its claim.",
		KindRunContinued:             "{item} is moving again, from where it stopped.",
		KindBlockerRecorded:          "{item} is blocked, and recorded as blocked rather than left implicit, with {remains}: {text}",
		KindRunEnded:                 "The run on {item} {ending} with nothing recorded for anybody to decide, and {remains}: {text}",
		KindUsageLimitExhausted:      "{waiting} is stopped by the provider rather than by the queue: {cause}. Nothing moves until it lifts.",
		KindModelSubstituted:         "{waiting} is served by {servedby} rather than {model}: {cause}. My order stands and the queue keeps being pulled from.",
		KindReportFiled:              "Noticed while planning {item}: {text}",
		KindProposalRaised:           "{artifact} isn't mine to change, so here is the case for changing it: {text}",
		KindExchangeTurn:             "{exchange} is at {rounds} and the item waits on it: {text}",
		KindExchangeClosed:           "{exchange} closed {outcome}, and the item it held moves accordingly.",
		KindDirectiveRecorded:        "Direction came in on this thread and is recorded against the item: {text} — {effect}",
		KindDirectiveResolved:        "That is settled: {text}. The item it held moves again.",
		KindDirectiveCarriedOut:      "That was carried out: {text}. It held nothing up, so this is what came of it rather than the queue moving.",
		KindDirectiveRefused:         "That reply is not direction anything can act on, so nothing about this item moved: {why}",
		KindDirectiveWithdrawn:       "That was taken back, so it is no longer direction this item is under and anything it was holding moves again: {text}",
		KindQuestionHeard:            "That was a question rather than direction, so nothing about this item moved; the Lead Product Manager answers it here.",
		KindIntakeHeld:               "Intake is held, so I pull nothing new until it lifts: {why}{stops}{lifts}",
		KindIntakeReleased:           "Intake is released, {released}; I'm pulling from the top of the backlog again.",
		KindIntakeEscalated:          "The harness escalated the brake's hold to the operator over my head — I was asked every cycle and did not — so no further probe starts and the queue waits on a person: {why}",
		KindOperatorAction:           "This is on you rather than on my docket: {needs} Found by {foundby}; recorded in {recordedin}. I won't raise it again, and {ends}.",
		KindHoldPlaced:               "Everything is held. Nothing new starts, and nothing in flight is lost.",
		KindHoldLifted:               "The hold is lifted; the work in flight carries on.",
		KindWatchStarted:             "The queue is being pulled from until somebody stops it, rather than once: {why}",
		KindWatchIdle:                "Nothing was pulled from the queue at this poll: {why}",
		KindWatchBraked:              "I'm pulling nothing while intake is held, and what was already running finishes: {why}",
		KindWatchBlocked:             "The queue has pullable work in it and nothing can be started from it: {why}",
		KindWatchResumed:             "I'm pulling from the top of the queue again: {why}",
		KindWatchStopped:             "The queue stops being pulled from here; what is in it stays in it: {why}",
		KindWatchRedeploying:         "The queue stops being pulled from only until the session is back on the build deployed over it, and nothing in it moved: {why}",
		KindWatchReadRetrying:        "The queue could not be read at this poll, so what is in it is unknown rather than empty, and it is being read again: {why}",
		KindLineWaiting:              "The queue has not been pulled from for {age}: {stopped}, with {ready} pullable right now and {publications} still counting as in flight.\n\n{standing}",
		KindStallNoticed:             "My queue has not been pulled from in {age} — {ready} pullable, no hold on the line, no full machine, no run in flight: {cause}. {stopped}.\n\n{standing}",
		KindProviderWindow:           "{stopped}. My queue has not been pulled from for {age}; the order stands as I set it, and the next pull takes it from the top when the window lifts.\n\n{standing}",
		KindCapacityHold:             "{stopped}. My queue has not been pulled from for {age}, and I cannot decide what is waiting on me either: the provider refuses my turns as it refuses everybody's, and nothing they could be moved onto is being served.\n\n{standing}",
		KindProviderOutage:           "{stopped}. My queue is not being pulled from and my sweeps record the wait rather than a pass; nothing on it has stopped, the runs in flight are holding, and the brake is not tripping over this.\n\n{standing}",
		KindProviderRestored:         "The provider is answering again after {age}: {stopped}. My queue is being pulled from again, from the top, in the order I set.",
		KindRecurringTaskFailing:     "{stopped}. I am not being asked anything on that pass, so whatever it would have decided is waiting, and it will go on waiting: every firing meets the same refusal until its cause is fixed. It has stood for {age}.",
		KindClaimReleased:            "{item} had been out of the queue for {age} with nothing working on it, so it is back in it and pullable again: {stopped}.",
		KindResidentStale:            "What pulls my queue was built at {commit}, before {behind} landed. Rounds spent against work the system has already done come out of the same capacity the real queue does, and they stop when that build is installed — the session takes it up itself between runs.",
		KindBundleImprovement:        "{improvement}. Nothing in the queue moves for it, and nothing I hand out changes until {setting} is adopted by hand.",
		KindBundleImprovements:       "{improvement}. Nothing in the queue moves for any of them, and nothing I hand out changes until one is adopted by hand; `yoyo config drift` shows what each one was and is.",
		KindCatchUpDigest:            "{events} piled up here over {age} with nothing posting them. The work moved regardless, and the record is the account of it.",
		KindLogLineSkipped:           "A line of the {log} log is unreadable and reporting has stepped over it — {line}: {cause}. The queue's account carries on from the line after.",
	},
}

// The product manager: what the product is for, and what the operator needs to
// be able to disagree with.
var productManagerVoice = voice{
	title:  "Lead Product Manager",
	avatar: ":compass:",
	lines: map[Kind]string{
		KindItemAdmitted:             "I've admitted this to the backlog: {title}. It serves {goal}, and that claim is the operator's to disagree with.",
		KindItemDecomposed:           "{title} was decomposed out of a larger item. What the work is for stays what that larger item was admitted for.",
		KindItemAttributed:           "I've recorded what {item} is for: {goal}. Work that says nothing about intent is work nobody can decide to stop doing.",
		KindItemReprioritized:        "I've put {item} at {priority}: {why}",
		KindTrackerBlockRefused:      "A block I sent was refused whole — {refused} — so nothing I meant to change about the backlog changed: {why}",
		KindTrackerRefusalUnresolved: "A block I sent was refused and the turn after it did not put the actions back — {refused} — and {cause}, so I cannot put this right myself: {why}",
		KindWorkApproved:             "The operator approved the work I proposed, and it is admitted: {title}, serving {goal}.",
		KindWorkDeclined:             "The operator turned down work I proposed — {title}, which would have served {goal} — because: {why}. Nothing was created.",
		KindWorkHandedOff:            "{item} is work {executor} carries rather than a run, and it is marked as such so nothing spends a run on it: {why}",
		KindWorkPickedUp:             "Somebody has taken this up: {title}. What the work serves is unchanged by who carries it.",
		KindWorkCarriedOut:           "{item} is delivered, in a conversation rather than in a change: {why}",
		KindCapCrossed:               "More room went to {item} than the queue budgeted for it: the {budget} cap stands at {cap}, on {crossing}. The item is no closer to being decided for it: {why}",
		KindRunStarted:               "Work started on {item} as {run}, on account {account} at configuration {config}, chosen by {by}: {reason}. That reason is the operator's to disagree with.",
		KindChecksPassed:             "{item} passed its checks in {checks} — progress on what it was admitted for.",
		KindChecksFailed:             "{item} failed its checks: {command} exited {exit}. Nothing about what it is for has changed.",
		KindPathRefused:              "{item} reached for something it was not admitted to change: {paths}, where the item grants {grants}. If the work really needs it, the grant is a line reading {grant} in the item's text, and putting it there is a decision about scope rather than a repair.",
		KindReviewApproved:           "{item} was approved: what was admitted is what was built.",
		KindReviewRepairs:            "{item} needs repairs: {findings}. Still the same item, not a new one.\n\n{requested}",
		KindRaceLost:                 "{item} was approved and then {branch} moved under it, so it is being replayed and judged again before it lands. That is {races}; nothing about the work has changed.",
		KindPromoted:                 "{item} is integrated into {branch} and serves the goal it was admitted for.",
		KindPublished:                "{item} is published as {pr}.",
		KindMergeQueued:              "{pr} is queued to merge; {item} is not delivered until it lands.",
		KindMergeCompleted:           "{pr} merged, so {item} is delivered.",
		KindMergeDropped:             "{pr} is not merging: {cause}. {item} is built and promoted, and it is not delivered until somebody publishes it.",
		KindMergeWaitingOnTarget:     "{pr} is waiting on the target branch: {cause}. The fix is filed under the same goal {item} served.",
		KindLandingGreen:             "{item} landed green: {landing}. Nothing about what it is for has changed.",
		KindLandingRed:               "{item} landed red: {landing}. The work that answers it is filed under the same goal the item served.",
		KindLandingUnverified:        "{item} landed without its landing checks: {landing}. What it is for has not changed; what nobody checked has.",
		KindRunParked:                "{item} is waiting on {cause}. Nothing about its priority changed while it waits.",
		KindRunContinued:             "{item} is moving again.",
		KindBlockerRecorded:          "{item} is blocked and stays in the backlog until somebody decides otherwise, with {remains}: {text}",
		KindRunEnded:                 "The attempt on {item} {ending}, which is no verdict on what the item is for, and {remains}: {text}",
		KindUsageLimitExhausted:      "Nothing is being spent, because the provider refused {waiting}: {cause}. What is admitted keeps its place.",
		KindModelSubstituted:         "{waiting} is being served by {servedby} rather than {model}: {cause}. The work carries on and is priced against the model that served it.",
		KindReportFiled:              "Noticed about {item}: {text}",
		KindProposalRaised:           "A change to {artifact} is proposed, and I decide it only where the document is mine: {text}",
		KindExchangeTurn:             "The operator is being asked something, in {exchange} at {rounds}: {text}",
		KindExchangeClosed:           "{exchange} closed {outcome}. What it settles is product intent.",
		KindDirectiveRecorded:        "The operator has directed this work from the thread, and it is recorded for the {receiver}: {text} — {effect}",
		KindDirectiveResolved:        "The operator settled it: {text}",
		KindDirectiveCarriedOut:      "It was carried out, and this is what came of what the operator asked for: {text}",
		KindDirectiveRefused:         "The operator said something here the harness would not record as a directive rather than guess at it: {why}",
		KindDirectiveWithdrawn:       "The operator took that back, so it no longer applies; what was directed while it stood stays on the record: {text}",
		KindQuestionHeard:            "I read that as a question rather than an instruction, so nothing was recorded against this item; my answer follows here.",
		KindIntakeHeld:               "Intake is held, so nothing new is chosen until somebody lifts it: {why}{stops}{lifts}",
		KindIntakeReleased:           "Intake is released, {released}; the backlog is being pulled from again.",
		KindIntakeEscalated:          "The brake's hold on intake is now the operator's: the harness stopped asking the development manager and escalated it itself, so nothing I admit is chosen until somebody releases it: {why}",
		KindOperatorAction:           "This one needs your hand rather than a decision of mine: {needs} Found by {foundby}; recorded in {recordedin}. It stays named on `yoyo status` until it is done — {ends} — and I won't say it again.",
		KindHoldPlaced:               "The operator holds all harness activity.",
		KindHoldLifted:               "The operator lifted the hold.",
		KindWatchStarted:             "What is admitted is now what is spent on, since the queue is pulled from until somebody stops it: {why}",
		KindWatchIdle:                "No new spending was started at this poll: {why}",
		KindWatchBraked:              "Spending has stopped while intake is held, and what is in the backlog keeps its place: {why}",
		KindWatchBlocked:             "Nothing I admit is being started, and admitting more would not change that: {why}",
		KindWatchResumed:             "Work is being chosen again, and what I admit is what gets spent on: {why}",
		KindWatchStopped:             "Nothing further is being chosen or spent, and the backlog is untouched by that: {why}",
		KindWatchRedeploying:         "Choosing and spending pause only while the session restarts into the build deployed over it, and the backlog is untouched by that: {why}",
		KindWatchReadRetrying:        "The backlog could not be read at this poll, so nothing new was started and nothing about it is known; it is being read again: {why}",
		KindLineWaiting:              "Nothing has been spent on this product for {age}: {stopped}, with {ready} admitted and ready to be worked on, and {publications} paid for and not yet delivered.\n\n{standing}",
		KindStallNoticed:             "Nothing has been spent on this product for {age}, and {ready} I admitted is still waiting: {cause}. This is not a quiet queue; it is a queue nothing has been started from. {stopped}.\n\n{standing}",
		KindProviderWindow:           "{stopped}. Nothing has been spent on this product for {age}: this is the one quiet stretch that costs nothing and buys nothing, and it ends on the provider's clock rather than on anybody's decision.\n\n{standing}",
		KindCapacityHold:             "{stopped}. Nothing has been spent on this product for {age}, and this quiet is not free: every role is refused at once, the window lifts on the provider's clock, and a second model each agent could fail over to is what keeps the work moving through the next one.\n\n{standing}",
		KindProviderOutage:           "{stopped}. Nothing is being spent on this product and nothing is being lost: the work in flight is kept where it is, and this ends when the provider answers rather than on any clock or any decision of mine.\n\n{standing}",
		KindProviderRestored:         "The provider is answering again after {age}: {stopped}. The product is moving again, and nothing that was in flight was lost to the wait.",
		KindRecurringTaskFailing:     "{stopped}. What that pass decides is not being decided, and nothing on a clock ends it: every firing meets the same refusal until its cause is fixed. It has stood for {age}.",
		KindClaimReleased:            "{item} spent {age} looking like work in progress while nothing was being spent on it. It is back in the queue: {stopped}.",
		KindResidentStale:            "What is being spent on this product was built at {commit}, before {behind} landed. Until that build is installed, some of that spend buys work the system has already paid for once; the session takes it up itself between runs once it is.",
		KindBundleImprovement:        "{improvement}. Whether {setting} is worth taking is the operator's to decide and nobody else's, which is why it is offered once rather than asked for repeatedly.",
		KindBundleImprovements:       "{improvement}. Whether any of them is worth taking is the operator's to decide and nobody else's, which is why they are offered once, together, rather than one message each; `yoyo config drift` shows what each one was and is.",
		KindCatchUpDigest:            "{events} accumulated here over {age} that nobody read as they happened. What they add up to is in the record, rather than in a scroll of replays.",
		KindLogLineSkipped:           "Something recorded in the {log} log cannot be read back — {line}: {cause}. It has been read past so that nothing recorded after it goes unsaid.",
	},
}

// The architect: the shape of the system, the constraints on it, and whether
// what happened is what the design said would.
var architectVoice = voice{
	title:  "Architect",
	avatar: ":triangular_ruler:",
	lines: map[Kind]string{
		KindItemAdmitted:             "Entered the backlog under {goal}: {title}. Work traceable to intent is what keeps the shape of the thing deliberate.",
		KindItemDecomposed:           "Carved out of a larger item: {title}. Decomposition is structure, and structure is where a design holds or does not.",
		KindItemAttributed:           "{item} was traced back to {goal}. A queue nobody can trace is a system nobody can reason about.",
		KindItemReprioritized:        "{item} moved to {priority}, which changes the order and nothing about the design it derives from.",
		KindTrackerBlockRefused:      "A block of {refused} from the {asking} was refused whole rather than partly applied, which is the contract holding: {why}",
		KindTrackerRefusalUnresolved: "A refused block from the {asking} that the turn after it did not restore — {cause} — leaves {refused} lost and the self-correcting path exhausted: {why}",
		KindWorkApproved:             "Approved and admitted: {title}, under {goal}. What it may become is bounded by the design it derives from.",
		KindWorkDeclined:             "{title} was declined, and the shape of the system is unchanged by work nobody started: {why}",
		KindWorkHandedOff:            "{item} is executed by {executor} rather than by a run, and saying so is what keeps a run from discovering it by refusing an empty diff: {why}",
		KindWorkPickedUp:             "{title} has been taken up in conversation. What it produces is a judgment rather than a diff.",
		KindWorkCarriedOut:           "{item} is carried out, and what it settled is in the documents rather than in a promotion: {why}",
		KindCapCrossed:               "A budget the project set has been crossed rather than argued with: {item} now has a {budget} cap of {cap}, on {crossing}. A cap crossed repeatedly is a cap that is wrong or work that is: {why}",
		KindRunStarted:               "{item} is under way as {run}, on account {account} at configuration {config}, chosen by {by}: {reason}. The design it derives from is unchanged.",
		KindChecksPassed:             "{item} passed its checks in {checks}. The gate held.",
		KindChecksFailed:             "{item} failed its checks: {command} exited {exit}. A gate that catches this is a gate doing its job.",
		KindPathRefused:              "{item} was refused for touching {paths}, which the item does not grant — it grants {grants}. That is the boundary between a developer's change and the documents it is measured against holding; a line reading {grant} in the item is how an exception is decided before a run rather than by one.",
		KindReviewApproved:           "{item} was approved against the design it derives from.",
		KindReviewRepairs:            "{item} was sent back with {findings}, which is the loop working rather than failing.\n\n{requested}",
		KindRaceLost:                 "{item} lost its race for {branch}: the promotion refused a target that had moved, and the change is replayed and re-earns the whole gate rather than carrying its approval over. That is {races}.",
		KindPromoted:                 "{item} is on {branch} at {commit}, promoted by the harness itself as the invariant requires.",
		KindPublished:                "{item} is published as {pr}. The local branch remains the authoritative one.",
		KindMergeQueued:              "{pr} is queued to merge; the forge settles it, not this run.",
		KindMergeCompleted:           "{pr} merged, so the forge's history and the local target agree again.",
		KindMergeDropped:             "{pr} was dropped rather than merged: {cause}. The local target carries the promotion and the forge does not, which is the divergence somebody has to close.",
		KindMergeWaitingOnTarget:     "{pr} waits on the target branch's own red check: {cause}. Nothing but the change differs from the target, so the failure is the target's.",
		KindLandingGreen:             "{item} landed green: {landing}. The whole suite agrees with the narrowed gate.",
		KindLandingRed:               "{item} landed red: {landing}. The narrowed gate passed and the whole suite did not, which is exactly the case the landing check exists to catch.",
		KindLandingUnverified:        "{item} landed unverified: {landing}. A gate narrowed to what a change touches is only as safe as the whole suite run behind it.",
		KindRunParked:                "{item} is stopped part-way, waiting on {cause}. A design that cannot survive an interruption is the wrong design.",
		KindRunContinued:             "{item} resumed from exactly where it stopped.",
		KindBlockerRecorded:          "{item} is blocked, which is a fact about the system rather than about the attempt, and {remains}: {text}",
		KindRunEnded:                 "The run on {item} {ending}, which is a fact about the attempt rather than about the work, and {remains}: {text}",
		KindUsageLimitExhausted:      "{waiting} met the account's own ceiling rather than a defect: {cause}. Capacity is a boundary condition, and a design that treats it as a failure is the wrong design.",
		KindModelSubstituted:         "{waiting} fell back from {model} to {servedby}, which is a stated alternative rather than a degradation: {cause}. The substitution is recorded per turn, and affinity returns to {model} as soon as it can be asked for again.",
		KindReportFiled:              "Noticed about the design of {item}: {text}",
		KindProposalRaised:           "{artifact} should change, and this is the case: {text}",
		KindExchangeTurn:             "{exchange}, {rounds}, on what the design left open: {text}",
		KindExchangeClosed:           "{exchange} closed {outcome}, which is where the design stops being ambiguous.",
		KindDirectiveRecorded:        "This arrived through a thread rather than a terminal, and is the same record with the same force either way: {text} — {effect}",
		KindDirectiveResolved:        "That is settled: {text}. The pause it held is lifted where it stood, rather than the work restarting.",
		KindDirectiveCarriedOut:      "That was carried out: {text}. A directive that pauses nothing still has a disposition, and this is it recorded rather than remembered.",
		KindDirectiveRefused:         "The channel refused that reply rather than inferring a directive from it: {why}",
		KindDirectiveWithdrawn:       "That was taken back and kept rather than deleted, so what was done while it stood stays explicable: {text}",
		KindQuestionHeard:            "That was a question rather than a directive, so the record holds nothing from it; the Lead Product Manager answers it here.",
		KindIntakeHeld:               "Intake is held, which stops selection and nothing already running: {why}{stops}{lifts}",
		KindIntakeReleased:           "Intake is released, {released}; selection resumes.",
		KindIntakeEscalated:          "The brake's summons-and-probe loop reached its bound and the harness escalated the hold to the operator, which is the loop working as designed rather than standing silent: {why}",
		KindOperatorAction:           "A change only the operator can make is recorded: {needs} Found by {foundby}; recorded in {recordedin}. No design decides it, it is said once, and {ends}.",
		KindHoldPlaced:               "All harness activity is held, at the provider-call boundary rather than mid-generation.",
		KindHoldLifted:               "The hold is lifted, and every run that stopped for it carries on from its own record.",
		KindWatchStarted:             "Selection is now a loop rather than a pass, and nothing between its readings is cached: {why}",
		KindWatchIdle:                "Selection read the queue and started nothing, which costs a tracker read and no provider call: {why}",
		KindWatchBraked:              "Selection is stopped by the intake hold, which is that hold working rather than failing: {why}",
		KindWatchBlocked:             "Selection is choosing correctly and starting nothing, which is a state of the machine rather than of the queue: {why}",
		KindWatchResumed:             "Selection resumes where it left off, from a queue read fresh rather than remembered: {why}",
		KindWatchStopped:             "The selection loop is closed; every run it started was waited out rather than abandoned: {why}",
		KindWatchRedeploying:         "The selection loop closes and restarts on the build deployed over it; every run it started was waited out rather than abandoned: {why}",
		KindWatchReadRetrying:        "Selection could not read the harness's store at this poll and is reading it again, with no provider call spent on it: {why}",
		KindLineWaiting:              "Selection has chosen nothing for {age}: {stopped}, with {ready} a run could have been started for behind it and {publications}. Quiet nobody chose is the failure mode this exists to say out loud.\n\n{standing}",
		KindStallNoticed:             "Selection has started nothing for {age} over {ready} ready: {cause}. The failure this watches for is the one where the process that would have said why is the process that died. {stopped}.\n\n{standing}",
		KindProviderWindow:           "{stopped}. Selection has chosen nothing for {age}: the loop is alive and reading the queue, and what it would start with is what the provider will not serve yet.\n\n{standing}",
		KindCapacityHold:             "{stopped}. Selection has started nothing for {age}: every role depends on capacity the provider is refusing, and a system whose every role shares one capacity dependency stops whole.\n\n{standing}",
		KindProviderOutage:           "{stopped}. Every role shares one provider and one login, so the system waits whole — correctly, this time: the runs hold their state, no budget is spent, and no brake mistakes the wait for a failure storm.\n\n{standing}",
		KindProviderRestored:         "The provider is answering again after {age}: {stopped}. The system resumed as designed, from where each run held.",
		KindRecurringTaskFailing:     "{stopped}. A scheduled role that never receives its turn is a watcher that has stopped watching, and nothing on a clock restarts it: every firing meets the same refusal. It has stood for {age}.",
		KindClaimReleased:            "Selection had been passing over {item} for {age} because the tracker called it claimed, and nothing was working on it: {stopped}. An item that leaves the ready queue and never comes back is the failure a stall reading structurally cannot see, which is why the claims are audited against the runs.",
		KindResidentStale:            "Selection is running a build made at {commit}, before {behind} landed. A process that outlives the deploys it is supposed to be running is the supervision gap; the session closes it itself, between the runs it is carrying, once a build is installed over it.",
		KindBundleImprovement:        "{improvement}. A project that never hears its template moved is one whose configuration drifts by neglect rather than by decision; saying {setting} once makes the difference visible without deciding it for anybody.",
		KindBundleImprovements:       "{improvement}. A project that never hears its template moved is one whose configuration drifts by neglect rather than by decision; naming them once, in one message, makes the difference visible without deciding it for anybody, and `yoyo config drift` shows what each one was and is.",
		KindCatchUpDigest:            "{events} went unsaid here over {age}. A surface that replayed all of them would carry less than this line does; the record is the full account either way.",
		KindLogLineSkipped:           "The {log} log holds a line that fails to decode, {line}: {cause}. The reader keeps its place and carries on, which is the designed failure; the line itself is what somebody looks at.",
	},
}

// programManagerVoice is a program manager's: an instance watching one outcome
// across the line, so what it says about an event is what the event adds to the
// pattern it is watching rather than what the event is.
var programManagerVoice = voice{
	title:  "Program Manager",
	avatar: ":telescope:",
	lines: map[Kind]string{
		KindItemAdmitted:             "Admitted under {goal}: {title}. One more item in the queue is one more thing the line has to carry, and I count it.",
		KindItemDecomposed:           "Split out of a larger item: {title}. Smaller pieces are easier to see stall, which is what I watch for.",
		KindItemAttributed:           "{item} now says it serves {goal}. Work that says what it is for is work whose progress I can read.",
		KindItemReprioritized:        "{item} moved to {priority}; the order the line pulls in has changed, and so has what it will reach first.",
		KindTrackerBlockRefused:      "The {asking} asked for {refused} and the whole block was refused, so nothing in it happened: {why}",
		KindTrackerRefusalUnresolved: "The {asking}'s refused block was not put right on the turn after — {cause} — so {refused} stays undone, and that is the kind of loss I track across passes: {why}",
		KindWorkApproved:             "Approved and admitted under {goal}: {title}. It enters the line now, and I will see whether it moves.",
		KindWorkDeclined:             "{title} was declined, which leaves the line exactly as loaded as it was: {why}",
		KindWorkHandedOff:            "{item} is carried by {executor} rather than a run, so its progress shows up in a conversation rather than in the landings: {why}",
		KindWorkPickedUp:             "{title} is being worked in conversation now. Its progress is a decision recorded rather than a change landed.",
		KindWorkCarriedOut:           "{item} is carried out, and the record of it is what the conversation settled: {why}",
		KindCapCrossed:               "{item} now has a {budget} cap of {cap}, on {crossing}. One crossing is a decision; the same cap crossed again and again is a pattern worth somebody's attention: {why}",
		KindRunStarted:               "{item} started as {run}, on account {account} at configuration {config}, chosen by {by}: {reason}. The line is moving here.",
		KindChecksPassed:             "{item} passed its checks in {checks}, one step nearer landing. A stage creeping toward its bound across items is a cost I watch.",
		KindChecksFailed:             "{item} failed its checks: {command} exited {exit}. One failure is the gate working; the same command failing across items is a cause somebody should look for.",
		KindPathRefused:              "{item} was refused for touching {paths}, which the item does not grant — it grants {grants}. The fix is in the item rather than the run: a line reading {grant} decides the exception before the run starts.",
		KindReviewApproved:           "{item} was approved, so it is on its way to landing.",
		KindReviewRepairs:            "{item} went back to its developer with {findings}. Repairs are normal; the same item going round repeatedly is what I note.\n\n{requested}",
		KindRaceLost:                 "{item} lost its race for {branch} to other work landing, and is being replayed, checked, and reviewed again. That is {races}; a lost race is the cost of a busy branch, not a stall.",
		KindPromoted:                 "{item} landed on {branch} at {commit}, promoted by the harness. That is throughput, and it counts.",
		KindLandingGreen:             "{item} landed green: {landing}. Throughput that the whole suite agrees with.",
		KindLandingRed:               "{item} landed red: {landing}. One red landing is news about the branch; red landings recurring on the same check are a cause somebody should look for.",
		KindLandingUnverified:        "{item} landed without its whole suite run: {landing}. An unverified landing is a gap in the picture, and I count those too.",
		KindPublished:                "{item} is published as {pr}, and the local branch stays the one that counts.",
		KindMergeQueued:              "{pr} is queued at the forge; whether it merges is the forge's to settle now.",
		KindMergeCompleted:           "{pr} merged, and the forge agrees with the local target again.",
		KindMergeDropped:             "{pr} was dropped instead of merged: {cause}. The local target has the change and the forge does not, and that gap stays open until somebody closes it.",
		KindMergeWaitingOnTarget:     "{pr} waits on the target's red check: {cause}. One red check on the target holds every merge queued behind it until the filed item lands.",
		KindRunParked:                "{item} is paused part-way, waiting on {cause}. A pause is not a stall, and I read them apart.",
		KindRunContinued:             "{item} picked up again from where it paused.",
		KindBlockerRecorded:          "{item} is blocked, and {remains}. A blocker says where the line is stuck, which is what I report on: {text}",
		KindRunEnded:                 "The run on {item} {ending}, and {remains}. How a run ended is one data point in the lane's picture: {text}",
		KindUsageLimitExhausted:      "{waiting} hit the account's limit, which is capacity rather than a defect: {cause}. Time the line spends waiting on capacity is time I account for separately.",
		KindModelSubstituted:         "{waiting} was answered by {servedby} instead of {model}, the stated alternative: {cause}. It is recorded per turn, and {model} is asked again as soon as it can serve.",
		KindReportFiled:              "Noticed while watching the line, about {item}: {text}",
		KindProposalRaised:           "{artifact} should change, and here is why: {text}",
		KindExchangeTurn:             "{exchange}, {rounds}, on a question about the line: {text}",
		KindExchangeClosed:           "{exchange} closed {outcome}; the question it asked is answered.",
		KindDirectiveRecorded:        "This came in through a thread, and it carries the same force as one typed at a terminal: {text} — {effect}",
		KindDirectiveResolved:        "Settled: {text}. Whatever it paused carries on from where it stood.",
		KindDirectiveCarriedOut:      "Carried out: {text}. It paused nothing, and its outcome is now on the record.",
		KindDirectiveRefused:         "That reply was not taken as a directive, rather than one being guessed from it: {why}",
		KindDirectiveWithdrawn:       "Withdrawn, and kept on the record, so what happened while it stood still makes sense: {text}",
		KindQuestionHeard:            "That was a question, not a directive, so nothing is recorded from it; the Lead Product Manager answers it here.",
		KindIntakeHeld:               "Intake is held: nothing new is chosen, and what is running carries on. How long the line stays held is part of what I watch: {why}{stops}{lifts}",
		KindIntakeReleased:           "Intake is released, {released}, and the line can choose work again.",
		KindIntakeEscalated:          "The brake went round its summons-and-probe loop to the bound, and the harness put the hold in front of the operator, as it is built to: {why}",
		KindOperatorAction:           "Something in my lane waits on the operator's own hand rather than on any role: {needs} Found by {foundby}; recorded in {recordedin}. It is said to you once, and {ends}.",
		KindHoldPlaced:               "Everything is held at the next provider call; nothing in flight is thrown away.",
		KindHoldLifted:               "The hold is off, and each held run continues from its own record.",
		KindWatchStarted:             "The line is being watched in a loop now, reading the queue fresh every time: {why}",
		KindWatchIdle:                "The line read the queue and started nothing this time, at the cost of a tracker read: {why}",
		KindWatchBraked:              "The line is stopped by the intake hold, which is the hold doing its job: {why}",
		KindWatchBlocked:             "The machine refused every start, and the queued work will be retried at the next poll: {why}",
		KindWatchResumed:             "The line is choosing again, from a fresh read of the queue: {why}",
		KindWatchStopped:             "The loop watching the line has closed, after waiting out every run it started: {why}",
		KindWatchRedeploying:         "The loop is closing to restart on the build installed over it, after waiting out every run it started: {why}",
		KindWatchReadRetrying:        "The line could not read the harness's store at this poll and is trying again, with no provider call spent: {why}",
		KindLineWaiting:              "The line has chosen nothing for {age}: {stopped}, with {ready} a run could start on behind it and {publications}. A quiet line with a reason is a line I can report on.\n\n{standing}",
		KindStallNoticed:             "The line has started nothing for {age} over {ready} ready: {cause}. A stall is the outcome I exist to notice, and the process that would explain it may be the one that stopped. {stopped}.\n\n{standing}",
		KindProviderWindow:           "{stopped}. The line has chosen nothing for {age}: it is running and reading the queue, and what it would start next is what the provider will not serve yet.\n\n{standing}",
		KindCapacityHold:             "{stopped}. The line has started nothing for {age}: every role needs capacity the provider is refusing, so the whole line waits together.\n\n{standing}",
		KindProviderOutage:           "{stopped}. Every role uses one provider and one login, so the line waits as a whole — the runs keep their state, no budget is spent, and no brake mistakes this for failures.\n\n{standing}",
		KindProviderRestored:         "The provider is answering again after {age}: {stopped}. The line picked up from where each run was held.",
		KindRecurringTaskFailing:     "{stopped}. A scheduled pass that never reaches its role is a watcher that has stopped watching, and waiting will not restart it: every firing meets the same refusal. It has stood for {age}.",
		KindClaimReleased:            "The line had been skipping {item} for {age} because the tracker said it was claimed, and nothing was working on it: {stopped}. An item stuck out of the queue that way is invisible to a stall reading, which is why the claims are checked against the runs.",
		KindResidentStale:            "The line is running a build made at {commit}, from before {behind} landed. The session takes up a newer build by itself between runs once one is installed.",
		KindBundleImprovement:        "{improvement}. The template this project started from has moved; saying {setting} once lets the project decide rather than drift.",
		KindBundleImprovements:       "{improvement}. The template this project started from has moved in several places; naming them together, once, lets the project decide, and `yoyo config drift` shows each old and new value.",
		KindCatchUpDigest:            "{events} were not posted here over {age}. The record holds all of them; this line is the count.",
		KindLogLineSkipped:           "Line {line} of the {log} log does not decode: {cause}. The reader skipped it and kept going; the line itself is what somebody should look at.",
	},
}

// voices is every speaker there is, keyed exactly as an envelope names one.
var voices = map[string]voice{
	HarnessSpeaker:                        harnessVoice,
	string(domain.RoleDeveloper):          developerVoice,
	string(domain.RoleReviewer):           reviewerVoice,
	string(domain.RoleDevelopmentManager): developmentManagerVoice,
	string(domain.RoleProductManager):     productManagerVoice,
	string(domain.RoleArchitect):          architectVoice,
	string(domain.RoleProgramManager):     programManagerVoice,
}

// Whose move it is, once this has been said.
//
// A thread is a narrative and a narrative goes quiet — a run takes an hour, a
// handoff waits on a role opening a conversation, an item sits in the queue for
// a night. The silence after the last message is the problem: it reads the same
// whether somebody is working, somebody is waiting to be asked, or nobody at all
// holds the ball. An operator reading a thread the morning after should never
// have to reconstruct which of those it was.
//
// So every message says what follows it and whose it is to make. It is one table
// keyed by the kind rather than a clause per persona, for the same reason the
// severity mark is: whose move follows a promotion is a fact about the state of
// the work, identical whoever is narrating it, and six paraphrases of one fact
// are six chances to state it differently. The persona's own line is still the
// whole of what the persona says.
//
// It is on every message rather than only on the ones that look final, because
// which message turns out to be a thread's last is not knowable when it is
// written: a run that dies, a sink that stops, a role that never picks the work
// up. A guarantee that held only for the messages somebody predicted would be
// last is not the guarantee.
const nextMoveLead = " Next: "

var nextMoves = map[Kind]string{
	KindWatchBlocked: "the harness's — starts are retried at the next poll once the refusing condition clears.",
	// Work sitting in the backlog. What follows is the harness choosing it, which
	// is the one move in this whole table that happens without anybody deciding
	// anything.
	KindItemAdmitted:      "the harness's, when this reaches the top of the queue and a run is free.",
	KindItemDecomposed:    "the harness's, when this reaches the top of the queue and a run is free.",
	KindItemAttributed:    "the harness's, when this reaches the top of the queue and a run is free.",
	KindItemReprioritized: "the harness's, and this is where it now gets pulled from.",
	// A refused block is the one item here whose move belongs to the harness and
	// then to the role that asked. The harness wakes that role's own conversation
	// with the refusal in it, once, and the actions come back only if the role
	// issues them again — so what a reader has to know is that a turn is coming
	// without anybody starting it, and that it is the role rather than the wakeup
	// that puts the queue back.
	KindTrackerBlockRefused: "the harness's, then the role that asked — a turn is started for it with the refusal in it, and the actions happen only if the role issues them again.",
	// And once the turn after a refusal has failed to put the actions back — a
	// second block refused, or a turn that sent none at all — the move stops being
	// anybody's inside the harness. The actions are still lost, the role has had a
	// turn to re-issue them, and nothing further is scheduled. The clause
	// deliberately says nothing about which of those endings it was, or about
	// whether the harness woke the turn: they reach the same place, and which one
	// it was is the message's own to say.
	KindTrackerRefusalUnresolved: "the operator's — the harness has stopped trying to have these re-issued and will not start another turn for them.",
	KindWorkApproved:             "the harness's, when this reaches the top of the queue and a run is free.",
	KindWorkDeclined:             "nobody's — nothing was created, and nothing follows.",
	// Work a conversation carries. The handoff is the one state where the thread
	// waits on a person opening a conversation rather than on anything the harness
	// will do by itself, which is exactly the silence this exists to name.
	// The clause here is the one an item's marker does not name a role for, which
	// is work marked before it could. Where the marker names one, handedOffMove
	// says whose it is instead: the wait between the handoff and the pickup is the
	// longest silence in any thread, and the role holding the item is the whole of
	// what a reader wants from it.
	KindWorkHandedOff:  "the role that carries it, in conversation — no run will ever be started for this.",
	KindWorkPickedUp:   "the role carrying it, until the work is done and the item closed.",
	KindWorkCarriedOut: "nobody's — the item is done.",
	// A crossing is already in force, so nobody has to do anything for it to take
	// effect — which is exactly why the clause names the operator anyway. The
	// delegation was granted on the condition that they could still say no, and a
	// message that said nobody's move followed would be telling the reader they had
	// nothing to do about the one kind of message they were promised a say in.
	KindCapCrossed: "the operator's, only if they disagree — the cap is crossed already, and the decision it makes recordable is the development manager's to record next.",
	// One run's own arc. Each of these is followed by the next by itself, so what
	// they say is who is working rather than who is being waited on.
	KindRunStarted:     "the developer's, until the checks say otherwise.",
	KindChecksPassed:   "the reviewer's — a verdict on the change.",
	KindChecksFailed:   "the developer's — another attempt at the same item.",
	KindPathRefused:    "the developer's — another attempt at the same item, with the refused paths taken back out.",
	KindReviewApproved: "the harness's — the promotion onto the target branch.",
	KindReviewRepairs:  "the developer's — the findings as written.",
	// A lost race is followed by the harness's own replay and the gate it
	// re-earns, so nobody is waited on and nothing is docketed for it.
	KindRaceLost:       "the harness's — the replayed change goes through the checks and a fresh review, and lands if they pass.",
	KindPromoted:       "the harness's — publishing the change where the product publishes.",
	KindPublished:      "the forge's, until the request merges.",
	KindMergeQueued:    "the forge's, until it settles.",
	KindMergeCompleted: "nobody's — the item is done.",
	// A dropped merge is the one crossing in this stretch whose move is a
	// person's. The forge is done with it — it refused the merge or gave up on
	// one it had queued, and asking again earns the same answer — so nothing
	// happens to the publication until somebody makes it happen.
	KindMergeDropped: "the operator's — the forge will not merge this by itself, and the publication stands until somebody settles it.",
	// A merge waiting on its target's red check is the harness's: the failure is
	// filed as the target's, and the harness takes the merge up once that closes.
	KindMergeWaitingOnTarget: "the harness's, on the item filed for the target's red check — it takes the merge up again once that closes, and nobody has anything to decide.",
	// A landing is after the run: green asks nothing of anybody, red has filed
	// the work that answers it and that work is queued like any other, and one
	// the checks could not run over is the operator's to look at, because
	// nothing else will run them again.
	KindLandingGreen:      "nobody's — the target branch is as green as the landing checks can say.",
	KindLandingRed:        "the harness's, on the item the landing filed — it is queued at the front, and every run until it lands is cut from a red base.",
	KindLandingUnverified: "the operator's — nothing will run the landing checks over this commit again by itself.",
	KindRunParked:         "whatever it is waiting on; the run resumes from its own record once that clears.",
	KindRunContinued:      "the developer's, from where the change stopped.",
	// Work that stopped and stayed stopped, and capacity that ran out. Neither
	// clears on its own, which is why naming who has to act on it is the whole of
	// what a reader needs.
	KindBlockerRecorded: "the development manager's, in triage — nothing moves this item until it is decided.",
	// A run that ended with no blocker left nobody a decision, so nothing is
	// waiting on a person and the item is queued exactly as it was. The clause
	// says so rather than naming a move: sending a reader to triage over a run
	// that recorded nothing to triage is the same guessing as saying nothing.
	KindRunEnded: "the harness's — nothing was recorded for anybody to decide, and the item is where the run left it.",
	// The clause deliberately says nothing about when. Whether the provider named a
	// moment the capacity comes back is the message's own to say, and a whose-move
	// clause that implied one would be the sink inventing the fact the record was
	// careful not to claim.
	KindUsageLimitExhausted: "the provider's — nothing here moves while the limit stands.",
	// A substitution asks for nothing, which is the whole of what makes it worth
	// saying without interrupting anybody: the work carried on, and the clause
	// says so rather than sending a reader to look at a window that is lifting on
	// the provider's clock either way.
	KindModelSubstituted: "nobody's — the turn was served, and the model it asked for is asked for again as soon as it can be.",
	// What an agent said in its own words. A report asks for nothing by design and
	// says so; the other three are all waiting on the operator.
	KindReportFiled:    "nobody's — the work carried on.",
	KindProposalRaised: "the operator's — nothing reaches the document until they decide it.",
	KindExchangeTurn:   "the operator's, until the exchange is answered.",
	KindExchangeClosed: "back to the work the exchange was holding.",
	// What a reply did. The recorded clause is the pausing one, because a
	// directive that stopped work is the one a reader has to do something about;
	// directiveInForceMove is the other case, and nextMove chooses between them
	// from what the record left unsettled.
	KindDirectiveRecorded: "the operator's — the work this affects waits until the directive is resolved.",
	KindDirectiveResolved: "the harness's — the work this held carries on from where it stopped.",
	// A directive that has been carried out is over, and it was holding nothing
	// up while it stood. Naming somebody's move would invent a wait where the
	// answer somebody was owed has just arrived.
	KindDirectiveCarriedOut: "nobody's — what was asked for is done, and nothing was waiting on it.",
	KindDirectiveRefused:    "the operator's — nothing was recorded, so nothing about the work has changed.",
	// A withdrawn directive is over, and nobody has to do anything for that to be
	// so: one that paused work stops holding it, and one that paused nothing was
	// holding nothing. The clause covers both rather than naming a wait, because
	// the person reading it has just been told the thing they asked about was
	// taken back.
	KindDirectiveWithdrawn: "nobody's — the directive no longer applies, and any work it was holding carries on from where it stopped.",
	// A question is answered rather than recorded, and the answer is the product
	// manager's: it is carried to her conversation the moment the receipt is
	// posted, and her reply lands in the same thread. Nothing about the work
	// waits on it, and the person who asked has nothing to do but read.
	KindQuestionHeard: "the Lead Product Manager's — the answer follows in this thread.",
	// The operator's switches and the session that chooses work. These are about
	// the whole line rather than one item, and every one of them is waiting on
	// somebody by name.
	KindIntakeHeld:     "the operator's — nothing new is chosen until `yoyo release` lifts it.",
	KindIntakeReleased: "the harness's — the backlog is being pulled from again.",
	// The harness's own escalation: the account above it already says why in the
	// hold's own words, so this says only what follows.
	KindIntakeEscalated: "the operator's — the harness has stopped probing, and nothing new is chosen until `yoyo release` lifts it.",
	// A finding only the operator can act on. The message carries the read
	// model's own wording of whose move it is and what ends it in Mover, so the
	// terminal's attention line and this clause are one wording; this is what a
	// record that carried none would say.
	KindOperatorAction: "the operator's — only a person can act on this, and `yoyo status` names it until the record says it is done.",
	KindHoldPlaced:     "the operator's — nothing runs until the hold is lifted.",
	KindHoldLifted:     "the harness's — every run that stopped for the hold carries on from its own record.",
	KindWatchStarted:   "the harness's — the queue is pulled from until somebody stops it.",
	// The idle poll with nothing else to say: the queue was read, no run is going,
	// and nothing passed over is a person's to carry. Admitting ready work is then
	// genuinely the act that changes the answer, which is the only case this clause
	// is said in — see idleMove, which answers for the three states it used to be
	// said over wrongly before it reaches this.
	KindWatchIdle:    "the Lead Product Manager's — nothing is chosen until work that is ready is admitted.",
	KindWatchBraked:  "the operator's — choosing resumes when intake is released.",
	KindWatchResumed: "the harness's — work is being chosen again.",
	KindWatchStopped: "the operator's — nothing more is chosen until a session is started again.",
	// The one stop nobody has to answer. The session waited out its runs and is
	// being re-executed into the build deployed over it, so telling the reader to
	// start a session would be handing them a move they do not have — once per
	// deploy, which is exactly the standing chore self-redeployment removes.
	KindWatchRedeploying: "nobody's — the session is coming back on the build that was deployed, and the queue is read again when it does.",
	// A read of the store that failed is the harness's to retry, and it retries
	// it itself: nothing a person admits, releases, or opens reaches a store that
	// will not answer, and a session that gives up on it records a stop that is
	// said as one.
	KindWatchReadRetrying: "the harness's — the queue could not be read, and it is read again until it answers or the session gives up on it.",
	// The line carries the read model's own wording of whose move it is in Mover,
	// so this is what a line that carried none would say.
	KindLineWaiting: "the operator's — this stands until somebody clears what stopped it.",
	// A stall names the machine rather than the state, because there is no state:
	// what has to be looked at is the thing that chooses work, and whether it is
	// dead or merely wedged is in the message above this clause.
	KindStallNoticed: "the operator's — nothing starts until whatever chooses work is looked at, and started again if it has died.",
	// The same silence as the stall above, and nobody's move. Naming one would be
	// sending somebody to look at a machine that is behaving, which is exactly
	// what the alarm this replaces did on 2026-09-05.
	KindProviderWindow: "nobody's — the window lifts on the provider's clock, and the queue is read again when it does.",
	// Every role held is the one capacity state that is somebody's move. The window
	// is still the provider's; what is the operator's is the configuration that
	// let one window hold every role. The message carries the read model's own
	// wording of that in Mover, so a terminal and a channel name the same move,
	// and this is what a record that carried none would say.
	KindCapacityHold: "the operator's — the window lifts on the provider's clock, and enabling failover on the agents is what would move the work onto another model before it does.",
	// The provider answering nobody is the operator's move in the one way a
	// window is not, and the message carries the read model's own wording of it
	// in Mover — which cause it is decides whether the move is a login or the
	// network — so a terminal and a channel name the same move. This is what a
	// record that carried none would say.
	KindProviderOutage: "the operator's — log in to the provider, or wait for the network; the harness resumes on its own once it answers, and nothing is released or restarted.",
	// The provider answering again is nobody's move: the runs resumed by
	// themselves, which is the whole of what the message is for.
	KindProviderRestored: "nobody's — the line carried on by itself, and nothing was released or restarted to make it.",
	// A task failing before its first turn is the harness's or the operator's by
	// its cause, and the message carries the read model's own wording of which in
	// Mover, so a terminal and a channel name the same move. This is what a record
	// that carried none would say.
	KindRecurringTaskFailing: "the harness's or the operator's, by its cause — fix what refuses the firing; the first firing that takes a turn clears it.",
	// Nobody's move, and the message is still worth sending. The item is back in
	// the queue, the run that left it is ended, and the slot it was filling is
	// free — so the state this reports is one the harness has already put right,
	// and a clause naming a chore would be inventing one. What it is for is that
	// the line was quietly degraded until it was, and a second run for an item is
	// something a reader has to be able to account for afterwards.
	KindClaimReleased: "nobody's — the item is pullable again and will be chosen in its turn, and whatever the run that left it produced is still on its branch.",
	// The restart is no longer anybody's: a watch session takes up a build
	// installed over it by itself, between the runs it is carrying and without
	// interrupting one. What is left is the install, which is why this names it
	// rather than naming a restart nobody has to make.
	KindResidentStale: "the operator's — installing the build is the whole of it; the session takes it up itself between runs.",
	// Nobody's, and saying so is the point. Every other move in this table is one
	// somebody eventually has to make; this one is an offer an operator is
	// entitled to decline forever, and a message that implied otherwise would be
	// the nagging that gets a channel muted.
	KindBundleImprovement:  "nobody's — the value stands as this project has it until somebody decides otherwise, and nothing will ask again.",
	KindBundleImprovements: "nobody's — every one of them stands as this project has it until somebody decides otherwise, and nothing will ask again.",
	KindCatchUpDigest:      "nobody's — the record holds all of it, and the thread carries on from here.",
	// A line nothing can read is the operator's to look at and nobody's to wait
	// on: the reader has already stepped past it, so nothing after it is held up,
	// and what the line held is recoverable only by somebody opening the file.
	KindLogLineSkipped: "the operator's, only to look at the line — nothing after it is waiting, and nothing here will read it again.",
}

// directiveInForceMove is whose move follows a directive that stopped nothing.
// An operational directive takes effect the moment it is recorded, so there is
// nobody to wait for — and a thread that told a reader to wait for a resolution
// would be naming a move nobody has to make, on the ordinary case rather than
// the rare one.
//
// It says the work carries on rather than that the directive is in force, which
// is the operator's rule about every message here: the everyday word, not the
// term of art, wherever a reader would have to already know the vocabulary. That
// it applies from now on is the account's own to say, and saying it twice in one
// message — once as what was recorded, once as what follows — is the padding an
// acknowledgment reads worst as.
const directiveInForceMove = "the harness's — the work carries on under it."

// unstartedMove is whose move follows a run that died before it claimed
// anything. The table's answer for a run that ended is that nothing was recorded
// for anybody to decide, and this is the one ending where something was: the
// death is docketed as it happens, so the item is where it always was and the
// dispatch is a decision waiting on the development manager.
//
// That clause is the whole reason this class was worth recording. The message an
// operator read on 2026-09-07, twenty-nine times over, said nothing was recorded
// for anybody to decide — which was true, and was the defect.
const unstartedMove = "the development manager's, in triage — the run took nothing, and the dispatch that could not start it is on the docket."

// nextMove is whose move follows one event, and says whether anything does. A
// kind nothing answers for is a kind added to the vocabulary without anybody
// deciding what a reader is supposed to do about it, which is a mistake in this
// table rather than in any record — so it is refused the way a missing voice line
// is, rather than posted as a message that leaves the reader exactly where this
// exists to stop leaving them.
func nextMove(event Event) (string, bool) {
	// Work already marked for a conversation is not queued for a run and never
	// will be, so the queue's answer would be telling a reader to expect something
	// that cannot come. The handoff's answer is the true one, whether the marker
	// arrived with the admission or afterwards.
	if strings.TrimSpace(event.Detail.Executor) != "" {
		switch event.Kind {
		case KindItemAdmitted, KindItemDecomposed, KindItemAttributed, KindItemReprioritized, KindWorkHandedOff:
			return handedOffMove(event.Detail.Executor), true
		}
	}
	// A recorded directive that left nothing unsettled is in force already rather
	// than holding anything up, and the two are opposite answers to the question
	// this clause exists to answer.
	if event.Kind == KindDirectiveRecorded && strings.TrimSpace(event.Detail.Unresolved) == "" {
		return directiveInForceMove, true
	}
	// A watch that started nothing idles for opposite reasons, and the fixed clause
	// answered for one of them.
	if event.Kind == KindWatchIdle {
		return idleMove(event.Detail), true
	}
	// A stall over a queue whose last poll said what was holding it is waiting on
	// whoever releases that, rather than on somebody restarting a chooser that is
	// running and doing exactly what it should. A run that died before it claimed
	// anything is docketed as it dies, where every other ending under that kind
	// recorded nothing for anybody to decide. An approved change the environment
	// stopped is the harness's to resume by a verb, under either ending kind,
	// where the table says a decision or nothing. Each is one kind of message
	// covering two situations that send a reader to different people, so the
	// clause is the read model's — derived beside the fact the message states, so
	// the two cannot disagree. The waiting line is the same shape over the
	// intake hold: the brake's is the development manager's while she decides
	// and the operator's once she has escalated it, and the line is repeated to
	// the operator exactly when it is his.
	if strings.TrimSpace(event.Detail.Mover) != "" {
		switch event.Kind {
		case KindStallNoticed, KindRunEnded, KindBlockerRecorded, KindCapacityHold, KindProviderOutage, KindRecurringTaskFailing, KindWatchBraked, KindIntakeHeld, KindLineWaiting, KindOperatorAction:
			return ended(strings.TrimSpace(event.Detail.Mover)), true
		}
	}
	move, ok := nextMoves[event.Kind]
	return move, ok
}

// idleMove is whose move follows a poll that started nothing. It is derived from
// what the session recorded rather than taken from the table, because a watch
// idles for opposite reasons and one fixed clause can only be right about one of
// them.
//
// The clause it replaced named the product manager whatever the session had
// found, and an operator acted on that three times over a queue whose only
// unstarted work was the architect's to carry, while a developer run worked on
// the other slot. Both halves of that were wrong: nothing was waiting on an
// admission, and the line had not stopped. So every state that is not an
// admission answers first — a store that would not answer, the provider's usage
// window, work a conversation carries, runs still going — and the admission
// clause is left to the case where admitting ready work is genuinely what
// changes the answer.
func idleMove(detail Detail) string {
	// A store that will not answer. Nothing a person admits, releases, or opens
	// reaches it, so this answers before anything else: the queue was not read, so
	// what is in it is not what stopped the choosing.
	if detail.Unreadable {
		return "the harness's — the queue could not be read, and it is read again until it answers or the session gives up on it."
	}
	// The provider refusing to serve any more work. Nothing a person admits,
	// releases, or opens is startable while the window stands, so this answers
	// ahead of the admission clause for the same reason the unreadable store does.
	if detail.ProviderWindow {
		return nextMoves[KindProviderWindow]
	}
	// Work only a conversation carries. No admission and no run moves it, and the
	// marker names who does.
	if role := domain.WorkItemExecutor(strings.TrimSpace(detail.Executor)).Role(); role != "" {
		return "the " + role.Title() + "'s, in conversation — the work this poll passed over is carried there, and no run will ever start it."
	}
	// A run in flight is the harness working. Naming anybody's move over it would
	// be reporting a stall while the chain moves, which is what sent an operator to
	// look at a line that was not stopped.
	if detail.Running > 0 {
		return "nobody's — the runs in flight carry on, and the queue is read again as each of them finishes."
	}
	return nextMoves[KindWatchIdle]
}

// handedOffMove is whose move follows work only a conversation will carry. It
// names the role the marker names, because nothing else in that stretch of the
// thread does: the handoff is followed by however long it takes somebody to open
// the conversation, and until the pickup says who started, this clause is the
// only thing standing between a reader and an unattributed silence.
//
// A marker that names no role falls back to the clause that says a role carries
// it without saying which. That is what the record holds, and a thread that
// named a role the marker did not would send the operator to the wrong one.
func handedOffMove(executor string) string {
	if role := domain.WorkItemExecutor(strings.TrimSpace(executor)).Role(); role != "" {
		return "the " + role.Title() + "'s, in conversation — no run will ever be started for this."
	}
	return nextMoves[KindWorkHandedOff]
}

// The words each severity is said in, and the decoration that is added to them.
// The words come first and carry the whole meaning: a reader whose client will
// not render the emoji, or who is reading this piped into a file, still cannot
// mistake a critical report for a note. A note is the baseline and is marked
// with nothing, because marking everything marks nothing.
const (
	criticalMark = ":rotating_light: Critical — "
	warningMark  = ":warning: Warning — "
)

func severityMark(severity report.Severity) string {
	switch severity {
	case report.SeverityCritical:
		return criticalMark
	case report.SeverityWarning:
		return warningMark
	default:
		return ""
	}
}

// MaxBodyBytes bounds one message body. A body over it is cut with a marker
// naming the durable record that holds the whole, rather than split into a flood
// of messages to fit: a thread is a narrative, and the record is authoritative
// anyway.
const MaxBodyBytes = 2900

const bodyCutNote = "… [cut; the whole is in %s]"

// Render says one event in one persona's voice, addressed to one topic. It is
// the whole of what this package decides, and it is deterministic: the same
// topic, speaker, and event always produce the same message.
func Render(topic Topic, speaker Speaker, event Event) (Message, error) {
	var problems []error
	if err := topic.Validate(); err != nil {
		problems = append(problems, err)
	}
	if err := speaker.Validate(); err != nil {
		problems = append(problems, err)
	}
	if err := event.Validate(); err != nil {
		problems = append(problems, err)
	}
	if err := errors.Join(problems...); err != nil {
		return Message{}, fmt.Errorf("render %s: %w", event.Kind, err)
	}
	spoken, ok := voices[speaker.Key()]
	if !ok {
		return Message{}, fmt.Errorf("render %s: no voice for speaker %q", event.Kind, speaker.Key())
	}
	line, ok := spoken.lines[event.Kind]
	if !ok {
		return Message{}, fmt.Errorf("render %s: the %s has no line for it", event.Kind, speaker.Key())
	}
	said, err := substitute(line, event.fields(topic))
	if err != nil {
		return Message{}, fmt.Errorf("render %s as the %s: %w", event.Kind, speaker.Key(), err)
	}
	move, ok := nextMove(event)
	if !ok {
		return Message{}, fmt.Errorf("render %s: nothing says who it waits on next", event.Kind)
	}
	message := Message{
		SchemaVersion: SchemaVersion,
		Kind:          event.Kind,
		Topic:         topic.Key(),
		// The title is bounded here as well as where a producer sets it, so what
		// the envelope carries is a header line whichever way the topic was built.
		TopicTitle: boundTitle(topic.Title),
		Speaker:    speaker.Key(),
		Identity:   speaker.Identity(),
		Severity:   event.Severity,
		Reach:      reachOf(topic, event),
		Body:       bound(ended(severityMark(event.Severity)+said), nextMoveLead+move, event.Refs),
		Refs:       event.Refs,
		At:         event.At.UTC(),
	}
	if err := message.Validate(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// fields is every placeholder a voice line may use, filled from the event and
// the topic it is addressed to. Every one of them always has a value: where the
// record holds nothing, what is substituted states the absence rather than
// leaving a blank, because a sentence with a hole in it reads as a bug and a
// record that is missing something is still worth being told about.
//
// The two reasons are separate placeholders because they are separate facts and
// their absences mean opposite things: an unaccounted run is what recording a
// selection exists to expose, while an operator who holds intake in a hurry owes
// nobody an explanation.
func (e Event) fields(topic Topic) map[string]string {
	detail := e.Detail
	return map[string]string{
		"item":      stated(itemOf(e.Refs, topic, detail), "an unnamed work item"),
		"run":       stated(e.Refs.RunID, "an unrecorded run"),
		"by":        stated(detail.SelectedBy, "nobody the record names"),
		"account":   stated(detail.Account, "an account the record does not name"),
		"config":    stated(detail.Configuration, "a configuration the record does not name"),
		"reason":    stated(detail.SelectionReason, "no reason recorded"),
		"command":   stated(detail.Command, "a check the record does not name"),
		"exit":      strconv.Itoa(detail.ExitCode),
		"paths":     pathsOf(detail.RefusedPaths, detail.OmittedPaths),
		"grants":    grantsOf(detail.Grants),
		"grant":     grantLineOf(detail.RefusedPaths),
		"checks":    stated(detail.Checks, "a spend the record does not carry"),
		"landing":   stated(detail.Landing, "a landing the record does not describe"),
		"findings":  countOf(detail.Findings, "finding", "findings", "findings the record does not count"),
		"requested": requestedOf(detail.Requested),
		"branch":    stated(detail.TargetBranch, "an unnamed branch"),
		"races":     countOf(detail.Races, "lost race", "lost races", "a number of lost races the record does not carry"),
		"commit":    stated(shortCommit(detail.Commit), "an unrecorded commit"),
		"pr":        stated(detail.PullRequest, "an unrecorded pull request"),
		"cause":     stated(detail.Cause, "something the record does not name"),
		"waiting":   stated(detail.Waiting, "something the record does not name"),
		"model":     stated(detail.Model, "a model the record does not name"),
		"servedby":  stated(detail.ServedBy, "a model the record does not name"),
		"rounds":    roundsOf(detail),
		"why":       stated(detail.Reason, "no reason given"),
		// What the brake counted, as a clause after the cause, or nothing at all
		// on a hold that counted nothing: the operator's own hold names no runs,
		// and a sentence saying so would be padding on the message he wrote.
		"stops": stopsOf(detail.Stops),
		// The verb that lifts a brake's hold, said on the message that reaches the
		// operator directly: whoever is working the hold, he can lift it sooner,
		// and the next-move clause names whoever is working it rather than him.
		"lifts": liftsOf(e.Kind, detail),
		// Who lifted a hold. A release from before releases were recorded names
		// nobody, and says so rather than naming the operator by default.
		"released": stated(detail.Reason, "released by somebody the record does not name"),
		// A finding for the operator: what he has to do, who found it, and where
		// it is recorded. Each states its absence, because a finding with a hole
		// in it is still one he has to be told about.
		"needs":      ended(stated(detail.Needs, "what is needed could not be read from the record")),
		"foundby":    stated(detail.FoundBy, "somebody the record does not name"),
		"recordedin": stated(detail.RecordedIn, "a record this message cannot name"),
		"ends":       stated(detail.Ends, "what ends it is whatever record says it is done"),
		"text":       stated(e.Text, "nothing the record could carry"),
		"artifact":   stated(detail.Artifact, "an unnamed artifact"),
		"receiver":   stated(detail.ReceivedBy, "a role the record does not name"),
		"effect":     effectOf(detail),
		"outcome":    outcomeOf(detail),
		"exchange":   stated(exchangeOf(e.Refs, topic), "an unnamed exchange"),
		"title":      stated(itemOf(e.Refs, topic, detail), "a title the record does not carry"),
		"goal":       stated(detail.Goal, "no goal the record names"),
		// Two things a voice line could want and deliberately has no placeholder
		// for, both of them identifiers a reader would have to go and resolve.
		//
		// The item a decomposition was created under is the first: the record holds
		// its identifier and nothing that names it in words, so a line reaching for
		// it could only hand a reader a slug — which is the whole of what these
		// messages stopped doing.
		//
		// The directive a message is about is the second. The four directive kinds
		// are the acknowledgment a person gets for something they typed in a
		// thread, so what they are owed is their own words read back and what
		// became of them; the identifier stays in the references, where the
		// delivery pass and anything else tracing the record find it.
		//
		// A line that asks for either is refused as it renders, and every line in
		// every voice is rendered by the tests, so one added later fails there
		// rather than in somebody's channel.
		"priority": priorityOf(detail),
		"executor": stated(carrierOf(detail.Executor), "something the record does not name"),
		// What a refused block asked for, and who asked. The count states an absence
		// rather than reading as none: a block whose actions nobody could count is
		// not a block that asked for nothing, and the two are opposite news.
		"refused": countOf(detail.Refused, "tracker action", "tracker actions", "a number of tracker actions the record does not count"),
		"asking":  stated(detail.Asking, "a role the record does not name"),
		// What became of a run and what remains of its change, both already said in
		// the read model's own words. A record that carries neither says so rather
		// than leaving a blank, for the reason every absence here does — but more
		// pointedly, because a run whose remains nobody can state is exactly the run
		// an operator must not read as one whose work is safe.
		"ending":  stated(detail.Ending, "an ending the record does not name"),
		"remains": stated(detail.Remains, "what remains of it could not be read"),
		"stopped": stated(detail.Stopped, "nothing the record names has stopped it"),
		"age":     ageOf(detail.Since, e.At),
		"ready":   countOf(detail.Ready, "item", "items", "a number of items the record does not carry"),
		// What is waiting on the forge, said at zero as well: a reader scanning a
		// quiet channel is checking that nothing is, and a clause that vanished
		// when the count was nought would leave them unable to tell a line with no
		// outstanding publication from a message written before they were counted.
		"publications": countOf(detail.Outstanding, "promotion awaiting the forge", "promotions awaiting the forge",
			"a number of promotions the record does not carry"),
		"behind": countOf(detail.Behind, "harness change", "harness changes", "a number of harness changes the record does not carry"),
		"events": countOf(detail.Accumulated, "event", "events", "a number of events the record does not carry"),
		// The four lines, already rendered by the read model. A surface with no way
		// to read them says so rather than leaving the block out, because a message
		// that simply lacks the lines is indistinguishable from a harness with
		// nothing in any of them.
		"standing": stated(detail.Standing, "where the harness stands could not be read here"),
		// Which value the template improved, and what the comparison says about it.
		// The sentence is already worded by the derivation that made it, for the
		// reason the four lines above are: one comparison, said one way wherever it
		// is read.
		"setting":     stated(detail.Setting, "a setting the record does not name"),
		"improvement": stated(detail.Improvement, "what the template improved could not be read here"),
		// The delegated cap crossing. The ceiling is said as a number rather than as
		// arithmetic a reader has to do, and the count is said with its bound for the
		// reason every count here is: "crossing 4" tells an operator nothing until
		// they know there are five.
		"budget":   stated(detail.Budget, "a cap the record does not name"),
		"cap":      capOf(detail.Cap),
		"crossing": crossingOf(detail),
		// Which log a line could not be read from, and where in the file it is. The
		// place is said as a line and the byte it starts at, because those are the
		// two things a person opening the file actually uses.
		"log":  stated(detail.Log, "a log the record does not name"),
		"line": lineOf(detail.Line, detail.Offset),
	}
}

// lineOf is where in a file an unreadable line is. Line numbers start at one, so
// a record that carries none says so rather than pointing at line nought.
func lineOf(line int, offset int64) string {
	if line <= 0 {
		return "at a line the record does not number"
	}
	return "line " + strconv.Itoa(line) + " (byte " + strconv.FormatInt(offset, 10) + ")"
}

// stopsOf is the runs a brake counted, as a clause that follows the cause:
// each run with its item and what stopped it. It is empty on a hold that
// counted nothing, so the line reads exactly as it did before the runs were
// recorded.
func stopsOf(stops string) string {
	trimmed := strings.TrimSpace(stops)
	if trimmed == "" {
		return ""
	}
	return "; the runs it counted: " + trimmed
}

// liftsOf is the verb that lifts a brake's hold, as a closing clause. It is
// said only on a brake's hold — the one intake hold whose next-move clause
// names somebody other than the operator — and is empty everywhere else.
func liftsOf(kind Kind, detail Detail) string {
	if kind != KindIntakeHeld || strings.TrimSpace(detail.Mover) == "" {
		return ""
	}
	return ". `yoyo release`, or `/release` in the conversation, lifts it sooner"
}

// capOf is the ceiling a crossing put in force. Zero is a record that did not
// carry one rather than a cap of nothing: a crossing raises a budget by a step,
// so a ceiling of nought is not something the only thing that writes them can
// produce.
func capOf(ceiling int) string {
	if ceiling <= 0 {
		return "a ceiling the record does not carry"
	}
	return strconv.Itoa(ceiling)
}

// pathsOf names the paths a change was refused for, and how many more the
// record's bound dropped from the list. The dropped count is said rather than
// left off because the developer has to take every one of them back out, and a
// list that stopped without saying so would read as the whole of what the gate
// caught. A record carrying no path says so, for the reason every absence here
// does: the gate never refuses on nothing, so a refusal naming nothing is a
// record with something missing rather than a refusal of nothing.
func pathsOf(refused []string, omitted int) string {
	if len(refused) == 0 {
		return "paths the record does not name"
	}
	named := strings.Join(refused, ", ")
	if omitted > 0 {
		named += " and " + countOf(omitted, "further path", "further paths", "") + " not listed here"
	}
	return named
}

// grantsOf says what the work item grants. Nothing is the ordinary answer and
// is said as itself, because the reader is being told what the item admits so
// they can see whether the refusal is a change reaching outside its item or an
// item missing a grant — and "nothing" answers that where a blank would not.
func grantsOf(grants []string) string {
	if len(grants) == 0 {
		return "nothing"
	}
	return strings.Join(grants, ", ")
}

// grantLineOf is the line that would admit the refused paths, worded from the
// marker the gate reads rather than described. A thread that tells somebody
// what to write into an item has to say exactly what the gate will accept, and
// the marker is the one token whose spelling decides that. The paths are
// separated by spaces because that is one of the separators a grant line takes,
// and it is the one that reads as a line rather than as a list.
func grantLineOf(refused []string) string {
	if len(refused) == 0 {
		return "`" + protectedpath.GrantMarker + " <path>`"
	}
	return "`" + protectedpath.GrantMarker + " " + strings.Join(refused, " ") + "`"
}

// crossingOf says which of the crossings a role may take this one was. A record
// that counted the crossing without carrying the bound says so, rather than
// implying the crossing was the last available or the first: how much room is
// left is the operator's whole reason for reading this, and a half-answer is
// worse than an absence they can see.
func crossingOf(detail Detail) string {
	switch {
	case detail.Crossing <= 0:
		return "a crossing the record does not count"
	case detail.Crossings <= 0:
		return "crossing " + strconv.Itoa(detail.Crossing) + " of a bound the record does not carry"
	default:
		return "crossing " + strconv.Itoa(detail.Crossing) + " of " + strconv.Itoa(detail.Crossings)
	}
}

// substitute replaces each {placeholder} with what the record holds for it. A
// placeholder no field fills is a voice line naming something that does not
// exist, which is a mistake in the table rather than in any record, so it is an
// error rather than an empty string somebody would have to notice in a channel.
func substitute(line string, fields map[string]string) (string, error) {
	var built strings.Builder
	rest := line
	for {
		before, after, found := strings.Cut(rest, "{")
		built.WriteString(before)
		if !found {
			return built.String(), nil
		}
		name, remainder, closed := strings.Cut(after, "}")
		if !closed {
			return "", fmt.Errorf("unclosed placeholder in %q", line)
		}
		value, ok := fields[name]
		if !ok {
			return "", fmt.Errorf("no field named %q", name)
		}
		built.WriteString(value)
		rest = remainder
	}
}

// carrierOf is what a message calls the thing carrying an item, from the marker
// the record holds. A marker that names the role is said as that role's
// conversation, which is the whole of what the handoff was missing: a thread
// that names who holds the work is one an operator can read without waiting for
// the pickup to tell them.
//
// The bare marker is said as a role's conversation and no more, because that is
// all it says. Work marked before the marker carried a role is not attributed by
// this, and inventing a role for it would attribute it to the wrong one. A marker
// that is neither is given exactly as it was written, for the reason reading one
// is permissive at all: somebody meant it to be something other than a run.
func carrierOf(executor string) string {
	marker := domain.WorkItemExecutor(strings.TrimSpace(executor))
	if role := marker.Role(); role != "" {
		return "the " + role.Title() + "'s conversation"
	}
	if marker == domain.WorkItemExecutorConversation {
		return "a role's conversation"
	}
	return string(marker)
}

func stated(value, absence string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return absence
}

// itemOf carries the identifier to the outgoing surface's shared resolver.
// Recorded titles cannot supply the current priority and labels. A proposal
// that has not become a tracker item still uses the title its record carries.
func itemOf(refs Refs, topic Topic, detail Detail) string {
	if topic.Kind == TopicWorkItem {
		return topic.ID
	}
	if refs.WorkItemID != "" {
		return refs.WorkItemID
	}
	if named := strings.TrimSpace(detail.Title); named != "" {
		return named
	}
	return "this item"
}

func exchangeOf(refs Refs, topic Topic) string {
	if topic.Kind == TopicExchange {
		return topic.ID
	}
	return refs.ExchangeID
}

// countOf says a count in words rather than as a bare number, so "1 findings"
// never reaches anybody and a count nothing recorded says so.
func countOf(count int, singular, plural, absence string) string {
	switch {
	case count < 0:
		return absence
	case count == 0:
		return "no " + plural
	case count == 1:
		return "one " + singular
	default:
		return strconv.Itoa(count) + " " + plural
	}
}

// MaxRequestedBytes bounds one requested change to something that reads as a
// line. A finding longer than it is cut rather than wrapped across the message,
// because the value of the list is that a reader takes in what came back at a
// glance, and one finding that runs for a paragraph takes that from all of
// them. What was cut is in the record the message already points at.
const MaxRequestedBytes = 200

const requestedCutMark = "…"

// requestedOf lists what a repair round asked for, one requested change to a
// line. The lines are marked with a dash rather than numbered: the record is
// what a reader counts findings in, and a number here would read as an order to
// do them in that nobody decided.
//
// A record that counted findings without keeping them says so. The absence is
// not the same news as a reviewer who asked for nothing, and stating it points
// at the record rather than leaving the count looking like the whole account.
func requestedOf(requested []string) string {
	if len(requested) == 0 {
		return "What each of them asks for is in the record rather than here."
	}
	lines := make([]string, 0, len(requested))
	for _, one := range requested {
		lines = append(lines, "- "+boundLine(one))
	}
	return strings.Join(lines, "\n")
}

// restOfTheReason says where the whole of a reason is, once the channel has
// been given the first sentence of it. It names the record rather than trailing
// off, because a reader who wants the argument has to know there is more of it
// and where: the tracker keeps every word, and this is a view of it.
const restOfTheReason = " The rest of the reason is in the item's record."

// oneSentence is the reasoning behind a decision as a channel carries it: the
// first sentence, and a pointer at the record holding the rest.
//
// The whole of a reason is written for the tracker, where somebody deciding
// whether the decision was right reads it in full. In a channel it is one line
// of a narrative somebody is scanning, and a paragraph of justification under a
// one-line fact is what makes a reader skip the next message too. So the
// channel takes the first sentence, which is where the reason a decision was
// made is, and says where the argument for it is.
//
// Three things have to be true for a full stop to be the end of a sentence, and
// each of them is a way somebody's reason gets cut into nonsense otherwise:
// something other than a word character follows it, since yoyodyne-ifd.102.7 is
// one word; what comes next begins a sentence, since a lower-case word after a
// stop is the tail of something like "e.g. the adapter"; and the word it closes
// is not one of the few whose stop never ends a sentence, since those are
// followed by a capital as often as not. A boundary that fails any of them is
// read past, so the worst this does is carry more of the reason than it needed
// to — which is the safe direction, the record holding the whole either way.
func oneSentence(text string) string {
	trimmed := strings.TrimSpace(text)
	for index, character := range trimmed {
		switch character {
		case '.', '!', '?':
		default:
			continue
		}
		rest := strings.TrimSpace(trimmed[index+1:])
		if rest == "" {
			return trimmed
		}
		if next := trimmed[index+1]; next != ' ' && next != '\t' && next != '\n' {
			continue
		}
		if opening, _ := utf8.DecodeRuneInString(rest); !unicode.IsUpper(opening) {
			continue
		}
		if abbreviated(trimmed[:index]) {
			continue
		}
		return trimmed[:index+1] + restOfTheReason
	}
	return trimmed
}

// abbreviations are the short forms whose full stop closes a word rather than a
// sentence. The list is short on purpose: it is the ones that actually turn up
// in prose somebody wrote to justify a decision, and a form nobody thought of
// costs a message that carries more of the reason than it had to rather than
// one that carries none of it.
var abbreviations = map[string]bool{
	"e.g":    true,
	"eg":     true,
	"i.e":    true,
	"ie":     true,
	"etc":    true,
	"vs":     true,
	"cf":     true,
	"approx": true,
}

// abbreviated reports prose whose last word is one of them.
func abbreviated(said string) bool {
	word := said
	if space := strings.LastIndexAny(word, " \t\n"); space >= 0 {
		word = word[space+1:]
	}
	return abbreviations[strings.ToLower(word)]
}

// boundLine cuts one line that would not read as one, and marks the cut so
// nobody reads a truncated finding as the whole of what was asked for.
func boundLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) <= MaxRequestedBytes {
		return trimmed
	}
	cut := MaxRequestedBytes - len(requestedCutMark)
	for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
		cut--
	}
	return strings.TrimRight(trimmed[:cut], " \t") + requestedCutMark
}

// roundsOf says where an exchange has got to against its cap. The cap is said
// alongside the round because the round on its own does not say how close the
// exchange is to closing unresolved, which is the part an operator acts on.
func roundsOf(detail Detail) string {
	switch {
	case detail.Round <= 0:
		return "an unrecorded round"
	case detail.Rounds <= 0:
		return "round " + strconv.Itoa(detail.Round)
	default:
		return "round " + strconv.Itoa(detail.Round) + " of " + strconv.Itoa(detail.Rounds)
	}
}

// ageOf says how long a state has stood, as somebody would say it out loud: the
// largest unit that still says it honestly, and no more precision than that. A
// state said again every hour is read for how long it has been rather than for
// how many minutes, and "10 hours" is what makes an overnight stall obvious
// where "10h03m17s" is a number to parse.
//
// It is measured against the moment the message dates itself to rather than a
// clock of its own, so a message says the age it had when it was said however
// long afterwards it is read.
//
// What is unrecorded is a moment the record does not hold, or one that comes
// after the message that measures against it. A length of zero is not that: two
// records written in the same moment are a span of no time rather than a span
// nobody wrote down, and saying it was unrecorded would blame the record for
// something it holds exactly.
func ageOf(since, at time.Time) string {
	if since.IsZero() || at.IsZero() || at.Before(since) {
		return "an unrecorded length of time"
	}
	stood := at.Sub(since)
	unrecorded := "an unrecorded length of time"
	switch {
	case stood >= 24*time.Hour:
		return countOf(int(stood/(24*time.Hour)), "day", "days", unrecorded)
	case stood >= time.Hour:
		return countOf(int(stood/time.Hour), "hour", "hours", unrecorded)
	case stood >= time.Minute:
		return countOf(int(stood/time.Minute), "minute", "minutes", unrecorded)
	default:
		return "under a minute"
	}
}

// priorityOf says where in the queue an item sits. The number is said as the
// tracker's own, because that is what somebody reordering the backlog types, and
// a priority the record did not carry says so rather than reading as the highest
// one there is — zero is the top of this queue rather than an unstated place in
// it, which is exactly the confusion an absent value would cause.
func priorityOf(detail Detail) string {
	if detail.Priority < 0 {
		return "a priority the record does not state"
	}
	return "priority " + strconv.Itoa(detail.Priority)
}

// effectOf says what a recorded directive did to the work it names. The two
// cases are opposite facts rather than degrees of one — an operational directive
// is in force the moment it is recorded, and an unresolved one has stopped the
// work until somebody settles it — so the sentence says which, and says what is
// waiting where anything is. A reader who had to infer that from a severity mark
// would be inferring the whole of what a directive did.
func effectOf(detail Detail) string {
	if unresolved := strings.TrimSpace(detail.Unresolved); unresolved != "" {
		return "the work it affects waits until this is settled: " + unresolved
	}
	return "it applies from now on, and nothing waits on it"
}

// outcomeOf says how an exchange ended. Closing unresolved at the round cap is
// named as such and carries what was left unsettled, because that is the ending
// somebody has to do something about.
func outcomeOf(detail Detail) string {
	if unresolved := strings.TrimSpace(detail.Unresolved); unresolved != "" {
		return "unresolved at its round cap, leaving: " + unresolved
	}
	return "resolved"
}

// shortCommit says a commit the way a reader would quote one. The whole
// identifier is in the record; what a sentence needs is enough of it to match.
func shortCommit(commit string) string {
	trimmed := strings.TrimSpace(commit)
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12]
}

// ended closes the persona's own sentence before the whose-move clause is put
// after it. Most voice lines finish on a substituted value — a reason somebody
// typed, an agent's own words — and what somebody wrote rarely ends in a full
// stop, so without this the clause runs straight into the last word of the
// account and reads as part of what was said rather than as the harness's note
// about where the thread stands.
func ended(said string) string {
	trimmed := strings.TrimRight(said, " \t\n")
	if trimmed == "" {
		return trimmed
	}
	// A line that was cut ends on the mark saying so, and a full stop after it
	// would read as the end of a sentence the cut is what removed.
	if strings.HasSuffix(trimmed, requestedCutMark) {
		return trimmed
	}
	switch trimmed[len(trimmed)-1] {
	case '.', '!', '?':
		return trimmed
	default:
		return trimmed + "."
	}
}

// bound cuts a body that would not survive the surface it is posted to, and says
// it was cut and where the whole is. Cutting rather than splitting is deliberate:
// a message broken into four to fit is four messages in a narrative that had one
// thing to say.
//
// The tail is the part that is kept whatever else goes. It is the whose-move
// clause, and it is the one sentence a cut must not take: a reader given a
// truncated account of what happened can go to the record for the rest, and a
// reader given no idea who holds the ball has nothing to go to.
func bound(body, tail string, refs Refs) string {
	limit := MaxBodyBytes - len(tail)
	if len(body) <= limit {
		return body + tail
	}
	note := fmt.Sprintf(bodyCutNote, refs.Record())
	cut := limit - len(note)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return strings.TrimRight(body[:cut], " \n\t") + note + tail
}
