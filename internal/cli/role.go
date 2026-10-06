package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type roleOutput struct {
	Roles       []readmodel.RoleDefinitionStatus `json:"roles,omitempty"`
	Activations []runstate.RoleActivation        `json:"activations,omitempty"`
	Recorded    *runstate.RoleActivation         `json:"recorded,omitempty"`
	Error       string                           `json:"error,omitempty"`
}

func runRole(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printRoleUsage(stdout)
		return 0
	}
	verb := args[0]
	if verb != "activate" && verb != "list" && verb != "history" {
		fmt.Fprintf(stderr, "unknown role command %q\n\n", verb)
		printRoleUsage(stderr)
		return 2
	}
	flags := flag.NewFlagSet("role "+verb, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	var by *string
	if verb == "activate" {
		by = flags.String("by", "", "person recording the activation (default: USER)")
	}
	positional, err := parseArguments(flags, args[1:])
	if err != nil {
		return 2
	}
	if verb == "activate" {
		if err := refusedToAgentProcess("yoyo role activate", "a person activates a role definition"); err != nil {
			return reportRoleError(stdout, stderr, *jsonOutput, err)
		}
		if len(positional) != 1 {
			fmt.Fprintln(stderr, "role activate requires exactly one definition name")
			return 2
		}
		if err := domain.ValidateIdentifier("role definition name", positional[0]); err != nil {
			return reportRoleError(stdout, stderr, *jsonOutput, err)
		}
	} else if len(positional) != 0 {
		fmt.Fprintf(stderr, "role %s does not accept positional arguments\n", verb)
		return 2
	}

	resolved, err := roleConfiguration(*configPath, verb == "history")
	if err != nil {
		return reportRoleError(stdout, stderr, *jsonOutput, err)
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return reportRoleError(stdout, stderr, *jsonOutput, err)
	}
	store, err := runstate.NewRoleActivationStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return reportRoleError(stdout, stderr, *jsonOutput, err)
	}
	if verb == "activate" {
		definition, found := resolved.RoleDefinitions[positional[0]]
		if !found {
			return reportRoleError(stdout, stderr, *jsonOutput, fmt.Errorf("no role definition %q was found beside %s", positional[0], resolved.Path))
		}
		person := strings.TrimSpace(*by)
		if person == "" {
			person = strings.TrimSpace(os.Getenv("USER"))
		}
		if person == "" {
			fmt.Fprintln(stderr, "role activate requires --by: the record says which person activated the definition")
			return 2
		}
		activation, err := store.RecordRoleActivation(definition.Name, definition.Digest, definition.Source, person)
		if err != nil {
			return reportRoleError(stdout, stderr, *jsonOutput, err)
		}
		if *jsonOutput {
			return writeJSON(stdout, stderr, roleOutput{Recorded: &activation})
		}
		fmt.Fprintf(stdout, "activated role definition %s, digest %s, by %s at %s\n", activation.Name, activation.Digest, activation.Person, roleActivationTime(activation.ActivatedAt))
		fmt.Fprintf(stdout, "recorded in %s\n", store.Root())
		return 0
	}

	history, err := store.History()
	if err != nil {
		return reportRoleError(stdout, stderr, *jsonOutput, err)
	}
	if verb == "history" {
		if *jsonOutput {
			return writeJSON(stdout, stderr, map[string]any{"activations": history})
		}
		if len(history) == 0 {
			fmt.Fprintln(stdout, "no role definitions have been activated for this product")
		}
		for _, activation := range history {
			fmt.Fprintf(stdout, "%s activated by %s at %s, digest %s\n  definition: %s\n", activation.Name, activation.Person, roleActivationTime(activation.ActivatedAt), activation.Digest, activation.Source)
		}
		return 0
	}
	roles := readmodel.RoleDefinitions(resolved.RoleDefinitions, history)
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{"roles": roles})
	}
	if len(roles) == 0 {
		fmt.Fprintln(stdout, "no role definitions were found beside the configuration")
	}
	for _, role := range roles {
		status := "not activated"
		switch {
		case role.Activated:
			status = "activated"
		case role.AmendedSince:
			status = "amended since activation; the current file is not activated"
		case role.MovedSince:
			status = "moved since activation; the file where it now stands is not activated"
		}
		fmt.Fprintf(stdout, "%s (extends %s): %s\n  definition: %s\n  current digest: %s\n", role.Definition.Name, role.Definition.Extends, status, role.Definition.Source, role.Definition.Digest)
		if role.Activation != nil {
			fmt.Fprintf(stdout, "  last activated by %s at %s, digest %s\n", role.Activation.Person, roleActivationTime(role.Activation.ActivatedAt), role.Activation.Digest)
		}
	}
	return 0
}

func roleActivationTime(at time.Time) string { return at.Local().Format("2006-01-02 15:04:05 MST") }

func roleConfiguration(explicitPath string, history bool) (config.Resolved, error) {
	path, err := configurationPath(explicitPath)
	if err != nil {
		return config.Resolved{}, err
	}
	if !history {
		// The definitions and the product, and no agent: activating a definition
		// an agent already names has to work before anything binds to it.
		return config.LoadRoleDefinitionsBeside(path)
	}
	product, err := config.RoleHistoryProduct(path)
	if err != nil {
		return config.Resolved{}, err
	}
	return config.Resolved{Config: config.Config{Product: product}, Path: path}, nil
}

func reportRoleError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, roleOutput{Error: err.Error()}); code != 0 {
			return code
		}
	} else {
		fmt.Fprintln(stderr, err)
	}
	return 1
}

func printRoleUsage(output io.Writer) {
	fmt.Fprintln(output, `Usage: yoyo role <command> [options]

  yoyo role activate <name> [--by <person>] [--config <path>] [--json]
  yoyo role list [--config <path>] [--json]
  yoyo role history [--config <path>] [--json]

Activation records a person's decision about a validated definition's exact
content digest. A process the harness launched for a role, marked by
YOYODYNE_AGENT_ROLE, is refused activation. --by defaults to USER.

List compares each definition with its latest activation. An amended or moved
file requires a new activation. History shows every activation, newest first,
including definitions whose files have been removed.

An agent fills a definition by naming it as its role:. Every other command
refuses a configuration whose agents fill a definition that is not activated as
it stands; these three do not, so a person can always activate one.`)
}
