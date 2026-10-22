// Package resourcelevel narrows a caller's resource claims to their persona's
// portal resources level (#2225).
package resourcelevel

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Apply narrows a caller's resource claims to their persona's
// portal resources level (#2225): none refuses every resources route, shared
// reads the caller's own folder, their persona's folder and Global and writes
// nothing, own drops the persona folders and keeps the caller's own (which an
// administrator still sees), and persona leaves the claims as they were. An
// administrator is held to none of it.
func Apply(claims *resource.Claims, pr *persona.Registry, method string) (*resource.Claims, error) {
	if claims.IsAdmin || pr == nil {
		return claims, nil
	}
	per, ok := pr.GetForRoles(claims.Roles)
	if !ok {
		return claims, nil
	}
	switch per.PortalPermissions(nil).Resources {
	case persona.PortalNone:
		return nil, resource.ErrForbidden
	case persona.PortalShared:
		if isResourceWrite(method) {
			return nil, resource.ErrForbidden
		}
	case persona.PortalOwn:
		claims.Personas, claims.AdminOfPersonas = nil, nil
	}
	return claims, nil
}

func isResourceWrite(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}
