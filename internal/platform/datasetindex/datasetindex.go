// Package datasetindex is the catalog-dataset consumer of the shared indexjobs
// framework (#1131). It gives a fact written into the catalog a search-time
// route that does not depend on a tool result already naming the entity the
// fact hangs off.
//
// Without it, the catalog's only text route through `search` is the catalog's
// word search: a description an agent applied to a dataset is found only when
// a query happens to share its words. Knowledge pages have no such gap (they
// ride the platform's own embedding search), so whether applied knowledge was
// discoverable would depend on which sink an operator picked. This consumer
// removes that asymmetry by mirroring the catalog's dataset text into the
// platform's own index and ranking it there.
//
// The unit is the whole corpus (one Key: SourceKind/SourceID), not one dataset
// per unit, because the corpus is read through the semantic provider rather
// than a table the reconciler can diff. So Source.LoadItems enumerates the catalog and
// materializes catalog_datasets from the result, the framework embeds the
// rows, and Sink.Upsert's atomic replace drops the rows for datasets the
// catalog no longer returns. Two consequences follow from that shape:
//
//   - The periodic re-enumeration needs no goroutine of its own. Sink.FindGaps
//     reports the corpus stale once SyncInterval has passed since the last
//     enumeration, the reconciler enqueues the unit, and the queue's own
//     claim/lease machinery makes the sweep single-flight across replicas.
//   - A sweep is cheap when nothing changed: the framework's text-hash dedup
//     re-embeds only the datasets whose text actually moved.
//
// The catalog stays the system of record. Nothing here writes back to it,
// every hit is dereferenced against it by reference, and the tables are safe to
// drop: the cost is only that `search` falls back to the catalog's own word
// search.
package datasetindex

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "catalog-datasets"

// SourceID is the single logical catalog-corpus identifier. A deployment has
// one configured semantic catalog, so a constant source id is sufficient;
// the rows keyed under it are shared by every replica.
const SourceID = "catalog"

// Default tuning for the catalog sweep. Both are deliberately conservative:
// the sweep is a paged read against an external catalog, so it trades
// freshness for load.
const (
	// DefaultSyncInterval is how long a synced corpus is considered fresh.
	// Thirty minutes keeps a newly applied description discoverable within
	// one working pause while enumerating a large catalog only twice an hour.
	DefaultSyncInterval = 30 * time.Minute

	// DefaultMaxEntries caps how many datasets are mirrored. The cap bounds
	// both the table and the embed pass's working set (one vector per entry is
	// held in memory during a sweep). A deployment whose catalog exceeds it
	// keeps the datasets IndexedKinds orders first, logs the truncation, and
	// reads short of the catalog's total on the Indexing page; raise
	// knowledge.catalog_index.max_entries to cover the rest.
	DefaultMaxEntries = 5000
)

// Config is the operator's control over the catalog index. It is embedded in
// the platform's knowledge config as `knowledge.catalog_index`.
type Config struct {
	// Enabled turns the consumer on. Nil (unset) means enabled: a deployment
	// with the platform catalog, a database, and an embedding provider gets
	// topic-level discoverability of catalog descriptions out of the box. Set
	// false to opt out, which leaves `search` ranking catalog datasets through
	// the catalog's word search alone.
	Enabled *bool `yaml:"enabled"`

	// SyncInterval is how often the catalog is re-enumerated. Zero uses
	// DefaultSyncInterval.
	SyncInterval time.Duration `yaml:"sync_interval"`

	// MaxEntries caps how many datasets are mirrored. Zero uses
	// DefaultMaxEntries.
	MaxEntries int `yaml:"max_entries"`

	// Excluded names the connections whose datasets the index leaves out,
	// set by the platform rather than read from YAML: its own built-in
	// platform-admin api connection, whose operations are the platform's
	// admin API and are found through api_discover (#2381). Their datasets
	// stay in the catalog and are fetched as any other; they are only not
	// mirrored, and not counted in the coverage the index is held to.
	Excluded []Connection `yaml:"-"`
}

