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

// profileArgument is the permission profile an invocation selects ahead of
// `resume`, and fails the test where it selects none.
func profileArgument(t *testing.T, args []string) string {
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
	profile := profileArgument(t, args)
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
			network := "permissions." + want + ".network.enabled=false"
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

// A path that is quoted, spaced, or repeated is still one well-formed key: a
// malformed or repeated key is a TOML error the CLI refuses the whole
// invocation for.
func TestProfileArgsQuoteEveryPathAndGiveEachOnce(t *testing.T) {
	t.Parallel()
	odd := `/a "quoted" path\with a backslash`
	args := append([]string{"exec"}, profileArgs(profileDeveloper, []string{odd, "/plain", odd})...)
	if got, want := profileFilesystem(t, args), developerFilesystem(odd, "/plain"); !reflect.DeepEqual(got, want) {
		t.Fatalf("file system = %q, want %q", got, want)
	}
}

// profileEntry is one file system entry in the `<permission_profile>` the CLI
// renders into the model's context, which is the CLI's own account of the
// profile it resolved.
var profileEntry = regexp.MustCompile(`<entry access="([a-z]+)"><(path|special)>([^<]*)</(?:path|special)></entry>`)

// The installed CLI is asked, without a provider call, what profile the
// adapter's own configuration resolves to, and it has to be exactly the one
// the adapter declared, with the network restricted. `codex debug
// prompt-input` renders the context a session would open with, and that
// includes the resolved profile, so a key the CLI does not read or a profile it
// would not select shows up here rather than in a run. It is gated on the CLI
// being installed, as the command contract check is, and skips where it is not.
func TestTheInstalledCLIResolvesEachProfileAsDeclared(t *testing.T) {
	t.Parallel()

	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skipf("Codex is not installed, so what it resolves cannot be asked here: %v", err)
	}
	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			repository, worktree := sandboxRepositoryNamed(t, true, "repository with spaces")
			command := sandboxCommand(t, repository, worktree, "", role)
			declared := profileFilesystem(t, command.Args)
			// prompt-input takes configuration and features, and nothing that
			// would reach a provider.
			var args []string
			directory := ""
			for index := 1; index < len(command.Args); index++ {
				switch command.Args[index] {
				case "--config", "--enable", "--disable":
					args = append(args, command.Args[index], command.Args[index+1])
					index++
				case "--cd":
					directory = command.Args[index+1]
					index++
				case "--model":
					index++
				}
			}
			if directory == "" {
				t.Fatalf("no --cd in %q", command.Args)
			}
			if role != domain.RoleDeveloper {
				// The read-only launch directory was removed when Run returned.
				directory = t.TempDir()
			}
			probe := exec.Command(binary, append([]string{"debug", "prompt-input"}, args...)...)
			probe.Dir = directory
			probe.Env = append(os.Environ(), ProviderHomeVariable+"="+t.TempDir())
			output, err := probe.Output()
			if err != nil {
				t.Fatalf("codex debug prompt-input with the %s's configuration: %v\n%s", role, err, output)
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
			if !strings.Contains(rendered, "Network access is restricted") {
				t.Errorf("the CLI did not restrict the %s's network:\n%s", role, rendered)
			}
			resolved := make(map[string]string)
			for _, match := range profileEntry.FindAllStringSubmatch(rendered, -1) {
				resolved[canonicalPath(html.UnescapeString(match[3]))] = match[1]
			}
			want := make(map[string]string)
			for path, access := range declared {
				want[canonicalPath(path)] = access
			}
			if !reflect.DeepEqual(resolved, want) {
				t.Fatalf("the CLI resolved the %s's profile to %q, want what the adapter declared, %q", role, resolved, want)
			}
		})
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
