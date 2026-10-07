# yoyo

Writing code was never the hard part. The work around it is: writing down the
ideas, turning them into designs, breaking them up, assigning, reviewing,
testing, and checking the result against what was intended. Every coding agent
still leaves that to you.

Yoyo does that work without you driving each turn: goals in, merged software
out, a conversation to steer it. AI agents in fixed roles — a Lead Product
Manager, an architect, a development manager, developers, reviewers — do the
work. The **harness**, the `yoyo` program, starts every agent, gives each only
its role's authority, runs your checks, and decides what may merge.

You write down what the product is for — a brief and goals you approve once and
amend when your intent changes. After that, work is developed, checked,
reviewed, and merged without anybody approving each change. What reaches you is
what only you can decide: work items to approve (unless you hand that to your
goals), questions about intent, stopped work the development manager escalates,
and the pause and intake hold only you lift.

Three gates hold that up, and the harness enforces each rather than trusting an
agent's good behavior:

- **Nothing merges unreviewed.** Integration requires passing checks, an
  approving verdict, and two separate provider invocations, so no change is
  judged by the agent that wrote it.
- **The reviewer cannot merge, and cannot be talked into one.** It runs with no
  tools at all, everything it is shown is evidence rather than instruction, and
  a persona can change how a role works but never give it authority it does not
  have.
- **The written goals are the only authority work traces to.** Every work item
  names the goal it serves, in your goals document's words. The brief and the
  goals stay yours: a role that disagrees with one proposes a change rather than
  making it.

**You drive it from one conversation.** In `yoyo chat` the Lead Product
Manager, having read your written intent and the tracked work, proposes work
items; you approve as many as you like and say `/work <id>` to run one. The run
happens in the background: an isolated worktree, your checks, an independent
reviewer whose findings go back to the developer to repair, and a merge into
your target branch — or a pull request, [if you ask for
one](#optional-publishing-and-auto-merge). The other commands (`yoyo help`
lists them) are for administration and recovery.

