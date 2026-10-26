package receiver

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whspool"
)

func formSource(name, scriptID string) whsource.Source {
	return whsource.Source{
		Name: name, Enabled: true, Connection: "scratch",
		Config: whsource.Config{
			Input: whsource.InputForm, FlushMaxIntervalMS: 20,
			Form: &whsource.Form{ScriptID: scriptID, Endpoint: name[len(whsource.FormPrefix):]},
		},
	}
}

func formEvent(id string) (ev whevent.Event, body []byte) {
	payload := []byte(`{"endpoint":"contact","values":{"name":"Ana"}}`)
	return whevent.Event{ReceivedAt: time.Now().UTC(), EventID: id, EventType: "form_submission", Payload: payload}, payload
}

// A form's source is written by its form alone: /hooks/ answers it as it
// answers a name it does not serve.
func TestForm_SourceIsNotPostedTo(t *testing.T) {
	h := newHarness(t, formSource("form-contact", "scr_1"))
	resp := h.post(t, "/hooks/form-contact", "application/json", `{"values":{}}`)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, 0, h.objects.count())
}

func TestForm_DeliverWritesBeforeReturning(t *testing.T) {
	h := newHarness(t, formSource("form-contact", "scr_1"))
	src, ok := h.r.FormSource(t.Context(), "scr_1")
	require.True(t, ok)
	ev, body := formEvent("sub-1")
	require.NoError(t, h.r.Deliver(t.Context(), src, ev, body))
	stored := h.objects.events(t)
	require.Len(t, stored, 1, "the event is in object storage when Deliver returns")
	assert.Equal(t, "sub-1", stored[0].EventID)
	assert.Equal(t, "host-a:8080", stored[0].Replica, "the event is stamped with this replica")
	assert.Equal(t, 1, h.metrics.requests["form-contact/"+OutcomeAccepted])

	require.ErrorIs(t, h.r.Deliver(t.Context(), hmacSource("esp"), ev, body), ErrNoFormSource)
}

func TestForm_SourceFoundAfterARereadOfTheSources(t *testing.T) {
	h := newHarness(t)
	_, ok := h.r.FormSource(t.Context(), "scr_1")
	assert.False(t, ok)
	h.sources.set(formSource("form-contact", "scr_1"))
	src, ok := h.r.FormSource(t.Context(), "scr_1")
	require.True(t, ok, "a source made on another replica is found once the sources are read again")
	assert.Equal(t, "form-contact", src.Name)

	disabled := formSource("form-off", "scr_2")
	disabled.Enabled = false
	h.sources.set(disabled)
	_, ok = h.r.FormSource(t.Context(), "scr_2")
	assert.False(t, ok, "a disabled source stores nothing")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, ok = h.r.FormSource(ctx, "scr_3")
	assert.False(t, ok)
}

func TestForm_DeliverToAFullSpoolIsNotStored(t *testing.T) {
	objects := newObjects()
	objects.stall = make(chan struct{})
	h := newSpoolHarness(t, t.TempDir(), objects, whspool.Options{MaxBytes: 500}, formSource("form-contact", "scr_1"))
	src, ok := h.r.FormSource(t.Context(), "scr_1")
	require.True(t, ok)
	ev, body := formEvent("sub-1")
	require.NoError(t, h.r.Deliver(t.Context(), src, ev, body), "a submission is stored once it is on this replica's disk")
	ev2, body2 := formEvent("sub-2")
	err := h.r.Deliver(t.Context(), src, ev2, body2)
	require.ErrorIs(t, err, ErrNotStored)
	var ra interface{ RetryAfter() int }
	require.True(t, errors.As(err, &ra))
	assert.Equal(t, retrySpoolFull, ra.RetryAfter())
	assert.Equal(t, 1, h.metrics.requests["form-contact/"+OutcomeSpoolFull])
	close(objects.stall)
}
