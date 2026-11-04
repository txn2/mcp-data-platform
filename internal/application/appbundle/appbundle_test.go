package appbundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appschema"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// world is one deployment's stores, in memory.
type world struct {
	apps       map[string]*application.Application
	releases   map[string][]application.Release
	scripts    map[string]*script.Script
	versions   map[string][]script.Version
	assets     map[string]*portaldomain.Asset
	assetVers  map[string][]portaldomain.AssetVersion
	blobs      map[string][]byte
	migrations []postgreskit.Migration
	bindings   map[string]*epbinding.Binding
	schedules  map[string]*script.Schedule
	endpoints  []epcmd.Request
	landed     []byte
	authors    []script.Author
}

func newWorld() *world {
	return &world{
		apps: map[string]*application.Application{}, releases: map[string][]application.Release{},
		scripts: map[string]*script.Script{}, versions: map[string][]script.Version{},
		assets: map[string]*portaldomain.Asset{}, assetVers: map[string][]portaldomain.AssetVersion{},
		blobs: map[string][]byte{}, bindings: map[string]*epbinding.Binding{}, schedules: map[string]*script.Schedule{},
	}
}

// application.Store

func (w *world) Create(_ context.Context, a *application.Application) error {
	a.ID = "app_" + a.Name
	cp := *a
	w.apps[a.ID] = &cp
	return nil
}

func (w *world) Get(_ context.Context, id string) (*application.Application, error) {
	a, ok := w.apps[id]
	if !ok {
		return nil, application.ErrNotFound
	}
	cp := *a
	cp.Parts = slices.Clone(a.Parts)
	return &cp, nil
}

func (w *world) GetByName(ctx context.Context, name string) (*application.Application, error) {
	return w.Get(ctx, "app_"+name)
}
func (*world) List(context.Context) ([]application.Application, error)         { return nil, nil }
func (*world) Update(context.Context, *application.Application) error          { return nil }
func (*world) Delete(context.Context, string) error                            { return nil }
func (*world) RemovePart(context.Context, string, string, string) error        { return nil }
func (*world) Releases(context.Context, string) ([]application.Release, error) { return nil, nil }

func (w *world) AddPart(_ context.Context, id string, p application.Part) error {
	w.apps[id].Parts = append(w.apps[id].Parts, p)
	return nil
}

func (*world) Owning(context.Context, string, string) (*application.Application, error) {
	return nil, application.ErrNotFound
}

func (w *world) Release(_ context.Context, id string, n int) (*application.Release, error) {
	for _, r := range w.releases[id] {
		if r.Number == n {
			return &r, nil
		}
	}
	return nil, application.ErrNoRelease
}

func (w *world) Record(_ context.Context, id string, r application.Recorded) (*application.Release, error) {
	rel := r.Release
	rel.Number = w.apps[id].CurrentRelease + 1
	w.apps[id].CurrentRelease = rel.Number
	w.releases[id] = append(w.releases[id], rel)
	return &rel, nil
}

// scripts

type scripts struct{ w *world }

func (s scripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	return s.w.scripts[id], nil
}

func (s scripts) GetVersion(_ context.Context, id string, v int) (*script.Version, error) {
	for _, x := range s.w.versions[id] {
		if x.Version == v {
			return &x, nil
		}
	}
	return nil, nil //nolint:nilnil // the version store's contract: nil, nil is no such version
}

func (s scripts) Create(_ context.Context, sc *script.Script, author script.Author) error {
	s.w.authors = append(s.w.authors, author)
	sc.ID = fmt.Sprintf("00000000-0000-0000-0000-%012d", len(s.w.scripts))
	sc.Version = 1
	s.w.scripts[sc.ID] = sc
	s.w.versions[sc.ID] = []script.Version{{ScriptID: sc.ID, Version: 1, Source: sc.Source, Params: sc.Params}}
	return nil
}

func (s scripts) UpdateWithVersion(_ context.Context, sc *script.Script, _ script.Author) error {
	sc.Version++
	s.w.scripts[sc.ID] = sc
	s.w.versions[sc.ID] = append(s.w.versions[sc.ID], script.Version{ScriptID: sc.ID, Version: sc.Version, Source: sc.Source})
	return nil
}

func (s scripts) Update(_ context.Context, sc *script.Script) error {
	s.w.scripts[sc.ID] = sc
	return nil
}

func (s scripts) GetSchedule(_ context.Context, id string) (*script.Schedule, error) {
	if sc, ok := s.w.schedules[id]; ok {
		return sc, nil
	}
	return nil, script.ErrScheduleNotFound
}

