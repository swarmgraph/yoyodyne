---
id: scheduled-message-intake
kind: design
title: 'Scheduled message intake: the harness reads a configured query, a reader with no tools chooses from written rules, and message text never changes the work'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-07T03:59:28.958883Z
      reason: 'yoyodyne-ifd.435.41: Mason''s direction of November 6 that simple automations start from messages matching a scheduled query, under rules kept as a readable file. Answers the item''s nine points and the program manager''s request in report-4421ebadf0e6485477581844fb76e62b: a possible rule match is not a reason to name the operator, an unanswered question holds only its own message, and it ends as ignored after seven days. Names eight build pieces. Written from reads at main 52c181fa. Rewritten after the word check refused the earlier copy, which was never recorded.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-07T03:59:28.958883Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2390, for document-2390.1 under the automatic approval policy
---
# Scheduled message intake

## What this is for

Mason asked on November 6, 2026 that Yoyodyne serve simple automations, such as a bug report in Slack becoming a reviewed fix and a pull request waiting for him to merge. His words: we would run a query every hour, and messages that match would go to an agent that ignores them if they are obviously outside its rules, starts a workflow if they match one of its rules, and messages him for clarification if they might match.

This serves the goal to run development nearly autonomously, and the measure of useful software per hour of the owner's attention. It must not break the traceable chain, independent review, or the rule that intent is redefined only by whoever owns it. The plain-language and autonomy goals apply to everything below.

## What it rests on, and what was not read

Read at commit 52c181fa: [MCP tools through the harness](mcp-tools-through-the-harness.md), [who owns what](ownership-and-the-operator.md), [the artifact contract](artifact-contract.md), [configurable workflows](configurable-workflows.md), [working alongside other contributors](working-alongside-other-contributors.md), and the invariants [no agent session holds an MCP server](../decisions/invariants/no-agent-session-holds-an-mcp-server.md), [the operator owns only what only he can do](../decisions/invariants/the-operator-owns-only-what-only-he-can-do.md), and [selected work passes intake and records why](../decisions/invariants/selected-work-passes-intake-and-records-why.md).

Not read: any code, [the Slack design](slack-reporting-design.md), [follow-ups](follow-ups.md), the item on checking requests to the operator (yoyodyne-r3xa), section 6 of [portable agent configuration](portable-agent-configuration.md), how the tracker searches, and how each adapter could launch a role with no tools at all. The build piece that depends on one of these reads it first. Where it does not fit what is written here, the piece stops and asks the architect.

## The decision in one paragraph

The harness runs a configured read tool on a schedule and writes down each new message before anything else happens. The harness itself drops any message whose sender is not listed. A role with no tools, the message reader, sees one remaining message and the written rules, and can only answer ignore, start a named rule, or ask. Everything about the work it starts comes from the rule and nothing from the message, and the work then goes through the same admission, checks and review as any other. The worst a hostile message can do is start work a rule already allows, within a daily limit.

## 1. The query and its limits

A query is declared in the project's configuration:

```yaml
intake:
  queries:
    slack-bugs:
      tool: slack.search
      arguments: {query: in-bugs}
      since_argument: after
      every: 1h
      items: messages
      fields: {id: ts, sender: user, text: text, sent_at: ts, link: permalink}
      senders: [U123, U456]
      reader: message-reader
      rules: intake-rules
      questions: lead-product-manager
      limits: {messages_per_run: 20, messages_per_day: 100, starts_per_day: 5, questions_per_day: 5, text_bytes: 8192}
```

- **`tool`** must be a `read` tool declared under `mcp`. Section 7 of the MCP design gives a scheduled caller read tools only, and this query's tool is its list. An `act` tool is refused when the configuration loads.
- **`arguments`** are literals. **`since_argument`** names the one argument the harness fills in, with the time it last read up to, less ten minutes so that a late message is not missed. Section 2 makes reading a message twice harmless.
- **`items`** and **`fields`** say where the messages and their parts are in the tool's answer, by plain field names. No expression language is added, and no knowledge of any one service is built in.
- **All of `intake` is the operator's,** as `mcp` is. The typed write of section 6 of portable agent configuration refuses it, and a developer's change cannot touch `.yoyodyne/`.

