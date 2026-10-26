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

// Sealer encrypts a sensitive value (fieldcrypt's RestFieldEncryptor).
type Sealer interface {
	Enabled() bool
	Seal(plaintext string) (string, error)
	Open(sealed string) (string, error)
}
