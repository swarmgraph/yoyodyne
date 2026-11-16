package report

// A program manager's digest: the one report a pass files to the Lead Product
// Manager, in a fixed shape.
//
// "What leaves the lane" in docs/designs/program-manager.md is the rule. An
// instance files nothing per finding. Everything it has to say outside its lane
// in one pass goes into one report, filed through the ordinary report block, so
// it lands in the pile the Lead Product Manager already walks and is decided
// with the "handle" action she already has. What makes it a digest rather than
// a paragraph is the shape: the lane and the pass, what the pass admitted, each
// request outside the lane with the work, the goal, the priority, and why, each
// objection to an admission with the item and the concern, and the instance's
// open requests restated by id. A slice of them then reads alike.
//
// The shape is checked where every report is checked, so a digest that does
// not hold to it is refused with the reason, as any unreadable report is. The
// lane and the pass are the harness's to stamp, never the instance's to assert,
// for the reason the rest of a report's attribution is.

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// MaxDigestEntries bounds each list a digest carries, and
// maxDigestEntriesText is the same bound as the contract states it. A pass with
// more to say than this is a pass that should have said less of it.
const (
	MaxDigestEntries     = 20
	maxDigestEntriesText = "20"
)

// MaxDigestFieldBytes bounds one field of one entry, and maxDigestIDBytes one
// identifier in a list of them.
const (
	MaxDigestFieldBytes = 1 << 10
	maxDigestIDBytes    = 128
)

// MaxDigestMessageBytes bounds the digest's text as it is filed. A digest is a
// pass's whole account of what leaves its lane, so it is allowed more room than
// a report's two sentences; the block it came from is still held to
// MaxBlockBytes, so it is never larger than a reply could carry.
const MaxDigestMessageBytes = 12 << 10

// MaxDigestPriority is the lowest priority a request may recommend. The order
// is written as Beads priority, 0 first.
const MaxDigestPriority = 4

// Digest is the fixed shape of a program manager's digest.
type Digest struct {
	// Lane and Pass are the instance's lane and the pass that filed the digest.
	// Both are stamped by the harness; a digest that names either as written is
	// refused.
	Lane string `json:"lane,omitempty"`
	Pass string `json:"pass,omitempty"`
	// Admissions are the items the pass admitted in the lane, by identifier.
	Admissions []string `json:"admissions,omitempty"`
	// Requests are the work the instance recommends outside its lane, one entry
	// each.
	Requests []DigestRequest `json:"requests,omitempty"`
	// Objections are the instance's objections to new admissions, one entry per
	// item.
	Objections []DigestObjection `json:"objections,omitempty"`
	// OpenRequests are the instance's requests from earlier passes that are still
	// open, restated by identifier rather than repeated.
	OpenRequests []string `json:"open_requests,omitempty"`
	// StopCauses is one line per cause of stopped runs the pass counted: the
	// cause, its count, and the report that already names it. It is where the
	// pass carries what its stop-cause block's result handed it for the digest.
	StopCauses []string `json:"stop_causes,omitempty"`
}

// DigestRequest is one piece of work outside the lane the instance recommends:
// what it is, the goal it would serve, where it would go in the order, and why.
type DigestRequest struct {
	Work     string `json:"work"`
	Goal     string `json:"goal"`
	Priority *int   `json:"priority"`
	Why      string `json:"why"`
}

// DigestObjection is one objection to an admission: the item, and the concern.
type DigestObjection struct {
	Item    string `json:"item"`
	Concern string `json:"concern"`
}

// Empty reports a digest with nothing in it. A pass with nothing to say files
// no digest, so an empty one is refused rather than filed.
func (d Digest) Empty() bool {
	return len(d.Admissions) == 0 && len(d.Requests) == 0 && len(d.Objections) == 0 && len(d.OpenRequests) == 0 && len(d.StopCauses) == 0
}

