package apigateway

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/upstreamcall"
)

// Upstream returns the authorized outbound transport for the named
// connection, so a platform layer can call that upstream without holding its
// credential (#1720).
//
// It refuses the two connection shapes that have no upstream credential to
// offer: a handler=internal connection is served in this process and has no
// upstream at all, and an identity-passthrough connection deliberately carries
// no credential of its own, acting instead as whichever caller is on the
// request. A queued message has no caller -- it is delivered minutes after the
// sender's session ended -- so passthrough would authorize nothing.
func (t *Toolkit) Upstream(name string) (*upstreamcall.Upstream, error) {
	c, ok := t.lookup(name)
	if !ok {
		return nil, fmt.Errorf("apigateway: no api connection named %q", name)
	}
	if c.cfg.Handler == HandlerInternal {
		return nil, fmt.Errorf("apigateway: connection %q is served inside this process and has no upstream to deliver to", name)
	}
	if c.cfg.IdentityPassthrough {
		return nil, fmt.Errorf("apigateway: connection %q acts as the calling user and holds no credential of its own, so it cannot deliver a queued message", name)
	}
	return upstreamcall.New(upstreamcall.Config{
		Name:          name,
		BaseURL:       c.cfg.BaseURL,
		Client:        c.client,
		Auth:          c.auth,
		StaticHeaders: c.cfg.StaticHeaders,
		CallTimeout:   c.cfg.CallTimeout,
	}), nil
}
