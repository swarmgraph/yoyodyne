package cli

// The operator asking a program manager instance for a pass.
//
// "Passes on request" in docs/designs/program-manager.md: the operator asks
// with `yoyo agent pass <agent>`, the Lead Product Manager asks from her
// conversation, and nobody else asks. This records the request and nothing
// more: the scheduler takes it at its next pull, under every rule a pass is
// taken under, so the command never invokes the instance itself — the harness's
// scheduler is the only thing that wakes one.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// programManagerPassRequests is where a conversation's request for a pass is
// recorded: the product's pass requests, refusing an agent the configuration
// does not name as a program manager instance, so a request the scheduler
// could never take is never recorded.
type programManagerPassRequests struct {
	store     *runstate.PassRequestStore
	instances map[string]config.AgentConfig
}

func (p programManagerPassRequests) Request(request runstate.PassRequest) (runstate.PassRequest, error) {
	if _, configured := p.instances[request.Agent]; !configured {
		names := make([]string, 0, len(p.instances))
		for name := range p.instances {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return runstate.PassRequest{}, fmt.Errorf("%q is not a program manager instance, and this project configures none", request.Agent)
		}
		return runstate.PassRequest{}, fmt.Errorf("%q is not a program manager instance this project configures; the instances are %s", request.Agent, strings.Join(names, ", "))
	}
	return p.store.Request(request)
}

// focusValues is the repeated --focus flag.
type focusValues []string

func (f *focusValues) String() string { return strings.Join(*f, ", ") }

func (f *focusValues) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func askAgentPass(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent pass", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	var focus focusValues
	flags.Var(&focus, "focus", "an area of the remit, or a repository path; repeat for up to ten paths")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "name the program manager instance to ask, as `yoyo agent pass <name>`; `yoyo agent list` names them")
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
	if role != domain.RoleProgramManager {
		fmt.Fprintf(stderr, "%s is a %s, and only a program manager instance takes a pass on request\n", name, role.Title())
		return 1
	}
	requested, err := passFocus(parts.repository, focus)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	id, err := runstate.NewPassRequestID()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	requests := parts.store.PassRequests()
	recorded, err := requests.Request(runstate.PassRequest{
		SchemaVersion: runstate.PassRequestSchemaVersion,
		ProductID:     parts.config.Product.ID,
		ID:            id,
		Agent:         name,
		AskedBy:       runstate.PassAskerOperator,
		RequestedAt:   time.Now(),
		Focus:         requested,
	})
	if err != nil {
		fmt.Fprintf(stderr, "the request for a pass of %s was not recorded: %v\n", name, err)
		return 1
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"product": parts.config.Product.ID,
			"request": recorded,
		})
	}
	fmt.Fprint(stdout, describeAskedPass(requests, recorded))
	return 0
}

// passFocus sorts the --focus values into the one area or the repository paths
// a request names. A value naming a file or directory in the repository is a
// path; anything else is an area of the instance's remit, which the instance
// reads against the rotation its remit lists.
func passFocus(repository string, values []string) (runstate.PassFocus, error) {
	var area string
	var paths []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return runstate.PassFocus{}, errors.New("--focus was given nothing to focus on")
		}
		if namesRepositoryPath(repository, value) {
			paths = append(paths, value)
			continue
		}
		if area != "" {
			return runstate.PassFocus{}, fmt.Errorf("--focus names two areas, %q and %q, and a request names one; ask for the second after the first pass", area, value)
		}
		area = value
	}
	return runstate.NewPassFocus(area, paths)
}

// namesRepositoryPath reports a value that is a relative path to something in
// the checkout. It reads only whether the path exists, never what is in it.
func namesRepositoryPath(repository, value string) bool {
	if repository == "" || filepath.IsAbs(value) {
		return false
	}
	_, err := os.Stat(filepath.Join(repository, filepath.FromSlash(value)))
	return err == nil
}

// describeAskedPass is what the operator is told became of the request: that it
// is recorded and waits for the scheduler, or that it joined one already open.
func describeAskedPass(requests *runstate.PassRequestStore, recorded runstate.PassRequest) string {
	var rendered strings.Builder
	at := recorded.RequestedAt.Local().Format("15:04 MST")
	if recorded.JoinedTo == "" {
		fmt.Fprintf(&rendered, "Asked %s for a pass at %s, recorded as %s.\n", recorded.Agent, at, recorded.ID)
	} else {
		fmt.Fprintf(&rendered, "Asked %s for a pass at %s, recorded as %s. A request was already open (%s), so this one was added to it, with its focus, and the one pass answers both.\n",
			recorded.Agent, at, recorded.ID, recorded.JoinedTo)
		if open, found, err := requests.Open(recorded.Agent); err == nil && found {
			recorded = open
		}
	}
	if recorded.Focus.Empty() {
		fmt.Fprintln(&rendered, "It names no focus, so the pass inspects the next area in the rotation its remit lists.")
	} else {
		fmt.Fprintf(&rendered, "The pass inspects %s, in place of the next area in its rotation.\n", recorded.Focus.Describe())
	}
	fmt.Fprintln(&rendered, "Nothing has run yet: the scheduler takes the request at its next pull, as it takes any pass — the operator's pause holds it, a turn already in flight on the instance's conversation goes first, and a provider answering nobody leaves it waiting with that reason recorded on it. `yoyo sweeps --task "+recorded.Agent+"` shows the pass once it is taken.")
	return rendered.String()
}
