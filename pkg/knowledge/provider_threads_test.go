package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

type fakeThreadSearcher struct {
	gotEmail, gotIntent string
	gotLimit            int
	threads             []threads.Thread
	err                 error
}

func (f *fakeThreadSearcher) SearchThreads(_ context.Context, email, intent string, limit int) ([]threads.Thread, error) {
	f.gotEmail, f.gotIntent, f.gotLimit = email, intent, limit
	return f.threads, f.err
}

func TestThreadsProvider_Metadata(t *testing.T) {
	p := NewThreadsProvider(&fakeThreadSearcher{})
	if p.Name() != SourceFeedback {
		t.Errorf("Name = %q, want %q", p.Name(), SourceFeedback)
	}
	if p.Scope() != ScopePerUser {
		t.Errorf("Scope = %v, want per-user", p.Scope())
	}
}

func TestThreadsProvider_FailsClosedWithoutEmail(t *testing.T) {
	s := &fakeThreadSearcher{threads: []threads.Thread{{ID: "thr_1"}}}
	hits, err := NewThreadsProvider(s).Search(context.Background(), Query{Intent: "x", Caller: Caller{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits for anonymous caller, got %d", len(hits))
	}
	if s.gotEmail != "" {
		t.Errorf("store must not be queried for an anonymous caller: %+v", s)
	}
}

func TestThreadsProvider_NoIntentYieldsNothing(t *testing.T) {
	s := &fakeThreadSearcher{}
	hits, err := NewThreadsProvider(s).Search(context.Background(), Query{Caller: Caller{Email: "a@example.com"}})
	if err != nil || len(hits) != 0 {
		t.Errorf("expected no hits/err for empty intent, got %d hits, err %v", len(hits), err)
	}
	if s.gotIntent != "" {
		t.Error("store should not be queried with empty intent")
	}
}

func TestThreadsProvider_ScopesAndMaps(t *testing.T) {
	s := &fakeThreadSearcher{threads: []threads.Thread{
		{ID: "thr_1", Title: "amount is gross", Kind: "correction", TargetType: "asset", Status: "open", AuthorEmail: "a@example.com"},
		{ID: "thr_2", Kind: "question", TargetType: "prompt", Status: "resolved", AuthorEmail: "a@example.com"},
	}}
	hits, err := NewThreadsProvider(s).Search(context.Background(), Query{
		Intent: "amount",
		Caller: Caller{Email: "a@example.com"},
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.gotEmail != "a@example.com" || s.gotIntent != "amount" || s.gotLimit != 5 {
		t.Errorf("query not forwarded scoped to caller: %+v", s)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	if hits[0].Source != SourceFeedback || hits[0].Ref != "thr_1" ||
		hits[0].CapturedBy != "a@example.com" || hits[0].Status != "open" {
		t.Errorf("unexpected hit[0]: %+v", hits[0])
	}
	if hits[0].Text != "amount is gross\nfeedback on asset" {
		t.Errorf("hit[0] text = %q", hits[0].Text)
	}
	// An untitled thread falls back to "<kind> feedback".
	if hits[1].Text != "question feedback\nfeedback on prompt" {
		t.Errorf("hit[1] text = %q", hits[1].Text)
	}
	if hits[0].Score <= hits[1].Score {
		t.Errorf("expected descending score, got %v then %v", hits[0].Score, hits[1].Score)
	}
}

func TestThreadsProvider_SearchError(t *testing.T) {
	s := &fakeThreadSearcher{err: errors.New("db down")}
	_, err := NewThreadsProvider(s).Search(context.Background(), Query{Intent: "x", Caller: Caller{Email: "a@example.com"}})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}
