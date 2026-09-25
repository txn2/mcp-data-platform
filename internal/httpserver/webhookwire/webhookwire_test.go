package webhookwire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/webhook/whconfig"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

var (
	ctx     = context.Background()
	errBoom = errors.New("boom")
	start   = time.Date(2026, 9, 24, 7, 15, 0, 0, time.UTC)
)

// fakeS3 pages its listing two keys at a time.
type fakeS3 struct {
	keys    []string
	puts    map[string][]byte
	deleted []string
	err     error
}

func (f *fakeS3) PutObject(_ context.Context, in *s3client.PutObjectInput) (*s3client.PutObjectOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.puts[in.Key] = in.Body
	return &s3client.PutObjectOutput{}, nil
}

func (f *fakeS3) GetObject(_ context.Context, _, key string) (*s3client.ObjectContent, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &s3client.ObjectContent{Body: f.puts[key]}, nil
}

func (f *fakeS3) ListObjects(_ context.Context, _, prefix, _ string, _ int32, token string) (*s3client.ListObjectsOutput, error) { //nolint:revive // argument-limit: the upstream s3client interface fixes the arity
	if f.err != nil {
		return nil, f.err
	}
	start := 0
	if token != "" {
		_, _ = fmt.Sscanf(token, "%d", &start)
	}
	end := min(start+2, len(f.keys))
	out := &s3client.ListObjectsOutput{}
	for _, k := range f.keys[start:end] {
		out.Objects = append(out.Objects, s3client.ObjectInfo{Key: k})
	}
	if end < len(f.keys) {
		out.IsTruncated, out.NextContinueToken = true, fmt.Sprint(end)
	}
	_ = prefix
	return out, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, _, key string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func TestObjects(t *testing.T) {
	f := &fakeS3{keys: []string{"p/", "p/a", "p/b", "p/c", "p/d"}, puts: map[string][]byte{}}
	o := objects{c: f}
	keys, err := o.ListKeys(ctx, "b", "p/")
	require.NoError(t, err)
	assert.Equal(t, []string{"p/a", "p/b", "p/c", "p/d"}, keys, "every page is read; the prefix marker is not a key")

	require.NoError(t, o.PutObject(ctx, "b", "k", []byte("x"), "application/gzip"))
	got, err := o.GetObject(ctx, "b", "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("x"), got)
	require.NoError(t, o.DeleteObject(ctx, "b", "k"))
	assert.Equal(t, []string{"k"}, f.deleted)

	f.err = errBoom
	assert.Error(t, o.PutObject(ctx, "b", "k", nil, ""))
	_, err = o.GetObject(ctx, "b", "k")
	assert.Error(t, err)
	_, err = o.ListKeys(ctx, "b", "p/")
	assert.Error(t, err)
	assert.Error(t, o.DeleteObject(ctx, "b", "k"))
}

// fakeWriter records resource writes.
type fakeWriter struct {
	created  []resource.NewResource
	replaced []string
	rows     map[string]*resource.Resource
	err      map[string]error
	claims   resource.Claims
}

func (f *fakeWriter) Create(_ context.Context, in resource.NewResource, c resource.Claims) (*resource.Resource, error) {
	f.claims = c
	if err := f.err["create"]; err != nil {
		return nil, err
	}
	f.created = append(f.created, in)
	r := &resource.Resource{ID: "new", S3Key: "resources/global/global/new/" + in.Filename}
	f.rows[r.ID] = r
	return r, nil
}

func (f *fakeWriter) Replace(_ context.Context, id string, _ resource.RevisionUpload, _ resource.Claims) (*resource.Resource, int, error) {
	if err := f.err["replace"]; err != nil {
		return nil, 0, err
	}
	if _, ok := f.rows[id]; !ok {
		return nil, 0, resourcewrite.ErrNoSuchResource
	}
	f.replaced = append(f.replaced, id)
	r := &resource.Resource{ID: id, S3Key: "resources/global/global/" + id + "/v2/07.parquet"}
	f.rows[id] = r
	return r, 2, nil
}

func (f *fakeWriter) Get(_ context.Context, id string, _ resource.Claims) (*resource.Resource, error) {
	if err := f.err["get"]; err != nil {
		return nil, err
	}
	r, ok := f.rows[id]
	if !ok {
		return nil, resourcewrite.ErrNoSuchResource
	}
	return r, nil
}

func (f *fakeWriter) Delete(_ context.Context, id string, _ resource.Claims) (*resource.Resource, error) {
	if err := f.err["delete"]; err != nil {
		return nil, err
	}
	r, ok := f.rows[id]
	if !ok {
		return nil, resourcewrite.ErrNoSuchResource
	}
	delete(f.rows, id)
	return r, nil
}

type fakeByURI struct {
	found *resource.Resource
	err   error
}

func (f fakeByURI) GetByURI(context.Context, string) (*resource.Resource, error) {
	return f.found, f.err
}

func newWindowResources(byURI fakeByURI) (windowResources, *fakeWriter) {
	w := &fakeWriter{rows: map[string]*resource.Resource{}, err: map[string]error{}}
	return windowResources{w: w, byURI: byURI, uriScheme: "mcp"}, w
}

var esp = whsource.Source{Name: "esp"}

func TestWindowResourcesCreateThenRevise(t *testing.T) {
	h, w := newWindowResources(fakeByURI{err: sql.ErrNoRows})
	stored, err := h.Put(ctx, esp, start, "", []byte("parquet"))
	require.NoError(t, err)
	assert.Equal(t, "new", stored.ResourceID)
	require.Len(t, w.created, 1)
	in := w.created[0]
	assert.Equal(t, resource.ScopePersona, in.Scope, "a source naming no persona keeps its windows to administrators")
	assert.Equal(t, "admin", in.ScopeID, "the administrator persona, whose members find them in search")
	assert.Equal(t, "webhooks/esp/2026-09-24", in.Path)
	assert.Equal(t, "07-15.parquet", in.Filename, "named for the hour and minute the window starts at")
	assert.Equal(t, "esp 2026-09-24 07:15 UTC", in.DisplayName)
	assert.Equal(t, "application/vnd.apache.parquet", in.MIMEType)
	assert.Equal(t, []string{"webhook", "esp"}, in.Tags)
	assert.True(t, w.claims.IsAdmin, "the windows are written by the platform into the global scope")
	assert.Equal(t, systemPrincipal, w.claims.Sub)

	stored, err = h.Put(ctx, esp, start, "new", []byte("parquet v2"))
	require.NoError(t, err)
	assert.Equal(t, []string{"new"}, w.replaced)
	assert.Contains(t, stored.Key, "/v2/")
}

func TestWindowResourcesInTheConfiguredAdminPersona(t *testing.T) {
	h, w := newWindowResources(fakeByURI{err: sql.ErrNoRows})
	h.adminPersona = "platform-admins"
	_, err := h.Put(ctx, esp, start, "", []byte("x"))
	require.NoError(t, err)
	assert.Equal(t, "platform-admins", w.created[0].ScopeID)
}

func TestWindowResourcesInThePersonaLibrary(t *testing.T) {
	h, w := newWindowResources(fakeByURI{err: sql.ErrNoRows})
	src := whsource.Source{Name: "esp", Config: whsource.Config{Persona: "marketing"}}
	_, err := h.Put(ctx, src, start, "", []byte("x"))
	require.NoError(t, err)
	require.Len(t, w.created, 1)
	assert.Equal(t, resource.ScopePersona, w.created[0].Scope)
	assert.Equal(t, "marketing", w.created[0].ScopeID)
}

func TestWindowResourcesFindsOneAtItsAddress(t *testing.T) {
	h, w := newWindowResources(fakeByURI{})
	w.rows["earlier"] = &resource.Resource{ID: "earlier"}
	h.byURI = fakeByURI{found: &resource.Resource{ID: "earlier"}}
	stored, err := h.Put(ctx, esp, start, "", []byte("x"))
	require.NoError(t, err)
	assert.Equal(t, "earlier", stored.ResourceID, "an attempt that created the resource and stopped is revised, not duplicated")
	assert.Empty(t, w.created)
}

func TestWindowResourcesRecreatesAGoneResource(t *testing.T) {
	h, w := newWindowResources(fakeByURI{})
	stored, err := h.Put(ctx, esp, start, "deleted-by-hand", []byte("x"))
	require.NoError(t, err)
	assert.Equal(t, "new", stored.ResourceID)
	assert.Len(t, w.created, 1)
}

func TestWindowResourcesFailures(t *testing.T) {
	h, _ := newWindowResources(fakeByURI{err: errBoom})
	_, err := h.Put(ctx, esp, start, "", nil)
	assert.Error(t, err)

	h, w := newWindowResources(fakeByURI{})
	w.rows["r"] = &resource.Resource{ID: "r"}
	w.err["replace"] = errBoom
	_, err = h.Put(ctx, esp, start, "r", nil)
	assert.Error(t, err)

	h, w = newWindowResources(fakeByURI{})
	w.err["create"] = errBoom
	_, err = h.Put(ctx, esp, start, "", nil)
	assert.Error(t, err)
}

func TestWindowResourcesKeyAndDelete(t *testing.T) {
	h, w := newWindowResources(fakeByURI{})
	w.rows["r"] = &resource.Resource{ID: "r", S3Key: "resources/global/global/r/07.parquet"}
	key, ok, err := h.Key(ctx, "r")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "resources/global/global/r/07.parquet", key)
	_, ok, err = h.Key(ctx, "gone")
	require.NoError(t, err)
	assert.False(t, ok)
	w.err["get"] = errBoom
	_, _, err = h.Key(ctx, "r")
	assert.Error(t, err)

	require.NoError(t, h.Delete(ctx, "r"))
	require.NoError(t, h.Delete(ctx, "r"), "a resource already gone is not an error")
	w.err["delete"] = errBoom
	assert.Error(t, h.Delete(ctx, "x"))
}

type fakeExec struct {
	stmts []string
}

func (f *fakeExec) Exec(_ context.Context, _, stmt string) error {
	f.stmts = append(f.stmts, stmt)
	return nil
}

func (*fakeExec) ScratchTarget(c string) (trino.ScratchConfig, bool) {
	return trino.ScratchConfig{Catalog: "scratch_resources", Schema: "uploads"}, c == "scratch"
}

func (*fakeExec) AcceptsWrites(string) bool { return true }

func TestRawWindows(t *testing.T) {
	exec := &fakeExec{}
	r := rawWindows{tables: whtable.New(exec, "managed-resources")}
	require.NoError(t, r.EnsureRawWindow(ctx, whsource.Source{Name: "esp", Connection: "scratch"}, start))
	require.Len(t, exec.stmts, 1)
	assert.Contains(t, exec.stmts[0], "register_partition('uploads', 'webhook_esp_raw'")
	assert.Error(t, r.EnsureRawWindow(ctx, whsource.Source{Name: "esp", Connection: "none"}, start))
}

func TestNewRegistrationID(t *testing.T) {
	a, err := newRegistrationID()
	require.NoError(t, err)
	b, err := newRegistrationID()
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
	assert.Regexp(t, `^reg_[0-9a-f]{24}$`, a)
}

type dbOnly struct{ db *sql.DB }

func (d dbOnly) DB() *sql.DB { return d.db }

func freeAddress(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func TestAssembleAndLifecycle(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.MatchExpectationsInOrder(false)
	for range 4 {
		mock.ExpectQuery(`FROM webhook_sources`).WillReturnRows(sqlmock.NewRows([]string{"name"}))
	}
	mock.ExpectQuery(`UPDATE webhook_windows h`).WillReturnRows(sqlmock.NewRows([]string{"source"}))
	mock.ExpectExec(`DELETE FROM webhook_request_counts`).WillReturnResult(sqlmock.NewResult(0, 0))

	cfg := &platform.Config{}
	cfg.Webhooks.Receiver.Address = freeAddress(t)
	cfg.Webhooks.Compactor.Poll = time.Hour
	w := assemble(parts{
		db: dbOnly{db}, objects: objects{c: &fakeS3{puts: map[string][]byte{}}}, bucket: "managed-resources",
		tables: whtable.New(&fakeExec{}, "managed-resources"), cfg: cfg, replica: "host:8080",
	})
	require.NotNil(t, w.Service)
	require.NotNil(t, w.receiver)
	require.NotNil(t, w.compactor)

	mux := http.NewServeMux()
	w.Mount(mux)
	w.Start(ctx)
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+cfg.Webhooks.Receiver.Address+"/hooks/nope", http.NoBody)
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusNotFound && resp.Header.Get("X-Platform-Instance") != ""
	}, 5*time.Second, 20*time.Millisecond, "the receiver's own listener serves /hooks/")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/hooks/nope", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code, "the main listener serves /hooks/ too")
	w.Stop()
}

