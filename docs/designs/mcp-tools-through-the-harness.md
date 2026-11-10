---
id: mcp-tools-through-the-harness
kind: design
title: 'MCP tools through the harness: a role asks, the harness checks, calls and records, and no agent holds a server'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-07T01:05:29.109979Z
      reason: 'yoyodyne-ifd.435.40: Mason''s direction of November 6 that agents ask the harness to use specific configured MCP tools and never hold a server themselves. Settles the configuration, the action a conversation role writes, how a developer run asks without opening its sandbox, what is recorded and bounded, how the reviewer stays independent, and how grants and the unexpected tool use check account for these calls. The call path also takes a scheduled caller, for the later design on message intake. Names nine build pieces. Written from reads at main 88bff330.'
    - action: amended
      by: architect
      at: 2026-11-07T05:27:21.973046Z
      reason: 'Makes this design agree with the landed scheduled message intake design (yoyodyne-ifd.435.41), read at main 52f8379d: the role table gains the message reader, holding neither class; a run of an item started from a message holds no MCP tools; section 4 gains two exceptions, that an intake query''s call record keeps no result and that a link and one line of at most 300 bytes may reach the operator by direct message; section 7 points to that design. Also corrects the configuration example: the credential line on main held a masking marker in place of a value, and it now shows the value `file`, with the other choice named in the comment and the bullet below. The refused document-2397.1 wrongly said the line would show the other choice; the harness''s masking of a value that long is why it did not.'
    - action: amended
      by: architect
      at: 2026-11-10T06:56:57.220875Z
      reason: 'yoyodyne-ifd.435.59: adds section 8 on signing in to a remote server with OAuth: sign_in stated per url server, client id from configuration or self-registration only where offered, PKCE, a 127.0.0.1 listener for one answer, tokens stored only on success, where each token may go, renewal under a lock, a refused renewal told once under the credential reason, the command shown on status, doctor and the dashboard, and Slack''s case (the operator''s own internal app; the Claude Code Slack client as a stopgap declined). Section 1 names sign_in, section 5 drops the word the operating rule retires, build pieces 10 and 11 are added. Written from a read at main 78786b3a.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-07T01:05:29.109979Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2376, for document-2376.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-07T05:27:21.973046Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2400, for document-2400.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-10T06:56:57.220875Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2551, for document-2551.1 under the automatic approval policy
---
# MCP tools through the harness

## What this is for

The Model Context Protocol, MCP, lets a program offer named tools to a client. Today no role can use one: every Claude Code role starts with MCP servers off, read-only Codex roles ignore the account's configuration, and the Codex developer gap is being closed (yoyodyne-ifd.435.39). Mason directed on November 6, 2026: the right approach is not to give agents MCP capabilities, but to let agents ask the harness to use specific configured MCP capabilities.

This design says how. It serves the goal to keep roles, policies and provider selection configurable without making safety invariants optional. It must not break independent review, isolated worktrees, the traceable chain, or running nearly autonomously.

Mason's rule, kept whole:

- no agent holds an MCP server, a connector, or its credentials;
- a project's configuration names the servers and the specific tools each agent may ask for;
- an agent asks, and the harness checks the ask, makes the call, records who asked, what, and what came back, and hands the result back as data, never as instructions;
- what an agent may use is set once in configuration, and no call is put to the operator.

## What it rests on, and what was not read

Read at commit 88bff330: [the tool interface](tool-interface.md), [portable agent configuration](portable-agent-configuration.md), the invariant [configuration never grants authority](../decisions/invariants/configuration-never-grants-authority.md), the guides on operators and on writing a configuration, and `go.mod`. Read as delivered in this conversation's briefing: [developer sub-agents](developer-sub-agents.md), [pinned outside files](pinned-outside-files.md), [project dependencies in runs](project-dependencies-in-runs.md), and the provider guide's section on unexpected tool calls. Read at commit 52f8379d, for the changes it asks of this design: [scheduled message intake](scheduled-message-intake.md). This design itself was read whole at commit 78786b3a before section 8 was added.

