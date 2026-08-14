package scriptlayer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// failingStore wraps memStore and fails the write paths, so the tool's error
// handling is exercised without needing a database that can be broken.
type failingStore struct {
	*memStore
	updateErr       error
	deleteErr       error
	listErr         error
	createErr       error
	listVersionsErr error
}

func (f *failingStore) ListVersions(ctx context.Context, scriptID string) ([]script.Version, error) {
	if f.listVersionsErr != nil {
		return nil, f.listVersionsErr
	}
	return f.memStore.ListVersions(ctx, scriptID)
}

func (f *failingStore) Create(ctx context.Context, sc *script.Script, author script.Author) error {
	if f.createErr != nil {
		return f.createErr
	}
	return f.memStore.Create(ctx, sc, author)
}

func (f *failingStore) Delete(ctx context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return f.memStore.Delete(ctx, id)
}

func (f *failingStore) List(ctx context.Context, filter script.ListFilter) ([]script.Script, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.memStore.List(ctx, filter)
}

func (f *failingStore) UpdateWithVersion(ctx context.Context, sc *script.Script, author script.Author) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	return f.memStore.UpdateWithVersion(ctx, sc, author)
}

// newFailingHandle builds a Handle over a store that can be made to fail.
func newFailingHandle() (*Handle, *failingStore) {
	store := &failingStore{memStore: newMemStore()}
	return New(Config{Store: store, AdminPersona: "admin"}), store
}

// TestStoreFailuresAreReportedWithoutLeakingDetail pins the error contract: a
// caller learns the operation failed, and the underlying message (which can
// carry SQL or schema detail) stays in the log.
func TestStoreFailuresAreReportedWithoutLeakingDetail(t *testing.T) {
	boom := errors.New("pq: relation \"scripts\" does not exist")

	t.Run("create", func(t *testing.T) {
		h, store := newFailingHandle()
		store.createErr = boom
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "a", Source: "x = 1"})
		assert.True(t, res.IsError)
		assert.Equal(t, "failed to create script", resultText(res))
	})

	t.Run("update", func(t *testing.T) {
		h, store := newFailingHandle()
		createDaily(t, h)
		store.updateErr = boom
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", DisplayName: "x"})
		assert.True(t, res.IsError)
		assert.Equal(t, "failed to update script", resultText(res))
	})

	t.Run("delete", func(t *testing.T) {
		h, store := newFailingHandle()
		createDaily(t, h)
		store.deleteErr = boom
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdDelete, Name: "daily"})
		assert.True(t, res.IsError)
		assert.Equal(t, "failed to delete script", resultText(res))
	})

	t.Run("list", func(t *testing.T) {
		h, store := newFailingHandle()
		store.listErr = boom
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdList})
		assert.True(t, res.IsError)
		assert.Equal(t, "failed to list scripts", resultText(res))
	})
}

// TestVersionConflictReachesTheCaller is the exception to the rule above: a
// conflict is the caller's to resolve, so its message (re-read and retry) is
// passed through rather than replaced.
func TestVersionConflictReachesTheCaller(t *testing.T) {
	h, store := newFailingHandle()
	createDaily(t, h)
	store.updateErr = errors.New("script was approved while this edit was in flight: " + script.ErrVersionConflict.Error())

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", DisplayName: "x"})
	assert.True(t, res.IsError)
	assert.Equal(t, "failed to update script", resultText(res),
		"a plain error string is not a conflict; only the wrapped sentinel is")

	store.updateErr = errors.Join(script.ErrVersionConflict, errors.New("re-read and retry"))
	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", DisplayName: "y"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "re-read and retry")
}

func TestValidate_InlineSourceNeedsNoStoredScript(t *testing.T) {
	h, _ := newHandle()
	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdValidate, Source: "platform.query(connection=\"warehouse\", sql=\"SELECT 1\")",
	}))
	assert.Equal(t, true, fields["ok"])
	assert.Equal(t, []any{"warehouse"}, fields["connections"])
}

func TestValidate_ReportsADynamicConnection(t *testing.T) {
	h, _ := newHandle()
	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdValidate, Source: "c = \"a\" + \"b\"\nplatform.query(connection=c, sql=\"SELECT 1\")",
	}))
	assert.Equal(t, true, fields["dynamic_connections"])
	assert.Contains(t, fields["connections_note"], "incomplete")
}

func TestValidate_InvalidSourceOffersHelp(t *testing.T) {
	h, _ := newHandle()
	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdValidate, Source: "import os",
	}))
	assert.Equal(t, false, fields["ok"])
	assert.Contains(t, fields["help"], "command=help")
}

func TestValidate_UnknownScript(t *testing.T) {
	h, _ := newHandle()
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdValidate, Name: "nope"})
	assert.True(t, res.IsError)
}
