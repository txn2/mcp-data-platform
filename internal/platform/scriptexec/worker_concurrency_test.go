package scriptexec

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptadmit"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlive"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/procload"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeLoad reports a fixed load. The zero value measures nothing, which is a
// replica whose limits are unknown: it never refuses on load.
type fakeLoad struct{ sample procload.Sample }

func (f fakeLoad) Sample() procload.Sample { return f.sample }

// switchLoad reports a load a test changes while the worker runs.
type switchLoad struct {
	mu     sync.Mutex
	sample procload.Sample
}

func (s *switchLoad) Sample() procload.Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sample
}

func (s *switchLoad) set(sample procload.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sample = sample
}

func memory(pct float64) procload.Sample {
	return procload.Sample{MemoryPercent: pct, MemoryKnown: true}
}

func TestLeaseFor_OutlivesTheTimeout(t *testing.T) {
	assert.Equal(t, 20*time.Minute, LeaseFor(15*time.Minute))
	assert.Greater(t, LeaseFor(time.Hour), time.Hour)
}

// gateExecutor holds every run until the test opens the gate, counting how
// many execute at once and how often each run id is executed.
type gateExecutor struct {
	mu      sync.Mutex
	current int
	peak    int
	seen    map[string]int
	entered chan string
	open    chan struct{}
	// canceled counts runs whose context ended before the gate opened.
	canceled atomic.Int32
}

func newGateExecutor(n int) *gateExecutor {
	return &gateExecutor{seen: map[string]int{}, entered: make(chan string, n), open: make(chan struct{})}
}

func (g *gateExecutor) execute(ctx context.Context, run *script.Run, _ *script.Script, _ *script.Version) attempt {
	g.mu.Lock()
	g.current++
	g.peak = max(g.peak, g.current)
	g.seen[run.ID]++
	g.mu.Unlock()
	g.entered <- run.ID
	defer func() {
		g.mu.Lock()
		g.current--
		g.mu.Unlock()
	}()
	select {
	case <-g.open:
		return succeeded
	case <-ctx.Done():
		g.canceled.Add(1)
		return attempt{result: script.RunResult{Status: script.RunStatusFailed, Error: "context canceled"}}
	}
}

func (g *gateExecutor) inFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.current
}

// queueWorker assembles a worker over n pending runs.
func queueWorker(t *testing.T, n int, adm scriptadmit.Admission, load scriptadmit.LoadSource, exec executor) (*worker, *fakeRuns) {
	t.Helper()
	sc, v, base := executableState()
	runs := &fakeRuns{}
	for i := range n {
		r := *base
		r.ID = fmt.Sprintf("dpx_%d", i+1)
		require.NoError(t, runs.Enqueue(context.Background(), &r))
	}
	w := newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner: exec, load: load, admission: adm, pollEvery: 20 * time.Millisecond,
	})
	return w, runs
}

// waitEntered collects n run ids from the executor, failing if they do not
// arrive.
func waitEntered(t *testing.T, g *gateExecutor, n int) {
	t.Helper()
	for range n {
		select {
		case <-g.entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("only some runs started; wanted %d", n)
		}
	}
}

// TestWorker_FixedConcurrencyHoldsNRunsAtOnce is #1843's acceptance: with
// concurrency N and N+1 runs queued, one replica executes N at the same time,
// leaves the last queued until a slot frees, and never executes a run twice.
func TestWorker_FixedConcurrencyHoldsNRunsAtOnce(t *testing.T) {
	const n = 3
	g := newGateExecutor(n + 1)
	w, runs := queueWorker(t, n+1, scriptadmit.Admission{Fixed: n}, fakeLoad{}, g)
	w.Start(context.Background())
	t.Cleanup(func() { w.Stop(context.Background()) })

	waitEntered(t, g, n)
	// Several polls pass; the (N+1)th must still be waiting.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, n, g.inFlight())
	select {
	case id := <-g.entered:
		t.Fatalf("run %s started past the ceiling", id)
	default:
	}

	close(g.open)
	waitEntered(t, g, 1)
	require.Eventually(t, func() bool { return len(runs.results()) == n+1 }, 3*time.Second, 5*time.Millisecond)
	g.mu.Lock()
	defer g.mu.Unlock()
	assert.Equal(t, n, g.peak)
	for id, times := range g.seen {
		assert.Equal(t, 1, times, "run %s executed more than once", id)
	}
}

// TestWorker_ConcurrencyOneIsSerial is the pre-#1843 behavior, kept for an
// operator who wants the old memory guarantee.
func TestWorker_ConcurrencyOneIsSerial(t *testing.T) {
	g := newGateExecutor(3)
	close(g.open)
	w, runs := queueWorker(t, 3, scriptadmit.Admission{Fixed: 1}, fakeLoad{}, g)
	w.Start(context.Background())
	t.Cleanup(func() { w.Stop(context.Background()) })

	require.Eventually(t, func() bool { return len(runs.results()) == 3 }, 3*time.Second, 5*time.Millisecond)
	g.mu.Lock()
	defer g.mu.Unlock()
	assert.Equal(t, 1, g.peak)
}

// TestWorker_AdaptiveFollowsTheLoad covers the addendum: an I/O-bound replica
// admits more than one run, stops admitting while memory is past its
// threshold, never drops below the floor, and admits again when memory falls.
func TestWorker_AdaptiveFollowsTheLoad(t *testing.T) {
	load := &switchLoad{sample: memory(90)}
	g := newGateExecutor(4)
	w, _ := queueWorker(t, 4, scriptadmit.Admission{Max: 3}, load, g)
	w.Start(context.Background())
	t.Cleanup(func() { w.Stop(context.Background()) })

	waitEntered(t, g, 1)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, g.inFlight(), "over the threshold only the floor is admitted, so the replica keeps making progress")

	load.set(memory(20))
	waitEntered(t, g, 2)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 3, g.inFlight(), "with headroom it admits up to the ceiling and no further")
	close(g.open)
}

