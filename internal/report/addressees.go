package report

// How many unhandled reports wait on each addressee, and whether any recurring
// pass reads them (docs/designs/reports-nobody-reads.md, Decision 1).
//
// A project run by hand can hold dozens of reports while nothing in it reads
// them, and the pile's opening line alone does not say so. The count here is
// taken in the same walk as that line (SummarizeHandled), so the total `yoyo
// status` prints and the one `yoyo reports` opens with cannot disagree.
//
// The addressee is the role the run named, kept as written. A report naming no
// role is counted under "no role named" rather than added to the Lead Product
// Manager's count, so the figures match what the runs filed, even though she is
// the one who handles those.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// AddresseeCount is the unhandled reports waiting on one addressee.
type AddresseeCount struct {
	// Role is the role the reports were addressed to, and empty for reports that
	// named no role.
	Role      domain.AgentRole `json:"role,omitempty"`
	Unhandled int              `json:"unhandled"`
	Critical  int              `json:"critical,omitempty"`
	// Read is whether an enabled recurring pass belongs to the role that handles
	// these reports (ReadsReports). It is set only by Pile.ReadBy; a pile counted
	// without the project's configuration says false.
	Read bool `json:"read"`
	// Name is the addressee in words, and Says the figures for it in the words
	// the dashboard shows under the status line, so no surface words them again.
	Name string `json:"name"`
	Says string `json:"says,omitempty"`
}

// addresseeName is an addressee in words, as the status line names it.
func addresseeName(role domain.AgentRole) string {
	if role == "" {
		return "no role named"
	}
	return role.Title()
}

// figures is the count for one addressee in words.
func (c AddresseeCount) figures() string {
	said := fmt.Sprintf("%s: %d unhandled", c.Name, c.Unhandled)
	if c.Critical > 0 {
		said += fmt.Sprintf(", %d of them critical", c.Critical)
	}
	if c.Read {
		return said + "; a recurring pass reads them"
	}
	return said + "; no pass reads them"
}

// namedAddressee is the addressee a report is counted under: the role it named
// where that is a role reports may be addressed to, and none otherwise.
func namedAddressee(reported Report) domain.AgentRole {
	if addressable(reported.For) {
		return reported.For
	}
	return ""
}

// HandlerOf is the role whose pass reads the reports addressed to one
// addressee. The Lead Product Manager handles reports naming no role and
// reports for a program manager; every other addressee handles its own.
func HandlerOf(addressee domain.AgentRole) domain.AgentRole {
	if addressee == "" || addressee == domain.RoleProgramManager || !addressable(addressee) {
		return domain.RoleProductManager
	}
	return addressee
}

// Readers is the roles that have an enabled recurring pass in the project's
// configuration (config.Config.RecurringPassRoles).
type Readers map[domain.AgentRole]bool

// ReadersOf is the set of roles with an enabled recurring pass.
func ReadersOf(roles []domain.AgentRole) Readers {
	readers := Readers{}
	for _, role := range roles {
		readers[role] = true
	}
	return readers
}

// ReadsReports is whether a pass reads the reports addressed to one addressee:
// an enabled recurring task belongs to the role that handles them. It is the
// one test of it, for the status line and for anything that routes a report
// no pass would read.
func (r Readers) ReadsReports(addressee domain.AgentRole) bool {
	return r[HandlerOf(addressee)]
}

// ReadBy is the pile with each addressee marked by whether a pass reads its
// reports.
func (p Pile) ReadBy(readers Readers) Pile {
	counted := make([]AddresseeCount, len(p.Addressees))
	for i, count := range p.Addressees {
		count.Read = readers.ReadsReports(count.Role)
		count.Says = count.figures()
		counted[i] = count
	}
	p.Addressees = counted
	return p
}

// countAddressee adds one unhandled report to the per-addressee counts.
func countAddressee(counts []AddresseeCount, reported Report) []AddresseeCount {
	role := namedAddressee(reported)
	for i := range counts {
		if counts[i].Role == role {
			counts[i].Unhandled++
			if reported.Severity == SeverityCritical {
				counts[i].Critical++
			}
			return counts
		}
	}
	count := AddresseeCount{Role: role, Name: addresseeName(role), Unhandled: 1}
	if reported.Severity == SeverityCritical {
		count.Critical = 1
	}
	return append(counts, count)
}

// orderAddressees puts the most waited-on addressee first, and breaks ties in
// the order Addressees lists the roles, with no role named last.
func orderAddressees(counts []AddresseeCount) {
	place := func(role domain.AgentRole) int {
		for i, known := range Addressees() {
			if role == known {
				return i
			}
		}
		return len(Addressees())
	}
	sort.SliceStable(counts, func(i, j int) bool {
		if counts[i].Unhandled != counts[j].Unhandled {
			return counts[i].Unhandled > counts[j].Unhandled
		}
		return place(counts[i].Role) < place(counts[j].Role)
	})
}

// StatusLine is how the unhandled reports stand, as `yoyo status` prints it
// after "reports: ", and empty where none stands unhandled. It reads the same
// Unhandled and Critical the opening line of `yoyo reports` (Describe) reads.
//
// The clause on passes names the addressees no pass reads, says "no pass reads
// them" where that is all of them, and is left out where every one is read.
func (p Pile) StatusLine() string {
	if p.Unhandled == 0 {
		return ""
	}
	line := fmt.Sprintf("%d unhandled", p.Unhandled)
	if p.Critical > 0 {
		line += fmt.Sprintf(", %d of them critical", p.Critical)
	}
	var counts, unread []string
	for _, count := range p.Addressees {
		counts = append(counts, fmt.Sprintf("%s %d", count.Name, count.Unhandled))
		if !count.Read {
			if count.Role == "" {
				unread = append(unread, "those naming no role")
			} else {
				unread = append(unread, "those for the "+count.Name)
			}
		}
	}
	if len(counts) > 0 {
		line += " (" + strings.Join(counts, ", ") + ")"
	}
	switch {
	case len(unread) == 0:
	case len(unread) == len(p.Addressees):
		line += "; no pass reads them"
	default:
		line += "; no pass reads " + strings.Join(unread, " or ")
	}
	return line + "; read them with yoyo reports"
}
