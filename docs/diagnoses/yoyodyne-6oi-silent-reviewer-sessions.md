# yoyodyne-6oi: why the reviewer's AI session went silent, and whether it still does

Between October 5 and October 11 the harness ended a reviewer's AI session for
producing no output sixteen times. Every one was a Claude Code reviewer on
`claude-opus-5-5`, started with no tools, on a build from before the change
that lets a reviewer read the repository it reviews (yoyodyne-ifd.428.92), which
merged at 01:43 PDT on October 11. Since then 1,531 Claude Code reviews have
finished and none has gone silent. Times below are Pacific time: PDT up to
November 1, PST after.

The cause is the one [the diagnosis of the October 6
silences](yoyodyne-ifd-428-89-reviewer-silences.md) found, and it explains all
sixteen. A reviewer with no tools wrote its tool calls out as ordinary text.
Claude Code reports a block of text to the harness only once the block is
finished, so while the reviewer wrote a long block of calls that could not run,
the harness saw nothing, and after five minutes it ended the session. Giving the
reviewer read-only tools removed the reason it wrote those blocks, and the
silences stopped with it.

The design publication in pull request #1161 was not stopped three times by a
silent reviewer. It was stopped once by one. Its other two stops were checks
that ran out of time. A retry of that publication is no longer possible or
needed: the development manager retired it on October 13, and the same document
landed that day through pull request #1236 on its first review.

## Every reviewer session ended for silence

