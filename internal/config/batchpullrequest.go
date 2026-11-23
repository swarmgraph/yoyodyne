package config

// The settings of the harness's own batch pull request: the pull request the
// harness opens into the team's branch, grows, tracks and merges for the work
// landed on the target branch (docs/designs/integrating-to-a-local-branch.md,
// section 5). Batching rules are a project's settings and the mechanics are the
// harness's, so what is here is the settings and the bounds they are held to.
// Every bound is held in this file, and a value outside one refuses the
// configuration when it loads, in the words the problem is written in below.
//
// The route runs only with execution.upstream_branch set and enabled true
// (BatchRouteOn); with either off nothing of it runs.

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // the review window names a zone, so it must load on a machine with no zone database
)

const (
	// DefaultBatchMinItems is how many unpublished items open or grow a batch.
	DefaultBatchMinItems = 5
	// MinBatchMinItems and MaxBatchMinItems bound min_items.
	MinBatchMinItems = 1
	MaxBatchMinItems = 100
	// DefaultBatchMaxWait is how long the oldest unpublished item waits before a
	// batch opens or grows with fewer than min_items.
	DefaultBatchMaxWait = Duration(24 * time.Hour)
	// MinBatchMaxWait and MaxBatchMaxWait bound max_wait.
	MinBatchMaxWait = Duration(15 * time.Minute)
	MaxBatchMaxWait = Duration(14 * 24 * time.Hour)
	// MinBatchReviewWindowPerWeek is the least time a week a review window may
	// leave open, because a batch that can open for less is one nobody will see.
	MinBatchReviewWindowPerWeek = time.Hour

	// BatchMergeMethodMerge and BatchMergeMethodSquash are how the forge merges
	// the batch.
	BatchMergeMethodMerge  = "merge"
	BatchMergeMethodSquash = "squash"

	// BatchRequestFormatJSON and BatchRequestFormatText are the two forms a
	// review request is sent in.
	BatchRequestFormatJSON = "json"
	BatchRequestFormatText = "text"
)

// BatchPullRequest is execution.batch_pull_request.
type BatchPullRequest struct {
	// Enabled turns the route on, together with execution.upstream_branch.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// MinItems is how many unpublished items open or grow a batch, 1 to 100.
	MinItems int `yaml:"min_items" json:"min_items"`
	// MaxWait is how long the oldest unpublished item waits before a batch opens
	// or grows with fewer than MinItems, 15 minutes to 14 days.
	MaxWait Duration `yaml:"max_wait" json:"max_wait"`
	// ReviewWindow is when a batch may open or grow and a review request may go
	// out. Nil means any time.
	ReviewWindow *ReviewWindow `yaml:"review_window,omitempty" json:"review_window,omitempty"`
	// AutomatedReviewer is the forge account of a review tool whose review the
	// harness waits for. Empty means none.
	AutomatedReviewer string `yaml:"automated_reviewer,omitempty" json:"automated_reviewer,omitempty"`
	// ReviewRequest is what is told that a batch is ready for a person. Nil means
	// no request is sent.
	ReviewRequest *ReviewRequest `yaml:"review_request,omitempty" json:"review_request,omitempty"`
	// MergeMethod is how the forge merges the batch, merge or squash.
	MergeMethod string `yaml:"merge_method" json:"merge_method"`
}

// ReviewWindow is the days and hours, in a named time zone, when the batch route
// may act. The same hours apply on every day listed, and a window does not cross
// midnight: it is From to To on a day, never From on one day to To on the next.
type ReviewWindow struct {
	// TimeZone is an IANA zone name such as America/Los_Angeles.
	TimeZone string `yaml:"time_zone" json:"time_zone"`
	// Days are the weekdays the window is open on, written mon to sun.
	Days []string `yaml:"days" json:"days"`
	// From and To are the hours on each of those days, as 24-hour HH:MM, with
	// From before To.
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
}