func (s scripts) SetSchedule(_ context.Context, sc *script.Schedule) error {
	s.w.schedules[sc.ScriptID] = sc
	return nil
}

// assets

type assets struct{ w *world }

func (a assets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	x, ok := a.w.assets[id]
	if !ok {
		return nil, errors.New("no rows")
	}
	return x, nil
}

func (a assets) Insert(_ context.Context, as portaldomain.Asset) error {
	a.w.assets[as.ID] = &as
	return nil
}

func (a assets) GetByVersion(_ context.Context, id string, v int) (*portaldomain.AssetVersion, error) {
	for _, x := range a.w.assetVers[id] {
		if x.Version == v {
			return &x, nil
		}
	}
	return nil, errors.New("no rows")
}

func (a assets) CreateVersion(_ context.Context, v portaldomain.AssetVersion) (int, error) {
	v.Version = len(a.w.assetVers[v.AssetID]) + 1
	a.w.assetVers[v.AssetID] = append(a.w.assetVers[v.AssetID], v)
	as := a.w.assets[v.AssetID]
	as.CurrentVersion, as.S3Key, as.S3Bucket = v.Version, v.S3Key, v.S3Bucket
	return v.Version, nil
}

type blobs struct{ w *world }

func (b blobs) GetObject(_ context.Context, bucket, key string) (data []byte, contentType string, err error) {
	d, ok := b.w.blobs[bucket+"/"+key]
	if !ok {
		return nil, "", errors.New("no such key")
	}
	return d, "text/html", nil
}

func (b blobs) PutObject(_ context.Context, bucket, key string, data []byte, _ string) error {
	b.w.blobs[bucket+"/"+key] = data
	return nil
}

type migs struct{ w *world }

func (m migs) List(context.Context, string, string) ([]postgreskit.Migration, error) {
	return m.w.migrations, nil
}

func (m migs) Put(_ context.Context, x postgreskit.Migration) (bool, error) {
	m.w.migrations = append(m.w.migrations, x)
	return false, nil
}

type bindings struct{ w *world }

func (b bindings) Get(_ context.Context, id string) (*epbinding.Binding, error) {
	if x, ok := b.w.bindings[id]; ok {
		return x, nil
	}
	return nil, epbinding.ErrNotFound
}

type sources struct{}

func (sources) Exists(_ context.Context, name string) (bool, error) {
	return name == "orders-hook", nil
}

type schema struct{}

func (schema) State(context.Context, string, string) (*appschema.State, error) {
	return &appschema.State{}, nil
}

func (schema) Release(context.Context, string, string, string) (*appschema.Applied, error) {
	return &appschema.Applied{Versions: []int{}, Head: 1}, nil
}

func (schema) Rollback(context.Context, string, string, string, int) (*appschema.Reverted, error) {
	return &appschema.Reverted{}, nil
}

type lander struct{ w *world }

func (l lander) LandResource(_ context.Context, dest toolkit.ResourceDestination, r io.Reader, _ string) (*toolkit.ResourceLanding, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading the bundle: %w", err)
	}
	l.w.landed = data
	return &toolkit.ResourceLanding{ResourceID: "r1", Filename: dest.Filename, Path: dest.Path}, nil
}

type reader struct{ data string }

func (r reader) Source(_ context.Context, ref string, _ resource.Claims, _ int64) (string, error) {
	if ref != "mcp:resource:r1" {
		return "", errors.New("not found")
	}
	return r.data, nil
}

func bundler(w *world) *Bundler {
	sc, as := scripts{w}, assets{w}
	svc := &appsvc.Service{Store: w, Scripts: sc, Assets: as, AssetVersions: as, Schema: schema{}, Sources: sources{}}
	return New(Deps{
		Service: svc, Migrations: migs{w}, Bindings: bindings{w}, Schedules: sc,
		FormConnection: func(context.Context, string) (string, error) { return "warehouse", nil },
		Blobs:          blobs{w}, Lander: lander{w}, Claims: func(context.Context) resource.Claims { return resource.Claims{} },
		Scripts: sc, Assets: as, AssetVersions: as, Bucket: "portal", Prefix: "p",
		Endpoints: func(_ context.Context, req epcmd.Request) (map[string]any, error) {
			w.endpoints = append(w.endpoints, req)
			return map[string]any{}, nil
		},
		HasSchemaConnection: func(name string) bool { return name == "orders-prod" },
	})
}

var admin = application.Actor{Email: "admin@example.com", Admin: true, Subject: "u-admin", Roles: []string{"admin"}}

