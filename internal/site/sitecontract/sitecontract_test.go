package sitecontract

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

func TestCheck(t *testing.T) {
	ctx := context.Background()
	parts := func(_ context.Context, _, env string) (string, int, bool) { return "store", 3, env == "prod" }
	rel := &site.Release{Number: 4, Contract: &site.Contract{Application: "store", Range: "=2"}}
	err := Check(ctx, parts, "web", "prod", rel)
	require.ErrorContains(t, err, "works with store's contract =2, and store is at contract 3")
	require.NoError(t, Check(ctx, parts, "web", "stage", rel), "an environment that is a part of nothing")
	require.NoError(t, Check(ctx, parts, "web", "prod", &site.Release{Contract: &site.Contract{Application: "store", Range: ">=2"}}))
	require.NoError(t, Check(ctx, parts, "web", "prod", &site.Release{Contract: &site.Contract{Application: "other", Range: "=9"}}))
	require.NoError(t, Check(ctx, parts, "web", "prod", &site.Release{}), "a release declaring nothing")
	require.NoError(t, Check(ctx, nil, "web", "prod", rel))
}

// store is the part of site.Store the reader reads.
type store struct {
	site.Store
	releases map[int]*site.Release
}

func (s store) Release(_ context.Context, _ string, n int) (*site.Release, error) {
	if r, ok := s.releases[n]; ok {
		return r, nil
	}
	return nil, site.ErrNoRelease
}

func TestReader(t *testing.T) {
	ctx := context.Background()
	st := &site.Site{ID: "s1", Name: "web", Environments: []site.Environment{{Name: "dev", Release: 2}, {Name: "prod"}}}
	r := Reader{
		Store: store{releases: map[int]*site.Release{2: {Number: 2, Contract: &site.Contract{Application: "store", Range: ">=2"}}}},
		Find: func(_ context.Context, ref string) (*site.Site, error) {
			if ref != "web" {
				return nil, errors.New("no such site")
			}
			return st, nil
		},
	}
	n, c, err := r.ServedContract(ctx, "web", "dev")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, ">=2", c.Range)
	n, c, err = r.ServedContract(ctx, "web", "prod")
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Nil(t, c)
	_, _, err = r.ServedContract(ctx, "web", "nowhere")
	require.Error(t, err)
	_, _, err = r.ServedContract(ctx, "gone", "dev")
	require.Error(t, err)
	_, err = r.ReleaseContract(ctx, "web", 9)
	require.Error(t, err)
	_, err = r.ReleaseContract(ctx, "gone", 2)
	require.Error(t, err)
}
