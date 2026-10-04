package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// testProduct is the product every sink in these tests reports on, and
// testAppearance is how its speakers appear because of it: the shipped name and
// picture, with the product after the name.
const testProduct domain.ProductID = "yoyodyne"

var testAppearance = notify.Appearance{Product: testProduct}

// One thread per topic, held across restarts. The first thing said about a work
// item opens its thread and everything else about it replies into that thread —
// which is what makes a channel readable when three items are in flight at once.
func TestATopicOpensOneThreadAndEverythingElseRepliesIntoIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
		milestone(2, notify.KindChecksPassed),
	}}
	sink := newTestSink(t, root, feed, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 3 {
		t.Fatalf("posts = %d, want the thread opened and both milestones in it", len(posts.requests))
	}
	if posts.requests[0].ThreadTS != "" || !strings.Contains(posts.requests[0].Text, "yoyodyne-ifd.68.3") {
		t.Fatalf("first post = %#v, want the thread opened by naming the topic", posts.requests[0])
	}
	for _, reply := range posts.requests[1:] {
		if reply.ThreadTS != posts.timestamps[0] {
			t.Fatalf("reply = %#v, want it inside the thread the topic opened", reply)
		}
	}

	// A restart is a second sink over the same durable state. It must say
	// nothing further: every one of these transitions has already been said.
	posts.requests = nil
	if err := newTestSink(t, root, feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	if len(posts.requests) != 0 {
		t.Fatalf("second pass posted %#v, want a thread that is a narrative rather than a repetition", posts.requests)
	}
}

// A title recorded with a notification cannot substitute for the current
// tracker listing. Without that listing, the header marks its title unavailable.
func TestAThreadHeaderDoesNotReuseAHistoricalTitle(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	titled := milestone(1, notify.KindRunStarted)
	titled.Notification.Topic = titled.Notification.Topic.WithTitle(
		"Slack run-started messages speak as the role that actually selected the run")
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{titled}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the milestone in it", len(posts.requests))
	}
	want := "*title unavailable (yoyodyne-ifd.68.3)*"
	if posts.requests[0].Text != want {
		t.Fatalf("header = %q, want %q", posts.requests[0].Text, want)
	}
}

// A sink with no listing keeps the identifier and marks its title unavailable.
func TestAThreadWithoutAListingMarksTheTitleUnavailable(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
	}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if posts.requests[0].Text != "*title unavailable (yoyodyne-ifd.68.3)*" {
		t.Fatalf("header = %q, want the identifier with its title unavailable", posts.requests[0].Text)
	}
}

// The older title-only lookup still runs once when opening a thread, but
// cannot supply the current priority and labels the header needs.
func TestATitleOnlyLookupDoesNotReplaceTheCurrentListing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	titles := &fixedTitles{titles: map[string]string{
		"yoyodyne-ifd.68.3": "Park the Codex adapter until the provider answers",
	}}
	feed := &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindItemReprioritized),
		milestone(2, notify.KindRunParked),
	}}
	sink := newTestSinkWithTitles(t, root, feed, posts, titles)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	want := "*title unavailable (yoyodyne-ifd.68.3)*"
	if posts.requests[0].Text != want {
		t.Fatalf("header = %q, want %q", posts.requests[0].Text, want)
	}
	// A thread is opened once and the map that says so is durable, so the tracker
	// is asked once however many messages the thread goes on to carry — and never
	// again after a restart.
	if len(titles.asked) != 1 || titles.asked[0] != "yoyodyne-ifd.68.3" {
		t.Fatalf("asked the tracker %#v, want the one item whose thread was opened", titles.asked)
	}
	feed.deliveries = append(feed.deliveries, milestone(3, notify.KindChecksPassed))
	if err := newTestSinkWithTitles(t, root, feed, posts, titles).pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	if len(titles.asked) != 1 {
		t.Fatalf("asked the tracker %#v, want a thread that is named once", titles.asked)
	}
}

// A tracker that will not say what an item is called costs the header its title
// and nothing else. Reporting is never a gate, and a thread nobody opened is a
// whole narrative missing rather than a name.
func TestATrackerThatWillNotSayWhatAnItemIsCalledStillOpensTheThread(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	titles := &fixedTitles{err: errors.New("bd show failed: no work item yoyodyne-ifd.68.3")}
	said := ""
	sink := newTestSinkWithTitles(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindItemReprioritized),
	}}, posts, titles)
	sink.log = func(format string, args ...any) { said += fmt.Sprintf(format, args...) }

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the milestone in it", len(posts.requests))
	}
	if posts.requests[0].Text != "*title unavailable (yoyodyne-ifd.68.3)*" {
		t.Fatalf("header = %q, want the identifier with its title unavailable", posts.requests[0].Text)
	}
	if !strings.Contains(said, "would not say what yoyodyne-ifd.68.3 is called") {
		t.Fatalf("the sink's log = %q, want it to say why the header carries no title", said)
	}
}

// Each persona speaks under its own name and face. One sink posts for all of
// them, so a channel where they all arrived as one app would leave the voice as
// the only thing telling the speakers apart.
func TestEachPersonaPostsUnderItsOwnDisplayIdentity(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: reportStream,
		Cursor: Cursor{Position: 1},
		Notification: notify.Notification{
			Topic:   workItemTopic(t, "yoyodyne-ifd.68.3"),
			Speaker: notify.Persona(domain.RoleDeveloper, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: report.SeverityWarning,
				Text:     "the replay conflicted with the merged package",
			},
		},
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the report in it", len(posts.requests))
	}
	filed := posts.requests[1]
	identity := testAppearance.Identity(notify.Persona(domain.RoleDeveloper, ""))
	if filed.Username != identity.Name || filed.IconEmoji != identity.Avatar {
		t.Fatalf("post = %#v, want it under the developer's own name and face", filed)
	}
	// The words are the notifier's, severity included, and the sink changes none
	// of them.
	if !strings.Contains(filed.Text, "Warning") || !strings.Contains(filed.Text, "the replay conflicted") {
		t.Fatalf("post text = %q, want the rendered message carried through unchanged", filed.Text)
	}
	// The thread is opened by the harness rather than by whichever persona
	// happened to speak first: opening a thread is nobody's account of anything.
	if posts.requests[0].Username != testAppearance.Identity(notify.Harness()).Name {
		t.Fatalf("thread opened by %q, want the harness", posts.requests[0].Username)
	}
}

// A project may choose the picture beside each name, and Slack takes the two
// shapes in two different fields. A shortcode goes in one and an image in the
// other, never both on one post, or the call has said the same thing twice.
func TestAConfiguredAvatarIsPostedInTheFieldItsShapeBelongsIn(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: reportStream,
		Cursor: Cursor{Position: 1},
		Notification: notify.Notification{
			Topic:   workItemTopic(t, "yoyodyne-ifd.68.6"),
			Speaker: notify.Persona(domain.RoleDeveloper, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: report.SeverityNote,
				Text:     "the avatar is the picture and nothing else",
			},
		},
	}}}, posts)
	// The harness gets an image and the developer a shortcode, so one pass
	// exercises both fields — the thread header is the harness's own post.
	sink.appearance.Avatars = notify.Avatars{
		notify.HarnessSpeaker:        "https://example.invalid/faces/harness.png",
		string(domain.RoleDeveloper): ":ship-it:",
	}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the report in it", len(posts.requests))
	}
	opened := posts.requests[0]
	if opened.IconURL != "https://example.invalid/faces/harness.png" || opened.IconEmoji != "" {
		t.Errorf("thread opened as %#v, want the configured image in icon_url alone", opened)
	}
	filed := posts.requests[1]
	if filed.IconEmoji != ":ship-it:" || filed.IconURL != "" {
		t.Errorf("report posted as %#v, want the configured shortcode in icon_emoji alone", filed)
	}
	// The picture moved and nothing else did: the name is still the developer's.
	if filed.Username != testAppearance.Identity(notify.Persona(domain.RoleDeveloper, "")).Name {
		t.Errorf("report posted as %q, want it still under the developer's own name", filed.Username)
	}
}

