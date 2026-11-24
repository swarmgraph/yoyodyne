package cli

// `yoyo epic`: the operator naming the epics the factory works only on, and the
// one way the record of them is written (internal/epicscope). Every act but
// `list` is a person's, refused to a process the harness launched for a role and
// to one it started itself.
//
// What is below an epic is read from the tracker once, when the epic is named.
// Afterwards membership is the record's alone.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// epicCommand is one `yoyo epic` command. The list of paths and the tracker are
// its own so that a test can answer for both.
type epicCommand struct {
	paths   []maystart.PathEntry
	tracker func(configPath string) (itemLister, error)

	stdout, stderr io.Writer
	configPath     string
	jsonOutput     bool
	words          string
	product        domain.ProductID
}

type epicOutput struct {
	Record *epicscope.Record `json:"record,omitempty"`
	// Named and TurnedOn are what start did: gave the epic a new entry, and
	// turned epic mode on.
	Named    bool `json:"named,omitempty"`
	TurnedOn bool `json:"turned_on,omitempty"`
	// NotMembers is each open item below an epic whose members start left as
	// they were, and WorkedOnAgain each other epic that turning epic mode on
	// brought back.
	NotMembers    []string `json:"not_members,omitempty"`
	WorkedOnAgain []string `json:"worked_on_again,omitempty"`
	Error         string   `json:"error,omitempty"`
}

func runEpic(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	command := epicCommand{paths: maystart.Paths(), stdout: stdout, stderr: stderr}
	command.tracker = func(configPath string) (itemLister, error) {
		parts, err := buildComponents(configPath)
		if err != nil {
			return nil, err
		}
		return parts.tracker(), nil
	}
	return command.run(ctx, args)
}

func (c epicCommand) run(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printEpicUsage(c.stdout)
		return 0
	}
	verb := args[0]
	takes := map[string]int{"start": 1, "add": 2, "end": 1, "list": 0, "off": 0}
	count, known := takes[verb]
	if !known {
		fmt.Fprintf(c.stderr, "unknown epic command %q\n\n", verb)
		printEpicUsage(c.stderr)
		return 2
	}
	flags := flag.NewFlagSet("epic "+verb, flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	flags.StringVar(&c.configPath, "config", "", "configuration file path (default: the nearest project configuration)")
	flags.BoolVar(&c.jsonOutput, "json", false, "emit machine-readable JSON")
	if verb == "start" || verb == "end" {
		flags.StringVar(&c.words, "words", "", "what you say with it, kept in the record")
	}
	named, err := parseArguments(flags, args[1:])
	if err != nil {
		return 2
	}
	if len(named) != count {
		fmt.Fprintf(c.stderr, "epic %s takes %d argument(s), and was given %d\n\n", verb, count, len(named))
		printEpicUsage(c.stderr)
		return 2
	}
	if verb != "list" {
		if err := refusedUnlessAPersons("yoyo epic " + verb); err != nil {
			return c.fail(err)
		}
	}
	// Before anything is read, because it holds whatever the record says.
	if verb == "start" {
		if err := refusedUntilWired(c.paths); err != nil {
			return c.fail(err)
		}
	}
	resolved, err := loadConfiguration(c.configPath)
	if err != nil {
		return c.fail(err)
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return c.fail(err)
	}
	c.product = resolved.Config.Product.ID
	if verb == "list" {
		return c.list(ctx, stateRoot)
	}
	operator, err := epicscope.ForOperator(stateRoot, c.product)
	if err != nil {
		return c.fail(err)
	}
	switch verb {
	case "start":
		return c.start(ctx, operator, named[0])
	case "add":
		return c.add(ctx, operator, named[0], named[1])
	case "end":
		return c.end(ctx, operator, named[0])
	default:
		return c.off(ctx, operator)
	}
}

// refusedUnlessAPersons is the refusal of a process the harness launched for a
// role, or started itself.
func refusedUnlessAPersons(verb string) error {
	const act = "a person names the epics the factory works only on"
	if err := refusedToAgentProcess(verb, act); err != nil {
		return err
	}
	if by, started := execution.StartedBy(nil); started {
		return fmt.Errorf("%s is refused from a process the harness started (%s): %s", verb, by, act)
	}
	return nil
}

// refusedUntilWired refuses epic mode while something could start without
// asking whether it may, naming each way it could.
func refusedUntilWired(paths []maystart.PathEntry) error {
	var unwired []string
	for _, path := range paths {
		if !path.Wired {
			unwired = append(unwired, "\n  "+path.Says)
		}
	}
	if len(unwired) == 0 {
		return nil
	}
	return errors.New("epic mode cannot be turned on yet: work could still start outside the epics you name, because each of these starts work without asking whether it may:" + strings.Join(unwired, ""))
}

