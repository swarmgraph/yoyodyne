package goal

import (
	"errors"
	"fmt"
	"strings"
)

// MaxRelevantGoals bounds the list carried beside an item's served goal.
const MaxRelevantGoals = 20

// ValidateRelevant checks the shape of a list before resolving its entries.
func ValidateRelevant(named []string) error {
	var problems []error
	if len(named) > MaxRelevantGoals {
		problems = append(problems, fmt.Errorf("relevant_goals has %d entries, limit is %d", len(named), MaxRelevantGoals))
	}
	for _, name := range named {
		if strings.TrimSpace(name) == "" || len(name) > MaxStatementBytes || strings.ContainsAny(name, "\r\n") {
			problems = append(problems, fmt.Errorf("relevant_goals entry %q must be one nonempty line of at most %d bytes", name, MaxStatementBytes))
		}
	}
	return errors.Join(problems...)
}

// ResolveRelevant uses the same resolution as the goal served. Where a project
// has no goals to check, it preserves the claims just as attribution does.
func (s Set) ResolveRelevant(named []string) ([]string, error) {
	if err := ValidateRelevant(named); err != nil {
		return nil, err
	}
	if named == nil {
		return nil, nil
	}
	resolved := make([]string, 0, len(named))
	for _, name := range named {
		attribution := s.Attribute(name)
		if attribution.State == StateUnresolved {
			return nil, fmt.Errorf("relevant goal %q: %s", name, attribution.Reason)
		}
		reference := strings.TrimSpace(name)
		if attribution.Resolved() {
			reference = attribution.Goal.Reference()
		}
		resolved = append(resolved, reference)
	}
	return resolved, nil
}

// DescribeRelevant resolves identities for readers without dropping claims that
// have stopped resolving. The latter still need correction by the item's owner.
func (s Set) DescribeRelevant(named []string) string {
	if len(named) == 0 {
		return "none recorded"
	}
	descriptions := make([]string, 0, len(named))
	for _, name := range named {
		attribution := s.Attribute(name)
		description := name
		if attribution.Resolved() {
			description = attribution.Goal.Reference()
		}
		if attribution.State == StateUnresolved {
			description += " (unresolved: " + attribution.Reason + ")"
		}
		descriptions = append(descriptions, description)
	}
	return strings.Join(descriptions, "; ")
}
