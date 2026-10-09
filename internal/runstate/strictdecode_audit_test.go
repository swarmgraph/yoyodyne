package runstate

// The audit of every place this repository refuses a record for carrying a field
// it does not know, held to the code it describes.
//
// A strict read is right in two places and wrong everywhere else. It is right
// before a write, where a field stepped over on the way in is a field lost on
// the way out; and it is right in a validator, where the refusal is a visible
// failure somebody is owed — an agent's reply that is not what it was asked for,
// a gate that declines to proceed on a record it can only read part of. It is
// wrong in a long-lived reader that only says what a record holds: there, the
// refusal is the dashboard thirty-one hours stale and the Slack sink silent,
// which is how three such readers wedged in three days and why
// yoyodyne-ifd.428.9 and .428.13 exist.
//
// So every strict site is listed below with which of those it is, and this fails
// on one that is not listed: a new strict reader fails here rather than at the
// next landing that adds a field. It fails on a listed site that is gone too, so
// the table stays the account of what is there rather than of what once was.
//
// What the sweep sees is every call to DisallowUnknownFields, on any receiver,
// and every call to decodeStrictly, in the swept trees, keyed by the function it
// sits in. What it does not see is strictness reached another way — a type whose
// own UnmarshalJSON refuses by hand, a Validate that refuses a value it does not
// know. Those refuse a newer build's record too, and are what a reader of this
// file should know it is not watching for.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// strictTrees are the source trees swept, from this package's directory.
var strictTrees = []string{"../../internal", "../../cmd"}

// strictClass is what makes one strict site right.
type strictClass string

const (
	// strictDoor is a function both of a record's doors go through, whose strict
	// branch is taken only by the reads that precede a write, and whose tolerant
	// branch every read-only surface goes through. The helpers the doors are built
	// from are doors too.
	strictDoor strictClass = "door"
	// strictWriter reads a record that is about to be written back, or one read
	// under the lease of a caller about to act on it.
	strictWriter strictClass = "writer"
	// strictValidator refuses as its answer, and the refusal is a visible failure:
	// an agent's reply held to what it was asked to write, a gate that declines
	// to proceed rather than decide on part of a record, a test holding a
	// document to its schema.
	strictValidator strictClass = "validator"
)

type strictSite struct {
	class strictClass
	why   string
}

