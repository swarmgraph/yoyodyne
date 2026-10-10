---
id: portable-agent-configuration
kind: design
title: 'Portable agent configuration: materialization, the baseline, and bundle boundaries'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-01T19:35:00Z
      reason: drafted by the developer under yoyodyne-ifd.36, carried whole to the architect, and ratified with amendments recorded in conversation - the authority paragraph rebound to authority-by-capability and configuration-never-grants-authority with role definitions excluded from materialization, draft scaffolding replaced by the ratification decisions, and conversion output placed under the working-tree publication rule. The four questions the draft asked the architect are answered in its Decided-at-ratification section
    - action: amended
      by: architect
      at: 2026-09-07T00:30:00Z
      reason: 'yoyodyne-ifd.294 - the config.lock baseline ratified as a contract: bundle name, bundle revision digest, per-value digests and never values, a version field, generated and committed, nothing in the load path reading it, held by a conformance test; recorded in the baseline section, the 2026-09-02 ratification entries confirmed present'
    - action: amended
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: approved amendment eb9b385e from yoyodyne-ifd.418 - the same retirement; 'already applies' in place of 'in force'
    - action: amended
      by: architect
      at: 2026-10-06T23:59:49.44694Z
      reason: 'Applying recommended configuration through a role (yoyodyne-ifd.434.8): consolidate field-specific ownership, trusted protected-path delivery, baseline preservation and unknown-history handling, non-retroactive activation, and drift reporting without a routine operator decision; retain materialization, inheritance, and third-party bundle boundaries.'
approvals:
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-10-06T23:59:49.44694Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 343, for document-343.1 under the automatic approval policy
---
# Portable agent configuration

This design serves the V1 goal to keep roles, policies, and provider selection configurable without making safety invariants optional. It also serves the standing autonomy and plain-language goals: an authorized role decides routine operational settings, the harness delivers the reviewed change, and the operator can see who changed what and why.

Applying recommended configuration through a role (yoyodyne-ifd.434.8) extends the existing materialization and baseline design with a bounded configuration action. The action does not give agents direct write access to protected paths. Trusted code checks the role's authority, prepares the exact change, and carries it through independent review and normal integration.

## Resolution and ownership

Up to three layers produce the effective configuration, later layers winning: harness defaults, a bundle named by `extends`, and the project file. Existing merge rules remain authoritative. In particular, `checks` is replaced as a whole list, a persona override replaces the whole persona, and an agent is removed with `disabled: true` rather than by omission. The project recurring-task map replaces the inherited map as a whole.

A project owns every value it states and inherits every value it omits. No `owns` list, per-key ownership marker, or second inheritance mechanism is added. This describes where a configuration value comes from; it does not establish which role may change it.

`yoyo config show --origins` identifies each value's supplying layer. The effective configuration revision is derived from its values. Runs record the revision that configured them.

Some values have additional inheritance restrictions:

- Bundles cannot supply `version` or `product`. Schema version and product identity belong to the project.
- The built-in bundle supplies no project checks. A project's toolchain is not something the bundle can infer.
- Approval settings retain their existing opt-in rules. Conversion and adoption cannot turn inheritance into an approval or move an explicit opt-in silently.

The baseline records where materialized values came from. It is separate from the configuration and never participates in resolution.

## Provenance and drift reporting

Provenance is derived from configuration sources and recorded history, not from YAML comments. A comment can be stale or removed; it is not authority or evidence that a value was adopted.

One shared read-model derivation supplies drift to every surface. The CLI, dashboard, and conversation must not calculate different answers independently.

`yoyo config validate` and `yoyo doctor` report drift against a known baseline without changing their existing exit-code rules. No drift produces no drift message. `yoyo config show` identifies the materialization source, the baseline's known or unknown status, and the effective configuration revision.

For a role-applied change, the read model also identifies the deciding role, its recorded reason, the configuration change record, and the published revision. A role decision is never displayed as operator approval. A prepared change is not displayed as active configuration; publication and activation are separate facts.

An explicit project value chosen by a role remains a project-owned value. It is not called inherited or unchanged merely because the choice came from a harness recommendation. Doctor reports invalid configuration and unknown provenance honestly; the fact that an authorized role chose a value is not itself a warning.

