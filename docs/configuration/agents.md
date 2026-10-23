<!--
Landed by yoyodyne-ifd.117.1, tranche 1 of the configuration.md split, with
docs/configuration.md left intact. The links below into ../configuration.md
resolve today and point at sections a later tranche moves; the tranche that
moves a section retargets the link:

  #keeping-the-configuration-outside-the-repository
                                          -> no row in docs/docs-map.md; stays
                                             in configuration.md until the map
                                             gives it a home

117.2 retargeted #protected-paths-in-a-developers-change to artifacts.md and
#what-reaches-the-queue to goals.md when it landed those guides.

"The configuration index ... lists the other guides" below is a forward claim:
configuration.md becomes the index in 117.4.

Scope against docs/docs-map.md: the map's disposition table assigns this guide
six sections — Operators, Reporting to Slack (+ Avatars), Personas, and also
Provider accounts (+ Pooling), Research sources, and How long one role may ask
another. This tranche lifts the first three on the development manager's
re-run direction, which was written before the map's reconciliation; the other
three stay in configuration.md for 117.4. Size: 289 lines against the map's
370-line budget, which counts all six.
-->
# Configuring operators, personas, and reporting

The humans this project recognizes and what each may do, the personas that
specialize a role, and what reaches Slack.

[The configuration index](../configuration.md) lists the other guides.

## Operators

`operators` is the humans this project recognizes. Each entry binds one person's
identifier namespaces and says what that person may do:

```yaml
operators:
  mason:
    git_email: mason@example.com
    forge_account: mason-bryant
    slack_member_id: U0123456789
    grants:
      - own-intent
      - direct-work
  jordan:
    git_email: jordan@example.com
    forge_account: jordan-q
    grants:
      - direct-work
```

