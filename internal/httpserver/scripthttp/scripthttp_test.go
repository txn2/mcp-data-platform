package scripthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// reportSource is a script whose static read yields one capability and one
// connection, which is what a reviewer's grant has to cover.
const reportSource = `res = platform.query(connection="warehouse", sql="SELECT 1")
platform.export(name="daily", rows=res["rows"])
`

// stubStore serves the reads and the approval the review routes make.
type stubStore struct {
	scripts    []script.Script
	version    *script.Version
	approved   *script.Version
	grants     script.Grants
	listErr    error
	getErr     error
	versionErr error
	approveErr error
	// The review half, served by the methods in review_test.go.
	pending    []script.PendingReview
	pendingErr error
	rejectErr  error
	rejected   []int
	// byID resolves GetVersionByID when a test needs the approved version to
	// be a different row from the one under review.
	byID map[string]*script.Version
	// The schedule half, served by the methods in schedules_test.go.
	schedule         *script.Schedule
	scheduleErr      error
	scheduleWriteErr error
}

func (*stubStore) Create(context.Context, *script.Script, script.Author) error { return nil }
func (*stubStore) Get(context.Context, string) (*script.Script, error) {
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (*stubStore) GetPersonal(context.Context, string, string) (*script.Script, error) {
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (s *stubStore) GetByID(_ context.Context, id string) (*script.Script, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	for i := range s.scripts {
		if s.scripts[i].ID == id {
			return &s.scripts[i], nil
		}
	}
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (*stubStore) Update(context.Context, *script.Script) error { return nil }
func (*stubStore) Delete(context.Context, string) error         { return nil }
func (s *stubStore) List(context.Context, script.ListFilter) ([]script.Script, error) {
	return s.scripts, s.listErr
}

func (*stubStore) UpdateWithVersion(context.Context, *script.Script, script.Author) error {
	return nil
}

func (*stubStore) CreateDraftVersion(context.Context, string, *script.Script, script.Author) (int, error) {
	return 0, nil
}

func (s *stubStore) ListVersions(context.Context, string) ([]script.Version, error) {
	if s.versionErr != nil {
		return nil, s.versionErr
	}
	if s.version == nil {
		return []script.Version{}, nil
	}
	return []script.Version{*s.version}, nil
}

func (s *stubStore) GetVersion(_ context.Context, _ string, version int) (*script.Version, error) {
	if s.versionErr != nil {
		return nil, s.versionErr
	}
	if s.version == nil || s.version.Version != version {
		return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
	}
	return s.version, nil
}

func (s *stubStore) GetVersionByID(_ context.Context, id string) (*script.Version, error) {
	if s.versionErr != nil {
		return nil, s.versionErr
	}
	if s.byID != nil {
		return s.byID[id], nil
	}
	return s.version, nil
}

func (s *stubStore) ApproveVersion(_ context.Context, _ string, _ int, approver string, grants script.Grants) (*script.Version, error) {
	if s.approveErr != nil {
		return nil, s.approveErr
	}
	s.grants = grants
	approvedAt := time.Now().UTC()
	out := *s.version
	out.ApprovedBy, out.ApprovedAt = approver, &approvedAt
	// The real store fills the roles from the version's author; the fake does
	// the same, so a test cannot pass by supplying them in the request.
	out.Grants = grants
	out.Grants.Roles = s.version.AuthorRoles
	s.approved = &out
	return &out, nil
}

// newStore returns a store holding one script with one unapproved version.
func newStore() *stubStore {
	return &stubStore{
		scripts: []script.Script{{
			ID: "script_1", Name: "daily", Scope: script.ScopePersonal,
			OwnerEmail: "jane@example.com", Enabled: true, Status: script.StatusDraft,
		}},
		version: &script.Version{
			ID: "sver_1", ScriptID: "script_1", Version: 1, Source: reportSource,
			Author: "jane@example.com", AuthorRoles: []string{"analyst"},
			Status: script.VersionStatusApplied,
		},
	}
}

// serve mounts the routes and runs one request against them.
func serve(t *testing.T, store *stubStore, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(Deps{
		Scripts: store, Versions: store, Approvals: store, Schedules: store,
		Reviews: store, Rejections: store,
		AdminEmail: func(*http.Request) string { return "admin@example.com" },
	}).RegisterAdmin(mux, "/api/v1/admin", func(h http.Handler) http.Handler { return h })

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// decode reads a JSON response body.
func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// fullGrantBody is a request covering everything the script reaches for.
const fullGrantBody = `{"connections":["warehouse"],"capabilities":["platform.query","platform.export"],` +
	`"destinations":[{"name":"portal","kind":"portal"}]}`

func TestListScripts(t *testing.T) {
	rec := serve(t, newStore(), http.MethodGet, "/api/v1/admin/scripts", "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := decode(t, rec)
	assert.Equal(t, float64(1), body["total"])
}

func TestListScripts_StoreFailure(t *testing.T) {
	store := newStore()
	store.listErr = errors.New("boom")
	rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestListVersions(t *testing.T) {
	rec := serve(t, newStore(), http.MethodGet, "/api/v1/admin/scripts/script_1/versions", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(1), decode(t, rec)["total"])
}

func TestListVersions_MissingScriptAndStoreFailures(t *testing.T) {
	t.Run("unknown script", func(t *testing.T) {
		rec := serve(t, newStore(), http.MethodGet, "/api/v1/admin/scripts/nope/versions", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("script read fails", func(t *testing.T) {
		store := newStore()
		store.getErr = errors.New("boom")
		rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts/script_1/versions", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("version read fails", func(t *testing.T) {
		store := newStore()
		store.versionErr = errors.New("boom")
		rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts/script_1/versions", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// TestGetVersion_ShowsWhatTheCodeReachesForAndWhatIsMissing is the review
// payload: an unapproved version's grant covers nothing, so everything its
// source touches is reported as missing — which is the grant a reviewer would
// have to bind.
func TestGetVersion_ShowsWhatTheCodeReachesForAndWhatIsMissing(t *testing.T) {
	rec := serve(t, newStore(), http.MethodGet, "/api/v1/admin/scripts/script_1/versions/1", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decode(t, rec)
	referenced, ok := body["referenced"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.ElementsMatch(t, []any{"platform.query", "platform.export"}, referenced["capabilities"])
	assert.Equal(t, []any{"warehouse"}, referenced["connections"])
	assert.Equal(t, false, referenced["dynamic_connections"])
	assert.Equal(t, []any{"portal"}, referenced["destinations"],
		"an export naming no destination writes to the portal")
	assert.Equal(t, false, referenced["dynamic_destinations"])
	assert.ElementsMatch(t, []any{"platform.query", "platform.export"}, body["missing_capabilities"])
	assert.Equal(t, []any{"warehouse"}, body["missing_connections"])
	assert.Equal(t, []any{"portal"}, body["missing_destinations"])
}

func TestGetVersion_NotFound(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/scripts/script_1/versions/9",
		"/api/v1/admin/scripts/script_1/versions/0",
		"/api/v1/admin/scripts/script_1/versions/abc",
	} {
		t.Run(path, func(t *testing.T) {
			rec := serve(t, newStore(), http.MethodGet, path, "")
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestGetVersion_StoreFailure(t *testing.T) {
	store := newStore()
	store.versionErr = errors.New("boom")
	rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts/script_1/versions/1", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestApproveVersion_BindsTheGrantAndTheAuthorsRoles is the approval action:
// the reviewer decides what the script may REACH, and the authority comes from
// the version's author whatever the request says.
func TestApproveVersion_BindsTheGrantAndTheAuthorsRoles(t *testing.T) {
	store := newStore()
	rec := serve(t, store, http.MethodPost,
		"/api/v1/admin/scripts/script_1/versions/1/approve", fullGrantBody)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := decode(t, rec)
	assert.Equal(t, "admin@example.com", body["approved_by"])
	grants, ok := body["grants"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"analyst"}, grants["roles"], "the author's roles, bound by the approval")
	assert.Equal(t, []any{"warehouse"}, grants["connections"])
	assert.Empty(t, store.grants.Roles, "the handler never sends roles for the store to trust")
}

// TestApproveVersion_BindsADestinationsAddress is what makes a delivery grant
// mean something: the approval records the connection, bucket and prefix, so
// what the reviewer agreed to cannot be repointed underneath the script.
func TestApproveVersion_BindsADestinationsAddress(t *testing.T) {
	store := newStore()
	body := `{"connections":["warehouse"],"capabilities":["platform.query","platform.export"],` +
		`"destinations":[{"name":"portal","kind":"portal"},` +
		`{"name":"acme-drop","kind":"s3","connection":"acme-s3","bucket":"acme-exports","prefix":"/weekly/"}]}`
	rec := serve(t, store, http.MethodPost,
		"/api/v1/admin/scripts/script_1/versions/1/approve", body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.Len(t, store.grants.Destinations, 2)
	delivered := store.grants.Destinations[1]
	assert.Equal(t, "acme-drop", delivered.Name)
	assert.Equal(t, "acme-s3", delivered.Connection)
	assert.Equal(t, "acme-exports", delivered.Bucket)
	assert.Equal(t, "weekly", delivered.Prefix,
		"a prefix is stored in one form, or the next version's diff reports a widening that never happened")
}

// TestApproveVersion_ReadsADestinationRecordedByNameAlone covers the request
// shape a client written before delivery existed still sends. The portal was
// the only destination then, so the older form is unambiguous.
func TestApproveVersion_ReadsADestinationRecordedByNameAlone(t *testing.T) {
	store := newStore()
	body := `{"connections":["warehouse"],"capabilities":["platform.query","platform.export"],"destinations":["portal"]}`
	rec := serve(t, store, http.MethodPost,
		"/api/v1/admin/scripts/script_1/versions/1/approve", body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, store.grants.Destinations, 1)
	assert.Equal(t, script.PortalDestination(), store.grants.Destinations[0])
}

// TestApproveVersion_RefusesAGrantTheCodeWouldOutrun is the reviewer's safety
// net: approving a script that will refuse itself on its first query is not a
// decision anybody meant to make.
func TestApproveVersion_RefusesAGrantTheCodeWouldOutrun(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "capability missing",
			body:    `{"connections":["warehouse"],"capabilities":["platform.query"],"destinations":["portal"]}`,
			wantErr: "capabilities this version calls: platform.export",
		},
		{
			name:    "connection missing",
			body:    `{"capabilities":["platform.query","platform.export"],"destinations":[{"name":"portal","kind":"portal"}]}`,
			wantErr: "connections this version queries: warehouse",
		},
		{
			name:    "destination missing",
			body:    `{"connections":["warehouse"],"capabilities":["platform.query","platform.export"]}`,
			wantErr: "destinations this version writes to: portal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, newStore(), http.MethodPost,
				"/api/v1/admin/scripts/script_1/versions/1/approve", tt.body)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, decode(t, rec)["detail"], tt.wantErr)
		})
	}
}

// TestApproveVersion_ErrorMapping covers the statuses a reviewer branches on.
func TestApproveVersion_ErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		storeErr error
		want     int
	}{
		{"malformed body", "not json", nil, http.StatusBadRequest},
		{"conflict", fullGrantBody, fmt.Errorf("already resolved: %w", script.ErrVersionConflict), http.StatusConflict},
		{"invalid grant", fullGrantBody, fmt.Errorf("the author held no roles (%w)", script.ErrInvalidGrant), http.StatusBadRequest},
		{"version carries no grant", fullGrantBody, fmt.Errorf("reading it back: %w", script.ErrNoGrants), http.StatusBadRequest},
		{"store failure", fullGrantBody, errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore()
			store.approveErr = tt.storeErr
			rec := serve(t, store, http.MethodPost,
				"/api/v1/admin/scripts/script_1/versions/1/approve", tt.body)
			assert.Equal(t, tt.want, rec.Code, rec.Body.String())
		})
	}
}

// TestApproveVersion_UnknownVersion covers the path check before any grant is
// evaluated.
func TestApproveVersion_UnknownVersion(t *testing.T) {
	rec := serve(t, newStore(), http.MethodPost,
		"/api/v1/admin/scripts/script_1/versions/9/approve", fullGrantBody)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestJoin(t *testing.T) {
	assert.Empty(t, join(nil))
	assert.Equal(t, "a", join([]string{"a"}))
	assert.Equal(t, "a, b", join([]string{"a", "b"}))
}
