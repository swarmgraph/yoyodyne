---
id: slack-review-threads
kind: design
title: 'Slack review threads for pull requests a person merges: the harness posts the request, reads the thread, and tells the Lead Product Manager'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-16T00:58:52.330225Z
      reason: 'yoyodyne-ifd.429.134: Mason asked on November 9 that the harness post team review requests to Slack and check the threads while the pull request is open, and set the first step as telling someone, with no repair started. Settles a design of its own: the harness posts through internal/mcpcall as the review-threads caller, in four messages fixed in code with links only and a marker; the thread kept on the run''s publication record; reads at an interval the operator sets; listed reviewers by Slack id; one report per read to the Lead Product Manager with links, who and when, never the text; a final read and a closing line on merge or close; a Slack approval is information only. Narrows the item''s first point by posting no title. Names five build pieces, the fifth waiting on yoyodyne-ifd.429.120. Written from a whole read of the MCP design at main a6a3666d, after its Slack route landed.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-16T00:58:52.330225Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2847, for document-2847.1 under the automatic approval policy
---
# Slack review threads for pull requests a person merges

## What this is for

In team mode a person merges each pull request the harness opens, and the team asks for that review in Slack. Mason asked on November 9, 2026 that the harness post the request to Slack and check the thread on a schedule, as well as watching the pull request, so that a change asked for in Slack is not missed. Watching the pull request itself is built (yoyodyne-ifd.429.100).

This design says how the harness posts the request, ties the thread to the run, reads the thread while the pull request is open, tells the Lead Product Manager when a listed reviewer replies, and closes the thread when the pull request merges or closes. It serves the goal that a team can run Yoyodyne against one shared repository. It must not break independent review, publishing through the harness, running nearly autonomously, or plain language.

Mason set the first step on November 9: watch the threads and let someone know there is a request for changes. Nothing starts a repair on its own. Turning a reply into a repair round is a later build piece, and it waits for starting a repair on a run that waits for a person's merge (yoyodyne-ifd.429.120).

## What it rests on, and what was not read

Read whole at commit a6a3666d: [MCP tools through the harness](mcp-tools-through-the-harness.md), including its section 8, which says how a Slack call is made while the operator has no Slack app. Read from the tracker: the work item that asked for this design and the Lead Product Manager's notes on it.

Not read for this design: [scheduled message intake](scheduled-message-intake.md) beyond what the MCP design says of it, [working alongside other contributors](working-alongside-other-contributors.md), the decision record [human merge keeps review](../decisions/human-merge-keeps-review.md), the code that opens and watches a pull request, and which Slack tools post into a thread and read its replies. Piece 1 reads the configuration code, and piece 2 reads the code that opens and watches a pull request and what the Slack server offers, before changing anything. Where that code or that server does something this design does not account for, the piece stops and asks the architect.

## The decision in one paragraph

The harness, not a role, makes every Slack call here, through `internal/mcpcall` as a caller named `review-threads`. When it opens a pull request that a person will merge, it posts one request in fixed words with the link, and keeps the thread's channel and message on the run's publication record. While the pull request is open it reads the thread at the interval the project sets. A new reply from a reviewer the project lists is told to the Lead Product Manager, with the pull request, the thread's link, who replied and when, and never the reply's text, and the same shows on `yoyo status` and the dashboard. When the pull request merges or closes, the harness reads the thread once more and posts one closing line. Slack carries only the harness's own words and links. An approval in Slack is information, and never stands in for the reviewer or for the person's merge.

## 1. The configuration

```yaml
review_threads:
  server: slack                 # a server declared under mcp
  post_tool: post_message       # declared there as act
  read_tool: read_thread        # declared there as read
  channel: C0123456789          # Slack's id for the channel
  reviewers: [U0AAAA1111, U0BBBB2222]   # Slack member ids
  check_every: 15m
```

- `server` names a server the `mcp` section declares. `post_tool` must be declared there as `act` and `read_tool` as `read`. Anything else is refused when the configuration loads, naming the setting. While the operator has no Slack app, that server states `sign_in: connector`, and its calls take the route section 8 of the MCP design sets out; with an app, it states `sign_in: oauth`. This design works the same either way.
- `channel` and `reviewers` are Slack's ids, never names, because anyone in the workspace can change a name. A list with no reviewers is refused at load.
- `check_every` is at least 5 minutes and at most 24 hours, and 15 minutes when absent.
- With no `review_threads` section, nothing here runs and every pull request is handled exactly as today.