// Every name a sink posts under says which product it is reporting on, the
// thread header the harness opens included. An operator develops more than one
// product, and where two harnesses are read in one channel the name is the only
// thing a message carries that says which of them is talking.
func TestEveryNameASinkPostsUnderCarriesItsProduct(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: reportStream,
		Cursor: Cursor{Position: 1},
		Notification: notify.Notification{
			Topic:   workItemTopic(t, "yoyodyne-ifd.68.13"),
			Speaker: notify.Persona(domain.RoleDevelopmentManager, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: report.SeverityNote,
				Text:     "which harness is talking is the boundary that matters most",
			},
		},
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the report in it", len(posts.requests))
	}
	if want := "Yoyodyne (yoyodyne)"; posts.requests[0].Username != want {
		t.Errorf("thread opened by %q, want %q", posts.requests[0].Username, want)
	}
	if want := "Development Manager (yoyodyne)"; posts.requests[1].Username != want {
		t.Errorf("report posted by %q, want %q", posts.requests[1].Username, want)
	}
	for _, post := range posts.requests {
		if !strings.HasSuffix(post.Username, " ("+string(testProduct)+")") {
			t.Errorf("post = %#v, want a name saying which product is talking", post)
		}
	}
}

// The product a sink names its speakers for is the one its own state is kept
// under, taken from the store rather than given beside it. A sink cannot
// therefore hold one product's threads and post another product's name, which
// is a channel of misattributed messages nothing would detect.
func TestASinkIsNamedForTheProductItsStoreIsFor(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "context-conductor")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	api, err := NewAPI("xoxb-test", "xapp-test")
	if err != nil {
		t.Fatalf("NewAPI() error = %v", err)
	}
	sink, err := New(Options{Channel: "C1", Store: store, API: api, Feed: &fixedFeed{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := sink.appearance.Product; got != "context-conductor" {
		t.Errorf("sink names its speakers for %q, want the product its store is for", got)
	}
	if want := "Yoyodyne (context-conductor)"; sink.appearance.Identity(notify.Harness()).Name != want {
		t.Errorf("harness posts as %q, want %q", sink.appearance.Identity(notify.Harness()).Name, want)
	}
}

// A sink with no store has nowhere to keep its cursors and no product to name
// its speakers for, so it is refused at assembly rather than discovered in a
// channel.
func TestASinkWithoutAStoreIsRefusedAtAssembly(t *testing.T) {
	t.Parallel()

	api, err := NewAPI("xoxb-test", "xapp-test")
	if err != nil {
		t.Fatalf("NewAPI() error = %v", err)
	}
	if _, err := New(Options{Channel: "C1", API: api, Feed: &fixedFeed{}}); err == nil {
		t.Fatal("New() without a store = nil, want a refusal")
	}
}

// A speaker nothing was configured for keeps the avatar the harness ships, so a
// project that named one persona's picture has not blanked the rest.
func TestASpeakerWithNoConfiguredAvatarKeepsTheShippedOne(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
	}}, posts)
	sink.appearance.Avatars = notify.Avatars{string(domain.RoleDeveloper): ":ship-it:"}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	shipped := notify.Harness().Identity().Avatar
	for _, post := range posts.requests {
		if post.IconEmoji != shipped || post.IconURL != "" {
			t.Errorf("post = %#v, want the harness's shipped avatar %q", post, shipped)
		}
	}
}

// A message is posted and then its cursor advances, so a sink that dies between
// the two repeats a message rather than losing one. The durable record is
// authoritative and this is a view of it, so a repetition is the right side of
// that trade.
func TestAMessageThatCouldNotBePostedIsPostedOnTheNextPass(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// The thread's own message and the first milestone land; everything after
	// them is refused, which is what a workspace going away looks like.
	posts := &recordedPosts{allow: 2}
	feed := &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
		milestone(2, notify.KindPromoted),
	}}
	sink := newTestSink(t, root, feed, posts)

	if err := sink.pass(context.Background()); err == nil {
		t.Fatal("pass() = nil, want the refusal reported so the sink backs off")
	}
	posts.allow = 0
	posts.requests = nil
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("second pass posted %d, want only the message that never landed", len(posts.requests))
	}
	if !strings.Contains(posts.requests[0].Text, "promoted") {
		t.Fatalf("second pass posted %q, want the message the first pass could not", posts.requests[0].Text)
	}
}

// What is about the whole line — not any one item — is posted at the top level.
// Burying it in one item's thread would misfile it.
func TestProductLevelNewsIsNotBuriedInAnItemsThread(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream:       productStream,
		Cursor:       Cursor{Position: 1},
		Notification: notify.IntakeReleased(time.Now(), runstate.IntakeRelease{}, false),
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("posts = %d, want one unthreaded message and no thread opened for it", len(posts.requests))
	}
	if posts.requests[0].ThreadTS != "" {
		t.Fatalf("post = %#v, want product news at the top level of the channel", posts.requests[0])
	}
}

// The main channel view hides thread replies by design, and what decides which
// of them are shown there anyway is the reach the envelope carries.
//
// A filed report reaches its item's thread and no further, whether it was said
// as a note or as a warning: it asks for nothing, the report store holds it, and
// the summaries built from that store are what carry it to the operator. It was
// the severity until the survey that counted 320 individual report pushes
// against under forty posts of the kinds somebody actually has to act on.
//
// A critical report is the exception, and it is the operator's own rule: severity
// is importance, so something already wrong that will cost somebody is shown
// where he is reading whatever its kind says.
func TestOnlyReportsOfSomethingAlreadyBrokenReachTheChannel(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		filedReport(1, report.SeverityNote, "the replay was clean"),
		filedReport(2, report.SeverityWarning, "the replay conflicted with the merged package"),
		filedReport(3, report.SeverityCritical, "the target branch has diverged under the change"),
	}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 4 {
		t.Fatalf("posts = %d, want the thread opened and all three reports in it", len(posts.requests))
	}
	// The message a thread hangs from is already in the channel, so asking for it
	// to be sent there as well is a flag that says nothing.
	if posts.requests[0].ReplyBroadcast {
		t.Fatalf("thread header = %#v, want no broadcast on a message that is not a reply", posts.requests[0])
	}
	for index, want := range []bool{false, false, true} {
		reply := posts.requests[index+1]
		if reply.ThreadTS != posts.timestamps[0] {
			t.Fatalf("reply = %#v, want every severity still inside the topic's thread", reply)
		}
		if reply.ReplyBroadcast != want {
			t.Fatalf("reply %q broadcast = %v, want %v", reply.Text, reply.ReplyBroadcast, want)
		}
	}
}

// Product-level news is already at the top of the channel, so nothing about its
// severity turns it into a reply Slack is asked to send there twice.
func TestProductLevelNewsIsNeverBroadcastBackIntoTheChannel(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: reportStream,
		Cursor: Cursor{Position: 1},
		Notification: notify.Notification{
			Topic:   notify.Product(),
			Speaker: notify.Persona(domain.RoleDeveloper, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: report.SeverityCritical,
				Text:     "the provider refused every account",
			},
		},
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("posts = %d, want one unthreaded message", len(posts.requests))
	}
	if posts.requests[0].ThreadTS != "" || posts.requests[0].ReplyBroadcast {
		t.Fatalf("post = %#v, want product news posted once at the top level", posts.requests[0])
	}
}

// The operator's motivating case, replayed: a report that is neither important
// nor asking for anything, filed against no work item, and worn a warning icon
// at the top of the channel. There is no thread to say it in, so saying it at all
// means saying it at the level he reads — which is the one place it must not be.
// It advances its cursor, the report store holds it, and the summaries built from
// that store are what carry it.
func TestAnUnimportantReportWithNoItemPostsNothing(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: reportStream,
		Cursor: Cursor{Position: 1},
		Notification: notify.Notification{
			Topic:   notify.Product(),
			Speaker: notify.Persona(domain.RoleDeveloper, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: report.SeverityWarning,
				Text:     "nobody is next on this",
			},
		},
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 0 {
		t.Fatalf("posts = %#v, want nothing in the channel", posts.requests)
	}
	cursors, err := sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if cursors.Streams[reportStream].Position != 1 {
		t.Fatalf("cursor = %#v, want the report read past rather than re-read every pass", cursors.Streams[reportStream])
	}
}

