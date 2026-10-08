package codex

import (
	"encoding/json"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// configValues is every `--config` value an invocation gives to the command
// level it names, in order: "exec" for the options ahead of `resume`, and
// "exec resume" for the ones after it.
func configValues(args []string, level string) []string {
	var values []string
	current := "exec"
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "resume":
			current = "exec resume"
		case "--config":
			if index+1 < len(args) && current == level {
				values = append(values, args[index+1])
			}
			index++
		}
	}
	return values
}

// randomProfileEnding is what profileName adds to a profile's own name.
var randomProfileEnding = regexp.MustCompile(`-[0-9a-f]{32}$`)

// profileArgument is the permission profile an invocation selects ahead of
// `resume`, by the profile's own name with the random ending taken off, and
// fails the test where it selects none or the ending is missing.
func profileArgument(t *testing.T, args []string) string {
	t.Helper()
	name := profileNameArgument(t, args)
	if !randomProfileEnding.MatchString(name) {
		t.Fatalf("profile %q has no random ending, so a configuration file could name it: %#v", name, args)
	}
	return randomProfileEnding.ReplaceAllString(name, "")
}

// withProfileNamesFixed is args with every profile's random ending taken off,
// so a test can compare a whole command line.
func withProfileNamesFixed(args []string) []string {
	fixed := make([]string, len(args))
	ending := regexp.MustCompile(`(yoyodyne-(?:developer|read-only))-[0-9a-f]{32}`)
	for index, arg := range args {
		fixed[index] = ending.ReplaceAllString(arg, "$1")
	}
	return fixed
}

// profileNameArgument is the name, random ending included, of the permission
// profile an invocation selects ahead of `resume`, and fails the test where it
// selects none.
func profileNameArgument(t *testing.T, args []string) string {
	t.Helper()
	for _, value := range configValues(args, "exec") {
		if selected, found := strings.CutPrefix(value, "default_permissions="); found {
			var profile string
			if err := json.Unmarshal([]byte(selected), &profile); err != nil {
				t.Fatalf("default_permissions=%s is not a quoted name: %v", selected, err)
			}
			return profile
		}
	}
	t.Fatalf("no permission profile in %#v", args)
	return ""
}

// profileFilesystem is the file system table an invocation defines for the
// profile it selects, read back from the TOML inline table the CLI is given.
func profileFilesystem(t *testing.T, args []string) map[string]string {
	t.Helper()
	profile := profileNameArgument(t, args)
	for _, value := range configValues(args, "exec") {
		if table, found := strings.CutPrefix(value, "permissions."+profile+".filesystem="); found {
			return parseInlineTable(t, table)
		}
	}
	t.Fatalf("profile %q is selected but its file system is never defined in %#v", profile, args)
	return nil
}

// parseInlineTable reads the one shape profileArgs writes: an inline table of
// quoted keys and quoted values. It refuses anything else and a key given
// twice, which the CLI would refuse too.
func parseInlineTable(t *testing.T, table string) map[string]string {
	t.Helper()
	if !strings.HasPrefix(table, "{") || !strings.HasSuffix(table, "}") {
		t.Fatalf("%q is not an inline table", table)
	}
	rest := table[1 : len(table)-1]
	quoted := func() string {
		if !strings.HasPrefix(rest, `"`) {
			t.Fatalf("expected a quoted string at %q in %q", rest, table)
		}
		end := 1
		for ; end < len(rest) && rest[end] != '"'; end++ {
			if rest[end] == '\\' {
				end++
			}
		}
		var value string
		if end >= len(rest) || json.Unmarshal([]byte(rest[:end+1]), &value) != nil {
			t.Fatalf("unterminated or malformed string at %q in %q", rest, table)
		}
		rest = rest[end+1:]
		return value
	}
	entries := make(map[string]string)
	for rest != "" {
		key := quoted()
		if !strings.HasPrefix(rest, "=") {
			t.Fatalf("expected = after %q in %q", key, table)
		}
		rest = rest[1:]
		if _, twice := entries[key]; twice {
			t.Fatalf("key %q is given twice in %q", key, table)
		}
		entries[key] = quoted()
		rest = strings.TrimPrefix(rest, ",")
	}
	return entries
}

// developerFilesystem is what the developer's profile has to say for these
// writable directories: the whole machine readable, the temporary directories
// and each directory writable, and inside each the names the CLI's old
// `workspace-write` sandbox kept read-only.
func developerFilesystem(writable ...string) map[string]string {
	want := map[string]string{":root": "read", ":slash_tmp": "write", ":tmpdir": "write"}
	for _, root := range writable {
		want[root] = "write"
		for _, name := range protectedNames {
			want[filepath.Join(root, name)] = "read"
		}
	}
	return want
}

