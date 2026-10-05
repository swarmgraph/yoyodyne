# docs/product/goals

**Purpose.** The goals derived from [the product brief](../brief.md), and the
non-goals that bound them. [The v1 goals](v1-goals.md) and
[the v1 non-goals](v1-non-goals.md) were moved here unchanged from
[the v1 harness design](../../designs/v1-harness-design.md), because goals are
the Lead Product Manager's to own rather than the architect's. Every statement
under a goals document's `Goals` heading is a goal work can be attributed to, in the
words that document states it in; `yoyo goals list` is what they resolve to.
[Writing a goal](writing-goals.md) is filed beside them and states none of its
own: it says what a goal has to be, for whoever is writing one.

**Owner.** The Lead Product Manager. A change to what the product is for is the
operator's to decide. Any change consistent with the goals is the Lead Product
Manager's, or a program manager's within its lane, and is reported afterwards
(ruling of 2026-09-26). It is the only role that changes a document filed here.
Every other role proposes an amendment and waits for the owner to decide rather
than editing one, and `yoyo amendment list`
is what is waiting. See
[artifact ownership](../../designs/v1-harness-design.md#artifact-ownership).

**Editing by hand.** You may, and nothing here refuses it — you are the
operator, and these documents are yours. Two things follow rather than nothing.
Record the change in the document's own revision log under the Lead Product
Manager: a revision recorded under any other role is reported as unauthorized every time
the artifacts load, which is something to look at rather than a refusal. And
whatever downstream traced to what you changed is reported by `yoyo stale` until
its own owner revises it. Then say what you changed in `yoyo chat`, because a
conversation that is already open is working from these documents as they read
when it opened.

## What a goal looks like

The shape and identity a goals document must carry — the `Goals` heading, the
frontmatter, the *Supports* trailer resolved against the brief — is governed by
[the artifact contract](../../designs/artifact-contract.md), the normative home
for those rules. What each goal itself has to be — traceable upstream, specific
enough to design against, an outcome rather than an implementation — is
[writing a goal](writing-goals.md), filed beside the goals it describes. This
index links and describes; it states no rules of its own, and
[index-and-stray-identity-governance](../../decisions/index-and-stray-identity-governance.md)
is the decision that keeps it that way.
