package cli

// What an agent remembers, as the operator reads it: every memory the store
// holds for one agent, each with its whole history and the invocation behind
// every revision. The design makes that history the audit of agent-authored
// durable state, and says an audit history is a CLI surface rather than an agent
// one, so this is where it is read.
//
// The command reads and renders and does nothing else. The records are the
// memory store's own, read through the store, so the history an operator is
// shown is the history a later turn is briefed from; the Markdown is dressed by
// the console's own renderer, so a terminal that cannot show emphasis loses the
// weighting and none of the words.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// memoryReport is one memory as the operator reads it. Its revisions are the
// store's own records, newest first: the question an operator brings is what the
// agent believes now and how it came to, and the answer to the first is the top.
type memoryReport struct {
	Name       string                    `json:"name"`
	Continuity runstate.MemoryContinuity `json:"continuity"`
	Subject    string                    `json:"subject,omitempty"`
	Retired    bool                      `json:"retired"`
	Compacted  bool                      `json:"compacted"`
	Revisions  []runstate.MemoryRevision `json:"revisions"`
}

func showAgentMemory(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent memory", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "name the agent whose memories to read, as `yoyo agent memory <name>`; `yoyo agent list` names them")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	name, role, err := resolveAgent(parts.config, positional[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	keeps := conversationAuthority(role, parts.config.Agents[name]).Memory

	// Built with no redaction values: everything in the store was redacted on the
	// way in, and a reader that redacted again would be a second answer to what
	// the record says.
	store, err := runstate.NewMemoryStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	memories, problems, err := store.Memories(name)
	if err != nil {
		// A store that will not be read is said to be exactly that. Rendering it as
		// a role with no memories would tell the operator the agent knows nothing,
		// when what is true is that nobody can say what it knows.
		fmt.Fprintf(stderr, "the memory store for %s under %s could not be read: %v\n", name, store.Root(), err)
		return 1
	}
	reports := make([]memoryReport, 0, len(memories))
	for _, memory := range memories {
		revisions := make([]runstate.MemoryRevision, len(memory.Revisions))
		for index, revision := range memory.Revisions {
			revisions[len(revisions)-1-index] = revision
		}
		reports = append(reports, memoryReport{
			Name:       memory.Name,
			Continuity: memory.Continuity,
			Subject:    memory.Subject,
			Retired:    memory.Retired(),
			Compacted:  memory.Compacted(),
			Revisions:  revisions,
		})
	}
	if problems == nil {
		problems = []runstate.MemoryProblem{}
	}

	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"product":      parts.config.Product.ID,
			"agent":        name,
			"role":         role,
			"keeps_memory": keeps,
			"memories":     reports,
			"problems":     problems,
		})
	}
	theme := console.ThemeFor(stdout, os.Getenv)
	fmt.Fprint(stdout, theme.Reply(renderAgentMemory(name, role, keeps, reports, problems)))
	return 0
}

// renderAgentMemory is one agent's memories as Markdown. Every distinction it
// draws is in the words — a heading's hashes, "retired", "compacts" — so the
// same text undressed, under NO_COLOR or into a pipe, still draws all of them.
func renderAgentMemory(name string, role domain.AgentRole, keeps bool, memories []memoryReport, problems []runstate.MemoryProblem) string {
	var rendered strings.Builder
	who := agentCalled(name, role)
	if !keeps && len(memories) == 0 && len(problems) == 0 {
		fmt.Fprintf(&rendered, "%s keeps no memory: the %s's turns carry no memory briefing and record none.\n",
			capitalized(who), role.Title())
		return rendered.String()
	}
	if len(memories) == 0 && len(problems) == 0 {
		fmt.Fprintf(&rendered, "%s has recorded no memories yet.\n", capitalized(who))
		return rendered.String()
	}

	fmt.Fprintf(&rendered, "# What %s remembers\n\n", who)
	if !keeps {
		// A role that keeps none can still find records under its name — written
		// under a configuration that gave it one, or by hand. They are shown rather
		// than hidden, because durable state nobody is shown is the failure this
		// command exists to prevent.
		fmt.Fprintf(&rendered, "The %s keeps no memory, and the store holds these under %s's name all the same.\n\n",
			role.Title(), name)
	}
	retired := 0
	for _, memory := range memories {
		if memory.Retired {
			retired++
		}
	}
	fmt.Fprintf(&rendered, "%s, %d of them retired. Each memory's newest revision is listed first.\n",
		counted(len(memories), "memory", "memories"), retired)

	for _, memory := range memories {
		rendered.WriteString("\n")
		heading := memory.Name
		if memory.Retired {
			heading += " (retired)"
		}
		fmt.Fprintf(&rendered, "## %s\n\n", heading)
		if memory.Continuity == runstate.MemoryContinuitySubject {
			fmt.Fprintf(&rendered, "About %s. ", memory.Subject)
		} else {
			rendered.WriteString("About the agent's own work. ")
		}
		fmt.Fprintf(&rendered, "%s.\n", counted(len(memory.Revisions), "revision", "revisions"))
		for _, revision := range memory.Revisions {
			rendered.WriteString("\n")
			rendered.WriteString(renderMemoryRevision(revision))
		}
	}

	if len(problems) > 0 {
		rendered.WriteString("\n## Records that could not be read\n\n")
		rendered.WriteString("These lines of the store would not decode, so what they held is not shown above.\n\n")
		for _, problem := range problems {
			fmt.Fprintf(&rendered, "- %s\n", problem.String())
		}
	}
	return rendered.String()
}

