---
id: claude-execution-and-account-routing
kind: design
title: 'Execution routing: pinned invocations, developer slots, and safe provider fallback'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:39:19Z
      reason: promoted from the operator's multi-model execution and account routing brief under the 2026-08-22 mandate; deviations recorded in the promotion, with the capacity-blocked state carried as deviation-to-implement from the observability promotion
    - action: amended
      by: architect
      at: 2026-09-07T00:30:00Z
      reason: yoyodyne-ifd.306 - conversation account failover designed, turn-granular, durable-record rebuild exercising provider-independence, named-account affinity with return at window reopen, per-turn alias attribution with failover reason, cost paid in context reconstruction only while the window is closed; configuration is a pooled named endpoint plus operator-local enablement
    - action: amended
      by: architect
      at: 2026-09-07T19:30:00Z
      reason: yoyodyne-ifd.337 - the provider-general contract moves to provider-adapters-and-endpoints; this document keeps the Claude-specific configuration and capacity semantics
    - action: amended
      by: architect
      at: 2026-10-05T15:03:53.470705Z
      reason: 'Complete Design per-developer-slot endpoint selection and cross-provider run failover (yoyodyne-ifd.435.13): replace obsolete Claude-only and no-fallback restrictions; specify stable slots, endpoint precedence, bounded developer and reviewer fallback, persisted transitions, recovery and legacy behavior, attribution, acceptance cases, and narrow adoption of the operator''s four-slot mapping. Preserve provider eligibility, account boundaries, independent review, and existing recovery rules outside the explicit quota-fallback refinement.'
approvals:
    - revision: 3
      by: operator
      at: 2026-10-05T15:03:53.470705Z
      reason: approved by the operator in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 45, for the document the architect wrote there (document-45.1)
---

# Execution routing: pinned invocations, developer slots, and safe provider fallback

## Purpose and governing boundaries

This design serves v1-goals: keep roles, policies, and provider selection configurable without making safety invariants optional, and support multiple providers behind one adapter contract. It also serves the standing autonomy and plain-language goals: eligible fallback proceeds without another operator decision, and every wait or switch explains what happened.

[Provider adapters and execution endpoints](provider-adapters-and-endpoints.md) continues to own adapter obligations, capabilities, role eligibility, endpoint identity, and provider-independent evidence. [Recoverable and terminal failures](recoverable-and-terminal-failures.md) continues to own failure classification, recovery windows, and protection against duplicate effects. This document specifies slot selection and automatic fallback for actual developer and reviewer invocations, including initial work, repair, and continuation.

Both configured providers may serve eligible roles. Earlier statements in this document that every role must use Claude Code, that the Codex adapter is deferred, or that automatic fallback is unavailable are withdrawn. This revision does not authorize additional providers, account enrollment, weakened permissions, or a new account-pooling implementation. Conversation routing retains its separately configured policy; developer fallback does not modify management-role configuration.

The design is delivered through Design per-developer-slot endpoint selection and cross-provider run failover (yoyodyne-ifd.435.13). Implementation and live adoption belong to Implement and adopt per-slot developer routing with cross-provider run failover (yoyodyne-ifd.435.14). These responsibilities do not imply that either implementation or adoption has occurred.

## Decisions and rejected alternatives

Use stable developer slot numbers with an explicit ordered pair of endpoints. Resolve and record the pair before execution; pin each actual invocation to one endpoint. When a classified usage limit prevents execution, automatically choose the eligible alternate at a safe invocation boundary, preserving the logical operation and all existing work.

A shared developer model, label-only model overrides, or differently named developer agents cannot express the required one-versus-three mapping reliably. They are therefore not substitutes for slots. A running invocation cannot be moved to another provider: terminate or reconcile its execution first, then start a separately recorded invocation. Provider-native sessions cannot be the only recovery record, and cannot cross provider or account boundaries.

Allow one automatic endpoint switch per logical operation. This prevents quota failures and restarts from creating an unbounded alternation of billable requests. Ordinary transient recovery keeps its existing limits; fallback is not a way to reset them. Prefer existing configuration loading, account selection, run persistence, and launch boundaries over a second routing service.

These are enforceable requirements of this design. This revision does not create a new repository-wide invariant; it applies the existing provider-independent state and safety constraints to routing.

## Slot identity and endpoint precedence