Conversational reporting follows the existing communication rule. An available bundle improvement alone does not interrupt the operator on every pass. A role reports it when it affects work or a decision, and may decide an authorized operational change without a routine operator gate. Conflicting values go to the role that owns the setting. Matters outside that role's authority follow their existing authority path. This resolves the former open question about conversational drift reporting; implementation does not wait for an operator answer.

## Moving between explicit and inherited configuration

`yoyo config materialize` resolves an inheriting configuration and writes a standalone configuration without `extends`, copies the relevant personas into `.yoyodyne/personas/`, and records the baseline. It preserves all effective project values.

`yoyo config extract` compares an explicit configuration with a named bundle, removes values identical to what that bundle supplies, writes `extends`, and retains deviations and values that cannot be inherited. A differing persona remains a complete file and override.

For either conversion, effective configuration before and after must be identical, including its configuration revision. Conversion refuses a change it cannot make while preserving that property. Tests must cover both directions and the round trip.

These conversion commands retain their existing explicit overwrite controls and use the shared confined-write mechanism. They validate and prepare their output before replacing destinations. An I/O error can still leave an uncertain publication outcome; the command must report that uncertainty and reconcile the destinations before retrying rather than promise that every failure occurred before a write.

The existing direct CLI conversion path produces working-tree changes under its existing publication rules. It is distinct from the role-applied operational action below. The latter must reach committed configuration through reviewed delivery and cannot finish by asking the operator to paste or commit a recommended value. The operational action cannot invoke conversion as a way to obtain broader write authority.

## The baseline

`materialize` and `init` write `.yoyodyne/config.lock`. Its contract remains: bundle name, bundle revision digest, per-value digests rather than values, and an explicit format version. It is generated and committed with the configuration. No configuration loader opens it; a conformance test holds that boundary.

The baseline is evidence for comparison, never an instruction to execute a value. Losing it cannot prevent an otherwise valid project from running.

For values with known comparable baselines, drift distinguishes:

| Answer | Meaning | Consequence |
| --- | --- | --- |
| unchanged | Neither project nor bundle changed the value. | No action. |
| yours | The project changed it and the bundle did not. | Preserve the project choice. |
| available | The bundle changed it and the project still matches the baseline. | Offer adoption to the setting's owner. |
| conflicting | Project and bundle changed it to different values. | The setting's owner decides which explicit value to use. |

When the project and current bundle already agree, no adoption is needed, even if both differ from the baseline. The derivation states that agreement rather than presenting a conflict requiring a decision.

A missing, unreadable, unsupported, or insufficient baseline yields unknown for the affected comparison. Unknown is not available. Equality with today's bundle does not prove that a historical project value was never edited.

`yoyo config adopt <key>` adopts one available improvement. It checks the expected configuration, baseline entry, and selected bundle revision, then changes only the chosen value and its corresponding baseline digest. It never adopts every available change implicitly. A conflicting value requires an explicit value decision through the applicable authority path rather than being relabeled available.

Partial adoption does not advance unrelated baseline entries or claim that the whole configuration came from the newest bundle. The baseline's materialization revision remains the original source reference; the durable adoption record identifies the selected value's later source revision and resulting digest. Surfaces must distinguish partial adoption from complete materialization.

A role choosing an explicit operational value is different from adopting a bundle improvement. It may proceed with an unknown baseline if its request and reviewed change are otherwise valid. Missing baseline evidence remains missing; an unreadable or unsupported lock is preserved rather than overwritten. The change record states that drift remains unknown. The action neither guesses history nor records a new whole-project baseline to make the warning disappear.

An explicit choice that is not bundle adoption leaves an existing baseline entry unchanged. This preserves the distinction between project choices and later bundle improvements.

Baseline-format migration must preserve the comparisons the old format supported. It must not replace historical digests with digests of current project values. If a digest representation cannot be translated from available source evidence, retain the original evidence and mark the affected comparison unknown. A migration cannot turn unknown or conflicting values into available improvements.

## Authority for operational changes

The operational action is registered in trusted Go code and exposed through the existing action and tool registry. Its field permissions are enforced in code. Recognizing a field in the configuration schema establishes neither permission to change it nor validity of a proposed value. Newly introduced fields remain unavailable until their ownership and permitted changes are explicitly registered.

The action accepts an authenticated role invocation, a stable request identity, the product and target branch, the expected base revision and source digests, exact field changes, expected old values, proposed values, and a recorded reason. It also names authorized derived effects and any bundle source used for adoption. A caller-supplied role name cannot establish authority.

