// Package producedview holds the read-side views over the producer relation
// (#1569): what one script has produced, and which script producers still
// exist.
//
// The relation in internal/producedby is deliberately keyless in both
// directions -- no foreign key to scripts, to portal assets or to resources --
// so that deleting any of them leaves the record of what wrote what standing.
// The cost of that is exactly what this package pays: a row names an id, and a
// surface that wants to display it has to resolve that id against what exists
// now, and be able to say when nothing does.
//
// It sits apart from the surfaces that render it because both ends need it and
// they live in different places: the file end is served by the portal's
// producer routes, the script end beside the script routes.
//
//nolint:revive // max-public-structs: this package's exported surface is one cohesive read view (the item a producer listing renders, the reader that composes it, and the four lookup contracts the ids in the relation resolve through), not a heap of unrelated types.
package producedview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The kinds an Item's TargetKind takes, as the relation records them. They are
// restated here so a surface that renders items reads the vocabulary from the
// view it reads the items from.
const (
	TargetAsset      = producedby.TargetAsset
	TargetCollection = producedby.TargetCollection
	TargetResource   = producedby.TargetResource
)

// Item is one file a producer has produced or modified.
type Item struct {
	// TargetKind is TargetAsset, TargetCollection or TargetResource; TargetID
	// is the id within that kind.
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
	// Name is what the file is called now, empty when it no longer exists.
	Name string `json:"name,omitempty"`
	// OwnerEmail is the address the file's row records as its owner, for an
	// asset or a collection. It is what says whether the script's owner can
	// reach the file: a transfer moves the script and, unless it was asked to,
	// leaves the address on every file the script wrote as it was (#1588).
	// Empty for a resource, which is filed by library rather than by address,
	// and for a file that no longer exists.
	OwnerEmail string `json:"owner_email,omitempty"`
	// Created marks a file this producer brought into existence, as against one
	// it has only changed since.
	Created      bool      `json:"created"`
	FirstWriteAt time.Time `json:"first_write_at"`
	LastWriteAt  time.Time `json:"last_write_at"`
	WriteCount   int       `json:"write_count"`
	// LastVersion is the file version this producer last wrote, or zero for a
	// file whose kind does not number its writes.
	LastVersion int `json:"last_version"`
	// Deleted reports a file that has since been removed. The row stays: that
	// this producer wrote it is still true, and somebody deciding whether to
	// retire a script needs to see what it wrote that is already gone.
	Deleted bool `json:"deleted,omitempty"`
}

// AssetNames resolves asset ids to the assets they name, in one read.
type AssetNames interface {
	GetByIDs(ctx context.Context, ids []string) (map[string]*portaldomain.Asset, error)
}

// ResourceNames resolves one resource id to its record.
type ResourceNames interface {
	Get(ctx context.Context, id string) (*resource.Resource, error)
}

// CollectionNames resolves one collection id to its record. The store answers
// a missing id with an error wrapping sql.ErrNoRows and a removed one with a
// record carrying its deletion time; both read as gone here.
type CollectionNames interface {
	Get(ctx context.Context, id string) (*portaldomain.Collection, error)
}

// ScriptLookup resolves one script id to its record. A nil record with a nil
// error is the store's not-found contract and means the script is gone.
type ScriptLookup interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
}

// Reader composes the producer relation with what the ids in it resolve to now.
//
// Every collaborator is optional. A deployment with no asset store leaves an
// asset row unnamed rather than unlisted: that this script wrote something is
// the fact the list exists to report, and not being able to say what it is
// called is a smaller loss than dropping the row.
type Reader struct {
	producers   producedby.Store
	assets      AssetNames
	resources   ResourceNames
	collections CollectionNames
	scripts     ScriptLookup
}

// resolveConcurrency bounds how many per-id lookups run at once. The rows are
// independent reads, so they are issued together rather than one after another;
// the bound keeps a script that wrote a file per run from opening a connection
// per row.
const resolveConcurrency = 8

// New builds the reader, or nil when there is no producer record to read.
func New(producers producedby.Store, assets AssetNames, resources ResourceNames, collections CollectionNames, scripts ScriptLookup) *Reader {
	if producers == nil {
		return nil
	}
	return &Reader{producers: producers, assets: assets, resources: resources, collections: collections, scripts: scripts}
}

