---
id: no-agent-session-holds-an-mcp-server
title: No agent session holds an MCP server, and every MCP call is made, checked and recorded by the harness
status: active
established_by:
    - yoyodyne-ifd.435.40
    - mcp-tools-through-the-harness
revisions:
    - action: created
      by: architect
      at: 2026-11-07T02:56:14.180752Z
      reason: 'yoyodyne-ifd.435.40: the invariant section 6 of the design for MCP tools through the harness calls for, because a correct change to a provider adapter could turn a server on and break the operator''s rule from outside that design''s scope.'
---

## Must hold

No agent session holds a Model Context Protocol (MCP) server, a connector to an outside service, or the credentials for either, whatever the project's configuration or the provider account says. Every provider adapter starts every role with none of them, including when the project declares servers and their credentials are stored on the machine.

Every MCP call is made in the harness's own process by its one package, `internal/mcpcall`. Each call is checked against the list configured for whoever asked, is recorded before it is made, and has its outcome added to that record afterwards. Its result reaches an agent with secrets removed, within a size limit, and marked as untrusted data. Until that package is built, no MCP call is made at all.

## Why

A session holding a server could reach outside the sandbox, use the credentials, and act with no check and no record, and the review of a change could not see it happen. The operator directed on November 6, 2026 that agents ask the harness to use configured tools and never hold them. This binds work that never mentions MCP: a correct change to how an adapter builds its arguments, environment or home directory could turn a server on, which is why it is held as a constraint on the whole repository and tested across every role and provider.
