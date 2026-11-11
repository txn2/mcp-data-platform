package sitehttp

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
)

const (
	adminEmail    = "admin@example.com"
	settingsQuery = "SELECT value, updated_by, updated_at FROM platform_settings"
)

var (
	adminDomainCols = []string{"hostname", "site_id", "name", "environment", "application_id", "application", "created_by", "created_at"}
	adminSiteCols   = []string{"id", "name", "description", "owner_email", "main_environment", "created_by", "created_at", "updated_at"}
	adminEnvCols    = []string{"name", "release_number", "requires_approval", "public", "internal_view", "access_changed_at", "updated_by", "updated_at", "keep_releases", "kept"}
	stamp           = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
)

type adminFixture struct {
	mock  sqlmock.Sqlmock
	store *memStore
	rt    *sitewire.Runtime
	mux   *http.ServeMux
}

func newAdmin(t *testing.T) *adminFixture {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	store := newMem()
	store.sites[siteID] = &site.Site{ID: siteID, Name: siteName, OwnerEmail: userOwner.Email, Environments: []site.Environment{{Name: site.EnvDev}}}
	cfg := siteconfig.Config{PagesDomain: pagesDomain}
	live := siteconfig.NewLive(cfg)
	rt := &sitewire.Runtime{
		Store: sitestore.New(db), Config: cfg, Live: live, PortalURL: "https://portal.example.com",
		Settings: settingsjson.New[siteconfig.Settings](db, siteconfig.SettingsSection),
		Service: &sitesvc.Service{
			Store: store, Live: live, PublicScheme: "https", PortalHost: "portal.example.com",
			Apps: fakeApps{"shop": &application.Application{ID: "app_shop", Name: "shop"}},
		},
	}
	f := &adminFixture{mock: mock, store: store, rt: rt, mux: http.NewServeMux()}
	wrap := func(h http.Handler) http.Handler { return h }
	(&adminAPI{rt: rt, author: func(*http.Request) string { return adminEmail }}).mount(f.mux, wrap)
	return f
}

func (f *adminFixture) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *adminFixture) expectDomains(rows ...[]driver.Value) {
	out := sqlmock.NewRows(adminDomainCols)
	for _, r := range rows {
		out.AddRow(r...)
	}
	f.mock.ExpectQuery(regexp.QuoteMeta("ORDER BY d.hostname")).WillReturnRows(out)
}

func (f *adminFixture) expectSites(names ...string) {
	rows := sqlmock.NewRows(adminSiteCols)
	for _, n := range names {
		rows.AddRow("site_"+n, n, "", userOwner.Email, "dev", "creator@example.com", stamp, stamp)
	}
	f.mock.ExpectQuery(regexp.QuoteMeta("FROM sites ORDER BY name")).WillReturnRows(rows)
	for range names {
		f.mock.ExpectQuery(regexp.QuoteMeta("FROM site_environments")).WillReturnRows(
			sqlmock.NewRows(adminEnvCols).AddRow("dev", 1, false, false, false, stamp, "", stamp, nil, 0))
	}
}