// A message leads back to the durable record it was read from rather than
// standing in for it.
func TestAMessageNamesTheRecordItWasReadFrom(t *testing.T) {
	t.Parallel()

	rendered, err := notify.Render(workItemTopic(t, "yoyodyne-ifd.68.3"), notify.Harness(), notify.Event{
		Kind:     notify.KindPromoted,
		At:       time.Now(),
		Severity: report.SeverityNote,
		Refs:     notify.Refs{RunID: "run-a", WorkItemID: "yoyodyne-ifd.68.3"},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if text := renderText(rendered); !strings.Contains(text, "run run-a") {
		t.Fatalf("rendered = %q, want the durable record named", text)
	}
}

// The reference line under a message names what a reader follows, and a
// directive is not one of them. It is the second half of what an operator was
// shown twice in one acknowledgment: the voice line said the identifier and this
// line said it again, italicised, under a message answering a sentence he had
// just typed himself.
func TestTheReferenceLineNamesNoDirective(t *testing.T) {
	t.Parallel()

	const recordedDirective = "directive-f007fa2734c8b1ee9d5a6470c1b2e8a3"
	reference := renderRefs(notify.Refs{
		RunID:       "run-a",
		WorkItemID:  "yoyodyne-ifd.68.26",
		DirectiveID: recordedDirective,
	})
	if strings.Contains(reference, recordedDirective) {
		t.Fatalf("reference line = %q, want the directive's identifier left to the durable record", reference)
	}
	if !strings.Contains(reference, "run run-a") {
		t.Fatalf("reference line = %q, want the records a reader does follow still named", reference)
	}
	// A message whose only reference is the directive has nothing to point at,
	// and an empty italic line under it would be decoration standing in for one.
	if trailing := renderRefs(notify.Refs{DirectiveID: recordedDirective}); trailing != "" {
		t.Fatalf("reference line = %q, want no line at all where nothing is left to name", trailing)
	}
}

// The message the operator read, replayed.
//
// What he was shown of a priority change was an item said as yoyodyne-ifd.102.7,
// a paragraph of the reasoning behind it, and a conversation identifier trailing
// the whole thing — three ways of making a reader resolve something to find out
// what happened. This is the same durable record, posted the way the channel
// posts it now: the thread's header carries the identity, and the message inside
// it names the work, says where it went, and stops.
func TestThePriorityChangeTheOperatorReadNamesTheWorkAndNothingElse(t *testing.T) {
	t.Parallel()

	const (
		item        = "yoyodyne-ifd.102.7"
		chat        = "chat-91253e0e070c17b0663651cc48602122"
		named       = "Re-arm the dropped-merge check"
		firstOfIt   = "The dropped merge left the check disarmed."
		restOfIt    = "It sits below the rendering work rather than above it"
		itsPriority = "priority 2"
	)
	recorded, err := execution.NewEvent(chat, 1, time.Now(), execution.EventTrackerActionApplied, "harness.chat", map[string]any{
		"action_id": "t1.1",
		"turn":      187,
		"action": map[string]any{
			"action":   "reprioritize",
			"id":       item,
			"priority": 2,
			"reason":   firstOfIt + " " + restOfIt + ", and nothing else in the epic depends on it.",
		},
		"work_item_id":    item,
		"work_item_title": named,
		"summary":         "the harness's own account of it",
	})
	if err != nil {
		t.Fatalf("record the tracker action: %v", err)
	}
	conversation := runstate.Conversation{
		SchemaVersion:  runstate.ConversationSchemaVersion,
		ConversationID: chat,
		ProductID:      testProduct,
		RepositoryID:   "yoyodyne",
		Agent:          string(domain.RoleProductManager),
		Role:           domain.RoleProductManager,
		Backend:        domain.BackendClaudeCode,
		StartedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	notification, err := notify.FromConversation(conversation, []execution.Event{recorded}, 0)
	if err != nil {
		t.Fatalf("select the reprioritization: %v", err)
	}

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream:       "conversation:" + chat,
		Cursor:       Cursor{Position: 1},
		Notification: notification,
	}}}, posts)
	sink.citing = &titleIndex{read: func(context.Context) (*readmodel.WorkItemTitles, error) {
		return readmodel.NewWorkItemTitles([]beads.WorkItem{{ID: item, Title: named, Priority: 2, Labels: []string{"reliability"}}}), nil
	}}
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread opened and the change said in it", len(posts.requests))
	}
	header, said := posts.requests[0].Text, posts.requests[1].Text

	// Both the header and the message carry the complete current citation.
	if !strings.Contains(header, item) || !strings.Contains(header, named) {
		t.Fatalf("header = %q, want the identifier and what the item is called", header)
	}
	if !strings.Contains(said, named) {
		t.Fatalf("the change reads as %q, which does not name the work", said)
	}
	citation := "(P2, reliability) " + named + " (" + item + ")"
	if !strings.Contains(header, citation) || !strings.Contains(said, citation) {
		t.Fatalf("header %q and message %q must both carry %q", header, said, citation)
	}
	if strings.Contains(said, chat) {
		t.Fatalf("the change reads as %q, which trails a conversation identifier nobody reading acts on", said)
	}
	if !strings.Contains(said, itsPriority) {
		t.Fatalf("the change reads as %q, which does not say where the item went", said)
	}
	// One sentence of the reasoning, and the record for the rest of it.
	if !strings.Contains(said, firstOfIt) {
		t.Fatalf("the change reads as %q, which does not say why", said)
	}
	if strings.Contains(said, restOfIt) {
		t.Fatalf("the change reads as %q, which carries the whole argument the record holds", said)
	}
	if !strings.Contains(said, "in the item's record") {
		t.Fatalf("the change reads as %q, which does not say where the rest of the reason is", said)
	}
}

// A message too long for Slack is cut with a marker naming the record that holds
// the whole of it, and never split into a flood of messages to fit.
func TestAnOversizedMessageIsTruncatedAndSaysWhereTheRestIs(t *testing.T) {
	t.Parallel()

	text := truncate(strings.Repeat("a", maxTextBytes+2048), notify.Refs{RunID: "run-a"})
	if len(text) > maxTextBytes {
		t.Fatalf("rendered %d bytes, want it inside the limit", len(text))
	}
	if !strings.Contains(text, "run-a") {
		t.Fatalf("rendered %q, want the marker to name the record that holds the whole", text)
	}
}

// A record nothing can be said about must not hold up every later message on
// every stream forever, so it is said once in the log and its cursor moves past
// it. A workspace that refused the post is the other case entirely, and the two
// must not be confused: that one is retried.
func TestARecordNothingCanBeSaidAboutIsSkippedRatherThanRepeatedForever(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	var logged []string
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		// An event with no kind has no voice line in any persona, so nothing
		// could be said about it whatever the workspace does.
		{Stream: "run:run-a", Cursor: Cursor{Position: 1}, Notification: notify.Notification{
			Topic:   workItemTopic(t, "yoyodyne-ifd.68.3"),
			Speaker: notify.Harness(),
			Event:   notify.Event{Kind: "nothing.happened", At: time.Now(), Severity: report.SeverityNote},
		}},
		milestone(2, notify.KindRunStarted),
	}}, posts)
	sink.log = func(format string, args ...any) { logged = append(logged, format) }

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(logged) == 0 {
		t.Fatal("a notification that could not be said must be said out loud")
	}
	// The thread and the message that followed it still went out.
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the rest of the stream unaffected", len(posts.requests))
	}
}

// A delivery that carries nothing to say is a cursor advance and no more. It is
// how a run that was over before the sink started stops being carried, and it
// must not put a message nobody asked for into the channel.
func TestASilentDeliveryAdvancesTheCursorWithoutSayingAnything(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	sink := newTestSink(t, root, &fixedFeed{deliveries: []Delivery{
		{Stream: "run:run-a", Cursor: Cursor{Closed: true, Position: 1}},
	}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 0 {
		t.Fatalf("posts = %#v, want nothing said about a run nothing was said about", posts.requests)
	}
	cursors, err := sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if !cursors.Streams["run:run-a"].Closed {
		t.Fatalf("cursor = %#v, want the run closed so it stops being carried", cursors.Streams["run:run-a"])
	}
}

// The moment this product's reporting begins at is taken once, ever, written
// before anything is read, and never taken again. A sink that took it afresh on
// every start would carry it forward past every outage, and everything filed
// while it was down would then be older than the restart and read past as
// history — which is the one record somebody coming back most needs to see.
func TestTheWatermarkIsTakenOnceAndSurvivesEveryRestart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := newTestSinkAt(t, root, &fixedFeed{}, &recordedPosts{}, moment)
	if err := first.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	cursors, err := first.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if !cursors.Since.Equal(moment) {
		t.Fatalf("watermark = %s, want the moment the sink was first pointed at this product", cursors.Since)
	}

	// A second process, a day later, over the same durable state.
	restarted := newTestSinkAt(t, root, &fixedFeed{}, &recordedPosts{}, moment.Add(24*time.Hour))
	if err := restarted.pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	cursors, err = restarted.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if !cursors.Since.Equal(moment) {
		t.Fatalf("watermark = %s, want it unmoved by a restart", cursors.Since)
	}
}

// A sink assembled with a missing piece would discover it with a run in flight,
// which is the one moment nobody is watching the reporting process.
func TestASinkWithAMissingPieceIsRefusedAtAssembly(t *testing.T) {
	t.Parallel()

	if _, err := New(Options{}); err == nil {
		t.Fatal("New() = nil, want every missing piece named at once")
	}
	store, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	api, err := NewAPI("xoxb-test", "xapp-test")
	if err != nil {
		t.Fatalf("NewAPI() error = %v", err)
	}
	if _, err := New(Options{Store: store, API: api, Feed: &fixedFeed{}}); err == nil {
		t.Fatal("New() without a channel = nil, want a refusal")
	}
}

// A single pass posts, so it is as capable of doubling a channel as a running
// sink is. It holds the same lease, and the lease is what makes "do not run
// two" a property rather than a warning in a document.
func TestASinglePassHoldsTheSameLeaseARunningSinkDoes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{milestone(1, notify.KindRunStarted)}}
	running := newTestSink(t, root, feed, posts)
	release, err := running.hold()
	if err != nil {
		t.Fatalf("hold() error = %v", err)
	}

	if err := newTestSink(t, root, feed, posts).Once(context.Background()); err == nil {
		t.Fatal("Once() = nil, want a pass alongside a running sink refused")
	}
	if len(posts.requests) != 0 {
		t.Fatalf("posts = %#v, want nothing posted by the sink that was refused", posts.requests)
	}

	release()
	if err := newTestSink(t, root, feed, posts).Once(context.Background()); err != nil {
		t.Fatalf("Once() after the sink stopped error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the milestone once the lease was free", len(posts.requests))
	}
}