No code was read: not the conversation loop, the code that resumes a developer session, the adapters, or the secret stores. The official Go SDK for MCP was not read either. A build piece reads the code it changes first. Where that code does something this design does not account for, the piece stops and asks the architect.

Section 8's facts about Slack were not read from Slack's documentation by the architect. They come from what the Lead Product Manager reported from that documentation on November 9, 2026: Slack offers no way for an app to register itself, and gives a user token to a desktop client only through an app registered with the workspace, with PKCE turned on. Build piece 10 confirms them first.

## The decision in one paragraph

One package, `internal/mcpcall`, is the only code that speaks MCP. Everything reaches it through one function that takes a caller, a tool, and arguments. It checks the caller's list, records the ask, makes the call in the harness's own process, scrubs and bounds the result, records the outcome, and returns the result as untrusted data. A conversation role asks with a block in its reply. A developer asks by ending its turn with the same block and is resumed in the same session. A recurring task can be a caller too. No provider session ever sees a server. Where a remote server wants a person to sign in, the operator does it once at his own terminal, and the harness keeps and renews the tokens itself.

## 1. The configuration

```yaml
mcp:
  servers:
    mail:
      command: [mail-mcp, --stdio]   # a local program the harness starts
      login: [mail-mcp, login]       # optional; yoyo mcp login runs it
      tools:
        search: read
        fetch: read
        send: act
    tickets:
      url: https://mcp.example.com/  # a remote server
      sign_in: token                 # or oauth; see section 8
      credential: file               # or keychain; never a path
      tools:
        list_open: read

agents:
  product-manager:
    mcp_tools: [mail.search, mail.fetch, tickets.list_open]
```

- **A server** has exactly one of `command` or `url`. A `url` must be https. Redirects stay on the same host, at most five, and a credential is never sent to any other host.
- **A server with a `url` states `sign_in`,** either `token`, where the operator puts a token in the store himself, or `oauth`, where the harness signs in as section 8 says. One that states neither is refused when the configuration loads.
- **`tools` names every tool of that server the project will ever use, each marked `read` or `act`.** `read` means the call changes nothing outside the harness; `act` means it may. The project states this, because what a server says about its own tools is untrusted. A tool the server offers and this list does not name cannot be called by anyone.
- **`agents.<name>.mcp_tools` lists exact names, written as server and tool.** There are no wildcards, and the default is empty, so a project that says nothing has no MCP calls at all.
- **Credentials are named by store and never by path.** `credential` is `keychain`, the item `yoyo-mcp.<product id>.<server>` under the account `yoyo`, or `file`, `<server>.token` under the project's state directory in `mcp/`, readable only by the user the harness runs as. This follows `dashboard_token`. A free path was rejected because one line of configuration could then send any file on the machine to a remote server.
- **A local server keeps its own login in its own directory.** It runs with `mcp/<server>/` under the project's state directory as both its working directory and its home, so its login files are there and nowhere an agent can read them. `credential`, where set for a command, is passed in the environment variable `credential_env` names.

**Who may set it.** All of this is the operator's, as an account is. The typed write of section 6 of portable agent configuration refuses `mcp` and `mcp_tools`, and a developer's change cannot touch `.yoyodyne/`. A role may recommend a tool. The operator sets it once, and nothing is asked call by call.

**A credential that is missing** is a step only a person can take, under the reason `credential`. `yoyo doctor` reports it, one report names the server and the command to run, and calls to that server are refused with that reason meanwhile. Nothing stops. `yoyo mcp login <server>` runs the declared login at the operator's terminal, or for a server with `sign_in: oauth` signs in as section 8 describes, and is refused to an agent's shell. Each machine has its own credentials, so on a machine with no login for a server the calls to it are refused the same way.

**Why this does not break the invariant.** The invariant says configuration selects and narrows among registered actions, and research already shows the shape: configuration supplies where, never whether. Here the capabilities `mcp.read` and `mcp.act` exist only in Go, and a role whose bundle lacks one gets nothing from any list. The bundles, in Go:

