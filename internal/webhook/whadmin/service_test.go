package whadmin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

var (
	ctx     = context.Background()
	now     = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	errBoom = errors.New("boom")
	tg      = whtable.Target{Connection: "scratch", Catalog: "scratch_resources", Schema: "uploads"}
)

type memSources struct {
	rows      map[string]whsource.Source
	err       map[string]error
	deleted   []string
	getErrAll error
}

func (m *memSources) List(context.Context) ([]whsource.Source, error) {
	if err := m.err["list"]; err != nil {
		return nil, err
	}
	out := make([]whsource.Source, 0, len(m.rows))
	for _, s := range m.rows {
		out = append(out, s)
	}
	return out, nil
}

func (m *memSources) Get(_ context.Context, name string) (whsource.Source, error) {
	if m.getErrAll != nil {
		return whsource.Source{}, m.getErrAll
	}
	s, ok := m.rows[name]
	if !ok {
		return whsource.Source{}, whsource.ErrNotFound
	}
	return s, nil
}

func (m *memSources) Create(_ context.Context, s whsource.Source) error {
	if err := m.err["create"]; err != nil {
		return err
	}
	m.rows[s.Name] = s
	if err := m.err["reread"]; err != nil {
		m.getErrAll = err
	}
	return nil
}

func (m *memSources) Update(_ context.Context, s whsource.Source) error {
	if err := m.err["update"]; err != nil {
		return err
	}
	m.rows[s.Name] = s
	return nil
}

func (m *memSources) Delete(_ context.Context, name string) error {
	if err := m.err["delete"]; err != nil {
		return err
	}
	m.deleted = append(m.deleted, name)
	delete(m.rows, name)
	return nil
}

type fakeTables struct {
	created, dropped, probed int
	err                      map[string]error
}

func (f *fakeTables) TargetFor(string) (whtable.Target, error) {
	if err := f.err["target"]; err != nil {
		return whtable.Target{}, err
	}
	return tg, nil
}

func (*fakeTables) S3Location(p string) string { return "s3://managed-resources/" + p }

func (f *fakeTables) Create(context.Context, whtable.Target, whsource.Source) error {
	f.created++
	return f.err["create"]
}

func (f *fakeTables) Probe(context.Context, whtable.Target, whsource.Source) error {
	f.probed++
	return f.err["probe"]
}

func (f *fakeTables) Drop(context.Context, whtable.Target, whsource.Source) error {
	f.dropped++
	return f.err["drop"]
}

type memRegs struct {
	rows map[string]tableregister.Registration
	err  map[string]error
}

func (m *memRegs) Insert(_ context.Context, r tableregister.Registration) error {
	if err := m.err["insert"]; err != nil {
		return err
	}
	m.rows[r.ID] = r
	return nil
}

func (m *memRegs) ByName(_ context.Context, c, cat, sch, table string) (*tableregister.Registration, error) {
	if err := m.err["byname"]; err != nil {
		return nil, err
	}
	for _, r := range m.rows {
		if r.Connection == c && r.Catalog == cat && r.Schema == sch && r.Table == table {
			return &r, nil
		}
	}
	return nil, nil //nolint:nilnil // a free name
}

