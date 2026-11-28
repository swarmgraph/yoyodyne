package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// fakeCorrections owes a correction for the check "check-1" of "yoyodyne-ep" only,
// and records each piece linked and each note recorded.
type fakeCorrections struct {
	linked, noted []string
	linkErr       error
}

func (f *fakeCorrections) MayCorrect(epic, check string) error {
	if epic != "yoyodyne-ep" || check != "check-1" || len(f.linked) > 0 {
		return errors.New("no correction is owed for the check " + check + " of epic " + epic)
	}
	return nil
}

func (f *fakeCorrections) Correcting(epic, check, piece string) (string, error) {
	if f.linkErr != nil {
		return "", f.linkErr
	}
	f.linked = append(f.linked, epic+" "+check+" "+piece)
	return "Written as the correction owed by the check of the whole epic " + epic + ", " + check + ", which read commit abc.", nil
}

func (f *fakeCorrections) Noted(epic, check string) error {
	f.noted = append(f.noted, epic+" "+check)
	return nil
}

const correctingAction = `{"action":"create","kind":"feature","title":"Read the new file","description":"Make something read it.","goal":"` + theGoal + `","parent":"yoyodyne-ep","epic_check":"check-1","reason":"the check found it missing"}`

// createCorrecting runs one creation in a conversation of the role, with the
// corrections given.
func createCorrecting(t *testing.T, role domain.AgentRole, action string, corrections EpicCorrections) (TrackerOutcome, *fakeTracker) {
	t.Helper()
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Writing the correction.", action)},
		{SessionID: "session-1", FinalText: "Written."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Role, options.Agent = role, string(role)
	options.EpicCorrections = corrections
	reply, err := openTestSession(t, options).Send(context.Background(), "Go on.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) > 1 {
		t.Fatalf("actions = %#v, want at most one", reply.Actions)
	}
	if len(reply.Actions) == 0 {
		// The whole block was refused before anything was carried out.
		return TrackerOutcome{}, tracker
	}
	return reply.Actions[0], tracker
}

// The development manager's piece naming a check with a correction owed is created,
// linked to the check before its note is written, and the note goes on the item.
func TestAPieceNamingAnOwedCheckIsLinkedThenNoted(t *testing.T) {
	t.Parallel()
	corrections := &fakeCorrections{}
	outcome, tracker := createCorrecting(t, domain.RoleDevelopmentManager, correctingAction, corrections)
	if !outcome.Applied || len(tracker.created) != 1 || !strings.Contains(outcome.Summary, "as the correction owed by the check check-1 of epic yoyodyne-ep") {
		t.Fatalf("outcome %#v, created %d", outcome, len(tracker.created))
	}
	if len(corrections.linked) != 1 || corrections.linked[0] != "yoyodyne-ep check-1 yoyodyne-1" || len(corrections.noted) != 1 {
		t.Fatalf("linked %v, noted %v", corrections.linked, corrections.noted)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].id != "yoyodyne-1" || !strings.Contains(tracker.updates[0].change.AppendNotes, "check-1, which read commit abc") {
		t.Fatalf("updates %#v, want the note of fixed form on the piece", tracker.updates)
	}
}

// A creation naming a check is refused before anything is created where no
// correction is owed for it, where one is already written, for any role but the
// development manager, without a parent, and where nothing can link it.
func TestAPieceNamingACheckWithNoCorrectionOwedIsRefused(t *testing.T) {
	t.Parallel()
	written := &fakeCorrections{linked: []string{"yoyodyne-ep check-1 yoyodyne-9"}}
	for name, test := range map[string]struct {
		role        domain.AgentRole
		action      string
		corrections EpicCorrections
		says        string
	}{
		"no correction owed": {domain.RoleDevelopmentManager, strings.Replace(correctingAction, "check-1", "check-2", 1), &fakeCorrections{}, "no correction is owed for the check check-2"},
		"already written":    {domain.RoleDevelopmentManager, correctingAction, written, "no correction is owed for the check check-1"},
		"another role":       {domain.RoleProductManager, correctingAction, &fakeCorrections{}, "only the development manager writes the piece"},
		"nothing to link it": {domain.RoleDevelopmentManager, correctingAction, nil, "no record of the epics' progress"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			outcome, tracker := createCorrecting(t, test.role, test.action, test.corrections)
			if outcome.Applied || len(tracker.created) != 0 || !strings.Contains(outcome.Failure, test.says) {
				t.Fatalf("outcome %#v, created %d; want refused saying %q", outcome, len(tracker.created), test.says)
			}
		})
	}
}