// A refusal only a person can clear — an app nobody invited to the channel — is
// said once and then waited out quietly. Saying it every pass would put the same
// line in the log every few seconds for as long as the process runs, which is
// how a log stops being read; and it must still be retried, because what clears
// it happens in Slack rather than here.
func TestARefusalOnlyAPersonCanClearIsSaidOnceAndThenWaitedOut(t *testing.T) {
	t.Parallel()

	posts := &refusingPosts{code: "not_in_channel"}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
	}}, &recordedPosts{})
	sink.api = newTestAPI(t, posts.handle)
	// The waits are driven rather than spent: what is being tested is how often
	// the sink speaks, not how long it sleeps between attempts.
	passes := stepPasses(sink)

	var mutex sync.Mutex
	var refusals int
	sink.log = func(format string, _ ...any) {
		mutex.Lock()
		defer mutex.Unlock()
		if strings.Contains(format, "keep refusing") {
			refusals++
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sink.deliver(ctx)
	}()
	// Let the workspace refuse several times over, so a sink that said it every
	// pass would have said it several times.
	passes.run(4)
	cancel()
	<-done

	if attempts := posts.attempts(); attempts != 4 {
		t.Fatalf("the workspace was asked %d time(s), want one attempt per pass over 4 passes", attempts)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if refusals != 1 {
		t.Fatalf("the refusal was reported %d time(s) over %d attempts, want it said once", refusals, posts.attempts())
	}
}

// The other half of saying it once: reporting has to come back by itself when
// the operator fixes it, and say so, or a channel that went quiet for a reason
// nobody remembers looks like one that broke.
func TestReportingSaysSoWhenItStartsWorkingAgain(t *testing.T) {
	t.Parallel()

	posts := &refusingPosts{code: "not_in_channel"}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
	}}, &recordedPosts{})
	sink.api = newTestAPI(t, posts.handle)
	passes := stepPasses(sink)

	var mutex sync.Mutex
	var recovered int
	sink.log = func(format string, _ ...any) {
		mutex.Lock()
		defer mutex.Unlock()
		if strings.Contains(format, "accepting messages again") {
			recovered++
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sink.deliver(ctx)
	}()
	passes.run(2)
	posts.invite() // the operator invites the app to the channel
	passes.run(1)
	cancel()
	<-done

	mutex.Lock()
	defer mutex.Unlock()
	if recovered != 1 {
		t.Fatalf("reporting coming back was said %d time(s) over %d attempts, want it said once on the pass after the invitation", recovered, posts.attempts())
	}
}

// The process an operator leaves running has to stop when they stop it. A sink
// that kept its terminal until a read deadline passed would look hung at exactly
// the moment somebody had decided to intervene.
//
// How promptly it stops is read off what it did rather than off a clock: a sink
// stopped before it started reads its context before the lease, the identity
// call, and the presence record, so the workspace is never asked anything. The
// five seconds this used to allow was a guess at how long those took on a
// loaded machine, and a wrong one.
func TestStoppingTheSinkStopsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	sink := newTestSink(t, root, &fixedFeed{}, posts)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sink.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v, want a stopped sink to be a clean exit", err)
	}
	if asked := posts.identified(); asked != 0 {
		t.Fatalf("the workspace was asked who this app is %d time(s), want a sink stopped before it started to ask nothing", asked)
	}
	// A sink stopped before it started never was, so it records nothing about
	// itself: no presence to write and fsync on the way in, and none to forget
	// on the way out. The write is the one thing on the startup path that waits
	// on the disk, and a stop that waits on the disk is the hang this guards.
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, found, err := store.LoadPresence(); err != nil || found {
		t.Fatalf("LoadPresence() = %t, %v, want a sink stopped before it started to have recorded nothing", found, err)
	}
	if len(posts.requests) != 0 {
		t.Fatalf("a sink stopped before it started posted %d message(s)", len(posts.requests))
	}
}

// countingFeed records how many passes actually read the records.
type countingFeed struct {
	mutex sync.Mutex
	polls int
}

func (f *countingFeed) Poll(_ context.Context, _ Cursors) (Batch, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.polls++
	return Batch{Streams: map[string]struct{}{}}, nil
}

func (f *countingFeed) count() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.polls
}

// A sink stopped before it started does no pass at all, which is what makes the
// stop above prompt rather than merely eventual.
//
// A pass reads every record and posts what is due, so a sink that ran one on its
// way out would return only after that pass had finished — on a loaded machine,
// seconds during which the operator who pressed Ctrl-C has had no sign that
// anything heard them. The connection's own loop beside this one already reads
// the context before its first iteration; this is the delivery loop agreeing
// with it.
func TestASinkStoppedBeforeItStartedDoesNoPass(t *testing.T) {
	t.Parallel()

	feed := &countingFeed{}
	sink := newTestSink(t, t.TempDir(), feed, &recordedPosts{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sink.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v, want a stopped sink to be a clean exit", err)
	}
	if polls := feed.count(); polls != 0 {
		t.Errorf("the records were read %d time(s), want a sink stopped before it started to read nothing", polls)
	}
}

// The channel's top level is a status board. The message a thread hangs from
// carries what its item is doing now, and the mark that has stopped being true
// comes off as the record moves — so a scan of the channel answers what is
// working, what is with the reviewer, and what landed without a thread being
// opened.
func TestAThreadsOpenerCarriesTheItemsStatusAndTheStaleMarkComesOff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	feed := &fixedFeed{
		deliveries: []Delivery{milestone(1, notify.KindRunStarted)},
		statuses:   map[string]notify.Status{"work-item:yoyodyne-ifd.68.3": notify.StatusWorking},
	}
	sink := newTestSink(t, root, feed, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	opener := posts.timestamps[0]
	wantWearing(t, posts, opener, notify.StatusWorking)

	// A status that has not moved is left alone. Most passes are this one, and a
	// sink that re-marked every fifteen seconds would spend a workspace's
	// tolerance saying what it had already said.
	posts.marks = nil
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	if len(posts.marks) != 0 {
		t.Fatalf("marks = %#v, want a status that has not moved marked again by nothing", posts.marks)
	}

	// The record moves and the mark moves with it. Every other status in the
	// vocabulary comes off first — three calls, two of which hit nothing — because
	// what is on the message is the question, and only the sweep answers it
	// without trusting a record that a crash could have left behind.
	feed.statuses["work-item:yoyodyne-ifd.68.3"] = notify.StatusInReview
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("third pass() error = %v", err)
	}
	wantMarks(t, posts.marks,
		mark{method: "reactions.remove", ts: opener, name: notify.StatusWorking.Symbol()},
		mark{method: "reactions.remove", ts: opener, name: notify.StatusBlocked.Symbol()},
		mark{method: "reactions.remove", ts: opener, name: notify.StatusCompleted.Symbol()},
		mark{method: "reactions.add", ts: opener, name: notify.StatusInReview.Symbol()})
	wantWearing(t, posts, opener, notify.StatusInReview)

	// A restart is a second sink over the same durable state: what the opener is
	// already marked with is remembered, so the item moving once more leaves it
	// wearing the new status and nothing else.
	posts.marks = nil
	feed.statuses["work-item:yoyodyne-ifd.68.3"] = notify.StatusCompleted
	if err := newTestSink(t, root, feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("restarted pass() error = %v", err)
	}
	wantWearing(t, posts, opener, notify.StatusCompleted)
}

