//nolint:revive // max-public-structs: one cohesive capacity reading (the database half, the bucket half, and the rows each is made of) that the sampler in internal/platform/capacity fills, not a heap of unrelated types.
package observability

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Capacity instruments (#1899): how full the platform's database and buckets
// are, how fast they grow, and whether objects and rows still agree. Running
// out of disk or bucket quota is one of the commonest real outages, and before
// these series nothing reported it. Exposed names, all read on a scrape from
// the sampler RegisterCapacity installs:
//
//   - db_table_size_bytes{table}            on-disk size of a platform table,
//     its indexes and TOAST, summed over partitions
//   - db_table_rows_estimate{table}         the planner's row estimate
//     (reltuples), never a COUNT(*)
//   - db_vector_index_size_bytes{index}     each HNSW index, which grows and
//     bloats apart from its table's rows
//   - db_transaction_id_age                 age(datfrozenxid) of the database
//   - storage_bucket_bytes{bucket, purpose, backend}, storage_bucket_objects{...}
//     what the platform holds in each bucket it owns, by what it is for:
//     the last full listing plus the writes and deletes since
//   - storage_bucket_budget_bytes{bucket}   the operator's byte budget for a
//     bucket, where one is set (a cloud bucket has no "free")
//   - storage_orphaned_objects{purpose}, storage_dangling_references{purpose}
//     objects under a platform prefix with no row, and rows whose object is
//     gone, as the last reconcile counted them
//   - storage_scan_duration_seconds, storage_scan_objects  the last full
//     listing's duration and the objects it read
//   - mcp_platform_storage_backend_info{backend} 1
//
// Every replica reports the same values (the database is shared and the bucket
// counts are kept in it): read them with max, never sum.
//
// Cardinality: table and index are the migrations' own names; bucket is the
// buckets the deployment configures; purpose and backend are closed sets.
const (
	instDBTableSize        = "db_table_size"
	instDBTableRows        = "db_table_rows_estimate"
	instDBVectorIndexSize  = "db_vector_index_size"
	instDBXIDAge           = "db_transaction_id_age"
	instStorageBytes       = "storage_bucket"
	instStorageObjects     = "storage_bucket_objects"
	instStorageBudget      = "storage_bucket_budget"
	instStorageOrphans     = "storage_orphaned_objects"
	instStorageDangling    = "storage_dangling_references"
	instStorageScanSeconds = "storage_scan_duration"
	instStorageScanObjects = "storage_scan_objects"
	instStorageBackendInfo = "mcp_platform_storage_backend_info"

	capacityScrapeLimit = 10 * time.Second

	attrTable   = "table"
	attrIndex   = "index"
	attrBucket  = "bucket"
	attrBackend = "backend"
)

// The object stores mcp_platform_storage_backend_info names.
const (
	StorageBackendSeaweedFS = "seaweedfs"
	StorageBackendS3        = "s3"
	StorageBackendGCS       = "gcs"
	StorageBackendOther     = "other"
)

// TableCapacity is one platform table's size and row estimate.
type TableCapacity struct {
	Table string
	Bytes int64
	Rows  int64
}

// IndexCapacity is one vector index's size.
type IndexCapacity struct {
	Index string
	Bytes int64
}

// DatabaseCapacity is what the table sampler read. Known is false until a
// read has succeeded.
type DatabaseCapacity struct {
	Known            bool
	Tables           []TableCapacity
	VectorIndexes    []IndexCapacity
	TransactionIDAge int64
}

// BucketUsage is what the platform holds in one bucket for one purpose.
type BucketUsage struct {
	Bucket  string
	Purpose string
	Backend string
	Bytes   int64
	Objects int64
}

// BucketBudget is an operator's byte budget for a bucket.
type BucketBudget struct {
	Bucket string
	Bytes  int64
}

// ReconcileCount is what the last integrity reconcile found for one purpose.
type ReconcileCount struct {
	Purpose  string
	Orphaned int64
	Dangling int64
}

// StorageCapacity is the bucket side of a capacity sample. ScanKnown is false
// until a full listing has finished; Reconciled until a reconcile has.
type StorageCapacity struct {
	Usage       []BucketUsage
	Budgets     []BucketBudget
	Backends    []string
	Reconcile   []ReconcileCount
	ScanKnown   bool
	ScanSeconds float64
	ScanObjects int64
}

// CapacitySample is one scrape's capacity reading.
type CapacitySample struct {
	Database DatabaseCapacity
	Storage  StorageCapacity
}

// CapacitySampler returns the last capacity reading. It is called on each
// scrape with a bounded context and must answer from a cache.
type CapacitySampler func(ctx context.Context) CapacitySample

// capacityInstruments are the #1899 gauges and their sampler.
type capacityInstruments struct {
	tableSize, tableRows, vectorIndexSize, xidAge metric.Int64ObservableGauge
	bucketBytes, bucketObjects, bucketBudget      metric.Int64ObservableGauge
	orphans, dangling, scanObjects, backendInfo   metric.Int64ObservableGauge
	scanSeconds                                   metric.Float64ObservableGauge
	mu                                            sync.RWMutex
	sampler                                       CapacitySampler
}

// registerCapacityInstruments registers the capacity gauges and their scrape
// callback.
func (m *Metrics) registerCapacityInstruments(meter metric.Meter) error {
	c := &m.capacity
	var err error
	gauge := func(name, unit, desc string) metric.Int64ObservableGauge {
		if err != nil {
			return nil
		}
		opts := []metric.Int64ObservableGaugeOption{metric.WithDescription(desc)}
		if unit != "" {
			opts = append(opts, metric.WithUnit(unit))
		}
		var g metric.Int64ObservableGauge
		g, err = meter.Int64ObservableGauge(name, opts...)
		err = wrapReg(name, err)
		return g
	}
	c.tableSize = gauge(instDBTableSize, unitBytes,
		"On-disk bytes of a platform table with its indexes and TOAST, summed over its partitions, labeled by table. Every replica reports the same value: read with max.")
	c.tableRows = gauge(instDBTableRows, "",
		"The planner's row estimate (pg_class.reltuples) of a platform table, summed over its partitions, labeled by table. Refreshed by ANALYZE, so it lags a burst of writes.")
	c.vectorIndexSize = gauge(instDBVectorIndexSize, unitBytes,
		"On-disk bytes of a pgvector HNSW index, labeled by index. An HNSW index grows and bloats apart from its table's row count.")
	c.xidAge = gauge(instDBXIDAge, "",
		"Transactions since the database's oldest unfrozen transaction id (age(datfrozenxid)). PostgreSQL refuses writes as it nears 2^31; a value that only climbs is a vacuum that cannot freeze.")
	c.bucketBytes = gauge(instStorageBytes, unitBytes,
		"Bytes the platform holds in a bucket it owns, labeled by bucket, purpose and backend: the last full listing plus the writes and deletes recorded since.")
	c.bucketObjects = gauge(instStorageObjects, "",
		"Objects the platform holds in a bucket it owns, labeled by bucket, purpose and backend: the last full listing plus the writes and deletes recorded since.")
	c.bucketBudget = gauge(instStorageBudget, unitBytes,
		"The operator's byte budget for a bucket (MCP_PLATFORM_STORAGE_BUDGETS), labeled by bucket. Absent where none is set.")
	c.orphans = gauge(instStorageOrphans, "",
		"Objects under a platform prefix that no database row references, as the last reconcile counted them, labeled by purpose.")
	c.dangling = gauge(instStorageDangling, "",
		"Database rows whose object is missing from the bucket, as the last reconcile counted them, labeled by purpose.")
	c.scanObjects = gauge(instStorageScanObjects, "",
		"Objects the last full bucket listing read, across every platform bucket.")
	c.backendInfo = gauge(instStorageBackendInfo, "",
		"1 under each object store kind the platform's buckets are on (seaweedfs, s3, gcs, other).")
	if err != nil {
		return err
	}
	if c.scanSeconds, err = meter.Float64ObservableGauge(instStorageScanSeconds,
		metric.WithDescription("Seconds the last full bucket listing took, across every platform bucket."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instStorageScanSeconds, err)
	}
	if _, err := meter.RegisterCallback(m.observeCapacity,
		c.tableSize, c.tableRows, c.vectorIndexSize, c.xidAge, c.bucketBytes, c.bucketObjects,
		c.bucketBudget, c.orphans, c.dangling, c.scanObjects, c.backendInfo, c.scanSeconds); err != nil {
		return fmt.Errorf(instErrFmt, "capacity callback", err)
	}
	return nil
}

// RegisterCapacity installs the sampler the capacity gauges are read from.
// Nil-safe.
func (m *Metrics) RegisterCapacity(s CapacitySampler) {
	if m == nil {
		return
	}
	m.capacity.mu.Lock()
	defer m.capacity.mu.Unlock()
	m.capacity.sampler = s
}

// observeCapacity is the scrape callback for the capacity gauges.
func (m *Metrics) observeCapacity(ctx context.Context, o metric.Observer) error {
	m.capacity.mu.RLock()
	sampler := m.capacity.sampler
	m.capacity.mu.RUnlock()
	if sampler == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, capacityScrapeLimit)
	defer cancel()
	s := sampler(ctx)
	m.observeDatabaseCapacity(o, s.Database)
	m.observeStorageCapacity(o, s.Storage)
	return nil
}

