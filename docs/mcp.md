# Using MCP servers

MCP, the Model Context Protocol, is a way for a program, called an MCP server,
to offer named tools: search a mailbox, list open tickets, send a message. This
guide is for an operator who wants the agents in a Yoyodyne project to be able
to use some of those tools. It says how to declare a server, how to choose the
tools each agent may ask for, how to log a server in, and what it means for
private data when a tool's result is sent to an AI provider.

Which servers and tools a project uses, and what they do, are yours to choose.
Yoyodyne ships no server and turns none on. It ships one worked example,
[`docs/examples/mcp.yaml`](examples/mcp.yaml), to copy and adapt; nothing loads
it, and a project that copies none of it has no MCP servers at all.

The full rules for each key are in
[the configuration guide](configuration.md#mcp-servers-and-the-tools-each-agent-may-ask-for),
and the reasons behind them in
[the design](designs/mcp-tools-through-the-harness.md).

## How it works

No agent ever holds an MCP server, a connection to one, or its login. An agent
asks the harness for a call by name; the harness checks the name against the
list you gave that agent, writes the call down, makes the call itself, removes
known secrets from what comes back, and hands the result to the agent marked as
outside data rather than instructions. Every agent is started with no MCP
server, whatever your configuration declares.

**What this build does.** It loads and checks the settings below, logs servers
in, keeps the record of calls, and shows that record. The steps that let a role
in a conversation, or a developer during a run, ask for a call are still being
built, so in this build no agent asks for one yet. Everything you set up now is
what those steps will use.

## 1. Declare a server

Add an `mcp` section to `.yoyodyne/config.yaml`. Each server has a name you
choose, how to reach it, and every tool of it your project will ever use:

```yaml
mcp:
  servers:
    mail:
      command: [example-mail-mcp, --stdio]
      login: [example-mail-mcp, login]
      tools:
        search: read
        read_message: read
        draft_reply: act
        send: act
```

- **How to reach it.** `command` is a program the harness starts on this
  machine, once for each batch of calls, and stops afterwards; it runs as your
  user, outside every sandbox, with its own folder under the state directory
  as its working directory and its home. `url` is a server somewhere else, at
  an `https` address with no user name or password in it. A server has one or
  the other.
- **Its tools, each marked by you.** `read` means a call changes nothing
  outside the harness; `act` means it may, such as sending a message. You mark
  them, not the server, because what a server says about its own tools cannot
  be trusted. A tool the server offers and you leave out cannot be called by
  anybody.

## 2. Choose each agent's tools

Each agent gets its own list, under its block in `agents`, of exact names
written as server and tool:

```yaml
agents:
  product-manager:
    mcp_tools: [mail.search, mail.read_message, mail.draft_reply]
  developer:
    mcp_tools: [mail.search]
```

There are no wildcards, and an agent with no list has no tools. What a role may
hold at all is fixed in the harness, and no line of configuration widens it:

| Role | May hold |
| --- | --- |
| Lead Product Manager, architect, development manager | `read` and `act` tools |
| program manager | `read` tools only |
| developer | `read` tools only, because an act taken during a run would happen before review |
| reviewer | none |

The configuration is refused when it loads if a list names a server or tool the
`mcp` section does not declare, or a tool whose mark the agent's role may not
hold, and the refusal names the agent, the tool, and why. `yoyo config show`
prints each agent's tools beside its other tools.

Choosing a tool is a decision about reach. An agent can put what it has read,
your repository included, into the arguments of any tool on its list, and only
values the harness already knows to be secret are refused. Give each agent the
tools its work needs and no more.

## 3. Log the server in

Each machine keeps its own logins, so this is done once on every machine that
runs the project.

- **A server that declares `login`:** run `yoyo mcp login mail` at your own
  terminal. It runs the declared login with the server's own folder as its
  working directory and its home, so the files it writes stay where the server
  will find them and where no agent can read them. It is refused from a process
  the harness started for an agent.
- **A server whose credential you store by hand:** declare `credential: keychain`
  or `credential: file`, never a path. `yoyo mcp login <server>` then prints the
  exact command that stores it: a keychain item named
  `yoyo-mcp.<product id>.<server>` under the account `yoyo`, or a file
  `<server>.token` in the `mcp` folder of the product's state directory,
  readable by your user alone. A server the harness starts is handed that
  credential in the environment variable its `credential_env` names.

`yoyo doctor` names every server whose credential is missing on this machine and
the command to run. Until it is stored, calls to that server are refused with
that reason, and nothing else stops.

Both the `mcp` section and every agent's `mcp_tools` are yours alone: a role
asking to change its configuration is refused for either.

## 4. What a result sent to a provider means for private data

When an agent asks for a call, what the server returns goes to that agent, and
the agent is an AI model run by a provider, such as Anthropic or OpenAI. So:

- **The result is sent to that provider**, as part of that agent's
  conversation or run, and is kept in the provider's records and in the
  harness's records of that conversation or run. Treat a tool that returns
  private data, such as your mail, as sending that data to the provider. If
  your agreement with the provider does not allow that data, do not put the
  tool on any list.
- **Known secrets are removed first.** Before a result is recorded or handed
  over, the harness removes the values it already holds as secret, including
  every MCP server's credential, and anything shaped like a key or a token. It
  cannot recognize private text that is not a secret, such as the body of a
  message.
- **The record stays on this machine.** The harness keeps up to 256 KiB of each
  result, under the product's state directory, so you can see what an agent was
  given. It is never copied into the work tracker, Slack, or the dashboard,
  which show only tool names, how each call ended, and counts.
- **An agent could still copy what it read into something it writes.** The
  harness cannot tell when an agent puts a private result into the work
  tracker, whose export is committed to the repository. The roles are to be
  told never to, in the instructions that come with the step that lets them
  ask, and that instruction will be the only guard on it.
- **A result can try to steer the agent.** It is marked as outside data, never
  instructions, which lowers this and does not remove it. That is part of why
  developers hold read tools only.

## 5. See what was called

- `yoyo mcp calls` lists every call in the record: when it started, which agent
  asked and in which conversation or run, the tool, and how it ended, with the
  size of what came back. It shows no arguments and no result. `--since 24h`,
  `--tool mail.search`, and `--unknown` narrow the list; `--unknown` is the act
  calls whose outcome nobody knows, which may have happened and should be
  checked before anybody asks for them again.
- `yoyo mcp calls --entry <entry>` prints one call whole, with its arguments and
  its result as kept, and `--json` prints every listed call that way. Both are
  refused from a process the harness started for an agent, because a result is
  for the agent that asked and for you.
- `yoyo status` and the dashboard show the last 24 hours of calls by tool and
  how each ended, with nothing a call sent or returned. A project that has made
  no calls shows nothing.
- When a server's list of its tools changes from the one it gave at its last
  call, the harness files one note for the development manager, naming the
  server. Agents are shown each tool's description from that list, and your
  `read` and `act` marks describe the tools as they were when you declared them,
  so a changed list is worth a look.

## The worked example

[`docs/examples/mcp.yaml`](examples/mcp.yaml) declares one invented server and
gives a list to an agent of every role. To use it, copy its `mcp` section into
your configuration as it stands, add each `mcp_tools` line to the agent of the
same name in your `agents` section, change both to your own server and tools,
and log the server in. A test under `make test` loads it into the configuration
`yoyo init` writes, so a change to the configuration that would refuse it fails
there first.
