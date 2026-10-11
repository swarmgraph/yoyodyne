package orchestrator

// This repository's own copy of its delivery definition, read the way a run of
// this repository reads it.
//
// Yoyodyne is a project like any other project it runs, so it keeps its own
// delivery definition under `.yoyodyne/workflows/` rather than only inside the
// executable. That copy is what every run here executes, which makes it the
// acceptance case for project-owned definitions: if it did not compile, or
// answered to another name, the work would stop before it was claimed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// repositoryConfigPath is this repository's own configuration, from the package
// directory these tests run in.
const repositoryConfigPath = "../../.yoyodyne/config.yaml"

// TestThisRepositorysOwnDeliveryDefinitionIsWhatItsRunsExecute reads the
// definition in force for this repository exactly as a run does, and holds it to
// being the project's file rather than the built-in.
func TestThisRepositorysOwnDeliveryDefinitionIsWhatItsRunsExecute(t *testing.T) {
	t.Parallel()

	sequence, err := deliverySequenceFor(DeliveryWorkflowID, repositoryConfigPath)
	if err != nil {
		t.Fatalf("deliverySequenceFor(%s, %s) error = %v", DeliveryWorkflowID, repositoryConfigPath, err)
	}
	if !sequence.Project {
		t.Fatalf("this repository's runs execute the built-in definition; it keeps its own under %s", ProjectDefinitionPath(DeliveryWorkflowID))
	}
	want := filepath.Join("..", "..", ".yoyodyne", filepath.FromSlash(ProjectDefinitionPath(DeliveryWorkflowID)))
	if sequence.Source != want {
		t.Errorf("the definition was read from %s and this repository keeps its own at %s", sequence.Source, want)
	}
	if _, err := compileDelivery(sequence); err != nil {
		t.Fatalf("this repository's own delivery definition does not compile, so every run of it stops before claiming anything: %v", err)
	}
}

// TestThisRepositorysDeliveryDefinitionMatchesTheBuiltIn holds the copy and the
// built-in to one sequence, less what the built-in has added that the copy has
// not yet adopted.
//
// The parity harness walks the built-in against the recorded baseline, and this
// repository's runs execute the copy, so a change to one and not the other would
// leave the two measuring different things with nothing saying so. They are
// compared as decoded definitions, which is what the digest an instance pins is
// taken over and which deliberately ignores comments — the copy carries a header
// of its own explaining what it is.
//
// One difference is allowed: a transition the built-in routes and the copy does
// not route at all, which `yoyo config drift` offers this repository as
// available. The copy is protected, so a transition added to the built-in reaches
// it only by a change that carries the copy; until then a run of the copy that
// takes the outcome records a divergence, and this test names each such
// transition rather than reporting a bare digest mismatch. Any other difference
// fails it, naming where the two read differently.
//
// This repository diverging from the built-in on purpose is allowed and is what
// project-owned definitions are for. It means changing this test, which is the
// point at which somebody has decided to.
func TestThisRepositorysDeliveryDefinitionMatchesTheBuiltIn(t *testing.T) {
	t.Parallel()

	project, err := observedDeliveryGraph(DeliveryWorkflowID, repositoryConfigPath)
	if err != nil {
		t.Fatalf("observedDeliveryGraph(%s, %s) error = %v", DeliveryWorkflowID, repositoryConfigPath, err)
	}
	builtin, err := observedDeliveryGraph(DeliveryWorkflowID, "")
	if err != nil {
		t.Fatalf("observedDeliveryGraph(%s, built in) error = %v", DeliveryWorkflowID, err)
	}
	drifts, err := DeliveryDefinitionDrift(repositoryConfigPath)
	if err != nil {
		t.Fatalf("DeliveryDefinitionDrift(%s) error = %v", repositoryConfigPath, err)
	}
	var drift DefinitionDrift
	for _, compared := range drifts {
		if compared.Workflow == DeliveryWorkflowID {
			drift = compared
		}
	}
	if drift.Workflow == "" {
		t.Fatalf("no comparison was made for %s; this repository keeps its own copy", DeliveryWorkflowID)
	}
	for _, change := range drift.OfClass(config.ClassYours) {
		t.Errorf("this repository's copy and the built-in differ at %s: the copy says %q and the built-in %q; the parity harness measures the built-in and the runs here execute the copy",
			change.Key, change.Yours, change.Build)
	}
	available := drift.Available()
	for _, change := range available {
		t.Logf("the built-in routes %s to %q and this repository's copy does not yet; a run here that takes it records a divergence until the copy adopts it",
			change.Said(), change.Build)
	}
	if len(drift.Changes) == 0 && project.Digest() != builtin.Digest() {
		t.Errorf("this repository's copy digests to %s and the built-in to %s, and no difference between them was found to name",
			project.Digest(), builtin.Digest())
	}
}

// TestThisRepositorysDeliveryDefinitionSaysWhereTheBuiltInIs holds the copy's
// header to naming what it was copied from.
//
// A project file that does not say where it came from is one nobody can update
// against a later build, which is the cost this whole arrangement has: nothing
// is merged, so an improvement to the built-in reaches a project that ejected a
// copy only if somebody goes and looks.
func TestThisRepositorysDeliveryDefinitionSaysWhereTheBuiltInIs(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", ".yoyodyne", filepath.FromSlash(ProjectDefinitionPath(DeliveryWorkflowID)))
	definition, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if !strings.Contains(string(definition), "internal/orchestrator/delivery.yaml") {
		t.Errorf("%s does not name the built-in it was copied from", path)
	}
}
