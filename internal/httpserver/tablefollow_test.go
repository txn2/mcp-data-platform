package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpserver/tablesource"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	trinotoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// The follow is driven from the one place each kind's head moves (#1536), and
// this is the assembled path for a managed resource: the replace-content route
// writes a revision through resource.ReviseContent, the hook this root installs
// resolves the resource with no caller, and the registrar moves the following
// table onto the revision's directory before the route answers. Nothing here is
// hand-fed to the registrar; the DDL asserted is what the route caused.

// --- fakes ---

// followStore is an in-memory registration store with the writes a follow makes.
type followStore struct {
	mu   sync.Mutex
	rows map[string]tableregister.Registration
}

func newFollowStore() *followStore {
	return &followStore{rows: map[string]tableregister.Registration{}}
}

func (s *followStore) Insert(_ context.Context, r tableregister.Registration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[r.ID] = r
	return nil
}

func (s *followStore) Get(_ context.Context, id string) (*tableregister.Registration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return nil, tableregister.ErrNotFound
	}
	return &r, nil
}

func (s *followStore) ByName(_ context.Context, conn, catalog, schema, table string) (*tableregister.Registration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.Connection == conn && r.Catalog == catalog && r.Schema == schema && r.Table == table {
			return &r, nil
		}
	}
	return nil, nil //nolint:nilnil // a free name is an answer
}

func (s *followStore) BySource(_ context.Context, kind, sourceID string) ([]tableregister.Registration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []tableregister.Registration
	for _, r := range s.rows {
		if r.SourceKind == kind && r.SourceID == sourceID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (*followStore) ForSources(context.Context, string, []string) (map[string][]tableregister.Registration, error) {
	return map[string][]tableregister.Registration{}, nil
}

func (*followStore) List(context.Context, tableregister.Filter) ([]tableregister.Registration, int, error) {
	return nil, 0, nil
}

func (s *followStore) Relocate(_ context.Context, id, location string, columns []tableregister.Column) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return tableregister.ErrNotFound
	}
	r.Location, r.Columns, r.FollowError = location, columns, ""
	s.rows[id] = r
	return nil
}

func (s *followStore) RecordFollowFailure(_ context.Context, id, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return tableregister.ErrNotFound
	}
	r.FollowError = reason
	s.rows[id] = r
	return nil
}

func (s *followStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	return nil
}

// followTrino records the statements a follow runs.
type followTrino struct {
	statements []string
	err        error
}

func (f *followTrino) Exec(_ context.Context, _, sql string) error {
	f.statements = append(f.statements, sql)
	return f.err
}

func (*followTrino) ScratchTarget(string) (trinotoolkit.ScratchConfig, bool) {
	return trinotoolkit.ScratchConfig{Catalog: "scratch", Schema: "uploads"}, true
}

func (*followTrino) AcceptsWrites(string) bool { return true }

// followObjects is one bucket: the resource route writes revisions into it,
// and the registrar reads heads and lists directories out of it.
type followObjects struct {
	*reviseObjects
}

func (o followObjects) ListDirectory(_ context.Context, bucket, prefix string) ([]s3adapter.ObjectEntry, bool, error) {
	var out []s3adapter.ObjectEntry
	for key, data := range o.put {
		if strings.HasPrefix(key, bucket+"/"+prefix) {
			out = append(out, s3adapter.ObjectEntry{Key: strings.TrimPrefix(key, bucket+"/"), Size: int64(len(data))})
		}
	}
	return out, false, nil
}

// --- the assembled path ---

type followHarness struct {
	registrar *tableregister.Registrar
	store     *followStore
	trino     *followTrino
	objects   followObjects
	res       *resource.Resource
	handler   http.Handler
	hooks     TableSourceHooks
}

func newFollowHarness(t *testing.T) *followHarness {
	t.Helper()
	res := &resource.Resource{
		ID: "res_1", Scope: resource.ScopeGlobal, Filename: "stores.csv", DisplayName: "Stores",
		MIMEType: "text/csv", S3Key: "resources/global/reference/res_1/stores.csv", UploaderSub: "u1",
	}
	objects := followObjects{newReviseObjects()}
	require.NoError(t, objects.PutObject(context.Background(), "managed-resources", res.S3Key,
		[]byte("store_id,name\n101,Mill Road\n"), "text/csv"))
	resources := &reviseResourceStore{res: res, reviseResourceVersions: &reviseResourceVersions{res: res}}

	h := &followHarness{store: newFollowStore(), trino: &followTrino{}, objects: objects, res: res}
	n := 0
	h.registrar = tableregister.New(tableregister.Deps{
		Store:   h.store,
		Trino:   h.trino,
		Objects: map[string]tableregister.ObjectReader{tableregister.KindResource: objectReaderAdapter{client: objects}},
		NewID:   func() (string, error) { n++; return "reg_" + strconv.Itoa(n), nil },
	})
	follower := sourceFollower{registrar: h.registrar, locate: tablesource.Locator(resources, "managed-resources", nil)}
	h.hooks = TableSourceHooks{
		ResourceRevised: func(ctx context.Context, id string, version int) []string {
			return follower.follow(ctx, tableregister.KindResource, id, version)
		},
	}

	h.handler = resource.NewHandler(resource.Deps{
		Store:     resources,
		S3Client:  objects,
		S3Bucket:  "managed-resources",
		URIScheme: "mcp",
		Versions:  resources.reviseResourceVersions,
		OnRevised: h.hooks.ResourceRevised,
	}, func(*http.Request) (*resource.Claims, error) {
		return &resource.Claims{Sub: "u1", Email: "alice@example.com", IsAdmin: true}, nil
	}, nil)
	return h
}