**The schedule** is a recurring task under [configurable workflows](configurable-workflows.md): one instance for each window, recorded before anything is spent, skipped while the previous run of that query is still working. The default is one hour and the shortest allowed is five minutes. No query run holds a developer slot.

**The first run** reads only messages newer than the moment the query was first switched on. A source that cannot filter by time has what its first run returns recorded as ignored, with that reason and no call to a provider, so that switching a query on never spends money on old messages.

**What bounds the spend.** Each message costs at most one provider call, and two where the first reply cannot be read (section 3). The query call itself costs no provider money. Past `messages_per_run` or `messages_per_day`, messages stay recorded and unread, oldest first, and are read on a later run; none is dropped, and `yoyo status` says how many wait. The reader's model is chosen in configuration for this one task, and its spend is reported under its own kind, as the goal that spend follows the work requires. The operator's pause, set by `yoyo pause`, stops queries and readers alike, and budgets are committed before any spend.

## 2. Each message handled once

A message's key is the server's name and the source's own identifier for it. The key does not name the query, so two queries on one server that return one message produce one record. The first query to write the record handles the message under its own rules, and the record lists any other query that returned it.

The record is written through the confined writer under the project's state directory, in `intake/`, and is only ever added to. It is written before any provider call.

After a restart:

- a message with no decision is read again, which is safe because the reader changes nothing;
- a decision to start whose item is not recorded first looks for an item carrying the message key, and creates one only if there is none;
- the time the query has read up to moves on only after every message from that run is written down.

A message edited after it was recorded is not read again. Its record keeps the text as first read, and a changed digest is noted on the record. Replies in a thread are new messages, each judged alone. That may cost a useful reply being ignored, and it is accepted for the first build because the alternative lets one message reach into the judgment of another.

**Team mode.** Each machine keeps its own records, so two factories could both start one message. The first build refuses `intake` at load where team mode is on, and says why in plain words. A later revision can make the first decision shared, using a path named by the key in the records reference of the contributors design.

## 3. Message text stays data

**The message reader is a role with nothing.** It is a shipped bundle in Go with no repository, tracker, memory, MCP tool, or ask. It has one fresh session for each message and nothing carries over. A provider that cannot launch it with no tools cannot serve as its provider, and the configuration is refused.

It receives its fixed instructions, the rules at the recorded commit, and the sender, time and text of one message, marked as untrusted text from outside. Its reply is one block:

```yoyodyne-intake
{"decision":"ignore|start|ask","rule":"rule id, for start","candidates":["rule ids, for ask"],"reason":"why","question":"what is unclear, for ask"}
```

The harness carries out only that block:

- A rule the document at that commit does not hold is refused and counts as an unreadable reply.
- An unreadable reply is tried once more. A second one becomes a question the harness writes itself, saying the reader could not decide.
- The reader's `reason` and `question` are recorded as text derived from the message, and never as an instruction to anybody.

**Nothing in the message can change the work.** The goal, priority, type, label, title, done criteria and workflow all come from the rule. Someone writing ignore your rules can at worst change which of the three answers comes back. So a hostile message can start work a rule already allows, up to `starts_per_day`, or ask a question, up to `questions_per_day`, and that work is still sandboxed and reviewed.

**The text never enters the tracker.** The MCP design keeps call results out of the tracker, whose export is committed to the repository. A started item carries the rule's wording and the message key, and no text. When a run of that item starts, the harness adds the text from the machine's own record to the developer's and the reviewer's evidence, marked as untrusted text from outside, with secrets removed. Text can still try to steer the developer, as the MCP design's risks say. The sandbox, the rule's done criteria and the independent reviewer are what limit that. Role instructions say not to copy the text into a commit or pull request beyond what the fix needs, and the harness cannot tell, so this is not enforced.

## 4. Whose messages count

`senders` is required and lists exact identifiers as the source gives them. The harness compares them before any provider call. A message from anyone else is recorded as ignored with the reason sender not listed, with its key and sender but not its text. To take messages from anyone, a query must say `anyone: true`, which replaces the list and is shown on `yoyo config show`. The identifier is only as good as the server that reports it, which is one reason choosing a server is the operator's decision.

## 5. Started work passes the same admission

