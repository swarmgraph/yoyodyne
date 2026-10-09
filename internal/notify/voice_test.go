package notify

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var moment = time.Date(2026, 8, 19, 15, 4, 5, 0, time.UTC)

// testProduct is the product these speakers speak for, and testAppearance is the
// appearance of a project that configured nothing but is still running a named
// product — which is every project, because the id is required configuration.
const testProduct domain.ProductID = "yoyodyne"

var testAppearance = Appearance{Product: testProduct}

// speakers is every speaker there is, which is what "events from every persona
// type" has to mean for a test to be able to check it.
func speakers() []Speaker {
	all := []Speaker{Harness()}
	for _, role := range domain.Roles() {
		all = append(all, Persona(role, ""))
	}
	return all
}

// requestedChanges is what a repair round asked for, as the record holds it: two
// findings a line takes whole and one longer than any line, so what a message
// does with each of them is exercised wherever this event is rendered.
var requestedChanges = []string{
	"blocker: handle the nil worktree (runner.go:42)",
	"major: integration is now automatic; README.md still says the harness does not integrate (README.md:38)",
	"minor: " + strings.Repeat("a finding nobody could fit on a line ", 12),
}

// recordedDirective is a directive identifier as the record writes one, and it
// is the shape of the one an operator was shown twice in a single
// acknowledgment.
const recordedDirective = "directive-f007fa2734c8b1ee9d5a6470c1b2e8a3"

// fullyRecorded is an event with every field a voice line could reach for, so a
// rendering failure is a missing line rather than a missing fact.
func fullyRecorded(kind Kind) Event {
	return Event{
		Kind:     kind,
		At:       moment,
		Severity: report.SeverityNote,
		Refs: Refs{
			RunID:      "run-4d1f",
			WorkItemID: "yoyodyne-ifd.68.2",
			ExchangeID: "exchange-7f3a",
			// The directive reference is carried here so the sweeps below render
			// every directive kind with one to leak. Without it the four
			// acknowledgment kinds rendered their identifier as a stated absence,
			// which is how the one renderer an operator meets in person went through
			// the whole of the identifiers sweep looking clean.
			DirectiveID: recordedDirective,
			PullRequest: "https://example.test/pull/84",
		},
		Detail: Detail{
			SelectedBy:      "development manager",
			SelectionReason: "highest-priority item nothing is holding back",
			Command:         "go test ./...",
			ExitCode:        1,
			RefusedPaths:    []string{"docs/designs/v1-harness-design.md"},
			OmittedPaths:    2,
			Grants:          []string{"docs/product/brief.md"},
			Findings:        3,
			Requested:       requestedChanges,
			TargetBranch:    "main",
			Commit:          "0123456789abcdef0123456789abcdef01234567",
			PullRequest:     "#84 (https://example.test/pull/84)",
			Cause:           "an exhausted provider usage limit",
			ReceivedBy:      "Product Manager",
			Round:           2,
			Rounds:          5,
			Unresolved:      "which branch the change belongs on",
			Artifact:        "slack-reporting-design",
			Title:           "Conversation milestones reach Slack",
			Goal:            "Work the harness runs on its own is visible while it runs",
			Parent:          "yoyodyne-ifd.68",
			Priority:        1,
			Refused:         12,
			Asking:          "product manager",
			Reason:          "reordering the backlog first",
			Since:           moment.Add(-3 * time.Hour),
			Ready:           4,
			Outstanding:     2,
			Behind:          12,
			Accumulated:     37,
			Ending:          string(runstate.OutcomeTimedOut),
			Remains:         "work preserved",
			Budget:          runstate.TriageReviewRoundBudget,
			Cap:             5,
			Crossing:        2,
			Crossings:       runstate.MaxDelegatedCapCrossings,
		},
		Text: "the developer's own words, carried through",
	}
}

func TestEveryPersonaSaysEveryReportableKind(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	for _, speaker := range speakers() {
		for _, kind := range Kinds() {
			message, err := Render(topic, speaker, fullyRecorded(kind))
			if err != nil {
				t.Fatalf("the %s says %s: %v", speaker.Key(), kind, err)
			}
			if strings.ContainsAny(message.Body, "{}") {
				t.Fatalf("the %s says %s as %q, which left a placeholder", speaker.Key(), kind, message.Body)
			}
			if message.Speaker != speaker.Key() || message.Kind != kind {
				t.Fatalf("message is %+v, want speaker %q and kind %q", message, speaker.Key(), kind)
			}
			if message.Topic != topic.Key() {
				t.Fatalf("the %s says %s to %q, want %q", speaker.Key(), kind, message.Topic, topic.Key())
			}
		}
	}
}

// A thread's silence must never leave a reader guessing who holds the ball, and
// which message turns out to be a thread's last is not knowable when it is
// written. So the guarantee is on every message from every persona rather than
// on the ones somebody predicted would be final.
func TestEveryMessageSaysWhoseMoveFollowsIt(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	for _, speaker := range speakers() {
		for _, kind := range Kinds() {
			message, err := Render(topic, speaker, fullyRecorded(kind))
			if err != nil {
				t.Fatalf("the %s says %s: %v", speaker.Key(), kind, err)
			}
			move, ok := nextMoves[kind]
			if !ok {
				t.Fatalf("%s says nothing about whose move follows it", kind)
			}
			if !strings.HasSuffix(message.Body, nextMoveLead+move) {
				t.Fatalf("the %s says %s as %q, which does not end on whose move follows", speaker.Key(), kind, message.Body)
			}
			// The clause is the harness's note about where the thread stands rather
			// than part of what the persona said, and most lines finish on words
			// somebody typed rather than on a full stop.
			// A line the message cut ends on the mark saying so, which closes the
			// account exactly as a full stop does — and putting a full stop after it
			// would end a sentence the cut is what removed.
			account := strings.TrimSuffix(message.Body, nextMoveLead+move)
			if strings.HasSuffix(account, requestedCutMark) {
				continue
			}
			if !strings.HasSuffix(account, ".") && !strings.HasSuffix(account, "!") && !strings.HasSuffix(account, "?") {
				t.Fatalf("the %s says %s as %q, which runs the clause into the account", speaker.Key(), kind, message.Body)
			}
		}
	}
}