// Every role runs under the permission profile its posture names, on a fresh
// launch and on a resume alike, and never under `--sandbox`: codex-cli 0.160.0
// lets that option replace a selected profile without saying so, so an
// invocation carrying both would run under the option.
func TestEveryInvocationSelectsItsProfileAheadOfResumeAndNoSandboxMode(t *testing.T) {
	t.Parallel()
	for _, role := range domain.Roles() {
		for _, session := range []string{"", "session-one"} {
			repository, worktree := sandboxRepository(t, true)
			command := sandboxCommand(t, repository, worktree, session, role)
			want := profileReadOnly
			if role == domain.RoleDeveloper {
				want = profileDeveloper
			}
			if got := profileArgument(t, command.Args); got != want {
				t.Errorf("%s (session %q) runs under profile %q, want %q", role, session, got, want)
			}
			network := "permissions." + profileNameArgument(t, command.Args) + ".network.enabled=false"
			if !reflect.DeepEqual(configValues(command.Args, "exec resume"), []string(nil)) {
				t.Errorf("%s (session %q) gives configuration after resume: %q", role, session, command.Args)
			}
			found := false
			for _, value := range configValues(command.Args, "exec") {
				found = found || value == network
			}
			if !found {
				t.Errorf("%s (session %q) does not switch the profile's network off: %q", role, session, command.Args)
			}
			for _, forbidden := range []string{"--sandbox", "-s", "--add-dir", "--dangerously-bypass-approvals-and-sandbox", "sandbox_mode", "sandbox_workspace_write", ":danger-full-access", ":workspace"} {
				for _, arg := range command.Args {
					if arg == forbidden || strings.Contains(arg, forbidden+"=") || strings.Contains(arg, `"`+forbidden+`"`) {
						t.Errorf("%s (session %q) passes %q, which would replace or widen its profile: %q", role, session, forbidden, command.Args)
					}
				}
			}
		}
	}
}

// A read-only role's profile reads and writes nothing: no directory, not even
// the temporary ones, on a fresh launch or a resume of a session a developer
// saved.
func TestTheReadOnlyProfileWritesNothing(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	for _, role := range domain.Roles() {
		if backendapi.PostureFor(role) != backendapi.PostureReadOnly {
			continue
		}
		for _, session := range []string{"", "session-one"} {
			command := sandboxCommand(t, repository, worktree, session, role)
			if got := profileFilesystem(t, command.Args); !reflect.DeepEqual(got, map[string]string{":root": "read"}) {
				t.Fatalf("read-only role %s (session %q) file system = %q, want reading only", role, session, got)
			}
		}
	}
}

// Every invocation names its profile afresh, so a name seen in one run's
// command line is no use to a file written for the next.
func TestEachInvocationNamesItsProfileAfresh(t *testing.T) {
	t.Parallel()
	repository, worktree := sandboxRepository(t, true)
	first := profileNameArgument(t, sandboxCommand(t, repository, worktree, "", domain.RoleDeveloper).Args)
	second := profileNameArgument(t, sandboxCommand(t, repository, worktree, "session-one", domain.RoleDeveloper).Args)
	if first == second {
		t.Fatalf("two invocations ran under one profile name %q", first)
	}
}

// A path that is quoted, spaced, or repeated is still one well-formed key: a
// malformed or repeated key is a TOML error the CLI refuses the whole
// invocation for.
func TestProfileArgsQuoteEveryPathAndGiveEachOnce(t *testing.T) {
	t.Parallel()
	odd := `/a "quoted" path\with a backslash`
	args := append([]string{"exec"}, profileArgs(profileDeveloper, profileDeveloper+"-0123456789abcdef0123456789abcdef", []string{odd, "/plain", odd})...)
	if got, want := profileFilesystem(t, args), developerFilesystem(odd, "/plain"); !reflect.DeepEqual(got, want) {
		t.Fatalf("file system = %q, want %q", got, want)
	}
}

// profileEntry is one file system entry in the `<permission_profile>` the CLI
// renders into the model's context, which is the CLI's own account of the
// profile it resolved.
var profileEntry = regexp.MustCompile(`<entry access="([a-z]+)"><(path|special)>([^<]*)</(?:path|special)></entry>`)

// hostileConfig is a config.toml that tries every way codex-cli 0.160.0 was
// found to read to widen what a role may do: an unrestricted sandbox mode, an
// unrestricted profile selected by default, the worktree trusted so its own
// .codex/config.toml is read too, and a table under each profile's own name
// adding a writable directory outside the worktree and turning the network on.
func hostileConfig(outside string, trusted ...string) string {
	var config strings.Builder
	config.WriteString("sandbox_mode = \"danger-full-access\"\n")
	config.WriteString("default_permissions = \":danger-full-access\"\n")
	for _, profile := range []string{profileDeveloper, profileReadOnly} {
		config.WriteString("[permissions." + profile + ".filesystem]\n")
		config.WriteString(tomlString(outside) + " = \"write\"\n")
		config.WriteString("[permissions." + profile + ".network]\n")
		config.WriteString("enabled = true\nallow_local_binding = true\n")
	}
	for _, directory := range trusted {
		config.WriteString("[projects." + tomlString(directory) + "]\ntrust_level = \"trusted\"\n")
	}
	return config.String()
}

