---
id: pinned-outside-files
kind: design
title: 'Pinned outside files: how a file from outside the project reaches the repository without widening what developer runs can reach'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-22T01:56:39.214836Z
      reason: 'yoyodyne-9bic: rules how a pinned outside file, first the dashboard''s Chart.js, reaches the repository. The harness fetches only records already on main, from listed hosts under fixed limits, and lands the file through a reviewed run of its own. No developer run gains network reach. Chart.js is kept, as the dashboard design already decided. Resent with one joined word the word check refused written as ordinary words.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-22T01:56:39.214836Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1457, for document-1457.1 under the automatic approval policy
---
# Pinned outside files

## What this is for

Developer runs cannot reach the network: their sandbox refuses every download, from package registries, from GitHub, and from the content delivery networks alike. That is deliberate, and it stays. But some work needs a file nobody in the project wrote. The first case is the chart library the dashboard design chose for the backlog page and the delivery page ([the dashboard design](observability-and-dashboard.md), the chart library under the backlog page). The backlog page (yoyodyne-ifd.432.33) landed without it, and the delivery page (yoyodyne-ifd.432.36) is parked until it is on main. No role can bring such a file in, and a person checking it in by hand is not a step the harness may ask of the operator.

This design says how a project declares a pinned outside file, and how the harness, not any run's sandbox, brings it into the repository. It serves the goal that implementation tasks are isolated in harness-managed worktrees and integrated automatically. It keeps the goal that safety invariants hold whatever the configuration says, because no developer run gains any reach. It also keeps the goal that any language is supported, because the harness fetches a file at an address and knows nothing about any package manager, which [the v1 non-goals](../product/goals/v1-non-goals.md) rule out.

The ruling was made on October 21 on yoyodyne-9bic, the item that asked for it.

## The chart library

The dashboard keeps Chart.js. The dashboard design already chose it. The backlog page, the delivery page, and the chart of closed items (yoyodyne-ifd.432.41) are all planned around one shared library. Drawing the charts as SVG from Go would keep every byte inside the project, but it would mean writing and keeping a chart drawer of our own for no gain the operator asked for. That choice is revisited only if a library ever cannot be pinned this way.

The dashboard design's chart library paragraph asks for a file beside the library recording its version, where it came from, its checksum, and its license text. This design meets it with the record below and a license file pinned the same way. The dashboard design's wording is brought into line with this at its next revision.

## The record

A pinned outside file is declared by a record file in the same directory. The record is named by adding `.yoyodyne-source.yaml` to the pinned file's name. One record declares one file. The harness finds records by looking for that ending among the files in the commit, leaving out files git ignores.

The person or role declaring the file writes these fields:

- `file`: the pinned file's name, in this directory.
- `url`: where to fetch it.
- `version`: the exact version, which must appear in the path of `url`.
- `license`: the license's SPDX identifier, such as `MIT`.
- `license_file`: the file in this directory that holds the license text. It is either a file the project wrote or a pinned file with its own record.
- `for`: the full id of the work item that asked for the file.
- `sha256`: optional. Where the source publishes a checksum, it should be given here.

The harness alone writes these fields, when it fetches:

- `sha256`, where the record did not give it;
- `size`, in bytes;
- `fetched_from`, the address the file finally came from after any redirects;
- `fetched_at`, the time of the fetch.

For example, the record `internal/dashboard/assets/chart.umd.js.yoyodyne-source.yaml` before the harness has fetched anything, where 4.4.1 stands for whatever exact version is declared:

```yaml
file: chart.umd.js
url: https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.js
version: 4.4.1
license: MIT
license_file: chart.js-LICENSE.md
for: yoyodyne-ifd.432.33
```

## Where a file may come from

The project lists the hosts it allows in `.yoyodyne/config.yaml` under `outside_files.hosts`. The list is empty unless the project fills it, and with an empty list nothing is ever fetched. The list changes only by a reviewed change. In this repository the development manager decides it, as she decides the project's checks.

The capability is trusted code, and the configuration names hosts and a size limit but never relaxes any of the following:

- The address uses https.
- Its host is on the list.
- It carries no user name, password, query string, or fragment.
- Its path contains the declared version.
- The request carries no credentials, cookies, or authorization header.
- At most five redirects are followed, each to https and to a host on the list, and the final address is recorded.
- The file is at most `outside_files.max_bytes`, 5 MiB unless the project sets it, and never more than 50 MiB whatever it sets.
- The file is never unpacked, run, or interpreted by the harness. One record brings in one file exactly as served.

## When the harness fetches

The harness fetches only records that are on main. A record a developer run has just written in its worktree is never fetched. The address is text an agent wrote, and fetching it before review would let any run make the harness send a request to a place of its choosing, and an address can carry data out. A record on main has been approved by an independent reviewer. The ban on query strings and the version in the path narrow what an address could carry even so.

At each pull, the scheduler reads main's records. A record is due when its file is missing, or when its `sha256` is empty.

## The run that brings a file in