All of it is the operator's to set, as the `mcp` section is. The typed write of section 6 of [portable agent configuration](portable-agent-configuration.md) refuses `review_threads`.

## 2. Which pull requests, and which harness

A pull request gets a thread when the harness opened it for a run whose project has a person merge, and `review_threads` is set. Where several collaborators each run their own harness, only the harness that opened the pull request posts to its thread and reads it, because the thread is kept on that harness's run record. The others never touch it.

## 3. Posting the request

Once the pull request is open, the harness posts one message to `channel`:

> [Sent by Yoyodyne] Review requested: the pull request's link. Reply in this thread to ask for changes.

- The words are fixed in code, and only the link is filled in. No title, summary or other text from the work item, the change or the run is posted. Some workspaces forbid copying work content into Slack, and the link shows the title to anyone who opens it. This is narrower than the work item's first point, which named the item's title, and it is narrower on purpose.
- The marker at the start says the harness sent it, because the post appears under the operator's own Slack account.
- The call is an `act` call whose `id` is made from the run and the pull request, so a repeat calls nothing, as section 4 of the MCP design says.
- On success, the channel's id and the message's id, which Slack uses to name the thread, are written to the run's publication record.
- A call refused for a missing credential is a step only a person can take, under the reason `credential`, as the MCP design says. The harness tries the post again at the next interval while the pull request is open. A call that failed clearly is tried again the same way.
- A call whose outcome is unknown is never made again. The run's record says the request may not have been posted, the development manager is told once, and `yoyo status` shows it.

The pull request does not wait on Slack. Review on GitHub and the watch already built go on whether or not the post succeeded.

## 4. Reading the thread

While the pull request is open, at each `check_every` the harness reads the thread's replies with `read_tool`, as part of the same watch that already watches the pull request.

- The time of the newest reply already read is kept on the run's publication record, so a restart carries on from there and nothing is told twice.
- Only replies from members on `reviewers` count. Other replies are counted, and nothing else is done with them.
- The harness does not judge what a reply says, and no model sorts replies in this design. Every new reply from a listed reviewer is told.
- A read that fails is tried once more and, failing again, waits for the next interval. A flaky network or Slack being down ends nothing and names nobody.
- A reply's text is held in memory only until the reply's author and time are written down. The call record keeps the result's size and digest and not its content, as it does for an intake query's call under section 4 of the MCP design.

## 5. Telling somebody

For each read that finds new replies from listed reviewers, the harness files one report addressed to the Lead Product Manager, at the severity note. It gives the pull request's link, the thread's link, and each reviewer who replied and when, in the operator's local time with the zone named, and says that the harness has not read what they asked. It never carries the text. She decides what follows, as for any report: a repair she asks the development manager for, or nothing.

`yoyo status` and the dashboard show, through the read model, each pull request with a thread, how many replies from listed reviewers have arrived since the request, the time of the newest, and the thread's link. Neither shows text.

## 6. When the pull request merges or closes

When the watch sees the pull request merged or closed, the harness reads the thread once more and tells any new replies as section 5 says. Then it posts one line in the thread:

> [Sent by Yoyodyne] Merged: the pull request's link

or

> [Sent by Yoyodyne] Closed without merging: the pull request's link

and stops reading. The closing line is an `act` call under the same rules as the request. A thread whose request was never posted gets no closing line.

## 7. Approval in Slack

A reply that approves is information only. It is told like any other reply. It never counts as the reviewer's verdict, never satisfies a check, and never merges anything. The person still merges on GitHub, and the reviewer's independent verdict still comes first.

## 8. The later piece: a reply becomes a repair round

Once starting a repair on a run that waits for a person's merge is built (yoyodyne-ifd.429.120), a reply can lead to a repair:

- The development manager's list carries each told reply with its text, marked as text from outside that nobody checked, as a review comment on the pull request is today. The text never goes to Slack, the tracker or the dashboard.
- She decides, with the decision that item builds, whether a reply becomes a repair round on the same run. The harness does not decide this for her.
- The developer gets the reply as evidence with its author and time, never as an instruction that widens the run.
- The repaired change goes to the same pull request, and the harness posts one line in the thread:

> [Sent by Yoyodyne] Updated after review: a link to the pull request's changes since the request

