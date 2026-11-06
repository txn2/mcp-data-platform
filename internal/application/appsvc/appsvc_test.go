package appsvc

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appschema"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// memStore is an in-memory application.Store.
type memStore struct {
	apps     map[string]*application.Application
	releases map[string][]application.Release
	held     map[string]int // kind/id -> released version
	failRec  error
}

func newMem() *memStore {
	return &memStore{apps: map[string]*application.Application{}, releases: map[string][]application.Release{}, held: map[string]int{}}
}

func (m *memStore) Create(_ context.Context, a *application.Application) error {
	for _, x := range m.apps {
		if x.Name == a.Name {
			return application.ErrNameTaken
		}
	}
	a.ID = "app_" + a.Name
	cp := *a
	m.apps[a.ID] = &cp
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*application.Application, error) {
	a, ok := m.apps[id]
	if !ok {
		return nil, application.ErrNotFound
	}
	cp := *a
	cp.Parts = slices.Clone(a.Parts)
	return &cp, nil
}

func (m *memStore) GetByName(ctx context.Context, name string) (*application.Application, error) {
	for id, a := range m.apps {
		if a.Name == name {
			return m.Get(ctx, id)
		}
	}
	return nil, application.ErrNotFound
}

func (m *memStore) List(context.Context) ([]application.Application, error) {
	out := make([]application.Application, 0, len(m.apps))
	for _, a := range m.apps {
		out = append(out, *a)
	}
	return out, nil
}

func (m *memStore) Update(_ context.Context, a *application.Application) error {
	m.apps[a.ID].Description, m.apps[a.ID].OwnerEmail = a.Description, a.OwnerEmail
	return nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	delete(m.apps, id)
	return nil
}

func (m *memStore) AddPart(_ context.Context, id string, p application.Part) error {
	for _, a := range m.apps {
		if slices.ContainsFunc(a.Parts, func(x application.Part) bool { return x.Kind == p.Kind && x.ID == p.ID }) {
			return application.ErrPartTaken
		}
	}
	m.apps[id].Parts = append(m.apps[id].Parts, p)
	return nil
}

func (m *memStore) RemovePart(_ context.Context, id, kind, part string) error {
	a := m.apps[id]
	i := slices.IndexFunc(a.Parts, func(x application.Part) bool { return x.Kind == kind && x.ID == part })
	if i < 0 {
		return application.ErrNoPart
	}
	a.Parts = slices.Delete(a.Parts, i, i+1)
	return nil
}

func (m *memStore) Owning(ctx context.Context, kind, id string) (*application.Application, error) {
	for aid, a := range m.apps {
		if slices.ContainsFunc(a.Parts, func(x application.Part) bool { return x.Kind == kind && x.ID == id }) {
			return m.Get(ctx, aid)
		}
	}
	return nil, application.ErrNotFound
}

func (m *memStore) Releases(_ context.Context, id string) ([]application.Release, error) {
	return m.releases[id], nil
}

func (m *memStore) Release(_ context.Context, id string, n int) (*application.Release, error) {
	for _, r := range m.releases[id] {
		if r.Number == n {
			cp := r
			return &cp, nil
		}
	}
	return nil, application.ErrNoRelease
}

func (m *memStore) Record(_ context.Context, id string, r application.Recorded) (*application.Release, error) {
	if m.failRec != nil {
		return nil, m.failRec
	}
	a := m.apps[id]
	if a.CurrentRelease != r.After {
		return nil, application.ErrStale
	}
	rel := r.Release
	rel.Number = a.CurrentRelease + 1
	a.CurrentRelease = rel.Number
	m.releases[id] = append(m.releases[id], rel)
	for _, p := range rel.Pins {
		m.held[p.Kind+"/"+p.PartID] = p.Version
	}
	return &rel, nil
}

type fakeScripts struct {
	scripts map[string]*script.Script
}

func (f *fakeScripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	return f.scripts[id], nil
}

func (f *fakeScripts) GetVersion(_ context.Context, id string, v int) (*script.Version, error) {
	sc := f.scripts[id]
	if sc == nil || v > sc.Version {
		return nil, nil //nolint:nilnil // the version store's contract: nil, nil is no such version
	}
	return &script.Version{ScriptID: id, Version: v}, nil
}

type fakeAssets struct {
	assets map[string]*portaldomain.Asset
}

func (f *fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	a, ok := f.assets[id]
	if !ok {
		return nil, errors.New("no rows")
	}
	return a, nil
}

