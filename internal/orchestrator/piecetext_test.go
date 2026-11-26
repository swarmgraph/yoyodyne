package orchestrator

// A piece that joined a named epic starts no developer on a text no judgment
// found inside it (PieceTexts, docs/designs/epic-mode-second-part.md, "A piece
// edited after it joined").

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const textWaits = "its title, description or acceptance criteria changed since a reviewer judged it inside epic yoyodyne-epic, so it waits for the new text to be judged"

// judgedTexts answers as the records would for a piece judged inside on the text
// "judged", and counts what it was asked.
type judgedTexts struct {
	asked  []beads.WorkItem
	broken error
}

func (j *judgedTexts) wait(item beads.WorkItem) (string, error) {
	j.asked = append(j.asked, item)
	if j.broken != nil {
		return "", j.broken
	}
	if item.Description != "judged" {
		return textWaits, nil
	}
	return "", nil
}

func pieceFixture(t *testing.T, description string) (*orchestratortest.Tracker, *orchestratortest.Backend, Pipeline, *runstate.Store) {
	t.Helper()
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Description: description, Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	return tracker, provider, automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider), store
}

func TestAPieceWhoseTextWasNotJudgedInsideStartsNoRun(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		texts  judgedTexts
		answer maystart.Answer
	}{
		"its text changed":      {answer: maystart.MayNot},
		"the records are wrong": {texts: judgedTexts{broken: errors.New("the epics' progress cannot be read")}, answer: maystart.CouldNotTell},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tracker, provider, pipeline, _ := pieceFixture(t, "edited since it was judged")
			pipeline.PieceTexts = test.texts.wait
			var refused *OutsideNamedEpicsError
			_, err := pipeline.Run(context.Background(), "yoyodyne-task")
			if !errors.As(err, &refused) || refused.Decision.Answer != test.answer || tracker.Claimed || len(provider.Requests) != 0 {
				t.Fatalf("Run() = %v, claimed %t, %d calls; want it refused before anything is claimed", err, tracker.Claimed, len(provider.Requests))
			}
			if test.answer == maystart.MayNot && !strings.Contains(err.Error(), "waits for the new text to be judged") {
				t.Errorf("the refusal says %q", err)
			}
			// The reading compared is the one the run was about to use.
			if asked := test.texts.asked; len(asked) != 1 || asked[0].Description != "edited since it was judged" {
				t.Errorf("it compared %+v", asked)
			}
		})
	}
}

// The text is compared again before each developer session, and a run already
// under way waits with its work kept until the text it is given was judged inside.
func TestARunUnderWayWaitsForItsTextToBeJudgedWithItsWorkKept(t *testing.T) {
	t.Parallel()
	tracker, provider, pipeline, store := pieceFixture(t, "judged")
	texts := &judgedTexts{}
	// The text is judged at the start and has changed by the developer session.
	pipeline.PieceTexts = func(item beads.WorkItem) (string, error) {
		if len(texts.asked) > 0 {
			item.Description = "edited during the run"
		}
		return texts.wait(item)
	}
	paused, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil || paused.PausedByEpic == nil || !strings.Contains(paused.PausedByEpic.Error(), "waits for the new text") || len(provider.Requests) != 0 || !tracker.Claimed {
		t.Fatalf("Run() = %#v, %v; want it waiting before its developer session with its item claimed", paused, err)
	}
	waiting := waitsForEpic(t, store, paused.RunID)

	// Taken up again with the item's text still not judged, nothing is called.
	tracker.Item.Description = "edited during the run"
	pipeline.PieceTexts = (&judgedTexts{}).wait
	var refused *OutsideNamedEpicsError
	if _, err := pipeline.Run(context.Background(), "yoyodyne-task"); !errors.As(err, &refused) || refused.RunID != paused.RunID {
		t.Fatalf("Run() of the waiting run = %v, want it refused naming the run", err)
	}
	if again := waitsForEpic(t, store, paused.RunID); !again.UpdatedAt.Equal(waiting.UpdatedAt) || len(provider.Requests) != 0 {
		t.Fatal("a second look changed the wait or called a provider")
	}

	// Its text judged inside, or put back to one that was, the same run goes on.
	tracker.Item.Description = "judged"
	finished, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil || finished.RunID != paused.RunID || finished.Integration == nil || len(provider.RequestsForRole(domain.RoleDeveloper)) != 1 {
		t.Fatalf("Run() once judged = %#v, %v; want the same run finished", finished, err)
	}
}
