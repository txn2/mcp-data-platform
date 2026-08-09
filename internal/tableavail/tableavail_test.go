package tableavail

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
)

const (
	ordersURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.orders,PROD)"
	salesURN  = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"
)

// fakeProvider answers availability from a fixed table and counts lookups so a
// test can assert what the cache spared the warehouse.
type fakeProvider struct {
	query.NoopProvider
	byURN map[string]*query.TableAvailability
	err   error
	delay time.Duration
	calls atomic.Int32
}

func (f *fakeProvider) GetTableAvailability(ctx context.Context, urn string) (*query.TableAvailability, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, fmt.Errorf("availability lookup: %w", ctx.Err())
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	avail, ok := f.byURN[urn]
	if !ok {
		return &query.TableAvailability{Available: false, Error: "not found"}, nil
	}
	return avail, nil
}

func available(table string) *query.TableAvailability {
	return &query.TableAvailability{Available: true, QueryTable: table, Connection: "primary"}
}

func TestNewNilProvider(t *testing.T) {
	assert.Nil(t, New(nil))
}

func TestNilCacheResolvesNothing(t *testing.T) {
	var c *Cache
	assert.Empty(t, c.Resolve(context.Background(), []string{ordersURN}))
	assert.Empty(t, c.Verifiables(context.Background(), []string{ordersURN}))
}

func TestResolveEmptyInput(t *testing.T) {
	assert.Empty(t, New(&fakeProvider{}).Resolve(context.Background(), nil))
}

func TestResolveReturnsAvailableTables(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders"),
	}}

	got := New(p).Resolve(context.Background(), []string{ordersURN, salesURN})

	require.Len(t, got, 1, "an entity the provider does not report available is absent, not present-and-false")
	assert.Equal(t, "iceberg.retail.orders", got[ordersURN].QueryTable)
}

func TestResolveDegradesOnProviderFailure(t *testing.T) {
	p := &fakeProvider{err: errors.New("trino unreachable")}
	assert.Empty(t, New(p).Resolve(context.Background(), []string{ordersURN}))
}

func TestResolveExpiredBudgetResolvesNothing(t *testing.T) {
	p := &fakeProvider{
		byURN: map[string]*query.TableAvailability{ordersURN: available("iceberg.retail.orders")},
		delay: time.Second,
	}
	c := NewWithOptions(p, Options{Timeout: 5 * time.Millisecond})

	assert.Empty(t, c.Resolve(context.Background(), []string{ordersURN}),
		"a slow warehouse degrades to no answer, not a stalled request")
}

func TestResolveRemembersAnswers(t *testing.T) {
	tests := []struct {
		name      string
		provider  *fakeProvider
		wantCalls int32
	}{
		{
			name: "available entity",
			provider: &fakeProvider{byURN: map[string]*query.TableAvailability{
				ordersURN: available("iceberg.retail.orders"),
			}},
			wantCalls: 1,
		},
		{
			name:      "entity the provider reports unavailable",
			provider:  &fakeProvider{byURN: map[string]*query.TableAvailability{}},
			wantCalls: 1,
		},
		{
			// A lookup that failed is not an answer: caching it would keep every
			// caller blind for the whole TTL after a blip.
			name:      "failed lookup",
			provider:  &fakeProvider{err: errors.New("trino unreachable")},
			wantCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(tt.provider)

			first := c.Resolve(context.Background(), []string{ordersURN})
			second := c.Resolve(context.Background(), []string{ordersURN})

			assert.Equal(t, tt.wantCalls, tt.provider.calls.Load())
			assert.Equal(t, first, second, "a remembered answer reads the same as a fresh one")
		})
	}
}

func TestResolveReAsksAfterTheTTL(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders"),
	}}
	const ttl = time.Minute
	clock := time.Now()
	c := NewWithOptions(p, Options{TTL: ttl, Now: func() time.Time { return clock }})

	c.Resolve(context.Background(), []string{ordersURN})
	clock = clock.Add(ttl + time.Second)
	got := c.Resolve(context.Background(), []string{ordersURN})

	assert.Equal(t, int32(2), p.calls.Load(), "a stale answer is asked again")
	assert.Len(t, got, 1)
}

// One read must not turn a page of records into hundreds of describe and count
// queries at once. The answered entities are remembered, so the next read starts
// where this one stopped.
func TestResolveBoundsLookupsPerRequest(t *testing.T) {
	byURN := make(map[string]*query.TableAvailability, maxLookupsPerRequest*2)
	urns := make([]string, 0, maxLookupsPerRequest*2)
	for i := range maxLookupsPerRequest * 2 {
		urn := fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.t%d,PROD)", i)
		byURN[urn] = available(fmt.Sprintf("iceberg.retail.t%d", i))
		urns = append(urns, urn)
	}
	p := &fakeProvider{byURN: byURN}
	c := New(p)

	first := c.Resolve(context.Background(), urns)
	assert.Equal(t, int32(maxLookupsPerRequest), p.calls.Load())
	assert.Len(t, first, maxLookupsPerRequest)

	second := c.Resolve(context.Background(), urns)
	assert.Equal(t, int32(maxLookupsPerRequest*2), p.calls.Load(), "the next read continues from the remembered answers")
	assert.Len(t, second, maxLookupsPerRequest*2, "the page fills in over a refresh")
}

func TestRememberStaysWithinItsBound(t *testing.T) {
	c := New(&fakeProvider{})
	answers := make(map[string]*query.TableAvailability, cacheMax)
	for i := range cacheMax {
		answers[fmt.Sprintf("urn-%d", i)] = nil
	}

	c.remember(answers)
	require.Len(t, c.cache, cacheMax)
	c.remember(map[string]*query.TableAvailability{"urn-overflow": nil})

	assert.Len(t, c.cache, 1, "over the bound the cache is emptied, not grown")
}

