package chat

// A program manager's digest and its one read-model query.
//
// These hold "What leaves the lane" and the query half of `readmodel.read` in
// docs/designs/program-manager.md: one digest per pass, filed through the report
// block into the pile the Lead Product Manager walks and decided with her
// handle action; a reply with two refused, and a pass with none filing nothing;
// and one named query answered in full in the same reply, with two, a second
// block, or an unknown name refused naming the queries there are.

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

const testPass = "factory-flow#12"

// digestEntry is a digest the way the contract asks for one.
const digestEntry = `{"severity":"warning","digest":{"admissions":["yoyodyne-ifd.500"],"requests":[{"work":"a check that fails a run whose worktree lost its build cache","goal":"Isolate implementation tasks in harness-managed Git worktrees and integrate successful work automatically.","priority":1,"why":"three runs stopped on it this week"}],"objections":[{"item":"yoyodyne-ifd.501","concern":"it repeats yoyodyne-ifd.480, which already covers the cache"}],"open_requests":["report-00000000000000000000000000000abc"]}}`

// passSession opens a program manager instance's conversation over a pile,
// marked as the named pass's where a pass is named, answering with the replies
// given in order.
func passSession(t *testing.T, pile *fakeReports, pass string, replies ...string) (*Session, *fakeBackend) {
	t.Helper()

	results := make([]backendapi.RunResult, 0, len(replies))
	for _, reply := range replies {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: reply})
	}
	provider := &fakeBackend{results: results}
	options := testOptions(t, provider)
	options.Role = domain.RoleProgramManager
	options.Agent = "factory-flow"
	options.Lane = "factory-flow"
	options.Reports = pile
	session := openTestSession(t, options)
	session.ForPass(pass)
	return session, provider
}

// The digest a pass files is one report in the pile, in the fixed shape and
// stamped with the lane and the pass; the Lead Product Manager is shown it in
// her walk of the pile and decides it with her handle action.
func TestAPassDigestReachesTheLeadProductManagersWalkAndIsDecidedWithHandle(t *testing.T) {
	t.Parallel()

	pile := &fakeReports{}
	session, provider := passSession(t, pile, testPass, reportReply("The line held this pass.", digestEntry))
	reply, err := session.Send(context.Background(), "pass")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if reply.ReportProblem != "" {
		t.Fatalf("the digest was not filed: %s", reply.ReportProblem)
	}
	if !strings.Contains(provider.requests[0].SystemPrompt, "# Your digest to the Lead Product Manager") {
		t.Error("the program manager's contract does not say how to file a digest")
	}
	if len(pile.appended) != 1 {
		t.Fatalf("pile = %#v, want the one digest", pile.appended)
	}
	digest := pile.appended[0]
	if digest.Digest == nil || digest.Digest.Lane != "factory-flow" || digest.Digest.Pass != testPass {
		t.Fatalf("digest = %#v, want it stamped with the lane and the pass", digest.Digest)
	}
	if digest.Severity != report.SeverityWarning || digest.Role != domain.RoleProgramManager || digest.Agent != "factory-flow" {
		t.Fatalf("digest filed as %s by %s/%s", digest.Severity, digest.Role, digest.Agent)
	}
	for _, want := range []string{
		`Digest of the lane "factory-flow", from pass ` + testPass,
		"Admitted in the lane this pass: yoyodyne-ifd.500.",
		"a check that fails a run whose worktree lost its build cache — would serve: Isolate implementation tasks",
		"recommended priority 1; why: three runs stopped on it this week",
		"yoyodyne-ifd.501: it repeats yoyodyne-ifd.480",
		"Open requests from earlier passes: report-00000000000000000000000000000abc.",
	} {
		if !strings.Contains(digest.Message, want) {
			t.Errorf("the digest's text is missing %q:\n%s", want, digest.Message)
		}
	}

	// The Lead Product Manager's walk of the pile carries it, and her handle
	// action is what takes it out.
	manager := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("I have read the factory-flow digest.",
			`{"action":"handle","report":"`+digest.ID+`","reason":"the cache check is admitted as yoyodyne-ifd.502; the objection to yoyodyne-ifd.501 is upheld and it is retired"}`)},
		{SessionID: "session-1", FinalText: "Recorded."},
	}}
	options := testOptions(t, manager)
	options.Reports = pile
	options.Tracker = &fakeTracker{}
	lead := openTestSession(t, options)
	handled, err := lead.Send(context.Background(), "what came in?")
	if err != nil {
		t.Fatalf("the Lead Product Manager's Send() error = %v", err)
	}
	walk := manager.requests[0].Prompt
	if !strings.Contains(walk, "Reports nobody has decided about") || !strings.Contains(walk, digest.ID) ||
		!strings.Contains(walk, "Digest of the lane") {
		t.Fatalf("the digest did not reach the Lead Product Manager's walk:\n%s", walk)
	}
	if len(handled.Actions) != 1 || !handled.Actions[0].Applied {
		t.Fatalf("handle = %#v", handled.Actions)
	}
	if len(pile.handled) != 1 || pile.handled[0].ReportID != digest.ID || pile.handled[0].Role != domain.RoleProductManager {
		t.Fatalf("handlings = %#v, want the digest handled by the Lead Product Manager", pile.handled)
	}
	if left := report.Unhandled(pile.appended, pile.handled); len(left) != 0 {
		t.Fatalf("the digest is still unhandled after her handle: %#v", left)
	}
}

