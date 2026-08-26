package assetrefs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// ErrRefused marks every rejection a declaration can produce, so a caller can
// map it to a refusal the author reads rather than to an internal failure.
var ErrRefused = errors.New("reference refused")

// GrantNotice is the sentence a surface shows the author at the moment a
// reference is made. It names the consequence in the terms it matters in
// rather than in the terms the implementation is written in.
//
// It says "files" for both kinds because that is what a reference resolves to
// either way: a referenced asset is served as its stored content, not as a
// page with its own sharing controls.
const GrantNotice = "Anyone this asset is shared with can load these files through it, " +
	"including anyone holding a public link, now and later."

// errNoRefStore is the refusal a declaration gets where there is nowhere to
// record it. An author must never be told a reference was recorded when there
// was nothing to record it in.
func errNoRefStore() error {
	return fmt.Errorf("this deployment cannot record references: %w", ErrRefused)
}

// errNoResourceLayer is the refusal a resource declaration gets where there is
// no managed-resource layer to name.
func errNoResourceLayer() error {
	return fmt.Errorf("this deployment has no managed-resource layer to reference: %w", ErrRefused)
}

// errNoAssetLayer is the same answer for an asset reference on a deployment
// with no asset store bound.
func errNoAssetLayer() error {
	return fmt.Errorf("this deployment has no asset store to reference: %w", ErrRefused)
}

// Declared is one validated declaration: a target the author was proved able
// to read, waiting to be attached to an asset.
//
// Validation and attachment are separate steps because a new asset does not
// exist until its row is written, and a declaration naming a target the author
// cannot read has to be refused before anything is created. Resolve answers
// "may this author reference these things?" with nothing written; Apply records
// the answer once there is an asset to record it against.
type Declared struct {
	Kind     TargetKind
	TargetID string
	URI      string
}

// Author is the identity one declaration is checked against.
//
// It carries a permission per target kind because the two kinds are held under
// different models and neither can be derived from the other: a managed
// resource is reached through scope claims, while an asset is reached through
// ownership and shares. Both arrive from the caller rather than being resolved
// here, because the two doors a declaration comes through -- an agent's save
// and a person's add in the portal -- resolve identity in their own terms, and
// this package must not become a third opinion on who anybody is.
type Author struct {
	// Claims is the author's managed-resource permission, built by
	// resource.BuildClaims.
	Claims resource.Claims
	// ReadsAsset reports whether the author may read a referenced asset. A nil
	// func refuses every asset reference, which is the right answer for a
	// caller that has not established an identity to check against.
	ReadsAsset func(ctx context.Context, asset *portaldomain.Asset) bool
}

// Assets is the asset layer as this package reads it: one asset by id, and a
// whole set at once. portaldomain.AssetStore satisfies it.
//
// It is the same shape Resources has and for the same reason: declaring a
// reference, serving one, and re-checking a copied asset's references are the
// two ends and the middle of one fact -- this asset names that thing -- and
// splitting the dependency would let them drift onto different notions of what
// a referenceable asset is.
type Assets interface {
	Get(ctx context.Context, id string) (*portaldomain.Asset, error)
	GetByIDs(ctx context.Context, ids []string) (map[string]*portaldomain.Asset, error)
}

// Declarer records the things an asset's content references, after checking
// each one against the author who declared it.
//
// The check is the author's own read permission at the moment of the save, and
// it is the only check there ever is: once declared, the reference carries the
// asset's audience, so anyone who can open the asset can load the target
// through it -- including an anonymous viewer of a public share. That is the
// grant model a managed script already uses, where a run acts as its version
// author rather than as its caller (#1419).
//
// The rule is the same for an asset target as for a resource (#1488). It is
// deliberately not the referenced asset's own shares: the reference is served
// to a reader with no session, inside a sandboxed frame or on a public link,
// so there is no reader identity to resolve those shares against. What
// protects the referenced asset is that the author had to be able to read it,
// and that the notice states the consequence before the reference is made.
type Declarer struct {
	refs      Store
	assets    Assets
	resources Resources
	scheme    string
}

// NewDeclarer builds the declaration path over the reference store and the
// asset store. The managed-resource layer is bound afterwards through
// BindResources, because it is assembled after the portal layer is.
func NewDeclarer(refs Store, assets Assets) *Declarer {
	return &Declarer{refs: refs, assets: assets}
}

// BindResources gives the declarer the managed-resource layer, which is what
// admits an mcp:// URI. scheme is the deployment's resource URI scheme; empty
// selects resource.DefaultURIScheme.
//
// A declarer that is never bound still records asset references: the two kinds
// are independent, and a deployment with no resource library is not a
// deployment with no assets.
func (d *Declarer) BindResources(resources Resources, scheme string) {
	if d == nil {
		return
	}
	d.resources = resources
	d.scheme = scheme
}

// Available reports whether this deployment can record references at all. A
// declaration made against an unavailable declarer is refused rather than
// silently dropped, so an author is never told a reference was recorded when
// nothing was.
func (d *Declarer) Available() bool {
	return d != nil && d.refs != nil
}

