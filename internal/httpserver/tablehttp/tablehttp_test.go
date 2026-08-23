package tablehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	trinotoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// --- fakes ---

type fakeTrino struct {
	statements []string
	err        error
	hasTarget  bool
}

func (f *fakeTrino) Exec(_ context.Context, _, sql string) error {
	f.statements = append(f.statements, sql)
	return f.err
}

func (f *fakeTrino) ScratchTarget(string) (trinotoolkit.ScratchConfig, bool) {
	return trinotoolkit.ScratchConfig{Catalog: "scratch", Schema: "uploads"}, f.hasTarget
}

type fakeObjects struct {
	body    []byte
	entries []tableregister.ObjectEntry
	listErr error
}

func (f *fakeObjects) GetObject(_ context.Context, _, _ string) (body []byte, contentType string, err error) {
	return f.body, "text/csv", nil
}

// ListDirectory answers for the prefix it was asked about, the way the S3
// adapter's delimiter listing does: a corrected version of a file sits in its
// own directory, and a fake blind to the prefix would report the version it
// replaced as a sibling of it.
func (f *fakeObjects) ListDirectory(
	_ context.Context, _, prefix string,
) (entries []tableregister.ObjectEntry, truncated bool, err error) {
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	var under []tableregister.ObjectEntry
	for _, e := range f.entries {
		if strings.HasPrefix(e.Key, prefix) {
			under = append(under, e)
		}
	}
	return under, false, nil
}

// fakeReviser stands in for the version trail an asset already has: it records
// the corrected bytes, mints a per-version directory, and puts the new object
// in the listing so the registrar sees the directory it will point at.
type fakeReviser struct {
	objects *fakeObjects
	saved   [][]byte
}

func (f *fakeReviser) Revise(
	_ context.Context, src tableregister.Source, _ tableregister.Caller, content []byte, _ string,
) (tableregister.Revised, error) {
	f.saved = append(f.saved, content)
	key := tableregister.DirectoryOf(src.HeadKey) + "v/rev_1/content.csv"
	f.objects.entries = append(f.objects.entries, tableregister.ObjectEntry{Key: key})
	return tableregister.Revised{Bucket: src.Bucket, Key: key, Version: 2}, nil
}

// memStore is the in-memory Store the handler acts through, enforcing the same
// unique table name the migration's index does.
type memStore struct {
	mu   sync.Mutex
	rows map[string]tableregister.Registration
	err  error
}

func newMemStore() *memStore {
	return &memStore{rows: map[string]tableregister.Registration{}}
}

func (m *memStore) Insert(_ context.Context, r tableregister.Registration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[r.ID] = r
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*tableregister.Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, tableregister.ErrNotFound
	}
	return &r, nil
}

func (m *memStore) ByName(_ context.Context, conn, cat, sch, tbl string) (*tableregister.Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.Connection == conn && r.Catalog == cat && r.Schema == sch && r.Table == tbl {
			found := r
			return &found, nil
		}
	}
	return nil, nil //nolint:nilnil // a free name is an answer, not a failure
}

func (m *memStore) BySource(_ context.Context, kind, id string) ([]tableregister.Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	var out []tableregister.Registration
	for _, r := range m.rows {
		if r.SourceKind == kind && r.SourceID == id {
			out = append(out, r)
		}
	}
	return out, nil
}

func (*memStore) ForSources(
	_ context.Context, _ string, _ []string,
) (map[string][]tableregister.Registration, error) {
	return nil, nil //nolint:nilnil // the handler never reads this path
}

func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[id]; !ok {
		return tableregister.ErrNotFound
	}
	delete(m.rows, id)
	return nil
}

// --- harness ---

const csvBody = "store_id,vendor_code\n101,ACME-NW\n"

var owner = &portal.User{UserID: "u1", Email: "alice@example.com", Roles: []string{"analyst"}}

// tornCSV is the shape a spreadsheet export takes: a multi-line address in one
// quoted cell, which a line-based reader tears into fragments.
const tornCSV = "store_id,address\n101,\"12 Mill Rd\nSuite 4\"\n"

type harness struct {
	mux     *http.ServeMux
	store   *memStore
	trino   *fakeTrino
	assets  map[string]tableregister.Source
	user    *portal.User
	body    string
	reviser *fakeReviser
}

