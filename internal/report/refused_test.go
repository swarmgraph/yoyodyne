package report

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A block whose entries are partly unacceptable files the rest, and keeps each
// refused one with what was wrong and the message it gave: a severity not on
// the list, an addressee that is not a role, and a message past the limit,
// which is cut to the limit with the cut said.
func TestARefusedEntryKeepsItsMessageAndCostsTheOthersNothing(t *testing.T) {
	long := strings.Repeat("x", MaxMessageBytes+10)
	reply := "Done.\n```yoyodyne-report\n" + `{"reports":[` +
		`{"severity":"warning","message":"kept","for":"development-manager"},` +
		`{"severity":"loud","message":"the test server stays up"},` +
		`{"severity":"note","message":"tell him","for":"operator"},` +
		`{"severity":"note","message":"` + long + `"}]}` + "\n```\n"
	rest, entries, refused, err := ExtractKeeping(reply)
	if err != nil {
		t.Fatalf("ExtractKeeping() error = %v", err)
	}
	if strings.Contains(rest, "yoyodyne-report") || len(entries) != 1 || entries[0].Message != "kept" {
		t.Fatalf("rest = %q, entries = %#v", rest, entries)
	}
	if len(refused) != 3 {
		t.Fatalf("refused = %#v", refused)
	}
	if refused[0].Index != 1 || !strings.Contains(refused[0].Why, `severity "loud"`) || refused[0].Message != "the test server stays up" {
		t.Fatalf("refused severity = %#v", refused[0])
	}
	if !strings.Contains(refused[1].Why, `for "operator"`) || refused[1].For != "operator" {
		t.Fatalf("refused addressee = %#v", refused[1])
	}
	if len(refused[2].Message) != MaxMessageBytes || refused[2].CutBytes != 10 {
		t.Fatalf("refused long message kept %d bytes, cut %d", len(refused[2].Message), refused[2].CutBytes)
	}
	says := refused[0].Says(domain.RoleDeveloper)
	if !strings.HasPrefix(says, "a developer report was refused: severity \"loud\"") || !strings.Contains(says, "kept in this run's summary and in the work item's notes") {
		t.Fatalf("says = %q", says)
	}
	if whole := refused[2].Whole(domain.RoleDeveloper); !strings.Contains(whole, "[cut: the last 10 bytes were left out to keep it within the 4096-byte report limit]") {
		t.Fatalf("whole = %q", whole)
	}
	if whole := refused[1].Whole(domain.RoleDeveloper); whole != `refused note report from the developer, for "operator": tell him` {
		t.Fatalf("whole = %q", whole)
	}

	// Extract, which a conversation still reads with, refuses the block whole
	// in the words it always used.
	if _, kept, err := Extract(reply); err == nil || len(kept) != 0 || !strings.Contains(err.Error(), "reports[1]: invalid report: severity") {
		t.Fatalf("Extract() = %#v, %v", kept, err)
	}
}

// A cut never splits a character.
func TestACutMessageEndsOnAWholeCharacter(t *testing.T) {
	message := strings.Repeat("a", MaxMessageBytes-1) + "é" + "b"
	kept, cut := cutMessage(message, MaxMessageBytes)
	if kept != strings.Repeat("a", MaxMessageBytes-1) || cut != 3 {
		t.Fatalf("kept %d bytes, cut %d", len(kept), cut)
	}
}

// Who reads a report for an addressee: its own pass, her pass in its place,
// or none at all.
func TestWhoReadsAReportNoPassOfItsAddresseeReads(t *testing.T) {
	dm := domain.RoleDevelopmentManager
	none := ReadersOf(nil)
	lead := ReadersOf([]domain.AgentRole{domain.RoleProductManager})
	own := ReadersOf([]domain.AgentRole{dm})
	if !none.ReachesNoPass(dm) || none.LeadReadsInstead(dm) || !none.ReachesNoPass("") {
		t.Fatal("with no pass, nothing reads a report")
	}
	if lead.ReachesNoPass(dm) || !lead.LeadReadsInstead(dm) || lead.LeadReadsInstead("") {
		t.Fatal("with only her pass, she reads the development manager's reports in its place, and her own as her own")
	}
	if own.ReachesNoPass(dm) || own.LeadReadsInstead(dm) || !own.ReachesNoPass("") {
		t.Fatal("with only the development manager's pass, its reports are its own and those naming no role reach nobody")
	}
	reported := Report{ID: "report-1", For: dm, Severity: SeverityWarning, Message: "m"}
	if says := lead.ReachesNoPassSays(reported); !strings.Contains(says, "development manager, a role no pass reads here") {
		t.Fatalf("says = %q", says)
	}
	if says := own.ReachesNoPassSays(reported); says != "" {
		t.Fatalf("says = %q, want nothing for a report its own pass reads", says)
	}
}

// The block a run prints after its outcome holds the reports no pass reads,
// whole with severity and addressee, and then the refused messages.
func TestTheClosingBlockHoldsUnreadReportsThenRefusedMessages(t *testing.T) {
	unread := []Report{{ID: "report-1", For: domain.RoleDevelopmentManager, Severity: SeverityWarning, Message: "the test server stays up"}}
	refused := []RefusedBy{{Role: domain.RoleReviewer, Refused: Refused{Severity: "loud", Message: "the plugin crashes", Why: "bad"}}}
	block := ClosingBlock(unread, refused)
	unreadAt := strings.Index(block, "warning report report-1, for the development manager, which no pass reads here: the test server stays up")
	refusedAt := strings.Index(block, "refused loud report from the reviewer, naming no role: the plugin crashes")
	if unreadAt < 0 || refusedAt < unreadAt {
		t.Fatalf("block = %q", block)
	}
	if ClosingBlock(nil, nil) != "" {
		t.Fatal("an empty closing block was printed")
	}
}
