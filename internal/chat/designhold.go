package chat

// The needs-design label in a conversation (docs/designs/needs-design-gate.md).
// The Lead Product Manager, the development manager, and a program manager in
// its lane put it on, through the label action and a creation's labels, which
// they already hold; no other role's conversation holds either. The architect
// changes no work item, so she has no way to put it on or take it off.

import "github.com/mason-bryant/yoyodyne/internal/domain"

// needsDesignLabelClause is what each role that may put the needs-design label
// on is told about it, in one constant so the three contracts cannot come to
// say different things.
const needsDesignLabelClause = `The label "` + domain.NeedsDesignLabel + `" holds an item from every developer run the harness would start, as a parking does, until the architect's design for it has landed: put it on an item whose conditions for done are choices about behaviour nobody has made yet, rather than letting a developer guess them, and say in the reason the question the design must answer, because that sentence is what the architect reads. The development manager takes it off, for now, once a document in docs/designs naming the item among the items it answers has landed on the target branch, and the reason she gives says which document and which commit; every other role leaves it on. A ruling only in the item's notes or its design field, or a document written and not yet landed, is not a reason to take it off.`
