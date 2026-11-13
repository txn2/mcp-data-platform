package appbundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appschema"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// world is one deployment's stores, in memory.
type world struct {
	gated      []scriptsave.Request
	recordings map[string]*scriptrec.Stored
	kept       map[string][]string
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
		recordings: map[string]*scriptrec.Stored{}, kept: map[string][]string{},
	}
}

// recs is the recording store.
type recs struct{ w *world }

func (r recs) Get(_ context.Context, runID string) (*scriptrec.Stored, error) {
	if rec, ok := r.w.recordings[runID]; ok {
		return rec, nil
	}
	return nil, scriptrec.ErrNotFound
}

func (r recs) Save(_ context.Context, rec scriptrec.Stored) error {
	r.w.recordings[rec.RunID] = &rec
	return nil
}

func (r recs) Keep(_ context.Context, scriptID, _ string, runIDs []string) error {
	r.w.kept[scriptID] = runIDs
	return nil
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

// isPart reports whether script id is a part of any application.
func (w *world) isPart(id string) bool {
	for _, a := range w.apps {
		for _, p := range a.Parts {
			if p.Kind == application.KindScript && p.ID == id {
				return true
			}
		}
	}
	return false
}

func (w *world) AddPart(_ context.Context, id string, p application.Part) error {
	w.apps[id].Parts = append(w.apps[id].Parts, p)
	return nil
}

func (*world) Owning(context.Context, string, string) (*application.Application, error) {
	return nil, application.ErrNotFound
}

func (w *world) SetSchema(_ context.Context, id string, h application.HeldSchema) error {
	for aid, a := range w.apps {
		if aid != id && a.Connection == h.Connection && a.Schema == h.Schema {
			return application.ErrSchemaHeld
		}
	}
	a := w.apps[id]
	a.Connection, a.Schema, a.SchemaAdoptedVersion = h.Connection, h.Schema, h.AdoptedVersion
	return nil
}

func (*world) SetRunRole(context.Context, string, application.HeldSchema) error { return nil }

func (w *world) ClearSchema(_ context.Context, id string) error {
	w.apps[id].Connection, w.apps[id].Schema = "", ""
	return nil
}

func (w *world) Holding(ctx context.Context, connection, schema string) (*application.Application, error) {
	for id, a := range w.apps {
		if a.Connection == connection && a.Schema == schema {
			return w.Get(ctx, id)
		}
	}
	return nil, application.ErrNotFound
}

func (*world) RunRoleOf(context.Context, string) (*application.RunRole, error) {
	return nil, application.ErrNoRunRole
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

// roles makes and drops run roles without a database.
type roles struct{}

func (roles) CreateRunRole(_ context.Context, _, schema string) (role, password string, err error) {
	return schema + "_run", "pw", nil
}
func (roles) GrantRunRole(context.Context, string, string) (string, error) { return "", nil }
func (roles) DropRunRole(context.Context, string, string) error            { return nil }

// sealer seals by wrapping.
type sealer struct{}

func (sealer) Enabled() bool                         { return true }
func (sealer) SealBound(_, p string) (string, error) { return "sealed:" + p, nil }
func (sealer) OpenBound(_, s string) (string, error) { return strings.TrimPrefix(s, "sealed:"), nil }

func bundler(w *world) *Bundler {
	sc, as := scripts{w}, assets{w}
	svc := &appsvc.Service{
		Store: w, Scripts: sc, Assets: as, AssetVersions: as, Schema: schema{}, Sources: sources{},
		Roles: roles{}, Sealer: sealer{}, Reach: func(context.Context, application.Actor) ([]string, error) {
			return []string{"orders-prod"}, nil
		},
	}
	return New(Deps{
		Service: svc, Migrations: migs{w}, Bindings: bindings{w}, Schedules: sc,
		FormConnection: func(context.Context, string) (string, error) { return "warehouse", nil },
		Blobs:          blobs{w}, Lander: lander{w}, Claims: func(context.Context) resource.Claims { return resource.Claims{} },
		Scripts: sc, Assets: as, AssetVersions: as, Bucket: "portal", Prefix: "p",
		Endpoints: func(_ context.Context, req epcmd.Request) (map[string]any, error) {
			w.endpoints = append(w.endpoints, req)
			// As the real binding refuses (#2554): a page belongs to an
			// application, so its script must be a part before it is bound.
			if req.Input.ResponseMode == epbinding.ResponsePage && !w.isPart(req.Script.ID) {
				return nil, errors.New("a page belongs to an application, and this script is a part of none")
			}
			return map[string]any{}, nil
		},
		HasSchemaConnection: func(name string) bool { return name == "orders-prod" },
		SaveGate:            &gate{w: w}, Recordings: recs{w},
	})
}

// gate is a save gate that formats nothing and refuses a source holding
// "REFUSE", recording each request it was asked.
type gate struct{ w *world }

func (g *gate) Check(_ context.Context, req scriptsave.Request) scriptsave.Result {
	g.w.gated = append(g.w.gated, req)
	res := scriptsave.Result{}
	res.Lint.Source = req.Source
	if strings.Contains(req.Source, "REFUSE") {
		res.Refusal = "statement 3: lint_rule says no"
		return res
	}
	res.ChangeSummary, res.ChangeAgreedBy = req.ChangeSummary, req.Caller.Email
	return res
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
	w.bindings[apiID] = &epbinding.Binding{
		Name: "shop", Methods: []string{"GET"}, Path: "/", AuthMode: "public", BotCheck: "pow", Enabled: true,
		ResponseMode: epbinding.ResponsePage, PageFrameOrigins: []string{"https://js.stripe.com"},
		PageConnectOrigins: []string{"https://api.stripe.com"}, PageFormOrigins: []string{"https://pay.example.test"}, IPRateLimit: 30,
	}
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
	assert.Equal(t, []string{"https://js.stripe.com"}, bundle.Scripts[0].Endpoint.PageFrameOrigins, "#2554's lists are exported")
	assert.Equal(t, []string{"https://api.stripe.com"}, bundle.Scripts[0].Endpoint.PageConnectOrigins)
	assert.Equal(t, []string{"https://pay.example.test"}, bundle.Scripts[0].Endpoint.PageFormOrigins)
	assert.Equal(t, 30, bundle.Scripts[0].Endpoint.IPRateLimit, "#2556's limit is exported")
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
	require.NotEmpty(t, w.gated, "every script crosses the save gate")
	req := w.gated[len(w.gated)-1]
	assert.Contains(t, req.Source, `connection="orders-prod"`, "the gate checks the source as it will be stored")
	assert.Equal(t, scriptsave.Caller{Email: admin.Email, Admin: true, Roles: []string{"admin"}}, req.Caller)
	assert.True(t, req.Agreed)
	assert.Equal(t, "imported from release 1 of shop", req.ChangeSummary)
	assert.Equal(t, "imported from release 1 of shop", created.ChangeSummary, "the saved version carries what the gate agreed")
	assert.Contains(t, created.Source, "{{secret:stripe-live}}")
	assert.Equal(t, admin.Email, created.OwnerEmail, "an imported script belongs to the importing administrator")
	require.Len(t, w.authors, 1)
	assert.Equal(t, []string{"admin"}, w.authors[0].Roles, "its runs present the importing administrator's own roles")
	assert.Equal(t, "u-admin", w.authors[0].Subject)
	assert.Equal(t, "orders-hook", created.EventSource)
	require.Len(t, w.endpoints, 1)
	assert.Equal(t, "shop", w.endpoints[0].Input.Endpoint)
	assert.Equal(t, "warehouse", w.endpoints[0].Input.Connection)
	assert.Equal(t, []string{"https://js.stripe.com"}, w.endpoints[0].Input.PageFrameOrigins, "#2554's lists are imported")
	assert.Equal(t, []string{"https://api.stripe.com"}, w.endpoints[0].Input.PageConnectOrigins)
	assert.Equal(t, []string{"https://pay.example.test"}, w.endpoints[0].Input.PageFormOrigins)
	require.NotNil(t, w.endpoints[0].Input.IPRateLimit)
	assert.Equal(t, 30, *w.endpoints[0].Input.IPRateLimit, "#2556's limit is imported")
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
		"refused script":                     {bundle: refusedScript(t, data), mapping: fullMap, want: "script shop-api is refused by this deployment's save gate: statement 3: lint_rule says no"},
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
			if name == "refused script" || name == "refused migration" {
				assert.Empty(t, w.apps, "nothing is written before the review")
				assert.Empty(t, w.migrations)
				assert.Empty(t, w.scripts)
			}
		})
	}
	noGate := bundler(newWorld())
	noGate.Reader, noGate.SaveGate = reader{data}, nil
	_, err := noGate.Import(ctx, admin, "mcp:resource:r1", fullMap)
	require.ErrorContains(t, err, "no script save gate")
	w := newWorld()
	b := bundler(w)
	_, err = b.Import(ctx, admin, "mcp:resource:r1", fullMap)
	require.ErrorContains(t, err, "no managed-resource library", "no reader configured")
	b.Reader = reader{data}
	_, err = b.Import(ctx, admin, " ", fullMap)
	require.ErrorContains(t, err, "name the bundle")
	_, err = b.Import(ctx, admin, "mcp:resource:other", fullMap)
	require.ErrorContains(t, err, "reading the bundle")
}

