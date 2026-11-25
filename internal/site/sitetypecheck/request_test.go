package sitetypecheck

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// fakeReleases serves one release's source and refuses publishing to anyone
// but deny == false.
type fakeReleases struct {
	src     fstest.MapFS
	readErr error
	deny    error
	readAs  []sitesvc.Actor
}

func (f *fakeReleases) ReadBuild(_ context.Context, actor sitesvc.Actor, _, _ string, number int) (*sitesvc.ReleaseBuild, error) {
	f.readAs = append(f.readAs, actor)
	if f.readErr != nil {
		return nil, f.readErr
	}
	return &sitesvc.ReleaseBuild{Release: &site.Release{SiteID: "site_1", Number: max(number, 4)}, Source: f.src}, nil
}

func (f *fakeReleases) MayPublish(context.Context, sitesvc.Actor, string, string) error {
	return f.deny
}

type fakeEnqueuer struct {
	queued []string
	err    error
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, r Request) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.queued = append(f.queued, fmt.Sprintf("%s#%d@%s:%s by %s", r.SiteID, r.Release, r.Environment, r.Tsconfig, r.By))
	return int64(len(f.queued)), nil
}

func asking(build string) fstest.MapFS {
	return fstest.MapFS{sitebuild.ControlFile: {Data: []byte(build)}}
}

func TestAsked(t *testing.T) {
	assert.Nil(t, Asked(fstest.MapFS{}), "no build file")
	assert.Nil(t, Asked(asking(`not json`)), "an unreadable build file")
	assert.Nil(t, Asked(asking(`{"entries":["a.ts"]}`)), "a build file asking for none")
	tc := Asked(asking(`{"entries":["a.ts"],"typecheck":{"tsconfig":"tsconfig.app.json"}}`))
	require.NotNil(t, tc)
	assert.Equal(t, "tsconfig.app.json", tc.TsconfigPath())
}

// TestAfterPublish: a published release asking for a check is queued under
// the environment and publisher; one asking for none, or whose source cannot
// be read, is not; a failed enqueue never fails the publish.
func TestAfterPublish(t *testing.T) {
	rel := &site.Release{SiteID: "site_1", Number: 3}
	q := &fakeEnqueuer{}
	r := &fakeReleases{src: asking(`{"entries":["a.ts"],"typecheck":{}}`)}
	AfterPublish(q, r)(context.Background(), rel, "dev", "pat@example.com")
	assert.Equal(t, []string{"site_1#3@dev:tsconfig.json by pat@example.com"}, q.queued)
	assert.True(t, r.readAs[0].Admin, "the platform reads the source of a check the site asked for")

	AfterPublish(q, &fakeReleases{src: asking(`{"entries":["a.ts"]}`)})(context.Background(), rel, "dev", "pat@example.com")
	AfterPublish(q, &fakeReleases{readErr: errors.New("gone")})(context.Background(), rel, "dev", "pat@example.com")
	assert.Len(t, q.queued, 1)

	AfterPublish(&fakeEnqueuer{err: errors.New("down")}, r)(context.Background(), rel, "dev", "pat@example.com")
}

// TestRecheck: someone who may publish queues the release again; a release
// asking for none is refused naming the fix; a refusal to publish and a
// failed read or enqueue are the caller's answer.
func TestRecheck(t *testing.T) {
	ctx := context.Background()
	who := sitesvc.Actor{Email: "pat@example.com"}
	q := &fakeEnqueuer{}
	n, queued, err := Recheck(ctx, q, &fakeReleases{src: asking(`{"entries":["a.ts"],"typecheck":{}}`)}, RecheckRequest{Actor: who, Site: "docs", Environment: "", Release: 0})
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, &Queued{ID: 1, Status: "queued"}, queued)
	assert.Equal(t, "site_1#4@dev:tsconfig.json by pat@example.com", q.queued[0])

	_, _, err = Recheck(ctx, q, &fakeReleases{src: asking(`{"entries":["a.ts"]}`)}, RecheckRequest{Actor: who, Site: "docs", Environment: "dev", Release: 2})
	var refused *sitesvc.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Msg, `add "typecheck"`)

	denied := errors.New("not yours")
	_, _, err = Recheck(ctx, q, &fakeReleases{deny: denied}, RecheckRequest{Actor: who, Site: "docs", Environment: "dev", Release: 2})
	require.ErrorIs(t, err, denied)
	_, _, err = Recheck(ctx, q, &fakeReleases{readErr: denied}, RecheckRequest{Actor: who, Site: "docs", Environment: "dev", Release: 2})
	require.ErrorIs(t, err, denied)
	_, _, err = Recheck(ctx, &fakeEnqueuer{err: denied}, &fakeReleases{src: asking(`{"entries":["a.ts"],"typecheck":{}}`)}, RecheckRequest{Actor: who, Site: "docs", Environment: "dev", Release: 2})
	require.ErrorIs(t, err, denied)
}

func TestReleaseSource(t *testing.T) {
	src, err := ReleaseSource(&fakeReleases{src: asking(`{}`)})(context.Background(), "site_1", 2)
	require.NoError(t, err)
	assert.NotNil(t, src)
	_, err = ReleaseSource(&fakeReleases{readErr: errors.New("gone")})(context.Background(), "site_1", 2)
	require.Error(t, err)
}
