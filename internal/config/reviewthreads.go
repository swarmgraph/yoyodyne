package config

// The Slack review thread a project asks the harness to keep for each pull
// request a person merges (docs/designs/slack-review-threads.md, section 1).
//
// Nothing here posts or reads anything. What this file settles is what loads:
// which declared MCP server, which of its tools post and read, which channel,
// which reviewers, and how often a thread is read. A project that says nothing
// has no section, and publishes exactly as it did before the key existed.
//
// Configuration names and narrows here and never grants: the section can only
// point at tools the operator's own mcp section already declares, the post tool
// must be one declared as act and the read tool one declared as read, and a
// role's write to the configuration is refused for the whole section.

import (
	"fmt"
	"time"
)

// ReviewThreads is the project's review_threads section.
type ReviewThreads struct {
	// Server is the name of a server the mcp section declares.
	Server string `yaml:"server,omitempty" json:"server,omitempty"`
	// PostTool is the tool of that server that posts a message, declared there
	// as act.
	PostTool string `yaml:"post_tool,omitempty" json:"post_tool,omitempty"`
	// ReadTool is the tool of that server that reads a thread's replies,
	// declared there as read.
	ReadTool string `yaml:"read_tool,omitempty" json:"read_tool,omitempty"`
	// Channel is Slack's id for the channel requests are posted in, never its
	// name, because anyone in the workspace can change a name.
	Channel string `yaml:"channel,omitempty" json:"channel,omitempty"`
	// Reviewers are the Slack member ids whose replies are told. They are ids
	// for the same reason the channel is.
	Reviewers []string `yaml:"reviewers,omitempty" json:"reviewers,omitempty"`
	// CheckEvery is how often a thread is read while its pull request is open.
	// It is DefaultReviewThreadCheckEvery when the section leaves it out.
	CheckEvery Duration `yaml:"check_every,omitempty" json:"check_every,omitempty"`
}

const (
	// DefaultReviewThreadCheckEvery is how often a thread is read when the
	// section does not say.
	DefaultReviewThreadCheckEvery = Duration(15 * time.Minute)
	// MinReviewThreadCheckEvery is the shortest interval a project may set; each
	// read is a provider call while Slack is reached through the operator's
	// account.
	MinReviewThreadCheckEvery = Duration(5 * time.Minute)
	// MaxReviewThreadCheckEvery is the longest interval a project may set; past
	// it a request for changes would wait about as long as it would unread.
	MaxReviewThreadCheckEvery = Duration(24 * time.Hour)
)

// Configured reports whether the project wrote a review_threads section at all.
// A section naming nothing is no section.
func (r ReviewThreads) Configured() bool {
	return r.Server != "" || r.PostTool != "" || r.ReadTool != "" || r.Channel != "" ||
		len(r.Reviewers) > 0 || r.CheckEvery != 0
}

// copyReviewThreads is a layer's section copied rather than aliased, with the
// interval filled in when the section is written and leaves it out.
func copyReviewThreads(source ReviewThreads) ReviewThreads {
	if !source.Configured() {
		return ReviewThreads{}
	}
	source.Reviewers = append([]string(nil), source.Reviewers...)
	if source.CheckEvery == 0 {
		source.CheckEvery = DefaultReviewThreadCheckEvery
	}
	return source
}

// reviewThreadProblems reports everything wrong with the review_threads
// section against the mcp section, at once, each naming the setting.
func (c Config) reviewThreadProblems() []string {
	r := c.ReviewThreads
	if !r.Configured() {
		return nil
	}
	var problems []string
	declared, serverDeclared := c.MCP.Servers[r.Server]
	switch {
	case r.Server == "":
		problems = append(problems, "review_threads.server is empty; name a server the mcp section declares")
	case !serverDeclared:
		problems = append(problems, fmt.Sprintf("review_threads.server names %q, and the mcp section declares no server by that name", r.Server))
	}
	problems = append(problems, r.toolProblems("post_tool", r.PostTool, MCPAct, r.Server, declared, serverDeclared)...)
	problems = append(problems, r.toolProblems("read_tool", r.ReadTool, MCPRead, r.Server, declared, serverDeclared)...)
	if r.Channel == "" {
		problems = append(problems, "review_threads.channel is empty; give Slack's id for the channel, never its name")
	}
	if len(r.Reviewers) == 0 {
		problems = append(problems, "review_threads.reviewers is empty; list the Slack member ids whose replies are told, or nobody's would be")
	}
	seen := map[string]bool{}
	for _, id := range r.Reviewers {
		switch {
		case id == "":
			problems = append(problems, "review_threads.reviewers holds an empty entry; each is a Slack member id")
		case seen[id]:
			problems = append(problems, fmt.Sprintf("review_threads.reviewers names %q twice", id))
		}
		seen[id] = true
	}
	if r.CheckEvery != 0 && (r.CheckEvery < MinReviewThreadCheckEvery || r.CheckEvery > MaxReviewThreadCheckEvery) {
		problems = append(problems, fmt.Sprintf("review_threads.check_every is %s; it must be at least %s and at most %s", r.CheckEvery, MinReviewThreadCheckEvery, MaxReviewThreadCheckEvery))
	}
	return problems
}

func (r ReviewThreads) toolProblems(key, tool string, want MCPToolClass, server string, declared MCPServer, serverDeclared bool) []string {
	setting := "review_threads." + key
	if tool == "" {
		return []string{fmt.Sprintf("%s is empty; name a tool of the server declared as %s", setting, want)}
	}
	if !serverDeclared {
		// The server's own refusal already says why this cannot be checked.
		return nil
	}
	class, named := declared.Tools[tool]
	switch {
	case !named:
		return []string{fmt.Sprintf("%s names %q, and mcp.servers.%s.tools does not name it, so it cannot be called by anyone", setting, tool, server)}
	case class != want:
		return []string{fmt.Sprintf("%s names %q, which mcp.servers.%s.tools declares as %s; it must be declared as %s", setting, tool, server, class, want)}
	}
	return nil
}
