package portal

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcedownload"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeDownloads mints a fixed link and keeps what it was asked.
type fakeDownloads struct {
	res     *resource.Resource
	expires string
	err     error
}

func (f *fakeDownloads) Mint(res *resource.Resource, expires string) (resourcedownload.Link, error) {
	f.res, f.expires = res, expires
	if f.err != nil {
		return resourcedownload.Link{}, f.err
	}
	return resourcedownload.Link{URL: "https://p.example.com/api/v1/resources/downloads/tok/" + res.Filename, Expires: expires}, nil
}

// TestDownloadLink: the action resolves the file by reference or by address as
// the caller may read it, and hands back the link with the file's name, type
// and size; a deployment with no minter, a file that is not there and a
// refused lifetime are answered as errors (#2847).
func TestDownloadLink(t *testing.T) {
	tk, w := resourceToolkit(t)
	ref := manageResourceInput{Action: resourceActionDownloadLink, Reference: "mcp:resource:res_1", Expires: "6h"}
	res := callResource(t, tk, ref)
	require.True(t, res.IsError)
	assert.Contains(t, errText(t, res), "portal.public_base_url")

	d := &fakeDownloads{}
	tk.SetDownloadLinks(d)
	w.existing = &resource.Resource{ID: "res_1", Filename: "invoice.pdf", MIMEType: "application/pdf", SizeBytes: 9}
	res = callResource(t, tk, ref)
	require.False(t, res.IsError, errText(t, res))
	var out map[string]any
	tc, _ := res.Content[0].(*mcp.TextContent)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	assert.Equal(t, "https://p.example.com/api/v1/resources/downloads/tok/invoice.pdf", out["url"])
	assert.Equal(t, "mcp:resource:res_1", out["reference"])
	assert.Equal(t, "application/pdf", out["content_type"])
	assert.Equal(t, "res_1", w.gotID)
	assert.Equal(t, resourcedownload.SixHours, d.expires)
	assert.Contains(t, out["message"], "until expires_at.")
	assert.Contains(t, downloadMessage(resourcedownload.SingleDownload), "first full download")

	w.located = &resource.Resource{ID: "res_2", Filename: "a.pdf"}
	res = callResource(t, tk, manageResourceInput{Action: resourceActionDownloadLink, Path: "docs", Filename: "a.pdf"})
	require.False(t, res.IsError, errText(t, res))
	assert.Equal(t, "res_2", d.res.ID)
	assert.Equal(t, "docs", w.locatedAddr.Path)

	w.located = nil
	res = callResource(t, tk, manageResourceInput{Action: resourceActionDownloadLink, Path: "docs", Filename: "none.pdf"})
	require.True(t, res.IsError)
	assert.Contains(t, errText(t, res), "Nothing you can read is filed at")

	w.getErr = resource.ErrNoSuchResource
	res = callResource(t, tk, ref)
	require.True(t, res.IsError)
	assert.Contains(t, errText(t, res), "Nothing you can read is filed under mcp:resource:res_1")
	w.getErr = errors.New("store down")
	assert.Contains(t, errText(t, callResource(t, tk, ref)), "store down")
	w.getErr = nil

	for label, in := range map[string]manageResourceInput{
		"bad reference": {Action: resourceActionDownloadLink, Reference: "mcp:asset:a1"},
		"no file named": {Action: resourceActionDownloadLink},
	} {
		assert.True(t, callResource(t, tk, in).IsError, label)
	}
	w.locateErr = errors.New("locate failed")
	assert.True(t, callResource(t, tk, manageResourceInput{Action: resourceActionDownloadLink, Path: "p", Filename: "f"}).IsError)
	w.locateErr = nil

	d.err = resourcedownload.ErrBadExpiry
	res = callResource(t, tk, ref)
	require.True(t, res.IsError)
	assert.Contains(t, errText(t, res), "single_download")
}
