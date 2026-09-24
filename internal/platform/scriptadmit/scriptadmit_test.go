package scriptadmit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/procload"
)

// fakeLoad reports a fixed load. The zero value measures nothing, which is a
// replica whose limits are unknown: it never refuses on load.
type fakeLoad struct{ sample procload.Sample }

func (f fakeLoad) Sample() procload.Sample { return f.sample }

func memory(pct float64) procload.Sample {
	return procload.Sample{MemoryPercent: pct, MemoryKnown: true}
}

func TestAdmission_Defaults(t *testing.T) {
	a := Admission{}.WithDefaults()
	assert.True(t, a.Adaptive(), "adaptive is the default")
	assert.Equal(t, DefaultMinConcurrency, a.Min)
	assert.Equal(t, DefaultMaxConcurrency, a.Max)
	assert.InDelta(t, DefaultMaxMemoryPercent, a.MaxMemoryPercent, 0)
	assert.InDelta(t, DefaultMaxCPUPercent, a.MaxCPUPercent, 0)
	assert.InDelta(t, DefaultShedMemoryPercent, a.ShedMemoryPercent, 0)

	odd := Admission{Fixed: -3, Min: 20, Max: 4, MaxMemoryPercent: 95, ShedMemoryPercent: 80}.WithDefaults()
	assert.True(t, odd.Adaptive(), "a value at or below zero is unset")
	assert.Equal(t, 20, odd.Max, "the ceiling is never below the floor")
	assert.InDelta(t, 95.0, odd.ShedMemoryPercent, 0, "a run is never shed below the admission threshold")
}

func TestAdmitter_Fixed(t *testing.T) {
	a := NewAdmitter(Admission{Fixed: 2}, fakeLoad{memory(99)})
	ok, _ := a.Admit(1)
	assert.True(t, ok, "a fixed number ignores load")
	ok, reason := a.Admit(2)
	assert.False(t, ok)
	assert.Equal(t, RefusedCeiling, reason)
	assert.False(t, a.Shed(5), "a fixed number never sheds")
}

func TestAdmitter_Adaptive(t *testing.T) {
	adm := Admission{Min: 2, Max: 4}.WithDefaults()
	cases := map[string]struct {
		load     procload.Sample
		inFlight int
		ok       bool
		reason   string
	}{
		"under the floor, whatever the load": {memory(99), 1, true, ""},
		"headroom":                           {procload.Sample{MemoryPercent: 50, MemoryKnown: true, CPUPercent: 50, CPUKnown: true}, 2, true, ""},
		"memory":                             {memory(70), 2, false, RefusedMemory},
		"cpu":                                {procload.Sample{CPUPercent: 80, CPUKnown: true}, 3, false, RefusedCPU},
		"ceiling":                            {procload.Sample{}, 4, false, RefusedCeiling},
		"nothing measured":                   {procload.Sample{}, 3, true, ""},
		"an unknown reading never refuses":   {procload.Sample{MemoryPercent: 99, CPUPercent: 99}, 3, true, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ok, reason := NewAdmitter(adm, fakeLoad{tc.load}).Admit(tc.inFlight)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.reason, reason)
		})
	}
}

func TestAdmitter_Shed(t *testing.T) {
	adm := Admission{}.WithDefaults()
	assert.True(t, NewAdmitter(adm, fakeLoad{memory(95)}).Shed(2))
	assert.False(t, NewAdmitter(adm, fakeLoad{memory(95)}).Shed(1), "the last run is never shed")
	assert.False(t, NewAdmitter(adm, fakeLoad{memory(80)}).Shed(3))
	assert.False(t, NewAdmitter(adm, fakeLoad{}).Shed(3), "an unknown reading never sheds")
}

// TestNewAdmitter_ReadsThisProcessWithoutASource covers the production path:
// no source means procload, which answers on any platform.
func TestNewAdmitter_ReadsThisProcessWithoutASource(t *testing.T) {
	ok, _ := NewAdmitter(Admission{}, nil).Admit(0)
	assert.True(t, ok, "the floor admits whatever the load")
}

func TestConfig_Admission(t *testing.T) {
	cases := map[string]struct {
		in       string
		fixed    int
		adaptive bool
	}{
		"unset is adaptive":        {"", 0, true},
		"adaptive":                 {" Adaptive ", 0, true},
		"a number is fixed":        {"3", 3, false},
		"one is the serial worker": {"1", 1, false},
		"zero falls back":          {"0", 0, true},
		"negative falls back":      {"-2", 0, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			adm, err := Config{Concurrency: tc.in, MaxConcurrency: 8}.Admission()
			require.NoError(t, err)
			assert.Equal(t, tc.fixed, adm.Fixed)
			assert.Equal(t, tc.adaptive, adm.Adaptive())
			assert.Equal(t, 8, adm.Max)
		})
	}
	_, err := Config{Concurrency: "adpative"}.Admission()
	require.ErrorContains(t, err, "scripts.worker.concurrency")
}

// TestAdmitter_OverShed holds #1861's last-resort guard: past the shed
// threshold the only run executing is stopped, under every admission, and a
// reading the platform cannot take never stops one.
func TestAdmitter_OverShed(t *testing.T) {
	adm := Admission{}.WithDefaults()
	over, reason := NewAdmitter(adm, fakeLoad{memory(95)}).OverShed()
	assert.True(t, over)
	assert.Contains(t, reason, "95%")
	assert.Contains(t, reason, "the only one executing")
	over, _ = NewAdmitter(Admission{Fixed: 1}.WithDefaults(), fakeLoad{memory(95)}).OverShed()
	assert.True(t, over, "a fixed admission is guarded too")
	over, _ = NewAdmitter(adm, fakeLoad{memory(80)}).OverShed()
	assert.False(t, over)
	over, _ = NewAdmitter(adm, fakeLoad{}).OverShed()
	assert.False(t, over, "an unknown reading never stops a run")
}

func TestConfig_RunMemoryBudget(t *testing.T) {
	const limit = int64(512 << 20)
	cases := map[string]struct {
		in   string
		want int64
	}{
		"unset is half the limit": {"", limit / 2},
		"a share of the limit":    {"25%", limit / 4},
		"mebibytes":               {"300MiB", 300 << 20},
		"gibibytes with a space":  {"1.5 GiB", 3 << 29},
		"kibibytes":               {"512KiB", 512 << 10},
		"megabytes":               {"200MB", 200_000_000},
		"gigabytes":               {"1GB", 1_000_000_000},
		"kilobytes":               {"10KB", 10_000},
		"bytes":                   {"4096B", 4096},
		"unlimited":               {"Unlimited", 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Config{MaxRunMemory: tc.in}.RunMemoryBudget(limit)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	unset, err := Config{}.RunMemoryBudget(0)
	require.NoError(t, err)
	assert.Zero(t, unset, "no limit read and none set is no budget")

	for _, bad := range []string{"lots", "0%", "150%", "-5MiB", "MiB", "x%"} {
		_, err := Config{MaxRunMemory: bad}.RunMemoryBudget(limit)
		require.ErrorContains(t, err, "scripts.worker.max_run_memory", bad)
	}
}

func TestConfig_ProcessRunMemoryBudget(t *testing.T) {
	assert.Equal(t, int64(300<<20), Config{MaxRunMemory: "300MiB"}.ProcessRunMemoryBudget())
	assert.Zero(t, Config{MaxRunMemory: "unlimited"}.ProcessRunMemoryBudget())
	assert.Zero(t, Config{MaxRunMemory: "nonsense"}.ProcessRunMemoryBudget(), "unreadable is no budget; validation refuses it at startup")
}
