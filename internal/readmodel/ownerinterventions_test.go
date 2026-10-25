package readmodel

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

type fakeInterventionLog struct {
	events []intervention.Event
	err    error
}

func (f fakeInterventionLog) List() ([]intervention.Event, error) { return f.events, f.err }

// The three things an intervention is — an ask put to the operator, a message
// he sent a role, and a hand step — are each counted into the stretch they fell
// in, and split by whether only a person could have taken them: an ask naming
// a reason from his closed list is his alone, one the harness's missing
// capability raised is not, and a step says what its recorder said.
func TestTheOperatorsInterventionsAreCountedByDayAndWeek(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC)
	yes, no := true, false
	restart := countedStep(1, intervention.KindRestart, nil, "", true)
	restart.At = now.Add(-2 * time.Hour)
	restart.PersonOnly = &no
	message := countedStep(2, intervention.KindMessage, nil, "", false)
	message.At = now.Add(-3 * 24 * time.Hour)
	license := countedStep(3, intervention.KindOther, nil, "", true)
	license.At = now.Add(-9 * 24 * time.Hour)
	license.PersonOnly = &yes
	ancient := countedStep(4, intervention.KindRun, nil, "", false)
	ancient.At = now.Add(-60 * 24 * time.Hour)
	handlings := []report.Handling{
		{ReportID: "report-a", Reason: "the token needs a new scope", NeedsOperator: true, OperatorReason: ownership.OperatorReason("credential"), RecordedAt: now.Add(-time.Hour)},
		{ReportID: "report-b", Reason: "a configuration value only a person can write", Hand: ownership.HandNeed("operational-configuration"), RecordedAt: now.Add(-4 * 24 * time.Hour)},
		{ReportID: "report-c", Reason: "covered by work already admitted", RecordedAt: now.Add(-time.Hour)},
	}
	reading := ReadOwnerInterventions(Sources{
		Reports:       fakeReports{handlings: handlings},
		Interventions: fakeInterventionLog{events: []intervention.Event{restart, message, license, ancient}},
	}, now)
	if reading.Problem != "" {
		t.Fatalf("problem = %q", reading.Problem)
	}
	day := reading.Day
	if day.Asks != 1 || day.HandSteps != 1 || day.Messages != 0 || day.PersonOnly != 1 || day.NotPersonOnly != 1 {
		t.Errorf("the last day = %+v, want the credential ask and the restart", day)
	}
	week := reading.Week()
	if week.Asks != 2 || week.Messages != 1 || week.HandSteps != 1 || week.PersonOnly != 1 || week.NotPersonOnly != 2 || week.NotSaid != 1 {
		t.Errorf("the last seven days = %+v, want two asks, a message, and the restart", week)
	}
	if len(reading.Weeks) != 4 || reading.Weeks[1].HandSteps != 1 || reading.Weeks[1].PersonOnly != 1 {
		t.Errorf("the weeks = %+v, want the licence acceptance in the week before", reading.Weeks)
	}
	for _, earlier := range reading.Weeks[2:] {
		if earlier.Total() != 0 {
			t.Errorf("an earlier week counted %+v, want nothing: the oldest step is outside every week", earlier)
		}
	}
	if len(reading.Entries) != 4 || reading.Entries[0].Record != "report-a" {
		t.Errorf("the week's entries = %+v, want four, newest first", reading.Entries)
	}

	rendered := Standing{OwnerInterventions: reading}.RenderOwnerInterventions()
	for _, want := range []string{
		"The operator's interventions (asks put to him, his messages to the roles, and hand steps; a floor):\n",
		"  last 24 hours: 2 — 1 ask, 0 messages, 1 hand step; 1 only a person could take, 1 the harness should have taken, 0 not said\n",
		"  last 7 days: 4 — 2 asks, 1 message, 1 hand step; 1 only a person could take, 2 the harness should have taken, 1 not said\n",
		"  week from ",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered\n%s\nwant it to contain %q", rendered, want)
		}
	}
}

// Nothing is printed under the four lines where nothing is recorded, and a
// record that could not be read is said rather than read as none.
func TestTheInterventionCountIsPrintedOnlyWhereAnyIsRecorded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC)
	quiet := ReadOwnerInterventions(Sources{Reports: fakeReports{}, Interventions: fakeInterventionLog{}}, now)
	if rendered := (Standing{OwnerInterventions: quiet}).RenderOwnerInterventions(); rendered != "" {
		t.Errorf("a product with nothing recorded printed %q", rendered)
	}
	broken := ReadOwnerInterventions(Sources{Reports: fakeReports{}, Interventions: fakeInterventionLog{err: errors.New("disk on fire")}}, now)
	rendered := Standing{OwnerInterventions: broken}.RenderOwnerInterventions()
	if !strings.Contains(rendered, "not fully read: the record of hand steps could not be read, so no message or hand step is counted: disk on fire") {
		t.Errorf("an unreadable record printed %q, want it said", rendered)
	}
}

// A message is not a hand step on any change, so the count per merged change
// leaves it out.
func TestAMessageIsNotCountedAgainstAChange(t *testing.T) {
	t.Parallel()
	count := CountInterventions([]intervention.Event{countedStep(1, intervention.KindMessage, nil, "", false)}, nil)
	if count.Attributed != 0 || count.Unattributed != 0 {
		t.Errorf("count = %+v, want the message left out", count)
	}
}