func (f *fakeAssets) GetByVersion(_ context.Context, id string, v int) (*portaldomain.AssetVersion, error) {
	a, ok := f.assets[id]
	if !ok || v > a.CurrentVersion {
		return nil, errors.New("no rows")
	}
	return &portaldomain.AssetVersion{AssetID: id, Version: v}, nil
}

type fakeSources map[string]bool

func (f fakeSources) Exists(_ context.Context, name string) (bool, error) { return f[name], nil }

type fakeSchema struct {
	applied, pending []int
	failRelease      error
	kept             string
}

func (f *fakeSchema) State(context.Context, string, string) (*appschema.State, error) {
	top := 0
	for _, v := range f.applied {
		top = max(top, v)
	}
	return &appschema.State{Applied: top, Pending: f.pending}, nil
}

func (f *fakeSchema) Release(context.Context, string, string, string) (*appschema.Applied, error) {
	if f.failRelease != nil {
		return nil, f.failRelease
	}
	done := f.pending
	f.applied, f.pending = append(f.applied, f.pending...), nil
	top := 0
	for _, v := range f.applied {
		top = max(top, v)
	}
	return &appschema.Applied{Versions: done, Head: top}, nil
}

func (f *fakeSchema) Rollback(_ context.Context, _, _, _ string, to int) (*appschema.Reverted, error) {
	if f.kept != "" {
		return &appschema.Reverted{Versions: []int{}, Head: 9, Kept: f.kept}, nil
	}
	reverted := []int{}
	for _, v := range f.applied {
		if v > to {
			reverted = append(reverted, v)
		}
	}
	return &appschema.Reverted{Versions: reverted, Head: to}, nil
}

const (
	scriptA = "11111111-1111-1111-1111-111111111111"
	scriptB = "22222222-2222-2222-2222-222222222222"
)

var (
	jane  = application.Actor{Email: "jane@example.com"}
	joe   = application.Actor{Email: "joe@example.com"}
	admin = application.Actor{Email: "admin@example.com", Admin: true}
)

//nolint:revive // a fixture hands back each fake a test drives
func fixture() (*Service, *memStore, *fakeScripts, *fakeAssets, *fakeSchema) {
	store := newMem()
	scripts := &fakeScripts{scripts: map[string]*script.Script{
		scriptA: {ID: scriptA, Name: "shop-api", OwnerEmail: jane.Email, Version: 1},
		scriptB: {ID: scriptB, Name: "joes", OwnerEmail: joe.Email, Version: 1},
	}}
	assets := &fakeAssets{assets: map[string]*portaldomain.Asset{
		"as1": {ID: "as1", Name: "storefront", OwnerEmail: jane.Email, CurrentVersion: 1},
	}}
	schema := &fakeSchema{pending: []int{1, 2}}
	svc := &Service{
		Store: store, Scripts: scripts, Assets: assets, AssetVersions: assets,
		Sources: fakeSources{"orders-hook": true}, Schema: schema,
	}
	return svc, store, scripts, assets, schema
}

func TestCreateAndEdit(t *testing.T) {
	svc, _, _, _, _ := fixture()
	ctx := context.Background()
	_, err := svc.Create(ctx, application.Actor{}, application.Application{Name: "shop"})
	var refusedErr *RefusedError
	require.ErrorAs(t, err, &refusedErr, "an application belongs to a person with an email")
	_, err = svc.Create(ctx, jane, application.Application{Name: "Bad Name"})
	require.ErrorAs(t, err, &refusedErr)

	a, err := svc.Create(ctx, jane, application.Application{Name: "shop", Connection: "orders", Schema: "shop", OwnerEmail: "x@y"})
	require.NoError(t, err)
	assert.Equal(t, jane.Email, a.OwnerEmail, "the creator owns it whatever the request says")

	_, err = svc.Update(ctx, joe, "shop", "theirs", "")
	require.ErrorIs(t, err, application.ErrNotAllowed, "only the owner and administrators edit")
	_, err = svc.Update(ctx, jane, "shop", "mine", "joe@example.com")
	require.ErrorIs(t, err, application.ErrNotAllowed, "only an administrator moves the owner")
	got, err := svc.Update(ctx, admin, a.ID, "moved", "joe@example.com")
	require.NoError(t, err)
	assert.Equal(t, "joe@example.com", got.OwnerEmail)

	require.ErrorIs(t, svc.Delete(ctx, jane, "shop"), application.ErrNotAllowed)
	require.NoError(t, svc.Delete(ctx, joe, "shop"))
	_, err = svc.Find(ctx, "shop")
	require.ErrorIs(t, err, application.ErrNotFound)
}

