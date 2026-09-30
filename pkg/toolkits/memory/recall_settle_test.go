package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
)

// t0 is when the capture under test was stored.
var t0 = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func pendingRecord(status string) *memstore.Record {
	return &memstore.Record{
		ID: "new-mem", CreatedBy: "a@example.com", CreatedAt: t0, Status: status,
		EntityURNs: []string{"urn:x"},
		Metadata:   map[string]any{memstore.MetaKeyRecallCheck: memstore.RecallCheckPending},
	}
}

func settleToolkit(rec *memstore.Record, rc RecallChecker) (*Toolkit, *mockStore) {
	store := &mockStore{getResult: rec}
	tk := newTestToolkit(store, nil)
	if rc != nil {
		tk.SetRecallChecker(rc)
	}
	return tk, store
}

// TestSettleRecall_NewerCaptureSupersedesEveryOlderRestatement: the capture
// is the newest of the set, so it supersedes every restated record, lists the
// suggest band on itself, skips its own id, and records the check as done.
func TestSettleRecall_NewerCaptureSupersedesEveryOlderRestatement(t *testing.T) {
	rc := &fakeRecallChecker{matches: []RecallMatch{
		{ID: "new-mem", Score: 1, CreatedAt: t0},
		{ID: "dup-a", Score: 0.97, CreatedAt: t0.Add(-time.Hour)},
		{ID: "dup-b", Score: 0.93, CreatedAt: t0.Add(-2 * time.Hour)},
		{ID: "nearby", Score: 0.8, CreatedAt: t0.Add(time.Hour)},
	}}
	tk, store := settleToolkit(pendingRecord(memstore.StatusActive), rc)

	vec := []float32{0.4, 0.5}
	tk.SettleRecall(context.Background(), "new-mem", vec)

	assert.Equal(t, vec, rc.gotEmbedding, "the check uses the vector the index job wrote")
	assert.Equal(t, recallSuggestThreshold, rc.gotMinScore)
	assert.Equal(t, [][2]string{{"dup-a", "new-mem"}, {"dup-b", "new-mem"}}, store.supersedeCalls)
	assert.Equal(t, "new-mem", store.updatedID)
	meta := store.updatedFields.Metadata
	assert.Equal(t, memstore.RecallCheckDone, meta[memstore.MetaKeyRecallCheck])
	similar, ok := meta[memstore.MetaKeySimilarExisting].([]RecallMatch)
	require.True(t, ok)
	require.Len(t, similar, 1)
	assert.Equal(t, "nearby", similar[0].ID)
}

// TestSettleRecall_OlderCaptureIsSupersededByTheNewer covers the order a
// queue can embed two restatements in: the older one's check runs last and
// finds the newer, and the newer wins.
func TestSettleRecall_OlderCaptureIsSupersededByTheNewer(t *testing.T) {
	rc := &fakeRecallChecker{matches: []RecallMatch{
		{ID: "newest", Score: 0.95, CreatedAt: t0.Add(2 * time.Minute)},
		{ID: "newer", Score: 0.94, CreatedAt: t0.Add(time.Minute)},
		{ID: "older", Score: 0.92, CreatedAt: t0.Add(-time.Minute)},
	}}
	tk, store := settleToolkit(pendingRecord(memstore.StatusActive), rc)

	tk.SettleRecall(context.Background(), "new-mem", []float32{1})

	assert.Equal(t, [][2]string{{"older", "new-mem"}, {"new-mem", "newest"}}, store.supersedeCalls)
	assert.NotContains(t, store.updatedFields.Metadata, memstore.MetaKeySimilarExisting)
	assert.Equal(t, memstore.RecallCheckDone, store.updatedFields.Metadata[memstore.MetaKeyRecallCheck])
}

// TestSettleRecall_RunsOncePerCapture: a record not marked pending (a re-embed
// after an edit or a model swap, or one already checked) is left alone.
func TestSettleRecall_RunsOncePerCapture(t *testing.T) {
	rec := pendingRecord(memstore.StatusActive)
	rec.Metadata[memstore.MetaKeyRecallCheck] = memstore.RecallCheckDone
	rc := &fakeRecallChecker{matches: []RecallMatch{{ID: "dup", Score: 0.99, CreatedAt: t0.Add(-time.Hour)}}}
	tk, store := settleToolkit(rec, rc)

	tk.SettleRecall(context.Background(), "new-mem", []float32{1})

	assert.Nil(t, rc.gotEmbedding)
	assert.Empty(t, store.supersedeCalls)
	assert.Empty(t, store.updatedID)
}

