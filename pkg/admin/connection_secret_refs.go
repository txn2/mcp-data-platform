package admin

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/connsecretref"
)

// SecretScopes reads a stored secret's scope, never its value.
// *secretstore.Store satisfies it.
type SecretScopes = connsecretref.Scopes

// checkSecretReferences refuses a connection configuration that names a
// stored secret where it would not be filled or may not be used (#2066); see
// connsecretref.Check.
func (h *Handler) checkSecretReferences(ctx context.Context, kind, name string, config map[string]any) error {
	return connsecretref.Check(ctx, h.deps.Secrets, kind, name, config) //nolint:wrapcheck // the refusal names the field and the secret
}