func TestVerifiables(t *testing.T) {
	// A resolved entity naming no table would name no query to run, so it is not
	// a verifiable identity even though the provider called it available.
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders"),
		salesURN:  {Available: true, Connection: "primary"},
	}}

	got := New(p).Verifiables(context.Background(), []string{ordersURN, salesURN})

	require.Len(t, got, 1)
	assert.Equal(t, query.Verifiable{
		URN:        ordersURN,
		QueryTable: "iceberg.retail.orders",
		Connection: "primary",
	}, got[ordersURN])
}

func TestVerifiablesNoneResolve(t *testing.T) {
	assert.Empty(t, New(&fakeProvider{}).Verifiables(context.Background(), []string{ordersURN}))
}

// locatingProvider offers the cheap location path and counts which of the two
// answer paths a caller took.
type locatingProvider struct {
	fakeProvider
	locations atomic.Int32
}

func (p *locatingProvider) ResolveLocation(ctx context.Context, urn string) (*query.TableAvailability, error) {
	p.locations.Add(1)
	return p.GetTableAvailability(ctx, urn)
}

// A caller that reads only the table and connection must not pay for the
// COUNT(*) that fills EstimatedRows: with SkipRowEstimate the cheap path is
// used, and without it the ordinary one is, so a caller that DOES read the
// estimate is never quietly served an answer without it.
func TestResolveUsesTheCheapPathOnlyWhenAsked(t *testing.T) {
	tests := []struct {
		name          string
		skip          bool
		wantLocations int32
	}{
		{name: "skip row estimate", skip: true, wantLocations: 1},
		{name: "row estimate wanted", skip: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &locatingProvider{fakeProvider: fakeProvider{
				byURN: map[string]*query.TableAvailability{ordersURN: available("iceberg.retail.orders")},
			}}
			c := NewWithOptions(p, Options{SkipRowEstimate: tt.skip})

			got := c.Resolve(context.Background(), []string{ordersURN})

			require.Len(t, got, 1)
			assert.Equal(t, tt.wantLocations, p.locations.Load())
			assert.Equal(t, int32(1), p.calls.Load(), "either path is exactly one provider call")
		})
	}
}

// A provider with no cheap path is asked the ordinary way even when the caller
// asked to skip estimation, since skipping is an optimization, not a contract
// the caller can require of every provider.
func TestResolveSkipRowEstimateFallsBack(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders"),
	}}

	got := NewWithOptions(p, Options{SkipRowEstimate: true}).Resolve(context.Background(), []string{ordersURN})

	require.Len(t, got, 1)
	assert.Equal(t, int32(1), p.calls.Load())
}

// A query adapter reports a describe that failed for ANY reason as an ordinary
// "not available" answer with no error, so a negative must not be trusted for as
// long as a positive: after a blip the marker must come back in seconds, not
// minutes.
func TestNegativeAnswersExpireSooner(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{}}
	clock := time.Now()
	c := NewWithOptions(p, Options{Now: func() time.Time { return clock }})

	c.Resolve(context.Background(), []string{ordersURN})
	assert.Equal(t, int32(1), p.calls.Load())

	// Still remembered inside the negative window.
	clock = clock.Add(negativeTTL / 2)
	c.Resolve(context.Background(), []string{ordersURN})
	assert.Equal(t, int32(1), p.calls.Load())

	// Re-asked well before a positive answer would have expired.
	clock = clock.Add(negativeTTL)
	c.Resolve(context.Background(), []string{ordersURN})
	assert.Equal(t, int32(2), p.calls.Load(), "a negative is re-asked long before the positive TTL")
	assert.Less(t, negativeTTL, defaultTTL)
}

// An answer computed after the pass budget expired is not an answer: the adapter
// turns a canceled describe into "not available", so remembering it would
// record a warehouse that was merely slow as one that does not hold the table.
func TestExpiredBudgetIsNotRememberedAsAbsent(t *testing.T) {
	p := &fakeProvider{
		byURN: map[string]*query.TableAvailability{ordersURN: available("iceberg.retail.orders")},
		delay: 20 * time.Millisecond,
	}
	c := NewWithOptions(p, Options{Timeout: time.Millisecond})

	assert.Empty(t, c.Resolve(context.Background(), []string{ordersURN}))

	c.mu.Lock()
	remembered := len(c.cache)
	c.mu.Unlock()
	assert.Zero(t, remembered, "a lookup that ran out of budget is left for the next request")
}

// An expiring entry must not turn every concurrent session into its own
// warehouse query for the same table.
func TestConcurrentResolversShareOneLookup(t *testing.T) {
	p := &fakeProvider{
		byURN: map[string]*query.TableAvailability{ordersURN: available("iceberg.retail.orders")},
		delay: 20 * time.Millisecond,
	}
	c := New(p)

	const callers = 16
	var wg sync.WaitGroup
	results := make([]map[string]*query.TableAvailability, callers)
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			results[i] = c.Resolve(context.Background(), []string{ordersURN})
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), p.calls.Load(), "one cold entity costs one provider call however many ask")
	for i, got := range results {
		assert.Len(t, got, 1, "caller %d got no answer", i)
	}
}

func TestDistinct(t *testing.T) {
	got := Distinct(
		[]string{ordersURN, "", ordersURN},
		nil,
		[]string{salesURN, ordersURN},
	)
	assert.Equal(t, []string{ordersURN, salesURN}, got, "first-seen order, blanks and repeats dropped")
}

func TestDistinctEmpty(t *testing.T) {
	assert.Empty(t, Distinct())
}
