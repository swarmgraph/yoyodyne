//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package mcpcall

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/mcpcredential"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// pathFixture is a call path over the fake server, declared twice: "fake",
// whose credential is stored, and "nologin", whose credential is not.
type pathFixture struct {
	path   *Path
	config config.Config
	hold   *runstate.OperatorHoldStore
}

var fakeToolClasses = map[string]config.MCPToolClass{
	"echo": config.MCPRead, "send": config.MCPAct, "hang": config.MCPRead, "hang-act": config.MCPAct,
	"flaky": config.MCPRead, "flaky-act": config.MCPAct, "mixed": config.MCPRead, "big": config.MCPRead,
	"leak": config.MCPRead, "leak-error": config.MCPRead, "described": config.MCPRead,
}

func newPathFixture(t *testing.T) *pathFixture {
	t.Helper()
	stores := testStores(t, "fake")
	declared := func() config.MCPServer {
		return config.MCPServer{Command: fakeCommand(), Credential: config.MCPCredentialFile, CredentialEnv: fakeCredentialEnv, Tools: fakeToolClasses}
	}
	leadTools := []string{"nologin.echo"}
	for tool := range fakeToolClasses {
		leadTools = append(leadTools, "fake."+tool)
	}
	cfg := config.Config{
		MCP: config.MCP{Servers: map[string]config.MCPServer{"fake": declared(), "nologin": declared()}},
		Agents: map[string]config.AgentConfig{
			"lead": {Role: domain.RoleProductManager, MCPTools: leadTools},
			// A list naming an act tool for a developer is refused when the
			// configuration loads; this one was never loaded, so the caller
			// itself has to leave the act tool out.
			"dev": {Role: domain.RoleDeveloper, MCPTools: []string{"fake.echo", "fake.send"}},
		},
	}
	hold, err := runstate.NewOperatorHoldStore(stores.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	records, err := NewRecords(stores.StateRoot, testProduct)
	if err != nil {
		t.Fatal(err)
	}
	return &pathFixture{
		path:   &Path{Config: cfg, Stores: stores, Hold: hold, Records: records, RedactValues: []string{fakeHeldSecret}},
		config: cfg,
		hold:   hold,
	}
}

func (f *pathFixture) lead(t *testing.T) Caller {
	t.Helper()
	caller, err := ConversationCaller(f.config, "lead", "chat-1", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	return caller
}

func (f *pathFixture) do(t *testing.T, caller Caller, requests ...Request) []Answer {
	t.Helper()
	answers, err := f.path.Do(context.Background(), caller, requests)
	if err != nil {
		t.Fatalf("the record failed: %v", err)
	}
	if len(answers) != len(requests) {
		t.Fatalf("%d answers for %d requests", len(answers), len(requests))
	}
	return answers
}

func (f *pathFixture) folder(t *testing.T) string {
	t.Helper()
	return serverFolder(t, Options{Stores: f.path.Stores}, Server{Name: "fake"})
}

// lines is how many lines a file in the fake's folder holds; none where the
// file is not there.
func (f *pathFixture) lines(t *testing.T, name string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(mcpcredential.ServerDirectory(f.path.Stores.StateRoot, testProduct, "fake"), name))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Count(data, []byte("\n"))
}

func (f *pathFixture) records(t *testing.T) []Record {
	t.Helper()
	records, unreadable, err := f.path.Records.Read()
	if err != nil || unreadable != 0 {
		t.Fatalf("read the record: %d unreadable, %v", unreadable, err)
	}
	return records
}

// recordText is every byte the record holds.
func (f *pathFixture) recordText(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(f.path.Records.Directory())
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(f.path.Records.Directory(), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		all.Write(data)
	}
	return all.String()
}

func TestACallIsRecordedBeforeItIsMadeAndSettledAfter(t *testing.T) {
	f := newPathFixture(t)
	answers := f.do(t, f.lead(t), Request{Tool: "fake.echo", Arguments: json.RawMessage(`{"query":"invoice"}`), ID: "a1", Why: "which invoice"})
	if got := answers[0]; got.Outcome != OutcomePerformed || got.Result != `{"query":"invoice"}` || got.Tool != "fake.echo" || got.ID != "a1" {
		t.Fatalf("answer = %+v", got)
	}
	if evidence := answers[0].Evidence(); !strings.Contains(evidence, "untrusted data") || !strings.HasSuffix(evidence, `{"query":"invoice"}`+"\n") {
		t.Errorf("evidence is not framed as untrusted data:\n%s", evidence)
	}
	var phases []Phase
	for _, line := range strings.Split(strings.TrimSpace(f.recordText(t)), "\n") {
		var record Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		phases = append(phases, record.Phase)
	}
	if len(phases) != 2 || phases[0] != PhaseAsked || phases[1] != PhaseSettled {
		t.Fatalf("lines written = %v, want the ask before the call and the outcome after", phases)
	}
	records := f.records(t)
	if len(records) != 1 {
		t.Fatalf("entries = %d, want 1", len(records))
	}
	record := records[0]
	want := CallerRecord{Kind: CallerConversation, Role: domain.RoleProductManager, Agent: "lead", Conversation: "chat-1", Turn: 3}
	if record.Caller != want {
		t.Errorf("caller = %+v, want %+v", record.Caller, want)
	}
	if record.Server != "fake" || record.Tool != "fake.echo" || record.Class != config.MCPRead || record.ConfigRevision != f.config.Revision() ||
		!strings.HasPrefix(record.ListingDigest, "sha256:") || string(record.Arguments) != `{"query":"invoice"}` || record.Why != "which invoice" {
		t.Errorf("what was asked is not recorded whole: %+v", record)
	}
	if record.Outcome != OutcomePerformed || record.Result != `{"query":"invoice"}` || record.ResultBytes != len(record.Result) ||
		!strings.HasPrefix(record.ResultDigest, "sha256:") || record.StartedAt.IsZero() || record.EndedAt.IsZero() || record.Tries != 1 {
		t.Errorf("what came back is not recorded whole: %+v", record)
	}
}

func TestTheRecordIsOnlyEverAddedTo(t *testing.T) {
	f := newPathFixture(t)
	f.do(t, f.lead(t), Request{Tool: "fake.echo"})
	before := f.recordText(t)
	f.do(t, f.lead(t), Request{Tool: "fake.echo"}, Request{Tool: "fake.nothing"})
	after := f.recordText(t)
	if !strings.HasPrefix(after, before) || len(after) <= len(before) {
		t.Fatalf("the record was rewritten rather than added to:\nbefore %q\nafter %q", before, after)
	}
}

func TestEveryRefusalIsOnTheClosedListAndCallsNothing(t *testing.T) {
	big := `{"text":"` + strings.Repeat("y", 17<<10) + `"}`
	cases := []struct {
		name    string
		prepare func(t *testing.T, f *pathFixture)
		request Request
		want    RefusalReason
	}{
		{name: "a tool off the list", request: Request{Tool: "fake.environment"}, want: RefusedNotOnList},
		{name: "a tool nobody declared", request: Request{Tool: "elsewhere.send", ID: "x"}, want: RefusedNotOnList},
		{name: "arguments that are not an object", request: Request{Tool: "fake.send", ID: "x", Arguments: json.RawMessage(`[1]`)}, want: RefusedArguments},
		{name: "arguments over their size", request: Request{Tool: "fake.send", ID: "x", Arguments: json.RawMessage(big)}, want: RefusedArguments},
		{name: "arguments holding a held secret", request: Request{Tool: "fake.send", ID: "x", Arguments: json.RawMessage(`{"body":"` + fakeHeldSecret + `"}`)}, want: RefusedSecret},
		{name: "a held secret written with escapes", request: Request{Tool: "fake.send", ID: "x", Arguments: json.RawMessage(`{"body":"held-secret-value-0077"}`)}, want: RefusedSecret},
		{name: "arguments holding a server's credential", request: Request{Tool: "fake.send", ID: "x", Arguments: json.RawMessage(`{"body":"` + testCredential + `"}`)}, want: RefusedSecret},
		{name: "the operator's pause", prepare: func(t *testing.T, f *pathFixture) {
			if _, err := f.hold.Hold(time.Now()); err != nil {
				t.Fatal(err)
			}
		}, request: Request{Tool: "fake.send", ID: "x"}, want: RefusedPaused},
		{name: "a credential not stored", request: Request{Tool: "nologin.echo"}, want: RefusedCredential},
		{name: "an act with no id", request: Request{Tool: "fake.send"}, want: RefusedID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newPathFixture(t)
			if c.prepare != nil {
				c.prepare(t, f)
			}
			answer := f.do(t, f.lead(t), c.request)[0]
			if answer.Outcome != OutcomeRefused || answer.Refusal != c.want {
				t.Fatalf("answer = %+v, want refused for %s", answer, c.want)
			}
			if f.lines(t, "sent.log") != 0 {
				t.Fatal("a refused act call was made")
			}
			if c.want == RefusedNotOnList && answer.Tool != "" {
				t.Errorf("a refusal names the tool it refused: %+v", answer)
			}
			// The tools this caller may not use are the two off its list; no
			// refusal names either, and none lists any other tool.
			for _, words := range []string{answer.Reason, answer.Evidence()} {
				if strings.Contains(words, "environment") || strings.Contains(words, "elsewhere") {
					t.Errorf("a refusal names a tool the caller may not use: %s", words)
				}
				for tool := range fakeToolClasses {
					if name := "fake." + tool; name != c.request.Tool && strings.Contains(words, name) {
						t.Errorf("a refusal lists tool %q: %s", name, words)
					}
				}
			}
			if c.want == RefusedCredential && !strings.Contains(answer.Reason, "only a person") {
				t.Errorf("a missing credential does not say only a person can store it: %s", answer.Reason)
			}
			records := f.records(t)
			if len(records) != 1 || records[0].Outcome != OutcomeRefused || records[0].Refusal != c.want || records[0].Phase != PhaseSettled {
				t.Fatalf("records = %+v, want the one refusal", records)
			}
			if strings.Contains(f.recordText(t), fakeHeldSecret) || strings.Contains(f.recordText(t), testCredential) {
				t.Fatal("the record holds a secret")
			}
			known := false
			for _, reason := range RefusalReasons {
				known = known || reason == c.want
			}
			if !known {
				t.Fatalf("%s is not on the closed list", c.want)
			}
		})
	}
}

