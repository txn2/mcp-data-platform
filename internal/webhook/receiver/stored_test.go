package receiver

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whspool"
)

// storedCalls records what the receiver told its hook.
type storedCalls struct {
	mu    sync.Mutex
	calls map[string][]string
}

func (s *storedCalls) hook(_ context.Context, source string, events []whevent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = map[string][]string{}
	}
	for _, e := range events {
		s.calls[source] = append(s.calls[source], e.EventID)
	}
}

func (s *storedCalls) of(source string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls[source]...)
}

// #2332: every stored delivery is told to the hook once it is durable --
// posted to object storage, synced to the spool, or a form's submission --
// and a refused one is not.
func TestStored_TellsEachDurableDelivery(t *testing.T) {
	calls := &storedCalls{}
	h := newHarness(t, hmacSource("esp"), formSource("form-contact", "scr_1"))
	h.r.SetStored(calls.hook)

	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", body("e1")).StatusCode)
	assert.Equal(t, []string{"e1"}, calls.of("esp"))

	resp := h.post(t, "/hooks/esp", "application/json", `{"id":"unsigned"}`)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, []string{"e1"}, calls.of("esp"), "a refused delivery stored nothing to run for")

	src, ok := h.r.FormSource(t.Context(), "scr_1")
	require.True(t, ok)
	ev, raw := formEvent("sub-1")
	require.NoError(t, h.r.Deliver(t.Context(), src, ev, raw))
	assert.Equal(t, []string{"sub-1"}, calls.of("form-contact"))

	h.r.SetStored(nil)
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", body("e2")).StatusCode)
	assert.Equal(t, []string{"e1"}, calls.of("esp"), "no hook, nothing told")
}

func TestStored_TellsASpooledDelivery(t *testing.T) {
	calls := &storedCalls{}
	h := newSpoolHarness(t, t.TempDir(), newObjects(), whspool.Options{}, hmacSource("esp"))
	h.r.SetStored(calls.hook)
	require.Equal(t, http.StatusAccepted, h.signed(t, "esp", body("s1")).StatusCode)
	assert.Equal(t, []string{"s1"}, calls.of("esp"))
}