// Connection names one connection by its kind and instance name, as a catalog
// reference does: mcp:dataset:(<kind>,<name>,...).
type Connection struct {
	Kind string
	Name string
}

// Excluding returns the config with conns added to the connections the index
// leaves out.
func (c Config) Excluding(conns ...Connection) Config {
	c.Excluded = append(slices.Clone(c.Excluded), conns...)
	return c
}

// IndexedKinds is the order the catalog is mirrored in, and so which datasets
// are kept when MaxEntries binds: every table of a query engine connection
// first, then GraphQL operations, then API operations, each in reference order.
// A table is what an agent searches the catalog for, and a deployment with many
// API connections holds thousands of operations that a reference order lists
// first ("api" sorts before "trino"), which left curated tables out of the
// index entirely (#2381). These are the kinds the catalog scan writes
// datasets under; a dataset of any other kind is not mirrored and keeps the
// index's coverage short of the catalog's total, which is where it shows.
var IndexedKinds = []string{"trino", "graphql", "api"}

// IsEnabled reports whether the catalog index is enabled, defaulting to true
// when not explicitly set.
func (c Config) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// ResolvedSyncInterval returns the configured sweep interval, substituting the
// default for an unset (non-positive) value.
func (c Config) ResolvedSyncInterval() time.Duration {
	if c.SyncInterval <= 0 {
		return DefaultSyncInterval
	}
	return c.SyncInterval
}

// ResolvedMaxEntries returns the configured entry cap, substituting the default
// for an unset (non-positive) value.
func (c Config) ResolvedMaxEntries() int {
	if c.MaxEntries <= 0 {
		return DefaultMaxEntries
	}
	return c.MaxEntries
}

// Entry is one catalog dataset as the platform mirrors it: the identity the
// catalog and `fetch` share (URN) plus the text an agent searches by. It
// carries no schema, lineage, or ownership: those stay in the catalog and are
// read through fetch when a caller drills in.
type Entry struct {
	URN         string
	Name        string
	Description string
	Tags        []string
	Domain      string
}

// IndexText composes the text a catalog dataset is embedded and lexically
// indexed on: its name, description, tags, and domain. The Source and the
// request-path ranking MUST agree on this composition so a stored embedding
// lives in the same space as the query; it is defined once here for both, and
// catalog_dataset_fts (migration 000096) composes the same corpus from the same
// columns for the lexical arm. Empty fields are skipped so a bare dataset does
// not pad the text with blank lines.
func IndexText(e Entry) string {
	fields := []string{e.Name, e.Description, strings.Join(e.Tags, " "), e.Domain}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, "\n")
}

// Searcher returns the request-path ranking capability over the mirrored
// catalog, or nil when the deployment has no database or the operator disabled
// the index. It returns the interface (never a typed nil) so a caller can pass
// the result straight into the search federation and have a nil check mean
// "no index".
func Searcher(db *sql.DB, cfg Config) knowledge.CatalogIndexSearcher {
	if db == nil || !cfg.IsEnabled() {
		return nil
	}
	return NewStore(db)
}

// RegisterConsumer registers the catalog-dataset Source/Sink pair on the shared
// indexjobs registry. lister enumerates the catalog; currentModel is the
// embedding provider's model identifier, which the gap query diffs stored rows
// against so a model swap re-embeds them.
func RegisterConsumer(reg interface {
	Register(indexjobs.Source, indexjobs.Sink) error
}, db *sql.DB, lister Lister, currentModel string, cfg Config,
) error {
	store := NewStore(db)
	src := NewSource(store, lister, cfg.ResolvedMaxEntries()).Excluding(cfg.Excluded...)
	sink := NewSink(store, currentModel, cfg.ResolvedSyncInterval()).Excluding(cfg.Excluded...)
	if err := reg.Register(src, sink); err != nil {
		return fmt.Errorf("registering catalog-datasets index consumer: %w", err)
	}
	return nil
}
