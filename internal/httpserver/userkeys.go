package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/apikeyissue"
	"github.com/txn2/mcp-data-platform/internal/httpserver/userkeyhttp"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// wirePortalUserKeys attaches the self-scoped API key routes (#1759) to the
// portal deps, so a person issues and revokes keys against their own account
// from the settings page.
//
// It issues through the same core the admin route does, so a key somebody makes
// for themselves is made by one set of rules. A deployment with no key store,
// or none that can resolve an account, registers nothing rather than offering a
// page whose every action fails.
func wirePortalUserKeys(deps *portal.Deps, p *platform.Platform) {
	store := p.APIKeyStore()
	manager := p.APIKeyAuthenticator()
	if store == nil || manager == nil || manager.Principals() == nil {
		return
	}
	if _, noop := store.(*platform.NoopAPIKeyStore); noop {
		return
	}

	api := &userkeyhttp.API{
		Issuer: &apikeyissue.Issuer{
			Manager:    manager,
			Store:      store,
			Principals: manager.Principals(),
			Announce:   p.PublishAPIKeyReload,
		},
		Keys: manager,
		Revoke: func(r *http.Request, storedName string) error {
			if err := store.Delete(r.Context(), storedName); err != nil {
				if errors.Is(err, platform.ErrAPIKeyNotFound) {
					return userkeyhttp.ErrNoSuchKey
				}
				return err //nolint:wrapcheck // the handler logs it and answers 500
			}
			return nil
		},
		// A key another replica wrote a moment ago is not in this replica's
		// copy yet, and its owner must not be told they have no such key.
		Sync: func(r *http.Request) {
			if err := manager.SyncHashedKeys(r.Context()); err != nil {
				slog.Warn("user api key: reading the key store failed; answering from the copy held",
					"error", logsan.SanitizeForLog(err.Error()))
			}
		},
		// A revoked key stops authenticating from the store on the next
		// request everywhere (#1715); this only brings the copies up.
		Refresh: func(r *http.Request) {
			if err := manager.SyncHashedKeys(r.Context()); err != nil {
				slog.Warn("user api key: re-reading the key store after a revoke failed",
					"error", logsan.SanitizeForLog(err.Error()))
			}
			p.PublishAPIKeyReload()
		},
		Caller: func(r *http.Request) (string, string) {
			if user := portal.GetUser(r.Context()); user != nil {
				return user.Email, user.AuthType
			}
			return "", ""
		},
	}
	deps.APIKeyRegistrar = api.Register
}