A decision to start admits one item through the harness's ordinary admission. The rule is the Lead Product Manager's standing decision to admit that kind of work, because she owns the rules. Admission is not skipped:

- Each rule names a goal by its identifier. A rule whose goal is not an active goal is refused, and a message that needs it waits. So a rule cannot admit work the goals refuse, and a rule needing a goal nobody has stated needs that goal first, which is a change of intent and is decided as one.
- The item's origin names the rule, the commit of the rules, and the message key, so the chain runs from merged change to goal.
- The usual checks on an item's readiness apply. An item they refuse ends the message as could not start, and files one report for the Lead Product Manager.
- While the intake hold or the intake brake is on, no item is admitted from a rule. The message records its decision and waits, and queries keep reading. Once admitted, the item is pulled by the scheduler like any other, so the hold is read again and the reason for selecting it recorded, as the invariant requires.
- A rule may not set priority 0, because a message from outside must not go ahead of reliability work without somebody deciding that.
- The reviewer judges the change against the item's goal and done criteria, and the standing goals. A change resting on what a message claimed gets no credit for the claim.
- A rule cannot change the project's merge policy. A pull request waits for a person where the project's policy says so, as section 1 of the contributors design describes. That is how Mason's example ends.

**Only the delivery workflow in the first build.** A rule names `workflow: delivery`, and any other name is refused. The configurable workflows design says the graph does not yet run anything, and that no other sequence can run until the graph takes over. A later revision widens this list when that has happened.

## 6. The rules file

The rules are a `rules` document at `docs/product/intake/intake-rules.md`, and a project may have more than one. A query names its document by id. For example:

```markdown
## Rules

- [slack-bug-report] A person reports something in the product that is broken. Not a feature request, and not a question about how to use it.
  - workflow: delivery
  - goal: nearly-autonomous
  - priority: 1
  - type: bug
  - label: bug
  - title: Fix the problem reported in a message
  - done: The reported behaviour no longer happens, and a test shows it.
  - from: the direction this rule came from
```

**Owner.** The Lead Product Manager, because deciding what work may enter the backlog is hers. Any other role proposes a change and waits for her decision. Mason said the rules are the agent's, and the reader does follow them, but it cannot change them. A reader that could change its own rules would be taken over by the first message that asked it to.

**How a change is reviewed.** It takes the document route in the artifact contract: the word and link checks, confirmation under the three cases for her documents, then a reviewed run changing only that file. A rule that names an existing goal does not change what the goals admit, so she can mark it consistent and the operator is not asked.

**What the harness reads.** The file as the target branch holds it at the start of each query run, never the working tree. The commit is recorded on each message. A document that cannot be read as rules stops that query's readers and files one report for her. Messages wait and nothing is lost. A malformed rule refuses that rule and the others still apply.

These rules are not read as instructions for every role, which is what the contract says of a rules document. Section 6 of this design is the exception, and the contract gains a sentence saying so.

## 7. Questions, and why the closed list does not grow

**A possible match is not a reason to name the operator, and this design adds no reason to the closed list.** The list holds what only a person can do. Whether a message falls under a rule is a judgment about the rules, and the rules are the Lead Product Manager's. Adding a reason would also let anyone on a senders list put work in front of the operator, which is the opposite of the autonomy goal.

**So questions go to her by default.** With `questions: lead-product-manager`, a question appears on her list with the message framed as untrusted text. She answers with one typed action, `intake.answer`, naming the message key and either ignore or a rule. She is also the one who can improve the rule so the question stops arising.

**Mason asked to be messaged, and he can have that.** With `questions: person`, a query's questions are reserved for him. Only he can set this, since a role's write refuses all of `intake`. It uses the existing reason `human-gate`: a step reserved for a person, whose wording widens from an item to also cover a query. It is his own choice to spend his attention this way, made once, and he can undo it by changing that line. The harness never chooses it.

**What he sees.** The harness writes the question, not the reader: the query, the sender, the rules that might apply, and a link to the source message. One further line of up to 300 bytes carries the reader's question, with secrets removed, marked as written by the AI reader from the message and not checked. This is the one exception to the MCP design's rule that nothing from a result reaches Slack. It goes only by direct message, or on the line listing what needs him where Slack is not in use, and never to a channel. `yoyo intake show` gives the whole text at his terminal. The request passes the checks for requests to him (yoyodyne-r3xa).

