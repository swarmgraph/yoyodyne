package factory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/home"
)

type countedForge struct {
	account string
	err     error
	asked   int
}

func (f *countedForge) Account(context.Context) (string, error) {
	f.asked++
	return f.account, f.err
}

// The identity is the product, the machine identity kept in the home, and the
// forge account; the forge is asked once and the home's identity is made once.
func TestReadNamesTheProductTheMachineAndTheForgeAccount(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), ".yoyodyne")
	forge := &countedForge{account: "factory-bot\n"}
	reader := &Reader{Home: root, Product: "yoyodyne", Forge: forge}
	first, err := reader.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	machine, err := home.MachineIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Identity{Product: "yoyodyne", Machine: machine, ForgeAccount: "factory-bot"}); first != want {
		t.Fatalf("Read() = %+v, want %+v", first, want)
	}
	again, err := reader.Read(context.Background())
	if err != nil || again != first {
		t.Fatalf("Read() again = %+v, %v; want %+v", again, err, first)
	}
	if forge.asked != 1 {
		t.Fatalf("the forge was asked %d times, want once", forge.asked)
	}
	// Another reader on the same home is the same factory.
	other, err := (&Reader{Home: root, Product: "yoyodyne", Forge: &countedForge{account: "factory-bot"}}).Read(context.Background())
	if err != nil || other != first {
		t.Fatalf("a second reader on the same home = %+v, %v; want %+v", other, err, first)
	}
	if line := first.String(); !strings.Contains(line, "yoyodyne") || !strings.Contains(line, machine) || !strings.Contains(line, "factory-bot") {
		t.Fatalf("String() = %q, want every part named", line)
	}
}

// Two homes on one machine against one forge account are two factories.
func TestTwoHomesAreTwoFactories(t *testing.T) {
	t.Parallel()

	left, err := (&Reader{Home: t.TempDir(), Product: "yoyodyne", Forge: &countedForge{account: "factory-bot"}}).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	right, err := (&Reader{Home: t.TempDir(), Product: "yoyodyne", Forge: &countedForge{account: "factory-bot"}}).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if left == right || left.Machine == right.Machine {
		t.Fatalf("two homes read as one factory: %+v", left)
	}
}

// An identity missing a part would name another factory, so it is refused, and
// a forge that did not answer is asked again next time.
func TestReadRefusesAnIdentityMissingAPart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if _, err := (&Reader{Home: t.TempDir(), Product: "yoyodyne"}).Read(ctx); err == nil {
		t.Fatal("Read() with no forge returned an identity")
	}
	if _, err := (&Reader{Home: t.TempDir(), Forge: &countedForge{account: "factory-bot"}}).Read(ctx); err == nil {
		t.Fatal("Read() with no product returned an identity")
	}
	if _, err := (&Reader{Home: t.TempDir(), Product: "yoyodyne", Forge: &countedForge{account: "  "}}).Read(ctx); err == nil {
		t.Fatal("Read() with an empty forge account returned an identity")
	}
	var unset *Reader
	if _, err := unset.Read(ctx); err == nil {
		t.Fatal("Read() on no reader returned an identity")
	}

	forge := &countedForge{err: errors.New("the forge could not be reached")}
	reader := &Reader{Home: t.TempDir(), Product: "yoyodyne", Forge: forge}
	if _, err := reader.Read(ctx); err == nil || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("Read() with the forge unreachable error = %v", err)
	}
	forge.err, forge.account = nil, "factory-bot"
	if identity, err := reader.Read(ctx); err != nil || identity.ForgeAccount != "factory-bot" || forge.asked != 2 {
		t.Fatalf("Read() after the forge answered = %+v, %v (asked %d times)", identity, err, forge.asked)
	}
}
