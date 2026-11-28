package report

// A report that says again what a standing report already says.
//
// Runs in a project nobody triages file the same fact over and over, and each
// copy is one more line in a pile that already looks too long to read. So a
// report a run files that repeats a standing unhandled one is kept as a repeat
// of it: a record of its own, beside the reports and the handlings, naming the
// report it repeats. The standing report is never edited; its count is the
// number of repeat records naming it. What makes two reports the same fact is
// decided in Match, and the design that settled it is
// docs/designs/reports-nobody-reads.md, Decision 2.

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
)

// RepeatSchemaVersion is versioned independently of the report it repeats, as
// a handling's is.
const RepeatSchemaVersion = 1

// Repeat is one report a run filed that said again what a standing report
// says. It carries everything the report would have carried, so nothing the
// run said is lost by being counted rather than listed.
type Repeat struct {
	SchemaVersion int `json:"schema_version"`
	// ReportID is the standing report this repeats.
	ReportID     string           `json:"report_id"`
	Role         domain.AgentRole `json:"role"`
	Agent        string           `json:"agent,omitempty"`
	RunID        string           `json:"run_id"`
	WorkItemID   string           `json:"work_item_id,omitempty"`
	Build        string           `json:"build,omitempty"`
	ProductID    domain.ProductID `json:"product_id"`
	RepositoryID string           `json:"repository_id"`
	Severity     Severity         `json:"severity"`
	// Message is the new report's message, whole.
	Message string `json:"message"`
	// Named says the run named the standing report as the one it repeats,
	// rather than the two messages being equal in plain form.
	Named      bool      `json:"named,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Validate reports every contract violation in the repeat at once.
func (r Repeat) Validate() error {
	var problems []error
	if r.SchemaVersion != RepeatSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", RepeatSchemaVersion))
	}
	if !ValidID(r.ReportID) {
		problems = append(problems, errors.New("report id is invalid"))
	}
	if err := domain.ValidateIdentifier("role", string(r.Role)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(r.RunID) == "" {
		problems = append(problems, errors.New("run id is required"))
	}
	if r.Build != "" && !buildPattern.MatchString(r.Build) {
		problems = append(problems, fmt.Errorf("build %q is not a revision", r.Build))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("repository id", r.RepositoryID); err != nil {
		problems = append(problems, err)
	}
	// The severity and the message are held to exactly what a report's are.
	problems = append(problems, Entry{Severity: r.Severity, Message: r.Message}.Validate())
	if r.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid report repeat: %w", err)
	}
	return nil
}

// Counted is a repeat a run filed and the standing report's count of repeats
// once it was counted, for the run's closing lines.
type Counted struct {
	Repeat Repeat `json:"repeat"`
	Count  int    `json:"count"`
}

// Says is the closing line for a counted repeat: which report it repeated and
// how many times that report has now been repeated.
func (c Counted) Says() string {
	times := "once"
	if c.Count > 1 {
		times = fmt.Sprintf("%d times", c.Count)
	}
	return fmt.Sprintf("%srepeated report %s rather than filing a new one; it has now been repeated %s", c.Repeat.Severity.Prefix(), c.Repeat.ReportID, times)
}

// RepeatOf is the repeat record a report would be kept as: the same
// attribution, severity, message and time, naming the standing report.
func RepeatOf(reported Report, standing string, named bool) Repeat {
	return Repeat{
		SchemaVersion: RepeatSchemaVersion,
		ReportID:      standing,
		Role:          reported.Role,
		Agent:         reported.Agent,
		RunID:         reported.RunID,
		WorkItemID:    reported.WorkItemID,
		Build:         reported.Build,
		ProductID:     reported.ProductID,
		RepositoryID:  reported.RepositoryID,
		Severity:      reported.Severity,
		Message:       reported.Message,
		Named:         named,
		RecordedAt:    reported.RecordedAt,
	}
}

// RepeatsOf indexes repeat records by the report each repeats, oldest first.
func RepeatsOf(repeats []Repeat) map[string][]Repeat {
	indexed := make(map[string][]Repeat)
	for _, repeat := range repeats {
		indexed[repeat.ReportID] = append(indexed[repeat.ReportID], repeat)
	}
	for id := range indexed {
		sort.SliceStable(indexed[id], func(i, j int) bool { return indexed[id][i].RecordedAt.Before(indexed[id][j].RecordedAt) })
	}
	return indexed
}

var (
	plainIdentifier = regexp.MustCompile(`\b(?:report|run|mqe|exchange)-[a-z0-9]*[0-9][a-z0-9]*`)
	plainHex        = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	plainDate       = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[t ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:z|[+-]\d{2}:?\d{2})?)?\b|\b\d{1,2}/\d{1,2}/\d{2,4}\b`)
	plainTime       = regexp.MustCompile(`\b\d{1,2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:\s?[ap]m)?(?:\s(?:[a-z]{1,2}[sd]t|utc|gmt))?\b`)
	plainNumber     = regexp.MustCompile(`\d+(?:[.,]\d+)*`)
)