func newHarness(t *testing.T, opts ...func(*harness)) *harness {
	t.Helper()
	h := &harness{
		mux:   http.NewServeMux(),
		store: newMemStore(),
		trino: &fakeTrino{hasTarget: true},
		user:  owner,
		body:  csvBody,
		assets: map[string]tableregister.Source{
			"asset_1": tableregister.SourceFromAssetRecord(tableregister.Record{
				ID: "asset_1", Name: "Vendor keys", Bucket: "portal-assets",
				Key: "artifacts/u1/asset_1/content.csv", ContentType: "text/csv", OwnerID: "u1",
			}),
		},
	}
	for _, opt := range opts {
		opt(h)
	}

	objects := &fakeObjects{
		body:    []byte(h.body),
		entries: []tableregister.ObjectEntry{{Key: "artifacts/u1/asset_1/content.csv"}},
	}
	h.reviser = &fakeReviser{objects: objects}
	n := 0
	registrar := tableregister.New(tableregister.Deps{
		Store: h.store,
		Trino: h.trino,
		Objects: map[string]tableregister.ObjectReader{
			tableregister.KindAsset:    objects,
			tableregister.KindResource: objects,
		},
		Revisers: map[string]tableregister.Reviser{
			tableregister.KindAsset:    h.reviser,
			tableregister.KindResource: h.reviser,
		},
		NewID: func() (string, error) { n++; return "reg_" + strconv.Itoa(n), nil },
	})

	handler := New(Deps{
		Registrar: registrar,
		Assets: func(_ context.Context, id string, _ tableregister.Caller) (tableregister.Source, bool) {
			src, ok := h.assets[id]
			return src, ok
		},
		Connections: func(context.Context, *portal.User) []ConnectionChoice {
			return []ConnectionChoice{{Name: "scratch", Catalog: "scratch", Schema: "uploads"}}
		},
		Caller: func(u *portal.User) tableregister.Caller {
			return tableregister.Caller{UserID: u.UserID, Email: u.Email, Persona: "analyst"}
		},
	})
	require.NotNil(t, handler)

	// The wrap stands in for the portal authentication chain, which is what
	// puts the user on the request context.
	handler.Routes(h.mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h.user != nil {
				r = r.WithContext(portal.ContextWithUser(r.Context(), h.user))
			}
			next.ServeHTTP(w, r)
		})
	})
	return h
}

func (h *harness) do(method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, reader)
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, req)
	return w
}

// --- tests ---

func TestRegisterRoute_CreatesTheTable(t *testing.T) {
	h := newHarness(t)

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables",
		`{"connection":"scratch","table_name":"vendor keys"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var got registrationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "scratch.uploads.analyst_vendor_keys", got.QueryTable)
	assert.False(t, got.Stale)
	assert.Contains(t, got.SampleSQL, "CAST")
	assert.Contains(t, h.trino.statements[1], "CREATE TABLE")
}

func TestListRoute_ReportsWhatIsRegistered(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, http.StatusCreated,
		h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `{"connection":"scratch"}`).Code)

	w := h.do(http.MethodGet, "/api/v1/portal/assets/asset_1/tables", "")
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Registrations []registrationView `json:"registrations"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Registrations, 1)
	assert.Equal(t, "scratch.uploads.analyst_content", body.Registrations[0].QueryTable)
}

// TestListRoute_EmptyIsAnArrayNotNull so a client can render a list without
// distinguishing "none" from "the field is missing".
func TestListRoute_EmptyIsAnArray(t *testing.T) {
	h := newHarness(t)
	w := h.do(http.MethodGet, "/api/v1/portal/assets/asset_1/tables", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"registrations":[]`)
}

func TestUnregisterRoute_DropsTheTable(t *testing.T) {
	h := newHarness(t)
	created := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `{"connection":"scratch"}`)
	require.Equal(t, http.StatusCreated, created.Code)
	var reg registrationView
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &reg))

	w := h.do(http.MethodDelete, "/api/v1/portal/assets/asset_1/tables/"+reg.ID, "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Contains(t, h.trino.statements[len(h.trino.statements)-1], "DROP TABLE IF EXISTS")
}

// TestResolve_UnknownRecordIsNotFound: a record the caller may not see and one
// that never existed are the same answer, so a probe learns nothing.
func TestResolve_UnknownRecordIsNotFound(t *testing.T) {
	h := newHarness(t)
	w := h.do(http.MethodGet, "/api/v1/portal/assets/asset_other/tables", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRoutes_RequireAuthentication(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.user = nil })

	assert.Equal(t, http.StatusUnauthorized,
		h.do(http.MethodGet, "/api/v1/portal/assets/asset_1/tables", "").Code)
	assert.Equal(t, http.StatusUnauthorized,
		h.do(http.MethodGet, "/api/v1/table-connections", "").Code)
}

func TestConnectionsRoute(t *testing.T) {
	h := newHarness(t)
	w := h.do(http.MethodGet, "/api/v1/table-connections", "")
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Connections []ConnectionChoice `json:"connections"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Connections, 1)
	assert.Equal(t, "scratch", body.Connections[0].Name)
	assert.Equal(t, "uploads", body.Connections[0].Schema)
}