This is every reviewer session in the product's run records that the harness
ended because it produced no output for five minutes: each run's event log
(`runs/<run>.events.jsonl` under the product's state directory) was searched for
the harness's record of a stopped reviewer response, and only those where the
reviewer's last event came about five minutes before the stop were kept. Size is
the patch the reviewer was handed, in bytes. "Ran" is from the start of the
review to the stop; "active" is how long the session reported anything before it
went quiet. All sixteen are Claude Code, model `claude-opus-5-5`, tool list
empty (`"tools": []`), a new session rather than a resumed one, and no provider
error, rate limit or disconnect reported.

| Stopped (PDT) | What it was reviewing | Run | Which review of the run | Size | Ran | Active | The next review of the same change |
|---|---|---|---|---|---|---|---|
| 19:41 Oct 5 | an agent block's role (yoyodyne-ifd.434.6) | `run-7676dcb8de54d1b9c0d041e9b57cec41` | 1st of 11 | 108,846 | 5 min 19 s | 17 s | asked for a repair, in 63 s |
| 02:14 Oct 6 | stopped-run delivery (yoyodyne-5v6) | `run-03ae7d4a3c34be22ed780be3fbd9c080` | 2nd of 7 | 11,065 | 5 min 12 s | 10 s | approved, in 28 s |
| 05:35 Oct 6 | Codex token usage display (yoyodyne-ifd.435.16) | `run-214fb454129b31db021e1a81c7f457a1` | 1st of 4 | 74,535 | 5 min 27 s | 24 s | approved, in 173 s |
| 10:29 Oct 6 | first half of the orchestrator fakes move (yoyodyne-ifd.429.13.6.1) | `run-f2de33ab3e57f36cc7e95e67ca748310` | 2nd of 5 | 261,165 | 5 min 10 s | 8 s | approved, in 48 s |
| 10:43 Oct 6 | the plain-words rule (yoyodyne-ifd.430.23) | `run-cde683caf3b63154cc554a7dc36314ea` | 1st of 6 | 60,024 | 5 min 13 s | 11 s | went silent again (next row) |
| 12:08 Oct 6 | the plain-words rule (yoyodyne-ifd.430.23) | `run-cde683caf3b63154cc554a7dc36314ea` | 3rd of 6 | 60,024 | 5 min 15 s | 13 s | approved, in 33 s |
| 15:09 Oct 6 | stopped-run delivery (yoyodyne-5v6) | `run-03ae7d4a3c34be22ed780be3fbd9c080` | 5th of 7 | 11,067 | 5 min 8 s | 7 s | approved, in 23 s |
| 20:01 Oct 6 | the installer (yoyodyne-ifd.125) | `run-cabd04cb8aed8dd94f2c59161c5ec576` | 3rd of 4 | 49,122 | 5 min 25 s | 22 s | approved, in 172 s, after writing 211 tool calls as text |
| 12:36 Oct 7 | a watch session waiting out a run at its promotion (yoyodyne-ifd.428.80) | `run-33e510f160fe1b31646829a4962d12f0` | 2nd of 6 | 29,461 | 5 min 28 s | 26 s | approved, in 29 s |
| 17:28 Oct 7 | the launcher test on Linux (yoyodyne-ifd.435.14.9) | `run-fb6c61a75cdb4425dbce190446b4c2c8` | 1st of 4 | 9,266 | 5 min 37 s | 34 s | approved |
| 23:31 Oct 7 | the Codex permission profile (yoyodyne-ifd.435.27) | `run-e389ddbd610732129bc2396a85e4ba39` | 3rd of 6 | 75,575 | 5 min 7 s | 5 s | approved, in 62 s |
| 23:38 Oct 7 | dependency dates in the tracker export (yoyodyne-ifd.433.33) | `run-748015a3f0a6bf9de43a31a849a7da51` | 2nd of 4 | 19,514 | 5 min 25 s | 15 s | asked for a repair, in 79 s, after writing 24 tool calls as text |
| 10:21 Oct 8 | the Codex resume test check (yoyodyne-ifd.435.28) | `run-78fd30ccfbe07570eae84ad50557334d` | 3rd of 5 | 4,998 | 5 min 11 s | 6 s | approved, in 70 s, after writing 20 tool calls as text |
| 20:46 Oct 9 | standard-library names in the readiness check (yoyodyne-ifd.428.93) | `run-e8d09ff2ece66b635342a0299e8067d5` | 1st of 3 | 23,433 | 5 min 26 s | 24 s | approved, in 30 s |
| 02:08 Oct 10 | a failed GitHub build recorded by step (yoyodyne-oq3) | `run-d443a162e35fe6357e4b2b21ea5a0eaf` | 1st of 2 | 31,668 | 5 min 10 s | 8 s | refused at once: the account's weekly usage limit was reached |
| 00:27 Oct 11 | publishing the artifact contract (pull request #1161) | `run-c722ce649a71afd9a740f8a51c1f6d6c` | 2nd of 2 | 7,965 | 5 min 9 s | 7 s | none: the run stopped, and was retired on October 13 |

The first eight are the ones [the October 6
diagnosis](yoyodyne-ifd-428-89-reviewer-silences.md) examined; the last eight
came after it. How large the prompt was cannot be given for the last eight:
Claude Code's own copies of those sessions are no longer on this machine, and
the run records keep only the patch size. Size did not decide anything in the
first eight, whose prompts ran from 258,088 to 547,629 characters, and the
patches above run from 4,998 to 261,165 bytes with the smallest silent as often
as the largest.

Four other reviewer sessions were ended in the same records, and none of them
was silent. The Codex review of automatically approved documents
(yoyodyne-ifd.433.21.1) at 15:01 PDT October 5 ran out of its session budget.
The three reviews of the architect's pass instructions (yoyodyne-ifd.430.60) at
10:53 PDT October 18 each ended within a second, because the account's login had
expired and could not be refreshed. The harness writes the same "observations
during the stopped response" line after all of these, so the line alone does not
mean the session was silent.

## The cause

Every silent session has the same events. The session starts with no tools and
nothing loaded, Claude Code reports the reviewer thinking about once a second
for 5 to 34 seconds, and then nothing until the harness ends it five minutes
later. That is the point at which a reviewer begins writing its first block of
text, and Claude Code, run with `--output-format stream-json` and without
`--include-partial-messages`, reports that block only when it is finished.

The reviewers that did finish on those builds show what those blocks were. Of
the 266 Claude Code reviews that finished with no tools between October 5 and
October 11, 70 wrote tool calls out as text, which ran nothing. The time with
no event before a finished block grew with its length, and some came close to
the limit: a 67,913-character block left 270 seconds without an event
(the supervisor tests' state root, yoyodyne-ifd.434.21, 07:48 PDT October 9),
and one review of the tracker export dates (yoyodyne-ifd.433.33, 23:04 PDT
October 7) went 289 seconds without an event and finished eleven seconds short
of being stopped. The next review of that same change is one of the silences
above.

The publication shows both halves in one run. Its first review, at 23:33 PDT
October 10, wrote one tool call as text, got nothing back, and approved in
30 seconds. The change was then rebuilt on the moved main branch and reviewed
again at 00:22 PDT October 11; that reviewer thought for 7 seconds and was never
heard from again.

Nothing else separates the silent reviews from the others. Every change that
was asked again was judged by a later review of it, most in under three
minutes, except the one refused for the usage limit; the provider reported no
error in any of them; and silent reviews came from first reviews and later ones,
small patches and large, publications and code changes alike. The one
publication among the sixteen is one of 16 silences across 282 Claude Code
reviews on those builds, about one in eighteen.

How sure this is: the silent sessions were ended before their text was
finished, so nobody can read what they were writing, exactly as in the October 6
diagnosis. What this diagnosis adds is the test that diagnosis could not run:
the cause it named was removed on October 11, and the silences stopped. Since
then, up to 14:52 PST November 13, 1,531 Claude Code reviews have finished,
every one started with read-only tools (`Read`, `Glob`, `Grep`, `Bash`), none
wrote a tool call as text, none was silent, and the longest any of them went
without an event was 92 seconds; for nine in ten the longest was 13 seconds or
less. On the earlier builds it was 19 seconds or less for nine in ten, but the
longest reached 270 and 289.

## The publication in pull request #1161

The architect's revision of the artifact contract (document-566.1) was given
three tries, each a separate run:

| Run | Started (PDT) | How it stopped |
|---|---|---|
| `run-daefc188a1a92f326581840d1f9ba391` | 18:38 Oct 7 | `make race` ran out of time; the reviewer was never started |
| `run-fb45df8fa329666f80384fb47818c1f6` | 06:21 Oct 10 | `make race` was stopped when the work that started the run reached its deadline; the reviewer was never started |
| `run-c722ce649a71afd9a740f8a51c1f6d6c` | 22:53 Oct 10 | the reviewer went silent at 00:22 Oct 11, after an earlier review had approved it |

The last run's record says the publication "stopped 3 times without anything
judging the document" and names only the last stop's cause. That sentence is
true, but it counts every stop, and the development manager's report de1cc461
and then the program manager's report 9050b15a read it as three silent
reviewers. It was one.

Whether a retry would hit it again: no. First, there is nothing left to retry.
The development manager retired the run at 15:43 PDT October 13 and closed
pull request #1161, because the same document had landed earlier that day
through `run-abc4c88b26d2d250fb366ff195aed847` (pull request #1236), whose
reviewer read 23 things in the repository, went no more than 7 seconds without
an event, and approved in 84 seconds. Second, any publication retried now gets a
reviewer with read-only tools, the case in which 1,531 reviews have produced no
silence.

## What is still true, and what would need a fix

The cause is fixed, by the change that gave the reviewer its reads
(yoyodyne-ifd.428.92). Nothing more is needed to stop these silences.

Two things the October 6 diagnosis described are still true and are worth
knowing, though neither has cost anything since October 11:

- The Claude Code adapter still does not ask for text as it is written
  (`--include-partial-messages`), so a block of text being written still
  reaches the harness only when it is finished. Any Claude Code session, in any
  role, that writes one block for more than five minutes is ended as silent. A
  reviewer that reads writes short blocks between reads, which is why this no
  longer happens; it is a margin, not a guarantee.
- A publication run's stop record counts every stop without saying what each
  one was, which is how one silent reviewer was reported as three.
