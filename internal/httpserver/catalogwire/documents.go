package catalogwire

import (
	"bytes"
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
	"github.com/txn2/mcp-data-platform/internal/platform/catalogscan"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// catalogPrincipal is who writes a connection's schema document: the
// platform's catalog scan, not a person.
const catalogPrincipal = "system:catalog"

// documentWriter is the part of resourcewrite.Writer the keeper uses.
type documentWriter interface {
	Create(ctx context.Context, in resource.NewResource, claims resource.Claims) (*resource.Resource, error)
	ReplaceIfChanged(ctx context.Context, id string, content []byte, mimeType string, claims resource.Claims) (*resource.Resource, bool, error)
}

// documentKeeper keeps each connection's schema documents as managed
// resources in the global library, under catalog/<kind>/<connection>, one
// resource per document that is revised only when the document changes
// (#1918).
type documentKeeper struct {
	w         documentWriter
	byURI     uriLookup
	uriScheme string
}

// uriLookup finds a resource by its address.
type uriLookup interface {
	GetByURI(ctx context.Context, uri string) (*resource.Resource, error)
}

var _ catalogscan.DocumentKeeper = documentKeeper{}

// systemClaims are the catalog scan's authority to write the global library.
func systemClaims() resource.Claims {
	return resource.BuildClaims(catalogPrincipal, "", "", nil, true)
}

// documentFolder is where a connection's schema documents are kept.
func documentFolder(kind, connection string) string {
	return "catalog/" + kind + "/" + connection
}

// Keep stores a document, or records it as the next version of the resource
// already holding it, and returns that resource's id.
func (k documentKeeper) Keep(ctx context.Context, kind, connection string, doc catalogscan.Document) (string, error) {
	folder := documentFolder(kind, connection)
	uri := resource.BuildURI(k.uriScheme, resource.ScopeGlobal, "", folder, doc.Filename)
	found, err := k.byURI.GetByURI(ctx, uri)
	if err != nil && !resource.IsNotFound(err) {
		return "", fmt.Errorf("looking up %s: %w", uri, err)
	}
	if found != nil {
		res, _, err := k.w.ReplaceIfChanged(ctx, found.ID, doc.Content, doc.MIMEType, systemClaims())
		if err != nil {
			return "", fmt.Errorf("revising %s: %w", uri, err)
		}
		return res.ID, nil
	}
	res, err := k.w.Create(ctx, resource.NewResource{
		Scope: resource.ScopeGlobal, Path: folder, Filename: doc.Filename,
		DisplayName: connection + " " + doc.Filename,
		Description: "The schema document the " + kind + " connection " + connection +
			" is read from, kept by the platform catalog's scan.",
		Tags:     []string{"catalog", "schema", kind},
		Content:  bytes.NewReader(doc.Content),
		MIMEType: doc.MIMEType,
		Origin:   indexpolicy.OriginCatalog,
	}, systemClaims())
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", uri, err)
	}
	return res.ID, nil
}
