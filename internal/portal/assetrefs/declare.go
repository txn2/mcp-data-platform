package assetrefs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// ErrRefused marks every rejection a declaration can produce, so a caller can
// map it to a refusal the author reads rather than to an internal failure.
var ErrRefused = errors.New("resource reference refused")

// GrantNotice is the sentence a surface shows the author at the moment a
// reference is made. It names the consequence in the terms it matters in
// rather than in the terms the implementation is written in.
const GrantNotice = "Anyone this asset is shared with can load these files through it, " +
	"including anyone holding a public link, now and later."

// errNoResourceLayer is the refusal a declaration gets where there is nothing
// to declare against. It is one function because both entry points give the
// same answer, and an author must never be told a reference was recorded when
// there was nowhere to record it.
func errNoResourceLayer() error {
	return fmt.Errorf("this deployment has no managed-resource layer to reference: %w", ErrRefused)
}

// Declared is one validated declaration: a resource the author was proved able
// to read, waiting to be attached to an asset.
//
// Validation and attachment are separate steps because a new asset does not
// exist until its row is written, and a declaration naming a resource the
// author cannot read has to be refused before anything is created. Resolve
// answers "may this author reference these files?" with nothing written; Apply
// records the answer once there is an asset to record it against.
type Declared struct {
	ResourceID string
	URI        string
}

// Declarer records the managed resources an asset's content references, after
// checking each one against the author who declared it.
//
// The check is the author's own read permission at the moment of the save, and
// it is the only check there ever is: once declared, the reference carries the
// asset's audience, so anyone who can open the asset can load the file through
// it -- including an anonymous viewer of a public share. That is the grant
// model a managed script already uses, where a run acts as its version author
// rather than as its caller (#1419).
type Declarer struct {
	refs      portaldomain.AssetResourceRefStore
	resources Resources
	scheme    string
}

// NewDeclarer builds the declaration path. scheme is the deployment's resource
// URI scheme; empty selects resource.DefaultURIScheme.
func NewDeclarer(refs portaldomain.AssetResourceRefStore, resources Resources, scheme string) *Declarer {
	return &Declarer{refs: refs, resources: resources, scheme: scheme}
}

// Available reports whether this deployment can record references at all. A
// declaration made against an unavailable declarer is refused rather than
// silently dropped, so an author is never told a reference was recorded when
// nothing was.
func (d *Declarer) Available() bool {
	return d != nil && d.refs != nil && d.resources != nil
}

// Resolve validates a declaration and returns what it names, writing nothing.
//
// Every URI is checked before any is accepted, so a declaration naming one
// unreadable resource yields none of the others: a partially applied
// declaration would leave the asset's markup referring to files that resolve
// for some readers and not others, with no record of which.
//
// Duplicates collapse to the first occurrence, which keeps the declared order
// meaningful and stops one file consuming two slots of the cap.
func (d *Declarer) Resolve(ctx context.Context, uris []string, claims resource.Claims) ([]Declared, error) {
	if !d.Available() {
		return nil, errNoResourceLayer()
	}
	if len(uris) > portaldomain.MaxAssetResourceRefs {
		return nil, fmt.Errorf("at most %d resource references per asset, and %d were declared: %w",
			portaldomain.MaxAssetResourceRefs, len(uris), ErrRefused)
	}

	out := make([]Declared, 0, len(uris))
	seen := make(map[string]bool, len(uris))
	for _, uri := range uris {
		if seen[uri] {
			continue
		}
		seen[uri] = true
		res, err := d.readable(ctx, uri, claims)
		if err != nil {
			return nil, err
		}
		out = append(out, Declared{ResourceID: res.ID, URI: uri})
	}
	return out, nil
}

// Apply replaces the asset's references with exactly what Resolve returned, and
// returns what was recorded. An empty declared clears every reference the asset
// had; callers that mean "leave the references alone" must not call this at
// all, because a save that never mentioned resources has decided nothing about
// them.
func (d *Declarer) Apply(
	ctx context.Context, assetID string, declared []Declared, author string,
) ([]portaldomain.AssetResourceRef, error) {
	if !d.Available() {
		return nil, errNoResourceLayer()
	}

	existing, err := d.refs.ListByAsset(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("reading existing resource references: %w", err)
	}
	// A reference that survives a save keeps the token it already had, so a URL
	// already rendered into a reader's open page does not break every time the
	// author saves the asset again.
	tokens := make(map[string]string, len(existing))
	for _, ref := range existing {
		tokens[ref.ResourceID] = ref.RefToken
	}

	refs := make([]portaldomain.AssetResourceRef, 0, len(declared))
	for i, dec := range declared {
		token, err := tokenFor(tokens, dec.ResourceID)
		if err != nil {
			return nil, err
		}
		refs = append(refs, portaldomain.AssetResourceRef{
			AssetID:    assetID,
			ResourceID: dec.ResourceID,
			URI:        dec.URI,
			RefToken:   token,
			Position:   i,
			DeclaredBy: author,
		})
	}
	if err := d.refs.Replace(ctx, assetID, refs); err != nil {
		return nil, fmt.Errorf("recording resource references: %w", err)
	}
	logGrants(assetID, refs)
	return refs, nil
}

// readable resolves one declared URI to the resource it names and checks the
// author may read it. Every refusal names the URI, because that is the string
// the author wrote and the only one they can find in their own content.
func (d *Declarer) readable(ctx context.Context, uri string, claims resource.Claims) (*resource.Resource, error) {
	if _, err := resource.ParseURI(d.scheme, uri); err != nil {
		return nil, fmt.Errorf("cannot reference %q: it is not a managed resource URI: %w", uri, ErrRefused)
	}
	res, err := d.resources.GetByURI(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("resolving resource reference %q: %w", uri, err)
	}
	if res == nil {
		return nil, fmt.Errorf("no managed resource at %q: %w", uri, ErrRefused)
	}
	if !resource.CanReadResource(claims, res) {
		// The refusal names the URI the author wrote and nothing about the
		// resource behind it: a caller who cannot read a file must not learn
		// its name or its scope from being refused.
		return nil, fmt.Errorf("you cannot read the resource at %q: %w", uri, ErrRefused)
	}
	return res, nil
}

// tokenFor returns the reference's existing token, minting one when the asset
// is naming this resource for the first time.
func tokenFor(tokens map[string]string, resourceID string) (string, error) {
	if token, ok := tokens[resourceID]; ok && token != "" {
		return token, nil
	}
	token, err := portaldomain.GenerateRefToken()
	if err != nil {
		return "", fmt.Errorf("minting reference token: %w", err)
	}
	return token, nil
}

// logGrants records each grant the declaration made. The reference row itself
// carries who declared it and when; this puts the same three facts -- asset,
// resource, author -- in the operator's log at the moment the grant widens the
// file's audience to the asset's.
func logGrants(assetID string, refs []portaldomain.AssetResourceRef) {
	for _, ref := range refs {
		slog.Info("asset_resource_reference.granted",
			"asset_id", logsan.SanitizeForLog(assetID),
			"resource_id", logsan.SanitizeForLog(ref.ResourceID),
			"uri", logsan.SanitizeForLog(ref.URI),
			"declared_by", logsan.SanitizeForLog(ref.DeclaredBy))
	}
}
