package datahubapi

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// newLabelerBridge wires one connection's fake backend into a bridge, the way
// the server does at startup.
func newLabelerBridge(t *testing.T, backends map[string]*fakeDataHub) Bridge {
	t.Helper()
	b := NewStaticBridge()
	for _, name := range sortedKeys(backends) {
		b.Add(name, backends[name], nil)
	}
	return b
}

// sortedKeys returns the map keys in registration-stable order, so a
// multi-connection test asserts "first connection wins" against a fixed order.
func sortedKeys(m map[string]*fakeDataHub) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// TestLabelerNamesEachGovernanceKind is the core contract: a term resolves
// through its by-URN read, and a tag and a domain through the vocabulary list,
// which is the only read either has. Without this the portal renders the
// generated key inside the URN as if it were the entity's name.
func TestLabelerNamesEachGovernanceKind(t *testing.T) {
	backend := newFakeDataHub()
	backend.terms = map[string]*semantic.GlossaryTerm{
		"urn:li:glossaryTerm:8f3c": {URN: "urn:li:glossaryTerm:8f3c", Name: "Net Revenue"},
	}
	backend.refs = []semantic.EntityRef{
		{URN: "urn:li:tag:a1b2", Name: "PII"},
		{URN: "urn:li:domain:c3d4", Name: "Finance"},
	}

	labeler := NewLabeler(newLabelerBridge(t, map[string]*fakeDataHub{"primary": backend}))
	got := labeler.Labels(context.Background(), []string{
		"urn:li:glossaryTerm:8f3c",
		"urn:li:tag:a1b2",
		"urn:li:domain:c3d4",
	})

	want := map[string]string{
		"urn:li:glossaryTerm:8f3c": "Net Revenue",
		"urn:li:tag:a1b2":          "PII",
		"urn:li:domain:c3d4":       "Finance",
	}
	for urn, name := range want {
		if got[urn] != name {
			t.Errorf("Labels()[%q] = %q, want %q", urn, got[urn], name)
		}
	}
}

// TestLabelerSkipsNonGovernanceURNs proves a dataset costs no upstream read: its
// name is already in its URN, so resolving it would spend a call to learn what
// the caller has.
func TestLabelerSkipsNonGovernanceURNs(t *testing.T) {
	backend := newFakeDataHub()
	backend.refs = []semantic.EntityRef{{URN: "urn:li:tag:a1b2", Name: "PII"}}
	labeler := NewLabeler(newLabelerBridge(t, map[string]*fakeDataHub{"primary": backend}))

	dataset := "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.public.orders,PROD)"
	got := labeler.Labels(context.Background(), []string{dataset, "mcp:asset:a1"})
	if len(got) != 0 {
		t.Fatalf("Labels() = %v, want no resolution for non-governance URNs", got)
	}
	if n := backend.readCount(); n != 0 {
		t.Errorf("backend reads = %d, want 0: a dataset URN already carries its name", n)
	}
}

// TestLabelerReadsOnlyTheKindsAsked proves resolving one tag costs one read: the
// term and domain reads are not issued for a batch that holds neither.
func TestLabelerReadsOnlyTheKindsAsked(t *testing.T) {
	backend := newFakeDataHub()
	backend.refs = []semantic.EntityRef{{URN: "urn:li:tag:pii", Name: "PII"}}
	labeler := NewLabeler(newLabelerBridge(t, map[string]*fakeDataHub{"primary": backend}))

	labeler.Labels(context.Background(), []string{"urn:li:tag:pii"})
	if n := backend.readCount(); n != 1 {
		t.Errorf("backend reads = %d, want exactly the tag listing", n)
	}
}

// TestLabelerOmitsWhatItCannotName covers the fallback contract: a URN the
// catalog does not hold is absent from the result rather than mapped to an empty
// or invented name, so the caller keeps its URN-derived label.
func TestLabelerOmitsWhatItCannotName(t *testing.T) {
	backend := newFakeDataHub()
	backend.terms = map[string]*semantic.GlossaryTerm{}
	backend.refs = []semantic.EntityRef{{URN: "urn:li:tag:known", Name: "Known"}}
	labeler := NewLabeler(newLabelerBridge(t, map[string]*fakeDataHub{"primary": backend}))

	got := labeler.Labels(context.Background(), []string{
		"urn:li:glossaryTerm:missing",
		"urn:li:tag:missing",
		"urn:li:domain:missing",
		"urn:li:tag:known",
	})
	if len(got) != 1 || got["urn:li:tag:known"] != "Known" {
		t.Fatalf("Labels() = %v, want only the known tag named", got)
	}
}

