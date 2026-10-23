package artifact

// Which goals a revision adds or removes.
//
// A change the Lead Product Manager marks consistent with intent is approved as
// hers (rewording.go), and the mark is her judgement: nothing here reads two
// versions of a sentence and decides whether they admit the same work. One kind
// of change needs no reading, though. Adding a goal makes the goals admit work
// they refused, and removing one makes them refuse work they admitted, so either
// is a change of fundamental intent by the operator's own test, whatever the
// revision says it is. That is what this finds: whether the goals the document
// states, under its `Goals` heading, gained or lost an entry. A goal reworded in
// place is neither, and is left to the mark and to the reviewer of the run that
// lands it.
//
// The goals are read here the way internal/goal reads them — one top-level list
// entry under a heading whose whole text is `Goals`, its statement rejoined from
// the indented lines under it, ended by the emphasized trailer naming what it
// supports — because that package resolves attributions against what this one
// stores, and cannot be imported from it. TestGoalStatementsAgreeWithTheGoalsPackage
// in internal/goal holds the two readings to one answer.

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	goalsSectionHeading    = regexp.MustCompile(`(?i)^goals?$`)
	nonGoalsSectionHeading = regexp.MustCompile(`(?i)^non-?\s*goals?\b`)
	goalEntryPattern       = regexp.MustCompile(`^[-*+]\s+(.*)$`)
	nestedGoalEntryPattern = regexp.MustCompile(`^\s+(?:[-*+]|\d+[.)])\s`)
	goalIdentityPattern    = regexp.MustCompile(`(?i)^\[([a-z0-9]+(?:-[a-z0-9]+)*)\]\s*`)
)

// StatesGoals reports a kind whose documents state goals work is admitted
// against: the goals, and the brief, which states what the product is for.
func StatesGoals(kind Kind) bool {
	return kind == KindGoals || kind == KindBrief
}

// GoalStatements returns the goals a document body states under its `Goals`
// heading, each rejoined onto one line with its identifier, if any, still on it.
func GoalStatements(body string) []string {
	var (
		stated           []string
		level            int
		inGoals, inFence bool
		open             bool
	)
	for _, raw := range strings.Split(strings.TrimPrefix(body, "\ufeff"), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence, open = !inFence, false
			continue
		}
		if inFence || line == "" {
			open = false
			continue
		}
		if depth, text, ok := atxHeading(line); ok {
			open = false
			switch {
			case nonGoalsSectionHeading.MatchString(text):
				inGoals = false
			case inGoals && depth <= level:
				inGoals = false
			case !inGoals && goalsSectionHeading.MatchString(text):
				inGoals, level = true, depth
			}
			continue
		}
		if !inGoals {
			continue
		}
		if entry := goalEntryPattern.FindStringSubmatch(raw); entry != nil {
			open = false
			if statement := strings.TrimSpace(entry[1]); statement != "" {
				stated = append(stated, statement)
				open = true
			}
			continue
		}
		if !open || (!strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t")) || nestedGoalEntryPattern.MatchString(raw) || goalTrailer(line) {
			open = false
			continue
		}
		stated[len(stated)-1] += " " + line
	}
	return stated
}

// goalTrailer reports the emphasized line under a goal naming what it supports,
// which ends the statement: a line opening emphasis that closes only at its end,
// or not at all because it wraps. One whose emphasis closes and then carries on
// is the rest of a wrapped sentence.
func goalTrailer(line string) bool {
	for _, marker := range []string{"**", "__", "*", "_"} {
		rest, found := strings.CutPrefix(line, marker)
		if !found || rest == "" || rest[0] == ' ' {
			continue
		}
		closing := strings.Index(rest, marker)
		return closing < 0 || strings.TrimSpace(rest[closing+len(marker):]) == ""
	}
	return false
}

// GoalChange says which goals the after body adds to or removes from the before
// body, in a clause a person reads, and is empty where it does neither.
//
// A goal is matched first by its identifier, then by its words, and what is
// left is matched in order, because that is what a rewording is: the same goal
// in the same place saying it differently. Two goals carrying different
// identifiers are never matched, because an identifier names one goal for good.
// What is left over on either side is a goal added or removed.
func GoalChange(before, after string) string {
	added, removed := goalChanges(GoalStatements(before), GoalStatements(after))
	var said []string
	for _, statement := range added {
		said = append(said, fmt.Sprintf(`adds the goal "%s"`, quotedGoal(statement)))
	}
	for _, statement := range removed {
		said = append(said, fmt.Sprintf(`removes the goal "%s"`, quotedGoal(statement)))
	}
	if len(said) == 0 {
		return ""
	}
	return "it " + strings.Join(said, ", and ")
}

func goalChanges(before, after []string) (added, removed []string) {
	beforeMatched := make([]bool, len(before))
	afterMatched := make([]bool, len(after))
	match := func(same func(b, a string) bool) {
		for i, b := range before {
			if beforeMatched[i] {
				continue
			}
			for j, a := range after {
				if !afterMatched[j] && same(b, a) {
					beforeMatched[i], afterMatched[j] = true, true
					break
				}
			}
		}
	}
	match(func(b, a string) bool {
		identity, _ := goalIdentity(b)
		other, _ := goalIdentity(a)
		return identity != "" && identity == other
	})
	match(func(b, a string) bool {
		_, words := goalIdentity(b)
		_, others := goalIdentity(a)
		return foldGoal(words) == foldGoal(others)
	})
	var restBefore, restAfter []int
	for i := range before {
		if !beforeMatched[i] {
			restBefore = append(restBefore, i)
		}
	}
	for j := range after {
		if !afterMatched[j] {
			restAfter = append(restAfter, j)
		}
	}
	for len(restBefore) > 0 && len(restAfter) > 0 {
		b, a := before[restBefore[0]], after[restAfter[0]]
		identity, _ := goalIdentity(b)
		other, _ := goalIdentity(a)
		switch {
		case identity == "" || other == "" || identity == other:
			restBefore, restAfter = restBefore[1:], restAfter[1:]
		default:
			removed = append(removed, b)
			restBefore = restBefore[1:]
		}
	}
	for _, i := range restBefore {
		removed = append(removed, before[i])
	}
	for _, j := range restAfter {
		added = append(added, after[j])
	}
	return added, removed
}

// goalIdentity separates the identifier a goal opens with from its words.
func goalIdentity(statement string) (string, string) {
	found := goalIdentityPattern.FindStringSubmatch(statement)
	if found == nil {
		return "", statement
	}
	return strings.ToLower(found[1]), strings.TrimSpace(statement[len(found[0]):])
}

func foldGoal(statement string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(statement), " ")), ".;:,")
}

// quotedGoal is a goal short enough to name in a sentence.
func quotedGoal(statement string) string {
	const limit = 160
	if len(statement) <= limit {
		return statement
	}
	cut := strings.LastIndex(statement[:limit], " ")
	if cut <= 0 {
		cut = limit
	}
	return statement[:cut] + "…"
}
