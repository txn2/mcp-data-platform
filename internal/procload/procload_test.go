package procload

import (
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// files answers readFile from a map; a path it does not hold does not exist.
func files(m map[string]string) readFile {
	return func(name string) ([]byte, error) {
		if v, ok := m[name]; ok {
			return []byte(v), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestMemoryLimit(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  int64
	}{
		"cgroup v2":               {map[string]string{"/sys/fs/cgroup/memory.max": "536870912\n"}, 536870912},
		"cgroup v2 unlimited":     {map[string]string{"/sys/fs/cgroup/memory.max": "max\n"}, 0},
		"cgroup v1":               {map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "1073741824"}, 1073741824},
		"cgroup v1 unlimited":     {map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712"}, 0},
		"the smaller one applies": {map[string]string{"/sys/fs/cgroup/memory.max": "900", "/sys/fs/cgroup/memory/memory.limit_in_bytes": "700"}, 700},
		"none":                    {nil, 0},
		"garbage":                 {map[string]string{"/sys/fs/cgroup/memory.max": "lots"}, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, memoryLimit(files(tc.files)))
		})
	}
}

func TestCPUCapacity(t *testing.T) {
	assert.InDelta(t, 1.5, cpuCapacity(files(map[string]string{"/sys/fs/cgroup/cpu.max": "150000 100000\n"})), 1e-9)
	fallback := cpuCapacity(files(nil))
	assert.GreaterOrEqual(t, fallback, 1.0, "with no quota the capacity is GOMAXPROCS")
	assert.InDelta(t, fallback, cpuCapacity(files(map[string]string{"/sys/fs/cgroup/cpu.max": "max 100000"})), 1e-9)
	assert.InDelta(t, fallback, cpuCapacity(files(map[string]string{"/sys/fs/cgroup/cpu.max": "x y"})), 1e-9)
}

// clock is a settable time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// TestSampler_CPUOverAWindow reads 1.5 CPU-seconds used over one second of two
// CPUs as 75%, and does not recompute inside a window.
func TestSampler_CPUOverAWindow(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	used := time.Duration(0)
	s := newSampler(1000, 2, c.now, func() (time.Duration, bool) { return used, true }, func() uint64 { return 250 })

	first := s.Sample()
	assert.False(t, first.CPUKnown, "no window has elapsed yet")
	assert.True(t, first.MemoryKnown)
	assert.InDelta(t, 25.0, first.MemoryPercent, 1e-9)

	c.t = c.t.Add(time.Second)
	used = 1500 * time.Millisecond
	got := s.Sample()
	assert.True(t, got.CPUKnown)
	assert.InDelta(t, 75.0, got.CPUPercent, 1e-9)

	c.t = c.t.Add(100 * time.Millisecond)
	used = 5 * time.Second
	assert.InDelta(t, 75.0, s.Sample().CPUPercent, 1e-9, "inside a window the last reading stands")
}

func TestSampler_UnknownSources(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	noCPU := func() (time.Duration, bool) { return 0, false }
	s := newSampler(0, 2, c.now, noCPU, func() uint64 { return 1 })
	got := s.Sample()
	assert.False(t, got.MemoryKnown, "no memory limit, nothing to measure against")
	assert.False(t, got.CPUKnown)

	s = newSampler(0, 0, c.now, func() (time.Duration, bool) { return 0, true }, func() uint64 { return 1 })
	assert.False(t, s.Sample().CPUKnown, "no capacity, nothing to measure against")
}

// TestSampler_StartsAWindowLate covers a CPU source that could not be read
// when the sampler was built and can be now.
func TestSampler_StartsAWindowLate(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	ok := false
	s := newSampler(0, 1, c.now, func() (time.Duration, bool) { return time.Second, ok }, func() uint64 { return 0 })
	ok = true
	assert.False(t, s.Sample().CPUKnown)
	c.t = c.t.Add(2 * time.Second)
	assert.True(t, s.Sample().CPUKnown)
}

// TestNew_ReadsThisProcess runs the real sources: they must produce a reading
// without error on the platform the tests run on.
func TestNew_ReadsThisProcess(t *testing.T) {
	s := New()
	assert.NotPanics(t, func() { _ = s.Sample() })
	assert.Positive(t, goMemory())
}
