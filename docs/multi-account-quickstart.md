# Running on several Claude accounts: quick start

One Claude subscription has usage limits, and a project that runs steadily will
meet them. Pooling spreads the runs across two or more accounts, one account per
run, so the work keeps moving when one subscription is spent. This page is the
shortest path to a working pool; [Provider accounts](configuration.md#provider-accounts)
and [Pooling work across several accounts](configuration.md#pooling-work-across-several-accounts)
have the full behavior.

## 1. Declare the accounts

In `.yoyodyne/config.yaml`, name each account. `default` is the account this
machine is already signed in to; every other alias is a subscription of its own:

```yaml
accounts:
  default:
    description: the Claude subscription this machine is signed in to
  second:
    description: the other subscription
    weekly_budget_usd: 100   # optional: stand it down past this spend over 7 days
```

An entry names an account, never a credential — no token or path goes in this
file.

## 2. Sign the new account in

The moment a second account exists, the new alias needs a login of its own.
Run:

```sh
yoyo doctor
```

It reports the new account as unauthenticated and prints the exact login
command to run. `bin/yoyo-account` is the same step as a walkthrough: it asks
for the alias, the pool, and an optional weekly budget, runs the login, and
prints the entry to add. The account you were already signed in to needs
nothing.

**Run the login in a new browser profile.** It opens an OAuth flow that binds
whichever Claude account the browser is already signed in as. Signing the
second alias in from the browser the first one used gives you one account under
two names, which looks like a working pool until both halves run out at the
same moment. Use a fresh browser profile, or a private window signed in as
nobody, signed in as the second account and left as the default browser.

By hand, the login is two commands: make the alias its own provider home under
the state directory, then sign in into it.

```sh
home="$HOME/.yoyodyne/accounts/second"
mkdir -p "$home" && chmod 700 "$home"
CLAUDE_CONFIG_DIR="$home" claude auth login
```

The state directory is `$YOYODYNE_STATE_HOME`, the `state_root` in the
machine's own `machine.yaml`, `$XDG_STATE_HOME/yoyodyne`, or `~/.yoyodyne`, the first of those that is set
(a machine whose state is still in the earlier builds' default,
`~/Library/Application Support/Yoyodyne/state` on macOS or
`~/.local/state/yoyodyne` on Linux, keeps using it until it is moved) — the same
one `yoyo status` reads, and the one `yoyo config show` prints on its
`# state root:` line. `yoyo project list --home` prints that path and nothing
else, which is what `bin/yoyo-account` asks rather than working it out itself:

```sh
home="$(yoyo project list --home)/accounts/second"
```

## 3. That's it

Runs now rotate across the accounts, one account per run. Nothing else changes:

- `yoyo status` and each run's Slack thread say which account a run used.
- An account with a `weekly_budget_usd` stands down when this product's runs
  have cost that much in the last seven days, and rejoins as the spend ages out.
- Add `pool: reserved` to an account to hold it back until no active account
  can serve.
- An account that is not a Claude Code login names its provider —
  `provider: codex` — because a provider home holds one provider's
  authentication. An account of the wrong provider is skipped by the rotation,
  and a pool that holds none for an agent is refused before a run claims
  anything.
- Long-lived agent conversations stay on one account; only runs rotate.

To check the pool at any time, `yoyo doctor` reports every account and its
login state.
