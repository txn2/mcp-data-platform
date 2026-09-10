package resourcewrite

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Locate resolves an address to the resource filed there, and reports the
// canonical mcp:// URI the address names whether or not anything is filed at
// it.
//
// It is the lookup a caller holding a path rather than an id needs, and it is
// the same resolution a landing performs: the alias trail a move left behind is
// followed, so a file somebody refiled is still the file that path names, and
// the record that comes back reports the address it actually lives at.
//
// Nothing being there is an answer rather than an error, which is what lets one
// lookup serve both callers: a create that may replace asks whether to create
// or to revise, and a read reports the address as empty. A resource the caller
// may not see reads as nothing, for the reason Get states. A read that FAILED
// is a third answer and stays an error, because acting on "nothing is there"
// when the store could not say is how a second file gets filed at an address
// that already has one.
//
// The URI is returned beside the record rather than only inside it so a caller
// can name the address it asked about when the answer is that nothing is filed
// there.
func (w *Writer) Locate(
	ctx context.Context, addr toolkit.ResourceAddress, claims resource.Claims,
) (found *resource.Resource, uri string, err error) {
	p, err := w.resolveAddress(addr, claims)
	if err != nil {
		return nil, "", err
	}
	existing, err := w.at(ctx, p.uri)
	if err != nil {
		return nil, p.uri, err
	}
	if existing == nil || !resource.CanAccessResource(claims, existing) {
		return nil, p.uri, nil
	}
	return existing, p.uri, nil
}

// DefaultListLimit is the page size a listing takes when its caller names none,
// and the largest page it will answer with. It is a tool's page rather than a
// browser's: a model reading a folder listing pays for every row in context,
// and a folder with more rows than this is one to narrow by path.
const DefaultListLimit = 100

// List returns the resources filed under a folder that the caller may see,
// newest first, with the total the page was cut from.
//
// The visible libraries are derived from the caller's claims and never from
// what they asked for: naming a library they cannot reach narrows the answer to
// nothing rather than widening it.
func (w *Writer) List(
	ctx context.Context, q toolkit.ResourceQuery, claims resource.Claims,
) (found []resource.Resource, total int, err error) {
	// Empty is the whole library here, unlike an address, where a file has to
	// be filed in a folder.
	path := strings.TrimSpace(q.Path)
	if path != "" {
		if err := validateFolderPath(path); err != nil {
			return nil, 0, err
		}
	}
	scopes, all := resource.ListScopes(claims, strings.TrimSpace(q.Scope), strings.TrimSpace(q.ScopeID))
	limit := q.Limit
	if limit <= 0 || limit > DefaultListLimit {
		limit = DefaultListLimit
	}
	offset := max(q.Offset, 0)
	found, total, err = w.deps.Store.List(ctx, resource.Filter{
		Scopes: scopes, AllScopes: all, Path: path, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("could not list the managed resources: %w", err)
	}
	return found, total, nil
}

// at reads what is filed at a canonical URI, telling "nothing is there" apart
// from "the store could not say".
//
// The distinction is the point: creating on a failed read would file a second
// file at an address that already has one, and the caller would never learn
// which of them they wrote.
func (w *Writer) at(ctx context.Context, uri string) (*resource.Resource, error) {
	existing, err := w.deps.Store.GetByURI(ctx, uri)
	if err != nil && !resource.IsNotFound(err) {
		return nil, fmt.Errorf("could not read what is filed at %s: %w", uri, err)
	}
	return existing, nil
}

// resolveAddress validates an address's parts and composes the canonical URI
// they name. Every rule is the managed-resource layer's own, so an address a
// tool call may reach is one an upload may write.
func (w *Writer) resolveAddress(addr toolkit.ResourceAddress, claims resource.Claims) (landing, error) {
	scope, scopeID := resource.ResolveScopeFor(resource.Scope(strings.TrimSpace(addr.Scope)),
		strings.TrimSpace(addr.ScopeID), claims)
	if err := resource.ValidateScope(scope, scopeID); err != nil {
		return landing{}, fmt.Errorf("the library is not one this platform has: %w", err)
	}
	path := strings.TrimSpace(addr.Path)
	if err := validateFolderPath(path); err != nil {
		return landing{}, err
	}
	filename, err := resource.SanitizeFilename(addr.Filename)
	if err != nil {
		return landing{}, fmt.Errorf("the address needs a plain file name: %w", err)
	}
	return landing{
		scope: scope, scopeID: scopeID, path: path, filename: filename,
		uri: resource.BuildURI(w.deps.URIScheme, scope, scopeID, path, filename),
	}, nil
}

// validateFolderPath checks a folder path and says what a path is when it is
// not one. One copy, so a lookup and a listing refuse the same path in the same
// words.
func validateFolderPath(path string) error {
	if err := resource.ValidatePath(path); err != nil {
		return fmt.Errorf("%w. A path is the folder chain the file is filed under inside the "+
			"library, for example \"datasets\" or \"datasets/media-manager/shows\"", err)
	}
	return nil
}
