package runstate

import (
	"reflect"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestNamedItemsFindsIdentifiersAndNotHyphenatedWords(t *testing.T) {
	t.Parallel()

	text := "yoyodyne-ifd.78.8: see yoyodyne-v3cy and yoyodyne-ifd.78.8 again; the plain-language goal, no-silent-redefinition, yoyodyne-7o2."
	want := []string{"yoyodyne-ifd.78.8", "yoyodyne-v3cy", "yoyodyne-7o2"}
	if got := NamedItems(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("NamedItems() = %v, want %v", got, want)
	}
}

func TestWithRelationsFillsARecordWrittenBeforeTheyWereKept(t *testing.T) {
	t.Parallel()

	old := PendingWrite{
		ID: "document-3.1", Turn: 3, WrittenAt: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC),
		Action: "revise", Artifact: "unpublished-designs", Body: "# Design\n\nRefuses what yoyodyne-ifd.78.8 decided.",
		Reason: "yoyodyne-ifd.78.9: the correction", Answers: []string{"yoyodyne-q2l"},
		Hold:        &artifact.SendingHold{UntilClosed: "yoyodyne-ifd.1", Reason: "wait"},
		Publication: &DocumentPublication{Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: "unpublished-designs", Path: "docs/designs/unpublished-designs.md"}}},
	}
	got := old.WithRelations()
	if want := []string{"yoyodyne-q2l", "yoyodyne-ifd.1", "yoyodyne-ifd.78.9", "yoyodyne-ifd.78.8"}; !reflect.DeepEqual(got.Names, want) {
		t.Fatalf("names = %v, want %v", got.Names, want)
	}
	if want := []DocumentRef{{ID: "unpublished-designs", Path: "docs/designs/unpublished-designs.md"}}; !reflect.DeepEqual(got.Revises, want) {
		t.Fatalf("revises = %v, want %v", got.Revises, want)
	}
	if !got.WrittenAt.Equal(old.WrittenAt) {
		t.Fatalf("written time changed to %v", got.WrittenAt)
	}

	created := PendingWrite{Action: "create", Artifact: "new-design", Body: "# New"}.WithRelations()
	if len(created.Revises) != 0 || len(created.Names) != 0 {
		t.Fatalf("a creation with nothing named = %+v, want no relations", created)
	}
	kept := PendingWrite{Action: "revise", Artifact: "a", Names: []string{"x-1"}, Revises: []DocumentRef{{ID: "a", Path: "docs/a.md"}}, Body: "yoyodyne-q2l"}.WithRelations()
	if !reflect.DeepEqual(kept.Names, []string{"x-1"}) || kept.Revises[0].Path != "docs/a.md" {
		t.Fatalf("a record that kept them was changed: %+v", kept)
	}
}

func TestAConversationRecordWrittenBeforeRelationsWereKeptReadsBackWithThem(t *testing.T) {
	t.Parallel()

	store := newConversationStore(t, t.TempDir())
	conversation := testConversation(t)
	conversation.Role = domain.RoleArchitect
	conversation.PendingWrites = []PendingWrite{{
		ID: "document-2.1", Turn: 2, Action: "revise", Artifact: "some-design", Body: "# D", Reason: "yoyodyne-ifd.78.9: why",
	}}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	read, err := store.Read(conversation.Identity())
	if err != nil || len(read.PendingWrites) != 1 {
		t.Fatalf("Read() = %+v, %v", read, err)
	}
	write := read.PendingWrites[0]
	if !reflect.DeepEqual(write.Names, []string{"yoyodyne-ifd.78.9"}) || !reflect.DeepEqual(write.Revises, []DocumentRef{{ID: "some-design"}}) {
		t.Fatalf("read back names %v revises %v, want the item and the document", write.Names, write.Revises)
	}
}