// Work marked for a conversation is not queued for a run and never will be, so
// the queue's answer to what comes next would be telling the reader to expect
// something that cannot arrive — which is the same guessing, dressed up as an
// answer.
func TestWorkAConversationCarriesIsNeverSaidToBeWaitingForARun(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.138")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	for _, kind := range []Kind{KindItemAdmitted, KindItemDecomposed, KindItemAttributed, KindItemReprioritized} {
		queued := fullyRecorded(kind)
		message, err := Render(topic, Persona(domain.RoleProductManager, ""), queued)
		if err != nil {
			t.Fatalf("render ordinary %s: %v", kind, err)
		}
		if !strings.HasSuffix(message.Body, nextMoveLead+nextMoves[kind]) {
			t.Fatalf("ordinary %s reads as %q, want the queue's answer", kind, message.Body)
		}
		queued.Detail.Executor = string(domain.WorkItemExecutorConversation)
		handed, err := Render(topic, Persona(domain.RoleProductManager, ""), queued)
		if err != nil {
			t.Fatalf("render conversation-carried %s: %v", kind, err)
		}
		if !strings.HasSuffix(handed.Body, nextMoveLead+nextMoves[KindWorkHandedOff]) {
			t.Fatalf("conversation-carried %s reads as %q, want the handoff's answer", kind, handed.Body)
		}
		// An admission that says whose conversation carries the item answers with
		// that role: the item is in the queue and nothing will pull it, so the wait
		// starts here rather than at a later handoff.
		queued.Detail.Executor = string(domain.ConversationWith(domain.RoleArchitect))
		attributed, err := Render(topic, Persona(domain.RoleProductManager, ""), queued)
		if err != nil {
			t.Fatalf("render %s carried by a named role: %v", kind, err)
		}
		if !strings.HasSuffix(attributed.Body, nextMoveLead+"the architect's, in conversation — no run will ever be started for this.") {
			t.Fatalf("%s carried by the architect reads as %q, want the wait left with them", kind, attributed.Body)
		}
	}
}

// A watch idles for opposite reasons, and the clause it used to close on
// answered for one of them. It named the product manager whatever the session
// had found, and an operator acted on that three times over a queue whose only
// unstarted work was the architect's to carry, while a developer run worked on
// the other slot: nothing was waiting on an admission, and the line had not
// stopped.
//
// So the named actor is the one who can act. The admission clause is what is
// left when nothing is going and nothing is anybody's to carry, which is the one
// state admitting ready work actually changes.
func TestAnIdleWatchNamesTheActorWhoCanActOnIt(t *testing.T) {
	topic := Product()
	for _, testCase := range []struct {
		name   string
		detail func(*Detail)
		want   string
	}{
		{
			name:   "carried in an architect's conversation",
			detail: func(d *Detail) { d.Executor = string(domain.ConversationWith(domain.RoleArchitect)) },
			want:   "the architect's, in conversation — the work this poll passed over is carried there, and no run will ever start it.",
		},
		{
			// The run in flight answers second: an item somebody has to open is still
			// waiting on them while the other slot works.
			name: "a run in flight beside work an architect carries",
			detail: func(d *Detail) {
				d.Executor = string(domain.ConversationWith(domain.RoleArchitect))
				d.Running = 1
			},
			want: "the architect's, in conversation — the work this poll passed over is carried there, and no run will ever start it.",
		},
		{
			name:   "a run in flight and nothing anybody carries",
			detail: func(d *Detail) { d.Running = 1 },
			want:   "nobody's — the runs in flight carry on, and the queue is read again as each of them finishes.",
		},
		{
			// The queue was never read, so nothing that is in it stopped the choosing
			// and nothing anybody admits reaches a store that will not answer.
			name:   "a queue that could not be read",
			detail: func(d *Detail) { d.Unreadable = true },
			want:   "the harness's — the queue could not be read, and it is read again until it answers or the session gives up on it.",
		},
		{
			name: "a queue that could not be read while a run carries on",
			detail: func(d *Detail) {
				d.Unreadable = true
				d.Running = 1
			},
			want: "the harness's — the queue could not be read, and it is read again until it answers or the session gives up on it.",
		},
		{
			name:   "nothing going and nothing anybody carries",
			detail: func(*Detail) {},
			want:   nextMoves[KindWatchIdle],
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			event := fullyRecorded(KindWatchIdle)
			testCase.detail(&event.Detail)
			message, err := Render(topic, Harness(), event)
			if err != nil {
				t.Fatalf("render an idle watch: %v", err)
			}
			if !strings.HasSuffix(message.Body, nextMoveLead+testCase.want) {
				t.Fatalf("idle reads as %q, want it to close on %q", message.Body, testCase.want)
			}
		})
	}
}

// A stall over a queue whose last poll said what was in the way of it closes on
// whoever releases that, rather than on somebody restarting a chooser that is
// running and doing exactly what it should. The clause is the read model's, said
// beside the cause the message states, so the two cannot disagree.
//
// Every persona is rendered rather than the harness alone. The harness's is the
// one an operator meets most, and it is not the one the mover names: the
// development manager reads her own stall message, and a preamble of hers that
// said nothing was holding the queue would contradict the clause telling her to
// release it.
func TestAStallPointsOutTheCauseAndClosesOnWhoeverReleasesIt(t *testing.T) {
	topic := Product()
	cause := "33 of the 47 admitted items are stopped, waiting on the development manager's decision or the harness carrying it out"
	mover := "the development manager's, or the harness's where she has decided — nothing pulls a stopped item until her decision about it is made and carried out"
	for _, speaker := range speakers() {
		event := fullyRecorded(KindStallNoticed)
		event.Detail.Cause = cause
		event.Detail.Mover = mover
		message, err := Render(topic, speaker, event)
		if err != nil {
			t.Fatalf("the %s says a stall: %v", speaker.Key(), err)
		}
		if !strings.Contains(message.Body, cause) {
			t.Fatalf("the %s says a stall as %q, which does not point out the cause", speaker.Key(), message.Body)
		}
		if !strings.HasSuffix(message.Body, nextMoveLead+mover+".") {
			t.Fatalf("the %s says a stall as %q, want it to close on the person who releases what it named", speaker.Key(), message.Body)
		}
		// The half of the old message that the cause replaces. A line that names
		// what is holding the queue and then says nothing accounts for the silence,
		// or that nothing has read the queue, contradicts itself in one breath —
		// which is what the alarm did across surfaces before this and must not now
		// do inside one message.
		for _, contradiction := range []string{
			"nothing accounting for it",
			"nothing explains it",
			"no record says why",
			"nothing is reading",
			"nothing holding it",
			"no hold,",
		} {
			if strings.Contains(message.Body, contradiction) {
				t.Fatalf("the %s says a stall as %q, which still claims %q beside the cause", speaker.Key(), message.Body, contradiction)
			}
		}
	}

	// A stall with no poll to read a cause from falls back to the chooser being
	// looked at, which is the whole of what anybody can do about it.
	event := fullyRecorded(KindStallNoticed)
	event.Detail.Cause = cause
	fallen, err := Render(topic, Harness(), event)
	if err != nil {
		t.Fatalf("render a stall with no mover: %v", err)
	}
	if !strings.HasSuffix(fallen.Body, nextMoveLead+nextMoves[KindStallNoticed]) {
		t.Fatalf("the stall reads as %q, want the table's answer where no poll named one", fallen.Body)
	}
}

