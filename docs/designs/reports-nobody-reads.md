---
id: reports-nobody-reads
kind: design
title: 'Reports in a project no role pass reads: a count on status, and a repeat counted on the report it repeats'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-27T17:32:03.940946Z
      reason: 'yoyodyne-ifd.430.120: designs what the item asks. Status counts unhandled reports by addressee and says which no pass reads. A report a run files that matches a standing unhandled one, named by the run or equal in plain form, at the same or lower severity, is a repeat record counted on it rather than a new report. Written from docs/reporting.md at 75cfaf65cdf9; the report code was not read.'
    - action: amended
      by: architect
      at: 2026-11-27T18:30:09.461565Z
      reason: 'yoyodyne-ifd.430.120: adds Decision 3 for done points 3 and 4, which the operator added after the first revision was queued, as settled with the Lead Product Manager in exchange 4f0ea38b. A report for a role no pass reads goes to the Lead Product Manager''s pass where it runs, else at once to the person on status and in what the run prints when it ends. No report is refused for lack of a reader; a refused report''s text is kept in the run''s summary. Adds build piece 3. Written from main at 6ab5a03f3dd4.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-27T17:32:03.940946Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4245, for document-4245.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-27T18:30:09.461565Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4259, for document-4259.1 under the automatic approval policy
---
# Reports in a project no role pass reads

## What this is for

In the operator's game server plugin project, run by hand on November 26, 2026, the runs filed 34 reports and none was handled:

- 27 were for the development manager;
- 4 were for the Lead Product Manager;
- 3 named no role.

No recurring pass reads reports in that project, the Lead Product Manager's included, and 17 of the 34 repeat one fact, filed again by nearly every run. The project looked quiet while 34 things waited. Those reports were nonetheless the only way three real faults reached anyone: a refusal to write a temporary folder, a test server left running, and a defect in the plugin.

This design does three things:

- a person reading `yoyo status` is told that reports wait, for whom, and which command reads them;
- a run that would say again what a standing report already says adds to that report's count instead of filing a new report;
- a report addressed to a role that does not run in the project reaches a reader who will see it, and no fact a run noticed is lost because a report was refused.

It answers the item on reports in a project run by hand (yoyodyne-ifd.430.120). It serves the goal that development runs nearly autonomously, with a person's attention going only where it is needed, and the goal that the harness's surfaces read clearly.

## What exists today

From the reporting guide, [reporting](../reporting.md), at commit 75cfaf65cdf9:

- Reports are kept in one collection per project, outside the repository, beside the run records.
- A report is written once and never changed.
- What a role decided about a report, its handling, is kept in a second file beside the reports, and only a handling takes a report off the unhandled list.
- `yoyo reports` lists the reports and opens with a line counting the unhandled ones.
- `yoyo status` lists each report addressed to a role.
- `yoyo status` names the Lead Product Manager on its line of things needing a person once the oldest undecided report is a week old.
- A role reads the reports only where the project configures a recurring task for it.

So a project with no recurring task gets one status line, and only after a week. Reports naming no role get no entry of their own, and nothing counts repeats.

The report code is in internal/report. This design was written from the guide, not from that code; where the code differs, the build follows this design and the guide is corrected with it.

## Decision 1: status counts unhandled reports by addressee

Whenever any report stands unhandled, `yoyo status` prints one line, and the dashboard shows the same figures. The line comes from the same count as the opening line of `yoyo reports`, so the two cannot disagree. For the project above it reads:

```text
reports: 34 unhandled (development manager 27, Lead Product Manager 4, no role named 3); no pass reads them; read them with yoyo reports
```

- **Addressees.** Each is named in words. A report naming no role is counted under "no role named", not added to the Lead Product Manager's count, so the figures match what the runs filed.
- **Which reports no pass reads.** A pass reads an addressee's reports when an enabled recurring task in the project's configuration belongs to the agent that handles them. The Lead Product Manager handles reports naming no role and reports for a program manager. Where every addressee lacks such a task, the line says "no pass reads them". Where only some do, it names those, such as "no pass reads those for the development manager". Where every addressee has one, the clause is left out.
- **Critical reports.** Critical reports are counted in the line, as the `yoyo reports` line counts them.
- **When nothing stands.** The line is not shown.
- **For a script.** `--json` carries the count for each addressee and whether a pass reads its reports.

