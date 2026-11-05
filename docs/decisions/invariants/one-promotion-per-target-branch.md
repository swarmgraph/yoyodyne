---
id: one-promotion-per-target-branch
title: At most one promotion per target branch, taken by the harness
status: active
established_by:
    - yoyodyne-ifd.48
    - yoyodyne-ifd.429.22
    - yoyodyne-7o2
revisions:
    - action: created
      by: architect
      at: 2026-08-18T13:56:16.917375Z
      reason: Settled with the operator on 2026-08-18 as the concurrency invariant when integration contention was turned from a race into an ordered queue.
    - action: amended
      by: architect
      at: 2026-11-05T17:22:24.133182Z
      reason: 'Brings the invariant up to the merge-queue design (the lease covers only the guarded change and is never held through checks, review or provider work) and the design for working alongside other contributors (across machines the guarantee is the remote''s own compare and swap: a pinned pull request merge or a push with a lease naming the candidate''s base, never a forced target, and a moved target means the candidate is rebuilt without charging a repair). The contributors design named this amendment as waiting on invariants becoming writable, which has now happened. Rejected: a shared lock service, which is state outside Git and the forge; and leaving the cross-machine rule to the design alone, which a promotion path written later would never read.'
---

## Must hold

On one machine, at most one promotion per target branch happens at a time, enforced by a lease in the harness's run state store that the promoting run's own harness acquires before it promotes and releases once the promotion settles. The lease covers only the fresh read of the target, the guarded change to it, and the durable record of that change; it is never held through checks, review, AI provider work, or a long wait on the remote.

Across machines, and against anyone else who can move the target, the guarantee is the remote's own compare and swap. A protected target moves only through its pull request merge naming the exact reviewed commit. An unprotected target is pushed only with a lease naming the target the candidate was built on. No promotion forces the target. A promotion that finds the target has moved does not land: its candidate is built again on the new target and earns its checks and independent review again, and no repair is charged for the move.

No agent acquires, releases, or otherwise touches that lease, and no agent performs a promotion.

## Why

Development is parallel and integration is serial. A promotion reads where the target branch is and then moves it, so two of them interleaved is a race every concurrent run can lose rather than two promotions. Putting the serialization in the store rather than in agent behaviour is what makes it hold across processes on one machine and survive a crash: the lease is an advisory file lock the operating system drops when its holder dies, so a killed promotion leaves no stale lock and reconciliation settles the half-finished work behind it.

That lock lives on one machine, so another copy of the factory, or a person merging through the forge, never sees it. The remote's compare and swap is the one place every contributor's promotion meets, so making it the guard holds whether the other contributor is a person or a factory, and needs no shared lock service outside Git and the forge, which the design for working alongside other contributors rules out. A moved target is not the candidate's fault, which is why rebuilding it charges no repair.

The lease is held only for the guarded change because holding it through checks or review would put all verification behind one lock, and one stuck provider call would stop every landing. Keeping agents away from it is the same boundary that keeps them away from the merge: the roles that authorize a promotion must not be able to perform one.