// plainMarker is what each identifier, hash, date, time and number becomes. It
// holds no digit, so the passes after the one that wrote it leave it alone.
const plainMarker = "#"

// PlainForm is a message with what differs between two runs saying the same
// thing taken out: lower case, every run of whitespace one space, and each
// harness identifier, hexadecimal string of 7 to 40 characters, date, time and
// other number one marker. It changes nothing else, so two messages equal in
// plain form differ only in those.
func PlainForm(message string) string {
	plain := strings.ToLower(message)
	plain = plainIdentifier.ReplaceAllString(plain, plainMarker)
	plain = plainDate.ReplaceAllString(plain, plainMarker)
	plain = plainTime.ReplaceAllString(plain, plainMarker)
	plain = plainHex.ReplaceAllString(plain, plainMarker)
	plain = plainNumber.ReplaceAllString(plain, plainMarker)
	return strings.Join(strings.Fields(plain), " ")
}

// Matching is what a filing is matched against: the reports in the project,
// what became of them, the repeats already recorded, and the reports the run
// was handed at its start, which are the only ones it may name.
type Matching struct {
	Reports   []Report
	Handlings []Handling
	Offered   []string
}

// Match decides whether a report a run is filing repeats a standing one. It
// returns the standing report's identifier where it does, whether the run named
// it, and, where the run named a report that could not be used, why not — the
// report is then filed as a new one, and the reason is for the run's closing
// lines.
//
// The four conditions are Decision 2's: the standing report is in the same
// project and unhandled; both name the same addressee or both none; the new
// severity is the same or lower; and the run named it, or the two messages are
// equal in plain form. The work item and the filing role are not compared. A
// digest is never a repeat and never repeated, because it is a pass's account
// rather than a fact noticed.
func Match(filing Report, named string, in Matching) (standing string, byName bool, refused string) {
	if filing.Digest != nil {
		return "", false, ""
	}
	open := Unhandled(in.Reports, in.Handlings)
	fits := func(candidate Report) string {
		switch {
		case candidate.Digest != nil:
			return "it is a program manager's digest, which is never repeated"
		case candidate.ProductID != filing.ProductID:
			return "it is in another project"
		case candidate.For != filing.For:
			return fmt.Sprintf("it is for %s and this one is for %s", addresseeWords(candidate.For), addresseeWords(filing.For))
		case filing.Severity.rank() < candidate.Severity.rank():
			return fmt.Sprintf("this one is %s and that one only %s, and a report more severe than the one it repeats is filed on its own", filing.Severity, candidate.Severity)
		}
		return ""
	}
	if named = strings.TrimSpace(named); named != "" {
		offered := false
		for _, id := range in.Offered {
			offered = offered || id == named
		}
		if !offered {
			return "", false, fmt.Sprintf("it names %s as the report it repeats, which was not on the list of standing reports the run was handed", singleWord(named))
		}
		for _, candidate := range open {
			if candidate.ID != named {
				continue
			}
			if why := fits(candidate); why != "" {
				return "", false, fmt.Sprintf("it names %s as the report it repeats, but %s", named, why)
			}
			return named, true, ""
		}
		return "", false, fmt.Sprintf("it names %s as the report it repeats, which somebody has handled since", named)
	}
	plain := PlainForm(filing.Message)
	for _, candidate := range open {
		if fits(candidate) == "" && PlainForm(candidate.Message) == plain {
			return candidate.ID, false, ""
		}
	}
	return "", false, ""
}

