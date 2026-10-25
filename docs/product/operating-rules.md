---
id: operating-rules
kind: rules
title: Operating rules
supports:
    - brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-09-29T00:00:00Z
      reason: the nine standing directives the operator gave, recorded in the product home by the item recording the operating rules in the product home (yoyodyne-ifd.433.17) under his direction of 2026-09-28 so they are versioned and delivered to every role; each is a rule he already gave, so recording it is a consistent rewording under his ruling of 2026-09-26
    - action: amended
      by: product-manager
      at: 2026-10-25T19:41:31.587635Z
      reason: 'yoyodyne-ifd.428.126.2.1: records Mason''s direction of October 25, 2026, that work on a change a run already wrote goes before anything else, and says plainly that the dedicated reliability seat takes it first too (report 20616344). It is a rule he already gave; no goal changes, so the goals admit and refuse exactly the work they did before.'
      intent: consistent
approvals:
    - revision: 1
      by: product-manager
      at: 2026-10-25T19:41:31.587635Z
      reason: yoyodyne-ifd.428.126.2.1 - approved by the Lead Product Manager, who recorded this amendment as consistent with intent; a change consistent with the goals is hers to approve under the operator's ruling of 2026-09-26, so he was not asked
---

# Operating rules

These are standing rules the operator gave as directives and the Lead Product
Manager recorded here, so every role reads them as product intent. A rule here
is applied by every role to its own work; a change to one is proposed to the
Lead Product Manager.

## Rules

- An approval of a brief, goals, or any other product document is recorded with the harness's approve command, never by editing a document by hand. (directive e82486d6)
- A work item's notes are append-only: nothing rewrites, reorders, or removes an earlier note; a correction is a new note. (da04b051)
- The delivery workflow is configurable: the graph of what happens to a change is project-owned data, and the capability behind each step stays trusted code that no configuration widens. (18dfadd1, 2026-08-31)
- Anything that blocks reliable execution is a defect: admitted with the reliability label at priority 0 or 1 ahead of feature work, and a developer seat prefers it. (5268040c, 2026-09-14; a43f7677 and d83b6391 for the seats)
- A run is never killed for a flaky network or a sleeping laptop: a transport that drops or a machine that sleeps is waited out, and only a person or a bound on the work itself ends a run. (fb5a74a9)
- Slack, the dashboard, the scheduler, and maintenance are parts of one product: started, stopped, restarted, and moved onto a new build together, by the product and not by a person. (6de828a8)
- Developer seats are tagged, and one seat is dedicated to reliability work. (a43f7677, d83b6391)
- Work on a change a run has already written and kept, such as a decided repair or continuation, or a merge being tried again, is taken first by any freed developer seat, the reliability seat included, ahead of fresh work of any priority. (the operator, 2026-10-25, recorded on yoyodyne-ifd.428.126.2.1)
- When the intake brake trips, it summons the development manager to decide, never the operator; the operator is told and may release it. (789c052c)
- A developer run never runs the tracker's command directly; the harness reads and writes the tracker on its behalf. (from CLAUDE.md, moved here by the item beside this one)
