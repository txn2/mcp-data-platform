// Package portalstore assembles the asset-portal store layer behind one Handle:
// the five Postgres stores (asset, share, version, collection, thread), the
// knowledge-page store, the S3 blob backend, and the save/manage-asset
// toolkit built on top of them.
//
// Construction takes explicit inputs — a *sql.DB, the resolved portal.S3Client
// (or nil for database-only mode), the embedding.Provider that powers ranked
// asset search, and the toolkit's Config knobs — so the subsystem is
// constructible and testable without a Platform. It imports pkg/portal,
// pkg/portal/knowledgepage, and pkg/toolkits/portal, never pkg/platform. The
// *sql.DB and embedding.Provider back many other subsystems, so they stay owned
// by Platform and are passed in rather than owned here.
//
// Toolkit registration stays a Platform/registry concern: New builds the
// toolkit and exposes it via Toolkit() for Platform to register into the shared
// toolkit registry. The only resource this package owns for shutdown is the S3
// client, closed via Close.
package portalstore

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/assetindex"
	"github.com/txn2/mcp-data-platform/internal/platform/collectionindex"
	"github.com/txn2/mcp-data-platform/internal/platform/knowledgepageindex"
	"github.com/txn2/mcp-data-platform/internal/platform/notices"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefstore"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// Config carries the portal-toolkit knobs the owner needs to build the
// save/manage-asset toolkit. The stores themselves need only the *sql.DB;
// these values shape the toolkit's blob addressing and asset limits.
type Config struct {
	// Name is the toolkit instance name (the platform passes its default).
	Name string

	// CaptureProvenance resolves which calls an asset write was built from,
	// passed straight through to the asset toolkit (#1320). Nil in a
	// deployment with no audit log to read.
	CaptureProvenance portal.ProvenanceCapturer
	// S3Bucket / S3Prefix address the portal's blob backend; empty in
	// database-only mode (no S3 client).
	S3Bucket string
	S3Prefix string
	// BaseURL is the portal's public base URL used to render share links.
	BaseURL string
	// MaxContentSize caps asset content size in bytes (0 = no limit).
	MaxContentSize int
	// MaxVersions is the deployment's asset version-retention default, applying
	// to every asset that carries no override of its own. Nil selects the
	// platform default (100); 0 keeps every version.
	MaxVersions *int
	// Directory is the known-users directory the toolkit's share action
	// resolves a recipient's name against (#1280). Nil leaves sharing working
	// by email address only.
	Directory portalkit.DirectoryReader
}

// Handle owns the assembled portal store layer: the six stores, the S3 blob
// backend, and the asset toolkit. The read accessors expose the stores +
// S3 client that Platform surfaces through its Portal* accessors (the admin
// REST handler and portal REST wiring) and that the cross-toolkit export wiring
// and search/enrichment provider assembly consume; Toolkit() exposes the
// asset toolkit for registration and as the memory thread linker. Close is
// the shutdown seam Platform wires into its own lifecycle (only the S3 client
// needs closing — the stores share Platform's *sql.DB, which Platform closes).
type Handle struct {
	assetStore         portal.AssetStore
	shareStore         portal.ShareStore
	versionStore       portal.VersionStore
	collectionStore    portal.CollectionStore
	threadStore        portal.ThreadStore
	knowledgePageStore knowledgepage.Store
	s3Client           portal.S3Client
	contentRefs        assetrefs.Store
	declarer           *assetrefs.Declarer
	toolkit            *portalkit.Toolkit
	// notices assembles a caller's session-start digest from the asset, share
	// and thread stores above plus its own watermark table (#1278). Set only by
	// New, which has the *sql.DB the watermark needs; a Handle assembled from
	// injected stores carries none and reports no notices.
	notices *notices.Handle
	// indexProducers are the write-path index-job producers the Postgres
	// stores were built with, exposed for the index-jobs queue to bind once it
	// exists. Empty when the Handle was assembled from injected stores
	// (NewFromStores), which own their own indexing arrangements.
	indexProducers []*indexjobs.Producer
}

