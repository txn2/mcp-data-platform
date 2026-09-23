// Package scriptadmit decides how many managed-script runs one replica
// executes at once, and owns the scripts.worker settings that say so (#1843).
//
// A run spends most of its life waiting on a query engine or an upstream API,
// so a replica's capacity is its free memory and CPU, not a count. Admission
// is adaptive by default: another run is claimed only while the process's
// memory and CPU, as internal/procload measures them, are under their
// thresholds, between a floor that keeps a replica making progress and a
// ceiling it never passes. The run worker in internal/platform/scriptexec asks
// before every claim and, past the shed threshold, stops its newest run.
package scriptadmit

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/procload"
)

// Admission defaults (#1843). A run spends most of its life waiting on a query
// engine or an upstream API, so a replica's capacity is its free memory and
// CPU, not a count; these are the bounds that turn that into a claim decision.
const (
	// DefaultMaxConcurrency is the ceiling adaptive admission never passes,
	// whatever headroom the replica reports.
	DefaultMaxConcurrency = 16

	// DefaultMinConcurrency is how many runs adaptive admission always admits,
	// whatever the load, so a replica never stops making progress.
	DefaultMinConcurrency = 1

	// DefaultMaxMemoryPercent is the memory, as a share of the container's
	// limit, above which no further run is claimed. The rest is headroom for
	// the runs already admitted, which keep growing after the decision.
	DefaultMaxMemoryPercent = 70

	// DefaultMaxCPUPercent is the CPU, as a share of the container's quota,
	// above which no further run is claimed.
	DefaultMaxCPUPercent = 75

	// DefaultShedMemoryPercent is the memory above which the most recently
	// started run is stopped and requeued, so a replica sheds work before the
	// container is killed with all of it.
	DefaultShedMemoryPercent = 90
)

// Refusal reasons, as the admission metric labels them.
const (
	RefusedCeiling = "ceiling"
	RefusedMemory  = "memory"
	RefusedCPU     = "cpu"
)

// Admission is how many runs this replica executes at once.
//
// Fixed > 0 is a fixed number, and 1 is the one-at-a-time worker every
// replica ran before #1843. Fixed == 0 is adaptive: a run is claimed while
// fewer than Min are executing, or while fewer than Max are and the replica's
// memory and CPU are both under their thresholds. A measurement the platform
// cannot take (no container memory limit, no CPU time on this OS) never
// refuses; the ceiling still applies.
type Admission struct {
	Fixed             int
	Min               int
	Max               int
	MaxMemoryPercent  float64
	MaxCPUPercent     float64
	ShedMemoryPercent float64
}

// Adaptive reports whether admission follows the replica's load.
func (a Admission) Adaptive() bool { return a.Fixed <= 0 }

// WithDefaults fills every unset bound. A value at or below zero is unset.
func (a Admission) WithDefaults() Admission {
	if a.Fixed < 0 {
		a.Fixed = 0
	}
	a.Min = positiveOr(a.Min, DefaultMinConcurrency)
	a.Max = max(positiveOr(a.Max, DefaultMaxConcurrency), a.Min)
	a.MaxMemoryPercent = positiveFloatOr(a.MaxMemoryPercent, DefaultMaxMemoryPercent)
	a.MaxCPUPercent = positiveFloatOr(a.MaxCPUPercent, DefaultMaxCPUPercent)
	a.ShedMemoryPercent = max(positiveFloatOr(a.ShedMemoryPercent, DefaultShedMemoryPercent), a.MaxMemoryPercent)
	return a
}

func positiveOr(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}

func positiveFloatOr(v, fallback float64) float64 {
	if v <= 0 {
		return fallback
	}
	return v
}

// LoadSource is the replica's load, as procload measures it.
type LoadSource interface {
	Sample() procload.Sample
}

// Admitter applies an Admission to the load a source reports.
type Admitter struct {
	Admission
	load LoadSource
}