**The answer.** He answers with `yoyo intake answer <key> ignore` or `yoyo intake answer <key> start <rule>`. The piece that adds answering from Slack reads the Slack design first. An answer picks only from ignore and the rules the document holds now, so it cannot start work no rule allows. Starting then goes through section 5. Anything further he says about the rules goes to the Lead Product Manager as any direction does.

**An unanswered question holds only its own message.** It holds no query, no other message, and no developer slot. Its wait is recorded as a follow-up with these deadlines, whoever owns it:

- **After three days,** if the rules have changed since the message was read, the reader reads it again, and a clear answer ends it. If the question still stands, it stays open.
- **After seven days,** it ends as ignored, with the reason question unanswered, and the question is withdrawn. Nothing is reported for this beyond the count on `yoyo status`, because a person not answering is an answer of a kind. A late answer can still start the work.

**The limit on questions.** Past `questions_per_day`, a question waits unasked, and its seven days start only when it is asked. So a run of confusing messages cannot flood anyone.

## 8. What is recorded, and telling the sender

Each message has one record, kept on this machine and never in the tracker, holding:

- the key, every query that returned it, the sender, the time, the link, the text up to `text_bytes`, its whole size and digest;
- the rules commit, the reader's decision and reason, its model and cost;
- how it ended, with why: ignored, started with its item, could not start, or answered, with who answered and when;
- for a question, what was asked, of whom, and when.

`yoyo intake list` and `yoyo intake show <key>` read it. `yoyo status` and the dashboard show counts by how messages ended, through the read model, with no text.

**The sender is not told, in the first build.** Replying is an `act` call, and a scheduled caller may hold read tools only. A later revision could allow a rule to name one `act` tool, with fixed wording from the rule, no text from the message, and the message key as the call's `id` so that a repeat calls nothing. It would need a reason somebody can state, and it would change section 7 of the MCP design. Until then a sender sees what becomes of a report through the pull request or the fix, if at all, and the design says so plainly rather than promising more.

## 9. When the query or its server fails

- **A failed call** is tried once more, as the MCP design says for a read call. If it still fails, the time read up to does not move, and the next scheduled run tries again and covers the gap. Nothing stops, and a flaky network ends nothing.
- **An answer that was cut, or that does not hold `items` and `fields` as declared,** counts as a failed run. The harness calls no provider and marks nothing as read, because a cut answer could hide messages. The report says to narrow the query.
- **Three failed runs in a row** file one warning for the development manager, naming the query and the last failure. No more are filed until a run succeeds.
- **A missing credential** is refused under the reason `credential`, as the MCP design says, and is the only failure here that reaches the operator.
- **A provider that cannot serve the reader** leaves messages recorded and unread, to be read when it returns. It spends no question.

## Why no new invariant

The rules that could be broken from outside this design are already held: no session holds a server, every call goes through the one package, and the operator is named only through the registry. What remains lives in one package with its own tests. Should the reader's launch prove to be something an adapter change could widen, the containment test of the MCP design gains the reader as one more role, which holds it without a new constraint.

## Risks accepted, stated plainly

- A message on the senders list can start allowed work it should not, up to the daily limit, by being written to look like a rule's intent. The reviewer and the merge policy are what stop it landing.
- The text goes to the AI provider serving the reader, the developer and the reviewer.
- A useful message may be ignored by a wrong judgment. Ignored messages stay on record with their reasons, and an answer can still start one.
- Seven days of silence ends a question, so a real bug report can end as ignored if nobody looks. Choosing `questions: person` accepts that risk, and the default sends it to a role that does look.

## Alternatives rejected

- **An existing role as reader, such as the Lead Product Manager.** She holds the tracker, memory and acting tools, so a hostile message would reach all of them, and each message would cost her whole context.
- **Letting the reader write the item's title or description.** It would carry message text into the committed tracker export, and let a message shape the work.
- **A new reason on the closed list for a possible match.** See section 7.
- **Keeping the rules in configuration.** They state what work may enter, which is intent, and they would have no owner, revision log or review as intent.
- **Holding the query until a question is answered.** One silent person would stop every other message.
- **Telling the sender in the first build.** It needs an `act` tool on a schedule, which would let a message cause an outside action before any person or reviewer looks.
- **Reading messages through the existing recurring pass of a role.** A pass carries the role's whole briefing, and its memory would hold what messages said.

