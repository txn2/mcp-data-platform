package indexqueue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// The platform's metrics recorder is the queue's observer.
var _ indexjobs.Observer = (*observability.Metrics)(nil)

// sampleTTL is how long one database read answers the scrapes that follow it.
// A scrape every 15-30s from each Prometheus, and one per replica, would
// otherwise count a large kind's vector table several times a minute to report
// a number that moves at the pace of an index pass.
const sampleTTL = 30 * time.Second

// depthReader is the store read the sampler needs: every kind's open work in
// one statement.
type depthReader interface {
	QueueDepth(ctx context.Context) ([]indexjobs.QueueDepth, error)
}

// coverageReader is the per-kind coverage read, as the admin summary makes it.
type coverageReader interface {
	Kinds() []string
	Coverage(ctx context.Context, kind string) (*indexjobs.Coverage, error)
}

// queueSampler answers the metrics scrape's database gauges from a cached
// read: every registered kind's open work and vector coverage.
type queueSampler struct {
	depth    depthReader
	coverage coverageReader
	ttl      time.Duration
	now      func() time.Time

	mu      sync.Mutex
	at      time.Time
	samples []observability.IndexQueueSample
}

func newQueueSampler(depth depthReader, coverage coverageReader) *queueSampler {
	return &queueSampler{depth: depth, coverage: coverage, ttl: sampleTTL, now: time.Now}
}

// Sample returns the cached read while it is fresh, and reads again when it is
// not. A failed read is returned as an error and leaves the previous read in
// place for the next attempt to replace.
func (s *queueSampler) Sample(ctx context.Context) ([]observability.IndexQueueSample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.samples != nil && s.now().Sub(s.at) < s.ttl {
		return s.samples, nil
	}
	samples, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	s.samples, s.at = samples, s.now()
	return samples, nil
}

// read builds one sample per registered kind. A kind with no open work has no
// QueueDepth row and reports zeros, which is the truth and keeps its series
// from going stale. Coverage is read per kind in parallel; a kind whose read
// fails reports its queue figures without coverage rather than failing every
// kind.
func (s *queueSampler) read(ctx context.Context) ([]observability.IndexQueueSample, error) {
	depths, err := s.depth.QueueDepth(ctx)
	if err != nil {
		return nil, fmt.Errorf("queue depth: %w", err)
	}
	byKind := make(map[string]indexjobs.QueueDepth, len(depths))
	for _, d := range depths {
		byKind[d.SourceKind] = d
	}
	kinds := s.coverage.Kinds()
	out := make([]observability.IndexQueueSample, len(kinds))
	g, gctx := errgroup.WithContext(ctx)
	for i, kind := range kinds {
		d := byKind[kind]
		out[i] = observability.IndexQueueSample{
			Kind:               kind,
			Pending:            int64(d.Pending),
			Running:            int64(d.Running),
			Retrying:           int64(d.Retrying),
			FailedUnits:        int64(d.FailedUnits),
			OldestRunnableWait: d.OldestRunnableWait,
		}
		g.Go(func() error {
			cov, err := s.coverage.Coverage(gctx, kind)
			if err != nil {
				slog.Warn("index jobs: coverage sample failed", "kind", kind, logKeyError, err)
				return nil
			}
			if cov != nil {
				out[i].CoverageKnown = true
				out[i].Indexed = int64(cov.Indexed)
				out[i].Expected = int64(cov.Expected)
				out[i].ExpectedKnown = cov.ExpectedKnown
			}
			return nil
		})
	}
	_ = g.Wait() // every goroutine returns nil; a failed kind is logged above
	return out, nil
}
