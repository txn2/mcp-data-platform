package sitewire

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
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
	cfg := siteconfig.Config{PagesDomain: pagesDomain}
	return &Runtime{
		Service: &sitesvc.Service{PublicScheme: "https"},
		Store:   sitestore.New(db), Apps: appstore.New(db),
		Config: cfg, Live: siteconfig.NewLive(cfg),
		Settings:  settingsjson.New[siteconfig.Settings](db, siteconfig.SettingsSection),
		PortalURL: portalBase,
	}, mock
}

const settingsQuery = "SELECT value, updated_by, updated_at FROM platform_settings"

// expectNoSettings is a deployment that has saved no sites settings.
func expectNoSettings(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta(settingsQuery)).WithArgs(siteconfig.SettingsSection).WillReturnError(sql.ErrNoRows)
}

func expectSettings(mock sqlmock.Sqlmock, s siteconfig.Settings) {
	raw, _ := json.Marshal(s) //nolint:errchkjson // a struct of plain fields
	mock.ExpectQuery(regexp.QuoteMeta(settingsQuery)).WithArgs(siteconfig.SettingsSection).
		WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow(raw, "admin@example.com", time.Now()))
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
	rt.Live.Set(siteconfig.Config{})
	err := rt.PageAllowed(context.Background(), scriptRef)
	require.ErrorContains(t, err, "no pages domain")
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

func TestPageAllowed_AScriptInAnApplication(t *testing.T) {
	rt, mock := newDB(t)
	expectOwningApplication(mock, "shop")
	require.NoError(t, rt.PageAllowed(context.Background(), scriptRef))
	assert.NoError(t, mock.ExpectationsWereMet())
}

type nullObjects struct{}

func (nullObjects) PutObject(context.Context, string, string, []byte, string) error { return nil }
func (nullObjects) DeleteObject(context.Context, string, string) error              { return nil }
func (nullObjects) GetObject(context.Context, string, string) (data []byte, contentType string, err error) {
	return nil, "", nil
}

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
		mock.ExpectQuery("SELECT d.hostname").WithArgs("app_1", pagesDomain).
			WillReturnRows(sqlmock.NewRows([]string{"hostname"}).AddRow("www.client.example").AddRow("marketing--prod." + pagesDomain).AddRow("marketing." + pagesDomain))
		got := rt.SiteOrigins(context.Background(), "app_1")
		assert.Equal(t, []string{"https://www.client.example", "https://marketing--prod." + pagesDomain, "https://marketing." + pagesDomain}, got, "an environment's own address and the main address are origins too")
	})
	t.Run("the pages domain in force is the one asked about", func(t *testing.T) {
		rt, mock := newDB(t)
		rt.Live.Set(siteconfig.Config{PagesDomain: "saved.example.net"})
		mock.ExpectQuery("SELECT d.hostname").WithArgs("app_1", "saved.example.net").
			WillReturnRows(sqlmock.NewRows([]string{"hostname"}).AddRow("marketing.saved.example.net"))
		assert.Equal(t, []string{"https://marketing.saved.example.net"}, rt.SiteOrigins(context.Background(), "app_1"))
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
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectNoSettings(mock)

	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	rt, err := Register(server, Deps{
		DB: db, Objects: nullObjects{}, Bucket: "sites", PortalURL: "http://Portal.example.com:8443",
		ReservedURLs: []string{" https://MCP.Example.org:443/path ", "not a url at all", ""},
		Config:       siteconfig.Config{PagesDomain: pagesDomain},
	})
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Equal(t, []string{sitetool.ToolName}, toolNames(t, server))
	assert.True(t, rt.Serving())
	assert.Equal(t, []string{"mcp.example.org"}, rt.Service.ReservedHosts, "only an address with a hostname is reserved, as a request's Host names it")
	assert.Equal(t, pagesDomain, rt.Live.Get().PagesDomain)
	assert.NotNil(t, rt.Crawler)
	assert.Equal(t, "portal.example.com", rt.Service.PortalHost)
	assert.Equal(t, "http", rt.Service.PublicScheme)
	assert.Equal(t, "8443", rt.Service.PublicPort)
}

func TestRegister_SitesTurnedOffHoldsNoObjectClient(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectNoSettings(mock)

	off := false
	rt, err := Register(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), Deps{
		DB: db, Objects: nullObjects{}, Bucket: "sites", Config: siteconfig.Config{Enabled: &off},
	})
	require.NoError(t, err)
	assert.Nil(t, rt.Service.Objects)
	assert.False(t, rt.Serving())
}