// A pass with nothing to say files nothing, and a digest with nothing in it is
// refused rather than filed.
func TestAPassWithNothingToSayFilesNoDigest(t *testing.T) {
	t.Parallel()

	pile := &fakeReports{}
	session, _ := passSession(t, pile, testPass,
		"Nothing moved in the lane.",
		reportReply("Nothing moved.", `{"severity":"note","digest":{}}`),
	)
	quiet, err := session.Send(context.Background(), "pass")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(pile.appended) != 0 || quiet.ReportProblem != "" {
		t.Fatalf("a pass with no digest filed %#v (%q)", pile.appended, quiet.ReportProblem)
	}
	empty, err := session.Send(context.Background(), "pass")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(pile.appended) != 0 || !strings.Contains(empty.ReportProblem, "a pass with nothing to say files no digest") {
		t.Fatalf("an empty digest was filed or not refused with the reason: %#v (%q)", pile.appended, empty.ReportProblem)
	}
}

// A reply carrying two digests is refused whole, nothing is filed, and the role
// is told why on its next turn.
func TestAReplyWithTwoDigestsIsRefused(t *testing.T) {
	t.Parallel()

	pile := &fakeReports{}
	session, provider := passSession(t, pile, testPass,
		reportReply("Two things.", digestEntry, digestEntry, `{"severity":"critical","message":"Nothing has landed for six hours. Every run is stopping at its checks."}`),
		"Understood.",
	)
	reply, err := session.Send(context.Background(), "pass")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	// Neither digest is filed, and the critical report beside them still is.
	if len(pile.appended) != 1 || pile.appended[0].Digest != nil || pile.appended[0].Severity != report.SeverityCritical {
		t.Fatalf("a reply with two digests filed %#v, want the critical report alone", pile.appended)
	}
	if !strings.Contains(reply.ReportProblem, "2 digests in one reply, and a pass files one digest") {
		t.Fatalf("report problem = %q", reply.ReportProblem)
	}
	if _, err := session.Send(context.Background(), "and now?"); err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if next := provider.requests[1].Prompt; !strings.Contains(next, "# Report result") || !strings.Contains(next, "2 digests in one reply") {
		t.Fatalf("the role was not told its digests were refused:\n%s", next)
	}
}

