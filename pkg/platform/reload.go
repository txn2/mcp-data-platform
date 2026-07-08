package platform

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// This file holds the reload re-materialization handlers and the public
// reload-publish surface, both of which stay on Platform: the handlers reach
// into Platform-owned state (connection store, toolkit registry, persona
// registry, API-key store) and the Publish* methods are called by admin
// handlers. The dedicated cross-replica reload BUS (its broadcaster channel and
// the publish/subscribe machinery) lives in pkg/platform/sessionsync and is
// reached through the sessions handle; the handlers are injected into it at
// construction (issue #843).

// reloadConnectionLocal re-materializes one connection on this replica
// from the connection store. Used both by the reload subscriber (peer
// announced a change) and indirectly mirrors the admin hot-reload path.
func (p *Platform) reloadConnectionLocal(kind, name string) {
	inst, err := p.connectionStore.Get(context.Background(), kind, name)
	for _, tk := range p.toolkitRegistry.All() {
		if tk.Kind() != kind {
			continue
		}
		cm, ok := tk.(toolkit.ConnectionManager)
		if !ok {
			continue
		}
		_ = cm.RemoveConnection(name)
		if err == nil && inst != nil {
			_ = cm.AddConnection(name, inst.Config)
		}
	}
}

// reloadCatalogLocal rebuilds every api-gateway connection that mounts
// the given catalog on this replica.
func (p *Platform) reloadCatalogLocal(catalogID string) {
	for _, tk := range p.toolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.ReloadConnectionsByCatalog(catalogID)
		}
	}
}

// reloadPersonaLocal reconciles the persona registry from the store on
// this replica (re-registers/updates DB personas). Used by the reload
// subscriber when a peer changes a persona.
func (p *Platform) reloadPersonaLocal() {
	p.loadDBPersonas()
}

// reloadAPIKeyLocal re-syncs the in-memory DB-loaded API keys from the
// store on this replica, dropping revoked keys (ReplaceHashedKeys).
func (p *Platform) reloadAPIKeyLocal() {
	if p.apiKeyStore == nil || p.apiKeyAuth == nil {
		return
	}
	defs, err := p.apiKeyStore.List(context.Background())
	if err != nil {
		slog.Warn("reload-bus: failed to list api keys for reload", logKeyError, err)
		return
	}
	keys := make([]auth.APIKey, 0, len(defs))
	for _, d := range defs {
		keys = append(keys, auth.APIKey{
			KeyHash:     d.KeyHash,
			Name:        d.Name,
			Email:       d.Email,
			Description: d.Description,
			Roles:       d.Roles,
			ExpiresAt:   d.ExpiresAt,
		})
	}
	p.apiKeyAuth.ReplaceHashedKeys(keys)
}

// PublishConnectionReload announces a connection config change to peer
// replicas. Implements admin.ReloadNotifier. Safe when the layer is nil.
func (p *Platform) PublishConnectionReload(kind, name string) {
	p.sessions.PublishConnectionReload(context.Background(), kind, name)
}

// PublishPersonaReload announces a persona change to peer replicas.
func (p *Platform) PublishPersonaReload() {
	p.sessions.PublishPersonaReload(context.Background())
}

// PublishAPIKeyReload announces an API-key change to peer replicas.
func (p *Platform) PublishAPIKeyReload() {
	p.sessions.PublishAPIKeyReload(context.Background())
}

// PublishCatalogReload announces an API-catalog spec change to peer
// replicas. Implements admin.ReloadNotifier. Safe when the layer is nil.
func (p *Platform) PublishCatalogReload(catalogID string) {
	p.sessions.PublishCatalogReload(context.Background(), catalogID)
}
