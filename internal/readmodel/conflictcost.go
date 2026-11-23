package readmodel

// What merge conflicts cost in a week, and the files they were in.
//
// The cost is read from the cost log alone: a line whose invocation was made
// for a hand-back for a conflict names that hand-back
// (runstate.SpendConflictHandback). It covers the attempts to bring a change up
// to date and the reviews after them. It does not cover a management role's
// turn deciding what to do about one, which names no hand-back.
//
// Two kinds of spend cannot be told apart and are stated on their own, never
// added to a conflict's cost or taken for something else's: an invocation made
// after a conflict that something else also came before, and a run's invocation
// recorded before any of this was. A conflict's invocation the provider never
// priced is counted and not estimated. Any of the three makes the cost a floor,
// and where no cost is known at all beside one of them, the cost is not known.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxConflictFiles is how many of a week's files are named.
const maxConflictFiles = 10

// ConflictItem is a work item whose change was handed back for a conflict.
type ConflictItem struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

func (i ConflictItem) says() string {
	if i.Title == "" {
		return i.ID
	}
	return i.Title + " (" + i.ID + ")"
}

// ConflictFile is one file and the hand-backs that named it.
type ConflictFile struct {
	Path      string         `json:"path"`
	Handbacks int            `json:"handbacks"`
	Items     []ConflictItem `json:"items"`
}

// ConflictFiles is a week's hand-backs for conflicts and the files named in
// the most of them.
type ConflictFiles struct {
	Handbacks int `json:"handbacks"`
	// Lines is every conflicting line that was counted, and LinesUnknown how
	// many hand-backs' lines were not.
	Lines        int            `json:"lines"`
	LinesUnknown int            `json:"lines_unknown,omitempty"`
	Files        []ConflictFile `json:"files"`
	// Omitted is how many more files were named than are listed, and Unnamed
	// how many conflicting files the records dropped without a name.
	Omitted int    `json:"omitted,omitempty"`
	Unnamed int    `json:"unnamed,omitempty"`
	Says    string `json:"says"`
}

// ConflictCost is what conflicts cost in one week.
type ConflictCost struct {
	// Known says CostUSD is a figure at all. Where it is, Floor says the cost
	// is at least that much.
	Known    bool    `json:"known"`
	Floor    bool    `json:"floor"`
	CostUSD  float64 `json:"cost_usd"`
	Calls    int     `json:"calls"`
	Unpriced int     `json:"unpriced,omitempty"`
	// PerLandedChange is CostUSD divided by the week's landed code changes,
	// absent where either is not known or nothing landed.
	PerLandedChange *float64 `json:"per_landed_change,omitempty"`
	// Unclear is the spend after a conflict that cannot be told apart, and
	// NotRecorded the runs' spend from before conflicts were recorded. Each is
	// the priced part; its calls count the unpriced ones too.
	UnclearUSD       float64 `json:"unclear_usd"`
	UnclearCalls     int     `json:"unclear_calls"`
	NotRecordedUSD   float64 `json:"not_recorded_usd"`
	NotRecordedCalls int     `json:"not_recorded_calls"`
	Says             string  `json:"says"`
	// Files is nil where the run records could not be read.
	Files *ConflictFiles `json:"files,omitempty"`
}

// add counts one line of the cost log.
func (c *ConflictCost) add(line runstate.Spend) {
	amount := 0.0
	if line.Known() {
		amount = line.AmountUSD
	}
	switch line.Conflict {
	case runstate.SpendConflictHandback:
		c.Calls++
		c.CostUSD += amount
		if !line.Known() {
			c.Unpriced++
		}
	case runstate.SpendConflictUnclear:
		c.UnclearCalls++
		c.UnclearUSD += amount
	case runstate.SpendConflictNotRecorded:
		if strings.TrimSpace(line.RunID) != "" && (line.Role == domain.RoleDeveloper || line.Role == domain.RoleReviewer) {
			c.NotRecordedCalls++
			c.NotRecordedUSD += amount
		}
	}
}

// settle works out what the lines added come to.
func (c *ConflictCost) settle(landed *int) {
	c.Floor = c.Unpriced > 0 || c.UnclearCalls > 0 || c.NotRecordedCalls > 0
	c.Known = !c.Floor || c.Calls > c.Unpriced
	if c.Known {
		c.PerLandedChange = per(c.CostUSD, landed)
	}
	c.Says = c.says(landed)
}

func (c ConflictCost) says(landed *int) string {
	var parts []string
	switch {
	case !c.Known:
		parts = append(parts, "what conflicts cost is not known")
	case c.Calls == 0:
		parts = append(parts, "conflicts cost nothing")
	default:
		atLeast := ""
		if c.Floor {
			atLeast = "at least "
		}
		spent := fmt.Sprintf("conflicts cost %s$%.2f across %s", atLeast, c.CostUSD, count(c.Calls, "provider call"))
		switch {
		case landed == nil:
			spent += ", code changes landed not known"
		case *landed == 0:
			spent += ", no code change landed"
		default:
			spent += fmt.Sprintf(", %s$%.2f per landed code change", atLeast, *c.PerLandedChange)
		}
		parts = append(parts, spent)
	}
	if c.Unpriced > 0 {
		parts = append(parts, fmt.Sprintf("%s for conflicts had no cost reported", count(c.Unpriced, "call")))
	}
	if c.UnclearCalls > 0 {
		parts = append(parts, fmt.Sprintf("%s after a conflict ($%.2f known) cannot be told apart from other work", count(c.UnclearCalls, "call"), c.UnclearUSD))
	}
	if c.NotRecordedCalls > 0 {
		parts = append(parts, fmt.Sprintf("%s by runs ($%.2f known) did not record whether a conflict caused them", count(c.NotRecordedCalls, "call"), c.NotRecordedUSD))
	}
	return strings.Join(parts, "; ")
}

