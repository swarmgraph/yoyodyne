package report

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const writtenDigest = `{"severity":"warning","message":"The line held.","digest":{"admissions":["yoyodyne-ifd.500"],"requests":[{"work":"a cache check","goal":"Run development nearly autonomously.","priority":2,"why":"three stops"}],"objections":[{"item":"yoyodyne-ifd.501","concern":"it repeats yoyodyne-ifd.480"}],"open_requests":["exchange-7"]}}`

func digestAttribution(role domain.AgentRole) Attribution {
	return Attribution{Role: role, Agent: "factory", RunID: "chat-1", ProductID: "yoyodyne", RepositoryID: "yoyodyne"}
}

// A digest is decoded from the report block, stamped with the lane and the
// pass, and filed as one report whose text is the fixed shape rendered.
func TestADigestIsFiledAsOneReportInItsFixedShape(t *testing.T) {
	t.Parallel()

	entries, err := Decode(`{"reports":[` + writtenDigest + `]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	entries[0].Digest.Lane, entries[0].Digest.Pass = "factory", "factory#3"
	collected, err := Collect(entries, digestAttribution(domain.RoleProgramManager), time.Now())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(collected) != 1 || collected[0].Digest == nil {
		t.Fatalf("collected = %#v", collected)
	}
	want := strings.Join([]string{
		`Digest of the lane "factory", from pass factory#3.`,
		"The line held.",
		"Admitted in the lane this pass: yoyodyne-ifd.500.",
		"Requests outside the lane:",
		"- a cache check — would serve: Run development nearly autonomously.; recommended priority 2; why: three stops",
		"Objections to new admissions:",
		"- yoyodyne-ifd.501: it repeats yoyodyne-ifd.480",
		"Open requests from earlier passes: exchange-7.",
	}, "\n")
	if collected[0].Message != want {
		t.Fatalf("message =\n%s\nwant\n%s", collected[0].Message, want)
	}
}

// Two digests in one reply are refused whole.
func TestTwoDigestsInOneBlockAreRefused(t *testing.T) {
	t.Parallel()

	entries, err := Decode(`{"reports":[` + writtenDigest + `,` + writtenDigest + `,{"severity":"critical","message":"Nothing is landing."}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v; a block's digests are judged as it is collected, not as it is read", err)
	}
	kept, refused := SeparateDigests(entries)
	if !strings.Contains(refused, "2 digests in one reply") {
		t.Fatalf("refused = %q, want two digests refused", refused)
	}
	if len(kept) != 1 || kept[0].Digest != nil || kept[0].Severity != SeverityCritical {
		t.Fatalf("kept = %#v, want the critical report alone", kept)
	}
}

// A malformed digest is dropped on its own, and the report beside it is kept.
func TestAMalformedDigestIsDroppedAndTheRestKept(t *testing.T) {
	t.Parallel()

	entries, err := Decode(`{"reports":[{"severity":"note","digest":{"requests":[{"work":"w","why":"y"}]}},{"severity":"critical","message":"Nothing is landing."}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	kept, refused := SeparateDigests(entries)
	if !strings.Contains(refused, "requests[0].goal is required") {
		t.Fatalf("refused = %q", refused)
	}
	if len(kept) != 1 || kept[0].Severity != SeverityCritical {
		t.Fatalf("kept = %#v, want the critical report alone", kept)
	}
}

// A digest is the program manager's alone, and one filed unstamped is refused.
func TestADigestFromAnyOtherRoleOrUnstampedIsRefused(t *testing.T) {
	t.Parallel()

	entries, err := Decode(`{"reports":[` + writtenDigest + `]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if _, err := Collect(entries, digestAttribution(domain.RoleProgramManager), time.Now()); err == nil || !strings.Contains(err.Error(), "digest lane") {
		t.Fatalf("an unstamped digest was collected: %v", err)
	}
	entries[0].Digest.Lane, entries[0].Digest.Pass = "factory", "factory#3"
	if _, err := Collect(entries, digestAttribution(domain.RoleDeveloper), time.Now()); err == nil || !strings.Contains(err.Error(), "filed by the program manager alone") {
		t.Fatalf("a developer's digest was collected: %v", err)
	}
}

// A digest carries a line for each repeated stop cause the pass counted, and a
// digest carrying nothing but those is still a digest.
func TestADigestCarriesTheStopCausesThePassCounted(t *testing.T) {
	t.Parallel()

	entries, err := Decode(`{"reports":[{"severity":"note","digest":{"stop_causes":["check-time-limit/bound/checking: 4 stops, report report-1"]}}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	kept, refused := SeparateDigests(entries)
	if refused != "" || len(kept) != 1 {
		t.Fatalf("SeparateDigests() = %d kept, refused %q; want the digest kept", len(kept), refused)
	}
	kept[0].Digest.Lane, kept[0].Digest.Pass = "factory", "factory#4"
	collected, err := Collect(kept, digestAttribution(domain.RoleProgramManager), time.Now())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if !strings.Contains(collected[0].Message, "Repeated stop causes:\n- check-time-limit/bound/checking: 4 stops, report report-1") {
		t.Errorf("message = %q, want the stop cause line", collected[0].Message)
	}
	empty, err := Decode(`{"reports":[{"severity":"note","digest":{"stop_causes":[""]}}]}`)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if _, refused := SeparateDigests(empty); !strings.Contains(refused, "stop_causes[0] is required") {
		t.Errorf("refused = %q, want an empty stop cause line refused", refused)
	}
}
