//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package mcpcall

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// The project's git server is declared as a shell command naming the
// repository it is confined to from USER, because the harness starts every
// command server with HOME set to the server's own folder. A stand-in declared
// the same way, and started the way the harness starts it, is handed that
// repository as its argument, with USER expanded, and a call's repository path
// reaches it unchanged: nothing in how the harness starts a server or passes a
// call puts a program's path where the repository's belongs. The refusal the
// real server gave was about links in the repository's Git directory, which
// execution.ClearOutwardScratchLinks clears.
func TestAServerDeclaredWithItsRepositoryIsHandedThatRepository(t *testing.T) {
	t.Setenv("USER", "yoyo-test-user")
	base := t.TempDir()
	confined := filepath.Join(base, "yoyo-test-user", "github", "yoyodyne")
	server := Server{Name: "git", Declared: config.MCPServer{
		Command: []string{"sh", "-c", `exec "$0" ` + fakeServerArgument + ` --repository "` + base + `/$USER/github/yoyodyne"`, os.Args[0]},
	}}
	options := Options{Stores: testStores(t)}

	var seen struct {
		Cwd  string   `json:"cwd"`
		Args []string `json:"args"`
	}
	var echoed string
	err := batch(context.Background(), server, options, func(session *Session) error {
		if err := json.Unmarshal([]byte(onlyText(t, call(t, session, "environment", `{}`))), &seen); err != nil {
			return err
		}
		arguments, _ := json.Marshal(map[string]any{"repo_path": confined, "max_count": 1})
		echoed = onlyText(t, call(t, session, "echo", string(arguments)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	at := slices.Index(seen.Args, "--repository")
	if at < 0 || at+1 >= len(seen.Args) {
		t.Fatalf("the server was not handed --repository: %v", seen.Args)
	}
	if got := seen.Args[at+1]; got != confined {
		t.Errorf("the server was handed the repository %q, want the confined %q", got, confined)
	}
	if seen.Args[0] != os.Args[0] {
		t.Errorf("the server ran as %q, want the declared program %q", seen.Args[0], os.Args[0])
	}
	cwd, _ := filepath.EvalSymlinks(seen.Cwd)
	if folder := serverFolder(t, options, server); cwd != folder {
		t.Errorf("the server ran in %q, want its own folder %q", seen.Cwd, folder)
	}
	var arguments struct {
		RepoPath string `json:"repo_path"`
	}
	if err := json.Unmarshal([]byte(echoed), &arguments); err != nil || arguments.RepoPath != confined {
		t.Errorf("the call reached the server as %s, want repo_path %q", echoed, confined)
	}
	if strings.Contains(echoed, os.Args[0]) {
		t.Errorf("the call's arguments carry the program's path: %s", echoed)
	}
}
