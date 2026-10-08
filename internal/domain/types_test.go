package domain

import (
	"slices"
	"testing"
)

func TestValidateIdentifier(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"yoyodyne", "product-1", "a1"} {
		if err := ValidateIdentifier("identifier", value); err != nil {
			t.Errorf("ValidateIdentifier(%q) returned error: %v", value, err)
		}
	}

	for _, value := range []string{"", "Yoyodyne", "two_words", "-leading", "trailing-"} {
		if err := ValidateIdentifier("identifier", value); err == nil {
			t.Errorf("ValidateIdentifier(%q) returned nil", value)
		}
	}
}

func TestAgentRoleValid(t *testing.T) {
	t.Parallel()

	for _, role := range Roles() {
		if !role.Valid() {
			t.Errorf("Valid() = false for role %q", role)
		}
	}

	// A typo, a role from another tool, a role that has not been added to the
	// harness, and no role at all: none of them name authority anybody wrote.
	for _, role := range []AgentRole{"", "developor", "Developer", "security-reviewer", "tech-lead"} {
		if role.Valid() {
			t.Errorf("Valid() = true for role %q", role)
		}
	}
}

func TestRolesIsTheWholeSet(t *testing.T) {
	t.Parallel()

	want := []AgentRole{RoleProductManager, RoleArchitect, RoleDevelopmentManager, RoleDeveloper, RoleReviewer, RoleProgramManager}
	got := Roles()
	if len(got) != len(want) {
		t.Fatalf("Roles() = %v, want %v", got, want)
	}
	for index, role := range want {
		if got[index] != role {
			t.Fatalf("Roles()[%d] = %q, want %q", index, got[index], role)
		}
	}
}

// A backend identifier is checked for shape and nothing else. Which backends a
// project may name, and which roles each of them serves, moved to the registry
// in internal/backend when a project became able to declare a provider of its
// own: a durable record naming a provider has to stay readable whether or not
// that provider is still configured, and this package is what reads it back.
func TestBackendValidIsAboutShapeAlone(t *testing.T) {
	t.Parallel()

	// The two this build ships, and the shape a project's own provider takes.
	for _, named := range []Backend{BackendClaudeCode, BackendCodex, "my-openai-harness"} {
		if !named.Valid() {
			t.Errorf("Valid() = false for backend %q", named)
		}
	}

	// Nothing that is not an identifier at all.
	for _, named := range []Backend{"", "Claude Code", "carrier pigeon", "-leading", "under_score"} {
		if named.Valid() {
			t.Errorf("Valid() = true for backend %q", named)
		}
	}
}

// The two questions an executor answers are deliberately not the same one. What
// the harness recognizes decides whether a marker may be written; what is a
// developer run decides whether an item may be selected — and an unrecognized
// marker answers no to both, so a typo costs an item nobody pulls rather than
// the run the marker exists to save.
func TestAnUnrecognizedExecutorIsStillNotADeveloperRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executor     WorkItemExecutor
		valid        bool
		developerRun bool
	}{
		{name: "none named", executor: "", valid: false, developerRun: true},
		{name: "whitespace only", executor: "  ", valid: false, developerRun: true},
		{name: "a conversation with a named role", executor: ConversationWith(RoleArchitect), valid: true, developerRun: false},
		// The bare marker is the same case as a typo for writing and the opposite of
		// it for selection: nothing may be marked with it now that a marker names a
		// role, and every item marked with it before is still work no run carries.
		{name: "a conversation naming no role", executor: WorkItemExecutorConversation, valid: false, developerRun: false},
		{name: "a conversation with a role that is not one", executor: "conversation:security-reviewer", valid: false, developerRun: false},
		{name: "a role that is not an executor", executor: "architect", valid: false, developerRun: false},
		{name: "a typo", executor: "converstaion", valid: false, developerRun: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.executor.Valid(); got != tt.valid {
				t.Fatalf("Valid() = %v, want %v", got, tt.valid)
			}
			if got := tt.executor.DeveloperRun(); got != tt.developerRun {
				t.Fatalf("DeveloperRun() = %v, want %v", got, tt.developerRun)
			}
		})
	}
}

// The marker's whole point is that somebody can be named from it. Every role
// that holds one conversation has one, because which roles carry work in
// conversation is a product judgement rather than a fact about the harness, and a
// role left out of the vocabulary is work that would have to be marked with a lie
// or not marked at all. The program manager is the exception: its role is filled
// by instances, one per lane, so the marker would name no conversation in
// particular, and its design hands it work through its passes rather than
// through items marked for it.
func TestEveryRoleHasAnExecutorThatNamesIt(t *testing.T) {
	t.Parallel()

	if ConversationWith(RoleProgramManager).Valid() {
		t.Fatal("an item may be marked for the program manager's conversation, which names no instance")
	}
	for _, role := range Roles() {
		if role == RoleProgramManager {
			continue
		}
		executor := ConversationWith(role)
		if !executor.Valid() {
			t.Fatalf("ConversationWith(%q) = %q, which is not an executor an item may be marked with", role, executor)
		}
		if executor.Role() != role {
			t.Fatalf("%q names %q, want %q", executor, executor.Role(), role)
		}
		if executor.DeveloperRun() {
			t.Fatalf("%q reads as a developer run", executor)
		}
	}
}

