package protectedpath

import (
	"slices"
	"testing"
)

const intakeBase = `version: 1
checks: [make test]
intake:
  queries:
    slack-bugs:
      tool: slack.search
      senders: [U123]
      questions: lead-product-manager
`

// A change elsewhere in the file, or one that only reformats or reorders the
// section, leaves the section as configuration unchanged.
func TestIntakeChangesIgnoresEverythingOutsideTheSection(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]string{
		"another setting": `version: 1
checks: [make test, make vet]
intake:
  queries:
    slack-bugs:
      tool: slack.search
      senders: [U123]
      questions: lead-product-manager
`,
		"reordered and reformatted": `intake: {queries: {slack-bugs: {questions: lead-product-manager, senders: ["U123"], tool: slack.search}}}
checks:
  - make test
version: 1
`,
	} {
		changed, err := IntakeChanges([]byte(intakeBase), []byte(change))
		if err != nil || len(changed) != 0 {
			t.Fatalf("%s: IntakeChanges() = %v, %v, want no change", name, changed, err)
		}
	}
}

// Each change to the section is named down to the query it touched.
func TestIntakeChangesNamesTheQueryThatMoved(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		change string
		want   []string
	}{
		"repointed questions": {`intake:
  queries:
    slack-bugs:
      tool: slack.search
      senders: [U123]
      questions: person
`, []string{"intake.queries.slack-bugs"}},
		"added query": {intakeBase + `    more:
      tool: slack.search
`, []string{"intake.queries.more"}},
		"section removed":            {"version: 1\n", []string{"intake.queries.slack-bugs"}},
		"setting beside the queries": {intakeBase + "  extra: true\n", []string{"intake.extra"}},
		"section made a scalar":      {"intake: off\n", []string{"intake"}},
		"changed through an alias": {`shared: &who [U999]
intake:
  queries:
    slack-bugs:
      tool: slack.search
      senders: *who
      questions: lead-product-manager
`, []string{"intake.queries.slack-bugs"}},
	} {
		changed, err := IntakeChanges([]byte(intakeBase), []byte(test.change))
		if err != nil {
			t.Fatalf("%s: IntakeChanges() error = %v", name, err)
		}
		if !slices.Equal(changed, test.want) {
			t.Fatalf("%s: IntakeChanges() = %v, want %v", name, changed, test.want)
		}
	}
}

// A file that is not there holds no section, and an empty section is the same
// configuration as none; adding one where there was no file is a change.
func TestIntakeChangesReadsAMissingFileAsNoSection(t *testing.T) {
	t.Parallel()

	if changed, err := IntakeChanges(nil, []byte("checks: []\nintake: {}\n")); err != nil || len(changed) != 0 {
		t.Fatalf("IntakeChanges(no file, empty section) = %v, %v, want no change", changed, err)
	}
	if changed, err := IntakeChanges(nil, []byte(intakeBase)); err != nil || !slices.Equal(changed, []string{"intake.queries.slack-bugs"}) {
		t.Fatalf("IntakeChanges(no file, a query) = %v, %v, want the query named", changed, err)
	}
	if changed, err := IntakeChanges([]byte(intakeBase), nil); err != nil || !slices.Equal(changed, []string{"intake.queries.slack-bugs"}) {
		t.Fatalf("IntakeChanges(a query, file removed) = %v, %v, want the query named", changed, err)
	}
}

// A copy that cannot be decoded cannot be shown to leave the section alone.
func TestIntakeChangesRefusesACopyItCannotDecode(t *testing.T) {
	t.Parallel()

	if _, err := IntakeChanges([]byte(intakeBase), []byte("intake: [unclosed\n")); err == nil {
		t.Fatal("IntakeChanges(undecodable change) = nil error, want it refused")
	}
}