func TestRegisterRoute_BadRequests(t *testing.T) {
	h := newHarness(t)

	assert.Equal(t, http.StatusBadRequest,
		h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `not json`).Code)

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "name the connection")
}

// TestRegisterRoute_RefusalCarriesItsReason: the platform's refusals name what
// to do next, so a surface passes them through rather than replacing them.
func TestRegisterRoute_RefusalCarriesItsReason(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.store.Insert(context.Background(), tableregister.Registration{
		ID: "reg_held", SourceKind: tableregister.KindAsset, SourceID: "asset_9",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "analyst_content", RegisteredBy: "bob@example.com",
	}))

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `{"connection":"scratch"}`)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "bob@example.com")
}

func TestRegisterRoute_NoScratchTarget(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.trino.hasTarget = false })

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `{"connection":"warehouse"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "scratch catalog")
}

// TestRegisterRoute_PlatformFailureIsNotAConflict pins the separation: a store
// or object-store outage is a 500 and does not hand the caller a wrapped
// driver error to read.
func TestRegisterRoute_PlatformFailureIsNotAConflict(t *testing.T) {
	h := newHarness(t)
	// Rebuild with a listing that fails, which is a failure of the platform
	// rather than something the caller can resolve.
	h2 := newHarness(t)
	h2.mux = http.NewServeMux()
	failing := tableregister.New(tableregister.Deps{
		Store: h2.store,
		Trino: h2.trino,
		Objects: map[string]tableregister.ObjectReader{
			tableregister.KindAsset: &fakeObjects{listErr: errors.New("s3 unreachable")},
		},
		NewID: func() (string, error) { return "reg_a", nil },
	})
	handler := New(Deps{
		Registrar: failing,
		Assets: func(_ context.Context, id string, _ tableregister.Caller) (tableregister.Source, bool) {
			src, ok := h.assets[id]
			return src, ok
		},
		Caller: func(u *portal.User) tableregister.Caller {
			return tableregister.Caller{UserID: u.UserID, Email: u.Email, Persona: "analyst"}
		},
	})
	handler.Routes(h2.mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(portal.ContextWithUser(r.Context(), owner)))
		})
	})

	w := h2.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `{"connection":"scratch"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "s3 unreachable",
		"an internal failure's text is not handed to the caller")
}

func TestListRoute_StoreFailure(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.store.err = errors.New("connection refused") })

	w := h.do(http.MethodGet, "/api/v1/portal/assets/asset_1/tables", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "connection refused")
}

// TestNew_UnwiredRegistersNothing so a deployment that cannot register never
// advertises an action that would always refuse.
func TestNew_UnwiredRegistersNothing(t *testing.T) {
	assert.Nil(t, New(Deps{Registrar: tableregister.New(tableregister.Deps{})}))

	// A registrar with no way to resolve either kind is the same case.
	assert.Nil(t, New(Deps{Registrar: tableregister.New(tableregister.Deps{
		Store:   newMemStore(),
		Trino:   &fakeTrino{hasTarget: true},
		Objects: map[string]tableregister.ObjectReader{tableregister.KindAsset: &fakeObjects{}},
	})}))

	// Routes on a nil handler is a no-op rather than a panic.
	mux := http.NewServeMux()
	var nilHandler *Handler
	nilHandler.Routes(mux, func(h http.Handler) http.Handler { return h })
}