// refusedScript is data with its script's source one the save gate refuses.
func refusedScript(t *testing.T, data string) string {
	t.Helper()
	var b Bundle
	require.NoError(t, json.Unmarshal([]byte(data), &b))
	b.Scripts[0].Source += "# REFUSE\n"
	out, err := json.Marshal(b)
	require.NoError(t, err)
	return string(out)
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

// testedSource is a script whose test replays dpx_1, which calls the
// connection the import maps.
const testedSource = "def main():\n    \"\"\"Reads one row.\"\"\"\n" +
	"    platform.query(\"SELECT 1\", connection = \"orders\")\n\n" +
	"def test_main():\n    \"\"\"Replays a run.\"\"\"\n    out = testing.replay(\"dpx_1\")\n    assert.eq(out.result, None)\n"

// testedWorld is a source deployment whose script's tests replay dpx_1.
func testedWorld(t *testing.T) *world {
	t.Helper()
	w := source(t)
	w.versions[apiID][0].Source = testedSource
	r := scriptrec.NewRecorder(scriptrec.Header{RunID: "dpx_1"})
	r.OnCall("trino_query", map[string]any{"connection": "orders", "sql": "SELECT 1"}, map[string]any{"rows": []any{}}, nil)
	data, reason := r.Finish()
	require.Empty(t, reason)
	w.recordings["dpx_1"] = &scriptrec.Stored{RunID: "dpx_1", Data: data}
	return w
}

// A script's test recordings travel in the bundle, renamed by the mapping,
// reach the target's save gate, and are stored and kept for the script the
// import writes (#2476).
func TestImport_CarriesTheRecordingsAScriptsTestsReplay(t *testing.T) {
	src := testedWorld(t)
	_, err := bundler(src).Export(context.Background(), admin, "shop", 1)
	require.NoError(t, err)
	var bundle Bundle
	require.NoError(t, json.Unmarshal(src.landed, &bundle))
	require.Len(t, bundle.Scripts[0].Recordings, 1)
	assert.Equal(t, "dpx_1", bundle.Scripts[0].Recordings[0].RunID)

	w := newWorld()
	b := bundler(w)
	b.Reader = reader{string(src.landed)}
	_, err = b.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
	require.NoError(t, err)
	require.NotEmpty(t, w.gated)
	carried := w.gated[len(w.gated)-1].Carried["dpx_1"]
	require.NotNil(t, carried, "the gate replays the carried recording")
	rec, err := scriptrec.Decode(carried)
	require.NoError(t, err)
	args := map[string]any{"connection": "orders-prod", "sql": "SELECT 1"}
	assert.Equal(t, args, rec.Calls[0].Args, "the recording is renamed as the source is")
	assert.Equal(t, scriptrec.ToolKey("trino_query", args), rec.Calls[0].Key)

	stored := w.recordings["dpx_1"]
	require.NotNil(t, stored)
	var id string
	for k := range w.scripts {
		id = k
	}
	assert.Equal(t, id, stored.ScriptID)
	assert.Equal(t, scriptrec.KindDraft, stored.Kind)
	assert.Equal(t, admin.Email, stored.RecordedBy)
	assert.Equal(t, []string{"dpx_1"}, w.kept[id])

	// The same bundle again finds the recording held with the same content.
	_, err = b.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
	require.NoError(t, err)

	// A recording held here under the same run id with other content is
	// never replaced.
	other := newWorld()
	other.recordings["dpx_1"] = &scriptrec.Stored{RunID: "dpx_1", ScriptID: "someone-else", Data: src.recordings["dpx_1"].Data}
	ob := bundler(other)
	ob.Reader = reader{string(src.landed)}
	_, err = ob.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
	require.ErrorContains(t, err, "recording dpx_1 is held here with other content")
	assert.Equal(t, "someone-else", other.recordings["dpx_1"].ScriptID)
	assert.Empty(t, other.apps)

	// One over a run's own cap, and one that does not read, refuse.
	for name, data := range map[string][]byte{"oversized": make([]byte, scriptrec.MaxBytes+1), "garbled": []byte("nope")} {
		t.Run(name, func(t *testing.T) {
			bundle.Scripts[0].Recordings[0].Data = data
			raw, err := json.Marshal(bundle)
			require.NoError(t, err)
			nb := bundler(newWorld())
			nb.Reader = reader{string(raw)}
			_, err = nb.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
			require.ErrorContains(t, err, "recording dpx_1")
		})
	}

	// A deployment that keeps no recordings refuses such a script.
	nr := bundler(newWorld())
	nr.Reader, nr.Recordings = reader{string(src.landed)}, nil
	_, err = nr.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
	require.ErrorContains(t, err, "keeps no recordings")
}

// unsaved is a recording store that cannot store.
type unsaved struct{ recs }

func (unsaved) Save(context.Context, scriptrec.Stored) error { return errors.New("disk full") }

// A carried recording that cannot be stored refuses the import naming it, and
// no part of the application is created past it.
func TestImport_ARecordingThatCannotBeStoredRefuses(t *testing.T) {
	src := testedWorld(t)
	_, err := bundler(src).Export(context.Background(), admin, "shop", 1)
	require.NoError(t, err)
	w := newWorld()
	b := bundler(w)
	b.Reader, b.Recordings = reader{string(src.landed)}, unsaved{recs{w}}
	_, err = b.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
	require.ErrorContains(t, err, "storing recording dpx_1")
	assert.Empty(t, w.endpoints, "nothing is bound past the refusal")
}

// An export whose script's tests replay a recording this deployment does not
// hold, or with no recording store, is refused naming it.
func TestExport_RefusesATestRecordingItDoesNotHold(t *testing.T) {
	w := testedWorld(t)
	delete(w.recordings, "dpx_1")
	_, err := bundler(w).Export(context.Background(), admin, "shop", 1)
	require.ErrorContains(t, err, "replay recording dpx_1, which this deployment does not hold")
	b := bundler(testedWorld(t))
	b.Recordings = nil
	_, err = b.Export(context.Background(), admin, "shop", 1)
	require.ErrorContains(t, err, "keeps no recordings to export")
}

// TestImport_AnApplicationWithNoSchemaTakesTheBundles: an application here
// that grew without a database takes the bundle's schema on as add_part adds
// one (#2539), and the import's migrations land in it.
func TestImport_AnApplicationWithNoSchemaTakesTheBundles(t *testing.T) {
	data := exported(t)
	w := newWorld()
	w.apps["app_shop"] = &application.Application{ID: "app_shop", Name: "shop", OwnerEmail: admin.Email, Parts: []application.Part{}}
	b := bundler(w)
	b.Reader = reader{data}
	out, err := b.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
	require.NoError(t, err)
	im, ok := out.(*Imported)
	require.True(t, ok)
	assert.False(t, im.Created)
	assert.Equal(t, "orders-prod", w.apps["app_shop"].Connection)
	assert.Equal(t, "shop", w.apps["app_shop"].Schema)
	require.Len(t, w.migrations, 1)
}
