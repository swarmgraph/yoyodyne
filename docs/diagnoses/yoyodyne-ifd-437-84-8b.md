# yoyodyne-ifd.437.84: second reading of the design documents against the code, group 8: the machine and configuration

A second, adversarial reading of the report `yoyodyne-ifd-437-84-8.md` beside this file. It re-read every finding there that differs or is not built against the code, tried to settle the unclear ones, checked a sample of the matches, and looked for what the first reading missed. **Where the two files disagree, this one stands.**

- **Read on:** 2026-11-21, in the evening, between 18:33 and 18:43 PST, with main at `848d86190`. The code had moved since the first reading; each finding is judged against the code as it was at this reading.
- **Read by:** a read-only reader on Opus 5.5 in the operator's assistant's session. It read code and ran nothing, tests included.
- **Checked by hand:** nothing in this file was checked again by hand.
- **How to use it:** evidence for the architect's review of the designs (yoyodyne-ifd.437.84), not a ruling. Read the code at the line a finding names before a revision rests on it.
- **Second-reading classes:** `CONFIRMED`, the first finding is right as written; `CORRECTED`, its gist is right and a detail is wrong; `WRONG`, the design and the code agree or the finding misread one of them; a class in the first reading's own words where an unclear finding was settled.

Read at main `848d86190` (2026-11-21 18:14 PST), read-only. `portable-agent-configuration.md` was re-read as it stands after the revision that landed today (commit `73d3e72d7`, revision stamped 2026-11-21T23:50Z). That revision moved most of its lines, so every line number the first reading gives for that document is stale. The other five documents have not changed since the first reading.

## Summary
- Findings re-read: 19. CONFIRMED 11, CORRECTED 7, WRONG 1, CANNOT TELL 0.
- Unclear findings settled: 9 of 9. G8-17 settled in two halves.
- Verdicts changed: pinned-outside-files.md should be "partly stale", not "current". The first reader's own G8-20 is a DIFFERS.
- **The ruling made today (section 6, the typed write a role uses to apply a configuration value):** no such write exists in the code. What does exist is one piece: the list of keys a role may apply, held in Go, with the refusal for every other key (`internal/config/roleapply.go:17-70`, `RoleMayApply`). Nothing calls it except tests (`intake_test.go:273`, `mcp_test.go:216`, `install_test.go:250`, `timelimits_test.go:169`, `maincheck_test.go:66`, `reviewthreads_test.go:135`).
  - The following do not exist: a capability for it (`internal/capability/capability.go` has no config capability), a tool or chat action, the reviewed run that may change only `config.yaml` and `config.lock`, the comparison against the value the role saw, and "applied by" in drift or doctor.
  - The code records this gap explicitly. `internal/ownership/hand.go:42-45,75-79` lists `HandOperationalConfiguration` as "applying operational configuration a role recommends through a reviewed run", which a person types by hand meanwhile. It names `yoyodyne-ifd.434.8` as what closes the gap, but that item is the design item, and it is closed (`.beads/issues.jsonl`). No build item for section 6 is in the tracker.
  - The list of keys in Go also differs from the design's table (see G8-16).
- Findings the architect should not act on as written:
  - G8-8: not a mismatch. The reader says so itself, and nothing needs changing.
  - G8-10 (a MATCHES): wrong. `checkBundleDocument` does not refuse `version`. It requires one: a bundle without a version, or with the wrong one, is refused (`internal/config/builtin.go:152-157`). The design repeats the error at line 131.
  - The machine-home MATCHES claim "precedence env, XDG, machine file" has the order wrong. The code goes `YOYODYNE_STATE_HOME`, then the machine file, then `XDG_STATE_HOME`, then the default (`internal/home/home.go:244-263`).
  - G8-4: the binding is already in the design's step 2. Only the move of the machine file is missing.
  - G8-6: this is a DIFFERS (built code, stale sentence), not a NOT BUILT.
  - G8-16: "no capability for applying configuration exists" is true but incomplete. The refusal list is built.
- The most important things the first reader missed:
  - configurable-workflows.md:127 gives the development manager `run.cause`, `run.repair-continue`, `publication.merge-repeat` and `budget.cross-cap`. No capability by any of those names exists (`internal/capability/capability.go:41-332`). She holds `work.triage`, `run.recover` and others (`internal/rolecapability/bundles.go:137-173`).
  - Seven keys in section 6's table (`model_kinds`, `reviewer_models`, `developer_cheaper_first`, `high_risk_labels`, `skip_when_unchanged`, `full_pass_every`, `repository_ask`) exist nowhere in `internal/config`, and are not in `roleApplicableKeys`.
  - v1-harness-design.md:469 puts `machine.yaml` "under the configurations home". machine-home ends that home, and the code reads `~/.yoyodyne/machine.yaml` (`internal/home/home.go:55-58`).
  - pinned-outside-files.md does not state the limit of three runs that stop without judging the file (`internal/runstate/outsidefile.go:38`, enforced at `internal/orchestrator/outsidefilelanding.go:383`). That limit is half of the "six runs".
  - configurable-workflows settled question 6 (line 209): `init` writes no workflow files.

