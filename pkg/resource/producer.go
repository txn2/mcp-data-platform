package resource

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// noteProducer records one write of a managed resource against whatever
// produced it (#1569). The target kind is this package's by construction, so
// the caller states only which resource it wrote and what the write was. It is
// best effort throughout: a resource is written whether or not the note lands.
func noteProducer(ctx context.Context, deps Deps, claims *Claims, w producedby.Write) {
	w.TargetKind = producedby.TargetResource
	producedby.Note(producerContext(ctx, claims), deps.Producers, w)
}

// producerContext names the producer this write is recorded against.
//
// A producer already on the context wins, because the surface that put it there
// knows more than these claims do: a managed-script run stamps its own script
// id, which is stable across a rename, where the claims carry only the
// script:<name> principal; an MCP call stamps the session it was made in.
//
// Otherwise the caller is a person working through the resources API, and their
// subject is the producer. The one caller this does not cover is an unattended
// one whose surface named no producer -- OnBehalfOf is set for exactly those
// and empty for every human. It records nothing rather than filing a run's
// write under a person who was not at a keyboard.
func producerContext(ctx context.Context, claims *Claims) context.Context {
	if producedby.Has(ctx) || claims == nil || claims.OnBehalfOf != "" {
		return ctx
	}
	return producedby.With(ctx, producedby.Producer{
		Kind:  producedby.KindPerson,
		ID:    claims.Sub,
		Label: claims.Email,
	})
}
