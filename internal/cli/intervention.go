package cli

// The operator's hand steps, as events (see internal/intervention).
//
// Two things live here. The verbs that carry out a hand step — `yoyo run`, the
// triage verbs, `yoyo reconcile`, `yoyo amendment approve` and `decline`, and
// `yoyo directive record` — each write one event as they do it, through
// noteHandStep. And `yoyo intervention` is how a step taken outside the harness
// is written down afterwards, and how the record is read back with the count per
// merged change beside it.
//
// A verb records nothing when an agent's process or the harness itself ran it:
// either is the system doing its own work, and counting it would make the
// measure say the operator did what the system did.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/intervention"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// viaCommandLine is where a step typed at a terminal came in.
const viaCommandLine = "the command line"

// handStep is one step a verb carried out on a person's word, as the verb knows
// it.
type handStep struct {
	kind    intervention.Kind
	items   []string
	run     string
	subject string
	said    string
}

// noteHandStepFor records a step against the product the configuration names.
// It is for a verb that has not built the product's parts; one that has calls
// noteHandStep with the store it already holds.
func noteHandStepFor(configPath string, stderr io.Writer, step handStep) {
	if !personAtCommandLine() {
		return
	}
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		unrecordedHandStep(stderr, step, err)
		return
	}
	store, err := interventionStoreOf(resolved)
	if err != nil {
		unrecordedHandStep(stderr, step, err)
		return
	}
	noteHandStep(store, resolved.Config.Product.ID, stderr, step)
}

// noteHandStep records one step a person took at this command line. A step
// that could not be recorded is said on stderr and changes nothing about the
// verb: the step was taken either way, and failing the verb over its record
// would undo nothing.
func noteHandStep(store *runstate.InterventionStore, productID domain.ProductID, stderr io.Writer, step handStep) {
	if store == nil || !personAtCommandLine() {
		return
	}
	id, err := intervention.NewID()
	if err != nil {
		unrecordedHandStep(stderr, step, err)
		return
	}
	now := time.Now().UTC()
	event := intervention.Event{
		SchemaVersion: intervention.SchemaVersion,
		ID:            id,
		ProductID:     productID,
		Kind:          step.kind,
		At:            now,
		Items:         nonEmpty(step.items),
		Run:           strings.TrimSpace(step.run),
		Subject:       strings.TrimSpace(step.subject),
		Said:          strings.TrimSpace(step.said),
		Via:           viaCommandLine,
		RecordedAt:    now,
	}
	if err := store.Record(event); err != nil {
		unrecordedHandStep(stderr, step, err)
	}
}

// personAtCommandLine reports a process a person started rather than one an
// agent's run or the harness's own schedule did.
func personAtCommandLine() bool {
	if _, launched := execution.LaunchedForRole(nil); launched {
		return false
	}
	if _, started := execution.StartedBy(nil); started {
		return false
	}
	return true
}

func unrecordedHandStep(stderr io.Writer, step handStep, err error) {
	fmt.Fprintf(stderr, "this hand step (%s) was taken but could not be recorded as one: %v\n", step.kind.Describe(), err)
}

func nonEmpty(values []string) []string {
	var kept []string
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return kept
}

type interventionOutput struct {
	Interventions []intervention.Event         `json:"interventions,omitempty"`
	Count         *readmodel.InterventionCount `json:"count,omitempty"`
	Error         string                       `json:"error,omitempty"`
}

func runIntervention(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printInterventionUsage(stdout)
		return 0
	}
	switch args[0] {
	case "record":
		return recordIntervention(args[1:], stdout, stderr)
	case "list":
		return listInterventions(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown intervention command %q\n\n", args[0])
		printInterventionUsage(stderr)
		return 2
	}
}

