package indexjobs

import (
	"context"
	"testing"
)

// stubSource / stubSink are minimal Source/Sink implementations for
// registry and worker tests.
type stubSource struct {
	kind  string
	items []Item
	err   error
	onOK  func(string)
}

func (s *stubSource) Kind() string { return s.kind }
func (s *stubSource) LoadItems(_ context.Context, _ string) ([]Item, error) {
	return s.items, s.err
}

func (s *stubSource) OnSucceeded(id string) {
	if s.onOK != nil {
		s.onOK(id)
	}
}

type stubSink struct {
	kind     string
	existing map[string]Vector
	listErr  error
	upserted []Vector
	upErr    error
	stamped  int
	gaps     []string
}

func (s *stubSink) Kind() string { return s.kind }
func (s *stubSink) ListExisting(_ context.Context, _ Key) (map[string]Vector, error) {
	return s.existing, s.listErr
}

func (s *stubSink) Upsert(_ context.Context, _ Key, rows []Vector) error {
	s.upserted = rows
	return s.upErr
}
func (*stubSink) UpsertBatch(_ context.Context, _ Key, _ []Vector) error { return nil }
func (s *stubSink) StampExpected(_ context.Context, _ Key, n int) error {
	s.stamped = n
	return nil
}
func (s *stubSink) FindGaps(_ context.Context) ([]string, error) { return s.gaps, nil }

func TestRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	src := &stubSource{kind: "k1"}
	snk := &stubSink{kind: "k1"}
	if err := r.Register(src, snk); err != nil {
		t.Fatalf("Register: %v", err)
	}
	gotSrc, gotSnk, ok := r.Lookup("k1")
	if !ok {
		t.Fatal("expected k1 registered")
	}
	if gotSrc != src || gotSnk != snk {
		t.Error("Lookup returned different instances")
	}
	if _, _, ok := r.Lookup("missing"); ok {
		t.Error("unregistered kind should not be found")
	}
}

func TestRegistry_RegisterRejectsBadInput(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	cases := []struct {
		name string
		src  Source
		snk  Sink
	}{
		{"nil source", nil, &stubSink{kind: "k"}},
		{"nil sink", &stubSource{kind: "k"}, nil},
		{"kind mismatch", &stubSource{kind: "a"}, &stubSink{kind: "b"}},
		{"empty kind", &stubSource{kind: ""}, &stubSink{kind: ""}},
	}
	for _, tc := range cases {
		if err := r.Register(tc.src, tc.snk); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

func TestRegistry_RegisterRejectsDuplicate(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(&stubSource{kind: "k"}, &stubSink{kind: "k"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(&stubSource{kind: "k"}, &stubSink{kind: "k"}); err == nil {
		t.Error("duplicate kind should be rejected")
	}
}

func TestRegistry_SinksAndKindsSorted(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	for _, k := range []string{"zeta", "alpha", "mid"} {
		if err := r.Register(&stubSource{kind: k}, &stubSink{kind: k}); err != nil {
			t.Fatalf("register %s: %v", k, err)
		}
	}
	kinds := r.Kinds()
	want := []string{"alpha", "mid", "zeta"}
	if len(kinds) != 3 || kinds[0] != want[0] || kinds[1] != want[1] || kinds[2] != want[2] {
		t.Errorf("Kinds() = %v; want %v (sorted)", kinds, want)
	}
	sinks := r.Sinks()
	if len(sinks) != 3 || sinks[0].Kind() != "alpha" || sinks[2].Kind() != "zeta" {
		t.Errorf("Sinks() not sorted by kind: %v", []string{sinks[0].Kind(), sinks[1].Kind(), sinks[2].Kind()})
	}
}
