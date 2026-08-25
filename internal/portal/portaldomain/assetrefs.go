package portaldomain

import (
	"context"
	"time"
)

// MaxAssetResourceRefs bounds how many managed resources one asset may
// reference. The cap exists because every reference costs a row, a token, and
// one HTTP request per render, and because an unbounded list is a way to turn
// a single save into an arbitrary amount of serving work.
//
// The constant lives here with the rest of the reference vocabulary; it is
// enforced at the one door a declaration comes through, which states the
// number in its refusal so an author learns it rather than guessing.
const MaxAssetResourceRefs = 20

// AssetResourceRef links an asset to a managed resource its content names
// (#1474): the logo the report shows, the photograph the page embeds.
//
// The asset's stored content names the resource by its mcp:// URI and nothing
// else; the bytes stay in the resource. Every viewing surface rewrites the
// declared URIs into the serving URL below as it serves, so a reference costs
// the asset one URI in its markup rather than a copy of the file in every
// version it retains.
type AssetResourceRef struct {
	AssetID    string `json:"asset_id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
	ResourceID string `json:"resource_id" example:"res_01HK7R9F"`

	// URI is the mcp:// form the content names the resource by, recorded as
	// declared. It is what the rewrite matches on, so it is stored rather than
	// rebuilt: a resource renamed after the declaration keeps rendering,
	// because the content still says what it said when it was written.
	URI string `json:"uri" example:"mcp://global/brand/logo.png"`

	// RefToken is the capability the reference is served under. Possession of
	// it is the whole authorization: an asset's content renders inside a
	// sandboxed iframe with an opaque origin, and on a public share for a
	// reader with no session at all, so the URL written into served content
	// cannot rely on the reader's own credentials. The token reaches a reader
	// only inside content they were already allowed to open, which is the
	// audience the reference is meant to carry.
	RefToken string `json:"-"`

	// Position is the order the author declared, starting at 0. It orders the
	// rewrite so a URI that is a prefix of another cannot clobber it; see
	// SortRefsForRewrite.
	Position int `json:"position" example:"0"`

	// DeclaredBy is the address of the author whose read permission admitted
	// this reference. It is the durable record of who made the grant, kept for
	// the reason an approval stamp is kept.
	DeclaredBy string `json:"declared_by,omitempty" example:"analyst@example.com"`

	CreatedAt time.Time `json:"created_at"`
}

// AssetResourceRefStore persists the asset-to-resource references.
type AssetResourceRefStore interface {
	// Replace rewrites one asset's references to exactly refs, minting no
	// tokens of its own — the caller supplies them. It is Replace rather than
	// Attach because a save declares the whole list: a save that names two
	// resources where the previous named three has dropped one, and the
	// dropped one must stop resolving.
	//
	// A reference that survives the write keeps its existing token, so a URL
	// already rendered into a reader's page does not break every time the
	// author saves. Passing an empty list removes every reference.
	Replace(ctx context.Context, assetID string, refs []AssetResourceRef) error

	// Attach adds one reference to an asset that does not already have it,
	// reporting whether it was added. It exists beside Replace because the two
	// have different writers: a save declares the whole list, while a person
	// adding one file through the portal has decided nothing about the others.
	// Rewriting the list from a read would silently drop whatever a concurrent
	// save had just declared.
	//
	// The reference lands at the end of the declared order, and an asset that
	// already names the resource is (false, nil) rather than an error: the
	// primary key decides it, so two callers racing on the same file cannot
	// both win.
	Attach(ctx context.Context, ref AssetResourceRef) (bool, error)

	// Detach removes one reference, reporting whether there was one. It is the
	// counterpart of Attach and leaves every other reference untouched, for
	// the same reason.
	Detach(ctx context.Context, assetID, resourceID string) (bool, error)

	// ListByAsset returns one asset's references in declared order.
	ListByAsset(ctx context.Context, assetID string) ([]AssetResourceRef, error)

	// ListByResource returns at most limit references naming one resource,
	// across every asset that declares it. It answers "what is holding this
	// file up?" for the person about to edit or delete the resource, which is
	// the question a reference makes askable and nothing else on the resource
	// can answer.
	//
	// It is deliberately unscoped: a reference row carries no notion of who
	// may see the asset that owns it. The caller narrows the answer to the
	// assets its reader is allowed to open, because that check needs the asset
	// and its shares, neither of which this store holds -- and it costs a
	// query per asset, which is why the limit is here rather than applied to
	// the rows after they arrive.
	ListByResource(ctx context.Context, resourceID string, limit int) ([]AssetResourceRef, error)

	// GetByToken resolves the reference a serving URL names. It takes the
	// asset id as well as the token and requires both to match, so a token
	// pasted onto another asset's path resolves to nothing rather than to the
	// resource it names.
	//
	// No such reference is (nil, nil), not an error: a token that names
	// nothing is an ordinary answer on a route whose whole audience is
	// callers holding a URL, and an error would put a database failure and a
	// wrong token in the same bucket.
	GetByToken(ctx context.Context, assetID, token string) (*AssetResourceRef, error)
}

// GenerateRefToken mints the capability one asset resource reference is served
// under. It is the same generator and the same 256 bits a share token gets,
// because it is the same kind of secret: the URL is the authorization, so the
// token has to be as unguessable as the share link that carries a whole asset.
func GenerateRefToken() (string, error) { return generateToken() }
