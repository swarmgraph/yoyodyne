package readmodel

// Which mode the factory is in and, in epic mode, the epics the operator named:
// how many of each one's members are done, running, ready and waiting, what is
// held out, and each problem in the words the operator reads.
//
// Membership is the recorded list (epicscope) and nothing else. The tracker's
// parent links are read for one thing only, which epic an `yoyo epic add`
// command names where more than one is open.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
)

// maxEpicReasonBytes bounds why a record could not be read, which names the file.
const maxEpicReasonBytes = 512

// EpicRecords is the two records the epic lines are read from.
type EpicRecords interface {
	// Named is the epics the operator named; it names none where there is no
	// record.
	Named() (epicscope.Record, error)
	// NotStarted is what was refused a start.
	NotStarted() (maystart.Refusals, error)
	// MayStart is the one decision on a start, with nothing kept.
	MayStart(start maystart.Start) maystart.Decision
}

type epicRecords struct {
	stateRoot string
	product   domain.ProductID
}

// NewEpicRecords reads one product's records under a state root.
func NewEpicRecords(stateRoot string, product domain.ProductID) EpicRecords {
	return epicRecords{stateRoot, product}
}

func (e epicRecords) Named() (epicscope.Record, error) {
	return epicscope.Read(e.stateRoot, e.product)
}

func (e epicRecords) NotStarted() (maystart.Refusals, error) {
	return maystart.ReadRefusals(e.stateRoot, e.product)
}

func (e epicRecords) MayStart(start maystart.Start) maystart.Decision {
	return maystart.Decide(e.stateRoot, e.product, start)
}

// Epics is the mode and the named epics as of one reading.
type Epics struct {
	// Mode is empty where the record of named epics could not be read.
	Mode epicscope.Mode `json:"mode,omitempty"`
	// WaysNotAsking is how many ways of starting work do not ask about the named
	// epics yet, so epic mode does not hold them out.
	WaysNotAsking int        `json:"ways_not_asking,omitempty"`
	Open          []OpenEpic `json:"open,omitempty"`
	// ItemsHeldOut is how many ready items are not a member of an open epic, and
	// ItemsHeldOutSince when the first of them was refused a start, where one was.
	ItemsHeldOut      int        `json:"items_held_out"`
	ItemsHeldOutSince *time.Time `json:"items_held_out_since,omitempty"`
	// PassesNotStarted is how many role passes were refused, and
	// PassesNotStartedSince when the first of them was.
	PassesNotStarted      int        `json:"passes_not_started"`
	PassesNotStartedSince *time.Time `json:"passes_not_started_since,omitempty"`
	// Refused is each start that was refused and not made since, one for each
	// item and way of starting it.
	Refused []maystart.RefusedItem `json:"refused,omitempty"`
	// RefusedDocuments is each document whose run was refused and has not started
	// since.
	RefusedDocuments []maystart.RefusedDocument `json:"refused_documents,omitempty"`
	// RefusedUpdates is each target branch whose update from the team's branch was
	// refused and still may not start.
	RefusedUpdates []maystart.RefusedUpdate `json:"refused_updates,omitempty"`
	// Problems is each problem in full, one line each: the runs that stopped,
	// then the members that wait, then the starts that were refused.
	Problems []string `json:"problems,omitempty"`
	// FactoryWide is whether each kind of work that is allowed or not for the
	// whole factory may start now.
	FactoryWide []FactoryWideStart `json:"factory_wide,omitempty"`
	// ItemsProblem is why the items could not be counted, and NotStartedProblem
	// why what was refused could not be read.
	ItemsProblem      string `json:"items_problem,omitempty"`
	NotStartedProblem string `json:"not_started_problem,omitempty"`
}

// FactoryWideStart is whether one kind of work may start, and why not.
type FactoryWideStart struct {
	Path     maystart.Path `json:"path"`
	MayStart bool          `json:"may_start"`
	Why      string        `json:"why,omitempty"`
}

