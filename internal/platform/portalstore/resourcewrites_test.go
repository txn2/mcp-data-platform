package portalstore

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// stubStore satisfies resource.Store for assembly alone: nothing here reaches
// a store method.
type stubStore struct{ resource.Store }

// stubBlobs is a blob client; rangedBlobs is one that also reads by range.
type stubBlobs struct{ resource.S3Client }

type rangedBlobs struct{ stubBlobs }

func (rangedBlobs) GetObjectRange(context.Context, string, string, int64, int64) (body []byte, size int64, err error) {
	return nil, 0, errors.New("not read in this test")
}

// extractAnswer calls manage_resource extract with a reference that is not one
// through a real MCP session. Its refusal says whether an extractor is bound:
// an unbound deployment answers that it cannot extract before it reads the
// arguments.
func extractAnswer(t *testing.T, h *Handle) string {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "platform", Version: "v0"}, nil)
	h.Toolkit().RegisterTools(server)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(middleware.WithPlatformContext(ctx, &middleware.PlatformContext{
				UserID: "user1", UserEmail: "user1@example.com",
			}), method, req)
		}
	})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "v0"}, nil).Connect(t.Context(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: portalkit.ManageResourceToolName, Arguments: map[string]any{
		"action": "extract", "reference": "not-a-reference", "path": "staging",
	}})
	require.NoError(t, err)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

func TestBindResourceWritesBindsTheExtractor(t *testing.T) {
	h := NewFromStores(Stores{}, nil, Config{Name: "portal"})
	h.BindResourceWrites(ResourceWriteDeps{Store: stubStore{}, Blobs: rangedBlobs{}, Bucket: "b"})
	assert.Contains(t, extractAnswer(t, h), "not a reference this platform issues",
		"with a writer and a range reader, extract reaches its own argument checks")
	assert.NotNil(t, h.ResourceLanding(), "the export lander is bound beside it")
}

func TestBindResourceWritesWithoutRangeReads(t *testing.T) {
	h := NewFromStores(Stores{}, nil, Config{Name: "portal"})
	h.BindResourceWrites(ResourceWriteDeps{Store: stubStore{}, Blobs: stubBlobs{}, Bucket: "b"})
	assert.Contains(t, extractAnswer(t, h), "cannot extract archives")
}

func TestBindResourceWritesWithoutBlobs(t *testing.T) {
	h := NewFromStores(Stores{}, nil, Config{Name: "portal"})
	h.BindResourceWrites(ResourceWriteDeps{Store: stubStore{}})
	assert.Contains(t, extractAnswer(t, h), "no managed-resource library")
	var missing *Handle
	assert.NotPanics(t, func() { missing.BindResourceWrites(ResourceWriteDeps{Store: stubStore{}, Blobs: rangedBlobs{}}) })
}

// fakeExtraction records what the adapter handed the writer and answers the
// result the test set.
type fakeExtraction struct {
	got resourcewrite.Extraction
	out *resourcewrite.Extracted
	err error
}

func (f *fakeExtraction) ExtractArchive(
	_ context.Context, req resourcewrite.Extraction, _ resource.Claims,
) (*resourcewrite.Extracted, error) {
	f.got = req
	return f.out, f.err
}

// TestExtractorAdapterCarriesEveryField holds the two copies of the fields to
// each other, including a partial result returned beside its error.
func TestExtractorAdapterCarriesEveryField(t *testing.T) {
	stop := errors.New("member b.csv is corrupt")
	f := &fakeExtraction{
		out: &resourcewrite.Extracted{Format: "zip", Skipped: []string{"link"}, Members: []resourcewrite.ExtractedMember{{
			Member: "a.csv", Landing: toolkit.ResourceLanding{Reference: "mcp:resource:r1", Version: 2},
		}}},
		err: stop,
	}
	req := portalkit.ArchiveExtraction{
		ArchiveID: "arch", Scope: "persona", ScopeID: "analyst", Path: "staging", Members: "*.csv",
		Filename: "d.csv", Replace: true, Description: "feed", Tags: []string{"t"}, ChangeSummary: "oct",
	}
	out, err := extractorAdapter{x: f}.ExtractArchive(context.Background(), req, resource.Claims{})
	require.ErrorIs(t, err, stop)
	assert.Equal(t, resourcewrite.Extraction{
		ArchiveID: "arch", Scope: "persona", ScopeID: "analyst", Path: "staging", Members: "*.csv",
		Filename: "d.csv", Replace: true, Description: "feed", Tags: []string{"t"}, ChangeSummary: "oct",
	}, f.got)
	require.NotNil(t, out)
	assert.Equal(t, "zip", out.Format)
	assert.Equal(t, []string{"link"}, out.Skipped)
	require.Len(t, out.Members, 1)
	assert.Equal(t, "a.csv", out.Members[0].Member)
	assert.Equal(t, "mcp:resource:r1", out.Members[0].Reference)
	assert.Equal(t, 2, out.Members[0].Version)

	f.out, f.err = nil, errors.New("refused")
	out, err = extractorAdapter{x: f}.ExtractArchive(context.Background(), req, resource.Claims{})
	assert.Nil(t, out)
	assert.EqualError(t, err, "refused")
}
