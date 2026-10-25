# yoyodyne-ifd.432.25.18: why the Xcode license step did not reach the operator while the factory was stopped

From about 17:36 PDT on October 23 to 11:49 PDT on October 24 the Xcode
license on the harness's Mac was not accepted. `/usr/bin/make` is Apple's
developer-tools launcher, so every check exited 69, every developer run failed
whatever its change, the brake held new work at 17:43 PDT, and the developer
slots emptied. The step that ended it, `sudo xcodebuild -license accept`, needs
the Mac's administrator password, so only the operator could take it. This
records what Slack was actually told during those hours, read from the Slack
sink's own log and records under the product's state directory, and why none
of it worked as a call to take that step. All times are PDT.

## What the records say happened

- **17:36 October 23.** A developer run on shared provider cost reporting
  (yoyodyne-ani) filed a critical report, addressed to the development manager,
  naming the license and the command to run (report-10e5f629).
- **17:37.** The Lead Product Manager handled it as the operator's, for the
  reason `credential`, with the exact step in her handling.
- **The next sink pass, about 17:37 to 17:40.** The sink posted the finding
  once, as a reply in the thread for the cost-reporting item, also shown in the
  channel, tagged to the operator, and sent it to him once as a direct message.
  The sink log records both: `posted operator-action.recorded about
  yoyodyne-ani` and `said operator-action.recorded to U0BR9M2EKKP directly`.
  The direct-message permission fixed that morning worked; the last refusal for
  a missing permission in the log is earlier.
- **17:43.** The brake held new work after three runs blocked in a row. The
  hold was posted and sent directly twice. Its words are the brake's own: three
  runs blocked, each with "make fmtcheck exited with 69". They do not name the
  license or the step.
- **About 17:45.** The development manager escalated the hold to the Lead
  Product Manager. From then on the read model names the hold as hers, so the
  hourly stopped-line message was posted untagged and was never sent directly
  (`internal/slack/heartbeat.go`, the brake-hold branch of
  `heartbeatDeliveries`).
- **18:44.** The operator was sent one direct question about the stopped line
  (`internal/slack/dm.go`). It offered "release intake" or "keep intake held".
  That question is about the brake's hold, not the license, and releasing would
  only have sent runs into the same failure. It is asked once per held state.
- **18:44 October 23 to the release.** The stopped-line message was posted in
  the channel 18 times, once an hour, each naming the brake's hold. In the
  same stretch the channel carried 654 brake messages (`watch.braked`) and 19
  stale-build messages, and the item threads carried about four thousand
  failed-check replies.
- **11:49 October 24.** The operator's assistant confirmed the license was
  accepted. The hold was released at 12:40, on the development manager's
  decision; nothing re-checked the license on its own (that is the item for
  re-checking a hold whose cause a command can confirm, yoyodyne-ifd.432.25.19).

## Why it did not work

The step was sent, once, and nothing connected it to the stop.

1. **A finding for the operator is said exactly once.** The sink marks it as
   said and never repeats it while it stands
   (`operatorActionDeliveries` in `internal/slack/feed.go`); the message itself
   says "this is not said again". One message among hundreds in those hours, in
   a thread for a cost-reporting item that had been open since October 2, was
   the only time Slack carried the step.
2. **The message that does repeat names a different cause.** The hourly
   stopped-line message and the direct question are derived from the read
   model's account of why nothing starts (`readmodel.WhyNothingStarts`), which
   knows about holds, sessions and the provider, and knows nothing about a
   person-only step. So every repeating message said the brake was holding new
   work, and none said what would end it.
3. **The repeating message was not his.** Once the hold was escalated to the
   Lead Product Manager, the ownership registry named it hers, so the hourly
   message was not tagged to the operator and was not sent to him directly.
   That is correct for the hold, and it is exactly why the one thing only he
   could do never reached him again.
4. **Nothing records that the step is what stops the factory.** The handling
   that names the operator records the step and the reason, not what it holds
   back. The link between report-10e5f629 and the hold exists only in the
   prose of the development manager's report. No code can tell from the records
   that this step, rather than any other standing finding, is why the slots are
   empty.

The work item says the step "reached him only as one entry on the
needs-your-hand list, never on Slack". The sink log says otherwise: it was
posted and sent directly once. What failed is that it was said once, buried,
and never tied to the stop.

## What would have reached him

A message that is sent to the operator while the factory is stopped, that names
the step only he can take as the reason, and that repeats until the step is
done. Point 4 above is the precondition for that: a record of which waits hold
intake or leave the slots empty, owned by the operator. That record is the
follow-up of [the follow-ups design](../designs/follow-ups.md) (its fields
"what it blocks" and "owner", and its environment check for a license
accepted). The design's section "A step only a person can take, when it stops
the factory" is this item's sending half, built as its build piece 5, after the
store (piece 1), the check runner (piece 2), the acts that write follow-ups,
asking the operator among them (piece 3), and the deadlines (piece 4).

Building the sending on today's records instead would mean guessing which
standing finding stops the factory, which is point 4 again, and a second
schedule beside the one that design puts in the check runner.