func (f *adminFixture) expectSavedSettings(s siteconfig.Settings) {
	raw, _ := json.Marshal(s) //nolint:errchkjson // a struct of plain fields
	f.mock.ExpectQuery(regexp.QuoteMeta(settingsQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow(raw, adminEmail, stamp))
}

func TestAdmin_Domains(t *testing.T) {
	f := newAdmin(t)
	f.expectDomains(
		[]driver.Value{"zzz.client.example", siteID, siteName, "prod", "", "", "a", stamp},
		[]driver.Value{"shop.client.example", "", "", "", "app_shop", "shop", "b", stamp},
	)
	f.expectSites("alpha")

	rec := f.do(t, http.MethodGet, adminDomains, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeBody[AddressList](t, rec)
	require.Len(t, got.Addresses, 4)

	byHost := map[string]DeploymentAddress{}
	hosts := make([]string, 0, 4)
	for _, a := range got.Addresses {
		byHost[a.Hostname], hosts = a, append(hosts, a.Hostname)
	}
	assert.Equal(t, []string{"alpha--dev.pages.example.net", "alpha.pages.example.net", "shop.client.example", "zzz.client.example"}, hosts,
		"in hostname order: the main address and every environment's own")
	assert.Equal(t, site.EnvDev, byHost["alpha--dev.pages.example.net"].Environment)
	assert.Equal(t, addressSite, byHost["alpha.pages.example.net"].Kind)
	assert.Equal(t, site.EnvDev, byHost["alpha.pages.example.net"].Environment)
	assert.Equal(t, "creator@example.com", byHost["alpha.pages.example.net"].CreatedBy)
	assert.Equal(t, stamp.Format(timeLayout), byHost["alpha.pages.example.net"].CreatedAt)
	assert.Equal(t, addressApp, byHost["shop.client.example"].Kind)
	assert.Equal(t, addressDomain, byHost["zzz.client.example"].Kind)
	assert.NoError(t, f.mock.ExpectationsWereMet())
}

func TestAdmin_DomainsWithNoPagesDomain(t *testing.T) {
	f := newAdmin(t)
	f.rt.Live.Set(siteconfig.Config{})
	f.expectDomains()
	f.expectSites("alpha")
	rec := f.do(t, http.MethodGet, adminDomains, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `{"addresses":[]}`, strings.TrimSpace(rec.Body.String()), "no pages domain, no own address")
}

func TestAdmin_DomainsThatCannotBeRead(t *testing.T) {
	t.Run("the domains", func(t *testing.T) {
		f := newAdmin(t)
		f.mock.ExpectQuery(regexp.QuoteMeta("ORDER BY d.hostname")).WillReturnError(errBoom)
		rec := f.do(t, http.MethodGet, adminDomains, "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), secretText)
	})
	t.Run("the sites", func(t *testing.T) {
		f := newAdmin(t)
		f.expectDomains()
		f.mock.ExpectQuery(regexp.QuoteMeta("FROM sites ORDER BY name")).WillReturnError(errBoom)
		rec := f.do(t, http.MethodGet, adminDomains, "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), secretText)
	})
}

func TestAdmin_AddAndRemoveDomain(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{"an application", `{"hostname":"shop.client.example","application":"shop"}`, http.StatusCreated, "shop.client.example"},
		{"a site environment", `{"hostname":"qa.client.example","site":"marketing","environment":"qa","requires_approval":true}`, http.StatusCreated, "qa.client.example"},
		{"a reserved host", `{"hostname":"portal.example.com","site":"marketing","environment":"qa"}`, http.StatusUnprocessableEntity, "answers on itself"},
		{"an application that does not exist", `{"hostname":"x.client.example","application":"nope"}`, http.StatusNotFound, "no such application"},
		{"neither target", `{"hostname":"a.client.example"}`, http.StatusUnprocessableEntity, "maps to a site"},
		{"an unknown site", `{"hostname":"a.client.example","site":"nope","environment":"qa"}`, http.StatusNotFound, "no such site"},
		{"not JSON", `{`, http.StatusBadRequest, "not valid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdmin(t)
			rec := f.do(t, http.MethodPost, adminDomains, tc.body)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.want)
		})
	}

	t.Run("the actor is the authenticated administrator", func(t *testing.T) {
		f := newAdmin(t)
		rec := f.do(t, http.MethodPost, adminDomains, `{"hostname":"a.client.example","site":"marketing","environment":"qa"}`)
		require.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, adminEmail, decodeBody[site.Domain](t, rec).CreatedBy)
		assert.Equal(t, adminEmail, f.store.sites[siteID].Environment("qa").UpdatedBy)

		assert.Equal(t, http.StatusNoContent, f.do(t, http.MethodDelete, adminDomains+"/a.client.example", "").Code)
		assert.Empty(t, f.store.domains)
		assert.Equal(t, http.StatusNotFound, f.do(t, http.MethodDelete, adminDomains+"/none.client.example", "").Code)
	})
}

