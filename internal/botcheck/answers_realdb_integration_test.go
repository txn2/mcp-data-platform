//go:build integration

package botcheck

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// Two replicas handed the same answer at once: the primary key lets exactly
// one of them accept it, which is the replay guarantee #2173 rests on.
func TestRealDB_AnAnswerIsAcceptedOnceAcrossReplicas(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	replicas := []*Checker{New(db, key, nil), New(db, key, nil)}
	low := *lowDifficulty()
	require.NoError(t, replicas[0].Settings.Set(ctx, low, "admin@example.com"))
	ch, err := replicas[0].Issue(ctx, "orders")
	require.NoError(t, err)
	answer := solve(ch.Token, ch.Difficulty)

	var accepted, replayed atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := replicas[i%2].Verify(ctx, "orders", answer); err {
			case nil:
				accepted.Add(1)
			case ErrReplayed:
				replayed.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), accepted.Load())
	assert.Equal(t, int32(7), replayed.Load())

	n, err := replicas[1].Answers.Recent(ctx, "orders", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the accepted answer is the endpoint's recent rate on every replica")
	swept, err := replicas[0].Answers.Sweep(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), swept)
}