The whole mapping is optional, and a project that names nobody recognizes
nobody — which is every project until it names somebody, and is closed rather
than open. `yoyo init` writes an example of it commented out, beside the
[`slack`](#reporting-to-slack) block, so a generated file shows what an entry
looks like without recognizing anybody.

It is **top level rather than under any one surface**, because a human is known
by more than one. An act carries an identifier and never a person: a commit
carries an address, a push carries a forge account, a thread reply carries a
member id. Binding all three to one entry is what lets an authority check
resolve whichever namespace the act arrived through to the same person and then
ask what that person may do. Filing the whole thing under `slack` would have
made the Slack id the identity and the other two an afterthought.

**No new identity machinery, deliberately.** Git and Dolt authorship are the
assertion — the address on a commit is what the author says about themselves —
and the forge's push authentication is the proof, at the one boundary that is
shared. This mapping adds the join between namespaces that otherwise have
nothing to do with each other; it does not add a login.

Each key is a short name for a person, in the same shape as an agent name
(`mason`, `jordan-q`). Every field under it is optional except that at least one
namespace has to be bound: a human bound to nothing is authority attached to
nobody, since no act can arrive carrying an identifier that reaches them.

- `git_email` — the address their commits and tracker writes are authored with.
- `forge_account` — their account on the remote the project publishes to.
- `slack_member_id` — their member id in the reporting workspace, from their
  profile → "Copy member ID". It is identity rather than a secret, which is why
  it is checked in here with the rest.
- `dashboard_token` — where their own dashboard token is kept, `keychain` (the
  item `yoyo-dashboard.<product id>.<name>` under the account `yoyo`) or `file`
  (`<state root>/projects/<product id>/state/dashboard.<name>.token`). It names
  a store and never holds the token. A dashboard tab signed in with that token
  is this person, which is what lets [the page take their own approve and
  decline](../operations.md#deciding-from-the-page); the dashboard's shared token resolves to nobody. For now only the
  operator — the one human holding `own-intent` — may bind one; a
  `dashboard_token` on anybody else's entry is refused when the configuration
  loads.

Addresses and forge accounts are matched without regard to case, because they
are case-insensitive where they live; a member id is matched exactly, because it
is an opaque id the workspace issued rather than something a person types. One
identifier may be bound by one human: an identifier that resolves to two people
resolves to neither, so it is refused when the configuration loads.

`grants` is what the human may do, whichever namespace they arrive through, and
it defaults to empty. Recognizing somebody and authorizing them are two
decisions, so an entry with no grants records who a person is without giving
them anything — which is also how you take authority back without forgetting the
person.

| grant | what it is |
| --- | --- |
| `own-intent` | stating and approving what the product is for: the brief, the goals, and the non-goals. **At most one human may hold it** — several people amending goals concurrently is conflict machinery nobody has designed. |
| `direct-work` | steering work already in flight: the directives that reach a run, and the thread replies the Slack sink acts on. |
| `decide-amendments` | deciding a change proposed to a document from the dashboard. A change to a document the Lead Product Manager owns needs `own-intent` beside it. `yoyo amendment` at a terminal carries no identifier to check it against. |

**Grants are checked in two places, and it is worth being exact about which.**
The Slack sink's allow-list is derived from the `direct-work` holders who bound a
member id, so a thread reply is acted on or refused by asking this mapping who
sent it. And the dashboard resolves a tab to the person whose own
`dashboard_token` it presents, and approves or declines from the page only for
a person holding `own-intent` — for a document or proposed work — or
`decide-amendments`, with `own-intent` beside it for a change to a document the
Lead Product Manager owns. Those are the two acts that carry something the
harness can resolve to a person: a member id the workspace put on the message,
and a token the person stored.

**At a terminal `own-intent` is checked by nothing, and `by: operator` proves
only that a command was run.** A terminal carries no identifier at all, so `yoyo artifact
approve` records `by: operator` on the strength of whoever ran it, and so do
`yoyo amendment approve`, `yoyo directive record`, and the `--by` on `yoyo triage
override`, which is a string somebody types. Nothing in any of those records
distinguishes the operator from anything else with a shell. Read an `own-intent`
grant as this project's record of who owns intent — which is what refuses a
second holder when the configuration loads, and what a person auditing the
mapping reads — rather than as a gate an act passes through.

**What keeps an agent out of the goals is two enforcements that do not depend on
the signature.** A conversation cannot write: on Claude Code it runs with no
tools at all, and on Codex it can only read, so the roles that could argue for a
goal cannot change a file; and a run's change is compared
against the [protected paths](artifacts.md#protected-paths-in-a-developers-change) before any
check runs and before any reviewer sees it, so an approval a developer wrote is
refused with the rest of the diff and never reaches the repository the goals are
read from. [What reaches the queue](goals.md#what-reaches-the-queue) rests on those two
rather than on who an approval says gave it, which is what
`internal/chat/admission.go` says in its own words. If either is ever loosened,
this is what was resting on them.

At a terminal, making an approval name the resolved human, and refusing one
from anybody who does not hold `own-intent`, is designed and not built. The
dashboard refuses a person without the grant, and records what it decides as
the operator's, exactly as the terminal's verb does.
[Operator identity, designed once](../team-mode-coordination.md#operator-identity-designed-once)
is where it is specified, and what it would and would not close.

## Reporting to Slack

`yoyo slack` reports what the harness is doing into a Slack channel: one thread
per work item, one message per milestone, and every report an agent filed at the
severity it was filed under. The project says where to report and what each
speaker looks like; nothing else about reporting is configurable here.

```yaml
slack:
  enabled: true
  channel: C0123456789   # a channel id, or a #name
```

The whole block is optional, and a project that omits it reports nothing — which
is every project until it opts in. `channel` takes a channel id or a name;
an id is worth preferring because renaming the channel does not break it.

**`yoyo init` writes it commented out**, together with the
[`operators`](#operators) example beside it, so the generated file shows the
shape and says the capability exists rather than leaving both to be found in
[`docs/slack/setup.md`](../slack/setup.md). Deleting the leading `# ` from each
line is the whole of turning it on; `yoyo setup` does the same edit for you, and
replaces the commented example rather than writing a second block under it.

### Avatars

Each speaker posts under its own name and picture, and the picture is the
project's to choose:

```yaml
slack:
  enabled: true
  channel: C0123456789
  avatars:
    harness: ":gear:"
    developer: ":ship-it:"                              # a custom emoji works
    reviewer: https://example.com/faces/reviewer.png
```

Keys are roles — `product-manager`, `architect`, `development-manager`,
`developer`, `reviewer` — or `harness` for what no persona did. A value is
either an **emoji shortcode**, including a custom emoji this workspace added
itself, or the **https URL of an image** Slack fetches. Both shapes need the
`chat:write.customize` scope the [app manifest](../slack/manifest.yaml) already
declares, so neither costs a reinstall.

The mapping is optional and so is every entry in it. A speaker with no entry
keeps the avatar the harness ships, so naming one persona's picture does not
blank the rest. An avatar that is neither shape is refused when the
configuration loads, whether or not reporting is switched on — Slack accepts an
unknown shortcode or an unreachable image without complaint and quietly shows
the app's own icon, so nothing downstream would ever say so.

Entries **merge across layers** rather than replacing each other, the way agents
do: a project that extends a bundle and changes the developer's picture keeps
every other one it inherited.

**Only the picture is configurable.** The name a message appears under, and
whose account it is, are not here and are not meant to be — who speaks is a
claim about who did the work, and a project that could rewrite it could
attribute a promotion to a developer. The avatar carries none of that:
everything it distinguishes is already distinguished by the name beside it and
the voice below it, so a reader whose client renders no picture loses nothing.

**Every name says which product it speaks for**, from
[`product.id`](setup.md#layout): `Development Manager (yoyodyne)`,
`Yoyodyne (yoyodyne)`, and a project that configured a second agent for a role
reads `Developer (opus) (yoyodyne)` — the product is last on every name, in the
same shape, for every speaker including the harness. It is applied by the voice
layer from the id the configuration already carries, never authored per message
and not configurable beside the avatars, because it is a fact about which
harness is talking rather than a claim about who did the work. An operator with
two products in development is running two harnesses, and where both are read in
one channel this is the only thing a message carries that tells them apart.

**Who may steer the harness from a thread is not configured here.** The
allow-list is derived from [`operators`](#operators): the humans granted
`direct-work` who have bound a `slack_member_id`, and nobody else. It is a
derivation rather than a second list because a list maintained beside those
grants is a list that disagrees with them — silently, and about authority. A
human granted `direct-work` who has bound no member id simply is not on it: they
hold the authority, and Slack is not a boundary they can reach it through.

An instruction from somebody on that list is recorded as a directive against the
item whose thread it was said in, and reaches the work exactly as one typed at a
terminal does; a question from them is answered by the Lead Product Manager in the
same thread and recorded as nothing. A reply from a human this mapping names who is not on it is
answered in the thread saying it was not acted on, naming the grant they are
missing — visibly, because a channel that silently ignores some people looks
broken rather than closed. What a reply may say is in
[`docs/slack/setup.md`](../slack/setup.md#steering-the-work-from-a-thread).

**Somebody the mapping does not name at all gets a different answer**, and gets
it once: *I don't know you. Please reach out to … if you need something*, with
the humans this mapping names filling in the gap — by the names it files them
under, rather than as Slack mentions, so telling one stranger who to ask does not
notify everybody. It is said at most once per thread — in a thread this sink
opened, or under the message that @-mentioned the app — and everything the same
person says after it in that thread is written to the sink's log and answered
with nothing, so an unknown user cannot make the app talk by repeating
themselves. A project whose mapping names nobody has drawn no boundary and has
nobody to name as a contact, so it says this to nobody.

An earlier shape put this list under `slack` as `slack.operators`. It is gone,
and a file that still carries it is refused when the configuration loads, with a
message naming the entry to write instead.

**The credentials are not here and must never be.** The sink reads
`SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` from its own process environment and
from nowhere else: never from this file, never from a work item, never from a
prompt. That is what keeps the boundary structural rather than behavioral — one
separate process posts, and the harness builds every run's environment from an
allowlist rather than handing down its own, so no run process, and therefore no
agent's subprocess tree, has a Slack token in its environment at all, even on a
machine where the pair is exported in a shell profile. What such an export does
still cost is the harness's own process and the sink: they are read from a
store only the sink's own launch looks at, under names that carry the product —
`yoyo-slack-bot.<product id>` and `yoyo-slack-app.<product id>`. The product is
in the name because a machine running more than one harness has more than one
pair, and a sink launched from a shell holding the wrong one connects,
authenticates, and posts this project's work into another project's channel.
`yoyo doctor` asks whether this project's pair is stored, and whether the sink
that is running was launched with it; [`docs/slack/setup.md`](../slack/setup.md#5-store-the-two-tokens-under-this-projects-names)
has the launcher.

Reporting is an observation and never a gate: a workspace that is down, slow, or
misconfigured changes nothing about any run. [`docs/slack/setup.md`](../slack/setup.md)
takes a workspace from nothing to live reporting, and the app manifest it asks
for is checked in beside it.

## Personas

A persona is a Markdown file describing how an agent works. Personas specialize
behavior; they never grant it. The harness invariants — agent authority,
worktree sandboxing, the protected paths a developer's change may not touch, the
review verdict contract, integration preconditions, and cleanup — are enforced in
Go and are not configurable, so a persona cannot weaken them:

- the developer prompt starts with the harness contract verbatim, and the
  persona follows it as subordinate guidance;
- the reviewer's system prompt starts with the immutable review contract, and
  the persona follows it; the decision vocabulary and the JSON response format
  are not negotiable, and a persona cannot authorize approving a change the
  reviewer cannot see;
- untrusted developer output is never treated as configuration, and configured
  text never replaces harness policy.

Persona rules:

- `version` is a free-form revision label recorded in the effective
  configuration, so a change of guidance is visible in diagnostics.
- `path` is relative to the directory the configuration file is in, and must name
  a Markdown file inside it. Absolute paths, `..` traversal, and symlinks that
  escape the directory are rejected. For the ordinary project that is the
  `.yoyodyne` directory, and for a configuration
  [kept outside the repository](../configuration.md#keeping-the-configuration-outside-the-repository)
  it is the directory `init --external` wrote, which is what lets that directory
  be moved as one. A `.yoyodyne.yaml` uses the `.yoyodyne` directory beside it,
  which is where migrating it would put the personas; and a `config.yaml` placed
  by hand somewhere of its own falls back to a `.yoyodyne` directory beside it
  when the persona is not there, so an arrangement that predates this goes on
  loading.
- A persona is limited to 32 KiB. It is role guidance, not a document to paste
  into every prompt.

In a project `init` wrote, every persona is already a file in
`.yoyodyne/personas/`: change how the reviewer works by editing
`personas/reviewer.md`, and bump the `version` label beside it in the
configuration so the change is visible in diagnostics.

```yaml
agents:
  reviewer:
    persona:
      version: house-1            # bumped from v1 after editing the file
      path: personas/reviewer.md
```

In a project that uses `extends`, the same block is how one inherited persona is
replaced without changing anything else.