const apiID = "11111111-1111-1111-1111-111111111111"

// source is a deployment with a released application.
func source(t *testing.T) *world {
	t.Helper()
	w := newWorld()
	w.apps["app_shop"] = &application.Application{
		ID: "app_shop", Name: "shop", Description: "A shop", OwnerEmail: "jane@example.com",
		Connection: "orders", Schema: "shop", CurrentRelease: 1, Parts: []application.Part{
			{Kind: "script", ID: apiID}, {Kind: "asset", ID: "as1"}, {Kind: "source", ID: "orders-hook"},
		},
	}
	src := "def handle(req):\n    rows = platform.query(connection=\"orders\", sql=\"SELECT 1\")\n" +
		"    key = \"{{secret:stripe-key}}\"\n    return {\"status\": 200}\n"
	w.scripts[apiID] = &script.Script{ID: apiID, Name: "shop-api", Version: 2, OwnerEmail: "jane@example.com", EventSource: "orders-hook"}
	w.versions[apiID] = []script.Version{{ScriptID: apiID, Version: 1, Source: src, DisplayName: "Shop API"}, {Version: 2, Source: "def handle(req):\n    return 2\n"}}
	w.bindings[apiID] = &epbinding.Binding{Name: "shop", Methods: []string{"GET"}, Path: "/", AuthMode: "public", BotCheck: "pow", Enabled: true}
	w.schedules[apiID] = &script.Schedule{ScriptID: apiID, CronSpec: "0 3 * * *", Timezone: "UTC", Enabled: true}
	w.assets["as1"] = &portaldomain.Asset{ID: "as1", Name: "storefront", ContentType: "text/html", S3Bucket: "portal", CurrentVersion: 2}
	w.assetVers["as1"] = []portaldomain.AssetVersion{{AssetID: "as1", Version: 1, S3Key: "v1", ContentType: "text/html"}}
	w.blobs["portal/v1"] = []byte("<h1>shop</h1>")
	w.migrations = []postgreskit.Migration{
		{Version: 1, Name: "products", Up: "CREATE TABLE products (id int)", Down: "DROP TABLE products", Transactional: true},
		{Version: 2, Name: "later", Up: "CREATE TABLE later (id int)", Transactional: true},
	}
	w.releases["app_shop"] = []application.Release{{Number: 1, MigrationVersion: 1, Pins: []application.Pin{
		{Kind: "script", PartID: apiID, Version: 1}, {Kind: "asset", PartID: "as1", Version: 1},
	}}}
	return w
}

func TestExport_WritesTheReleaseAndNamesWhatItUses(t *testing.T) {
	w := source(t)
	b := bundler(w)
	ctx := context.Background()
	_, err := b.Export(ctx, application.Actor{Email: "jane@example.com"}, "shop", 0)
	require.ErrorIs(t, err, application.ErrNotAllowed, "the owner does not export; an administrator does")

	out, err := b.Export(ctx, admin, "shop", 0)
	require.NoError(t, err)
	ex, ok := out.(*Exported)
	require.True(t, ok)
	assert.Equal(t, 1, ex.Release)
	assert.Equal(t, 1, ex.Migrations, "only the migrations the release holds")
	assert.Equal(t, Names{
		Connections: []string{"orders", "warehouse"}, Secrets: []string{"stripe-key"},
		Sources: []string{"orders-hook"}, BotChecks: []string{"pow"},
	}, ex.Names)
	assert.Equal(t, "shop-release-1.json", ex.Bundle.Filename)

	var bundle Bundle
	require.NoError(t, json.Unmarshal(w.landed, &bundle))
	require.Len(t, bundle.Scripts, 1)
	assert.Equal(t, 1, bundle.Scripts[0].Version, "the pinned version, not the newest")
	assert.Equal(t, "shop", bundle.Scripts[0].Endpoint.Name)
	assert.Equal(t, "warehouse", bundle.Scripts[0].Endpoint.FormConnection)
	assert.Equal(t, "0 3 * * *", bundle.Scripts[0].Schedule.CronSpec)
	require.Len(t, bundle.Assets, 1)
	assert.Equal(t, []byte("<h1>shop</h1>"), bundle.Assets[0].Content)
	assert.Equal(t, []string{"orders-hook"}, bundle.Sources)

	_, err = b.Export(ctx, admin, "shop", 7)
	require.ErrorIs(t, err, application.ErrNoRelease)
	w.apps["app_shop"].CurrentRelease = 0
	_, err = b.Export(ctx, admin, "shop", 0)
	require.ErrorContains(t, err, "no release to export")
}

