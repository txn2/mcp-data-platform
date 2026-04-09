package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	// defaultStalenessInterval is the default staleness check interval.
	defaultStalenessInterval = 15 * time.Minute
	// defaultStalenessBatchSize is the default number of records per check.
	defaultStalenessBatchSize = 50
	// urnDatasetParts is the expected number of comma-separated parts in a dataset URN.
	urnDatasetParts = 3
	// urnTablePathParts is the minimum number of dot-separated segments in a table path.
	urnTablePathParts = 3
)

// StalenessConfig configures the staleness watcher.
type StalenessConfig struct {
	Interval  time.Duration
	BatchSize int
}

// StalenessWatcher periodically checks active memories against DataHub
// entity state and flags stale records.
type StalenessWatcher struct {
	store            Store
	semanticProvider semantic.Provider
	cfg              StalenessConfig
	stopCh           chan struct{}
	wg               sync.WaitGroup
	started          atomic.Bool
	stopOnce         sync.Once
}

// NewStalenessWatcher creates a new watcher.
func NewStalenessWatcher(store Store, sp semantic.Provider, cfg StalenessConfig) *StalenessWatcher {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultStalenessInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultStalenessBatchSize
	}
	return &StalenessWatcher{
		store:            store,
		semanticProvider: sp,
		cfg:              cfg,
		stopCh:           make(chan struct{}),
	}
}

// Start begins the periodic staleness check loop.
// It is safe to call multiple times; only the first call starts the loop.
func (w *StalenessWatcher) Start(_ context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return
	}
	w.wg.Add(1)
	go w.run() // #nosec G118 -- background goroutine intentionally uses its own context per tick
}

// Stop signals the watcher to stop and waits for completion.
// It is safe to call multiple times.
func (w *StalenessWatcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	w.wg.Wait()
}

// run is the main loop that checks batches of records at the configured interval.
func (w *StalenessWatcher) run() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), w.cfg.Interval/2)
			if err := w.checkBatch(ctx); err != nil {
				slog.Error("staleness check failed", "error", err)
			}
			cancel()
		}
	}
}

// checkBatch checks one batch of the oldest-verified active memories.
func (w *StalenessWatcher) checkBatch(ctx context.Context) error {
	records, _, err := w.store.List(ctx, Filter{
		Status:  StatusActive,
		Limit:   w.cfg.BatchSize,
		OrderBy: "last_verified ASC NULLS FIRST",
	})
	if err != nil {
		return fmt.Errorf("listing records for staleness check: %w", err)
	}

	var staleIDs []string
	var verifiedIDs []string

	for _, record := range records {
		if len(record.EntityURNs) == 0 {
			verifiedIDs = append(verifiedIDs, record.ID)
			continue
		}

		reason := w.checkEntityStaleness(ctx, record)
		if reason != "" {
			staleIDs = append(staleIDs, record.ID)
			slog.Info("memory flagged as stale",
				"id", record.ID, "reason", reason,
				"entity_urns", record.EntityURNs)
		} else {
			verifiedIDs = append(verifiedIDs, record.ID)
		}
	}

	if len(staleIDs) > 0 {
		if err := w.store.MarkStale(ctx, staleIDs, "entity changed or deprecated"); err != nil {
			return fmt.Errorf("marking stale: %w", err)
		}
	}

	if len(verifiedIDs) > 0 {
		if err := w.store.MarkVerified(ctx, verifiedIDs); err != nil {
			return fmt.Errorf("marking verified: %w", err)
		}
	}

	return nil
}

// checkEntityStaleness checks if any entity referenced by a memory has changed.
// Returns a non-empty reason string if stale, empty string if still valid.
func (w *StalenessWatcher) checkEntityStaleness(ctx context.Context, record Record) string {
	var reasons []string

	for _, urn := range record.EntityURNs {
		table, err := ParseURNToTable(urn)
		if err != nil {
			continue
		}

		tc, err := w.semanticProvider.GetTableContext(ctx, table)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("entity %s: lookup failed", urn))
			continue
		}

		if tc.Deprecation != nil {
			reasons = append(reasons, fmt.Sprintf("entity %s is deprecated", urn))
		}
	}

	return strings.Join(reasons, "; ")
}

// ParseURNToTable attempts to extract a TableIdentifier from a DataHub dataset URN.
// URN format: urn:li:dataset:(urn:li:dataPlatform:platform,catalog.schema.table,ENV).
func ParseURNToTable(urn string) (semantic.TableIdentifier, error) {
	// Extract the dataset key portion.
	const prefix = "urn:li:dataset:(urn:li:dataPlatform:"
	if !strings.HasPrefix(urn, prefix) {
		return semantic.TableIdentifier{}, fmt.Errorf("not a dataset URN: %s", urn)
	}

	inner := strings.TrimPrefix(urn, prefix)
	inner = strings.TrimSuffix(inner, ")")

	parts := strings.SplitN(inner, ",", urnDatasetParts)
	if len(parts) < 2 {
		return semantic.TableIdentifier{}, fmt.Errorf("malformed dataset URN: %s", urn)
	}

	tablePath := parts[1]
	pathParts := strings.Split(tablePath, ".")
	if len(pathParts) < urnTablePathParts {
		return semantic.TableIdentifier{}, fmt.Errorf("incomplete table path in URN: %s", urn)
	}

	return semantic.TableIdentifier{
		Catalog: pathParts[0],
		Schema:  pathParts[1],
		Table:   pathParts[2],
	}, nil
}
