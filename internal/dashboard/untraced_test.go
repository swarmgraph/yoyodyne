package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// The dashboard receives the untraced finding from the durable pass log,
// with the role as mover. A firing that took no turn leaves it visible; a
// later pass that saved a trace removes it from the next reading.
func TestAnUntracedPassReachesTheDashboardWithItsRoleAsMover(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{
		domain.RoleProductManager, domain.RoleArchitect,
		domain.RoleDevelopmentManager, domain.RoleProgramManager,
	} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()

			store, err := runstate.NewSweepStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
			pass := runstate.Sweep{
				Task: string(role) + "-sweep", Role: role,
				StartedAt: at, EndedAt: at, Turns: 1, Untraced: true,
				Result: &sweep.Result{
					Status: sweep.StatusComplete, Summary: "one finding",
					Findings: []sweep.Finding{{Issue: "reviews wait an hour for a slot", Disposition: sweep.DispositionLeft}},
				},
			}
			appendPass := func(pass runstate.Sweep) {
				t.Helper()
				if err := store.Append(pass); err != nil {
					t.Fatal(err)
				}
			}
			readEntries := func() []readmodel.Attention {
				t.Helper()
				standing := readmodel.ReadStanding(context.Background(), readmodel.Sources{
					Passes: store, Now: func() time.Time { return at.Add(3 * time.Hour) },
				})
				w := serve(t, stubReader{standing: standing})
				response, body := w.get("/api/standing", bearer(w.server.Token()))
				if response.StatusCode != http.StatusOK {
					t.Fatalf("standing: %d %s", response.StatusCode, body)
				}
				var decoded readmodel.Standing
				if err := json.Unmarshal([]byte(body), &decoded); err != nil {
					t.Fatalf("decode standing: %v", err)
				}
				var entries []readmodel.Attention
				for _, entry := range decoded.NeedsHuman {
					if entry.Kind == readmodel.AttentionUntracedPass {
						entries = append(entries, entry)
					}
				}
				return entries
			}
			assertUntraced := func() {
				t.Helper()
				entries := readEntries()
				if len(entries) != 1 {
					t.Fatalf("untraced entries = %+v, want one", entries)
				}
				entry := entries[0]
				if entry.ID != pass.Task || entry.Mover != readmodel.MoverOf(role) || entry.Mover == ownership.MoverOperator {
					t.Fatalf("entry = %+v, want the task with its role as mover", entry)
				}
				finding := entry.UntracedPass
				if finding == nil || finding.Role != role || !finding.StartedAt.Equal(at) || finding.Findings != 1 || finding.First != pass.Result.Findings[0].Issue {
					t.Fatalf("finding = %+v, want the original pass's finding", finding)
				}
				if entry.Label() != "findings not recorded" || !strings.Contains(entry.What(), finding.First) || !strings.Contains(entry.Whose(), "nothing here needs a person") {
					t.Fatalf("entry does not explain the finding and whose move it is: %s; %s", entry.What(), entry.Whose())
				}
			}

			appendPass(pass)
			assertUntraced()
			appendPass(runstate.Sweep{
				Task: pass.Task, Role: role,
				StartedAt: at.Add(time.Hour), EndedAt: at.Add(time.Hour),
				NotStarted: runstate.PreTurnMessageRefused, Problem: "message too large",
			})
			assertUntraced()

			traced := pass
			traced.StartedAt, traced.EndedAt = at.Add(2*time.Hour), at.Add(2*time.Hour)
			traced.Untraced = false
			traced.Saved = []runstate.SavedWrite{{Kind: runstate.SavedMemory, Action: "remember", Memory: "reviews-wait-for-slots", Revision: 1}}
			appendPass(traced)
			if entries := readEntries(); len(entries) != 0 {
				t.Fatalf("untraced entries = %+v, want none after the trace was saved", entries)
			}
		})
	}
}