// factoryWide asks the one decision about what nothing else is kept of.
func factoryWide(records EpicRecords) []FactoryWideStart {
	var starts []FactoryWideStart
	for _, start := range []maystart.Start{maystart.GoalReview(), maystart.MessageReading()} {
		decision := records.MayStart(start)
		starts = append(starts, FactoryWideStart{Path: start.Path(), MayStart: decision.MayStart(), Why: singleLine(decision.Why, maxEpicReasonBytes)})
	}
	return starts
}

// renderFactoryWide says each in its own line.
func (e Epics) renderFactoryWide() string {
	var rendered strings.Builder
	for _, start := range e.FactoryWide {
		says := string(start.Path)
		switch start.Path {
		case maystart.UnmetGoalReview:
			says = "The review of unmet goals"
		case maystart.MessagesForWork:
			says = "Reading outside messages for work"
		}
		if start.MayStart {
			fmt.Fprintf(&rendered, "  %s: may start\n", says)
		} else {
			fmt.Fprintf(&rendered, "  %s: may not start; %s\n", says, start.Why)
		}
	}
	return rendered.String()
}

// OpenEpic is one epic that has not ended. Every member is counted once.
type OpenEpic struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
	Done       int    `json:"done"`
	Running    int    `json:"running"`
	Ready      int    `json:"ready"`
	Waiting    int    `json:"waiting"`
}

// readEpics reads the epic lines from the records, the runs and the queue the
// standing already read, and the one tracker listing.
func readEpics(sources Sources, standing Standing, listed []beads.WorkItem, listedErr error) *Epics {
	if sources.Epics == nil {
		return nil
	}
	record, err := sources.Epics.Named()
	if err != nil {
		epics := &Epics{Problems: []string{fmt.Sprintf("Every start that asks about the epics you named is refused: the record of them could not be read (%s).",
			singleLine(err.Error(), maxEpicReasonBytes))}}
		epics.FactoryWide = factoryWide(sources.Epics)
		facts := epicFacts{items: make(map[string]beads.WorkItem, len(listed)), readable: sources.Tracker != nil && listedErr == nil, running: map[string]bool{}}
		for _, item := range listed {
			facts.items[item.ID] = item
		}
		refusals, err := sources.Epics.NotStarted()
		if err != nil {
			epics.NotStartedProblem = singleLine(err.Error(), maxEpicReasonBytes)
		}
		for _, run := range standing.Running {
			facts.running[run.WorkItemID] = true
		}
		epics.refuse(facts, refusals, nil)
		return epics
	}
	if len(record.Epics) == 0 {
		return nil
	}
	epics := &Epics{Mode: record.Mode}
	if !record.On() {
		return epics
	}
	epics.FactoryWide = factoryWide(sources.Epics)
	for _, path := range maystart.Paths() {
		if !path.Wired {
			epics.WaysNotAsking++
		}
	}
	for _, problem := range []string{standing.RunningProblem, standing.NotStartableProblem} {
		epics.ItemsProblem = joinProblems(epics.ItemsProblem, problem)
	}
	switch {
	case sources.Tracker == nil:
		listedErr = errors.New("nothing was wired to read the tracker")
		fallthrough
	case listedErr != nil:
		epics.ItemsProblem = joinProblems(epics.ItemsProblem, fmt.Sprintf("the tracker could not be listed: %v", listedErr))
	}
	facts := epicFacts{
		record:   record,
		items:    make(map[string]beads.WorkItem, len(listed)),
		readable: listedErr == nil,
		running:  map[string]bool{},
		ready:    map[string]bool{},
	}
	for _, item := range listed {
		facts.items[item.ID] = item
	}
	for _, run := range standing.Running {
		if !run.waitingForEpic {
			facts.running[run.WorkItemID] = true
		}
	}
	// What the queue holds ready, whether or not something stops all starts now.
	for _, item := range standing.StartableItems {
		facts.ready[item.WorkItemID] = true
	}
	for _, refused := range standing.NotStartable {
		if refused.Kind == backlog.HeldByStall {
			facts.ready[refused.WorkItemID] = true
		}
	}
	refusals, err := sources.Epics.NotStarted()
	if err != nil {
		refusals = maystart.Refusals{}
		epics.NotStartedProblem = singleLine(err.Error(), maxEpicReasonBytes)
	}

	for _, epic := range record.Epics {
		if epic.Open() {
			epics.Open = append(epics.Open, facts.count(epic))
		}
	}
	refusedSince := map[string]time.Time{}
	for _, refused := range refusals.Items {
		if since, known := refusedSince[refused.WorkItemID]; !known || refused.Since.Before(since) {
			refusedSince[refused.WorkItemID] = refused.Since
		}
	}
	for id := range facts.ready {
		// An epic is carried out by its members and is not work of its own.
		if facts.inside(id) || facts.isOpenEpic(id) {
			continue
		}
		epics.ItemsHeldOut++
		if since, refused := refusedSince[id]; refused {
			epics.ItemsHeldOutSince = earliest(epics.ItemsHeldOutSince, since)
		}
	}
	for _, passes := range refusals.Passes {
		epics.PassesNotStarted += passes.Count
		epics.PassesNotStartedSince = earliest(epics.PassesNotStartedSince, passes.Since)
	}

	stopped := map[string]bool{}
	for _, runs := range [][]RunningRun{standing.Running, standing.PausedRuns} {
		for _, run := range runs {
			if run.waitingForEpic && !facts.inside(run.WorkItemID) {
				stopped[run.WorkItemID] = true
				epics.Problems = append(epics.Problems, fmt.Sprintf("%s has stopped before its next step because it is not in an epic you named. Its work is kept.",
					facts.name(run.WorkItemID, run.Title)))
			}
		}
	}
	epics.Problems = append(epics.Problems, facts.waitsOutside()...)
	epics.refuse(facts, refusals, stopped)
	return epics
}