A repeated request with identical contents returns the existing operation and outcome. Reusing its identity with different contents is refused. The durable record distinguishes decision, preparation, review, publication, activation, refusal, and uncertain outcome.

The action cannot change agent identity or definitions, role identity or definitions, personas, remits, prompts, capability policy, account selection, account pools, approval policy, workflow bindings, product identity, or machine-local policy. Model identifiers are permitted only in the specific fields below and do not authorize changing provider or account bindings.

The protected-path gate accepts only the exact configuration and baseline changes authorized by the trusted operation. It does not grant a developer general access to `.yoyodyne`. Grants naming `.yoyodyne/roles/` remain forbidden. The existing configuration-never-grants-authority invariant needs no amendment: this action's authority comes from trusted code, not from the values being edited.

### Checks and verification

The development manager decides `checks`, `landing_checks`, and the execution-time settings listed below, within the approved verification design.

The publication run uses the verification requirements that authorized that run. Proposed configuration cannot remove or weaken its own verification gate. Testing the proposed check commands is additional evidence, not a replacement for existing required checks or independent review.

A reduction in verification coverage must cite the architectural ruling and revision-bound evidence that justify it. Moving a check after landing does not establish equivalent verification before integration. This configuration design alone does not authorize narrowing race coverage or treating Linux execution and Darwin cross-compilation as equivalent to required macOS execution.

### Developer models

The development manager may replace `execution.developer_models` as one ordered list. The first matching label wins, so order is part of the requested value. The action never sorts, merges, or deduplicates the list on the caller's behalf. It uses existing label and model validation, refuses duplicate labels, and preserves the configured fallback when no rule matches.

This permission changes neither reviewer configuration nor provider, account, role, or tool authority. Explicit slot endpoint choices retain the precedence specified by the execution-routing design; changing this list does not override them.

### Developer capacity and work preferences

The Lead Product Manager decides `execution.developer_slots` preferences because they affect which ready work is pulled first. The development manager decides `execution.max_concurrent_developers` within the capacity supported by existing agent configuration. This action cannot edit an agent or its instance count.

A request requiring both decisions carries each owner's recorded decision. Neither role decides on the other's behalf.

Slot positions matter. Preserve empty entries and unedited positions, validate labels with the existing domain validator, and refuse a preference list longer than the resulting capacity. Preserve the standing requirement for a reliability-preferring seat. These permissions cover work preferences and capacity, not slot endpoint routing.

Reducing capacity does not terminate, renumber, or reassign an existing run. A change that removes an occupied slot waits until the slot can be removed under the execution-routing design. New work uses only capacity permitted by the activated configuration.

### Existing recurring tasks

The owner of an existing recurring task may change only `every`, `model`, and `max_turns`. Ownership is checked against the existing task and authenticated invocation before applying the change. The action cannot create, delete, rename, enable, disable, or reassign a task, or change its prompt or workflow.

Intervals are at least five minutes. Zero `max_turns` selects the default of three; positive values cannot exceed ten; negative values are refused. An empty model selects the role's configured model. The recorded explanation states these effective meanings rather than calling zero unlimited or treating an empty model as absent execution.

Because the project task map replaces the inherited map, an update compares the complete expected task map and preserves all other tasks. Materializing an inherited map must not silently remove, add, or change another task. The prepared diff and effective comparison expose any such change, and the action refuses it.

### Operational thresholds

The development manager may change only the following threshold fields. Every request states the effective meaning of its proposed values. Existing field validation and relationships still apply.

| Fields | Additional bounds for this action |
| --- | --- |
| `execution.check_timeout`, `execution.check_stage_timeout`, `execution.landing_check_timeout`, `execution.work_poll`, `execution.redeploy_drain_limit` | Positive finite durations. |
| `execution.repair_attempts_before_replan`, `execution.integration_retries_before_reconciliation`, `execution.transient_relaunches_before_blocking` | Nonnegative integers. Zero allows none of the retries counted; it never means unlimited. |
| `execution.usage_limit_max_pause`, `execution.usage_limit_in_process_pause`, `execution.usage_limit_unknown_reset_pause`, `execution.server_overload_pause` | Positive finite durations, preserving existing maximum-wait and polling relationships. |
| `execution.factory_stall_after` | Positive finite duration; cannot disable stall detection. |
| `execution.missing_reports_before_fresh_conversation` | Nonnegative integer; zero selects the default of three. |
| `execution.blocked_runs_before_intake_hold`, `execution.brake_escalation_cycles` | Positive integers; cannot disable the intake brake or bounded escalation. |
| `execution.brake_cooldown` | Nonnegative finite duration; zero removes only the additional cooldown wait. |
| `triage.stuck_merge_age` | Positive finite duration. |
| `triage.review_rounds_cap` | Nonnegative integer under its existing validation and meaning. |
| `triage.repair_grant_attempts` | An explicitly configured value is a positive integer. The derived-value rule below also applies. |