// TestCreateRefusesAConnectionThatIsNoApplicationDatabase is #2491's server
// half: a connection naming no appdb connection is refused at create, naming
// the way out, instead of being stored and reported later as a problem.
func TestCreateRefusesAConnectionThatIsNoApplicationDatabase(t *testing.T) {
	svc, store, _, _, _ := fixture()
	svc.HasConnection = func(name string) bool { return name == "orders" }
	ctx := context.Background()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop", Connection: "appdb-qq", Schema: "shop"})
	var refusedErr *RefusedError
	require.ErrorAs(t, err, &refusedErr)
	assert.Contains(t, err.Error(), `connection "appdb-qq" is not an application database connection`)
	assert.Contains(t, err.Error(), "leave connection and schema empty")
	_, err = svc.Find(ctx, "shop")
	require.ErrorIs(t, err, application.ErrNotFound, "nothing was stored")
	assert.Empty(t, store.apps)

	_, err = svc.Create(ctx, jane, application.Application{Name: "shop", Connection: "orders", Schema: "shop"})
	require.NoError(t, err)
	_, err = svc.Create(ctx, jane, application.Application{Name: "notes"})
	require.NoError(t, err, "no database is still allowed")
}

func TestAddPart_OwnershipAndExclusivity(t *testing.T) {
	svc, _, _, _, _ := fixture()
	ctx := context.Background()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop"})
	require.NoError(t, err)

	var refusedErr *RefusedError
	_, err = svc.AddPart(ctx, jane, "shop", "table", "x")
	require.ErrorAs(t, err, &refusedErr)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindScript, "not-a-uuid")
	require.ErrorAs(t, err, &refusedErr)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindScript, scriptB)
	require.ErrorIs(t, err, application.ErrNotAllowed, "a script is added by its owner")
	_, err = svc.AddPart(ctx, jane, "shop", application.KindSource, "orders-hook")
	require.ErrorIs(t, err, application.ErrNotAllowed, "webhook sources are an administrator's")
	_, err = svc.AddPart(ctx, admin, "shop", application.KindSource, "missing")
	require.ErrorAs(t, err, &refusedErr)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindAsset, "nope")
	require.ErrorAs(t, err, &refusedErr)
	_, err = svc.AddPart(ctx, joe, "shop", application.KindAsset, "as1")
	require.ErrorIs(t, err, application.ErrNotAllowed, "joe does not edit jane's application")

	a, err := svc.AddPart(ctx, jane, "shop", application.KindScript, scriptA)
	require.NoError(t, err)
	assert.Len(t, a.Parts, 1)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindAsset, "as1")
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, admin, "shop", application.KindSource, "orders-hook")
	require.NoError(t, err)

	_, err = svc.Create(ctx, jane, application.Application{Name: "other"})
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, jane, "other", application.KindScript, scriptA)
	require.ErrorIs(t, err, application.ErrPartTaken)
	assert.Contains(t, err.Error(), `application "shop"`, "the refusal names the application holding it")

	a, err = svc.RemovePart(ctx, jane, "shop", application.KindAsset, "as1")
	require.NoError(t, err)
	assert.Len(t, a.Parts, 2)
	_, err = svc.RemovePart(ctx, jane, "shop", application.KindAsset, "as1")
	require.ErrorIs(t, err, application.ErrNoPart)
}

//nolint:revive // a fixture hands back each fake a test drives
func released(t *testing.T) (*Service, *memStore, *fakeScripts, *fakeAssets, *fakeSchema) {
	t.Helper()
	svc, store, scripts, assets, schema := fixture()
	ctx := context.Background()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop", Connection: "orders", Schema: "shop"})
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindScript, scriptA)
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindAsset, "as1")
	require.NoError(t, err)
	return svc, store, scripts, assets, schema
}