// refuse says each refused start, one line for each document, target branch and item. The run that stopped
// is already said, and an item that is running, is finished or has joined since
// was not left unstarted.
func (e *Epics) refuse(facts epicFacts, refusals maystart.Refusals, stopped map[string]bool) {
	says := map[maystart.Path]string{}
	for _, path := range maystart.Paths() {
		says[path.Path] = path.Says
	}
	for _, refused := range refusals.Documents {
		if slices.ContainsFunc(refused.Answers, func(id string) bool { return facts.inside(id) || facts.isOpenEpic(id) }) {
			continue
		}
		e.RefusedDocuments = append(e.RefusedDocuments, refused)
		e.Problems = append(e.Problems, facts.refusedDocument(refused, says))
	}
	// Any open epic lets an update start, at the next check that finds it due.
	if !slices.ContainsFunc(facts.record.Epics, epicscope.Epic.Open) {
		for _, refused := range refusals.Updates {
			way := says[refused.Path]
			if way == "" {
				way = string(refused.Path)
			}
			e.RefusedUpdates = append(e.RefusedUpdates, refused)
			e.Problems = append(e.Problems, fmt.Sprintf("Not started: %s, for %s (refused since %s, last at %s: %s).", way, refused.TargetBranch,
				localMoment(refused.Since), localMoment(refused.Last), singleLine(refused.Why, maxEpicReasonBytes)))
		}
	}
	var order []string
	byItem := map[string][]maystart.RefusedItem{}
	for _, refused := range refusals.Items {
		id := refused.WorkItemID
		if stopped[id] || facts.running[id] || facts.inside(id) || facts.isOpenEpic(id) || facts.done(id) || facts.gone(id) {
			continue
		}
		if byItem[id] == nil {
			order = append(order, id)
		}
		byItem[id] = append(byItem[id], refused)
		e.Refused = append(e.Refused, refused)
	}
	for _, id := range order {
		var ways []string
		for _, refused := range byItem[id] {
			way := says[refused.Path]
			if way == "" {
				way = string(refused.Path)
			}
			ways = append(ways, fmt.Sprintf("%s (refused since %s, last at %s: %s)", way, localMoment(refused.Since), localMoment(refused.Last),
				singleLine(strings.ReplaceAll(refused.Why, id, "it"), maxEpicReasonBytes)))
		}
		e.Problems = append(e.Problems, fmt.Sprintf("Not started: %s, by %s.%s", facts.name(id, ""), strings.Join(ways, "; "), facts.toAdd(id)))
	}
}