// A check named without the epic as the parent, or as words rather than an
// identifier, refuses the action before anything is carried out.
func TestACheckNamedWithoutItsEpicIsRefusedWhole(t *testing.T) {
	t.Parallel()
	for action, says := range map[string]string{
		strings.Replace(correctingAction, `"parent":"yoyodyne-ep",`, "", 1):    "names the epic as its",
		strings.Replace(correctingAction, `"check-1"`, `"the first check"`, 1): "is not a check's identifier",
	} {
		var parsed TrackerAction
		if err := json.Unmarshal([]byte(action), &parsed); err != nil {
			t.Fatal(err)
		}
		if err := errors.Join(parsed.epicCheckProblems()...); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: problems %v, want one saying %q", action, err, says)
		}
		outcome, tracker := createCorrecting(t, domain.RoleDevelopmentManager, action, &fakeCorrections{})
		if outcome.Applied || len(tracker.created) != 0 {
			t.Errorf("%s: carried out %#v", action, outcome)
		}
	}
	var other TrackerAction
	if err := json.Unmarshal([]byte(`{"action":"update","id":"yoyodyne-1","epic_check":"check-1"}`), &other); err != nil {
		t.Fatal(err)
	}
	if err := other.Validate(); err == nil || !strings.Contains(err.Error(), "epic_check") {
		t.Errorf("an update naming a check: %v, want it refused", err)
	}
}

// A piece created and then refused its link is said not to be a correction.
func TestAPieceWhoseLinkFailsIsSaidNotToJoin(t *testing.T) {
	t.Parallel()
	corrections := &fakeCorrections{linkErr: errors.New("the epic has ended")}
	outcome, tracker := createCorrecting(t, domain.RoleDevelopmentManager, correctingAction, corrections)
	if len(tracker.created) != 1 || !strings.Contains(outcome.Summary, "NOT linked to the check check-1") || len(tracker.updates) != 0 {
		t.Fatalf("outcome %#v, updates %#v", outcome, tracker.updates)
	}
}

// fakeCommentCorrections owes a correction for the review comment "comment-21" of
// "yoyodyne-ep" only, beside what fakeCorrections owes for checks.
type fakeCommentCorrections struct {
	fakeCorrections
	commentLinked, commentNoted []string
}

func (f *fakeCommentCorrections) MayCorrectComment(epic, comment string) error {
	if epic != "yoyodyne-ep" || comment != "comment-21" {
		return errors.New("no correction is owed for the review comment " + comment + " of epic " + epic)
	}
	if len(f.commentLinked) > 0 {
		return errors.New("the correction owed for the review comment comment-21 of epic yoyodyne-ep has already been written, as yoyodyne-1")
	}
	return nil
}

func (f *fakeCommentCorrections) CorrectingComment(epic, comment, piece string) (string, error) {
	f.commentLinked = append(f.commentLinked, epic+" "+comment+" "+piece)
	return "Written as the correction owed by review comment " + comment + " on merged pull request #7 of yoyodyne-ep.1, in the epic " + epic + ".", nil
}

func (f *fakeCommentCorrections) NotedComment(epic, comment string) error {
	f.commentNoted = append(f.commentNoted, epic+" "+comment)
	return nil
}

var commentAction = strings.Replace(correctingAction, `"epic_check":"check-1"`, `"epic_comment":"comment-21"`, 1)

// The development manager's piece naming a review comment with a correction owed
// is linked to that comment and noted; naming one with none owed, or a second
// piece for one, is refused with the reason before anything is created.
func TestAPieceNamingAnOwedReviewCommentIsLinkedOnce(t *testing.T) {
	t.Parallel()
	corrections := &fakeCommentCorrections{}
	outcome, tracker := createCorrecting(t, domain.RoleDevelopmentManager, commentAction, corrections)
	if !outcome.Applied || len(tracker.created) != 1 || !strings.Contains(outcome.Summary, "as the correction owed by the review comment comment-21 of epic yoyodyne-ep") {
		t.Fatalf("outcome %#v, created %d", outcome, len(tracker.created))
	}
	if len(corrections.commentLinked) != 1 || corrections.commentLinked[0] != "yoyodyne-ep comment-21 yoyodyne-1" || len(corrections.commentNoted) != 1 || len(corrections.linked) != 0 {
		t.Fatalf("linked %v, noted %v, checks linked %v", corrections.commentLinked, corrections.commentNoted, corrections.linked)
	}
	if len(tracker.updates) != 1 || !strings.Contains(tracker.updates[0].change.AppendNotes, "review comment comment-21 on merged pull request #7") {
		t.Fatalf("updates %#v, want the note of fixed form on the piece", tracker.updates)
	}
	for name, test := range map[string]struct {
		action      string
		corrections EpicCorrections
		says        string
	}{
		"no correction owed":  {strings.Replace(commentAction, "comment-21", "comment-22", 1), &fakeCommentCorrections{}, "no correction is owed for the review comment comment-22"},
		"a second piece":      {commentAction, corrections, "has already been written, as yoyodyne-1"},
		"comments not wired":  {commentAction, &fakeCorrections{}, "no piece can be linked to a review comment"},
		"a check and comment": {strings.Replace(commentAction, `"epic_comment"`, `"epic_check":"check-1","epic_comment"`, 1), &fakeCommentCorrections{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			outcome, tracker := createCorrecting(t, domain.RoleDevelopmentManager, test.action, test.corrections)
			if outcome.Applied || len(tracker.created) != 0 || !strings.Contains(outcome.Failure, test.says) {
				t.Fatalf("outcome %#v, created %d; want refused saying %q", outcome, len(tracker.created), test.says)
			}
		})
	}
}