// NewAdmitter applies adm, with its unset bounds defaulted, to the load a
// source reports; a nil source reads this process through procload.
func NewAdmitter(adm Admission, load LoadSource) Admitter {
	if load == nil {
		load = procload.New()
	}
	return Admitter{Admission: adm.WithDefaults(), load: load}
}

// Admit decides whether one more run may be claimed while inFlight are
// executing, and names the reason when it may not.
func (a Admitter) Admit(inFlight int) (ok bool, reason string) {
	if !a.Adaptive() {
		if inFlight < a.Fixed {
			return true, ""
		}
		return false, RefusedCeiling
	}
	if inFlight < a.Min {
		return true, ""
	}
	if inFlight >= a.Max {
		return false, RefusedCeiling
	}
	s := a.load.Sample()
	if s.MemoryKnown && s.MemoryPercent >= a.MaxMemoryPercent {
		return false, RefusedMemory
	}
	if s.CPUKnown && s.CPUPercent >= a.MaxCPUPercent {
		return false, RefusedCPU
	}
	return true, ""
}

// Shed reports whether a run should be stopped to relieve memory while live
// runs are executing. Only adaptive admission sheds, and never its last run: a
// single run over the line is that script's own size, and stopping it would
// only rebuild the same heap wherever it ran next.
func (a Admitter) Shed(live int) bool {
	if !a.Adaptive() || live <= 1 {
		return false
	}
	s := a.load.Sample()
	return s.MemoryKnown && s.MemoryPercent >= a.ShedMemoryPercent
}

// concurrencyAdaptive is the Config.Concurrency value that follows the load.
const concurrencyAdaptive = "adaptive"

// Config is the capacity half of the scripts.worker configuration block,
// inlined there beside enabled. Every field is optional; zero or a negative
// number takes the default.
type Config struct {
	// Concurrency is "adaptive" (the default, as are empty, zero and a
	// negative number) or a whole number of runs executed at once; 1 is the
	// one-at-a-time worker of earlier releases.
	Concurrency string `yaml:"concurrency"`
	// MaxConcurrency and MinConcurrency bound adaptive admission.
	MaxConcurrency int `yaml:"max_concurrency"`
	MinConcurrency int `yaml:"min_concurrency"`
	// MaxMemoryPercent and MaxCPUPercent are the shares of the container's
	// memory limit and CPU quota above which no further run is claimed;
	// ShedMemoryPercent is the memory above which the newest run is stopped.
	MaxMemoryPercent  float64 `yaml:"max_memory_percent"`
	MaxCPUPercent     float64 `yaml:"max_cpu_percent"`
	ShedMemoryPercent float64 `yaml:"shed_memory_percent"`
	// RunTimeout, MaxSteps and MaxQueryRows are a platform run's ceilings:
	// wall clock, interpreter steps, and the rows one platform.query may
	// return. The worker derives the claim lease from RunTimeout.
	RunTimeout   time.Duration `yaml:"run_timeout"`
	MaxSteps     int64         `yaml:"max_steps"`
	MaxQueryRows int           `yaml:"max_query_rows"`
	// ResultMaxBytes caps the value a run hands back with platform.result
	// (#1845, default 1 MiB).
	ResultMaxBytes int `yaml:"result_max_bytes"`
}

// Admission reads the configured admission. A Concurrency that is neither
// "adaptive" nor a whole number is refused, so a misspelling fails at startup
// rather than quietly running adaptive.
func (c Config) Admission() (Admission, error) {
	adm := Admission{
		Min: c.MinConcurrency, Max: c.MaxConcurrency,
		MaxMemoryPercent: c.MaxMemoryPercent, MaxCPUPercent: c.MaxCPUPercent,
		ShedMemoryPercent: c.ShedMemoryPercent,
	}
	raw := strings.TrimSpace(c.Concurrency)
	if raw == "" || strings.EqualFold(raw, concurrencyAdaptive) {
		return adm, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return adm, fmt.Errorf("scripts.worker.concurrency: %q is neither %q nor a whole number of runs", c.Concurrency, concurrencyAdaptive)
	}
	adm.Fixed = max(n, 0)
	return adm, nil
}
