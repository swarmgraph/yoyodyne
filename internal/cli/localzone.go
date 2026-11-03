package cli

import (
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// operatorZone is the zone the dashboard counts the delivery measures in: the
// operator's local zone, which is this machine's, because the dashboard listens
// on this machine alone and the browser reading it runs here too. No
// configuration names the operator's zone yet, and the machine's is the
// default the local-times work (yoyodyne-ifd.432.15) gives one.
//
// The zone is loaded by its name, such as America/New_York, because the
// reading names its zone to the page, and time.Local is only ever called
// "Local". The name comes from TZ where it is set, and otherwise from where
// /etc/localtime points into the zone database. A machine whose zone cannot be
// named counts in Pacific time, readmodel.DeliveryZone, and the reading says
// so by the zone's name, so the page can tell the reader the periods are not
// in their zone rather than label them as if they were.
func operatorZone() *time.Location {
	return operatorZoneFrom(os.LookupEnv, os.Readlink)
}

func operatorZoneFrom(lookupEnv func(string) (string, bool), readlink func(string) (string, error)) *time.Location {
	if name, set := lookupEnv("TZ"); set {
		name = strings.TrimPrefix(name, ":")
		if name == "" {
			return time.UTC
		}
		if zone, err := time.LoadLocation(name); err == nil {
			return zone
		}
	}
	if target, err := readlink("/etc/localtime"); err == nil {
		if _, name, found := strings.Cut(target, "zoneinfo/"); found {
			if zone, err := time.LoadLocation(name); err == nil {
				return zone
			}
		}
	}
	return readmodel.DeliveryZone()
}