// Stores bundles the six store implementations and the S3 blob client the
// Handle owns. New builds these from a *sql.DB; NewFromStores assembles a Handle
// from already-built implementations — the path New itself delegates to after
// constructing the Postgres stores, and the seam that lets callers (and tests)
// inject their own store implementations without a database.
type Stores struct {
	Asset         portal.AssetStore
	Share         portal.ShareStore
	Version       portal.VersionStore
	Collection    portal.CollectionStore
	Thread        portal.ThreadStore
	KnowledgePage knowledgepage.Store
	S3Client      portal.S3Client
	// ContentRefs records the managed resources an asset's content
	// references (#1474). Nil on a Handle assembled without a database, which
	// leaves every surface that would declare a reference refusing to.
	ContentRefs assetrefs.Store
}

// New assembles the six Postgres-backed stores and the asset toolkit from an
// explicit *sql.DB, the resolved S3 client (nil for database-only mode), the
// embedding provider, and the toolkit Config. It returns nil when db is nil:
// the portal is a no-op without a database, matching the platform precondition.
func New(db *sql.DB, s3Client portal.S3Client, embedder embedding.Provider, cfg Config) *Handle {
	if db == nil {
		return nil
	}
	// One write-path index-job producer per indexed kind, handed to the store
	// that writes that kind and left unbound until the index-jobs queue is
	// assembled (it is built after this layer, from it). Until then — and on a
	// deployment with no queue at all — the stores' notify calls are no-ops and
	// the reconciler remains the only path to the index (#1256).
	assets := indexjobs.NewProducer(assetindex.SourceKind)
	collections := indexjobs.NewProducer(collectionindex.SourceKind)
	pages := indexjobs.NewProducer(knowledgepageindex.SourceKind)

	h := NewFromStores(Stores{
		Asset:         portal.NewPostgresAssetStore(db, indexjobs.WithProducer(assets)),
		Share:         portal.NewPostgresShareStore(db),
		Version:       portal.NewPostgresVersionStore(db, s3Client, cfg.MaxVersions),
		Collection:    portal.NewPostgresCollectionStore(db, indexjobs.WithProducer(collections)),
		Thread:        portal.NewPostgresThreadStore(db),
		KnowledgePage: knowledgepage.NewPostgresStore(db, indexjobs.WithProducer(pages)),
		S3Client:      s3Client,
		ContentRefs:   assetrefstore.New(db),
	}, embedder, cfg)
	h.indexProducers = []*indexjobs.Producer{assets, collections, pages}
	h.notices = notices.New(db, h.assetStore, h.shareStore, h.threadStore)
	return h
}

// NewFromStores assembles the Handle and its asset toolkit from already-built
// store implementations. New delegates here after constructing the Postgres
// stores; it is also the entry point for assembling a Handle over injected store
// implementations (fakes or alternative backends) without a *sql.DB.
func NewFromStores(s Stores, embedder embedding.Provider, cfg Config) *Handle {
	h := &Handle{
		assetStore:         s.Asset,
		shareStore:         s.Share,
		versionStore:       s.Version,
		collectionStore:    s.Collection,
		threadStore:        s.Thread,
		knowledgePageStore: s.KnowledgePage,
		s3Client:           s.S3Client,
		contentRefs:        s.ContentRefs,
	}
	// The declaration path is built here, over the two stores it checks
	// against, so an asset reference works on a deployment with no
	// managed-resource layer at all; BindResources adds that layer when there
	// is one (#1488).
	h.declarer = assetrefs.NewDeclarer(h.contentRefs, h.assetStore)
	h.toolkit = portalkit.New(portalkit.Config{
		Name:            cfg.Name,
		AssetStore:      h.assetStore,
		ShareStore:      h.shareStore,
		VersionStore:    h.versionStore,
		CollectionStore: h.collectionStore,
		ThreadStore:     h.threadStore,
		S3Client:        h.s3Client,
		S3Bucket:        cfg.S3Bucket,
		S3Prefix:        cfg.S3Prefix,
		BaseURL:         cfg.BaseURL,
		MaxContentSize:  cfg.MaxContentSize,
		Embedder:        embedder,

		CaptureProvenance: cfg.CaptureProvenance,
		Directory:         cfg.Directory,
	})
	h.toolkit.SetContentRefs(h.declarer)
	return h
}

// ContentRefs returns the store of the things assets reference, or nil on a
// nil Handle. The REST surfaces read it to rewrite served content; the asset
// toolkit writes it through the declarer built in NewFromStores.
func (h *Handle) ContentRefs() assetrefs.Store {
	if h == nil {
		return nil
	}
	return h.contentRefs
}

