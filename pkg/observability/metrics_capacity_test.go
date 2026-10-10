package observability

import (
	"context"
	"strings"
	"testing"
)

// TestCapacity_Observed reads every capacity gauge from a sampler, under the
// names the documentation carries.
func TestCapacity_Observed(t *testing.T) {
	m := newEnabledMetrics(t)
	m.RegisterCapacity(func(context.Context) CapacitySample {
		return CapacitySample{
			Database: DatabaseCapacity{
				Known:            true,
				Tables:           []TableCapacity{{Table: "audit_logs", Bytes: 8192, Rows: 12}},
				VectorIndexes:    []IndexCapacity{{Index: "idx_prompts_embedding_hnsw", Bytes: 4096}},
				TransactionIDAge: 731,
			},
			Storage: StorageCapacity{
				Usage:       []BucketUsage{{Bucket: "managed-resources", Purpose: "resources", Backend: "seaweedfs", Bytes: 2048, Objects: 3}},
				Budgets:     []BucketBudget{{Bucket: "managed-resources", Bytes: 1 << 30}},
				Backends:    []string{"seaweedfs"},
				Reconcile:   []ReconcileCount{{Purpose: "resources", Orphaned: 1, Dangling: 2}},
				ScanKnown:   true,
				ScanSeconds: 1.5,
				ScanObjects: 40,
			},
		}
	})
	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`db_table_size_bytes{table="audit_logs"} 8192`,
		`db_table_rows_estimate{table="audit_logs"} 12`,
		`db_vector_index_size_bytes{index="idx_prompts_embedding_hnsw"} 4096`,
		`db_transaction_id_age 731`,
		`storage_bucket_bytes{backend="seaweedfs",bucket="managed-resources",purpose="resources"} 2048`,
		`storage_bucket_objects{backend="seaweedfs",bucket="managed-resources",purpose="resources"} 3`,
		`storage_bucket_budget_bytes{bucket="managed-resources"} 1.073741824e+09`,
		`mcp_platform_storage_backend_info{backend="seaweedfs"} 1`,
		`storage_orphaned_objects{purpose="resources"} 1`,
		`storage_dangling_references{purpose="resources"} 2`,
		`storage_scan_duration_seconds 1.5`,
		`storage_scan_objects 40`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}

// TestCapacity_UnknownReadsAreAbsent shows a sampler that has read nothing
// yet reports no table series and no scan series, rather than zeros a
// dashboard would read as an empty database.
func TestCapacity_UnknownReadsAreAbsent(t *testing.T) {
	m := newEnabledMetrics(t)
	if body := scrapeMetrics(t, m.Handler()); strings.Contains(body, "db_transaction_id_age ") {
		t.Error("capacity reported with no sampler")
	}
	m.RegisterCapacity(func(context.Context) CapacitySample { return CapacitySample{} })
	body := scrapeMetrics(t, m.Handler())
	for _, absent := range []string{"db_transaction_id_age ", "storage_scan_duration_seconds ", "storage_scan_objects "} {
		if strings.Contains(body, absent) {
			t.Errorf("%q reported before anything was read", absent)
		}
	}
}

func TestCapacity_NilSafe(_ *testing.T) {
	var m *Metrics
	m.RegisterCapacity(func(context.Context) CapacitySample { return CapacitySample{} })
}
