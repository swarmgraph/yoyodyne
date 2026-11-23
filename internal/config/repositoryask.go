package config

// The reading helper's bounds: execution.repository_ask.
//
// docs/designs/reducing-what-the-factory-costs.md, sections 7 and 8. A
// conversation role asks one question about the repository, and the harness
// answers it with one session on the provider's lightest model
// (internal/repositoryask). This setting bounds how long that session may run
// and how much of its answer comes back. It bounds and never grants: which roles
// hold the helper is held in the role bundles in Go, and nothing here reaches
// them (configuration-never-grants-authority).

import (
	"fmt"
	"time"
)

const (
	// DefaultRepositoryAskTimeLimit and DefaultRepositoryAskAnswerBytes are what
	// a project that states nothing gets.
	DefaultRepositoryAskTimeLimit   = 5 * time.Minute
	DefaultRepositoryAskAnswerBytes = 4 << 10
	// MinRepositoryAskTimeLimit and MaxRepositoryAskTimeLimit bound the time
	// limit. The upper bound keeps a role's message from waiting on one answer
	// for longer than a quarter of the time the whole message may run; the lower
	// one is the least a session needs to start and read what it is handed.
	MinRepositoryAskTimeLimit = 30 * time.Second
	MaxRepositoryAskTimeLimit = 15 * time.Minute
	// MinRepositoryAskAnswerBytes and MaxRepositoryAskAnswerBytes bound the
	// answer. Below the lower bound an answer has no room for the paths it
	// points at; above the upper one it stops being a pointer and becomes the
	// reading the role should do itself.
	MinRepositoryAskAnswerBytes = 1 << 10
	MaxRepositoryAskAnswerBytes = 8 << 10
)

// RepositoryAsk is execution.repository_ask. A zero field takes its default.
type RepositoryAsk struct {
	// TimeLimit is how long the helper's session may run.
	TimeLimit Duration `yaml:"time_limit,omitempty" json:"time_limit,omitempty"`
	// AnswerBytes is the most text of the answer that reaches the asking role,
	// counting the answer's prose and the paths and line ranges it cites.
	AnswerBytes int `yaml:"answer_bytes,omitempty" json:"answer_bytes,omitempty"`
}

// Limit is the time limit with its default applied.
func (r RepositoryAsk) Limit() time.Duration {
	if r.TimeLimit <= 0 {
		return DefaultRepositoryAskTimeLimit
	}
	return r.TimeLimit.Duration()
}

// AnswerLimit is the answer limit with its default applied.
func (r RepositoryAsk) AnswerLimit() int {
	if r.AnswerBytes <= 0 {
		return DefaultRepositoryAskAnswerBytes
	}
	return r.AnswerBytes
}

type repositoryAskDocument struct {
	TimeLimit   *Duration `yaml:"time_limit"`
	AnswerBytes *int      `yaml:"answer_bytes"`
}

// repositoryAskProblems refuses a time limit or an answer limit outside the
// bounds held here. Zero is left out, which takes the default; a negative value
// is never a default and is refused.
func repositoryAskProblems(c Config) []string {
	var problems []string
	setting := c.Execution.RepositoryAsk
	if limit := setting.TimeLimit.Duration(); limit != 0 && (limit < MinRepositoryAskTimeLimit || limit > MaxRepositoryAskTimeLimit) {
		problems = append(problems, fmt.Sprintf("execution.repository_ask.time_limit is %s; it must be between %s and %s, or left out for %s",
			limit, MinRepositoryAskTimeLimit, MaxRepositoryAskTimeLimit, DefaultRepositoryAskTimeLimit))
	}
	if answer := setting.AnswerBytes; answer != 0 && (answer < MinRepositoryAskAnswerBytes || answer > MaxRepositoryAskAnswerBytes) {
		problems = append(problems, fmt.Sprintf("execution.repository_ask.answer_bytes is %d; it must be between %d and %d, or left out for %d",
			answer, MinRepositoryAskAnswerBytes, MaxRepositoryAskAnswerBytes, DefaultRepositoryAskAnswerBytes))
	}
	return problems
}
