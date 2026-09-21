package scriptlayer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestDraftResult_WithoutAnEngineResult covers the outcome of a run that never
// reached the interpreter: the response still has to describe itself, and every
// reader of it is walking a nil result.
func TestDraftResult_WithoutAnEngineResult(t *testing.T) {
	sc := &script.Script{ID: "sc_1", Name: "ingest"}

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
	assert.NotContains(t, succeeded, "saved", "a draft of a saved script says nothing about saving")

	unsaved := draftResult(&script.Script{Name: "new"}, &scriptdraft.Outcome{RunID: "r3"})
	assert.Equal(t, false, unsaved["saved"], "a draft of a script not saved yet says so")
}

// TestHandle_DraftExports hands the portal editor the same writer the tool's
// drafts use (#1822), and a nil Handle none.
func TestHandle_DraftExports(t *testing.T) {
	var none *Handle
	assert.Nil(t, none.DraftExports())

	called := false
	h := New(Config{DraftExports: func(scriptdraft.Target) scriptrun.Exporter {
		called = true
		return nil
	}})
	exports := h.DraftExports()
	if assert.NotNil(t, exports) {
		exports(scriptdraft.Target{})
	}
	assert.True(t, called, "the writer handed out is the one the layer was given")
}
