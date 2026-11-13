package gitworktree

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func recordsRepositoryFor(t *testing.T, manager *Manager) *RecordsRepository {
	t.Helper()
	records, err := manager.RecordsRepository(context.Background(), filepath.Join(t.TempDir(), "state", "records.git"))
	if err != nil {
		t.Fatalf("RecordsRepository() error = %v", err)
	}
	return records
}

// A record built and pushed on an empty remote is what a second factory fetches,
// lists and reads back, byte for byte.
func TestRecordsBuildPushThenAnotherFactoryReadsThem(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	first := recordsRepositoryFor(t, newConvergeManager(t, repository))
	second := recordsRepositoryFor(t, secondFactory(t, remote))

	fetched, err := first.Fetch(ctx)
	if err != nil || fetched != "" {
		t.Fatalf("Fetch() on an empty remote = %q, %v, want no commit", fetched, err)
	}
	body := []byte("{\"id\":\"r1\",\n \"text\":\"line\\nwith \\\"quotes\\\"\"}\n\x00\xff")
	commit, err := first.Build(ctx, "", map[string][]byte{"reports/r1.json": body, "directives/d1.json": []byte("{}")}, now)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := first.Push(ctx, commit, ""); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if got := gitLine(t, remote, "rev-parse", RecordsRef); got != commit {
		t.Fatalf("remote records = %s, want %s", got, commit)
	}
	if author := gitLine(t, remote, "log", "-1", "--format=%an <%ae> %cn <%ce> %ct", commit); author != harnessCommitAuthorName+" <"+harnessCommitAuthorEmail+"> "+harnessCommitAuthorName+" <"+harnessCommitAuthorEmail+"> "+gitLine(t, remote, "log", "-1", "--format=%ct", commit) {
		t.Errorf("commit identity = %s", author)
	}

	got, err := second.Fetch(ctx)
	if err != nil || got != commit {
		t.Fatalf("second Fetch() = %q, %v, want %s", got, err, commit)
	}
	paths, err := second.Paths(ctx, got)
	if err != nil || len(paths) != 2 || !paths["reports/r1.json"] || !paths["directives/d1.json"] {
		t.Fatalf("Paths() = %v, %v", paths, err)
	}
	contents, err := second.Read(ctx, got, []string{"reports/r1.json", "directives/d1.json"})
	if err != nil || string(contents["reports/r1.json"]) != string(body) || string(contents["directives/d1.json"]) != "{}" {
		t.Fatalf("Read() = %q, %v", contents, err)
	}
	if _, err := second.Read(ctx, got, []string{"reports/missing.json"}); err == nil {
		t.Errorf("Read() of a missing path succeeded")
	}
	if empty, err := second.Paths(ctx, ""); err != nil || len(empty) != 0 {
		t.Errorf("Paths(\"\") = %v, %v, want none", empty, err)
	}

	// A second commit is built on the first and keeps its parent.
	next, err := second.Build(ctx, got, map[string][]byte{"reports/r2.json": []byte("{}")}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Build() on a parent error = %v", err)
	}
	if parent := gitLine(t, second.Dir(), "rev-parse", next+"^"); parent != commit {
		t.Errorf("parent = %s, want %s", parent, commit)
	}
	if err := second.Push(ctx, next, got); err != nil {
		t.Fatalf("Push() on the fetched commit error = %v", err)
	}
	if refs := gitLine(t, second.Dir(), "for-each-ref", "refs/yoyodyne/building"); refs != "" {
		t.Errorf("temporary references left behind: %s", refs)
	}
}

func TestRecordsBuildRefusesTakenAndBadPaths(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	records := recordsRepositoryFor(t, newConvergeManager(t, repository))
	parent, err := records.Build(ctx, "", map[string][]byte{"reports/r1.json": []byte("{}")}, now)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, path := range []string{"reports/r1.json"} {
		if _, err := records.Build(ctx, parent, map[string][]byte{path: []byte("{\"changed\":true}")}, now); !errors.Is(err, ErrRecordPathTaken) {
			t.Errorf("Build() of the held path %s error = %v, want ErrRecordPathTaken", path, err)
		}
	}
	if _, err := records.Build(ctx, parent, map[string][]byte{"reports/r1.json/x.json": []byte("{}")}, now); !errors.Is(err, ErrRecordPathTaken) {
		t.Errorf("Build() under a record error = %v, want ErrRecordPathTaken", err)
	}
	for _, path := range []string{"", "/abs.json", "../x.json", "a/../x.json", "a//b.json", "a/./b.json", "a\nb.json", "reports/r1.txt", "reports/", ".git/x.json"} {
		_, err := records.Build(ctx, parent, map[string][]byte{path: []byte("{}")}, now)
		if err == nil || errors.Is(err, ErrRecordPathTaken) {
			t.Errorf("Build() of path %q error = %v, want a refusal of the path", path, err)
		}
	}
	if _, err := records.Build(ctx, parent, nil, now); err == nil {
		t.Errorf("Build() of no files succeeded")
	}
}

