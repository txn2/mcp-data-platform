package trino

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trinoStub answers the coordinator's query protocol with one row, enough for
// the SELECT 1 a probe sends. It is a stand-in for the wire, not for Trino: a
// real coordinator is what test/acceptance runs against.
func trinoStub(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"access denied"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"q1","infoUri":"http://localhost/q1","stats":{"state":"FINISHED"},
			"columns":[{"name":"_col0","type":"integer"}],"data":[[1]]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// hostPortOf splits an httptest URL into the two fields a connection config
// holds.
func hostPortOf(t *testing.T, srv *httptest.Server) (host string, port int) {
	t.Helper()
	trimmed := strings.TrimPrefix(srv.URL, "http://")
	name, portText, found := strings.Cut(trimmed, ":")
	require.True(t, found, "the test server URL carries no port")
	number, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return name, number
}

// A coordinator that answers SELECT 1 is a connection that works: the DSN
// parsed, the host was reachable and the credential authenticated. That is the
// question a connection's config could not answer before (#1805).
func TestProbeConnection_CoordinatorAnswers(t *testing.T) {
	srv := trinoStub(t, http.StatusOK)
	host, port := hostPortOf(t, srv)

	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances: map[string]Config{
			"warehouse": {Host: host, Port: port, User: "analyst"},
			"readonly":  {Host: host, Port: port, User: "analyst", ReadOnly: true},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "warehouse")
	require.True(t, res.OK, "probe failed: %+v", res)
	assert.Contains(t, res.Detail, "SELECT 1")
	assert.NotContains(t, res.Detail, "read_only")

	// Writability rides on the same answer, so a caller learns it before
	// staging data rather than from a refusal partway through a run.
	readOnly := tk.ProbeConnection(context.Background(), "readonly")
	require.True(t, readOnly.OK)
	assert.Contains(t, readOnly.Detail, "read_only")
}

// A coordinator that refuses the statement is a failure carrying its own
// words, not a connection reported as healthy.
func TestProbeConnection_CoordinatorRefuses(t *testing.T) {
	srv := trinoStub(t, http.StatusForbidden)
	host, port := hostPortOf(t, srv)

	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]Config{"warehouse": {Host: host, Port: port, User: "analyst"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "warehouse")
	assert.False(t, res.OK)
	assert.Contains(t, res.Detail, "refused")
	assert.NotEmpty(t, res.Error)
}

// A name the toolkit does not serve is reported as that, rather than as an
// upstream fault.
func TestProbeConnection_UnknownConnection(t *testing.T) {
	srv := trinoStub(t, http.StatusOK)
	host, port := hostPortOf(t, srv)

	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]Config{"warehouse": {Host: host, Port: port, User: "analyst"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "nope")
	assert.False(t, res.OK)
	assert.Contains(t, res.Detail, "could not be opened")
}
