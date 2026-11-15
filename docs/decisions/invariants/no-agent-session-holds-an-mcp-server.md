---
id: no-agent-session-holds-an-mcp-server
title: No agent session holds an MCP server, and every MCP call is made, checked and recorded by the harness
status: active
established_by:
    - yoyodyne-ifd.435.40
    - mcp-tools-through-the-harness
    - yoyodyne-ifd.435.60
revisions:
    - action: created
      by: architect
      at: 2026-11-07T02:56:14.180752Z
      reason: 'yoyodyne-ifd.435.40: the invariant section 6 of the design for MCP tools through the harness calls for, because a correct change to a provider adapter could turn a server on and break the operator''s rule from outside that design''s scope.'
    - action: amended
      by: architect
      at: 2026-11-15T19:58:01.280589Z
      reason: 'Narrows the rule for the Slack route that section 8 of the design for MCP tools through the harness sets out (built under yoyodyne-ifd.435.60): a session the harness starts for one Slack call it has already checked and recorded, limited to that one tool, with its output checked against the decided call, while the operator has no Slack app. Answers the developer''s reports on 435.60 asking for this exception. Written from a whole read at main 7639c9c2.'
---

## Must hold

No agent session holds a Model Context Protocol (MCP) server, a connector to an outside service, or the credentials for either, whatever the project's configuration or the provider account says. Every provider adapter starts every role with none of them, including when the project declares servers and their credentials are stored on the machine.

Every MCP call is decided, checked and recorded by the harness's one package, `internal/mcpcall`, and made in the harness's own process, except in the one case below. Each call is checked against the list configured for whoever asked, is recorded before it is made, and has its outcome added to that record afterwards. Its result reaches an agent with secrets removed, within a size limit, and marked as untrusted data. Until that package is built, no MCP call is made at all.

There is one exception, and only one. A call to a Slack server the project declares with `sign_in: connector`, while the operator has no Slack app, may be carried out by a Claude Code session the harness itself starts for that one call, as section 8 of [the design for MCP tools through the harness](../../designs/mcp-tools-through-the-harness.md) says. That session belongs to no role, and no role asks for it. It starts only after `internal/mcpcall` has checked and recorded the call. It runs in an empty directory with no repository and no files, with every tool and connector off but the one Slack tool the call names. Its instructions are fixed in code, and the call's arguments are given to it as data. A call that posts carries only wording the harness wrote, with links. The harness reads the session's output, and unless it shows exactly the decided call with the decided arguments and no other call, the call is recorded as failed and a critical report is filed. The exception ends when the operator registers a Slack app and Slack is reached with `sign_in: oauth`.

## Why

A session holding a server could reach outside the sandbox, use the credentials, and act with no check and no record, and the review of a change could not see it happen. The operator directed on November 6, 2026 that agents ask the harness to use configured tools and never hold them. This binds work that never mentions MCP: a correct change to how an adapter builds its arguments, environment or home directory could turn a server on, which is why it is held as a constraint on the whole repository and tested across every role and provider.

The one exception exists because the operator directed on November 9, 2026 that Slack calls post as him and that he register no Slack app, and Slack gives a user token to a desktop client only through a registered app, so the harness has no token of its own to hold. The exception keeps what the rule is for: no role chooses what is called or what is posted, every call is checked and recorded before it happens, and a session that makes any other call is caught and recorded. It is kept narrow so that it cannot spread: Slack only, one call to a session, started only by the harness.
