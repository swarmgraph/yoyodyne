# Handing the reading and the checks to sub-agents

Sub-agents are on for this run, on a cheaper model than yours. Everything you read stays in your context and is paid for again on every turn after, so keep your own context small: sub-agents do the reading, the writing, the checks and the fixes, and you spend your own turns deciding and reviewing.

1. Hand the exploration to a sub-agent. Tell it what the work item asks and what you need to know: which code is involved, what it does now, and the repository's conventions around it. Ask for a summary of at most twenty lines naming files and functions with line numbers. Read files yourself only where the summary leaves a decision open, and then only the lines you need.
2. Plan the change yourself from that summary: which files change, what each change must do, and in what order.
3. Hand the writing, the checks and the fixes to a sub-agent, one bounded piece at a time. The sub-agent runs the checks, fixes what fails, and runs them again until they pass or it cannot see how to fix them. Give it a brief in this shape:

```
Files: the files to change, and any it must not touch.
Change: what the change must do, and why.
Done when: the conditions that must hold, including which checks to run and that they pass.
Conventions: read CLAUDE.md or AGENTS.md and follow it; match the surrounding code's naming, error handling and comments; scratch files and check logs go in the scratch directory your instructions name, never in the worktree.
Report: reply in these three lines and nothing else, without pasting check output:
  Changed: the files changed
  Checks: each command run and whether it passed
  Open: anything not done or not certain, or "nothing"
```

4. Review each piece by its diff alone, such as `git diff` on the files it changed, not by reading whole files. Hand a correction back in the same brief shape rather than making it yourself, unless it is a few lines.
5. Before you submit, review the complete change as one diff, and run the checks your instructions name yourself, sending their output to a file in your scratch directory and reading only how each ended and, for one that failed, the lines that say why. A sub-agent's report that checks passed is not your verification.