// strictSites is every strict site in the tree, by file and function.
var strictSites = map[string]strictSite{
	// The doors.
	"internal/runstate/tolerantread.go:decodeStrictly":                               {strictDoor, "the strict door itself"},
	"internal/runstate/tolerantread.go:decodeTolerating":                             {strictDoor, "tries the strict door first and steps over only an unknown field"},
	"internal/runstate/store.go:(*Store).load":                                       {strictDoor, "Load is strict for the pipeline's writes; Read, the listings, and the stream listing are tolerant"},
	"internal/runstate/conversation.go:(*ConversationStore).decode":                  {strictDoor, "Load is strict for the resuming agent; Read and the listings are tolerant"},
	"internal/runstate/exchange.go:(*ExchangeStore).load":                            {strictDoor, "Load is strict for the conductor that answers and settles; Read, List, and the spend totals are tolerant"},
	"internal/runstate/directive.go:(*DirectiveStore).load":                          {strictDoor, "Find and Pausing are strict for settling and gating; List, which the read model and the sink read, is tolerant"},
	"internal/runstate/firstseen.go:(*FirstSeenStore).read":                          {strictDoor, "Observe is strict under its lock before it rewrites the record; FirstSeen, which the read model reads, is tolerant"},
	"internal/runstate/capacityserved.go:(*CapacityServedStore).read":                {strictDoor, "Record is strict because it rewrites what it read; List, which the read model, the sink, and the watch read, is tolerant"},
	"internal/runstate/trackerlisting.go:(*TrackerListingStore).read":                {strictDoor, "Failed and Answered are strict because they rewrite what they read; Read, which the read model reads, is tolerant"},
	"internal/runstate/mergequeue.go:(*MergeQueueStore).decode":                      {strictDoor, "Admit and its readback are strict because they rewrite and settle what they read; Entries is tolerant"},
	"internal/runstate/mergequeuegeneration.go:(*MergeQueueStore).decodeGenerations": {strictDoor, "RecordGeneration is strict because it rewrites what it reads; Generations and VerifiedGeneration are tolerant"},
	"internal/runstate/mergequeuelanding.go:(*MergeQueueStore).decodeLanding":        {strictDoor, "RecordLanding is strict because it extends what it reads; Landing is tolerant"},

	// Reads that precede a write.
	"internal/runstate/workflowinstance.go:(*Store).LoadWorkflowInstance": {strictWriter, "an instance is only ever read to be advanced and saved back"},
	"internal/runstate/escalation.go:(*EscalationStore).read":             {strictWriter, "the escalation docket is claimed, advanced, and rewritten under its lease"},
	"internal/runstate/rerun.go:(*RerunStore).read":                       {strictWriter, "the rerun docket is claimed, settled, and withdrawn under its lease"},
	"internal/runstate/sweep.go:(*SweepStore).load":                       {strictWriter, "a recurring task's claim is read to be taken or released"},
	"internal/runstate/triage.go:(*TriageStore).load":                     {strictWriter, "the triage counters are read, incremented, and saved back"},
	"internal/runstate/sidestream.go:(*SideStreamStore).Load":             {strictWriter, "a side stream is read to be answered and settled; its listing counts the per-agent bound"},
	"internal/runstate/memory.go:(*MemoryStore).decodeTip":                {strictWriter, "a memory tip is read only by the write it numbers, under the agent's lock, and one that will not decode is rebuilt from the history rather than refused"},

	// Gates: a refusal the caller declines to proceed on and reports.
	"internal/runstate/hold.go:(*OperatorHoldStore).Held":                         {strictValidator, "a hold nobody can read is never spent through as though it were absent; the read model reports it as a problem and the sink reads it past"},
	"internal/runstate/intake.go:(*IntakeHoldStore).Held":                         {strictValidator, "an intake hold nobody can read is never taken for a clear one; the read model reports it and the sink reads it past"},
	"internal/runstate/provideroutage.go:(*ProviderOutageStore).Standing":         {strictValidator, "an outage nobody can read is never started through; the read model reports it and the sink reads it past"},
	"internal/runstate/launchsettingshold.go:(*LaunchSettingsHoldStore).Standing": {strictWriter, "a hold on developers nobody can read is never pulled through as though absent, and the same read precedes every write of the record"},
	"internal/runstate/divergedtarget.go:(*DivergedTargetStore).Standing":         {strictWriter, "a divergence nobody can read is never pulled through as though absent, and the same read precedes every write of the record"},
	"internal/runstate/conversation.go:(*ConversationStore).readHolder":           {strictValidator, "who is mid-turn with an agent is refused rather than guessed at from part of a record"},
	"internal/runstate/presence.go:readRunHolder":                                 {strictValidator, "whether a process is behind a run is refused rather than guessed at from part of a stamp; the reading then reports the run as found"},
	"internal/runstate/humanact.go:readHumanAct":                                  {strictValidator, "a gate is passed only by an act the harness can read whole; one it cannot is refused where the gate is asked, so the gate holds and the refusal is named rather than read past"},
	"internal/runstate/stop.go:(*Store).StopRequested":                            {strictValidator, "a run acts on a stop request, and one it cannot read fails its step rather than being ignored"},
	"internal/runstate/release.go:(*Store).ReleasedWait":                          {strictValidator, "a run acts on the operator's release of a wait, and one it cannot read fails its step rather than being ignored"},
	"internal/runstate/passcursor.go:(*PassCursorStore).Load":                     {strictWriter, "an instance's cursor is read to be advanced and written back, and a cursor nobody can read must stop its passes rather than hand them nothing or everything"},
	"internal/runstate/lanereport.go:(*LaneReportStore).decode":                   {strictWriter, "the writer numbers the next version from the history and writes it back; Current and History read through the tolerant door"},
	"internal/runstate/memory.go:decodeMemoryRevision":                            {strictValidator, "a revision that will not decode is reported as a problem against its line, and the lines beside it are read"},
	"internal/readmodel/attention.go:(*Attention).UnmarshalJSON":                  {strictValidator, "no stored record is read through it; it holds the dashboard's fixtures and scripted readers to the shape"},

	// An agent's reply, held to exactly what it was asked to write: the block
	// was written seconds ago by a role this build instructed, and the refusal
	// reaches the turn as an error naming the block.
	"internal/amendment/amendment.go:Decode":            {strictValidator, "an amendment block in an agent's reply"},
	"internal/artifact/write.go:DecodeWrites":           {strictValidator, "a document-write block in an owning role's conversation reply"},
	"internal/chat/concern.go:decodeConcerns":           {strictValidator, "a concern block in a conversation reply"},
	"internal/chat/intervention.go:extractIntervention": {strictValidator, "an intervention block in a conversation reply"},
	"internal/chat/lanereport.go:decodeLaneReport":      {strictValidator, "a lane report block in a program manager's reply"},
	"internal/chat/memory.go:decodeMemoryWrites":        {strictValidator, "a memory block in a conversation reply"},
	"internal/chat/restart.go:decodeRestart":            {strictValidator, "a restart block in a conversation reply"},
	"internal/chat/proposal.go:decodeProposals":         {strictValidator, "a proposal block in a conversation reply"},
	"internal/chat/tracker.go:decodeTrackerActions":     {strictValidator, "a tracker-action block in a conversation reply"},
	"internal/evaluation/evaluation.go:Decode":          {strictValidator, "an evaluation block in an agent's reply"},
	"internal/exchange/exchange.go:Decode":              {strictValidator, "an ask block in an agent's reply"},
	"internal/landing/landing.go:Decode":                {strictValidator, "a landing claim in a developer's reply"},
	"internal/report/report.go:Decode":                  {strictValidator, "a report block in an agent's reply"},
	"internal/repositoryread/repositoryread.go:Decode":  {strictValidator, "a repository-read block in an agent's reply"},
	"internal/research/research.go:Decode":              {strictValidator, "a research block in an agent's reply"},
	"internal/selfcheck/selfcheck.go:Decode":            {strictValidator, "a verification block in a developer's reply"},
	"internal/sidestream/contract.go:decodeSide":        {strictValidator, "a side-stream block in an agent's reply"},
	"internal/sweep/sweep.go:Decode":                    {strictValidator, "a sweep block in a role's reply"},

	// Tests holding a document to its schema.
	"internal/backend/declarative_test.go:TestADeclaredDialectCannotStateAWait":                         {strictValidator, "a test holding a declared dialect to its schema"},
	"internal/cli/skill_repository_test.go:TestTheSetupSkillExampleIsAReportThisBuildCouldHaveProduced": {strictValidator, "a test holding the skill's example to the report schema"},
	"internal/dashboard/page_test.go:strict":                                                            {strictValidator, "a test holding the dashboard's documents to their schema"},
}

