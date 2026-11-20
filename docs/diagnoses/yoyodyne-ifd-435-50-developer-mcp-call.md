# yoyodyne-ifd.435.50: one git_log call from a developer run

This note records the developer-run half of the trial of one real read tool
through the harness (yoyodyne-ifd.435.50), carried by the item for a developer
run making one `git_log` call (yoyodyne-ifd.435.50.1). The conversation half
succeeded at 02:58 PST on November 20, 2026, and is recorded on the trial's
notes.

**Its answer:** the harness's route for a developer run worked end to end. The
developer asked with a block at the end of its turn, and the harness checked the
call, recorded it, made it, recorded the outcome, and resumed the same session
with the answer. No repair attempt was spent. **The git server itself refused
the call**, because the path the developer gave was its own worktree, and the
server is started for the primary checkout only. So no commits came back.

All times are PST.

## The call

- **Tool:** `git.git_log`, on the git server declared in `.yoyodyne/config.yaml`
  (`mcp-server-git --repository "/Users/$USER/github/yoyodyne"`).
- **Arguments:**
  - `repo_path`: `/Users/mbryant/Library/Application Support/Yoyodyne/state/worktrees/yoyodyne/yoyodyne/yoyodyne-ifd-435-50-1-b99b8377`,
    the root of the repository as the developer run sees it (`git rev-parse
    --show-toplevel` in its worktree);
  - `max_count`: `3`.
- **Asked by:** the developer of run `run-b99b8377f03fcbd0e170bd2ed4a171b5`.
- **Time:** started 03:17:19.94 and ended 03:17:20.45 on November 20, 2026,
  about half a second, as the harness's record of the call gives it.
- **Calls made:** one. The item asks for exactly one, so the call was not tried
  again with another path.

## The server's answer

The harness reported the call as failed, with the reason "the server answered
that the tool failed; what it said is the result". The server's words, quoted
exactly:

```
Error executing tool git_log: Repository path '/Users/mbryant/Library/Application Support/Yoyodyne/state/worktrees/yoyodyne/yoyodyne/yoyodyne-ifd-435-50-1-b99b8377' is outside the allowed repository '/Users/mbryant/github/yoyodyne'
```

No commit identifiers were returned. For comparison, the three newest commits
in the developer's worktree, read with `git log` in the worktree itself, were
`d648557eb`, `5b711351e` and `49141b43b`.

## What the call cost

- **The call itself:** no provider money. The git server is a local command the
  harness starts on this machine; the harness's record of the call carries no
  cost, and the result was 231 bytes, all of which reached the developer.
- **The turn that asked:** the run's spend record has one entry so far, written
  at 03:17:19.70 when the asking turn ended: US$0.40 (0.3986562) on
  `claude-opus-5-5`, with 4 fresh input tokens, 85,281 cached input tokens,
  45,818 tokens written to the cache, and 752 output tokens. That turn also
  read the repository's instructions and ran the build check, so not all of it
  is the cost of asking.
- **The resumed turn** that received the answer and wrote this note is another
  ordinary paid turn of the same session, counted to the run as the design says.
  It had not been recorded when this note was written, so its cost is not given
  here.

## The harness's own record

The record is in the product's state directory, under
`mcp-calls/20261120-51463.jsonl`, entry `49c35c7832682faf10f2ca4f`. It was
written twice, as the design says: once when the call was asked, before it was
made, and once when it settled. It holds the caller (kind, role, agent and run),
the server, tool and its class (`read`), the configuration revision
(`cfg-3a573bd0b12a`), the digest of the server's tool listing, the arguments and
their digest, the `why`, the outcome `failed` with its reason, one try, the
result, its size and digest, and how much of it was shown.

## What did not behave as documented

Measured against section 3 ("How a developer run asks") and section 4 ("Making
the call, what is recorded, and what is kept out") of
[the design for MCP tools through the harness](../designs/mcp-tools-through-the-harness.md),
the harness's route behaved as documented in every point this call exercised:

- the call was checked against the developer's list and made;
- it was recorded before it was made and completed afterwards;
- the server's error came back marked as untrusted data, and was treated as a
  failure with its reason, not as one of the harness's own refusals;
- the same session was resumed in the same attempt, and no repair attempt was
  spent.

One thing did not go as the trial expected, and it is in the configuration
rather than in the route: **the declared git server reads only the primary
checkout, and a developer run works in a worktree of its own.** The server is
started with `--repository /Users/$USER/github/yoyodyne` and refuses any other
path. A developer's own repository root is its worktree, so the natural
argument from a developer run is refused. A developer could still name the
primary checkout's path and get the history of what is there, but that is not
its own branch, and nothing the developer is told says which path the server
accepts. The design does not say whether a developer's tools are meant to read
its worktree or the primary checkout, so this is a question for the design's
owner rather than a defect in the route.