## Documents this changes

- [The MCP design](mcp-tools-through-the-harness.md): the table of role bundles gains the message reader, holding neither class. Its rule on Slack names the one line of section 7 as an exception.
- [Who owns what](ownership-and-the-operator.md): the table gains the kind for an intake question, owned as the query says, and the wording of `human-gate` covers a step a query reserved for a person.
- [The artifact contract](artifact-contract.md): the rules kind gains one sentence for documents under `docs/product/intake`, which are read by the message reader and not delivered to every role.
- [Portable agent configuration](portable-agent-configuration.md): section 6's refused list names `intake`.

## Build pieces, for the Lead Product Manager to admit

1. **The configuration.** The `intake` key, every refusal at load, including team mode and any tool that is not a declared read tool, the refusal in the section 6 write, and `yoyo config show`. Needs MCP piece 1.
2. **The rules document.** Reading rules from the target branch, refusing a malformed rule or an inactive goal, and the sentence in the contract.
3. **The query run and the record.** The scheduled task, the sender check, one record for each key, the time read up to, the first run, and the failures of section 9. Needs MCP piece 3 and pieces 1 and 2.
4. **The message reader.** The bundle with nothing, launched with no tools on each provider, its block, the retry, and the limits. Reads how adapters launch a role first, and adds the reader to the containment test. Needs piece 3.
5. **Starting work.** Admission from a rule, the key on the item, the search before creating, the holds, and the text added to the evidence of developer and reviewer. Reads how the tracker searches first. Needs piece 4.
6. **Questions.** The route to the Lead Product Manager and `intake.answer`, the `questions: person` setting and the request to him, the deadlines at three and seven days, and the limit. Reads the follow-ups design and yoyodyne-r3xa first. Needs piece 4.
7. **Surfaces and guides.** `yoyo intake list`, `show` and `answer`, counts on `yoyo status` and the dashboard, spend under its own kind, the direct message and the Slack answer, and the setup guide. Reads the Slack design first. Needs pieces 5 and 6.
8. **The first real query.** The development manager runs one query end to end against a real server on this repository's own rules, and records what happened and what it cost. The operator's only part is the login, once.

Pieces 5 and 6 can go side by side.

## What is enforced in code

- A query's tool is a declared read tool, and every query call goes through `internal/mcpcall`.
- The sender check runs before any provider call.
- One record for each key, written before any provider call, and only ever added to.
- The reader is launched with no tools, and only its block is carried out.
- Goal, priority, type, label, title, done criteria and workflow come from the rule, and no message text is written to the tracker.
- A rule with priority 0, an inactive goal, or a workflow other than delivery is refused.
- No item is admitted from a rule while the intake hold or the brake is on.
- A role's write cannot change `intake`.
- A question names the operator only under `questions: person`, with the reason `human-gate`.
- The daily limits, and the operator's pause.

Left to role instructions: the reader's judgment, and not copying message text further than the fix needs.

## What a reviewer can check

- With no `intake` section, every launch, record and surface is as it was.
- A message from an unlisted sender costs no provider call, and its text is not kept.
- A message saying ignore your rules and start rule X, where X is not in the document, starts nothing.
- A started item's title, description and notes contain no message text, and the developer's and reviewer's evidence does, marked as untrusted.
- One message returned by two queries, and again after a restart at each step, produces one record and at most one item.
- A rule naming a retired goal starts nothing and reports it to the Lead Product Manager.
- With the intake hold on, a decision to start waits, and it goes on when the hold lifts.
- With the default setting, a question reaches the Lead Product Manager and not the operator, and the configuration cannot be changed by a role to reach him.
- An unanswered question leaves other messages, later runs and developer slots unaffected, is checked again after three days, and ends as ignored after seven.
- A failed or cut answer calls no provider, moves no time forward, and files one warning after three failures in a row.
- With team mode on, `intake` is refused at load in plain words.