## machine-home.md
Verdict: agree (partly stale).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G8-1 | NOT BUILT | CONFIRMED | No Go file names `moving.json` or `home-move.json`. `internal/home/migrate.go:99-105`: "this build moves nothing on its own". |
| G8-2 | DIFFERS | CONFIRMED | `internal/home/migrate.go:103-104` names `yoyo home migrate` and says nothing about a next attempt. |
| G8-3 | DIFFERS | CORRECTED | Gist right. The command is `migrateHome` at `internal/cli/home.go:40`, not `:239` (that line is usage text). Its in-flight refusal reads both homes through `runstate.HomeInFlight` (`internal/runstate/homemigrate.go:71-80`, which also checks for a watching session). The `home-move.json` and "move under way" clauses cannot be met. |
| G8-4 | DIFFERS | CORRECTED | The design's step 2 (machine-home.md:469-470) already writes the binding, so only the machine file's move (`migrate.go:65-72,136`) is missing. No per-step "done" record exists: the only marker is `MovedMarkerName`, at `migrate.go:79`. |
| G8-5 | DIFFERS | CONFIRMED | `internal/cli/project.go:578`: "still holds %d worktree(s) under %s". The design (279-280) lists only runs in flight and a name already taken. |
| G8-6 | NOT BUILT | CORRECTED | This is a DIFFERS, not a NOT BUILT. `internal/cli/init.go:38-39` (`--intent`, `--intent-from`), `goals.go:194` and `artifact_check.go:89` are built, so line 374-375 is stale. |
| G8-7 | NOT BUILT | CONFIRMED | No Go file names `group.yaml`, `group init` or a groups directory. `home.go:267-289` lists no `groups`. |
| G8-8 | DIFFERS | WRONG | The reader's own text says it matches (`home.go` `Default` keeps the earlier home). There is nothing to change. Drop it. |

Unclear settled:
- G8-9: MATCHES as far as checked. The design (machine-home.md:731-735) says the script's list of what may sit at the top of the home leaves out `machine-identity.json`, and it still does (`scripts/validate-machine-home.sh:206`). The script prints its "not taken" parts at lines 531 and 622-639, as the design says. I did not trace every one of the seven checks.

MATCHES that are not:
- The precedence order. The code is env, then machine file, then XDG, then default (`internal/home/home.go:244-263`, which agrees with `v1-harness-design.md:469`), not "env, XDG, machine file".
- Under "Missing", the reader cites `machine.yaml` at `home.go:42-46`. The constant is at `home.go:58`.
- In "Overlap", `config.go:330` is the comment on `DefaultRedeployDrainLimit`. The key's field is at `config.go:528`.

### Missed by the first reading
- machine-home.md:152-158 never says where the machine file lives now. The code reads it at `~/.yoyodyne/machine.yaml` (`home.go:55-58`), and the validation script allows it at the top of the home (`validate-machine-home.sh:206`). The design's list of the top of the home (163-191) omits it. The report notes the file, but not that the top-of-home list is incomplete.

### Contradictions with other designs
- `v1-harness-design.md:469`: "`state_root` in the machine's own `machine.yaml` under the configurations home". machine-home.md:156-158 says the external configuration home ends, and the code reads `~/.yoyodyne/machine.yaml` (`home.go:55-58`). v1-harness-design is the one that is stale.
- machine-home.md:439-442 calls the move's drain limit "one of the numeric limits the development manager decides under portable agent configuration". portable-agent-configuration.md:337 includes numeric limits in general, but no key for the move's drain exists, and `execution.redeploy_drain_limit` is not in `roleApplicableKeys` (`internal/config/roleapply.go:17-36`).

## portable-agent-configuration.md
Verdict: agree (partly stale), with a sharper split. Sections 1, 2 (in part) and 4 are built. Sections 3, 5 and 6 are a plan, except for the refusal list of section 6.

