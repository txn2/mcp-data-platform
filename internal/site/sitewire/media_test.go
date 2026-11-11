package sitewire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// mediaLibrary is a resource store holding one mounted file.
type mediaLibrary struct {
	resource.Store
	files []resource.Resource
}

func (l mediaLibrary) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	for i := range l.files {
		if l.files[i].URI == uri {
			return &l.files[i], nil
		}
	}
	return nil, fmt.Errorf("scanning resource: %w", sql.ErrNoRows)
}

func (l mediaLibrary) List(_ context.Context, f resource.Filter) ([]resource.Resource, int, error) {
	var out []resource.Resource
	for _, r := range l.files {
		if r.Path == f.Path {
			out = append(out, r)
		}
	}
	return out, len(out), nil
}

// rangeBlobs reads by range, as the platform's object client does.
type rangeBlobs struct {
	resource.S3Client
	body []byte
}

func (b rangeBlobs) GetObjectRange(_ context.Context, _, _ string, off, length int64) (body []byte, size int64, err error) {
	end := min(off+length, int64(len(b.body)))
	return b.body[off:end], int64(len(b.body)), nil
}

func mediaReader() *scriptsrc.Reader {
	lib := mediaLibrary{files: []resource.Resource{{
		ID: "res_1", Scope: resource.ScopeGlobal, Path: "sites/m", Filename: "logo.png", MIMEType: "image/png",
		URI: "mcp://global/sites/m/logo.png", S3Key: "resources/global/res_1/logo.png", SizeBytes: 4, UpdatedAt: time.Unix(1700000000, 0),
	}}}
	return scriptsrc.New(lib, rangeBlobs{body: []byte("PNG!")}, "resources", "")
}

func TestMediaSource_FindsTheFileAMountedPathNames(t *testing.T) {
	ctx := context.Background()
	rt, mock := newDB(t)
	src := mediaSource{store: rt.Store, reader: mediaReader()}
	mount := site.Mount{Prefix: "/media/", Scope: "global", Folder: "sites/m"}

	f, err := src.Find(ctx, mount, "logo.png")
	require.NoError(t, err)
	require.NotNil(t, f)
	assert.Equal(t, "image/png", f.ContentType)
	assert.Equal(t, blobserve.Object{Store: rangeBlobs{body: []byte("PNG!")}, Bucket: "resources", Key: "resources/global/res_1/logo.png", Size: 4}, f.Object,
		"read from the library's bucket by the key its record holds")

	none, err := src.Find(ctx, mount, "missing.png")
	require.NoError(t, err)
	assert.Nil(t, none)

	mock.ExpectQuery(regexp.QuoteMeta("FROM site_media WHERE site_id = $1")).WithArgs("site_1").
		WillReturnRows(sqlmock.NewRows([]string{"prefix", "scope", "scope_id", "folder", "created_by", "created_at"}).AddRow("/media/", "global", "", "sites/m", "a", time.Now()))
	mounts, err := src.Mounts(ctx, "site_1")
	require.NoError(t, err)
	require.Len(t, mounts, 1)
}

func TestMediaFolders_ChecksTheCaller(t *testing.T) {
	folders := mediaFolders{reader: mediaReader()}
	pc := middleware.NewPlatformContext("")
	pc.UserID, pc.UserEmail, pc.PersonaName, pc.Roles = "sub-1", "jane@example.com", "analyst", []string{"analyst"}
	ctx := middleware.WithPlatformContext(context.Background(), pc)
	require.NoError(t, folders.CheckFolder(ctx, site.Mount{Prefix: "/media/", Scope: "global", Folder: "sites/m"}))
	err := folders.CheckFolder(ctx, site.Mount{Prefix: "/x/", Scope: "user", ScopeID: "someone-else", Folder: "private"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, scriptsrc.ErrNotFound), "a library the caller cannot see reads as absent")
}

func TestArchiveSource_ReadsByRange(t *testing.T) {
	src := sources{reader: mediaReader()}
	res := &resource.Resource{Filename: "site.zip", S3Key: "k", SizeBytes: 4}
	got, err := src.archiveSource(context.Background(), res, 1<<20)
	require.NoError(t, err)
	assert.Equal(t, int64(4), got.Size)
	body, err := got.Fetch(context.Background(), 1, 2)
	require.NoError(t, err)
	assert.Equal(t, "NG", string(body), "only the range asked for is read")
}
