package assetrefs_test

import (
	"context"
	"errors"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// errStore is what a fake returns when a test asks for a database failure, so
// the "read failed" and "row is absent" paths stay distinguishable.
var errStore = errors.New("database unavailable")

// fakeRefs is an in-memory AssetContentRefStore.
//
// Its GetByToken models the Postgres store's contract exactly: no such
// reference is (nil, nil), never an error. A fake that reported not-found as an
// error would let the handler's error branch pass here and diverge in
// production.
type fakeRefs struct {
	byAsset map[string][]assetrefs.Ref
	// listErr fails every read. byAssetErr fails only ListByAsset, which the
	// Postgres store answers with a different query than GetByToken: a serving
	// request resolves its token and then reads the target asset's own list,
	// and one of those two can fail while the other succeeds.
	listErr    error
	byAssetErr error
	putErr     error
	// replaced records the last Replace, so a test can assert what was written
	// rather than only that the call returned nil.
	replaced map[string][]assetrefs.Ref
}

func newFakeRefs() *fakeRefs {
	return &fakeRefs{
		byAsset:  map[string][]assetrefs.Ref{},
		replaced: map[string][]assetrefs.Ref{},
	}
}

func (f *fakeRefs) Replace(_ context.Context, assetID string, refs []assetrefs.Ref) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.replaced[assetID] = refs
	f.byAsset[assetID] = refs
	return nil
}

func (f *fakeRefs) ListByAsset(_ context.Context, assetID string) ([]assetrefs.Ref, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.byAssetErr != nil {
		return nil, f.byAssetErr
	}
	return f.byAsset[assetID], nil
}

func (f *fakeRefs) Attach(_ context.Context, ref assetrefs.Ref) (bool, error) {
	if f.putErr != nil {
		return false, f.putErr
	}
	for _, existing := range f.byAsset[ref.AssetID] {
		if existing.TargetKind == ref.TargetKind && existing.TargetID == ref.TargetID {
			return false, nil
		}
	}
	f.byAsset[ref.AssetID] = append(f.byAsset[ref.AssetID], ref)
	return true, nil
}

func (f *fakeRefs) Detach(
	_ context.Context, assetID string, kind assetrefs.TargetKind, targetID string,
) (bool, error) {
	if f.putErr != nil {
		return false, f.putErr
	}
	kept := make([]assetrefs.Ref, 0, len(f.byAsset[assetID]))
	found := false
	for _, ref := range f.byAsset[assetID] {
		if ref.TargetKind == kind && ref.TargetID == targetID {
			found = true
			continue
		}
		kept = append(kept, ref)
	}
	f.byAsset[assetID] = kept
	return found, nil
}

// ListByTarget scans the same map the declaring side writes, so a test cannot
// see a reference from one side that the other never recorded.
func (f *fakeRefs) ListByTarget(
	_ context.Context, kind assetrefs.TargetKind, targetID string, limit int,
) ([]assetrefs.Ref, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []assetrefs.Ref
	for _, refs := range f.byAsset {
		for _, ref := range refs {
			if ref.TargetKind == kind && ref.TargetID == targetID && len(out) < limit {
				out = append(out, ref)
			}
		}
	}
	return out, nil
}

func (f *fakeRefs) GetByToken(_ context.Context, assetID, token string) (*assetrefs.Ref, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	for _, ref := range f.byAsset[assetID] {
		if ref.RefToken == token {
			return &ref, nil
		}
	}
	return nil, nil //nolint:nilnil // mirrors the Postgres store: no such reference is (nil, nil)
}

// fakeResources is an in-memory resource store keyed by both id and URI.
type fakeResources struct {
	byID   map[string]*resource.Resource
	getErr error
	uriErr error
}

func (f *fakeResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	res, ok := f.byID[id]
	if !ok {
		return nil, nil //nolint:nilnil // resource.Store reports a missing row as (nil, nil)
	}
	return res, nil
}

func (f *fakeResources) GetByIDs(_ context.Context, ids []string) (map[string]*resource.Resource, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := make(map[string]*resource.Resource, len(ids))
	for _, id := range ids {
		if res, ok := f.byID[id]; ok {
			out[id] = res
		}
	}
	return out, nil
}

func (f *fakeResources) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	if f.uriErr != nil {
		return nil, f.uriErr
	}
	for _, res := range f.byID {
		if res.URI == uri {
			return res, nil
		}
	}
	return nil, nil //nolint:nilnil // resource.Store reports a missing row as (nil, nil)
}

// fakeAssets is an in-memory asset store: the two reads the reference paths
// make, over a map a test fills.
//
// Get reports a missing asset the way the Postgres store does -- as an error,
// not as (nil, nil) -- because that is the contract the declaration path has to
// survive: it treats any read failure as "no such asset", and a fake that
// returned nil, nil would let a different branch pass here than runs in
// production.
type fakeAssets struct {
	byID   map[string]*portaldomain.Asset
	getErr error
}

func (f *fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	asset, ok := f.byID[id]
	if !ok {
		return nil, errNoAsset
	}
	return asset, nil
}

func (f *fakeAssets) GetByIDs(_ context.Context, ids []string) (map[string]*portaldomain.Asset, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := make(map[string]*portaldomain.Asset, len(ids))
	for _, id := range ids {
		if asset, ok := f.byID[id]; ok {
			out[id] = asset
		}
	}
	return out, nil
}

// errNoAsset is what the fake asset store reports for a missing row, matching
// the Postgres store's wrapped sql.ErrNoRows in shape if not in identity.
var errNoAsset = errors.New("querying asset: no rows in result set")

// fakeBlobs is an in-memory object store keyed by bucket/key.
type fakeBlobs struct {
	byKey  map[string]string
	err    error
	bucket string
}

func (f *fakeBlobs) GetObject(_ context.Context, bucket, key string) (body []byte, contentType string, err error) {
	if f.err != nil {
		return nil, "", f.err
	}
	f.bucket = bucket
	text, ok := f.byKey[key]
	if !ok {
		return nil, "", errors.New("NoSuchKey: the specified key does not exist")
	}
	return []byte(text), "", nil
}

// fixtureResources is the standard set: a globally readable logo, and a
// finance-only chart nobody outside that persona may read.
func fixtureResources() *fakeResources {
	return &fakeResources{byID: map[string]*resource.Resource{
		"res-logo": {
			ID: "res-logo", Scope: resource.ScopeGlobal, Filename: "logo.png",
			DisplayName: "Brand Logo", MIMEType: "image/png",
			S3Key: "resources/global/logo.png", URI: logoURI,
			UpdatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		"res-chart": {
			ID: "res-chart", Scope: resource.ScopePersona, ScopeID: "finance",
			Filename: "chart.png", DisplayName: "Forecast", MIMEType: "image/png",
			S3Key: "resources/persona/finance/chart.png",
			URI:   "mcp://persona/finance/chart.png",
		},
	}}
}
