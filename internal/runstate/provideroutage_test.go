package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func newProviderOutageStoreAt(t *testing.T, root string, productID domain.ProductID) *ProviderOutageStore {
	t.Helper()
	store, err := NewProviderOutageStore(root, productID)
	if err != nil {
		t.Fatalf("NewProviderOutageStore() error = %v", err)
	}
	return store
}

// The outage is met by one process and read by every other, so it has to
// survive the gap between them: the process that met the provider refusing is
// usually not the one that tells the operator, and never the one that finds it
// answering again.
func TestAProviderOutageSurvivesTheProcessThatNoticedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newProviderOutageStoreAt(t, root, "yoyodyne")
	if _, standing, err := store.Standing(); err != nil || standing {
		t.Fatalf("Standing() = %t, %v, want no outage on a fresh state root", standing, err)
	}

	since := time.Date(2026, 9, 17, 18, 17, 0, 0, time.UTC)
	first, err := store.Notice(ProviderOutageObservation{
		Cause:        domain.ProviderUnauthenticated,
		Provider:     domain.BackendClaudeCode,
		AccountAlias: "default",
		Detail:       "the claude-code backend is not authenticated",
		Waiting:      "the dispatch of yoyodyne-ifd.1",
		At:           since,
	})
	if err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if !first.Since.Equal(since) || !first.LastSeen.Equal(since) || first.Refusals != 1 || first.Cause != domain.ProviderUnauthenticated {
		t.Fatalf("Notice() = %#v, want an outage beginning now with one refusal", first)
	}

	// A second sighting of the same cause is the same outage: it keeps when it
	// began — the age is what every surface says — counts the refusal, and takes
	// the latest thing stopped.
	again, err := store.Notice(ProviderOutageObservation{
		Cause:   domain.ProviderUnauthenticated,
		Detail:  "Not logged in",
		Channel: domain.ProviderChannelStderr,
		Waiting: "the development manager conversation chat-1",
		At:      since.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("second Notice() error = %v", err)
	}
	if !again.Since.Equal(since) || !again.LastSeen.Equal(since.Add(time.Hour)) || again.Refusals != 2 {
		t.Fatalf("second Notice() = %#v, want the same outage confirmed an hour on", again)
	}
	if again.Waiting != "the development manager conversation chat-1" || again.Detail != "Not logged in" || again.Channel != domain.ProviderChannelStderr {
		t.Fatalf("second Notice() = %#v, want the latest refusal's words, the channel they came on, and what it stopped", again)
	}
	if again.Provider != domain.BackendClaudeCode || again.AccountAlias != "default" {
		t.Fatalf("second Notice() = %#v, want the endpoint kept from the sighting that named it", again)
	}

	// A separate store over the same root is what every other process is.
	loaded, standing, err := newProviderOutageStoreAt(t, root, "yoyodyne").Standing()
	if err != nil || !standing {
		t.Fatalf("Standing() = %t, %v, want the recorded outage", standing, err)
	}
	if !loaded.Since.Equal(since) || loaded.Refusals != 2 || loaded.Channel != domain.ProviderChannelStderr {
		t.Fatalf("Standing() = %#v, want the outage as it was recorded", loaded)
	}

	cleared, wasStanding, err := store.Clear()
	if err != nil || !wasStanding {
		t.Fatalf("Clear() = %t, %v, want the outage lifted", wasStanding, err)
	}
	if !cleared.Since.Equal(since) {
		t.Fatalf("Clear() = %#v, want it to report what was lifted", cleared)
	}
	if _, standing, err := store.Standing(); err != nil || standing {
		t.Fatalf("Standing() after Clear() = %t, %v, want the provider answering again", standing, err)
	}
	// Clearing what is not standing is what a served turn does on a healthy
	// harness, which is nearly every turn.
	if _, wasStanding, err := store.Clear(); err != nil || wasStanding {
		t.Fatalf("second Clear() = %t, %v, want a no-op over a provider that is answering", wasStanding, err)
	}
}

// A machine that came back online to find its login expired has met two
// different waits, and the second begins afresh: the operator is told to log in
// now, not that the network has been down since yesterday.
func TestADifferentCauseBeginsAFreshOutage(t *testing.T) {
	t.Parallel()

	store := newProviderOutageStoreAt(t, t.TempDir(), "yoyodyne")
	since := time.Date(2026, 9, 17, 18, 17, 0, 0, time.UTC)
	if _, err := store.Notice(ProviderOutageObservation{Cause: domain.ProviderUnreachable, At: since}); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	renewed, err := store.Notice(ProviderOutageObservation{Cause: domain.ProviderUnauthenticated, At: since.Add(2 * time.Hour)})
	if err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if renewed.Cause != domain.ProviderUnauthenticated || !renewed.Since.Equal(since.Add(2*time.Hour)) || renewed.Refusals != 1 {
		t.Fatalf("Notice() = %#v, want a fresh outage of the new cause", renewed)
	}
}

