package publish

import (
	"testing"
	"time"
)

// The forge's merge time is read in UTC, and an answer that gives none, or
// gives one in a form the forge does not use, is no time rather than an error.
func TestTheForgesMergeTimeIsReadInUTC(t *testing.T) {
	t.Parallel()

	want := time.Date(2026, 11, 9, 23, 22, 12, 0, time.UTC)
	for reported, expected := range map[string]time.Time{
		"2026-11-09T23:22:12Z":      want,
		"2026-11-09T15:22:12-08:00": want,
		"":                          {},
		"yesterday":                 {},
	} {
		if got := forgeTime(reported); !got.Equal(expected) || got.Location() != time.UTC {
			t.Errorf("forgeTime(%q) = %v, want %v in UTC", reported, got, expected)
		}
	}
}
