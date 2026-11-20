package readmodel

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/intake"
)

type fakeIntakeRecord struct {
	state intake.State
	err   error
}

func (f fakeIntakeRecord) Read() (intake.State, error) { return f.state, f.err }

func TestStatusCountsEachIntakeQuerysSkippedMinutesAndShowsNoMessageContent(t *testing.T) {
	readUpTo := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	state := intake.State{
		Messages: map[string]*intake.Recorded{"slack\x00m1": {Message: intake.Message{Text: "the export button does nothing", Sender: "U-listed", Link: "https://x.test/m1"}}},
		Queries: map[string]*intake.QueryState{
			"bugs": {ReadUpTo: readUpTo, Messages: 4, Ignored: 1, FailedInARow: 2,
				Skipped: []intake.Window{{From: readUpTo, Until: readUpTo.Add(time.Minute)}, {From: readUpTo.Add(time.Hour), Until: readUpTo.Add(time.Hour + time.Minute)}}},
			"ideas": {},
		},
	}
	counted, problem := ReadIntakeQueries(fakeIntakeRecord{state: state})
	if problem != "" || counted == nil || len(counted.Queries) != 2 {
		t.Fatalf("ReadIntakeQueries() = %+v, %q", counted, problem)
	}
	bugs := counted.Queries[0]
	if bugs.Query != "bugs" || bugs.SkippedMinutes != 2 || bugs.Messages != 4 || bugs.Ignored != 1 || bugs.FailedInARow != 2 {
		t.Fatalf("bugs = %+v", bugs)
	}
	rendered := Standing{IntakeQueries: counted}.RenderIntakeQueries()
	for _, want := range []string{"bugs: read up to", "2 minute(s) skipped as too large to read", "1 of them ignored because the sender is not listed", "its last 2 run(s) failed", "ideas: not switched on yet"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("status does not say %q:\n%s", want, rendered)
		}
	}
	for _, content := range []string{"export button", "U-listed", "x.test"} {
		if strings.Contains(rendered, content) {
			t.Errorf("status shows message content %q", content)
		}
	}
	// The counts have no field a message's content could be put in.
	for _, kind := range []reflect.Type{reflect.TypeOf(IntakeQueries{}), reflect.TypeOf(IntakeQueryCounts{})} {
		for i := 0; i < kind.NumField(); i++ {
			if name := kind.Field(i).Name; strings.Contains(name, "Text") || strings.Contains(name, "Sender") || strings.Contains(name, "Link") {
				t.Errorf("%s carries a field %s", kind.Name(), name)
			}
		}
	}
}

func TestStatusSaysNothingAboutIntakeWhereNoQueryHasRun(t *testing.T) {
	if counted, problem := ReadIntakeQueries(fakeIntakeRecord{state: intake.State{}}); counted != nil || problem != "" {
		t.Fatalf("an empty record reads %+v, %q", counted, problem)
	}
	if counted, problem := ReadIntakeQueries(nil); counted != nil || problem != "" {
		t.Fatalf("no record reads %+v, %q", counted, problem)
	}
	if rendered := (Standing{}).RenderIntakeQueries(); rendered != "" {
		t.Fatalf("status printed %q with no intake", rendered)
	}
	if _, problem := ReadIntakeQueries(fakeIntakeRecord{err: errors.New("disk gone")}); !strings.Contains(problem, "disk gone") {
		t.Fatalf("a record that cannot be read says %q", problem)
	}
}
