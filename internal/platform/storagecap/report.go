package storagecap

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
)

// Report is what GET /api/v1/admin/storage answers: every store the platform
// writes to, used and free, with its growth and projection, and the detail
// behind each.
type Report struct {
	GeneratedAt time.Time       `json:"generated_at"`
	HistoryDays int             `json:"history_days"`
	Stores      []StoreView     `json:"stores"`
	Database    DatabaseView    `json:"database"`
	Buckets     []BucketView    `json:"buckets"`
	Reconciles  []ReconcileView `json:"reconciles"`
	LastScan    *ScanView       `json:"last_scan"`
	SeaweedFS   *ClusterView    `json:"seaweedfs"`
	// SeaweedFSBuckets are the buckets whose endpoint answers as SeaweedFS:
	// the settings offer the cluster's address only where one does.
	SeaweedFSBuckets []string    `json:"seaweedfs_buckets"`
	Spools           []SpoolView `json:"spools"`
}

// StoreView is one store: the database, the SeaweedFS cluster, a bucket, or a
// replica's spool volume.
type StoreView struct {
	// Store is the key its history is kept under: database, seaweedfs,
	// bucket:<name>, spool:<replica>.
	Store string `json:"store"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	// UsedBytes is what the store holds. TotalBytes is its capacity, nil
	// where the platform knows none: a database with no limit set, a bucket
	// with no budget.
	UsedBytes  int64  `json:"used_bytes"`
	TotalBytes *int64 `json:"total_bytes"`
	// CapacityFrom names where a bucket's capacity comes from: "budget", or
	// "seaweedfs" for a bucket on the cluster with no budget, whose capacity
	// is what it holds plus the cluster's free space. Empty for the rest.
	CapacityFrom string   `json:"capacity_from,omitempty"`
	FreeBytes    *int64   `json:"free_bytes"`
	PercentUsed  *float64 `json:"percent_used"`
	Forecast
	// Level is the worse of the store's two alert levels under the current
	// thresholds, empty when neither is crossed.
	Level string `json:"level,omitempty"`
}

// DatabaseView is the database's size, its limit and its largest relations.
type DatabaseView struct {
	Bytes      int64       `json:"bytes"`
	LimitBytes *int64      `json:"limit_bytes"`
	Tables     []TableView `json:"tables"`
}

// TableView is one table or index.
type TableView struct {
	Name  string `json:"name"`
	Index bool   `json:"index"`
	Bytes int64  `json:"bytes"`
	Rows  int64  `json:"rows"`
}

// Findings is every store at warning or critical under settings, by percent
// used and by days until it fills, and marks each store's level on r.
func Findings(r Report, settings Settings, pageURL string) []levelalert.Finding {
	settings = settings.normalized()
	out := []levelalert.Finding{}
	for i := range r.Stores {
		st := &r.Stores[i]
		var worst levelalert.Level
		if st.PercentUsed != nil {
			if lvl := settings.UsedPercent.Of(*st.PercentUsed); lvl != "" {
				worst = levelalert.Worse(worst, lvl)
				limit := map[levelalert.Level]float64{levelalert.Warning: settings.UsedPercent.Warning, levelalert.Critical: settings.UsedPercent.Critical}[lvl]
				out = append(out, levelalert.Finding{
					Key: "storage:" + st.Store + ":used", Level: lvl, Link: pageURL,
					Title: fmt.Sprintf("%s is %.0f%% full", st.Name, *st.PercentUsed),
					Body: fmt.Sprintf("%s used of %s, %s free. The %s threshold is %.0f%%.",
						bytesText(st.UsedBytes), bytesText(*st.TotalBytes), bytesText(*st.FreeBytes), lvl, limit),
				})
			}
		}
		if st.DaysLeft != nil && st.ExhaustionAt != nil {
			if lvl := settings.DaysLeft.Of(*st.DaysLeft); lvl != "" {
				worst = levelalert.Worse(worst, lvl)
				limit := map[levelalert.Level]float64{levelalert.Warning: settings.DaysLeft.Warning, levelalert.Critical: settings.DaysLeft.Critical}[lvl]
				out = append(out, levelalert.Finding{
					Key: "storage:" + st.Store + ":exhaustion", Level: lvl, Link: pageURL,
					Title: fmt.Sprintf("%s fills in %s", st.Name, daysText(*st.DaysLeft)),
					Body: fmt.Sprintf("At the %s growth rate of %s a day it fills around %s. The %s threshold is %.0f days.",
						st.RateUsed, bytesText(int64(rateOf(st.Forecast))), st.ExhaustionAt.UTC().Format("2006-01-02"), lvl, limit),
				})
			}
		}
		st.Level = string(worst)
	}
	return out
}

// rateOf is the rate a forecast projects with.
func rateOf(f Forecast) float64 {
	if f.RateUsed == rate30dName && f.Rate30d != nil {
		return *f.Rate30d
	}
	if f.Rate7d != nil {
		return *f.Rate7d
	}
	return 0
}

// daysText is a day count as a reader says it.
func daysText(d float64) string {
	if d < 1 {
		return "less than a day"
	}
	n := int(math.Round(d))
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

// bytesText is a byte count in binary units.
func bytesText(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	prefixes := strings.Split("K M G T P", " ")
	v, exp := float64(n), 0
	for v >= unit && exp < len(prefixes) {
		v /= unit
		exp++
	}
	return fmt.Sprintf("%.1f %siB", v, prefixes[exp-1])
}
