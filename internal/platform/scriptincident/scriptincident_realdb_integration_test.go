//go:build integration

package scriptincident

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestStore_RealDB is #2041 against PostgreSQL: of many replicas
// recording a failed run of one script at once, exactly one opens its
// incident and the rest are counted on it; of many closing it, exactly one is
// handed it; and a script's deletion takes its incident with it.
func TestPostgresStore_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	sc := &script.Script{Name: "nightly", Source: "def main():\n    print(1)\n", OwnerEmail: "jane@example.com", Enabled: true}
	scripts := scriptstore.New(db)
	require.NoError(t, scripts.Create(ctx, sc, script.Author{Email: "jane@example.com"}))
	s := NewPostgresStore(db)

	opened := concurrently(8, func(i int) bool {
		ok, err := s.Open(ctx, Opening{ScriptID: sc.ID, RunID: "run-" + string(rune('a'+i)), Cause: "upstream", Error: "timeout", FailedAt: time.Now()})
		require.NoError(t, err)
		return ok
	})
	assert.Equal(t, 1, opened, "one opener among concurrent failures")

	closed := concurrently(4, func(int) bool {
		in, err := s.Close(ctx, sc.ID)
		require.NoError(t, err)
		if in != nil {
			assert.Equal(t, 8, in.FailedRuns, "every failed run was counted")
			assert.Equal(t, "upstream", in.OpeningCause)
			assert.Equal(t, "timeout", in.OpeningError)
		}
		return in != nil
	})
	assert.Equal(t, 1, closed, "one closer is handed the incident")

	ok, err := s.Open(ctx, Opening{ScriptID: sc.ID, RunID: "run-z", FailedAt: time.Now()})
	require.NoError(t, err)
	assert.True(t, ok, "after a close the next failure opens a new incident")
	_, err = scripts.Delete(ctx, sc.ID)
	require.NoError(t, err)
	in, err := s.Close(ctx, sc.ID)
	require.NoError(t, err)
	assert.Nil(t, in, "a deleted script's incident goes with it")
}

// concurrently runs f n times at once and counts the true results.
func concurrently(n int, f func(i int) bool) int {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		count int
	)
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			if f(i) {
				mu.Lock()
				count++
				mu.Unlock()
			}
		})
	}
	close(start)
	wg.Wait()
	return count
}
