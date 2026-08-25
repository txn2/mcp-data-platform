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

	// ListByAsset returns one asset's references in declared order.
	ListByAsset(ctx context.Context, assetID string) ([]AssetResourceRef, error)

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
