package readmodel

// The weekly cost figures: what each week cost in all, per work item closed,
// and per code change landed, by role, beside the weeks before it.
//
// They are the measure of whether a change to how the harness spends worked:
// a fix that makes runs cheaper shows as a lower cost per landed change, and
// one that only moves spend from one role to another shows in the split. They
// attribute nothing. A week's cost is every call in the spend log recorded in
// that week, a record naming no work item included, and each per-item figure
// is that cost divided by a count:
//
//   - items closed: every item the tracker records closed in the week, by its
//     closed time, whatever it was closed with;
//   - code changes landed: every run that ended in the week having promoted its
//     change to the target branch, exactly as the throughput counts a landing,
//     less the runs that published a confirmed document. A document published
//     is counted beside them and never as one, so publishing documents cannot
//     stand in for delivering software.
//
// Each role's figures are the same division over that role's share of the
// week: developer runs, reviewers, and each management role by name. The
// management roles' share of the week's known spend is stated on its own,
// because one of the goals bounds it.
//
// Every week runs from Monday to Sunday in Pacific time, as the accomplishment
// and the delivery measures count weeks. A call whose cost the provider never
// reported is counted as unpriced rather than as free, and makes the week's
// cost a floor. A count whose source could not be read is not a zero: the
// reading says why under not_read and gives no figure divided by it.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// weeklyCostWeeks is how many weeks a reading covers, the current one
// included.
const weeklyCostWeeks = 4

// WeeklyCostSources are the records one reading is assembled from. Each is
// optional and carries the reason its caller could not open it.
type WeeklyCostSources struct {
	Spend        AccomplishmentSpend
	SpendProblem string
	// Runs is every run record, as its caller has already read them: the
	// reading does not read every record itself, because a routine reader that
	// does so is one more over the whole run history, and its callers each read
	// them already for an answer of their own (`yoyo status`'s history, and what
	// the spend accomplished). RunsRead says they were read, and RunsProblem,
	// in a sentence naming the run records, why they could not be where they
	// were not.
	Runs        []runstate.State
	RunsRead    bool
	RunsProblem string
	// Tracker is listed once, closed items included, out of the shared reading
	// where TrackerReadings is set (listEveryItem).
	Tracker         DeliveryTracker
	TrackerProblem  string
	TrackerTimeout  time.Duration
	TrackerReadings TrackerReadings
	// Zone is the zone weeks are counted in. Nil is DeliveryZoneName.
	Zone *time.Location
	// Now stamps the reading and picks the current week. It defaults to the
	// wall clock.
	Now func() time.Time
}

// RoleWeekCost is one role's part of a week.
type RoleWeekCost struct {
	Role domain.AgentRole `json:"role"`
	// Says is the role as a sentence names it: "developer runs", "reviewers",
	// or the management role's title.
	Says       string  `json:"says"`
	Management bool    `json:"management"`
	CostUSD    float64 `json:"cost_usd"`
	Calls      int     `json:"calls"`
	Unpriced   int     `json:"unpriced,omitempty"`
	// Share is the role's part of the week's known spend, from 0 to 1.
	Share float64 `json:"share"`
	// PerClosedItem and PerLandedChange are the role's cost divided by the
	// week's count, absent where the count is zero or could not be read.
	PerClosedItem   *float64 `json:"per_closed_item,omitempty"`
	PerLandedChange *float64 `json:"per_landed_change,omitempty"`
}