// TestKindRoutes_ResourcesAreServedToo pins that both kinds get routes from
// the one handler, which is the whole reason it is one handler.
func TestKindRoutes_ResourcesAreServedToo(t *testing.T) {
	mux := http.NewServeMux()
	objects := &fakeObjects{
		body:    []byte(csvBody),
		entries: []tableregister.ObjectEntry{{Key: "resources/global/global/res_1/keys.csv"}},
	}
	handler := New(Deps{
		Registrar: tableregister.New(tableregister.Deps{
			Store:   newMemStore(),
			Trino:   &fakeTrino{hasTarget: true},
			Objects: map[string]tableregister.ObjectReader{tableregister.KindResource: objects},
			NewID:   func() (string, error) { return "reg_a", nil },
		}),
		Resources: func(_ context.Context, id string, _ tableregister.Caller) (tableregister.Source, bool) {
			if id != "res_1" {
				return tableregister.Source{}, false
			}
			return tableregister.SourceFromResource(tableregister.Record{
				ID: "res_1", Name: "Keys", Bucket: "managed-resources",
				Key: "resources/global/global/res_1/keys.csv", ContentType: "text/csv", OwnerID: "u1",
			}), true
		},
		Caller: func(u *portal.User) tableregister.Caller {
			return tableregister.Caller{UserID: u.UserID, Email: u.Email, Persona: "analyst"}
		},
	})
	require.NotNil(t, handler)
	handler.Routes(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(portal.ContextWithUser(r.Context(), owner)))
		})
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/resources/res_1/tables", strings.NewReader(`{"connection":"scratch"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var got registrationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, tableregister.KindResource, got.SourceKind,
		"a registration over a resource is filed as one, which is what a delete sweep keys on")
	assert.Equal(t, "scratch.uploads.analyst_keys", got.QueryTable)
}

func TestStatusFor(t *testing.T) {
	assert.Equal(t, http.StatusForbidden, statusFor(tableregister.ErrConnectionDenied))
	assert.Equal(t, http.StatusUnauthorized, statusFor(tableregister.ErrNoIdentity))
	assert.Equal(t, http.StatusNotFound, statusFor(tableregister.ErrNotFound))
	assert.Equal(t, http.StatusServiceUnavailable, statusFor(tableregister.ErrUnavailable))
	assert.Equal(t, http.StatusBadRequest, statusFor(tableregister.ErrNotCSV))
	assert.Equal(t, http.StatusBadRequest, statusFor(tableregister.ErrEmptyHeader))
	assert.Equal(t, http.StatusBadRequest, statusFor(tableregister.ErrNoScratchTarget))
	assert.Equal(t, http.StatusConflict, statusFor(tableregister.ErrNameTaken))
	assert.Equal(t, http.StatusConflict, statusFor(tableregister.ErrRefused))
	assert.Equal(t, http.StatusInternalServerError, statusFor(errors.New("driver: bad connection")))
}

func TestDetailFor(t *testing.T) {
	assert.Equal(t, "the registration could not be completed",
		detailFor(errors.New("pq: relation does not exist"), http.StatusInternalServerError))
	assert.Equal(t, tableregister.ErrNotCSV.Error(),
		detailFor(tableregister.ErrNotCSV, http.StatusBadRequest))
}

// TestUnregisterRoute_UnknownRegistration: an id that is not there is a
// not-found, not a conflict.
func TestUnregisterRoute_UnknownRegistration(t *testing.T) {
	h := newHarness(t)
	w := h.do(http.MethodDelete, "/api/v1/portal/assets/asset_1/tables/reg_missing", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestUnregisterRoute_SomeoneElsesIsRefused: the shared schema's rule reaches
// the HTTP surface with its reason intact.
func TestUnregisterRoute_SomeoneElsesIsRefused(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.store.Insert(context.Background(), tableregister.Registration{
		ID: "reg_theirs", SourceKind: tableregister.KindAsset, SourceID: "asset_1",
		Connection: "scratch", Catalog: "scratch", Schema: "uploads",
		Table: "bob_keys", RegisteredBy: "bob@example.com",
	}))

	w := h.do(http.MethodDelete, "/api/v1/portal/assets/asset_1/tables/reg_theirs", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "bob@example.com")
}

// TestCallerOf_WithoutABuilderUsesTheAuthenticatedUser. A deployment that
// wires no Caller still identifies who is acting; what it loses is the persona
// and the administrator lift, so the connection boundary denies rather than
// admits.
func TestCallerOf_WithoutABuilder(t *testing.T) {
	h := &Handler{deps: Deps{}}
	got := h.callerOf(owner)
	assert.Equal(t, "u1", got.UserID)
	assert.Equal(t, "alice@example.com", got.Email)
	assert.Empty(t, got.Persona, "no persona means the connection boundary denies")
	assert.False(t, got.IsAdmin)
}

// TestConnectionsRoute_NoEnumeratorIsAnEmptyArray, which a form renders as "no
// connection here can hold a table" rather than failing to load.
func TestConnectionsRoute_NoEnumerator(t *testing.T) {
	mux := http.NewServeMux()
	handler := New(Deps{
		Registrar: tableregister.New(tableregister.Deps{
			Store:   newMemStore(),
			Trino:   &fakeTrino{hasTarget: true},
			Objects: map[string]tableregister.ObjectReader{tableregister.KindAsset: &fakeObjects{}},
		}),
		Assets: func(context.Context, string, tableregister.Caller) (tableregister.Source, bool) {
			return tableregister.Source{}, false
		},
	})
	require.NotNil(t, handler)
	handler.Routes(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(portal.ContextWithUser(r.Context(), owner)))
		})
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/table-connections", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"connections":[]`)
}

