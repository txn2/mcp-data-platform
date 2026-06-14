package portal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// --- fakes ------------------------------------------------------------------

// fakeThreadStore implements portal.ThreadStore with configurable returns and
// captured inputs for the manage_artifact thread actions.
type fakeThreadStore struct {
	listResult  []portal.ThreadWithMeta
	listTotal   int
	listErr     error
	lastFilter  portal.ThreadFilter
	listFilters []portal.ThreadFilter // every filter passed to ListThreads, in order

	getResult *portal.Thread
	getErr    error

	events    []portal.ThreadEvent
	eventsErr error

	appended  *portal.ThreadEvent
	appendErr error

	lastUpdate *portal.ThreadUpdate
	updateErr  error

	validatedID     string
	validateErr     error
	respondedResult string
	respondErr      error
}

func (*fakeThreadStore) CreateThread(_ context.Context, t portal.Thread, _ portal.ThreadEvent) (*portal.Thread, error) {
	return &t, nil
}

func (f *fakeThreadStore) ListThreads(_ context.Context, filter portal.ThreadFilter) ([]portal.ThreadWithMeta, int, error) {
	f.lastFilter = filter
	f.listFilters = append(f.listFilters, filter)
	return f.listResult, f.listTotal, f.listErr
}

func (f *fakeThreadStore) GetThread(_ context.Context, _ string) (*portal.Thread, error) {
	return f.getResult, f.getErr
}

func (f *fakeThreadStore) ListEvents(_ context.Context, _ string) ([]portal.ThreadEvent, error) {
	return f.events, f.eventsErr
}

func (f *fakeThreadStore) AppendEvent(_ context.Context, e portal.ThreadEvent) (*portal.ThreadEvent, error) {
	if f.appendErr != nil {
		return nil, f.appendErr
	}
	if f.appended != nil {
		return f.appended, nil
	}
	return &e, nil
}

func (f *fakeThreadStore) UpdateThread(_ context.Context, _ string, u portal.ThreadUpdate, _, _ string) error {
	f.lastUpdate = &u
	return f.updateErr
}

func (*fakeThreadStore) SoftDeleteThread(_ context.Context, _ string) error { return nil }

func (*fakeThreadStore) LinkInsight(_ context.Context, ids []string, _, _, _ string) ([]string, error) {
	return ids, nil
}

func (f *fakeThreadStore) RequestValidation(_ context.Context, id, _, _ string) error {
	f.validatedID = id
	return f.validateErr
}

func (f *fakeThreadStore) RespondValidation(_ context.Context, _ string, resp portal.ValidationResponse, _, _ string) error {
	f.respondedResult = resp.Result
	return f.respondErr
}

func (*fakeThreadStore) CountOpenByTargets(_ context.Context, _ string, _ []string) (map[string]int, error) {
	return nil, nil //nolint:nilnil // test stub
}

func (*fakeThreadStore) CountSignoffs(_ context.Context, _, _ string) (int, error) { return 0, nil }

var _ portal.ThreadStore = (*fakeThreadStore)(nil)

// fakeShareStore implements portal.ShareStore; only the two access-path methods
// used by canEditAsset / canEditCollection return configurable values.
type fakeShareStore struct {
	assetShare *portal.Share
	collPerm   portal.SharePermission
}

func (f *fakeShareStore) GetActiveShareForTarget(_ context.Context, _, _, _, _ string) (*portal.Share, error) {
	return f.assetShare, nil
}

func (f *fakeShareStore) GetUserCollectionPermission(_ context.Context, _, _, _ string) (portal.SharePermission, error) {
	return f.collPerm, nil
}

func (*fakeShareStore) Insert(_ context.Context, _ portal.Share) error             { return nil }
func (*fakeShareStore) GetByID(_ context.Context, _ string) (*portal.Share, error) { return nil, nil } //nolint:nilnil // test stub
func (*fakeShareStore) GetByToken(_ context.Context, _ string) (*portal.Share, error) {
	return nil, nil //nolint:nilnil // test stub
}

func (*fakeShareStore) ListByAsset(_ context.Context, _ string) ([]portal.Share, error) {
	return nil, nil
}

