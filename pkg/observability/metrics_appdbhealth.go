package observability

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Application database health gauges (#2409): the latest sample the health
// loop recorded for each application database, read from the platform's own
// database on a scrape, never from the application database. Exposed names,
// each labeled by connection alone:
//
//   - appdb_health_size_bytes
//   - appdb_health_transaction_id_age
//   - appdb_health_connections and appdb_health_max_connections (the server's)
//   - appdb_health_idle_in_transaction_sessions, appdb_health_long_running_sessions,
//     appdb_health_blocked_sessions
//   - appdb_health_invalid_indexes, appdb_health_unused_indexes
//   - appdb_health_dead_tuple_ratio  the highest of a table holding 1,000 rows or more
//   - appdb_health_cache_hit_ratio
//   - appdb_health_schema_drift_objects  across the schemas that could be checked
//
// Cardinality: connection is an operator-configured name. No table, role or
// statement is a label.
const appdbHealthPrefix = "appdb_health_"

// appdbHealthScrapeLimit bounds the read of the stored samples on a scrape.
const appdbHealthScrapeLimit = 5 * time.Second

// AppDBHealthSample is one application database's latest health sample.
type AppDBHealthSample struct {
	Connection        string
	SizeBytes         int64
	TransactionIDAge  int64
	Connections       int64
	MaxConnections    int64
	IdleInTransaction int64
	LongRunning       int64
	Blocked           int64
	InvalidIndexes    int64
	UnusedIndexes     int64
	DriftObjects      int64
	DeadTupleRatio    float64
	CacheHitRatio     float64
}

type appdbHealthInstruments struct {
	ints    map[string]metric.Int64ObservableGauge
	floats  map[string]metric.Float64ObservableGauge
	mu      sync.RWMutex
	sampler func(ctx context.Context) []AppDBHealthSample
}

// appdbHealthInts and appdbHealthFloats read each gauge's value from a sample.
var (
	appdbHealthInts = map[string]struct {
		desc string
		of   func(AppDBHealthSample) int64
	}{
		"size_bytes":                   {"Bytes the application database takes (pg_database_size).", func(s AppDBHealthSample) int64 { return s.SizeBytes }},
		"transaction_id_age":           {"Transactions since the application database's oldest unfrozen transaction id; PostgreSQL refuses writes near 2^31.", func(s AppDBHealthSample) int64 { return s.TransactionIDAge }},
		"connections":                  {"Client connections the application database's server holds.", func(s AppDBHealthSample) int64 { return s.Connections }},
		"max_connections":              {"The application database server's max_connections.", func(s AppDBHealthSample) int64 { return s.MaxConnections }},
		"idle_in_transaction_sessions": {"Sessions holding a transaction open with no statement running.", func(s AppDBHealthSample) int64 { return s.IdleInTransaction }},
		"long_running_sessions":        {"Sessions running a statement past the long-running bound.", func(s AppDBHealthSample) int64 { return s.LongRunning }},
		"blocked_sessions":             {"Sessions waiting on a lock another session holds.", func(s AppDBHealthSample) int64 { return s.Blocked }},
		"invalid_indexes":              {"Indexes a failed concurrent build left invalid.", func(s AppDBHealthSample) int64 { return s.InvalidIndexes }},
		"unused_indexes":               {"Indexes never scanned since the statistics were reset that back no constraint.", func(s AppDBHealthSample) int64 { return s.UnusedIndexes }},
		"schema_drift_objects":         {"Objects whose live definition differs from what the applied migrations build, across the schemas that could be checked.", func(s AppDBHealthSample) int64 { return s.DriftObjects }},
	}
	appdbHealthFloats = map[string]struct {
		desc string
		of   func(AppDBHealthSample) float64
	}{
		"dead_tuple_ratio": {"The highest dead tuple ratio of a table holding 1,000 rows or more.", func(s AppDBHealthSample) float64 { return s.DeadTupleRatio }},
		"cache_hit_ratio":  {"The share of block reads served from shared buffers.", func(s AppDBHealthSample) float64 { return s.CacheHitRatio }},
	}
)

func (m *Metrics) registerAppDBHealthInstruments(meter metric.Meter) error {
	a := &m.appdbHealth
	a.ints = map[string]metric.Int64ObservableGauge{}
	a.floats = map[string]metric.Float64ObservableGauge{}
	insts := []metric.Observable{}
	for name, g := range appdbHealthInts {
		inst, err := meter.Int64ObservableGauge(appdbHealthPrefix+name, metric.WithDescription(g.desc))
		if err != nil {
			return wrapReg(appdbHealthPrefix+name, err)
		}
		a.ints[name] = inst
		insts = append(insts, inst)
	}
	for name, g := range appdbHealthFloats {
		inst, err := meter.Float64ObservableGauge(appdbHealthPrefix+name, metric.WithDescription(g.desc))
		if err != nil {
			return wrapReg(appdbHealthPrefix+name, err)
		}
		a.floats[name] = inst
		insts = append(insts, inst)
	}
	if _, err := meter.RegisterCallback(m.observeAppDBHealth, insts...); err != nil {
		return wrapReg("appdb health callback", err)
	}
	return nil
}

// RegisterAppDBHealth installs what the application database health gauges
// are read from on each scrape. Nil-safe; a later call replaces the earlier.
func (m *Metrics) RegisterAppDBHealth(s func(ctx context.Context) []AppDBHealthSample) {
	if m == nil {
		return
	}
	m.appdbHealth.mu.Lock()
	defer m.appdbHealth.mu.Unlock()
	m.appdbHealth.sampler = s
}

// observeAppDBHealth is the scrape callback for the health gauges.
func (m *Metrics) observeAppDBHealth(ctx context.Context, o metric.Observer) error {
	a := &m.appdbHealth
	a.mu.RLock()
	sampler := a.sampler
	a.mu.RUnlock()
	if sampler == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, appdbHealthScrapeLimit)
	defer cancel()
	for _, s := range sampler(ctx) {
		attrs := metric.WithAttributes(attribute.String(attrConnection, s.Connection))
		for name, g := range appdbHealthInts {
			o.ObserveInt64(a.ints[name], g.of(s), attrs)
		}
		for name, g := range appdbHealthFloats {
			o.ObserveFloat64(a.floats[name], g.of(s), attrs)
		}
	}
	return nil
}