// ReviewRequest is how a person is told that a batch is ready: either a command
// the harness runs or an https address it posts to, exactly one of the two.
type ReviewRequest struct {
	// Command is the program and its arguments, run without a shell.
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
	// URL is an https address the request is posted to.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
	// Format is json or text. Empty means json.
	Format string `yaml:"format,omitempty" json:"format,omitempty"`
}

// DefaultBatchPullRequest is the setting a project that says nothing has.
func DefaultBatchPullRequest() BatchPullRequest {
	return BatchPullRequest{
		MinItems:    DefaultBatchMinItems,
		MaxWait:     DefaultBatchMaxWait,
		MergeMethod: BatchMergeMethodMerge,
	}
}

// IsZero reports whether the setting holds only what an absent one means: off,
// and nothing but defaults. Such a block is left out of the form the
// configuration revision digests, so a project that says nothing about batching
// keeps the revision it had.
func (b BatchPullRequest) IsZero() bool {
	return !b.Enabled && b.ReviewWindow == nil && b.AutomatedReviewer == "" && b.ReviewRequest == nil &&
		(b.MinItems == 0 || b.MinItems == DefaultBatchMinItems) &&
		(b.MaxWait == 0 || b.MaxWait == DefaultBatchMaxWait) &&
		(b.MergeMethod == "" || b.MergeMethod == BatchMergeMethodMerge)
}

// BatchRouteOn reports whether the harness's own batch pull request runs: the
// team's branch is set and the route is enabled. With either off, nothing of the
// route runs.
func (e Execution) BatchRouteOn() bool {
	return e.BatchPullRequest.Enabled && strings.TrimSpace(e.UpstreamBranch) != ""
}

// MergeMethodInForce is the configured merge method, or merge where none is.
func (b BatchPullRequest) MergeMethodInForce() string {
	if b.MergeMethod == "" {
		return BatchMergeMethodMerge
	}
	return b.MergeMethod
}

// FormatInForce is the request's format, or json where none is named.
func (r ReviewRequest) FormatInForce() string {
	if r.Format == "" {
		return BatchRequestFormatJSON
	}
	return r.Format
}

