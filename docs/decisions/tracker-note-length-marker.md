---
id: tracker-note-length-marker
kind: decision
title: Each tracker note the harness writes opens with a line giving its length
supports:
    - beads-durable-workflow-store
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-08T23:31:16.048552Z
      reason: 'Answers the reviewer''s reports report-bb1e3b5f and report-70a15869 (the same report filed twice on October 27): the note length marker added by the tracker read fix changed how every tracker note is stored, and nothing recorded the choice or its limits.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-08T23:31:16.048552Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2489, for document-2489.1 under the automatic approval policy
---
# Each tracker note the harness writes opens with a line giving its length

## Decision

Every note the harness adds to a work item in the tracker starts with one line recording the note's length in bytes, written as an HTML comment: `<!-- yoyodyne-note-bytes: N -->`. The fix for how the harness reads the tracker (yoyodyne-ifd.433.25) added it, so that a bounded read of an item's notes can tell where each note ends.

The marker is kept inside the note text. These rules go with it:

- Only the harness writes the marker, and only once, at the moment it adds the note.
- Notes written before the marker existed are never rewritten to add one. A work item's notes are only ever added to, never rewritten, and a note without a marker is read as it was before.
- Anything the harness shows a person, such as a briefing, status, the dashboard, Slack, or a role's read of an item, shows the note without the marker line. Showing a person an HTML comment would break the plain-language goal.
- `bd show` and the committed tracker export show the marker as written. They are Beads' own views of what it stores, not surfaces of the harness, and that is accepted.

## Alternatives rejected

- **Recording where each note ends outside the note text.** A separate store of note boundaries would be a second record of work beside Beads, which [the decision to keep Beads as the durable store of work](beads-durable-workflow-store.md) rules out. Changing how Beads itself stores notes would be replacing part of Beads, which the v1 non-goals exclude.
- **No marker.** Without it, a read that has to stop part way through an item's notes cannot reliably say which notes it returned whole and which it cut.

## What decided it

Beads is the only durable store of work, and the harness adds to it rather than changing what it stores. The marker is the only place to record a note's length that needs no second store and no change to Beads.

## Not known when this was recorded

Whether every surface of the harness already leaves the marker out was not checked against the code when this was written. A surface that shows it to a person is a defect against this decision and against the plain-language goal.

This is a decision rather than an architectural invariant, because it binds one writer and its readers rather than work across the repository.