| Role | Holds |
| --- | --- |
| Lead Product Manager, architect, development manager | `mcp.read`, `mcp.act` |
| program manager | `mcp.read` |
| developer | `mcp.read` |
| reviewer | neither |
| message reader | neither |

The message reader is the role with no tools of any kind that [scheduled message intake](scheduled-message-intake.md) defines.

A list naming a tool whose class the agent's role does not hold is refused when the configuration loads, naming the agent, the tool, and why. So is a list naming a server or tool the `mcp` section does not declare.

**A developer holds read tools only** in the first build. An act taken during a run happens before review, and it could not be undone if review then refuses the change. A later revision may widen this, for a reason somebody can state.

**A run of an item started from a message holds no MCP tools,** whatever the agent's `mcp_tools` says. Its evidence holds text from outside, and that text must not be able to send anything out through the arguments of a tool. Section 3 of the intake design says why.

## 2. How a conversation role asks

`mcp.read` and `mcp.act` are two tool descriptors under the tool interface, sharing one block:

```yoyodyne-mcp
{"requests":[{"tool":"mail.search","arguments":{"query":"invoice"},"id":"a1","why":"what this settles"}]}
```

`arguments` is one JSON object of at most 16 KiB. `id` is made up by the asker, is required for an `act` tool, and is never reused. `why` is optional and is recorded. The result comes back in the same message, framed as untrusted evidence, as a repository read does.

**Limits.** At most 5 calls a reply, 4 rounds a message, 32 KiB returned from one call and 64 KiB from one reply, and 60 seconds for one call. An agent's `tools:` block may tighten these and never widen them. The rounds count against the one budget for each paid message, under guarantee 4 of [one owner for each guarantee](one-owner-for-each-guarantee.md).

**Refusals** are handed back in the same message, and nothing is called. The reasons are a closed list:

- the tool is not on this agent's list;
- the arguments are malformed or over their size;
- the arguments contain a value the harness holds as a secret; such a call is refused rather than scrubbed, because silently changing what a call says changes what it does;
- a limit is spent;
- the operator's pause, set by `yoyo pause`, is on;
- the credential is missing;
- an `act` call has no `id`, or reuses one with different content.

A refusal never lists the tools the agent may not use.

**What an agent is told.** The generated contract section lists only the tools the agent holds. Each shows its name, its class, and its parameter names and types from the server's listing. The server's description follows, cut at 500 bytes and marked as untrusted text from the server. Every listing's digest is kept, and a digest that changes since the last call files one note for the development manager.

## 3. How a developer run asks

A developer's sandbox stays exactly as it is. The developer ends its turn with the same `yoyodyne-mcp` block. Before treating that reply as the attempt's answer, the harness carries out the block and resumes the same session within the same attempt, with the results as the next message.

- No repair attempt is spent, because nothing about the change was judged.
- At most 8 such rounds and 20 calls an attempt. Past that the ask is refused and the developer is told to go on without it.
- The developer keeps its slot, and the run's time limits are unchanged. Each resumed turn is an ordinary paid turn, counted to the run, and the budget decides it.
- Only the session's own final reply can ask. A sub-agent cannot, and a developer that needs a result asks for it itself.
- A reply with this block is never read as the developer's final account.
- A turn that also left background work running is handled as that rule already says, first, and the block is carried out on the reply that follows.
- A session that cannot be resumed stops as any failed resume does today.
- This works the same on Claude Code and on Codex, because both resume a session.
- The developer is told of the block only where it holds a tool, so every other run's instructions are unchanged.

## 4. Making the call, what is recorded, and what is kept out

**One way in.** `internal/mcpcall` takes a caller, a tool, arguments, and the `id`. A caller is one of:

- an agent in a conversation, with its turn or pass;
- a developer, with its run and attempt;
- a recurring task, by its name.

Each caller brings its own list. The rest of the path is the same for all three.

