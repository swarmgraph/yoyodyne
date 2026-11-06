package orchestrator

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/factory"
)

// FactoryIdentities reads which factory this harness is: the product id, the
// machine identity kept in the home, and the forge account it acts as
// (factory.Reader).
type FactoryIdentities interface {
	Read(ctx context.Context) (factory.Identity, error)
}

// factoryLine is the sentence of a pull request description that names the factory
// that opened it, so a contributor reading the request on the forge can tell
// which factory's work it is. An identity that cannot be read is said to be
// unread rather than stopping the publication, because the request carries the
// work either way; the reason is left out, because it names paths on this
// machine that do not belong on the forge. A pipeline with no reader names no
// factory.
func (p Pipeline) factoryLine(ctx context.Context) string {
	if p.Factory == nil {
		return ""
	}
	identity, err := p.Factory.Read(ctx)
	if err != nil {
		return "Opened by: a factory whose identity could not be read when this request was opened"
	}
	return "Opened by: product `" + identity.Product + "`, machine `" + identity.Machine + "`, forge account `" + identity.ForgeAccount + "`"
}
