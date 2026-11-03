package cli

import (
	"errors"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// The delivery measures are counted in the machine's zone by its name, from
// TZ where it is set and from /etc/localtime otherwise, and in Pacific time,
// named as such, where neither names a zone the database knows.
func TestTheDeliveryZoneIsTheMachinesByName(t *testing.T) {
	t.Parallel()
	unset := func(string) (string, bool) { return "", false }
	tz := func(value string) func(string) (string, bool) {
		return func(name string) (string, bool) { return value, name == "TZ" }
	}
	link := func(target string) func(string) (string, error) {
		return func(string) (string, error) { return target, nil }
	}
	noLink := func(string) (string, error) { return "", errors.New("not a link") }

	for _, test := range []struct {
		name     string
		env      func(string) (string, bool)
		readlink func(string) (string, error)
		want     string
	}{
		{name: "TZ names the zone", env: tz("Europe/Berlin"), readlink: link("/var/db/timezone/zoneinfo/Asia/Tokyo"), want: "Europe/Berlin"},
		{name: "TZ names the zone after a colon", env: tz(":America/New_York"), readlink: noLink, want: "America/New_York"},
		{name: "TZ set empty is UTC", env: tz(""), readlink: link("/usr/share/zoneinfo/Asia/Tokyo"), want: "UTC"},
		{name: "a TZ the database does not know falls to the link", env: tz("Nowhere/Special"), readlink: link("/usr/share/zoneinfo/Asia/Tokyo"), want: "Asia/Tokyo"},
		{name: "macOS points /etc/localtime into its zone database", env: unset, readlink: link("/var/db/timezone/zoneinfo/America/Chicago"), want: "America/Chicago"},
		{name: "Linux points it into its own", env: unset, readlink: link("/usr/share/zoneinfo/Australia/Adelaide"), want: "Australia/Adelaide"},
		{name: "a link outside any zone database names nothing", env: unset, readlink: link("/etc/somewhere-else"), want: readmodel.DeliveryZoneName},
		{name: "no link names nothing", env: unset, readlink: noLink, want: readmodel.DeliveryZoneName},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			zone := operatorZoneFrom(test.env, test.readlink)
			if zone.String() != test.want {
				t.Fatalf("operatorZoneFrom() = %s, want %s", zone, test.want)
			}
			if zone == time.Local {
				t.Fatalf("operatorZoneFrom() is time.Local, which the page could only call %q", time.Local.String())
			}
		})
	}
}
