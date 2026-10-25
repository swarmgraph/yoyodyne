package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// `yoyo status --spend --from --to` reports what the spend over a range of days
// accomplished, from the spend log and the records beside it.
func TestStatusSpendReportsWhatARangeAccomplished(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	store, err := runstate.NewSpendStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	monday := time.Date(2026, 10, 19, 12, 0, 0, 0, readmodel.DeliveryZone())
	for _, line := range []runstate.Spend{
		{Role: domain.RoleProductManager, Phase: runstate.SpendPhaseConversation, At: monday, AmountUSD: 2,
			ConversationID: "chat-0123456789abcdef0123456789abcdef"},
		{Role: domain.RoleDeveloper, Phase: runstate.SpendPhaseDevelopment, At: monday.Add(time.Hour), AmountUSD: 3,
			RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: "yoyodyne-aaa"},
		// A day outside the range is not in it.
		{Role: domain.RoleDeveloper, Phase: runstate.SpendPhaseDevelopment, At: monday.AddDate(0, 0, -7), AmountUSD: 50,
			RunID: "run-0123456789abcdef0123456789abcdee", WorkItemID: "yoyodyne-bbb"},
	} {
		line.SchemaVersion = runstate.SpendSchemaVersion
		line.ProductID = "yoyodyne"
		line.Classification = runstate.SpendKnown
		line.AccountAlias = "default"
		line.ConfigRevision = "cfg-0123456789ab"
		line.Backend = "claude-code"
		if err := store.Append(line); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, code := runCLI(t, "status", "--spend", "--from", "2026-10-19", "--to", "2026-10-25", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"what the spend accomplished, 2026-10-19 to 2026-10-25 (America/Los_Angeles): $5.00",
		"2026-10-19 to 2026-10-25: $5.00 (product-manager $2.00, developer $3.00); no item completed",
		"reviews: 0",
		"completed items: no item completed",
		"1 management pass(es) costing $2.00 named no work item",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	// The turn changed nothing anybody recorded, so its role's passes all did.
	if row := ledgerLine(stdout, "product-manager "); !strings.Contains(row, "$2.00") || !strings.HasSuffix(strings.TrimSpace(row), "100%") {
		t.Fatalf("stdout = %q, want the Lead Product Manager's one pass to have changed nothing", stdout)
	}

	stdout, stderr, code = runCLI(t, "status", "--spend", "--from", "2026-10-19", "--to", "2026-10-25", "--json", "--config", configPath)
	if code != 0 {
		t.Fatalf("json code = %d, stderr = %q", code, stderr)
	}
	var decoded struct {
		Accomplishment readmodel.Accomplishment `json:"accomplishment"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("json = %q: %v", stdout, err)
	}
	if got := decoded.Accomplishment; got.CostUSD != 5 || len(got.Passes) != 1 || got.Passes[0].Outcome != readmodel.PassChangedNothing {
		t.Fatalf("json reading = %+v", got)
	}

	for _, refused := range [][]string{
		{"status", "--from", "2026-10-19"},
		{"status", "--list", "--to", "2026-10-19"},
		{"status", "--shipped", "--from", "2026-10-19"},
		{"status", "--spend", "--from", "2026-10-19", "--kind", "runs"},
		{"status", "--spend", "--from", "2026-10-19", "30"},
	} {
		if _, _, code := runCLI(t, append(refused, "--config", configPath)...); code != 2 {
			t.Fatalf("%v exited %d, want it refused", refused, code)
		}
	}
	if _, stderr, code := runCLI(t, "status", "--spend", "--from", "October 19", "--config", configPath); code != 1 || !strings.Contains(stderr, "YYYY-MM-DD") {
		t.Fatalf("a day that is not a date exited %d: %q", code, stderr)
	}
}