func TestAssembleRespectsSwitches(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	off := false
	cfg := &platform.Config{Webhooks: whconfig.Config{
		Receiver: whconfig.ReceiverConfig{Enabled: &off}, Compactor: whconfig.CompactorConfig{Enabled: &off},
	}}
	w := assemble(parts{db: dbOnly{db}, tables: whtable.New(&fakeExec{}, "b"), cfg: cfg})
	assert.Nil(t, w.receiver)
	assert.Nil(t, w.compactor)
	assert.NotNil(t, w.Service, "a replica that neither receives nor compacts still manages sources")
	mux := http.NewServeMux()
	w.Mount(mux)
	w.Start(ctx)
	w.Stop()

	var none *Webhooks
	none.Mount(mux)
	none.Start(ctx)
	none.Stop()
	assert.Nil(t, Build(nil, ":8080", nil))
}

// fakePlatform is the part of the platform build reads.
type fakePlatform struct {
	db       *sql.DB
	cfg      *platform.Config
	store    resource.Store
	blobs    resource.S3Client
	personas *persona.Registry
}

func (f fakePlatform) DB() *sql.DB                                 { return f.db }
func (f fakePlatform) Config() *platform.Config                    { return f.cfg }
func (f fakePlatform) ResourceStore() resource.Store               { return f.store }
func (f fakePlatform) ResourceS3Client() resource.S3Client         { return f.blobs }
func (fakePlatform) RegisterManagedResource(*resource.Resource)    {}
func (fakePlatform) UnregisterManagedResource(string)              {}
func (fakePlatform) RestEncryptor() *fieldcrypt.RestFieldEncryptor { return nil }
func (fakePlatform) Metrics() *observability.Metrics               { return nil }
func (f fakePlatform) PersonaRegistry() *persona.Registry          { return f.personas }

