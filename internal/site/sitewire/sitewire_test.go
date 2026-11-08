package sitewire

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application/appstore"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetool"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

const (
	pagesDomain = "pages.example.com"
	portalBase  = "https://portal.example.com"
	scriptRef   = "script_1"
	ownerSub    = "user-1"
	indexPage   = "index.html"
)

func newDB(t *testing.T) (*Runtime, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &Runtime{
		Service: &sitesvc.Service{PublicScheme: "https"},
		Store:   sitestore.New(db), Apps: appstore.New(db),
		Config: siteconfig.Config{PagesDomain: pagesDomain},
	}, mock
}

func expectOwningApplication(mock sqlmock.Sqlmock, name string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT application_id FROM application_parts")).
		WithArgs("script", scriptRef).
		WillReturnRows(sqlmock.NewRows([]string{"application_id"}).AddRow("app_1"))
	now := time.Now()
	mock.ExpectQuery("SELECT .+ FROM applications WHERE id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "name", "description", "owner_email", "connection", "schema_name", "current_release",
		"created_by", "created_at", "updated_at", "schema_adopted_version", "schema_added_by", "schema_added_at",
	}).AddRow("app_1", name, "", "jane@example.com", "", "", 0, "jane@example.com", now, now, 0, "", nil))
	mock.ExpectQuery("SELECT kind, part_id").WillReturnRows(sqlmock.NewRows([]string{"kind", "part_id", "added_by", "added_at"}))
}

func TestPageAllowed_NoPagesDomain(t *testing.T) {
	rt, mock := newDB(t)
	rt.Config.PagesDomain = ""
	err := rt.PageAllowed(context.Background(), scriptRef)
	require.ErrorContains(t, err, "no sites.pages_domain")
	assert.NoError(t, mock.ExpectationsWereMet(), "no query is made")
}

func TestPageAllowed_AScriptInNoApplication(t *testing.T) {
	rt, mock := newDB(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT application_id FROM application_parts")).
		WillReturnRows(sqlmock.NewRows([]string{"application_id"}))
	err := rt.PageAllowed(context.Background(), scriptRef)
	require.ErrorIs(t, err, epbinding.ErrNotAllowed)
	assert.ErrorContains(t, err, "manage_application add_part")
}

func TestPageAllowed_AnApplicationNameHoldingTheSeparator(t *testing.T) {
	rt, mock := newDB(t)
	expectOwningApplication(mock, "shop--eu")
	err := rt.PageAllowed(context.Background(), scriptRef)
	require.ErrorContains(t, err, `application "shop--eu" cannot serve pages`)
	assert.ErrorContains(t, err, "site preview")
}

func TestPageAllowed_AScriptInAnApplication(t *testing.T) {
	rt, mock := newDB(t)
	expectOwningApplication(mock, "shop")
	require.NoError(t, rt.PageAllowed(context.Background(), scriptRef))
	assert.NoError(t, mock.ExpectationsWereMet())
}

type nullObjects struct{}

func (nullObjects) PutObject(context.Context, string, string, []byte, string) error { return nil }
func (nullObjects) DeleteObject(context.Context, string, string) error              { return nil }

func TestServing(t *testing.T) {
	off := false
	tests := []struct {
		name    string
		enabled *bool
		objects sitesvc.Objects
		bucket  string
		want    bool
	}{
		{name: "on with a bucket", objects: nullObjects{}, bucket: "sites", want: true},
		{name: "turned off", enabled: &off, objects: nullObjects{}, bucket: "sites"},
		{name: "no object client", bucket: "sites"},
		{name: "no bucket", objects: nullObjects{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &Runtime{Service: &sitesvc.Service{Objects: tt.objects, Bucket: tt.bucket}, Config: siteconfig.Config{Enabled: tt.enabled}}
			assert.Equal(t, tt.want, rt.Serving())
		})
	}
}

func TestSiteOrigins(t *testing.T) {
	t.Run("no application is none", func(t *testing.T) {
		rt, mock := newDB(t)
		assert.Nil(t, rt.SiteOrigins(context.Background(), ""))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("each hostname as a browser origin", func(t *testing.T) {
		rt, mock := newDB(t)
		mock.ExpectQuery("SELECT d.hostname").WithArgs("app_1", pagesDomain, site.PreviewSeparator).
			WillReturnRows(sqlmock.NewRows([]string{"hostname"}).AddRow("www.client.example").AddRow("dev--marketing." + pagesDomain))
		got := rt.SiteOrigins(context.Background(), "app_1")
		assert.Equal(t, []string{"https://www.client.example", "https://dev--marketing." + pagesDomain}, got)
	})
	t.Run("a store failure is none", func(t *testing.T) {
		rt, mock := newDB(t)
		mock.ExpectQuery("SELECT d.hostname").WillReturnError(errors.New("db down"))
		assert.Nil(t, rt.SiteOrigins(context.Background(), "app_1"))
	})
}

func TestRegister_NoDatabaseRegistersNothing(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	rt, err := Register(server, Deps{Config: siteconfig.Config{PagesDomain: "bad domain"}})
	require.NoError(t, err, "with no database the configuration is not read")
	assert.Nil(t, rt)
	assert.Empty(t, toolNames(t, server))
}

func TestRegister_RefusesAPagesDomainHoldingThePortal(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = Register(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), Deps{
		DB: db, PortalURL: "https://pages.example.com", Config: siteconfig.Config{PagesDomain: pagesDomain},
	})
	require.ErrorContains(t, err, "holds the portal's host")
}

func TestRegister_AssemblesTheService(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	rt, err := Register(server, Deps{
		DB: db, Objects: nullObjects{}, Bucket: "sites", PortalURL: "http://Portal.example.com:8443",
		Config: siteconfig.Config{PagesDomain: pagesDomain},
	})
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Equal(t, []string{sitetool.ToolName}, toolNames(t, server))
	assert.True(t, rt.Serving())
	assert.Equal(t, "portal.example.com", rt.Service.PortalHost)
	assert.Equal(t, "http", rt.Service.PublicScheme)
	assert.Equal(t, "8443", rt.Service.PublicPort)
}

func TestRegister_SitesTurnedOffHoldsNoObjectClient(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	off := false
	rt, err := Register(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), Deps{
		DB: db, Objects: nullObjects{}, Bucket: "sites", Config: siteconfig.Config{Enabled: &off},
	})
	require.NoError(t, err)
	assert.Nil(t, rt.Service.Objects)
	assert.False(t, rt.Serving())
}

func TestRegister_APortalAddressThatCannotBeReadIsHTTPS(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	rt, err := Register(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), Deps{DB: db, PortalURL: "not a url at all"})
	require.NoError(t, err)
	assert.Equal(t, "https", rt.Service.PublicScheme)
	assert.Empty(t, rt.Service.PortalHost)
}

