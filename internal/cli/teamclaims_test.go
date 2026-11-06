package cli

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// Claims and pull request descriptions read the factory through one reader
// built over the same home and product, and a claim whose identity cannot be
// read in full still names the product and the home's machine identity, saying
// the forge account was not read.
func TestClaimsNameTheFactoryThroughTheOneReader(t *testing.T) {
	t.Parallel()

	var cfg config.Config
	cfg.Product.ID = "example"
	parts := components{config: cfg, stateRoot: t.TempDir(), repository: t.TempDir()}
	reader := factoryIdentities(parts)
	if reader.Home != parts.stateRoot || reader.Product != "example" || reader.Forge == nil {
		t.Fatalf("factoryIdentities() = %+v, want this home, this product and the forge access", reader)
	}
	machine, err := home.MachineIdentity(parts.stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	name := factoryName(parts)
	for _, want := range []string{"product example", "machine " + machine, "forge account not read"} {
		if !strings.Contains(name, want) {
			t.Errorf("factoryName() = %q, missing %q", name, want)
		}
	}
}
