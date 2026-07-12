//go:build integration

package platform_test

// Real-Postgres proof for #927: a client connected to the platform's session
// broadcaster observes prompt and managed-resource changes without reconnecting.
// It builds a real Platform (New wires the postgres prompt store, the managed-
// resource layer, and the postgres LISTEN/NOTIFY broadcaster from the DSN), then
// subscribes to the broadcaster and verifies by observation that:
//
//   - creating a prompt through the platform's PromptStore emits
//     notifications/prompts/list_changed, and
//   - registering a managed resource emits notifications/resources/list_changed.
//
// The event round-trips through pg_notify (the cross-replica channel), so this
// also exercises the multi-replica fan-out path a mock broadcaster cannot. Run
// under `make test-realdb`.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

func TestRealDB_ListChanged_PromptAndResourceNotifications(t *testing.T) {
	_, dsn := testdb.NewWithDSN(t)

	p, err := platform.New(platform.WithConfig(&platform.Config{
		Server:   platform.ServerConfig{Name: "listchanged-it", Version: "1.0.0"},
		Database: platform.DatabaseConfig{DSN: dsn, MaxOpenConns: 5},
	}))
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	require.NotNil(t, p.Broadcaster(), "platform must have a broadcaster")
	require.NotNil(t, p.PromptStore(), "platform must have a prompt store with a database")

	ctx := t.Context()

	t.Run("prompt create emits prompts/list_changed", func(t *testing.T) {
		sub := p.Broadcaster().Subscribe(ctx, "session-prompt")
		defer sub.Close()

		// Populate every NOT NULL field (Tags/Personas) so the write survives the
		// real schema — the exact class of defect the RealDB gate exists to catch.
		require.NoError(t, p.PromptStore().Create(ctx, &prompt.Prompt{
			Name:     "listchanged-it-prompt",
			Content:  "Do the thing about {topic}.",
			Scope:    prompt.ScopeGlobal,
			Source:   prompt.SourceOperator,
			Status:   prompt.StatusApproved,
			Enabled:  true,
			Tags:     []string{},
			Personas: []string{},
		}))

		requireEvent(t, sub, "notifications/prompts/list_changed")
	})

	t.Run("managed resource register emits resources/list_changed", func(t *testing.T) {
		sub := p.Broadcaster().Subscribe(ctx, "session-resource")
		defer sub.Close()

		p.RegisterManagedResource(&resource.Resource{
			URI:         "mcp://resource/listchanged-it",
			DisplayName: "listchanged-it",
			MIMEType:    "text/plain",
			Scope:       resource.ScopeGlobal,
		})

		requireEvent(t, sub, "notifications/resources/list_changed")
	})
}

// requireEvent blocks until sub delivers an event with the wanted method or a
// generous deadline elapses (the postgres broadcaster round-trips through
// pg_notify, so delivery is asynchronous).
func requireEvent(t *testing.T, sub session.Subscription, want string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				t.Fatalf("subscription closed before %q", want)
			}
			if ev.Method == want {
				return
			}
			// Ignore unrelated events (heartbeats or other list_changed signals).
		case <-deadline:
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}
