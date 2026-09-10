package resourcewrite

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Delete removes a managed resource and the objects its content lives in,
// returning the record that was removed (#1665).
//
// The authority is the authority to change the file, which is the same rule
// replacing its content meets: the person who uploaded it, or an administrator
// of the library it is filed in. Deleting is not a stronger authority than
// overwriting -- a replacement already leaves nothing of the previous content
// beyond the version trail this delete takes with it.
//
// What still points at the file is deliberately not consulted here. That
// question is the caller's to ask and to put to whoever is deleting, because
// the answer is a warning rather than a rule: the surface that asks it decides
// what to do with the answer, and this is the one place the delete itself
// happens for every surface that reaches it.
func (w *Writer) Delete(ctx context.Context, id string, claims resource.Claims) (*resource.Resource, error) {
	res, err := w.Get(ctx, id, claims)
	if err != nil {
		return nil, err
	}
	if !resource.CanModifyResource(claims, res) {
		return nil, fmt.Errorf("you cannot delete a file in %s: %w",
			ScopePhrase(res.Scope, res.ScopeID), ErrRefused)
	}
	if err := resource.DeleteResource(ctx, w.deps, res); err != nil {
		return nil, fmt.Errorf("could not delete the managed resource: %w", err)
	}
	w.unregister(res)
	return res, nil
}

// unregister takes the deleted resource out of the MCP resource list and tells
// connected clients the list moved. Without it a client that has already listed
// goes on offering a file that is gone.
func (w *Writer) unregister(res *resource.Resource) {
	if w.unregistered != nil {
		w.unregistered(res.URI)
	}
}