// BindResources gives the declaration path the managed-resource layer, which
// is what admits an mcp:// URI in a save (#1474).
//
// It is a setter rather than a Config field because that layer is assembled
// after this one: it needs the resource store, which does not exist when the
// portal toolkit is built. A Handle that is never bound still declares asset
// references (#1488) and refuses a resource URI rather than dropping it, which
// is the right answer for a deployment with no managed resources at all.
func (h *Handle) BindResources(resources assetrefs.Resources, scheme string) {
	if h == nil {
		return
	}
	h.declarer.BindResources(resources, scheme)
}

// BindResourceWriter gives the asset toolkit the ability to write a managed
// resource, which is what puts manage_resource behind an agent and a scheduled
// script (#1487).
//
// It is a setter for the same reason BindResources is: the managed-resource
// layer is assembled after this one. A Handle that is never bound leaves
// manage_resource reporting that the deployment has no resource library rather
// than accepting a write that would go nowhere.
//
// The writer is an interface here and is built by the caller, which is what
// keeps a deployment with no resource layer from binding a nil one: the caller
// has a concrete writer to test for nil, and an untyped nil never reaches the
// toolkit.
func (h *Handle) BindResourceWriter(w portalkit.ResourceWriter) {
	if h == nil || h.toolkit == nil || w == nil {
		return
	}
	h.toolkit.SetResourceWriter(w)
}

// AssetStore returns the portal asset store, or nil on a nil Handle (portal
// disabled or no database).
func (h *Handle) AssetStore() portal.AssetStore {
	if h == nil {
		return nil
	}
	return h.assetStore
}

// ShareStore returns the portal share store, or nil on a nil Handle.
func (h *Handle) ShareStore() portal.ShareStore {
	if h == nil {
		return nil
	}
	return h.shareStore
}

// VersionStore returns the portal version store, or nil on a nil Handle.
func (h *Handle) VersionStore() portal.VersionStore {
	if h == nil {
		return nil
	}
	return h.versionStore
}

// CollectionStore returns the portal collection store, or nil on a nil Handle.
func (h *Handle) CollectionStore() portal.CollectionStore {
	if h == nil {
		return nil
	}
	return h.collectionStore
}

// Notices returns the session-start notice digest assembler, or nil on a nil
// Handle or one assembled without a database. A nil assembler builds no digest,
// so callers need no guard of their own.
func (h *Handle) Notices() *notices.Handle {
	if h == nil {
		return nil
	}
	return h.notices
}

// ThreadStore returns the portal feedback-thread store, or nil on a nil Handle.
func (h *Handle) ThreadStore() portal.ThreadStore {
	if h == nil {
		return nil
	}
	return h.threadStore
}

// KnowledgePageStore returns the canonical knowledge-page store, or nil on a
// nil Handle.
func (h *Handle) KnowledgePageStore() knowledgepage.Store {
	if h == nil {
		return nil
	}
	return h.knowledgePageStore
}

// IndexProducers returns the write-path index-job producers for the asset,
// collection and knowledge-page stores this Handle built, for the index-jobs
// queue to bind. Nil on a nil Handle or one assembled from injected stores; the
// result is a fresh slice, so a caller may append to it.
func (h *Handle) IndexProducers() []*indexjobs.Producer {
	if h == nil {
		return nil
	}
	return append([]*indexjobs.Producer(nil), h.indexProducers...)
}

// S3Client returns the portal S3 blob backend, or nil on a nil Handle or in
// database-only mode (no s3_connection configured).
func (h *Handle) S3Client() portal.S3Client {
	if h == nil {
		return nil
	}
	return h.s3Client
}

// SaveToolName re-exports the portal toolkit's save-tool name so Platform can
// configure provenance harvesting without importing pkg/toolkits/portal.
const SaveToolName = portalkit.SaveToolName

// Toolkit returns the save/manage-asset toolkit for Platform to register
// into the shared toolkit registry and to wire as the memory thread linker, or
// nil on a nil Handle.
func (h *Handle) Toolkit() *portalkit.Toolkit {
	if h == nil {
		return nil
	}
	return h.toolkit
}

// Close closes the S3 blob backend. No-op on a nil Handle or in database-only
// mode (no S3 client). The stores share Platform's *sql.DB, which Platform
// closes, so they are not closed here.
func (h *Handle) Close() error {
	if h == nil || h.s3Client == nil {
		return nil
	}
	slog.Debug("portalstore: closing portal S3 client")
	if err := h.s3Client.Close(); err != nil {
		return fmt.Errorf("portalstore: close S3 client: %w", err)
	}
	return nil
}
