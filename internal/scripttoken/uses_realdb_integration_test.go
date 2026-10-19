//go:build integration

package scripttoken

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

// Two replicas verifying one single-use token at once: the primary key lets
// exactly one of them accept it (#2175), and the sweep removes the use once
// the token has expired.
func TestRealDB_ASingleUseTokenIsAcceptedOnceAcrossReplicas(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	replicas := []*Issuer{
		{Keys: func(context.Context, string, string, bool) (string, error) { return "k", nil }, Uses: NewUses(db)},
		{Keys: func(context.Context, string, string, bool) (string, error) { return "k", nil }, Uses: NewUses(db)},
	}
	at := Binding{Endpoint: "credit"}
	minted, err := replicas[0].Bind(at, nil).CallTool(ctx, CapabilityMint, map[string]any{"ttl": 60, "secret": "s", "single_use": true})
	require.NoError(t, err)

	var accepted, used atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			out, err := replicas[i%2].Bind(at, nil).CallTool(ctx, CapabilityVerify, map[string]any{"token": minted["token"], "secret": "s"})
			if !assert.NoError(t, err) {
				return
			}
			switch {
			case out["ok"] == true:
				accepted.Add(1)
			case out["reason"] == ReasonUsed:
				used.Add(1)
			default:
				t.Errorf("unexpected answer: %v", out)
			}
		})
	}
	wg.Wait()
	assert.Equal(t, int32(1), accepted.Load())
	assert.Equal(t, int32(7), used.Load())

	uses := NewUses(db)
	n, err := uses.Sweep(ctx, time.Now())
	require.NoError(t, err)
	assert.Zero(t, n, "the token has a minute left")
	n, err = uses.Sweep(ctx, time.Now().Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}
