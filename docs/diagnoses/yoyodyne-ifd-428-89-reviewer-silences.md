# yoyodyne-ifd.428.89: why reviewer sessions went silent on October 6

On October 6 reviewer sessions produced no output for five minutes, and were
stopped by the harness, five times across the three runs the work item names
and three more times in other runs in the same day. All times below are
Pacific Daylight Time (PDT).

The cause is the same in every case the logs can speak to. Since 15:39 PDT
October 5 the reviewer has run on Claude Code (commit `a04888b5`, "developers
and reviewers on Opus (medium), off Codex Sol"), and Claude Code reviewers are
started with no tools at all. The reviewer still tries to read the repository:
in 36 of the 120 Claude Code reviews between then and October 7 it wrote tool
calls out as ordinary text, which runs nothing and returns nothing. Claude Code
reports a block of text to the harness only once the block is finished, so
while the reviewer writes those calls the harness sees nothing. It writes about
255 characters a second; a block of more than about 77,000 characters keeps the
session quiet past the harness's five-minute limit. Reviews that finished show
the same thing growing towards the limit: 35,134 characters written without a
word for 138 seconds, and 54,602 characters (246 made-up tool calls) for 213
seconds.

None of the three causes the work item suggests explains any of the silences.
The fix that removes this cause is the admitted item that lets a reviewer
session read the repository it reviews (yoyodyne-ifd.428.92), whose two
reported cases of empty reads come from the same missing tools.

## The silences

| When it went quiet (PDT) | Work item | Run | Reviewer session |
|---|---|---|---|
| 02:09, stopped 02:14 | stopped-run delivery (yoyodyne-5v6) | `run-03ae7d4a3c34be22ed780be3fbd9c080` | `522fb1df-19d6-475e-a41e-8c223b7399cc` |
| 10:24, stopped 10:29 | first half of the orchestrator fakes move (yoyodyne-ifd.429.13.6.1) | `run-f2de33ab3e57f36cc7e95e67ca748310` | `f8f1d863-4267-4c89-8d3a-47d90b1dfd2c` |
| 10:38, stopped 10:43 | the plain-words rule (yoyodyne-ifd.430.23) | `run-cde683caf3b63154cc554a7dc36314ea` | `724e1d41-eaf2-4bfe-aaae-f29feec11b55` |
| 12:03, stopped 12:08 | the plain-words rule (yoyodyne-ifd.430.23) | `run-cde683caf3b63154cc554a7dc36314ea` | `0ba61656-14f5-4059-a8f5-5c1c0d8bc567` |
| 15:04, stopped 15:09 | stopped-run delivery (yoyodyne-5v6) | `run-03ae7d4a3c34be22ed780be3fbd9c080` | `584f5ba4-0e8c-4764-a4ea-f75744a41201` |

The fifth is the one the factory-flow program manager added. Its note names
Claude session `2bde5a13-c50a-4776-a19d-dfba8d7b8f64`; that is the run's
developer session, kept on the run record as `provider_session_id`. The
reviewer session that went silent at 15:09 is `584f5ba4-…`, from the event log.

The same logs hold three more reviewer silences of exactly the same shape:

| When it went quiet (PDT) | Work item | Run | Reviewer session |
|---|---|---|---|
| 19:36 October 5, stopped 19:41 | an agent block's role (yoyodyne-ifd.434.6) | `run-7676dcb8de54d1b9c0d041e9b57cec41` | `6eaa2edd-0a60-4707-8a4d-fd2a62dd697f` |
| 05:29 October 6, stopped 05:35 | Codex token usage display (yoyodyne-ifd.435.16) | `run-214fb454129b31db021e1a81c7f457a1` | `be34dca0-aac9-4f70-b4be-862ae550704f` |
| 19:56 October 6, stopped 20:01 | the installer (yoyodyne-ifd.125) | `run-cabd04cb8aed8dd94f2c59161c5ec576` | `78d9799b-de66-4532-ad1e-3fea34168184` |

Three developer sessions also went silent in the same logs (stopped at 13:05
and 23:09 October 6 and 01:15 October 7). A developer has tools and was in the
middle of using them, so those are a different case and are not covered here.
The one Codex review stopped on October 5 (the automatically approved
documents run, yoyodyne-ifd.433.21.1, at 15:01) was stopped because its
session's total budget ran out, not for silence; the item on continuing such a
stop (yoyodyne-ifd.428.78) covers it.

## What each silent session shows

The run's event log is `runs/<run>.events.jsonl` under the product's state
directory. Claude Code's own copy of each session is
`~/.claude/projects/<worktree path>/<session>.jsonl`.

| Run, silence | Backend and model | Ran before going quiet | Review copy (patch bytes against the 262,144-byte bound) | Prompt characters | Kind of review | Provider reported |
|---|---|---|---|---|---|---|
| 5v6, 02:14 | Claude Code, `claude-opus-5-5` | 11 s | 11,065, not cut | 425,527 | a second review of an unchanged change, after the development manager's repair and a restart | nothing |
| fakes move, 10:29 | Claude Code, `claude-opus-5-5` | 9 s | 261,165, not cut | 507,768 | a second review, after the developer's repair | nothing |
| plain words, 10:43 | Claude Code, `claude-opus-5-5` | 12 s | 60,024, not cut | 310,970 | the first review | nothing |
| plain words, 12:08 | Claude Code, `claude-opus-5-5` | 14 s | 60,024, not cut | 314,447 | the harness's own continuation after the 10:43 silence, retried after a restart | nothing |
| 5v6, 15:09 | Claude Code, `claude-opus-5-5` | 7 s | 11,067, not cut | 447,217 | a review of the change replayed onto the moved main branch | nothing |
| 434.6, 19:41 Oct 5 | Claude Code, `claude-opus-5-5` | 17 s | 108,846, not cut | 547,629 | the first review | nothing |
| 435.16, 05:35 | Claude Code, `claude-opus-5-5` | 25 s | 74,535, not cut | 343,161 | the first review | nothing |
| installer, 20:01 | Claude Code, `claude-opus-5-5` | 23 s | 49,122, not cut | 258,088 | a review of the change replayed onto the moved main branch | nothing |

Every reviewer session is a new Claude Code session: none was a resumed
session. "Ran before going quiet" is from the session's start to its last
event.

The events are the same in all eight. The session starts (`run.started`, model
`claude-opus-5-5`, `"tools": []`, `"loaded": "settings sources: none; skills:
none; plugins: none; connectors: none; instruction files: none"`), then Claude
Code reports the reviewer thinking (`thinking_tokens`) about once a second for
7 to 25 seconds, and then nothing. Five minutes after the last
event the harness ends the session and records that no machine sleep or harness
downtime was found. The first 5v6 silence, for example:

```
619 2026-10-06T09:09:15.302Z run.started claude-code {"model": "claude-opus-5-5", "session_id": "522fb1df-…", "tools": [], …}
620 2026-10-06T09:09:17.197Z process.output claude-code {"provider_subtype": "thinking_tokens", …}
…
627 2026-10-06T09:09:25.248Z process.output claude-code {"provider_subtype": "thinking_tokens", …}
628 2026-10-06T09:14:26.282Z process.output harness {"role": "reviewer", "text": "observations during the stopped response: no machine sleep or harness downtime was established during this response; its cause is unavailable", …}
```

What the provider reported, in each: no error, no usage-limit or rate-limit
event, no compaction, no retry, no disconnect. The only provider messages are
the session's title, its command list, and the thinking reports. The product's
usage-limit record (`usage-limits.jsonl`) has nothing for October 6 or 7. Claude
Code's own copy of each session holds the prompt and nothing the reviewer
wrote: the session was stopped before any block of its answer was finished.

Whether reads were returning output before the silence: no session read
anything, and none could. The reviewer is started with no tools (below), and no
silent session's events contain a tool call.

## What is not the cause

**The size of the review copy (yoyodyne-ifd.429.67).** No review copy was cut
and every one was under the bound. The silent ones range from 11,065 to
261,165 bytes of patch and from 258,088 to 547,629 characters of prompt. On the
same day reviews with prompts of 600,000 to 712,809 characters finished in
under a minute. Each silent review was asked again with the same change, and
every one of those finished: 5v6's in 28 seconds at 03:18 and 23 seconds at
17:23, the fakes move's in 47 seconds, the plain-words rule's in 33 seconds,
and the three others in 62, 173 and 171 seconds.

**Continuing on the wrong backend (yoyodyne-ifd.428.88).** Every reviewer
session was a fresh Claude Code session on the configured reviewer; none was
resumed, so no session was offered to the wrong provider. The developer of 5v6
ran on Codex, but the reviewer never resumes the developer's session.

**An unreadable verdict (yoyodyne-ifd.429.66).** No verdict was written at all,
readable or not.

**Being stopped and picked back up for a redeploy.** The development manager's
lead (report f54e5095) is right that the plain-words run and both 5v6 silences
came after the run had been stopped for a redeploy and picked back up. That is
true of almost every run that day: the harness restarted onto a new build
sixteen times between midnight and midnight on October 6 and picked runs back
up 34 times. It does not separate the silent reviews from the others. Of the
108 Claude Code reviews from 16:00 October 5 to 05:00 October 7, 55 were the
first review after the run was picked back up and 3 of those went silent; 53
were not and 5 of those went silent. A reviewer in a run that was picked back
up is started exactly as one in a run that never stopped: a new session, the
same command, the same empty tool list, nothing loaded. Nothing carries over
from the stopped process.

**The network or the account.** In five of the eight silences other runs'
Claude Code sessions kept reporting events during the same five minutes, for
example run `run-f787fa68…` during the 10:29 silence.

## The cause

### The reviewer has no tools on Claude Code

The Claude Code adapter gives the reviewer no tools on purpose
(`readOnlyTools`, `internal/backend/claudecode/backend.go`, unchanged since
August 15): it runs Claude Code with `--tools ""`, so the reviewer judges from
the evidence it is handed and cannot be talked into reading anything else. On
Codex the reviewer has read-only inspection of the worktree
(`docs/configuration.md`). Until 15:39 October 5 every review ran on Codex.
After that commit every review ran on Claude Code, and every reviewer silence
since is a Claude Code review.

The reviewer is not told it has no tools, and where a change touches
`docs/configuration.md` its prompt carries that document's sentence that
reviewers have "native read-only inspection". So it tries. In 36 of the
120 Claude Code reviews from 16:00 October 5 to 09:30 October 7, the reviewer's
answer contains tool calls written out as text, such as:

```
<invoke name="Bash">
<parameter name="command">cd "$PWD"; git grep -n "NotFoundError\|EnvironmentVariable" 39a2c98e -- internal/config/discover.go | head -30; …</parameter>
</invoke>
```

That runs nothing. The reviewer gets no output back, which is what the
installer and run-record write lock reviewers reported as "every read returned
nothing" (yoyodyne-ifd.428.92).

### Text being written reaches the harness only when it is finished

The harness runs Claude Code with `--output-format stream-json --verbose` and
without `--include-partial-messages`. In that mode Claude Code reports thinking
as it goes (`thinking_tokens`, about once a second) but reports text only as a
whole block once the block is finished. So from the end of the reviewer's
thinking to the end of its first block of text, the harness sees nothing.

How long that takes grows with the block, at about 255 characters a second in
reviews that finished:

| Review (run, started PDT) | Longest text block | Seconds with no event before it |
|---|---|---|
| installer, 20:49 October 6 | 35,134 characters | 138 |
| capacity-blocked list (yoyodyne-ifd.432.21), 07:13 October 7 | 54,602 characters, 246 made-up tool calls | 213 |
| capacity-blocked list, 06:43 October 7 | 17,417 characters | 68 |
| capacity-blocked list, 05:38 October 7 | 12,094 characters | 46 |

At that rate five minutes is about 77,000 characters. In every silent session
the last event is the end of the reviewer's thinking, which is the point at
which these blocks begin, and no block was finished before the harness stopped
the session. A reviewer that writes some 350 made-up tool calls in one block
looks exactly like these eight.

### How sure this is

The silent sessions were stopped before their text was finished, so nobody
can see what they were writing. The diagnosis rests on the pattern rather than
on the text itself: every silence is a Claude Code reviewer with no tools,
started since the switch away from Codex; each went quiet at the moment the
reviewers that finished began writing their long blocks; the time with nothing
reported grows steadily with the length of the block, and finished reviews
reached 213 seconds of it; the provider reported nothing wrong; and the same
review asked again finished, which fits the reviewer choosing how much to write
and not the input or the account being at fault. A stall on the provider's side
that happened, all eight times, at the end of the reviewer's thinking cannot be
ruled out from these logs, but nothing in them points to it. Running Claude Code
with `--include-partial-messages` would show the text as it is written and
settle it.

## Which cause explains which silence, and what fixes it

The one cause, a reviewer with no tools writing tool calls as text that Claude
Code does not report until the block is finished, explains all eight silences,
the five the work item names among them. It also explains the empty reads that
the reviewer-reading fix (yoyodyne-ifd.428.92) was admitted for. One cause, so
it should be fixed once:

- **Admitted:** a reviewer session can read the repository it reviews
  (yoyodyne-ifd.428.92). Giving the Claude Code reviewer read-only tools for the
  worktree under review removes the reason it writes tool calls as text, and so
  both the empty reads and these silences. That item has to decide how to keep
  the reason `readOnlyTools` is empty, a reviewer that cannot be talked into
  reading outside its evidence, while giving it reads; the Codex reviewer's
  read-only inspection is the precedent.

Not admitted, for the Lead Product Manager to consider:

- Until the reviewer can read, a Claude Code reviewer is told in its prompt
  that it has no tools and judges from what it is handed, so it does not write
  tool calls it cannot run.
- The Claude Code adapter asks for text as it is written
  (`--include-partial-messages`) and counts it as output, so a reviewer that is
  writing is not mistaken for one that has stopped. That changes what the
  five-minute limit measures for every role on Claude Code, so it is a change
  to how silence is handled and outside this item.

The cost the work item describes comes from what follows a silence, and not
from the silence itself: the harness's own continuation waited 48 to 81 minutes
before asking again, and a second silence in the same run, even on a different
change hours later as in 5v6, went to the development manager. That is also a
change to how a silence is handled, and is not in this item's scope.
