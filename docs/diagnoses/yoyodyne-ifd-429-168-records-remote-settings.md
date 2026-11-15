# yoyodyne-ifd.429.168: shared records and the project's own Git remote settings

The item asks that the shared records be fetched and pushed with the project's
own Git remote settings — a credential helper, a URL rewrite (`insteadOf`), and
`core.sshCommand` — as claims and pull requests already are. It points at
`remoteURL` in `internal/gitworktree/recordsref.go`.

Read on November 12, 2026, against main at `c5c33342`.

## Why the item cannot be done yet

`internal/gitworktree/recordsref.go` does not exist on main. It arrives with
sharing reports and directives between factories (yoyodyne-ifd.429.108), whose
approved change is commit `5785f160`. On November 11 that change was waiting
in the harness's merge queue for main, with pull request #1747 open. Nothing on
main fetches or pushes shared records, so there is nothing here to correct and
no test can exercise it. The work can start once that change has landed.

## What the code in the waiting change does

These lines are read from commit `5785f160`.

- `RecordsRepository` is a bare repository in the machine's state directory.
  Every records command runs with `--git-dir=<that repository>`, so Git reads
  that repository's config and the user's global config, and **not** the
  project's `.git/config`.
- `remoteURL` asks the project's repository for the remote's URL with
  `git -C <repository> remote get-url [--push] <remote>`, then fetches from and
  pushes to that URL from the bare repository.
- Claims, by contrast, run in the project's repository and name the remote
  (`FetchClaims` in `internal/gitworktree/claimrefs.go`). That way Git applies
  everything the project's config says about reaching that remote.

So, setting by setting:

- **`url.<base>.insteadOf` / `pushInsteadOf` in the project's config:** these
  already apply. `git remote get-url` returns the rewritten URL, and `--push`
  returns the push rewrite. This was checked with Git 2.50.1: a remote
  `team:r.git` with `url.<dir>/.insteadOf team:` gives back `<dir>/r.git` for
  both. A test should still pin this, because the item asks for one.
- **`credential.helper` and `credential.<url>.*` in the project's config:**
  these do not apply. The bare repository never reads them, so an HTTPS remote
  that needs them fails to authenticate.
- **`core.sshCommand` in the project's config:** this does not apply, for the
  same reason. `GIT_SSH_COMMAND` in the environment still applies, because the
  environment is inherited.
- **`http.*` settings in the project's config** (proxy, extra headers, CA file)
  are missed the same way. The item does not name them, but they fail in the
  same way.

## The fix for whoever picks the item up

1. Before each records fetch or push, read the settings that decide how the
   remote is reached from the project's repository, with
   `git -C <repository> config --get-regexp` over `credential\..*`,
   `url\..*\.(insteadof|pushinsteadof)`, `core\.sshcommand` and `http\..*`.
   Pass each one to the records command as `-c key=value`, keeping a key that
   appears more than once (a list of credential helpers) in its order.
   Do not include the project's whole config: that would carry `core.bare=false`,
   `core.worktree` and hooks into the bare repository.
2. Wrap a failed records fetch or push in an error that names the remote and
   says in plain words that the records could not be fetched or pushed, and why
   (the Git stderr). Then make sure the status line shows that reason beside the
   count of records not yet shared, instead of showing the count alone.
3. Add one test for each setting, each on a remote reachable only through that
   setting:
   - `insteadOf` alias: the remote is configured as an alias that only a
     rewrite turns into the local bare path.
   - Credential helper: Git asks a helper only for HTTP remotes, and a test
     must not reach the network. So test the settings that are passed instead:
     set `credential.helper` in the project's config, and assert it is among
     the `-c` options the records command receives. Also cover a helper that
     appears more than once, which must keep its order.
   - `core.sshCommand`: an `ssh://` remote with `core.sshCommand` set to a
     script that runs `git-upload-pack` / `git-receive-pack` against a local
     bare repository. This is the same trick the claims tests can use, and it
     needs no network.
