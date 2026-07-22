package catalogindex

import (
	"context"
	"errors"
	"testing"
)

// stubQueue implements Store for the best-effort hook tests, recording
// the calls and returning a configurable error.
type stubQueue struct {
	err              error
	enqueued         []SpecKey
	canceled         []SpecKey
	canceledCatalogs []string
}

func (s *stubQueue) Enqueue(_ context.Context, key SpecKey, _ Kind) (bool, error) {
	s.enqueued = append(s.enqueued, key)
	return s.err == nil, s.err
}

func (s *stubQueue) Cancel(_ context.Context, key SpecKey) error {
	s.canceled = append(s.canceled, key)
	return s.err
}

func (s *stubQueue) CancelCatalog(_ context.Context, catalogID string) error {
	s.canceledCatalogs = append(s.canceledCatalogs, catalogID)
	return s.err
}

func (*stubQueue) List(context.Context, ListFilter) ([]Job, error)               { return nil, nil }
func (*stubQueue) Get(context.Context, int64) (*Job, error)                      { return nil, ErrNotFound }
func (*stubQueue) SpecStatuses(context.Context, string) ([]SpecStatusRow, error) { return nil, nil }
func (*stubQueue) Health(context.Context, string) (*CatalogHealth, error) {
	return &CatalogHealth{}, nil
}

func TestEnqueueBestEffort(t *testing.T) {
	t.Parallel()
	// nil store (file mode / no DB) is a silent no-op.
	EnqueueBestEffort(context.Background(), nil, "c", "s")

	q := &stubQueue{}
	EnqueueBestEffort(context.Background(), q, "c", "s")
	if len(q.enqueued) != 1 || q.enqueued[0] != (SpecKey{CatalogID: "c", SpecName: "s"}) {
		t.Errorf("enqueued = %+v; want [{c s}]", q.enqueued)
	}

	// An enqueue error is logged, not surfaced: the hook has no
	// return value, so the write path proceeds regardless.
	EnqueueBestEffort(context.Background(), &stubQueue{err: errors.New("queue down")}, "c", "s")
}

func TestCancelBestEffort(t *testing.T) {
	t.Parallel()
	CancelBestEffort(context.Background(), nil, "c", "s")

	q := &stubQueue{}
	CancelBestEffort(context.Background(), q, "c", "s")
	if len(q.canceled) != 1 || q.canceled[0] != (SpecKey{CatalogID: "c", SpecName: "s"}) {
		t.Errorf("canceled = %+v; want [{c s}]", q.canceled)
	}

	CancelBestEffort(context.Background(), &stubQueue{err: errors.New("queue down")}, "c", "s")
}

func TestCancelCatalogBestEffort(t *testing.T) {
	t.Parallel()
	CancelCatalogBestEffort(context.Background(), nil, "c")

	q := &stubQueue{}
	CancelCatalogBestEffort(context.Background(), q, "c")
	if len(q.canceledCatalogs) != 1 || q.canceledCatalogs[0] != "c" {
		t.Errorf("canceledCatalogs = %+v; want [c]", q.canceledCatalogs)
	}

	CancelCatalogBestEffort(context.Background(), &stubQueue{err: errors.New("queue down")}, "c")
}
