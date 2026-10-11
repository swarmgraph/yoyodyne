package orchestrator

// What a project's own copy of a delivery definition lacks that the build now
// ships, said the way `yoyo config drift` says what the template improved.
//
// A project that keeps its own copy owns the whole sequence and nothing is
// merged between the two (see ProjectDefinitionPath), so a transition the build
// adds reaches such a project only if somebody tells it. This is the telling.
// It is a derivation rather than something each surface works out, for the
// reason the configuration's own drift is: two surfaces comparing the files
// differently is a disagreement only the operator could settle.
//
// There is no recorded baseline for a definition the way there is for the
// configuration's values, so the comparison is two-way, and it is cautious in
// the direction that matters. Only one difference is offered: an outcome the
// build routes and the copy does not route at all, from a state that performs the
// same action in both. A run executing the copy that produces that outcome
// records a divergence, so nothing a project chose is lost by offering it, and
// the copy cannot have sent it somewhere else on purpose because it sends it
// nowhere. Every other difference — a transition routed elsewhere, a state or a
// summary that reads differently — is reported as the project's own and never
// offered, because without a baseline nothing can tell a project's edit from an
// improvement the build made since.

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/workflow"
)

// DefinitionChange is one place a project's copy and the build's definition
// read differently, with both sides so a report can show them rather than assert
// a verdict. An empty side is one that says nothing there.
type DefinitionChange struct {
	// Key names the place in the definition: `states.<state>.on.<outcome>` for a
	// transition, and the field's path for anything else.
	Key   string       `json:"key"`
	Class config.Class `json:"class"`
	Yours string       `json:"yours"`
	Build string       `json:"build"`
}

// Transition reports whether this change is a transition, and which one.
func (c DefinitionChange) Transition() (state, outcome string, ok bool) {
	rest, found := strings.CutPrefix(c.Key, "states.")
	if !found {
		return "", "", false
	}
	state, outcome, found = strings.Cut(rest, ".on.")
	return state, outcome, found
}

// Said is the change in words: a transition as the step and outcome it routes,
// anything else by its key.
func (c DefinitionChange) Said() string {
	if state, outcome, ok := c.Transition(); ok {
		return fmt.Sprintf("%s -> %s", state, outcome)
	}
	return c.Key
}

// DefinitionDrift is the comparison for one delivery definition a project keeps
// its own copy of.
type DefinitionDrift struct {
	// Workflow is the definition compared, by the name it is bound under.
	Workflow string `json:"workflow"`
	// Source is the project's copy, as it was read.
	Source string `json:"source"`
	// Changes is every place the two differ, in key order. A copy level with the
	// build has none.
	Changes []DefinitionChange `json:"changes,omitempty"`
}

// Available is the transitions the build routes and the copy does not.
func (d DefinitionDrift) Available() []DefinitionChange { return d.OfClass(config.ClassAvailable) }

// OfClass is every change with one answer, in key order.
func (d DefinitionDrift) OfClass(class config.Class) []DefinitionChange {
	var matched []DefinitionChange
	for _, change := range d.Changes {
		if change.Class == class {
			matched = append(matched, change)
		}
	}
	return matched
}

// Notice is the line the unprompted surfaces say beside the configuration's own
// notice, and the empty string when the copy lacks nothing the build routes. It
// speaks the available class only, as the configuration's notice does.
func (d DefinitionDrift) Notice() string {
	available := d.Available()
	if len(available) == 0 {
		return ""
	}
	said := make([]string, 0, len(available))
	for _, change := range available {
		said = append(said, change.Said())
	}
	transitions := "transitions"
	if len(available) == 1 {
		transitions = "transition"
	}
	return fmt.Sprintf("note: this build's %s definition routes %d %s this project's own copy at %s does not (%s); a run of the copy that takes one records a divergence, and `yoyo config drift` shows each",
		d.Workflow, len(available), transitions, d.Source, strings.Join(said, ", "))
}

// DeliveryDefinitionDrift compares every delivery definition the project at
// configPath keeps its own copy of against the one this build ships. A project
// that keeps no copy runs the build's own and has nothing to compare, so it
// answers with nothing.
//
// A copy that cannot be read or decoded is an error, naming the file: there is
// nothing to compare, and the run that would execute it refuses it in the same
// words before it claims anything.
func DeliveryDefinitionDrift(configPath string) ([]DefinitionDrift, error) {
	var drifts []DefinitionDrift
	for _, builtin := range builtinDeliveryWorkflows() {
		project, err := deliverySequenceFor(builtin.ID, configPath)
		if err != nil {
			return nil, err
		}
		if !project.Project {
			continue
		}
		drift, err := compareDeliveryDefinitions(builtin, project)
		if err != nil {
			return nil, err
		}
		drifts = append(drifts, drift)
	}
	return drifts, nil
}

// compareDeliveryDefinitions is the comparison itself, over the decoded files so
// that what is compared is what the digest is taken over and nothing of how
// either file was written down.
func compareDeliveryDefinitions(builtin, project deliverySequence) (DefinitionDrift, error) {
	build, err := workflow.Decode(bytes.NewReader(builtin.Definition))
	if err != nil {
		return DefinitionDrift{}, fmt.Errorf("%s: %w", builtin.Source, err)
	}
	yours, err := workflow.Decode(bytes.NewReader(project.Definition))
	if err != nil {
		return DefinitionDrift{}, fmt.Errorf("%s: %w", project.Source, err)
	}
	buildValues, yourValues := definitionValues(build), definitionValues(yours)
	keys := make([]string, 0, len(buildValues)+len(yourValues))
	for key := range buildValues {
		keys = append(keys, key)
	}
	for key := range yourValues {
		if _, shared := buildValues[key]; !shared {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	drift := DefinitionDrift{Workflow: builtin.ID, Source: project.Source}
	for _, key := range keys {
		if buildValues[key] == yourValues[key] {
			continue
		}
		change := DefinitionChange{Key: key, Class: config.ClassYours, Yours: yourValues[key], Build: buildValues[key]}
		if offeredTransition(change, build, yours) {
			change.Class = config.ClassAvailable
		}
		drift.Changes = append(drift.Changes, change)
	}
	return drift, nil
}

// offeredTransition is the one difference offered: an outcome the build routes
// and the copy leaves unrouted, from a state performing the same action in both.
func offeredTransition(change DefinitionChange, build, yours workflow.Definition) bool {
	state, _, ok := change.Transition()
	if !ok || change.Yours != "" || change.Build == "" {
		return false
	}
	theirs, inYours := yours.States[state]
	return inYours && theirs.Action == build.States[state].Action
}

// definitionValues is a definition flattened to one value per place, so two of
// them compare key by key. A terminal is keyed whether or not it carries a
// summary, so one declared on only one side is a difference.
func definitionValues(definition workflow.Definition) map[string]string {
	values := map[string]string{
		"schema":  fmt.Sprint(definition.Schema),
		"id":      definition.ID,
		"summary": definition.Summary,
		"initial": definition.Initial,
	}
	for name, state := range definition.States {
		values["states."+name+".action"] = state.Action
		values["states."+name+".summary"] = state.Summary
		values["states."+name+".gate"] = state.Gate
		for outcome, destination := range state.On {
			values["states."+name+".on."+outcome] = destination
		}
	}
	for name, terminal := range definition.Terminals {
		values["terminals."+name] = "declared"
		values["terminals."+name+".summary"] = terminal.Summary
	}
	return values
}
