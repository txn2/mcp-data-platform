package sitesvc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// TestPublish_TheApplicationFileIsTheReleasesContract (#2679): an
// _application file becomes the release's contract, reads back as itself, is
// listed with the files, survives an overlay that does not send it, goes when
// deleted, and one that does not parse refuses the publish.
func TestPublish_TheApplicationFileIsTheReleasesContract(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := seeded(t)
	_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 1, Files: []InputFile{
		{Path: "_application", Body: []byte(`{"application": "store", "contract": ">=2 <4"}`)},
	}})
	require.NoError(t, err)
	newest := func() *site.Release { return &store.releases[siteID][0] }
	assert.Equal(t, &site.Contract{Application: "store", Range: ">=2 <4"}, newest().Contract)

	ref := site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "_application"}
	read, err := svc.ReadFile(ctx, owner, ref)
	require.NoError(t, err)
	assert.Contains(t, read.Body, `"contract": ">=2 <4"`)
	all, err := svc.Tree(ctx, owner, treeRef, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, "_application", all.Files[0].Path, "listed with the files")

	_, err = svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: newest().Number, Files: []InputFile{page("more.html")}})
	require.NoError(t, err)
	assert.NotNil(t, newest().Contract, "an overlay keeps the base's contract")

	_, err = svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: newest().Number, Remove: []string{"_application"}})
	require.NoError(t, err)
	assert.Nil(t, newest().Contract, "deleting the file declares nothing")

	_, err = svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: newest().Number, Files: []InputFile{
		{Path: "_application", Body: []byte(`{"application": "store", "contract": "soon"}`)},
	}})
	require.ErrorContains(t, err, "contract range")
}

// TestPromote_AHookRefusalIsARefusal (#2679): a promotion CheckPromote
// refuses is refused in its words, and moves nothing.
func TestPromote_AHookRefusalIsARefusal(t *testing.T) {
	svc, store, _, _ := fixture(t)
	released(store, 1, site.EnvDev)
	svc.CheckPromote = func(_ context.Context, _ *site.Site, env string, _ *site.Release) error {
		if env == "stage" {
			return errors.New("works with store's contract =2, and store is at contract 3")
		}
		return nil
	}
	_, err := svc.Promote(context.Background(), owner, PromoteRequest{Site: "marketing", To: "stage"})
	var r *RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Error(), "store is at contract 3")
	assert.Zero(t, store.sites[siteID].Environment("stage").Release)
}
