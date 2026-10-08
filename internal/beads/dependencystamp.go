package beads

import (
	"strings"
	"time"
)

// bd 1.1.2 records when a dependency was added (`dependencies[].created_at`,
// in `bd export` and in every listing that carries the edge) as the wall-clock
// time of the zone the bd process ran in, followed by a Z that says UTC. Every
// other stamp it writes, the item's own `created_at` among them, is real UTC.
// So a link added on a machine in Pacific daylight time reads seven hours
// before the moment it was made, and one added in Tokyo nine hours after it.
// docs/developing-yoyo.md says which records carry the offset and why they
// are left as they are.
//
// The harness holds the zone of every bd it runs to UTC (inUTC), which makes
// the wall clock bd writes the UTC it claims, so the links the harness makes
// are stamped correctly. A link somebody made with bd by hand, and every link
// made before that, still carries its writer's offset, and this file is how a
// reader of the stamp copes with it.

// dependencyStampSlack is how far a correctly written link may read before the
// item that carries it. bd stamps the link a moment before it stores the item
// it creates with one, and both are written to the second.
const dependencyStampSlack = time.Minute

// inUTC runs bd with its zone set to UTC, so the wall clock it writes into a
// dependency's stamp is the UTC that stamp's Z claims.
func inUTC(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "TZ=") {
			result = append(result, entry)
		}
	}
	return append(result, "TZ=UTC")
}

// dependencyCreatedAt reads when a link was made, correcting the stamps that
// are provably wrong. A link cannot have been made before the item that
// carries it existed, so a stamp more than dependencyStampSlack before that
// item's creation was written in some zone's wall clock. It is read again as
// wall-clock time in writerZone — the zone the tracker's writer ran in, which
// for a tracker kept on this machine is the machine's own — and that reading
// is taken if it is no longer impossible.
//
// A stamp that does not precede its item is returned as written, which is
// right for every link the harness made under inUTC and wrong by the writer's
// offset for a link somebody added by hand well after the item existed: from
// the stamp alone the two look the same. So is a stamp whose item's creation
// is unknown, and one still impossible when read in writerZone, which was
// written in some other zone. The zero time means the stamp could not be read.
func dependencyCreatedAt(recorded string, itemCreated time.Time, writerZone *time.Location) time.Time {
	stamp, err := time.Parse(time.RFC3339, strings.TrimSpace(recorded))
	if err != nil {
		return time.Time{}
	}
	stamp = stamp.UTC()
	if itemCreated.IsZero() || !stamp.Before(itemCreated.Add(-dependencyStampSlack)) {
		return stamp
	}
	if writerZone == nil {
		writerZone = time.Local
	}
	wall := time.Date(stamp.Year(), stamp.Month(), stamp.Day(), stamp.Hour(), stamp.Minute(), stamp.Second(), stamp.Nanosecond(), writerZone).UTC()
	if wall.Before(itemCreated.Add(-dependencyStampSlack)) {
		return stamp
	}
	return wall
}