// nopBlobs stands in for the managed-resources blob client, which build
// hands to the resource writer and never calls itself.
type nopBlobs struct{}

func (nopBlobs) PutObject(context.Context, string, string, []byte, string) error { return nil }
func (nopBlobs) PutObjectStream(context.Context, string, string, io.Reader, string) (int64, error) {
	return 0, nil
}

func (nopBlobs) GetObject(context.Context, string, string) (body []byte, contentType string, err error) {
	return nil, "", nil
}
func (nopBlobs) DeleteObject(context.Context, string, string) error { return nil }

func buildConfig(connection string) *platform.Config {
	cfg := &platform.Config{}
	cfg.Toolkits = map[string]any{"s3": map[string]any{"instances": map[string]any{
		"resources": map[string]any{
			"region": "us-east-1", "endpoint": "http://127.0.0.1:1",
			"access_key_id": "k", "secret_access_key": "s", "use_path_style": true,
		},
	}}}
	cfg.Resources.Managed.S3Connection = connection
	cfg.Resources.Managed.S3Bucket = "managed-resources"
	off := false
	cfg.Webhooks.Receiver.Enabled = &off
	cfg.Webhooks.Compactor.Enabled = &off
	return cfg
}

func TestBuildFromThePlatform(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "marketing"}))
	p := fakePlatform{
		db: db, cfg: buildConfig("resources"), store: resource.NewPostgresStore(db),
		blobs: nopBlobs{}, personas: reg,
	}
	w := buildFrom(p, ":8080", &fakeExec{})
	require.NotNil(t, w)
	require.NotNil(t, w.Service)

	assert.Nil(t, buildFrom(p, ":8080", nil), "no Trino executor, no webhooks")
	p.blobs = nil
	assert.Nil(t, buildFrom(p, ":8080", &fakeExec{}), "no managed-resources blob client, no webhooks")
	p.blobs = nopBlobs{}
	p.cfg = buildConfig("not-configured")
	assert.Nil(t, buildFrom(p, ":8080", &fakeExec{}), "a managed-resources connection that is not configured")
}

func TestPersonaLookup(t *testing.T) {
	assert.Nil(t, personaLookup(nil))
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "marketing"}))
	exists := personaLookup(reg)
	assert.True(t, exists("marketing"))
	assert.False(t, exists("nobody"))
}
