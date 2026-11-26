package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func confinedAdmission(t *testing.T) MergeQueueAdmission {
	t.Helper()
	admission := testAdmission(t, mainQueue)
	admission.ResolvedHead = strings.Repeat("b", 40)
	admission.Resolution = &MergeQueueResolution{
		ApprovedBy: "reviewer-session", TargetCommit: strings.Repeat("c", 40), MergedTree: strings.Repeat("d", 40),
		Conflicting: []string{"feature.txt"}, ComparedAt: time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC),
	}
	return admission
}

// A confined resolution is admitted with both heads and the comparison, its
// candidate is built from the resolved head, and it is read back as written.
func TestAConfinedResolutionIsAdmittedWithBothHeadsAndTheComparison(t *testing.T) {
	t.Parallel()

	store := newMergeQueueStore(t, t.TempDir())
	asked := confinedAdmission(t)
	entry, admitted, err := store.Admit(context.Background(), asked)
	if err != nil || !admitted {
		t.Fatalf("Admit() = %v, %v", admitted, err)
	}
	entries, err := store.Entries(mainQueue)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Entries() = %#v, %v", entries, err)
	}
	read := entries[0]
	if read.ApprovedHead != asked.ApprovedHead || read.ResolvedHead != asked.ResolvedHead || read.Head() != asked.ResolvedHead ||
		read.Resolution == nil || read.Resolution.ApprovedBy != "reviewer-session" || read.Resolution.Conflicting[0] != "feature.txt" {
		t.Fatalf("entry = %#v, want both heads and the comparison", read)
	}
	if record := entry.AdmissionRecord(); record.Head() != asked.ResolvedHead || record.ApprovedHead != asked.ApprovedHead {
		t.Fatalf("admission record = %#v, want the resolved head apart from the approved one", record)
	}
	// Admitting the same run again is the same admission only where both heads
	// agree.
	if _, again, err := store.Admit(context.Background(), asked); err != nil || again {
		t.Fatalf("Admit() again = %v, %v; want the entry already there", again, err)
	}
	other := asked
	other.ResolvedHead = strings.Repeat("e", 40)
	var conflict MergeQueueConflictError
	if _, _, err := store.Admit(context.Background(), other); !errors.As(err, &conflict) || !strings.Contains(err.Error(), "no reviewer has approved") {
		t.Fatalf("Admit(another resolved head) error = %v, want it refused, naming the head without calling it approved", err)
	}
}

func TestAnEntryWithOneHalfOfAResolutionIsRefused(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(*MergeQueueAdmission){
		"a resolved head without the comparison": func(a *MergeQueueAdmission) { a.Resolution = nil },
		"a comparison without a resolved head":   func(a *MergeQueueAdmission) { a.ResolvedHead = "" },
		"a resolved head that is the approved":   func(a *MergeQueueAdmission) { a.ResolvedHead = a.ApprovedHead },
		"a resolved head that is no commit":      func(a *MergeQueueAdmission) { a.ResolvedHead = "resolved" },
		"no approving review named":              func(a *MergeQueueAdmission) { a.Resolution.ApprovedBy = "" },
	} {
		asked := confinedAdmission(t)
		change(&asked)
		if _, _, err := newMergeQueueStore(t, t.TempDir()).Admit(context.Background(), asked); err == nil {
			t.Errorf("%s: Admit() admitted it", name)
		}
	}
}

// An entry with no resolved head is written without the new fields, so a
// record from before them reads as it is; a record carrying them is refused by
// a strict reading that does not know them rather than written back without
// them.
func TestTheResolutionFieldsAreLeftOutOfEveryOtherEntry(t *testing.T) {
	t.Parallel()

	plain := MergeQueueEntry{ApprovedHead: strings.Repeat("a", 40)}
	encoded, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "resolved_head") || strings.Contains(string(encoded), "resolution") {
		t.Fatalf("an ordinary entry encodes as %s, want no resolution fields", encoded)
	}
	if plain.Head() != plain.ApprovedHead || plain.HeadSays() != "approved head "+plain.ApprovedHead {
		t.Fatalf("an ordinary entry's head is %q, said %q", plain.Head(), plain.HeadSays())
	}
	resolved, err := json.Marshal(MergeQueueEntry{ApprovedHead: strings.Repeat("a", 40), ResolvedHead: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	var older struct {
		ApprovedHead string `json:"approved_head"`
	}
	unknown, err := decodeTolerating(resolved, &older)
	if err != nil || !slices.Contains(unknown, "resolved_head") {
		t.Fatalf("decodeTolerating() = %v, %v; want the resolved head named as a field a build without it does not know", unknown, err)
	}
}

func TestOnlyAMadeComparisonWithNothingOutsideIsConfined(t *testing.T) {
	t.Parallel()

	confined := ResolutionComparison{ComparedAt: time.Now(), MergedTree: strings.Repeat("d", 40), Conflicting: []string{"feature.txt"}}
	if !confined.Confined() || !strings.Contains(confined.Says(), "one conflicting file") {
		t.Fatalf("comparison %#v is not confined: %s", confined, confined.Says())
	}
	for name, comparison := range map[string]ResolutionComparison{
		"not made":       {MergedTree: confined.MergedTree},
		"could not be":   {ComparedAt: time.Now(), Problem: "Git refused"},
		"a file outside": {ComparedAt: time.Now(), MergedTree: confined.MergedTree, Outside: []string{"other.txt"}},
		"more outside":   {ComparedAt: time.Now(), MergedTree: confined.MergedTree, OutsideOmitted: 1},
	} {
		if comparison.Confined() {
			t.Errorf("%s: confined", name)
		}
	}
}

func TestAHandbackKeepsTheTwoHeadsApart(t *testing.T) {
	t.Parallel()

	approved, resolved := strings.Repeat("a", 40), strings.Repeat("b", 40)
	if h := (MergeQueueHandback{ApprovedHead: approved}); h.Head() != approved {
		t.Fatalf("Head() = %q, want the approved head of an ordinary hand-back", h.Head())
	}
	if h := (MergeQueueHandback{ApprovedHead: approved, ResolvedHead: resolved}); h.Head() != resolved {
		t.Fatalf("Head() = %q, want the resolved head", h.Head())
	}
	encoded, err := json.Marshal(MergeQueueHandback{ApprovedHead: approved})
	if err != nil || strings.Contains(string(encoded), "resolved_head") {
		t.Fatalf("an ordinary hand-back encodes as %s, %v; want no resolved head", encoded, err)
	}
}
