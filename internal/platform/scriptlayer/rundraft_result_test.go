package scriptlayer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestDraftResult_WithoutAnEngineResult covers the outcome of a run that never
// reached the interpreter: the response still has to describe itself, and every
// reader of it is walking a nil result.
func TestDraftResult_WithoutAnEngineResult(t *testing.T) {
	sc := &script.Script{Name: "ingest"}

	failed := draftResult(sc, &scriptdraft.Outcome{RunID: "r1", Err: errors.New("boom")})
	assert.Equal(t, "failed", failed[fieldStatus])
	assert.Equal(t, "boom", failed["error"])
	assert.NotContains(t, failed, "refused_write", "nothing was refused, so nothing is named")
	assert.Contains(t, failed["message"], "deterministic")

	succeeded := draftResult(sc, &scriptdraft.Outcome{RunID: "r2"})
	assert.Equal(t, "succeeded", succeeded[fieldStatus])
	assert.Contains(t, succeeded["message"], "Nothing was persisted")
	assert.Equal(t, []scriptrun.WriteRecord{}, succeeded["writes"],
		"an empty answer is an empty list, not null")
}

// TestDraftPersistedNothing_NamesOneWriteInTheSingular keeps the sentence a
// reader acts on grammatical for the common case of a draft that landed one
// thing.
func TestDraftPersistedNothing_NamesOneWriteInTheSingular(t *testing.T) {
	one := draftPersistedNothing(&scriptrun.Result{
		Writes: []scriptrun.WriteRecord{{Tool: "manage_resource", Call: "manage_resource action=create"}},
	})
	assert.Contains(t, one, "the 1 call listed under writes persisted for real")

	two := draftPersistedNothing(&scriptrun.Result{
		Writes: []scriptrun.WriteRecord{{Tool: "a"}, {Tool: "b"}},
	})
	assert.Contains(t, two, "the 2 calls listed under writes persisted for real")
}

// TestDraftPersistedNothing_NamesAnUnsavedState pins that a reader who sees a
// state object is told it did not land, on both the barred and the allowed
// path.
func TestDraftPersistedNothing_NamesAnUnsavedState(t *testing.T) {
	msg := draftPersistedNothing(&scriptrun.Result{State: &script.StateWrite{Value: map[string]any{"c": 1}}})
	require.Contains(t, msg, "Nothing was persisted")
	assert.Contains(t, msg, "did not save it")
}