// The admission pointer is said only where admitting ready work is what changes
// the answer. A session polling beside a run, or over work only a conversation
// carries, is not waiting on the product manager for anything.
func TestAnIdleWatchPointsAtAdmissionOnlyWhenAdmissionIsTheNextAct(t *testing.T) {
	topic := Product()
	for _, detail := range []Detail{
		{Executor: string(domain.ConversationWith(domain.RoleArchitect))},
		{Running: 2},
		{Executor: string(domain.ConversationWith(domain.RoleDevelopmentManager)), Running: 1},
		{Unreadable: true},
		{Unreadable: true, Running: 1},
	} {
		event := fullyRecorded(KindWatchIdle)
		event.Detail = detail
		message, err := Render(topic, Harness(), event)
		if err != nil {
			t.Fatalf("render an idle watch: %v", err)
		}
		if strings.HasSuffix(message.Body, nextMoveLead+nextMoves[KindWatchIdle]) {
			t.Fatalf("idle over %+v reads as %q, want somebody who can act on it named", detail, message.Body)
		}
	}
}

// Every voice names the same way out of a session running an old build, because
// six accounts of one remedy are six chances to send a reader somewhere else.
//
// The remedy moved: a watching session takes up a build installed over it by
// itself, so what is left for a person is the install, and the whose-move clause
// says exactly that. A voice still telling the reader to restart the session
// would be contradicting the clause printed underneath it in the same message.
// This is the guard against a table left behind — one voice was, and the sink
// speaks this kind as the harness, so nothing about the sink could ever have
// caught it.
func TestEveryVoiceNamesTheSameWayOutOfAStaleResident(t *testing.T) {
	topic := Product()
	// Wording that puts the restart on a person. The session restarting itself is
	// the thing being said, so it is what somebody has to do that is looked for
	// rather than the word "restart".
	handedToAPerson := []string{
		"somebody restarts",
		"until it is restarted",
		"restarting it",
		"until restart has an owner",
	}
	move, ok := nextMoves[KindResidentStale]
	if !ok {
		t.Fatalf("%s says nothing about whose move follows it", KindResidentStale)
	}
	for _, speaker := range speakers() {
		message, err := Render(topic, speaker, fullyRecorded(KindResidentStale))
		if err != nil {
			t.Fatalf("the %s says %s: %v", speaker.Key(), KindResidentStale, err)
		}
		// The persona's own words, with the whose-move clause taken off. Reading
		// the whole body would pass every voice on the strength of the one clause
		// they all share, which is the drift this exists to catch rather than a
		// thing it may lean on.
		account := strings.TrimSuffix(message.Body, nextMoveLead+move)
		if account == message.Body {
			t.Fatalf("the %s says %s as %q, which does not end on whose move follows", speaker.Key(), KindResidentStale, message.Body)
		}
		said := strings.ToLower(account)
		if !strings.Contains(said, "install") {
			t.Fatalf("the %s says %s as %q, which never names installing the build -- the one thing left for a person to do",
				speaker.Key(), KindResidentStale, account)
		}
		for _, handed := range handedToAPerson {
			if strings.Contains(said, handed) {
				t.Fatalf("the %s says %s as %q, which asks for a restart the session now makes for itself",
					speaker.Key(), KindResidentStale, account)
			}
		}
	}
}

// A session stopping to take up a build deployed over it is the one stop nobody
// has to answer, and every voice has to say it that way.
//
// The recorded transition is an ordinary stop, so before this kind existed the
// restart was posted as a session ending — every voice telling the reader the
// line was down, and the whose-move clause telling them to start a session
// again. That is a move they do not have, handed to them once per deploy, which
// is the standing chore the self-restart was built to end rather than reproduce.
func TestEveryVoiceSaysARedeployingSessionIsComingBack(t *testing.T) {
	topic := Product()
	move, ok := nextMoves[KindWatchRedeploying]
	if !ok {
		t.Fatalf("%s says nothing about whose move follows it", KindWatchRedeploying)
	}
	if !strings.HasPrefix(move, "nobody's") {
		t.Fatalf("whose move follows %s is %q, want nobody waiting on anything", KindWatchRedeploying, move)
	}
	// Wording that leaves the reader holding a session that has ended. The stopped
	// kind says all of it and should; this one must not.
	ended := []string{
		"until somebody starts it again",
		"nothing more is chosen until a session is started",
		"no more changes will arrive",
		"the watch session ended",
	}
	for _, speaker := range speakers() {
		message, err := Render(topic, speaker, fullyRecorded(KindWatchRedeploying))
		if err != nil {
			t.Fatalf("the %s says %s: %v", speaker.Key(), KindWatchRedeploying, err)
		}
		account := strings.TrimSuffix(message.Body, nextMoveLead+move)
		if account == message.Body {
			t.Fatalf("the %s says %s as %q, which does not end on whose move follows", speaker.Key(), KindWatchRedeploying, message.Body)
		}
		said := strings.ToLower(account)
		if !strings.Contains(said, "restart") && !strings.Contains(said, "back on the build") {
			t.Fatalf("the %s says %s as %q, which never says the session is coming back", speaker.Key(), KindWatchRedeploying, account)
		}
		for _, over := range ended {
			if strings.Contains(said, over) {
				t.Fatalf("the %s says %s as %q, which reads as a session that ended rather than one restarting",
					speaker.Key(), KindWatchRedeploying, account)
			}
		}
	}
}

