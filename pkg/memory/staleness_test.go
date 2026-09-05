package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestNewStalenessWatcher_Defaults(t *testing.T) {
	store := NewNoopStore()
	sp := semantic.NewNoopProvider()

	t.Run("zero values get defaults", func(t *testing.T) {
		w := NewStalenessWatcher(store, sp, StalenessConfig{})
		assert.Equal(t, defaultStalenessInterval, w.cfg.Interval)
		assert.Equal(t, defaultStalenessBatchSize, w.cfg.BatchSize)
	})

	t.Run("custom values preserved", func(t *testing.T) {
		w := NewStalenessWatcher(store, sp, StalenessConfig{
			Interval:  5 * time.Minute,
			BatchSize: 25,
		})
		assert.Equal(t, 5*time.Minute, w.cfg.Interval)
		assert.Equal(t, 25, w.cfg.BatchSize)
	})

	t.Run("negative values get defaults", func(t *testing.T) {
		w := NewStalenessWatcher(store, sp, StalenessConfig{
			Interval:  -1 * time.Second,
			BatchSize: -10,
		})
		assert.Equal(t, defaultStalenessInterval, w.cfg.Interval)
		assert.Equal(t, defaultStalenessBatchSize, w.cfg.BatchSize)
	})
}

func TestParseURNToTable(t *testing.T) {
	tests := []struct {
		name    string
		urn     string
		want    semantic.TableIdentifier
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid URN",
			urn:  "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)",
			want: semantic.TableIdentifier{
				Catalog: "catalog",
				Schema:  "schema",
				Table:   "table",
			},
		},
		{
			name:    "invalid prefix",
			urn:     "urn:li:corpuser:foo",
			wantErr: true,
			errMsg:  "invalid dataset URN",
		},
		{
			name:    "malformed - missing comma",
			urn:     "urn:li:dataset:(urn:li:dataPlatform:trino)",
			wantErr: true,
			errMsg:  "invalid dataset URN format",
		},
		{
			name:    "incomplete table path",
			urn:     "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema,PROD)",
			wantErr: true,
			errMsg:  "incomplete table path",
		},
		{
			name: "extra dots in table path",
			urn:  "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table.extra,PROD)",
			want: semantic.TableIdentifier{
				Catalog: "catalog",
				Schema:  "schema",
				Table:   "table",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseURNToTable(tt.urn)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestStalenessWatcher_StartStop(t *testing.T) {
	t.Parallel()

	store := NewNoopStore()
	sp := semantic.NewNoopProvider()

	w := NewStalenessWatcher(store, sp, StalenessConfig{
		Interval:  100 * time.Millisecond,
		BatchSize: 5,
	})

	// Start should not panic.
	w.Start(context.Background())

	// Second start is a no-op.
	w.Start(context.Background())

	// Stop should not panic.
	w.Stop()

	// Double-stop should not panic (sync.Once).
	w.Stop()
}

// mockSemanticProvider is a test double for semantic.Provider.
type mockSemanticProvider struct {
	semantic.NoopProvider
	tableCtxFn func(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error)
}

func (m *mockSemanticProvider) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	if m.tableCtxFn != nil {
		return m.tableCtxFn(ctx, table)
	}
	return &semantic.TableContext{}, nil
}

func TestCheckEntityStaleness(t *testing.T) {
	t.Run("deprecated entity", func(t *testing.T) {
		sp := &mockSemanticProvider{
			tableCtxFn: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Deprecation: &semantic.Deprecation{},
				}, nil
			},
		}
		w := NewStalenessWatcher(NewNoopStore(), sp, StalenessConfig{})

		record := Record{
			EntityURNs: []string{
				"urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)",
			},
		}
		reason := w.checkEntityStaleness(context.Background(), record)
		assert.Contains(t, reason, "deprecated")
	})

	t.Run("non-deprecated entity", func(t *testing.T) {
		sp := &mockSemanticProvider{
			tableCtxFn: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{}, nil
			},
		}
		w := NewStalenessWatcher(NewNoopStore(), sp, StalenessConfig{})

		record := Record{
			EntityURNs: []string{
				"urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)",
			},
		}
		reason := w.checkEntityStaleness(context.Background(), record)
		assert.Empty(t, reason)
	})

	t.Run("lookup error", func(t *testing.T) {
		sp := &mockSemanticProvider{
			tableCtxFn: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return nil, errors.New("connection refused")
			},
		}
		w := NewStalenessWatcher(NewNoopStore(), sp, StalenessConfig{})

		record := Record{
			EntityURNs: []string{
				"urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)",
			},
		}
		reason := w.checkEntityStaleness(context.Background(), record)
		assert.Contains(t, reason, "lookup failed")
	})

	// #1610: mcp-datahub v1.15.1 made a URN the catalog holds no entity for
	// visible here for the first time. It is not evidence the record went
	// stale -- a URN that was never ingested is indistinguishable from one that
	// was removed, and flagging on it would mark every memory citing an
	// uncataloged table at once.
	t.Run("catalog holds no entity for the cited urn", func(t *testing.T) {
		sp := &mockSemanticProvider{
			tableCtxFn: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return nil, fmt.Errorf("datahub holds no entity: %w", semantic.ErrNotFound)
			},
		}
		w := NewStalenessWatcher(NewNoopStore(), sp, StalenessConfig{})

		record := Record{
			EntityURNs: []string{
				"urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)",
			},
		}
		reason := w.checkEntityStaleness(context.Background(), record)
		assert.Empty(t, reason, "a URN the catalog does not hold must not flag the record stale")
	})

	t.Run("invalid URN skipped", func(t *testing.T) {
		sp := &mockSemanticProvider{}
		w := NewStalenessWatcher(NewNoopStore(), sp, StalenessConfig{})

		record := Record{
			EntityURNs: []string{"not-a-valid-urn"},
		}
		reason := w.checkEntityStaleness(context.Background(), record)
		assert.Empty(t, reason)
	})

	t.Run("no entity URNs", func(t *testing.T) {
		sp := &mockSemanticProvider{}
		w := NewStalenessWatcher(NewNoopStore(), sp, StalenessConfig{})

		record := Record{EntityURNs: []string{}}
		reason := w.checkEntityStaleness(context.Background(), record)
		assert.Empty(t, reason)
	})
}

