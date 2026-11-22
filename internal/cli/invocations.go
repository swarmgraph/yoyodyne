package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// invocationLog is the record of what each developer and reviewer invocation is
// sent, or nil where there is no run store to keep it in.
func invocationLog(parts components) backend.InvocationLog {
	if parts.store == nil {
		return nil
	}
	return parts.store.Invocations()
}

// personaOrigin says where a configured persona was read from, for the record of
// what an invocation was sent: its file, and the version that pins its content.
func personaOrigin(persona config.Persona) string {
	origin := backend.OriginRepositoryFile(persona.Path, "")
	if persona.Version != "" {
		origin += ", persona version " + persona.Version
	}
	return origin
}

// readInvocations is `yoyo invocations`: what each developer and reviewer
// invocation of a run was sent, and the exact text of one of them.
func readInvocations(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("invocations", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) < 1 || len(positional) > 2 {
		printInvocationsUsage(stderr)
		return 2
	}
	runID := positional[0]
	number := 0
	if len(positional) == 2 {
		if _, scanErr := fmt.Sscanf(positional[1], "%d", &number); scanErr != nil || number < 1 || fmt.Sprint(number) != positional[1] {
			fmt.Fprintf(stderr, "invocations: %q is not an invocation number; numbers start at 1\n", positional[1])
			return 2
		}
	}
	parts, err := buildComponents(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "invocations: %v\n", err)
		return 1
	}
	if parts.store == nil {
		fmt.Fprintln(stderr, "invocations: this project has no run store to read")
		return 1
	}
	return writeInvocations(parts.store.Invocations(), runID, number, stdout, stderr)
}

func writeInvocations(store *runstate.InvocationStore, runID string, number int, stdout, stderr io.Writer) int {
	if number == 0 {
		entries, err := store.List(runID)
		if err != nil {
			fmt.Fprintf(stderr, "invocations: %v\n", err)
			return 1
		}
		if len(entries) == 0 {
			fmt.Fprintf(stdout, "Run %s has no invocation recorded. Only developer and reviewer invocations are kept, and only those started after this record was introduced.\n", runID)
			return 0
		}
		fmt.Fprint(stdout, renderInvocationList(runID, entries))
		return 0
	}
	sent, err := store.Text(runID, number)
	if err != nil {
		fmt.Fprintf(stderr, "invocations: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, renderInvocationText(sent))
	return 0
}

// renderInvocationList says each invocation of a run with its parts and sizes,
// and none of its text.
func renderInvocationList(runID string, entries []runstate.InvocationEntry) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Run %s: %d invocation(s) recorded. `yoyo invocations %s <number>` shows the exact text of one.\n", runID, len(entries), runID)
	for _, entry := range entries {
		fmt.Fprintf(&out, "\n%d. %s, started %s, %s", entry.Number, entry.Role, entry.StartedAt.Local().Format("2006-01-02 15:04:05 MST"), entry.Backend)
		if entry.Model != "" {
			fmt.Fprintf(&out, " %s", entry.Model)
		}
		if entry.Effort != "" {
			fmt.Fprintf(&out, " (effort %s)", entry.Effort)
		}
		if entry.Resumed != "" {
			out.WriteString(", continuing an earlier provider session")
		}
		fmt.Fprintf(&out, "; %d bytes in all\n", entry.WholeSize)
		for _, message := range entry.Messages {
			for _, part := range message.Parts {
				fmt.Fprintf(&out, "   - %s message: %s, %d bytes (%s)\n", message.Role, part.Name, part.Size, part.Origin)
			}
		}
	}
	return out.String()
}

// renderInvocationText is one invocation: what it was started with, then each
// message exactly as it was sent. A message's text follows a line that says its
// size in bytes and is followed by a line that ends it, so a reader, or a test,
// can take the text out whatever it contains.
func renderInvocationText(sent runstate.InvocationText) string {
	entry := sent.Entry
	var out strings.Builder
	fmt.Fprintf(&out, "Run %s, invocation %d: %s, started %s\n", entry.RunID, entry.Number, entry.Role, entry.StartedAt.Local().Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&out, "Provider: %s %s", entry.Backend, entry.ProviderVersion)
	if entry.Model != "" {
		fmt.Fprintf(&out, "; model %s", entry.Model)
	}
	if entry.Effort != "" {
		fmt.Fprintf(&out, "; effort %s", entry.Effort)
	}
	out.WriteString("\n")
	if entry.Posture != "" {
		fmt.Fprintf(&out, "Tool access: %s", entry.Posture)
		if len(entry.Tools) > 0 {
			fmt.Fprintf(&out, " (%s)", strings.Join(entry.Tools, ", "))
		}
		out.WriteString("\n")
	}
	if entry.WorkingDirectory != "" {
		fmt.Fprintf(&out, "Working directory: %s", entry.WorkingDirectory)
		if entry.Commit != "" {
			fmt.Fprintf(&out, " at commit %s", entry.Commit)
		}
		out.WriteString("\n")
	}
	if entry.Resumed != "" {
		fmt.Fprintf(&out, "Continues the provider session %s\n", entry.Resumed)
	}
	out.WriteString("This is the text the harness sent. What the provider's own program adds by itself, such as its built-in instructions and files it loads from the checkout, like CLAUDE.md, is not in it; the provider version and commit above say where to find those.\n")
	total := len(sent.Messages)
	for index, message := range sent.Messages {
		fmt.Fprintf(&out, "\n----- message %d of %d: %s, %d bytes -----\n", index+1, total, message.Role, len(message.Text))
		out.WriteString(message.Text)
		fmt.Fprintf(&out, "\n----- end of message %d -----\n", index+1)
	}
	return out.String()
}

func printInvocationsUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo invocations [options] <run-id> [<number>]

What each developer and reviewer invocation of a run was sent. With a run id it
lists the run's invocations, each with its settings and the parts its text is
made of with their sizes. With a number as well it prints the exact text of that
invocation: every message the harness gave the provider, in order, between lines
that say how many bytes it is.

The text is read back from the harness's own record and checked as it is read.
A part that is missing or no longer matches is said plainly and never filled in.
The text stays on this machine and is shown as it was sent, secrets included if
the harness sent one; that is a fault in what was sent, which this makes findable.

What the provider's own program adds by itself, such as its built-in
instructions and files it loads from the checkout, is not in it.

Options:
  --config <path>   configuration file path (default: the nearest project configuration)`)
}
