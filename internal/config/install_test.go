package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const npmInstall = `
install:
  - name: npm packages
    run: npm ci --cache "$YOYODYNE_INSTALL_CACHE" --prefer-offline
    inputs: [package.json, package-lock.json]
    outputs: [node_modules]
`

// installProject is a Git repository holding a project configuration with the
// given install section, the two npm files tracked, and node_modules ignored.
func installProject(t *testing.T, install string) string {
	t.Helper()
	project := t.TempDir()
	writeProject(t, project, minimalProjectConfig+install, nil)
	for name, body := range map[string]string{
		"package.json":      "{}\n",
		"package-lock.json": "{}\n",
		".gitignore":        "node_modules/\n",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	installGitIn(t, project, "init", "--quiet")
	installGitIn(t, project, "add", "package.json", "package-lock.json", ".gitignore")
	installGitIn(t, project, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "start")
	return project
}

func installGitIn(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func loadInstallProject(t *testing.T, project string) (Resolved, error) {
	t.Helper()
	return LoadResolved(filepath.Join(project, DirectoryName, FileName))
}

// A project that declares no install step, or an empty list, loads exactly as
// it did before steps existed, and is never asked about Git: the project here
// is not a repository at all.
func TestNoInstallStepLoadsAsBefore(t *testing.T) {
	t.Parallel()

	before := loadProject(t, minimalProjectConfig, nil)
	empty := loadProject(t, minimalProjectConfig+"install: []\n", nil)
	if len(before.Config.Install) != 0 || len(empty.Config.Install) != 0 {
		t.Fatalf("install = %#v and %#v, want none", before.Config.Install, empty.Config.Install)
	}
	before.Path, empty.Path = "", ""
	delete(empty.Origins, "install")
	before.Config.Product.Repository, empty.Config.Product.Repository = "", ""
	if !reflect.DeepEqual(before.Config, empty.Config) {
		t.Fatalf("an empty install list changed the effective configuration")
	}
}

// A declared step is read whole, and its time limit is ten minutes where it
// names none.
func TestAnInstallStepIsReadWithItsDefaultTimeLimit(t *testing.T) {
	t.Parallel()

	resolved, err := loadInstallProject(t, installProject(t, npmInstall))
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if len(resolved.Config.Install) != 1 {
		t.Fatalf("install = %#v, want one step", resolved.Config.Install)
	}
	step := resolved.Config.Install[0]
	if step.Name != "npm packages" || !strings.HasPrefix(step.Run, "npm ci") || len(step.Inputs) != 2 || len(step.Outputs) != 1 {
		t.Fatalf("step = %#v, want the npm step as written", step)
	}
	if step.Limit() != 10*time.Minute || DefaultInstallTimeLimit != 10*time.Minute {
		t.Fatalf("Limit() = %s, want 10m", step.Limit())
	}
	written, err := loadInstallProject(t, installProject(t, npmInstall+"    time_limit: 3m\n"))
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if limit := written.Config.Install[0].Limit(); limit != 3*time.Minute {
		t.Fatalf("Limit() = %s, want the 3m written", limit)
	}
}

// Each rule a step is held to refuses the configuration when it is read, naming
// the path that breaks it.
func TestAnInstallStepIsRefusedWhenReadNamingThePath(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		install string
		arrange func(t *testing.T, project string)
		want    []string
	}{
		{
			name:    "an input Git does not track",
			install: strings.Replace(npmInstall, "package-lock.json]", "yarn.lock]", 1),
			want:    []string{"input yarn.lock is not tracked by Git"},
		},
		{
			name:    "an input Git does not track though the file exists",
			install: strings.Replace(npmInstall, "package-lock.json]", "notes.txt]", 1),
			arrange: func(t *testing.T, project string) {
				if err := os.WriteFile(filepath.Join(project, "notes.txt"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"input notes.txt is not tracked by Git"},
		},
		{
			name:    "an output Git does not ignore",
			install: strings.Replace(npmInstall, "[node_modules]", "[vendor]", 1),
			want:    []string{"output vendor is not ignored by Git"},
		},
		{
			name:    "an output holding a tracked file",
			install: npmInstall,
			arrange: func(t *testing.T, project string) {
				if err := os.MkdirAll(filepath.Join(project, "node_modules"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(project, "node_modules", "kept.js"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				installGitIn(t, project, "add", "--force", "node_modules/kept.js")
			},
			want: []string{"output node_modules holds node_modules/kept.js, which Git tracks"},
		},
		{
			name:    "an output outside the root",
			install: strings.Replace(npmInstall, "[node_modules]", "[../node_modules]", 1),
			want:    []string{`output "../node_modules" lies outside the repository root`},
		},
		{
			name:    "an absolute output",
			install: strings.Replace(npmInstall, "[node_modules]", "[/var/node_modules]", 1),
			want:    []string{`output "/var/node_modules" must be relative to the repository root`},
		},
		{
			name:    "an output linked outside the root",
			install: npmInstall,
			arrange: func(t *testing.T, project string) {
				if err := os.Symlink(t.TempDir(), filepath.Join(project, "node_modules")); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"output node_modules resolves to", "outside the repository root"},
		},
		{
			name: "two steps naming the same output",
			install: npmInstall + `  - name: again
    run: npm install
    inputs: [package.json]
    outputs: [./node_modules]
`,
			want: []string{`install step "again": output "./node_modules" is also an output of install step 0`},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			project := installProject(t, test.install)
			if test.arrange != nil {
				test.arrange(t, project)
			}
			_, err := loadInstallProject(t, project)
			if err == nil {
				t.Fatalf("LoadResolved() succeeded, want %q refused", test.want)
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("LoadResolved() error = %v, want it to say %q", err, want)
				}
			}
		})
	}
}

// What a step has to say about itself is refused without a repository, so a
// configuration read from a stream is held to it too.
func TestAnInstallStepSaysWhatItRunsAndWhatItReadsAndMakes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		step InstallStep
		want string
	}{
		{step: InstallStep{Run: "npm ci", Inputs: []string{"package.json"}, Outputs: []string{"node_modules"}}, want: "install step 0: name cannot be empty"},
		{step: InstallStep{Name: "npm", Inputs: []string{"package.json"}, Outputs: []string{"node_modules"}}, want: `install step "npm": run cannot be empty`},
		{step: InstallStep{Name: "npm", Run: "npm ci", Outputs: []string{"node_modules"}}, want: "inputs must name"},
		{step: InstallStep{Name: "npm", Run: "npm ci", Inputs: []string{"package.json"}}, want: "outputs must name"},
		{step: InstallStep{Name: "npm", Run: "npm ci", Inputs: []string{"../package.json"}, Outputs: []string{"node_modules"}}, want: `input "../package.json" lies outside the repository root`},
		{step: InstallStep{Name: "npm", Run: "npm ci", Inputs: []string{"package.json"}, Outputs: []string{"."}}, want: `output "." lies outside the repository root`},
		{step: InstallStep{Name: "npm", Run: "npm ci", Inputs: []string{"package.json"}, Outputs: []string{"node_modules"}, TimeLimit: Duration(-time.Minute)}, want: "time_limit cannot be negative"},
	} {
		problems := installProblems([]InstallStep{test.step})
		if len(problems) != 1 || !strings.Contains(problems[0], test.want) {
			t.Errorf("installProblems(%#v) = %q, want one saying %q", test.step, problems, test.want)
		}
	}
	twice := InstallStep{Name: "npm", Run: "npm ci", Inputs: []string{"package.json"}, Outputs: []string{"node_modules"}}
	other := twice
	other.Outputs = []string{"vendor"}
	if problems := installProblems([]InstallStep{twice, other}); len(problems) != 1 || !strings.Contains(problems[0], "has the same name") {
		t.Errorf("installProblems(two named npm) = %q, want the name refused", problems)
	}
}

// A heavy check may name an install step's command, so a step the project says
// is heavy waits for a place as a heavy check does.
func TestAHeavyCheckMayNameAnInstallStep(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Checks:  []string{"npm test"},
		Install: []InstallStep{{Name: "npm packages", Run: "npm ci --prefer-offline", Inputs: []string{"package.json"}, Outputs: []string{"node_modules"}}},
		Execution: Execution{
			HeavyChecks: map[string]int{"npm ci": 1},
		},
	}
	if problems := heavyCheckProblems(cfg); len(problems) != 0 {
		t.Fatalf("heavyCheckProblems() = %q, want the install command to count", problems)
	}
}

// An install step is the operator's to write: a role's write to it is refused
// naming the key and saying why, while the values section 6 of portable agent
// configuration gives a role stay applicable.
func TestARoleMayNotApplyAnInstallStep(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"install", "install.[].run", "install.0.inputs"} {
		err := RoleMayApply(key)
		if err == nil {
			t.Fatalf("RoleMayApply(%q) = nil, want it refused", key)
		}
		for _, want := range []string{key + " is refused for a role's write", "stays the operator's", "network access"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("RoleMayApply(%q) = %v, want it to say %q", key, err, want)
			}
		}
	}
	for _, key := range []string{"agents.developer.model", "approvals.integration", "product", "accounts"} {
		if err := RoleMayApply(key); err == nil || !strings.Contains(err.Error(), "stays the operator's") {
			t.Errorf("RoleMayApply(%q) = %v, want it refused as the operator's", key, err)
		}
	}
	for _, key := range []string{"checks", "landing_checks.[]", "execution.developer_models", "recurring_tasks.sweep.every", "execution.check_timeout"} {
		if err := RoleMayApply(key); err != nil {
			t.Errorf("RoleMayApply(%q) = %v, want it applicable", key, err)
		}
	}
}