// The record of which mark is on a thread is written after the workspace has
// taken it, so a sink killed between the two leaves a record naming a status the
// message is not wearing. The mark that is actually there has to come off anyway:
// a removal aimed at what the record named would leave the real one on the opener
// for good, saying "working" under a run that failed hours ago.
func TestAMarkTheRecordDoesNotNameIsStillTakenOff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	feed := &fixedFeed{
		deliveries: []Delivery{milestone(1, notify.KindRunStarted)},
		statuses:   map[string]notify.Status{"work-item:yoyodyne-ifd.68.3": notify.StatusWorking},
	}
	sink := newTestSink(t, root, feed, posts)
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	opener := posts.timestamps[0]
	wantWearing(t, posts, opener, notify.StatusWorking)

	// The write that would have said so never landed: the opener wears working
	// and the durable record still names the status before it.
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	threads, err := store.LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	stale := threads.Threads["work-item:yoyodyne-ifd.68.3"]
	stale.Status = notify.StatusInReview
	threads.Record("work-item:yoyodyne-ifd.68.3", stale)
	if err := store.SaveThreads(threads); err != nil {
		t.Fatalf("SaveThreads() error = %v", err)
	}

	feed.statuses["work-item:yoyodyne-ifd.68.3"] = notify.StatusBlocked
	if err := newTestSink(t, root, feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	wantWearing(t, posts, opener, notify.StatusBlocked)
}

// A sink stopped while a mark is waiting out the pace is not a workspace
// refusing anything. The line it would otherwise log is the one the setup
// document teaches an operator to read as a missing scope, so a shutdown must
// not print it — a diagnosis somebody has to rule out later is worse than
// silence on the way out.
func TestASinkStoppedWhileMarkingSaysNothingAboutARefusal(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{}, posts)
	var said []string
	sink.log = func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The pace is the only blocking call in a mark, so it is where a shutdown is
	// actually met: the wait is made due and the sink stopped inside it.
	sink.pace.next = time.Now().Add(time.Hour)
	sink.pace.sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}

	topic := "work-item:yoyodyne-ifd.68.3"
	threads := ThreadMap{Threads: map[string]Thread{
		topic: {Channel: "C1", ThreadTS: "1755.0001"},
	}}
	sink.mark(ctx, &threads, map[string]notify.Status{topic: notify.StatusWorking})

	if len(said) != 0 {
		t.Fatalf("said %q on the way out, want a shutdown to say nothing about a refusal", said)
	}
	if sink.marking != "" {
		t.Fatalf("marking = %q, want a shutdown not remembered as a standing refusal", sink.marking)
	}
}

// wantWearing checks that a message carries exactly one status and that it is
// this one. Two at once is the failure worth naming: a thread that says both
// working and blocked is worse than one that says neither.
func wantWearing(t *testing.T, posts *recordedPosts, ts string, status notify.Status) {
	t.Helper()
	worn := posts.wearing[ts]
	if len(worn) != 1 || !worn[status.Symbol()] {
		t.Fatalf("the opener wears %v, want %q alone", worn, status.Symbol())
	}
}

// A topic nobody has said anything about has no thread and nothing to mark.
// Opening one to carry a status would put a thread in the channel whose whole
// content is that nothing has happened yet.
func TestATopicWithNoThreadIsNotMarked(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	feed := &fixedFeed{statuses: map[string]notify.Status{
		"work-item:yoyodyne-ifd.68.3": notify.StatusWorking,
	}}
	if err := newTestSink(t, t.TempDir(), feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 0 || len(posts.marks) != 0 {
		t.Fatalf("posted %#v and marked %#v, want an item nothing has been said about left alone", posts.requests, posts.marks)
	}
}

// Marking is not a delivery and it is never a gate. A workspace that refuses the
// reaction — an app installed before the manifest asked for the scope — costs
// the channel its status board and not one message; and because nothing durable
// says the mark went on, it goes on by itself once somebody reinstalls, without
// the item having to move again.
func TestAWorkspaceThatRefusesAMarkStillGetsEveryMessage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{refuseMarks: "missing_scope"}
	feed := &fixedFeed{
		deliveries: []Delivery{milestone(1, notify.KindRunStarted)},
		statuses:   map[string]notify.Status{"work-item:yoyodyne-ifd.68.3": notify.StatusBlocked},
	}
	sink := newTestSink(t, root, feed, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v, want a refused mark to cost the pass nothing", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread opened and the milestone in it", len(posts.requests))
	}

	posts.refuseMarks = ""
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	wantWearing(t, posts, posts.timestamps[0], notify.StatusBlocked)
}

// What became of a directive somebody asked for in a thread is the one message
// the pass posts for a person rather than for the channel, and both halves of
// that have to survive the pass: the text reaches them by name, and the reply
// they typed stops wearing the thinking face at the moment the outcome is said.
func TestAnOutcomeThePassSaysTagsWhoAskedAndSettlesTheMarkOnTheirReply(t *testing.T) {
	t.Parallel()

	const member = "U0OPERATOR"
	const askTS = "1750000001.000200"
	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{outcome(1, member, askTS)}}
	if err := newTestSink(t, t.TempDir(), feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}

	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want the thread opened and the outcome said in it", posts.requests)
	}
	said := posts.requests[1]
	if !strings.HasPrefix(said.Text, "<@"+member+"> ") {
		t.Fatalf("said %q, want the outcome to reach the person who asked by name", said.Text)
	}
	if !strings.Contains(said.Text, "the second one, and the design says so") {
		t.Fatalf("said %q, want the tag in front of what became of it rather than instead of it", said.Text)
	}
	if worn := posts.wearing[askTS]; !worn[notify.ReceiptSettled.Symbol()] || len(worn) != 1 {
		t.Fatalf("the reply that asked wears %#v, want the settled mark alone once the outcome was said", worn)
	}
}

// A withdrawal is not a settlement, and the pass must not need it to be one to
// move the mark: the reply that asked has worn the thinking face since the
// directive was recorded, and it stops doing so at the moment the withdrawal is
// said in its thread, exactly as it would for an outcome. The mark is decided by
// the delivery carrying the reply rather than by any table keyed on the kind, so
// what this pins is that the withdrawal delivery carries it and the pass acts on
// it — the reply starts wearing the thinking face here, and ends wearing the
// settled mark alone.
func TestAWithdrawalThePassSaysTagsWhoAskedAndSettlesTheMarkOnTheirReply(t *testing.T) {
	t.Parallel()

	const member = "U0OPERATOR"
	const askTS = "1750000001.000200"
	posts := &recordedPosts{wearing: map[string]map[string]bool{
		askTS: {notify.ReceiptUnderConsideration.Symbol(): true},
	}}
	feed := &fixedFeed{deliveries: []Delivery{withdrawal(1, member, askTS)}}
	if err := newTestSink(t, t.TempDir(), feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}

	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want the thread opened and the withdrawal said in it", posts.requests)
	}
	said := posts.requests[1]
	if !strings.HasPrefix(said.Text, "<@"+member+"> ") {
		t.Fatalf("said %q, want the withdrawal to reach the person who asked by name", said.Text)
	}
	if !strings.Contains(said.Text, "never mind: the work went the other way") {
		t.Fatalf("said %q, want why it was withdrawn", said.Text)
	}
	if !strings.HasPrefix(said.Text, "<@"+member+"> The operator took that back") {
		t.Fatalf("said %q, want it in the voice of the role it was withdrawn under", said.Text)
	}
	worn := posts.wearing[askTS]
	if worn[notify.ReceiptUnderConsideration.Symbol()] {
		t.Fatalf("the reply that asked still wears %#v, want the thinking face cleared once the withdrawal was said", worn)
	}
	if !worn[notify.ReceiptSettled.Symbol()] || len(worn) != 1 {
		t.Fatalf("the reply that asked wears %#v, want the settled mark alone once the withdrawal was said", worn)
	}
}

