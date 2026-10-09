package apigateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/secretref"
	"github.com/txn2/mcp-data-platform/internal/secretstore"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// SetSecrets installs the stored secrets a request's {{secret:<name>}}
// placeholders are filled from (#2051). Without it a request that names one
// is refused, never sent with the placeholder in it.
func (t *Toolkit) SetSecrets(src secretstore.Source) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.secrets = src
	for name, c := range t.connections {
		wireKeySecrets(c.auth, src, name)
	}
}

// connectionSecrets is the read of a secret a connection's own configuration
// names, which the store answers (secretstore.Store.ConnectionValue).
type connectionSecrets interface {
	ConnectionValue(ctx context.Context, name, connection string) (string, error)
}

// wireKeySecrets binds the stored-secret read a connection whose Google
// service account key is a stored secret mints its tokens through (#2061),
// scoped to that connection. A source that cannot answer it leaves the
// authenticator to refuse naming the secret.
func wireKeySecrets(auth upstreamauth.Authenticator, src secretstore.Source, connection string) {
	if cs, ok := src.(connectionSecrets); ok {
		upstreamauth.BindKeySecrets(auth, cs.ConnectionValue, connection)
	}
}

// secretLookupKey carries a call's lookup on its context.
type secretLookupKey struct{}

// withSecrets prepares ctx for one call through connection: the lookup its
// placeholders are filled through, scoped to the connection and the calling
// persona, and the Redactor its response passes through.
func (t *Toolkit) withSecrets(ctx context.Context, connection string) context.Context {
	t.mu.RLock()
	src := t.secrets
	t.mu.RUnlock()
	ctx, _ = secretref.WithRedactor(ctx)
	if src == nil {
		return ctx
	}
	return context.WithValue(ctx, secretLookupKey{}, src.Lookup(ctx, connection, mcpcontext.GetPersona(ctx)))
}

// errSecretsUnavailable refuses a placeholder where nothing can fill it.
var errSecretsUnavailable = errors.New("apigateway: this request references a stored secret ({{secret:<name>}}), and stored secrets are not available here; a placeholder is never sent as written")

// fillSecrets returns a copy of in with every placeholder in its path,
// query, headers and body filled in, each value recorded with the call's
// Redactor. in itself keeps its placeholders, so whatever is built from it
// afterwards (the export arguments, the next page's arguments) never holds
// a value.
func fillSecrets(ctx context.Context, in InvokeInput) (InvokeInput, error) {
	lookup, _ := ctx.Value(secretLookupKey{}).(secretref.Lookup)
	record := secretref.FromContext(ctx).Recording(func(name string) (string, error) {
		if lookup == nil {
			return "", errSecretsUnavailable
		}
		v, err := lookup(name)
		if err != nil {
			return "", fmt.Errorf("apigateway: %w; nothing was sent", err)
		}
		return v, nil
	})
	ct, _ := callerContentType(in.Headers)
	req, err := secretref.FillRequest(secretref.Request{Path: in.Path, Headers: in.Headers, Query: in.Query, Body: in.Body, ContentType: ct}, record)
	if err != nil {
		return in, err //nolint:wrapcheck // the lookup's refusal, which names the secret
	}
	out := in
	out.Path, out.Headers, out.Query, out.Body = req.Path, req.Headers, req.Query, req.Body
	return out, nil
}

// redactedError is err with every secret value this call sent replaced, for
// an error raised after the request was filled.
func redactedError(ctx context.Context, err error) error {
	r := secretref.FromContext(ctx)
	if err == nil || r.Empty() {
		return err
	}
	return errors.New(r.Error(err))
}