// CostWeek is one Monday-to-Sunday week.
type CostWeek struct {
	// Week is its Monday; From and To its first and last days. Current says it
	// is the week holding the moment the reading was taken.
	Week    string `json:"week"`
	From    string `json:"from"`
	To      string `json:"to"`
	Current bool   `json:"current"`
	// CostUSD is every known cost recorded in the week, Calls how many calls
	// there were, and Unpriced how many of them the provider never priced,
	// which make CostUSD a floor.
	CostUSD  float64 `json:"cost_usd"`
	Calls    int     `json:"calls"`
	Unpriced int     `json:"unpriced,omitempty"`
	Floor    bool    `json:"floor"`
	// NoItemUSD and NoItemCalls are the part of the week whose records name no
	// work item: conversations, side threads, asks between roles, and anything
	// else that served no one item. It is in CostUSD, and in no count below.
	NoItemUSD   float64 `json:"no_item_usd"`
	NoItemCalls int     `json:"no_item_calls"`
	// Closed, Landed, and Published are the week's counts, each nil where its
	// source could not be read.
	Closed    *int `json:"closed,omitempty"`
	Landed    *int `json:"landed,omitempty"`
	Published *int `json:"published,omitempty"`
	// CostPerClosedItem and CostPerLandedChange are CostUSD divided by the
	// counts, absent where the count is zero or could not be read.
	CostPerClosedItem   *float64 `json:"cost_per_closed_item,omitempty"`
	CostPerLandedChange *float64 `json:"cost_per_landed_change,omitempty"`
	// ManagementUSD is what the management roles spent, and ManagementShare
	// that as a part of the week's known spend, from 0 to 1.
	ManagementUSD   float64 `json:"management_usd"`
	ManagementShare float64 `json:"management_share"`
	// Roles are the week's spend by role: developer runs, reviewers, then the
	// management roles in the order the hierarchy names them.
	Roles []RoleWeekCost `json:"roles"`
	// Says is the week in one sentence, and RolesSays its split by role.
	Says      string `json:"says"`
	RolesSays string `json:"roles_says"`
}

// WeeklyCost is the reading: the weeks, oldest first, and what could not be
// read.
type WeeklyCost struct {
	ObservedAt time.Time  `json:"observed_at"`
	Zone       string     `json:"zone"`
	Weeks      []CostWeek `json:"weeks"`
	// NotRead names every count whose source could not be read.
	NotRead []string `json:"not_read,omitempty"`
	// Problem is set where the spend log could not be read at all, and every
	// figure is then absent.
	Problem string `json:"problem,omitempty"`
}