// A second digest in a pass that already filed one is refused naming the first,
// and a report filed beside it is still filed.
func TestASecondDigestInOnePassIsRefusedNamingTheFirst(t *testing.T) {
	t.Parallel()

	pile := &fakeReports{}
	session, _ := passSession(t, pile, testPass,
		reportReply("First turn.", digestEntry),
		reportReply("Second turn.", digestEntry, `{"severity":"critical","message":"Nothing has landed for six hours. Every run is stopping at its checks."}`),
	)
	if _, err := session.Send(context.Background(), "pass"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(pile.appended) != 1 {
		t.Fatalf("pile = %#v, want the first digest", pile.appended)
	}
	first := pile.appended[0].ID
	second, err := session.Send(context.Background(), "continue the pass")
	if err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if !strings.Contains(second.ReportProblem, "already filed its digest as "+first) {
		t.Fatalf("report problem = %q, want the first digest named", second.ReportProblem)
	}
	if len(pile.appended) != 2 || pile.appended[1].Digest != nil || pile.appended[1].Severity != report.SeverityCritical {
		t.Fatalf("pile = %#v, want the critical filed beside the refused digest", pile.appended)
	}
}

// A digest that does not hold to the shape is refused with every reason, and a
// digest from a turn no pass woke is refused as well; either way a critical
// report filed beside it is still filed.
func TestAMalformedDigestIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		pass  string
		entry string
		wants []string
	}{
		{
			name:  "objection at note and a priority out of range",
			pass:  testPass,
			entry: `{"severity":"note","digest":{"requests":[{"work":"w","goal":"g","priority":7,"why":"y"}],"objections":[{"item":"yoyodyne-ifd.9","concern":"c"}]}}`,
			wants: []string{"priority 7 is not a priority", `a digest carrying an objection is filed at "warning"`},
		},
		{
			name:  "missing fields",
			pass:  testPass,
			entry: `{"severity":"note","digest":{"requests":[{"work":"w","why":"y"}]}}`,
			wants: []string{"requests[0].goal is required", "requests[0].priority is required"},
		},
		{
			name:  "asserts its own lane",
			pass:  testPass,
			entry: `{"severity":"note","digest":{"lane":"somebody-else","admissions":["yoyodyne-ifd.1"]}}`,
			wants: []string{"stamped by the harness"},
		},
		{
			name:  "critical",
			pass:  testPass,
			entry: `{"severity":"critical","digest":{"admissions":["yoyodyne-ifd.1"]}}`,
			wants: []string{`never at "critical"`},
		},
		{
			name:  "not a pass",
			pass:  "",
			entry: `{"severity":"note","digest":{"admissions":["yoyodyne-ifd.1"]}}`,
			wants: []string{"this turn is not a pass's"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pile := &fakeReports{}
			session, _ := passSession(t, pile, test.pass, reportReply("A pass.", test.entry, `{"severity":"critical","message":"Nothing has landed for six hours. Every run is stopping at its checks."}`))
			reply, err := session.Send(context.Background(), "pass")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			// The digest is refused alone: the critical report beside it is filed.
			if len(pile.appended) != 1 || pile.appended[0].Digest != nil || pile.appended[0].Severity != report.SeverityCritical {
				t.Fatalf("pile = %#v, want the critical report alone", pile.appended)
			}
			for _, want := range test.wants {
				if !strings.Contains(reply.ReportProblem, want) {
					t.Errorf("report problem = %q, want %q", reply.ReportProblem, want)
				}
			}
		})
	}
}

// fakeReadModel answers the queries it holds and records what it was asked.
type fakeReadModel struct {
	answers map[string]string
	asked   []string
}

func (f *fakeReadModel) Query(_ context.Context, name, agent string) (readmodel.QueryResult, error) {
	f.asked = append(f.asked, name+"/"+agent)
	answer, held := f.answers[name]
	if !held {
		return readmodel.QueryResult{}, errors.New("not held")
	}
	return readmodel.QueryResult{Query: name, Agent: agent, JSON: answer, WholeBytes: len(answer)}, nil
}

func readModelBlock(payload string) string {
	return readModelFence + "\n" + payload + "\n```\n"
}

func readModelSession(t *testing.T, role domain.AgentRole, model *fakeReadModel, replies ...string) (*Session, *fakeBackend) {
	t.Helper()

	results := make([]backendapi.RunResult, 0, len(replies))
	for _, reply := range replies {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: reply})
	}
	provider := &fakeBackend{results: results}
	options := testOptions(t, provider)
	options.Role = role
	options.Agent = string(role)
	options.ReadModel = model
	return openTestSession(t, options), provider
}