// Produced lists everything one script has produced or modified, most recently
// written first.
func (r *Reader) Produced(ctx context.Context, scriptID string, limit int) ([]Item, error) {
	rows, err := r.producers.ListByProducer(ctx, producedby.KindScript, scriptID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing what script %s produced: %w", scriptID, err)
	}
	assets := r.liveAssets(ctx, rows)
	items := make([]Item, len(rows))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(resolveConcurrency)
	for i, row := range rows {
		g.Go(func() error {
			items[i] = r.item(gctx, row, assets)
			return nil
		})
	}
	_ = g.Wait() // item reports a failed lookup on the row; none returns an error
	return items, nil
}

// item renders one row, resolving the file it names.
func (r *Reader) item(ctx context.Context, row producedby.Row, assets map[string]*portaldomain.Asset) Item {
	it := Item{
		TargetKind: row.TargetKind, TargetID: row.TargetID,
		Created: row.Created, FirstWriteAt: row.FirstWriteAt, LastWriteAt: row.LastWriteAt,
		WriteCount: row.WriteCount, LastVersion: row.LastVersion,
	}
	switch row.TargetKind {
	case producedby.TargetAsset:
		if assets == nil {
			return it
		}
		if a, ok := assets[row.TargetID]; ok {
			it.Name, it.OwnerEmail = a.Name, a.OwnerEmail
		} else {
			it.Deleted = true
		}
	case producedby.TargetResource:
		it.Name, it.Deleted = r.resourceName(ctx, row.TargetID)
	case producedby.TargetCollection:
		it.Name, it.OwnerEmail, it.Deleted = r.collectionName(ctx, row.TargetID)
	}
	return it
}

// liveAssets resolves the asset rows in one read, or nil when this deployment
// cannot resolve them -- in which case nothing is reported as deleted, because
// nothing was established.
func (r *Reader) liveAssets(ctx context.Context, rows []producedby.Row) map[string]*portaldomain.Asset {
	if r.assets == nil {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.TargetKind == producedby.TargetAsset {
			ids = append(ids, row.TargetID)
		}
	}
	if len(ids) == 0 {
		return map[string]*portaldomain.Asset{}
	}
	found, err := r.assets.GetByIDs(ctx, ids)
	if err != nil {
		return nil
	}
	live := make(map[string]*portaldomain.Asset, len(found))
	for id, asset := range found {
		// A soft-deleted asset is gone as far as a reader is concerned, and
		// saying so is the point: it is what tells someone the script is still
		// refreshing something nobody can open.
		if asset != nil && asset.DeletedAt == nil {
			live[id] = asset
		}
	}
	return live
}

// resourceName resolves one resource, reporting whether it is gone. A
// deployment with no resource layer reports neither name nor deletion.
func (r *Reader) resourceName(ctx context.Context, id string) (name string, deleted bool) {
	if r.resources == nil {
		return "", false
	}
	res, err := r.resources.Get(ctx, id)
	if err != nil && !resource.IsNotFound(err) {
		return "", false
	}
	if res == nil {
		return "", true
	}
	return res.DisplayName, false
}

// collectionName resolves one collection, reporting whether it is gone. A
// deployment with no collection store, or a lookup that failed for a reason
// other than absence, reports neither name nor deletion.
func (r *Reader) collectionName(ctx context.Context, id string) (name, owner string, deleted bool) {
	if r.collections == nil {
		return "", "", false
	}
	coll, err := r.collections.Get(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (coll == nil || coll.DeletedAt != nil)) {
		return "", "", true
	}
	if err != nil {
		return "", "", false
	}
	return coll.Name, coll.OwnerEmail, false
}

// Names resolves script ids to the names those scripts carry now. An id absent
// from the result is a script that no longer exists.
//
// A lookup that fails is reported as an error rather than as a missing script:
// a database that is briefly unavailable must not make every producer read as
// deleted.
func (r *Reader) Names(ctx context.Context, ids []string) (map[string]string, error) {
	names := make(map[string]string, len(ids))
	if r.scripts == nil {
		return names, nil
	}
	for _, id := range ids {
		if _, done := names[id]; done {
			continue
		}
		sc, err := r.scripts.GetByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("resolving script producer %s: %w", id, err)
		}
		if sc != nil {
			names[id] = sc.Name
		}
	}
	return names, nil
}