// validate reports every problem with the digest's content at once. It does not
// ask about the lane and the pass, which are whoever stamped them's to supply.
func (d Digest) validate(severity Severity) error {
	var problems []error
	if d.Empty() {
		problems = append(problems, errors.New("the digest has no admissions, requests, objections, or open requests; a pass with nothing to say files no digest"))
	}
	switch severity {
	case SeverityNote, SeverityWarning:
	default:
		problems = append(problems, fmt.Errorf("a digest is filed at %q, or at %q where it carries an objection or a blocker of the instance's own, and never at %q", SeverityNote, SeverityWarning, severity))
	}
	if len(d.Objections) > 0 && severity == SeverityNote {
		problems = append(problems, fmt.Errorf("a digest carrying an objection is filed at %q", SeverityWarning))
	}
	problems = append(problems, identifiers("admissions", d.Admissions))
	problems = append(problems, identifiers("open_requests", d.OpenRequests))
	if len(d.StopCauses) > MaxDigestEntries {
		problems = append(problems, fmt.Errorf("stop_causes: %d entries, limit is %d", len(d.StopCauses), MaxDigestEntries))
	}
	for index, line := range d.StopCauses {
		problems = append(problems, field(fmt.Sprintf("stop_causes[%d]", index), line))
	}
	if len(d.Requests) > MaxDigestEntries {
		problems = append(problems, fmt.Errorf("requests: %d entries, limit is %d", len(d.Requests), MaxDigestEntries))
	}
	for index, request := range d.Requests {
		problems = append(problems, request.validate(index))
	}
	if len(d.Objections) > MaxDigestEntries {
		problems = append(problems, fmt.Errorf("objections: %d entries, limit is %d", len(d.Objections), MaxDigestEntries))
	}
	for index, objection := range d.Objections {
		problems = append(problems, objection.validate(index))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid digest: %w", err)
	}
	return nil
}

// validateWritten is validate for a digest as an agent wrote it, which may not
// assert the lane or the pass.
func (d Digest) validateWritten(severity Severity) error {
	var problems []error
	if strings.TrimSpace(d.Lane) != "" || strings.TrimSpace(d.Pass) != "" {
		problems = append(problems, errors.New("invalid digest: the lane and the pass are stamped by the harness, and a digest may not name either"))
	}
	problems = append(problems, d.validate(severity))
	return errors.Join(problems...)
}

// validateStamped is validate for a digest as it is filed, which carries the
// lane and the pass the harness stamped.
func (d Digest) validateStamped(severity Severity) error {
	var problems []error
	if err := domain.ValidateIdentifier("digest lane", strings.TrimSpace(d.Lane)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(d.Pass) == "" {
		problems = append(problems, errors.New("a digest names the pass that filed it"))
	}
	problems = append(problems, d.validate(severity))
	return errors.Join(problems...)
}

func (r DigestRequest) validate(index int) error {
	var problems []error
	problems = append(problems, field(fmt.Sprintf("requests[%d].work", index), r.Work))
	problems = append(problems, field(fmt.Sprintf("requests[%d].goal", index), r.Goal))
	problems = append(problems, field(fmt.Sprintf("requests[%d].why", index), r.Why))
	switch {
	case r.Priority == nil:
		problems = append(problems, fmt.Errorf("requests[%d].priority is required: the recommended priority, 0 first and %d last", index, MaxDigestPriority))
	case *r.Priority < 0 || *r.Priority > MaxDigestPriority:
		problems = append(problems, fmt.Errorf("requests[%d].priority %d is not a priority: 0 first and %d last", index, *r.Priority, MaxDigestPriority))
	}
	return errors.Join(problems...)
}

func (o DigestObjection) validate(index int) error {
	return errors.Join(
		identifier(fmt.Sprintf("objections[%d].item", index), o.Item),
		field(fmt.Sprintf("objections[%d].concern", index), o.Concern),
	)
}

// field is one required piece of prose, held to MaxDigestFieldBytes.
func field(name, value string) error {
	switch trimmed := strings.TrimSpace(value); {
	case trimmed == "":
		return fmt.Errorf("%s is required", name)
	case len(trimmed) > MaxDigestFieldBytes:
		return fmt.Errorf("%s is %d bytes, limit is %d", name, len(trimmed), MaxDigestFieldBytes)
	}
	return nil
}

// identifiers holds a list of identifiers to the digest's bounds.
func identifiers(name string, values []string) error {
	if len(values) > MaxDigestEntries {
		return fmt.Errorf("%s: %d entries, limit is %d", name, len(values), MaxDigestEntries)
	}
	var problems []error
	for index, value := range values {
		problems = append(problems, identifier(fmt.Sprintf("%s[%d]", name, index), value))
	}
	return errors.Join(problems...)
}

// identifier is one identifier: a work item, a report, an exchange, an
// amendment, a restart request. Which of those it is the reader resolves; what
// is held here is that it is one word a reader can look up.
func identifier(name, value string) error {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		return fmt.Errorf("%s is required", name)
	case len(trimmed) > maxDigestIDBytes:
		return fmt.Errorf("%s is %d bytes, and an identifier is at most %d", name, len(trimmed), maxDigestIDBytes)
	case strings.IndexFunc(trimmed, unicode.IsSpace) >= 0:
		return fmt.Errorf("%s %q is not an identifier: name the item, report, or request by its id alone", name, trimmed)
	}
	return nil
}