// A block naming one query has it answered in full in the same reply, as a
// further round of it.
func TestOneNamedReadModelQueryIsAnsweredInFullInTheSameReply(t *testing.T) {
	t.Parallel()

	model := &fakeReadModel{answers: map[string]string{"docket": `{"live":3,"critical":1,"stoppages":[{"key":"run-1"}]}`}}
	session, provider := readModelSession(t, domain.RoleProgramManager, model,
		"Let me see the docket.\n\n"+readModelBlock(`{"query":"docket"}`),
		"Three stoppages wait, one critical.",
	)
	reply, err := session.Send(context.Background(), "how is the docket?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("%d provider requests, want the query answered as a second round of the same reply", len(provider.requests))
	}
	answered := provider.requests[1].Prompt
	if !strings.Contains(answered, `The "docket" query`) || !strings.Contains(answered, `{"live":3,"critical":1,"stoppages":[{"key":"run-1"}]}`) {
		t.Fatalf("the round after the block does not carry the query whole:\n%s", answered)
	}
	if len(reply.ReadModel) != 1 || reply.ReadModel[0].Problem != "" || reply.ReadModel[0].Ask.Query != "docket" {
		t.Fatalf("reply.ReadModel = %#v", reply.ReadModel)
	}
	if !strings.Contains(reply.Text, "Three stoppages wait") || strings.Contains(reply.Text, "yoyodyne-readmodel") {
		t.Fatalf("reply text = %q", reply.Text)
	}
	if !strings.Contains(provider.requests[0].SystemPrompt, "## readmodel.read") || !strings.Contains(provider.requests[0].SystemPrompt, "`yoyodyne-readmodel` block") {
		t.Error("the program manager's contract does not say how to ask the read model")
	}
}

// A block naming two queries, a reply carrying two blocks, and a name that is
// not a query are each refused naming every query there is, and nothing is
// read.
func TestAReadModelBlockNamingTwoOrAnUnknownQueryIsRefusedNamingTheQueries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		reply string
		want  string
	}{
		{"two in one block", readModelBlock(`{"query":["docket","reports"]}`), "the block names 2 queries, and a block names one"},
		{"two blocks", readModelBlock(`{"query":"docket"}`) + "\n" + readModelBlock(`{"query":"reports"}`), "the reply carried 2 read-model blocks"},
		{"unknown", readModelBlock(`{"query":"weather"}`), `"weather" is not a read-model query`},
		{"lane report with no agent", readModelBlock(`{"query":"lane-report"}`), `names it as "agent"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			model := &fakeReadModel{answers: map[string]string{"docket": `{}`, "reports": `{}`}}
			session, provider := readModelSession(t, domain.RoleProgramManager, model, "Asking.\n\n"+test.reply, "Understood.")
			reply, err := session.Send(context.Background(), "ask")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(model.asked) != 0 {
				t.Fatalf("the read model was asked %v", model.asked)
			}
			if len(reply.ReadModel) != 1 || !strings.Contains(reply.ReadModel[0].Problem, test.want) {
				t.Fatalf("reply.ReadModel = %#v, want %q", reply.ReadModel, test.want)
			}
			refused := provider.requests[1].Prompt
			if !strings.Contains(refused, "Refused, and nothing was read") {
				t.Fatalf("the role was not handed the refusal:\n%s", refused)
			}
			for _, query := range readmodel.Queries() {
				if !strings.Contains(refused, `"`+query+`"`) {
					t.Errorf("the refusal does not name the %q query:\n%s", query, refused)
				}
			}
		})
	}
}

// The read model's queries are the program manager's: any other role's block
// is refused by the authority table and nothing is read.
func TestOnlyTheProgramManagerMayAskTheReadModel(t *testing.T) {
	t.Parallel()

	model := &fakeReadModel{answers: map[string]string{"docket": `{}`}}
	session, _ := readModelSession(t, domain.RoleProductManager, model, readModelBlock(`{"query":"docket"}`))
	_, err := session.Send(context.Background(), "ask")
	var refused *AuthorityError
	if !errors.As(err, &refused) || !strings.Contains(refused.Refused, "read-model query") {
		t.Fatalf("Send() error = %v, want the authority table's refusal", err)
	}
	if len(model.asked) != 0 {
		t.Fatalf("the read model was asked %v", model.asked)
	}
}