func toolNames(t *testing.T, server *mcp.Server) []string {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestScheme(t *testing.T) {
	assert.Equal(t, "https", scheme(parsePortal("")))
	assert.Equal(t, "http", scheme(parsePortal(" http://a.example ")))
}

// library is a resource.Store and resource.S3Client over one list of files.
type library struct {
	resource.Store
	resource.S3Client
	byID    map[string]*resource.Resource
	listed  []resource.Resource
	bodies  map[string][]byte
	listErr error
	getErr  error
}

func (l *library) Get(_ context.Context, id string) (*resource.Resource, error) {
	return l.byID[id], nil
}

func (l *library) List(context.Context, resource.Filter) ([]resource.Resource, int, error) {
	return l.listed, len(l.listed), l.listErr
}

func (l *library) GetObject(_ context.Context, _, key string) (data []byte, contentType string, err error) {
	if l.getErr != nil {
		return nil, "", l.getErr
	}
	return l.bodies[key], "", nil
}

func newSources(l *library) sources {
	return sources{
		reader: scriptsrc.New(l, l, "resources", "mcp"),
		limits: siteconfig.Config{}.Limits(),
	}
}

func ownerCtx() context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: ownerSub, UserEmail: "jane@example.com", PersonaName: "analyst",
	})
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func archiveLibrary(body []byte) *library {
	res := &resource.Resource{
		ID: "res_1", Scope: resource.ScopeUser, ScopeID: ownerSub, Filename: "site.zip",
		S3Key: "resources/res_1", SizeBytes: int64(len(body)), URI: "mcp://user/site.zip",
	}
	return &library{byID: map[string]*resource.Resource{"res_1": res}, bodies: map[string][]byte{"resources/res_1": body}}
}

func refusedMessage(t *testing.T, err error) string {
	t.Helper()
	var refused *sitesvc.RefusedError
	require.ErrorAs(t, err, &refused, "a source's failure is a refusal the caller reads")
	return refused.Msg
}

