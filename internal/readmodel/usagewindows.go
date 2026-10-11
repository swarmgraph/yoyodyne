package readmodel

// How much of each account's usage windows the provider says is used, and when
// each resets, read once for every surface that shows it.
//
// The operator asked for this in two parts: the week's percentage and the time
// it resets, on the dashboard, and shown at the top of it as plain information
// rather than as a warning — the account's seven-day percentage with Fable's
// own beside it. The dashboard's status band and `yoyo status` both print the
// sentence derived here, so the two cannot word one reading differently. A
// window past the configured threshold is said separately, as degraded
// capacity among the factory problems, and never by the reading itself.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
)

// UsageWindowRecord is the durable record of the provider's latest reading of
// each account's windows. It is satisfied by *runstate.UsageWindowStore.
type UsageWindowRecord interface {
	List() ([]backend.UsageWindowReading, error)
}

// DefaultUsageWindowDegradedPercent is the percentage of a window used past
// which the window is said to be degraded capacity, where none was configured:
// execution.usage_window_degraded_percent's default.
const DefaultUsageWindowDegradedPercent = 90

// unnamedAccount is what a reading heard by an invocation that named no account
// is said under.
const unnamedAccount = "an account no invocation named"

// UsageWindows is every account's reading, in account order.
type UsageWindows struct {
	Accounts []AccountUsage `json:"accounts"`
	// DegradedPercent is the threshold the degraded windows below were read
	// against.
	DegradedPercent int `json:"degraded_percent"`
	// Problem is a record that could not be read. The accounts are then said as
	// unread rather than as having no reading.
	Problem string `json:"problem,omitempty"`
}

// AccountUsage is one account's windows as the provider last reported them.
type AccountUsage struct {
	Account string `json:"account"`
	// SevenDay and FiveHour are the account's own windows, and Models the
	// seven-day windows the provider keeps for one model family alone — Fable's
	// among them where the readings carry one. Each is absent where no reading
	// has reported it.
	SevenDay *UsageWindowShown  `json:"seven_day,omitempty"`
	FiveHour *UsageWindowShown  `json:"five_hour,omitempty"`
	Models   []UsageWindowShown `json:"models,omitempty"`
	// Says is the account's reading in the one sentence every surface prints.
	Says string `json:"says"`
}

// UsageWindowShown is one window's latest reading, with what a reader needs
// beside it: how old it is, and whether the window has reset since.
type UsageWindowShown struct {
	backend.UsageWindowReading
	// Family is the model family a model's own window is kept for, and empty on
	// the account's windows.
	Family string `json:"family,omitempty"`
	// Age is how long before the reading this window was heard.
	Age time.Duration `json:"age"`
	// Reset says the window's reset time has passed since it was read, so the
	// figure is what was used before the reset rather than what is used now.
	Reset bool `json:"reset,omitempty"`
	// Degraded says the window is past the configured threshold and has not
	// reset since.
	Degraded bool `json:"degraded,omitempty"`
}

// ReadUsageWindows reads the record against the accounts the caller names,
// every one of which is said whether or not it has a reading. An account the
// record holds that the caller did not name is said as well: a reading that
// arrived is a fact about the account whatever the configuration says now.
func ReadUsageWindows(record UsageWindowRecord, accounts []string, degradedPercent int, now time.Time) UsageWindows {
	if degradedPercent <= 0 {
		degradedPercent = DefaultUsageWindowDegradedPercent
	}
	windows := UsageWindows{Accounts: []AccountUsage{}, DegradedPercent: degradedPercent}
	// A caller that wired no record says nothing about any account, rather than
	// "no reading yet" for each: nothing was asked.
	if record == nil {
		return windows
	}
	readings, err := record.List()
	if err != nil {
		windows.Problem = err.Error()
	}
	byAccount := map[string][]backend.UsageWindowReading{}
	for _, reading := range readings {
		account := reading.Account
		if account == "" {
			account = unnamedAccount
		}
		byAccount[account] = append(byAccount[account], reading)
	}
	names := map[string]struct{}{}
	for _, account := range accounts {
		if account = strings.TrimSpace(account); account != "" {
			names[account] = struct{}{}
		}
	}
	for account := range byAccount {
		names[account] = struct{}{}
	}
	ordered := make([]string, 0, len(names))
	for account := range names {
		ordered = append(ordered, account)
	}
	sort.Strings(ordered)
	for _, account := range ordered {
		windows.Accounts = append(windows.Accounts, accountUsage(account, byAccount[account], degradedPercent, windows.Problem != "", now))
	}
	return windows
}