Changing a duration does not change what it bounds or authorize an otherwise forbidden interruption. These permissions do not alter failure classification or make a dropped connection or sleeping machine a failure of the work. Check interruption and provider recovery keep their governing recovery rules.

A threshold edit does not release a hold, reset a counter, revive stopped work, grant another repair, or extend an existing operation's budget. Those acts use existing recovery actions and their separate authority checks.

## Effective changes and concurrency

Before publication, resolve and validate the complete proposed configuration, then compare every effective change with the authorized request. Permission to edit one source field does not automatically authorize every consequence of the edit. Changes through inheritance, aliases, YAML references, or defaults are included in the comparison.

Changing `execution.repair_attempts_before_replan` may change an unstated `triage.repair_grant_attempts`. The development manager may authorize both effects explicitly. The action records both effective changes and leaves the grant unstated, preserving its derivation. An explicitly configured grant remains unchanged unless separately named in the request. Other effects outside the authorized request are refused.

Ordered-list replacement compares the entire expected prior list, including order. A task edit compares the entire expected task map. The action also compares the configuration and baseline source digests with the recorded expected pair. A concurrent change requires renewed comparison and a fresh owner decision where the requested effects change; it is never resolved by an automatic merge that changes the decision's meaning.

Configuration parsing, semantic validation, authority checking, and effective-change comparison are separate checks. A duration that parses, or a key that exists, has not thereby passed the others.

## Reviewed publication and safe writes

Prepare changes in an isolated harness-managed worktree. Record the expected configuration and baseline contents, intended replacement digests, deciding invocation, applicable authority, and publication operation before writing. Never prepare routine role changes by modifying the operator's primary working tree.

Use the shared physically confined writer with held directory handles and destination checks. Write complete temporary files, synchronize their contents, and publish them through the supported safe replacement or creation operation. Creation must refuse an occupied name. Synchronize the containing directory where required for durable publication. A check followed by an unrestricted path-based replacement is not sufficient protection against concurrent or redirected writes.

Safe replacement of one file is not an atomic transaction across two files. A preparation interruption may leave only part of the intended pair. Reconciliation reads both files and compares them with the recorded expected and intended contents before deciding whether to complete preparation, adopt a completed preparation, or stop for conflicting changes. It never overwrites unrelated changes merely to make the pair agree.

The reviewed candidate contains the exact authorized configuration change and any corresponding baseline changes together. The protected-path authorization is bound to that candidate and operation. A developer cannot enlarge the grant or substitute another configuration value. Independent review checks field ownership, effective changes, baseline treatment, verification requirements, and confinement evidence.

Normal integration checks the current target against the expected source state. A changed target requires reconciliation and renewed evidence under the existing delivery rules. The merge makes the reviewed pair part of one committed revision. No active service loads a partly prepared pair from the isolated worktree.

An uncertain write or publication result is reconciled under the same operation identity before retrying. Read back the candidate, commit, and publication evidence; adopt an already completed outcome where established. Do not duplicate a commit or publication, and do not infer that an error means nothing happened.

Record a refusal or interruption with preserved work and its specific cause. Failure to publish is not a reason to hand the operator a paste or request that they commit the recommendation manually.

## Activation and existing work

Publication records the committed configuration revision. Activation uses the existing explicit reload or restart boundary and records which service or invocation consumed it. A committed change is not claimed as active until that evidence exists.

New invocations and newly created bounded operations use the activated configuration. Existing attempts, waits, grants, routing snapshots, and cumulative work-item budgets retain their recorded limits and expenditure. Configuration changes never migrate authority into an active invocation.