func TestSourcesArchive_ReadsAZipsMembers(t *testing.T) {
	body := zipOf(t, map[string]string{indexPage: "home", "css/site.css": "body{}"})
	files, err := newSources(archiveLibrary(body)).Archive(ownerCtx(), "mcp:resource:res_1")
	require.NoError(t, err)

	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Body)
	}
	assert.Equal(t, map[string]string{indexPage: "home", "css/site.css": "body{}"}, got)
}

func TestSourcesArchive_Refusals(t *testing.T) {
	tests := []struct {
		name string
		lib  func(t *testing.T) *library
		ref  string
		want string
	}{
		{
			name: "a reference that names nothing",
			lib:  func(*testing.T) *library { return archiveLibrary([]byte("x")) },
			ref:  "mcp:resource:absent", want: "no managed resource",
		},
		{
			name: "a reference of another form",
			lib:  func(*testing.T) *library { return archiveLibrary([]byte("x")) },
			ref:  "res_1", want: "not a managed-resource reference",
		},
		{
			name: "a file that is not an archive",
			lib:  func(*testing.T) *library { return archiveLibrary([]byte("plain text, not an archive")) },
			ref:  "mcp:resource:res_1", want: "cannot be published",
		},
		{
			name: "a file that cannot be read",
			lib: func(t *testing.T) *library {
				t.Helper()
				l := archiveLibrary(zipOf(t, map[string]string{indexPage: "home"}))
				l.getErr = errors.New("bucket is gone")
				return l
			},
			ref: "mcp:resource:res_1", want: "bucket is gone",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newSources(tt.lib(t)).Archive(ownerCtx(), tt.ref)
			assert.Contains(t, refusedMessage(t, err), tt.want)
		})
	}
}

func TestSourcesArchive_AMemberPastTheLimitIsRefused(t *testing.T) {
	body := zipOf(t, map[string]string{indexPage: strings.Repeat("x", 4096)})
	s := newSources(archiveLibrary(body))
	s.limits.MaxFileBytes = 1
	_, err := s.Archive(ownerCtx(), "mcp:resource:res_1")
	assert.Contains(t, refusedMessage(t, err), "cannot be published")
}

func folderLibrary() *library {
	return &library{
		listed: []resource.Resource{
			{ID: "1", Scope: resource.ScopeUser, ScopeID: ownerSub, Path: "web", Filename: indexPage, S3Key: "k1", SizeBytes: 4, URI: "mcp://1"},
			{ID: "2", Scope: resource.ScopeUser, ScopeID: ownerSub, Path: "web/css", Filename: "site.css", S3Key: "k2", SizeBytes: 6, URI: "mcp://2"},
		},
		bodies: map[string][]byte{"k1": []byte("home"), "k2": []byte("body{}")},
	}
}

func TestSourcesFolder_ReadsTheFilesRelativeToIt(t *testing.T) {
	files, err := newSources(folderLibrary()).Folder(ownerCtx(), "", "", "web")
	require.NoError(t, err)
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Body)
	}
	assert.Equal(t, map[string]string{indexPage: "home", "css/site.css": "body{}"}, got)
}

func TestSourcesFolder_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		adjust  func(*library, *sources)
		scope   string
		scopeID string
		want    string
	}{
		{name: "a library the caller cannot see", scope: "user", scopeID: "someone-else", want: ""},
		{name: "a persona the caller is not in", scope: "persona", scopeID: "finance", want: "no managed resource"},
		{name: "a listing that fails", adjust: func(l *library, _ *sources) { l.listErr = errors.New("db down") }, want: "db down"},
		{name: "a file that cannot be read", adjust: func(l *library, _ *sources) { l.getErr = errors.New("bucket is gone") }, want: "bucket is gone"},
		{name: "a folder past the release limit", adjust: func(_ *library, s *sources) { s.limits.MaxReleaseBytes = 5 }, want: "sites.max_release_bytes"},
		{name: "a file past the file limit", adjust: func(_ *library, s *sources) { s.limits.MaxFileBytes = 5 }, want: "more than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := folderLibrary()
			s := newSources(l)
			if tt.adjust != nil {
				tt.adjust(l, &s)
			}
			files, err := s.Folder(ownerCtx(), tt.scope, tt.scopeID, "web")
			if tt.want == "" {
				// A scope of "user" reads the caller's own library whatever
				// scope_id says.
				require.NoError(t, err)
				assert.Len(t, files, 2)
				return
			}
			assert.Contains(t, refusedMessage(t, err), tt.want)
		})
	}
}
