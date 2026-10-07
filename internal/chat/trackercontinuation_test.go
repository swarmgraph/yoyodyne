package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func TestEveryReadingRoleCanReachAllDescriptionAndAcceptanceText(t *testing.T) {
	t.Parallel()
	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		if !authority.MayAct(actionRead) {
			continue
		}
		for _, withNotes := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/notes=%t", role, withNotes), func(t *testing.T) {
				t.Parallel()
				item := beads.WorkItem{
					ID: "yoyodyne-task", Title: "Read the entire admitted scope", Status: "open",
					Description:        " \n" + strings.Repeat("description 説明\n", 1100) + "\nDone means: every requirement is reachable. \n",
					Design:             " \n" + strings.Repeat("design guidance\n", 700) + " \n",
					AcceptanceCriteria: " \n" + strings.Repeat("acceptance 条件\n", 800) + "\nDone means: the last requirement is reachable. \n",
				}
				if withNotes {
					item.Notes = strings.Repeat("old note\n", 2000) + "recent decision"
				}
				options := testOptions(t, &fakeBackend{})
				options.Role, options.Agent = role, string(role)
				options.Tracker = &fakeTracker{items: map[string]beads.WorkItem{item.ID: item}}
				session := openTestSession(t, options)
				if !strings.Contains(authority.Contract, itemReadClause) {
					t.Fatal("the reading role was not told how to continue a long read")
				}

				request := fmt.Sprintf(`{"action":"read","id":%q}`, item.ID)
				var joined strings.Builder
				start, total, parts := 0, -1, 0
				for {
					_, actions, _, err := extractTrackerActions(trackerReply("Reading the next part.", request))
					if err != nil {
						t.Fatalf("parse continuation: %v", err)
					}
					if err := session.authorize(parsedReply{Actions: actions}); err != nil {
						t.Fatalf("authorize continuation: %v", err)
					}
					outcomes, err := session.performTrackerActions(context.Background(), actions)
					if err != nil || len(outcomes) != 1 || !outcomes[0].Applied {
						t.Fatalf("read = %#v, %v", outcomes, err)
					}
					// Runs have their own count bound and follow the bounded item.
					page, _, _ := strings.Cut(outcomes[0].Detail, "\nruns:")
					if len(page) > maxTrackerItemBytes || !utf8.ValidString(page) {
						t.Fatalf("page has %d bytes or breaks UTF-8", len(page))
					}
					line, body, found := strings.Cut(page, "\n")
					var gotStart, count, remaining int
					if !found {
						t.Fatal("missing part declaration")
					}
					if n, err := fmt.Sscanf(line, "[item text starts at byte %d; %d bytes returned; %d bytes remain]", &gotStart, &count, &remaining); err != nil || n != 3 {
						t.Fatalf("part declaration = %q: %v", line, err)
					}
					if total < 0 {
						total = count + remaining
					}
					if gotStart != start || count <= 0 || remaining != total-start-count || count > len(body) {
						t.Fatalf("part starts=%d returns=%d remains=%d; wanted start=%d of %d", gotStart, count, remaining, start, total)
					}
					joined.WriteString(body[:count])
					start += count
					parts++
					if withNotes && (!strings.Contains(page, "recent decision") || !strings.Contains(page, "are cut; treat them as unread rather than absent")) {
						t.Fatal("the bounded notes lost their recent writing or cut declaration")
					}
					if remaining == 0 {
						break
					}
					_, next, found := strings.Cut(body[count:], "[item text continues; read ")
					if !found {
						t.Fatal("unreachable remainder: the read has no continuation")
					}
					request, _, found = strings.Cut(next, "]\n")
					if !found {
						t.Fatal("unclosed continuation declaration")
					}
					var action TrackerAction
					if err := json.Unmarshal([]byte(request), &action); err != nil || action.Offset == nil || *action.Offset != start {
						t.Fatalf("next action = %q, %v; want offset %d", request, err, start)
					}
				}
				if parts < 2 || joined.Len() != total {
					t.Fatalf("read %d parts, %d of %d bytes", parts, joined.Len(), total)
				}
				want := "\ndescription:\n" + item.Description + "\n\ndesign:\n" + item.Design + "\n\nacceptance criteria:\n" + item.AcceptanceCriteria + "\n"
				if !strings.HasSuffix(joined.String(), want) {
					t.Fatal("following the returned requests did not preserve every description, design, and acceptance byte in order")
				}
			})
		}
	}
}