**The client is our own, and small, in the first build.** It speaks only what a client needing these calls speaks: the opening exchange, listing tools, and calling a tool, over standard input and output for a command, and over https for a url. It declares no client abilities, and any request a server makes back, such as asking a model or asking a person, is refused. Protocol versions it accepts are one list in code. The official Go SDK was considered and is not used yet: `go.mod` holds one dependency today, a developer run has no network to fetch a new module, and bringing one in would first need a step only a person can take. Everything sits behind the one package, so the SDK can replace the client later with nothing else changing.

**A server runs for one batch.** The harness starts a command server for the calls in one reply, in its own process group, with an environment built from a list and not inherited, under the time limit, and ends the group afterwards. A url server gets a fresh session for each batch. Nothing stays running, so there is no new part to supervise or move onto a new build. A command server runs outside every sandbox as the harness's user, with the same trust as a declared check or research source, which is why only the operator declares one.

**The record** is one entry for each call, written through the confined writer under the project's state directory, only ever added to. It is written before the call and completed after it, and holds:

- who asked: the caller, role, agent, and its turn, pass, run or attempt;
- when it started and ended;
- the server, the tool, its class, the configuration revision, and the listing's digest;
- the arguments with secrets removed, and the `why`;
- the outcome: performed, refused with its reason, failed with its reason, or unknown;
- the result as handed back, its whole size, its digest, and what was cut.

Up to 256 KiB of a result is kept in the record; 32 KiB reaches the agent. It stays on this machine and is never copied into the tracker, Slack, or the dashboard, which show names, outcomes and sizes. `yoyo mcp calls` reads it.

**Two exceptions,** both from [scheduled message intake](scheduled-message-intake.md):

- A recurring task that is an intake query records the call's size, digest and what was cut, and not the result. The result is held in memory only until the messages in it are written down, so `yoyo mcp calls` shows a query's calls with no content (section 4 of that design).
- Where a query's questions are reserved for the operator with `questions: person`, what reaches him may carry a link to the source message and one line of at most 300 bytes with secrets removed, marked as written by the AI reader from the message and not checked. It goes only by direct message, or on the line listing what needs him where Slack is not in use, never to a channel, and it is delivered to no role's briefing (section 7 of that design).

**Secrets.** Before a result is recorded or handed back, it is scrubbed of the values every path to a provider is scrubbed of, and of this project's MCP credentials. Credentials never enter a prompt, a record, or a session's environment. A server's error output is scrubbed the same way.

**Bounds on content.** Text and JSON pass through. An image or other binary content is dropped, and the result says so with its type and size.

**Failures, and never calling twice.**

- A network failure on a `read` call is tried once more. If it still fails, the result says so, and nothing stops: a flaky network ends no run.
- An `act` call is never retried. If the harness cannot tell whether it happened, including after a restart that finds an entry with no outcome, the outcome is unknown, and the agent is told it may have happened and to check before asking again.
- A repeat with the same `id` and content returns the recorded outcome and calls nothing.

## 5. The reviewer's independence

The reviewer holds no MCP tools. A call could change something, and a second call could return something different, so a reviewer repeating one would prove little. Instead, the reviewer's evidence includes the record of every call the run made: the tool, the arguments, and the result the developer received, labelled as outside data the harness fetched and nobody verified.

- What the developer says a call returned is judged against that record.
- A change resting on a result that was cut, or that the record does not hold, may be sent back on that ground.
- These records have their own share of the fixed 768 KiB review limit, at most 64 KiB, with any cut declared. The whole record stays readable with `yoyo mcp calls`.
- A call result is never evidence that a required check passed. It is not a check result and satisfies no configured check, under [the rule on evidence bound to the reviewed revision](../decisions/invariants/integration-requires-revision-bound-evidence.md).

## 6. Grants and the unexpected tool use check

- `mcp.read` and `mcp.act` are rows in the role bundles, the authority inventory, and the tool registry. `yoyo config show` reports each agent's `mcp_tools` beside its capabilities.
- Calls the harness makes appear in no provider stream, so the check on unexpected tool use neither sees nor counts them. They are recorded as tools are, with `tool.requested`, then `tool.performed` or `tool.refused`, on the conversation, the pass, or the run.
- An MCP call seen in a provider's own stream stays what it is today: access no role was launched with, filed as a critical report. That report now means the rule below was broken.
- Every adapter launches every role with no MCP server, whatever the `mcp` section says. A test launches each role on each provider with servers configured and credentials stored, and finds no server, connector or credential in its arguments, environment or home. That test depends on the Codex developer fix (yoyodyne-ifd.435.39).