var (
	batchReviewerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})?(?:\[bot\])?$`)
	batchClockPattern    = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)
	batchWeekdays        = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	batchWeekdayOf       = map[string]time.Weekday{
		"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
		"fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
	}
)

// batchClock is a 24-hour HH:MM as minutes into the day.
func batchClock(text string) (int, bool) {
	match := batchClockPattern.FindStringSubmatch(text)
	if match == nil {
		return 0, false
	}
	var hours, minutes int
	_, _ = fmt.Sscanf(match[1]+" "+match[2], "%d %d", &hours, &minutes)
	return hours*60 + minutes, true
}

// window reads the review window, and says in plain words what is wrong with it.
func (w ReviewWindow) window() (location *time.Location, days map[time.Weekday]bool, from, to int, problems []string) {
	const key = "execution.batch_pull_request.review_window"
	location, err := time.LoadLocation(strings.TrimSpace(w.TimeZone))
	if strings.TrimSpace(w.TimeZone) == "" || err != nil {
		problems = append(problems, fmt.Sprintf("%s.time_zone %q is not a time zone name such as America/Los_Angeles", key, w.TimeZone))
	}
	days = map[time.Weekday]bool{}
	if len(w.Days) == 0 {
		problems = append(problems, fmt.Sprintf("%s.days must name at least one day, written %s", key, strings.Join(batchWeekdays, ", ")))
	}
	for _, day := range w.Days {
		weekday, known := batchWeekdayOf[strings.ToLower(strings.TrimSpace(day))]
		if !known {
			problems = append(problems, fmt.Sprintf("%s.days has %q, which is not a day written %s", key, day, strings.Join(batchWeekdays, ", ")))
			continue
		}
		days[weekday] = true
	}
	var fromOK, toOK bool
	if from, fromOK = batchClock(w.From); !fromOK {
		problems = append(problems, fmt.Sprintf("%s.from %q is not a 24-hour time such as 09:00", key, w.From))
	}
	if to, toOK = batchClock(w.To); !toOK {
		problems = append(problems, fmt.Sprintf("%s.to %q is not a 24-hour time such as 17:00", key, w.To))
	}
	if fromOK && toOK {
		if to <= from {
			problems = append(problems, fmt.Sprintf("%s.to %s must be later than from %s on the same day; a window does not run past midnight", key, w.To, w.From))
		} else if open := time.Duration(len(days)*(to-from)) * time.Minute; len(days) > 0 && open < MinBatchReviewWindowPerWeek {
			problems = append(problems, fmt.Sprintf("%s is open %s a week, and it must be open at least %s a week", key, open, MinBatchReviewWindowPerWeek))
		}
	}
	return location, days, from, to, problems
}

// Open reports whether the window is open at the given moment. A nil window is
// open at every moment, which is what an unset review window means. A window
// that cannot be read is closed: the configuration that carries one is refused
// when it loads, so this only meets one in a test.
func (w *ReviewWindow) Open(at time.Time) bool {
	if w == nil {
		return true
	}
	location, days, from, to, problems := w.window()
	if len(problems) > 0 {
		return false
	}
	local := at.In(location)
	minutes := local.Hour()*60 + local.Minute()
	return days[local.Weekday()] && minutes >= from && minutes < to
}

// NextOpen is the first moment at or after the given one when the window is
// open, and false where a window can never open. A nil window is open now.
func (w *ReviewWindow) NextOpen(at time.Time) (time.Time, bool) {
	if w == nil {
		return at, true
	}
	if w.Open(at) {
		return at, true
	}
	location, days, from, _, problems := w.window()
	if len(problems) > 0 {
		return time.Time{}, false
	}
	local := at.In(location)
	for offset := 0; offset <= 7; offset++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+offset, 0, 0, 0, 0, location)
		start := time.Date(day.Year(), day.Month(), day.Day(), from/60, from%60, 0, 0, location)
		if days[day.Weekday()] && start.After(at) {
			return start, true
		}
	}
	return time.Time{}, false
}

// batchPullRequestProblems is what is wrong with the batch settings, in plain
// words, one problem each.
func batchPullRequestProblems(execution Execution) []string {
	const key = "execution.batch_pull_request"
	batch := execution.BatchPullRequest
	var problems []string
	// A zero is a setting never stated, as in a configuration built in code; one a
	// file states as zero is refused where it is read (batchPullRequestDocument).
	if batch.MinItems != 0 && (batch.MinItems < MinBatchMinItems || batch.MinItems > MaxBatchMinItems) {
		problems = append(problems, fmt.Sprintf("%s.min_items is %d, and it must be from %d to %d", key, batch.MinItems, MinBatchMinItems, MaxBatchMinItems))
	}
	if batch.MaxWait != 0 && (batch.MaxWait < MinBatchMaxWait || batch.MaxWait > MaxBatchMaxWait) {
		problems = append(problems, fmt.Sprintf("%s.max_wait is %s, and it must be from %s to %s", key, batch.MaxWait, time.Duration(MinBatchMaxWait), time.Duration(MaxBatchMaxWait)))
	}
	if batch.ReviewWindow != nil {
		_, _, _, _, windowProblems := batch.ReviewWindow.window()
		problems = append(problems, windowProblems...)
	}
	if account := batch.AutomatedReviewer; account != "" && !batchReviewerPattern.MatchString(account) {
		problems = append(problems, fmt.Sprintf("%s.automated_reviewer %q is not one forge account name; it takes one account, or is left out for none", key, account))
	}
	if request := batch.ReviewRequest; request != nil {
		problems = append(problems, reviewRequestProblems(key+".review_request", *request)...)
	}
	switch batch.MergeMethodInForce() {
	case BatchMergeMethodMerge, BatchMergeMethodSquash:
	default:
		problems = append(problems, fmt.Sprintf("%s.merge_method %q must be %s or %s", key, batch.MergeMethod, BatchMergeMethodMerge, BatchMergeMethodSquash))
	}
	return problems
}

func reviewRequestProblems(key string, request ReviewRequest) []string {
	var problems []string
	hasCommand, hasURL := len(request.Command) > 0, strings.TrimSpace(request.URL) != ""
	switch {
	case hasCommand && hasURL:
		problems = append(problems, fmt.Sprintf("%s names both a command and an address; it takes one of the two", key))
	case !hasCommand && !hasURL:
		problems = append(problems, fmt.Sprintf("%s names neither a command nor an https address; name one, or leave review_request out to send none", key))
	}
	if hasCommand {
		if strings.TrimSpace(request.Command[0]) == "" {
			problems = append(problems, fmt.Sprintf("%s.command must start with the program to run", key))
		}
		for _, argument := range request.Command {
			if strings.ContainsRune(argument, 0) {
				problems = append(problems, fmt.Sprintf("%s.command has an argument with a null character in it", key))
				break
			}
		}
	}
	if hasURL {
		parsed, err := url.Parse(strings.TrimSpace(request.URL))
		switch {
		case err != nil || parsed.Scheme != "https" || parsed.Hostname() == "":
			problems = append(problems, fmt.Sprintf("%s.url %q must be an https address with a host", key, request.URL))
		case parsed.User != nil:
			problems = append(problems, fmt.Sprintf("%s.url must not carry a user name or password in the address", key))
		}
	}
	switch request.FormatInForce() {
	case BatchRequestFormatJSON, BatchRequestFormatText:
	default:
		problems = append(problems, fmt.Sprintf("%s.format %q must be %s or %s", key, request.Format, BatchRequestFormatJSON, BatchRequestFormatText))
	}
	return problems
}

// batchPullRequestDocument is the section as a file writes it. Each key inherits
// on its own, except that a review window, a review request and an account
// replace an inherited one wholesale: which hours, which command and which
// reviewer is one statement, and half of each from a bundle is not one anybody
// wrote down.
type batchPullRequestDocument struct {
	Enabled           *bool          `yaml:"enabled"`
	MinItems          *int           `yaml:"min_items"`
	MaxWait           *Duration      `yaml:"max_wait"`
	ReviewWindow      *ReviewWindow  `yaml:"review_window"`
	AutomatedReviewer *string        `yaml:"automated_reviewer"`
	ReviewRequest     *ReviewRequest `yaml:"review_request"`
	MergeMethod       *string        `yaml:"merge_method"`
}

func (d batchPullRequestDocument) applyTo(r *resolution, target *BatchPullRequest, origin string) error {
	const key = "execution.batch_pull_request."
	// Zero reads as unset everywhere else, so a file that says zero is refused
	// here, where it can still be told from one that said nothing.
	if d.MinItems != nil && *d.MinItems == 0 {
		return fmt.Errorf("%smin_items is 0, and it must be from %d to %d", key, MinBatchMinItems, MaxBatchMinItems)
	}
	if d.MaxWait != nil && *d.MaxWait == 0 {
		return fmt.Errorf("%smax_wait is 0s, and it must be from %s to %s", key, time.Duration(MinBatchMaxWait), time.Duration(MaxBatchMaxWait))
	}
	setValue(r.origins, key+"enabled", d.Enabled, &target.Enabled, origin)
	setValue(r.origins, key+"min_items", d.MinItems, &target.MinItems, origin)
	setValue(r.origins, key+"max_wait", d.MaxWait, &target.MaxWait, origin)
	setValue(r.origins, key+"automated_reviewer", d.AutomatedReviewer, &target.AutomatedReviewer, origin)
	setValue(r.origins, key+"merge_method", d.MergeMethod, &target.MergeMethod, origin)
	if d.ReviewWindow != nil {
		window := *d.ReviewWindow
		window.Days = append([]string(nil), window.Days...)
		target.ReviewWindow = &window
		r.origins[key+"review_window"] = origin
	}
	if d.ReviewRequest != nil {
		request := *d.ReviewRequest
		request.Command = append([]string(nil), request.Command...)
		target.ReviewRequest = &request
		r.origins[key+"review_request"] = origin
	}
	return nil
}
