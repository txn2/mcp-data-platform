package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceupload"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeLinks issues a fixed link and keeps what it was asked.
type fakeLinks struct {
	asked resourceupload.IssueRequest
	err   error
}

func (f *fakeLinks) Issue(_ context.Context, in resourceupload.IssueRequest, _ resource.Claims, _ string) (resourceupload.Issued, error) {
	f.asked = in
	return resourceupload.Issued{URL: "https://p.example.com/api/v1/resources/uploads/tok", Method: "PUT", MaxBytes: in.MaxBytes}, f.err
}

func linkInput() manageResourceInput {
	return manageResourceInput{
		Action: resourceActionUploadLink, Filename: "site.zip", Path: "sources", DisplayName: "Site source", Description: "The site's source tree",
		ContentType: "application/zip", SizeBytes: 200 << 20, TTLSeconds: 600,
	}
}

// TestUploadLink: the action checks the address as a create does and hands
// back the link with the curl that uses it; a script's run, a deployment with
// no issuer and a bad address are refused (#2639).
func TestUploadLink(t *testing.T) {
	tk, _ := resourceToolkit(t)
	res := callResource(t, tk, linkInput())
	require.True(t, res.IsError)
	assert.Contains(t, errText(t, res), "cannot issue upload links")

	links := &fakeLinks{}
	tk.SetUploadLinks(links)
	res = callResource(t, tk, linkInput())
	require.False(t, res.IsError, errText(t, res))
	var out map[string]any
	tc, _ := res.Content[0].(*mcp.TextContent)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	assert.Equal(t, "https://p.example.com/api/v1/resources/uploads/tok", out["upload_url"])
	assert.Contains(t, out["example"], "curl --fail -T 'site.zip'")
	assert.Equal(t, int64(200<<20), links.asked.MaxBytes)
	assert.Equal(t, 10*time.Minute, links.asked.TTL)
	assert.Equal(t, "sources", links.asked.Path)

	for label, in := range map[string]manageResourceInput{
		"no filename":     {Action: resourceActionUploadLink, ContentType: "application/zip", SizeBytes: 1},
		"no content type": {Action: resourceActionUploadLink, Filename: "a.zip", SizeBytes: 1},
		"bad if_exists":   {Action: resourceActionUploadLink, Filename: "a.zip", ContentType: "application/zip", SizeBytes: 1, IfExists: "merge"},
	} {
		assert.True(t, callResource(t, tk, in).IsError, label)
	}
	links.err = resourceupload.ErrRefused
	res = callResource(t, tk, linkInput())
	assert.True(t, res.IsError)

	script := mcpcontext.WithSource(refCtx(""), mcpcontext.SourceScript)
	res, _, err := tk.handleManageResource(script, nil, linkInput())
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, errText(t, res), "saved script's run cannot use an upload link")
	assert.Equal(t, "'it'\\''s.zip'", shellQuoted("it's.zip"))
	mux := http.NewServeMux()
	tk.MountUploadLinks(mux) // an issuer with no route of its own mounts nothing
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/resources/uploads/x", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