func accountUsage(account string, readings []backend.UsageWindowReading, degradedPercent int, unread bool, now time.Time) AccountUsage {
	usage := AccountUsage{Account: account}
	for _, reading := range readings {
		shown := UsageWindowShown{UsageWindowReading: reading, Age: now.Sub(reading.At)}
		shown.Reset = reading.ResetsAt != nil && !reading.ResetsAt.After(now)
		if percent, reported := reading.Percent(); reported && !shown.Reset {
			shown.Degraded = percent >= degradedPercent
		}
		switch family := reading.SevenDayModel(); {
		case reading.Window == backend.UsageWindowSevenDay:
			usage.SevenDay = keepLater(usage.SevenDay, shown)
		case reading.Window == backend.UsageWindowFiveHour:
			usage.FiveHour = keepLater(usage.FiveHour, shown)
		case family != "":
			shown.Family = family
			usage.Models = append(usage.Models, shown)
		}
	}
	sort.Slice(usage.Models, func(i, j int) bool { return usage.Models[i].Family < usage.Models[j].Family })
	usage.Says = usage.says(unread)
	return usage
}

// keepLater keeps the later of two readings of one window. The record keeps one
// per provider and account, so two arise only where one alias was served by two
// providers that both report windows.
func keepLater(held *UsageWindowShown, shown UsageWindowShown) *UsageWindowShown {
	if held != nil && !shown.At.After(held.At) {
		return held
	}
	return &shown
}

// says is the account's reading as a sentence: the week first, with each
// model's own week beside it, then the five hours, each with when it resets,
// and then how old the reading is.
func (u AccountUsage) says(unread bool) string {
	if u.SevenDay == nil && u.FiveHour == nil && len(u.Models) == 0 {
		if unread {
			return u.Account + ": the readings could not be read"
		}
		return u.Account + ": no reading yet"
	}
	var parts []string
	var shown []UsageWindowShown
	if u.SevenDay != nil {
		week := percentSaid(*u.SevenDay) + " of the week used"
		var models []string
		for _, model := range u.Models {
			models = append(models, model.Family+" "+percentSaid(model))
		}
		if len(models) > 0 {
			week += " (" + strings.Join(models, ", ") + ")"
		}
		parts = append(parts, week+resetSaid(*u.SevenDay))
		shown = append(shown, *u.SevenDay)
		for _, model := range u.Models {
			// A model's week resetting at another time than the account's says
			// its own reset; the same reset is said once.
			if !sameReset(model, *u.SevenDay) {
				parts = append(parts, model.Family+"'s week"+resetSaid(model))
			}
			shown = append(shown, model)
		}
	} else {
		parts = append(parts, "no reading of the week yet")
		for _, model := range u.Models {
			parts = append(parts, model.Family+" "+percentSaid(model)+" of its week used"+resetSaid(model))
			shown = append(shown, model)
		}
	}
	if u.FiveHour != nil {
		parts = append(parts, "five hours "+percentSaid(*u.FiveHour)+resetSaid(*u.FiveHour))
		shown = append(shown, *u.FiveHour)
	}
	return u.Account + ": " + strings.Join(parts, "; ") + "; " + readSaid(shown)
}

func percentSaid(shown UsageWindowShown) string {
	percent, reported := shown.Percent()
	if !reported {
		return "an unreported share"
	}
	return fmt.Sprintf("%d%%", percent)
}

