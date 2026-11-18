# The getting-started walkthrough, run against this README

*The recorded output of `make getting-started` — [`scripts/walk-getting-started.sh`](walk-getting-started.sh) —
against the README this change delivers. It is here because yoyodyne-ifd.121.5
made it the gate on the README split's reduction rather than leaving the
reduced README to review alone: a section that moved out of the README is a
step somebody following it can no longer take, and only executing the section
says whether that happened.*

**This is a record and not a check.** It goes stale the moment the README's
install or getting-started sections move. Re-run `make getting-started` and replace
this file rather than citing it if they have — and note that since
yoyodyne-ifd.121.6 the walkthrough is a merge gate in its own right: the
`getting-started` job in [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)
runs it on every pull request. So this file is a readable snapshot of what the
walkthrough prints, and CI rather than this file is what stands behind the
claim that the documented path works.

The walkthrough was called `adoption` — the CI job, `make adoption`, and
`scripts/walk-adoption.sh` — when the runs recorded below were made, so they
name it that way.

**Re-run by yoyodyne-ifd.121.6**, which edited the paragraph above `### 1.
Install yoyo` to say that CI runs the walkthrough. That edit changes
`README.md`'s digest, so the one recorded below no longer matches the file:
`README.md` now hashes to
`7530ef387bd517468f922e5de87e0c6bfd1fa616c96e8abb0dff624890f5261e`, and
`make adoption` was run against that README on 2026-09-05 with the same result —
57 assertions passed, none failed, and the same one claim named as unexercisable.
The transcript below is ifd.121.5's and is kept as the readable record; it is
unchanged because the walk's own output did not change.

**The digest ceremony below is spent.** It existed because this file was the only
evidence the reduced README still worked, and pinning it to a hash is what made
that evidence checkable. CI now runs the walkthrough on every pull request, so a
README that breaks the documented path fails a gate rather than falsifying a
digest nobody re-hashes. Read the rows below as ifd.121.5's record of what it
walked, not as a claim about the file as it now stands.

## What it was run against

| | |
|---|---|
| Run on | 2026-09-05 |
| `HEAD` at the walk | `3a236e2e57108608a4e00f3f9365d7ea0e68fdd5`, this branch's tip |
| Working tree at the walk | **clean** — `git status --porcelain` was empty, which is why `yoyo version` in the output below reads `v0.4.0-255-g3a236e2` with no `-dirty`. The walk built from the committed tree and nothing else. |
| Branch base | `64a3cfb` |
| `README.md` sha256 at the walk | `9db4bdab2b1a33c945145bb4201f351572ed495033185f8f82f9730246203771` |

**yoyodyne-ifd.121.5 did edit `README.md`**, and inside the section this walk
executes: it rewrote the "What the product manager can see" paragraph under
`### 3. yoyo chat` to name the eight documents the product manager is shown
rather than to say "the documents it links to under Further reading". That
rewrite was in the tip above, so the digest recorded here is the digest of the
rewritten README that tranche merged. Against its base that change also touched
`Makefile`, `docs/configuration.md`, `docs/conversation.md`,
`docs/developing-yoyo.md`, `internal/composition/composition.go`,
`internal/config/scaffold.go`, `internal/doclink/doclink.go`,
`internal/doclink/doclink_test.go`, and this file.

**Why the digest was recorded at all, and why nothing needs to record one now.**
A developer run makes no commits, so this file is written after the walk and the
harness commits it afterwards, in a commit later than the `HEAD` named above —
which is why the record was bound to a hash rather than to a revision. That
reasoning was sound and is no longer needed: with the walkthrough running in CI,
what binds the claim to a revision is the job that executed it on that revision.

## Result

**`the documented adoption path works as written`** — 57 assertions passed, none
failed, and one claim was named as unexercisable rather than passed over: `go
install` of a published tag needs network access and a pushed tag, and this
environment has neither. Step 12, which hands an item to a developer agent,
is opt-in behind `WALK_PROVIDER=1` and was not run; everything before it needs
no provider.

## The output