func TestAnIDReusedWithOtherContentIsRefused(t *testing.T) {
	f := newPathFixture(t)
	caller := f.lead(t)
	f.do(t, caller, Request{Tool: "fake.send", ID: "s1", Arguments: json.RawMessage(`{"to":"a"}`)})
	answer := f.do(t, caller, Request{Tool: "fake.send", ID: "s1", Arguments: json.RawMessage(`{"to":"b"}`)})[0]
	if answer.Outcome != OutcomeRefused || answer.Refusal != RefusedID {
		t.Fatalf("answer = %+v, want refused for its id", answer)
	}
	if got := f.lines(t, "sent.log"); got != 1 {
		t.Fatalf("sent %d times, want once", got)
	}
}

func TestARepeatWithTheSameIDAndContentCallsNothing(t *testing.T) {
	f := newPathFixture(t)
	caller := f.lead(t)
	first := f.do(t, caller, Request{Tool: "fake.send", ID: "s1", Arguments: json.RawMessage(`{"to":"a","n":1}`)})[0]
	if first.Outcome != OutcomePerformed || first.Repeat {
		t.Fatalf("first = %+v", first)
	}
	// The same object with its keys in another order is the same content.
	again := f.do(t, caller, Request{Tool: "fake.send", ID: "s1", Arguments: json.RawMessage(`{ "n":1, "to":"a" }`)})[0]
	if again.Outcome != OutcomePerformed || !again.Repeat || again.Result != first.Result || again.Entry != first.Entry {
		t.Fatalf("repeat = %+v, want the recorded outcome", again)
	}
	inOneReply := f.do(t, caller,
		Request{Tool: "fake.send", ID: "s2", Arguments: json.RawMessage(`{"to":"c"}`)},
		Request{Tool: "fake.send", ID: "s2", Arguments: json.RawMessage(`{"to":"c"}`)})
	if !inOneReply[1].Repeat || inOneReply[1].Entry != inOneReply[0].Entry {
		t.Fatalf("a repeat in one reply = %+v", inOneReply)
	}
	if got := f.lines(t, "sent.log"); got != 2 {
		t.Fatalf("sent %d times, want twice: once for each id", got)
	}
}

