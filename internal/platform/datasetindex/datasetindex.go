// Package datasetindex is the catalog-dataset consumer of the shared indexjobs
// framework (#1131). It gives a fact written into the DataHub catalog a
// search-time route that does not depend on a tool result already naming the
// entity the fact hangs off.
//
// Before it, the catalog's only text route through `search` was DataHub's own
// keyword search: a description an agent applied to a dataset was found when a
// query happened to share its words, and only once DataHub's index caught up.
// Knowledge pages had no such gap — they ride the platform's own embedding
// search — so whether applied knowledge was discoverable depended on which sink
// an operator picked. This consumer removes that asymmetry by mirroring the
// catalog's dataset text into the platform's own index and ranking it there.
//
// The unit is the whole corpus (one Key: SourceKind/SourceID), not one dataset
// per unit, because the corpus does not live in a table the reconciler can
// diff: it lives in DataHub. So Source.LoadItems enumerates the catalog and
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
// The catalog stays the system of record. Nothing here writes back to DataHub,
// every hit is dereferenced against DataHub by URN, and the tables are safe to
// drop — the cost is only that `search` falls back to DataHub's own keyword
// search, which is what it used before this package existed.
package datasetindex

import (
	"database/sql"
	"fmt"
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
	// indexes the first page-order slice and logs the truncation; raise
	// knowledge.catalog_index.max_entries to cover the rest.
	DefaultMaxEntries = 5000
)

// Config is the operator's control over the catalog index. It is embedded in
// the platform's knowledge config as `knowledge.catalog_index`.
type Config struct {
	// Enabled turns the consumer on. Nil (unset) means enabled: a deployment
	// with a DataHub catalog, a database, and an embedding provider gets
	// topic-level discoverability of catalog descriptions out of the box. Set
	// false to opt out, which leaves `search` ranking catalog datasets through
	// DataHub's own keyword search alone.
	Enabled *bool `yaml:"enabled"`

	// SyncInterval is how often the catalog is re-enumerated. Zero uses
	// DefaultSyncInterval.
	SyncInterval time.Duration `yaml:"sync_interval"`

	// MaxEntries caps how many datasets are mirrored. Zero uses
	// DefaultMaxEntries.
	MaxEntries int `yaml:"max_entries"`
}

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
// carries no schema, lineage, or ownership — those stay in DataHub and are
// read through the entity path when a caller drills in.
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
	src := NewSource(store, lister, cfg.ResolvedMaxEntries())
	if err := reg.Register(src, NewSink(store, currentModel, cfg.ResolvedSyncInterval())); err != nil {
		return fmt.Errorf("registering catalog-datasets index consumer: %w", err)
	}
	return nil
}