func TestShortItemReadKeepsItsExistingOutput(t *testing.T) {
	t.Parallel()
	for _, notes := range []string{"", " \nA recent note. \n"} {
		item := beads.WorkItem{ID: "yoyodyne-task", Title: "Short item", Status: "open", IssueType: "task",
			Description: " \nThe description. \n", Design: " A design. ", AcceptanceCriteria: " Done means: tested. ", Notes: notes}
		want := "id: yoyodyne-task\ntitle: Short item\nstatus: open\npriority: 0\ntype: task\nattribution: " +
			describeAttribution(goal.Set{}.AttributionOf(item.Notes, item.GoalWitness)) +
			"\nrelevant goals — goals the change must not break: " + goal.Set{}.DescribeRelevant(nil) +
			"\norigin: " + domain.WorkItemOrigin{}.Describe() +
			"\n\ndescription:\nThe description.\n\ndesign:\nA design.\n\nacceptance criteria:\nDone means: tested.\n"
		if notes != "" {
			want += "\nnotes:\nA recent note.\n"
		}
		got, err := renderWorkItemRead(item, goal.Set{}, nil)
		if err != nil || got != want {
			t.Fatalf("short read = %q, %v; want %q", got, err, want)
		}
	}
}

func TestItemReadRefusesOffsetsThatCannotContinueItsText(t *testing.T) {
	t.Parallel()
	for _, action := range []string{
		`{"action":"read","id":"yoyodyne-task","offset":-1}`,
		`{"action":"survey","offset":0}`,
		`{"action":"read","report":"report-0123456789abcdef0123456789abcdef","offset":0}`,
		`{"action":"read","id":"yoyodyne-task","offset":"1"}`,
		`{"action":"read","id":"yoyodyne-task","offset":1.5}`,
	} {
		if _, _, _, err := extractTrackerActions(trackerReply("Read.", action)); err == nil {
			t.Fatalf("accepted invalid offset action %s", action)
		}
	}
	item := beads.WorkItem{ID: "yoyodyne-task", Description: "説明"}
	head := renderWorkItemHead(item, goal.Set{}, false)
	for _, offset := range []int{-1, len(head) + 1, strings.Index(head, "説明") + 1} {
		outcome := TrackerOutcome{Action: TrackerAction{Action: actionRead, ID: item.ID, Offset: &offset}}
		session := &Session{options: Options{Tracker: &fakeTracker{items: map[string]beads.WorkItem{item.ID: item}}}}
		session.carryOutTrackerAction(context.Background(), &outcome)
		if outcome.Applied || outcome.Failure == "" || outcome.Detail != "" {
			t.Fatalf("offset %d = %#v, want a declared failure without a partial read", offset, outcome)
		}
	}
	oversizedID := beads.WorkItem{ID: strings.Repeat("a", maxTrackerItemBytes), Notes: "a recent note"}
	if text, err := renderWorkItemRead(oversizedID, goal.Set{}, nil); err == nil || text != "" {
		t.Fatal("an oversized continuation request was not refused")
	}
	if text := renderWorkItemEvidence(oversizedID, goal.Set{}); !strings.Contains(text, "item read failed:") {
		t.Fatal("the first-read renderer silently lost an item it could not carry")
	}

	for _, offset := range []int{0, len(head)} {
		if _, err := renderWorkItemRead(item, goal.Set{}, &offset); err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
	}
}

// Read results must actually reach the role's next provider invocation, rather
// than exist only in the action's returned account. Finish each round in prose
// to exercise continuation across conversation turns as well as tracker actions.
func TestAContinuationReachesTheReadingRoleInItsNextTurn(t *testing.T) {
	t.Parallel()
	item := beads.WorkItem{ID: "yoyodyne-task", Title: "Read all requirements", Status: "open",
		Description:        strings.Repeat("A requirement.\n", 2400) + "Done means: the last requirement is read.",
		AcceptanceCriteria: "Done means: the separate acceptance field is read.", Notes: "Keep the latest decision."}
	provider := &fakeBackend{}
	options := testOptions(t, provider)
	options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
	options.Tracker = &fakeTracker{items: map[string]beads.WorkItem{item.ID: item}}
	session := openTestSession(t, options)
	request := `{"action":"read","id":"yoyodyne-task"}`
	for part := 0; ; part++ {
		if part > 32 {
			t.Fatal("continuations did not finish within the parts this item needs")
		}
		provider.results = append(provider.results,
			backendapi.RunResult{SessionID: "session-1", FinalText: trackerReply("Reading the next part.", request)},
			backendapi.RunResult{SessionID: "session-1", FinalText: "This part has been read."})
		reply, err := session.Send(context.Background(), "Continue reading the requirements.")
		if err != nil || len(reply.Actions) != 1 || !reply.Actions[0].Applied {
			t.Fatalf("read turn = %#v, %v", reply, err)
		}
		detail := reply.Actions[0].Detail
		if !strings.Contains(provider.requests[len(provider.requests)-1].Prompt, detail) {
			t.Fatal("the continuation result was not handed back to the reading role")
		}
		_, next, found := strings.Cut(detail, "[item text continues; read ")
		if !found {
			if part < maxTrackerRounds || !strings.Contains(detail, item.AcceptanceCriteria) {
				t.Fatal("the role did not reach the acceptance text beyond one message's round bound")
			}
			break
		}
		request, _, found = strings.Cut(next, "]\n")
		if !found {
			t.Fatal("the continuation request was cut before it could be asked for")
		}
	}
}