func TestAKilledProcessLeavesAnActEntryThatReadsAsUnknown(t *testing.T) {
	f := newPathFixture(t)
	caller := f.lead(t)
	arguments := json.RawMessage(`{"to":"a"}`)
	// What a process killed between the ask and the outcome leaves behind.
	if err := f.path.Records.append(Record{Entry: "killed", Phase: PhaseAsked, Scope: caller.scope, Caller: caller.record, StartedAt: time.Now(),
		Server: "fake", Tool: "fake.send", Class: config.MCPAct, ID: "k1", ArgumentsDigest: digestArguments(arguments), Tries: 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.path.Records.append(Record{Entry: "killed-read", Phase: PhaseAsked, Scope: caller.scope, Caller: caller.record, StartedAt: time.Now(),
		Server: "fake", Tool: "fake.echo", Class: config.MCPRead, Tries: 1}); err != nil {
		t.Fatal(err)
	}
	records := f.records(t)
	if len(records) != 2 || records[0].Outcome != OutcomeUnknown || records[1].Outcome != OutcomeFailed {
		t.Fatalf("records = %+v, want the act unknown and the read failed", records)
	}
	answer := f.do(t, caller, Request{Tool: "fake.send", ID: "k1", Arguments: arguments})[0]
	if answer.Outcome != OutcomeUnknown || !answer.Repeat || !strings.Contains(answer.Reason, "not known") {
		t.Fatalf("answer = %+v, want the unknown outcome handed back", answer)
	}
	if f.lines(t, "sent.log") != 0 {
		t.Fatal("calling again with the id of an act whose outcome is unknown made the call")
	}
}

func TestResultsAreScrubbedOfSecrets(t *testing.T) {
	f := newPathFixture(t)
	answers := f.do(t, f.lead(t), Request{Tool: "fake.leak"}, Request{Tool: "fake.leak-error"})
	if answers[0].Outcome != OutcomePerformed || !strings.Contains(answers[0].Result, "credential") {
		t.Fatalf("leak = %+v", answers[0])
	}
	if answers[1].Outcome != OutcomeFailed || !strings.Contains(answers[1].Reason, "credential") {
		t.Fatalf("leak-error = %+v", answers[1])
	}
	for _, answer := range answers {
		for _, secret := range []string{testCredential, fakeHeldSecret} {
			if strings.Contains(answer.Result, secret) || strings.Contains(answer.Reason, secret) {
				t.Errorf("a secret reached the caller: %+v", answer)
			}
		}
	}
	text := f.recordText(t)
	if strings.Contains(text, testCredential) || strings.Contains(text, fakeHeldSecret) {
		t.Fatal("a secret reached the record")
	}
}

func TestBinaryContentIsDroppedAndTheDropSaid(t *testing.T) {
	f := newPathFixture(t)
	answer := f.do(t, f.lead(t), Request{Tool: "fake.mixed"})[0]
	if !strings.Contains(answer.Result, "a caption") || !strings.Contains(answer.Result, "resource text") {
		t.Fatalf("text and JSON did not pass through: %q", answer.Result)
	}
	if strings.Contains(answer.Result, "AAAA") || strings.Contains(answer.Result, "BBBB") {
		t.Fatalf("binary content passed through: %q", answer.Result)
	}
	if len(answer.Dropped) != 2 || answer.Dropped[0].Type != "image" || answer.Dropped[0].Bytes < 1000 || answer.Dropped[1].Type != "resource" {
		t.Fatalf("dropped = %+v, want the image and the binary resource with their sizes", answer.Dropped)
	}
	if evidence := answer.Evidence(); !strings.Contains(evidence, "Dropped from the result: image content") {
		t.Errorf("the drop is not said:\n%s", evidence)
	}
	if record := f.records(t)[0]; len(record.Dropped) != 2 || strings.Contains(record.Result, "AAAA") {
		t.Errorf("the record does not say what was dropped: %+v", record)
	}
}

func TestResultsAreBoundedToTheDesignsLimits(t *testing.T) {
	f := newPathFixture(t)
	answers := f.do(t, f.lead(t),
		Request{Tool: "fake.big", Arguments: json.RawMessage(`{"bytes":40000}`)},
		Request{Tool: "fake.big", Arguments: json.RawMessage(`{"bytes":300000}`)},
		Request{Tool: "fake.big", Arguments: json.RawMessage(`{"bytes":10}`)})
	if len(answers[0].Result) != 32<<10 || !answers[0].Cut || answers[0].ResultBytes != 40000 {
		t.Errorf("first = %d bytes, cut %v, of %d; want 32 KiB of 40000", len(answers[0].Result), answers[0].Cut, answers[0].ResultBytes)
	}
	if len(answers[1].Result) != 32<<10 || !answers[1].Cut {
		t.Errorf("second = %d bytes, want 32 KiB", len(answers[1].Result))
	}
	if answers[2].Outcome != OutcomeRefused || answers[2].Refusal != RefusedLimit {
		t.Errorf("third = %+v, want refused once the reply's 64 KiB are used", answers[2])
	}
	if evidence := answers[0].Evidence(); !strings.Contains(evidence, "The result was cut: 32768 of its 40000 bytes") {
		t.Errorf("the cut is not said:\n%s", evidence[:200])
	}
	records := f.records(t)
	if len(records[0].Result) != 40000 || records[0].ResultCut {
		t.Errorf("the record kept %d bytes of 40000", len(records[0].Result))
	}
	if len(records[1].Result) != 256<<10 || !records[1].ResultCut || records[1].ResultBytes != 300000 {
		t.Errorf("the record kept %d bytes of %d, cut %v; want 256 KiB", len(records[1].Result), records[1].ResultBytes, records[1].ResultCut)
	}
}

func TestAReadIsTriedOnceMoreAndAnActNever(t *testing.T) {
	f := newPathFixture(t)
	caller := f.lead(t)
	read := f.do(t, caller, Request{Tool: "fake.flaky"})[0]
	if read.Outcome != OutcomePerformed || read.Result != "answered the second time" {
		t.Fatalf("read = %+v, want it answered when tried once more", read)
	}
	if got := f.lines(t, "flaky.log"); got != 2 {
		t.Fatalf("the read was put to the server %d times, want twice", got)
	}
	act := f.do(t, caller, Request{Tool: "fake.flaky-act", ID: "f1"})[0]
	if act.Outcome != OutcomeUnknown || !strings.Contains(act.Reason, "check before asking again") {
		t.Fatalf("act = %+v, want unknown", act)
	}
	if got := f.lines(t, "flaky-act.log"); got != 1 {
		t.Fatalf("the act was put to the server %d times, want once", got)
	}
	records := f.records(t)
	if records[0].Tries != 2 || records[1].Tries != 1 || records[1].Outcome != OutcomeUnknown {
		t.Fatalf("records = %+v", records)
	}
}

func TestAScheduledCallerReadsAndIsRefusedAnAct(t *testing.T) {
	f := newPathFixture(t)
	if _, err := ScheduledCaller(f.config, "intake", []string{"fake.echo", "fake.send"}); err == nil || !strings.Contains(err.Error(), "read tools only") {
		t.Fatalf("a recurring task holding an act tool was not refused: %v", err)
	}
	caller, err := ScheduledCaller(f.config, "intake", []string{"fake.echo"})
	if err != nil {
		t.Fatal(err)
	}
	answers := f.do(t, caller, Request{Tool: "fake.echo", Arguments: json.RawMessage(`{"since":"monday"}`)}, Request{Tool: "fake.send", ID: "t1"})
	if answers[0].Outcome != OutcomePerformed || answers[0].Result != `{"since":"monday"}` {
		t.Fatalf("read = %+v, want it made", answers[0])
	}
	if answers[1].Outcome != OutcomeRefused || answers[1].Refusal != RefusedNotOnList {
		t.Fatalf("act = %+v, want it refused", answers[1])
	}
	if f.lines(t, "sent.log") != 0 {
		t.Fatal("a recurring task's act call was made")
	}
	if record := f.records(t)[0]; record.Caller.Kind != CallerScheduled || record.Caller.Task != "intake" || record.Scope != "task:intake" {
		t.Fatalf("caller recorded = %+v", record.Caller)
	}
}

func TestADeveloperHoldsReadToolsOnly(t *testing.T) {
	f := newPathFixture(t)
	caller, err := DeveloperCaller(f.config, "dev", "run-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if limits := caller.Limits(); limits.Rounds != 8 || limits.CallsPerMessage != 20 {
		t.Errorf("developer limits = %+v, want 8 rounds and 20 calls an attempt", limits)
	}
	answers := f.do(t, caller, Request{Tool: "fake.echo"}, Request{Tool: "fake.send", ID: "d1"})
	if answers[0].Outcome != OutcomePerformed || answers[1].Refusal != RefusedNotOnList {
		t.Fatalf("answers = %+v", answers)
	}
	if record := f.records(t)[0]; record.Caller.Run != "run-1" || record.Caller.Attempt != 2 || record.Caller.Kind != CallerDeveloper {
		t.Fatalf("caller recorded = %+v", record.Caller)
	}
}

func TestLimitsOnCallsAndRoundsAreSpentAcrossAMessage(t *testing.T) {
	f := newPathFixture(t)
	caller := f.lead(t)
	six := make([]Request, 6)
	for i := range six {
		six[i] = Request{Tool: "fake.echo"}
	}
	answers := f.do(t, caller, six...)
	if answers[4].Outcome != OutcomePerformed || answers[5].Refusal != RefusedLimit {
		t.Fatalf("the sixth call of a reply = %+v, want refused", answers[5])
	}
	for range 3 {
		f.do(t, caller, Request{Tool: "fake.echo"})
	}
	if answer := f.do(t, caller, Request{Tool: "fake.echo"})[0]; answer.Refusal != RefusedLimit {
		t.Fatalf("a fifth round = %+v, want refused", answer)
	}
	if widened := caller.Tightened(Limits{CallsPerReply: 50, CallTimeout: time.Hour}).Limits(); widened.CallsPerReply != 5 || widened.CallTimeout != 60*time.Second {
		t.Fatalf("limits were widened: %+v", widened)
	}
}

func TestAServerThatNeverAnswersIsEndedAtTheLimitAndTheCallerCarriesOn(t *testing.T) {
	if got := DefaultLimits(CallerDeveloper).CallTimeout; got != 60*time.Second {
		t.Fatalf("a call's limit is %s, want 60 seconds", got)
	}
	f := newPathFixture(t)
	// The limit is shortened so the test does not wait a minute; the same
	// watchdog ends the call at 60 seconds.
	caller := f.lead(t).Tightened(Limits{CallTimeout: 1500 * time.Millisecond})
	started := time.Now()
	answers := f.do(t, caller, Request{Tool: "fake.hang"}, Request{Tool: "fake.echo"})
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("the call took %s; a hung server held the caller", elapsed)
	}
	if answers[0].Outcome != OutcomeFailed || !strings.Contains(answers[0].Reason, "did not answer within 1.5s") {
		t.Fatalf("hang = %+v, want failed for not answering in time", answers[0])
	}
	if answers[1].Outcome != OutcomeFailed || !strings.Contains(answers[1].Reason, "not made") {
		t.Fatalf("the call after it = %+v, want it said not made", answers[1])
	}
	if !sleeperGone(t, f.folder(t)) {
		t.Fatal("the hung server's process group outlived the call")
	}
	records := f.records(t)
	if records[0].Outcome != OutcomeFailed || !strings.Contains(records[0].Reason, "did not answer within") {
		t.Fatalf("record = %+v", records[0])
	}

	act := f.do(t, caller, Request{Tool: "fake.hang-act", ID: "h1"})[0]
	if act.Outcome != OutcomeUnknown || !strings.Contains(act.Reason, "did not answer within") {
		t.Fatalf("hang-act = %+v, want unknown", act)
	}
	if after := f.do(t, caller, Request{Tool: "fake.echo"})[0]; after.Outcome != OutcomePerformed {
		t.Fatalf("the caller could not carry on: %+v", after)
	}
}