// ReadWeeklyCost reads the spend log, the run records, and the tracker once
// each and makes the figures for the current week and the three before it.
func ReadWeeklyCost(ctx context.Context, sources WeeklyCostSources) WeeklyCost {
	zone := sources.Zone
	if zone == nil {
		zone = DeliveryZone()
	}
	now := time.Now()
	if sources.Now != nil {
		now = sources.Now()
	}
	local := now.In(zone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	first := weekStart(today).AddDate(0, 0, -7*(weeklyCostWeeks-1))

	var lines []runstate.Spend
	problem := ""
	switch {
	case ctx.Err() != nil:
		problem = fmt.Sprintf("the spend was not read: %v", ctx.Err())
	case sources.Spend == nil:
		problem = absent("the spend log", sources.SpendProblem)
	default:
		var err error
		if lines, err = sources.Spend.List(); err != nil {
			problem = fmt.Sprintf("the spend log could not be read: %v", err)
		}
	}
	if problem != "" {
		return WeeklyCost{ObservedAt: now, Zone: zone.String(), Weeks: []CostWeek{}, Problem: problem}
	}

	runsProblem := ""
	switch {
	case sources.RunsRead:
	case strings.TrimSpace(sources.RunsProblem) != "":
		runsProblem = sources.RunsProblem
	default:
		runsProblem = absent(sourceRuns, "")
	}

	var items []beads.WorkItem
	trackerProblem := ""
	switch {
	case sources.Tracker == nil:
		trackerProblem = absent(sourceTracker, sources.TrackerProblem)
	default:
		var err error
		if items, err = listEveryItem(ctx, sources.Tracker, sources.TrackerReadings, sources.TrackerTimeout, sources.Now); err != nil {
			trackerProblem = fmt.Sprintf("%s could not be read: %v", sourceTracker, err)
		}
	}

	return weeklyCost(lines, sources.Runs, runsProblem, items, trackerProblem, first, weekStart(today).AddDate(0, 0, 7), now, zone)
}

// weeklyCost makes the figures for every whole week from the Monday first to
// the Monday end, from records already read. A problem names a source that
// could not be read, and its counts are then absent.
func weeklyCost(lines []runstate.Spend, runs []runstate.State, runsProblem string, items []beads.WorkItem, trackerProblem string, first, end time.Time, now time.Time, zone *time.Location) WeeklyCost {
	reading := WeeklyCost{ObservedAt: now, Zone: zone.String(), Weeks: []CostWeek{}}
	if runsProblem != "" {
		reading.NotRead = append(reading.NotRead, runsProblem+", so no code change is known to have landed")
	}
	if trackerProblem != "" {
		reading.NotRead = append(reading.NotRead, trackerProblem+", so no item is known to have closed")
	}
	for start := first; start.Before(end); start = start.AddDate(0, 0, 7) {
		next := start.AddDate(0, 0, 7)
		within := func(at time.Time) bool { return !at.Before(start) && at.Before(next) }
		week := CostWeek{
			Week:    start.Format(time.DateOnly),
			From:    start.Format(time.DateOnly),
			To:      next.AddDate(0, 0, -1).Format(time.DateOnly),
			Current: within(now),
		}
		roles := map[domain.AgentRole]*RoleWeekCost{}
		for _, line := range lines {
			if !within(line.At) {
				continue
			}
			role := roles[line.Role]
			if role == nil {
				role = &RoleWeekCost{Role: line.Role, Says: roleCostSays(line.Role), Management: ManagementRole(line.Role)}
				roles[line.Role] = role
			}
			week.Calls++
			role.Calls++
			if strings.TrimSpace(line.WorkItemID) == "" {
				week.NoItemCalls++
			}
			if !line.Known() {
				week.Unpriced++
				role.Unpriced++
				continue
			}
			week.CostUSD += line.AmountUSD
			role.CostUSD += line.AmountUSD
			if strings.TrimSpace(line.WorkItemID) == "" {
				week.NoItemUSD += line.AmountUSD
			}
			if role.Management {
				week.ManagementUSD += line.AmountUSD
			}
		}
		week.Floor = week.Unpriced > 0
		if week.CostUSD > 0 {
			week.ManagementShare = week.ManagementUSD / week.CostUSD
		}
		if runsProblem == "" {
			landed, published := 0, 0
			for _, state := range runs {
				if !state.Status.Terminal() || state.Outcome() != runstate.OutcomeSucceeded || state.Integration == nil || !within(EndedAt(state)) {
					continue
				}
				if state.Document != nil {
					published++
				} else {
					landed++
				}
			}
			week.Landed, week.Published = &landed, &published
			week.CostPerLandedChange = per(week.CostUSD, week.Landed)
		}
		if trackerProblem == "" {
			closed := 0
			for _, item := range items {
				if item.Status == "closed" && !item.ClosedAt.IsZero() && within(item.ClosedAt) {
					closed++
				}
			}
			week.Closed = &closed
			week.CostPerClosedItem = per(week.CostUSD, week.Closed)
		}
		week.Roles = make([]RoleWeekCost, 0, len(roles))
		for _, role := range roles {
			if week.CostUSD > 0 {
				role.Share = role.CostUSD / week.CostUSD
			}
			role.PerClosedItem = per(role.CostUSD, week.Closed)
			role.PerLandedChange = per(role.CostUSD, week.Landed)
			week.Roles = append(week.Roles, *role)
		}
		sort.SliceStable(week.Roles, func(a, b int) bool {
			if roleCostRank(week.Roles[a].Role) != roleCostRank(week.Roles[b].Role) {
				return roleCostRank(week.Roles[a].Role) < roleCostRank(week.Roles[b].Role)
			}
			return week.Roles[a].Role < week.Roles[b].Role
		})
		week.Says = week.says()
		week.RolesSays = week.rolesSays()
		reading.Weeks = append(reading.Weeks, week)
	}
	return reading
}

// per is a cost divided by a count, and nil where the count is unknown or
// zero: nothing closed is not an item that cost everything.
func per(cost float64, count *int) *float64 {
	if count == nil || *count == 0 {
		return nil
	}
	each := cost / float64(*count)
	return &each
}

// roleCostSays names a role's part of a week.
func roleCostSays(role domain.AgentRole) string {
	switch role {
	case domain.RoleDeveloper:
		return "developer runs"
	case domain.RoleReviewer:
		return "reviewers"
	case "":
		return "no role recorded"
	}
	return role.Title()
}

// roleCostRank orders the split: the work first, then the management roles in
// the order the hierarchy names them, then anything else.
func roleCostRank(role domain.AgentRole) int {
	switch role {
	case domain.RoleDeveloper:
		return 0
	case domain.RoleReviewer:
		return 1
	case domain.RoleProductManager:
		return 2
	case domain.RoleArchitect:
		return 3
	case domain.RoleDevelopmentManager:
		return 4
	case domain.RoleProgramManager:
		return 5
	}
	return 6
}

// says is the week in one sentence.
func (w CostWeek) says() string {
	var parts []string
	spent := "nothing spent"
	if w.Calls > 0 {
		spent = fmt.Sprintf("$%.2f known spend across %s", w.CostUSD, count(w.Calls, "provider call"))
		if w.Unpriced > 0 {
			spent = fmt.Sprintf("at least $%.2f known spend across %s, %d of them with no cost reported", w.CostUSD, count(w.Calls, "provider call"), w.Unpriced)
		}
	}
	parts = append(parts, spent)
	switch {
	case w.Closed == nil:
		parts = append(parts, "items closed not known")
	case *w.Closed == 0:
		parts = append(parts, "no item closed")
	default:
		parts = append(parts, fmt.Sprintf("%s closed, $%.2f per item", count(*w.Closed, "item"), *w.CostPerClosedItem))
	}
	switch {
	case w.Landed == nil:
		parts = append(parts, "code changes landed not known")
	case *w.Landed == 0:
		parts = append(parts, "no code change landed")
	default:
		parts = append(parts, fmt.Sprintf("%s landed, $%.2f per change", count(*w.Landed, "code change"), *w.CostPerLandedChange))
	}
	if w.Published != nil && *w.Published > 0 {
		parts = append(parts, fmt.Sprintf("%s published, not counted as code changes", count(*w.Published, "document")))
	}
	if w.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("management roles %.0f%% of known spend", w.ManagementShare*100))
	}
	return strings.Join(parts, "; ")
}