A developer slot is a stable positive number in the product's configured developer capacity, not a PID, provider account, agent name, work label, or position in a transient worker list. The first through fourth configured slots are slots 1 through 4. Persist the slot number when a run claims capacity, and retain it through developer invocations, repairs, waits, and resumes while the run owns that slot. Releasing execution capacity during a wait does not erase the run's routing identity or authorize a second worker on its worktree.

A resumed run must reacquire its slot's execution capacity before launching. Another run using that capacity may delay the resume; it must not silently renumber it. Run ownership and slot capacity are separate: both must be valid before execution. A scheduler restart reconstructs ownership from durable records rather than numbering live processes again. Configuration cannot remove or reinterpret an occupied slot; reject that change until it can be applied without changing existing owners.

Each slot may specify a primary and one alternate, using the existing endpoint vocabulary: provider or backend, model, and an account alias or an approved account-selection reference. Resolve the adapter version and account alias through existing endpoint and account policy. Missing credentials are not supplied by this design. Persist the resolved endpoint, including its account alias, before every launch.

An explicit slot pair takes precedence over shared developer endpoint defaults and label-based model overrides. The slot's label preference continues to choose suitable work; it does not replace the explicit endpoint pair. Slots without an explicit pair retain existing model-selection precedence. Reviewer selection uses reviewer configuration, never the developer's slot pair.

Configuration validation rejects duplicate or invalid slot identities, malformed endpoint pairs, duplicate resolved primary and alternate endpoints, unapproved providers or accounts, unsupported model settings, and endpoints lacking the role's required capabilities. A same-provider alternate with a different supported model is valid. Temporary capacity exhaustion is an availability condition, not an invalid configuration. If an explicitly configured endpoint fails validation, do not silently revert to the shared developer model.

After resolving model-dependent options such as effort, validate them for each endpoint. Do not forward options supported only by the primary to an alternate that cannot interpret them. All effective options and their configuration origins belong in the recorded selection.

## The authorized four-slot mapping

Adoption uses exactly these four developer slots:

| Slot | Primary | Alternate | Work preference |
| --- | --- | --- | --- |
| 1 | codex / gpt-6.1-sol | claude-code / opus | Preserve the existing reliability preference |
| 2 | claude-code / opus | codex / gpt-6.1-sol | Preserve existing preferences |
| 3 | claude-code / opus | codex / gpt-6.1-sol | Preserve existing preferences |
| 4 | claude-code / opus | codex / gpt-6.1-sol | Preserve existing preferences |

These are configured model identifiers, not assertions about provider-side availability. Use the operator's existing authorized account aliases for the respective providers. A missing or ineligible account is reported explicitly; no substitute account or credential is invented.

Implement the pair in the existing slot configuration and make actual dispatch consume it. The exact serialized field names must follow the repository's configuration conventions and be covered by validation and effective-configuration output. The table is the required behavior, not a claim that those fields are already shipped.

## Pinning and configuration changes

Before a run's first developer launch, record a non-secret routing snapshot: slot, primary and alternate specifications, resolved selection rules, configuration revision or digest, source locations, applicable capability and permission policy, and fallback bounds. Repair and continuation use this snapshot. An invocation additionally pins its actual provider, adapter version, model, account alias, launch options, and permission policy for its entire lifetime.

Valid configuration reloads activate atomically for new runs. They do not rewrite an existing run's pair or alter a running invocation. Invalid reloads retain the last valid configuration and emit a visible warning and audit record. Temporary health and capacity are checked again at every prospective launch. A subsequently revoked account, provider, or permission is not usable merely because it appears in a stored snapshot; record the refusal and preserve the run.

Changing an existing run's routing snapshot requires an explicit supported reconfiguration operation while no invocation can still execute. It records the old and new revisions and reason, preserves history, and does not reset retry or switch budgets for an unfinished logical operation. Ordinary reload and resume do not perform that operation implicitly.

## Logical operations, invocation attempts, and bounded fallback

A logical operation is one developer work request, one repair responding to recorded findings, or one required reviewer judgment. Its identity survives reissue, capacity waits, controller restarts, and provider changes. A genuine subsequent repair or a review of a new candidate is a new operation under the existing work and review budgets. Pressing resume, reconnecting a stream, or switching endpoints does not create a new operation.

Every provider launch has a distinct invocation attempt identifier beneath that operation. Native session identity is separate from both. Persist the operation's attempt count, endpoint-switch count, recovery deadline, spent budgets, and outstanding wait before spending or waiting. A restart continues these values.