// TestWorker_ShedsTheNewestRunUnderMemoryPressure stops the most recently
// started run and requeues it on the platform's retry budget, leaving the
// older run executing.
func TestWorker_ShedsTheNewestRunUnderMemoryPressure(t *testing.T) {
	load := &switchLoad{}
	g := newGateExecutor(2)
	w, runs := queueWorker(t, 2, scriptadmit.Admission{}, load, g)
	w.drain()
	waitEntered(t, g, 2)

	load.set(memory(95))
	w.maybeShed()
	require.Eventually(t, func() bool { return g.canceled.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
	load.set(memory(50))
	w.maybeShed() // back under the threshold: the last run is left alone

	close(g.open)
	w.wg.Wait()
	require.Len(t, runs.retried, 1)
	assert.Contains(t, runs.retried[0], "memory pressure")
	require.Len(t, runs.ends, 1)
	assert.Equal(t, runstate.AttemptShed, runs.ends[0].outcome, "a shed run's history says it was shed")
	require.Len(t, runs.results(), 1)
	assert.Equal(t, script.RunStatusSucceeded, runs.results()[0].Status)
}

// TestWorker_FailsTheOnlyRunPastTheShedThreshold holds #1861's last-resort
// guard: with one run left and memory still past the threshold, requeueing it
// would rebuild the same heap elsewhere, and leaving it would let the kernel
// kill the replica, so the run fails on memory and is not retried.
func TestWorker_FailsTheOnlyRunPastTheShedThreshold(t *testing.T) {
	load := &switchLoad{}
	g := newGateExecutor(1)
	w, runs := queueWorker(t, 1, scriptadmit.Admission{Fixed: 1}, load, g)
	w.drain()
	waitEntered(t, g, 1)

	load.set(memory(95))
	w.maybeShed()
	require.Eventually(t, func() bool { return g.canceled.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
	w.maybeShed() // already stopped: not counted again

	close(g.open)
	w.wg.Wait()
	assert.Empty(t, runs.retried, "a run over the line on its own is not requeued")
	require.Len(t, runs.results(), 1)
	res := runs.results()[0]
	assert.Equal(t, script.RunStatusFailed, res.Status)
	assert.Equal(t, runstate.CauseMemory, res.Cause)
	assert.Contains(t, res.Error, "the only one executing")
	assert.Equal(t, int32(1), g.canceled.Load())
}

// TestWorker_RecordsAdmissionAndQueueWait reads the two #1843 series back from
// the exporter.
func TestWorker_RecordsAdmissionAndQueueWait(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	g := newGateExecutor(2)
	w, _ := queueWorker(t, 2, scriptadmit.Admission{Fixed: 1}, fakeLoad{}, g)
	w.cfg.metrics = m
	w.drain()
	waitEntered(t, g, 1)
	w.drain() // at the ceiling with work queued
	close(g.open)
	w.wg.Wait()

	body := scrapeWorkerMetrics(t, m)
	assert.Contains(t, body, `script_run_admission_refusals_total{`)
	assert.Contains(t, body, `reason="ceiling"`)
	assert.Contains(t, body, "script_run_queue_wait_seconds")
}

func TestQueueWait(t *testing.T) {
	assert.Zero(t, queueWait(&script.Run{}))
	due := time.Now().Add(-time.Minute)
	started := due.Add(30 * time.Second)
	assert.Equal(t, 30*time.Second, queueWait(&script.Run{ScheduledFor: due, StartedAt: &started}))
	assert.GreaterOrEqual(t, queueWait(&script.Run{ScheduledFor: due}), time.Minute)
}

// TestNew_CarriesTheConfiguredLimitsToTheWorkerAndRunner proves the settings
// reach what uses them: the worker's lease follows the configured timeout,
// its admission is the configured one, and the runner executes under the
// configured ceilings.
func TestNew_CarriesTheConfiguredLimitsToTheWorkerAndRunner(t *testing.T) {
	sc, v, _ := executableState()
	h := New(Config{
		Runs: &fakeRuns{}, Scripts: &fakeScripts{script: sc}, Versions: &fakeVersions{version: v},
		Limits:    scriptrun.PlatformLimits{Timeout: 40 * time.Minute, MaxSteps: 7, MaxRows: 9},
		Admission: scriptadmit.Admission{Fixed: 2},
	})
	require.NotNil(t, h)
	assert.Equal(t, 45*time.Minute, h.worker.cfg.lease)
	assert.Equal(t, 2, h.worker.admit.Fixed)
	r, ok := h.worker.cfg.runner.(*runner)
	require.True(t, ok)
	assert.Equal(t, scriptrun.PlatformLimits{
		Timeout: 40 * time.Minute, MaxSteps: 7, MaxRows: 9, ResultMaxBytes: scriptlive.DefaultMaxResultBytes,
	}, r.limits)

	d := New(Config{Runs: &fakeRuns{}, Scripts: &fakeScripts{script: sc}, Versions: &fakeVersions{version: v}})
	assert.Equal(t, LeaseFor(scriptrun.RunTimeout), d.worker.cfg.lease)
	assert.True(t, d.worker.admit.Adaptive())
}
