package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FileAtCommit reads a path as the named commit holds it, whatever the working
// tree and the branch have since become, and returns it byte for byte.
func TestFileAtCommitReadsThePathAsTheCommitHoldsIt(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	writeFile(t, repository, "docs/guide.md", "at the base\n")
	writeFile(t, repository, "docs/unterminated.md", "no final newline")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "the base")
	base := gitLine(t, repository, "rev-parse", "HEAD")
	writeFile(t, repository, "docs/guide.md", "later\n")
	runGit(t, repository, "commit", "-am", "later")
	if err := os.Symlink("guide.md", filepath.Join(repository, "docs", "link.md")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "a link")
	tip := gitLine(t, repository, "rev-parse", "HEAD")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	ctx := context.Background()

	for _, test := range []struct {
		commit, path, want string
	}{
		{commit: base, path: "docs/guide.md", want: "at the base\n"},
		{commit: tip, path: "docs/guide.md", want: "later\n"},
		{commit: base, path: "docs/unterminated.md", want: "no final newline"},
	} {
		file, err := manager.FileAtCommit(ctx, test.commit, test.path, 1<<20)
		if err != nil {
			t.Fatalf("FileAtCommit(%s, %s) error = %v", test.commit, test.path, err)
		}
		if string(file.Content) != test.want || file.Size != int64(len(test.want)) {
			t.Fatalf("FileAtCommit(%s, %s) = %q (%d bytes), want %q", test.commit, test.path, file.Content, file.Size, test.want)
		}
	}

	// A file larger than the caller would read is measured and not read.
	large, err := manager.FileAtCommit(ctx, base, "docs/guide.md", 4)
	if err != nil || large.Content != nil || large.Size != int64(len("at the base\n")) {
		t.Fatalf("FileAtCommit() over the bound = %#v, %v; want its size and no content", large, err)
	}

	// Absent, a directory, and a symlink are none of them a document to read.
	for _, test := range []struct{ commit, path string }{
		{commit: base, path: "docs/missing.md"},
		{commit: base, path: "docs"},
		{commit: base, path: "docs/link.md"},
		{commit: tip, path: "docs/link.md"},
	} {
		if _, err := manager.FileAtCommit(ctx, test.commit, test.path, 1<<20); !errors.Is(err, ErrNotAtCommit) {
			t.Fatalf("FileAtCommit(%s, %s) error = %v, want ErrNotAtCommit", test.commit, test.path, err)
		}
	}
}

// Git output is data rather than an AI provider's text, so a file whose lines
// look like credentials, such as a test of the secret scrubbing, is read back
// byte for byte rather than rewritten and then refused for its size. The samples
// are put together from pieces, as a whole one written in a file is one the
// forge's secret scanner refuses a push over.
func TestFileAtCommitReadsSecretShapedTextByteForByte(t *testing.T) {
	t.Parallel()

	content := strings.Join([]string{
		`{"api_key":"abcdefgh12345678","password":"hunter2hunter2"}`,
		`OPENAI_API_KEY=\"abcdefgh12345678\"`,
		`curl -H "Authorization: Bearer $TOKEN" -H "x-api-key: abcdefgh12345678"`,
		"key := \"sk-" + "proj-" + strings.Repeat("A", 48) + "\"",
		"",
	}, "\n")
	repository := newRepository(t)
	writeFile(t, repository, "internal/shapes_test.go", content)
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "secret-shaped text")
	head := gitLine(t, repository, "rev-parse", "HEAD")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	file, err := manager.FileAtCommit(context.Background(), head, "internal/shapes_test.go", 1<<20)
	if err != nil {
		t.Fatalf("FileAtCommit() error = %v", err)
	}
	if string(file.Content) != content {
		t.Fatalf("FileAtCommit() = %q, want it byte for byte: %q", file.Content, content)
	}
}