func TestRegister_APortalAddressThatCannotBeReadIsHTTPS(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectNoSettings(mock)

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
		live:   siteconfig.NewLive(siteconfig.Config{}),
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
	s.live.Set(siteconfig.Config{MaxFileBytes: 1})
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
		{name: "a folder past the release limit", adjust: func(_ *library, s *sources) { s.live.Set(siteconfig.Config{MaxReleaseBytes: 5}) }, want: "sites.max_release_bytes"},
		{name: "a file past the file limit", adjust: func(_ *library, s *sources) { s.live.Set(siteconfig.Config{MaxFileBytes: 5}) }, want: "more than"},
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

func TestRegister_ASettingsReadThatFailsIsSaid(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(regexp.QuoteMeta(settingsQuery)).WillReturnError(errors.New("db down"))

	_, err = Register(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), Deps{DB: db})
	require.ErrorContains(t, err, "reading the sites settings")
}

func TestRegister_SavedSettingsAreInForceAtStart(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expectSettings(mock, siteconfig.Settings{PagesDomain: "saved.example.net", MaxFiles: 3})

	rt, err := Register(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), Deps{
		DB: db, PortalURL: portalBase, Config: siteconfig.Config{PagesDomain: pagesDomain},
	})
	require.NoError(t, err)
	assert.Equal(t, "saved.example.net", rt.Live.Get().PagesDomain)
	assert.Equal(t, "https://marketing.saved.example.net", rt.Service.Addressing().OwnAddress(&site.Site{Name: "marketing", MainEnvironment: site.EnvDev}), "the service reads the configuration in force")
}

func TestReservedHosts(t *testing.T) {
	assert.Equal(t, []string{}, reservedHosts(nil), "none is [], never null")
	assert.Equal(t, []string{"a.example.org", "b.example.org"}, reservedHosts([]string{"https://A.example.org/x", "http://b.example.org.:8080", "::bad", ""}))
}

func TestRefresh(t *testing.T) {
	ctx := context.Background()

	t.Run("none saved keeps the file's", func(t *testing.T) {
		rt, mock := newDB(t)
		expectNoSettings(mock)
		require.NoError(t, rt.Refresh(ctx))
		assert.Equal(t, pagesDomain, rt.Live.Get().PagesDomain)
	})
	t.Run("a read that fails leaves the configuration in force", func(t *testing.T) {
		rt, mock := newDB(t)
		mock.ExpectQuery(regexp.QuoteMeta(settingsQuery)).WillReturnError(errors.New("db down"))
		require.ErrorContains(t, rt.Refresh(ctx), "reading the sites settings")
		assert.Equal(t, pagesDomain, rt.Live.Get().PagesDomain)
	})
	t.Run("a saved pages domain replaces the file's and drops the routes", func(t *testing.T) {
		rt, mock := newDB(t)
		var invalidated []string
		rt.Service.Invalidate = func(h string) { invalidated = append(invalidated, h) }
		expectSettings(mock, siteconfig.Settings{PagesDomain: "saved.example.net", MaxFiles: 7})
		require.NoError(t, rt.Refresh(ctx))
		assert.Equal(t, "saved.example.net", rt.Live.Get().PagesDomain)
		assert.Equal(t, 7, rt.Live.Get().MaxFiles)
		assert.Equal(t, []string{""}, invalidated, "every cached route is dropped")
	})
	t.Run("the same pages domain keeps the routes", func(t *testing.T) {
		rt, mock := newDB(t)
		var invalidated []string
		rt.Service.Invalidate = func(h string) { invalidated = append(invalidated, h) }
		expectSettings(mock, siteconfig.Settings{PagesDomain: pagesDomain, MaxFiles: 9})
		require.NoError(t, rt.Refresh(ctx))
		assert.Equal(t, 9, rt.Live.Get().MaxFiles)
		assert.Empty(t, invalidated)
	})
	t.Run("a changed domain with no gateway to tell", func(t *testing.T) {
		rt, mock := newDB(t)
		expectSettings(mock, siteconfig.Settings{PagesDomain: "saved.example.net"})
		require.NoError(t, rt.Refresh(ctx))
	})
}