func (c epicCommand) start(ctx context.Context, operator *epicscope.Operator, id string) int {
	items, err := c.items(ctx)
	if err != nil {
		return c.fail(err)
	}
	epic, found := findItem(items, id)
	if !found {
		return c.fail(fmt.Errorf("the tracker has no work item %s", id))
	}
	titles := c.titlesOf(items)
	below := beads.UnfinishedBelow(id, items)
	naming := epicscope.Naming{Epic: id, From: viaCommandLine, Words: c.words, Description: epic.Description}
	for _, item := range below {
		naming.Members = append(naming.Members, item.ID)
	}
	started, err := operator.Start(naming, time.Now())
	if err != nil {
		return c.refused(titles, err, append(itemIDs(below), id)...)
	}
	output := epicOutput{Record: &started.Record, Named: started.Named, TurnedOn: started.TurnedOn}
	if !started.Named {
		for _, item := range below {
			if !started.Epic.Has(item.ID) {
				output.NotMembers = append(output.NotMembers, item.ID)
			}
		}
	}
	if started.TurnedOn {
		for _, other := range started.Record.OpenEpics() {
			if other.WorkItemID != id {
				output.WorkedOnAgain = append(output.WorkedOnAgain, other.WorkItemID)
			}
		}
	}
	if c.jsonOutput {
		return writeJSON(c.stdout, c.stderr, output)
	}
	if started.Named {
		fmt.Fprintf(c.stdout, "named epic %s; epic mode is on\n", titles.Name(id))
	} else {
		fmt.Fprintf(c.stdout, "epic mode is on again, and epic %s is still open; its members are unchanged\n", titles.Name(id))
		if strings.TrimSpace(c.words) != "" {
			fmt.Fprintln(c.stdout, "the words you gave were not kept, because the epic's entry is unchanged")
		}
	}
	// The members are printed from what was recorded, so the two cannot differ.
	fmt.Fprintf(c.stdout, "%d member(s), the only items that may start in it:\n", len(started.Epic.Members))
	for _, member := range started.Epic.Members {
		fmt.Fprintf(c.stdout, "  %s\n", titles.Name(member.WorkItemID))
	}
	if len(output.NotMembers) > 0 {
		fmt.Fprintln(c.stdout, "open items below it that are not members, and the command that adds each:")
		for _, item := range output.NotMembers {
			if holder, held := started.Record.Holding(item); held {
				fmt.Fprintf(c.stdout, "  %s: in epic %s, and can be added once that has ended: yoyo epic add %s %s\n", titles.Name(item), titles.Name(holder.WorkItemID), id, item)
				continue
			}
			fmt.Fprintf(c.stdout, "  %s: yoyo epic add %s %s\n", titles.Name(item), id, item)
		}
	}
	if len(output.WorkedOnAgain) > 0 {
		fmt.Fprintln(c.stdout, "also worked on again, and the command that ends each:")
		for _, other := range output.WorkedOnAgain {
			fmt.Fprintf(c.stdout, "  %s: yoyo epic end %s\n", titles.Name(other), other)
		}
	}
	return 0
}

func (c epicCommand) add(ctx context.Context, operator *epicscope.Operator, epic, item string) int {
	// The record is the operator's word for exactly this item, so a name the
	// tracker does not have is refused rather than kept as a member that can
	// never start.
	items, err := c.items(ctx)
	if err != nil {
		return c.fail(err)
	}
	if _, found := findItem(items, item); !found {
		return c.fail(fmt.Errorf("the tracker has no work item %s", item))
	}
	titles := c.titlesOf(items)
	record, err := operator.Add(epic, item, time.Now())
	if err != nil {
		return c.refused(titles, err, epic, item)
	}
	if c.jsonOutput {
		return writeJSON(c.stdout, c.stderr, epicOutput{Record: &record})
	}
	fmt.Fprintf(c.stdout, "%s is a member of epic %s\n", titles.Name(item), titles.Name(epic))
	if !record.On() {
		fmt.Fprintln(c.stdout, "epic mode is off, so this changes nothing that starts until `yoyo epic start` turns it on")
	}
	return 0
}

func (c epicCommand) end(ctx context.Context, operator *epicscope.Operator, epic string) int {
	record, err := operator.End(epic, c.words, time.Now())
	if err != nil {
		return c.refused(c.titles(ctx), err, epic)
	}
	if c.jsonOutput {
		return writeJSON(c.stdout, c.stderr, epicOutput{Record: &record})
	}
	fmt.Fprintf(c.stdout, "ended epic %s; its record is kept\n", c.titles(ctx).Name(epic))
	if record.On() {
		fmt.Fprintf(c.stdout, "epic mode is still on, with %d epic(s) open; `yoyo epic off` returns the factory to goal mode\n", len(record.OpenEpics()))
	}
	return 0
}

func (c epicCommand) off(ctx context.Context, operator *epicscope.Operator) int {
	record, err := operator.TurnOff()
	if err != nil {
		return c.fail(err)
	}
	if c.jsonOutput {
		return writeJSON(c.stdout, c.stderr, epicOutput{Record: &record})
	}
	fmt.Fprintln(c.stdout, "epic mode is off; the factory works in goal mode")
	if open := record.OpenEpics(); len(open) > 0 {
		fmt.Fprintln(c.stdout, "epics still open, and the command that ends each:")
		titles := c.titles(ctx)
		for _, epic := range open {
			fmt.Fprintf(c.stdout, "  %s: yoyo epic end %s\n", titles.Name(epic.WorkItemID), epic.WorkItemID)
		}
	}
	return 0
}

