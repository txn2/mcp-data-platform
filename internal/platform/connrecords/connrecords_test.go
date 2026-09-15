package connrecords

import (
	"context"
	"errors"
	"testing"
)

// record is a connection store's record in these tests.
type record struct{ config map[string]any }

// errNoRecord is the platform store's error for a record it does not
// hold, and errNotServed the toolkit sentinel it is translated into.
var (
	errNoRecord  = errors.New("no record")
	errNotServed = errors.New("graphql: connection not found")
)

// recordStore is a RecordStore over a map, recording the kinds read.
type recordStore struct {
	records    map[string]*record
	err        error
	persistent bool
	kinds      []string
}

// Get returns one record, noting the kind it was asked for.
func (s *recordStore) Get(_ context.Context, kind, name string) (*record, error) {
	s.kinds = append(s.kinds, kind)
	if s.err != nil {
		return nil, s.err
	}
	r, ok := s.records[name]
	if !ok {
		return nil, errNoRecord
	}
	return r, nil
}

// Persistent reports whether the store outlives the process.
func (s *recordStore) Persistent() bool { return s.persistent }

// config reads a record's configuration.
func config(r *record) map[string]any { return r.config }

func TestAStoreThatHoldsNothingAnotherReplicaWroteAdaptsToNothing(t *testing.T) {
	if Saved[*record](nil, "graphql", errNoRecord, errNotServed, config) != nil {
		t.Error("a nil store adapted to a connection reader")
	}
	if Saved[*record](&recordStore{}, "graphql", errNoRecord, errNotServed, config) != nil {
		t.Error("a store that does not outlive the process adapted to a connection reader")
	}
}

func TestSavedReadsOneKindAndReturnsItsConfiguration(t *testing.T) {
	store := &recordStore{persistent: true, records: map[string]*record{
		"erp": {config: map[string]any{"endpoint_url": "https://erp.example.com/graphql"}},
	}}
	saved := Saved[*record](store, "graphql", errNoRecord, errNotServed, config)

	got, err := saved.GetConnection(context.Background(), "erp")
	if err != nil || got["endpoint_url"] != "https://erp.example.com/graphql" {
		t.Errorf("GetConnection = %v, %v", got, err)
	}
	if len(store.kinds) != 1 || store.kinds[0] != "graphql" {
		t.Errorf("read kinds %v; want only the one kind asked for", store.kinds)
	}
}

func TestAnAbsentRecordIsTheToolkitsOwnNotFound(t *testing.T) {
	store := &recordStore{persistent: true, records: map[string]*record{}}
	saved := Saved[*record](store, "api", errNoRecord, errNotServed, config)

	_, err := saved.GetConnection(context.Background(), "absent")
	if !errors.Is(err, errNotServed) {
		t.Errorf("an absent record gave %v; want the toolkit's not-found sentinel", err)
	}
}

func TestAStoreFailureIsNotAMissingConnection(t *testing.T) {
	store := &recordStore{persistent: true, err: errors.New("db unavailable")}
	saved := Saved[*record](store, "api", errNoRecord, errNotServed, config)

	_, err := saved.GetConnection(context.Background(), "erp")
	if err == nil || errors.Is(err, errNotServed) {
		t.Errorf("a store failure gave %v; want a failure that is not a missing connection", err)
	}
}
