// Package upstreamwarn hands what an API connection's upstream answered to
// whatever records upstream warnings (#2486): a Deprecation or Sunset header,
// and a 404 or 410 from an operation that answered before. The outbound
// chain calls Observe for every answer an API connection receives, the way it
// feeds internal/connstate; the API gateway marks the request with the
// catalog operation it addresses, and an answer to a request with no operation
// is not observed. The operation is the catalog's operation id or "METHOD
// template", never the concrete path, which can carry a filled secret.
//
// It holds no recorder of its own: the platform installs one with SetObserver
// once its database is open, and until then every answer is passed over.
package upstreamwarn

import (
	"context"
	"net/http"
	"sync/atomic"
)

// Observer records what an upstream said about an operation.
type Observer interface {
	Observe(ctx context.Context, connection, operation string, status int, header http.Header)
}

type box struct{ o Observer }

//nolint:gochecknoglobals // the one process-wide recorder, installed like outbound's default metrics
var installed atomic.Pointer[box]

// SetObserver installs the recorder every answer is handed to; nil removes it.
func SetObserver(o Observer) {
	if o == nil {
		installed.Store(nil)
		return
	}
	installed.Store(&box{o: o})
}

type operationKey struct{}

// WithOperation marks the requests made under ctx as addressing operation.
func WithOperation(ctx context.Context, operation string) context.Context {
	if operation == "" {
		return ctx
	}
	return context.WithValue(ctx, operationKey{}, operation)
}

// Operation is the operation ctx marks, or "".
func Operation(ctx context.Context) string {
	op, _ := ctx.Value(operationKey{}).(string)
	return op
}

// Observe hands one answer to the installed recorder when the request named
// an operation. A transport failure (status 0) says nothing about the
// operation and is passed over.
func Observe(ctx context.Context, connection string, status int, header http.Header) {
	b := installed.Load()
	op := Operation(ctx)
	if b == nil || op == "" || status == 0 {
		return
	}
	b.o.Observe(ctx, connection, op, status, header)
}