**Quick start.** One script installs `yoyo`, tells you where it put it, and
checks the two things it needs — [Beads](https://github.com/gastownhall/beads)
(`bd`) and [Claude Code](https://code.claude.com/docs):

```sh
curl -fsSL https://raw.githubusercontent.com/swarmgraph/yoyodyne/main/scripts/install.sh | bash
yoyo version  # not found? the script printed the PATH line to add
cd path/to/your/project
yoyo setup    # asks before each step, and ends with `yoyo doctor`
yoyo chat
```

`yoyo setup` walks the configuration steps as questions. [Getting
started](#getting-started) is the same path typed by hand, with what each step
is for; [Install](#install) has what the script does and the other routes.

**What is bounded today**, worth knowing before you start:

- **One run at a time by default.** `/work <id>` runs one item. `yoyo work` lets
  the harness pull ready items from the top of the backlog itself, up to
  `execution.max_concurrent_developers` at once (default 1). The Lead Product
  Manager owns the backlog and its order.
- **Some work happens without you asking.** When a run fails review after
  every repair attempt, the development manager decides whether to repair,
  re-run, or escalate it, and `yoyo work` carries that out. A project can also
  configure [recurring tasks](docs/configuration.md#recurring-tasks) that wake a
  role on a schedule, and [program managers](docs/designs/program-manager.md)
  that each watch one area of work. `yoyo agent chat <name>` talks to any agent.
- **Claude Code and Codex can each run every role.** Roles other than the
  developer run read-only on either. Codex has been checked less: yoyo has
  read real Codex output for a reply and a finished turn, but not yet for a
  turn that fails or one that runs shell commands, edits files, or calls tools,
  so if you choose Codex, how yoyo reads those parts of a run is untested
  against real output. [Provider plugins](docs/provider-plugins.md) has the
  detail. A fork, proxy, or variant of either can be declared as a [provider
  plugin](docs/provider-plugins.md).
- **One `yoyo` per repository.** Teammates can commit alongside it the ordinary
  way, but two people each running `yoyo` against one repository is not
  supported yet: claims, reports, budgets, and the merge lock stay on the
  machine that made them. [Team mode
  scope](docs/team-mode-scope.md#what-v1-supports-meanwhile) has the details.
- **Any language.** The harness only runs the shell commands you declare as
  `checks` and reads their exit codes.

## Install

`yoyo` is one binary. The repository lives at
[github.com/swarmgraph/yoyodyne](https://github.com/swarmgraph/yoyodyne); its
Go module path is still `github.com/mason-bryant/yoyodyne`, which installs
through GitHub's redirect.

**With the install script:**

```sh
curl -fsSL https://raw.githubusercontent.com/swarmgraph/yoyodyne/main/scripts/install.sh | bash
```

[`scripts/install.sh`](scripts/install.sh) detects your platform, downloads that
platform's release binary and checks it against the release's `checksums.txt`
(or builds it with `go install` where no release binary exists for your
platform), and installs it into `/usr/local/bin` if you can write there, or
`~/.local/bin` if not. It runs the binary to print its version, prints the
`PATH` line for your shell if that directory is not on your `PATH`, and names
`bd` or `claude` if either is missing.

It does not edit your shell profile, use `sudo`, or touch any project: it writes
the binary and nothing else, and prints every other change for you to run.
Flags, passed through the pipe with `bash -s --`:

- `--dir <path>` installs somewhere else.
- `--version <tag>` installs that release instead of the newest.
- `--from-source` builds with Go instead of downloading.
- `--install-prereqs` installs a missing `claude` (with `npm install -g
  @anthropic-ai/claude-code`, or `claude.ai/install.sh` where there is no
  `npm`). `bd` is always named, never installed.

```sh
curl -fsSL https://raw.githubusercontent.com/swarmgraph/yoyodyne/main/scripts/install.sh | bash -s -- --dir ~/bin --version v0.3.0
```

The script only automates the routes below.

**With Go 1.25 or newer:**

```sh
go install github.com/mason-bryant/yoyodyne/cmd/yoyo@latest
```

That writes `yoyo` into `$GOBIN`, or `$(go env GOPATH)/bin` (usually
`~/go/bin`) if you have not set one. `go install` does not put that directory on
your `PATH`; if `which yoyo` finds nothing, add it once and open a new shell:

```sh
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc   # bash: ~/.bashrc
yoyo version   # prints the release tag it was installed at
```

Replace `@latest` with a tag such as `@v1.2.3` to pin a release.

**From a release download**, with no Go needed. Each tag on [the releases
page](https://github.com/swarmgraph/yoyodyne/releases) carries a binary per
platform and a `checksums.txt`:

```sh
tag=<the tag from the releases page>
platform=darwin_arm64   # or darwin_amd64, or linux_amd64
base="https://github.com/swarmgraph/yoyodyne/releases/download/$tag"
curl -fsSLO "$base/yoyo_${tag}_${platform}.tar.gz"
curl -fsSL "$base/checksums.txt" | shasum -a 256 -c --ignore-missing
tar -xzf "yoyo_${tag}_${platform}.tar.gz"
install -m 0755 yoyo /usr/local/bin/yoyo
yoyo version   # the tag you downloaded
```

**From source**, which is also how you work on yoyo itself:

```sh
git clone https://github.com/swarmgraph/yoyodyne
cd yoyodyne
make build     # writes ./bin/yoyo, stamped with the commit it came from
```

**Platforms.** `yoyo` is developed and used on macOS on Apple silicon; the
`linux_amd64` binary is exercised only by CI and `darwin_amd64` not at all.
There is no Windows binary.

Run `yoyo` from inside your own project: it finds its configuration by searching
upwards from the current directory.

## Getting started

Three steps, in this order:

1. **[Install `yoyo`](#1-install-yoyo)** — one binary, on your `PATH`.
2. **[`yoyo init`](#2-yoyo-init--give-the-project-its-own-configuration)** —
   give your project its own configuration, personas, and checks.
3. **[`yoyo chat`](#3-yoyo-chat--establish-the-brief-and-the-goals)** — write
   the brief and goals with the Lead Product Manager, and drive the work from
   there.

**`yoyo setup` does step 2 for you**, as questions: the tracker, the
configuration, the checks, the tracker's sync remote, the artifact-home indexes,
optionally [reporting into Slack](docs/reporting.md#reporting-into-slack), and,
on macOS, a [launch agent](docs/operations.md#starting-the-product-and-stopping-it)
that starts the product with the machine, ending with `yoyo doctor`. It asks before each step, changes nothing already
there, and is safe to run again: it resumes where an earlier run actually got
to. `--yes` accepts every proposal; `--json` reports what is done and what is
left and changes nothing.

**Or have your own coding agent do it.**
[`skills/yoyo-setup/SKILL.md`](skills/yoyo-setup/SKILL.md) is a prompt to paste
into your coding session (or install with the command at its top). It sets up
or repairs an installation from what `yoyo setup --json` and `yoyo doctor
--json` report, asking before each command.

**What you need.** Git and a repository with at least one commit;
[Beads](https://github.com/gastownhall/beads) (`bd`), the tracker every role
reads and writes; and [Claude Code](https://code.claude.com/docs), installed and
signed in. The install script checks for `bd` and `claude` and names whichever
is missing. Go 1.25 or newer only if you install with `go install` or build from
source, which the script also does on a platform with no release binary. For
pull requests, also a Git remote and [`gh`](https://cli.github.com) signed in
with `gh auth login`; without them nothing is pushed.

If a configured provider is outside PATH, such as desktop-bundled Codex, set its
absolute executable path once in the project configuration. [Provider setup and
precedence](docs/provider-plugins.md#executable-setup-and-precedence) covers
terminal chats, scheduled runs, fallback providers and account login commands.

CI executes this section on every change via
[`scripts/walk-adoption.sh`](scripts/walk-adoption.sh), against a throwaway
Python project; `make adoption` runs it locally.

### 1. Install `yoyo`

```sh
curl -fsSL https://raw.githubusercontent.com/swarmgraph/yoyodyne/main/scripts/install.sh | bash
yoyo version
```

If `yoyo version` is not found, the install directory is not on your `PATH`;
the script printed the line that adds it, and [Install](#install) has the other
routes. The script ends by printing the next steps; it names `yoyo setup` and
`yoyo doctor` only if the binary it installed has them. Then change into your
project:

```sh
cd path/to/your/project
```

### 2. `yoyo init` — give the project its own configuration

Initialize the tracker first. It is required: work items, their dependencies,
and the record of what agents did all live there.

```sh
bd init
yoyo init
```

No `bd` yet? Install it from [its home](https://github.com/gastownhall/beads);
this is the command `yoyo doctor` prints when it is missing:

```sh
curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash
```

`yoyo init` writes `.yoyodyne/config.yaml`, the six personas in
`.yoyodyne/personas/`, and `.yoyodyne/config.lock`, the template baseline `yoyo
config drift` compares against; it overwrites nothing without `--force`. It
writes a `README.md` into each artifact home under `docs/` saying what is filed
there and who owns it, and [points the tracker at your Git
remote](docs/configuration.md#where-the-tracker-syncs) so the backlog is shared.

**Then review the checks it proposed.** `init` reads what your repository
already declares — a Makefile's `check` or `test` target, `go.mod`,
`package.json`, `pyproject.toml`, `pom.xml`, a Gradle wrapper — and writes the
commands that follow into `checks`, each with a comment naming its source. It
executes nothing to find them. Read them and edit or delete what does not
belong:

```yaml
checks:
  # from pyproject.toml
  - python3 -m pytest -q
```

Commands it found but did not add are written beside the list, commented out;
remove the `#` to take one. Under `YOU MUST CHOOSE`, `checks` is empty and runs
are refused until you pick. [What `init`
proposes](docs/configuration.md#what-init-proposes-for-checks) has the rest.

Each check runs through `/bin/sh -c` in the run's worktree, must be
non-interactive, and must exit non-zero on failure. Checks have [time
limits](docs/configuration.md#how-long-a-check-may-take), thirty minutes by
default.

**Then validate the file, and the installation:**

```sh
yoyo config validate
yoyo doctor
```

`config validate` says whether the file loads (an empty `checks` list does;
`yoyo run` is what refuses it). `yoyo doctor` checks whether work can actually
run here — binary, Git, tracker, configuration, checks, provider, and forge
access if you publish — and gives the fix for each problem it finds.

Everything under `.yoyodyne/` belongs in version control. If you ignore it, both
`init` and `config validate` warn you, because clones and run worktrees would
then get an unconfigured project. In a repository that is not yours to add a
tool directory to, see [keeping the configuration outside the
repository](docs/configuration.md#keeping-the-configuration-outside-the-repository).

### 3. `yoyo chat` — establish the brief and the goals

Commit what you added first: a run refuses to start while the primary checkout
has uncommitted changes, and names the files. The tracker's own exports,
`.beads/issues.jsonl` and `.beads/interactions.jsonl`, are the exception.

```sh
git add -A && git commit -m "adopt yoyo"
yoyo chat
```

The Lead Product Manager reads your product's intent from the Markdown under
`product.specifications` (`docs/product` by default). A specification says what
the product is and why, then lists its goals:

```markdown
# Calc

A tiny arithmetic library, kept small enough that a change to it is obvious.

## Goals

- Arithmetic is correct for the operations the library claims to support.
- Every operation has a test that would fail if the operation broke.
```

**A repository with none of that is the normal starting point.** The Lead
Product Manager says intent is not written down rather than guessing it; tell it
what you are building and it drafts the brief and goals with you. It has no
tools and never touches your files, so when a document is ready it hands the
harness a typed write: you are shown the document, and **on your `y` the harness
files it in `docs/product/` with its frontmatter and your approval recorded in
it — then you commit it**, because a run refuses to start while it is
uncommitted. [Writing a document from a
conversation](docs/artifacts.md#writing-a-document-from-a-conversation) has the
rest. Goals are what work is admitted against, so without them it will ask you
for one.

**Then drive the work from the same conversation.** Approve the work items it
proposes. Once you trust it, `approvals.work_items: automatic` hands that
decision to your goals: approve them with `yoyo artifact approve <goals-id>` and
work that serves them reaches the queue without asking you. You can also file an
item by hand:

```sh
bd create --title="Add a subtract function" \
  --description="calc has add and nothing else. Add subtract(a, b) with a test." \
  --type=feature --priority=2
bd ready
```

`/work <beads-id>` runs an item in the background; `/status` says where it got
to, `/diff` shows what it changed, and `/stop` ends it and cleans up. [The
conversation](docs/conversation.md) covers the rest.

### Optional: publishing and auto-merge

By default yoyo is entirely local. Two settings turn publishing on, and each
works without the other:

```yaml
approvals:
  publishing: automatic   # push the run branch and open a pull request
  integration: automatic  # merge it on an approving verdict
```

`publishing: automatic` needs a remote (`execution.remote`, `origin` by
default) and `gh` signed in, and refuses before claiming any work if `gh` is
missing. `integration: automatic` is refused unless checks and a reviewer both
exist. With branch protection, enable the repository's **"Allow auto-merge"**
setting and permit merge commits, so the forge merges once your required checks
pass. [Publishing through pull
requests](docs/configuration.md#publishing-through-pull-requests) has what each
combination does, and [how work flows](docs/work.md) what happens when a merge
is dropped.

## Further reading

- [The conversation](docs/conversation.md) — proposals, steering, directives,
  and the other agents.
- [How work flows](docs/work.md) — after you approve an item, and publishing.
- [What comes back to you](docs/reporting.md) — cost, reports, and Slack.
- [Artifacts, goals, and invariants](docs/artifacts.md) — the documents work
  traces to.
- [Operations and recovery](docs/operations.md) — starting, stopping, pausing,
  provider limits, recovery, and `yoyo dashboard`.
- [The configuration guide](docs/configuration.md) — the full reference.
- [Running on several Claude accounts](docs/multi-account-quickstart.md).
- [Provider plugins](docs/provider-plugins.md) — declaring a provider variant.
- [The v1 harness design](docs/designs/v1-harness-design.md) — the architecture.
- [Reporting into Slack](docs/slack/setup.md) and [release
  notes](docs/releases/README.md).
- [`docs/product/`](docs/product) — this project's own brief, goals, and
  everything else filed there, which every role reads as product intent.
- [Terms](docs/terms.md) — words this project coined, in ordinary words.
- [Working on yoyo itself](docs/developing-yoyo.md) — checks, build, releases.
- [The documentation map](docs/docs-map.md) — which document holds what,
  including what moved out of this README.