The current line numbers are: section 3 at 179-219, section 4 at 221-266, section 5 at 268-314, section 6 at 316-437, and the lock sentence at 227.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G8-13 | DIFFERS | CORRECTED | The design's line is now 227, not 218. `init` writes the lock (`internal/config/scaffold.go:113`), and so does `yoyo config baseline` (`internal/cli/cli.go:182,523`). There is no `materialize`. |
| G8-14 | NOT BUILT | CORRECTED | Lines are now 184-208 and 279-302. Content confirmed: `internal/cli/cli.go:175-188` dispatches only `validate`, `show`, `drift` and `baseline`. |
| G8-15 | NOT BUILT | CORRECTED | The line is now 254-260, and the text "designed but not yet built" is at 258-259. Confirmed: `adopt` is not in `cli.go:175-188`. |
| G8-16 | NOT BUILT | CORRECTED | Lines 316-437. The typed write (372-376), the reviewed run (378-407), "applied by" in drift and doctor (409-415) and the `RACE_PACKAGES` grant (435-437) are not built, as the reader says. But the applicable-key list and its refusal (358-368) are built, in `internal/config/roleapply.go:17-70`, and only tests call them. That list differs from the table at 332-343 in four ways. (1) It lacks all seven cost keys, which exist nowhere in `internal/config`. (2) It adds `main_checks` and `agents.*.time_limits` (`roleapply.go:20,35`), which the table does not name. (3) It names keys with their real paths, `execution.developer_models` and `execution.developer_slots`. (4) It gives reasons for refusing `install` and `review_threads` (`roleapply.go:41,44`), which the refused list at 358-366 does not name. The design says the list is held "beside the role contracts" (367-368). It is in `internal/config`. `internal/ownership/hand.go:75-79` treats the whole write as a gap a person fills by hand. |

Unclear settled:
- G8-17: half MATCHES, half NOT BUILT.
  - `config validate` and `doctor` report drift on standard error without changing the exit code (`internal/cli/cli.go:232,286-287`; `internal/cli/doctor.go:83,104-112`).
  - `config show` has no "fourth line" naming the bundle and whether it is current. Its header prints the configuration, the layers, the revision and the state root (`internal/cli/cli.go:737-746`).
- G8-18: DIFFERS. Drift is one function, `config.ReadDrift` (`internal/config/drift.go:277`), which the CLI (`cli.go:232,362`), doctor (`doctor.go:112`) and Slack (`cli/slack.go:952`) each call for themselves. It is not in `internal/readmodel` (no drift field; `services.go` "SettingsDrift" is unrelated), and the dashboard does not show it. Lines 173-177 and decision 4 (477-479) claim a read-model derivation.

MATCHES that are not:
- G8-10. `checkBundleDocument` refuses `extends` and `product`, and requires `version` to equal `CurrentVersion` (`internal/config/builtin.go:152-164`). The design (131-136) says it refuses a bundle "declaring either" `version` or `product`. This is wrong in the design and was missed by the reader.

### Missed by the first reading
- Lines 131-136, `version` "refused from a bundle". The code requires it (`builtin.go:152-157`). The design's own line 305-306 says "product or extends another bundle", which is right.
- Section 6 table, lines 338-343. Seven keys this revision added do not exist in configuration at all (no `yaml:"model_kinds"` etc. in `internal/config`), so "their bounds ... are held in Go" (348-349) is not true today.
- Line 167-169, the `config show` header line, is not built (see G8-17). The design states it in the present tense, as if built.

### Contradictions with other designs
- Within the document, line 131-136 (`version` refused) contradicts line 305-306 (product or extends refused).
- `machine-home.md:227-228` puts a kept configuration at `projects/<id>/config.yaml`, outside the repository. Section 6's reviewed run (384-401) and the lock "committed with the configuration" (232) assume a committed `.yoyodyne/config.yaml`. Neither design says how a role's value reaches a kept, uncommitted configuration.

## pinned-outside-files.md
Verdict: should be **partly stale**, not current. "What this repository needs" (lines 162-168) and the "parked" sentence (line 51) describe work that is done.

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G8-20 | DIFFERS | CONFIRMED | `internal/dashboard/assets/` holds `chart.umd.js`, `chart.js-LICENSE.md`, both `.yoyodyne-source.yaml` records, `delivery.html` and `delivery.js`. |

Unclear settled:
- G8-19: MATCHES.
  - "At most six runs from one content of the record": `MaxOutsideFileRuns = MaxOutsideFileAttempts + MaxOutsideFileQueueFailures` = 3 + 3 (`internal/runstate/outsidefile.go:38,46,51`). It is enforced when a new build restarts a stopped record (`internal/orchestrator/outsidefilelanding.go:166`) and when a record is due (`:195`).
  - "Three failed queue runs stop the record" (design line 136): `outsidefilelanding.go:409`.
  - One warning report: `stopLanding` at `:384`, `:861`.

Constants re-checked and right: `fetch.go:17-24` (5 MiB, 50 MiB, 5 redirects, 10 minutes) and `record.go:31,35`.

### Missed by the first reading
- The six-run bound is made of two limits, and the design states only one. Three runs that stop for a cause that judged nothing (a time limit, the forge, the machine) also stop the record (`runstate/outsidefile.go:32-38`; `outsidefilelanding.go:383-384`). The design's "When a fetch fails" has no such rule, and the "six" at line 160 is unexplained without it.