func (*fakeShareStore) ListByCollection(_ context.Context, _ string) ([]portal.Share, error) {
	return nil, nil
}

func (*fakeShareStore) ListByPrompt(_ context.Context, _ string) ([]portal.Share, error) {
	return nil, nil
}

func (*fakeShareStore) ListSharedWithUser(_ context.Context, _, _ string, _, _ int) ([]portal.SharedAsset, int, error) {
	return nil, 0, nil
}

func (*fakeShareStore) ListSharedCollectionsWithUser(_ context.Context, _, _ string, _, _ int) ([]portal.SharedCollection, int, error) {
	return nil, 0, nil
}

func (*fakeShareStore) ListSharedPromptsWithUser(_ context.Context, _, _ string) ([]portal.SharedPromptRef, error) {
	return nil, nil
}

func (*fakeShareStore) ListActiveShareSummaries(_ context.Context, _ []string) (map[string]portal.ShareSummary, error) {
	return nil, nil //nolint:nilnil // test stub
}

func (*fakeShareStore) ListActiveCollectionShareSummaries(_ context.Context, _ []string) (map[string]portal.ShareSummary, error) {
	return nil, nil //nolint:nilnil // test stub
}

func (*fakeShareStore) GetUserAssetPermissionViaCollection(_ context.Context, _, _, _ string) (portal.SharePermission, error) {
	return "", nil
}
func (*fakeShareStore) Revoke(_ context.Context, _ string) error          { return nil }
func (*fakeShareStore) IncrementAccess(_ context.Context, _ string) error { return nil }

var _ portal.ShareStore = (*fakeShareStore)(nil)

// errShareStore fails the shared-asset lookup so the pending gather errors.
type errShareStore struct{ fakeShareStore }

func (*errShareStore) ListSharedWithUser(_ context.Context, _, _ string, _, _ int) ([]portal.SharedAsset, int, error) {
	return nil, 0, errors.New("share boom")
}

var _ portal.ShareStore = (*errShareStore)(nil)

// --- helpers ----------------------------------------------------------------

const (
	ownerID    = "owner1"
	ownerEmail = "owner1@example.com"
	otherID    = "stranger"
)

func ownerCtx() context.Context {
	return middleware.WithPlatformContext(context.Background(),
		&middleware.PlatformContext{UserID: ownerID, UserEmail: ownerEmail})
}

func adminCtx() context.Context {
	return middleware.WithPlatformContext(context.Background(),
		&middleware.PlatformContext{UserID: "admin1", IsAdmin: true})
}

func strangerCtx() context.Context {
	return middleware.WithPlatformContext(context.Background(),
		&middleware.PlatformContext{UserID: otherID, UserEmail: "stranger@example.com"})
}

// threadToolkit builds a portal toolkit wired with the given thread store and
// an asset/collection/share store seeded so that `ownerID` owns asset_1 and
// collection_1.
func threadToolkit(t *testing.T, threads portal.ThreadStore, shares portal.ShareStore) *Toolkit {
	t.Helper()
	assets := newInMemoryAssetStore()
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{ID: "asset_1", OwnerID: ownerID, OwnerEmail: ownerEmail}))
	colls := newInMemoryCollectionStore()
	require.NoError(t, colls.Insert(context.Background(), portal.Collection{ID: "collection_1", OwnerID: ownerID}))
	if shares == nil {
		shares = &fakeShareStore{}
	}
	return New(Config{
		Name: "test", S3Bucket: "b",
		ThreadStore: threads, AssetStore: assets, CollectionStore: colls, ShareStore: shares,
	})
}

func decodeResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

// --- threadScopeFromInput / countNonEmpty -----------------------------------

