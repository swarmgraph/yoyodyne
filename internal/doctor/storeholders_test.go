package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

type holdersAre []beads.StoreHolder

func (h holdersAre) Holders(context.Context, string) ([]beads.StoreHolder, error) { return h, nil }

func TestDoctorNamesAnOutsideProcessHoldingTheStoreAndNotTheServer(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	store := filepath.Join(repository, ".beads")
	if err := os.MkdirAll(filepath.Join(store, "embeddeddolt", "calc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := beads.WriteServerRecord(store, beads.ServerRecord{State: beads.ServerRunning, PID: 4242, Port: 1, Credentials: "/c"}); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		holders holdersAre
		status  Status
		want    string
	}{
		"only the server": {holdersAre{{PID: 4242, Command: "dolt sql-server"}}, StatusOK, "no process outside the harness"},
		"another bd":      {holdersAre{{PID: 4242, Command: "dolt sql-server"}, {PID: 777, Command: "bd list --sandbox"}}, StatusWarning, "process 777 (bd list --sandbox)"},
	} {
		findings := (&diagnosis{env: Environment{StoreHolders: test.holders}}).checkStoreHolders(t.Context(), repository)
		if len(findings) != 1 || findings[0].Status != test.status || !strings.Contains(findings[0].Summary, test.want) || strings.Contains(findings[0].Summary, "4242") {
			t.Errorf("%s: findings = %#v, want %q at %s without the server", name, findings, test.want, test.status)
		}
	}
}