func TestEveryStrictDecodeIsAWriterOrAValidator(t *testing.T) {
	found := map[string]token.Position{}
	fileSet := token.NewFileSet()
	for _, tree := range strictTrees {
		err := filepath.WalkDir(tree, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			parsed, err := parser.ParseFile(fileSet, path, nil, 0)
			if err != nil {
				return err
			}
			relative := strings.TrimPrefix(filepath.ToSlash(path), "../../")
			for _, declaration := range parsed.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Body == nil {
					continue
				}
				ast.Inspect(function.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok || !refusesUnknownFields(call) {
						return true
					}
					key := relative + ":" + functionName(function)
					if _, seen := found[key]; !seen {
						found[key] = fileSet.Position(call.Pos())
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("sweep %s: %v", tree, err)
		}
	}

	var unlisted []string
	for key, at := range found {
		if _, listed := strictSites[key]; !listed {
			unlisted = append(unlisted, key+" (at "+at.String()+")")
		}
	}
	sort.Strings(unlisted)
	for _, key := range unlisted {
		t.Errorf("%s refuses a record for a field it does not know and is not in the audit. If it only reads a record to say what it holds, read it through the tolerant door (decodeTolerating, or a store's Read) instead; if the read precedes a write, or the refusal is a visible failure somebody is owed, list it here with which and why.", key)
	}
	var stale []string
	for key := range strictSites {
		if _, present := found[key]; !present {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("%s is in the audit and no longer refuses anything; take it out of the table", key)
	}
	for key, site := range strictSites {
		switch site.class {
		case strictDoor, strictWriter, strictValidator:
		default:
			t.Errorf("%s is listed as %q, which is none of the classes a strict read can be right in", key, site.class)
		}
		if strings.TrimSpace(site.why) == "" {
			t.Errorf("%s is listed without saying why it is strict", key)
		}
	}
}

// refusesUnknownFields reports a call that makes a decode refuse a field it does
// not know: DisallowUnknownFields on any receiver, or the strict door here.
func refusesUnknownFields(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name == "DisallowUnknownFields" || fun.Sel.Name == "decodeStrictly"
	case *ast.Ident:
		return fun.Name == "decodeStrictly"
	}
	return false
}

// functionName names a declaration the way the table does: a method by its
// receiver type, pointer included, and a function by its name.
func functionName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	receiver := function.Recv.List[0].Type
	pointer := ""
	if star, ok := receiver.(*ast.StarExpr); ok {
		pointer = "*"
		receiver = star.X
	}
	if index, ok := receiver.(*ast.IndexExpr); ok {
		receiver = index.X
	}
	name := "?"
	if ident, ok := receiver.(*ast.Ident); ok {
		name = ident.Name
	}
	return "(" + pointer + name + ")." + function.Name.Name
}