// Two factories built on the same fetched commit push at once: one lands, the
// other is told its lease was refused, and after fetching and building again
// the remote holds both factories' records.
func TestRecordsTwoFactoriesRacingExactlyOneWins(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	factories := []*RecordsRepository{
		recordsRepositoryFor(t, newConvergeManager(t, repository)),
		recordsRepositoryFor(t, secondFactory(t, remote)),
	}
	base, err := factories[0].Build(ctx, "", map[string][]byte{"reports/base.json": []byte("{}")}, now)
	if err != nil {
		t.Fatalf("Build() base error = %v", err)
	}
	if err := factories[0].Push(ctx, base, ""); err != nil {
		t.Fatalf("Push() base error = %v", err)
	}

	names := []string{"reports/a.json", "reports/b.json"}
	commits := make([]string, len(factories))
	for index, factory := range factories {
		fetched, err := factory.Fetch(ctx)
		if err != nil || fetched != base {
			t.Fatalf("Fetch() = %q, %v, want %s", fetched, err, base)
		}
		commits[index], err = factory.Build(ctx, fetched, map[string][]byte{names[index]: []byte("{}")}, now)
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
	}

	var wait sync.WaitGroup
	errs := make([]error, len(factories))
	start := make(chan struct{})
	for index, factory := range factories {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errs[index] = factory.Push(ctx, commits[index], base)
		}()
	}
	close(start)
	wait.Wait()

	winner, loser := -1, -1
	for index, err := range errs {
		switch {
		case err == nil:
			if winner != -1 {
				t.Fatalf("both pushes succeeded")
			}
			winner = index
		case errors.Is(err, ErrRecordsLeaseRefused):
			loser = index
		default:
			t.Fatalf("factory %d push error = %v, want a refused lease", index, err)
		}
	}
	if winner == -1 || loser == -1 {
		t.Fatalf("push errors = %v, want exactly one winner and one refused lease", errs)
	}

	fetched, err := factories[loser].Fetch(ctx)
	if err != nil || fetched != commits[winner] {
		t.Fatalf("loser Fetch() = %q, %v, want %s", fetched, err, commits[winner])
	}
	again, err := factories[loser].Build(ctx, fetched, map[string][]byte{names[loser]: []byte("{}")}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("loser Build() error = %v", err)
	}
	if err := factories[loser].Push(ctx, again, fetched); err != nil {
		t.Fatalf("loser Push() error = %v", err)
	}
	listed := gitLine(t, remote, "ls-tree", "-r", "--name-only", RecordsRef)
	for _, want := range []string{"reports/base.json", "reports/a.json", "reports/b.json"} {
		if !containsLine(listed, want) {
			t.Errorf("remote tree %q lacks %s", listed, want)
		}
	}
}

func containsLine(text, want string) bool {
	for _, line := range splitLines(text) {
		if line == want {
			return true
		}
	}
	return false
}

func splitLines(text string) []string {
	var lines []string
	start := 0
	for index := 0; index < len(text); index++ {
		if text[index] == '\n' {
			lines = append(lines, text[start:index])
			start = index + 1
		}
	}
	return append(lines, text[start:])
}

// A remote that cannot be reached is a failure, never an answer about who got
// there first.
func TestRecordsUnreachableRemoteIsNotARefusedLease(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	ctx := context.Background()
	runGit(t, repository, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	records := recordsRepositoryFor(t, newConvergeManager(t, repository))
	commit, err := records.Build(ctx, "", map[string][]byte{"reports/r1.json": []byte("{}")}, time.Now())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := records.Fetch(ctx); err == nil || errors.Is(err, ErrRecordsLeaseRefused) {
		t.Errorf("Fetch() error = %v, want a failure to reach the remote", err)
	}
	if err := records.Push(ctx, commit, ""); err == nil || errors.Is(err, ErrRecordsLeaseRefused) {
		t.Errorf("Push() error = %v, want a failure to reach the remote", err)
	}
}

// The records live only in the bare repository: the user's repository gains
// neither a reference nor an object.
func TestRecordsWriteNothingInTheUsersRepository(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	ctx := context.Background()
	records := recordsRepositoryFor(t, newConvergeManager(t, repository))
	commit, err := records.Build(ctx, "", map[string][]byte{"reports/r1.json": []byte("{}")}, time.Now())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := records.Push(ctx, commit, ""); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if _, err := records.Fetch(ctx); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if refs := gitLine(t, repository, "for-each-ref", "refs/yoyodyne"); refs != "" {
		t.Errorf("references in the user's repository: %s", refs)
	}
	if output, err := attemptGit(repository, "cat-file", "-e", commit); err == nil {
		t.Errorf("the records commit is in the user's repository: %s", output)
	}
	if gitLine(t, records.Dir(), "rev-parse", "--is-bare-repository") != "true" {
		t.Errorf("records repository is not bare")
	}
}
