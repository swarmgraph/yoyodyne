# Yoyodyne configuration

**A Yoyodyne project owns its configuration outright.** `yoyo init` writes a
complete `.yoyodyne/config.yaml` — every agent, backend, model selector,
provider account, instance count, and persona reference stated in the file — and copies the
personas themselves into `.yoyodyne/personas/`. Nothing is inherited at load
time, so what the file says is what runs, and an edit to it is an edit to the
harness's behavior with nothing in between.

The executable still contains a versioned, read-only bundle of agent definitions
and personas. It is the **template `init` generates from**, not a layer
underneath your project. A project therefore never needs access to the Yoyodyne
source checkout, and nobody reading its configuration has to be told where a
value came from.

**What owning your defaults costs.** A later Yoyodyne that improves a persona or
corrects a model selector does not change a project that already has its own
copy — nothing is inherited, so nothing arrives. It does say so, and moving a
value stays yours. That report, and inheritance for projects wanting the other
half of the trade, are both under
[Extending a built-in bundle](configuration/setup.md#extending-a-built-in-bundle).

## The guides

The configuration is described in seven guides in
`docs/configuration/`, one for each part of a project's
configuration a person sets out to change:

- **[Writing a project configuration](configuration/setup.md)** — what `yoyo init` writes, where the harness looks for it, how the layers combine and what refuses a configuration when it loads, where the harness keeps its state on the machine, how to keep the configuration and the intent outside the repository, the parts of the product the supervisor starts, and how to read back what a project actually runs under. Start here if you are giving a project its own configuration or changing one it already has.
- **[Configuring artifact homes, identity, and ownership](configuration/artifacts.md)** — where every role reads the product's intent from, what identifies a document, how an approval is recorded, who may change which document, and what a developer's change is refused from touching.
- **[Configuring admission, attribution, and staleness](configuration/goals.md)** — what reaches the work queue and on whose approval, how work is traced back to the brief, how a goal claims the work attributed to it, what a change upstream leaves stale, how a release is judged ready, and the invariants a change is held to.
- **[Configuring checks, scheduling, and what a run may spend](configuration/runs.md)** — the checks every run passes before it reaches review or integration — which run when, what they may cost, the environment they are given and what they may leave behind — how ready work is picked up, how much of it runs at once, in which slot and on which model, and the workflow definition a run follows.
- **[Configuring publishing, branches, and promotion](configuration/publishing.md)** — how a finished branch reaches the remote, what to do when you cannot push to it, which branch is authoritative, what publishing needs from the machine, how one repository is worked from more than one machine, what happens when two promotions race for the same target, and how a merge queue lands approved changes one at a time.
- **[Configuring triage thresholds and provider waits](configuration/recovery.md)** — what the harness does when a provider refuses, serves a turn from another model, runs out of room in a session, or dies mid-run, and when the network drops; the model version, effort level, sub-agents, skills, and settings each agent is given; and the thresholds that decide when a stalled run, a stuck merge, or a work item that has been given enough is put to the development manager.
- **[Configuring operators, personas, and reporting](configuration/agents.md)** — the humans this project recognizes and what each may do, the provider accounts agents run under, how agents ask one another and how long their turns may take, the research sources, pinned files, tools, and scheduled message intake a conversation is given, the recurring tasks that wake a role on a schedule, the personas that specialize a role, and what reaches Slack.

## Where each section went

Every heading this document carried before it became an index is still a
heading here, so a link written against one keeps working. Each says which
guide now holds that section, and links to it.

### Creating a project configuration
Now in [Writing a project configuration](configuration/setup.md#creating-a-project-configuration).
#### When the repository ignores the configuration
Now in [Writing a project configuration](configuration/setup.md#when-the-repository-ignores-the-configuration).
#### Where the tracker syncs
Now in [Writing a project configuration](configuration/setup.md#where-the-tracker-syncs).
### Layout
Now in [Writing a project configuration](configuration/setup.md#layout).
#### Protected role definitions
Now in [Writing a project configuration](configuration/setup.md#protected-role-definitions).
#### A program manager instance
Now in [Writing a project configuration](configuration/setup.md#a-program-manager-instance).
### Discovery
Now in [Writing a project configuration](configuration/setup.md#discovery).
### Keeping the configuration outside the repository
Now in [Writing a project configuration](configuration/setup.md#keeping-the-configuration-outside-the-repository).
#### Keeping the intent outside the repository too
Now in [Writing a project configuration](configuration/setup.md#keeping-the-intent-outside-the-repository-too).
### Where the harness keeps its state: `state_root`
Now in [Writing a project configuration](configuration/setup.md#where-the-harness-keeps-its-state-state_root).
#### A product id names one repository on the machine
Now in [Writing a project configuration](configuration/setup.md#a-product-id-names-one-repository-on-the-machine).
### Precedence
Now in [Writing a project configuration](configuration/setup.md#precedence).
### Product specifications
Now in [Configuring artifact homes, identity, and ownership](configuration/artifacts.md#product-specifications).
#### What the Lead Product Manager sees besides them, and what it does not
Now in [Configuring artifact homes, identity, and ownership](configuration/artifacts.md#what-the-lead-product-manager-sees-besides-them-and-what-it-does-not).
### Artifact identity and metadata
Now in [Configuring artifact homes, identity, and ownership](configuration/artifacts.md#artifact-identity-and-metadata).
#### Approving a document
Now in [Configuring artifact homes, identity, and ownership](configuration/artifacts.md#approving-a-document).
#### What reaches the queue
Now in [Configuring admission, attribution, and staleness](configuration/goals.md#what-reaches-the-queue).
#### Who may change an artifact
Now in [Configuring artifact homes, identity, and ownership](configuration/artifacts.md#who-may-change-an-artifact).
#### Protected paths in a developer's change
Now in [Configuring artifact homes, identity, and ownership](configuration/artifacts.md#protected-paths-in-a-developers-change).
#### Proposing a change to a document you do not own
Now in [Configuring artifact homes, identity, and ownership](configuration/artifacts.md#proposing-a-change-to-a-document-you-do-not-own).
#### Traceability: references and orphans
Now in [Configuring admission, attribution, and staleness](configuration/goals.md#traceability-references-and-orphans).
#### Goals, and the work attributed to them
Now in [Configuring admission, attribution, and staleness](configuration/goals.md#goals-and-the-work-attributed-to-them).
#### What a change upstream leaves stale
Now in [Configuring admission, attribution, and staleness](configuration/goals.md#what-a-change-upstream-leaves-stale).
#### The release-readiness workflow
Now in [Configuring admission, attribution, and staleness](configuration/goals.md#the-release-readiness-workflow).
### Architectural invariants
Now in [Configuring admission, attribution, and staleness](configuration/goals.md#architectural-invariants).
### Checks
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#checks).
#### What a developer has to have run
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#what-a-developer-has-to-have-run).
#### What a check leaves running
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#what-a-check-leaves-running).
#### The environment a check runs in
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#the-environment-a-check-runs-in).
#### The environment the harness's own Git and forge commands run in
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#the-environment-the-harnesss-own-git-and-forge-commands-run-in).
#### Which provider authentication is supported
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#which-provider-authentication-is-supported).
#### What `init` proposes for `checks`
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#what-init-proposes-for-checks).
#### How long a check may take
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#how-long-a-check-may-take).
#### What a whole check stage may cost
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#what-a-whole-check-stage-may-cost).
#### Where the whole suite runs
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#where-the-whole-suite-runs).
#### How many of a heavy check may run at once
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#how-many-of-a-heavy-check-may-run-at-once).
##### Setting a heavy check's number from what it cost
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#setting-a-heavy-checks-number-from-what-it-cost).
##### How long the heavy checks took, week by week
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#how-long-the-heavy-checks-took-week-by-week).
#### Checks a change runs only when it touches what they vouch for
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#checks-a-change-runs-only-when-it-touches-what-they-vouch-for).
#### The document checks
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#the-document-checks).
#### The checks run on main
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#the-checks-run-on-main).
#### The checks the merge queue runs
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#the-checks-the-merge-queue-runs).
#### Checks on CI (`execution.ci_checks`)
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#checks-on-ci-executionci_checks).
#### The companion intent repository's checks
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#the-companion-intent-repositorys-checks).
#### A check that could not run
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#a-check-that-could-not-run).
#### Naming a failed check's failing tests
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#naming-a-failed-checks-failing-tests).
### Scheduling ready work
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#scheduling-ready-work).
#### A developer slot that prefers a label
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#a-developer-slot-that-prefers-a-label).
##### A slot dedicated to its label
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#a-slot-dedicated-to-its-label).
#### Developer slot endpoints
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#developer-slot-endpoints).
##### What a developer run does with its slot's pair
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#what-a-developer-run-does-with-its-slots-pair).
##### What a review does with the reviewer's pair
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#what-a-review-does-with-the-reviewers-pair).
#### A developer model chosen by the item's label
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#a-developer-model-chosen-by-the-items-label).
#### Saying why ready work has not started
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#saying-why-ready-work-has-not-started).
#### Watching instead of draining
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#watching-instead-of-draining).
#### Pull requests waiting for a person to merge
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#pull-requests-waiting-for-a-person-to-merge).
#### Waiting for the team's branch
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#waiting-for-the-teams-branch).
#### When a configuration change takes effect
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#when-a-configuration-change-takes-effect).
#### A setting a running build does not know
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#a-setting-a-running-build-does-not-know).
#### Why each run says why it was there
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#why-each-run-says-why-it-was-there).
### Running a work item against the workflow definition
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#running-a-work-item-against-the-workflow-definition).
#### The definition is the project's to own
Now in [Configuring checks, scheduling, and what a run may spend](configuration/runs.md#the-definition-is-the-projects-to-own).
### Publishing through pull requests
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#publishing-through-pull-requests).
#### Publishing from a fork
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#publishing-from-a-fork).
#### Publishing without automatic integration
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#publishing-without-automatic-integration).
#### Which branch is authoritative
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#which-branch-is-authoritative).
#### A protected target lands through its pull request
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#a-protected-target-lands-through-its-pull-request).
#### What publishing needs
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#what-publishing-needs).
### Working one repository from more than one machine: `team`
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#working-one-repository-from-more-than-one-machine-team).
#### What the factories share: the records reference
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#what-the-factories-share-the-records-reference).
### Waiting out a provider that refuses
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#waiting-out-a-provider-that-refuses).
#### Serving a turn from a permitted alternate model
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#serving-a-turn-from-a-permitted-alternate-model).
#### A conversation too long for its provider session
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#a-conversation-too-long-for-its-provider-session).
#### Pinning an agent to a model version
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#pinning-an-agent-to-a-model-version).
#### An agent's effort level
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#an-agents-effort-level).
#### Developer sub-agents on a cheaper model
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#developer-sub-agents-on-a-cheaper-model).
#### Skills and instruction files a Codex role is given
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#skills-and-instruction-files-a-codex-role-is-given).
#### Settings, memory, skills, connectors, and instruction files a Claude Code role is given
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#settings-memory-skills-connectors-and-instruction-files-a-claude-code-role-is-given).
### Relaunching a run the provider killed
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#relaunching-a-run-the-provider-killed).
### Waiting out a network that dropped
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#waiting-out-a-network-that-dropped).
### Losing a race for the target branch
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#losing-a-race-for-the-target-branch).
### Integrating through a merge queue
Now in [Configuring publishing, branches, and promotion](configuration/publishing.md#integrating-through-a-merge-queue).
### How long one role may ask another
Now in [Configuring operators, personas, and reporting](configuration/agents.md#how-long-one-role-may-ask-another).
### How far behind a conversation's picture may fall
Now in [Configuring operators, personas, and reporting](configuration/agents.md#how-far-behind-a-conversations-picture-may-fall).
### Queueing a question, or holding it on a side thread
Now in [Configuring operators, personas, and reporting](configuration/agents.md#queueing-a-question-or-holding-it-on-a-side-thread).
### How long an agent's turns and calls may take
Now in [Configuring operators, personas, and reporting](configuration/agents.md#how-long-an-agents-turns-and-calls-may-take).
### Research sources
Now in [Configuring operators, personas, and reporting](configuration/agents.md#research-sources).
### Pinned outside files
Now in [Configuring operators, personas, and reporting](configuration/agents.md#pinned-outside-files).
### MCP servers, and the tools each agent may ask for
Now in [Configuring operators, personas, and reporting](configuration/agents.md#mcp-servers-and-the-tools-each-agent-may-ask-for).
### Scheduled message intake
Now in [Configuring operators, personas, and reporting](configuration/agents.md#scheduled-message-intake).
#### How a query runs
Now in [Configuring operators, personas, and reporting](configuration/agents.md#how-a-query-runs).
#### The rules document
Now in [Configuring operators, personas, and reporting](configuration/agents.md#the-rules-document).
### Slack review threads
Now in [Configuring operators, personas, and reporting](configuration/agents.md#slack-review-threads).
### Reading the repository from a conversation
Now in [Configuring operators, personas, and reporting](configuration/agents.md#reading-the-repository-from-a-conversation).
### Triage thresholds
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#triage-thresholds).
#### What one work item has been given
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#what-one-work-item-has-been-given).
##### What spends a round and what does not
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#what-spends-a-round-and-what-does-not).
##### Crossing a cap the operator decides to cross
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#crossing-a-cap-the-operator-decides-to-cross).
##### A crossing the development manager takes himself
Now in [Configuring triage thresholds and provider waits](configuration/recovery.md#a-crossing-the-development-manager-takes-himself).
### Merge and removal semantics
Now in [Writing a project configuration](configuration/setup.md#merge-and-removal-semantics).
#### What fails closed
Now in [Writing a project configuration](configuration/setup.md#what-fails-closed).
### Provider accounts
Now in [Configuring operators, personas, and reporting](configuration/agents.md#provider-accounts).
#### Pooling work across several accounts
Now in [Configuring operators, personas, and reporting](configuration/agents.md#pooling-work-across-several-accounts).
### Operators
Now in [Configuring operators, personas, and reporting](configuration/agents.md#operators).
### Reporting to Slack
Now in [Configuring operators, personas, and reporting](configuration/agents.md#reporting-to-slack).
#### Avatars
Now in [Configuring operators, personas, and reporting](configuration/agents.md#avatars).
### Services
Now in [Writing a project configuration](configuration/setup.md#services).
#### Saying when the factory has stalled
Now in [Writing a project configuration](configuration/setup.md#saying-when-the-factory-has-stalled).
#### The dashboard's entry
Now in [Writing a project configuration](configuration/setup.md#the-dashboards-entry).
### Recurring tasks
Now in [Configuring operators, personas, and reporting](configuration/agents.md#recurring-tasks).
#### Skipping a pass with nothing new
Now in [Configuring operators, personas, and reporting](configuration/agents.md#skipping-a-pass-with-nothing-new).
#### A task's own model
Now in [Configuring operators, personas, and reporting](configuration/agents.md#a-tasks-own-model).
#### Working the report pile on a cadence
Now in [Configuring operators, personas, and reporting](configuration/agents.md#working-the-report-pile-on-a-cadence).
#### Working the amendment queue on a cadence
Now in [Configuring operators, personas, and reporting](configuration/agents.md#working-the-amendment-queue-on-a-cadence).
#### A program manager instance's passes
Now in [Configuring operators, personas, and reporting](configuration/agents.md#a-program-manager-instances-passes).
### Personas
Now in [Configuring operators, personas, and reporting](configuration/agents.md#personas).
### Extending a built-in bundle
Now in [Writing a project configuration](configuration/setup.md#extending-a-built-in-bundle).
#### Converting an inheriting configuration to an explicit one
Now in [Writing a project configuration](configuration/setup.md#converting-an-inheriting-configuration-to-an-explicit-one).
### Migrating from `.yoyodyne.yaml`
Now in [Writing a project configuration](configuration/setup.md#migrating-from-yoyodyneyaml).
### Inspection
Now in [Writing a project configuration](configuration/setup.md#inspection).
