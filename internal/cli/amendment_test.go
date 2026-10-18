package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// What a run left behind is what the operator finds: the proposal is read from
// the durable log by a process that had nothing to do with the run that raised
// it, decided, and the decision is what is recorded. The document is untouched
// throughout, which is the difference between a proposal and a deferred edit.
func TestAProposalIsReadAndDecidedLongAfterTheRunThatRaisedIt(t *testing.T) {
	configPath, project := amendmentProject(t)
	document := filepath.Join(project, "docs", "designs", "v1-design.md")
	before, err := os.ReadFile(document)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	proposal := recordProposal(t, "amendment-0123456789abcdef0123456789abcdef")

	stdout, stderr, code := runCLI(t, "amendment", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{proposal.ID, "v1-design", "decided by the architect", "say which ordering holds"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list stdout = %q, want it to contain %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "amendment", "approve", "--config", configPath,
		"--reason", "the ordering was never settled", proposal.ID)
	if code != 0 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	// An approval authorizes the owner; it does not perform the change, and it
	// says so, because an approval everybody reads as "done" is how an approved
	// change quietly never happens.
	for _, want := range []string{"approved", "nothing was written to the artifact", "amend v1-design as the architect"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("approve stdout = %q, want it to contain %q", stdout, want)
		}
	}
	after, err := os.ReadFile(document)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("approving edited the document:\n%s", after)
	}

	// Decided is not pending, and the record of what was argued and what came of
	// it stays readable.
	stdout, _, code = runCLI(t, "amendment", "list", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "no proposed changes are waiting") {
		t.Fatalf("list after deciding = %q (code %d)", stdout, code)
	}
	stdout, _, code = runCLI(t, "amendment", "list", "--config", configPath, "--all")
	if code != 0 || !strings.Contains(stdout, proposal.ID) || !strings.Contains(stdout, "the ordering was never settled") {
		t.Fatalf("list --all = %q (code %d)", stdout, code)
	}

	// A proposal somebody has already acted on is not decided again.
	if _, stderr, code = runCLI(t, "amendment", "decline", "--config", configPath, "--reason", "no", proposal.ID); code == 0 {
		t.Fatal("declining a settled proposal succeeded")
	} else if !strings.Contains(stderr, "already approved") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// What the operator is told about who decides has to be what actually happens.
// The owning role decides a design change from its conversation, so the
// operator is not told one waits on him; a goals change that does not say it is
// consistent with their intent is his, and is said to wait on him. The
// operator's own command still decides either, as the owner's override.
func TestTheOperatorIsToldWhoDecidesEachProposal(t *testing.T) {
	configPath, _ := amendmentProject(t)
	design := recordProposal(t, "amendment-0123456789abcdef0123456789abcdef")
	goals := recordProposal(t, "amendment-fedcba9876543210fedcba9876543210", func(p *amendment.Proposal) {
		p.Artifact, p.Kind, p.Owner = "v1-goals", artifact.KindGoals, domain.RoleProductManager
	})

	stdout, stderr, code := runCLI(t, "amendment", "show", "--config", configPath, design.ID)
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "waiting on you") || !strings.Contains(stdout, "waiting on the architect") {
		t.Fatalf("show of a design change does not say the architect decides it: %q", stdout)
	}
	stdout, stderr, code = runCLI(t, "amendment", "show", "--config", configPath, goals.ID)
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "waiting on you") || !strings.Contains(stdout, "decided by the operator") {
		t.Fatalf("show of a goals change that does not say its intent does not say it waits on the operator: %q", stdout)
	}

	usage, _, code := runCLI(t, "amendment", "help")
	if code != 0 {
		t.Fatalf("help code = %d", code)
	}
	for _, want := range []string{"The role that owns the document decides a proposal against it", "You are told\nafterwards and are not asked"} {
		if !strings.Contains(usage, want) {
			t.Fatalf("the help does not say %q: %q", want, usage)
		}
	}
	if strings.Contains(usage, "a\nproposal waits on you whoever owns the document") {
		t.Fatalf("the help still says every proposal waits on the operator: %q", usage)
	}

	// The operator's command still decides a design change, as his override,
	// and records him as the one who decided it.
	if _, stderr, code = runCLI(t, "amendment", "approve", "--config", configPath, "--reason", "yes", design.ID); code != 0 {
		t.Fatalf("approve code = %d, stderr = %q", code, stderr)
	}
	stateRoot, err := resolvedTestStateRoot()
	if err != nil {
		t.Fatalf("resolvedTestStateRoot() error = %v", err)
	}
	store, err := runstate.NewAmendmentStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	records, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	decision, decided := amendment.DecisionOn(records, design.ID)
	if !decided {
		t.Fatal("nothing was recorded for the proposal")
	}
	if decision.Decider != amendment.DeciderOperator || decision.Authority != domain.RoleArchitect {
		t.Fatalf("decision = %#v, want the operator deciding under the architect's authority", decision)
	}
}

