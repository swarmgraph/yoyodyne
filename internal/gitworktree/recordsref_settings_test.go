package gitworktree

// The shared records are fetched and pushed from a bare repository of the
// machine's own, which does not read the user's repository's config. These tests
// put the remote where only one of that config's settings for reaching it can
// find it, one setting each, and check the records get there as claims would.

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// pushThenFetchRecords pushes one record through records and fetches it back,
// and checks the remote at remoteDir holds it.
func pushThenFetchRecords(t *testing.T, records *RecordsRepository, remoteDir string) {
	t.Helper()
	ctx := context.Background()
	commit, err := records.Build(ctx, "", map[string][]byte{"reports/r1.json": []byte("{}")}, time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := records.Push(ctx, commit, ""); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if got := gitLine(t, remoteDir, "rev-parse", RecordsRef); got != commit {
		t.Fatalf("remote records = %s, want %s", got, commit)
	}
	fetched, err := records.Fetch(ctx)
	if err != nil || fetched != commit {
		t.Fatalf("Fetch() = %q, %v, want %s", fetched, err, commit)
	}
}

// A remote named by an alias only the project's url.<base>.insteadOf turns into
// a path is fetched from, and one only its pushInsteadOf turns into another
// path is pushed to.
func TestRecordsFollowTheProjectsURLRewrites(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	// The push rewrite turns the same alias into a repository of the same name
	// somewhere else.
	pushed := filepath.Join(t.TempDir(), filepath.Base(remote))
	runGit(t, filepath.Dir(pushed), "init", "--quiet", "--bare", pushed)
	disableBackgroundMaintenance(t, pushed)
	runGit(t, repository, "remote", "set-url", "origin", "team:"+filepath.Base(remote))
	runGit(t, repository, "config", "url."+filepath.Dir(remote)+"/.insteadOf", "team:")
	runGit(t, repository, "config", "url."+filepath.Dir(pushed)+"/.pushInsteadOf", "team:")
	ctx := context.Background()
	records := recordsRepositoryFor(t, newConvergeManager(t, repository))

	commit, err := records.Build(ctx, "", map[string][]byte{"reports/r1.json": []byte("{}")}, time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := records.Push(ctx, commit, ""); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if got := gitLine(t, pushed, "rev-parse", RecordsRef); got != commit {
		t.Fatalf("records on the push rewrite's remote = %s, want %s", got, commit)
	}
	if output, err := attemptGit(remote, "rev-parse", "--verify", "--quiet", RecordsRef); err == nil {
		t.Fatalf("the push went to the fetch remote too: %s", output)
	}
	if fetched, err := records.Fetch(ctx); err != nil || fetched != "" {
		t.Fatalf("Fetch() before the fetch remote holds records = %q, %v, want none", fetched, err)
	}
	runGit(t, pushed, "push", "--quiet", remote, RecordsRef+":"+RecordsRef)
	if fetched, err := records.Fetch(ctx); err != nil || fetched != commit {
		t.Fatalf("Fetch() = %q, %v, want %s", fetched, err, commit)
	}
}

// An ssh:// remote on a host that does not exist, reached only through the
// project's core.sshCommand, which runs the remote side of Git locally.
//
// Not parallel: GIT_SSH_COMMAND in the environment would take the place of
// core.sshCommand, so it is cleared for the test.
func TestRecordsUseTheProjectsSSHCommand(t *testing.T) {
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}

	repository, remote := newPublishedRepository(t)
	script := filepath.Join(t.TempDir(), "local-ssh")
	// Git runs `<command> <host> "git-upload-pack '<path>'"`; the script runs the
	// last argument through Git here instead of on the host.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nfor arg; do last=$arg; done\ncase \"$last\" in git-upload-pack*|git-receive-pack*) ;; *) exit 1;; esac\neval \"exec git ${last#git-}\"\n"), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGit(t, repository, "remote", "set-url", "origin", "ssh://records.invalid"+remote)
	runGit(t, repository, "config", "core.sshCommand", script)
	runGit(t, repository, "config", "ssh.variant", "simple")

	pushThenFetchRecords(t, recordsRepositoryFor(t, newConvergeManager(t, repository)), remote)
}

// An HTTP remote that asks for a password, which only the project's credential
// helper supplies. The project's config first clears the helpers set globally,
// so no helper on the machine is asked or given the password.
//
// Not parallel: a terminal prompt is turned off for the test, so a helper that
// was not used fails the test rather than waiting on a person.
func TestRecordsUseTheProjectsCredentialHelper(t *testing.T) {
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatalf("git --exec-path error = %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("no git-http-backend to serve the remote: %v", err)
	}

	repository, remote := newPublishedRepository(t)
	runGit(t, remote, "config", "http.receivepack", "true")
	served := &cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Dir(remote), "GIT_HTTP_EXPORT_ALL=1"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "records" || password != "s3cret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="records"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		served.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	runGit(t, repository, "remote", "set-url", "origin", server.URL+"/"+filepath.Base(remote))
	runGit(t, repository, "config", "--add", "credential.helper", "")
	runGit(t, repository, "config", "--add", "credential.helper", "!f() { test \"$1\" = get && printf 'username=records\\npassword=s3cret\\n'; }; f")

	pushThenFetchRecords(t, recordsRepositoryFor(t, newConvergeManager(t, repository)), remote)
}

// Only the settings for reaching a remote are carried, from the repository's
// own config, in the order Git lists them, so a list of helpers headed by an
// empty one that clears those before it keeps its meaning.
func TestRecordsCarryOnlyTheSettingsForReachingTheRemoteInOrder(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	runGit(t, repository, "config", "--add", "credential.helper", "")
	runGit(t, repository, "config", "--add", "credential.helper", "first")
	runGit(t, repository, "config", "url./elsewhere/.insteadOf", "team:")
	runGit(t, repository, "config", "--add", "credential.helper", "second")
	runGit(t, repository, "config", "core.sshCommand", "ssh -i key")
	runGit(t, repository, "config", "http.https://forge.example/.extraHeader", "X-Token: a=b")
	runGit(t, repository, "config", "user.name", "Somebody")
	records := recordsRepositoryFor(t, newConvergeManager(t, repository))

	got, err := records.remoteSettings(context.Background())
	if err != nil {
		t.Fatalf("remoteSettings() error = %v", err)
	}
	// Git lists them as the file holds them: under the core section the
	// repository was made with, then the credential section.
	want := []string{
		"core.sshcommand=ssh -i key",
		"credential.helper=",
		"credential.helper=first",
		"credential.helper=second",
		"http.https://forge.example/.extraheader=X-Token: a=b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remoteSettings() = %q, want %q", got, want)
	}
	options, err := records.remoteGit(context.Background())
	if err != nil || options[0] != "--git-dir="+records.Dir() || len(options) != 1+2*len(want) || options[1] != "-c" || options[2] != want[0] || options[len(options)-1] != want[len(want)-1] {
		t.Fatalf("remoteGit() = %q, %v", options, err)
	}

	if got := parseRemoteSettings("http.sslverify\x00credential.helper\nwith\nlines\x00"); !reflect.DeepEqual(got, []string{"http.sslverify", "credential.helper=with\nlines"}) {
		t.Errorf("parseRemoteSettings() = %q", got)
	}
}
