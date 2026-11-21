// Package resourcelevel narrows a caller's resource claims to their persona's
// portal resources level (#2225).
package resourcelevel

import (
	"context"
	"fmt"
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
// A person of an external persona reads only their own folder, never Global
// or their persona's (#2257, #2290), and writes to it only when the persona
// lets its people upload (#2258). What is shared with them they read through
// their shares.
//
// The claims carry the caller's write grants on Global folders (#2684), read
// once for the request; the level above is the ceiling no grant overrides.
func Apply(ctx context.Context, claims *resource.Claims, pr *persona.Registry, method string) (*resource.Claims, error) {
	if claims.IsAdmin || pr == nil {
		return claims, nil
	}
	granted := claims.WithFolderGrants(ctx)
	claims = &granted
	per, ok := pr.GetForRoles(claims.Roles)
	if !ok {
		return claims, nil
	}
	perms := per.PortalPermissions(nil)
	claims.NoGlobal = per.External
	if per.External {
		claims.Personas, claims.AdminOfPersonas = nil, nil
	}
	return byLevel(claims, per, perms, method)
}

func isResourceWrite(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// byLevel holds claims to a persona's Resources level: none refuses every
// route, shared refuses a write unless the persona uploads, own drops the
// persona folders. A refusal names the persona and the setting that turns the
// refused thing on (#2744): a client who could not upload, and the
// administrators looking into it, were told "invalid or missing CSRF token".
func byLevel(claims *resource.Claims, per *persona.Persona, perms persona.PortalAccess, method string) (*resource.Claims, error) {
	switch perms.Resources {
	case persona.PortalNone:
		return nil, refused(fmt.Sprintf("your persona (%s) has no access to Resources", per.Name))
	case persona.PortalShared:
		if isResourceWrite(method) && (perms.Uploads == nil || !*perms.Uploads) {
			return nil, refused(uploadsOff(per))
		}
	case persona.PortalOwn:
		claims.Personas, claims.AdminOfPersonas = nil, nil
	}
	return claims, nil
}

// uploadsOff is the refusal of a write to a persona whose Resources level
// reads only, naming where an administrator turns uploads on.
func uploadsOff(per *persona.Persona) string {
	if per.External {
		return fmt.Sprintf("uploads are not turned on for your persona (%s); an administrator turns them on with Resources: Read and upload to their own folder", per.Name)
	}
	return fmt.Sprintf("your persona (%s) reads Resources and uploads nothing; an administrator sets its Resources level to Own folder or Persona folders to let it upload", per.Name)
}

// RefusedError is a refusal for what the caller's persona allows: the resource
// handler answers it 403 with its words, and it is a resource.ErrForbidden.
type RefusedError struct{ msg string }

func refused(msg string) error { return &RefusedError{msg: msg} }

func (e *RefusedError) Error() string { return e.msg }

// Refusal is what the caller is told.
func (e *RefusedError) Refusal() string { return e.msg }

// Is makes every RefusedError a resource.ErrForbidden.
func (*RefusedError) Is(target error) bool { return target == resource.ErrForbidden }
