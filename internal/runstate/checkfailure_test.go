package runstate

import (
	"strings"
	"testing"
)

func TestAForgeCheckFailureRequiresAFullCommitID(t *testing.T) {
	t.Parallel()
	failure := CheckFailure{Command: "build", ForgeHeadCommit: "short-head"}
	if err := failure.Validate(); err == nil || !strings.Contains(err.Error(), "forge_head_commit must be a full commit id") {
		t.Fatalf("Validate() = %v, want the invalid forge revision refused", err)
	}
	failure.ForgeHeadCommit = strings.Repeat("a", 40)
	if err := failure.Validate(); err != nil {
		t.Fatal(err)
	}
	failure.ForgeHeadCommit = ""
	if err := failure.Validate(); err != nil {
		t.Fatalf("a local check was refused for having no forge revision: %v", err)
	}
}