func TestThreadScopeFromInput(t *testing.T) {
	tests := []struct {
		name   string
		input  manageFeedbackInput
		want   string
		wantOK bool
	}{
		{"standalone no ids", manageFeedbackInput{TargetType: "standalone"}, "standalone", true},
		{"standalone with asset id", manageFeedbackInput{TargetType: "standalone", AssetID: "a"}, "standalone", false},
		{"asset only", manageFeedbackInput{AssetID: "a"}, "asset", true},
		{"collection only", manageFeedbackInput{CollectionID: "c"}, "collection", true},
		{"prompt only", manageFeedbackInput{PromptID: "p"}, "prompt", true},
		{"no ids, not standalone", manageFeedbackInput{}, "", false},
		{"two ids", manageFeedbackInput{AssetID: "a", CollectionID: "c"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := threadScopeFromInput(tt.input)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestCountNonEmpty(t *testing.T) {
	assert.Equal(t, 0, countNonEmpty("", "", ""))
	assert.Equal(t, 1, countNonEmpty("a", "", ""))
	assert.Equal(t, 3, countNonEmpty("a", "b", "c"))
}

// --- handleListThreads ------------------------------------------------------

func TestHandleListThreads(t *testing.T) {
	t.Run("store unavailable", func(t *testing.T) {
		tk := New(Config{Name: "test", S3Bucket: "b"}) // no ThreadStore
		res, _, err := tk.handleListThreads(ownerCtx(), manageFeedbackInput{AssetID: "asset_1"})
		require.NoError(t, err)
		assert.True(t, res.IsError)
		assert.Contains(t, decodeResult(t, res)["error"], "not available")
	})

	t.Run("no target lists pending across everything", func(t *testing.T) {
		fts := &fakeThreadStore{
			listTotal:  3,
			listResult: []portal.ThreadWithMeta{{Thread: portal.Thread{ID: "thr_1"}}},
		}
		tk := threadToolkit(t, fts, nil)
		res, _, err := tk.handleListThreads(ownerCtx(), manageFeedbackInput{})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		// First ListThreads call is the pending query: my owned/editable artifacts
		// + the general channel, unresolved, excluding my own threads.
		require.GreaterOrEqual(t, len(fts.listFilters), 1)
		pf := fts.listFilters[0]
		assert.True(t, pf.IncludeStandalone)
		assert.True(t, pf.Unresolved)
		assert.Equal(t, ownerID, pf.ExcludeAuthorID)
		assert.Equal(t, ownerEmail, pf.ExcludeAuthorEmail)
		assert.Contains(t, pf.TargetAssetIDs, "asset_1")
		assert.Contains(t, pf.TargetCollectionIDs, "collection_1")
		// Second call is the awaiting-my-validation query.
		require.Len(t, fts.listFilters, 2)
		assert.Equal(t, portal.ValidationStatePending, fts.listFilters[1].ValidationState)
		assert.Equal(t, ownerID, fts.listFilters[1].AuthorID)

		body := decodeResult(t, res)
		assert.Contains(t, body, "pending")
		assert.Contains(t, body, "awaiting_my_validation")
	})

	t.Run("no target, anonymous skips validation queue", func(t *testing.T) {
		fts := &fakeThreadStore{}
		tk := threadToolkit(t, fts, nil)
		res, _, _ := tk.handleListThreads(context.Background(), manageFeedbackInput{})
		assert.False(t, res.IsError)
		// Anonymous: pending query runs, but the validation queue is skipped.
		assert.Len(t, fts.listFilters, 1)
	})

	t.Run("no target, gather error", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, &errShareStore{})
		res, _, _ := tk.handleListThreads(ownerCtx(), manageFeedbackInput{})
		assert.True(t, res.IsError)
		assert.Contains(t, decodeResult(t, res)["error"], "gather")
	})

	t.Run("no target, pending store error", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{listErr: errors.New("boom")}, nil)
		res, _, _ := tk.handleListThreads(ownerCtx(), manageFeedbackInput{})
		assert.True(t, res.IsError)
		assert.Contains(t, decodeResult(t, res)["error"], "pending feedback")
	})

	t.Run("access denied for non-owner non-editor", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, nil)
		res, _, _ := tk.handleListThreads(strangerCtx(), manageFeedbackInput{AssetID: "asset_1"})
		assert.True(t, res.IsError)
		assert.Contains(t, decodeResult(t, res)["error"], "own or can edit")
	})

	t.Run("owner sees threads and filter honors requires_resolution", func(t *testing.T) {
		fts := &fakeThreadStore{listTotal: 2}
		tk := threadToolkit(t, fts, nil)
		req := true
		res, _, err := tk.handleListThreads(ownerCtx(), manageFeedbackInput{
			AssetID: "asset_1", RequiresResolution: &req, ValidationState: "pending",
		})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		// #602: list_threads honors the requires_resolution and validation_state filters.
		require.NotNil(t, fts.lastFilter.RequiresResolution)
		assert.True(t, *fts.lastFilter.RequiresResolution)
		assert.Equal(t, "pending", fts.lastFilter.ValidationState)
		assert.Equal(t, "asset", fts.lastFilter.TargetType)
	})

	t.Run("editor share grants access", func(t *testing.T) {
		shares := &fakeShareStore{assetShare: &portal.Share{Permission: portal.PermissionEditor}}
		tk := threadToolkit(t, &fakeThreadStore{}, shares)
		res, _, _ := tk.handleListThreads(strangerCtx(), manageFeedbackInput{AssetID: "asset_1"})
		assert.False(t, res.IsError)
	})

	t.Run("admin sees any target", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, nil)
		res, _, _ := tk.handleListThreads(adminCtx(), manageFeedbackInput{AssetID: "asset_1"})
		assert.False(t, res.IsError)
	})

	t.Run("store error", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{listErr: errors.New("boom")}, nil)
		res, _, _ := tk.handleListThreads(ownerCtx(), manageFeedbackInput{AssetID: "asset_1"})
		assert.True(t, res.IsError)
	})

	t.Run("standalone scope open to any authed caller", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, nil)
		res, _, _ := tk.handleListThreads(strangerCtx(), manageFeedbackInput{TargetType: "standalone"})
		assert.False(t, res.IsError)
	})

	t.Run("collection owner lists", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, nil)
		res, _, _ := tk.handleListThreads(ownerCtx(), manageFeedbackInput{CollectionID: "collection_1"})
		assert.False(t, res.IsError)
	})

	t.Run("prompt scope denied for non-admin", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, nil)
		res, _, _ := tk.handleListThreads(ownerCtx(), manageFeedbackInput{PromptID: "p1"})
		assert.True(t, res.IsError)
	})
}

