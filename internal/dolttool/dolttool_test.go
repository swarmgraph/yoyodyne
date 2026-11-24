package dolttool_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/dolttool"
	"github.com/mason-bryant/yoyodyne/internal/dolttool/dolttooltest"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
)

func state(t *testing.T, root string) dolttool.Inspection {
	t.Helper()
	found, err := dolttool.Inspect(root)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	return found
}

func TestEveryReleasedPlatformHasAChecksum(t *testing.T) {
	t.Parallel()
	wellFormed := regexp.MustCompile(`^[0-9a-f]{64}$`)
	seen := map[string]string{}
	for _, platform := range [][2]string{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		name := platform[0] + "/" + platform[1]
		sum := dolttool.For(platform[0], platform[1]).Checksum
		if !wellFormed.MatchString(sum) {
			t.Errorf("%s has checksum %q, want 64 lowercase hexadecimal characters", name, sum)
		}
		if other, repeated := seen[sum]; repeated {
			t.Errorf("%s and %s have one checksum", name, other)
		}
		seen[sum] = name
	}
	if sum := dolttool.For("windows", "amd64").Checksum; sum != "" {
		t.Errorf("windows/amd64 has checksum %q, want none", sum)
	}
}

func TestAMatchingReleaseIsInstalledInTheToolsFolder(t *testing.T) {
	t.Parallel()
	// Neither the state directory nor the tools folder is there yet.
	root := filepath.Join(t.TempDir(), "state")
	if found := state(t, root); found.State != dolttool.Missing {
		t.Fatalf("Inspect() = %+v before the install, want missing", found)
	}
	installer, serving := dolttooltest.Installer("linux", "amd64", dolttooltest.Release(t, "linux", "amd64", "the program"))
	if err := installer.Install(context.Background(), root); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if want := "https://github.com/dolthub/dolt/releases/download/v2.7.6/dolt-linux-amd64.tar.gz"; len(serving.Requests) != 1 || serving.Requests[0] != want {
		t.Errorf("requested %q, want %q alone", serving.Requests, want)
	}
	content, err := os.ReadFile(filepath.Join(root, "tools", "dolt"))
	if err != nil || string(content) != "the program" {
		t.Fatalf("installed %q, %v", content, err)
	}
	if info, _ := os.Stat(dolttool.Path(root)); info.Mode().Perm() != 0o700 {
		t.Errorf("installed with mode %v, want 0700", info.Mode())
	}
	if found := state(t, root); found.State != dolttool.Installed || found.Version != "2.7.6" {
		t.Errorf("Inspect() = %+v, want 2.7.6 installed", found)
	}
}

func TestADownloadThatIsNotTheReleaseIsRefusedAndWritesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installer, serving := dolttooltest.Installer("darwin", "arm64", dolttooltest.Release(t, "darwin", "arm64", "dolt"))
	serving.Content = dolttooltest.Release(t, "darwin", "arm64", "another program")
	err := installer.Install(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Install() error = %v, want the checksum refused", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("a refused download left %v", entries)
	}

	// Nor does it disturb an install that is already there.
	dolttooltest.Install(t, root)
	if err := installer.Install(context.Background(), root); err == nil {
		t.Fatal("Install() accepted the wrong download")
	}
	if found := state(t, root); found.State != dolttool.Installed {
		t.Errorf("Inspect() = %+v after a refused download, want the earlier install kept", found)
	}
}

func TestWithoutAPinnedChecksumNothingIsDownloaded(t *testing.T) {
	t.Parallel()
	installer, serving := dolttooltest.Installer("darwin", "arm64", nil)
	installer.Checksum = ""
	if err := installer.Install(context.Background(), t.TempDir()); err == nil {
		t.Error("Install() installed an archive nothing pins")
	}
	if len(serving.Requests) != 0 {
		t.Errorf("requested %q", serving.Requests)
	}
}

func TestAnArchiveWithoutTheProgramIsRefused(t *testing.T) {
	t.Parallel()
	for name, content := range map[string][]byte{
		"another platform's": dolttooltest.Release(t, "linux", "amd64", "dolt"),
		"not an archive":     []byte("a page saying the file moved"),
	} {
		root := t.TempDir()
		installer, _ := dolttooltest.Installer("darwin", "arm64", content)
		if err := installer.Install(context.Background(), root); err == nil {
			t.Errorf("%s was installed", name)
		}
		if found := state(t, root); found.State != dolttool.Missing {
			t.Errorf("%s: Inspect() = %+v, want missing", name, found)
		}
	}
}

func TestInspectTellsWhatIsNotTheInstalledVersion(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		change func(t *testing.T, tools string)
		want   dolttool.State
	}{
		"another version": {func(t *testing.T, tools string) {
			path := filepath.Join(tools, "dolt.json")
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			write(t, path, strings.Replace(string(content), "2.7.6", "2.7.5", 1), 0o600)
		}, dolttool.OtherVersion},
		"a replaced program": {func(t *testing.T, tools string) {
			write(t, filepath.Join(tools, "dolt"), "another program", 0o700)
		}, dolttool.Changed},
		"no record": {func(t *testing.T, tools string) {
			if err := os.Remove(filepath.Join(tools, "dolt.json")); err != nil {
				t.Fatal(err)
			}
		}, dolttool.Changed},
		"a link to a copy elsewhere": {func(t *testing.T, tools string) {
			other := filepath.Join(t.TempDir(), "dolt")
			if err := os.Rename(filepath.Join(tools, "dolt"), other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(tools, "dolt")); err != nil {
				t.Fatal(err)
			}
		}, dolttool.Changed},
	} {
		root := t.TempDir()
		dolttooltest.Install(t, root)
		test.change(t, filepath.Join(root, "tools"))
		if found := state(t, root); found.State != test.want {
			t.Errorf("%s: Inspect() = %+v, want %s", name, found, test.want)
		}
		dolttooltest.Install(t, root)
		if found := state(t, root); found.State != dolttool.Installed {
			t.Errorf("%s: installing again left %+v", name, found)
		}
	}
}

func TestTheInstallStaysInsideTheStateDirectory(t *testing.T) {
	t.Parallel()
	writertest.Run(t, writertest.Writer{
		Name:      "the dolt install",
		Directory: "tools",
		File:      "dolt",
		Write: func(t *testing.T, root string) error {
			installer, _ := dolttooltest.Installer("darwin", "arm64", dolttooltest.Release(t, "darwin", "arm64", "dolt"))
			return installer.Install(context.Background(), root)
		},
	})
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
