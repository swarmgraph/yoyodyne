package readmodel

import (
	"strings"
	"testing"
	"time"
)

// A waiting line names the provider's reset, or the next probe, in the
// operator's zone with the zone named, while the window and the probe record it
// writes keep the moment in UTC.
//
// It changes the process's zone, so it does not run in parallel.
func TestCapacityWindowSaysItsTimeInTheOperatorsZone(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skipf("no zone database: %v", err)
	}
	saved := time.Local
	time.Local = tokyo
	defer func() { time.Local = saved }()

	until := time.Date(2026, 10, 10, 18, 30, 0, 0, time.UTC)
	for name, window := range map[string]CapacityWindow{
		"reset named": {Model: "opus", Until: until, Kind: "seven_day_opus"},
		"next probe":  {Model: "opus", Until: until, NoReset: true},
		"run probes":  {Model: "opus", Until: until, NoReset: true, Prober: "run-1"},
	} {
		said := window.Says("opus")
		if !strings.Contains(said, "2026-10-11 03:30 JST") || strings.Contains(said, "18:30") || strings.Contains(said, "Z") {
			t.Errorf("%s: said %q, want the time as 2026-10-11 03:30 JST and no UTC time", name, said)
		}
		window.Scope = []string{"opus"}
		if probes := window.Probes(until.In(tokyo), "probe"); len(probes) != 1 || probes[0].At.Location() != time.UTC {
			t.Errorf("%s: probe record %+v, want its moment kept in UTC", name, probes)
		}
	}
}
