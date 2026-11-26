package epicjoin

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
)

const correcting = "yoyodyne-ep.3"

// checked is a check of the whole epic that found something missing, so one
// correction is owed for it.
func (b *bench) checked(id string) {
	b.t.Helper()
	store, err := epicprogress.Open(b.root, product)
	b.must(err)
	record, err := epicscope.Read(b.root, product)
	b.must(err)
	named, _ := record.Named(epic)
	naming := epicprogress.Of(named)
	_, err = store.CallWhileOpen(naming, id, "the check", func() (string, epicprogress.Looked, error) { return "", epicprogress.NotSent, nil },
		func() (string, error) { return "made", nil })
	b.must(err)
	_, err = store.Checked(naming, epicprogress.Check{ID: id, Commit: "0f35eeff1e4a", Findings: []epicprogress.Finding{{Asks: "the second thing", Answer: epicprogress.NotMet, Says: "nothing reads it"}}})
	b.must(err)
}

// corrected is a piece the development manager wrote under the epic and the
// harness linked to the check, with no note on it yet.
func (b *bench) corrected(id, check string) {
	b.t.Helper()
	store, err := epicprogress.Open(b.root, product)
	b.must(err)
	_, err = store.Correcting(epic, check, id)
	b.must(err)
	b.split(id, epic)
}

// A correcting piece is told apart by the owed correction that names it, not by its
// notes: it is noted, then judged in a session of its own, and joins from the check
// only where that session answers inside.
func TestACorrectingPieceJoinsOnlyByItsJudgment(t *testing.T) {
	t.Parallel()
	for answer, joins := range map[epicprogress.Answer]bool{epicprogress.AnswerInside: true, epicprogress.AnswerOutside: false} {
		t.Run(string(answer), func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			b.checked("check-1")
			b.corrected(correcting, "check-1")
			b.says(string(answer))
			if b.starts(correcting) {
				t.Fatal("it starts before it is judged")
			}
			noted := map[string]string{}
			joiner := b.joiner()
			joiner.Note = func(_ context.Context, item, note string) error { noted[item] += note; return nil }
			if owed, err := joiner.Owed(context.Background()); err != nil || !owed {
				t.Fatalf("owed %t, %v, want its judgment owed", owed, err)
			}
			b.must(joiner.Round(context.Background()))
			if !strings.Contains(noted[correcting], "check-1") || !strings.Contains(noted[correcting], "0f35eeff1e4a") || !b.entry().Checks[0].Owed.Noted {
				t.Fatalf("noted %q, recorded %+v, want the note of fixed form written and recorded", noted, b.entry().Checks[0].Owed)
			}
			asked := b.wants(correcting, answer, joins)
			if asked.From != epicprogress.FromCheck("check-1") || len(b.requests) != 1 || !strings.Contains(b.requests[0].Prompt, "made from the check check-1") {
				t.Fatalf("asked %+v with %d requests", asked, len(b.requests))
			}
			if joins && b.entry().Joined[0].Session != "session-1" {
				t.Errorf("it joined as %+v", b.entry().Joined[0])
			}
			b.must(joiner.Round(context.Background()))
			if len(b.requests) != 1 || len(noted) != 1 || strings.Count(noted[correcting], "check-1") != 1 {
				t.Errorf("a second reading judged %d times and noted %q", len(b.requests), noted)
			}
		})
	}
}

// A note of the fixed form that no owed correction matches makes nothing join, and
// a piece written under the epic that names no check is not a correction.
func TestANoteNoCorrectionMatchesMakesNothingJoin(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.checked("check-1")
	record, err := epicscope.Read(b.root, product)
	b.must(err)
	view, err := epicprogress.Read(b.root, product, record)
	b.must(err)
	named, _ := record.Named(epic)
	entry, _ := view.Entry(epicprogress.Of(named))
	b.split("yoyodyne-claims", epic)
	b.items[len(b.items)-1].Notes += "\n\n" + epicprogress.CorrectionNote(epic, entry.Checks[0])
	b.split("yoyodyne-names-nothing", epic)
	if owed, err := b.joiner().Owed(context.Background()); err != nil || owed {
		t.Fatalf("owed %t, %v, want nothing owed", owed, err)
	}
	b.round()
	for _, id := range []string{"yoyodyne-claims", "yoyodyne-names-nothing"} {
		if _, asked := b.entry().Asked(id); asked || b.starts(id) || len(b.requests) != 0 {
			t.Errorf("%s was asked about (%t) or starts (%t)", id, asked, b.starts(id))
		}
	}
}
