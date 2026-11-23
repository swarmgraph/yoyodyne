package report

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

var filedAt = time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)

// piled is a pile of the given size, filed a minute apart in order, all at the
// severity given. Identifiers count up so a test can name one by where it sits.
func piled(count int, severity Severity) []Report {
	pile := make([]Report, 0, count)
	for i := 1; i <= count; i++ {
		pile = append(pile, filed(i, severity, filedAt.Add(time.Duration(i)*time.Minute)))
	}
	return pile
}

func filed(number int, severity Severity, at time.Time) Report {
	return Report{
		SchemaVersion: SchemaVersion,
		ID:            reportID(number),
		Role:          domain.RoleDeveloper,
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      severity,
		Message:       "something was noticed",
		RecordedAt:    at,
	}
}

func reportID(number int) string {
	digits := "00000000000000000000000000000000"
	suffix := ""
	for n := number; n > 0; n /= 16 {
		suffix = string("0123456789abcdef"[n%16]) + suffix
	}
	if suffix == "" {
		suffix = "0"
	}
	return "report-" + digits[:len(digits)-len(suffix)] + suffix
}

// The failure the whole walk exists to fix: a bound that takes the first ten of
// a worst-first pile takes the same ten every time, so a pile deeper than the
// bound never reaches its own tail however long it waits.
func TestTheWalkReachesTheWholePileRatherThanItsFirstSlice(t *testing.T) {
	t.Parallel()

	pile := piled(25, SeverityNote)
	shown := map[string]bool{}
	position := Position{}
	seen := map[string]bool{}
	for turn := 0; turn < 3; turn++ {
		waiting := Pending(pile, position, shown)
		if len(waiting.Next) == 0 {
			t.Fatalf("turn %d was offered nothing of a pile of %d", turn, len(pile))
		}
		carried := waiting.Next
		if len(carried) > 10 {
			carried = carried[:10]
		}
		for _, reported := range carried {
			seen[reported.ID] = true
			shown[reported.ID] = true
			position = At(reported)
		}
	}
	// Three turns of ten reach twenty-five reports rather than the same ten three
	// times, which is the whole of the arithmetic that decides whether a pile
	// drains.
	if len(seen) != 25 {
		t.Fatalf("three turns of ten reached %d of %d reports", len(seen), len(pile))
	}
	// Oldest first, so the age of the oldest thing nobody has decided about falls
	// rather than climbing.
	if !seen[reportID(1)] {
		t.Fatal("the oldest report in the pile was never offered")
	}
}

// An ordinary turn does not offer again what its reader was just shown, or a
// pile being worked through would be re-read every turn. Being shown a report is
// not deciding about it, though, so the start of the reader's next recurring
// pass offers every one of them again.
func TestAShownPileIsSkippedByOrdinaryTurnsAndOfferedAgainAtTheNextPass(t *testing.T) {
	t.Parallel()

	pile := piled(3, SeverityNote)
	shown := map[string]bool{}
	waiting := Pending(pile, Position{}, shown)
	if len(waiting.Next) != 3 {
		t.Fatalf("Next = %d reports, want the whole pile", len(waiting.Next))
	}
	position := Position{}
	for _, reported := range waiting.Next {
		shown[reported.ID] = true
		position = At(reported)
	}
	if again := Pending(pile, position, shown); !again.Empty() {
		t.Fatalf("Pending() = %#v, want nothing offered to an ordinary turn of a reader that has seen the pile", again)
	}
	again := PendingAgain(pile, position, shown)
	if len(again.Again) != 3 || len(again.Next) != 0 || len(again.Urgent) != 0 {
		t.Fatalf("PendingAgain() = %#v, want all three undecided reports offered again and nothing walked", again)
	}
	for i, reported := range again.Again {
		if reported.ID != reportID(i+1) {
			t.Fatalf("Again[%d] = %s, want the pile oldest first", i, reported.ID)
		}
	}
}

// A pass offers again what was shown and still leads with criticals, and what it
// offers again sits apart from the walk, so the position can be advanced over
// the walk alone.
func TestAPassOffersAgainBesideTheWalkAndCriticalsStillLead(t *testing.T) {
	t.Parallel()

	pile := piled(4, SeverityNote)
	pile[0].Severity = SeverityCritical
	shown := map[string]bool{reportID(1): true, reportID(2): true}
	waiting := PendingAgain(pile, At(pile[1]), shown)
	if len(waiting.Urgent) != 1 || waiting.Urgent[0].ID != reportID(1) {
		t.Fatalf("Urgent = %#v, want the critical shown before", waiting.Urgent)
	}
	if len(waiting.Again) != 1 || waiting.Again[0].ID != reportID(2) {
		t.Fatalf("Again = %#v, want the note shown before", waiting.Again)
	}
	if len(waiting.Next) != 2 || waiting.Next[0].ID != reportID(3) || waiting.Next[1].ID != reportID(4) {
		t.Fatalf("Next = %#v, want the walk from where it stopped", waiting.Next)
	}
}