// rolesSays is the week's split by role in one line.
func (w CostWeek) rolesSays() string {
	if len(w.Roles) == 0 {
		return "no role spent anything"
	}
	parts := make([]string, 0, len(w.Roles))
	for _, role := range w.Roles {
		part := fmt.Sprintf("%s $%.2f", role.Says, role.CostUSD)
		var each []string
		if role.PerClosedItem != nil {
			each = append(each, fmt.Sprintf("$%.2f per closed item", *role.PerClosedItem))
		}
		if role.PerLandedChange != nil {
			each = append(each, fmt.Sprintf("$%.2f per landed change", *role.PerLandedChange))
		}
		if len(each) > 0 {
			part += " (" + strings.Join(each, ", ") + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

// Render is the current week and the one before it, as `yoyo status` prints
// them. It is deterministic, so the same reading always prints the same text.
func (c WeeklyCost) Render() string {
	var rendered strings.Builder
	rendered.WriteString("Weekly cost, Monday to Sunday in " + c.Zone + ":\n")
	if c.Problem != "" {
		rendered.WriteString("  could not be read just now: " + c.Problem + "\n")
		return rendered.String()
	}
	shown := c.Weeks
	if len(shown) > 2 {
		shown = shown[len(shown)-2:]
	}
	for index := len(shown) - 1; index >= 0; index-- {
		week := shown[index]
		label := "week of " + week.From
		switch {
		case week.Current:
			label = "this week, from " + week.From
		case index == len(shown)-2 && len(shown) > 1 && shown[len(shown)-1].Current:
			label = "last week, " + week.From + " to " + week.To
		}
		fmt.Fprintf(&rendered, "  %s: %s\n", capitalized(label), week.Says)
		fmt.Fprintf(&rendered, "    by role: %s\n", week.RolesSays)
	}
	for _, problem := range c.NotRead {
		fmt.Fprintf(&rendered, "  not read: %s\n", problem)
	}
	return rendered.String()
}