// Resolve validates a declaration and returns what it names, writing nothing.
//
// selfID is the asset the declaration is for, empty when it does not exist yet
// (a create). A reference naming that asset is refused: the serving route
// answers such a reference rather than following it, so nothing breaks, but it
// resolves to the very content it was written in and there is no reading of it
// the author could have meant. The portal's own add refuses it in the same
// words, so the two doors onto one mechanism agree about what a legal
// reference is.
//
// Every reference is checked before any is accepted, so a declaration naming
// one unreadable target yields none of the others: a partially applied
// declaration would leave the asset's markup referring to things that resolve
// for some readers and not others, with no record of which.
//
// Each entry is trimmed before it is checked and recorded as trimmed, because
// the URI is what the rewrite matches on: a padded entry that resolved and was
// stored with its padding would match nothing in the content, and the save
// would report a reference that never renders.
//
// Duplicates collapse to the first occurrence, which keeps the declared order
// meaningful and stops one target consuming two slots of the cap.
func (d *Declarer) Resolve(
	ctx context.Context, uris []string, author Author, selfID string,
) ([]Declared, error) {
	if !d.Available() {
		return nil, errNoRefStore()
	}
	if len(uris) > MaxRefs {
		return nil, fmt.Errorf("at most %d references per asset, and %d were declared: %w",
			MaxRefs, len(uris), ErrRefused)
	}

	out := make([]Declared, 0, len(uris))
	seen := make(map[string]bool, len(uris))
	for _, raw := range uris {
		uri := strings.TrimSpace(raw)
		if seen[uri] {
			continue
		}
		seen[uri] = true
		declared, err := d.readable(ctx, uri, author)
		if err != nil {
			return nil, err
		}
		if declared.Kind == TargetAsset && declared.TargetID == selfID && selfID != "" {
			return nil, fmt.Errorf("an asset cannot reference itself: %w", ErrRefused)
		}
		out = append(out, declared)
	}
	return out, nil
}

// Apply replaces the asset's references with exactly what Resolve returned, and
// returns what was recorded. An empty declared clears every reference the asset
// had; callers that mean "leave the references alone" must not call this at
// all, because a save that never mentioned references has decided nothing about
// them.
func (d *Declarer) Apply(
	ctx context.Context, assetID string, declared []Declared, author string,
) ([]Ref, error) {
	if !d.Available() {
		return nil, errNoRefStore()
	}

	existing, err := d.refs.ListByAsset(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("reading existing references: %w", err)
	}
	// A reference that survives a save keeps the token it already had, so a URL
	// already rendered into a reader's open page does not break every time the
	// author saves the asset again.
	tokens := make(map[string]string, len(existing))
	for _, ref := range existing {
		tokens[refKey(ref.TargetKind, ref.TargetID)] = ref.RefToken
	}

	refs := make([]Ref, 0, len(declared))
	for i, dec := range declared {
		token, err := tokenFor(tokens, refKey(dec.Kind, dec.TargetID))
		if err != nil {
			return nil, err
		}
		refs = append(refs, Ref{
			AssetID:    assetID,
			TargetKind: dec.Kind,
			TargetID:   dec.TargetID,
			URI:        dec.URI,
			RefToken:   token,
			Position:   i,
			DeclaredBy: author,
		})
	}
	if err := d.refs.Replace(ctx, assetID, refs); err != nil {
		return nil, fmt.Errorf("recording references: %w", err)
	}
	for _, ref := range refs {
		LogGranted(assetID, ref)
	}
	return refs, nil
}

// refKey identifies a reference within one asset. The kind is part of it
// because a resource id and an asset id are separate id spaces: the same string
// can name both, and carrying one's token over to the other would serve the
// wrong thing through a URL a reader already holds.
func refKey(kind TargetKind, targetID string) string {
	return string(kind) + ":" + targetID
}

// readable resolves one declared URI to what it names and checks the author may
// read it. Every refusal names the URI, because that is the string the author
// wrote and the only one they can find in their own content.
//
// The two forms are told apart by their syntax and not by trying each in turn:
// a managed resource is named by an mcp:// URI whose path is its scope and
// filename, and an asset by the mcp:asset:<id> reference every search hit and
// fetch document already carries. Anything else is refused naming both forms,
// so an author who wrote a plain URL learns what the field takes.
func (d *Declarer) readable(ctx context.Context, uri string, author Author) (Declared, error) {
	if strings.HasPrefix(uri, d.resourceScheme()+"://") {
		return d.readableResource(ctx, uri, author.Claims)
	}
	if ref, err := knowledgepage.ParseEntityRef(uri); err == nil &&
		ref.TargetType == knowledgepage.RefTargetAsset {
		return d.readableAsset(ctx, uri, ref.AssetID, author)
	}
	return Declared{}, fmt.Errorf(
		"cannot reference %q: a reference names a managed resource by its %s:// URI "+
			"or an asset by its mcp:asset:<id> reference: %w", uri, d.resourceScheme(), ErrRefused)
}

