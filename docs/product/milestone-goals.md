---
id: milestone-goals
kind: goals
title: 'Milestone goals: useful delivery for little of the owner''s attention'
supports:
    - brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-10-25T15:16:34.273648Z
      reason: 'yoyodyne-ifd.437.59: resend of the milestone goals document after pull request #1461 was closed, with the text unchanged and a Supports line under each goal naming the brief goal it serves, as the reviewer asked (report cce46f79). A new goals document, so it waits for Mason''s approval.'
approvals:
    - revision: 0
      by: operator
      at: 2026-10-25T15:16:34.273648Z
      reason: approved by the operator in conversation chat-91253e0e070c17b0663651cc48602122, turn 3599, for the document the product-manager wrote there (document-3599.1)
---
# Milestone goals: useful delivery for little of the owner's attention

These goals set the next milestone for Yoyodyne and how it is measured. They add to the goals already recorded and narrow none of them. They come from the product leadership review the owner agreed with on October 24, 2026.

## Goals

- [owner-attention-measure] Yoyodyne is measured by useful software delivered per hour of the owner's attention. Useful software is a completed change whose observable behaviour does what its work item asked; the owner's attention is the time the owner spends answering, approving, or fixing things by hand.

  *Supports: the human's attention goes only where it is needed.*

- [internal-week-bar] Before any outside trial, Yoyodyne runs one week on its own repository in which no hour passes with ready work waiting and a developer slot idle, nothing is asked of the owner that the harness or a role could have done, and the management roles' spend is at most 40% of all known spend for the week.

  *Supports: the human's attention goes only where it is needed.*

- [outside-trial-week] After the internal week, Yoyodyne delivers a week of useful work on a few outside repositories written in different languages, doing ordinary product work, with little operational intervention from the owner. It is judged on the observable behaviour of what it delivered, not only on whether the work traced to a goal.

  *Supports: the human's attention goes only where it is needed.*

## What is tracked

For every week, from the first week counted, these figures are recorded and shown to the owner:

- the owner's intervention time and the number of interventions, split into requests made of the owner, messages from the owner, and fixes the owner made by hand;
- completed outcomes;
- defects found after a change landed;
- delivery latency, from admission to landing;
- the cost of retries;
- time any work spent blocked without an owner able to move it.

Code delivery and document publication are counted separately, so publishing documents never stands in for delivering software.