// Lines is the week's conflicts as a surface prints them: what they cost, and
// the files they were in where any change was handed back.
func (c ConflictCost) Lines() []string {
	switch {
	case c.Files == nil:
		return []string{c.Says, "files in conflict: not known"}
	case c.Files.Handbacks == 0:
		return []string{c.Says}
	}
	return []string{c.Says, "files in conflict: " + c.Files.Says}
}

// conflictFiles is the hand-backs made within a span and the files named in
// the most of them.
func conflictFiles(runs []runstate.State, titles map[string]string, within func(time.Time) bool) ConflictFiles {
	found := ConflictFiles{Files: []ConflictFile{}}
	files := map[string]*ConflictFile{}
	for _, run := range runs {
		item := ConflictItem{ID: run.WorkItemID, Title: strings.TrimSpace(run.WorkItemTitle)}
		if title := strings.TrimSpace(titles[run.WorkItemID]); title != "" {
			item.Title = title
		}
		for _, handback := range run.ConflictHandbacks {
			if !within(handback.At) {
				continue
			}
			found.Handbacks++
			found.Unnamed += handback.Omitted
			if handback.Lines == nil {
				found.LinesUnknown++
			} else {
				found.Lines += *handback.Lines
			}
			for _, path := range handback.Paths {
				file := files[path]
				if file == nil {
					file = &ConflictFile{Path: path}
					files[path] = file
				}
				file.Handbacks++
				known := false
				for _, named := range file.Items {
					known = known || named.ID == item.ID
				}
				if !known {
					file.Items = append(file.Items, item)
				}
			}
		}
	}
	for _, file := range files {
		sort.Slice(file.Items, func(a, b int) bool { return file.Items[a].ID < file.Items[b].ID })
		found.Files = append(found.Files, *file)
	}
	sort.Slice(found.Files, func(a, b int) bool {
		if found.Files[a].Handbacks != found.Files[b].Handbacks {
			return found.Files[a].Handbacks > found.Files[b].Handbacks
		}
		return found.Files[a].Path < found.Files[b].Path
	})
	if extra := len(found.Files) - maxConflictFiles; extra > 0 {
		found.Omitted = extra
		found.Files = found.Files[:maxConflictFiles]
	}
	found.Says = found.says()
	return found
}

func (f ConflictFiles) says() string {
	if f.Handbacks == 0 {
		return "no change was handed back for a conflict"
	}
	said := fmt.Sprintf("changes handed back for a conflict %s, ", count(f.Handbacks, "time"))
	switch {
	case f.LinesUnknown == f.Handbacks:
		said += "conflicting lines not known"
	case f.LinesUnknown > 0:
		said += fmt.Sprintf("at least %s (not known for %d of them)", count(f.Lines, "conflicting line"), f.LinesUnknown)
	default:
		said += count(f.Lines, "conflicting line")
	}
	if len(f.Files) == 0 {
		return said + "; no file was named"
	}
	named := make([]string, 0, len(f.Files))
	for _, file := range f.Files {
		items := make([]string, 0, len(file.Items))
		for _, item := range file.Items {
			items = append(items, item.says())
		}
		named = append(named, fmt.Sprintf("%s in %d, from %s", file.Path, file.Handbacks, strings.Join(items, ", ")))
	}
	said += "; files named most: " + strings.Join(named, "; ")
	if f.Omitted > 0 {
		said += fmt.Sprintf("; and %s", count(f.Omitted, "more file"))
	}
	if f.Unnamed > 0 {
		said += fmt.Sprintf("; %s not named in the records", count(f.Unnamed, "conflicting file"))
	}
	return said
}

// ConflictHandbackRuns lists the runs handed back for a conflict since a
// moment, through the run index rather than from every record.
type ConflictHandbackRuns interface {
	ConflictHandbacksSince(since time.Time) ([]runstate.State, error)
}

// ConflictFilesSection is what a development manager's pass is told of the
// files changes conflicted in this week and last, and empty where nothing was
// handed back for a conflict in either.
func ConflictFilesSection(runs ConflictHandbackRuns, now time.Time) string {
	zone := DeliveryZone()
	local := now.In(zone)
	this := weekStart(time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone))
	last := this.AddDate(0, 0, -7)
	const heading = "# The files changes conflicted in\n\n"
	found, err := runs.ConflictHandbacksSince(last)
	if err != nil {
		return heading + fmt.Sprintf("The runs handed back for a conflict could not be read: %v", err)
	}
	current := conflictFiles(found, nil, func(at time.Time) bool { return !at.Before(this) })
	before := conflictFiles(found, nil, func(at time.Time) bool { return !at.Before(last) && at.Before(this) })
	if current.Handbacks+before.Handbacks == 0 {
		return ""
	}
	return heading + fmt.Sprintf("Weeks run Monday to Sunday in %s. Link open items that will touch the same files, or propose splitting a file that keeps conflicting.\n\n- This week, from %s: %s.\n- Last week, from %s: %s.",
		zone, this.Format(time.DateOnly), current.Says, last.Format(time.DateOnly), before.Says)
}