Where an older operation lacks its original limit, recovery must reconcile that absence; the latest configuration is not evidence of the earlier limit. Invalid reloads retain the last valid configuration and explain the refusal. Reverting configuration is another reviewed change and cannot erase expenditure or history.

## Third-party bundles

A third-party bundle may be a materialization source, never a load-time layer. At the operator's explicit instruction, `materialize --from <bundle>` reads its content once and writes project-owned values for inspection. Upgrading a plugin cannot change a running project's configuration through an external resolution layer.

Bundle discovery, fetching, verification, and pinning belong to the plugin design. This design supplies no registry or network-fetch mechanism.

Bundles are ordinary configuration. They never write protected role definitions under `.yoyodyne/roles/`, activate authority, or widen a role through persona guidance. Existing bundle validation, including refusal of product identity and another `extends`, applies to third-party content too. Unpacked content and all resulting writes remain confined to their declared roots.

The operational action cannot use a third-party bundle to evade its field permissions. Even a valid bundle is evidence for a proposed value, not authority to apply it.

## Decisions and rejected alternatives

Materialization continues to express project ownership. A second ownership schema was rejected because the configuration already distinguishes stated and inherited values.

The baseline remains committed, digest-only evidence outside the load path. Machine-local baselines were rejected because collaborators need the same comparison history. Reconstructing history from current value equality was rejected because it cannot distinguish an unchanged value from a deliberate edit back to that value.

Operational authority is field-specific and enforced in trusted code. Blanket permission over numeric settings or recognized schema keys was rejected because some values disable safeguards or change authority. Whole-section writes and automatic list merging were rejected because they can change decisions the caller does not own.

Routine recommendations use reviewed delivery. Direct agent writes to protected paths and operator pastes were rejected because the former bypass confinement and authority, while the latter make the operator drive routine work.

Publication retains its prior verification requirements. Allowing a configuration change to weaken the checks that authorize itself was rejected because it would make configuration its own evidence.

These choices apply existing repository invariants. They do not create a new repository-wide invariant or redefine product intent. The companion authority and recurring-task provisions belong in Configurable workflows, alongside its protected-path rules and authority table.

## Acceptance

A reviewer must be able to establish all of the following:

- Materialization and extraction preserve effective configuration and its revision, including personas, approval opt-ins, and whole-list or whole-map semantics.
- The loader never opens the baseline. Missing or unsupported baseline evidence does not stop valid project execution and is never classified as an available improvement.
- A permitted role can deliver a specific operational change through independent review and normal integration without an operator paste or routine approval decision.
- A recognized but unauthorized field, an authority-bearing field, a role-directory grant, an unauthorized caller, and a mixed request containing an unauthorized effect are refused.
- Model-list order, empty slot positions, existing task identities, and unrelated tasks survive permitted edits. Concurrent changes are detected.
- Zero task turns means three; zero retry count allows none; zero missing-report threshold means three; zero brake cooldown preserves holds and probe limits. Attempts to disable the intake brake or stall detection through this action are refused.
- A derived repair-grant change requires explicit authorization of its effect and preserves its unstated origin. An explicit grant is not changed accidentally.
- Inheritance or YAML references cannot cause an unapproved effective change.
- Partial adoption changes only its authorized value and baseline entry, retains unrelated history, and records the adopted source revision. An explicit non-adoption choice with an unknown baseline leaves that uncertainty visible.
- Baseline migration neither fabricates old values nor converts unknown history into safe adoption.
- Crashes before, between, and after preparation writes, occupied creation names, conflicting replacements, and uncertain publication are reconciled without lost unrelated changes or duplicate publication.
- The configuration change cannot weaken its own checks. Reduced coverage needs its governing ruling and candidate-bound evidence; postlanding checks do not substitute for required preintegration verification.
- Active runs retain their limits, spent counters, slot identities, and authority. A capacity reduction cannot remove an occupied slot by silently renumbering its run.
- Status distinguishes decided, prepared, reviewed, published, and activated changes and names the deciding role without manufacturing operator approval.

## Scope left elsewhere

This design does not change what `init` chooses to materialize, define fleet-wide mutable state, design a bundle registry, grant configuration access to new roles, or choose optimal performance thresholds. The development manager owns decomposition, and the Lead Product Manager owns work ordering. Bounded race-check adoption retains its separate verification ruling and implementation prerequisites.