func (m *memRegs) BySource(_ context.Context, kind, id string) ([]tableregister.Registration, error) {
	if err := m.err["bysource"]; err != nil {
		return nil, err
	}
	var out []tableregister.Registration
	for _, r := range m.rows {
		if r.SourceKind == kind && r.SourceID == id {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memRegs) Delete(_ context.Context, id string) error {
	if err := m.err["delete"]; err != nil {
		return err
	}
	delete(m.rows, id)
	return nil
}

type fakeWindows struct {
	ids []string
	err map[string]error
}

func (f *fakeWindows) Status(context.Context, string, time.Time) (whstore.Status, error) {
	return whstore.Status{Pending: 2}, f.err["status"]
}

func (f *fakeWindows) ResourceIDs(context.Context, string) ([]string, error) {
	return f.ids, f.err["ids"]
}

type fakeObjects struct {
	keys    []string
	deleted []string
	err     map[string]error
}

func (f *fakeObjects) ListKeys(_ context.Context, _, prefix string) ([]string, error) {
	if err := f.err["list"]; err != nil {
		return nil, err
	}
	var out []string
	for _, k := range f.keys {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeObjects) DeleteObject(_ context.Context, _, key string) error {
	if err := f.err["delete"]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, key)
	return nil
}

type fakeResources struct {
	deleted []string
	err     error
}

func (f *fakeResources) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.err
}

type rig struct {
	svc       *Service
	sources   *memSources
	tables    *fakeTables
	regs      *memRegs
	windows   *fakeWindows
	objects   *fakeObjects
	resources *fakeResources
	changed   int
	idErr     error
}

func newRig() *rig {
	r := &rig{
		sources:   &memSources{rows: map[string]whsource.Source{}, err: map[string]error{}},
		tables:    &fakeTables{err: map[string]error{}},
		regs:      &memRegs{rows: map[string]tableregister.Registration{}, err: map[string]error{}},
		windows:   &fakeWindows{err: map[string]error{}},
		objects:   &fakeObjects{err: map[string]error{}},
		resources: &fakeResources{},
	}
	r.svc = New(Deps{
		Sources: r.sources, Tables: r.tables, Registrations: r.regs, Windows: r.windows,
		Objects: r.objects, Resources: r.resources, Bucket: "managed-resources",
		Changed: func() { r.changed++ },
		NewID: func() (string, error) {
			if r.idErr != nil {
				return "", r.idErr
			}
			return "reg-1", nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return now },
	})
	return r
}

func input() whsource.Source {
	return whsource.Source{
		Name: "esp", Enabled: true, Connection: "scratch", CreatedBy: "admin@example.com",
		Auth: whsource.Auth{Mode: whsource.AuthHMAC, Secret: "k", SignatureHeader: "X-Sig"},
	}
}

func TestCreate(t *testing.T) {
	r := newRig()
	src, err := r.svc.Create(ctx, input())
	require.NoError(t, err)
	assert.Equal(t, whsource.DefaultBufferLimit, src.Config.BufferLimit, "defaults are applied")
	assert.Equal(t, 1, r.tables.created)
	assert.Equal(t, 1, r.tables.probed)
	assert.Equal(t, 1, r.changed)
	reg := r.regs.rows["reg-1"]
	assert.Equal(t, tableregister.KindWebhook, reg.SourceKind)
	assert.Equal(t, "esp", reg.SourceID)
	assert.Equal(t, "webhook_esp", reg.Table)
	assert.Equal(t, "s3://managed-resources/webhooks/esp/", reg.Location)
	assert.Len(t, reg.Columns, 11, "the eight event columns and dt, hour, minute")
	assert.Equal(t, "admin@example.com", reg.RegisteredBy)

	_, err = r.svc.Create(ctx, input())
	assert.ErrorIs(t, err, ErrExists)
}

func TestCreateSurvivesAFailedReread(t *testing.T) {
	r := newRig()
	r.sources.err["reread"] = errBoom
	src, err := r.svc.Create(ctx, input())
	require.NoError(t, err, "the source exists; only reading it back for its timestamps failed")
	assert.Equal(t, "esp", src.Name)
}

func TestCreateRefusals(t *testing.T) {
	cases := map[string]struct {
		breakIt func(*rig)
		in      func() whsource.Source
		want    error
		dropped int
	}{
		"invalid":          {in: func() whsource.Source { s := input(); s.Name = "Bad"; return s }, want: whsource.ErrInvalid},
		"lookup fails":     {breakIt: func(r *rig) { r.sources.getErrAll = errBoom }},
		"no scratch":       {breakIt: func(r *rig) { r.tables.err["target"] = whtable.ErrNoScratchTarget }, want: whtable.ErrNoScratchTarget},
		"name check fails": {breakIt: func(r *rig) { r.regs.err["byname"] = errBoom }},
		"name taken": {breakIt: func(r *rig) {
			r.regs.rows["other"] = tableregister.Registration{
				ID: "other", Connection: "scratch", Catalog: "scratch_resources",
				Schema: "uploads", Table: "webhook_esp", RegisteredBy: "someone@example.com",
			}
		}, want: ErrNameTaken},
		"create tables":      {breakIt: func(r *rig) { r.tables.err["create"] = errBoom }, want: ErrUnusable},
		"probe":              {breakIt: func(r *rig) { r.tables.err["probe"] = whtable.ErrRegisterDisabled }, want: whtable.ErrRegisterDisabled, dropped: 1},
		"store":              {breakIt: func(r *rig) { r.sources.err["create"] = errBoom }, dropped: 1},
		"registration":       {breakIt: func(r *rig) { r.regs.err["insert"] = errBoom }, dropped: 1},
		"registration id":    {breakIt: func(r *rig) { r.idErr = errBoom }, dropped: 1},
		"drop fails on undo": {breakIt: func(r *rig) { r.tables.err["probe"] = errBoom; r.tables.err["drop"] = errBoom }, want: ErrUnusable, dropped: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			if tc.breakIt != nil {
				tc.breakIt(r)
			}
			in := input()
			if tc.in != nil {
				in = tc.in()
			}
			_, err := r.svc.Create(ctx, in)
			require.Error(t, err)
			if tc.want != nil {
				assert.ErrorIs(t, err, tc.want)
			}
			if errors.Is(err, ErrUnusable) {
				assert.Contains(t, err.Error(), "the connection cannot hold this source's table: ",
					"the refusal carries the query engine's reason")
			}
			assert.Equal(t, tc.dropped, r.tables.dropped, "what was created is removed")
			assert.Empty(t, r.sources.rows, "no source is left behind")
			assert.Zero(t, r.changed)
		})
	}
}

