package config

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// newerSettingsRepository is a repository with two commits, the configuration
// at path committed in both, and the second adding a key no build knows — the
// shape of a landing that adds a setting and sets it at once. It returns the
// first commit, standing for a build made before the landing, and the second,
// standing for the build made from it.
func newerSettingsRepository(t *testing.T, before, after string, edits ...func(string) string) (path, older, current string) {
	t.Helper()
	directory := bindingTestRepository(t)
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", directory, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
		return strings.TrimSpace(string(output))
	}
	path = writeScaffoldAt(t, directory, "newer", directory)
	commit := func(content, message string) string {
		t.Helper()
		if content != "" {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git("add", "-A")
		git("commit", "-q", "-m", message)
		return git("rev-parse", "HEAD")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	older = commit(strings.Replace(string(original), "\n  dashboard:\n", "\n  dashboard:\n"+before, 1), "the build before the setting")
	landed := strings.Replace(string(original), "\n  dashboard:\n", "\n  dashboard:\n"+after, 1)
	for _, edit := range edits {
		landed = edit(landed)
	}
	current = commit(landed, "the landing that adds and sets it")
	return path, older, current
}

const newerSettingKey = "    a_setting_from_a_later_build: true\n"

// setting rewrites one setting the scaffold writes.
func setting(from, to string) func(string) string {
	return func(content string) string { return strings.Replace(content, from, to, 1) }
}

func decodeFileAs(t *testing.T, path, build string) (configDocument, string, error) {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	document, err := decodeDocumentAt(bytes.NewReader(source), path, olderThanRepositoryAs(path, build), &log)
	return document, log.String(), err
}

func TestAnOlderBuildReadsAConfigurationCarryingASettingAddedAfterIt(t *testing.T) {
	t.Parallel()
	path, older, _ := newerSettingsRepository(t, "", newerSettingKey)

	document, said, err := decodeFileAs(t, path, older)
	if err != nil {
		t.Fatalf("a build older than the repository refused the file: %v", err)
	}
	if document.Version == nil {
		t.Fatal("the rest of the file was not read")
	}
	if !strings.Contains(said, "services.dashboard.a_setting_from_a_later_build") || !strings.Contains(said, shortCommit(older)) {
		t.Fatalf("the log does not name the setting and the build: %q", said)
	}

	// The same setting is named once for the life of the process, however
	// often the part reads the file again.
	if _, again, err := decodeFileAs(t, path, older); err != nil || again != "" {
		t.Fatalf("a second read said %q, %v; want nothing said and no error", again, err)
	}
}

func TestAnOlderBuildStillHonoursTheSettingsItKnows(t *testing.T) {
	t.Parallel()
	path, older, _ := newerSettingsRepository(t, "",
		newerSettingKey, setting("hide_summary: false", "hide_summary: true"))

	document, _, err := decodeFileAs(t, path, older)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if document.Services == nil || document.Services.Dashboard == nil || document.Services.Dashboard.HideSummary == nil || !*document.Services.Dashboard.HideSummary {
		t.Fatal("a known setting beside the unknown one was dropped")
	}
}

func TestTheCurrentBuildRefusesAMisspelledSetting(t *testing.T) {
	t.Parallel()
	path, _, current := newerSettingsRepository(t, "", "", setting("    hide_summary: ", "    hide_sumary: "))

	_, said, err := decodeFileAs(t, path, current)
	if err == nil || !strings.Contains(err.Error(), "field hide_sumary not found") {
		t.Fatalf("the build the repository stands at accepted a misspelled setting: %v", err)
	}
	if said != "" {
		t.Fatalf("a refused file still logged %q", said)
	}
}

func TestABuildThatCannotPlaceItselfRefusesAnUnknownSetting(t *testing.T) {
	t.Parallel()
	path, _, _ := newerSettingsRepository(t, "", newerSettingKey)

	for name, build := range map[string]string{
		"no commit recorded":            "",
		"a description, not a commit":   "a1371f2-dirty",
		"a commit the repository lacks": strings.Repeat("ab", 20),
	} {
		if _, _, err := decodeFileAs(t, path, build); err == nil {
			t.Errorf("%s: an unknown setting was accepted", name)
		}
	}
}

func TestLoadResolvedRefusesAMisspelledSettingWhereTheBuildCannotSayItIsOlder(t *testing.T) {
	t.Parallel()
	// A test binary records no commit, so this is the current build's answer.
	path, _, _ := newerSettingsRepository(t, "", "", setting("    hide_summary: ", "    hide_sumary: "))
	if _, err := LoadResolved(path); err == nil || !strings.Contains(err.Error(), "hide_sumary") {
		t.Fatalf("LoadResolved accepted a misspelled setting: %v", err)
	}
}

func TestAnOlderBuildStillRefusesWhatIsWrongBesidesAnUnknownSetting(t *testing.T) {
	t.Parallel()
	path, older, _ := newerSettingsRepository(t, "",
		newerSettingKey, setting("port: 8765", "port: not-a-number"))
	if _, _, err := decodeFileAs(t, path, older); err == nil {
		t.Fatal("a value of the wrong kind was accepted once the unknown setting was left out")
	}
}

func TestPruneUnknownKeysLeavesASecondDocumentForTheRefusal(t *testing.T) {
	t.Parallel()
	source := []byte("version: 1\nnot_a_setting: 1\n---\nversion: 1\n")
	if pruned, leftOut, _ := withoutNewerSettings(source, func() (string, bool) { return "build", true }); pruned != nil || leftOut != nil {
		t.Fatalf("a source with two documents was pruned to %q, leaving out %v", pruned, leftOut)
	}
}
