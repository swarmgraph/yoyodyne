package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ignoreCommandTimeout bounds the one command this asks. `git check-ignore`
// reads the ignore files and the index and reaches nothing else, so a
// repository that has not answered by now is not going to.
const ignoreCommandTimeout = 10 * time.Second

// IgnoredConfiguration is what the ignore rules in force say about the project's
// configuration.
//
// A project whose .yoyodyne is ignored is configured on the machine that ran
// `init` and nowhere else, and nothing fails to announce it: this checkout keeps
// reading the configuration off disk while every clone, every collaborator, and
// every dev worktree -- which check out tracked files only -- get a project with
// no configuration at all. That is drift discovered much later by someone who
// did not cause it, so it is said out loud whenever somebody is looking at the
// configuration on purpose: `yoyo init`, `yoyo setup`, `yoyo config validate`,
// and `yoyo doctor` all ask this one question here.
type IgnoredConfiguration struct {
	Ignored bool `json:"ignored"`
	// Path is the configuration Git was asked about, named as it was asked --
	// relative to the project -- so the report reads as the repository's own
	// path rather than as this machine's.
	Path string `json:"path,omitempty"`
	// Rule is what Git answered with, in its own `<file>:<line>:<pattern>` form.
	// It is carried whole rather than summarized because which file states the
	// rule is the difference between a decision every clone inherits and one
	// this checkout made for itself.
	Rule string `json:"rule,omitempty"`
	// Source is the ignore file the rule lives in, taken from the rule so a
	// caller does not have to parse it back out.
	Source string `json:"source,omitempty"`
}

// Shared reports whether the rule travels with the repository. Git names the
// file every rule came from: a `.gitignore` is committed and reaches every
// clone, while `.git/info/exclude` and a `core.excludesFile` are this checkout's
// and this machine's own. Git states the first two relative to where it was run
// and a global excludes file absolutely, so the path being absolute is what
// separates one somebody named `.gitignore` in their home directory from the
// repository's own.
func (i IgnoredConfiguration) Shared() bool {
	return !filepath.IsAbs(i.Source) && filepath.Base(i.Source) == ".gitignore"
}

// insideRepository names a configuration relative to the repository it
// describes, and reports false for one kept outside it.
func insideRepository(repository, configPath string) (string, bool) {
	if repository == "" {
		return "", false
	}
	named, err := filepath.Rel(repository, configPath)
	if err != nil || named == ".." || strings.HasPrefix(named, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(named), true
}

// ConfigurationIgnored asks Git whether the ignore rules in force exclude the
// project's configuration.
//
// Everything it cannot ask answers no -- a project that is not a repository, a
// configuration kept outside the repository it describes, a Git that would not
// run. This is an observation offered beside work that succeeded, and a warning
// invented from a question nobody could answer is worse than no warning at all.
//
// A configuration that is already tracked is not ignored however loudly a
// `.gitignore` names it: Git applies ignore rules to untracked paths only, and
// `check-ignore` consults the index for exactly that reason. A project that
// committed its configuration and later added the rule is therefore left alone,
// which is right -- what is committed is what the other machines get.
func ConfigurationIgnored(ctx context.Context, runner execution.ProcessRunner, repository, configPath string) IgnoredConfiguration {
	// A configuration outside the repository it describes is not something an
	// ignore rule can reach, and asking anyway would put the question to whatever
	// repository happens to contain the file instead: a home directory kept in
	// Git is common enough that the answer would be somebody else's rule about
	// somebody else's file.
	named, inside := insideRepository(repository, configPath)
	if !inside || runner == nil {
		return IgnoredConfiguration{}
	}
	result, err := runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    []string{"-C", repository, "check-ignore", "--verbose", "--", named},
		Env:     execution.GitEnvironment(nil),
		Timeout: ignoreCommandTimeout,
	}, nil)
	if err != nil || result.Status != execution.ProcessSucceeded {
		return IgnoredConfiguration{}
	}
	// `--verbose` answers `<file>:<line>:<pattern>\t<pathname>` for each ignored
	// path, and only one path was asked about. The tab is what separates the two,
	// so the answer is read before any whitespace is folded out of it.
	answer, _, _ := strings.Cut(strings.TrimSpace(result.Stdout), "\n")
	rule, _, separated := strings.Cut(answer, "\t")
	if !separated || rule == "" {
		return IgnoredConfiguration{}
	}
	source, _, _ := strings.Cut(rule, ":")
	return IgnoredConfiguration{Ignored: true, Path: named, Rule: rule, Source: source}
}