**An invariant is warranted.** A correct change to an adapter could turn a server on and break this rule from outside this design. It is recorded separately, as: no agent session holds an MCP server, a connector, or its credentials, and every MCP call is made by the harness's one package, checked against the caller's configured list and recorded.

## 7. A call on a schedule

A recurring task can be a caller, so the harness can make a configured call with no agent asking. Its list is declared with the task, in the same exact names, under the same operator rules. A scheduled caller may hold `read` tools only, until a design says otherwise. Limits, scrubbing and the operator's pause apply unchanged, and so do records, apart from the first exception in section 4.

This design builds only that path and a test driving it. What is queried, how often, what counts as new, and who receives it are settled by [scheduled message intake](scheduled-message-intake.md).

## 8. Signing in to a remote server

Many remote servers want a person to sign in through a web page rather than be handed a token. This section says how the operator does that once, at his own terminal, and how the harness keeps the result without any agent seeing it.

**What the configuration says.** A server with `sign_in: oauth` is signed in to under the OAuth standard the MCP specification names, always with PKCE, and its tokens are kept in the store its `credential` names, as section 1 says. It may also name:

- `client_id`: the identity of an app the operator registered with the service. A client id is public, so it may be written in configuration. A client secret is never used and never written there.
- `scopes`: the permissions asked for. Where absent, the server's published default is asked for.
- `redirect_port`: a fixed port on this machine where the answer arrives, for services that check that address exactly, as Slack does. Where absent, the system picks a free port.

```yaml
mcp:
  servers:
    chat:
      url: https://chat-mcp.example.com/   # the service's MCP address
      sign_in: oauth
      client_id: "111.222"                 # the operator's own app; public
      redirect_port: 3118                  # where the service checks it exactly
      credential: file
      tools:
        search_messages: read
```

**Where the client id comes from.** The harness never assumes a server registers clients itself. Where `client_id` is named, it is used. Where it is not, the harness registers itself with the service only where the server's published details say the service allows that. Otherwise signing in stops: it is a step only a person can take, under the reason `credential`, and the message names the `client_id` setting and says that an app has to be registered with the service.

**Signing in.** `yoyo mcp login <server>` runs at the operator's terminal and is refused to an agent's shell, as section 1 says. For a server with `sign_in: oauth` it:

- prints the link and opens it in the browser;
- listens on 127.0.0.1 only, for one answer, and stops after 10 minutes or an interrupt;
- refuses an answer whose state value is not the one it sent;
- exchanges the code it receives for tokens, and only once that succeeds writes them, whole, to the store. Signing in that is not finished stores nothing and leaves any earlier tokens as they were.

**Where tokens go.** The access token is sent only to the server's host. The refresh token is sent only to the address that issues tokens, recorded at signing in, and that address must be https. Neither reaches a prompt, a record, a log, or a session, as section 4 says of every credential.

**Staying signed in.** The harness renews an access token that has expired or is about to, under a lock so that two calls never renew at once, and writes the new tokens whole.

- A service that issues a new refresh token on each renewal, each usable only once, is handled by writing the new one before the old one is let go.
- A token that never expires is used until the service refuses it.
- A renewal the service refuses means the operator is no longer signed in. Calls to that server are refused under the reason `credential`, the operator is told once, with the command to run, and nothing else stops.
- A renewal that fails because the network failed names nobody. It is tried again at the next call, as section 4 treats a flaky network.

**Telling the operator.** `yoyo status`, `yoyo doctor` and the dashboard show a server that needs signing in, with the one command to run. The dashboard offers it as a button that starts the same signing in on this machine. Slack never carries a link for signing in, because a link there can be followed from another machine, or by another person.

