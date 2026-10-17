package storagewire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func TestBuildNeedsADatabase(t *testing.T) {
	assert.Nil(t, Build(nil, nil))
	Mount(http.NewServeMux(), nil, nil, nil) // a nil service mounts nothing
}

func TestAssemble(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	t.Setenv("MCP_PLATFORM_STORAGE_BUDGETS", "portal=1GiB")
	assert.NotNil(t, assemble(db, nil, "https://platform.example.com/", nil))
	assert.NotNil(t, assemble(db, nil, "", nil))
	t.Setenv("MCP_PLATFORM_STORAGE_BUDGETS", "portal=lots")
	assert.NotNil(t, assemble(db, nil, "", nil), "a malformed budget is logged and ignored")
}

func TestInterval(t *testing.T) {
	t.Setenv(EnvInterval, "")
	assert.Zero(t, interval())
	t.Setenv(EnvInterval, "5s")
	assert.Equal(t, 5*time.Second, interval())
	t.Setenv(EnvInterval, "soon")
	assert.Zero(t, interval())
	t.Setenv(EnvInterval, "-1s")
	assert.Zero(t, interval())
}

func TestAlertSenderIsNeverATypedNil(t *testing.T) {
	assert.Nil(t, AlertSender(nil), "no notification substrate is no sender")
}

// TestOnSeaweedFS asks each bucket's endpoint which store it is once, and
// names a bucket it knows no endpoint for as not on the cluster.
func TestOnSeaweedFS(t *testing.T) {
	asked := 0
	server := "SeaweedFS 30GB 3.88"
	seaweed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.Header().Set("Server", server)
	}))
	defer seaweed.Close()
	clock := time.Unix(0, 0)
	on := onSeaweedFS(map[string]string{"portal": seaweed.URL, "assets": seaweed.URL, "cloud": ""}, func() time.Time { return clock })
	ctx := context.Background()
	assert.True(t, on(ctx, "portal"))
	assert.True(t, on(ctx, "assets"))
	assert.Equal(t, 1, asked, "an endpoint two buckets share is asked once")
	assert.False(t, on(ctx, "cloud"), "no endpoint is AWS S3")
	assert.False(t, on(ctx, "unknown"))

	// A store that answered as something else, say while it was starting,
	// is asked again once redetect has passed.
	server = "starting"
	late := onSeaweedFS(map[string]string{"portal": seaweed.URL}, func() time.Time { return clock })
	assert.False(t, late(ctx, "portal"))
	server = "SeaweedFS 30GB 3.88"
	assert.False(t, late(ctx, "portal"), "not asked again inside the window")
	clock = clock.Add(redetect)
	assert.True(t, late(ctx, "portal"))
}

func TestBucketEndpoints(t *testing.T) {
	cfg := &platform.Config{Toolkits: map[string]any{"s3": map[string]any{
		"default":   "store",
		"instances": map[string]any{"store": map[string]any{"endpoint": "http://localhost:9000"}},
	}}}
	cfg.Portal.S3Bucket = "portal-assets"
	cfg.Resources.Managed.S3Bucket = ""
	got := bucketEndpoints(cfg)
	assert.Equal(t, map[string]string{"portal-assets": "http://localhost:9000"}, got)
}
