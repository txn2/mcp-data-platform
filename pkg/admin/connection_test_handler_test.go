package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// mockProbeToolkit is a toolkit that can be asked to open one of its
// connections, which is what the unified test endpoint reaches for.
type mockProbeToolkit struct {
	mockToolkit
	result connprobe.Result
	asked  []string
}

func (m *mockProbeToolkit) ProbeConnection(_ context.Context, name string) connprobe.Result {
	m.asked = append(m.asked, name)
	return m.result
}

// Verify interface compliance.
var (
	_ registry.Toolkit = (*mockProbeToolkit)(nil)
	_ connprobe.Prober = (*mockProbeToolkit)(nil)
)

// probeTestHandler builds a handler whose registry holds the given toolkits.
func probeTestHandler(toolkits ...registry.Toolkit) *Handler {
	return NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: &mockConnectionStore{},
		ConfigStore:     &mockConfigStore{mode: "database"},
		ToolkitRegistry: &mockToolkitRegistry{rawToolkits: toolkits},
	}, nil)
}

// postTest issues the test request for a connection named "warehouse" and
// returns the recorder and decoded body.
func postTest(t *testing.T, h *Handler, kind string) (*httptest.ResponseRecorder, testConnectionResponse) {
	t.Helper()
	const name = "warehouse"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/admin/connection-instances/"+kind+"/"+name+"/test", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var body testConnectionResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec, body
}

// A connection that answers reports what answered, not merely that it did:
// a bare success against the wrong credential is indistinguishable from one
// against the right credential (#1805).
func TestTestConnectionInstance_Answers(t *testing.T) {
	tk := &mockProbeToolkit{
		mockToolkit: mockToolkit{kind: "trino", name: "warehouse"},
		result:      connprobe.Success("the query engine answered SELECT 1"),
	}
	rec, body := postTest(t, probeTestHandler(tk), "trino")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, body.OK)
	assert.Equal(t, "trino", body.Kind)
	assert.Equal(t, "warehouse", body.Name)
	assert.Contains(t, body.Detail, "SELECT 1")
	assert.Equal(t, []string{"warehouse"}, tk.asked, "the probe is asked about the connection in the path")
}

// A connection that does NOT answer is the case the endpoint exists for: it
// must be a failing status carrying the upstream's own words, never a 200 with
// a flag a caller might not read.
func TestTestConnectionInstance_DoesNotAnswer(t *testing.T) {
	tk := &mockProbeToolkit{
		mockToolkit: mockToolkit{kind: "trino", name: "warehouse"},
		result: connprobe.Failure(`connection "warehouse" could not be opened`,
			errors.New(`invalid DSN: parse "https://${TRINO_USER}:x@host": net/url: invalid userinfo`)),
	}
	rec, body := postTest(t, probeTestHandler(tk), "trino")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.False(t, body.OK)
	assert.Contains(t, body.Detail, "could not be opened")
	assert.Contains(t, body.Error, "invalid userinfo")
}

// No toolkit of the kind runs in this process. That is a different answer from
// "the connection is broken" — the connection may well be serving on another
// replica — so it is a 409 naming that, not a 503.
func TestTestConnectionInstance_NoToolkitOfThatKind(t *testing.T) {
	rec, _ := postTest(t, probeTestHandler(), "trino")

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, strings.ToLower(rec.Body.String()), "no trino toolkit is registered")
}

// A toolkit that serves the kind but cannot probe is again its own answer, and
// must not be reported as an unhealthy connection.
func TestTestConnectionInstance_ToolkitCannotProbe(t *testing.T) {
	rec, _ := postTest(t, probeTestHandler(mockToolkit{kind: "trino", name: "warehouse"}), "trino")

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "cannot test a connection")
}

// An unknown kind is refused before any toolkit is looked for.
func TestTestConnectionInstance_UnknownKind(t *testing.T) {
	rec, _ := postTest(t, probeTestHandler(), "sqlite")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "unknown connection kind")
}

// --- Connection-kind discovery ---

// Every kind an instance can be created under answers with a config schema, so
// "what does a trino connection take?" is answerable through the API rather
// than only by reading the deployment's configuration file (#1805).
func TestListConnectionKinds(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-kinds", http.NoBody)
	rec := httptest.NewRecorder()
	probeTestHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var docs []connectionKindDoc
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &docs))
	require.Len(t, docs, len(knownConnectionKinds))

	for _, doc := range docs {
		assert.NotEmpty(t, doc.ConfigSchema, "kind %q answers with no config schema", doc.Kind)
		assert.NotEmpty(t, doc.Note, "kind %q answers with no note on how to read the schema", doc.Kind)
	}
}

// One kind's schema names the keys that kind reads, and says what the schema
// does not promise: that a config it admits will work.
func TestGetConnectionKind(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-kinds/trino", http.NoBody)
	rec := httptest.NewRecorder()
	probeTestHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var doc connectionKindDoc
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))

	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(doc.ConfigSchema, &schema))
	assert.Equal(t, []string{"host"}, schema.Required)
	for _, key := range []string{"host", "user", "password", "catalog", "read_only"} {
		assert.Contains(t, schema.Properties, key)
	}
	assert.Contains(t, doc.Note, "${VAR} is NOT expanded")
}

// An unknown kind is a 404 rather than an empty document, so a misspelled kind
// is not read as a kind that takes nothing.
func TestGetConnectionKind_Unknown(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/connection-kinds/sqlite", http.NoBody)
	rec := httptest.NewRecorder()
	probeTestHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// catchUpStub stands in for the platform's call catch-up: it records what it
// was asked to take on and, like the real one installing a stored connection,
// makes the probe answer for it.
type catchUpStub struct {
	asked [][2]string
	onto  *mockProbeToolkit
}

func (c *catchUpStub) TakeOn(_ context.Context, kind, name string) {
	c.asked = append(c.asked, [2]string{kind, name})
	c.onto.result = connprobe.Success("the query engine answered SELECT 1")
}

// A connection saved through another replica is a row here before the reload
// bus announces it. The test takes it on from the store before probing, so
// testing it straight after the save is not answered as unknown (#1888).
func TestTestConnectionInstance_TakesOnASavedConnectionFirst(t *testing.T) {
	tk := &mockProbeToolkit{
		mockToolkit: mockToolkit{kind: "trino", name: "warehouse"},
		result:      connprobe.Failure(`connection "warehouse" could not be opened`, errors.New("unknown connection")),
	}
	catchUp := &catchUpStub{onto: tk}
	h := NewHandler(Deps{
		Config:            testConfig(),
		ConnectionStore:   &mockConnectionStore{},
		ConfigStore:       &mockConfigStore{mode: "database"},
		ToolkitRegistry:   &mockToolkitRegistry{rawToolkits: []registry.Toolkit{tk}},
		ConnectionCatchUp: catchUp,
	}, nil)

	rec, body := postTest(t, h, "trino")
	require.Equal(t, http.StatusOK, rec.Code, "body: %+v", body)
	assert.Equal(t, [][2]string{{"trino", "warehouse"}}, catchUp.asked)
}
