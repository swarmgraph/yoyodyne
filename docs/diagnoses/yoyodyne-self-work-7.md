# Self-work review, case 7: how the factory writes, reviews and lands its own documents

One of seven case studies of work the factory did on itself, made at the operator's request of 2026-11-21: "Can you do a deep dive into a few of the factory flow items-- what caused them, how many beads did it take to address the issue, was it really fixed? Was is a real issue in the first place?", written up to "critically consider the possibility that we are wasting time with some or many of these issues". The summary of all seven is `yoyodyne-self-work-0.md` beside this file.

- **Read on:** 2026-11-21, between 18:44 and 19:00 PST.
- **Read by:** a read-only investigator on Opus 5.5 in the operator's assistant's session, told to be a sceptical auditor. It read the tracker export, git history and the factory's own logs and run records, and ran nothing.
- **Checked by hand:** the operator's assistant checked 103 document landings against 86 of code on November 19 to 21, and the ten landings of the V1 harness design, from git. Every other figure is the investigator's own.
- **How to use it:** evidence for the Lead Product Manager and the architect, not a ruling. The share of effort with lasting value and the verdict are the investigator's judgement. Costs are estimates for developer runs and reviews at mean prices, and leave out role conversations.

The investigator's summary of its findings:

```
thread: How the factory writes, reviews and lands its own documents (the document landing route and the stream of documents through it)
items_total: 89
items_landed: 47 (36 code changes landed, 11 closed as rulings or document revisions)
items_retired_undone: 6
items_open: 36 (34 open, 2 in progress)
first_item: 2026-08-23
latest_item: 2026-11-21
runs: 474 (58 runs on the machinery items, 55 of them with a developer session; 416 document publication runs)
estimated_cost_usd: 600-2500 (about $350 building the machinery, $230-$320 reviewing documents, and an architect writing cost I could only estimate at $1,200-$1,800)
fixed: partly
real_problem: yes
share_of_effort_with_lasting_value: 30-45%
verdict: partly wasted
confidence: medium
```

## In short

- In October the problem was real: the architect's documents could not land. From Oct 7 to Oct 12, 2 of 41 publication runs succeeded. The route of its own that the fp3 items built fixed that by Oct 14. Since Oct 15, 87% of publication runs succeed.
- On Nov 7 the architect ruled that documents go through the one merge queue with code (ifd.429.59.8). From Nov 18 documents took half the queue's landings. Ordinary code then waited a median 7.9 h against 0.8 h for documents, and its long waits ended in conflict hand-backs. This is the October P0 turned round, and it is open today at P0 (ifd.429.266).
- 301 document landings since Oct 11 touched only 79 files. 222 of them landed a file that had already landed before. The V1 harness design landed 11 times, 10 of them between Nov 20 and Nov 21, at a median of about 20 changed lines each. About 21% of document revisions are wording-only, mostly the vocabulary sweep.
- Of the 53 designs, 6 describe the code as it is and 19 are mostly unbuilt plans. 55 of the 301 landings were revisions to those plans.
- The machinery cost little in dollars, about $350 of developer runs and reviews. Its real costs were queue time taken from code, operator attention, and a self-feeding chain of 89 items, 36 of them still open.

## What went wrong

Someone watching the factory would have seen it go wrong in three phases.