## 9. How this sits with the MCP and message intake designs

- **MCP.** Every call goes through `internal/mcpcall`, checked, recorded and scrubbed as that design says. `review-threads` is the same kind of caller as a recurring task. Section 7 of that design lets such a caller hold only read tools until a design says otherwise. This design is that exception, and it is narrow: the `review-threads` caller holds exactly the two tools its configuration names, and its `act` tool is called only with the four messages in this design, built in code from fixed words and links, with no field a model or a person's text fills in.
- **Message intake.** Intake reads messages a scheduled query finds, and has the message reader decide what they ask. This design reads only threads the harness itself started, uses no message reader and no model, and starts no work from a reply.
- **Cost.** While Slack is reached through the operator's Claude account, each read and each post is one provider call. They are counted to the `review-threads` caller and shown in spend under that name, so the operator can see what the interval costs and change it.

## What is enforced in code

- Only the harness posts to a review thread or reads one; no role is given these tools.
- What is posted is one of four messages fixed in code, each starting with the marker, with only links filled in.
- The `review-threads` caller holds only the two tools its configuration names: the post tool as `act` and the read tool as `read`.
- Only replies from member ids on `reviewers` are told.
- In pieces 1 to 4, reply text reaches no record, report, role, surface or Slack message.
- An `act` call whose outcome is unknown is never repeated.
- A thread is read only while its pull request is open, and once more after it closes.

Left to role instructions: nothing.

## What a reviewer can check

- With no `review_threads` section, publishing is exactly today's.
- A configuration naming an undeclared server or tool, a tool of the wrong class, an empty reviewer list, or an interval outside its bounds is refused at load, naming the setting.
- The posted request carries the marker, the fixed words and the link, and no text from the item, the change or the run.
- A restart reads on from the newest reply already read and tells nothing twice.
- A reply from someone not listed is not told.
- The report to the Lead Product Manager carries the two links, who replied and when, and no reply text.
- After a merge or a close there is one final read and one closing line, then no further reads.
- A post whose outcome is unknown is not posted again, and the development manager is told once.
- An approval in Slack changes no verdict and merges nothing.

## Alternatives rejected

- **Folding this into the MCP design or the intake design.** The MCP design is about how any call is made, and intake is about reading messages nobody expected. This design uses the first and shares nothing with the second's reader.
- **A role posting the request in its turn.** The model would choose what is posted, and a role's turn would be spent on a fixed message.
- **Posting the item's title.** Some workspaces forbid copying work content into Slack, and the link carries the title for anyone who opens it.
- **Sorting replies with a model to find requests for changes.** Mason asked first only to be told. A model can be wrong either way, and the reply text would then reach a model. Every listed reviewer's reply is told instead.
- **Naming reviewers by their Slack names.** Names change; ids do not.
- **Counting a Slack approval as a review or a merge.** Review stays independent and on the record, and Slack is neither.

## Build pieces, for the Lead Product Manager to admit

1. **The configuration.** `review_threads`, its refusals at load, the refusal in the section 6 write, `yoyo config show` and `yoyo doctor`. Needs MCP build piece 1.
2. **Posting the request.** When a pull request a person will merge is opened, the fixed request through `internal/mcpcall` as the `review-threads` caller; the thread kept on the publication record; trying again at the interval after a refused or failed post; an unknown outcome told once and never posted again. It first reads the code that opens and watches a pull request. Needs 1 and the MCP call path (yoyodyne-ifd.435.45), and, for Slack while the operator has no app, the Slack route (yoyodyne-ifd.435.60).
3. **Reading and telling.** The reads at the interval inside the existing watch, the time of the newest reply kept, listed reviewers only, text held only in memory, one report per read to the Lead Product Manager, and counts and links on `yoyo status` and the dashboard, carrying on after a restart. Needs 2.
4. **Closing the thread.** The final read and the closing line on a merge or a close, then stopping. Needs 3.
5. **A reply becomes a repair round.** Section 8. Needs 3 and yoyodyne-ifd.429.120.

Pieces 2 to 4 are the first step Mason asked for.

## Documents this changes

- [MCP tools through the harness](mcp-tools-through-the-harness.md): section 7 names this design as the one exception to read tools only for a scheduled caller, and section 4 names the review thread's reads as a second call record that keeps no result. The architect writes both in that design's next revision.