// recordIntervention writes down a step taken outside the harness. It is a
// person's account of a person's step, so it is refused to an agent's process
// as the other verbs that record a person's act are; a role that noticed one
// records it from its own conversation instead, under its own name.
func recordIntervention(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("intervention record", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	kind := flags.String("kind", string(intervention.KindOther), "what sort of step: "+strings.Join(intervention.KindNames(), ", "))
	by := flags.String("by", "", "who took the step; required")
	recordedBy := flags.String("recorded-by", "", "who is writing it down (default: whoever took it)")
	items := flags.String("item", "", "comma-separated work items the step touched")
	run := flags.String("run", "", "the run the step touched")
	subject := flags.String("subject", "", "anything else it touched: a part of the product, a branch, a directive")
	at := flags.String("at", "", "when the step was taken, as 2026-10-05T09:30:00-07:00 (default: now)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "intervention record requires exactly one argument: what was done")
		printInterventionUsage(stderr)
		return 2
	}
	if refusal := refusedToAgentProcess("yoyo intervention record", "it is a person's account of a step a person took by hand"); refusal != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, refusal)
	}
	if strings.TrimSpace(*by) == "" {
		return reportInterventionError(stdout, stderr, *jsonOutput,
			errors.New("say who took the step, as --by; a command line does not say who typed at it, and the record is who did it"))
	}
	now := time.Now().UTC()
	when := now
	if strings.TrimSpace(*at) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*at))
		if err != nil {
			return reportInterventionError(stdout, stderr, *jsonOutput,
				fmt.Errorf("--at %q is not a time; give one as 2026-10-05T09:30:00-07:00", *at))
		}
		when = parsed.UTC()
	}
	recorder := strings.TrimSpace(*recordedBy)
	if recorder == "" {
		recorder = strings.TrimSpace(*by)
	}
	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	store, err := interventionStoreOf(resolved)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	id, err := intervention.NewID()
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	event := intervention.Event{
		SchemaVersion: intervention.SchemaVersion,
		ID:            id,
		ProductID:     resolved.Config.Product.ID,
		Kind:          intervention.Kind(strings.TrimSpace(*kind)),
		At:            when,
		Items:         splitList(*items),
		Run:           strings.TrimSpace(*run),
		Subject:       strings.TrimSpace(*subject),
		Said:          strings.TrimSpace(positional[0]),
		Observed:      true,
		By:            strings.TrimSpace(*by),
		RecordedBy:    recorder,
		RecordedAt:    now,
	}
	if err := store.Record(event); err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, interventionOutput{Interventions: []intervention.Event{event}})
	}
	fmt.Fprint(stdout, event.Render(time.Local))
	fmt.Fprintf(stdout, "recorded as %s, a step taken outside the harness and counted from now on\n", event.ID)
	return 0
}

// listInterventions reads the record back, with the count per merged change
// the read model derives from it.
func listInterventions(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("intervention list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "intervention list does not accept positional arguments")
		return 2
	}
	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	store, err := runstate.NewInterventionStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	events, err := store.List()
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	runs, err := runstate.NewStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	merged, err := runs.Promoted()
	if err != nil {
		return reportInterventionError(stdout, stderr, *jsonOutput, err)
	}
	count := readmodel.CountInterventions(events, merged)
	if *jsonOutput {
		return writeJSON(stdout, stderr, interventionOutput{Interventions: events, Count: &count})
	}
	if len(events) == 0 {
		fmt.Fprintln(stdout, "no hand steps are recorded for this product")
	}
	for _, event := range events {
		fmt.Fprint(stdout, event.Render(time.Local))
	}
	fmt.Fprintln(stdout, renderInterventionCount(count))
	return 0
}

// renderInterventionCount is the count in one sentence, floor included.
func renderInterventionCount(count readmodel.InterventionCount) string {
	if count.Merged == 0 {
		return fmt.Sprintf("no change has been merged yet, so there is nothing to count %d hand step(s) against; the count is %s", count.Attributed+count.Unattributed, count.Floor)
	}
	return fmt.Sprintf("%d hand step(s) named %d merged change(s): %.2f per change, and %d named no merged change; the count is %s",
		count.Attributed, count.Merged, count.PerChange, count.Unattributed, count.Floor)
}

func interventionStoreOf(resolved config.Resolved) (*runstate.InterventionStore, error) {
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, err
	}
	return runstate.NewInterventionStore(stateRoot, resolved.Config.Product.ID)
}

func reportInterventionError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, interventionOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printInterventionUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo intervention <record|list> [options]

A hand step is something the operator did by hand to keep the work moving. Every
one taken through the harness is recorded as it happens: running an item by name,
a re-run, repair, resume, re-arm, or cap override started by hand, stopping a run,
settling or reconciling by hand, approving or declining a proposal or a proposed
change to a document, and recording a directive. Nothing records a step taken
by an agent's run or by the harness's own schedule.

A step taken outside the harness — restarting the scheduler, resetting a branch,
editing the tracker — is recorded only if somebody writes it down, which is what
"record" is for. It is marked as observed, with who took it and who wrote it
down. A program manager that notices one records it from its own conversation.

"list" shows every recorded step and how many named each merged change. That
count is a floor: a step nobody recorded is not in it.

  record --by <who> [options] <what was done>
  list

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON

record options:
  --kind <kind>         restart, reset, tracker, other (the default), or any
                        step the harness also records, such as stop
  --by <who>            who took the step; required
  --recorded-by <who>   who is writing it down (default: whoever took it)
  --item <items>        comma-separated work items the step touched
  --run <run-id>        the run the step touched
  --subject <what>      anything else it touched: a part, a branch, a directive
  --at <time>           when it was taken, as 2026-10-05T09:30:00-07:00 (default: now)`)
}