// --- handleGetThread --------------------------------------------------------

func TestHandleGetThread(t *testing.T) {
	t.Run("missing thread_id", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, nil)
		res, _, _ := tk.handleGetThread(ownerCtx(), manageFeedbackInput{})
		assert.True(t, res.IsError)
		assert.Contains(t, decodeResult(t, res)["error"], "thread_id is required")
	})

	t.Run("not found", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getErr: errors.New("nope")}, nil)
		res, _, _ := tk.handleGetThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.True(t, res.IsError)
	})

	t.Run("access denied", func(t *testing.T) {
		fts := &fakeThreadStore{getResult: &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1"}}
		tk := threadToolkit(t, fts, nil)
		res, _, _ := tk.handleGetThread(strangerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.True(t, res.IsError)
		assert.Contains(t, decodeResult(t, res)["error"], "own or can edit")
	})

	t.Run("success returns thread and events", func(t *testing.T) {
		fts := &fakeThreadStore{
			getResult: &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1"},
			events:    []portal.ThreadEvent{{ID: "evt_1", EventType: portal.EventTypeComment}},
		}
		tk := threadToolkit(t, fts, nil)
		res, _, err := tk.handleGetThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1"})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		m := decodeResult(t, res)
		assert.NotNil(t, m["thread"])
		assert.NotNil(t, m["events"])
	})

	t.Run("events error", func(t *testing.T) {
		fts := &fakeThreadStore{
			getResult: &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1"},
			eventsErr: errors.New("boom"),
		}
		tk := threadToolkit(t, fts, nil)
		res, _, _ := tk.handleGetThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.True(t, res.IsError)
	})
}

// --- handleReplyThread ------------------------------------------------------

func TestHandleReplyThread(t *testing.T) {
	thread := &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1"}

	t.Run("empty body", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, nil)
		res, _, _ := tk.handleReplyThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1", Body: "  "})
		assert.True(t, res.IsError)
		assert.Contains(t, decodeResult(t, res)["error"], "body is required")
	})

	t.Run("success appends a comment", func(t *testing.T) {
		fts := &fakeThreadStore{getResult: thread}
		tk := threadToolkit(t, fts, nil)
		res, _, err := tk.handleReplyThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1", Body: "looks good"})
		require.NoError(t, err)
		assert.False(t, res.IsError)
	})

	t.Run("append error", func(t *testing.T) {
		fts := &fakeThreadStore{getResult: thread, appendErr: errors.New("boom")}
		tk := threadToolkit(t, fts, nil)
		res, _, _ := tk.handleReplyThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1", Body: "x"})
		assert.True(t, res.IsError)
	})
}