The line shows in every project, not only in one without passes. In a project with passes it is one line, and the entries for each report stay as they are. A rule for when to hide it would be a second rule that could be wrong.

## Decision 2: a repeat is counted on the report it repeats

### What makes two reports the same fact

A report a run files repeats a standing report when all four of these hold:

1. The standing report is in the same project and is unhandled when the repeat is recorded.
2. Both name the same addressee, or both name none.
3. The new report's severity is the same as the standing one's, or lower. A higher severity is filed as a new report, so a critical report is never hidden in a count.
4. Either the run names the standing report as the one it repeats, as described below, or the two messages are equal once both are put in plain form.

Plain form changes a message in these ways and no others:

- letters are lower case;
- every run of whitespace becomes one space;
- each identifier the harness makes, beginning with report, run, mqe or exchange and a hyphen, becomes one marker;
- each hexadecimal string of 7 to 40 characters becomes one marker;
- each date and each time becomes one marker;
- each other number becomes one marker.

The work item and the role that filed the report are not compared. A developer and a reviewer telling the same role the same thing are stating one fact.

### The run names what it repeats

Each developer run and each reviewer run is handed, at its start, the unhandled reports standing in its project:

- at most 10, the most repeated first and then the newest;
- each shown as its identifier, its addressee, its severity, and the first 160 bytes of its message;
- the whole list held to 2 KiB, and saying how many more stand.

A report entry may carry `"repeats"` with a report identifier. The harness accepts it only when that identifier was on the run's list and conditions 1 to 3 hold. Otherwise the entry is filed as a new report, and the run's closing lines say why the name was not used, as they already say other report problems. A report never costs its run anything.

Where no name is given, the plain form match is tried against every unhandled report in the project.

### What is recorded

A repeat is a third kind of record, kept beside the reports and the handlings. It holds:

- the standing report's identifier;
- the run, the work item, the role, the agent, and the build;
- the severity;
- the time;
- the new message whole.

The standing report is never edited. Its count is the number of repeat records naming it.

A repeat is written under the same lock as a new report. The check that the standing report is still unhandled is made again under that lock, so a handling recorded in between sends the report in as a new one. A repeat record that a restart cut short is dropped when read, exactly as a report cut short is.

### What readers see

- **`yoyo reports` and `/reports`.** They show the standing report once, with a line under it such as "repeated 16 times, the latest by run <id> on <item> at 14:02 PST", in the reader's local time. `--json` carries the repeat records under the report.
- **A role's pass.** It is shown the standing report once, with the same line.
- **A run's closing lines.** They say the run repeated that report and give its new count, in place of saying it reported something.
- **Handling.** A handling of the standing report covers its repeats, and nothing about a repeat needs handling of its own. A repeat filed after the handling is a new report. Matching a report to one already handled belongs to the item on that (yoyodyne-ifd.430.35), which this design does not change.
- **The count in Decision 1.** It counts standing reports, not repeats.

### Reports already filed

The 34 reports already in the game server project are not folded together. They are records as their runs wrote them. Matching starts with the first report filed after the build lands. The status line counts the old reports, and whoever works them handles them as usual.

## Decision 3: a report always reaches a reader, and none is lost to a refusal

The operator asked on November 27, 2026 that a report never be left addressed to a role that will not read it.

### Who reads a report no pass reads

Whether a pass reads an addressee's reports is the test Decision 1 uses: an enabled recurring task in the project's configuration belongs to the agent that handles them. For a report whose addressee fails that test:

1. **The Lead Product Manager, where her pass runs.** Where an enabled recurring task belongs to the agent that handles reports naming no role, her pass is shown the report with the others, marked as addressed to a role no pass reads here. She handles it as she handles a report naming no role.
2. **The person, where no pass reads it at all.** Where her pass does not run either, the report reaches the person in two places:
   - on `yoyo status`, the line of things needing a person names these reports at once, how many and for whom, with the command that reads them, instead of waiting for the oldest to be a week old;
   - in what the run that filed it prints when it ends, each such report whole, with its severity and addressee, and the same text in the work item's notes.

