package insightobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const (
	ordersURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.orders,PROD)"
	salesURN  = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"
)

// fakeProvider answers availability from a fixed table and counts lookups so a
// test can assert that a URN claimed by several insights is resolved once.
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

func rows(n int64) *int64 { return new(n) }

func available(table string, estimate *int64) *query.TableAvailability {
	return &query.TableAvailability{
		Available:     true,
		QueryTable:    table,
		Connection:    "primary",
		EstimatedRows: estimate,
	}
}

func pending(text string, urns ...string) knowledge.Insight {
	return knowledge.Insight{
		ID:          "ins-1",
		Status:      knowledge.StatusPending,
		InsightText: text,
		EntityURNs:  urns,
	}
}

func TestNewNilProvider(t *testing.T) {
	assert.Nil(t, New(nil))
}

func TestAnnotateNilObserverObservesNothing(t *testing.T) {
	var o *Observer
	got := o.Annotate(context.Background(), []knowledge.Insight{pending("1140 rows", ordersURN)})
	require.Len(t, got, 1)
	assert.Nil(t, got[0])
}

func TestAnnotateNoopProviderObservesNothing(t *testing.T) {
	o := New(query.NewNoopProvider())
	got := o.Annotate(context.Background(), []knowledge.Insight{pending("1140 rows", ordersURN)})
	require.Len(t, got, 1)
	assert.Nil(t, got[0], "the noop provider reports nothing available, so nothing is observed")
}

func TestAnnotateEmptyInput(t *testing.T) {
	o := New(&fakeProvider{})
	assert.Empty(t, o.Annotate(context.Background(), nil))
}

func TestAnnotateAvailableEntity(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders", rows(1200)),
	}}
	o := New(p)

	got := o.Annotate(context.Background(), []knowledge.Insight{
		pending("The orders table is the system of record.", ordersURN),
	})

	require.Len(t, got, 1)
	require.Len(t, got[0], 1)
	obs := got[0][0]
	assert.Equal(t, ordersURN, obs.URN)
	assert.Equal(t, "iceberg.retail.orders", obs.QueryTable)
	assert.Equal(t, "primary", obs.Connection)
	require.NotNil(t, obs.EstimatedRows)
	assert.Equal(t, int64(1200), *obs.EstimatedRows)
	assert.Nil(t, obs.Conflict, "a claim stating no number conflicts with nothing")
}

func TestAnnotateUnavailableAndUnresolvable(t *testing.T) {
	tests := []struct {
		name string
		p    *fakeProvider
	}{
		{
			name: "unknown urn",
			p:    &fakeProvider{byURN: map[string]*query.TableAvailability{salesURN: available("s", nil)}},
		},
		{
			name: "provider error",
			p:    &fakeProvider{err: errors.New("trino unreachable")},
		},
		{
			name: "provider returns nothing",
			p:    &fakeProvider{byURN: map[string]*query.TableAvailability{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(tt.p).Annotate(context.Background(), []knowledge.Insight{
				pending("orders holds 1140 rows", ordersURN),
			})
			require.Len(t, got, 1)
			assert.Nil(t, got[0])
		})
	}
}

func TestAnnotateSkipsDecidedInsights(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders", rows(1200)),
	}}

	insights := []knowledge.Insight{
		{Status: knowledge.StatusApproved, InsightText: "1140 rows", EntityURNs: []string{ordersURN}},
		{Status: knowledge.StatusApplied, InsightText: "1140 rows", EntityURNs: []string{ordersURN}},
		pending("1140 rows", ordersURN),
	}
	got := New(p).Annotate(context.Background(), insights)

	require.Len(t, got, 3)
	assert.Nil(t, got[0], "an approved insight is history, not a decision to inform")
	assert.Nil(t, got[1])
	assert.Len(t, got[2], 1)
}

func TestAnnotateResolvesEachURNOnce(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders", rows(1200)),
	}}

	insights := []knowledge.Insight{
		pending("claim one", ordersURN, ""),
		pending("claim two", ordersURN),
		pending("claim three", ordersURN),
	}
	got := New(p).Annotate(context.Background(), insights)

	assert.Equal(t, int32(1), p.calls.Load(), "the shared URN is resolved once per request")
	for i := range insights {
		assert.Len(t, got[i], 1, "every insight still carries its own observation")
	}
}