// observeDatabaseCapacity reports the table sampler's reading.
func (m *Metrics) observeDatabaseCapacity(o metric.Observer, d DatabaseCapacity) {
	if !d.Known {
		return
	}
	c := &m.capacity
	for _, t := range d.Tables {
		attrs := metric.WithAttributes(attribute.String(attrTable, t.Table))
		o.ObserveInt64(c.tableSize, t.Bytes, attrs)
		o.ObserveInt64(c.tableRows, t.Rows, attrs)
	}
	for _, i := range d.VectorIndexes {
		o.ObserveInt64(c.vectorIndexSize, i.Bytes, metric.WithAttributes(attribute.String(attrIndex, i.Index)))
	}
	o.ObserveInt64(c.xidAge, d.TransactionIDAge)
}

// observeStorageCapacity reports the bucket side.
func (m *Metrics) observeStorageCapacity(o metric.Observer, s StorageCapacity) {
	c := &m.capacity
	for _, u := range s.Usage {
		attrs := metric.WithAttributes(attribute.String(attrBucket, u.Bucket),
			attribute.String(attrPurpose, u.Purpose), attribute.String(attrBackend, u.Backend))
		o.ObserveInt64(c.bucketBytes, u.Bytes, attrs)
		o.ObserveInt64(c.bucketObjects, u.Objects, attrs)
	}
	for _, b := range s.Budgets {
		o.ObserveInt64(c.bucketBudget, b.Bytes, metric.WithAttributes(attribute.String(attrBucket, b.Bucket)))
	}
	for _, b := range s.Backends {
		o.ObserveInt64(c.backendInfo, 1, metric.WithAttributes(attribute.String(attrBackend, b)))
	}
	for _, r := range s.Reconcile {
		attrs := metric.WithAttributes(attribute.String(attrPurpose, r.Purpose))
		o.ObserveInt64(c.orphans, r.Orphaned, attrs)
		o.ObserveInt64(c.dangling, r.Dangling, attrs)
	}
	if s.ScanKnown {
		o.ObserveFloat64(c.scanSeconds, s.ScanSeconds)
		o.ObserveInt64(c.scanObjects, s.ScanObjects)
	}
}