// register makes the table over the resource the way the tool or the portal
// would, with the follow choice given.
func (h *followHarness) register(t *testing.T, follow bool) tableregister.Registration {
	t.Helper()
	src, ok := tablesource.Locator(&reviseResourceStore{res: h.res}, "managed-resources", nil)(
		context.Background(), tableregister.KindResource, h.res.ID)
	require.True(t, ok)
	reg, err := h.registrar.Register(context.Background(),
		tableregister.Caller{UserID: "u1", Email: "alice@example.com", Persona: "analyst"}, src,
		tableregister.Request{Connection: "scratch", TableName: "stores", Follow: follow})
	require.NoError(t, err)
	h.trino.statements = nil
	return reg.Registration
}

// replaceContent posts a new revision through the REST route.
func (h *followHarness) replaceContent(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "stores.csv")
	require.NoError(t, err)
	_, err = part.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resources/res_1/content", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)
	return w
}

// TestResourceRevision_MovesAFollowingTableBeforeTheRouteAnswers is the
// acceptance criterion end to end: a resource registered with follow, then
// written through replace-content, has its table rebuilt over the revision's
// directory with the revision's header before the response is sent, the
// response names the table, and the registration is current.
func TestResourceRevision_MovesAFollowingTableBeforeTheRouteAnswers(t *testing.T) {
	h := newFollowHarness(t)
	reg := h.register(t, true)

	w := h.replaceContent(t, "store_id,name,region\n101,Mill Road,west\n")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "res_1", body["id"], "the response is still the resource")
	assert.Equal(t, []any{
		"scratch.uploads.analyst_stores on scratch now reads version 2. Its columns changed with the file.",
	}, body["tables"])

	// The revision's directory is the one the route wrote the object into.
	require.Contains(t, h.res.S3Key, "/res_1/v/")
	newDir := tableregister.DirectoryOf(h.res.S3Key)
	require.Len(t, h.trino.statements, 3)
	assert.Equal(t, `DROP TABLE IF EXISTS "scratch"."uploads"."analyst_stores"`, h.trino.statements[1])
	assert.Contains(t, h.trino.statements[2], `external_location = 's3://managed-resources/`+newDir+`'`)
	assert.Contains(t, h.trino.statements[2], `"region" VARCHAR`)

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.False(t, stored.IsStale("managed-resources", h.res.S3Key), "current from the moment the write returns")
	assert.Len(t, stored.Columns, 3)
}

// TestResourceRevision_LeavesAPinnedTableAndSaysSo is the other half of the
// criterion: the default registration is left behind, and the write that put
// it there names it.
func TestResourceRevision_LeavesAPinnedTableAndSaysSo(t *testing.T) {
	h := newFollowHarness(t)
	reg := h.register(t, false)

	w := h.replaceContent(t, "store_id,name\n102,Oak Street\n")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	tables, _ := body["tables"].([]any)
	require.Len(t, tables, 1)
	assert.Contains(t, tables[0], "scratch.uploads.analyst_stores on scratch is pinned")
	assert.Contains(t, tables[0], "with follow left on")
	assert.Empty(t, h.trino.statements)

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.True(t, stored.IsStale("managed-resources", h.res.S3Key), "behind the file, as before")
}

// TestResourceRevision_AFailedFollowNeverFailsTheWrite: the coordinator
// refuses the statement; the revision is recorded and answered 200, the
// registration is behind with the failure on it, and the response says so.
func TestResourceRevision_AFailedFollowNeverFailsTheWrite(t *testing.T) {
	h := newFollowHarness(t)
	reg := h.register(t, true)
	h.trino.err = errors.New("coordinator down")

	w := h.replaceContent(t, "store_id,name\n102,Oak Street\n")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, h.res.S3Key, "/res_1/v/", "the revision was written")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	tables, _ := body["tables"].([]any)
	require.Len(t, tables, 1)
	assert.Contains(t, tables[0], "could not be moved to version 2")
	assert.Contains(t, tables[0], "coordinator down")

	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Contains(t, stored.FollowError, "coordinator down")
	assert.True(t, stored.IsStale("managed-resources", h.res.S3Key))
}

// TestFollowSource_UnresolvableRecordReportsNothing: the write happened, and
// a record the locator cannot find has no table to say anything about.
func TestFollowSource_UnresolvableRecordReportsNothing(t *testing.T) {
	h := newFollowHarness(t)
	follower := sourceFollower{
		registrar: h.registrar,
		locate:    tablesource.Locator(&reviseResourceStore{getErr: errors.New("away")}, "managed-resources", nil),
	}
	assert.Nil(t, follower.follow(context.Background(), tableregister.KindResource, "res_1", 2))
}
