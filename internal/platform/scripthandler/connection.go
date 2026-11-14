package scripthandler

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
)

// ConnectionStore lists and orders tool-call handlers by connection
// (callhandler.Store).
type ConnectionStore interface {
	All(ctx context.Context) ([]callhandler.Handler, error)
	Stats(ctx context.Context, connection string) (map[string]callhandler.Stat, error)
	SetOrder(ctx context.Context, ids []string, by string) error
}

// ConnectionHandlers is every tool-call handler that applies to a
// connection, as the connection's page lists it: policies, transforms in the
// order they run, then watches, each with its script, the version it runs and
// the one waiting, and its last day's counts. An administrator's only.
func (s *Service) ConnectionHandlers(ctx context.Context, connection string, c Caller) ([]map[string]any, error) {
	if !c.Admin {
		return nil, fmt.Errorf("a connection's handlers are an administrator's: %w", ErrNotAllowed)
	}
	store, ok := s.Calls.(ConnectionStore)
	if s.Calls == nil || !ok {
		return []map[string]any{}, nil
	}
	all, err := store.All(ctx)
	if err != nil {
		return nil, storeErr(err)
	}
	stats, err := store.Stats(ctx, connection)
	if err != nil {
		stats = map[string]callhandler.Stat{}
	}
	on := onConnection(all, connection)
	out := make([]map[string]any, 0, len(on))
	for _, h := range on {
		sc, err := s.Scripts.GetByID(ctx, h.ScriptID)
		if err != nil || sc == nil {
			continue
		}
		row := s.callFields(ctx, sc, h, Caller{})
		row["stats"] = stats[h.ID]
		out = append(out, row)
	}
	return out, nil
}

// onConnection is the handlers on connection: policies, transforms in the
// order they run, then watches.
func onConnection(all []callhandler.Handler, connection string) []callhandler.Handler {
	var on []callhandler.Handler
	for _, h := range all {
		if callhandler.ConnectionMatches(h.Connection, connection) {
			on = append(on, h)
		}
	}
	slices.SortStableFunc(on, func(a, b callhandler.Handler) int {
		return cmp.Or(cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)), cmp.Compare(a.Order, b.Order), a.CreatedAt.Compare(b.CreatedAt))
	})
	return on
}

func kindRank(kind string) int {
	switch kind {
	case KindPolicy:
		return 0
	case KindTransform:
		return 1
	}
	return 2
}

// OrderTransforms sets the order a connection's transforms run in, ids first
// to last. An administrator's only.
func (s *Service) OrderTransforms(ctx context.Context, ids []string, c Caller) error {
	if !c.Admin {
		return fmt.Errorf("an administrator orders a connection's transforms: %w", ErrNotAllowed)
	}
	store, ok := s.Calls.(ConnectionStore)
	if s.Calls == nil || !ok {
		return fmt.Errorf("this deployment keeps no tool-call handlers: %w", ErrInvalid)
	}
	if err := store.SetOrder(ctx, ids, c.Email); err != nil {
		return storeErr(err)
	}
	return nil
}
