package readmodel

// The operator's interventions, counted over the last day and week by week.
//
// The factory-flow program manager measures its lane in how often the operator
// has to step in, and the measure needs a count somebody can read. Three things
// are counted, each from a record that already exists:
//
//   - an ask: a report handling that names the operator, which is the harness
//     putting something in front of him (report.Handling's NeedsOperator,
//     OperatorReason, or Hand);
//   - a message: the operator, or somebody typing for him, sending a role a
//     message, recorded in the intervention log as intervention.KindMessage;
//   - a hand step: every other entry in the intervention log, whether the
//     harness carried it out on his word or somebody recorded it afterwards.
//
// Each says whether it was truly one only a person could take, where its record
// says: an ask is when it names a reason from the operator's closed list and is
// not the harness's missing capability; a hand step is when whoever recorded it
// said so. A message says nothing either way. The count is a floor for the
// reason the count per merged change is (InterventionsFloor).
//
// Weeks are seven days back from the moment of the reading rather than calendar
// weeks, so the latest week is always the last seven days and nothing is left
// out at a boundary; the delivery measures (delivery.go) are the calendar view.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// InterventionLog is the recorded hand steps and messages. It is satisfied by
// *runstate.InterventionStore.
type InterventionLog interface {
	List() ([]intervention.Event, error)
}

// OwnerInterventionClass is which of the three things an intervention was.
type OwnerInterventionClass string

const (
	OwnerInterventionAsk      OwnerInterventionClass = "ask"
	OwnerInterventionMessage  OwnerInterventionClass = "message"
	OwnerInterventionHandStep OwnerInterventionClass = "hand-step"
)

const (
	// ownerInterventionWeeks is how many weeks a reading counts, the last seven
	// days included.
	ownerInterventionWeeks = 4
	// maxOwnerInterventionEntries bounds the entries a reading carries.
	maxOwnerInterventionEntries = 50
	// maxOwnerInterventionAbout bounds what one entry says it was about.
	maxOwnerInterventionAbout = 200
)

// OwnerIntervention is one intervention: when, which class, what it was about,
// the record it was read from, and whether only a person could have taken it.
type OwnerIntervention struct {
	Class  OwnerInterventionClass `json:"class"`
	At     time.Time              `json:"at"`
	About  string                 `json:"about"`
	Record string                 `json:"record"`
	// PersonOnly is nil where the record does not say.
	PersonOnly *bool `json:"person_only,omitempty"`
}

// OwnerInterventionTally is the interventions in one stretch of time, from
// Since up to Until.
type OwnerInterventionTally struct {
	Since     time.Time `json:"since"`
	Until     time.Time `json:"until"`
	Asks      int       `json:"asks"`
	Messages  int       `json:"messages"`
	HandSteps int       `json:"hand_steps"`
	// PersonOnly, NotPersonOnly, and NotSaid split the same interventions by
	// whether only a person could have taken them.
	PersonOnly    int `json:"person_only"`
	NotPersonOnly int `json:"not_person_only"`
	NotSaid       int `json:"not_said"`
	// Label names the stretch and Says is its count in words, both as `yoyo
	// status` prints them and the dashboard shows them.
	Label string `json:"label"`
	Says  string `json:"says"`
}

// Total is every intervention in the stretch.
func (t OwnerInterventionTally) Total() int { return t.Asks + t.Messages + t.HandSteps }

// OwnerInterventions is the reading: the last 24 hours, the last four weeks
// newest first (Weeks[0] is the last seven days), and the last seven days'
// entries newest first.
type OwnerInterventions struct {
	Day     OwnerInterventionTally   `json:"day"`
	Weeks   []OwnerInterventionTally `json:"weeks"`
	Entries []OwnerIntervention      `json:"entries"`
	// Any is whether anything in the weeks the reading covers is recorded,
	// which is what decides whether a surface prints the count at all.
	Any bool `json:"recorded"`
	// Omitted is how many of the last seven days' entries are not listed.
	Omitted int    `json:"omitted,omitempty"`
	Floor   string `json:"floor"`
	Problem string `json:"problem,omitempty"`
}

// Week is the last seven days.
func (o OwnerInterventions) Week() OwnerInterventionTally {
	if len(o.Weeks) == 0 {
		return OwnerInterventionTally{}
	}
	return o.Weeks[0]
}

// Recorded reports any intervention in the weeks the reading covers.
func (o OwnerInterventions) Recorded() bool { return o.Any }

// ReadOwnerInterventions reads the handlings and the intervention log and
// counts them as of now.
func ReadOwnerInterventions(sources Sources, now time.Time) OwnerInterventions {
	var entries []OwnerIntervention
	var problems []string
	if sources.Reports == nil {
		problems = append(problems, "nothing was wired to read the report handlings, so no ask is counted")
	} else if handlings, err := sources.Reports.Handlings(); err != nil {
		problems = append(problems, fmt.Sprintf("the report handlings could not be read, so no ask is counted: %v", err))
	} else {
		entries = append(entries, askInterventions(handlings)...)
	}
	if sources.Interventions == nil {
		problems = append(problems, "nothing was wired to read the record of hand steps, so no message or hand step is counted")
	} else if events, err := sources.Interventions.List(); err != nil {
		problems = append(problems, fmt.Sprintf("the record of hand steps could not be read, so no message or hand step is counted: %v", err))
	} else {
		entries = append(entries, eventInterventions(events)...)
	}
	reading := CountOwnerInterventions(entries, now)
	reading.Problem = strings.Join(problems, "; ")
	return reading
}