func TestUpdateRotatesAndKeepsSecret(t *testing.T) {
	r := newRig()
	_, err := r.svc.Create(ctx, input())
	require.NoError(t, err)

	off := false
	src, err := r.svc.Update(ctx, "esp", Update{
		Enabled: &off,
		Auth:    whsource.Auth{Mode: whsource.AuthHMAC, SignatureHeader: "X-Other"},
		Config:  whsource.Config{BufferLimit: 100},
	})
	require.NoError(t, err)
	assert.False(t, src.Enabled)
	assert.Equal(t, "k", src.Auth.Secret, "an empty secret keeps the stored one")
	assert.Equal(t, "X-Other", src.Auth.SignatureHeader)
	assert.Equal(t, 100, src.Config.BufferLimit)

	src, err = r.svc.Update(ctx, "esp", Update{
		Auth:            whsource.Auth{Mode: whsource.AuthHMAC, Secret: "k2", SignatureHeader: "X-Other"},
		RotationOverlap: time.Hour,
	})
	require.NoError(t, err)
	assert.Equal(t, "k2", src.Auth.Secret)
	assert.Equal(t, "k", src.Auth.PreviousSecret)
	assert.Equal(t, now.Add(time.Hour), src.Auth.PreviousUntil)
	assert.False(t, src.Enabled, "enabled is kept when not sent")
	assert.Equal(t, 3, r.changed)
}

func TestPersona(t *testing.T) {
	r := newRig()
	r.svc.deps.PersonaExists = func(name string) bool { return name == "marketing" }
	in := input()
	in.Config.Persona = "nobody"
	_, err := r.svc.Create(ctx, in)
	assert.ErrorIs(t, err, whsource.ErrInvalid)
	assert.Zero(t, r.tables.created, "an unknown persona is refused before anything is made")

	in.Config.Persona = "marketing"
	_, err = r.svc.Create(ctx, in)
	require.NoError(t, err)
	src, err := r.svc.Update(ctx, "esp", Update{
		Auth:   whsource.Auth{Mode: whsource.AuthHMAC, SignatureHeader: "X-Sig"},
		Config: whsource.Config{Persona: "sales"},
	})
	require.NoError(t, err)
	assert.Equal(t, "marketing", src.Config.Persona, "the persona is fixed at creation")
}