// A recorded directive is demoted to its thread, and the whole of what makes that
// safe is that it reaches the person by name from inside the thread. Nothing in
// the reach table says that — it is a fact about how these deliveries are built —
// so it is asserted here rather than assumed there: the acknowledgment tags
// whoever asked, and a delivery that carries a mention posts whatever its reach
// would otherwise say.
//
// The second half is the one worth pinning. A directive that settled something
// reaches only the thread by kind, so without the mention exception in
// Delivery.Posts the message telling a person what became of what they typed
// would be dropped by a policy about how much of the channel a milestone is
// worth.
func TestADirectiveThatTagsWhoAskedIsDeliveredWhateverItsReachSays(t *testing.T) {
	t.Parallel()

	const member = "U0OPERATOR"
	settled := outcome(1, member, "1750000000.000100")
	if got := settled.Notification.Reach(); got != notify.ReachThread {
		t.Fatalf("a settled directive reaches %q, want the thread: the person is answered by name", got)
	}
	if !settled.Posts() {
		t.Fatalf("%#v posts nowhere, want the answer somebody is waiting for delivered", settled)
	}
	// Without the mention it is the reach alone that decides, which is what makes
	// the exception load-bearing rather than decorative.
	untagged := settled
	untagged.Mention = ""
	if !untagged.Posts() {
		t.Fatalf("a thread-reach delivery does not post, want the thread to carry it")
	}

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{settled}}, posts)
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	said := posts.requests[len(posts.requests)-1]
	if !strings.HasPrefix(said.Text, "<@"+member+"> ") {
		t.Fatalf("said %q, want it tagged to whoever asked rather than left in a thread they may not have open", said.Text)
	}
	if said.ReplyBroadcast {
		t.Fatalf("said = %#v, want a settled directive to stay in its thread: the tag is what reaches them", said)
	}
}

// A delivery whose reach is the durable record still posts when it answers one
// person by name. It is the exception Delivery.Posts carries, and it is here
// because the record-reach case is the one where the policy and the answer
// disagree outright: a posting policy that swallowed somebody's answer would be
// this surface deciding a person does not need to hear back.
func TestAnAnswerToOnePersonPostsEvenFromTheRecord(t *testing.T) {
	t.Parallel()

	answered := Delivery{
		Stream:  directiveStream,
		Cursor:  Cursor{Position: 1},
		Mention: "U0OPERATOR",
		Notification: notify.Notification{
			Topic:   notify.Product(),
			Speaker: notify.Harness(),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       moment,
				Severity: report.SeverityNote,
				Text:     "an answer that would otherwise reach nobody",
			},
		},
	}
	if got := answered.Notification.Reach(); got != notify.ReachRecord {
		t.Fatalf("reach = %q, want the record, so the exception is the only thing making this post", got)
	}
	if !answered.Posts() {
		t.Fatalf("%#v posts nowhere, want a message addressed to one person delivered to them", answered)
	}
	if answered.Notification.Posts() {
		t.Fatalf("the notification alone posts, so this test would pass without the exception it is for")
	}
}

// outcome is what the feed hands the sink when the record says a directive
// somebody asked for in a thread has been settled: said in their thread, tagged
// to them, and carrying the reply that asked so its mark can move.
func outcome(position uint64, member, replyTS string) Delivery {
	settled := moment
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            "directive-" + strings.Repeat("f", 32),
		ProductID:     testProduct,
		Kind:          directive.KindAmbiguous,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    moment,
		Text:          "ambiguous: which of the two branches did you mean",
		Unresolved:    "which of the two branches did you mean",
		Scope:         []string{"yoyodyne-ifd.68.3"},
		Resolution:    "the second one, and the design says so",
		ResolvedAt:    &settled,
	}
	topic := notify.Topic{Kind: notify.TopicWorkItem, ID: "yoyodyne-ifd.68.3"}
	return Delivery{
		Stream:       directiveStream,
		Cursor:       Cursor{Position: position},
		Mention:      member,
		Reply:        replyTS,
		Notification: acknowledged(topic, notify.KindDirectiveResolved, recorded, settled),
	}
}

// withdrawal is what the feed hands the sink when the record says a directive
// somebody asked for in a thread was taken back: the same shape as an outcome,
// built by the same constructor the feed uses, so what the sink is tested
// against is the delivery it actually gets.
func withdrawal(position uint64, member, replyTS string) Delivery {
	withdrawnAt := moment
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            "directive-" + strings.Repeat("e", 32),
		ProductID:     testProduct,
		Kind:          directive.KindAmbiguous,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    moment,
		Text:          "ambiguous: which of the two branches did you mean",
		Unresolved:    "which of the two branches did you mean",
		Scope:         []string{"yoyodyne-ifd.68.3"},
		Withdrawal:    "never mind: the work went the other way and the question no longer arises",
		WithdrawnBy:   "the operator, from conversation chat-91253e0e, after turn 557",
		WithdrawnAt:   &withdrawnAt,
		WithdrawnRole: domain.RoleProductManager,
	}
	topic := notify.Topic{Kind: notify.TopicWorkItem, ID: "yoyodyne-ifd.68.3"}
	return Delivery{
		Stream:       directiveStream,
		Cursor:       Cursor{Position: position},
		Mention:      member,
		Reply:        replyTS,
		Notification: withdrawn(topic, recorded),
	}
}

// The harness being degraded is the one thing a channel is the wrong place for:
// a channel is somewhere somebody chooses to look, and a resident running a
// binary from before the fix is exactly what nobody thinks to look for. So it
// goes to each operator directly as well, carrying the same account rather than a
// summary of it, and the channel keeps its copy.
//
// It reaches them the way Slack documents: the conversation is opened and the
// message goes to the channel that comes back, which is a different id from the
// member's own.
func TestADegradedHarnessReachesEachOperatorAsWellAsTheChannel(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{degraded(1)}}
	sink := newTestSink(t, t.TempDir(), feed, posts)
	sink.operators = []string{"U0FIRST", "U0SECOND"}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if !slices.Equal(posts.opened, sink.operators) {
		t.Fatalf("opened %#v, want a conversation with each operator", posts.opened)
	}
	if len(posts.requests) != 3 {
		t.Fatalf("posts = %#v, want the channel and both operators", posts.requests)
	}
	if channel := posts.requests[0]; channel.Channel != "C1" {
		t.Fatalf("first post = %#v, want the channel to keep its copy", channel)
	}
	for index, member := range sink.operators {
		said := posts.requests[index+1]
		if said.Channel != "D"+member {
			t.Fatalf("post %d went to %q, want the conversation opened with %s", index+1, said.Channel, member)
		}
		if said.ThreadTS != "" {
			t.Fatalf("post %d = %#v, want a direct message rather than a reply", index+1, said)
		}
		if said.Text != posts.requests[0].Text {
			t.Fatalf("post %d said %q, want the account the channel got", index+1, said.Text)
		}
	}
}

// A finding only the operator can act on is said to him directly and tagged by
// member id, in the channel and in the direct message alike: the communication
// rule says a message that is his to act on names him wherever it is said, and
// a member id is what makes the workspace notify a person rather than only
// print their name.
func TestAFindingForTheOperatorIsTaggedInTheChannelAndInTheDirectMessage(t *testing.T) {
	t.Parallel()

	finding, err := notify.FromOperatorAction(notify.OperatorAction{
		Needs:      "add the PreToolUse hook to .claude/settings.json by hand",
		RecordedIn: "the handling of report-0123456789abcdef0123456789abcde0 recorded in chat-1",
		FoundBy:    "the product manager, handling the report",
		Since:      moment,
	})
	if err != nil {
		t.Fatalf("select a finding: %v", err)
	}
	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{{
		Stream:       operatorActionStream,
		Cursor:       Cursor{Position: 1, Delivered: []string{findingMark + "report:report-0123456789abcdef0123456789abcde0"}},
		Direct:       true,
		Tag:          true,
		Notification: finding,
	}}}
	sink := newTestSink(t, t.TempDir(), feed, posts)
	sink.operators = []string{"U0FIRST", "U0SECOND"}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 3 {
		t.Fatalf("posts = %#v, want the channel and both operators", posts.requests)
	}
	channel := posts.requests[0]
	if channel.Channel != "C1" || !strings.Contains(channel.Text, "<@U0FIRST>") || !strings.Contains(channel.Text, "<@U0SECOND>") {
		t.Fatalf("channel post = %#v, want the operators tagged by member id", channel)
	}
	for index, member := range sink.operators {
		said := posts.requests[index+1]
		if said.Channel != "D"+member {
			t.Fatalf("post %d went to %q, want the conversation opened with %s", index+1, said.Channel, member)
		}
		if !strings.HasPrefix(said.Text, "<@"+member+">") {
			t.Fatalf("post %d said %q, want it tagged to %s", index+1, said.Text, member)
		}
		if !strings.Contains(said.Text, "add the PreToolUse hook to .claude/settings.json by hand") {
			t.Fatalf("post %d said %q, want the finding itself", index+1, said.Text)
		}
	}
}

