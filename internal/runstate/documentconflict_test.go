package runstate

import (
	"strings"
	"testing"
	"time"
)

func TestADocumentConflictRecordNamesTheDocumentAndBothCommits(t *testing.T) {
	written, now := strings.Repeat("a", 40), strings.Repeat("b", 40)
	conflict := DocumentConflict{Path: "docs/designs/x.md", TargetBranch: "main", TargetCommit: now, WrittenAgainst: written, RecordedAt: time.Now()}
	if err := conflict.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"docs/designs/x.md", written, now, "main"} {
		if !strings.Contains(conflict.Says(), want) {
			t.Errorf("%q does not name %s", conflict.Says(), want)
		}
	}
	err := DocumentConflict{}.Validate()
	for _, want := range []string{"path is required", "target_branch is required", "target_commit", "recorded_at is required"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("an empty record was refused with %v, want it to say %q", err, want)
		}
	}
}