For each operation, start with the recorded primary unless it is already known to be usage-limited. A classified usage limit on the primary permits one automatic switch to its alternate. If the primary is known to be limited before launch, the initial launch may use the alternate; record this as the operation's switch without inventing a primary invocation that never happened. The switch is automatic when all eligibility, budget, capacity, and execution-termination conditions hold.

An endpoint switch is committed only once for the operation. Once committed, the alternate remains selected for that operation through reissue and resume. Do not bounce back to the primary when the alternate also reaches a limit. A later genuine logical operation starts selection from the primary again, using current availability evidence and its own bounded switch allowance. Restarting an unfinished operation cannot manufacture that fresh allowance.

Same-provider model fallback uses the same mechanism and accounting as cross-provider fallback. Changing only a model still creates a new invocation attempt and records a transition. Session reuse is permitted only under the compatibility rules below.

Explicit provider usage-limit classification is the automatic fallback trigger in this revision. Existing transient connection recovery, overload pauses, and named provider-unavailable waits retain their own machinery. A failing check, repair verdict, authentication refusal, permission denial, sandbox refusal, unreadable stream, unknown error, or harness-issued stop is not converted into a quota switch. In particular, changing provider cannot bypass an authentication or permission decision. A future expansion of triggers requires an explicit design revision.

The existing provider recovery window and transient relaunch budget cover the logical operation across both endpoints; switching does not restart the clock or replenish the budget. Quota waiting remains governed by its own capacity policy. Retries after something outside the work stopped the run, and fallback, do not spend repair attempts or review rounds, but all actual provider usage remains attributable and subject to existing spend limits.

## Unavailable fallback and capacity waits

Check the alternate against current approved capabilities, role permissions, account eligibility, provider health, capacity, and spend policy before launch. Record why a candidate could not be used. A configured alternate is not a promise that it is currently available.

If no eligible alternate is available before a switch is committed, keep the primary selection and preserve the operation in the existing visible capacity-wait or capacity-blocked state. Rechecking the alternate later does not spend an invocation budget. If a switch has already been committed, wait for that selected alternate; do not reset the switch allowance or silently select another endpoint.

Use the provider's reset evidence and the configured polling interval. Do not estimate an unknown reset. A wait within the configured threshold follows existing polling; beyond it preserve the run with its claim, branch, worktree, phase, findings, budgets, routing snapshot, and capacity reason. A wake-up rechecks eligibility and capacity under the same operation identity. It is not an unlimited launch loop.

A capacity wait holds no promotion lease and no reservation for an invocation that is confirmed stopped. Release only the resources whose execution ownership has been reconciled. Preserve run ownership so another worker cannot mutate the same worktree. Status explains the selected endpoint, the unavailable alternative, the known reset or next check, and what must change before execution can continue.

## Durable transition and execution records

Extend the existing run persistence and launch records rather than introducing a parallel queue of runs. The representation may be a versioned journal with a derived snapshot or the existing atomic state mechanism, but the following records and ordering are mandatory.

The run routing record contains the slot, configuration snapshot and digest, endpoint pair, role, creation reason, and legacy-migration marker where applicable. An operation record contains its stable identity, phase and candidate or findings reference, counters and deadlines, current endpoint selection, switch allowance, and durable waiting or completion state.

Each invocation attempt records its operation and predecessor, full selected endpoint, launch configuration digest, normalized event schema version, session identifier when known, native-resume or reconstruction mode, input evidence references, and actual execution identity. Each completed or interrupted attempt records its refusal or stop classification, result references, usage, cost, and whether execution termination is confirmed or uncertain.

A transition record contains a stable transition identifier, source attempt when one exists, old and proposed endpoint, classification and evidence that triggered the switch, eligibility decision, applicable configuration revision, consumed switch allowance, context-reconstruction references, and destination attempt identifier. It records progress through planned, source execution reconciled, destination launch prepared, destination launched, and outcome recorded. A refusal before launch records its reason without claiming that a provider ran.

Use one serialized run writer or an equivalent compare-and-swap protocol with expected state generation. Every writer, including recovery, obeys it. A retry of the same transition or launch-preparation request returns its existing result; a request with the same identity and different contents is refused. Counters and the transition that consumes them become durable atomically. No second controller may independently choose or launch a destination.