// A report offered once and left has to come back, or the walk converges by
// forgetting rather than by draining. What brings it back is the reader's own
// record of what it has been shown moving on, which is bounded and does.
func TestTheWalkStartsAgainOnceTheReadersRecordHasMovedOn(t *testing.T) {
	t.Parallel()

	pile := piled(3, SeverityNote)
	shown := map[string]bool{reportID(2): true, reportID(3): true}
	// The walk is at the end of the pile and the record no longer says the first
	// report was shown, so it is offered again rather than buried.
	waiting := Pending(pile, At(pile[2]), shown)
	if len(waiting.Next) != 1 || waiting.Next[0].ID != reportID(1) {
		t.Fatalf("Next = %#v, want the report the record has forgotten", waiting.Next)
	}
}

// A critical is what has to be read today rather than when the walk reaches it,
// and it must not drag the position along with it: advancing to where a critical
// sits at the far end of the pile would skip everything the walk had not read.
func TestACriticalJumpsTheWalkWithoutAdvancingIt(t *testing.T) {
	t.Parallel()

	pile := append(piled(3, SeverityNote), filed(9, SeverityCritical, filedAt.Add(time.Hour)))
	waiting := Pending(pile, Position{}, map[string]bool{})
	if len(waiting.Urgent) != 1 || waiting.Urgent[0].ID != reportID(9) {
		t.Fatalf("Urgent = %#v, want the critical alone", waiting.Urgent)
	}
	for _, reported := range waiting.Next {
		if reported.Severity == SeverityCritical {
			t.Fatalf("a critical was in the walk as well as ahead of it: %#v", reported)
		}
	}
	if len(waiting.Next) != 3 {
		t.Fatalf("Next = %d reports, want the three the walk has not passed", len(waiting.Next))
	}
}

// The pile's own order, which the position is a point in. Two reports filed in
// one reply share a moment, so the identifier has to break the tie or the walk
// could pass one of them without ever offering it.
func TestThePileIsOrderedByWhenItWasFiledAndThenByIdentifier(t *testing.T) {
	t.Parallel()

	moment := filedAt.Add(time.Minute)
	ordered := ByFiling([]Report{
		filed(2, SeverityNote, moment),
		filed(3, SeverityNote, filedAt),
		filed(1, SeverityNote, moment),
	})
	want := []string{reportID(3), reportID(1), reportID(2)}
	for i, id := range want {
		if ordered[i].ID != id {
			t.Fatalf("ByFiling()[%d] = %q, want %q", i, ordered[i].ID, id)
		}
	}
	// A position is at a report rather than after it, so the report it names is
	// passed and the one beside it is not.
	position := At(ordered[1])
	if !position.Passed(ordered[1]) || position.Passed(ordered[2]) {
		t.Fatalf("Passed() disagrees with the order the pile is walked in")
	}
	if (Position{}).Passed(ordered[0]) {
		t.Fatal("the beginning of the pile passed a report")
	}
}

// How the pile stands is one derivation because two surfaces saying different
// numbers is a disagreement only the operator can settle.
func TestThePileSaysHowDeepItIsAndHowOldItsOldestUndecidedReportIs(t *testing.T) {
	t.Parallel()

	pile := []Report{
		filed(1, SeverityNote, filedAt),
		filed(2, SeverityCritical, filedAt.Add(time.Hour)),
		filed(3, SeverityWarning, filedAt.Add(2*time.Hour)),
	}
	handlings := []Handling{{ReportID: reportID(2)}}
	summarized := Summarize(pile, handlings, filedAt.Add(48*time.Hour))
	if summarized.Collected != 3 || summarized.Unhandled != 2 {
		t.Fatalf("Summarize() = %#v", summarized)
	}
	// The worst is the worst of what is still undecided: the critical somebody has
	// already dealt with no longer asks for anybody's eye.
	if summarized.Worst != SeverityWarning {
		t.Fatalf("Worst = %q, want the worst of the undecided reports", summarized.Worst)
	}
	if summarized.OldestAge != 48*time.Hour || !summarized.Oldest.Equal(filedAt) {
		t.Fatalf("oldest = %s, age = %s", summarized.Oldest, summarized.OldestAge)
	}
	if described := summarized.Describe(); described != "2 of 3 collected report(s) are unhandled, the oldest filed 2d ago, worst warning" {
		t.Fatalf("Describe() = %q", described)
	}
	// Unhandled criticals are counted on the line rather than folded into the
	// worst, because each one is somebody having to act.
	critical := Summarize(pile, nil, filedAt.Add(48*time.Hour))
	if critical.Critical != 1 {
		t.Fatalf("Critical = %d, want the one unhandled critical", critical.Critical)
	}
	if described := critical.Describe(); described != "3 of 3 collected report(s) are unhandled, the oldest filed 2d ago, 1 of them critical" {
		t.Fatalf("Describe() = %q", described)
	}
	if summarized.Critical != 0 {
		t.Fatalf("Critical = %d once the critical is handled, want 0", summarized.Critical)
	}
	// A pile nobody is waiting on says so rather than reporting an age of nothing.
	settled := Summarize(pile, []Handling{{ReportID: reportID(1)}, {ReportID: reportID(2)}, {ReportID: reportID(3)}}, filedAt)
	if settled.Draining() || settled.Describe() != "nobody is waiting on any of the 3 collected report(s)" {
		t.Fatalf("Describe() = %q on a settled pile", settled.Describe())
	}
}