// What every surface says about the wait names the remedy, because the one
// mechanism that did fire in September named the wrong one.
func TestAProviderOutageSaysWhatTheOperatorDoesAboutIt(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 9, 17, 18, 17, 0, 0, time.UTC)
	unauthenticated := ProviderOutage{Cause: domain.ProviderUnauthenticated, Since: since, Refusals: 3, Provider: domain.BackendClaudeCode, AccountAlias: "default"}
	if said := unauthenticated.Says(); !strings.HasPrefix(said, "The provider is not authenticated; the operator must log in") ||
		!strings.Contains(said, "3 turns refused since 2026-09-17T18:17:00Z") || !strings.Contains(said, "claude-code, account default") {
		t.Fatalf("Says() = %q, want the login named first, the count, and the endpoint", said)
	}
	unreachable := ProviderOutage{Cause: domain.ProviderUnreachable, Since: since, Refusals: 1}
	if said := unreachable.Says(); !strings.HasPrefix(said, "The provider cannot be reached") || !strings.Contains(said, "1 turn refused") {
		t.Fatalf("Says() = %q, want the network named first", said)
	}
	if DescribeProviderOutage(domain.ProviderUnauthenticated) != "a provider that is not authenticated; the operator must log in" ||
		DescribeProviderOutage(domain.ProviderUnreachable) != "a provider that cannot be reached" {
		t.Fatal("DescribeProviderOutage() does not name the two waits as the operator asked")
	}
	if unauthenticated.Mark() == unreachable.Mark() {
		t.Fatal("Mark() does not tell the two causes apart, so a surface would say the second as though it were the first")
	}
	// The pause a run takes on each cause reads back as that cause, and is
	// described the same way the outage is: one wait, one sentence.
	for _, cause := range domain.ProviderOutageCauses() {
		back, away := PausedForProviderOutage(PauseCauseForOutage(cause))
		if !away || back != cause {
			t.Fatalf("PausedForProviderOutage(PauseCauseForOutage(%q)) = %q, %t", cause, back, away)
		}
		if DescribePause(PauseCauseForOutage(cause), "") != DescribeProviderOutage(cause) {
			t.Fatalf("DescribePause() for %q does not say what DescribeProviderOutage() says", cause)
		}
	}
	if _, away := PausedForProviderOutage(PauseUsageLimit); away {
		t.Fatal("a usage limit pause read as a provider outage")
	}
}

// A record that cannot be read is an error rather than an absence: an outage
// nobody can read must never be started through as though the provider were
// answering.
func TestAnUnreadableProviderOutageIsAnErrorRatherThanAnAbsence(t *testing.T) {
	t.Parallel()

	store := newProviderOutageStoreAt(t, t.TempDir(), "yoyodyne")
	if err := writeFileForTest(store.path(), "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Standing(); err == nil {
		t.Fatal("Standing() = nil error over a record that cannot be read")
	}
	// And a record that names a cause this harness does not is refused the same
	// way, rather than read as one of the two.
	if err := writeFileForTest(store.path(), `{"schema_version":1,"product_id":"yoyodyne","cause":"eclipsed","since":"2026-09-17T18:17:00Z","last_seen":"2026-09-17T18:17:00Z","refusals":1}`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Standing(); err == nil || !strings.Contains(err.Error(), "eclipsed") {
		t.Fatalf("Standing() error = %v, want the unknown cause refused by name", err)
	}
}

func writeFileForTest(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// The channel a refusal was read on is part of what an outage record and a
// paused run say, and a channel this harness does not name is refused on both
// rather than written down as a word nobody can read back.
func TestAnOutageRecordNamesTheChannelItWasReadOnOrNone(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 9, 17, 18, 17, 0, 0, time.UTC)
	for _, channel := range append(domain.ProviderChannels(), "") {
		recorded := ProviderOutage{
			SchemaVersion: ProviderOutageSchemaVersion, ProductID: "yoyodyne",
			Cause: domain.ProviderUnauthenticated, Since: since, LastSeen: since, Refusals: 1, Channel: channel,
		}
		if err := recorded.Validate(); err != nil {
			t.Fatalf("Validate() with channel %q error = %v", channel, err)
		}
	}
	unnamed := ProviderOutage{
		SchemaVersion: ProviderOutageSchemaVersion, ProductID: "yoyodyne",
		Cause: domain.ProviderUnauthenticated, Since: since, LastSeen: since, Refusals: 1, Channel: "tty",
	}
	if err := unnamed.Validate(); err == nil || !strings.Contains(err.Error(), `channel "tty"`) {
		t.Fatalf("Validate() with an unnamed channel error = %v, want it refused", err)
	}

	state := testState(t, StatusRunning)
	state.ProviderOutageChannel = domain.ProviderChannelStderr
	if err := state.Validate(); err != nil {
		t.Fatalf("State.Validate() with the stderr channel error = %v", err)
	}
	state.ProviderOutageChannel = "tty"
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "provider_outage_channel is invalid") {
		t.Fatalf("State.Validate() with an unnamed channel error = %v, want it refused", err)
	}
}

// A missing program is said in ordinary words: which provider's program, the
// one fix, and since when in the operator's local time with the zone named
// rather than a UTC timestamp to convert.
func TestAMissingProgramIsSaidWithItsFixAndALocalTime(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 11, 7, 0, 38, 0, 0, time.UTC)
	said := ProviderOutage{Cause: domain.ProviderProgramMissing, Provider: domain.BackendClaudeCode, Since: since, LastSeen: since, Refusals: 2}.Says()
	for _, want := range []string{
		"The claude-code program cannot be found on this machine",
		"providers.claude-code.binary",
		"since " + since.Local().Format("Jan 2 15:04 MST"),
	} {
		if !strings.Contains(said, want) {
			t.Fatalf("said %q, want it to contain %q", said, want)
		}
	}
	if strings.Contains(said, "2026-11-07T00:38:00Z") {
		t.Fatalf("said %q, want no UTC timestamp", said)
	}
}