Nothing about the reader is written on the report. Reports are written once, and the reader is worked out from the configuration whenever reports are listed, so enabling a pass later sends the waiting reports to it without any record changing. The one place the reader is decided at filing time is what the run prints when it ends, because that is printed once.

The addressee a run named is kept as written, so the counts in Decision 1 still match what the runs filed, and repeats are matched on it as Decision 2 says.

### No report is refused for lack of a reader

A report is accepted whether or not any pass will read it. A refusal is kept only for a report the harness cannot accept as written: a message past the length limit, a severity not on the list, or an addressee that is not a role. Each such refusal:

- says in the run's closing lines what was wrong and that the fact is kept in the run's summary;
- copies the message the run gave, cut to the report length limit with the cut said, into what the run prints when it ends and into the work item's notes.

So a fact a run noticed reaches the item's record even when its report is refused.

### What the run prints when it ends

The design assumes each developer and reviewer run already prints closing lines that state report problems, as Decision 2 says. Where the build finds that a run prints none, its piece adds them: one block, after the run's outcome, holding the reports that reached no pass and the refused messages, in that order.

## Alternatives rejected

- **A model judging sameness.** It costs a provider call for each report, and its answer cannot be repeated or checked.
- **Matching wording that is close but not equal.** Any threshold would sooner or later merge two different findings, and a finding lost that way is lost without anyone noticing.
- **Plain form alone, with no named repeat.** Runs word one fact differently, so most repeats would be missed. The named repeat catches those; plain form catches a run that names none.
- **Counting on the report itself.** Reports are written once, which is what keeps them evidence.
- **Waking a role pass in a project that has none.** Which roles are woken is the project's choice, as the reporting guide says. Status tells the person instead.
- **Showing the count only where no pass runs.** Rejected for the reason given under Decision 1.
- **Sending every unread report to the Lead Product Manager.** In the game server project her pass did not run either, so it would have moved the reports from one unread list to another.
- **Refusing a report addressed to a role that does not run.** The three real faults in that project reached anyone only through such reports; a refusal would have lost them.
- **Writing the reader onto the report when it is filed.** It would change a record that is written once, and would go wrong as soon as the configuration changed.

## Build pieces

There are three pieces, each sized for the range of 300 to 600 changed lines:

1. The repeat record, plain form, the named repeat with the list handed to a run at its start, and what `yoyo reports`, `/reports`, a pass's listing and a run's closing lines show, with tests.
2. The status line and the dashboard figures, with tests.
3. Decision 3: the Lead Product Manager's pass shown reports no other pass reads, the line of things needing a person naming them at once where no pass reads them, what the run prints when it ends, and the refusal that keeps the fact, with tests.

Pieces 2 and 3 share the test of which pass reads an addressee's reports; whichever lands first builds it and the other uses it. None waits on another. If two of them together are expected to come under 600 lines, the development manager may keep them as one item.

## What a reviewer can check

- A test files two reports from two runs whose messages differ only in a run identifier, a commit hash, and a number. It finds one standing report and one repeat record naming it.
- A test files a repeat at a higher severity and finds two reports.
- A test names a report that was not on the run's list and finds a new report, with the reason on the run's closing lines.
- A test handles the standing report and then files a repeat, and finds a new report.
- A test of status with 27, 4 and 3 unhandled reports and no recurring task shows the line above. With a task enabled only for the development manager's agent, the line names the other two addressees as the ones no pass reads.
- A test with a recurring task only for the Lead Product Manager's agent files a report for the development manager and finds it in her pass's listing, marked as addressed to a role no pass reads here.
- A test with no recurring task files a report for the development manager and finds it named at once on the line of things needing a person, and printed whole in what the run prints when it ends and in the item's notes.
- A test files a report whose addressee is not a role and finds no report, the refusal on the run's closing lines, and the message in what the run prints when it ends and in the item's notes.