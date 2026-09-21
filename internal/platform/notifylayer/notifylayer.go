// Package notifylayer is the notify tool: the one route a person in a
// session, a managed script and the portal all send to a channel through.
//
// It holds no transport and no queue. A send is an enqueue onto the existing
// notification queue, delivered by the existing worker, so an upstream outage
// is a retry rather than a failed tool call. What this package owns is the
// surface: which channels a caller may reach, what a document may be, and
// what an asset becomes when it is published.
//
// Authorization is the channel's connection, not a new dimension. A channel
// the caller's persona cannot reach the connection of is not listed and
// cannot be sent to, which is the same rule a tool call against that
// connection already obeys.
package notifylayer

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/platform/connscope"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// AssetReader is the portal read path publish needs: the asset record and its
// stored content. It is an interface so this package depends on reading an
// asset rather than on the portal's storage.
type AssetReader interface {
	// Get returns one asset by id.
	Get(ctx context.Context, id string) (*portaldomain.Asset, error)
	// Content returns the asset's stored bytes at its current version.
	Content(ctx context.Context, asset *portaldomain.Asset) ([]byte, error)
}

// Entitlement reports whether a caller may read an asset. It is the portal's
// existing access core, narrowed to the one question publish asks.
type Entitlement interface {
	// CanRead reports whether the identified caller may read the asset.
	CanRead(ctx context.Context, asset *portaldomain.Asset, userID, email string) bool
}

// Config wires the tool to the stores it reads and the queue it writes.
type Config struct {
	// Channels lists and resolves the operator's channels. nil leaves the
	// tool unregistered: there is nothing to send to.
	Channels notification.ChannelStore
	// Enqueuer writes the queue row a send becomes. nil leaves the tool
	// unregistered, because a listed channel that cannot be sent to is worse
	// than no tool at all.
	Enqueuer *notification.Enqueuer
	// Scope decides whether a caller's persona reaches a channel's
	// connection. nil denies every channel that names one, which is the
	// fail-closed answer the action path already gives.
	Scope *connscope.Scope
	// Assets reads the asset publish turns into a document. nil leaves the
	// publish action refusing, and list and send unaffected.
	Assets AssetReader
	// Access decides whether the caller may read the asset they named. nil
	// leaves the publish action refusing: a deployment that cannot check
	// entitlement must not publish on the strength of an id.
	Access Entitlement
	// PortalURL is the deployment's public base URL, used to build the link
	// a published document carries. Empty omits the link.
	PortalURL string
	// AdminPersona is the persona name whose reach is unrestricted, as it is
	// everywhere else in the product. Empty means no persona is lifted.
	AdminPersona string
}

// Handle is the registered tool.
type Handle struct {
	cfg Config
}

// New builds the handle, or nil when the deployment cannot send at all.
//
// The two required pieces are the channel records and the queue. Either
// missing means there is no feature here: a tool that lists destinations it
// cannot write to would advertise a capability the deployment does not have,
// which is the shape the vaporware gates exist to refuse.
func New(cfg Config) *Handle {
	if cfg.Channels == nil || cfg.Enqueuer == nil {
		return nil
	}
	return &Handle{cfg: cfg}
}

// reachable reports whether a caller acting under personaName may send to ch.
//
// An email channel is reachable by everyone the tool is: it names no
// connection, so there is no upstream authorization to inherit, exactly as the
// built-in portal destination is reachable by every script. A channel naming a
// connection is reachable only by a persona that reaches that connection.
//
// unrestricted lifts the boundary for an administrator, whose reach is
// unrestricted by design everywhere else in the product.
func (h *Handle) reachable(ch notification.Channel, personaName string, unrestricted bool) bool {
	if !notification.ChannelNeedsConnection(ch.Kind) {
		return true
	}
	if unrestricted {
		return true
	}
	return h.cfg.Scope.AllowConnection(personaName, ch.Connection)
}

// reachableChannels lists the enabled channels the caller may send to, in the
// store's name order.
//
// A disabled channel is omitted rather than listed as unavailable: the list
// exists to be chosen from, and an entry whose only possible outcome is a
// refusal is not a choice.
func (h *Handle) reachableChannels(ctx context.Context, personaName string, unrestricted bool) ([]notification.Channel, error) {
	all, err := h.cfg.Channels.List(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller states what it was doing
	}
	out := make([]notification.Channel, 0, len(all))
	for _, ch := range all {
		if ch.Enabled && h.reachable(ch, personaName, unrestricted) {
			out = append(out, ch)
		}
	}
	return out, nil
}