// Render is the digest as the pile shows it, under any sentence the instance
// led it with. It is the report's text, so every surface that reads the pile
// reads a digest without knowing it is one.
func (d Digest) Render(lead string) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "Digest of the lane %q, from pass %s.\n", strings.TrimSpace(d.Lane), strings.TrimSpace(d.Pass))
	if lead = strings.Join(strings.Fields(lead), " "); lead != "" {
		rendered.WriteString(lead + "\n")
	}
	if len(d.Admissions) == 0 {
		rendered.WriteString("Admitted in the lane this pass: nothing.\n")
	} else {
		fmt.Fprintf(&rendered, "Admitted in the lane this pass: %s.\n", joinTrimmed(d.Admissions))
	}
	if len(d.Requests) > 0 {
		rendered.WriteString("Requests outside the lane:\n")
		for _, request := range d.Requests {
			priority := 0
			if request.Priority != nil {
				priority = *request.Priority
			}
			fmt.Fprintf(&rendered, "- %s — would serve: %s; recommended priority %d; why: %s\n",
				oneLine(request.Work), oneLine(request.Goal), priority, oneLine(request.Why))
		}
	}
	if len(d.Objections) > 0 {
		rendered.WriteString("Objections to new admissions:\n")
		for _, objection := range d.Objections {
			fmt.Fprintf(&rendered, "- %s: %s\n", strings.TrimSpace(objection.Item), oneLine(objection.Concern))
		}
	}
	if len(d.StopCauses) > 0 {
		rendered.WriteString("Repeated stop causes:\n")
		for _, line := range d.StopCauses {
			fmt.Fprintf(&rendered, "- %s\n", oneLine(line))
		}
	}
	if len(d.OpenRequests) > 0 {
		fmt.Fprintf(&rendered, "Open requests from earlier passes: %s.\n", joinTrimmed(d.OpenRequests))
	}
	return strings.TrimSpace(rendered.String())
}

func joinTrimmed(values []string) string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		trimmed = append(trimmed, strings.TrimSpace(value))
	}
	return strings.Join(trimmed, ", ")
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// DigestContract is what a role that files a digest is told about its shape.
// It is here beside the check so the two cannot come to disagree.
const DigestContract = `# Your digest to the Lead Product Manager

Everything a pass has to say outside your lane goes to the Lead Product Manager as one digest, filed as one entry of your report block, and nothing is filed per finding. It lands in the pile she walks, and she decides it with her "handle" action. The entry takes a "digest" in place of a message, or beside one sentence that leads it:

` + "```" + `yoyodyne-report
{"reports":[{"severity":"note","digest":{"admissions":["the ids of the items this pass admitted in your lane"],"requests":[{"work":"the work you recommend outside your lane","goal":"the goal it would serve, in the words the goals state it","priority":2,"why":"why"}],"objections":[{"item":"the id of a new admission","concern":"what is wrong with it"}],"open_requests":["the ids of your requests from earlier passes that are still open"],"stop_causes":["one line per cause your stop-cause block counted: the cause, its count, and the report it names"]}}]}
` + "```" + `

Leave out any list you have nothing for, and file no digest at all on a pass with nothing to say. File it at "note", or at "warning" where it carries an objection or a blocker of your own; a digest carrying an objection at "note" is refused, and a digest is never "critical" — something already costing somebody is its own report. Priority is 0 first and 4 last. Each list holds at most ` + maxDigestEntriesText + ` entries, and every id is the id alone. The harness stamps your lane and the pass, so name neither. One digest per pass: a reply carrying two, or a second in a pass that already filed one, is refused, and so is a digest that does not hold to this shape. A refused digest is not filed, the reason is written on the pass's record, and you are told why on your next turn.

The digest is read by people who do not know your lane's items by number, so say in the request and the concern what the work is, not only its id.`

// DigestFiler is the one role whose reports may be digests.
const DigestFiler = domain.RoleProgramManager

// SeparateDigests takes the digests out of one block's entries and judges them,
// leaving every other entry as it was. It returns the entries to file — the
// ordinary reports, and the one digest where it holds to its shape — and why any
// digest was refused. A refused digest is dropped on its own, so a critical
// report filed beside a malformed digest, or beside two of them, is still filed.
//
// One digest per pass, and a reply is at most one pass's: a reply carrying two
// files neither, since which of them the pass meant is not the harness's to
// guess.
func SeparateDigests(entries []Entry) ([]Entry, string) {
	kept := make([]Entry, 0, len(entries))
	var digests []Entry
	for _, entry := range entries {
		if entry.Digest == nil {
			kept = append(kept, entry)
			continue
		}
		digests = append(digests, entry)
	}
	switch {
	case len(digests) == 0:
		return kept, ""
	case len(digests) > 1:
		return kept, fmt.Sprintf("%d digests in one reply, and a pass files one digest, so neither was filed", len(digests))
	}
	if err := digests[0].Digest.validateWritten(digests[0].Severity); err != nil {
		return kept, err.Error()
	}
	return append(kept, digests[0]), ""
}