**Phase 1, Oct 7 to Oct 14: documents could not land.** A confirmed document (a design, decision or rule written from a role's conversation) landed through a run that took a developer slot and ran the full `make test` and `make race`. That took 25 to 30 minutes under load for a one-file Markdown change (`docs/designs/document-landing-route.md`, "What this is for"), and only two documents could wait at once. The run records show the result:

- From Oct 7 to Oct 12, 41 publication runs ran and 2 succeeded. The rest timed out (16), were cancelled (10) or failed.
- On Oct 10, five runs were stopped because they inherited the 15-minute deadline of the conversation message that started them (trt).
- Five runs were cancelled when a new build deployed (fbt).
- Others failed `make test` on the coined-words check about ten minutes in (7my).
- On Oct 13, "31 or more open publishing pull requests, two to six copies per document" filled both of the architect's waiting places, and this went unnoticed for about a day (7fl).

On Oct 11 the operator named it a P0 defect, and the architect stopped sending documents (4qw).

**Phase 2, Oct 15 to Nov 7: it worked.** The route landed between Oct 12 and Oct 14: its own landing count, `document_checks`, and its own deadline. After that, publication runs succeeded at 87% (271 of 310), with 0 to 18 document landings a day beside code landings.

**Phase 3, Nov 8 to today: documents crowd out code.** With the merge queue switched on, each document takes a whole queue landing. The queue lands one entry at a time, about one every 21 to 22 minutes. Landings on main by day, read from the inner commit subjects in `git log --first-parent main`:

| Day (PST) | Document landings | Code landings |
|---|---|---|
| Nov 18 | 15 | 37 |
| Nov 19 | 35 | 34 |
| Nov 20 | 37 | 31 |
| Nov 21 | 30 | 21 |

On Nov 20, the operator's assistant measured (ifd.429.255):

- Documents waited a median of 0.8 h.
- Priority 0 code waited 0.7 to 2.0 h.
- Other code waited a median of 7.9 h.
- Four of the day's five hand-backs were conflicts found after 5.7 to 8.3 h of waiting, and each cost a developer run.

On Nov 21, from 01:35 PST, six documents landed in a row while five code entries waited (ifd.429.266). Between 15:17 and 17:42 PST, five documents and one code change landed. My own join of run records to merge commits gives the same picture: on Nov 19 to Nov 20 the median wait was 0.9 to 1.8 h for documents and 7.3 to 8.6 h for code.

**The same documents land again and again.** The 301 document landings are 113 distinct titles across 79 files:

| File | Times landed |
|---|---|
| ownership-and-the-operator | 15 |
| observability-and-dashboard | 14 |
| machine-home | 14 |
| stopped-run-recovery | 13 |
| v1-harness-design | 11 |
| integration-through-a-merge-queue | 11 |
| scheduled-message-intake | 11 |

Each landing is a separate publication run, a review, and a queue landing. The median landing changes 25 lines. `v1-harness-design.md` (94 KB) was revised section by section: "Git Model section only", "Runtime state subsection only … rewords four joined-up compounds the word check refuses. No meaning changes", "Program manager subsection only … rewords 'collapsed events', 'triage', 'mints'".

## What caused it

**Phase 1.** It was there from the start. Document landing reused the developer run path: the developer pool, the full check set, and a deadline inherited from the conversation. That came from ifd.433.21.1, landed Oct 6, which first let a document of an automatically approved kind land through a reviewed run with no operator prompt. The earlier attempt, ifd.100.1, was retired undone after 6 runs and 13 review rounds. The route itself was ruled the same day it was asked for (eek, Oct 11) and built quickly (fp3.1 landed Oct 12; fp3.1.1 and fp3.4 declared the document checks Oct 13 and 14).

**Phase 3.** Earlier factory work introduced it. ifd.429.59.8 (landed Nov 8) made confirmed documents enter the merge queue: "the architect declined the developer's proposal to keep them out". The reason was sound, because nothing should land on a main its checks never saw. But no one costed the throughput. A one-file document with a 7-minute `make doccheck` takes the same serial queue landing as a code change. Two more design choices made it worse:

- The queue order (`internal/orchestrator/mergequeueorder.go`) put the architect's oldest document beside priority 0 code and let only an eight-hour age rule rescue ordinary code. The design was later changed to four hours, but the code still uses eight (ifd-437-84 summary, finding G3-1).
- Nothing batched documents. Each one takes a whole landing (ifd.429.267, open).

**The re-landings.** These come from how the architect works, not from a bug. She revises one section per document write, and each write is a full publication. The vocabulary sweep (ifd.437.21, "through the governed path, one document" at a time) and repeated "bring the design into line with what was built" corrections (hs28, 4ejz, ifd.437.67, .70, .71, .72, .84) multiply the landings. Of the 295 dated revisions in document front matter since Oct 7:

- 63 (21%) are wording-only.
- 33 (11%) catch a design up to code that already shipped.
- 199 are substantive: new designs, rulings for items, and amendments carried from developers.

**The repair chain.** It feeds itself. Each piece of machinery around publication (a work item per publication, a three-return stop, one open pull request per revision, queue admission) produced the next defect. Some examples:

- fp3.8 files a work item for each publication. That led to fp3.11 (those items stay open on withdrawal and carry no goal line) and to ifd.429.211 and ifd.428.174 (the harness looks up a run id as a work item).
- The three-return stop produced fp3.6 (the stop also caught documents that main had moved under).
- g5x (landed Nov 6, close the superseded pull request) did not hold: ifd.429.208 (Nov 16) asks for the same thing, ifd.429.263 (Nov 21) asks again, and fp3.10 (republished while a copy waited in the queue) followed.

## The chain of items

Runs, review rounds and repairs come from the run records in the state directory, matched by work item. They are a lower bound, because a run whose record was removed is not counted.

Origin: **O** is an observed failure with dated evidence. **H** might happen: no failure was cited, or it builds a plan. **R** is a ruling or design item. **F** marks a follow-on of an earlier item in this chain that did not finish the job.

| Item | Short name | Created | Origin | Outcome | Runs / reviews / repairs |
|---|---|---|---|---|---|
| ifd.100.2 | Architect settles publish shape for artifact writes | 08-23 | R | closed 08-31 | 0/0/0 |
| ifd.100.1 | Commit and publish an approved artifact write | 08-23 | O | **retired undone** 09-13 | 6/13/4 |
| ifd.437.21 | Replace coined vocabulary in governed documents, one at a time | 09-27 | H (wording) | closed 11-21 by document revisions | 0 developer runs; many publication runs |
| ifd.433.21 | Documents confirmed by approval policy, committed through a run | 09-29 | O | landed (commit 79b675427, 10-22), closed 11-15 | 1/7/2 |
| ifd.433.21.1 | Auto-approved document lands through a reviewed run | 10-04 | O | landed 10-06 (a6241fbce) | 1/6/6 |
| ifd.437.36 | Amend artifact contract and V1 design on document landing | 10-06 | R | closed 10-14 | 0/0/0 |
| ifd.437.43 | Amend artifact contract on how a write reaches disk | 10-06 | R | **retired undone** 10-06 | 0/0/0 |
| ifd.433.34 | Retry a document when a developer slot frees (F of 433.21.1) | 10-07 | O | landed 10-10 | 2/6/1 |
| ifd.433.21.2 | Retry a landing stopped by nothing that judged it (F) | 10-07 | O | landed 10-09 | 1/4/2 |
| 0bj | Architect submits invariant amendments as document writes | 10-08 | O | landed 11-05 | 2/3/1 |
| ifd.78.2 | Documents for a companion intent repository | 10-08 | O | landed 11-19 | 5/5/1 |
| 4qw | Architect's hold delays only sending | 10-09 | O | landed 10-12 | 2/5/2 |
| g5x | Newer revision closes the older pull request | 10-10 | O | landed 11-06 | 2/2/0 |
| trt | Publication run does not inherit the message deadline | 10-11 | O | landed 10-12 | 1/5/1 |
| eek | Architect rules the document landing route | 10-11 | R | closed 10-11 | 0/0/0 |
| fp3 | Document landing route (parent, P0) | 10-11 | O | **open** | 0/0/0 |
| 66h | Decision records confirmed under the design setting | 10-11 | O | landed 10-12 | 1/5/5 |
| 7my | Coined-word check at write time | 10-11 | O | landed 10-13 | 2/7/5 |
| fbt | A new build never cancels a document landing | 10-11 | O | landed 10-17 | 1/3/2 |
| fp3.1 | Own landing count and declared checks | 10-11 | O | landed 10-12 (99fdabb4f) | 1/5/1 |
| fp3.2 | One waiting revision per document, eight per role, withdraw | 10-11 | O | landed 11-15 | 1/1/1 |
| 3r7 | Put proposals and documents to the operator one at a time | 10-11 | O | landed 11-15 | 1/2/1 |
| bhb | Approved revision lands through a run, not uncommitted in the primary checkout | 10-11 | O | landed 10-13 | 1/7/3 |
| r2i | Record operator approval of a consistent revision | 10-11 | O | landed 11-15 | 1/1/0 |
| p89 | Retry or retire stalled publication pull request #1161 | 10-12 | O | **retired undone** 10-12 | 0/0/0 |
| 6oi | Why the reviewer's session goes silent | 10-12 | O | landed 11-16 | 1/1/0 |
| 94r | Documents awaiting approval on the needs-a-human list | 10-12 | O | landed 10-22 | 1/2/0 |
| fp3.1.1 | Configuration lists document checks (F of fp3.1) | 10-12 | O | landed 10-13 | 1/3/0 |
| bhb.1 | `yoyo artifact approve` lands through a run (F of bhb) | 10-12 | O | closed 11-15 | 1/5/2 |
| ifd.433.21.3 | Development manager retries or retires stopped publications (F of trt) | 10-13 | O | landed 10-13 | 1/7/0 |
| ifd.433.36 | Why publications fail `make test` and end cancelled | 10-13 | O | landed 10-14 | 1/3/1 |
| fp3.3 | Retry after timeout waits only on the document count (F of fp3.1) | 10-13 | O | landed 10-14 | 1/3/2 |
| fp3.4 | Repository declares document checks as one target (F of fp3.1.1, a second configuration item for the same list) | 10-13 | O | landed 10-14 | 1/7/0 |
| 7fl | Factory-flow program manager sees open pull requests and waiting documents | 10-13 | O | landed 11-16 | 1/1/1 |
| ifd.429.75 | Flaky document-wait restart test | 10-13 | O | landed 11-16 | 1/1/0 |
| ifd.437.46 | Revise artifact contract and V1 design (again) | 10-14 | R | **retired undone** 11-15 | 0/0/0 |
| ifd.437.47 | Architect item closes only on a revision of a named document | 10-14 | O | landed 11-18 | 3/5/3 |
| fp3.5 | Document whose linked documents changed goes back first | 10-14 | O | landed 11-17 | 1/4/4 |
| 4u4 | Publication stopped because main's copy changed is not auto-restarted | 10-14 | O | landed 10-15 | 1/3/0 |
| ifd.437.50 | Reconfirm six retired designs and publish one at a time | 10-14 | R | closed 10-14 | 0/0/0 |
| ifd.434.26 | Slack-started landing tested across a new build (F of fbt) | 10-17 | H | open | 0/0/0 |
| uuqo | Dropped forge merge does not tell the owner to revise | 10-21 | O | open | 0/0/0 |
| ifd.433.53 | Every publication run attached to a work item | 10-22 | O | **retired undone**, folded into fp3.8 | 0/0/0 |
| fp3.6 | Fresh return count for a revision against a newer main (F of the three-return stop) | 10-26 | O | landed 11-21 | 1/3/2 |
| ifd.430.69 | Architect rules on the amendment log | 10-26 | R | closed 10-26 | 0/0/0 |
| ifd.430.70 | Proposal records the revision it was written against | 10-26 | H | open | 0/0/0 |
| ifd.430.71 | Document write names the approved proposals it carries | 10-26 | H | open | 0/0/0 |
| ifd.429.59.8 | Documents enter the merge queue | 11-07 | H (guard) | landed 11-08 (8618ace47) | 1/4/0 |
| fp3.7 | Document holding the masking marker goes back first | 11-07 | O | open | 0/0/0 |
| ifd.429.126 | Under human merge approval, pull request only after review | 11-07 | H | open | 0/0/0 |
| ifd.78.5 | Held write already landed elsewhere recorded as landed | 11-08 | O | landed 11-10 | 1/1/1 |
| s4tn | Secret masking never changes document text | 11-09 | O | open | 0/0/0 |
| fp3.8 | Queued publication says so; an unlanded one returns to its author | 11-13 | O | landed 11-21 | 1/4/3 |
| hs28 | Correct five places where designs disagree with the code | 11-13 | O (docs) | open | 0/0/0 |
| ifd.429.188 | CI jobs pass at once for document-only pull requests | 11-15 | H | open | 0/0/0 |
| ifd.432.52 | Auto-approved document never listed as waiting on the operator | 11-15 | O | open | 0/0/0 |
| 4ejz | Two designs brought into line with the code | 11-15 | O (docs) | **retired undone** 11-15 | 0/0/0 |
| ifd.437.65 | Raise the shipped-documentation size limit | 11-16 | O | landed 11-18 | 1/1/1 |
| 2kab | Operator told when his approval is refused for a changed link | 11-16 | H | open | 0/0/0 |
| ifd.429.192 | Queue entry's checks chosen from its paths | 11-16 | H | open | 0/0/0 |
| ifd.429.194 | Stop record says what stopped it each time (F of 6oi) | 11-16 | O | open | 0/0/0 |
| ifd.429.197 | Stop record lists each stop (**duplicate of 429.194**) | 11-16 | O | open | 0/0/0 |
| ifd.429.208 | Newer publication closes the older pull request (**repeat of g5x**) | 11-16 | O | open | 0/0/0 |
| ifd.437.67 | Reword three designs after a rename | 11-16 | H (wording) | closed 11-21 | 0/0/0 |
| ifd.429.211 | No false tracker error from a run id lookup (F of the publication item) | 11-17 | O | open | 0/0/0 |
| ifd.437.70 | Correct the checks-on-CI design example | 11-17 | O (docs) | closed 11-18 | 0/0/0 |
| ifd.437.71 | Reword a management design line | 11-18 | H (wording) | closed 11-19 | 0/0/0 |
| ifd.437.72 | Check six designs against what landed | 11-18 | O (docs) | open | 0/0/0 |
| ifd.437.73 | Raise the documentation size limit or trim (F of 437.65) | 11-18 | O | open | 0/0/0 |
| ifd.437.75 | Size check reports room left | 11-18 | H | open | 0/0/0 |
| fp3.9 | Old publication run of a closed item never holds a slot | 11-18 | O | landed 11-19 | 1/2/1 |
| ifd.430.82 | Auto-approved document confirmed at once | 11-19 | O | open | 0/0/0 |
| ifd.430.83 | Masked long secret in repository reads | 11-19 | O | open | 0/0/0 |
| fp3.10 | No republish while a copy waits in the queue (F of g5x and 429.59.8) | 11-19 | O | landed 11-21 (0e04799c6) | 1/1/0 |
| ifd.428.174 | Carry-out notes reach a document run through its items (**near-duplicate of 429.211**) | 11-20 | O | in progress | 1/0/1 |
| ifd.437.81 | Amend the artifact contract for fp3.6 | 11-20 | R | open | 0/0/0 |
| ifd.78.9 | Written documents keep their links (plan piece 1) | 11-20 | H | in progress | 2/1/0 |
| ifd.78.10 | Unpublished documents overlapping work are listed (plan piece 2) | 11-20 | H | open | 1 run record, no developer session |
| ifd.78.11 | Architect notes that a revision agrees with an item (plan piece 3) | 11-20 | H | open | 1 run record, no developer session |
| ifd.429.255 | Architect rules on code waiting behind documents (F of 429.59.8) | 11-20 | O / R | closed 11-20 | 0/0/0 |
| ifd.436.24 | Running figure separates document publications | 11-20 | O | open | 0/0/0 |
| ifd.430.91 | Status names what a waiting document waits on | 11-21 | O | open | 0/0/0 |
| ifd.429.263 | Close superseded publication pull requests (**third ask after g5x and 429.208**) | 11-21 | O | open | 0/0/0 |
| ifd.435.86 | Review documents on the cheaper model after a trial | 11-21 | O (cost) | open | 0/0/0 |
| ifd.429.266 | No two document candidates in a row; four-hour age rule (F of 429.59.8) | 11-21 | O | **open, P0** | 0/0/0 |
| ifd.429.267 | Batch up to eight documents per candidate (F of 429.59.8) | 11-21 | O | open | 0/0/0 |
| ifd.429.268 | Daily queue-wait figure by kind | 11-21 | H | open | 0/0/0 |
| fp3.11 | Publication item closed on withdrawal and records its goal (F of fp3.8) | 11-21 | O | open | 0/0/0 |
| ifd.437.84 | Architect reviews every design against the code | 11-21 | O (docs) | open | 0/0/0 |

**Count.** There are 89 items:

- 36 landed code changes.
- 11 closed as rulings or document revisions.
- 6 retired undone.
- 36 open or in progress.

Roughly 30 are follow-ons (F) of an earlier item in the chain. About 14 are "might happen" (H) items or build pieces of a plan. About 12 are document upkeep (catching designs up to code, or rewording).

**The document stream itself.** This is separate from the items:

- 416 publication runs since Oct 7: 309 succeeded, 81 failed, 16 timed out, 10 cancelled.
- 347 review rounds on those runs.
- None of them had a developer session.
- 301 document landings on main.

Before the route existed, 128 docs-only changes had landed through ordinary developer runs (Aug 14 to Oct 12).

## Was it fixed

**Phase 1 (documents could not land): yes.** After fp3.1, trt, fp3.1.1, fp3.3 and fp3.4 landed (Oct 12 to Oct 14), publication runs went from 2 successes in 41 (Oct 7 to Oct 12) to 271 of 310 (Oct 15 onward). Most later failures are ordinary review refusals, conflicts and build drains. The fix held.

**The repeat copies and stale pull requests: partly, after several tries.**

- g5x (landed Nov 6) did not stop copies. On Nov 19, machine-home had four open publication pull requests (#1676, #2158, #2162, #2163), which is why fp3.10 was filed.
- fp3.10 landed Nov 21 at 00:19 PST. I cannot yet tell whether it held. After it, v1-harness-design still landed four times that day (07:12, 08:49, 09:47, 11:40 PST), but those were successive revisions landing in turn, which is what fp3.10 allows.
- ifd.429.263 (Nov 21) reports 126 old harness pull requests no run record names. The leftover clean-up is not done.

**fp3.9 (an Oct 11 publication run holding a developer slot from Nov 16 to Nov 19).** It landed Nov 19. I did not find the symptom in the watch log after that, but I did not check exhaustively.

**Phase 3 (code waiting behind documents): no.**

- The ruling exists: ifd.429.255, closed Nov 20, revised the merge-queue design.
- The build pieces (ifd.429.266, .267, .268) are open. ifd.429.266 was raised to P0 at 17:42 PST on Nov 21.
- The parent route item fp3 is still open at P0, six weeks after the operator's direction.
- Today, Nov 21, 30 documents landed against 21 code changes.

**Design accuracy: no.** The review of Nov 21 found 6 of 53 designs current, 28 partly stale and 19 mostly plans, and 82 places where the code does something else.

## Was it real

**Phase 1: real, and it reached the operator.**

- The architect's output was stuck for about a week.
- The operator named it a P0 on Oct 11 and restored a goals file by hand in the primary checkout (interventions.jsonl, Oct 11 22:50 UTC, item bhb).
- 31 or more pull requests were left open.
- The items from that week (433.21.x, trt, fbt, 7my, bhb, 66h, fp3.1 to fp3.4, 4u4) were nearly all observed failures with dated runs and pull requests.

**Phase 3: real, and it costs shipped code.** It is measured in the queue's own records, and it produced conflict hand-backs that each cost a developer run. Nobody measured it until Nov 20, twelve days after the cause landed.

**The re-landings and the upkeep: real cost, doubtful value.**

- A document that lands 11 times in two days is not a defect, but each landing costs a run, a review (about $0.66) and a queue slot.
- About a fifth of all revisions are wording-only.
- On Nov 20 the operator's assistant had to tell the architect that "the cost design on main is your mistaken rewrite, not the text the operator approved". The route does not stop a document that is wrong but well-formed.

**"Might happen" items.** About 14 items cite no failure or build a plan:

- ifd.434.26 (Slack-started landing across a build)
- 2kab, ifd.429.192, ifd.429.188, ifd.429.126 (human merge mode, which this repository does not use)
- ifd.430.70 and .71
- ifd.78.9 to .11 (the plan "unpublished designs and admitted work", itself rated "mostly a plan")
- ifd.437.75, ifd.429.268

None of these has landed except as runs in progress.

**Split.** About 60 items are observed failures and about 29 are hypothetical, rulings, or wording upkeep.

## Where the effort was wasted

1. **Putting documents into the code queue without costing it (ifd.429.59.8).** One item, but it led to ifd.429.255, .266, .267, .268, fp3.10 and ifd.436.24, and to every conflict hand-back from code waiting 6 to 9 hours. Documents took about 141 queue landings from Nov 8 to Nov 21, at about 21 minutes each. That is roughly 49 hours of serial queue time, at a time when the queue, not the developer slots, limited shipped code. The Lead PM's evidence on fp3 says so directly: "the limit on shipped code is the queue, not the seats".

2. **Re-publishing the same files.** 222 of the 301 document landings re-landed a file that had already landed. If the architect had batched her section edits, or if documents had been batched in the queue (the open ifd.429.267), these could have been a few dozen landings. At $0.66 per review, the 188 extra landings by title cost about $125 in reviews alone, plus their queue slots.

3. **Repeat fixes for the same symptom.** Closing superseded pull requests was asked three times (g5x, ifd.429.208, ifd.429.263) plus fp3.10. The stop record was asked twice (ifd.429.194, .197). The run-id-as-work-item lookup was asked twice (ifd.429.211, ifd.428.174). The document check list took two configuration items (fp3.1.1, fp3.4). The artifact-contract amendment was filed four times (ifd.437.36, .43, .46, .81), two of them retired.

4. **Designs that were never built.** 19 of the 53 designs are mostly plans, 373 KB of the 1.5 MB, and 55 document landings revised them. scheduled-message-intake was revised 11 times with 3 of its pieces built. Older plans have stood for weeks with no build: choosing-checks-by-change, repeated-checks, orchestrator-boundaries, one-label-per-item, fewer-lifecycle-states, follow-ups and forward-progress. Every design is marked `status: active`, so nothing tells a reader which is which.

5. **Upkeep the stream creates for itself.** Designs written ahead of the code then need "bring it into line" items (hs28, 4ejz, ifd.437.67, .70, .71, .72, .84), and each correction is another publication. The shipped-documentation size limit has been raised and argued over twice (ifd.437.65, .73, .75).

**Lasting value.**

- The October route items (about 15 items, perhaps $150 of runs) fixed a real stall that stayed fixed. That is good value.
- The decision records and operating rules (27 and 16 landings) carry the operator's directions and are read by every role. That is plausible value.
- The roughly 30 follow-ons, the ~14 hypothetical items, and most of the re-landings and the upkeep they create add little.

My estimate is 30 to 45% of the thread's effort has lasting value.

**Cost (estimate).**

- Machinery: 55 developer sessions × $3.83 + 149 review rounds × $0.93 ≈ $350.
- Document reviews: 347 × $0.66 ≈ $229. Add up to about $93 if each queue candidate is reviewed again ("checks and independent review" on the exact candidate; I could not confirm it is a separate session).
- Writing: the architect was 9% of $5,286 spent Nov 6 to Nov 20 (`docs/designs/reducing-what-the-factory-costs.md`), about $33 a day. Over about 46 days that is roughly $1,200 to $1,800, most of it writing and revising documents.
- Not counted: the Lead PM's admissions and the development manager's triage of publications.
- Total: about $600 to $2,500. The larger cost is not in dollars: half the queue's landings in the busiest week, and code waiting most of a working day.

## What would have been better

1. **Keep documents off the code path entirely, as the October ruling intended.** A docs-only change under `docs/designs`, `docs/decisions` or `docs/product` cannot break the build if `make doccheck` passes on the candidate. Documents could land through their own queue lane, batched (the open ifd.429.267), with the full checks run on main afterwards, as rule 3 of the landing-route design already accepts. The Nov 7 ruling put them back in the one queue to protect a guarantee. It should have come with a throughput estimate: at about 21 minutes a landing and 30 or more documents a day, documents take half the queue.

2. **One publication per working session, not per section.** Ten section-sized landings of v1-harness-design in 30 hours should have been one or two. A rule could limit each document to one publication a day, or hold revisions until the role's pass ends. That would have cut most of the 188 extra landings, together with their reviews, queue slots and pull-request clean-up items.

3. **Mark built versus plan, and stop revising plans.** A status per design, or per section, would have made the Nov 21 finding visible weeks earlier. A design should not be revised again until a build piece of it has been admitted. Several plans were revised repeatedly with nothing admitted to build them.

4. **Stop after the second fix for the same symptom.** Closing superseded pull requests (three items plus fp3.10) and the run-id lookup (two items) should each have gone to one diagnosis, as the fp3 children did in October, rather than to new near-identical items.

5. **Leave the wording sweep out of the governed path, or do it in one change.** Rewording coined terms one document at a time through a reviewed, queued publication spent dozens of landings on "no meaning changes". One reviewed change, or simply not doing it, would have been better.

## What I could not establish

- What the architect's and Lead PM's writing and admitting actually cost. The $1,200 to $1,800 figure extrapolates the 9% architect share measured Nov 6 to Nov 20.
- Whether a queue candidate made from a single document gets a second review session separate from the publication run's review. The commit body and design say the candidate is checked and reviewed, but I did not find a separate record.
- The exact queue time per document candidate. I used the measured 21 to 22 minutes per landing for all entries. My join of run records to merge commits for waits before Nov 18 gave implausibly long code waits (27 to 63 h), probably from matching re-runs of the same item, so I relied on the Nov 19 to Nov 20 figures that agree with the operator's assistant's measurement.
- Whether fp3.10 (landed Nov 21, 00:19 PST) has stopped repeat copies. There is less than a day of evidence.
- Run counts for items whose run records were removed. The per-item figures are lower bounds.
- How many of the 199 "substantive" revisions changed something a developer later built from. I classified revisions only by their stated reason.