func TestRelease(t *testing.T) {
	svc, store, scripts, assets, schema := released(t)
	ctx := context.Background()

	_, err := svc.Release(ctx, jane, "shop", "")
	require.ErrorIs(t, err, application.ErrNotAllowed, "the owner does not release; an administrator does")

	st, err := svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	assert.Nil(t, st.Current)
	assert.Len(t, st.Pending, 2, "both parts would be pinned")
	assert.Equal(t, []int{1, 2}, st.Migrations.Pending)

	rel, err := svc.Release(ctx, admin, "shop", "first")
	require.NoError(t, err)
	assert.Equal(t, 1, rel.Number)
	assert.Equal(t, application.ActionRelease, rel.Action)
	assert.Equal(t, []int{1, 2}, rel.Changes.Applied)
	assert.Equal(t, 2, rel.MigrationVersion)
	assert.Equal(t, 1, store.held["script/"+scriptA])

	scripts.scripts[scriptA].Version = 2
	assets.assets["as1"].CurrentVersion = 3
	st, err = svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, 1, st.Current.Number)
	require.Len(t, st.Pending, 2)

	schema.failRelease = errors.New("migration 3 failed")
	_, err = svc.Release(ctx, admin, "shop", "")
	require.ErrorContains(t, err, "nothing was released")
	assert.Equal(t, 1, store.apps["app_shop"].CurrentRelease, "a release whose migration fails changes nothing")
	schema.failRelease = nil

	rel, err = svc.Release(ctx, admin, "shop", "second")
	require.NoError(t, err)
	assert.Equal(t, 2, rel.Number)
	assert.ElementsMatch(t, []application.PinChange{
		{Kind: application.KindScript, PartID: scriptA, From: 1, To: 2},
		{Kind: application.KindAsset, PartID: "as1", From: 1, To: 3},
	}, rel.Changes.Parts)

	delete(scripts.scripts, scriptA)
	_, err = svc.Release(ctx, admin, "shop", "")
	require.ErrorContains(t, err, "nothing was released: script", "a part that cannot be pinned refuses the release")
}

func TestRollback(t *testing.T) {
	svc, store, scripts, _, schema := released(t)
	ctx := context.Background()
	_, err := svc.Rollback(ctx, admin, "shop", 0)
	var refusedErr *RefusedError
	require.ErrorAs(t, err, &refusedErr, "nothing to roll back to before two releases")

	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	scripts.scripts[scriptA].Version = 2
	schema.pending = []int{3}
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)

	_, err = svc.Rollback(ctx, jane, "shop", 0)
	require.ErrorIs(t, err, application.ErrNotAllowed)
	rel, err := svc.Rollback(ctx, admin, "shop", 0)
	require.NoError(t, err)
	assert.Equal(t, application.ActionRollback, rel.Action)
	assert.Equal(t, 1, rel.Restored)
	assert.Equal(t, []int{3}, rel.Changes.Reverted)
	assert.Equal(t, 1, store.held["script/"+scriptA], "the earlier script version is back behind the endpoint")

	schema.kept = "migration 3 has no down migration, so no migration was reverted"
	rel, err = svc.Rollback(ctx, admin, "shop", 2)
	require.NoError(t, err)
	assert.Equal(t, schema.kept, rel.Changes.MigrationsKept)

	scripts.scripts[scriptA].Version = 0 // the pinned version can no longer be read
	_, err = svc.Rollback(ctx, admin, "shop", 1)
	require.ErrorContains(t, err, "cannot be read")
}

func TestStatus_NamesProblems(t *testing.T) {
	svc, store, _, _, _ := released(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, admin, "shop", application.KindSource, "orders-hook")
	require.NoError(t, err)
	svc.Sources = fakeSources{}
	store.apps["app_shop"].Parts = append(store.apps["app_shop"].Parts, application.Part{Kind: application.KindAsset, ID: "gone"})
	st, err := svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	assert.Len(t, st.Problems, 2)
}

func TestReleasedPartsAreAnAdministratorsToTakeOut(t *testing.T) {
	svc, store, _, _, _ := released(t)
	ctx := context.Background()
	_, err := svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	_, err = svc.RemovePart(ctx, jane, "shop", application.KindScript, scriptA)
	require.ErrorIs(t, err, application.ErrNotAllowed, "the owner may not take a pinned part out from behind the release")
	require.ErrorIs(t, svc.Delete(ctx, jane, "shop"), application.ErrNotAllowed, "nor delete a released application")
	_, err = svc.RemovePart(ctx, admin, "shop", application.KindScript, scriptA)
	require.NoError(t, err)
	store.failRec = nil
	require.NoError(t, svc.Delete(ctx, admin, "shop"))
}

func TestRecordStaleIsReported(t *testing.T) {
	svc, store, _, _, _ := released(t)
	store.failRec = application.ErrStale
	_, err := svc.Release(context.Background(), admin, "shop", "")
	require.ErrorIs(t, err, application.ErrStale)
}