func TestSave(t *testing.T) {
	ctx := context.Background()
	saved := siteconfig.Settings{PagesDomain: "saved.example.net", MaxFiles: 7}

	t.Run("stores and applies on this replica", func(t *testing.T) {
		rt, mock := newDB(t)
		mock.ExpectExec("INSERT INTO platform_settings").WithArgs(siteconfig.SettingsSection, sqlmock.AnyArg(), "admin@example.com").
			WillReturnResult(sqlmock.NewResult(0, 1))
		expectSettings(mock, saved)
		require.NoError(t, rt.Save(ctx, saved, "admin@example.com"))
		assert.Equal(t, "saved.example.net", rt.Live.Get().PagesDomain)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	refusals := []struct {
		name         string
		settings     siteconfig.Settings
		cookieDomain string
		want         string
	}{
		{name: "not a DNS name", settings: siteconfig.Settings{PagesDomain: "*.example.net"}, want: "sites.pages_domain:"},
		{name: "holding the portal", settings: siteconfig.Settings{PagesDomain: "example.com"}, want: "holds the portal's host"},
		{name: "under the session cookie's domain", settings: siteconfig.Settings{PagesDomain: "pages.example.org"}, cookieDomain: ".example.org", want: "session cookie's domain"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			rt, mock := newDB(t)
			rt.CookieDomain = tc.cookieDomain
			err := rt.Save(ctx, tc.settings, "admin@example.com")
			assert.Contains(t, refusedMessage(t, err), tc.want)
			assert.Equal(t, pagesDomain, rt.Live.Get().PagesDomain, "a refusal changes nothing")
			assert.NoError(t, mock.ExpectationsWereMet(), "and writes nothing")
		})
	}
	t.Run("a write that fails is said", func(t *testing.T) {
		rt, mock := newDB(t)
		mock.ExpectExec("INSERT INTO platform_settings").WillReturnError(errors.New("db down"))
		require.ErrorContains(t, rt.Save(ctx, saved, "a"), "saving the sites settings")
		assert.Equal(t, pagesDomain, rt.Live.Get().PagesDomain)
	})
}

func TestSources_TheLimitsInForceAreRead(t *testing.T) {
	body := zipOf(t, map[string]string{indexPage: "home"})
	s := newSources(archiveLibrary(body))
	_, err := s.Archive(ownerCtx(), "mcp:resource:res_1")
	require.NoError(t, err)

	s.live.Set(siteconfig.Config{MaxFileBytes: 1})
	_, err = s.Archive(ownerCtx(), "mcp:resource:res_1")
	assert.Contains(t, refusedMessage(t, err), "cannot be published", "a saved limit applies on the next publish")
}

func expectApplicationNamed(mock sqlmock.Sqlmock, query, name string) {
	now := time.Now()
	mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{
		"id", "name", "description", "owner_email", "connection", "schema_name", "current_release",
		"created_by", "created_at", "updated_at", "schema_adopted_version", "schema_added_by", "schema_added_at",
	}).AddRow("app_1", name, "", "jane@example.com", "", "", 0, "jane@example.com", now, now, 0, "", nil))
	mock.ExpectQuery("SELECT kind, part_id").WillReturnRows(sqlmock.NewRows([]string{"kind", "part_id", "added_by", "added_at"}))
}

func TestPageAddress(t *testing.T) {
	t.Run("the application's page origin", func(t *testing.T) {
		rt, mock := newDB(t)
		rt.Service.Live = rt.Live
		expectOwningApplication(mock, "shop")
		assert.Equal(t, "https://shop."+pagesDomain, rt.PageAddress(context.Background(), scriptRef))
	})
	t.Run("a script in no application has none", func(t *testing.T) {
		rt, mock := newDB(t)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT application_id FROM application_parts")).WillReturnRows(sqlmock.NewRows([]string{"application_id"}))
		assert.Empty(t, rt.PageAddress(context.Background(), scriptRef))
	})
}

func TestApplicationAddresses(t *testing.T) {
	ctx := context.Background()
	t.Run("its page origin and the domains mapped to it", func(t *testing.T) {
		rt, mock := newDB(t)
		rt.Service.Live = rt.Live
		expectApplicationNamed(mock, "FROM applications WHERE name", "shop")
		mock.ExpectQuery("ORDER BY d.hostname").WillReturnRows(sqlmock.NewRows([]string{
			"hostname", "site_id", "name", "environment", "application_id", "application", "created_by", "created_at", "public", "access_changed_at",
		}).AddRow("shop.client.example", "", "", "", "app_1", "shop", "a", time.Now(), false, time.Now()).
			AddRow("www.client.example", "site_1", "marketing", "prod", "", "", "a", time.Now(), true, time.Now()))

		origin, domains, err := rt.ApplicationAddresses(ctx, "shop")
		require.NoError(t, err)
		assert.Equal(t, "https://shop."+pagesDomain, origin)
		assert.Equal(t, []string{"shop.client.example"}, domains)
	})
	t.Run("none mapped is [], never null", func(t *testing.T) {
		rt, mock := newDB(t)
		expectApplicationNamed(mock, "FROM applications WHERE name", "shop")
		mock.ExpectQuery("ORDER BY d.hostname").WillReturnRows(sqlmock.NewRows([]string{"hostname"}))
		_, domains, err := rt.ApplicationAddresses(ctx, "shop")
		require.NoError(t, err)
		assert.Equal(t, []string{}, domains)
	})
	t.Run("no such application", func(t *testing.T) {
		rt, mock := newDB(t)
		mock.ExpectQuery("FROM applications WHERE name").WillReturnError(sql.ErrNoRows)
		_, _, err := rt.ApplicationAddresses(ctx, "nope")
		require.ErrorContains(t, err, `application "nope"`)
	})
	t.Run("domains that cannot be read", func(t *testing.T) {
		rt, mock := newDB(t)
		expectApplicationNamed(mock, "FROM applications WHERE name", "shop")
		mock.ExpectQuery("ORDER BY d.hostname").WillReturnError(errors.New("db down"))
		_, _, err := rt.ApplicationAddresses(ctx, "shop")
		require.ErrorContains(t, err, "listing domains")
	})
}
