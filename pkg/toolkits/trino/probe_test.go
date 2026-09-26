package trino

import (
	"context"
	"encoding/json"
	"io"
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

// scratchStub answers the statements a scratch probe sends the way Trino 453
// answered them against the dev stack's access-controlled coordinator: SELECT 1
// answers, and each partition procedure answers with what refuses it -- access
// control, the catalog property, or the missing table that means neither did.
func scratchStub(t *testing.T, refusals map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sql := string(body)
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasPrefix(sql, "CALL") {
			_, _ = w.Write([]byte(`{"id":"q1","infoUri":"http://localhost/q1","stats":{"state":"FINISHED"},
				"columns":[{"name":"_col0","type":"integer"}],"data":[[1]]}`))
			return
		}
		msg := "Table 'uploads." + probeTable + "' not found"
		for procedure, refusal := range refusals {
			if strings.Contains(sql, ".system."+procedure+"(") {
				msg = refusal
			}
		}
		out, _ := json.Marshal(map[string]any{
			"id": "q2", "infoUri": "http://localhost/q2", "stats": map[string]any{"state": "FAILED"},
			"error": map[string]any{"message": msg, "errorName": "PERMISSION_DENIED", "errorType": "USER_ERROR", "errorCode": 4},
		})
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func scratchToolkit(t *testing.T, srv *httptest.Server) *Toolkit {
	t.Helper()
	host, port := hostPortOf(t, srv)
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "warehouse",
		Instances: map[string]Config{
			"warehouse": {Host: host, Port: port, User: "mcp-server", ReadOnly: true, Scratch: ScratchConfig{Catalog: "scratch", Schema: "uploads"}},
			"scratch":   {Host: host, Port: port, User: "mcp-scratch", Scratch: ScratchConfig{Catalog: "scratch", Schema: "uploads"}},
			"plain":     {Host: host, Port: port, User: "mcp-scratch"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

// A scratch connection whose user may call the three partition procedures on
// a catalog that allows register_partition passes, and says what it checked.
func TestProbeConnection_ScratchReady(t *testing.T) {
	tk := scratchToolkit(t, scratchStub(t, nil))
	res := tk.ProbeConnection(context.Background(), "scratch")
	require.True(t, res.OK, "probe failed: %+v", res)
	assert.Contains(t, res.Detail, "the scratch catalog scratch allows this connection to call sync_partition_metadata, register_partition, unregister_partition")

	// A connection with no scratch target, and a read-only one, are not asked.
	for _, name := range []string{"plain", "warehouse"} {
		res := tk.ProbeConnection(context.Background(), name)
		require.True(t, res.OK, name)
		assert.NotContains(t, res.Detail, "partition", name)
	}
}

// The refusal the reference install met (#1888): the scratch user held "all"
// on the catalog and no procedures rule, and the catalog lacked the property.
// Every missing setting is named at once, with the engine's own words.
func TestProbeConnection_ScratchMissingSettings(t *testing.T) {
	tk := scratchToolkit(t, scratchStub(t, map[string]string{
		"sync_partition_metadata": "Access Denied: Cannot execute procedure scratch.system.sync_partition_metadata",
		"register_partition":      "register_partition procedure is disabled",
		"unregister_partition":    "Access Denied: Cannot execute procedure scratch.system.unregister_partition",
	}))
	res := tk.ProbeConnection(context.Background(), "scratch")
	require.False(t, res.OK)
	assert.Contains(t, res.Detail, "webhook sources cannot run on this scratch connection")
	assert.Contains(t, res.Detail, "the Trino user of connection scratch may not EXECUTE scratch.system.sync_partition_metadata")
	assert.Contains(t, res.Detail, "the Trino user of connection scratch may not EXECUTE scratch.system.unregister_partition")
	assert.Contains(t, res.Detail, "set hive.allow-register-partition-procedure=true")
	assert.Contains(t, res.Error, "Cannot execute procedure scratch.system.sync_partition_metadata")
	assert.Contains(t, res.Error, "register_partition procedure is disabled")
}

// An answer the probe does not recognize fails the check naming the call,
// rather than passing a connection nothing proved.
func TestProbeConnection_ScratchUnexpectedAnswer(t *testing.T) {
	tk := scratchToolkit(t, scratchStub(t, map[string]string{"register_partition": "Catalog 'scratch' not found"}))
	res := tk.ProbeConnection(context.Background(), "scratch")
	require.False(t, res.OK)
	assert.Contains(t, res.Detail, "calling scratch.system.register_partition failed")
	assert.Contains(t, res.Error, "Catalog 'scratch' not found")
}

func TestProbeCall(t *testing.T) {
	target := ScratchConfig{Catalog: "scr\"atch", Schema: "up'loads"}
	assert.Equal(t, `CALL "scr""atch".system.sync_partition_metadata('up''loads', 'mcp_platform_connection_test_absent', 'ADD')`,
		probeCall(target, "sync_partition_metadata"))
	assert.Equal(t, `CALL "scr""atch".system.register_partition('up''loads', 'mcp_platform_connection_test_absent', ARRAY['dt'], ARRAY['probe'], 's3://mcp_platform_connection_test_absent/')`,
		probeCall(target, "register_partition"))
	assert.Equal(t, `CALL "scr""atch".system.unregister_partition('up''loads', 'mcp_platform_connection_test_absent', ARRAY['dt'], ARRAY['probe'])`,
		probeCall(target, "unregister_partition"))
}
