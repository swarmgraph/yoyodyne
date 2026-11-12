# yoyodyne-ifd.435.60: the Slack route's first check cannot be run from a developer run

The Slack route through the Claude Slack client (yoyodyne-ifd.435.60) asks for
one check before anything is built on it: can a Claude session the harness
starts, with no person present, reach the Claude Slack client the operator's
workplace approved for his account? The architect's text says that if it
cannot, building on the route stops. This records why a developer run could
not answer that question, and how to answer it.

## What was tried

On November 12, 2026, the developer run for this item ran, in its own scratch
folder outside the repository:

```sh
claude mcp list
```

Claude Code 2.1.322 printed only the project's local `anchor-mcp` server, which
was not running, and listed no Claude Slack client. The run's sandbox had
refused the connections that list depends on:

```
deny network-outbound api.anthropic.com:443 (user denied)
```

The connectors a Claude account has, the Slack client among them, are fetched
from Anthropic's service when a session starts. With that connection refused,
a session started from a developer run sees none of them. So the missing Slack
client shows only that the developer run's sandbox blocks Anthropic's service.
It says nothing about whether a session the harness starts outside that sandbox
can reach the Slack client.

## Why the build did not go ahead

The architect's text puts the check before anything else is built, so nothing
was built. One more point is for the architect: the active invariant that no
agent session holds an MCP server or a connector to an outside service
(`docs/decisions/invariants`, established by yoyodyne-ifd.435.40) says this
holds for every session, whatever the provider account says. A session the
harness starts that holds the Claude Slack client is an agent session holding a
connector, so building the route also needs that invariant amended to name the
carrying session as its one bounded exception. Main's section 8 of the MCP
design still declines this route on exactly that ground.

## How to run the check

It has to run where the harness's own sessions run: on the operator's machine,
outside a developer run's sandbox, signed in to the Claude account the
workplace approved, with no terminal attached so that nothing can ask a person:

```sh
cd "$(mktemp -d)" && claude mcp list </dev/null
```

The route can go ahead if that list shows a claude.ai Slack entry as connected.
A stronger check is to ask one headless session (`claude -p ... </dev/null`)
to list its tools without calling any, and see whether the Slack tools appear.
If the Slack client is missing, or shows as needing sign-in, the route stops
and the result goes to the Lead Product Manager, as the architect's text says.
