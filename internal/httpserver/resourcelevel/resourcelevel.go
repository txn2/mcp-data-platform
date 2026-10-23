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
//
// A person of an external persona never reads Global (#2257), and writes to
// their own folder only when the persona lets its people upload (#2258);
// nothing gives them a persona folder's write, which takes persona-admin.
func Apply(claims *resource.Claims, pr *persona.Registry, method string) (*resource.Claims, error) {
	if claims.IsAdmin || pr == nil {
		return claims, nil
	}
	per, ok := pr.GetForRoles(claims.Roles)
	if !ok {
		return claims, nil
	}
	perms := per.PortalPermissions(nil)
	claims.NoGlobal = per.External
	switch perms.Resources {
	case persona.PortalNone:
		return nil, resource.ErrForbidden
	case persona.PortalShared:
		if isResourceWrite(method) && perms.Uploads == nil {
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
