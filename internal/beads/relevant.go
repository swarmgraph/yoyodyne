package beads

import "encoding/json"

// relevantGoalsKey is independent of notes so a long or replaced note cannot
// lose the goals the run and review must consider.
const relevantGoalsKey = "yoyodyne_relevant_goals"

func encodeRelevantGoals(goals []string) string {
	encoded, _ := json.Marshal(goals)
	return string(encoded)
}
