package runstate

import (
	"slices"
	"testing"
	"time"
)

func TestAConflictsResolvingSessionIsRecordedBesideTheOneThatWroteTheChange(t *testing.T) {
	t.Parallel()

	at := time.Now()
	state := State{ProviderSessionID: "writer"}
	state = state.PutBackToCatchUpWithMergeQueue(ReplayConflict{Handback: "entry-1"}, "moved", at)
	// Before the new session reports itself there is nothing to resume: the
	// writer's session is never the one that resolves the conflict.
	if got := state.ResolvingSession(); got != "" {
		t.Fatalf("ResolvingSession() = %q, want none", got)
	}
	state.RecordResolvingSession("resolver", at)
	state.RecordResolvingSession("resolver", at)
	state.ProviderSessionID = "resolver"
	if got := state.ResolvingSession(); got != "resolver" {
		t.Fatalf("ResolvingSession() = %q, want the new session", got)
	}
	if len(state.AuthorSessions) != 2 || state.AuthorSessions[0].Work != AuthorWroteChange ||
		state.AuthorSessions[1].Work != AuthorResolvedConflict || state.AuthorSessions[1].Handback != "entry-1" {
		t.Fatalf("AuthorSessions = %#v, want the writer and the resolver once each", state.AuthorSessions)
	}
	if !state.AuthoredBy(" writer ") || !state.AuthoredBy("resolver") || state.AuthoredBy("reviewer") || state.AuthoredBy("") {
		t.Fatal("AuthoredBy() does not name exactly the two authors")
	}
	if others := state.OtherAuthorSessions(); !slices.Equal(others, []string{"writer"}) {
		t.Fatalf("OtherAuthorSessions() = %v, want the writer", others)
	}
	if problems := state.validateAuthorSessions(); len(problems) != 0 {
		t.Fatalf("validateAuthorSessions() = %v", problems)
	}
	state.AuthorSessions = append(state.AuthorSessions, AuthorSession{SessionID: " "})
	if problems := state.validateAuthorSessions(); len(problems) != 1 {
		t.Fatalf("validateAuthorSessions() = %v, want the empty author refused", problems)
	}
}