// A marker that names no role somebody could open a conversation with says so,
// rather than being read as naming one. Work marked before the marker carried a
// role is the case that matters: it is unattributed, and attributing it to a
// role nobody chose would send an operator to somebody who was never handed it.
func TestAMarkerThatNamesNoRoleSaysSo(t *testing.T) {
	t.Parallel()

	for _, executor := range []WorkItemExecutor{
		"",
		WorkItemExecutorConversation,
		"conversation:",
		"conversation:security-reviewer",
		"conversation:architect:extra",
		"converstaion:architect",
		"architect",
	} {
		if role := executor.Role(); role != "" {
			t.Fatalf("%q names the role %q, want no role at all", executor, role)
		}
	}
}

// Every role is a name prose gives a role, the program manager among them now
// that its authority is written in code — and a name that is no role is not one.
func TestRoleNamesAreTheRoles(t *testing.T) {
	t.Parallel()

	names := RoleNames()
	if len(names) != len(Roles()) {
		t.Errorf("RoleNames() = %v, want one name per role in %v", names, Roles())
	}
	for _, role := range Roles() {
		if !slices.Contains(names, string(role)) {
			t.Errorf("RoleNames() = %v, missing %q", names, role)
		}
	}
	if !RoleProgramManager.Valid() {
		t.Error("Valid() = false for the program manager, the sixth role")
	}
	if got := RoleProgramManager.Title(); got != "program manager" {
		t.Errorf("Title() = %q, want the name written in full", got)
	}
	if got := RoleProductManager.Title(); got != "Lead Product Manager" {
		t.Errorf("Title() = %q, want the product manager named as the Lead Product Manager", got)
	}
	if got := AgentRole("product-manager"); got != RoleProductManager {
		t.Errorf("the product manager's identifier = %q, want product-manager kept as it was", got)
	}
}

// TestAMemoryNameMayLeadWithAWorkItemNumber is the regression test for the
// development manager's sweep of 2026-09-26, refused whole for a memory named
// after the work item it was about.
func TestAMemoryNameMayLeadWithAWorkItemNumber(t *testing.T) {
	for _, name := range []string{"372-owes-rerun-decision", "429-13", "sweep-cadence", "v2"} {
		if err := ValidateMemoryName(name); err != nil {
			t.Errorf("ValidateMemoryName(%q) = %v, want accepted", name, err)
		}
	}
	for _, name := range []string{"", "Upper", "trailing-", "-leading", "two--hyphens", "has space", "dot.ted"} {
		if err := ValidateMemoryName(name); err == nil {
			t.Errorf("ValidateMemoryName(%q) = nil, want refused", name)
		}
	}
}

// The bug label reads as a bug whatever the type says, the two kinds read as
// themselves, and anything else is untyped rather than counted as either.
func TestWorkItemKindOfReadsTheTypeAndTheBugLabel(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		issueType string
		labels    []string
		want      WorkItemKind
	}{
		{issueType: "bug", want: WorkItemKindBug},
		{issueType: "feature", want: WorkItemKindFeature},
		{issueType: "task", labels: []string{"reliability", "bug"}, want: WorkItemKindBug},
		{issueType: "feature", labels: []string{"bug"}, want: WorkItemKindBug},
		{issueType: "task", labels: []string{"reliability"}, want: ""},
		{issueType: "epic", want: ""},
		{issueType: "", want: ""},
	} {
		if got := WorkItemKindOf(test.issueType, test.labels); got != test.want {
			t.Fatalf("WorkItemKindOf(%q, %q) = %q, want %q", test.issueType, test.labels, got, test.want)
		}
	}
}

// Untyped merges are counted apart from both kinds, and the rate is the share
// of all merges that were bug fixes, with none to report where nothing merged.
func TestReworkTallyReportsUntypedMergesApart(t *testing.T) {
	t.Parallel()

	tally := CountRework([]WorkItemKind{WorkItemKindBug, WorkItemKindFeature, WorkItemKindFeature, ""})
	if tally != (ReworkTally{Merges: 4, Bugs: 1, Features: 2, Untyped: 1}) {
		t.Fatalf("CountRework() = %#v", tally)
	}
	if rate, known := tally.Rate(); !known || rate != 0.25 {
		t.Fatalf("Rate() = %v, %v, want 0.25", rate, known)
	}
	if _, known := CountRework(nil).Rate(); known {
		t.Fatal("Rate() of no merges is known, want it reported as no rate")
	}
}