// resolvedProfile asks the installed CLI, without a provider call, what profile
// args resolve to when run in directory under home: `codex debug prompt-input`
// renders the context a session would open with, and that includes the
// resolved profile and whether the network is restricted.
func resolvedProfile(t *testing.T, binary, home, directory string, args []string) (map[string]string, bool) {
	t.Helper()
	// prompt-input takes configuration and features, and nothing that would
	// reach a provider.
	var probeArgs []string
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--config", "--enable", "--disable":
			probeArgs = append(probeArgs, args[index], args[index+1])
			index++
		case "--cd", "--model":
			index++
		}
	}
	probe := exec.Command(binary, append([]string{"debug", "prompt-input"}, probeArgs...)...)
	probe.Dir = directory
	probe.Env = append(os.Environ(), ProviderHomeVariable+"="+home)
	output, err := probe.Output()
	if err != nil {
		t.Fatalf("codex debug prompt-input: %v\n%s", err, output)
	}
	var items []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(output, &items); err != nil {
		t.Fatalf("codex debug prompt-input wrote no prompt input list: %v\n%s", err, output)
	}
	var texts []string
	for _, item := range items {
		for _, content := range item.Content {
			texts = append(texts, content.Text)
		}
	}
	rendered := strings.Join(texts, "\n")
	resolved := make(map[string]string)
	for _, match := range profileEntry.FindAllStringSubmatch(rendered, -1) {
		resolved[canonicalPath(html.UnescapeString(match[3]))] = match[1]
	}
	return resolved, strings.Contains(rendered, "Network access is restricted")
}

// The installed CLI is asked, without a provider call, what profile the
// adapter's own configuration resolves to, and it has to be exactly the one
// the adapter declared, with the network restricted — with no configuration
// files, and with an account config.toml and a worktree .codex/config.toml
// that each try to widen it. A key the CLI does not read, a profile it would
// not select, or a file that widens it shows up here rather than in a run. The
// control asks the same with the random ending taken off the profile's name,
// which those files can name, and has to see them widen it: without that the
// check would pass on files the CLI never read. It is gated on the CLI being
// installed, as the command contract check is, and skips where it is not.
func TestTheInstalledCLIResolvesEachProfileAsDeclared(t *testing.T) {
	t.Parallel()

	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("Codex is not installed, so what it resolves cannot be asked here: %v", err)
	}
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer} {
		for _, hostile := range []bool{false, true} {
			name := string(role) + "/no configuration files"
			if hostile {
				name = string(role) + "/configuration files that try to widen it"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				repository, worktree := sandboxRepositoryNamed(t, true, "repository with spaces")
				command := sandboxCommand(t, repository, worktree, "", role)
				directory := worktree
				if role != domain.RoleDeveloper {
					// The read-only launch directory was removed when Run returned;
					// an empty directory of the same kind stands in for it.
					directory = t.TempDir()
				}
				home, outside := t.TempDir(), t.TempDir()
				if hostile {
					physical, err := filepath.EvalSymlinks(directory)
					if err != nil {
						t.Fatal(err)
					}
					config := hostileConfig(outside, directory, physical)
					if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Join(directory, ".codex"), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(directory, ".codex", "config.toml"), []byte(config), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				want := make(map[string]string)
				for path, access := range profileFilesystem(t, command.Args) {
					want[canonicalPath(path)] = access
				}
				resolved, restricted := resolvedProfile(t, binary, home, directory, command.Args)
				if !restricted {
					t.Errorf("the CLI did not restrict the %s's network", role)
				}
				if !reflect.DeepEqual(resolved, want) {
					t.Fatalf("the CLI resolved the %s's profile to %q, want what the adapter declared, %q", role, resolved, want)
				}
				if hostile {
					fixed, _ := resolvedProfile(t, binary, home, directory, withProfileNamesFixed(command.Args))
					if fixed[canonicalPath(outside)] != "write" {
						t.Fatalf("the configuration files did not widen a profile they could name (%q), so this check shows nothing about the CLI reading them", fixed)
					}
				}
			})
		}
	}
}

// canonicalPath resolves what of a path exists through its links, so a
// temporary directory named through /var and the same one named through
// /private/var compare equal. Special names are left as they are.
func canonicalPath(path string) string {
	if strings.HasPrefix(path, ":") {
		return path
	}
	for suffix := ""; ; {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Join(resolved, suffix)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return filepath.Join(path, suffix)
		}
		suffix = filepath.Join(filepath.Base(path), suffix)
		path = parent
	}
}