// recordingStore is a Store that answers List from a fixed set, honoring the
// EntityLinked filter the real store implements in SQL, and records what the
// watcher asked of it.
type recordingStore struct {
	Store
	records  []Record
	asked    Filter
	verified []string
	staled   []string
}

func (s *recordingStore) List(_ context.Context, filter Filter) ([]Record, int, error) {
	s.asked = filter
	var out []Record
	for _, rec := range s.records {
		if filter.EntityLinked && len(rec.EntityURNs) == 0 {
			continue
		}
		if filter.Limit > 0 && len(out) == filter.Limit {
			break
		}
		out = append(out, rec)
	}
	return out, len(out), nil
}

func (s *recordingStore) MarkVerified(_ context.Context, ids []string) error {
	s.verified = append(s.verified, ids...)
	return nil
}

func (s *recordingStore) MarkStale(_ context.Context, ids []string, _ string) error {
	s.staled = append(s.staled, ids...)
	return nil
}

const stalenessTestURN = "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)"

// #1625: the batch is drawn from entity-linked records. A record naming no
// entity is not something a catalog can be asked about, so the watcher neither
// spends its batch on one nor stamps it verified.
func TestCheckBatchAsksOnlyForRecordsItCanCheck(t *testing.T) {
	store := &recordingStore{records: []Record{
		{ID: "linked-1", EntityURNs: []string{stalenessTestURN}},
		{ID: "unlinked-1"},
		{ID: "linked-2", EntityURNs: []string{stalenessTestURN}},
	}}
	var lookups int
	sp := &mockSemanticProvider{
		tableCtxFn: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			lookups++
			return &semantic.TableContext{}, nil
		},
	}

	w := NewStalenessWatcher(store, sp, StalenessConfig{BatchSize: 10})
	require.NoError(t, w.checkBatch(context.Background()))

	assert.True(t, store.asked.EntityLinked,
		"the batch must be filtered in the query; skipping in the loop spends it on rows that were never candidates")
	assert.Equal(t, StatusActive, store.asked.Status)
	assert.Equal(t, []string{"linked-1", "linked-2"}, store.verified)
	assert.Empty(t, store.staled)
	assert.Equal(t, 2, lookups, "one lookup per entity-linked record and none for the rest")
}

// A store that hands back a record the filter should have excluded still must
// not have last_verified stamped on it: verification means the watcher checked
// the record, and it cannot check this one.
func TestCheckBatchNeverVerifiesAnEntitylessRecord(t *testing.T) {
	store := &recordingStore{records: []Record{{ID: "unlinked-1"}, {ID: "unlinked-2"}}}
	// The filter is ignored, which is what a store that has not implemented it
	// does.
	store.records = append(store.records, Record{ID: "linked-1", EntityURNs: []string{stalenessTestURN}})
	ignoring := &filterIgnoringStore{recordingStore: store}

	w := NewStalenessWatcher(ignoring, &mockSemanticProvider{}, StalenessConfig{BatchSize: 10})
	require.NoError(t, w.checkBatch(context.Background()))

	assert.Equal(t, []string{"linked-1"}, store.verified)
}

// filterIgnoringStore answers List with every record it holds, whatever the
// filter said.
type filterIgnoringStore struct{ *recordingStore }

func (s *filterIgnoringStore) List(_ context.Context, filter Filter) ([]Record, int, error) {
	s.asked = filter
	return s.records, len(s.records), nil
}

// A record whose entity the catalog reports deprecated is flagged stale rather
// than verified, which is the watcher's whole purpose and the behavior the
// entity-linked filter must not have changed.
func TestCheckBatchStillFlagsADeprecatedEntity(t *testing.T) {
	store := &recordingStore{records: []Record{
		{ID: "linked-1", EntityURNs: []string{stalenessTestURN}},
	}}
	sp := &mockSemanticProvider{
		tableCtxFn: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return &semantic.TableContext{Deprecation: &semantic.Deprecation{}}, nil
		},
	}

	w := NewStalenessWatcher(store, sp, StalenessConfig{})
	require.NoError(t, w.checkBatch(context.Background()))

	assert.Equal(t, []string{"linked-1"}, store.staled)
	assert.Empty(t, store.verified)
}
