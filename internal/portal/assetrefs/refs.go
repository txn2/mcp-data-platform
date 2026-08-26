package assetrefs

import (
	"context"
	"time"
)

// The vocabulary of a reference: what an asset may name, the row that records
// it, and the store that persists it.
//
// It lives here rather than in portaldomain because it is the reference
// mechanism's own model and touches nothing else in the portal's domain: the
// declaration path, the serving route and the rewrite in this package are its
// only readers, and the Postgres store in internal/portal/assetrefstore is its
// only writer.

// MaxRefs bounds how many things one asset may reference. The cap exists
// because every reference costs a row, a token, and one HTTP request per
// render, and because an unbounded list is a way to turn a single save into an
// arbitrary amount of serving work.
//
// The constant lives here with the rest of the reference vocabulary; it is
// enforced at the one door a declaration comes through, which states the
// number in its refusal so an author learns it rather than guessing.
const MaxRefs = 20

// TargetKind names what a reference points at. Both kinds resolve through
// the same token, the same serving route and the same rewrite; the kind decides
// only which store the target is read from and which permission admitted the
// declaration.
type TargetKind string

// The reference target kinds. The strings are stored in target_kind and are
// the same words the platform's reference vocabulary uses (mcp:resource:<id>,
// mcp:asset:<id>), so a row and a reference string cannot come to disagree
// about what a kind is called.
const (
	// TargetResource is a managed resource: a file somebody uploaded
	// (#1474).
	TargetResource TargetKind = "resource"
	// TargetAsset is another saved asset, whose current content the
	// reference resolves to (#1488). It is what lets a scheduled script
	// refresh a data file and a dashboard read it without being re-saved.
	TargetAsset TargetKind = "asset"
)

// Valid reports whether k is a kind the platform can resolve. A row read back
// with anything else is data from a future version and is refused rather than
// guessed at.
func (k TargetKind) Valid() bool {
	return k == TargetResource || k == TargetAsset
}

// Ref links an asset to something its content names (#1474,
// #1488): the logo the report shows, the photograph the page embeds, the data
// file the dashboard reads.
//
// The asset's stored content names the target by its reference string and
// nothing else; the bytes stay where they are. Every viewing surface rewrites
// the declared strings into the serving URL below as it serves, so a reference
// costs the asset one URI in its markup rather than a copy of the file in every
// version it retains -- and a target that changes is current everywhere that
// names it, with no asset re-saved.
type Ref struct {
	AssetID string `json:"asset_id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`

	// TargetKind says what TargetID names, and is part of the identity of a
	// reference: an asset may name a resource and an asset that happen to
	// share an id, and the two are different references.
	TargetKind TargetKind `json:"target_kind" example:"resource"`

	// TargetID is the referenced thing's id, in the id space of its kind.
	TargetID string `json:"target_id" example:"res_01HK7R9F"`

	// URI is the form the content names the target by, recorded as declared:
	// an mcp:// resource URI, or an mcp:asset:<id> reference. It is what the
	// rewrite matches on, so it is stored rather than rebuilt: a target renamed
	// after the declaration keeps rendering, because the content still says
	// what it said when it was written.
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

// Store persists the references an asset's content makes.
type Store interface {
	// Replace rewrites one asset's references to exactly refs, minting no
	// tokens of its own — the caller supplies them. It is Replace rather than
	// Attach because a save declares the whole list: a save that names two
	// targets where the previous named three has dropped one, and the dropped
	// one must stop resolving.
	//
	// A reference that survives the write keeps its existing token, so a URL
	// already rendered into a reader's page does not break every time the
	// author saves. Passing an empty list removes every reference.
	Replace(ctx context.Context, assetID string, refs []Ref) error

	// Attach adds one reference to an asset that does not already have it,
	// reporting whether it was added. It exists beside Replace because the two
	// have different writers: a save declares the whole list, while a person
	// adding one file through the portal has decided nothing about the others.
	// Rewriting the list from a read would silently drop whatever a concurrent
	// save had just declared.
	//
	// The reference lands at the end of the declared order, and an asset that
	// already names the target is (false, nil) rather than an error: the
	// primary key decides it, so two callers racing on the same target cannot
	// both win.
	Attach(ctx context.Context, ref Ref) (bool, error)

	// Detach removes one reference, reporting whether there was one. It is the
	// counterpart of Attach and leaves every other reference untouched, for
	// the same reason. The kind is part of the key, so detaching a resource
	// cannot remove an asset reference that shares its id.
	Detach(ctx context.Context, assetID string, kind TargetKind, targetID string) (bool, error)

	// ListByAsset returns one asset's references in declared order.
	ListByAsset(ctx context.Context, assetID string) ([]Ref, error)

	// ListByTarget returns at most limit references naming one target, across
	// every asset that declares it. It answers "what is holding this up?" for
	// the person about to edit or delete the file or asset, which is the
	// question a reference makes askable and nothing else on the target can
	// answer.
	//
	// It is deliberately unscoped: a reference row carries no notion of who
	// may see the asset that owns it. The caller narrows the answer to the
	// assets its reader is allowed to open, because that check needs the asset
	// and its shares, neither of which this store holds -- and it costs a
	// query per asset, which is why the limit is here rather than applied to
	// the rows after they arrive.
	ListByTarget(ctx context.Context, kind TargetKind, targetID string, limit int) ([]Ref, error)

	// GetByToken resolves the reference a serving URL names. It takes the
	// asset id as well as the token and requires both to match, so a token
	// pasted onto another asset's path resolves to nothing rather than to the
	// target it names.
	//
	// No such reference is (nil, nil), not an error: a token that names
	// nothing is an ordinary answer on a route whose whole audience is
	// callers holding a URL, and an error would put a database failure and a
	// wrong token in the same bucket.
	GetByToken(ctx context.Context, assetID, token string) (*Ref, error)
}
