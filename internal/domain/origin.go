package domain

import (
	"errors"
	"fmt"
	"strings"
)

// WorkItemAsker is who asked for a piece of work to be admitted to the backlog.
// It is recorded as a field on the item, in the same write as the admission,
// because the tracker's own author field names whoever's git identity the
// harness runs under — the operator, on every item — and an admission's notes
// say where it came from in prose nothing can count. A page that splits the
// backlog by who filed it reads this.
type WorkItemAsker string

const (
	// AskerOperator is work the operator asked for: admitted in a conversation
	// they were speaking in, or from a proposal made there.
	AskerOperator WorkItemAsker = "operator"
	// AskerReport is work a role's report asked for: the admission cites a report
	// in the collected pile, and the role that filed it is recorded beside it.
	AskerReport WorkItemAsker = "report"
	// AskerSweep is work a role's own recurring pass decided on, with nobody
	// speaking in the conversation: the Lead Product Manager's sweep, or a program
	// manager's pass over its lane.
	AskerSweep WorkItemAsker = "sweep"
	// AskerHarness is work the harness filed itself, under a standing order, such
	// as the item a red landing files for its own repair.
	AskerHarness WorkItemAsker = "harness"
)

// WorkItemAskers lists every asker an origin may name, in the order a refusal
// names them.
var WorkItemAskers = []WorkItemAsker{AskerOperator, AskerReport, AskerSweep, AskerHarness}

// Valid reports an asker an origin may be written with. The empty asker is not
// one: it is what an item admitted before origins were recorded reads as, and
// nothing writes it.
func (a WorkItemAsker) Valid() bool {
	for _, known := range WorkItemAskers {
		if a == known {
			return true
		}
	}
	return false
}

// WorkItemOrigin is where an admitted item came from, as fields. The zero value
// is an origin nobody recorded, which is what every item admitted before this
// existed carries; it is said as unknown rather than guessed at.
type WorkItemOrigin struct {
	// Asker is who asked for the work.
	Asker WorkItemAsker
	// AdmittedBy is the role whose action put the item in the backlog. It is
	// empty only for work the harness filed itself.
	AdmittedBy AgentRole
	// Report is the collected report the admission cites, and ReportedBy the role
	// that filed it. Both are set exactly when the asker is a report. ReportedBy is
	// the reporter the report itself records, which is a role or the harness: the
	// harness files a report of its own when a role's pass keeps failing.
	Report     string
	ReportedBy AgentRole
	// Directive is the operator's recorded directive the admission answers, and
	// empty where it answers none. A directive is what makes work done on the
	// operator's behalf whoever asked for it in the moment: a report or a sweep
	// that admits work answering one is acting for the operator.
	Directive string
}

// Trimmed is the origin with the space around each value taken off, which is
// how the tracker reads it back.
func (o WorkItemOrigin) Trimmed() WorkItemOrigin {
	return WorkItemOrigin{
		Asker:      WorkItemAsker(strings.TrimSpace(string(o.Asker))),
		AdmittedBy: AgentRole(strings.TrimSpace(string(o.AdmittedBy))),
		Report:     strings.TrimSpace(o.Report),
		ReportedBy: AgentRole(strings.TrimSpace(string(o.ReportedBy))),
		Directive:  strings.TrimSpace(o.Directive),
	}
}

// Known reports an origin somebody recorded.
func (o WorkItemOrigin) Known() bool {
	return strings.TrimSpace(string(o.Asker)) != ""
}

// AskedBy is who asked, as one value a listing can split by: the operator, the
// harness, or the role — the one that filed the report, or the one whose pass
// it was. It is empty for an origin nobody recorded.
func (o WorkItemOrigin) AskedBy() string {
	switch o.Asker {
	case AskerOperator:
		return string(AskerOperator)
	case AskerHarness:
		return string(AskerHarness)
	case AskerReport:
		return string(o.ReportedBy)
	case AskerSweep:
		return string(o.AdmittedBy)
	default:
		return ""
	}
}

// OnBehalfOf is whom the work was admitted for: the operator, where the
// admission answers one of their directives, and otherwise whoever asked.
func (o WorkItemOrigin) OnBehalfOf() string {
	if !o.Known() {
		return ""
	}
	if strings.TrimSpace(o.Directive) != "" {
		return string(AskerOperator)
	}
	return o.AskedBy()
}

// Validate refuses an origin that could not describe a real admission. It is
// asked where an origin is written, never of one being read: what a tracker
// already holds is reported as it reads.
func (o WorkItemOrigin) Validate() error {
	if !o.Asker.Valid() {
		named := make([]string, 0, len(WorkItemAskers))
		for _, asker := range WorkItemAskers {
			named = append(named, fmt.Sprintf("%q", asker))
		}
		return fmt.Errorf("origin asker %q is not one of %s", o.Asker, strings.Join(named, ", "))
	}
	var problems []error
	if o.AdmittedBy != "" && !o.AdmittedBy.Valid() {
		problems = append(problems, fmt.Errorf("origin admitted by %q, which is not a role", o.AdmittedBy))
	}
	if o.Asker != AskerHarness && o.AdmittedBy == "" {
		problems = append(problems, fmt.Errorf("an origin asked for by %s names the role that admitted it", o.Asker))
	}
	report := strings.TrimSpace(o.Report) != ""
	switch {
	case o.Asker == AskerReport && (!report || strings.TrimSpace(string(o.ReportedBy)) == ""):
		problems = append(problems, errors.New("an origin asked for by a report names the report and the role that filed it"))
	case o.Asker != AskerReport && (report || o.ReportedBy != ""):
		problems = append(problems, fmt.Errorf("an origin asked for by %s names no report", o.Asker))
	}
	return errors.Join(problems...)
}

// Describe says the origin in a sentence a person reads where the item is
// read or listed. An origin nobody recorded says so, because an item admitted
// before origins were recorded is not one anybody can now account for.
func (o WorkItemOrigin) Describe() string {
	var asked string
	switch o.Asker {
	case AskerOperator:
		asked = "asked for by the operator"
	case AskerReport:
		asked = fmt.Sprintf("asked for by report %s, filed by the %s", strings.TrimSpace(o.Report), o.ReportedBy.Title())
	case AskerSweep:
		asked = fmt.Sprintf("decided on in the %s's own pass", o.AdmittedBy.Title())
	case AskerHarness:
		asked = "filed by the harness itself"
	default:
		return "not recorded; the item was admitted before origins were recorded"
	}
	if o.AdmittedBy != "" && o.Asker != AskerSweep {
		asked += ", admitted by the " + o.AdmittedBy.Title()
	}
	if directive := strings.TrimSpace(o.Directive); directive != "" {
		if o.Asker == AskerOperator {
			asked += ", in answer to their directive " + directive
		} else {
			asked += ", on the operator's behalf in answer to directive " + directive
		}
	}
	return asked
}