Persist the intent and reserved destination attempt before launching. The launcher must use that attempt identity as an idempotency and recovery key. It must durably register the execution before allowing provider work to begin, or provide an equivalent mechanism that can discover an already-started execution after controller failure. Persisting a PID only after an unrestricted spawn leaves an unacceptable crash gap.

Execution identity must distinguish host or boot generation, process start identity, launcher generation, and the relevant supervised process or execution group. A PID alone, a free controller lease, or an absent terminal event does not prove that an old invocation stopped. The launcher must also account for tool processes capable of continuing work after their parent exits.

## Restart and uncertain outcomes

On restart, acquire run ownership, read the operation and transition records, and reconcile the recorded execution with the launcher before taking another action. If it is still running, observe or reattach to it. If its result is durable, adopt that result without launching again. If launch preparation exists but execution is conclusively absent, launch the already-reserved attempt through the idempotent launcher.

If the source may still be executing, leave the transition waiting for reconciliation. Do not start the alternate, release execution-owned capacity, or infer successful termination from an expired lease. If safe termination is needed, use identity-checked interruption and confirm that relevant execution has ended. Do not use a permanent user stop request as an internal temporary switch signal.

If the destination may have launched but its launch acknowledgment was lost, query by the reserved attempt identity. Adopt its running execution or durable result. A replacement launch is permitted only after the launcher establishes that it cannot overlap the earlier execution. Where the platform cannot establish that fact, preserve the run with a visible recovery reason; an uncertain state is not permission to duplicate work.

A late event from an older attempt remains attached to that attempt. It cannot overwrite the current selection, complete the wrong operation, or authorize a gate. Validate generation, operation, attempt, and candidate references when consuming events. Preserve valid late usage reports even when their execution result is no longer current.

Recovery also checks externally visible effects before asking a reconstructed developer to repeat them. A provider switch must not duplicate a commit, push, publication, or other action already performed. Existing compare-and-swap, pinned-candidate, and reconciliation rules still apply. This design promises one active execution for an operation, not magical exactly-once semantics for arbitrary provider actions.

## Native resume and durable reconstruction

Native resume is an optimization. It is allowed only when provider, account, session, model compatibility, and launch permissions are verified by the adapter. Being on the same provider does not establish compatibility. For a same-provider model change, default to reconstruction unless the adapter explicitly supports and validates that session transition. Crossing providers or accounts always starts a new native session.

Build reconstruction inputs from trusted durable state: work item and approved requirements, relevant design references, run and operation identity, phase, branch and worktree location, current base and candidate, preserved committed and uncommitted changes, verified check results and their candidate references, reviewer findings, remaining budgets, previous actions and uncertain outcomes, and the reason for continuation. Record the input references or digest used for the new attempt.

Do not transfer provider credentials, hidden reasoning, or another role's private session. An old native session identifier is retained for attribution but never presented to a different provider as resumable state. The new provider receives current instructions and role permissions through its own adapter.

Switching does not reset or discard work. Checks remain evidence only for the candidate and environment they actually checked. Changed code invalidates evidence under the existing verification rules; an endpoint switch alone does not require erasing valid evidence. Findings remain outstanding until resolved through the normal workflow. No switch can declare acceptance, weaken a check, or bypass independent review.

## Reviewer fallback and independence

Reviewer invocations use the same automatic quota fallback, durable operation identity, transition protocol, and recovery bounds. This includes initial review and a later review of repaired work. Reviewer policy selects its own primary and alternate; it does not inherit the developer slot's preference, writable session, or permissions.

For the requested adoption, a reviewer using codex / gpt-6.1-sol receives claude-code / opus as its alternate, and a reviewer using claude-code / opus receives codex / gpt-6.1-sol. Preserve its current primary, reviewer count, review requirements, and account policy. An unrelated reviewer primary must be named explicitly rather than guessed into this pair.

Every reviewer attempt remains read-only with external actions disabled, as required by the provider-adapter design. Reapply those restrictions on native resume and on reconstructed launches. The review is tied to the same exact candidate and review request. Partial prose from an interrupted reviewer is not a completed verdict. A valid durable completed verdict is adopted instead of requesting a duplicate review.

Record reviewer independence using role, endpoint, and invocation identity. The developer's native session is never resumed as a reviewer. Sharing a provider or approved account does not by itself violate independence; sharing the developer invocation or write authority does. Fallback cannot reduce the required number of reviews or turn an unavailable reviewer into an implicit approval.

