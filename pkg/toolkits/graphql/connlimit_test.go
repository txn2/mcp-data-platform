package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMaxConcurrentCallsHoldsTheUpstreamToOneCallAtATime is #2209 through
// the toolkit: concurrent graphql_query calls on a connection limited to one
// reach the endpoint one at a time, and each succeeds by waiting instead of
// failing; list_connections reports the limit.
func TestMaxConcurrentCallsHoldsTheUpstreamToOneCallAtATime(t *testing.T) {
	var inFlight, most atomic.Int32
	u := newUpstream(t)
	u.respond = func(graphQLRequest, int) (int, string) {
		raise(&most, inFlight.Add(1))
		time.Sleep(5 * time.Millisecond) // the upstream working on the call
		inFlight.Add(-1)
		return http.StatusOK, `{"data":{"dataset":null}}`
	}
	tk := newToolkit(t, u, "flat", map[string]any{"max_concurrent_calls": float64(1), "endpoint_url": u.server.URL})

	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			res, _, err := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "gql", Query: datasetDocument, Variables: json.RawMessage(`{"urn":"x"}`)})
			if err != nil || res.IsError {
				t.Errorf("a call over the limit waits rather than failing: %v %+v", err, res)
			}
		})
	}
	wg.Wait()
	if got := most.Load(); got != 1 {
		t.Errorf("the endpoint saw %d calls at once; the limit is 1", got)
	}
	if got := tk.ListConnections()[0].MaxConcurrentCalls; got != 1 {
		t.Errorf("list_connections reports max_concurrent_calls %d", got)
	}
}

// raise sets most to n when n is higher.
func raise(most *atomic.Int32, n int32) {
	for {
		m := most.Load()
		if n <= m || most.CompareAndSwap(m, n) {
			return
		}
	}
}