// DescribeIgnoredConfiguration says what the rule costs and what to do about it.
//
// A rule local to this checkout is acknowledged rather than argued with: a
// contributor who cannot put tool config in somebody else's repository is doing
// the supported thing, and telling them to commit it is telling them to do what
// they came here unable to do. Both forms name the same way out, because a
// configuration outside the repository is what makes a project runnable by
// something other than the checkout that was configured -- and `--external`
// rather than a hand-placed file, because a configuration in the place discovery
// looks for one is a configuration nothing has to be told about again.
func DescribeIgnoredConfiguration(ignored IgnoredConfiguration) string {
	if ignored.Shared() {
		return fmt.Sprintf("warning: %s is ignored by %s, so nothing commits it -- this checkout keeps working from disk while clones, "+
			"collaborators, and dev worktrees, which check out tracked files only, get an unconfigured project; commit %s if this "+
			"repository is yours to commit tool config to, and if it is not, keep the configuration outside the repository with "+
			"`yoyo init --external`, ignoring this one in .git/info/exclude rather than in a tracked .gitignore",
			ignored.Path, ignored.Rule, config.DirectoryName)
	}
	return fmt.Sprintf("warning: %s is ignored by %s, which is local to this checkout rather than committed -- the supported way to keep "+
		"tool config out of a repository that is not yours; it is still uncommitted, so clones, collaborators, and dev worktrees, which "+
		"check out tracked files only, get an unconfigured project, and anything but this checkout that has to run work here needs the "+
		"configuration kept outside the repository with `yoyo init --external`, which yoyo finds from this repository without --config",
		ignored.Path, ignored.Rule)
}

// commitIgnoredConfiguration is the command that puts an ignored configuration
// in the repository. It adds the whole configuration directory rather than the
// one file Git was asked about, because the personas beside the file are the
// rest of what a clone needs, and `--force` because adding an ignored path is
// exactly what Git otherwise refuses. Once committed, the rule no longer applies
// to it, so nothing has to be taken out of the .gitignore.
func commitIgnoredConfiguration(repository string, ignored IgnoredConfiguration) string {
	added := ignored.Path
	if strings.HasPrefix(added, config.DirectoryName+"/") {
		added = config.DirectoryName
	}
	return fmt.Sprintf("git -C %s add --force -- %s && git -C %s commit -m %s",
		shellQuote(repository), shellQuote(added), shellQuote(repository), shellQuote("Commit the Yoyodyne configuration"))
}

// checkConfigurationIgnored reports whether the configuration reaches anything
// but this checkout.
//
// An ignore rule the repository carries is a warning: work runs here, and the
// clones and worktrees that need the configuration silently do not get it, so
// the remedy commits it. A rule local to this checkout is the supported way to
// keep tool config out of somebody else's repository, so it is said and not
// warned about -- warning about a deliberate choice whose only fix is undoing it
// would be a finding nobody can clear.
func (d *diagnosis) checkConfigurationIgnored(ctx context.Context, repository, configPath string) Finding {
	if _, inside := insideRepository(repository, configPath); !inside {
		return Finding{
			Check:   "configuration-ignored",
			Status:  StatusOK,
			Summary: "the configuration is kept outside the repository, so no ignore rule reaches it",
			Detail:  configPath,
		}
	}
	ignored := ConfigurationIgnored(ctx, d.env.Runner, repository, configPath)
	switch {
	case !ignored.Ignored:
		return Finding{
			Check:   "configuration-ignored",
			Status:  StatusOK,
			Summary: "Git does not ignore the configuration, so it can be committed with the project",
			Detail:  configPath,
		}
	case ignored.Shared():
		return Finding{
			Check:   "configuration-ignored",
			Status:  StatusWarning,
			Summary: fmt.Sprintf("%s is ignored by the repository's own .gitignore, so clones and worktrees get no configuration", ignored.Path),
			Detail:  DescribeIgnoredConfiguration(ignored),
			Remedy:  commitIgnoredConfiguration(repository, ignored),
		}
	default:
		return Finding{
			Check:   "configuration-ignored",
			Status:  StatusOK,
			Summary: fmt.Sprintf("%s is kept out of commits by a rule local to this checkout, so only this checkout is configured", ignored.Path),
			Detail:  DescribeIgnoredConfiguration(ignored),
		}
	}
}