## Legacy runs and compatibility

Older records remain readable without fabricated slot, endpoint, usage, or session history. Missing historical information is explicitly unknown. Completed legacy runs remain historical records and are not rewritten to appear to have used the new routing system.

A currently executing legacy invocation keeps its original endpoint and permissions. Before any continuation or switch, reconcile that execution and preserve its branch, worktree, phase, findings, and budgets. If an existing durable slot assignment is trustworthy, retain it. Otherwise, while the run is quiescent, allocate an available slot atomically and record that assignment as a migration made now. Never infer a historical slot from a label, account, or process order.

At that safe boundary, record the current validated routing snapshot and establish the unfinished logical operation's identity. Preserve known counters and deadlines. If history is insufficient to establish an unspent switch or retry allowance, do not grant a fresh allowance for that unfinished operation: retain its known endpoint and use existing safe continuation or visible reconciliation. Subsequent genuine operations can use the new bounded policy. This conservative case must not destroy preserved work or require restarting the work item from scratch.

An active legacy run that cannot yet acquire a slot waits visibly. Migration cannot evict a current worker or create a fifth developer execution. No live migration proceeds while an old invocation could still be running.

Introduce versioned record changes with compatibility checks. An older binary that cannot understand a live transition must refuse to resume it rather than treating missing knowledge as an idle run. Rollback of configuration cannot make new execution records disappear; software rollback requires a compatible reader or a quiescent reconciliation plan.

## Attribution, accounts, and configuration boundaries

For every actual attempt, retain the configured choice and the endpoint that actually served it. Attribute provider-reported tokens, usage, and cost to the actual provider, model when reported, and account alias. Distinguish requested model from reported model when they differ. Record unknown usage as unknown, never zero or an estimate presented as a provider report.

Deduplicate repeated usage events using attempt identity and the adapter's event identity or documented cumulative-report semantics. Preserve costs from failed, interrupted, and abandoned attempts. Aggregate the operation across all attempts without charging a replayed terminal event twice. Status exposes slot, phase, actual endpoint, switch reason, and waiting or recovery condition in ordinary language.

Project policy remains portable and versioned: roles, allowed providers, profiles, routing, and bounds. Machine-local capacity contains account aliases, authentication references, health, and approved pools. Credentials remain provider-native and local. No private account identifier or secret enters reconstruction inputs, read models, pages, messages, or audit output.

Existing approved account pooling remains additive: endpoint selection may resolve through an approved pool, and account affinity is maintained as its policy requires. This work does not add accounts, create new pools, or change pool membership. Existing alias attribution remains useful, but the earlier no-schema-change claim for account pooling does not prohibit the versioned operation and transition records required here.

Conversation account failover remains turn-boundary behavior under its own enabled policy. A turn that changes accounts reconstructs from its durable conversation record, records the serving alias and reason, and retains named-account affinity with return after the named account's capacity window reopens. Cross-provider conversation behavior is governed by the provider-general design and configured policy. The four-slot adoption changes none of the operator's completed manager configuration.

## Narrow adoption after implementation

Use the existing governed configuration-file application path. A generic role-applied configuration service, another account-pooling project, or replacement provider adapters are not prerequisites. The implementation must first support validation, selection, persistence, recovery, and actual invocation consumption of the new routing fields.

The development manager coordinates a bounded application step for the existing effective configuration source. That step records the baseline revision or digest, previews the exact change, validates it with the deployed compatible binary, and applies it atomically only if the baseline still matches. Its authorized scope is the four developer pairs in the table, preservation of slot 1's reliability preference, and the reviewer alternate rules above. Preserve the four-slot count, other preferences, completed manager configuration, credentials, unrelated role settings, and all existing gates.

If the effective source is protected from an ordinary developer run, the development manager routes this exact patch and verification through the supported protected-path application mechanism. A missing writer capability is an execution obstacle with an owner and release condition, not a request for Mason to decide the routing again. If only a person can write that source, provide the concrete patch and the exact action required. Keep live adoption outstanding until evidence returns.

After application, record validation success, the effective revision, the source and precedence of every pair, and the running binary that consumes it. Observe actual developer invocation records for each of the four slots and reviewer invocation records under the new policy. Verify the actual endpoint against the recorded selected configuration; printed configuration alone is insufficient.