// TestSettleRecall_SupersededMeanwhileIsMarkedDone: a capture another check
// already superseded is not checked again, and stops being pending.
func TestSettleRecall_SupersededMeanwhileIsMarkedDone(t *testing.T) {
	rc := &fakeRecallChecker{matches: []RecallMatch{{ID: "dup", Score: 0.99}}}
	tk, store := settleToolkit(pendingRecord(memstore.StatusSuperseded), rc)

	tk.SettleRecall(context.Background(), "new-mem", []float32{1})

	assert.Nil(t, rc.gotEmbedding)
	assert.Empty(t, store.supersedeCalls)
	assert.Equal(t, memstore.RecallCheckDone, store.updatedFields.Metadata[memstore.MetaKeyRecallCheck])
}

// TestSettleRecall_FailuresLeaveTheCaptureStored: every failure is logged and
// tolerated; a failed similarity check leaves the record pending for its next
// embed, and a failed supersede still records the check.
func TestSettleRecall_FailuresLeaveTheCaptureStored(t *testing.T) {
	t.Run("similarity check fails", func(t *testing.T) {
		tk, store := settleToolkit(pendingRecord(memstore.StatusActive), &fakeRecallChecker{err: errBoom})
		tk.SettleRecall(context.Background(), "new-mem", []float32{1})
		assert.Empty(t, store.updatedID, "the record stays pending")
	})
	t.Run("supersede fails", func(t *testing.T) {
		rc := &fakeRecallChecker{matches: []RecallMatch{{ID: "dup", Score: 0.99, CreatedAt: t0.Add(-time.Hour)}}}
		tk, store := settleToolkit(pendingRecord(memstore.StatusActive), rc)
		store.supersedeErr = errBoom
		tk.SettleRecall(context.Background(), "new-mem", []float32{1})
		assert.Equal(t, memstore.RecallCheckDone, store.updatedFields.Metadata[memstore.MetaKeyRecallCheck])
	})
	t.Run("record unreadable", func(t *testing.T) {
		rc := &fakeRecallChecker{}
		tk, store := settleToolkit(nil, rc)
		store.getErr = errBoom
		tk.SettleRecall(context.Background(), "new-mem", []float32{1})
		assert.Nil(t, rc.gotEmbedding)
	})
	t.Run("marking done fails", func(t *testing.T) {
		tk, store := settleToolkit(pendingRecord(memstore.StatusActive), &fakeRecallChecker{})
		store.updateErr = errBoom
		tk.SettleRecall(context.Background(), "new-mem", []float32{1})
		assert.Empty(t, store.supersedeCalls)
	})
	t.Run("no checker or no vector", func(t *testing.T) {
		tk, store := settleToolkit(pendingRecord(memstore.StatusActive), nil)
		tk.SettleRecall(context.Background(), "new-mem", []float32{1})
		tk.SetRecallChecker(&fakeRecallChecker{})
		tk.SettleRecall(context.Background(), "new-mem", nil)
		assert.Empty(t, store.updatedID)
	})
}

// TestIsNewer_TiesBreakOnID keeps the two checks of a pair stored in the same
// instant agreeing on the winner.
func TestIsNewer_TiesBreakOnID(t *testing.T) {
	rec := &memstore.Record{ID: "m-2", CreatedAt: t0}
	assert.True(t, isNewer(RecallMatch{ID: "m-1", CreatedAt: t0.Add(time.Second)}, rec))
	assert.False(t, isNewer(RecallMatch{ID: "m-3", CreatedAt: t0.Add(-time.Second)}, rec))
	assert.True(t, isNewer(RecallMatch{ID: "m-3", CreatedAt: t0}, rec))
	assert.False(t, isNewer(RecallMatch{ID: "m-1", CreatedAt: t0}, rec))
}
