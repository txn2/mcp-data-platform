package capacity

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// The environment the service reads. Capacity reporting is an observability
// concern, configured beside OTEL_* and the other MCP_PLATFORM_* telemetry
// knobs rather than in the platform's YAML.
const (
	EnvTableInterval = "MCP_PLATFORM_CAPACITY_INTERVAL"
	EnvScanInterval  = "MCP_PLATFORM_STORAGE_SCAN_INTERVAL"
	EnvOrphanGrace   = "MCP_PLATFORM_STORAGE_ORPHAN_GRACE"
	EnvBudgets       = "MCP_PLATFORM_STORAGE_BUDGETS"
	EnvBackend       = "MCP_PLATFORM_STORAGE_BACKEND"
	EnvReclaim       = "MCP_PLATFORM_STORAGE_RECLAIM"
	EnvReclaimAge    = "MCP_PLATFORM_STORAGE_RECLAIM_AGE"
)

// The reclaim modes (#2470): what the full listing does with the orphans it
// finds. Off counts them only, report also logs how many a delete pass would
// remove, and delete removes them.
const (
	ReclaimOff    = "off"
	ReclaimReport = "report"
	ReclaimDelete = "delete"
)

// reclaimModes is every mode MCP_PLATFORM_STORAGE_RECLAIM takes.
//
//nolint:gochecknoglobals // a read-only list.
var reclaimModes = []string{ReclaimOff, ReclaimReport, ReclaimDelete}

// Config is the service's cadence and the operator's budgets.
type Config struct {
	TableInterval time.Duration
	ScanInterval  time.Duration
	FlushInterval time.Duration
	OrphanGrace   time.Duration
	// Budgets is the byte budget per bucket an operator set.
	Budgets []observability.BucketBudget
	// Backend, when set, names the object store kind instead of asking it.
	Backend string
	// Reclaim is what a listing does with its orphans (ReclaimOff,
	// ReclaimReport, ReclaimDelete); empty is ReclaimDelete.
	Reclaim string
	// ReclaimAge is how old an orphan must be before it is reclaimed, apart
	// from the counting grace: a multipart upload's LastModified is when it
	// began, so a long extract can read older than the grace before its row
	// is written.
	ReclaimAge time.Duration
}

// withDefaults fills every unset interval.
func (c Config) withDefaults() Config {
	if c.TableInterval <= 0 {
		c.TableInterval = DefaultTableInterval
	}
	if c.ScanInterval <= 0 {
		c.ScanInterval = DefaultScanInterval
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = DefaultFlushInterval
	}
	if c.OrphanGrace < 0 {
		c.OrphanGrace = DefaultOrphanGrace
	}
	if c.Reclaim == "" {
		c.Reclaim = ReclaimDelete
	}
	if c.ReclaimAge <= 0 {
		c.ReclaimAge = DefaultReclaimAge
	}
	return c
}

// ConfigFromEnv reads the configuration. An unset variable takes its default.
// A value that does not parse takes its default too, and is named in the
// error returned beside a configuration that keeps every variable that did
// parse: a typo in one never drops the budgets set in another. An unset
// orphan grace is DefaultOrphanGrace; zero is accepted and counts an object
// the moment it has no row.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		OrphanGrace: DefaultOrphanGrace, Backend: strings.TrimSpace(os.Getenv(EnvBackend)),
		Reclaim: strings.ToLower(strings.TrimSpace(os.Getenv(EnvReclaim))),
	}
	var errs []error
	for _, d := range []struct {
		env string
		dst *time.Duration
	}{
		{EnvTableInterval, &cfg.TableInterval},
		{EnvScanInterval, &cfg.ScanInterval},
		{EnvOrphanGrace, &cfg.OrphanGrace},
		{EnvReclaimAge, &cfg.ReclaimAge},
	} {
		v := strings.TrimSpace(os.Getenv(d.env))
		if v == "" {
			continue
		}
		parsed, err := time.ParseDuration(v)
		if err != nil || parsed < 0 {
			errs = append(errs, fmt.Errorf("capacity: %s: %q is not a duration", d.env, v))
			continue
		}
		*d.dst = parsed
	}
	budgets, err := ParseBudgets(os.Getenv(EnvBudgets))
	if err != nil {
		errs = append(errs, fmt.Errorf("capacity: %s: %w", EnvBudgets, err))
	}
	cfg.Budgets = budgets
	if cfg.Backend != "" && !slices.Contains(backends, cfg.Backend) {
		errs = append(errs, fmt.Errorf("capacity: %s: %q is not one of %s", EnvBackend, cfg.Backend, strings.Join(backends, ", ")))
		cfg.Backend = ""
	}
	if cfg.Reclaim != "" && !slices.Contains(reclaimModes, cfg.Reclaim) {
		errs = append(errs, fmt.Errorf("capacity: %s: %q is not one of %s", EnvReclaim, cfg.Reclaim, strings.Join(reclaimModes, ", ")))
		cfg.Reclaim = ""
	}
	return cfg.withDefaults(), errors.Join(errs...)
}

// backends is every kind the backend label takes.
//
//nolint:gochecknoglobals // a read-only list.
var backends = []string{
	observability.StorageBackendSeaweedFS,
	observability.StorageBackendS3, observability.StorageBackendGCS, observability.StorageBackendOther,
}

// byteUnits are the suffixes a budget may carry, longest first so "GiB" is
// matched before "B".
//
//nolint:gochecknoglobals // a read-only table.
var byteUnits = []struct {
	suffix string
	mult   int64
}{
	{"TiB", 1 << 40},
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
	{"TB", 1e12},
	{"GB", 1e9},
	{"MB", 1e6},
	{"KB", 1e3},
	{"B", 1},
}

// ParseBudgets reads "bucket=size,bucket=size", where size is a whole number
// of bytes or one with a unit (KiB, MiB, GiB, TiB, or KB, MB, GB, TB). Empty
// is no budgets.
func ParseBudgets(v string) ([]observability.BucketBudget, error) {
	var out []observability.BucketBudget
	for item := range strings.SplitSeq(v, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		bucket, size, ok := strings.Cut(item, "=")
		bucket = strings.TrimSpace(bucket)
		if !ok || bucket == "" {
			return nil, fmt.Errorf("budget %q is not bucket=size", item)
		}
		n, err := parseBytes(strings.TrimSpace(size))
		if err != nil {
			return nil, fmt.Errorf("bucket %s: %w", bucket, err)
		}
		out = append(out, observability.BucketBudget{Bucket: bucket, Bytes: n})
	}
	return out, nil
}

// The base and width a size is parsed in.
const (
	decimal = 10
	bits64  = 64
)

// parseBytes reads one size.
func parseBytes(s string) (int64, error) {
	mult := int64(1)
	for _, u := range byteUnits {
		if rest, ok := strings.CutSuffix(s, u.suffix); ok {
			s, mult = strings.TrimSpace(rest), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(s, decimal, bits64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("size %q is not a positive whole number", s)
	}
	return n * mult, nil
}
