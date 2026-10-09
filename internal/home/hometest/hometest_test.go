package hometest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A record a test leaves in a live product's state fails the run, in either
// layout and in any directory beside config-readers; what the running product
// writes meanwhile, and what was there before, does not.
func TestTheGuardFailsOnlyOnRecordsTheTestsLeft(t *testing.T) {
	earlierHome, currentHome := t.TempDir(), t.TempDir()
	earlier := filepath.Join(earlierHome, "products", "yoyodyne")
	current := filepath.Join(currentHome, "projects", "yoyodyne", "state")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The product's own records, there before the tests ran.
	write(filepath.Join(current, "docket.jsonl"), "{}\n")
	write(filepath.Join(earlier, "config-readers", "supervisor-7045.json"), `{"pid": 7045, "config_path": "/repo/.yoyodyne/config.yaml"}`)
	write(filepath.Join(earlier, "runs", "run-1.events.jsonl"), "{}\n")

	// TMPDIR is the guard's to restore; t.Setenv puts it back if it does not.
	t.Setenv("TMPDIR", t.TempDir())
	var stderr strings.Builder
	code := Guard([]string{earlierHome, currentHome}, func() int {
		private := os.TempDir()
		// What the tests left: a reader naming a configuration under their
		// temporary directory, a supervision record naming their own process,
		// and a record beside config-readers in the current layout.
		write(filepath.Join(earlier, "config-readers", "supervisor-1-x.json"), `{"pid": 1, "config_path": "`+filepath.Join(private, "TestX", "001", "config.yaml")+`"}`)
		write(filepath.Join(earlier, "supervisor", "supervision.json"), `{"pid": `+strconv.Itoa(os.Getpid())+`}`)
		write(filepath.Join(current, "conversations", "c.json"), `{"root": "`+private+`"}`)
		// What the running product did meanwhile.
		write(filepath.Join(earlier, "config-readers", "slack-79198.json"), `{"pid": 79198, "config_path": "/repo/.yoyodyne/config.yaml"}`)
		write(filepath.Join(earlier, "runs", "run-1.events.jsonl"), "{}\n{\"pid\": "+strconv.Itoa(os.Getpid())+"0}\n")
		return 0
	}, &stderr)

	if code != 1 {
		t.Fatalf("Guard() = %d, want 1 for the records the tests left; said %s", code, stderr.String())
	}
	for _, want := range []string{"supervisor-1-x.json", "supervision.json", filepath.Join("projects", "yoyodyne", "state", "conversations", "c.json")} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the guard did not name %s:\n%s", want, stderr.String())
		}
	}
	for _, unwanted := range []string{"supervisor-7045.json", "slack-79198.json", "run-1.events.jsonl"} {
		if strings.Contains(stderr.String(), unwanted) {
			t.Errorf("the guard named the product's own %s:\n%s", unwanted, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(earlier, "config-readers", "supervisor-1-x.json")); err != nil {
		t.Errorf("the guard removed something from the live home: %v", err)
	}
}

// Tests that leave nothing pass with their own exit code, and get their
// temporary directory back as it was.
func TestTheGuardPassesCleanTestsThrough(t *testing.T) {
	live := t.TempDir()
	original := t.TempDir()
	t.Setenv("TMPDIR", original)
	var private string
	var stderr strings.Builder
	code := Guard([]string{live, filepath.Join(live, "missing")}, func() int {
		private = os.TempDir()
		return 3
	}, &stderr)
	if code != 3 || stderr.Len() != 0 {
		t.Fatalf("Guard() = %d, said %q, want the tests' own 3 and nothing said", code, stderr.String())
	}
	if private == original || os.Getenv("TMPDIR") != original {
		t.Errorf("TMPDIR was %q during the run and %q after, want private then %q", private, os.Getenv("TMPDIR"), original)
	}
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Errorf("the private temporary directory outlived the run: %v", err)
	}
}