// CountOwnerInterventions counts entries into the last day and the last four
// weeks as of now.
func CountOwnerInterventions(entries []OwnerIntervention, now time.Time) OwnerInterventions {
	now = now.UTC()
	reading := OwnerInterventions{
		Day:     OwnerInterventionTally{Since: now.Add(-24 * time.Hour), Until: now},
		Entries: []OwnerIntervention{},
		Floor:   InterventionsFloor,
	}
	for week := 0; week < ownerInterventionWeeks; week++ {
		until := now.Add(-time.Duration(week) * 7 * 24 * time.Hour)
		reading.Weeks = append(reading.Weeks, OwnerInterventionTally{Since: until.Add(-7 * 24 * time.Hour), Until: until})
	}
	sorted := append([]OwnerIntervention(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].At.Equal(sorted[j].At) {
			return sorted[i].Record > sorted[j].Record
		}
		return sorted[i].At.After(sorted[j].At)
	})
	for _, entry := range sorted {
		if inStretch(entry.At, reading.Day) {
			reading.Day.add(entry)
		}
		for index := range reading.Weeks {
			if inStretch(entry.At, reading.Weeks[index]) {
				reading.Weeks[index].add(entry)
			}
		}
		if inStretch(entry.At, reading.Weeks[0]) {
			if len(reading.Entries) < maxOwnerInterventionEntries {
				reading.Entries = append(reading.Entries, entry)
			} else {
				reading.Omitted++
			}
		}
	}
	reading.Day.Label = "last 24 hours"
	reading.Day.Says = reading.Day.says()
	for index := range reading.Weeks {
		week := &reading.Weeks[index]
		week.Label = "last 7 days"
		if index > 0 {
			week.Label = "week from " + week.Since.In(time.Local).Format("2006-01-02 15:04 MST")
		}
		week.Says = week.says()
		reading.Any = reading.Any || week.Total() > 0
	}
	return reading
}

func inStretch(at time.Time, tally OwnerInterventionTally) bool {
	return !at.Before(tally.Since) && at.Before(tally.Until)
}

func (t *OwnerInterventionTally) add(entry OwnerIntervention) {
	switch entry.Class {
	case OwnerInterventionAsk:
		t.Asks++
	case OwnerInterventionMessage:
		t.Messages++
	default:
		t.HandSteps++
	}
	switch {
	case entry.PersonOnly == nil:
		t.NotSaid++
	case *entry.PersonOnly:
		t.PersonOnly++
	default:
		t.NotPersonOnly++
	}
}

// askInterventions is each handling that names the operator. Only a reason from
// his closed list, with no capability the harness lacks, makes one his alone.
func askInterventions(handlings []report.Handling) []OwnerIntervention {
	var entries []OwnerIntervention
	for _, handling := range handlings {
		if !handling.NeedsOperator && handling.OperatorReason == "" && handling.Hand == "" {
			continue
		}
		personOnly := handling.OperatorReason != "" && handling.Hand == ""
		entries = append(entries, OwnerIntervention{
			Class:      OwnerInterventionAsk,
			At:         handling.RecordedAt,
			About:      oneline.Fold("report "+handling.ReportID+": "+handling.Reason, maxOwnerInterventionAbout),
			Record:     handling.ReportID,
			PersonOnly: &personOnly,
		})
	}
	return entries
}

// eventInterventions is each recorded message and hand step.
func eventInterventions(events []intervention.Event) []OwnerIntervention {
	entries := make([]OwnerIntervention, 0, len(events))
	for _, event := range events {
		class := OwnerInterventionHandStep
		if event.Kind == intervention.KindMessage {
			class = OwnerInterventionMessage
		}
		entries = append(entries, OwnerIntervention{
			Class:      class,
			At:         event.At,
			About:      oneline.Fold(event.Said, maxOwnerInterventionAbout),
			Record:     event.ID,
			PersonOnly: event.PersonOnly,
		})
	}
	return entries
}

// RenderOwnerInterventions is the count under the four lines: the last day and
// each of the last four weeks, one line each. It is empty where nothing in
// those weeks is recorded and every record could be read, and says what could
// not be read otherwise.
func (s Standing) RenderOwnerInterventions() string {
	reading := s.OwnerInterventions
	if !reading.Recorded() && reading.Problem == "" {
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString("The operator's interventions (asks put to him, his messages to the roles, and hand steps; a floor):\n")
	rendered.WriteString("  " + reading.Day.Label + ": " + reading.Day.Says + "\n")
	for _, week := range reading.Weeks {
		rendered.WriteString("  " + week.Label + ": " + week.Says + "\n")
	}
	if reading.Problem != "" {
		rendered.WriteString(partialRead + reading.Problem + "\n")
	}
	return rendered.String()
}

// says is one stretch in words.
func (t OwnerInterventionTally) says() string {
	if t.Total() == 0 {
		return "none"
	}
	return fmt.Sprintf("%d — %s, %s, %s; %d only a person could take, %d the harness should have taken, %d not said",
		t.Total(), countOf(t.Asks, "ask"), countOf(t.Messages, "message"), countOf(t.HandSteps, "hand step"),
		t.PersonOnly, t.NotPersonOnly, t.NotSaid)
}

func countOf(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}
