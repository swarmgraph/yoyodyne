// Package factory names the factory a harness is, for every contributor that
// learns about it only through the forge and Git: another copy of the factory on
// another machine, or a person reading a pull request
// (docs/designs/working-alongside-other-contributors.md, section 5).
//
// A factory is the product it works, the machine it runs on, and the forge
// account it acts as. Two homes on one machine are two factories, because each
// home keeps its own machine identity; one home working two products is two
// factories as well. Read is the one place the three are put together, so a
// claim, a shared record, and a pull request description name a factory the same
// way.
package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
)

// Identity is one factory. Each field is required: an identity missing a part
// would name a different factory, so Read refuses rather than returning one.
type Identity struct {
	// Product is the product id the factory works.
	Product string `json:"product"`
	// Machine is the machine identity kept at the top of the factory's home
	// (home.MachineIdentity).
	Machine string `json:"machine"`
	// ForgeAccount is the forge account the factory acts as.
	ForgeAccount string `json:"forge_account"`
}

// String is the identity on one line, in the form a pull request description
// and a record shown to a person carry: the product, the machine, and the
// account, each named.
func (i Identity) String() string {
	return fmt.Sprintf("product %s, machine %s, forge account %s", i.Product, i.Machine, i.ForgeAccount)
}

// ForgeAccount answers which forge account the harness acts as. The forge
// client (publish.GitHub) is the one in use.
type ForgeAccount interface {
	Account(ctx context.Context) (string, error)
}

// Reader reads one factory's identity. The machine identity is read from the
// home each time, which is a local file; the forge account is asked of the forge
// once and kept for as long as the reader lives, because the credential a
// running process holds does not change under it and the question costs a
// network call. A failed question is not kept, so the next Read asks again.
type Reader struct {
	// Home is the machine home the state root resolved to.
	Home    string
	Product domain.ProductID
	Forge   ForgeAccount

	mu      sync.Mutex
	account string
}

// Read is the factory's identity: the product id, the machine identity kept in
// the home (made the first time it is asked for), and the forge account.
func (r *Reader) Read(ctx context.Context) (Identity, error) {
	if r == nil {
		return Identity{}, errors.New("no factory identity reader is configured")
	}
	if err := domain.ValidateIdentifier("product id", string(r.Product)); err != nil {
		return Identity{}, fmt.Errorf("read the factory identity: %w", err)
	}
	machine, err := home.MachineIdentity(r.Home)
	if err != nil {
		return Identity{}, fmt.Errorf("read the factory identity: %w", err)
	}
	account, err := r.forgeAccount(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("read the factory identity: %w", err)
	}
	return Identity{Product: string(r.Product), Machine: machine, ForgeAccount: account}, nil
}

// Name is the identity Read returns, in the words Identity.String gives it: the
// name a claim records for the factory holding it (teamclaim.Claims.Identity).
func (r *Reader) Name(ctx context.Context) (string, error) {
	identity, err := r.Read(ctx)
	if err != nil {
		return "", err
	}
	return identity.String(), nil
}

func (r *Reader) forgeAccount(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account != "" {
		return r.account, nil
	}
	if r.Forge == nil {
		return "", errors.New("no forge is configured to say which account the factory acts as")
	}
	account, err := r.Forge.Account(ctx)
	if err != nil {
		return "", err
	}
	account = strings.TrimSpace(account)
	if account == "" {
		return "", errors.New("the forge named no account")
	}
	r.account = account
	return account, nil
}
