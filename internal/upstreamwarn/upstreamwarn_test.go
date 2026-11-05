package upstreamwarn

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

type seen struct {
	conn, op string
	status   int
	calls    int
}

func (s *seen) Observe(_ context.Context, conn, op string, status int, _ http.Header) {
	s.conn, s.op, s.status = conn, op, status
	s.calls++
}

// Not parallel: the observer is process-wide.
func TestObserve(t *testing.T) {
	s := &seen{}
	SetObserver(s)
	t.Cleanup(func() { SetObserver(nil) })

	Observe(context.Background(), "vendor", http.StatusOK, nil)
	assert.Zero(t, s.calls, "a request naming no operation is passed over")

	ctx := WithOperation(context.Background(), "listPets")
	assert.Equal(t, "listPets", Operation(ctx))
	Observe(ctx, "vendor", 0, nil)
	assert.Zero(t, s.calls, "a transport failure says nothing about the operation")

	Observe(ctx, "vendor", http.StatusGone, nil)
	assert.Equal(t, seen{conn: "vendor", op: "listPets", status: http.StatusGone, calls: 1}, *s)

	assert.Equal(t, context.Background(), WithOperation(context.Background(), ""))
	SetObserver(nil)
	Observe(ctx, "vendor", http.StatusOK, nil)
	assert.Equal(t, 1, s.calls, "with no recorder installed nothing is observed")
}