```text
scripts/walk-adoption.sh

=== prerequisites: the build requirement the README states
go.mod declares go 1.24.0
  ok: README's "Go 1.24 or newer" matches go.mod

=== 1. install the binary
origin: git@github.com:mason-bryant/yoyodyne.git
  ok: README's clone URL names this checkout's origin (reachability not checked)
module: github.com/mason-bryant/yoyodyne
  ok: go.mod names the module the README's `go install` line names
  ok: `go install` reaches the proxy rather than refusing the path
go env GOPATH: /Users/mbryant/go
  ok: the documented default destination, ~/go/bin, is where go would install
  SKIPPED: go install of a published tag, and the yoyo version check on it: needs network access and a pushed tag, neither assumed here
  ok: make build wrote ./bin/yoyo
built version: v0.4.0-255-g3a236e2
  ok: yoyo version names the build it came from

=== the scratch project: not this repository, not Go
  ok: created a Python project with one commit at /tmp/claude-501/yoyodyne-walk.45hCtR/calc

=== 2. initialize the tracker
  ok: bd init created .beads
  ok: bd ready answers in the scratch project

=== 3. write the configuration
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/.yoyodyne/config.yaml
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/.yoyodyne/personas/architect.md
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/.yoyodyne/personas/developer.md
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/.yoyodyne/personas/development-manager.md
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/.yoyodyne/personas/product-manager.md
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/.yoyodyne/personas/reviewer.md
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/.yoyodyne/config.lock
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/docs/product/README.md
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/docs/product/goals/README.md
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/docs/designs/README.md
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/docs/decisions/README.md
wrote /tmp/claude-501/yoyodyne-walk.45hCtR/calc/docs/decisions/invariants/README.md
the configuration is complete and inherits nothing
checks is empty: 2 candidates are commented in /tmp/claude-501/yoyodyne-walk.45hCtR/calc/.yoyodyne/config.yaml, and one of them has to be chosen before work can run
the tracker already syncs through origin: git+file:///tmp/claude-501/yoyodyne-walk.45hCtR/origin.git; name a URL with --tracker-remote to point it elsewhere
  ok: wrote personas/architect.md
  ok: wrote personas/developer.md
  ok: wrote personas/development-manager.md
  ok: wrote personas/product-manager.md
  ok: wrote personas/reviewer.md
  ok: a second init refuses rather than overwriting
  ok: init leaves the tracker syncing over the project's Git remote
origin               git+file:///tmp/claude-501/yoyodyne-walk.45hCtR/origin.git
  ok: bd holds the sync remote init configured

=== 4. init proposes checks from what this project already declares
  ok: an undecidable toolchain leaves checks empty rather than guessing
  ok: the candidates carry an explicit you-must-choose marker
  ok: offers a candidate, commented out: #  - python3 -m pytest -q
  ok: offers a candidate, commented out: #  - python3 -m unittest discover -q -s tests -t .
  ok: names tests/test_calc.py as where the candidates were derived from
  ok: carries a commented # Go example
  ok: carries a commented # TypeScript / Node example
  ok: carries a commented # Python example
  ok: carries a commented # Java (Maven) example
{"bundle":"builtin:v1","checks":["python3 -m pytest -q"],"config":"/tmp/claude-501/yoyodyne-walk.45hCtR/decided/.yoyodyne/config.yaml","detected":{"checks":[{"command":"python3 -m pytest -q","source":"pyproject.toml"}],"candidates":[],"alternatives":[]},"external":false,"files":["/tmp/claude-501/yoyodyne-walk.45hCtR/decided/.yoyodyne/config.yaml","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/.yoyodyne/personas/architect.md","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/.yoyodyne/personas/developer.md","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/.yoyodyne/personas/development-manager.md","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/.yoyodyne/personas/product-manager.md","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/.yoyodyne/personas/reviewer.md","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/.yoyodyne/config.lock","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/docs/product/README.md","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/docs/product/goals/README.md","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/docs/designs/README.md","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/docs/decisions/README.md","/tmp/claude-501/yoyodyne-walk.45hCtR/decided/docs/decisions/invariants/README.md"],"ignored":{"ignored":false},"repository":"/tmp/claude-501/yoyodyne-walk.45hCtR/decided","status":"written","tracker":{"status":"skipped","reason":"fatal: not a git repository (or any of the parent directories): .git"}}
  ok: a project that names pytest gets a runnable check written
  ok: the report names the file the check was derived from
  ok: the written check carries its provenance as a comment
  ok: a Makefile's check target is proposed as the project's gate
  ok: init read the Makefile without running anything in it
  ok: the superseded Go commands are headed as offered, not as owed
  ok: the superseded Go commands are still shown, commented out
  ok: a configuration that already runs demands nothing

=== 5. an empty checks list validates, but a run refuses it
  ok: config validate passes with checks: []
work item: calc-8k8
  ok: yoyo run refuses a run with no checks

=== 6. choose one of the candidates init offered
  ok: uncommented the chosen candidate in place
  ok: the uncommented candidate is the effective checks list
  ok: the chosen check passes through /bin/sh -c
  ok: config validate passes with the project's own checks
  ok: configuration is discovered from a subdirectory

=== 7. doctor checks the whole installation, not only the file
  ok: doctor calls this project's configuration, repository, tracker, and checks healthy, and carries a remedy for everything it does not

=== 7b. yoyo setup reaches the same state by asking, and again changes nothing
  ok: setup converges a blank project, and doctor then calls its configuration, repository, tracker, and checks healthy
  ok: running setup again reports what is already true and changes nothing

=== 8. write down what the product is for
  ok: wrote a specification with an introduction and goals

=== 9. a run refuses an uncommitted primary checkout, and names the files
  ok: the run refuses an uncommitted primary checkout
  ok: the refusal names the file that is dirty
  ok: the refusal names every file that is dirty
$ go test ./internal/gitworktree -run TestManagerAllowsOnlyConfiguredPrimaryControlPlaneChanges -count=1
ok  	github.com/mason-bryant/yoyodyne/internal/gitworktree	0.366s
  ok: .beads/issues.jsonl and .beads/interactions.jsonl are excepted from that refusal
  ok: committed the adoption

=== 10. the commands the README points a new project at
  ok: yoyo reconcile reports nothing outstanding
  ok: a project with no invariants directory simply has none
  ok: config show --origins names the project file
  ok: nothing is inherited from the built-in bundle

=== 11. following a run or a conversation
  ok: yoyo status --list honors YOYODYNE_STATE_HOME
  ok: yoyo status --list honors XDG_STATE_HOME by appending yoyodyne
  ok: yoyo status --spend reports on a machine with nothing recorded

=== 12. drive it from the conversation
skipped: set WALK_PROVIDER=1 to invoke the provider on this step.
Everything before it ran without one.

=== result
the documented adoption path works as written
1 claim(s) this environment could not exercise, named above
```