func TestNoTwoPersonasSayTheSameEventTheSameWay(t *testing.T) {
	// This is the whole point of a voice: a reader who has scrolled past the
	// display name still knows who is talking.
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	for _, kind := range Kinds() {
		said := make(map[string]string, len(speakers()))
		for _, speaker := range speakers() {
			message, err := Render(topic, speaker, fullyRecorded(kind))
			if err != nil {
				t.Fatalf("the %s says %s: %v", speaker.Key(), kind, err)
			}
			if other, seen := said[message.Body]; seen {
				t.Fatalf("the %s and the %s both say %s as %q", other, speaker.Key(), kind, message.Body)
			}
			said[message.Body] = speaker.Key()
		}
	}
}

func TestEveryPersonaAppearsAsItself(t *testing.T) {
	identities := make(map[string]string, len(speakers()))
	avatars := make(map[string]string, len(speakers()))
	for _, speaker := range speakers() {
		identity := speaker.Identity()
		if err := identity.Validate(); err != nil {
			t.Fatalf("the %s appears as %+v: %v", speaker.Key(), identity, err)
		}
		if other, seen := identities[identity.Name]; seen {
			t.Fatalf("the %s and the %s both appear as %q", other, speaker.Key(), identity.Name)
		}
		identities[identity.Name] = speaker.Key()
		if other, seen := avatars[identity.Avatar]; seen {
			t.Fatalf("the %s and the %s share the avatar %q", other, speaker.Key(), identity.Avatar)
		}
		avatars[identity.Avatar] = speaker.Key()
	}
}

func TestTheConfiguredAgentIsNamedOnlyWhenItSaysSomethingTheRoleDoesNot(t *testing.T) {
	plain := Persona(domain.RoleDeveloper, "").Identity()
	if plain.Name != "Developer" {
		t.Fatalf("an unnamed agent appears as %q", plain.Name)
	}
	same := Persona(domain.RoleDeveloper, "developer").Identity()
	if same.Name != "Developer" {
		t.Fatalf("an agent named for its role appears as %q", same.Name)
	}
	named := Persona(domain.RoleDeveloper, "opus").Identity()
	if named.Name != "Developer (opus)" {
		t.Fatalf("a configured agent appears as %q", named.Name)
	}
}

// Which harness is talking. An operator develops more than one product, and a
// role name on its own says which chair spoke and not which product's — so every
// name carries the product, the harness's included, in the same place and the
// same shape whichever speaker it is.
func TestEverySpeakerIsNamedWithTheProductItSpeaksFor(t *testing.T) {
	for _, want := range []struct {
		speaker Speaker
		name    string
	}{
		{speaker: Harness(), name: "Yoyodyne (context-conductor)"},
		{speaker: Persona(domain.RoleDevelopmentManager, ""), name: "Development Manager (context-conductor)"},
		{speaker: Persona(domain.RoleProductManager, ""), name: "Lead Product Manager (context-conductor)"},
		{speaker: Persona(domain.RoleArchitect, ""), name: "Architect (context-conductor)"},
		{speaker: Persona(domain.RoleDeveloper, ""), name: "Developer (context-conductor)"},
		{speaker: Persona(domain.RoleReviewer, ""), name: "Reviewer (context-conductor)"},
		// A project that configured more than one agent for a role still says the
		// product last, so the last thing on every name is the same fact.
		{speaker: Persona(domain.RoleDeveloper, "opus"), name: "Developer (opus) (context-conductor)"},
	} {
		appearance := Appearance{Product: "context-conductor"}
		if got := appearance.Identity(want.speaker); got.Name != want.name {
			t.Errorf("the %s appears as %q, want %q", want.speaker.Key(), got.Name, want.name)
		}
	}
	// A speaker the voice table has nothing for has no name to qualify, and stays
	// the empty identity a caller has to notice rather than becoming a product's
	// name for nobody.
	if got := (Appearance{Product: "context-conductor"}).Identity(Speaker{Role: "auditor"}); got != (Identity{}) {
		t.Errorf("an unvoiced speaker appears as %+v, want nothing", got)
	}
}

// The point of carrying it: two harnesses reporting into one channel are two
// sets of names rather than one, so nothing said by either is ambiguous about
// which product it is about.
func TestTwoProductsShareNoSpeakerName(t *testing.T) {
	here := Appearance{Product: testProduct}
	elsewhere := Appearance{Product: "context-conductor"}
	for _, speaker := range speakers() {
		mine, theirs := here.Identity(speaker), elsewhere.Identity(speaker)
		if mine.Name == theirs.Name {
			t.Errorf("the %s appears as %q on both products", speaker.Key(), mine.Name)
		}
		if err := mine.Validate(); err != nil {
			t.Errorf("the %s appears as %+v: %v", speaker.Key(), mine, err)
		}
	}
}

// The product qualifies the name and moves nothing else. Whose account a message
// is is a claim about who did the work, and it is the same claim whichever
// product the work was done on.
func TestNamingTheProductMovesNothingAboutAttribution(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.13")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	posted := &recorder{}
	speaker := Persona(domain.RoleDevelopmentManager, "")
	if err := New(posted, testAppearance).Notify(context.Background(), topic, speaker, fullyRecorded(KindItemAdmitted)); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(posted.posted) != 1 {
		t.Fatalf("posted %d messages, want one", len(posted.posted))
	}
	message := posted.posted[0]
	if message.Speaker != speaker.Key() {
		t.Errorf("message attributed to %q, want the %s", message.Speaker, speaker.Key())
	}
	if want := "Development Manager (yoyodyne)"; message.Identity.Name != want {
		t.Errorf("message posted as %q, want %q", message.Identity.Name, want)
	}
	// The words are the persona's own, and naming the product beside the name
	// leaves them exactly as the voice table renders them.
	rendered, err := Render(topic, speaker, fullyRecorded(KindItemAdmitted))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if message.Body != rendered.Body {
		t.Errorf("message body = %q, want the rendered %q", message.Body, rendered.Body)
	}
}