// --- a CSV a line-based reader cannot read (#1441) ---

// TestRegisterRoute_RefusesATornCSVWithACodeTheFormCanOffer is what makes the
// portal's offer possible: the detail is the sentence a person reads, and the
// problem type is the half a control can be keyed off, since prose is free to
// change.
func TestRegisterRoute_RefusesATornCSVWithACodeTheFormCanOffer(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.body = tornCSV })

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `{"connection":"scratch"}`)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))

	var problem httpjson.ProblemDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problem))
	assert.Equal(t, httpjson.ProblemTypePrefix+"csv-needs-repair", problem.Type)
	assert.Contains(t, problem.Detail, "line break inside a cell")
	assert.Contains(t, problem.Detail, "address")

	assert.Empty(t, h.reviser.saved, "an unasked refusal does not rewrite the file")
	assert.Empty(t, h.trino.statements)
}

// TestRegisterRoute_RepairSavesAVersionAndSaysWhatChanged is the second
// submission of the form: the corrected bytes become a version of the file, the
// table reads that version's directory, and the response says so.
func TestRegisterRoute_RepairSavesAVersionAndSaysWhatChanged(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.body = tornCSV })

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables",
		`{"connection":"scratch","repair":true}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var got registrationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Contains(t, got.Repaired, "version 2")
	assert.Contains(t, got.Repaired, "put 1 row back onto one line")
	assert.Equal(t, "s3://portal-assets/artifacts/u1/asset_1/v/rev_1/", got.Location)
	assert.False(t, got.Stale, "the registration points at the version it just wrote")

	require.Len(t, h.reviser.saved, 1)
	assert.Contains(t, string(h.reviser.saved[0]), "12 Mill Rd Suite 4")
}

// TestRegisterRoute_LineSafeFileSaysNothingExtra: the correction is reported
// only when there was one, so an ordinary registration is unchanged.
func TestRegisterRoute_LineSafeFileSaysNothingExtra(t *testing.T) {
	h := newHarness(t)

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables",
		`{"connection":"scratch","repair":true}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var got registrationView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Empty(t, got.Repaired)
	assert.Empty(t, h.reviser.saved)
}

// A 500's text is replaced, because a wrapped driver error says nothing a
// caller can act on -- but a correction that ran before the failure already
// changed their file, and it stays changed. That half is kept.
func TestRegisterRoute_APlatformFailureAfterACorrectionStillSaysTheFileChanged(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.body = tornCSV })
	h.trino.err = errors.New("trino coordinator unreachable at 10.0.0.7:8080")

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables",
		`{"connection":"scratch","repair":true}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())

	var problem httpjson.ProblemDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problem))
	assert.Contains(t, problem.Detail, "Saved version 2 of this file")
	assert.Contains(t, problem.Detail, "The table was not created")
	assert.NotContains(t, problem.Detail, "10.0.0.7",
		"the coordinator's address is still not repeated to the caller")

	require.Len(t, h.reviser.saved, 1, "the file did change, which is why it is said")
}

// A failure with no correction behind it says only what it always said.
func TestRegisterRoute_APlatformFailureWithNoCorrectionSaysNothingMore(t *testing.T) {
	h := newHarness(t)
	h.trino.err = errors.New("trino coordinator unreachable at 10.0.0.7:8080")

	w := h.do(http.MethodPost, "/api/v1/portal/assets/asset_1/tables", `{"connection":"scratch"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "the registration could not be completed")
	assert.NotContains(t, w.Body.String(), "10.0.0.7")
}
