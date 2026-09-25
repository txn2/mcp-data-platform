package portal

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/unarchive"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// GetObjectRange serves part of a stored object, as the managed-resource blob
// client does, so the archive is opened where it is stored.
func (s *sharedS3) GetObjectRange(
	_ context.Context, bucket, key string, offset, length int64,
) (body []byte, size int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[bucket+"/"+key]
	if !ok {
		return nil, 0, errors.New("NoSuchKey")
	}
	end := min(offset+length, int64(len(data)))
	return append([]byte(nil), data[offset:end]...), int64(len(data)), nil
}

// extractSystem is the real writer, lander and extractor over one set of rows
// and one blob backend, behind the real toolkit reached through a real MCP
// session: the whole path a script's platform.call takes, short of the
// platform's own middleware chain.
type extractSystem struct {
	*writeSystem
	followed []int
}

func newExtractSystem(t *testing.T) *extractSystem {
	t.Helper()
	rows := newResourceRows()
	blobs := newSharedS3()
	sys := &extractSystem{writeSystem: &writeSystem{rows: rows, blobs: blobs}}
	writer := resourcewrite.New(resourcewrite.Deps{Store: rows, Blobs: blobs, Bucket: intResBucket, URIScheme: "mcp"})
	lander := resourcewrite.NewLander(resourcewrite.LanderDeps{Writer: writer, MaxUploadBytes: 1 << 10})
	lander.SetTableFollower(func(_ context.Context, _ string, version int) []string {
		sys.followed = append(sys.followed, version)
		return []string{"Table scratch.feed followed onto the new version."}
	})
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Client: blobs, S3Bucket: intAssetBucket, MaxContentSize: 1 << 20})
	tk.SetResourceWriter(writer)
	tk.SetResourceExtractor(testExtractor{x: resourcewrite.NewExtractor(resourcewrite.ExtractorDeps{
		Lander: lander, Ranges: blobs, Limits: unarchive.Limits{},
	})})
	server := mcp.NewServer(&mcp.Implementation{Name: "platform", Version: "v0"}, nil)
	tk.RegisterTools(server)
	server.AddReceivingMiddleware(agentIdentityMiddleware)
	sys.session = connectWriteAgent(t, server)
	return sys
}

// testExtractor carries the call across to the real extractor. The platform's
// own copy is portalstore's adapter, which this package cannot import; it is
// held to the same field mapping by its own test.
type testExtractor struct{ x *resourcewrite.Extractor }

func (e testExtractor) ExtractArchive(
	ctx context.Context, req ArchiveExtraction, claims resource.Claims,
) (*ExtractedArchive, error) {
	out, err := e.x.ExtractArchive(ctx, resourcewrite.Extraction{
		ArchiveID: req.ArchiveID, Scope: req.Scope, ScopeID: req.ScopeID, Path: req.Path, Members: req.Members,
		Filename: req.Filename, Replace: req.Replace, Description: req.Description, Tags: req.Tags,
		ChangeSummary: req.ChangeSummary,
	}, claims)
	if out == nil {
		return nil, err //nolint:wrapcheck // the extractor's own sentence, as the platform's adapter passes it
	}
	result := &ExtractedArchive{Format: out.Format, Skipped: out.Skipped}
	for _, m := range out.Members {
		result.Members = append(result.Members, ExtractedMember{Member: m.Member, ResourceLanding: m.Landing})
	}
	return result, err //nolint:wrapcheck // as above
}

func zipArchive(t *testing.T, name, body string) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	require.NoError(t, err)
	_, err = f.Write([]byte(body))
	require.NoError(t, err)
	readme, err := w.Create("README.txt")
	require.NoError(t, err)
	_, err = readme.Write([]byte("not data"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// TestAnArchiveDeliveryBecomesOneRollingFile is #1879's path end to end: an
// archive is stored, its CSV extracted to a stable address, and the next
// month's archive extracted over it records the next version of that same file
// with the tables over it moved -- a file larger than the upload ceiling the
// lander was given, because extraction is bounded by its own limits.
func TestAnArchiveDeliveryBecomesOneRollingFile(t *testing.T) {
	sys := newExtractSystem(t)
	september := bytes.Repeat([]byte("month,value\n9,1\n"), 200) // past the 1 KiB upload ceiling

	extract := func(archiveBody, filename string) map[string]any {
		stored := sys.mustCall(t, ManageResourceToolName, withAction("create", map[string]any{
			"path": "pipelines/raw", "filename": filename, "display_name": "Delivery",
			"description": "Monthly delivery", "content_base64": archiveBody,
			"content_type": "application/zip",
		}))
		return sys.mustCall(t, ManageResourceToolName, withAction("extract", map[string]any{
			"reference": stored["reference"], "path": "pipelines/staging", "members": "*.csv",
			"filename": "delivery.csv", "if_exists": "replace",
		}))
	}

	first := extract(zipArchive(t, "export_2026_09.csv", string(september)), "delivery-2026-09.zip")
	members, _ := first["members"].([]any)
	require.Len(t, members, 1, "the README is not selected by *.csv")
	m, _ := members[0].(map[string]any)
	assert.Equal(t, "export_2026_09.csv", m["member"])
	assert.Equal(t, "mcp://user/user1/pipelines/staging/delivery.csv", m["uri"])
	assert.Equal(t, "text/csv", m["content_type"])
	assert.Equal(t, float64(len(september)), m["size_bytes"])
	assert.Equal(t, true, m["created"])
	resourceID, _ := m["resource_id"].(string)

	second := extract(zipArchive(t, "export_2026_10.csv", "month,value\n10,2\n"), "delivery-2026-10.zip")
	members, _ = second["members"].([]any)
	require.Len(t, members, 1)
	m, _ = members[0].(map[string]any)
	assert.Equal(t, resourceID, m["resource_id"], "the same file, recorded as its next version")
	assert.Equal(t, float64(2), m["version"])
	assert.Equal(t, []int{2}, sys.followed, "the table over the file moved to the new version")
	assert.Contains(t, second["message"], "Table scratch.feed followed onto the new version.")

	res, err := sys.rows.Get(context.Background(), resourceID)
	require.NoError(t, err)
	body, _, err := sys.blobs.GetObject(context.Background(), intResBucket, res.S3Key)
	require.NoError(t, err)
	assert.Equal(t, "month,value\n10,2\n", string(body))
	assert.Equal(t, resource.ScopeUser, res.Scope)
}

func TestAnExtractionRefusedOverTheWire(t *testing.T) {
	sys := newExtractSystem(t)
	stored := sys.mustCall(t, ManageResourceToolName, withAction("create", map[string]any{
		"path": "pipelines/raw", "filename": "evil.zip", "display_name": "Evil",
		"description": "zip-slip", "content_base64": zipArchive(t, "../../etc/cron.csv", "x"),
		"content_type": "application/zip",
	}))
	out, isErr := sys.call(t, ManageResourceToolName, withAction("extract", map[string]any{
		"reference": stored["reference"], "path": "pipelines/staging",
	}))
	require.True(t, isErr)
	assert.Contains(t, out["error"], "climbs out of its folder")
	assert.Contains(t, out["error"], "Nothing was written.")
	assert.Len(t, sys.rows.rows, 1, "only the archive itself is stored")
}
