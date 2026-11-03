package postgres

import (
	"context"
	"slices"
	"sync"
	"time"
)

// memStore is an in-memory MigrationStore with the PostgreSQL store's
// contract: Put replaces by version and reports it, List is version order,
// Events newest first.
type memStore struct {
	mu         sync.Mutex
	migrations []Migration
	events     []MigrationEvent
	err        error
}

func (s *memStore) List(_ context.Context, connection, schema string) ([]Migration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := []Migration{}
	for _, m := range s.migrations {
		if m.Connection == connection && m.Schema == schema {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b Migration) int { return a.Version - b.Version })
	return out, nil
}

func (s *memStore) Put(_ context.Context, m Migration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	for i, have := range s.migrations {
		if have.Connection == m.Connection && have.Schema == m.Schema && have.Version == m.Version {
			m.CreatedAt = have.CreatedAt
			s.migrations[i] = m
			return true, nil
		}
	}
	m.CreatedAt = time.Now()
	s.migrations = append(s.migrations, m)
	return false, nil
}

func (s *memStore) Record(_ context.Context, e MigrationEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.At = time.Now()
	s.events = append(s.events, e)
	return s.err
}

func (s *memStore) Events(_ context.Context, connection, schema string, limit int) ([]MigrationEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := []MigrationEvent{}
	for i := len(s.events) - 1; i >= 0 && len(out) < limit; i-- {
		if e := s.events[i]; e.Connection == connection && e.Schema == schema {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *memStore) eventsOf(kind string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.Event == kind {
			n++
		}
	}
	return n
}