1. **Its own place.** At most one such run is in progress at a time. It never takes a developer slot, and no developer run waits on it, in the same way as [the document landing route](document-landing-route.md). A heavy check inside it takes a machine-wide permit like any run.
2. **Attributed to its item.** The run is attributed to the item the record names in `for`. Its cost is counted there, and the item's notes say the file was brought in. A record whose `for` names no item the tracker holds is refused.
3. **The fetch.** The harness fetches the file in its own process, outside every agent's sandbox, under the limits above.
4. **The checksum.** Where the record gives `sha256`, the fetched file must match it. Where it does not, the checksum of this fetch is recorded, and the reviewer is told the file is trusted on its first fetch.
5. **The write.** The harness writes the file and the record's harness fields through the confined writer of [repository-confined writes](repository-confined-writes.md), with the run's worktree as the root. The candidate changes only those paths. A candidate that changes anything else is refused before any check runs.
6. **Checks.** The project's full checks run on the exact candidate, and the evidence is bound to it, as for any change.
7. **Review.** An independent reviewer judges the candidate. The reviewer is shown the record, the final address, the size, the checksum, whether the checksum was given or recorded on this fetch, the license file, and the first 2 KiB of the file where it is text. The reviewer refuses where the version the file names for itself differs from the record, where the license file is missing, or where the file is not what the record says it is.
8. **The merge.** It goes through the pull request, and through the merge queue when that is switched on.

## Every run checks every pinned file

Before the checks of every run, the harness compares every record on the candidate with its file:

- If the file is present, its checksum must match the record. A mismatch fails the run's checks and names the file. Nothing is ever overwritten to make it match.
- If the file is missing, that is not a failure. The run that brings files in will fetch it once the record is on main.
- A change made by anything but the run that brings a file in may not add or change the harness's fields in any record. Such a change fails the checks and names the record.

To change a version, a change edits `url` and `version`, clears the harness's fields, and removes the old file. Once that change is on main, the harness brings the new file in.

## When a fetch fails

- **The network.** A connection that fails, a timeout, or a server error is waited out. The record is tried again at a later pull, at least ten minutes after the last attempt, and nothing is counted against it. This follows the rule that a flaky network ends nothing.
- **The record.** Any failure that is about the record stops that record at once. These are: a host not on the list, an address that breaks the rules above, a redirect to a host not on the list, a missing file at the address, a file over the size limit, or a checksum that does not match. The harness files a warning report for the development manager naming the record, the address, and the reason. The record is tried again only after it changes on main. Nothing is put to the operator.

## What this repository needs

- `cdn.jsdelivr.net` in `outside_files.hosts`.
- The record above for `internal/dashboard/assets/chart.umd.js`, at the exact version the change declares.
- A second record for `internal/dashboard/assets/chart.js-LICENSE.md`, fetched from `LICENSE.md` in the same package at the same version.

Once both files are on main, the delivery page (yoyodyne-ifd.432.36) can be taken off its hold.

## Alternatives rejected

- **Letting one developer run reach a package registry.** This widens a sandbox, and every run on that seat could then carry data out. Installing from a registry also runs the package's own scripts.
- **The operator checking the file in by hand.** That is a hand step outside the operator's closed list, and every new library would need it again.
- **Using a package manager.** The v1 non-goals rule out native integration with any package manager, and the goal of supporting development in any language forbids building one in.
- **Fetching from a run's own worktree before review.** That would fetch addresses no reviewer has seen.
- **Checking the fetched file against a checksum the registry publishes.** This is stronger than trusting a first fetch, and a later revision may add it for sources that publish one. It needs code for each kind of source, and the record's own `sha256` field already lets a project give the published checksum.
- **Unpacking archives.** A library that needs many files is not covered yet. Each file it needs can be pinned on its own, and unpacking would put an archive format inside the harness.

## What is enforced in code

- No fetch happens from inside any agent's sandbox, and no developer run gains any network reach.
- Only records on main are fetched, only from listed hosts, and only under the limits above, whatever the configuration says.
- A run that brings a file in changes only the pinned file and its record's harness fields.
- Every run compares every pinned file with its record before its checks.
- Only the run that brings a file in writes the harness's fields.
- Writes go through the confined writer.

## What a reviewer can check

- With an empty host list, nothing is ever fetched.
- A record in a run's worktree that is not yet on main is never fetched.
- An address with a query string, an address on a host not on the list, a redirect to such a host, an address without the version in its path, or plain http is refused before any request is sent, or at the redirect.
- A file over the size limit is refused and not written.
- A fetched file whose checksum differs from the record's is refused, and a warning report names it.
- A pinned file changed by hand fails every run's checks, naming the file.
- A change that writes `sha256`, `size`, `fetched_from`, or `fetched_at` without being the run that brings a file in fails its checks.
- The run that brings a file in takes no developer slot, is reviewed independently, and merges through the pull request.
- A network failure is retried later without being counted. A failure about the record stops it and files one warning report for the development manager.