func TestAnnotateObservesARepeatedEntityOnce(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders", rows(1200)),
	}}

	got := New(p).Annotate(context.Background(), []knowledge.Insight{
		pending("claim", ordersURN, ordersURN),
	})

	assert.Len(t, got[0], 1, "an entity named twice is still one table")
}

// One read must not turn a full page of insights into hundreds of describe and
// count queries at once. The answered entities are remembered, so the next read
// starts where this one stopped.
func TestAnnotateBoundsLookupsPerRequest(t *testing.T) {
	byURN := make(map[string]*query.TableAvailability, maxLookupsPerRequest*2)
	insights := make([]knowledge.Insight, 0, maxLookupsPerRequest*2)
	for i := range maxLookupsPerRequest * 2 {
		urn := fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.t%d,PROD)", i)
		byURN[urn] = available(fmt.Sprintf("iceberg.retail.t%d", i), nil)
		insights = append(insights, pending("claim", urn))
	}
	p := &fakeProvider{byURN: byURN}
	o := New(p)

	first := o.Annotate(context.Background(), insights)
	assert.Equal(t, int32(maxLookupsPerRequest), p.calls.Load())
	observed := func(got [][]Observation) int {
		n := 0
		for _, entry := range got {
			n += len(entry)
		}
		return n
	}
	assert.Equal(t, maxLookupsPerRequest, observed(first))

	second := o.Annotate(context.Background(), insights)
	assert.Equal(t, int32(maxLookupsPerRequest*2), p.calls.Load(), "the next read continues from the remembered answers")
	assert.Equal(t, maxLookupsPerRequest*2, observed(second), "the page fills in over a refresh")
}

func TestAnnotateOrdersObservationsAsTheInsightCarriesThem(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders", nil),
		salesURN:  available("iceberg.retail.daily_sales", nil),
	}}

	got := New(p).Annotate(context.Background(), []knowledge.Insight{
		pending("both tables", salesURN, "urn:li:dataset:(urn:li:dataPlatform:trino,gone,PROD)", ordersURN),
	})

	require.Len(t, got[0], 2)
	assert.Equal(t, salesURN, got[0][0].URN)
	assert.Equal(t, ordersURN, got[0][1].URN)
}

func TestAnnotateExpiredBudgetObservesNothing(t *testing.T) {
	p := &fakeProvider{
		byURN: map[string]*query.TableAvailability{ordersURN: available("iceberg.retail.orders", rows(1200))},
		delay: time.Second,
	}
	o := New(p)
	o.timeout = 5 * time.Millisecond

	got := o.Annotate(context.Background(), []knowledge.Insight{pending("1140 rows", ordersURN)})

	require.Len(t, got, 1)
	assert.Nil(t, got[0], "a slow warehouse degrades to no observation, not a stalled request")
}

// The review queue polls, so a second read of the same entity must come from
// memory: without it, an open browser tab re-runs a page of COUNT(*) queries
// against the warehouse every refresh.
func TestAnnotateRemembersAnswersForTheTTL(t *testing.T) {
	tests := []struct {
		name      string
		provider  *fakeProvider
		wantCalls int32
		remembers bool
	}{
		{
			name: "available entity",
			provider: &fakeProvider{byURN: map[string]*query.TableAvailability{
				ordersURN: available("iceberg.retail.orders", rows(1200)),
			}},
			wantCalls: 1,
			remembers: true,
		},
		{
			name:      "entity the provider reports unavailable",
			provider:  &fakeProvider{byURN: map[string]*query.TableAvailability{}},
			wantCalls: 1,
			remembers: true,
		},
		{
			// A lookup that failed is not an answer: caching it would keep the
			// review path blind for the whole TTL after a blip.
			name:      "failed lookup",
			provider:  &fakeProvider{err: errors.New("trino unreachable")},
			wantCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := New(tt.provider)
			insights := []knowledge.Insight{pending("claim", ordersURN)}

			first := o.Annotate(context.Background(), insights)
			second := o.Annotate(context.Background(), insights)

			assert.Equal(t, tt.wantCalls, tt.provider.calls.Load())
			assert.Equal(t, first[0], second[0], "a remembered answer reads the same as a fresh one")
			if tt.remembers {
				assert.Len(t, o.cache, 1)
			}
		})
	}
}

