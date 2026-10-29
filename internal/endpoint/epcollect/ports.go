package epcollect

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// Sources finds the source the platform made for a form script.
type Sources interface {
	FormSource(ctx context.Context, scriptID string) (whsource.Source, bool)
}

// Writer makes one event durable in a source (the receiver's Deliver).
type Writer interface {
	Deliver(ctx context.Context, src whsource.Source, event whevent.Event, body []byte) error
}

// Sealer encrypts a sensitive value (fieldcrypt's RestFieldEncryptor). A
// value is sealed bound to its form and field and opened only for them
// (#2355).
type Sealer interface {
	Enabled() bool
	SealBound(binding, plaintext string) (string, error)
	OpenBound(binding, sealed string) (string, error)
}