// singleWord bounds a name the run wrote, which nothing has checked, to what a
// closing line can carry.
func singleWord(text string) string {
	return fmt.Sprintf("%q", oneline.Fold(text, 80))
}

func addresseeWords(role domain.AgentRole) string {
	if role == "" {
		return "no role named"
	}
	return "the " + role.Title()
}

// RepeatLine is the line a listing puts under a standing report its runs have
// repeated, indented as a handling is, in the reader's local time. It is empty
// for a report nothing has repeated.
func RepeatLine(repeats []Repeat, location *time.Location) string {
	if len(repeats) == 0 {
		return ""
	}
	latest := repeats[0]
	for _, repeat := range repeats[1:] {
		if !repeat.RecordedAt.Before(latest.RecordedAt) {
			latest = repeat
		}
	}
	if location == nil {
		location = time.Local
	}
	times := "once"
	if len(repeats) > 1 {
		times = fmt.Sprintf("%d times", len(repeats))
	}
	on := ""
	if latest.WorkItemID != "" {
		on = " on " + latest.WorkItemID
	}
	return fmt.Sprintf("      repeated %s, the latest by run %s%s at %s\n", times, latest.RunID, on, latest.RecordedAt.In(location).Format("15:04 MST on Jan 2"))
}

// The list of standing reports a developer or reviewer run is handed at its
// start: at most MaxOfferedReports, each message cut to OfferedMessageBytes,
// and the whole held to MaxOfferedListBytes.
const (
	MaxOfferedReports   = 10
	OfferedMessageBytes = 160
	MaxOfferedListBytes = 2 << 10
)

// Offered is the list handed to a run at its start and the reports on it.
type Offered struct {
	Text string
	IDs  []string
}

// OfferStanding lists the unhandled reports in a project for a run to name the
// one a report of its own repeats: the most repeated first and then the newest,
// each as its identifier, addressee, severity and the start of its message, and
// saying how many more stand. It is empty where nothing stands.
func OfferStanding(reports []Report, handlings []Handling, repeats []Repeat) Offered {
	var open []Report
	for _, reported := range Unhandled(reports, handlings) {
		if reported.Digest == nil {
			open = append(open, reported)
		}
	}
	if len(open) == 0 {
		return Offered{}
	}
	counts := make(map[string]int)
	for _, repeat := range repeats {
		counts[repeat.ReportID]++
	}
	sort.SliceStable(open, func(i, j int) bool {
		if counts[open[i].ID] != counts[open[j].ID] {
			return counts[open[i].ID] > counts[open[j].ID]
		}
		return open[i].RecordedAt.After(open[j].RecordedAt)
	})
	var text strings.Builder
	text.WriteString(offeredHeading)
	var ids []string
	for _, reported := range open {
		if len(ids) == MaxOfferedReports {
			break
		}
		line := fmt.Sprintf("- %s, for %s, %s: %s\n", reported.ID, addresseeWords(reported.For), reported.Severity, oneline.Fold(reported.Message, OfferedMessageBytes))
		if text.Len()+len(line)+len(moreStand(len(open))) > MaxOfferedListBytes {
			break
		}
		text.WriteString(line)
		ids = append(ids, reported.ID)
	}
	if more := len(open) - len(ids); more > 0 {
		text.WriteString(moreStand(more))
	}
	return Offered{Text: text.String(), IDs: ids}
}

const offeredHeading = "# Reports already standing in this project\n\n" +
	"These reports were filed in this project and nobody has handled them yet. If a report you would file says the same thing as one of them, add \"repeats\" with its identifier to your report's entry, such as {\"severity\":\"warning\",\"message\":\"...\",\"repeats\":\"report-...\"}. It is then counted on the standing report instead of listed again. It is counted only where your report names the same role, or both name none, and is no more severe; otherwise it is filed as a new report, and nothing is lost either way.\n\n"

func moreStand(more int) string {
	if more == 1 {
		return "1 more report stands and is not listed.\n"
	}
	return fmt.Sprintf("%d more reports stand and are not listed.\n", more)
}
