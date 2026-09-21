package notifywire

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/notification/notifypost"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// ChannelUpstream resolves the api connection a chat or webhook channel
// delivers through to its authorized transport (#1720).
//
// The registry is walked per send rather than snapshotted, for the reason the
// api browser's locator walks it per request: a connection added or re-keyed
// through the admin API is reachable without a restart, and a channel holding
// a transport built at startup would keep posting with a credential the
// operator has already rotated.
//
// Returns nil when there is no registry to walk, which leaves the three HTTP
// channel kinds undeliverable rather than silently failing per send.
func ChannelUpstream(reg *registry.Registry) notifypost.UpstreamFunc {
	if reg == nil {
		return nil
	}
	return func(connection string) (notifypost.Upstream, error) {
		api, err := locateAPIConnection(reg, connection)
		if err != nil {
			return nil, err
		}
		return api.Upstream(connection)
	}
}

// APIConnectionExists reports whether a live api toolkit serves a connection.
// The admin channel form uses it to warn about a channel naming a connection
// nothing serves, which is otherwise a channel that looks configured and fails
// only when something is sent to it.
func APIConnectionExists(reg *registry.Registry) func(context.Context, string) bool {
	if reg == nil {
		return nil
	}
	return func(ctx context.Context, connection string) bool {
		for _, tk := range reg.GetByKind(apigatewaykit.Kind) {
			api, ok := tk.(*apigatewaykit.Toolkit)
			if ok && api.ServesConnection(ctx, connection) {
				return true
			}
		}
		return false
	}
}

// locateAPIConnection finds the api toolkit serving one connection.
//
// ServesConnection rather than HasConnection: a connection saved on another
// replica reaches this one over the reload bus some time after the save
// returns, and this answers from the connection store in between (#1746).
func locateAPIConnection(reg *registry.Registry, connection string) (*apigatewaykit.Toolkit, error) {
	for _, tk := range reg.GetByKind(apigatewaykit.Kind) {
		api, ok := tk.(*apigatewaykit.Toolkit)
		if ok && api.ServesConnection(context.Background(), connection) {
			return api, nil
		}
	}
	return nil, &missingConnectionError{name: connection}
}

// missingConnectionError names the connection no live api toolkit serves. It
// is its own type so the message stays one sentence wherever it is wrapped:
// the sender turns it into a terminal failure whose text an operator reads in
// the delivery history.
type missingConnectionError struct{ name string }

// Error names the missing connection.
func (e *missingConnectionError) Error() string {
	return "no api connection named " + e.name + " is served here"
}