// A configuration that names no product is the appearance there was before there
// were two products to tell apart, rather than a name with an empty parenthesis
// in it or a speaker called nothing at all.
func TestAnUnnamedProductLeavesTheShippedNames(t *testing.T) {
	for name, appearance := range map[string]Appearance{
		"no product":      {},
		"a blank product": {Product: "   "},
	} {
		for _, speaker := range speakers() {
			if got := appearance.Identity(speaker); got != speaker.Identity() {
				t.Errorf("with %s the %s appears as %+v, want the shipped %+v", name, speaker.Key(), got, speaker.Identity())
			}
		}
		// And a speaker the voice table has nothing for stays the empty identity a
		// caller has to notice, rather than becoming a name for nobody.
		if got := appearance.Identity(Speaker{Role: "auditor"}); got != (Identity{}) {
			t.Errorf("with %s an unvoiced speaker appears as %+v, want nothing", name, got)
		}
	}
}

// A project may choose the picture beside a name, in either shape a surface
// takes one. A speaker it configured nothing for keeps what the voice table
// ships, so naming one persona's avatar does not quietly un-decorate the rest.
func TestAConfiguredAvatarReplacesTheShippedOne(t *testing.T) {
	appearance := Appearance{Product: testProduct, Avatars: Avatars{
		HarnessSpeaker:               "https://example.invalid/faces/harness.png",
		string(domain.RoleDeveloper): ":ship-it:",
	}}
	for _, want := range []struct {
		speaker Speaker
		avatar  string
	}{
		{speaker: Harness(), avatar: "https://example.invalid/faces/harness.png"},
		{speaker: Persona(domain.RoleDeveloper, ""), avatar: ":ship-it:"},
		{speaker: Persona(domain.RoleReviewer, ""), avatar: Persona(domain.RoleReviewer, "").Identity().Avatar},
	} {
		if got := appearance.Identity(want.speaker); got.Avatar != want.avatar {
			t.Errorf("the %s appears with %q, want %q", want.speaker.Key(), got.Avatar, want.avatar)
		}
	}

	// A project with nothing configured is the shipped table exactly, and a blank
	// entry is the same as no entry rather than a speaker with no picture at all.
	for name, configured := range map[string]Avatars{
		"nothing configured": nil,
		"a blank entry":      {string(domain.RoleDeveloper): "   "},
	} {
		speaker := Persona(domain.RoleDeveloper, "")
		shipped := testAppearance.Identity(speaker)
		if got := (Appearance{Product: testProduct, Avatars: configured}).Identity(speaker); got != shipped {
			t.Errorf("with %s the developer appears as %+v, want the shipped %+v", name, got, shipped)
		}
	}
}

// The boundary the override stops at. An avatar is decoration; the name a
// message appears under, and whose account it is, are the voice table's, so
// there is nothing a project can configure that moves either.
func TestConfiguringAnAvatarMovesNothingAboutWhoSpeaks(t *testing.T) {
	appearance := Appearance{Product: testProduct, Avatars: Avatars{string(domain.RoleDeveloper): ":ship-it:"}}
	for _, speaker := range speakers() {
		shipped := testAppearance.Identity(speaker)
		configured := appearance.Identity(speaker)
		if configured.Name != shipped.Name {
			t.Errorf("the %s appears as %q with an avatar configured, want %q", speaker.Key(), configured.Name, shipped.Name)
		}
	}
	// And the message still says whose account it is, whatever the picture is.
	topic, err := WorkItem("yoyodyne-ifd.68.6")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	posted := &recorder{}
	speaker := Persona(domain.RoleDeveloper, "")
	if err := New(posted, appearance).Notify(context.Background(), topic, speaker, fullyRecorded(KindChecksPassed)); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(posted.posted) != 1 {
		t.Fatalf("posted %d messages, want one", len(posted.posted))
	}
	message := posted.posted[0]
	if message.Speaker != speaker.Key() || message.Identity.Name != testAppearance.Identity(speaker).Name {
		t.Fatalf("message = %+v, want it still attributed to the developer", message)
	}
	if message.Identity.Avatar != ":ship-it:" {
		t.Fatalf("message avatar = %q, want the configured one", message.Identity.Avatar)
	}
}

func TestWhatAnAgentWroteIsCarriedThroughUnchanged(t *testing.T) {
	// The deterministic half must never paraphrase the genuine half: this is the
	// text an agent already wrote, and a message that summarized it would be the
	// harness speaking in the agent's name.
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	written := "The staleness check reads the tracker twice; the second read can disagree with the first."
	for _, speaker := range speakers() {
		for _, kind := range []Kind{KindReportFiled, KindProposalRaised, KindExchangeTurn, KindBlockerRecorded} {
			event := fullyRecorded(kind)
			event.Text = written
			message, err := Render(topic, speaker, event)
			if err != nil {
				t.Fatalf("the %s says %s: %v", speaker.Key(), kind, err)
			}
			if !strings.Contains(message.Body, written) {
				t.Fatalf("the %s says %s as %q, which does not carry what was written", speaker.Key(), kind, message.Body)
			}
		}
	}
}

func TestSeverityIsSaidInWordsBeforeItIsSaidInDecoration(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	bodies := make(map[report.Severity]string, 3)
	for _, severity := range []report.Severity{report.SeverityCritical, report.SeverityWarning, report.SeverityNote} {
		event := fullyRecorded(KindReportFiled)
		event.Severity = severity
		message, err := Render(topic, Persona(domain.RoleDeveloper, ""), event)
		if err != nil {
			t.Fatalf("render a %s report: %v", severity, err)
		}
		bodies[severity] = message.Body
	}
	// Strip every emoji shortcode and the distinctions must all survive, because
	// the words carry the meaning and the decoration only adds to it.
	stripped := func(body string) string {
		for _, shortcode := range []string{":rotating_light: ", ":warning: "} {
			body = strings.ReplaceAll(body, shortcode, "")
		}
		return body
	}
	if !strings.HasPrefix(stripped(bodies[report.SeverityCritical]), "Critical — ") {
		t.Fatalf("a critical report reads as %q with its decoration stripped", stripped(bodies[report.SeverityCritical]))
	}
	if !strings.HasPrefix(stripped(bodies[report.SeverityWarning]), "Warning — ") {
		t.Fatalf("a warning reads as %q with its decoration stripped", stripped(bodies[report.SeverityWarning]))
	}
	if strings.Contains(bodies[report.SeverityNote], "Critical") || strings.Contains(bodies[report.SeverityNote], "Warning") {
		t.Fatalf("a note reads as %q", bodies[report.SeverityNote])
	}
	if stripped(bodies[report.SeverityCritical]) == stripped(bodies[report.SeverityNote]) {
		t.Fatal("a critical report and a note are indistinguishable without decoration")
	}
}