// A workspace that will not open a direct conversation — an app installed before
// the manifest asked for `im:write` — costs the escalation and not the record.
// The channel already has the account and its cursor is written either way, so
// the pass carries on and the refusal is said in the sink's own log.
func TestAWorkspaceThatRefusesADirectMessageStillGetsTheChannelsCopy(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{refuseDirect: "missing_scope"}
	feed := &fixedFeed{deliveries: []Delivery{degraded(1)}}
	sink := newTestSink(t, t.TempDir(), feed, posts)
	sink.operators = []string{"U0FIRST"}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v, want a refused direct message to cost the pass nothing", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("posts = %#v, want the channel's copy alone", posts.requests)
	}
}

// A product that has named nobody is told nothing directly, which is the answer
// it already gets for steering: a workspace changes nothing about how this
// behaves until an operator names themselves.
func TestAProductThatNamedNobodyIsToldNothingDirectly(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{degraded(1)}}
	if err := newTestSink(t, t.TempDir(), feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("posts = %#v, want the channel alone", posts.requests)
	}
	if len(posts.opened) != 0 {
		t.Fatalf("opened %#v, want no conversation opened at all", posts.opened)
	}
}

// degraded is what the feed hands the sink when the session choosing work has
// fallen far enough behind the deployed harness to be a degraded system rather
// than a line worth reading: said in the channel, and said to the operators.
func degraded(position uint64) Delivery {
	return Delivery{
		Stream: residentStream,
		Cursor: Cursor{Position: position},
		Direct: true,
		Notification: notify.FromResident(
			notify.Resident{Build: strings.Repeat("a", 40), Behind: 31},
			report.SeverityWarning, moment),
	}
}

func wantMarks(t *testing.T, got []mark, want ...mark) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("marks = %#v, want %#v", got, want)
	}
	for index, marked := range want {
		if got[index] != marked {
			t.Fatalf("marks = %#v, want %#v", got, want)
		}
	}
}

// fixedFeed is a feed with a fixed answer, so the sink's own behavior is what a
// test exercises rather than the reading of durable records. Every delivery
// carries a position, which is how it knows what a cursor has already covered.
type fixedFeed struct {
	deliveries []Delivery
	// statuses is what each topic is doing at the end of this pass, which is a
	// reading rather than a history: a test moves it and polls again exactly as a
	// record moving underneath the sink would.
	statuses map[string]notify.Status
	// asking is the decision this pass owes the operators, which the heartbeat
	// derives in a real feed and a test states outright.
	asking *Ask
}

func (f *fixedFeed) Poll(_ context.Context, cursors Cursors) (Batch, error) {
	batch := Batch{Streams: map[string]struct{}{}, Statuses: f.statuses, Asking: f.asking}
	for _, delivery := range f.deliveries {
		batch.Streams[delivery.Stream] = struct{}{}
		if delivery.Cursor.Position <= cursors.Streams[delivery.Stream].Position {
			continue
		}
		batch.Deliveries = append(batch.Deliveries, delivery)
	}
	return batch, nil
}

// mark is one reaction the workspace was asked to put on or take off a message:
// which call, which message, and which emoji.
type mark struct {
	method string
	ts     string
	name   string
}

// recordedPosts is the workspace: what it was asked to post, what it was asked
// to mark, and what it refused.
type recordedPosts struct {
	// mutex is here because the real thing is a web service and this stands in for
	// one: a sink posts from its delivery loop and from the connection answering a
	// reply, and a workspace that could not take two calls at once would be the
	// double failing rather than the sink.
	mutex      sync.Mutex
	requests   []postRequest
	timestamps []string
	// marks is every reaction call in the order it was made, which is what says a
	// stale mark came off before the new one went on.
	marks []mark
	// markedIn is the conversation each of those calls named, in the same order:
	// a mark lands on whatever a channel and timestamp name together, so the
	// conversation is half of which message was marked.
	markedIn []string
	// wearing is what each message actually carries once those calls have been
	// applied. It is kept as well as the calls because the two answer different
	// questions: the calls say what the sink did, and this says what somebody
	// scanning the channel would see — which is the only thing that is wrong when
	// a mark is orphaned.
	wearing map[string]map[string]bool
	// refuseMarks, when set, is the error every reaction call is refused with —
	// an app installed before the manifest asked for the scope, which is what
	// every workspace looks like the first time it runs a sink that marks.
	refuseMarks string
	// opened is every member a direct conversation was opened with, in order, and
	// refuseDirect is the error every such call is refused with — a workspace
	// installed before the manifest asked for `im:write`.
	opened       []string
	refuseDirect string
	// allow, when set, is how many posts this workspace accepts before it
	// starts refusing — which is how a test puts an outage exactly where it
	// matters. It has to refuse every attempt rather than one, because a
	// transient refusal is retried inside the call.
	allow int
	count int
	// asked is how many times the workspace was asked who this app is, which a
	// sink does once at startup and a sink stopped before it started never does.
	asked int
}

func (r *recordedPosts) handle(writer http.ResponseWriter, request *http.Request) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	// A running sink asks the workspace who it is before it posts anything, and
	// that call carries no body at all. The member id in the answer is what a
	// message has to name to be addressed to this app.
	if request.Body == nil {
		r.asked++
		writeJSON(writer, map[string]any{"ok": true, "team": "test", "user": "yoyodyne", "user_id": testApp})
		return
	}
	body, _ := io.ReadAll(request.Body)
	method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
	if method == "conversations.open" {
		var opened openConversationRequest
		if err := json.Unmarshal(body, &opened); err != nil {
			writeJSON(writer, map[string]any{"ok": false, "error": "invalid_request"})
			return
		}
		if r.refuseDirect != "" {
			writeJSON(writer, map[string]any{"ok": false, "error": r.refuseDirect})
			return
		}
		r.opened = append(r.opened, opened.Users)
		// Slack answers with the conversation, and its id is not the member id:
		// posting to the one is not posting to the other, which is the whole reason
		// this call exists.
		writeJSON(writer, map[string]any{"ok": true, "channel": map[string]any{"id": "D" + opened.Users}})
		return
	}
	if strings.HasPrefix(method, "reactions.") {
		var reaction reactionRequest
		if err := json.Unmarshal(body, &reaction); err != nil {
			writeJSON(writer, map[string]any{"ok": false, "error": "invalid_request"})
			return
		}
		if r.refuseMarks != "" {
			writeJSON(writer, map[string]any{"ok": false, "error": r.refuseMarks})
			return
		}
		r.marks = append(r.marks, mark{method: method, ts: reaction.Timestamp, name: reaction.Name})
		r.markedIn = append(r.markedIn, reaction.Channel)
		// The workspace answers the way Slack does: a mark that is already there
		// and one that is already off are refusals rather than successes, which is
		// what a sweep over the vocabulary meets three times out of four.
		if r.wearing == nil {
			r.wearing = map[string]map[string]bool{}
		}
		if r.wearing[reaction.Timestamp] == nil {
			r.wearing[reaction.Timestamp] = map[string]bool{}
		}
		worn := r.wearing[reaction.Timestamp]
		if method == "reactions.add" {
			if worn[reaction.Name] {
				writeJSON(writer, map[string]any{"ok": false, "error": "already_reacted"})
				return
			}
			worn[reaction.Name] = true
		} else {
			if !worn[reaction.Name] {
				writeJSON(writer, map[string]any{"ok": false, "error": "no_reaction"})
				return
			}
			delete(worn, reaction.Name)
		}
		writeJSON(writer, map[string]any{"ok": true})
		return
	}
	var decoded postRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		writeJSON(writer, map[string]any{"ok": false, "error": "invalid_request"})
		return
	}
	r.count++
	if r.allow > 0 && r.count > r.allow {
		writeJSON(writer, map[string]any{"ok": false, "error": "internal_error"})
		return
	}
	r.requests = append(r.requests, decoded)
	ts := "1755.000" + strconv.Itoa(r.count)
	r.timestamps = append(r.timestamps, ts)
	writeJSON(writer, map[string]any{"ok": true, "ts": ts})
}