**Slack.** Slack offers no way for an app to register itself, and gives a user token to a desktop client only through an app registered with the workspace, with PKCE turned on. So for Slack the operator registers one internal app, once: it asks for user permissions only, has PKCE turned on, and has one fixed address on this machine for the answer. Its client id goes in the configuration. Until his workspace approves the app, calls to Slack are refused under the reason `credential`, and status says what is missing; nothing else stops.

Using Claude Code's own Slack client in the meantime was considered and declined. It would put a model between the call the harness decided on and the call actually made, cost a provider call for every Slack call, and break the rule that no agent session holds an MCP server, the rule Mason's direction of November 6 exists to keep. If the workspace refuses the app outright, rather than taking time, that choice is weighed again as a change to that rule with an end date, never as a quiet exception.

## Risks accepted, stated plainly

- **The list is the whole guard on what leaves.** An agent can put what it has read, repository content included, into the arguments of a tool it may call. Only known secret values are refused. Choosing tools is therefore a real decision about reach, which is why it is the operator's.
- **A result goes to the AI provider** serving that role, and is kept in that conversation's or run's records.
- **Text in a result can try to steer an agent.** Framing it as data lowers this and cannot remove it. Read tools alone for developers and scheduled callers are what limit the harm.
- **A role could copy a private result into the tracker,** whose export is in the repository. Role instructions say never to. The harness cannot tell, so this is left to instruction and is not enforced.
- **The claude.ai account's connectors are not reused.** Each service needs its own server and login, set up once.
- **A stored refresh token is a lasting key to the service.** It sits in the keychain or a file only the harness's user can read, as every credential here does. Anyone who can act as that user can use it.

## Alternatives rejected

- **Giving agents servers with narrow permissions.** It is what Mason ruled out: the session would hold the connection and the credentials.
- **A proxy the session reaches through a socket or a file.** It opens a hole in the sandbox, or makes a session poll, to save a resumed turn.
- **A server kept running under the supervisor.** It adds a part to move and restart for a start time nobody has measured. It can be revisited with a measurement.
- **One list for a role, shared by all its agents.** The conversation belongs to the agent, and agents are what configuration already names. The Go bundle is what holds for the whole role.
- **Trusting a server's own marks for which tools are read tools.** A server could say anything, and the harness relies on the mark.
- **Letting the reviewer call tools.** See section 5.
- **Asking the operator before each `act` call.** Mason ruled it out, and the autonomy goal does too.
- **Assuming every service lets a client register itself.** Slack does not, and a design that assumed it would leave the operator with an error and no setting to fix it.
- **Sending a link for signing in by Slack.** It could be followed from another machine, where nothing is listening, or by somebody else.
- **Claude Code's own Slack client until an app is approved.** See section 8.

## Documents this changes

- [The tool interface](tool-interface.md): its deferred line on operator tools gains this design as the answer for MCP, with the command coming from the operator's own file, as research's does.
- [Portable agent configuration](portable-agent-configuration.md): section 6's refused list names `mcp` and `mcp_tools`.
- The operator's guides gain the setup, under build piece 7, and signing in, under build piece 11.

## Build pieces, for the Lead Product Manager to admit