func TestExport_RefusesAPasswordMigration(t *testing.T) {
	w := source(t)
	w.migrations[0].Up = "CREATE ROLE app LOGIN PASSWORD 'canary-2412'"
	_, err := bundler(w).Export(context.Background(), admin, "shop", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "password_literal")
	assert.NotContains(t, err.Error(), "canary-2412", "the refusal never repeats the value")
}

func exported(t *testing.T) string {
	t.Helper()
	w := source(t)
	_, err := bundler(w).Export(context.Background(), admin, "shop", 0)
	require.NoError(t, err)
	return string(w.landed)
}

func adminCtx() context.Context {
	pc := middleware.NewPlatformContext("r")
	pc.UserID, pc.UserEmail, pc.Roles, pc.IsAdmin = "u-admin", admin.Email, []string{"admin"}, true
	return middleware.WithPlatformContext(context.Background(), pc)
}

var fullMap = map[string]string{
	"orders": "orders-prod", "warehouse": "warehouse", "stripe-key": "stripe-live", "orders-hook": "orders-hook", "pow": "pow",
}

func TestImport_CreatesTheApplicationAtTheRelease(t *testing.T) {
	data := exported(t)
	w := newWorld()
	b := bundler(w)
	b.Reader = reader{data}
	ctx := adminCtx()

	_, err := b.Import(ctx, application.Actor{Email: "jane@example.com"}, "mcp:resource:r1", fullMap)
	require.ErrorIs(t, err, application.ErrNotAllowed)

	partial := map[string]string{"orders": "orders-prod"}
	_, err = b.Import(ctx, admin, "mcp:resource:r1", partial)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unmapped connection "warehouse"`, "an unmapped name refuses the import naming it")
	assert.Contains(t, err.Error(), `secret "stripe-key"`)
	assert.Empty(t, w.apps, "nothing is written before every name is mapped")

	out, err := b.Import(ctx, admin, "mcp:resource:r1", fullMap)
	require.NoError(t, err)
	im, ok := out.(*Imported)
	require.True(t, ok)
	assert.True(t, im.Created)
	assert.Equal(t, application.ActionImport, im.Release.Action)
	assert.Equal(t, map[string]string{"shop-api": partCreated}, im.Scripts)
	assert.Equal(t, map[string]string{"storefront": partCreated}, im.Assets)

	app := w.apps["app_shop"]
	assert.Equal(t, "orders-prod", app.Connection, "the schema is on the mapped connection")
	assert.Equal(t, admin.Email, app.OwnerEmail)
	require.Len(t, w.migrations, 1)
	assert.Equal(t, "orders-prod", w.migrations[0].Connection)
	var created *script.Script
	for _, sc := range w.scripts {
		created = sc
	}
	assert.Contains(t, created.Source, `connection="orders-prod"`, "a mapped connection is renamed in the source")
	assert.Contains(t, created.Source, "{{secret:stripe-live}}")
	assert.Equal(t, admin.Email, created.OwnerEmail, "an imported script belongs to the importing administrator")
	require.Len(t, w.authors, 1)
	assert.Equal(t, []string{"admin"}, w.authors[0].Roles, "its runs present the importing administrator's own roles")
	assert.Equal(t, "u-admin", w.authors[0].Subject)
	assert.Equal(t, "orders-hook", created.EventSource)
	require.Len(t, w.endpoints, 1)
	assert.Equal(t, "shop", w.endpoints[0].Input.Endpoint)
	assert.Equal(t, "warehouse", w.endpoints[0].Input.Connection)
	assert.NotNil(t, w.schedules[created.ID])
	require.Len(t, w.assets, 1)

	// A later import of the same release finds every part unchanged.
	out, err = b.Import(ctx, admin, "mcp:resource:r1", fullMap)
	require.NoError(t, err)
	im, ok = out.(*Imported)
	require.True(t, ok)
	assert.False(t, im.Created)
	assert.Equal(t, partUnchanged, im.Scripts["shop-api"])
	assert.Equal(t, partUnchanged, im.Assets["storefront"])
}

func TestImport_Refusals(t *testing.T) {
	data := exported(t)
	ctx := adminCtx()
	cases := map[string]struct {
		bundle  string
		mapping map[string]string
		setup   func(w *world)
		want    string
	}{
		"not json":      {bundle: "nope", mapping: fullMap, want: "not a JSON application bundle"},
		"wrong format":  {bundle: `{"format":"x","format_version":1}`, mapping: fullMap, want: "this deployment reads"},
		"no connection": {bundle: data, mapping: mapWith("orders", "missing"), want: "is not an application database connection here"},
		"migration drift": {bundle: data, mapping: fullMap, setup: func(w *world) {
			w.migrations = []postgreskit.Migration{{Version: 1, Checksum: "other"}}
		}, want: "differs from the bundle's"},
		"mapped name breaks out of a string": {bundle: data, mapping: mapWith("orders", `x") + platform.call("x`), want: "a name of letters"},
		"refused migration":                  {bundle: refusedBundle(t, data), mapping: fullMap, want: "untrusted_code"},
		"schema moved": {bundle: data, mapping: fullMap, setup: func(w *world) {
			w.apps["app_shop"] = &application.Application{ID: "app_shop", Name: "shop", Connection: "orders-prod", Schema: "other"}
		}, want: "the bundle on"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			if tc.setup != nil {
				tc.setup(w)
			}
			b := bundler(w)
			b.Reader = reader{tc.bundle}
			_, err := b.Import(ctx, admin, "mcp:resource:r1", tc.mapping)
			require.ErrorContains(t, err, tc.want)
		})
	}
	w := newWorld()
	b := bundler(w)
	_, err := b.Import(ctx, admin, "mcp:resource:r1", fullMap)
	require.ErrorContains(t, err, "no managed-resource library", "no reader configured")
	b.Reader = reader{data}
	_, err = b.Import(ctx, admin, " ", fullMap)
	require.ErrorContains(t, err, "name the bundle")
	_, err = b.Import(ctx, admin, "mcp:resource:other", fullMap)
	require.ErrorContains(t, err, "reading the bundle")
}

// refusedBundle is data with its migration replaced by one the review refuses.
func refusedBundle(t *testing.T, data string) string {
	t.Helper()
	var b Bundle
	require.NoError(t, json.Unmarshal([]byte(data), &b))
	b.Schema.Migrations[0].Up = "CREATE FUNCTION f() RETURNS int LANGUAGE plpython3u AS $$return 1$$"
	out, err := json.Marshal(b)
	require.NoError(t, err)
	return string(out)
}

func mapWith(k, v string) map[string]string {
	out := maps.Clone(fullMap)
	out[k] = v
	return out
}

func TestRename(t *testing.T) {
	src := `platform.query(connection="orders", sql="SELECT 'orders'") + "{{secret:k}}" + "orders-archive"`
	got := rename(src, map[string]string{"orders": "orders-prod", "k": "k2", "same": "same", "empty": ""})
	assert.Equal(t, `platform.query(connection="orders-prod", sql="SELECT 'orders-prod'") + "{{secret:k2}}" + "orders-archive"`, got)
	assert.Equal(t, src, rename(src, map[string]string{}))
}

func TestNamesAll(t *testing.T) {
	n := Names{Connections: []string{"b", "a"}, Secrets: []string{"a"}}
	assert.Equal(t, []string{"a", "b"}, n.All())
}

// A deployment without a store a part needs refuses the import naming it,
// and one with no library to write a bundle to refuses the export.
func TestImportAndExport_RefuseWhatADeploymentCannotHold(t *testing.T) {
	data := exported(t)
	ctx := adminCtx()
	cases := map[string]struct {
		strip func(b *Bundler)
		want  string
	}{
		"no migrations": {func(b *Bundler) { b.Migrations = nil }, "keeps no migrations"},
		"no endpoints":  {func(b *Bundler) { b.Endpoints = nil }, "cannot bind endpoints"},
		"no assets":     {func(b *Bundler) { b.Blobs = nil }, "no asset storage"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := bundler(newWorld())
			b.Reader = reader{data}
			tc.strip(b)
			_, err := b.Import(ctx, admin, "mcp:resource:r1", fullMap)
			require.ErrorContains(t, err, tc.want)
		})
	}
	w := source(t)
	b := bundler(w)
	b.Lander = nil
	_, err := b.Export(ctx, admin, "shop", 0)
	require.ErrorContains(t, err, "no managed-resource library")
	b = bundler(w)
	b.Migrations = nil
	_, err = b.Export(ctx, admin, "shop", 0)
	require.ErrorContains(t, err, "keeps no migrations")
	b = bundler(w)
	b.Blobs = nil
	_, err = b.Export(ctx, admin, "shop", 0)
	require.ErrorContains(t, err, "no asset storage")
	b = bundler(w)
	b.Bindings, b.Schedules = nil, nil
	_, err = b.Export(ctx, admin, "shop", 0)
	require.NoError(t, err, "a deployment with no endpoints or schedules exports scripts without them")
}
