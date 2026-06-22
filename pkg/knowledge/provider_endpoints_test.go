package knowledge

import (
	"context"
	"errors"
	"testing"
)

// fakeEndpointSearcher returns fixed candidates (or an error).
type fakeEndpointSearcher struct {
	cands []EndpointCandidate
	err   error
}

func (f *fakeEndpointSearcher) SearchEndpoints(_ context.Context, _ string, _ int) ([]EndpointCandidate, error) {
	return f.cands, f.err
}

func TestEndpointsProvider_Metadata(t *testing.T) {
	p := NewEndpointsProvider()
	if p.Name() != SourceEndpoints {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopeShared {
		t.Errorf("Scope = %v, want shared", p.Scope())
	}
}

func TestEndpointsProvider_NoIntentSkips(t *testing.T) {
	p := NewEndpointsProvider(&fakeEndpointSearcher{cands: []EndpointCandidate{{Connection: "c", OperationID: "op"}}})
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{"urn:x"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("endpoints provider should not run without an intent, got %+v", hits)
	}
}

func TestEndpointsProvider_AggregatesAndRanksAcrossGateways(t *testing.T) {
	a := &fakeEndpointSearcher{cands: []EndpointCandidate{
		{Connection: "shop", OperationID: "listOrders", Method: "GET", Path: "/orders", Summary: "List orders", Score: 0.4},
	}}
	b := &fakeEndpointSearcher{cands: []EndpointCandidate{
		{Connection: "crm", OperationID: "getRetention", Method: "GET", Path: "/retention", Summary: "Retention report", Score: 0.9},
	}}
	p := NewEndpointsProvider(a, b)
	hits, err := p.Search(context.Background(), Query{Intent: "retention", Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	// Higher-scored candidate from the second gateway ranks first.
	if hits[0].Ref != "crm:getRetention" {
		t.Errorf("expected crm:getRetention first, got %q", hits[0].Ref)
	}
	if hits[0].Source != SourceEndpoints {
		t.Errorf("source = %q", hits[0].Source)
	}
	if hits[0].Text != "GET /retention\nRetention report" {
		t.Errorf("unexpected hit text: %q", hits[0].Text)
	}
}

func TestEndpointsProvider_OneGatewayErrorDoesNotBlank(t *testing.T) {
	good := &fakeEndpointSearcher{cands: []EndpointCandidate{
		{Connection: "shop", OperationID: "op", Method: "GET", Path: "/x", Score: 0.5},
	}}
	bad := &fakeEndpointSearcher{err: errors.New("gateway down")}
	p := NewEndpointsProvider(bad, good)
	hits, err := p.Search(context.Background(), Query{Intent: "q"})
	if err != nil {
		t.Fatalf("a single gateway failure must not fail the search: %v", err)
	}
	if len(hits) != 1 || hits[0].Ref != "shop:op" {
		t.Errorf("expected the healthy gateway's hit, got %+v", hits)
	}
}

func TestEndpointsProvider_LimitCaps(t *testing.T) {
	cands := make([]EndpointCandidate, 5)
	for i := range cands {
		cands[i] = EndpointCandidate{Connection: "c", OperationID: string(rune('a' + i)), Score: float64(i)}
	}
	p := NewEndpointsProvider(&fakeEndpointSearcher{cands: cands})
	hits, err := p.Search(context.Background(), Query{Intent: "q", Limit: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 3 {
		t.Errorf("len = %d, want 3 (limit)", len(hits))
	}
}

func TestEndpointRef_FallsBackToMethodPath(t *testing.T) {
	got := endpointRef(EndpointCandidate{Connection: "c", Method: "GET", Path: "/p"})
	if got != "c:GET /p" {
		t.Errorf("got %q, want %q", got, "c:GET /p")
	}
}

func TestEndpointHitText_NoSummary(t *testing.T) {
	if got := endpointHitText(EndpointCandidate{Method: "POST", Path: "/x"}); got != "POST /x" {
		t.Errorf("got %q", got)
	}
}
