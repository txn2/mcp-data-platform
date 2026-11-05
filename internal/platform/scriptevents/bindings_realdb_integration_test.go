//go:build integration

package scriptevents

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/pglisten"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// heard forwards each NOTIFY to the Bindings and then to a channel the test
// waits on, so the assertion follows the drop rather than racing it.
type heard struct {
	b  *Bindings
	ch chan struct{}
}

func (h heard) Notify() {
	h.b.Notify()
	h.ch <- struct{}{}
}

// #2480: binding a script to a source, moving it and clearing it each reach
// every replica's map through migration 000230's trigger and a LISTEN, with
// no wait on MaxAge.
func TestRealDB_ASourceChangeDropsTheBindings(t *testing.T) {
	db, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()
	scripts, events := scriptstore.New(db), NewStore(db)
	b := &Bindings{Reader: events, MaxAge: time.Hour}
	h := heard{b: b, ch: make(chan struct{}, 8)}
	l := pglisten.New(dsn, BindingsChannel, h)
	require.NoError(t, l.Start(ctx))
	t.Cleanup(l.Stop)

	sc := &script.Script{Name: "on-sale", Source: "def main():\n    print(1)\n", OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, scripts.Create(ctx, sc, script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}))
	ids, err := b.For(ctx, "shop")
	require.NoError(t, err)
	assert.Empty(t, ids)

	step := func(source string) {
		t.Helper()
		live, err := scripts.GetByID(ctx, sc.ID)
		require.NoError(t, err)
		live.EventSource = source
		require.NoError(t, scripts.Update(ctx, live))
		select {
		case <-h.ch:
		case <-time.After(10 * time.Second):
			t.Fatalf("no NOTIFY on %s after setting event_source to %q", BindingsChannel, source)
		}
	}

	step("shop")
	ids, err = b.For(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{sc.ID}, ids, "a script bound on any replica is seen without waiting out MaxAge")

	step("esp")
	ids, _ = b.For(ctx, "shop")
	assert.Empty(t, ids)
	ids, _ = b.For(ctx, "esp")
	assert.Equal(t, []string{sc.ID}, ids, "a moved binding")

	step("")
	ids, _ = b.For(ctx, "esp")
	assert.Empty(t, ids, "a cleared binding")
}