func TestDecliningKeepsWhyAndRefusesToInventOne(t *testing.T) {
	configPath, _ := amendmentProject(t)
	proposal := recordProposal(t, "amendment-0123456789abcdef0123456789abcdef")

	// A proposal turned down with no reason is one the same argument arrives to
	// make again, so the harness will not record a decline that says nothing.
	if _, stderr, code := runCLI(t, "amendment", "decline", "--config", configPath, proposal.ID); code == 0 {
		t.Fatal("declining with no reason succeeded")
	} else if !strings.Contains(stderr, "records why it was turned down") {
		t.Fatalf("stderr = %q", stderr)
	}

	stdout, stderr, code := runCLI(t, "amendment", "decline", "--config", configPath,
		"--reason", "the design is right and the work item was wrong", proposal.ID)
	if code != 0 {
		t.Fatalf("decline code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "declined") || !strings.Contains(stdout, "the work item was wrong") {
		t.Fatalf("decline stdout = %q", stdout)
	}
}

func TestOneOwnersQueueIsReadableOnItsOwn(t *testing.T) {
	configPath, _ := amendmentProject(t)
	design := recordProposal(t, "amendment-0123456789abcdef0123456789abcdef")
	goals := recordProposal(t, "amendment-fedcba9876543210fedcba9876543210", func(p *amendment.Proposal) {
		p.Artifact = "v1-goals"
		p.Kind = artifact.KindGoals
		p.Owner = domain.RoleProductManager
	})

	stdout, stderr, code := runCLI(t, "amendment", "list", "--config", configPath, "--owner", "architect", "--json")
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	var listed struct {
		Proposals []amendment.Proposal `json:"proposals"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout)
	}
	if len(listed.Proposals) != 1 || listed.Proposals[0].ID != design.ID {
		t.Fatalf("architect queue = %#v", listed.Proposals)
	}
	if strings.Contains(stdout, goals.ID) {
		t.Fatalf("the product manager's queue leaked into the architect's: %q", stdout)
	}
}

func TestAnOwnerNobodyHasIsRefusedRatherThanReadAsAnEmptyQueue(t *testing.T) {
	configPath, _ := amendmentProject(t)
	recordProposal(t, "amendment-0123456789abcdef0123456789abcdef")

	// "nothing is waiting" would be an answer about the queue, and this is an
	// answer about the typing.
	_, stderr, code := runCLI(t, "amendment", "list", "--config", configPath, "--owner", "architetc")
	if code == 0 {
		t.Fatal("listing succeeded for a role nobody owns anything as")
	}
	if !strings.Contains(stderr, "architect") {
		t.Fatalf("stderr does not name the roles that own artifacts: %q", stderr)
	}
}

func TestShowingAProposalNobodyRaisedSaysSo(t *testing.T) {
	configPath, _ := amendmentProject(t)

	_, stderr, code := runCLI(t, "amendment", "show", "--config", configPath, "amendment-0123456789abcdef0123456789abcdef")
	if code == 0 {
		t.Fatal("show succeeded for a proposal nobody raised")
	}
	if !strings.Contains(stderr, "was raised for this product") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// The proposal id read out of a listing is typed first and the options after
// it, which is the order the usage text and the documentation state. What
// proves the reason after the id was read as the reason is that it is what the
// decision records.
func TestAmendmentOptionsAreReadAfterTheProposalId(t *testing.T) {
	configPath, _ := amendmentProject(t)
	proposal := recordProposal(t, "amendment-0123456789abcdef0123456789abcdef")

	stdout, stderr, code := runCLI(t, "amendment", "show", proposal.ID, "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, proposal.ID) {
		t.Fatalf("show stdout = %q, want the proposal named before the flags", stdout)
	}

	stdout, stderr, code = runCLI(t, "amendment", "decline", proposal.ID,
		"--config", configPath, "--reason", "the design is right and the item is wrong")
	if code != 0 {
		t.Fatalf("decline code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "the design is right and the item is wrong") {
		t.Fatalf("decline stdout = %q, want the reason typed after the id kept with the decision", stdout)
	}
}

// amendmentProject writes a project with one governed document and points the
// durable state at a temporary root, so the log these commands read is this
// test's own.
func amendmentProject(t *testing.T) (configPath, project string) {
	t.Helper()
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath = writeConfig(t, validConfig)
	project = filepath.Dir(configPath)
	writeArtifact(t, project, "docs/designs/v1-design.md", `---
id: v1-design
kind: design
title: V1 design
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-17T12:00:00Z
      reason: recorded when identity arrived
---

The ordering is unspecified.
`)
	return configPath, project
}

// recordProposal puts a proposal in the durable log the way a finished run
// leaves one behind, so what the commands read is what a run actually wrote
// rather than something they were handed.
func recordProposal(t *testing.T, id string, adjust ...func(*amendment.Proposal)) amendment.Proposal {
	t.Helper()
	stateRoot, err := resolvedTestStateRoot()
	if err != nil {
		t.Fatalf("resolvedTestStateRoot() error = %v", err)
	}
	store, err := runstate.NewAmendmentStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	proposal := amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.1.5",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "v1-design",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        "say which ordering holds",
		Why:           "the work item cannot be implemented against both",
		RaisedAt:      time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
	}
	for _, change := range adjust {
		change(&proposal)
	}
	if err := store.Append(proposal); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	return proposal
}
