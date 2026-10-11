package orchestrator

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// withoutReconciling is the built-in definition as a project copied it before
// the replay-conflict hand-back was routed: the same file with that one
// transition taken out.
func withoutReconciling(t *testing.T) string {
	t.Helper()
	const line = "      reconciling: develop\n"
	if !strings.Contains(string(deliveryDefinition), line) {
		t.Fatalf("the built-in delivery definition does not route integrate -> reconciling to develop")
	}
	return strings.Replace(string(deliveryDefinition), line, "", 1)
}

func deliveryDriftOf(t *testing.T, configPath string) DefinitionDrift {
	t.Helper()
	drifts, err := DeliveryDefinitionDrift(configPath)
	if err != nil {
		t.Fatalf("DeliveryDefinitionDrift() error = %v", err)
	}
	if len(drifts) != 1 || drifts[0].Workflow != DeliveryWorkflowID {
		t.Fatalf("DeliveryDefinitionDrift() = %+v, want one comparison for %s", drifts, DeliveryWorkflowID)
	}
	return drifts[0]
}

// TestTheBuiltInDeliveryDefinitionSendsAReplayConflictBackToItsDeveloper holds
// the shipped definition to routing the hand-back, which is what keeps a run
// whose replay conflict goes back to its developer from recording a divergence.
func TestTheBuiltInDeliveryDefinitionSendsAReplayConflictBackToItsDeveloper(t *testing.T) {
	t.Parallel()

	graph, err := observedDeliveryGraph(DeliveryWorkflowID, "")
	if err != nil {
		t.Fatalf("observedDeliveryGraph() error = %v", err)
	}
	node, ok := graph.Node(deliveryIntegrate)
	if !ok {
		t.Fatalf("the built-in has no %q state", deliveryIntegrate)
	}
	destination, routed := node.Next("reconciling")
	if !routed || destination.Name != deliveryDevelop || destination.Terminal {
		t.Errorf("integrate -> reconciling leads to %+v (routed=%t), want the developer's state %q", destination, routed, deliveryDevelop)
	}
}

// TestAProjectWithNoCopyOfItsOwnHasNothingToCompare is the ordinary project: it
// runs the build's definition, so there is nothing it could be behind.
func TestAProjectWithNoCopyOfItsOwnHasNothingToCompare(t *testing.T) {
	t.Parallel()

	drifts, err := DeliveryDefinitionDrift(projectConfigPath(t))
	if err != nil {
		t.Fatalf("DeliveryDefinitionDrift() error = %v", err)
	}
	if len(drifts) != 0 {
		t.Errorf("DeliveryDefinitionDrift() = %+v for a project that keeps no copy", drifts)
	}
}

// TestACopyLevelWithTheBuildSaysNothing is a copy that differs only in its
// comments and so reads as the same definition.
func TestACopyLevelWithTheBuildSaysNothing(t *testing.T) {
	t.Parallel()

	configPath := projectConfigPath(t)
	writeProjectDefinition(t, configPath, DeliveryWorkflowID, "# this project's copy\n"+string(deliveryDefinition))
	drift := deliveryDriftOf(t, configPath)
	if len(drift.Changes) != 0 {
		t.Errorf("Changes = %+v for a copy that differs only in comments", drift.Changes)
	}
	if notice := drift.Notice(); notice != "" {
		t.Errorf("Notice() = %q for a copy level with the build", notice)
	}
}

// TestACopyMissingTheReconcilingTransitionIsOfferedIt is the copy this
// repository holds until it adopts the transition: the drift names exactly that
// transition as available, and the notice says which one rather than that two
// digests differ.
func TestACopyMissingTheReconcilingTransitionIsOfferedIt(t *testing.T) {
	t.Parallel()

	configPath := projectConfigPath(t)
	path := writeProjectDefinition(t, configPath, DeliveryWorkflowID, withoutReconciling(t))
	drift := deliveryDriftOf(t, configPath)

	want := DefinitionChange{Key: "states.integrate.on.reconciling", Class: config.ClassAvailable, Yours: "", Build: deliveryDevelop}
	if len(drift.Changes) != 1 || drift.Changes[0] != want {
		t.Fatalf("Changes = %+v, want only %+v", drift.Changes, want)
	}
	if drift.Source != path {
		t.Errorf("Source = %q, want the copy at %q", drift.Source, path)
	}
	notice := drift.Notice()
	for _, said := range []string{"integrate -> reconciling", path, "yoyo config drift", "divergence"} {
		if !strings.Contains(notice, said) {
			t.Errorf("Notice() = %q, does not say %q", notice, said)
		}
	}
}

// TestACopyRoutingAnOutcomeElsewhereIsTheProjectsOwn holds the comparison to
// offering nothing the project might have chosen: a transition the copy sends
// somewhere else, and a summary it reads differently, are reported as its own.
func TestACopyRoutingAnOutcomeElsewhereIsTheProjectsOwn(t *testing.T) {
	t.Parallel()

	configPath := projectConfigPath(t)
	copied := strings.Replace(string(deliveryDefinition), "      reconciling: develop\n", "      reconciling: blocked\n", 1)
	copied = strings.Replace(copied, "summary: claim a work item", "summary: this project's own delivery loop", 1)
	writeProjectDefinition(t, configPath, DeliveryWorkflowID, copied)
	drift := deliveryDriftOf(t, configPath)

	if available := drift.Available(); len(available) != 0 {
		t.Errorf("Available() = %+v; nothing the copy decided for itself is offered", available)
	}
	yours := drift.OfClass(config.ClassYours)
	keys := make([]string, 0, len(yours))
	for _, change := range yours {
		keys = append(keys, change.Key)
	}
	if got := strings.Join(keys, ","); got != "states.integrate.on.reconciling,summary" {
		t.Errorf("the project's own changes are %s, want the rerouted transition and the summary", got)
	}
	if notice := drift.Notice(); notice != "" {
		t.Errorf("Notice() = %q; the notice speaks only what is available", notice)
	}
}

// TestACopyThatCannotBeDecodedIsNamed is a copy with nothing to compare: the
// comparison fails naming the file rather than reporting the copy current.
func TestACopyThatCannotBeDecodedIsNamed(t *testing.T) {
	t.Parallel()

	configPath := projectConfigPath(t)
	path := writeProjectDefinition(t, configPath, DeliveryWorkflowID, "schema: 1\nunknown_key: true\n")
	_, err := DeliveryDefinitionDrift(configPath)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("DeliveryDefinitionDrift() error = %v, want one naming %s", err, path)
	}
}
