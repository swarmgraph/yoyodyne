# yoyodyne-ifd.429.145: why feedback on the team's tracked pull request waits for the tracking to land

Integrating to a local branch, piece 3 (yoyodyne-ifd.429.145) asks that
feedback on a pull request the team opened, and the harness tracks, reach the
work the way feedback on the harness's own pull requests does. It is section 2,
"Feedback on a tracked pull request", of
[integrating to a local branch](../designs/integrating-to-a-local-branch.md),
and the design builds it on piece 2, tracking that pull request
(yoyodyne-ifd.429.144). Read on November 10, 2026, at about 09:40 PST.

## The answer

- **Piece 2 is not on `main`.** This run's worktree was cut from `main` at
  `02ee090a`. Piece 2's change, `b2b41e5c` on branch
  `yoyodyne/yoyodyne-ifd-429-144/6984adf7`, is not an ancestor of it. The item
  is still in progress, waiting its turn in the harness's merge queue for
  `main`.
- **Everything piece 3 reads is in that change.** It adds 2,487 lines over 24
  files, among them the record of which pull request carries a waiting change
  (`TeamPullRequest` in `internal/runstate/teampullrequest.go`), the
  `yoyo pull-request track` and `untrack` commands
  (`internal/cli/pullrequest.go`), and the forge check that reads each tracked
  pull request (`internal/orchestrator/teampullrequest.go`). Piece 3 needs that
  record to know which pull requests to read feedback from and which items each
  one carries, and nothing on `main` holds it.
- **Building piece 3 here would mean copying piece 2.** A second copy of the
  same files in another change conflicts with piece 2 when either merges. So
  nothing for piece 3 was built in this run.

## What piece 3 needs, once piece 2 is on `main`

The feedback reading for the harness's own pull requests is already on `main`
in `internal/orchestrator/forgefeedback.go`. That includes ignoring only what
the harness marked or listed as its own (`ownWords`), from yoyodyne-ifd.429.125.
Piece 3 reuses it for each tracked pull request:

1. On each forge check, read the feedback on every tracked pull request since
   the last reading, ignoring what the harness wrote, as `newFeedback` does.
2. Put a review asking for changes, or a comment on a line, on the development
   manager's list. Name the pull request, the comment, and the items carried by
   the request whose landing commits touched the file the comment is about. Put
   an ordinary comment on her list the same way, with no item named. Record a
   person's approval as information only.
3. Never start a run from the feedback alone. Her decision names one item and
   starts a run of it on the target branch, with the feedback given as
   evidence. The run is charged to that item's repair budget and goes through
   the configured checks and the independent reviewer like any other run.
4. Do not count tracked pull requests against
   `execution.max_waiting_pull_requests`.

The item's first criterion asks for "a repair round where it calls for one".
Under the design, that round is the development manager's decision on the
listed feedback, not a round started by the feedback itself. The two agree as
long as the repair goes through her decision.
