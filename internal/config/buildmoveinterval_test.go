package config

import (
	"strings"
	"testing"
)

// The least time between two moves onto a new build is gone: a project file
// that still names it loads, at any value it was once allowed or not, and the
// setting is listed as one nothing reads so `yoyo doctor` can say so. Neither
// the built-in defaults nor a generated file names it (yoyodyne-ifd.428.225).
func TestAFileStillNamingTheBuildMoveIntervalLoadsAndTheSettingIsListedAsNoLongerRead(t *testing.T) {
	t.Parallel()

	inherited := loadProject(t, minimalProjectConfig, nil)
	if _, named := inherited.NoLongerRead["execution.build_move_interval"]; named {
		t.Fatalf("a file that does not name build_move_interval lists it as no longer read: %v", inherited.NoLongerRead)
	}
	if _, origin := inherited.Origins["execution.build_move_interval"]; origin {
		t.Fatal("build_move_interval still has an origin, want the built-in defaults to name it no more")
	}
	for _, value := range []string{"3h", "30m", "48h"} {
		named := loadProject(t, minimalProjectConfig+"execution:\n  build_move_interval: "+value+"\n", nil)
		if origin := named.NoLongerRead["execution.build_move_interval"]; origin == "" {
			t.Fatalf("build_move_interval %s: NoLongerRead = %v, want the setting listed with the file that names it", value, named.NoLongerRead)
		}
	}
	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: "example", Repository: "."})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	if strings.Contains(string(scaffold.Config.Content), "build_move_interval") {
		t.Fatal("a generated configuration still names build_move_interval")
	}
}