// resetSaid is when a window resets, in the machine's local zone with the zone
// named, or that it has reset since it was read.
func resetSaid(shown UsageWindowShown) string {
	switch {
	case shown.ResetsAt == nil:
		return ", no reset time reported"
	case shown.Reset:
		return ", reset at " + localMoment(*shown.ResetsAt) + " since it was read"
	default:
		return ", resets " + localMoment(*shown.ResetsAt)
	}
}

func sameReset(a, b UsageWindowShown) bool {
	if a.ResetsAt == nil || b.ResetsAt == nil {
		return a.ResetsAt == nil && b.ResetsAt == nil
	}
	return a.ResetsAt.Equal(*b.ResetsAt)
}

// readSaid is how old the readings are: once where one event reported them
// all, and the oldest and newest where they came from different invocations.
func readSaid(shown []UsageWindowShown) string {
	newest, oldest := shown[0].Age, shown[0].Age
	for _, window := range shown[1:] {
		if window.Age < newest {
			newest = window.Age
		}
		if window.Age > oldest {
			oldest = window.Age
		}
	}
	if newest == oldest {
		return "read " + agoSaid(newest)
	}
	return "read between " + strings.TrimSuffix(agoSaid(newest), " ago") + " and " + agoSaid(oldest)
}

// Degraded is every window past the threshold that has not reset since it was
// read, one attention entry each, for the factory problems.
func (w UsageWindows) Degraded() []Attention {
	var entries []Attention
	for _, account := range w.Accounts {
		var windows []UsageWindowShown
		for _, window := range []*UsageWindowShown{account.SevenDay, account.FiveHour} {
			if window != nil {
				windows = append(windows, *window)
			}
		}
		windows = append(windows, account.Models...)
		for _, window := range windows {
			if !window.Degraded {
				continue
			}
			degraded := DegradedCapacity{Account: account.Account, Window: window, Threshold: w.DegradedPercent}
			entries = append(entries, degradedCapacityAttention(degraded))
		}
	}
	return entries
}

// DegradedCapacity is one window past the configured threshold.
type DegradedCapacity struct {
	Account   string           `json:"account"`
	Window    UsageWindowShown `json:"window"`
	Threshold int              `json:"threshold_percent"`
}

// ID names the account and window.
func (d DegradedCapacity) ID() string { return d.Account + ":" + d.Window.Window }

// Says is the window in a sentence.
func (d DegradedCapacity) Says() string {
	name := map[string]string{backend.UsageWindowSevenDay: "seven-day", backend.UsageWindowFiveHour: "five-hour"}[d.Window.Window]
	if d.Window.Family != "" {
		name = d.Window.Family + "'s own seven-day"
	}
	if name == "" {
		name = d.Window.Window
	}
	return fmt.Sprintf("the %s usage limit on account %s is %s used, past the %d%% threshold%s; read %s",
		name, d.Account, percentSaid(d.Window), d.Threshold, resetSaid(d.Window), agoSaid(d.Window.Age))
}

func degradedCapacityAttention(degraded DegradedCapacity) Attention {
	return Attention{Kind: AttentionCapacityDegraded, ID: degraded.ID(), Mover: MoverNobody, DegradedCapacity: &degraded}
}

// RenderUsageWindows is each account's usage windows, one line per account,
// printed under the four lines rather than as a fifth: it is information, and
// nothing on it waits on anybody. It is empty where no record was wired and no
// account was named.
func (s Standing) RenderUsageWindows() string {
	windows := s.UsageWindows
	if len(windows.Accounts) == 0 && windows.Problem == "" {
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString("Provider usage (as the provider last reported it):\n")
	for _, account := range windows.Accounts {
		rendered.WriteString("  " + account.Says + "\n")
	}
	if windows.Problem != "" {
		rendered.WriteString(partialRead + windows.Problem + "\n")
	}
	return rendered.String()
}
