---
id: each-guarantee-has-one-owner
title: Each guarantee is kept by its one owning component and reached only through its way in
status: active
established_by:
    - one-owner-for-each-guarantee
    - yoyodyne-ifd.428.126
revisions:
    - action: created
      by: architect
      at: 2026-11-22T18:19:28.435174Z
      reason: The design for one owner for each guarantee states this invariant's wording and says it waits only for the architect to be able to record invariants from her conversation. That is now possible, so it is recorded with the design's own wording and the ways in its reviewer checks name. Found while reviewing that design against the code under the design review item.
---

## Must hold

Each guarantee named in [the design for one owner for each guarantee](../../designs/one-owner-for-each-guarantee.md) is kept by its one owning component, and code outside that component reaches it only through its way in. A change that needs a guarantee to behave differently changes the component, never a copy beside it.

In particular, no code outside its owner adds: a whole save of a run record other than the lease holder's versioned save; a slot read followed by a later start instead of a reservation; a paid turn without permission from `internal/budget`; a cost event parsed outside `internal/cost`; a tracker command run outside `internal/beads`; a configured check launched outside `internal/checks`; or a routine read of all history instead of the durable indexes. The list of places that still go around a component, kept by that design's checks, only ever shrinks.

## Why

A change can be correct for its own path and still add a second copy of a guarantee, and copies drift apart: the limits on paid turns came to disagree because each tool kept its own. A rule that only the design states binds only the work that reads the design, so it has to bind every change, including ones that never mention it. A list of exceptions that may grow would take the next hurried fix, so it may only shrink.