// --- handleResolveThread / handleRequestValidation --------------------------

func TestHandleResolveThread(t *testing.T) {
	thread := &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1"}

	t.Run("owner resolves", func(t *testing.T) {
		fts := &fakeThreadStore{getResult: thread}
		tk := threadToolkit(t, fts, nil)
		res, _, err := tk.handleResolveThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1"})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		require.NotNil(t, fts.lastUpdate)
		require.NotNil(t, fts.lastUpdate.Status)
		assert.Equal(t, portal.ThreadStatusResolved, *fts.lastUpdate.Status)
	})

	t.Run("update error", func(t *testing.T) {
		fts := &fakeThreadStore{getResult: thread, updateErr: errors.New("boom")}
		tk := threadToolkit(t, fts, nil)
		res, _, _ := tk.handleResolveThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.True(t, res.IsError)
	})

	t.Run("standalone non-author denied", func(t *testing.T) {
		standalone := &portal.Thread{ID: "t2", TargetType: "standalone", AuthorID: ownerID, AuthorEmail: ownerEmail}
		tk := threadToolkit(t, &fakeThreadStore{getResult: standalone}, nil)
		res, _, _ := tk.handleResolveThread(strangerCtx(), manageFeedbackInput{ThreadID: "t2"})
		assert.True(t, res.IsError)
	})

	t.Run("standalone author allowed", func(t *testing.T) {
		standalone := &portal.Thread{ID: "t2", TargetType: "standalone", AuthorID: ownerID, AuthorEmail: ownerEmail}
		tk := threadToolkit(t, &fakeThreadStore{getResult: standalone}, nil)
		res, _, _ := tk.handleResolveThread(ownerCtx(), manageFeedbackInput{ThreadID: "t2"})
		assert.False(t, res.IsError)
	})
}

