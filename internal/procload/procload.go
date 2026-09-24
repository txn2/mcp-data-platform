// Package procload reports how much of the memory and CPU this process may use
// it is using now, as a percentage of what the container allows it.
//
// It exists for the managed-script run worker, which admits a new run only
// while the replica has headroom (#1843). It knows nothing about scripts: it
// reads the process's own counters and the limits the container set, and says
// when it cannot know one, so its caller never refuses work on a number it
// made up.
//
// Memory is the Go runtime's mapped memory less what it has returned to the
// operating system (runtime/metrics), against the smallest of the cgroup's
// memory limit and GOMEMLIMIT. CPU is the process's user and system time over
// a short window (getrusage), against the cgroup's CPU quota or, without one,
// GOMAXPROCS. The runtime's own /cpu/classes metrics are not used: they are
// only brought up to date at garbage collection, so between collections they
// describe a load that has already changed.
package procload

import (
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sample is one reading. A percentage whose Known flag is false could not be
// measured on this platform or under this configuration, and is zero.
type Sample struct {
	MemoryPercent float64
	MemoryKnown   bool
	CPUPercent    float64
	CPUKnown      bool
}

// cpuWindow is the shortest interval a CPU reading is computed over. A reading
// asked for sooner returns the last one: a percentage over a few milliseconds
// is a coin toss between one busy goroutine and none.
const cpuWindow = time.Second

// percent is the scale a Sample reports in.
const percent = 100

// cgroupRoot is where a container's own cgroup is mounted.
const cgroupRoot = "/sys/fs/cgroup"

// unlimitedV1 is the smallest value cgroup v1 reports for "no limit" (the page
// counter's maximum, rounded to a page); anything at or above it is no limit.
const unlimitedV1 = int64(1) << 60

// Sampler reads the process's load. Safe for concurrent use.
type Sampler struct {
	memLimit    int64
	cpuCapacity float64
	now         func() time.Time
	cpuTime     func() (time.Duration, bool)
	memUsed     func() uint64

	mu      sync.Mutex
	lastAt  time.Time
	lastCPU time.Duration
	cpuPct  float64
	cpuOK   bool
}

// New resolves the limits once and returns a sampler over them. The limits are
// the container's and do not change under a running process; GOMEMLIMIT can,
// and a change to it takes effect for a sampler built afterwards.
func New() *Sampler {
	return newSampler(memoryLimit(os.ReadFile), cpuCapacity(os.ReadFile), time.Now, processCPUTime, goMemory)
}

// MemoryLimit is the smallest memory limit in force on this process: the
// container's cgroup limit or GOMEMLIMIT, in bytes. Zero means none is set.
// It is what a limit stated as a share of the container's memory is a share
// of (#1861).
func MemoryLimit() int64 { return memoryLimit(os.ReadFile) }

// newSampler builds a sampler over the given sources, which is what a test
// substitutes.
func newSampler(memLimit int64, capacity float64, now func() time.Time,
	cpuTime func() (time.Duration, bool), memUsed func() uint64,
) *Sampler {
	s := &Sampler{memLimit: memLimit, cpuCapacity: capacity, now: now, cpuTime: cpuTime, memUsed: memUsed}
	if t, ok := cpuTime(); ok {
		s.lastAt, s.lastCPU = now(), t
	}
	return s
}

// Sample reads the current load.
func (s *Sampler) Sample() Sample {
	out := Sample{}
	if s.memLimit > 0 {
		out.MemoryPercent = float64(s.memUsed()) / float64(s.memLimit) * percent
		out.MemoryKnown = true
	}
	out.CPUPercent, out.CPUKnown = s.cpu()
	return out
}

// cpu returns the CPU percentage over the last window, starting a new window
// when the current one has run its length.
func (s *Sampler) cpu() (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cpuCapacity <= 0 {
		return 0, false
	}
	t, ok := s.cpuTime()
	if !ok {
		return 0, false
	}
	now := s.now()
	if s.lastAt.IsZero() {
		s.lastAt, s.lastCPU = now, t
		return s.cpuPct, s.cpuOK
	}
	elapsed := now.Sub(s.lastAt)
	if elapsed < cpuWindow {
		return s.cpuPct, s.cpuOK
	}
	s.cpuPct = float64(t-s.lastCPU) / (float64(elapsed) * s.cpuCapacity) * percent
	s.cpuOK = true
	s.lastAt, s.lastCPU = now, t
	return s.cpuPct, s.cpuOK
}

// goMemory is the memory the Go runtime holds from the operating system: what
// it has mapped less what it has released back, which is what GOMEMLIMIT
// itself is measured against.
func goMemory() uint64 {
	samples := []metrics.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	metrics.Read(samples)
	total, released := samples[0].Value.Uint64(), samples[1].Value.Uint64()
	if released > total {
		return 0
	}
	return total - released
}

// Number parsing: a cgroup file is base-10 and read into 64 bits.
const (
	decimal = 10
	bits64  = 64
)

// readFile is os.ReadFile's shape, substituted by tests.
type readFile func(name string) ([]byte, error)

// memoryLimit is the smallest memory limit in force: cgroup v2, cgroup v1, or
// GOMEMLIMIT. Zero means none is set, and memory is then not measured.
func memoryLimit(read readFile) int64 {
	var limits []int64
	if v, ok := readLimit(read, cgroupRoot+"/memory.max"); ok {
		limits = append(limits, v)
	}
	if v, ok := readLimit(read, cgroupRoot+"/memory/memory.limit_in_bytes"); ok && v < unlimitedV1 {
		limits = append(limits, v)
	}
	if v := debug.SetMemoryLimit(-1); v > 0 && v < math.MaxInt64 {
		limits = append(limits, v)
	}
	if len(limits) == 0 {
		return 0
	}
	return slices.Min(limits)
}

// readLimit reads one integer limit file, reporting false for an absent file,
// "max", or anything else that is not a positive integer.
func readLimit(read readFile, path string) (int64, bool) {
	raw, err := read(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(raw)), decimal, bits64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// cpuCapacity is how many CPUs the process may use: the cgroup v2 quota
// ("<quota> <period>" in cpu.max) when one is set, else GOMAXPROCS, which the
// runtime already sizes to a cgroup quota on Linux.
func cpuCapacity(read readFile) float64 {
	if raw, err := read(cgroupRoot + "/cpu.max"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) == 2 && fields[0] != "max" {
			quota, qerr := strconv.ParseFloat(fields[0], bits64)
			period, perr := strconv.ParseFloat(fields[1], bits64)
			if qerr == nil && perr == nil && quota > 0 && period > 0 {
				return quota / period
			}
		}
	}
	return float64(runtime.GOMAXPROCS(0))
}