func TestUpdateRefusals(t *testing.T) {
	r := newRig()
	_, err := r.svc.Update(ctx, "nope", Update{})
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = r.svc.Create(ctx, input())
	require.NoError(t, err)
	_, err = r.svc.Update(ctx, "esp", Update{Auth: whsource.Auth{Mode: "oauth"}})
	assert.ErrorIs(t, err, whsource.ErrInvalid)

	r.sources.err["update"] = errBoom
	_, err = r.svc.Update(ctx, "esp", Update{Auth: whsource.Auth{Mode: whsource.AuthHMAC, SignatureHeader: "X"}})
	assert.Error(t, err)
}

func TestGetAndList(t *testing.T) {
	r := newRig()
	_, _, err := r.svc.Get(ctx, "esp")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = r.svc.Create(ctx, input())
	require.NoError(t, err)
	src, st, err := r.svc.Get(ctx, "esp")
	require.NoError(t, err)
	assert.Equal(t, "esp", src.Name)
	assert.Equal(t, 2, st.Pending)

	r.windows.err["status"] = errBoom
	_, _, err = r.svc.Get(ctx, "esp")
	assert.Error(t, err)

	list, err := r.svc.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, whsource.HandshakeNone, list[0].Config.Handshake)
	r.sources.err["list"] = errBoom
	_, err = r.svc.List(ctx)
	assert.Error(t, err)
}

func TestDeleteRemovesEverything(t *testing.T) {
	r := newRig()
	_, err := r.svc.Create(ctx, input())
	require.NoError(t, err)
	r.windows.ids = []string{"window-a", "window-b"}
	r.objects.keys = []string{"webhooks/esp/raw/dt=2026-09-24/hour=10/minute=00/a.jsonl.gz", "webhooks/other/raw/x"}

	require.NoError(t, r.svc.Delete(ctx, "esp"))
	assert.Equal(t, 1, r.tables.dropped)
	assert.Equal(t, []string{"window-a", "window-b"}, r.resources.deleted)
	assert.Equal(t, []string{"webhooks/esp/raw/dt=2026-09-24/hour=10/minute=00/a.jsonl.gz"}, r.objects.deleted,
		"another source's objects are untouched")
	assert.Empty(t, r.regs.rows)
	assert.Equal(t, []string{"esp"}, r.sources.deleted)

	assert.ErrorIs(t, r.svc.Delete(ctx, "esp"), ErrNotFound)
}

func TestDeleteFailures(t *testing.T) {
	cases := map[string]func(*rig){
		"ids":           func(r *rig) { r.windows.err["ids"] = errBoom },
		"resource":      func(r *rig) { r.windows.ids = []string{"x"}; r.resources.err = errBoom },
		"list":          func(r *rig) { r.objects.err["list"] = errBoom },
		"object":        func(r *rig) { r.objects.keys = []string{"webhooks/esp/x"}; r.objects.err["delete"] = errBoom },
		"bysource":      func(r *rig) { r.regs.err["bysource"] = errBoom },
		"registration":  func(r *rig) { r.regs.err["delete"] = errBoom },
		"source delete": func(r *rig) { r.sources.err["delete"] = errBoom },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			_, err := r.svc.Create(ctx, input())
			require.NoError(t, err)
			breakIt(r)
			assert.Error(t, r.svc.Delete(ctx, "esp"))
		})
	}
	t.Run("connection gone", func(t *testing.T) {
		r := newRig()
		_, err := r.svc.Create(ctx, input())
		require.NoError(t, err)
		r.tables.err["target"] = errBoom
		require.NoError(t, r.svc.Delete(ctx, "esp"), "a source on a connection that is gone can still be deleted")
		assert.Zero(t, r.tables.dropped)
	})
}

func TestNewDefaults(t *testing.T) {
	s := New(Deps{})
	assert.NotNil(t, s.deps.Logger)
	assert.NotNil(t, s.deps.Now)
	s.deps.Changed()
}
