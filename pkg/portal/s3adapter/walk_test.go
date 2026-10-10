package s3adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// pagedS3API serves a listing in pages chained by continuation token.
type pagedS3API struct {
	fakeS3API
	pages  []*s3client.ListObjectsOutput
	tokens []string
	delims []string
}

//nolint:revive // argument-limit: the signature mirrors the mcp-s3 client's
func (p *pagedS3API) ListObjects(
	_ context.Context, _, _, delimiter string, _ int32, token string,
) (*s3client.ListObjectsOutput, error) {
	p.tokens = append(p.tokens, token)
	p.delims = append(p.delims, delimiter)
	if p.listErr != nil {
		return nil, p.listErr
	}
	page := p.pages[len(p.tokens)-1]
	return page, nil
}

func TestWalk_FollowsEveryPage(t *testing.T) {
	when := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	api := &pagedS3API{pages: []*s3client.ListObjectsOutput{
		{Objects: []s3client.ObjectInfo{{Key: "a", Size: 1, LastModified: when}}, IsTruncated: true, NextContinueToken: "t1"},
		{Objects: []s3client.ObjectInfo{{Key: "b/c", Size: 2}}, IsTruncated: false},
	}}
	a := &ClientAdapter{client: api, purpose: "resources"}
	var got []WalkedObject
	require.NoError(t, a.Walk(context.Background(), "bucket", "", func(o WalkedObject) error {
		got = append(got, o)
		return nil
	}))
	assert.Equal(t, []WalkedObject{{Key: "a", Size: 1, LastModified: when}, {Key: "b/c", Size: 2}}, got)
	assert.Equal(t, []string{"", "t1"}, api.tokens)
	assert.Equal(t, []string{"", ""}, api.delims, "a walk descends into every directory")
}

func TestWalk_StopsAtTheFirstError(t *testing.T) {
	api := &pagedS3API{pages: []*s3client.ListObjectsOutput{
		{Objects: []s3client.ObjectInfo{{Key: "a"}, {Key: "b"}}, IsTruncated: true, NextContinueToken: "t1"},
	}}
	a := &ClientAdapter{client: api}
	stop := errors.New("stop")
	calls := 0
	err := a.Walk(context.Background(), "bucket", "", func(WalkedObject) error { calls++; return stop })
	require.ErrorIs(t, err, stop)
	assert.Equal(t, 1, calls)

	api = &pagedS3API{}
	api.listErr = errors.New("denied")
	a = &ClientAdapter{client: api}
	require.ErrorContains(t, a.Walk(context.Background(), "bucket", "", func(WalkedObject) error { return nil }), "s3 walk")
}

// A truncated page with no token ends the walk instead of asking for the
// first page again.
func TestWalk_TruncatedWithoutToken(t *testing.T) {
	api := &pagedS3API{pages: []*s3client.ListObjectsOutput{{IsTruncated: true}}}
	a := &ClientAdapter{client: api}
	require.NoError(t, a.Walk(context.Background(), "bucket", "", func(WalkedObject) error { return nil }))
	assert.Len(t, api.tokens, 1)
}
