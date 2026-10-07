package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
)

// A newcomer who reflexively ignores tool config gets no error and eventual
// drift: this checkout keeps working from disk while every clone and every dev
// worktree gets an unconfigured project. Both commands that look at the
// configuration on purpose say so, and neither turns it into a failure.
func TestRunInitWarnsWhenTheConfigurationIsIgnored(t *testing.T) {
	t.Parallel()

	project := gitProject(t)
	writeIgnoreFile(t, filepath.Join(project, ".gitignore"), ".yoyodyne\n")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	warning := stderr.String()
	for _, want := range []string{
		// What is ignored, and the rule that ignores it, so the operator can find
		// the line rather than hunt for it.
		config.DirectoryName + "/" + config.FileName,
		".gitignore:1:.yoyodyne",
		// What it costs, and the two supported ways out.
		"unconfigured project",
		"commit " + config.DirectoryName,
		"yoyo init --external",
		".git/info/exclude",
	} {
		if !strings.Contains(warning, want) {
			t.Errorf("stderr = %q, want it to mention %q", warning, want)
		}
	}
	// The files are written and valid, so this is something to know about an init
	// that worked rather than a reason to call it one that did not.
	if !strings.Contains(stdout.String(), "the configuration is complete") {
		t.Errorf("stdout = %q, want the ordinary report alongside the warning", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "--directory", project, "--force", "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	reported := reportedIgnore(t, stdout.Bytes())
	if !reported.Ignored || reported.Path != config.DirectoryName+"/"+config.FileName {
		t.Fatalf("ignored = %#v, want the configuration reported as ignored", reported)
	}
	if reported.Rule != ".gitignore:1:.yoyodyne" || reported.Source != ".gitignore" {
		t.Errorf("ignored = %#v, want Git's own rule and the file it is in", reported)
	}
}

// The legitimate case: a contributor who cannot commit tool config to somebody
// else's repository and has excluded it locally. That is the supported thing to
// do, so it is acknowledged rather than argued with -- and telling them to
// commit it, or to add the exclude they already have, would be telling them to
// do what they came here unable to do.
func TestALocalIgnoreIsAcknowledgedRatherThanArguedWith(t *testing.T) {
	t.Parallel()

	project := gitProject(t)
	writeIgnoreFile(t, filepath.Join(project, ".git", "info", "exclude"), ".yoyodyne/\n")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	warning := stderr.String()
	for _, want := range []string{".git/info/exclude:1:.yoyodyne/", "local to this checkout", "yoyo init --external"} {
		if !strings.Contains(warning, want) {
			t.Errorf("stderr = %q, want it to mention %q", warning, want)
		}
	}
	if strings.Contains(warning, "commit "+config.DirectoryName) {
		t.Errorf("stderr = %q, want a local exclude not told to commit what it cannot", warning)
	}
}

// Nothing is said about a configuration the repository would carry, and nothing
// is said about one no ignore rule reaches -- including a configuration that is
// tracked despite a rule naming it, because Git applies ignore rules to
// untracked paths only and what is committed is what other machines get.
func TestNothingIsSaidWhenTheConfigurationIsCommittable(t *testing.T) {
	// Not parallel: it asserts what this command says, and clearing the
	// provider keys is what keeps a key exported on the machine running the
	// tests from being one more thing said.
	withoutProviderKeys(t)

	project := gitProject(t)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if reported := reportedIgnore(t, stdout.Bytes()); reported.Ignored {
		t.Fatalf("ignored = %#v, want nothing reported for a repository with no rule", reported)
	}
	if strings.Contains(stderr.String(), "ignored") {
		t.Errorf("stderr = %q, want no warning for a repository with no rule", stderr.String())
	}

	// Tracked first, ignored afterwards: the rule is moot and saying otherwise
	// would be a warning on every healthy project that keeps one.
	git(t, project, "add", "-f", config.DirectoryName)
	writeIgnoreFile(t, filepath.Join(project, ".gitignore"), ".yoyodyne\n")
	path := filepath.Join(project, config.DirectoryName, config.FileName)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want nothing said about a tracked configuration", stderr.String())
	}

	// A project that is not a repository at all cannot be asked, and a warning
	// invented from a question nobody could answer is worse than no warning.
	outside := writeProjectConfig(t, portableConfig)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", outside}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want nothing said where Git could not be asked", stderr.String())
	}
}

// `config validate` is the command an operator runs to be told whether the
// configuration is right, and a configuration that reaches no other machine is
// something they want told there. It stays a warning: the file is valid, so the
// exit code is what it would have been.
func TestConfigValidateWarnsWhenTheConfigurationIsIgnored(t *testing.T) {
	t.Parallel()

	project := gitProject(t)
	writeIgnoreFile(t, filepath.Join(project, ".gitignore"), "# tool config\n.yoyodyne/\n")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	path := filepath.Join(project, config.DirectoryName, config.FileName)

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "configuration valid") {
		t.Errorf("stdout = %q, want the validity answer unchanged", stdout.String())
	}
	if !strings.Contains(stderr.String(), ".gitignore:2:.yoyodyne/") {
		t.Errorf("stderr = %q, want the rule that ignores the configuration", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", path, "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Status  string                      `json:"status"`
		Ignored doctor.IgnoredConfiguration `json:"ignored"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Status != "valid" || !result.Ignored.Ignored || result.Ignored.Rule != ".gitignore:2:.yoyodyne/" {
		t.Fatalf("result = %+v, want a valid configuration reported as ignored", result)
	}
}

// gitProject is a repository with no remote, so the tracker step reports a skip
// without invoking bd and these tests stay about the ignore rules.
func gitProject(t *testing.T) string {
	t.Helper()

	project := filepath.Join(t.TempDir(), "example-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	git(t, project, "init", "-b", "main")
	return project
}

func writeIgnoreFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func reportedIgnore(t *testing.T, payload []byte) doctor.IgnoredConfiguration {
	t.Helper()

	var result struct {
		Ignored doctor.IgnoredConfiguration `json:"ignored"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return result.Ignored
}
