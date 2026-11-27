---
name: yoyo-operator-questions
description: Use when the operator asks "anything waiting on me?" or "do you have questions for me?", or when you see yoyo work held up waiting on him. Gathers everything yoyo says waits on the operator, settles what your standing permissions allow, and asks his real decisions one at a time, the one with the most effect on the others first.
---

# Asking the operator what only he can decide

*This document is a prompt, and everything below the rule is addressed to an
agent rather than to you.* Give it to the coding session you use as your
assistant, in the repository yoyo runs on — paste it, point the session at this
file, or install it as a skill:

```sh
mkdir -p ~/.claude/skills/yoyo-operator-questions
curl -fsSL https://raw.githubusercontent.com/mason-bryant/yoyodyne/main/skills/yoyo-operator-questions/SKILL.md \
  -o ~/.claude/skills/yoyo-operator-questions/SKILL.md
```

Then ask it "anything waiting on me?". It sits beside
[the setup prompt](../yoyo-setup/SKILL.md), and is part of
[yoyo's documentation](../../README.md#further-reading).

---

## What this is for

The operator is the person yoyo works for. Yoyo lists what waits on him, but a
list is not a good way to ask a person anything: many entries are steps you
can settle, things he already answered, or another role's work, and the real
decisions are mixed in with them. Your job is to turn that list into the few
questions only he can answer, asked one at a time, and to carry out each answer
where every role will read it.

Run every command below from the project's own directory. Every command that
gathers is read-only. Every command that records something is named in the
step that uses it, and runs only after the operator's answer, or under a
standing permission he gave you.

## 1. Gather

Read all of these, as JSON, before saying anything:

- `yoyo status --json` — the main source. Two parts of it matter:
  - `summary.decisions.entries` is what yoyo says only he can do. Each entry has
    a `line`, and a decision entry has an `account`: `changes` (with `before`
    and `after` for a change to a document), `why`, `serves`, `if_approved`,
    `if_declined`, `why_yours`, and `missing`, which names any part the record
    lacks. Say what `missing` names rather than filling it in yourself.
  - `standing.needs_human` is every entry waiting on a person or a role. Each
    has a `kind`, an `id`, a `mover` (who moves next), and `ownership` with the
    `owner`, the `reason`, the `remedy`, and the `capability` the remedy needs,
    plus `what` and `whose` in words. Only an entry whose `mover` is
    `operator` is his. Every other entry is a role's, the harness's, or the
    forge's: report it as theirs, with its `whose`, and do not ask him about it.
- `yoyo amendment list --json` — proposed changes to documents still undecided.
- `yoyo gate list --json` — the steps only a person can take, which are taken
  and which still wait.
- `yoyo directive list --json` — directions he gave that are still open,
  including questions an agent could not settle without him.
- `yoyo reports --json` — what the roles reported. A report addressed to him
  already appears in `standing.needs_human`; read the rest only for context.

An entry whose `mover` is `operator` always carries a `reason` from a closed
list: `"fundamental-intent"`, `"credential"`, `"repository-setting"`,
`"protected-file"`, `"human-gate"`, `"own-hold"`, `"diverged-history"`. The
reason is why it is his. Use it when you tell him why he is being asked.

If a part of the status says it could not be read, say so plainly and name
which part. Do not treat what could not be read as empty.

## 2. Leave out what he already settled

Never ask again what the operator already answered. Before asking anything,
look for his earlier answer in:

- `yoyo directive list --all --json` — every direction he gave, including the
  ones already resolved or withdrawn;
- `yoyo amendment list --all --json` and `yoyo amendment show <id> --json` —
  proposals already decided;
- `yoyo gate list --json` — steps already recorded as taken;
- `yoyo intervention list --json` — what he did by hand;
- this conversation, and any standing instructions he gave you.

Where his earlier answer covers an entry, the entry is settled: say which
answer settled it and carry that answer out (step 5) if it was never carried
out. If his answer only partly covers it, ask only about the part it leaves
open, and say what he already decided.

## 3. Settle what your standing permissions allow

Your standing permissions are what the operator has told you in writing that
you may do without asking him each time: his instructions to you, and
[the operating rules](../../docs/product/operating-rules.md) every role works
under. **You settle only what those permissions allow.** If you cannot point
to the sentence that allows it, it is a question for him, not something you
settle.

For each entry you settle, report it with its evidence: the entry (its `kind`
and `id` with its plain words), the permission that allowed it, quoted, the
command you ran, and what that command printed. An entry you report as another
role's carries its `mover` and `whose` as its evidence.

Some things you never do on your own authority, whatever a permission seems to
say:

- **Never record a step only a person can take.** `yoyo gate record` writes
  that a person took a step. Hand him the command with the step's name, its
  `--for`, `--by`, and `--did` filled from what he tells you, and let him run
  it himself. Run it for him only if a written rule of his says you may, and
  then only for a step he has just told you he took.
- **Never record an approval he did not give.** Approving a document, a
  proposal, or a concern's answer is his act. You record one only as the
  answer he gives you in this conversation, or as a written rule of his
  already allows, and the record says it was his.
- **Never handle a credential.** A `"credential"` entry is his to sign in to;
  never read, print, or store a secret to make it run without him.

## 4. Ask his real decisions, one at a time, by effect

What is left is his real decisions. Put them in order before asking the first:

1. **Most effect first.** Ask first the question whose answer changes or
   removes the most other questions: one that decides a document several
   proposals depend on, a hold that keeps other work waiting, or a goal change
   that settles a concern.
2. **Then what holds up the most work**, by the work items each entry names.
3. **Then the oldest.**

Ask one question per message, and wait for the answer. Each question has this
shape:

```
Question 2/~5: approve the change to the operating rules (document-12.1)?

The Lead Product Manager wrote a change to the operating rules (operating-rules)
saying work that is not a bug is priority 0 only when you say so. You are asked
because it changes what the goals admit. Two proposals wait on it.

1. Approve (recommended) — the rule lands in the operating rules, and the two
   proposals that cite it are put to their owners.
2. Decline — the rule is not added; the Lead Product Manager is told why and the
   two proposals stay waiting.
3. Ask about it first — I put your question to the Lead Product Manager and
   nothing is decided until you answer.
```

- **The count**, such as `1/~5`: which question this is, and about how many are
  left. The `~` is because an answer can remove questions or add one.
- **Two to four sentences of context**: what is being decided, why it is his
  (from `why_yours` or the `reason`), and what it holds up.
- **Every id beside plain words**, the first time and every time: "the change
  to the operating rules (document-12.1)", never "document-12.1" alone.
- **Two to four choices**, the recommended one listed first and marked
  "(recommended)", each saying what it sets in motion. Base the recommendation
  on the entry's own record — `if_approved`, `if_declined`, the writer's
  `why` — and say so if you have no good basis for one.

After each answer, carry it out (step 5), then **plan again**: gather again if
the answer may have moved anything, drop what it settled, re-order, and give
the new count with the next question. Stop when nothing is left, or when he
says to stop; in that case, say what is still waiting.

## 5. Carry out each answer where every role reads it

Use the command that records each kind of answer. Each is his act, carried out
because he answered:

- **A proposed change to a document** (`kind` `amendment`):
  `yoyo amendment approve <id> --reason <his words>` or
  `yoyo amendment decline <id> --reason <his words>`.
- **A document a role wrote in a conversation** (`kind` `document-approval`,
  with its `document_approval.write_id`, such as `document-4.1`): send the
  answer to the conversation that wrote it, naming the document:
  `yoyo agent chat <agent> --message "approve document-4.1"`, or
  `"decline document-4.1 <his reason>"`. For the Lead Product Manager,
  `yoyo chat --message` is the same conversation.
- **A question a role stopped on** (`kind` `concern`, with its
  `concern.concern_id`, such as `c3.1`, and its `concern.options`):
  `yoyo agent chat <agent> --message "answer c3.1 <his answer>"`, in the
  conversation that raised it.
- **A step only a person can take** (one `yoyo gate list --json` shows still
  waiting): he takes the step,
  then `yoyo gate record <name> --for <subject> --by <who> --did <what he did>`,
  which he runs himself (step 3).
- **A hold he placed** (`reason` `"own-hold"`): `yoyo resume` lifts a pause on
  spending and `yoyo release` lifts a hold on taking new work, once he says to.
- **A direction for the roles** that none of the above records — a priority, a
  rule, an answer to a question asked outside yoyo:
  `yoyo directive record --received-by product-manager <what he said>`. A
  directive reaches every role whichever one heard it. An open directive he
  answered is closed with `yoyo directive resolve --resolution <his answer> <id>`.
- **An entry whose remedy is a step outside yoyo** — a repository setting, a
  protected file, a branch whose history diverged: give him the `remedy` and
  the `capability` it names. Do not take the step for him.

After each command, read what it printed. If it refused, say so with its
message, and do not count the answer as carried out.

## 6. Close

End with one list, one line each: every decision he made, and what was done
about it — the command and what it printed — and every entry you settled or
reported as another role's, with its evidence. Then say what is still waiting
on him, if anything, and why.
