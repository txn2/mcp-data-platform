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

// Warnings returns what a save of src should tell the administrator who made
// it without refusing it: a slow read of its partitions, which an uncached
// file metastore causes. A check that cannot be made says so rather than
// staying silent.
func (s *Service) Warnings(ctx context.Context, src whsource.Source) []string {
	src = src.WithDefaults()
	out := make([]string, 0, 1)
	if w := s.partitionWarning(ctx, src); w != "" {
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