1. **The configuration.** The `mcp` key and `mcp_tools`, every refusal at load, the refusal in the section 6 write, `yoyo config show`, `yoyo doctor`, and `yoyo mcp login`.
2. **The client.** `internal/mcpcall`'s speaking of the protocol over both transports, one batch at a time, the environment built from a list, credentials from the stores, refusing what a server asks back, tested against a small fake server in the repository.
3. **The call path.** The one way in with its three kinds of caller, the checks, the record, scrubbing, bounds, the outcomes and repeats, the operator's pause, a test driving a scheduled caller, and a check in `make test` that no other package speaks MCP. Needs 1 and 2.
4. **Conversation roles.** The two descriptors, the bundle and inventory rows, the block, the generated contract section, and refusals handed back. Needs 3.
5. **Developer runs.** Carrying out the block at the end of a turn and resuming the same session, with its bounds, on both providers. Needs 3.
6. **The reviewer's evidence.** The call records in the review, within their share. Needs 5.
7. **Surfaces and guides.** `yoyo mcp calls`, counts through the read model on `yoyo status` and the dashboard with no content, the note on a changed listing, and the setup guide. Needs 3.
8. **Containment.** The test across every role and provider in section 6. Needs yoyodyne-ifd.435.39.
9. **The first real server.** The development manager tries one read tool end to end from a conversation and from a developer run, and records what happened. The operator's only part is the login, once.
10. **Signing in and keeping the tokens.** `sign_in` and its refusal at load, `client_id`, `scopes` and `redirect_port`, registering with the service only where its published details allow it, the 127.0.0.1 listener with its one answer and ten minutes, the state check, writing tokens only on success, and where each token may be sent, tested against a fake service in the repository that issues tokens. It first confirms, from Slack's documentation and one real attempt by the operator, that Slack accepts an answer address on 127.0.0.1 at a fixed port; where it does not, the piece stops and asks the architect. Needs 1 and 2.
11. **Renewal and what the surfaces show.** Renewal under a lock, refresh tokens issued anew on each renewal, tokens that never expire, a refused renewal told once under the reason `credential`, a network failure naming nobody, the command on `yoyo status`, `yoyo doctor` and the dashboard with the dashboard's button, and the guide on signing in. Needs 10 and 7.

Pieces 1 and 2 can start at once and side by side. Pieces 4, 5 and 7 can go side by side after 3, and 10 can go beside them. The two exceptions in section 4, the message reader's row, and launching a run started from a message with no MCP tools are built under the intake design's own pieces.

## What is enforced in code

- Only `internal/mcpcall` speaks MCP.
- No adapter gives a session a server, a connector, or a credential.
- Every call passes the caller's list, and no list names a tool whose class the caller may not hold.
- A run of an item started from a message is launched with no MCP tools.
- The class of a tool comes from the project's declaration, never from the server.
- A role's write cannot change `mcp` or `mcp_tools`.
- A record is written before each call and completed after it, and only ever added to.
- An `act` call is never retried, and a repeat with the same `id` calls nothing.
- Results are scrubbed, bounded, and framed as untrusted before they reach a prompt.
- A server with a `url` that states no `sign_in` is refused at load.
- Signing in listens on 127.0.0.1 only, accepts one answer whose state value matches, and writes tokens only once the exchange succeeds.
- The access token goes only to the server's host, and the refresh token only to the recorded https address that issues tokens.
- Renewal holds a lock, and no link for signing in is sent to Slack.

Left to role instructions: what to ask for, and not copying a private result into the tracker.

## What a reviewer can check

- With no `mcp` section, every launch, instruction and record is exactly today's.
- A tool the server offers and the list omits is refused, and the refusal names no other tool.
- A credential appears in no prompt, record, log, or session environment.
- A configuration giving a developer or a reviewer an `act` tool is refused at load, naming both.
- A credential stored as `file` cannot be read from any path but the one derived from the server's name.
- A developer's ask spends no repair attempt, resumes the same session, and stops at its bounds.
- A developer run of an item started from a message has no MCP tools, even where the agent's list names some.
- A killed process leaves an `act` entry that reads as unknown, and calling again with that `id` calls nothing.
- An MCP call in a provider's own stream files the critical report.
- The reviewer's evidence holds the record of each call, and its cuts are declared.
- A scheduled caller makes a read call and is refused an `act` call.
- An intake query's call appears on `yoyo mcp calls` with its size and digest and no content.
- A server with a `url` and no `sign_in` is refused at load.
- With no `client_id` and no self-registration offered, signing in stops under the reason `credential`, naming the setting.
- Signing in that is interrupted, times out, or receives a wrong state value stores nothing and leaves earlier tokens untouched.
- Two calls that find an expired token at once renew it once.
- A refused renewal refuses calls to that server under the reason `credential`, tells the operator once, and stops nothing else.
- No Slack message the harness sends carries a link for signing in.