func TestHandleRequestValidation(t *testing.T) {
	thread := &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1"}

	t.Run("success", func(t *testing.T) {
		fts := &fakeThreadStore{getResult: thread}
		tk := threadToolkit(t, fts, nil)
		res, _, err := tk.handleRequestValidation(ownerCtx(), manageFeedbackInput{ThreadID: "t1"})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		assert.Equal(t, "t1", fts.validatedID)
		assert.Equal(t, portal.ValidationStatePending, decodeResult(t, res)["validation_state"])
	})

	t.Run("store error", func(t *testing.T) {
		fts := &fakeThreadStore{getResult: thread, validateErr: errors.New("boom")}
		tk := threadToolkit(t, fts, nil)
		res, _, _ := tk.handleRequestValidation(ownerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.True(t, res.IsError)
	})
}

// --- handleRespondValidation -------------------------------------------------

func TestHandleRespondValidation(t *testing.T) {
	// The responder is the thread author (the SME the request was routed to).
	authored := &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1", AuthorID: ownerID, AuthorEmail: ownerEmail}

	t.Run("author validates", func(t *testing.T) {
		fts := &fakeThreadStore{getResult: authored}
		tk := threadToolkit(t, fts, nil)
		res, _, err := tk.handleRespondValidation(ownerCtx(), manageFeedbackInput{ThreadID: "t1", ValidationResult: "validated"})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		assert.Equal(t, "validated", fts.respondedResult)
		assert.Equal(t, "validated", decodeResult(t, res)["validation_state"])
	})

	t.Run("author disputes with reason", func(t *testing.T) {
		fts := &fakeThreadStore{getResult: authored}
		tk := threadToolkit(t, fts, nil)
		res, _, _ := tk.handleRespondValidation(ownerCtx(),
			manageFeedbackInput{ThreadID: "t1", ValidationResult: "disputed", ValidationReason: "still wrong"})
		assert.False(t, res.IsError)
		assert.Equal(t, "disputed", fts.respondedResult)
	})

	t.Run("admin can respond", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: authored}, nil)
		res, _, _ := tk.handleRespondValidation(adminCtx(), manageFeedbackInput{ThreadID: "t1", ValidationResult: "validated"})
		assert.False(t, res.IsError)
	})

	t.Run("non-author denied", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: authored}, nil)
		res, _, _ := tk.handleRespondValidation(strangerCtx(), manageFeedbackInput{ThreadID: "t1", ValidationResult: "validated"})
		assert.True(t, res.IsError)
		assert.Contains(t, decodeResult(t, res)["error"], "only the feedback author")
	})

	t.Run("invalid validation_result", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: authored}, nil)
		res, _, _ := tk.handleRespondValidation(ownerCtx(), manageFeedbackInput{ThreadID: "t1", ValidationResult: "maybe"})
		assert.True(t, res.IsError)
		assert.Contains(t, decodeResult(t, res)["error"], "must be 'validated' or 'disputed'")
	})

	t.Run("missing thread_id", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, nil)
		res, _, _ := tk.handleRespondValidation(ownerCtx(), manageFeedbackInput{ValidationResult: "validated"})
		assert.True(t, res.IsError)
	})

	t.Run("thread not found", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getErr: errors.New("nope")}, nil)
		res, _, _ := tk.handleRespondValidation(ownerCtx(), manageFeedbackInput{ThreadID: "t1", ValidationResult: "validated"})
		assert.True(t, res.IsError)
	})

	t.Run("store error", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: authored, respondErr: errors.New("boom")}, nil)
		res, _, _ := tk.handleRespondValidation(ownerCtx(), manageFeedbackInput{ThreadID: "t1", ValidationResult: "validated"})
		assert.True(t, res.IsError)
	})

	t.Run("anonymous caller cannot respond", func(t *testing.T) {
		anonAuthored := &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1", AuthorID: "anonymous"}
		tk := threadToolkit(t, &fakeThreadStore{getResult: anonAuthored}, nil)
		res, _, _ := tk.handleRespondValidation(context.Background(), manageFeedbackInput{ThreadID: "t1", ValidationResult: "validated"})
		assert.True(t, res.IsError)
	})
}

// --- authorizing LinkInsight bridge (capture_insight gate) ------------------

func TestToolkitLinkInsightAuthorizes(t *testing.T) {
	thread := &portal.Thread{ID: "t1", TargetType: "asset", AssetID: "asset_1"}

	t.Run("owner links", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, nil)
		linked, err := tk.LinkInsight(ownerCtx(), []string{"t1"}, "ins_1", "u", "e")
		require.NoError(t, err)
		assert.Equal(t, []string{"t1"}, linked)
	})

	t.Run("stranger is not authorized; nothing linked", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, nil)
		linked, err := tk.LinkInsight(strangerCtx(), []string{"t1"}, "ins_1", "u", "e")
		require.NoError(t, err)
		assert.Empty(t, linked)
	})

	t.Run("editor share is authorized", func(t *testing.T) {
		shares := &fakeShareStore{assetShare: &portal.Share{Permission: portal.PermissionEditor}}
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, shares)
		linked, err := tk.LinkInsight(strangerCtx(), []string{"t1"}, "ins_1", "u", "e")
		require.NoError(t, err)
		assert.Equal(t, []string{"t1"}, linked)
	})

	t.Run("admin links any", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, nil)
		linked, err := tk.LinkInsight(adminCtx(), []string{"t1"}, "ins_1", "u", "e")
		require.NoError(t, err)
		assert.Equal(t, []string{"t1"}, linked)
	})

	t.Run("missing thread skipped", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getErr: errors.New("nope")}, nil)
		linked, err := tk.LinkInsight(ownerCtx(), []string{"t1"}, "ins_1", "u", "e")
		require.NoError(t, err)
		assert.Empty(t, linked)
	})

	t.Run("dedupes and drops empty ids", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, nil)
		linked, err := tk.LinkInsight(ownerCtx(), []string{"t1", "t1", ""}, "ins_1", "u", "e")
		require.NoError(t, err)
		assert.Equal(t, []string{"t1"}, linked)
	})

	t.Run("no threads or no insight is a noop", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, nil)
		linked, err := tk.LinkInsight(ownerCtx(), nil, "ins_1", "u", "e")
		require.NoError(t, err)
		assert.Nil(t, linked)
		linked, err = tk.LinkInsight(ownerCtx(), []string{"t1"}, "", "u", "e")
		require.NoError(t, err)
		assert.Nil(t, linked)
	})
}