### Contradictions with other designs
- None found beyond the reader's note on observability-and-dashboard (not re-checked).

## project-groups.md
Verdict: agree (mostly a plan not built).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G8-12 | NOT BUILT | CONFIRMED | No Go reference to `group.yaml`, `group init` or groups directories. `internal/cli/cli.go` has no `group` command. |
| G8-21 | DIFFERS | CONFIRMED | machine-home.md:164-170 already lists `groups/<group id>/intent/` (revision 14, frontmatter line 68). project-groups.md:48 still says "is revised to say so". |

Unclear settled:
- G8-22: NOT BUILT. There is no group code, so there is no doctor refusal. `refs/yoyodyne/records` is built (`internal/gitworktree/recordsref.go:38`). `refs/yoyodyne/tracker` is not: no Go reference, and it is piece 15 of `working-alongside-other-contributors.md:354`. Section 2 (line 52) rests on an unbuilt reference.

### Missed by the first reading
- Line 52: the one-tracker sync depends on `refs/yoyodyne/tracker`, which the contributors design has not built either (see G8-22).

### Contradictions with other designs
- None found.

## tracker-store-size.md
Verdict: agree (partly stale).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G8-23 | NOT BUILT | CONFIRMED | `internal/maintain` has no backup handling. `.beads/config.yaml:40-44` has the backup keys commented out. `.beads/.gitignore:61` has `backup/`. |
| G8-25 | DIFFERS | CONFIRMED | `internal/maintain/trackerrebuild.go:51-53,76-78`: `rebuildRetry = 24 * time.Hour`. |

Unclear settled:
- G8-26 (the one-minute refresh): MATCHES. `internal/readmodel/sharedlisting.go:63` has `sharedReadingFresh = time.Minute`, and its comment (`:52-60`) says the dashboard's ten-second standings are answered from a listing up to a minute old. A failed listing keeps the last good one and its time (`:34-38`). The reader looked in `internal/beads` (the in-process single flight) and missed this.
- G8-31: MATCHES. There is no `compact` or `flatten` in non-test Go code under `internal/maintain`.

### Missed by the first reading
- Nothing beyond what the report already lists.

### Contradictions with other designs
- None found.

## configurable-workflows.md
Verdict: agree (partly stale).

| Finding | First reading | Second reading | Evidence and correction |
|---|---|---|---|
| G8-27 | NOT BUILT | CONFIRMED | The design's line is 109. The top-level commands in `internal/cli/cli.go:36-145` include no `workflow`. |
| G8-28 | NOT BUILT | CONFIRMED | No `StepAttempt`, idempotency-key type or trial counter exists anywhere in `internal`. |

Unclear settled:
- G8-32: MATCHES. `repository.read` and `repository.list` are the real names (`internal/capability/capability.go:47,57`), and the Lead Product Manager, architect and development manager hold both (`internal/rolecapability/bundles.go:38,47,110-111,143-144`). The program manager holds them too, plus `repository.search` (`:242-249`), which the design (line 131) does not name. The trial counts behind the switch (50 runs over 7 days): no record of them, so NOT BUILT.
- G8-33: DIFFERS. `init` writes no workflow files. `internal/config/scaffold.go` and `internal/cli/init.go` never mention workflows, and `docs/configuration.md:5441-5449` tells a project to write `.yoyodyne/workflows/*.yaml` itself.

MATCHES re-checked: `MaxConcurrentFirings = 4` (`recurringfirings.go:56`) and the operators mapping (`scaffold.go:695-704`) hold.

### Missed by the first reading
- Line 127 names acting capabilities that do not exist: `run.cause`, `run.repair-continue`, `publication.merge-repeat`, `budget.cross-cap`. The development manager's bundle holds `work.triage`, `run.inspect`, `run.verify` and `run.recover` (`bundles.go:146-161`). Only `work-item.repair-state` for the Lead Product Manager exists as named (`capability.go:169`).
- Line 209, settled question 6: `init` does not materialize workflow files (see G8-33).
- Line 149: "that design is revised to say so; until then". program-manager.md was revised (its frontmatter line 52, yoyodyne-ifd.428.75.1), so the "until then" clause is stale.

### Contradictions with other designs
- `workflow-diagrams.md:41` describes `yoyo workflow show --effective` as what "prints" the definition, as though built. Neither that command nor configurable-workflows.md:109's `yoyo workflow validate` exists. Both designs rest on the same unbuilt command.
- configurable-workflows.md:127 (the development manager's capability names) against `fresh-factory-health-and-preserved-run-recovery.md`, which `bundles.go:158-161` cites for `run.recover`. The capability names differ, and the code follows the latter.
