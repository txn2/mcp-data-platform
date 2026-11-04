package portaldomain

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type versionsFake map[int]*AssetVersion

func (f versionsFake) GetByVersion(_ context.Context, _ string, v int) (*AssetVersion, error) {
	if v == 99 {
		return nil, errors.New("boom")
	}
	return f[v], nil
}

func TestServed(t *testing.T) {
	ctx := context.Background()
	versions := versionsFake{1: {Version: 1, S3Key: "k1", S3Bucket: "old", ContentType: "text/html", SizeBytes: 3}}
	newest := &Asset{ID: "a", S3Key: "k2", S3Bucket: "b", ContentType: "text/markdown", SizeBytes: 9, CurrentVersion: 2}

	got, err := Served(ctx, versions, newest)
	require.NoError(t, err)
	assert.Same(t, newest, got, "an asset in no released application is served as it is")
	assert.Equal(t, 2, newest.ServedVersion())

	held := *newest
	held.ReleasedVersion = 1
	assert.Equal(t, 1, held.ServedVersion())
	got, err = Served(ctx, versions, &held)
	require.NoError(t, err)
	assert.Equal(t, "k1", got.S3Key)
	assert.Equal(t, "old", got.S3Bucket)
	assert.Equal(t, "text/html", got.ContentType)
	assert.Equal(t, int64(3), got.SizeBytes)
	assert.Equal(t, 1, got.CurrentVersion)
	assert.Equal(t, "k2", held.S3Key, "the asset read is not changed in place")

	same := *newest
	same.ReleasedVersion = 2
	got, err = Served(ctx, versions, &same)
	require.NoError(t, err)
	assert.Same(t, &same, got)

	for name, v := range map[string]int{"missing": 7, "unreadable": 99} {
		bad := *newest
		bad.ReleasedVersion = v
		_, err = Served(ctx, versions, &bad)
		assert.Error(t, err, name)
	}
	_, err = Served(ctx, nil, &held)
	require.Error(t, err, "a held version with no store to read it is never answered with the newest")
	got, err = Served(ctx, versions, nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}
