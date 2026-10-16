package whadmin

import (
	"context"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// slowPartitionRead is how long a read of a source's partitions may take
// before the save that made it is answered with a warning (#2134). A cached
// metastore answers in a second or two; an uncached file metastore over a
// slow store took close to a minute.
const slowPartitionRead = 5 * time.Second

// partitionReadBound is the longest a save waits on that read: past it the
// read is reported slow without waiting for it to finish, so a save on the
// metastore the warning is about is not held for a minute.
const partitionReadBound = 3 * slowPartitionRead

// maxSegmentsPerRollup is the most segments a source's settings may imply
// between two rollups of its open window before the save is answered with a
// warning (#2132): each is an object every reader of recent events reads.
const maxSegmentsPerRollup = 1000

// Warnings returns what a save of src should tell the administrator who made
// it without refusing it: a slow read of its partitions, which an uncached
// file metastore causes, and settings under which the open window holds more
// segments between rollups than a reader should have to read. A check that
// cannot be made says so rather than staying silent.
func (s *Service) Warnings(ctx context.Context, src whsource.Source) []string {
	src = src.WithDefaults()
	out := make([]string, 0, 2)
	if w := s.partitionWarning(ctx, src); w != "" {
		out = append(out, w)
	}
	if w := s.segmentWarning(ctx, src); w != "" {
		out = append(out, w)
	}
	return out
}

// partitionWarning times a read of the source's partitions.
func (s *Service) partitionWarning(ctx context.Context, src whsource.Source) string {
	tg, err := s.deps.Tables.TargetFor(src.Connection)
	if err != nil {
		return "the partitions of " + src.RawTableName() + " could not be read: " + err.Error()
	}
	readCtx, cancel := context.WithTimeout(ctx, partitionReadBound)
	defer cancel()
	took, err := s.deps.Tables.PartitionReadTime(readCtx, tg, src)
	switch {
	case err != nil && readCtx.Err() != nil:
		took = partitionReadBound
	case err != nil:
		return err.Error()
	case took < slowPartitionRead:
		return ""
	}
	return fmt.Sprintf("reading the partitions of %s took %.0f s or more; on a file metastore set hive.metastore-cache-ttl "+
		"(for example 10m) and hive.metastore-refresh-interval (1m) on the %s catalog and restart the coordinator "+
		"and every worker, or the compactor's partition calls and table registrations time out when the store is slow",
		src.RawTableName(), took.Seconds(), tg.Catalog)
}

// segmentWarning estimates the segments the open window holds between two
// rollups at the most: one per flush interval from each receiving replica.
// The replicas counted are those whose spool reported recently, at least one.
func (s *Service) segmentWarning(ctx context.Context, src whsource.Source) string {
	replicas := 1
	if states, err := s.deps.Windows.SpoolStates(ctx); err == nil {
		live := 0
		for _, st := range states {
			if s.deps.Now().Sub(st.UpdatedAt) <= SpoolStale {
				live++
			}
		}
		replicas = max(replicas, live)
	}
	// WithDefaults gives every source a flush interval of at least a
	// millisecond, so the division is safe.
	implied := int64(replicas) * int64(src.Config.RollupEvery()/src.Config.FlushInterval())
	if implied <= maxSegmentsPerRollup {
		return ""
	}
	return fmt.Sprintf("with flush_max_interval_ms %d on %d receiving %s, a busy source writes up to %d segments "+
		"between rollups of its open window (rollup_every_minutes %d), and a reader of recent events reads each "+
		"one; raise flush_max_interval_ms or lower rollup_every_minutes",
		src.Config.FlushMaxIntervalMS, replicas, plural(replicas, "replica", "replicas"), implied, src.Config.RollupEveryMinutes)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
