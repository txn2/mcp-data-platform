package apigateway

import (
	"context"
	"fmt"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
)

// probePath is the address a probe asks for: the connection's own base, with
// no operation named. It is a GET, so it is safe by definition, and the status
// it comes back with is not the point — a 404 from the upstream's router is as
// good an answer as a 200, because both prove the request reached the upstream
// and was authorized.
const probePath = "/"

// ProbeConnection sends the named connection's upstream a GET at its base URL
// through the same client, authenticator and TLS material a tool call uses, and
// reports the status that came back (#1805).
//
// An answered status is a success whatever it is, EXCEPT the two that say the
// credential was not accepted: 401 and 403 are reported as failures, because a
// connection whose upstream rejects its credential is precisely the connection
// an operator is asking about, and calling that healthy is the silence this
// endpoint exists to end.
//
// Implements connprobe.Prober.
func (t *Toolkit) ProbeConnection(ctx context.Context, name string) connprobe.Result {
	c, ok := t.lookup(name)
	if !ok {
		return connprobe.Failure(fmt.Sprintf("connection %q is not served by this toolkit", name), nil)
	}
	// A connection resolved by an in-process handler has no upstream and a
	// synthetic base URL, so dialing it would report the platform's own
	// built-in connection as unreachable.
	if c.cfg.Handler == HandlerInternal {
		return connprobe.Success(fmt.Sprintf(
			"connection %q is served in this process by the platform itself, so there is no upstream to reach", name))
	}
	req, err := buildUpstreamRequest(ctx, c.cfg, c.auth, catalogView{}, InvokeInput{
		Method: http.MethodGet,
		Path:   probePath,
	})
	if err != nil {
		// Building the request is also where the credential is obtained, so an
		// OAuth token endpoint that cannot be reached surfaces here rather than
		// on the call below.
		return connprobe.Failure(fmt.Sprintf("connection %q could not build an authorized request", name), err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return connprobe.Failure(fmt.Sprintf("connection %q could not reach %s", name, c.cfg.BaseURL), err)
	}
	defer func() { _ = resp.Body.Close() }()

	detail := fmt.Sprintf("%s answered GET %s with HTTP %d", c.cfg.BaseURL, probePath, resp.StatusCode)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return connprobe.Failure(detail+
			", which is the upstream rejecting this connection's credential rather than the route", nil)
	}
	return connprobe.Success(detail +
		"; the status is the upstream's own answer at its base, and any answer proves the route and the credential")
}

// Verify interface compliance.
var _ connprobe.Prober = (*Toolkit)(nil)