// --- access model: collection + prompt + deleted asset ----------------------

func TestThreadAccessModel(t *testing.T) {
	t.Run("collection owner allowed", func(t *testing.T) {
		thread := &portal.Thread{ID: "t1", TargetType: "collection", CollectionID: "collection_1"}
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, nil)
		res, _, _ := tk.handleResolveThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.False(t, res.IsError)
	})

	t.Run("collection editor allowed", func(t *testing.T) {
		thread := &portal.Thread{ID: "t1", TargetType: "collection", CollectionID: "collection_1"}
		shares := &fakeShareStore{collPerm: portal.PermissionEditor}
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, shares)
		res, _, _ := tk.handleResolveThread(strangerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.False(t, res.IsError)
	})

	t.Run("collection stranger denied", func(t *testing.T) {
		thread := &portal.Thread{ID: "t1", TargetType: "collection", CollectionID: "collection_1"}
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, nil)
		res, _, _ := tk.handleResolveThread(strangerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.True(t, res.IsError)
	})

	t.Run("prompt target is admin only", func(t *testing.T) {
		thread := &portal.Thread{ID: "t1", TargetType: "prompt", PromptID: "p1"}
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread}, nil)
		// owner of an unrelated asset is not admin: denied.
		res, _, _ := tk.handleGetThread(ownerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.True(t, res.IsError)
		// admin: allowed.
		res, _, _ = tk.handleGetThread(adminCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.False(t, res.IsError)
	})

	t.Run("missing asset id denies edit", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, nil)
		assert.False(t, tk.canEditAsset(ownerCtx(), ""))
		assert.False(t, tk.canEditCollection(ownerCtx(), ""))
	})

	t.Run("deleted asset denies edit even for owner", func(t *testing.T) {
		assets := newInMemoryAssetStore()
		now := time.Now()
		require.NoError(t, assets.Insert(context.Background(),
			portal.Asset{ID: "asset_del", OwnerID: ownerID, DeletedAt: &now}))
		tk := New(Config{
			Name: "test", S3Bucket: "b",
			ThreadStore: &fakeThreadStore{}, AssetStore: assets, ShareStore: &fakeShareStore{},
		})
		assert.False(t, tk.canEditAsset(ownerCtx(), "asset_del"))
	})

	t.Run("unknown asset denies edit", func(t *testing.T) {
		tk := threadToolkit(t, &fakeThreadStore{}, nil)
		assert.False(t, tk.canEditAsset(ownerCtx(), "nope"))
		assert.False(t, tk.canEditCollection(ownerCtx(), "nope"))
	})

	t.Run("anonymous caller cannot moderate an anonymously-authored standalone thread", func(t *testing.T) {
		standalone := &portal.Thread{ID: "t1", TargetType: "standalone", AuthorID: "anonymous", AuthorEmail: "anonymous"}
		tk := threadToolkit(t, &fakeThreadStore{getResult: standalone}, nil)
		// context.Background() carries no PlatformContext, so resolveOwnerID is the
		// "anonymous" sentinel; the guard must fail closed despite the id matching.
		res, _, _ := tk.handleResolveThread(context.Background(), manageFeedbackInput{ThreadID: "t1"})
		assert.True(t, res.IsError)
	})

	t.Run("standalone non-moderate readable by anyone", func(t *testing.T) {
		thread := &portal.Thread{ID: "t1", TargetType: "standalone", AuthorID: ownerID}
		tk := threadToolkit(t, &fakeThreadStore{getResult: thread, events: []portal.ThreadEvent{}}, nil)
		res, _, _ := tk.handleGetThread(strangerCtx(), manageFeedbackInput{ThreadID: "t1"})
		assert.False(t, res.IsError)
	})
}
