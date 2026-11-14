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

// #2480, #2434: adding, turning off and deleting a webhook handler each reach
// every replica's map through the trigger on script_webhook_handlers and a
// LISTEN, with no wait on MaxAge. Several scripts may run for one source and
// one script for several sources.
func TestRealDB_ASourceChangeDropsTheBindings(t *testing.T) {
	db, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()
	scripts, events := scriptstore.New(db), NewStore(db)
	b := &Bindings{Reader: events, MaxAge: time.Hour}
	h := heard{b: b, ch: make(chan struct{}, 8)}
	l := pglisten.New(dsn, BindingsChannel, h)
	require.NoError(t, l.Start(ctx))
	t.Cleanup(l.Stop)

	author := script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}
	sc := &script.Script{Name: "on-sale", Source: "def main():\n    print(1)\n", OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, scripts.Create(ctx, sc, author))
	other := &script.Script{Name: "on-sale-audit", Source: "def main():\n    print(2)\n", OwnerEmail: "jane@example.com", Enabled: true}
	require.NoError(t, scripts.Create(ctx, other, author))
	ids, err := b.For(ctx, "shop")
	require.NoError(t, err)
	assert.Empty(t, ids)

	heardIt := func(what string) {
		t.Helper()
		select {
		case <-h.ch:
		case <-time.After(10 * time.Second):
			t.Fatalf("no NOTIFY on %s after %s", BindingsChannel, what)
		}
	}
	add := func(scriptID, source string) Handler {
		t.Helper()
		w, created, err := events.AddHandler(ctx, scriptID, source, "jane@example.com")
		require.NoError(t, err)
		require.True(t, created)
		heardIt("binding " + source)
		return w
	}

	shop := add(sc.ID, "shop")
	ids, err = b.For(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{sc.ID}, ids, "a script bound on any replica is seen without waiting out MaxAge")

	add(other.ID, "shop")
	ids, _ = b.For(ctx, "shop")
	assert.ElementsMatch(t, []string{sc.ID, other.ID}, ids, "two scripts run for one source")

	add(sc.ID, "esp")
	ids, _ = b.For(ctx, "esp")
	assert.Equal(t, []string{sc.ID}, ids, "one script runs for a second source")

	again, created, err := events.AddHandler(ctx, sc.ID, "shop", "admin@example.com")
	require.NoError(t, err)
	assert.False(t, created, "binding a source the script holds answers its handler")
	assert.Equal(t, shop.ID, again.ID)
	heardIt("binding shop again")

	require.NoError(t, events.EnableHandler(ctx, shop.ID, false, "admin@example.com"))
	heardIt("turning a handler off")
	ids, _ = b.For(ctx, "shop")
	assert.Equal(t, []string{other.ID}, ids, "a handler turned off runs nothing")

	require.NoError(t, events.DeleteHandler(ctx, shop.ID))
	heardIt("deleting a handler")
	held, err := events.Handlers(ctx, HandlerFilter{ScriptIDs: []string{sc.ID}})
	require.NoError(t, err)
	require.Len(t, held, 1)
	assert.Equal(t, "esp", held[0].Source)

	_, err = scripts.Delete(ctx, other.ID)
	require.NoError(t, err)
	heardIt("deleting a script, which takes its handlers")
	ids, _ = b.For(ctx, "shop")
	assert.Empty(t, ids)
}
