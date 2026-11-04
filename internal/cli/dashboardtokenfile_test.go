package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
)

func boundByFile() config.Config {
	return config.Config{Product: config.Product{ID: "yoyodyne"}, Operators: map[string]config.Operator{
		"mason": {DashboardToken: config.DashboardTokenFile, Grants: []config.Grant{config.GrantOwnIntent}},
	}}
}

// An operator whose entry names a token file that is not there is given one as
// the dashboard reads who it accepts: readable and writable by this user alone,
// named in what the dashboard says so he can paste it into the page, and bound
// at once. Nothing about it is left as a problem for a person to act on.
func TestTheDashboardCreatesAMissingOperatorTokenFile(t *testing.T) {
	t.Parallel()
	stateRoot := t.TempDir()
	stores := dashboard.TokenStores{Platform: "linux", StateRoot: stateRoot}
	server, err := dashboard.New("yoyodyne", stubDashboardReader{})
	if err != nil {
		t.Fatal(err)
	}
	file := dashboard.PersonTokenFile(stateRoot, "yoyodyne", "mason")

	lines, problems := dashboardPeople(context.Background(), server, boundByFile(), stores)
	said := strings.Join(lines, "\n")
	if len(problems) != 0 {
		t.Fatalf("problems = %q, want none", problems)
	}
	if !strings.Contains(said, "the harness created it at "+file) {
		t.Fatalf("lines do not say where the file is: %q", said)
	}
	if !strings.Contains(said, "mason approves, declines, and asks from the page with their own token, read from the file "+file) {
		t.Fatalf("lines = %q", said)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions = %o, want 600", perm)
	}
	contents, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(contents))
	if len(token) != 64 || strings.Contains(said, token) {
		t.Fatalf("token of %d characters, or printed in %q", len(token), said)
	}

	// Read again, the file it made is the one read and is not made again.
	again, _ := dashboardPeople(context.Background(), server, boundByFile(), stores)
	if strings.Contains(strings.Join(again, "\n"), "the harness created it") {
		t.Fatalf("created again: %q", again)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, contents) {
		t.Fatal("the token file it created was rewritten on the next read")
	}
}

// A token file that is there is left byte for byte as it is, whatever it
// holds: one the operator stored is never replaced or rotated, and one that
// holds nothing usable is refused for that rather than overwritten.
func TestTheDashboardLeavesAnExistingOperatorTokenFileAsItIs(t *testing.T) {
	t.Parallel()
	for name, contents := range map[string][]byte{
		"a usable token":    []byte(strings.Repeat("9f", 32) + "\n"),
		"a file left empty": {},
		"two lines":         []byte("one\ntwo\n"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stateRoot := t.TempDir()
			stores := dashboard.TokenStores{Platform: "linux", StateRoot: stateRoot}
			file := dashboard.PersonTokenFile(stateRoot, "yoyodyne", "mason")
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, contents, 0o640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			server, err := dashboard.New("yoyodyne", stubDashboardReader{})
			if err != nil {
				t.Fatal(err)
			}
			lines, _ := dashboardPeople(context.Background(), server, boundByFile(), stores)
			if strings.Contains(strings.Join(lines, "\n"), "the harness created it") {
				t.Fatalf("said it created a file that was there: %q", lines)
			}
			after, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, contents) {
				t.Fatalf("the file now holds %q, want %q unchanged", after, contents)
			}
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode() != before.Mode() || !info.ModTime().Equal(before.ModTime()) || !os.SameFile(info, before) {
				t.Fatal("the existing file was replaced or its mode changed")
			}
		})
	}
}