// resourceScheme is the deployment's resource URI scheme, defaulted the same
// way resource.ParseURI defaults it so the prefix this file matches on and the
// one that package parses cannot disagree.
func (d *Declarer) resourceScheme() string {
	if d.scheme == "" {
		return resource.DefaultURIScheme
	}
	return d.scheme
}

// readableResource resolves an mcp:// URI to the managed resource it names and
// checks the author may read it.
func (d *Declarer) readableResource(
	ctx context.Context, uri string, claims resource.Claims,
) (Declared, error) {
	if d.resources == nil {
		return Declared{}, errNoResourceLayer()
	}
	if _, err := resource.ParseURI(d.scheme, uri); err != nil {
		return Declared{}, fmt.Errorf("cannot reference %q: it is not a managed resource URI: %w", uri, ErrRefused)
	}
	res, err := d.resources.GetByURI(ctx, uri)
	if err != nil {
		return Declared{}, fmt.Errorf("resolving resource reference %q: %w", uri, err)
	}
	if res == nil {
		return Declared{}, fmt.Errorf("no managed resource at %q: %w", uri, ErrRefused)
	}
	if !resource.CanReadResource(claims, res) {
		// The refusal names the URI the author wrote and nothing about the
		// resource behind it: a caller who cannot read a file must not learn
		// its name or its scope from being refused.
		return Declared{}, fmt.Errorf("you cannot read the resource at %q: %w", uri, ErrRefused)
	}
	return Declared{Kind: TargetResource, TargetID: res.ID, URI: uri}, nil
}

// readableAsset resolves an mcp:asset:<id> reference to the asset it names and
// checks the author may read it (#1488).
//
// A store failure is answered as "no such asset", the same conflation every
// other asset read in the portal makes, because the asset store reports a
// missing row as an error and no implementation distinguishes the two. The
// underlying error is logged rather than dropped, so an operator can still tell
// an outage from a typo -- which the author, being told only that the reference
// did not resolve, cannot.
func (d *Declarer) readableAsset(
	ctx context.Context, uri, assetID string, author Author,
) (Declared, error) {
	if d.assets == nil {
		return Declared{}, errNoAssetLayer()
	}
	asset, err := d.assets.Get(ctx, assetID)
	if err != nil {
		slog.Warn("asset reference: reading the referenced asset failed",
			"asset_id", logsan.SanitizeForLog(assetID),
			"error", logsan.SanitizeForLog(err.Error()))
	}
	if asset == nil || asset.DeletedAt != nil {
		return Declared{}, fmt.Errorf("no asset at %q: %w", uri, ErrRefused)
	}
	if author.ReadsAsset == nil || !author.ReadsAsset(ctx, asset) {
		// As with a resource, the refusal says nothing about the asset behind
		// the reference: a caller who cannot read it must not learn its name
		// from being refused.
		return Declared{}, fmt.Errorf("you cannot read the asset at %q: %w", uri, ErrRefused)
	}
	return Declared{Kind: TargetAsset, TargetID: asset.ID, URI: uri}, nil
}

// tokenFor returns the reference's existing token, minting one when the asset
// is naming this target for the first time.
func tokenFor(tokens map[string]string, key string) (string, error) {
	if token, ok := tokens[key]; ok && token != "" {
		return token, nil
	}
	token, err := portaldomain.GenerateRefToken()
	if err != nil {
		return "", fmt.Errorf("minting reference token: %w", err)
	}
	return token, nil
}

// LogGranted records one grant. The reference row itself carries who declared
// it and when; this puts the same facts -- asset, target, author -- in the
// operator's log at the moment the grant widens the target's audience to the
// asset's.
//
// It is exported because a reference is made from two doors: an agent's save,
// through Apply above, and a person's add through the portal panel (#1475). An
// operator's log that covered only one of them would show half the grants and
// look complete, so both call this rather than each writing its own record.
func LogGranted(assetID string, ref Ref) {
	slog.Info("asset_reference.granted",
		"asset_id", logsan.SanitizeForLog(assetID),
		"target_kind", logsan.SanitizeForLog(string(ref.TargetKind)),
		"target_id", logsan.SanitizeForLog(ref.TargetID),
		"uri", logsan.SanitizeForLog(ref.URI),
		"declared_by", logsan.SanitizeForLog(ref.DeclaredBy))
}

// LogRevoked records the withdrawal of one grant, so the log that shows a
// target's audience widening also shows it narrowing again. A save that drops a
// reference does not pass through here -- Replace states the whole list, and
// what it dropped is the difference between two lists rather than an act --
// but a person removing one has done exactly one thing, and it is worth the
// line.
func LogRevoked(assetID string, kind TargetKind, targetID, actor string) {
	slog.Info("asset_reference.revoked",
		"asset_id", logsan.SanitizeForLog(assetID),
		"target_kind", logsan.SanitizeForLog(string(kind)),
		"target_id", logsan.SanitizeForLog(targetID),
		"revoked_by", logsan.SanitizeForLog(actor))
}
