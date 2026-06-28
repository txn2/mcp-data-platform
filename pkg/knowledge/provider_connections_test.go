package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// fakeConnLister returns a fixed set of connections.
type fakeConnLister struct {
	conns  []ConnectionInfo
	called bool
}

func (f *fakeConnLister) Connections() []ConnectionInfo {
	f.called = true
	return f.conns
}

func TestConnectionsProvider_Metadata(t *testing.T) {
	p := NewConnectionsProvider(&fakeConnLister{})
	if p.Name() != SourceConnections {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopeShared {
		t.Errorf("Scope = %v, want shared", p.Scope())
	}
}

func TestConnectionsProvider_NoIntentSkips(t *testing.T) {
	l := &fakeConnLister{conns: []ConnectionInfo{{Name: "warehouse", Kind: "trino"}}}
	p := NewConnectionsProvider(l)
	// Entity-only query: connections respond to the text path only.
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{"urn:x"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected no hits without an intent, got %+v", hits)
	}
}

func TestConnectionsProvider_RanksByTokenOverlap(t *testing.T) {
	l := &fakeConnLister{conns: []ConnectionInfo{
		{Name: "stripe", Kind: "api", Description: "payments and billing"},
		{Name: "warehouse", Kind: "trino", Description: "analytics tables"},
		{Name: "billing-db", Kind: "trino", Description: "invoices"},
	}}
	p := NewConnectionsProvider(l)
	hits, err := p.Search(context.Background(), Query{Intent: "billing payments", Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// stripe matches both query tokens ("billing", "payments"); billing-db
	// matches one; warehouse matches none and is dropped.
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2 (warehouse should be dropped), hits=%+v", len(hits), hits)
	}
	if hits[0].Ref != "stripe" {
		t.Errorf("expected stripe to rank first, got %q", hits[0].Ref)
	}
	if hits[0].Reference != "mcp:connection:(api,stripe)" {
		t.Errorf("canonical reference = %q, want mcp:connection:(api,stripe)", hits[0].Reference)
	}
	if hits[0].Source != SourceConnections {
		t.Errorf("source = %q", hits[0].Source)
	}
	if hits[0].Text != "stripe (api)\npayments and billing" {
		t.Errorf("unexpected hit text: %q", hits[0].Text)
	}
}

func TestConnectionsProvider_NoMatchYieldsNothing(t *testing.T) {
	l := &fakeConnLister{conns: []ConnectionInfo{{Name: "warehouse", Kind: "trino"}}}
	p := NewConnectionsProvider(l)
	hits, err := p.Search(context.Background(), Query{Intent: "completely unrelated zzz"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected nil hits for no match, got %+v", hits)
	}
}

func TestConnectionsProvider_LimitCaps(t *testing.T) {
	l := &fakeConnLister{conns: []ConnectionInfo{
		{Name: "data-a", Kind: "trino"},
		{Name: "data-b", Kind: "trino"},
		{Name: "data-c", Kind: "trino"},
	}}
	p := NewConnectionsProvider(l)
	hits, err := p.Search(context.Background(), Query{Intent: "data", Limit: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("len = %d, want 2 (limit)", len(hits))
	}
}

func TestConnectionHitText_NoKindNoDescription(t *testing.T) {
	if got := connectionHitText(ConnectionInfo{Name: "bare"}); got != "bare" {
		t.Errorf("got %q, want %q", got, "bare")
	}
}

func TestConnectionsProvider_Fetch(t *testing.T) {
	t.Run("returns the matching connection descriptor", func(t *testing.T) {
		l := &fakeConnLister{conns: []ConnectionInfo{
			{Name: "warehouse", Kind: "trino", Description: "primary lakehouse"},
			{Name: "events", Kind: "s3"},
		}}
		ref := knowledgepage.ConnectionRef("trino", "warehouse")
		doc, owned, err := NewConnectionsProvider(l).Fetch(context.Background(), ref, Caller{})
		if !owned || err != nil {
			t.Fatalf("owned=%v err=%v", owned, err)
		}
		if doc.Source != SourceConnections || doc.Reference != ref {
			t.Errorf("doc = %+v", doc)
		}
		c, ok := doc.Content.(ConnectionInfo)
		if !ok || c.Name != "warehouse" || c.Kind != "trino" {
			t.Errorf("Content = %+v, want the warehouse connection", doc.Content)
		}
	})

	t.Run("declines a non-connection reference", func(t *testing.T) {
		l := &fakeConnLister{}
		_, owned, err := NewConnectionsProvider(l).Fetch(context.Background(), "mcp:asset:a1", Caller{})
		if owned || err != nil {
			t.Errorf("owned=%v err=%v, want declined", owned, err)
		}
		if l.called {
			t.Errorf("Connections must not be enumerated for a non-connection reference")
		}
	})

	t.Run("unknown connection is not-found", func(t *testing.T) {
		l := &fakeConnLister{conns: []ConnectionInfo{{Name: "warehouse", Kind: "trino"}}}
		ref := knowledgepage.ConnectionRef("s3", "events")
		_, owned, err := NewConnectionsProvider(l).Fetch(context.Background(), ref, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})
}