// renderMemoryRevision is one revision: what it says, set apart as a quotation so
// Markdown inside it is never read as this listing's structure, and then the
// invocation that wrote it and the records it cites.
func renderMemoryRevision(revision runstate.MemoryRevision) string {
	var rendered strings.Builder
	state := ""
	if revision.Retired {
		state = ", retiring the memory"
	}
	fmt.Fprintf(&rendered, "### Revision %d%s, recorded %s\n\n",
		revision.Sequence, state, revision.RecordedAt.UTC().Format(time.RFC3339))
	for _, line := range strings.Split(strings.TrimRight(revision.Text, "\n"), "\n") {
		if line == "" {
			rendered.WriteString(">\n")
			continue
		}
		fmt.Fprintf(&rendered, "> %s\n", line)
	}
	rendered.WriteString("\n")
	fmt.Fprintf(&rendered, "- written by %s\n", describeMemoryInvocation(revision.Invocation))
	fmt.Fprintf(&rendered, "- %s\n", describeMemoryProvider(revision.Invocation))
	fmt.Fprintf(&rendered, "- under the %s role\n", revision.Role.Title())
	if revision.Compacted() {
		numbers := make([]string, 0, len(revision.Compacts))
		for _, compacted := range revision.Compacts {
			numbers = append(numbers, fmt.Sprintf("%d", compacted))
		}
		fmt.Fprintf(&rendered, "- compacts revisions %s into this one\n", strings.Join(numbers, ", "))
	}
	if len(revision.Sources) > 0 {
		sources := make([]string, 0, len(revision.Sources))
		for _, source := range revision.Sources {
			sources = append(sources, fmt.Sprintf("%s %s", source.Kind, source.ID))
		}
		fmt.Fprintf(&rendered, "- drawn from %s\n", strings.Join(sources, ", "))
	}
	return rendered.String()
}

func describeMemoryInvocation(invocation runstate.MemoryInvocation) string {
	switch invocation.Kind {
	case runstate.MemoryInvocationRun:
		return fmt.Sprintf("run %s", invocation.ID)
	case runstate.MemoryInvocationSideStream:
		return fmt.Sprintf("side thread %s, turn %d", invocation.ID, invocation.Turn)
	default:
		return fmt.Sprintf("conversation %s, turn %d", invocation.ID, invocation.Turn)
	}
}

// describeMemoryProvider names what answered. A field the harness did not know
// when the revision was written is said to be unrecorded rather than left out,
// so a missing account never reads as no account.
func describeMemoryProvider(invocation runstate.MemoryInvocation) string {
	model := invocation.Model
	if invocation.ResolvedModel != "" && invocation.ResolvedModel != invocation.Model {
		model = fmt.Sprintf("%s (served as %s)", invocation.Model, invocation.ResolvedModel)
	}
	return fmt.Sprintf("%s, model %s, account %s, configuration %s",
		invocation.Backend, model,
		recorded(invocation.AccountAlias, "not recorded"),
		recorded(invocation.ConfigRevision, "not recorded"))
}

// agentCalled is how a sentence names the agent: by its role where the two are
// the same, which is every project `yoyo init` configured, and by both where a
// project named it something else.
func agentCalled(name string, role domain.AgentRole) string {
	if name == string(role) {
		return "the " + role.Title()
	}
	return fmt.Sprintf("%s (the %s)", name, role.Title())
}

func capitalized(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func counted(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", count, many)
}