func TestAdmin_Settings(t *testing.T) {
	t.Run("none saved reads the file's, every bound at its default", func(t *testing.T) {
		f := newAdmin(t)
		f.mock.ExpectQuery(regexp.QuoteMeta(settingsQuery)).WillReturnError(sql.ErrNoRows)
		got := decodeBody[SiteSettings](t, f.do(t, http.MethodGet, adminSettings, ""))
		assert.Equal(t, pagesDomain, got.PagesDomain)
		assert.Equal(t, siteconfig.DefaultMaxFiles, got.MaxFiles)
		assert.Empty(t, got.UpdatedBy)
	})
	t.Run("saved reads as saved, with who and when", func(t *testing.T) {
		f := newAdmin(t)
		f.expectSavedSettings(siteconfig.Settings{PagesDomain: "saved.example.net", MaxFiles: 3})
		got := decodeBody[SiteSettings](t, f.do(t, http.MethodGet, adminSettings, ""))
		assert.Equal(t, "saved.example.net", got.PagesDomain)
		assert.Equal(t, 3, got.MaxFiles)
		assert.Equal(t, adminEmail, got.UpdatedBy)
		assert.Equal(t, stamp.Format(timeLayout), got.UpdatedAt)
	})
	t.Run("a read that fails says nothing of why", func(t *testing.T) {
		f := newAdmin(t)
		f.mock.ExpectQuery(regexp.QuoteMeta(settingsQuery)).WillReturnError(errBoom)
		rec := f.do(t, http.MethodGet, adminSettings, "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), secretText)
	})
}

func TestAdmin_SaveSettings(t *testing.T) {
	t.Run("stores the domain as a request's Host names it, and applies it", func(t *testing.T) {
		f := newAdmin(t)
		f.mock.ExpectExec("INSERT INTO platform_settings").WithArgs(siteconfig.SettingsSection, sqlmock.AnyArg(), adminEmail).
			WillReturnResult(sqlmock.NewResult(0, 1))
		f.expectSavedSettings(siteconfig.Settings{PagesDomain: "saved.example.net"})
		f.expectSavedSettings(siteconfig.Settings{PagesDomain: "saved.example.net"})

		rec := f.do(t, http.MethodPut, adminSettings, `{"pages_domain":"  SAVED.Example.NET. "}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "saved.example.net", decodeBody[SiteSettings](t, rec).PagesDomain)
		assert.Equal(t, "saved.example.net", f.rt.Live.Get().PagesDomain)
		assert.NoError(t, f.mock.ExpectationsWereMet())
	})
	cases := []struct {
		name, body string
		status     int
		want       string
	}{
		{"not JSON", `{`, http.StatusBadRequest, "not valid JSON"},
		{"holding the portal", `{"pages_domain":"example.com"}`, http.StatusUnprocessableEntity, "holds the portal's host"},
		{"not a DNS name", `{"pages_domain":"*.example.net"}`, http.StatusUnprocessableEntity, "sites.pages_domain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdmin(t)
			rec := f.do(t, http.MethodPut, adminSettings, tc.body)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.want)
			assert.Equal(t, pagesDomain, f.rt.Live.Get().PagesDomain, "a refusal changes nothing")
		})
	}
	t.Run("a write that fails says nothing of why", func(t *testing.T) {
		f := newAdmin(t)
		f.mock.ExpectExec("INSERT INTO platform_settings").WillReturnError(errors.New("pq: 10.1.2.3 refused"))
		rec := f.do(t, http.MethodPut, adminSettings, `{"pages_domain":"saved.example.net"}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), secretText)
	})
}
