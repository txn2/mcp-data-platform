// Package connrecords adapts the platform's connection store to the
// reader a toolkit answers a request for a connection it does not hold
// from (internal/conncatchup).
//
// Every kind with that catch-up needs the same three things done to the
// platform's store: ask it for one record of this kind, translate the
// store's absent-record error into the kind's own, and hand back the
// configuration map the toolkit parses. Two kinds have it (#1714, #1746)
// and the next one will, so it is written once here rather than forked
// per kind.
package connrecords

import (
	"context"
	"errors"
	"fmt"
)

// RecordStore is the platform's connection store, as far as a kind reads
// it: one saved connection of a kind, as the record type the store keeps,
// and whether the store outlives the process.
type RecordStore[R any] interface {
	Get(ctx context.Context, kind, name string) (R, error)
	Persistent() bool
}

// Reader is what a toolkit's connection store reads like. A kind declares
// its own interface of this shape, which this one satisfies.
type Reader interface {
	GetConnection(ctx context.Context, name string) (map[string]any, error)
}

// Saved adapts the platform's connection store to the connections of one
// kind. kind is the connection-instance kind discriminator, notFound the
// store's error for a connection it does not hold, kindNotFound the
// toolkit's own sentinel for one, and config reads a record's
// configuration.
//
// A nil store, or one that does not outlive the process and so holds
// nothing another replica wrote, adapts to nil: there is no catching up
// to do where every replica knows only what it was handed.
func Saved[R any](store RecordStore[R], kind string, notFound, kindNotFound error, config func(R) map[string]any) Reader {
	if store == nil || !store.Persistent() {
		return nil
	}
	return saved[R]{store: store, kind: kind, notFound: notFound, kindNotFound: kindNotFound, config: config}
}

// saved is Saved's adapter.
type saved[R any] struct {
	store        RecordStore[R]
	kind         string
	notFound     error
	kindNotFound error
	config       func(R) map[string]any
}

// GetConnection returns the stored configuration of one connection of
// this kind.
func (s saved[R]) GetConnection(ctx context.Context, name string) (map[string]any, error) {
	record, err := s.store.Get(ctx, s.kind, name)
	if errors.Is(err, s.notFound) {
		return nil, fmt.Errorf("the connection store holds no %s connection %s: %w", s.kind, name, s.kindNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s connection %s: %w", s.kind, name, err)
	}
	return s.config(record), nil
}