func TestAnnotateReAsksAfterTheTTL(t *testing.T) {
	p := &fakeProvider{byURN: map[string]*query.TableAvailability{
		ordersURN: available("iceberg.retail.orders", rows(1200)),
	}}
	clock := time.Now()
	o := New(p)
	o.now = func() time.Time { return clock }

	insights := []knowledge.Insight{pending("claim", ordersURN)}
	o.Annotate(context.Background(), insights)
	clock = clock.Add(observeTTL + time.Second)
	got := o.Annotate(context.Background(), insights)

	assert.Equal(t, int32(2), p.calls.Load(), "a stale answer is asked again")
	assert.Len(t, got[0], 1)
}

func TestRememberStaysWithinItsBound(t *testing.T) {
	o := New(&fakeProvider{})
	answers := make(map[string]*query.TableAvailability, observeCacheMax)
	for i := range observeCacheMax {
		answers[fmt.Sprintf("urn-%d", i)] = nil
	}

	o.remember(answers)
	require.Len(t, o.cache, observeCacheMax)
	o.remember(map[string]*query.TableAvailability{"urn-overflow": nil})

	assert.Len(t, o.cache, 1, "over the bound the cache is emptied, not grown")
}

func TestConflictMarker(t *testing.T) {
	tests := []struct {
		name      string
		claim     string
		estimate  *int64
		wantClaim int64
		wantFlag  bool
	}{
		{
			name:      "integer differs from the estimate",
			claim:     "The orders table holds 1140 rows.",
			estimate:  rows(1200),
			wantClaim: 1140,
			wantFlag:  true,
		},
		{
			name:     "integer matches the estimate",
			claim:    "The orders table holds 1200 rows.",
			estimate: rows(1200),
		},
		{
			name:     "claim states no integer",
			claim:    "The amount column is gross margin, not revenue.",
			estimate: rows(1200),
		},
		{
			name:     "provider gave no estimate",
			claim:    "The orders table holds 1140 rows.",
			estimate: nil,
		},
		{
			name:      "grouped digits are one number",
			claim:     "The orders table holds 1,140 rows.",
			estimate:  rows(1200),
			wantClaim: 1140,
			wantFlag:  true,
		},
		{
			name:      "the number nearest the estimate is the row claim",
			claim:     "In 2024 the orders table held 1140 rows across 12 regions.",
			estimate:  rows(1200),
			wantClaim: 1140,
			wantFlag:  true,
		},
		{
			name:     "any number matching the estimate settles the claim",
			claim:    "In 2024 the orders table held 1200 rows.",
			estimate: rows(1200),
		},
		{
			name:      "a decimal states no count",
			claim:     "discount_pct is stored as 0.15, not 15.",
			estimate:  rows(1200),
			wantClaim: 15,
			wantFlag:  true,
		},
		{
			name:     "digits inside an identifier are not a count",
			claim:    "The s3 export of the v2 pipeline is authoritative.",
			estimate: rows(1200),
		},
		{
			name:      "an empty table contradicts a stated count",
			claim:     "The orders table holds 1140 rows.",
			estimate:  rows(0),
			wantClaim: 1140,
			wantFlag:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := conflictFor(tt.claim, tt.estimate)
			if !tt.wantFlag {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantClaim, got.ClaimedRows)
			assert.Equal(t, *tt.estimate, got.ObservedRows)
			assert.Contains(t, got.Message, "the table currently estimates")
		})
	}
}

func TestConflictMessageNamesBothNumbers(t *testing.T) {
	got := conflictFor("The orders table holds 1140 rows.", rows(1200))
	require.NotNil(t, got)
	assert.Equal(t, "claim states 1140; the table currently estimates 1200", got.Message)
}

func TestClaimIntegersIsBounded(t *testing.T) {
	claim := strings.Repeat("7 ", maxClaimIntegers*2)
	assert.Len(t, claimIntegers(claim), maxClaimIntegers)
}

func TestClaimIntegersSkipsOverflow(t *testing.T) {
	assert.Empty(t, claimIntegers("99999999999999999999999 rows"))
}