// refusingPosts is a workspace that refuses every post with one named Slack
// error until somebody fixes what it is complaining about.
type refusingPosts struct {
	mutex sync.Mutex
	code  string
	count int
}

func (r *refusingPosts) handle(writer http.ResponseWriter, request *http.Request) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if request.Body == nil {
		writeJSON(writer, map[string]any{"ok": true, "team": "test", "user": "yoyodyne"})
		return
	}
	r.count++
	if r.code != "" {
		writeJSON(writer, map[string]any{"ok": false, "error": r.code})
		return
	}
	writeJSON(writer, map[string]any{"ok": true, "ts": "1755.0001"})
}

func (r *refusingPosts) attempts() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.count
}

// invite is the operator doing the thing the refusal asked them to do.
func (r *refusingPosts) invite() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.code = ""
}

// identified is how many times the workspace was asked who this app is.
func (r *recordedPosts) identified() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.asked
}

// passStepper drives a sink's delivery loop one pass at a time. It stands in
// for the wait between passes, so the loop runs a pass, reports that it ended,
// and holds until the test lets the next one through — which is how a test
// about what happens over several passes says exactly how many there were.
//
// It replaces a poll at a millisecond against a wall-clock bound, which was a
// loop fsyncing as fast as it could and a test that gave up after ten seconds:
// on a machine running another suite beside this one, ten seconds was reached
// with the sink working and the test failing, on changes that never touched
// this package.
type passStepper struct {
	// ended carries one send per pass the loop has finished.
	ended chan struct{}
	// next carries one receive per pass the test allows after the first, which
	// the loop runs without being asked.
	next chan struct{}
	// first is whether the pass the loop runs unasked is still to be waited for.
	first bool
}

// stepPasses puts a stepper between a sink's passes. It is called before the
// loop starts, because the wait it replaces is read from the sink on each pass.
func stepPasses(sink *Sink) *passStepper {
	stepper := &passStepper{ended: make(chan struct{}), next: make(chan struct{}), first: true}
	sink.wait = stepper.wait
	return stepper
}

// wait is what the loop calls between passes. Both halves give way to the
// context ending, so a test that cancels the sink is never left with a loop
// blocked on a stepper nobody is driving.
func (p *passStepper) wait(ctx context.Context, _ time.Duration) bool {
	select {
	case p.ended <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	select {
	case <-p.next:
		return true
	case <-ctx.Done():
		return false
	}
}

// run lets the loop through the given number of passes and returns once the
// last of them has ended. It waits on the loop and on nothing else.
func (p *passStepper) run(passes int) {
	for ; passes > 0; passes-- {
		if p.first {
			p.first = false
		} else {
			p.next <- struct{}{}
		}
		<-p.ended
	}
}

// fixedTitles is the tracker as the sink sees it: what each item is called, or
// one refusal to say. Every question it was asked is kept, so a sink that asks
// the same thread's name twice is visible rather than merely wasteful.
type fixedTitles struct {
	titles map[string]string
	err    error
	asked  []string
}

func (f *fixedTitles) Title(_ context.Context, workItemID string) (string, error) {
	f.asked = append(f.asked, workItemID)
	if f.err != nil {
		return "", f.err
	}
	return f.titles[workItemID], nil
}

func newTestSink(t *testing.T, root string, feed Feed, posts *recordedPosts) *Sink {
	t.Helper()
	return newTestSinkAt(t, root, feed, posts, time.Time{})
}

// newTestSinkWithTitles is a sink that can ask what an item is called, which is
// every sink the harness builds and none of the ones above: what the rest are
// about is what the records carry.
func newTestSinkWithTitles(t *testing.T, root string, feed Feed, posts *recordedPosts, titles Titles) *Sink {
	t.Helper()
	sink := newTestSink(t, root, feed, posts)
	sink.titles = titles
	return sink
}

// newTestSinkAt is a sink that says the time is whatever a test needs it to be,
// which is the only way to watch a watermark not move.
func newTestSinkAt(t *testing.T, root string, feed Feed, posts *recordedPosts, now time.Time) *Sink {
	t.Helper()
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	options := Options{
		Channel: "C1",
		Store:   store,
		API:     newTestAPI(t, posts.handle),
		Feed:    feed,
		Log:     func(string, ...any) {},
	}
	if !now.IsZero() {
		options.Now = func() time.Time { return now }
	}
	sink, err := New(options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// The pace is real time in a running sink and none at all here, so a test
	// about what is posted does not spend a second per message proving it. The
	// pace itself is what TestPostingIsHeldToWhatSlackKeepsAccepting is for.
	sink.pace.sleep = func(context.Context, time.Duration) error { return nil }
	return sink
}

func milestone(position uint64, kind notify.Kind) Delivery {
	return Delivery{
		Stream: "run:run-a",
		Cursor: Cursor{Position: position},
		Notification: notify.Notification{
			Topic:   notify.Topic{Kind: notify.TopicWorkItem, ID: "yoyodyne-ifd.68.3"},
			Speaker: notify.Harness(),
			Event: notify.Event{
				Kind:     kind,
				At:       time.Now(),
				Severity: report.SeverityNote,
				Refs:     notify.Refs{RunID: "run-a", WorkItemID: "yoyodyne-ifd.68.3"},
			},
		},
	}
}

// filedReport is one agent's report against a work item, at the severity it was
// filed under. It is what a test needs to watch severity decide anything: the
// milestones above are all notes.
func filedReport(position uint64, severity report.Severity, text string) Delivery {
	return Delivery{
		Stream: reportStream,
		Cursor: Cursor{Position: position},
		Notification: notify.Notification{
			Topic:   notify.Topic{Kind: notify.TopicWorkItem, ID: "yoyodyne-ifd.68.12"},
			Speaker: notify.Persona(domain.RoleDeveloper, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: severity,
				Refs:     notify.Refs{RunID: "run-a", WorkItemID: "yoyodyne-ifd.68.12"},
				Text:     text,
			},
		},
	}
}

func workItemTopic(t *testing.T, id string) notify.Topic {
	t.Helper()
	topic, err := notify.WorkItem(id)
	if err != nil {
		t.Fatalf("WorkItem(%q) error = %v", id, err)
	}
	return topic
}

// A pass is made under the pass lease, and a sink that finds the lease held —
// the supervisor stopping it to restart it into a deployed build — makes no pass
// rather than starting one the stop would land in the middle of.
func TestASinkMakesNoPassWhileItsPassLeaseIsHeldElsewhere(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	sink := newTestSink(t, root, &fixedFeed{deliveries: []Delivery{milestone(1, notify.KindRunStarted)}}, posts)

	held, taken, err := sink.store.PassLease()
	if err != nil || !taken {
		t.Fatalf("PassLease() = %t, %v, want it free before any pass", taken, err)
	}
	if err := sink.heldPass(context.Background()); err != nil {
		t.Fatalf("heldPass() error = %v", err)
	}
	if len(posts.requests) != 0 {
		t.Fatalf("posts = %d, want no pass made while another holds the pass lease", len(posts.requests))
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	if err := sink.heldPass(context.Background()); err != nil {
		t.Fatalf("heldPass() error = %v", err)
	}
	if len(posts.requests) == 0 {
		t.Fatal("posts = 0, want the pass made once the lease is free")
	}
	// And the pass let the lease go when it ended, so a restart can take it.
	again, taken, err := sink.store.PassLease()
	if err != nil || !taken {
		t.Fatalf("PassLease() after a pass = %t, %v, want it free between passes", taken, err)
	}
	again.Release()
}

// leaseProbingFeed asks for the pass lease from inside a pass, which is what the
// supervisor does when it wants to restart the sink while the pass is going.
type leaseProbingFeed struct {
	store *Store
	taken *bool
}

func (f leaseProbingFeed) Poll(context.Context, Cursors) (Batch, error) {
	lease, taken, err := f.store.PassLease()
	if err == nil && taken {
		lease.Release()
	}
	*f.taken = taken
	return Batch{}, err
}

// While a pass is under way the pass lease is the sink's, so a supervisor asking
// for it is told a pass is going and waits rather than stopping the sink inside it.
func TestAPassHoldsThePassLeaseForItsWholeLength(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	taken := true
	sink := newTestSink(t, root, leaseProbingFeed{store: store, taken: &taken}, &recordedPosts{})
	if err := sink.heldPass(context.Background()); err != nil {
		t.Fatalf("heldPass() error = %v", err)
	}
	if taken {
		t.Fatal("the pass lease was taken from inside a pass, want it held by the pass for its whole length")
	}
}
