package tableregister

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheck_AnswersWithNothingWritten is #2213: a script's export asks
// whether its register target can be used before it writes the file, so the
// answer must run no statement and record nothing.
func TestCheck_AnswersWithNothingWritten(t *testing.T) {
	h := newHarness(t)

	target, err := h.reg.Check(context.Background(), testCaller(), "scratch", "Weekly Sales")
	require.NoError(t, err)
	assert.Equal(t, "scratch.uploads.analyst_weekly_sales", target.QueryTable())
	assert.Empty(t, h.trino.statements)
	assert.Empty(t, h.audit.events)

	target, err = h.reg.Check(context.Background(), testCaller(), "scratch", "")
	require.NoError(t, err)
	assert.Empty(t, target.QueryTable(), "no name asked about, none answered")
	assert.Equal(t, "uploads", target.Schema)
}

// TestCheck_RefusalsNameTheConnectionsThatWork: the fix is in the message.
func TestCheck_RefusalsNameTheConnectionsThatWork(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.trino.noTarget = map[string]bool{"warehouse": true} })

	_, err := h.reg.Check(context.Background(), testCaller(), "warehouse", "t")
	require.ErrorIs(t, err, ErrNoScratchTarget, "every surface still classifies the refusal")
	assert.Contains(t, err.Error(), "Connections you can register a table on: lake, scratch")

	// Register says the same.
	_, err = h.reg.Register(context.Background(), testCaller(), testSource(), Request{Connection: "warehouse"})
	require.ErrorIs(t, err, ErrNoScratchTarget)
	assert.Contains(t, err.Error(), "lake, scratch")
}

func TestCheck_NoConnectionAcceptsATable(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.trino.hasTarget = false })
	_, err := h.reg.Check(context.Background(), testCaller(), "warehouse", "")
	require.ErrorIs(t, err, ErrNoScratchTarget)
	assert.Contains(t, err.Error(), "No connection you can reach accepts a table")
}

func TestCheck_ReadOnlyAndDenied(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.trino.readOnly = true })
	_, err := h.reg.Check(context.Background(), testCaller(), "scratch", "")
	require.ErrorIs(t, err, ErrConnectionReadOnly)

	d := newHarness(t, func(h *harness) { h.scope = denyScope{denied: "scratch"} })
	_, err = d.reg.Check(context.Background(), testCaller(), "scratch", "")
	require.ErrorIs(t, err, ErrConnectionDenied)
	assert.Contains(t, err.Error(), "lake, warehouse", "a connection the persona is denied is not offered")
}

func TestCheck_TableName(t *testing.T) {
	h := newHarness(t)
	_, err := h.reg.Check(context.Background(), testCaller(), "scratch", "!!!")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no letter or digit")

	// A name somebody else registered is refused as register refuses it.
	_, err = h.reg.Register(context.Background(), Caller{UserID: "u2", Email: "bob@example.com", Persona: "analyst"},
		testSource(), Request{Connection: "scratch", TableName: "keys"})
	require.NoError(t, err)
	_, err = h.reg.Check(context.Background(), testCaller(), "scratch", "keys")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered by bob@example.com")

	// Its holder may replace it.
	_, err = h.reg.Check(context.Background(), Caller{UserID: "u2", Email: "bob@example.com", Persona: "analyst"}, "scratch", "keys")
	require.NoError(t, err)
}

func TestCheck_StoreFailureAndUnavailable(t *testing.T) {
	h := newHarness(t)
	h.store.byNameErr = errors.New("db down")
	_, err := h.reg.Check(context.Background(), testCaller(), "scratch", "t")
	require.Error(t, err)

	_, err = New(Deps{}).Check(context.Background(), testCaller(), "scratch", "")
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestScratchConnections_WithoutAList(t *testing.T) {
	assert.Empty(t, New(Deps{Trino: &fakeTrino{hasTarget: true}}).ScratchConnections(testCaller()))
}