Exercise both fallback directions and same-provider model switching through bounded test fixtures at the real dispatch and launch boundaries. Live checks use existing allowed accounts and spend policy; they must not deliberately exhaust a plan. If live fallback has not occurred, distinguish fixture-proven transition behavior from observed primary routing and leave that limitation visible. Do not claim a live provider switch from a simulated refusal.

Existing live invocations remain pinned. Rollback restores the prior configuration for future selections, preserves all transition history, and drains or reconciles affected execution before any necessary per-run change. Record rollback evidence with the same baseline protection. Code landing, configuration application, and observed adoption are separate facts.

## Acceptance criteria

1. Validation accepts the exact four-slot table and same-provider model alternates, rejects malformed or ineligible pairs, and exposes effective origins. Slot 1 still prefers reliability work. Explicit pairs win over conflicting label-model rules; legacy slots retain prior precedence.

2. Actual initial dispatch, repair, and resume consume the recorded slot pair. Slots 1 through 4 launch their specified primaries when available. Restarts and capacity release do not renumber a run or allow overlapping workers in its worktree.

3. A classified primary quota refusal causes the eligible alternate to serve a new attempt automatically in both provider directions. A known pre-launch quota block skips the primary without inventing a primary attempt. Same-provider model fallback follows the same bounded transition path.

4. Missing credentials, ineligible capabilities, exhausted alternate capacity, spend refusal, and revoked permissions produce accurate preserved waits or refusals. Authentication, sandbox, check, and review answers are not bypassed through fallback. No unknown refusal is silently treated as quota exhaustion.

5. After one committed switch, repeated quota failures, resume requests, and controller restarts cannot create another switch or replenish budgets. Existing transient recovery deadlines span the operation. Genuine later work or review operations receive their own identities under the existing workflow budgets.

6. Inject crashes before and after transition persistence, source termination, destination preparation, actual launch, launch acknowledgment, terminal-result persistence, and usage persistence. Recovery adopts the correct running execution or result, or waits when identity is uncertain. It never launches overlapping source and destination work, loses a consumed budget, or counts usage twice.

7. Race two controllers against one transition and one slot claim. Exactly one authoritative transition and launch preparation stands. A retry with the same identity is idempotent; conflicting contents are refused. PID reuse, stale leases, late events, and surviving tool processes cannot masquerade as confirmed termination.

8. Continue a run containing committed and uncommitted changes, valid checks, unresolved reviewer findings, and a recorded ambiguous external action. Reconstruction preserves each, reconciles the external action, and invalidates only evidence made stale under existing rules. No incompatible native session is resumed.

9. Change configuration during active execution. The invocation and existing run snapshot remain pinned; new runs use the new valid revision. Invalid reload changes nothing. Revocation prevents a new launch without rewriting history. Routing rollback preserves new-format records and older incompatible binaries refuse unsafe resume.

10. Resume legacy records with a known slot, no slot, missing usage, and uncertain prior launch. Migration records only newly established facts, never creates a fifth worker, never resets unknown budgets into a new allowance, and preserves work while awaiting reconciliation.

11. Reviewer fallback produces an independent read-only invocation against the exact candidate, with separate session lineage and unchanged review requirements. Interrupted partial output is not a verdict, while an already durable valid verdict is adopted. Provider failure consumes no repair attempt or review round.

12. Attribute actual usage across primary and alternate attempts, repeated cumulative reports, late events, and unknown totals. Configured and actual endpoint evidence remain distinguishable, and no credential or private account identifier appears in status or reconstruction.

13. Adoption evidence includes the exact bounded configuration change, baseline protection, compatible binary, validated effective origins, actual primary invocations for all four slots, reviewer routing, and transition fixture results. State separately whether live fallback has been observed. Preserve all manager settings and unrelated configuration. A protected-source application remains an explicit outstanding adoption condition until completed.

## Delivery boundary

This revision contains the architectural guidance needed for bounded implementation. It does not require another architectural decision about slot identity, fallback scope, reviewer inclusion, recovery storage semantics, legacy treatment, or the four-slot mapping. The development manager owns implementation decomposition and the narrow application step; existing product sequencing remains with the Lead Product Manager.

Other documents that still state a universal Claude-only or no-fallback rule require reconciliation by their owners through the existing provider-policy work. Such wording is not permission to weaken this design's execution protections or to broaden the authorized configuration change. Record the conflicting location and owning role rather than silently editing product intent.
