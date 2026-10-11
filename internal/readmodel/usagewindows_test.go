package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

type fakeUsageWindows struct {
	readings []backend.UsageWindowReading
	err      error
}

func (f fakeUsageWindows) List() ([]backend.UsageWindowReading, error) { return f.readings, f.err }

func windowReading(account, window string, utilization float64, resets, at time.Time) backend.UsageWindowReading {
	return backend.UsageWindowReading{Provider: domain.BackendClaudeCode, Account: account, Window: window, Utilization: &utilization, ResetsAt: &resets, At: at}
}

var usageNow = time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)

// An account with no reading says so in words rather than as a zero, and a
// caller that wired no record says nothing about any account.
func TestAnAccountWithNoUsageReadingSaysNoReadingYet(t *testing.T) {
	t.Parallel()
	week := usageNow.Add(5 * 24 * time.Hour)
	windows := ReadUsageWindows(fakeUsageWindows{readings: []backend.UsageWindowReading{
		windowReading("default", "seven_day", 0.03, week, usageNow.Add(-4*time.Minute)),
	}}, []string{"default", "second"}, 0, usageNow)
	if len(windows.Accounts) != 2 || windows.Accounts[1].Account != "second" {
		t.Fatalf("accounts = %+v, want both named accounts", windows.Accounts)
	}
	if says := windows.Accounts[1].Says; says != "second: no reading yet" {
		t.Fatalf("says = %q", says)
	}
	if strings.Contains(windows.Accounts[1].Says, "0%") {
		t.Fatalf("an account with no reading is said as a zero: %q", windows.Accounts[1].Says)
	}
	if none := ReadUsageWindows(nil, []string{"default"}, 0, usageNow); len(none.Accounts) != 0 {
		t.Fatalf("no record wired = %+v, want nothing said", none)
	}
	unread := ReadUsageWindows(fakeUsageWindows{err: errors.New("decode usage window record: unexpected EOF")}, []string{"default"}, 0, usageNow)
	if unread.Problem == "" || unread.Accounts[0].Says != "default: the readings could not be read" {
		t.Fatalf("unreadable record = %+v, want it said rather than no reading", unread)
	}
}

// The week's share is said first with Fable's own week beside it, then the five
// hours, each with its reset in the local zone with the zone named, and how old
// the reading is.
func TestTheFableWeekIsSaidBesideTheAccounts(t *testing.T) {
	t.Parallel()
	week := usageNow.Add(5 * 24 * time.Hour)
	fiveHours := usageNow.Add(2 * time.Hour)
	read := usageNow.Add(-4 * time.Minute)
	windows := ReadUsageWindows(fakeUsageWindows{readings: []backend.UsageWindowReading{
		windowReading("default", "seven_day", 0.41, week, read),
		windowReading("default", "seven_day_fable", 0.62, week, read),
		windowReading("default", "five_hour", 0.07, fiveHours, read),
	}}, []string{"default"}, 0, usageNow)
	account := windows.Accounts[0]
	if account.SevenDay == nil || len(account.Models) != 1 || account.Models[0].Family != "fable" || account.FiveHour == nil {
		t.Fatalf("account = %+v, want the week, fable's week, and the five hours", account)
	}
	want := "default: 41% of the week used (fable 62%), resets " + localMoment(week) +
		"; five hours 7%, resets " + localMoment(fiveHours) + "; read 4 minutes ago"
	if account.Says != want {
		t.Fatalf("says = %q\nwant  %q", account.Says, want)
	}
	if zone := week.Local().Format("MST"); !strings.Contains(account.Says, zone) {
		t.Fatalf("says = %q, want the zone %s named", account.Says, zone)
	}
	standing := Standing{UsageWindows: windows}
	if rendered := standing.RenderUsageWindows(); rendered != "Provider usage (as the provider last reported it):\n  "+want+"\n" {
		t.Fatalf("rendered = %q", rendered)
	}
}

// A window past the threshold is degraded capacity among the factory problems,
// nobody's move, and never on the attention line; a window whose reset has
// passed since it was read is not, and says so; and the threshold is the
// configured one where there is one.
func TestAWindowPastTheThresholdIsAFactoryProblemOnly(t *testing.T) {
	t.Parallel()
	week := usageNow.Add(2 * 24 * time.Hour)
	record := fakeUsageWindows{readings: []backend.UsageWindowReading{
		windowReading("default", "seven_day", 0.92, week, usageNow.Add(-time.Minute)),
		windowReading("default", "five_hour", 0.95, usageNow.Add(-time.Hour), usageNow.Add(-3*time.Hour)),
	}}
	sources := quietSources()
	sources.UsageWindows = record
	sources.UsageAccounts = []string{"default"}
	sources.Now = func() time.Time { return usageNow }
	standing := ReadStanding(context.Background(), sources)

	var degraded []Attention
	for _, entry := range standing.FactoryProblems {
		if entry.Kind == AttentionCapacityDegraded {
			degraded = append(degraded, entry)
		}
	}
	if len(degraded) != 1 || degraded[0].ID != "default:seven_day" || degraded[0].Mover != MoverNobody {
		t.Fatalf("degraded = %+v, want the week alone, nobody's move", degraded)
	}
	if what := degraded[0].What(); !strings.Contains(what, "seven-day window on account default is 92% used, past the 90% threshold") {
		t.Fatalf("what = %q", what)
	}
	for _, entry := range standing.NeedsHuman {
		if entry.Kind == AttentionCapacityDegraded {
			t.Fatalf("degraded capacity is on the attention line: %+v", entry)
		}
	}
	if says := standing.UsageWindows.Accounts[0].Says; !strings.Contains(says, "92% of the week used") ||
		!strings.Contains(says, "five hours 95%, reset at "+localMoment(usageNow.Add(-time.Hour))+" since it was read") {
		t.Fatalf("says = %q", says)
	}

	sources.UsageWindowDegradedPercent = 95
	if raised := ReadStanding(context.Background(), sources).UsageWindows.Degraded(); len(raised) != 0 {
		t.Fatalf("at a 95%% threshold, degraded = %+v", raised)
	}
}