// refusedDocument is the line for a refused document: which, why, since when,
// and what it answers with the commands that would let it start.
func (f epicFacts) refusedDocument(refused maystart.RefusedDocument, says map[maystart.Path]string) string {
	way := says[refused.Path]
	if way == "" {
		way = string(refused.Path)
	}
	title := singleLine(refused.Title, maxCitedTitleBytes)
	if title == "" {
		title = "a document with no title recorded"
	}
	line := fmt.Sprintf("Not started: %s (%s, %s), by %s (refused since %s, last at %s: %s).", title, refused.DocumentID, refused.WriteID, way,
		localMoment(refused.Since), localMoment(refused.Last), singleLine(refused.Why, maxEpicReasonBytes))
	var names, commands []string
	for _, id := range refused.Answers {
		names = append(names, f.name(id, ""))
		for _, command := range f.addCommands(id) {
			if !slices.Contains(commands, command) {
				commands = append(commands, command)
			}
		}
	}
	if len(names) > 0 {
		line += " It answers " + strings.Join(names, ", ") + "."
	}
	if len(commands) > 0 {
		line += " To let it start: " + strings.Join(commands, ", or ")
	}
	return line
}

// epicFacts is what the epic lines are worked out from.
type epicFacts struct {
	record   epicscope.Record
	items    map[string]beads.WorkItem
	readable bool
	running  map[string]bool
	ready    map[string]bool
}

func (f epicFacts) inside(id string) bool {
	_, held := f.record.Holding(id)
	return held
}

func (f epicFacts) isOpenEpic(id string) bool {
	return slices.ContainsFunc(f.record.Epics, func(epic epicscope.Epic) bool { return epic.Open() && epic.WorkItemID == id })
}

func (f epicFacts) done(id string) bool { return f.items[id].Status == trackerClosed }

// gone is an item a tracker that was read does not hold.
func (f epicFacts) gone(id string) bool {
	_, held := f.items[id]
	return f.readable && !held
}

func (f epicFacts) count(epic epicscope.Epic) OpenEpic {
	counted := OpenEpic{WorkItemID: epic.WorkItemID, Title: f.title(epic.WorkItemID, "")}
	for _, member := range epic.Members {
		switch id := member.WorkItemID; {
		case f.done(id):
			counted.Done++
		case f.running[id]:
			counted.Running++
		case f.ready[id]:
			counted.Ready++
		default:
			counted.Waiting++
		}
	}
	return counted
}

// waitsOutside is each member that has not started and waits for an unfinished
// item in no open epic.
func (f epicFacts) waitsOutside() []string {
	unfinished := make(map[string]struct{}, len(f.items))
	for id := range f.items {
		if !f.done(id) {
			unfinished[id] = struct{}{}
		}
	}
	var says []string
	for _, epic := range f.record.Epics {
		if !epic.Open() {
			continue
		}
		for _, member := range epic.Members {
			id := member.WorkItemID
			if f.done(id) || f.running[id] {
				continue
			}
			for _, on := range f.items[id].WaitingOn(unfinished) {
				if f.done(on) || f.inside(on) || f.isOpenEpic(on) {
					continue
				}
				says = append(says, fmt.Sprintf("%s waits for %s, which is not in an epic you named. To add it: yoyo epic add %s %s",
					f.name(id, ""), f.name(on, ""), epic.WorkItemID, on))
			}
		}
	}
	return says
}

// toAdd is the commands that add an item, in words, and nothing where none would.
func (f epicFacts) toAdd(id string) string {
	commands := f.addCommands(id)
	if len(commands) == 0 {
		return ""
	}
	return " To add it: " + strings.Join(commands, ", or ")
}

// addCommands is the command that adds an item: to the open epic it is below in
// the tracker, and otherwise to any of the open ones. With none open there is no
// command that would.
func (f epicFacts) addCommands(id string) []string {
	seen := map[string]bool{}
	for above := id; above != "" && !seen[above]; above = f.items[above].DecomposedFrom() {
		seen[above] = true
		if f.isOpenEpic(above) {
			return []string{fmt.Sprintf("yoyo epic add %s %s", above, id)}
		}
	}
	var commands []string
	for _, epic := range f.record.Epics {
		if epic.Open() {
			commands = append(commands, fmt.Sprintf("yoyo epic add %s %s", epic.WorkItemID, id))
		}
	}
	return commands
}

