package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResumeCreationRecoversAnEmptyCheckoutAndRefusesChanges(t *testing.T) {
	for _, condition := range []string{"existing", "missing checkout", "changed"} {
		t.Run(condition, func(t *testing.T) {
			repository := newRepository(t)
			manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
			request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1.2", BaseRef: "main", TargetBranch: "main"}
			original, err := manager.Create(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if condition == "missing checkout" {
				if err := os.RemoveAll(original.Path); err != nil {
					t.Fatal(err)
				}
			}
			if condition == "changed" {
				writeFile(t, original.Path, "unrecorded.txt", "keep this work")
			}
			request.ResumeCreation = true
			resumed, err := manager.Create(context.Background(), request)
			if condition == "changed" {
				if err == nil {
					t.Fatal("a changed checkout was adopted as an empty creation")
				}
				if _, err := os.Stat(filepath.Join(original.Path, "unrecorded.txt")); err != nil {
					t.Fatal("unrecorded work was lost")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if original != resumed {
				t.Fatalf("creation changed: %+v, %+v", original, resumed)
			}
			if err := manager.VerifyOwnedHead(context.Background(), resumed); err != nil {
				t.Fatal(err)
			}
		})
	}
}