func (c epicCommand) list(ctx context.Context, stateRoot string) int {
	record, err := epicscope.Read(stateRoot, c.product)
	if err != nil {
		return c.fail(err)
	}
	if c.jsonOutput {
		return writeJSON(c.stdout, c.stderr, epicOutput{Record: &record})
	}
	if record.On() {
		fmt.Fprintf(c.stdout, "epic mode is on, with %d epic(s) open\n", len(record.OpenEpics()))
	} else {
		fmt.Fprintln(c.stdout, "epic mode is off; the factory works in goal mode")
	}
	if len(record.Epics) == 0 {
		fmt.Fprintln(c.stdout, "no epic has been named")
		return 0
	}
	titles := c.titles(ctx)
	for _, epic := range record.Epics {
		state := "open"
		if !epic.Open() {
			state = "ended at " + localTime(epic.Ended.At)
		}
		fmt.Fprintf(c.stdout, "\n%s: %s; named at %s from %s\n", titles.Name(epic.WorkItemID), state, localTime(epic.NamedAt), epic.NamedFrom)
		if epic.Words != "" {
			printIndented(c.stdout, "said when naming it:", titles.Cite(epic.Words))
		}
		if !epic.Open() && epic.Ended.Words != "" {
			printIndented(c.stdout, "said when ending it:", titles.Cite(epic.Ended.Words))
		}
		fmt.Fprintf(c.stdout, "  %d member(s):\n", len(epic.Members))
		for _, member := range epic.Members {
			how := "on the list when the epic was named"
			if member.Act == epicscope.ActAdd {
				how = "added at " + localTime(member.AddedAt)
			}
			fmt.Fprintf(c.stdout, "    %s, %s\n", titles.Name(member.WorkItemID), how)
		}
		if strings.TrimSpace(epic.Description) != "" {
			printIndented(c.stdout, "its description when it was named:", titles.Cite(epic.Description))
		}
	}
	return 0
}

func printIndented(writer io.Writer, heading, text string) {
	fmt.Fprintf(writer, "  %s\n    %s\n", heading, strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n    "))
}

// items is every work item of the tracker, closed ones included.
func (c epicCommand) items(ctx context.Context) ([]beads.WorkItem, error) {
	tracker, err := c.tracker(c.configPath)
	if err != nil {
		return nil, err
	}
	listed, err := tracker.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("read the tracker: %w", err)
	}
	return listed, nil
}

func findItem(items []beads.WorkItem, id string) (beads.WorkItem, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return beads.WorkItem{}, false
}

func itemIDs(items []beads.WorkItem) []string {
	var ids []string
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func (c epicCommand) titlesOf(items []beads.WorkItem) *readmodel.WorkItemTitles {
	return readmodel.NewWorkItemTitles(items, string(c.product))
}

// titles is for a command that needs the tracker only to name work items. One
// that cannot be read stops nothing: each item is then said with that beside it.
func (c epicCommand) titles(ctx context.Context) *readmodel.WorkItemTitles {
	ctx, cancel := context.WithTimeout(ctx, trackerCommandTimeout)
	defer cancel()
	items, err := c.items(ctx)
	if err != nil {
		return readmodel.UnreadableWorkItemTitles(time.Now(), string(c.product))
	}
	return c.titlesOf(items)
}

// refused is an act the record refused, with each work item the refusal names
// said as the tracker has it.
func (c epicCommand) refused(titles *readmodel.WorkItemTitles, err error, ids ...string) int {
	return c.fail(errors.New(titles.CiteKnowing(err.Error(), ids...)))
}

func (c epicCommand) fail(err error) int {
	if c.jsonOutput {
		if code := writeJSON(c.stdout, c.stderr, epicOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(c.stderr, err)
	return 1
}

func printEpicUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo epic <start|add|end|list|off> [options]

Name the epics the factory works only on. While epic mode is on, only the members
of an open epic are to start. The members are the list recorded when the epic was
named and what you add to it: nothing is worked out from the tracker afterwards,
and the epic itself is not one of them.

"start" is refused until every way the harness starts work asks whether it may
start, and the refusal names each one that does not yet.

  start <item>       name an epic, with every open item below it as its members,
                     and turn epic mode on; an epic that ended is named again.
                     On an epic still open while epic mode is off, turn epic mode
                     back on and leave its members as they are
  add <epic> <item>  add an item to an open epic's members
  end <epic>         end an open epic; its record is kept, and epic mode stays on
  list               show the record
  off                return the factory to goal mode; no epic is ended

"start" and "add" refuse an item another open epic has, and name that epic.

All but "list" are a person's. A process the harness launched for a role, or
started itself, is refused them and told so.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON

start and end options:
  --words <text>    what you say with it, kept in the record`)
}
