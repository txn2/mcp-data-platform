package memory

import (
	"context"
	"errors"
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
			errMsg:  "not a dataset URN",
		},
		{
			name:    "malformed - missing comma",
			urn:     "urn:li:dataset:(urn:li:dataPlatform:trino)",
			wantErr: true,
			errMsg:  "malformed dataset URN",
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