// title is the item's title, or that it has none here: an item is never shown
// as a bare identifier.
func (f epicFacts) title(id, recorded string) string {
	for _, title := range []string{f.items[id].Title, recorded} {
		if said := singleLine(title, maxCitedTitleBytes); said != "" {
			return said
		}
	}
	switch {
	case f.gone(id):
		return notInTracker
	case !f.readable:
		return "its title could not be read"
	default:
		return untitledWorkItem
	}
}

// name is "TITLE (ID)".
func (f epicFacts) name(id, recorded string) string {
	return f.title(id, recorded) + " (" + id + ")"
}

func earliest(current *time.Time, other time.Time) *time.Time {
	if current != nil && !other.Before(*current) {
		return current
	}
	return &other
}

// RenderEpics is the mode and what it holds out, printed under the four lines,
// and nothing for a product that never named an epic. Its words carry commands,
// so work items are named in them already and are not cited again.
func (s Standing) RenderEpics() string {
	if s.Epics == nil {
		return ""
	}
	e := s.Epics
	var rendered strings.Builder
	switch e.Mode {
	case epicscope.ModeEpic:
		if e.WaysNotAsking == 0 {
			rendered.WriteString("Mode: epic mode; only work in the epics you named starts\n")
		} else {
			fmt.Fprintf(&rendered, "Mode: epic mode; a start that asks about the epics you named goes ahead only for work in them, and %s of starting work do not ask yet\n", count(e.WaysNotAsking, "way"))
		}
	case epicscope.ModeGoal:
		rendered.WriteString("Mode: goal mode; epic mode is off\n")
	default:
		rendered.WriteString("Mode: could not be read\n")
	}
	if e.Mode == epicscope.ModeEpic {
		if len(e.Open) == 0 {
			rendered.WriteString("  Every epic you named has ended.\n")
		}
		for _, epic := range e.Open {
			fmt.Fprintf(&rendered, "  Open epic: %s (%s)", epic.Title, epic.WorkItemID)
			if e.ItemsProblem == "" {
				fmt.Fprintf(&rendered, " — %s: %d done, %d running, %d ready, %d waiting",
					count(epic.Done+epic.Running+epic.Ready+epic.Waiting, "member"), epic.Done, epic.Running, epic.Ready, epic.Waiting)
			}
			rendered.WriteString("\n")
		}
		if e.ItemsProblem == "" {
			fmt.Fprintf(&rendered, "  Ready items held out: %d%s\n", e.ItemsHeldOut, sinceSaid(e.ItemsHeldOutSince))
		} else {
			fmt.Fprintf(&rendered, "%sthe members and the items held out could not be counted: %s\n", partialRead, e.ItemsProblem)
		}
		if e.NotStartedProblem == "" {
			fmt.Fprintf(&rendered, "  Role passes not started: %d%s\n", e.PassesNotStarted, sinceSaid(e.PassesNotStartedSince))
		} else {
			fmt.Fprintf(&rendered, "%swhat was not started could not be read: %s\n", partialRead, e.NotStartedProblem)
		}
	}
	if e.Mode == epicscope.ModeEpic {
		rendered.WriteString(e.renderFactoryWide())
	}
	listed, further := bound(len(e.Problems))
	if e.Mode == "" && e.NotStartedProblem != "" {
		fmt.Fprintf(&rendered, "%swhat was not started could not be read: %s\n", partialRead, e.NotStartedProblem)
	}
	for _, problem := range e.Problems[:listed] {
		fmt.Fprintf(&rendered, "  %s\n", problem)
	}
	rendered.WriteString(remainder(further, "problem"))
	return s.wording.Render(rendered.String())
}

func sinceSaid(since *time.Time) string {
	if since == nil {
		return ""
	}
	return ", since " + localMoment(*since)
}
