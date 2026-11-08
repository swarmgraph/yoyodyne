package orchestrator

import (
	"context"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/factory"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
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

// forgeMark is the hidden line everything the harness writes on the forge ends
// with (factory.Identity.Mark): the factory's whole identity where the reader
// can say it, and otherwise the first product named among those the writer
// knows, which is all that reading feedback tests. A write is never held back
// for want of the rest. Where no product is known at all the line names none,
// and the forge adapter refuses the write rather than leave it unmarked.
func forgeMark(ctx context.Context, identities FactoryIdentities, products ...domain.ProductID) string {
	if identities != nil {
		if identity, err := identities.Read(ctx); err == nil && identity.Product != "" {
			return identity.Mark()
		}
	}
	for _, product := range products {
		if product != "" {
			return factory.ProductMark(product)
		}
	}
	return factory.ProductMark("")
}

// closeCommentID is the identifier a close's comment is kept under on the run's
// record of the request (runstate.PullRequest.Wrote), and empty where the forge
// named none. A close leaves its comment on the request as a whole, which the
// forge numbers among the request's comments.
func closeCommentID(closure publish.Closure) string {
	if closure.CommentID <= 0 {
		return ""
	}
	return runstate.FeedbackID("comment", closure.CommentID)
}
