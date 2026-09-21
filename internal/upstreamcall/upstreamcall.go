// Package upstreamcall is one connection's authorized outbound transport,
// handed to a platform layer that must call that upstream without holding its
// credential (#1720).
//
// It exists because a notification channel delivers through a connection the
// operator already created: the bot token is that connection's credential,
// encrypted at rest and rotated in one place, and a channel that held its own
// copy would be a second place to rotate and a second place to leak. The layer
// that posts a message therefore receives the ability to make an authorized
// call and never the material that authorizes it.
//
// It is its own package rather than a file in the api gateway because it
// depends on none of the gateway: given a client, an authenticator and a base
// URL it makes a call, which is what lets a second connection kind hand one
// out without the gateway growing another export.
package upstreamcall

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
)

// Upstream is one connection's authorized outbound transport.
//
// Do applies the connection's authentication on each call rather than once at
// construction, so a connection whose credential is an OAuth token being
// refreshed behind it stays callable through an Upstream held for the life of
// a worker.
type Upstream struct {
	name          string
	baseURL       string
	client        *http.Client
	auth          upstreamauth.Authenticator
	staticHeaders map[string]string
	callTimeout   time.Duration
}

// Config is what a connection kind hands over to make its upstream callable.
type Config struct {
	// Name is the connection's name, for an error that has to say which
	// connection refused.
	Name string
	// BaseURL is the connection's upstream root.
	BaseURL string
	// Client is the connection's materialized HTTP client, with its TLS
	// material and timeouts already applied.
	Client *http.Client
	// Auth applies the connection's credential to each request.
	Auth upstreamauth.Authenticator
	// StaticHeaders are the operator-configured headers on every request.
	StaticHeaders map[string]string
	// CallTimeout bounds a request that carries no deadline of its own.
	CallTimeout time.Duration
}

// New builds the transport for a connection.
func New(cfg Config) *Upstream {
	return &Upstream{
		name:          cfg.Name,
		baseURL:       cfg.BaseURL,
		client:        cfg.Client,
		auth:          cfg.Auth,
		staticHeaders: cfg.StaticHeaders,
		callTimeout:   cfg.CallTimeout,
	}
}

// Connection returns the name of the connection this transport belongs to,
// for an error message that has to say which connection refused.
func (u *Upstream) Connection() string { return u.name }

// BaseURL returns the connection's upstream root. A caller appends the path
// its protocol names (Slack's /chat.postMessage, Mattermost's /api/v4/posts)
// rather than being handed a whole URL, so the operator's base_url stays the
// one statement of which host is reached.
func (u *Upstream) BaseURL() string { return u.baseURL }

// Do applies the connection's static headers and authentication and sends the
// request through its client.
//
// A request carrying no deadline of its own is bounded by the connection's
// call timeout, so a queued send cannot hang a worker on an upstream that
// accepts a connection and never answers.
func (u *Upstream) Do(req *http.Request) (*http.Response, error) {
	// Static headers are the operator's, so they are set before auth: the
	// Authorization and API-key header names are reserved from operator
	// configuration by ValidateStaticHeaders, and applying auth last keeps
	// the credential authoritative even if that ever changes.
	for name, value := range u.staticHeaders {
		req.Header.Set(name, value)
	}
	if err := u.auth.Apply(req); err != nil {
		return nil, fmt.Errorf("upstreamcall: applying auth for connection %q: %w", u.name, err)
	}
	if _, hasDeadline := req.Context().Deadline(); !hasDeadline {
		timeout := u.callTimeout
		if timeout <= 0 {
			timeout = upstreamauth.DefaultCallTimeout
		}
		ctx, cancel := context.WithTimeout(req.Context(), timeout)
		// The cancel is released when the body is closed rather than when
		// Do returns: the response body is read by the caller, after this
		// frame is gone, and canceling here would close it under them.
		req = req.WithContext(ctx)
		resp, err := u.send(req)
		if err != nil {
			cancel()
			return nil, err
		}
		resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}
	return u.send(req)
}

// send transmits the prepared request.
func (u *Upstream) send(req *http.Request) (*http.Response, error) {
	// #nosec G107 G704 -- the URL is the operator's configured base_url with
	// a path this package chose, and the credential is injected by
	// auth.Apply above, identical to the invoke path.
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstreamcall: connection %q: %w", u.name, err)
	}
	return resp, nil
}

// cancelOnClose releases a request's timeout context when its response body
// is closed, which is the point the caller is done with the response.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close closes the body and releases the context.
func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err //nolint:wrapcheck // passthrough of the body's own error
}