// TestLabelerSurvivesAFailedRead proves a failing upstream degrades to the
// URN-derived label instead of failing the page read that asked. A read error
// on one kind must not take the others down with it.
func TestLabelerSurvivesAFailedRead(t *testing.T) {
	backend := newFakeDataHub()
	backend.readErr = errors.New("datahub unreachable")
	labeler := NewLabeler(newLabelerBridge(t, map[string]*fakeDataHub{"primary": backend}))

	got := labeler.Labels(context.Background(), []string{"urn:li:tag:a1b2", "urn:li:glossaryTerm:8f3c"})
	if len(got) != 0 {
		t.Fatalf("Labels() = %v, want nothing resolved from a failing backend", got)
	}
}

// TestLabelerAsksEveryConnection proves a reference carrying no connection is
// resolved wherever it lives: the first connection that knows a URN names it,
// and a URN only the second holds is still named.
func TestLabelerAsksEveryConnection(t *testing.T) {
	first := newFakeDataHub()
	first.refs = []semantic.EntityRef{{URN: "urn:li:tag:shared", Name: "From first"}}
	second := newFakeDataHub()
	second.refs = []semantic.EntityRef{
		{URN: "urn:li:tag:shared", Name: "From second"},
		{URN: "urn:li:tag:only-second", Name: "Only second"},
	}

	labeler := NewLabeler(newLabelerBridge(t, map[string]*fakeDataHub{"a-first": first, "b-second": second}))
	got := labeler.Labels(context.Background(), []string{"urn:li:tag:shared", "urn:li:tag:only-second"})

	if got["urn:li:tag:shared"] != "From first" {
		t.Errorf("shared tag named %q, want the first connection's name", got["urn:li:tag:shared"])
	}
	if got["urn:li:tag:only-second"] != "Only second" {
		t.Errorf("second-only tag named %q, want it resolved from the second connection", got["urn:li:tag:only-second"])
	}
}

// TestLabelerStopsOnceEverythingIsNamed proves the second connection is not read
// when the first answered everything: asking it anyway would spend a vocabulary
// listing per connection on every page render.
func TestLabelerStopsOnceEverythingIsNamed(t *testing.T) {
	first := newFakeDataHub()
	first.refs = []semantic.EntityRef{{URN: "urn:li:tag:pii", Name: "PII"}}
	second := newFakeDataHub()

	labeler := NewLabeler(newLabelerBridge(t, map[string]*fakeDataHub{"a-first": first, "b-second": second}))
	got := labeler.Labels(context.Background(), []string{"urn:li:tag:pii"})
	if got["urn:li:tag:pii"] != "PII" {
		t.Fatalf("Labels() = %v, want the tag named by the first connection", got)
	}
	if n := second.readCount(); n != 0 {
		t.Errorf("second connection reads = %d, want 0 once the first named everything", n)
	}
}

// TestLabelerDeduplicatesURNs proves a page citing the same term twice reads it
// once.
func TestLabelerDeduplicatesURNs(t *testing.T) {
	backend := newFakeDataHub()
	backend.terms = map[string]*semantic.GlossaryTerm{
		"urn:li:glossaryTerm:t1": {URN: "urn:li:glossaryTerm:t1", Name: "Churn"},
	}
	labeler := NewLabeler(newLabelerBridge(t, map[string]*fakeDataHub{"primary": backend}))

	got := labeler.Labels(context.Background(), []string{
		"urn:li:glossaryTerm:t1", "urn:li:glossaryTerm:t1", "urn:li:glossaryTerm:t1",
	})
	if len(got) != 1 || got["urn:li:glossaryTerm:t1"] != "Churn" {
		t.Fatalf("Labels() = %v, want one resolved term", got)
	}
}

// TestLabelerReturnsNilForNoGovernanceURNs proves the no-op case never touches
// the bridge at all.
func TestLabelerReturnsNilForNoGovernanceURNs(t *testing.T) {
	labeler := NewLabeler(newLabelerBridge(t, map[string]*fakeDataHub{}))
	if got := labeler.Labels(context.Background(), nil); got != nil {
		t.Fatalf("Labels(nil) = %v, want nil", got)
	}
}