// trackerIdentifier is what a work item's identifier looks like whoever issued
// it: a hyphenated prefix and a dotted number, as `yoyodyne-ifd.102.7` and
// `beads-core.14` both are. It is matched by shape rather than by the two the
// event happens to carry, because the failure this guards against is a voice
// line reaching for some other item — the one a decomposition came out of, the
// one a dependency names — and a test looking for two known strings passes
// while that message goes out.
//
// What it deliberately does not match is the other things a message names: a
// run, an exchange, a commit, a pull request, a source file. None of those has
// the dotted number after a hyphenated word, and each of them is something a
// reader follows rather than an item they would have to look up to know what
// the message is about.
var trackerIdentifier = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9]*(?:-[A-Za-z0-9]+)+\.\d+(?:\.\d+)*\b`)

// A channel is read by people, and an identifier is a name a reader has to go
// and resolve before they know what a message is about. So every message says
// the work in the words the record calls it, whatever the persona and whatever
// the kind, and the identifier stays where identity belongs: the header the
// thread hangs from.
func TestAMessageNamesTheWorkInWordsRatherThanByItsIdentifier(t *testing.T) {
	const identifier = "yoyodyne-ifd.102.7"
	const named = "Re-arm the dropped-merge check"
	topic, err := WorkItem(identifier)
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	topic = topic.WithTitle(named)
	for _, speaker := range speakers() {
		for _, kind := range Kinds() {
			message, err := Render(topic, speaker, fullyRecorded(kind))
			if err != nil {
				t.Fatalf("the %s says %s: %v", speaker.Key(), kind, err)
			}
			if strings.Contains(message.Body, identifier) {
				t.Fatalf("the %s says %s as %q, which makes a reader resolve an identifier", speaker.Key(), kind, message.Body)
			}
			// The reference the record correlates by is the same identifier said a
			// second way, and it is no more readable for being in the refs.
			if strings.Contains(message.Body, fullyRecorded(kind).Refs.WorkItemID) {
				t.Fatalf("the %s says %s as %q, which names the item by its reference", speaker.Key(), kind, message.Body)
			}
			// And no other item's identifier either. The event this renders carries
			// the item a decomposition was cut out of as well as its own, which is
			// exactly the one a line can reach for without anybody noticing.
			if found := trackerIdentifier.FindString(message.Body); found != "" {
				t.Fatalf("the %s says %s as %q, which names an item by the identifier %q", speaker.Key(), kind, message.Body, found)
			}
			// The directive a message is about is the same kind of name, and the
			// four acknowledgment kinds are where a person meets one: they are the
			// answer to something somebody typed in a thread, so the identifier in
			// them is the reader being handed a slug for their own sentence.
			if strings.Contains(message.Body, recordedDirective) {
				t.Fatalf("the %s says %s as %q, which names the directive by its identifier", speaker.Key(), kind, message.Body)
			}
		}
	}
}

// The acknowledgment an operator actually met, replayed: a reply in a thread,
// recorded as an operational directive, answered by the harness.
//
// What he was shown printed the identifier twice and narrated the machinery
// that recorded it. What it says now is that it was recorded, who it was
// recorded for, his own words, and that it applies from now on — with the
// identifier nowhere in it, and each of those facts said once.
func TestARecordedDirectiveIsAcknowledgedInASentenceAndNamesNoIdentifier(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.26")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	const said = "prefer the smaller change here"
	message, err := Render(topic.WithTitle("Inbound acknowledgments speak plainly"), Harness(), Event{
		Kind:     KindDirectiveRecorded,
		At:       moment,
		Severity: report.SeverityNote,
		Refs:     Refs{WorkItemID: "yoyodyne-ifd.68.26", DirectiveID: recordedDirective},
		Detail:   Detail{ReceivedBy: "Product Manager"},
		Text:     said,
	})
	if err != nil {
		t.Fatalf("acknowledge a recorded directive: %v", err)
	}
	if strings.Contains(message.Body, recordedDirective) {
		t.Fatalf("the acknowledgment reads as %q, which hands the operator a slug for their own sentence", message.Body)
	}
	// The reference is still on the envelope, because the delivery pass and
	// anything else tracing the record correlate by it. It leaves the words, not
	// the message.
	if message.Refs.DirectiveID != recordedDirective {
		t.Fatalf("refs = %#v, want the identifier kept where the processes that read directives find it", message.Refs)
	}
	for _, wanted := range []string{"Recorded", "Product Manager", said, "it applies from now on"} {
		if !strings.Contains(message.Body, wanted) {
			t.Fatalf("the acknowledgment reads as %q, which does not say %q", message.Body, wanted)
		}
	}
	// Said once. The whose-move clause used to repeat the effect word for word,
	// which is the padding an acknowledgment reads worst as.
	if count := strings.Count(message.Body, "applies from now on"); count != 1 {
		t.Fatalf("the acknowledgment reads as %q, which says what the directive does %d times", message.Body, count)
	}
}

// An item whose record carried no name at all is said as this item rather than
// as its identifier: the thread it is posted in is already headed by that
// identifier, so repeating it under the header gives a reader the opaque half
// of the header again and nothing they did not have.
func TestAnItemNoRecordNamesIsSaidAsThisItemRatherThanAsItsIdentifier(t *testing.T) {
	const identifier = "yoyodyne-ifd.102.7"
	topic, err := WorkItem(identifier)
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	for _, kind := range []Kind{KindChecksPassed, KindItemReprioritized, KindPromoted} {
		message, err := Render(topic, Harness(), Event{
			Kind:     kind,
			At:       moment,
			Severity: report.SeverityNote,
			Refs:     Refs{WorkItemID: identifier},
			Detail:   Detail{Priority: 2, TargetBranch: "main", Commit: "0123456789abcdef"},
		})
		if err != nil {
			t.Fatalf("say %s about an item nothing names: %v", kind, err)
		}
		if strings.Contains(message.Body, identifier) {
			t.Fatalf("%s reads as %q, which repeats the identifier its thread is headed by", kind, message.Body)
		}
		if !strings.Contains(message.Body, "this item") {
			t.Fatalf("%s reads as %q, which says nothing about which work it is", kind, message.Body)
		}
	}
}

// The reasoning behind a decision is written for the record, where somebody
// weighing the decision reads all of it. A channel carries the first sentence
// of it and says where the rest is, because a paragraph of justification under
// a one-line fact is what makes a reader skip the next message too.
func TestReasoningForADecisionIsOneSentenceAndSaysWhereTheRestIs(t *testing.T) {
	for reason, want := range map[string]string{
		// The whole of a one-sentence reason is the sentence, so nothing is cut
		// and nothing points anywhere.
		"the adapter is stopped and nothing is waiting on it": "the adapter is stopped and nothing is waiting on it",
		"": "",
		// A full stop inside an identifier ends no sentence. Cutting one in half
		// would be worse than carrying the whole paragraph.
		"yoyodyne-ifd.102.7 goes below the rendering work. The epic's order is unchanged by that.": "yoyodyne-ifd.102.7 goes below the rendering work." + restOfTheReason,
		"It goes below the rendering work! Nothing depends on it.":                                 "It goes below the rendering work!" + restOfTheReason,
		// Nor does the stop closing an abbreviation, whether what follows it is a
		// word or the capital that starts the next clause. A message cut there
		// would carry "e.g." and a pointer at the record, which is a message
		// saying nothing at all.
		"It waits on the adapter, e.g. the Codex one, which nobody has answered for.": "It waits on the adapter, e.g. the Codex one, which nobody has answered for.",
		"It waits on an adapter, i.e. The Codex one. Nothing else is holding it.":     "It waits on an adapter, i.e. The Codex one." + restOfTheReason,
		// A reason written in lower case throughout has no sentence boundary this
		// can be sure of, so the whole of it is carried rather than a guess at
		// half of it.
		"the adapter is stopped. nothing is waiting on it": "the adapter is stopped. nothing is waiting on it",
	} {
		if got := oneSentence(reason); got != want {
			t.Fatalf("oneSentence(%q) = %q, want %q", reason, got, want)
		}
	}
}

func TestACountIsSaidInWordsRatherThanAsABareNumber(t *testing.T) {
	for count, want := range map[int]string{-1: "findings the record does not count", 0: "no findings", 1: "one finding", 4: "4 findings"} {
		if got := countOf(count, "finding", "findings", "findings the record does not count"); got != want {
			t.Fatalf("countOf(%d) = %q, want %q", count, got, want)
		}
	}
}

// A count says how much came back and nothing about what it is, and an operator
// reading one cannot tell a correction to a document from a problem with the
// design without leaving the channel. So a repair request names each change it
// asks for, in every voice that can say one.
func TestARepairRequestNamesEachChangeItAsksFor(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	for _, speaker := range speakers() {
		message, err := Render(topic, speaker, fullyRecorded(KindReviewRepairs))
		if err != nil {
			t.Fatalf("the %s asks for repairs: %v", speaker.Key(), err)
		}
		if !strings.Contains(message.Body, "- blocker: handle the nil worktree (runner.go:42)") {
			t.Fatalf("the %s asks for repairs as %q, which does not name the first change", speaker.Key(), message.Body)
		}
		if !strings.Contains(message.Body, "- major: integration is now automatic") {
			t.Fatalf("the %s asks for repairs as %q, which does not name the second change", speaker.Key(), message.Body)
		}
		// One line per finding, so what came back is taken in at a glance rather
		// than read as a paragraph.
		lines := 0
		for _, line := range strings.Split(message.Body, "\n") {
			if strings.HasPrefix(line, "- ") {
				lines++
			}
		}
		if lines != len(requestedChanges) {
			t.Fatalf("the %s put %d changes on their own line, want %d: %q", speaker.Key(), lines, len(requestedChanges), message.Body)
		}
	}
}

func TestAFindingTooLongForALineIsCutRatherThanOverflowing(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	message, err := Render(topic, Persona(domain.RoleReviewer, ""), fullyRecorded(KindReviewRepairs))
	if err != nil {
		t.Fatalf("render a repair request: %v", err)
	}
	// The clause saying whose move follows is the harness's own note, and it lands
	// after the last change rather than being part of it.
	account := strings.TrimSuffix(message.Body, nextMoveLead+nextMoves[KindReviewRepairs])
	for _, line := range strings.Split(account, "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		if len(line) > len("- ")+MaxRequestedBytes {
			t.Fatalf("a requested change is %d bytes: %q", len(line), line)
		}
	}
	if !strings.Contains(message.Body, requestedCutMark) {
		t.Fatalf("a finding longer than a line was not marked as cut: %q", message.Body)
	}
	// The cut takes the tail rather than the beginning: what a reader is owed is
	// which change is being asked for, and that is where the finding starts.
	if !strings.Contains(message.Body, "- minor: a finding nobody could fit on a line") {
		t.Fatalf("a cut finding lost the words it starts with: %q", message.Body)
	}
	if got := boundLine("blocker: short enough."); got != "blocker: short enough." {
		t.Fatalf("a finding a line takes whole was changed to %q", got)
	}
}

// A record that counted findings without keeping them is not a reviewer who
// asked for nothing, and a message that said nothing about the difference would
// read as the whole account of a repair round.
func TestARepairRequestSaysWhenTheRecordKeptNoChanges(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	event := fullyRecorded(KindReviewRepairs)
	event.Detail.Requested = nil
	message, err := Render(topic, Persona(domain.RoleReviewer, ""), event)
	if err != nil {
		t.Fatalf("render a repair request: %v", err)
	}
	if !strings.Contains(message.Body, "3 findings") {
		t.Fatalf("body %q does not count the findings", message.Body)
	}
	if !strings.Contains(message.Body, "in the record rather than here") {
		t.Fatalf("body %q does not say where what each finding asks for is", message.Body)
	}
	if strings.Contains(message.Body, "\n- ") {
		t.Fatalf("body %q lists changes the record does not hold", message.Body)
	}
}

func TestAnExchangeSaysWhereItIsAndHowItEnded(t *testing.T) {
	if got := roundsOf(Detail{Round: 2, Rounds: 5}); got != "round 2 of 5" {
		t.Fatalf("rounds = %q", got)
	}
	if got := roundsOf(Detail{Round: 2}); got != "round 2" {
		t.Fatalf("rounds with no cap = %q", got)
	}
	if got := roundsOf(Detail{}); got != "an unrecorded round" {
		t.Fatalf("unrecorded rounds = %q", got)
	}
	if got := outcomeOf(Detail{}); got != "resolved" {
		t.Fatalf("a settled exchange closed %q", got)
	}
	unresolved := outcomeOf(Detail{Unresolved: "which branch the change belongs on"})
	if !strings.HasPrefix(unresolved, "unresolved at its round cap") || !strings.HasSuffix(unresolved, "which branch the change belongs on") {
		t.Fatalf("an exchange out of rounds closed %q", unresolved)
	}
}

func TestABodyTooLongIsCutWithTheRecordThatHoldsTheWhole(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	event := fullyRecorded(KindReportFiled)
	event.Text = strings.Repeat("a very long observation. ", 400)
	message, err := Render(topic, Persona(domain.RoleDeveloper, ""), event)
	if err != nil {
		t.Fatalf("render an oversized report: %v", err)
	}
	if len(message.Body) > MaxBodyBytes {
		t.Fatalf("body is %d bytes, limit is %d", len(message.Body), MaxBodyBytes)
	}
	if !strings.Contains(message.Body, "run-4d1f") || !strings.Contains(message.Body, "cut") {
		t.Fatalf("a cut body does not name the record that holds the whole: %q", message.Body[len(message.Body)-80:])
	}
	// A reader given a truncated account can go to the record for the rest; a
	// reader given no idea who holds the ball has nothing to go to, so the cut
	// takes the account rather than the clause.
	if !strings.HasSuffix(message.Body, nextMoveLead+nextMoves[KindReportFiled]) {
		t.Fatalf("a cut body lost whose move follows it: %q", message.Body[len(message.Body)-80:])
	}
}

// The envelope carries what the topic is called beside the key it is addressed
// by, so whatever posts the message can head a thread in words a reader knows
// without ever asking the tracker what an identifier means.
func TestTheEnvelopeCarriesWhatTheTopicIsCalled(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.5")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	message, err := Render(topic.WithTitle("Slack run-started messages speak as the selector"),
		Harness(), fullyRecorded(KindRunStarted))
	if err != nil {
		t.Fatalf("render a titled topic: %v", err)
	}
	if message.Topic != topic.Key() {
		t.Fatalf("addressed to %q, want the key alone: %q", message.Topic, topic.Key())
	}
	if message.TopicTitle != "Slack run-started messages speak as the selector" {
		t.Fatalf("topic title = %q, want what the record calls the item", message.TopicTitle)
	}
	// A topic built by hand rather than through WithTitle is bounded here, so
	// what the envelope carries is a header line whichever way it was assembled.
	overlong, err := Render(Topic{Kind: TopicWorkItem, ID: "yoyodyne-ifd.118", Title: strings.Repeat("long ", 200)},
		Harness(), fullyRecorded(KindRunStarted))
	if err != nil {
		t.Fatalf("render an oversized title: %v", err)
	}
	if len(overlong.TopicTitle) > MaxTopicTitleBytes {
		t.Fatalf("topic title is %d bytes, limit is %d", len(overlong.TopicTitle), MaxTopicTitleBytes)
	}
	// A record with no title says nothing about one rather than heading a thread
	// with a blank.
	plain, err := Render(topic, Harness(), fullyRecorded(KindRunStarted))
	if err != nil {
		t.Fatalf("render an untitled topic: %v", err)
	}
	if plain.TopicTitle != "" {
		t.Fatalf("topic title = %q, want nothing where the record carried nothing", plain.TopicTitle)
	}
}

func TestRenderRefusesWhatItHasNoWordsFor(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	if _, err := Render(topic, Speaker{Role: "chief-architect"}, fullyRecorded(KindChecksPassed)); err == nil {
		t.Fatal("rendered a speaker nothing has a voice for")
	}
	if _, err := Render(topic, Harness(), Event{Kind: "run.exploded", At: moment, Severity: report.SeverityNote}); err == nil {
		t.Fatal("rendered a kind nobody wrote words for")
	}
	if _, err := Render(Topic{Kind: TopicWorkItem}, Harness(), fullyRecorded(KindChecksPassed)); err == nil {
		t.Fatal("rendered a message addressed to nothing")
	}
}

func TestSubstitutionRefusesALineNamingSomethingNoRecordHolds(t *testing.T) {
	fields := map[string]string{"item": "yoyodyne-ifd.68.2"}
	if got, err := substitute("on {item}", fields); err != nil || got != "on yoyodyne-ifd.68.2" {
		t.Fatalf("substitute = %q, %v", got, err)
	}
	if _, err := substitute("on {nothing}", fields); err == nil {
		t.Fatal("substituted a field no record holds")
	}
	if _, err := substitute("on {item", fields); err == nil {
		t.Fatal("substituted an unclosed placeholder")
	}
}

// A dropped merge is the development manager's to move, in what follows the
// message and in every persona's own line: a repair, a re-run, or a re-arm is
// hers to decide and the harness's to carry out, and none of them is on the
// closed list of acts only a person can perform.
func TestADroppedMergeNamesTheDevelopmentManagerAsMovingNext(t *testing.T) {
	t.Parallel()

	if move := nextMoves[KindMergeDropped]; !strings.HasPrefix(move, "the development manager's") {
		t.Fatalf("whose move follows a dropped merge = %q, want the development manager's", move)
	}
	for persona, spoken := range voices {
		line := spoken.lines[KindMergeDropped]
		for _, refused := range []string{"somebody", "a person", "by hand", "the operator"} {
			if strings.Contains(line, refused) {
				t.Errorf("%s says a dropped merge waits on %q: %q", persona, refused, line)
			}
		}
	}
}
