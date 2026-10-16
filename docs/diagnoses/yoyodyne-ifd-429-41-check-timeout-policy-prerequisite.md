# Individual check timeout recovery needs architectural reconciliation

**Resolved.** The architect has since reconciled the design:
[Recoverable and terminal failures](../designs/recoverable-and-terminal-failures.md),
under **The checks and the review**, now treats a check stopped by a time limit as
unfinished and allows two reruns within three times its limit
(yoyodyne-gms). The load-scaled check limits and those bounded reruns were then
built on main by the rebuild of this item (yoyodyne-ifd.429.41.1). What follows
is the record as it stood before either.

Each check's own time limit scales with machine load
(yoyodyne-ifd.429.41) remains unfinished. Its required timeout recovery
contradicts the governing recovery design. This change records that prerequisite
and the remaining work; it enables none of the proposed behavior.

## The prerequisite

The work item's description requires load-scaled individual check limits and
bounded continuation after a timeout, without spending a developer repair
attempt. It also requires a durable continuation count and cumulative time
allowance shared with stage continuations, preservation on exhaustion, and an
explicit account that the unfinished check's cause remains unresolved.

[Recoverable and terminal failures](../designs/recoverable-and-terminal-failures.md),
under **The checks and the review**, instead says that a check ended by timeout
is a failed check and must not be retried. Load justifies more time but cannot
establish that a change did not make the command hang. The work item addresses
that uncertainty through finite shared allowances; the design has not yet
admitted that distinction.

The architect must reconcile that design through its governed amendment path
before the requested recovery behavior is enabled. This developer item grants
no write to the design, and a developer cannot invoke the architect. No design,
product document, decision record, project configuration, or tracker record was
changed by this diagnosis. A proposal in a run's reply is not proof that its
owner has amended the artifact.

Release this item for implementation after the architect's recorded design
permits the bounded distinction, or after the work item is revised to match the
architect's ruling. This is a delegated architectural decision, not a request
for the operator's routine approval.

## What the earlier candidate established

The harness retained the implementation in checkpoints `2641519a`, `59f7ef44`,
and `91cb1a7c` on this item's branch. They are evidence of an attempted
implementation, not an approved recovery policy. The candidate implemented
scaling, durable shared allowances, unresolved exhaustion routing, and refusal
of passing gate evidence for unfinished checks. Its final focused unit tests,
race checks, vet, and formatting passed in the developer run. Independent review
found the behavioral completion conditions supported, but rejected enabling
them while the design still contradicted them.

An earlier review also found that an exhausted reservation could stop a restart
before recording a new stage. When the preceding stage failed or was
interrupted, neither timeout flag was set, so the stoppage was not docketed for
the development manager. The repair recorded exhaustion independently of the
preceding stage and tested preservation, durable docketing after restart, and
integration refusal. That requirement belongs in the eventual implementation
even when no timeout flag on the last executed stage identifies the stop.

The last review correctly rejected calling executable policy changes
diagnostic evidence: integrating them would activate the very behavior still
awaiting architectural reconciliation. All 47 paths changed by the candidate,
including runtime code, tests, snapshots, configuration templates, and user
documentation, have therefore been restored to the pre-item tree
`172b8b5911f16bc46c0fdd5f68f557178de9866b`. The final change against that tree is
this diagnosis alone. Existing stage timeout behavior is left as that tree
carried it; the item adds neither individual timeout continuation nor shared
time allowances.

## Work still required after reconciliation

- Scale every individual check's configured limit by the stage's load rule,
  with a finite ceiling and no allowance above the scaled stage limit.
- Reserve a finite cumulative time allowance before execution, and share it
  and a finite continuation count across individual and stage timeout paths,
  automatic and decided continuations, and process or session restarts.
- Record configured and scaled limits, load, command, tested revision, and
  allowance consumed. Preserve prior executed-stage results when a later stage
  is refused because the allowance is exhausted.
- On exhaustion, stop automatic continuation, preserve the branch, worktree,
  and developer session, and durably route the unresolved stoppage to the
  development manager, including after interruption or returned failure.
- Keep returned command failures as failed checks and refuse integration for
  unfinished checks, even if older passing evidence remains on the record.
- Restore regression coverage for ordinary and heavy load, cross-path
  continuations, restart persistence, count and time exhaustion, preservation,
  durable notification, and gate refusal. Update editable configuration and
  operations documentation to describe the architect's admitted policy.

The earlier candidate chose a tenfold scaling ceiling, at most two shared
continuations, and a cumulative reservation of twenty times the initially
configured stage limit. It consumed full reserved stage budgets even after an
early failure or interruption, so a crash could not restore possibly spent
time. These were implementation choices in that candidate, not architectural
decisions established by this diagnosis; the next implementation must fit the
architect's reconciliation and re-run its focused checks.
