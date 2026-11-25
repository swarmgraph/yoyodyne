package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// The reconcile sweep's check of an escalation naming the operator's own hold
// reads the product's epic mode.
func TestTheSweepsOwnHoldCheckReadsEpicMode(t *testing.T) {
	t.Parallel()
	parts := components{stateRoot: t.TempDir(), config: config.Config{Product: config.Product{ID: "yoyodyne"}}}
	check := func(when string, want, fails bool) {
		t.Helper()
		if stands, err := ownHoldOf(parts)(); stands != want || (err != nil) != fails {
			t.Fatalf("%s: own hold stands = %t, %v, want %t, failing %t", when, stands, err, want, fails)
		}
	}
	check("no epic named", false, false)
	operator, err := epicscope.ForOperator(parts.stateRoot, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Start(epicscope.Naming{Epic: "yoyodyne-ep", From: "terminal", Members: []string{"yoyodyne-ep.1"}}, time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	check("epic mode on", true, false)
	if _, err := operator.TurnOff(); err != nil {
		t.Fatal(err)
	}
	check("epic mode off", false, false)
	if err := os.WriteFile(filepath.Join(home.ProductDirectory(parts.stateRoot, "yoyodyne"), "epic-scope.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("unreadable", false, true)
}
