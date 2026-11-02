package postgres

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"maps"
	"math"
	"math/big"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func validConfigMap() map[string]any {
	return map[string]any{"host": "db.example.com", "database": "orders", "user": "app", "password": "pw"}
}

func TestParseConfig_Defaults(t *testing.T) {
	c, err := ParseConfig(validConfigMap())
	require.NoError(t, err)
	assert.Equal(t, DefaultPort, c.Port)
	assert.Equal(t, SSLModeVerifyFull, c.SSLMode)
	assert.Equal(t, DefaultMaxConns, c.MaxConns)
	assert.Equal(t, DefaultStatementTimeout, c.StatementTimeout)
	assert.Equal(t, DefaultMaxRows, c.MaxRows)
	assert.False(t, c.ReadOnly)
}

func TestParseConfig_Refusals(t *testing.T) {
	for key, tc := range map[string]struct {
		set  map[string]any
		want string
	}{
		"no host":         {map[string]any{"host": ""}, "host is required"},
		"host with path":  {map[string]any{"host": "db/x"}, "is not a host name"},
		"port":            {map[string]any{"port": 70000}, "port 70000 is out of range"},
		"no database":     {map[string]any{"database": ""}, "database is required"},
		"no user":         {map[string]any{"user": ""}, "user is required"},
		"sslmode":         {map[string]any{"sslmode": "prefer"}, `invalid sslmode "prefer"`},
		"max_conns":       {map[string]any{"max_conns": 0}, "max_conns must be between"},
		"timeout":         {map[string]any{"statement_timeout": "11m"}, "statement_timeout must be positive"},
		"max_rows":        {map[string]any{"max_rows": MaxMaxRows + 1}, "max_rows must be between"},
		"tls on disable":  {map[string]any{"sslmode": "disable", "tls_ca_bundle_pem": "x"}, "TLS material is set but sslmode is disable"},
		"half a key pair": {map[string]any{"mtls_client_cert_pem": "x"}, "must both be set"},
	} {
		t.Run(key, func(t *testing.T) {
			cfg := validConfigMap()
			maps.Copy(cfg, tc.set)
			_, err := ParseConfig(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestParseMultiConfig_SkipsAnInvalidInstance(t *testing.T) {
	multi, err := ParseMultiConfig("a", map[string]map[string]any{
		"a": validConfigMap(),
		"b": {"host": "db"},
	})
	require.NoError(t, err)
	require.Len(t, multi.Instances, 1)
	assert.Equal(t, "a", multi.Instances["a"].ConnectionName)
}

func TestJSONValue(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 30, 0, 0, time.FixedZone("x", 2*3600))
	var interval pgtype.Interval
	require.NoError(t, interval.Scan("1 day"))
	for name, tc := range map[string]struct {
		v    any
		typ  string
		want string
	}{
		"nil":         {nil, "text", `null`},
		"int":         {int64(9007199254740991), "int8", `9007199254740991`},
		"big int":     {int64(9007199254740993), "int8", `"9007199254740993"`},
		"low int":     {int64(-9007199254740993), "int8", `"-9007199254740993"`},
		"numeric":     {mustNumeric("12345678901234567890.0123456789"), "numeric", `"12345678901234567890.0123456789"`},
		"bad numeric": {pgtype.Numeric{}, "numeric", `null`},
		"timestamptz": {at, "timestamptz", `"2026-11-01T10:30:00Z"`},
		"timestamp":   {time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC), "timestamp", `"2026-11-01T08:00:00"`},
		"date":        {time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), "date", `"2026-11-01"`},
		"uuid":        {[16]byte{0x6f, 0x1c, 0x2c, 0x1e, 0x7d, 0x2b, 0x4b, 0x9e, 0x9a, 0x51, 0x0c, 0x4f, 0x0d, 0x1e, 0x2a, 0x3b}, "uuid", `"6f1c2c1e-7d2b-4b9e-9a51-0c4f0d1e2a3b"`},
		"bytea":       {[]byte{0, 1, 2, 0xff}, "bytea", `"AAEC/w=="`},
		"array":       {[]any{[16]byte{}, nil}, "_uuid", `["00000000-0000-0000-0000-000000000000",null]`},
		"json":        {map[string]any{"a": []any{1.0}}, "jsonb", `{"a":[1]}`},
		"float":       {0.25, "float8", `0.25`},
		"nan":         {math.NaN(), "float8", `"NaN"`},
		"inf":         {float32(math.Inf(1)), "float4", `"Infinity"`},
		"-inf":        {math.Inf(-1), "float8", `"-Infinity"`},
		"bool":        {true, "bool", `true`},
		"interval":    {interval, "interval", `"1 day 00:00:00"`},
		"time":        {pgtype.Time{Microseconds: (13*3600 + 5) * 1e6, Valid: true}, "time", `"13:00:05"`},
		"null time":   {pgtype.Time{}, "time", `null`},
		"inet":        {netip.MustParsePrefix("10.0.0.0/8"), "cidr", `"10.0.0.0/8"`},
		"other":       {struct{ A int }{1}, "x", `{"A":1}`},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := json.Marshal(jsonValue(tc.v, tc.typ))
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
		})
	}
}

func TestBindValues(t *testing.T) {
	ctx := context.Background()
	b, err := bindValues(ctx, "SELECT 1", nil)
	require.NoError(t, err)
	assert.Empty(t, b.args)

	b, err = bindValues(ctx, "SELECT $1, $2", []any{json.Number("12345678901234567890.5"), "x"})
	require.NoError(t, err)
	assert.Equal(t, []any{json.Number("12345678901234567890.5"), "x"}, b.args)

	b, err = bindValues(ctx, "SELECT @a, @b, @a, '@c' -- @d", map[string]any{"a": int64(1), "b": true})
	require.NoError(t, err)
	assert.Equal(t, "SELECT $1, $2, $1, '@c' -- @d", b.sql)
	assert.Equal(t, []any{int64(1), true}, b.args)

	_, err = bindValues(ctx, "SELECT @a", map[string]any{"a": 1, "b": 2})
	require.Error(t, err, "a name no placeholder uses is refused")
	_, err = bindValues(ctx, "SELECT 1", 7)
	require.ErrorIs(t, err, errParamsShape)

	_, err = bindValues(ctx, "SELECT 1", make([]any, MaxParams+1))
	require.Error(t, err)
	bigMap := map[string]any{}
	for i := range MaxParams + 1 {
		bigMap[string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676))] = 1
	}
	_, err = bindValues(ctx, "SELECT 1", bigMap)
	require.Error(t, err)
}

func TestEncodeArgs(t *testing.T) {
	types := fakeTypes()
	args := []any{
		json.Number("12.5"), "text", true, false, nil,
		map[string]any{"a": 1.0},
		"abc",
		[]any{"x", `y"z`, nil, []any{"n"}},
		"{1,2}",
		"AAEC",
		map[string]any{"k": "v"},
	}
	got, err := encodeArgs(args, []typeInfo{
		types[oidNumeric], types[oidText], types[oidBool], types[oidBool], types[oidText],
		types[oidJSONB], types[oidJSONB],
		types[oidText1D], types[oidText1D],
		types[oidBytea],
		{name: "my_composite", display: "my_composite"},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{
		"12.5", "text", "true", "false", nil,
		`{"a":1}`, `"abc"`,
		`{"x","y\"z",NULL,{"n"}}`, "{1,2}",
		`\x000102`, `{"k":"v"}`,
	}, got)

	for name, tc := range map[string]struct {
		arg any
		ti  typeInfo
	}{
		"bytea not a string": {1.0, types[oidBytea]},
		"bytea not base64":   {"!!", types[oidBytea]},
		"array not a list":   {true, types[oidText1D]},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := encodeArgs([]any{tc.arg}, []typeInfo{tc.ti})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "$1")
		})
	}
}

func TestTransactionControl(t *testing.T) {
	for _, sql := range []string{"COMMIT", "  begin;", "-- note\nROLLBACK", "/* x */ end", "Abort", "(savepoint s)", "PREPARE TRANSACTION 'x'", "release s", "start transaction"} {
		assert.Error(t, refuseTransactionControl(sql), sql)
	}
	for _, sql := range []string{"SELECT 1", "-- only a comment", "/* unterminated", "WITH x AS (SELECT 1) SELECT * FROM x", "commitments"} {
		assert.NoError(t, refuseTransactionControl(sql), sql)
	}
	assert.Equal(t, "", firstWord("-- no newline"))
}

func TestEffectiveTimeout(t *testing.T) {
	cfg := baseConfig()
	assert.Equal(t, DefaultStatementTimeout, effectiveTimeout(cfg, 0))
	assert.Equal(t, 5*time.Second, effectiveTimeout(cfg, 5))
	assert.Equal(t, DefaultStatementTimeout, effectiveTimeout(cfg, 3600), "a caller can only lower it")
}

func TestPoolConfig_SetsWhatTheEnvironmentWouldOtherwiseSupply(t *testing.T) {
	cfg := baseConfig()
	cfg.Password = "pw'\\x"
	cfg.Database = "o'rders"
	cfg.StatementTimeout = 3 * time.Second
	called := false
	pc, err := poolConfig(cfg, func(context.Context, *pgx.Conn) error { called = true; return nil }, nil)
	require.NoError(t, err)
	assert.Equal(t, "pw'\\x", pc.ConnConfig.Password)
	assert.Equal(t, "o'rders", pc.ConnConfig.Database)
	assert.Nil(t, pc.ConnConfig.TLSConfig)
	assert.Nil(t, pc.ConnConfig.Fallbacks)
	assert.Equal(t, map[string]string{"application_name": applicationName, "statement_timeout": "3000"}, pc.ConnConfig.RuntimeParams)
	assert.Equal(t, pgx.QueryExecModeExec, pc.ConnConfig.DefaultQueryExecMode)
	assert.Equal(t, int32(DefaultMaxConns), pc.MaxConns)
	require.NotNil(t, pc.AfterConnect)
	require.NoError(t, pc.AfterConnect(context.Background(), nil))
	assert.True(t, called)
	assert.NotNil(t, pc.AfterRelease)
}

func TestPoolConfig_RefusesAPoolSizeOutOfBounds(t *testing.T) {
	for _, n := range []int{0, -1, MaxMaxConns + 1, math.MaxInt32 + 1} {
		cfg := baseConfig()
		cfg.MaxConns = n
		_, err := poolConfig(cfg, nil, nil)
		require.Error(t, err, "max_conns %d", n)
	}
}

func TestTLSConfig_PerMode(t *testing.T) {
	caPEM, leaf := selfSigned(t)
	cfg := baseConfig()
	cfg.CABundlePEM = caPEM

	cfg.SSLMode = SSLModeVerifyFull
	full, err := tlsConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, "db", full.ServerName)
	assert.False(t, full.InsecureSkipVerify)

	cfg.SSLMode = SSLModeRequire
	req, err := tlsConfig(cfg)
	require.NoError(t, err)
	assert.True(t, req.InsecureSkipVerify)
	assert.Nil(t, req.VerifyPeerCertificate)

	cfg.SSLMode = SSLModeVerifyCA
	ca, err := tlsConfig(cfg)
	require.NoError(t, err)
	assert.Nil(t, ca.VerifyPeerCertificate, "a resumed session skips VerifyPeerCertificate")
	require.NotNil(t, ca.VerifyConnection)
	peers := func(c ...*x509.Certificate) tls.ConnectionState { return tls.ConnectionState{PeerCertificates: c} }
	assert.NoError(t, ca.VerifyConnection(peers(leaf)), "a chain to the bundle passes whatever its name")
	assert.Error(t, ca.VerifyConnection(peers()))
	_, otherLeaf := selfSigned(t)
	assert.Error(t, ca.VerifyConnection(peers(otherLeaf)), "a chain to another root fails")

	plain := baseConfig()
	plain.SSLMode = SSLModeRequire
	noBundle, err := tlsConfig(plain)
	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS12), noBundle.MinVersion)
}

// selfSigned is a CA certificate as PEM and the parsed certificate.
func selfSigned(t *testing.T) (string, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "acc-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), cert
}

func TestStatusOf(t *testing.T) {
	assert.Equal(t, observability.StatusOK, statusOf(nil))
	assert.Equal(t, "canceled", statusOf(context.Canceled))
	assert.Equal(t, "class_23", statusOf(&pgconn.PgError{Code: "23505"}))
	assert.Equal(t, "error", statusOf(errors.New("x")))
}

func TestTracer_RecordsWithoutASpanOutsideATrace(t *testing.T) {
	var metrics atomicMetrics
	tr := &statementTracer{connection: "db", database: "app", metrics: &metrics.p, cfg: observability.DBConfig{SlowThreshold: time.Hour}}
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	st, ok := ctx.Value(traceKey{}).(*traceState)
	require.True(t, ok)
	assert.Nil(t, st.span)
	assert.Equal(t, "select", st.operation)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "42601"}})
	tr.TraceQueryEnd(context.Background(), nil, pgx.TraceQueryEndData{})
}

func TestToolkit_ManagesConnections(t *testing.T) {
	tk := NewMulti(MultiConfig{DefaultName: "a", Instances: map[string]Config{"a": baseConfig()}})
	t.Cleanup(func() { _ = tk.Close() })
	assert.Equal(t, Kind, tk.Kind())
	assert.Equal(t, "a", tk.Name())
	assert.Equal(t, "a", tk.Connection())
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)
	tk.SetMetrics(nil)

	require.NoError(t, tk.AddConnection("b", validConfigMap()))
	require.ErrorIs(t, tk.AddConnection("b", validConfigMap()), ErrConnectionExists)
	require.Error(t, tk.AddConnection("c", map[string]any{"host": "x"}))
	assert.True(t, tk.HasConnection("b"))

	ro := validConfigMap()
	ro["read_only"] = true
	ro["description"] = "read side"
	require.NoError(t, tk.UpdateConnection("b", ro))
	require.Error(t, tk.UpdateConnection("b", map[string]any{"host": "x"}))
	list := tk.ListConnections()
	require.Len(t, list, 2)
	assert.True(t, list[0].IsDefault)
	assert.Equal(t, "read side", list[1].Description)
	assert.True(t, *list[1].ReadOnly)

	require.NoError(t, tk.RemoveConnection("b"))
	require.ErrorIs(t, tk.RemoveConnection("b"), ErrConnectionNotFound)
	assert.False(t, tk.HasConnection("b"))

	// Without a guard there is nothing to ask the database; with one, a
	// database that does not answer is not refused at save.
	require.NoError(t, tk.ValidateConnection("x", validConfigMap()))
	require.Error(t, tk.ValidateConnection("x", map[string]any{}))
	tk.SetConnectGuard(func(context.Context, *pgx.Conn) error { return ErrPlatformDatabase })
	unreachable := validConfigMap()
	unreachable["host"], unreachable["port"], unreachable["sslmode"] = "127.0.0.1", 9, "disable"
	require.NoError(t, tk.ValidateConnection("x", unreachable))

	res := tk.ProbeConnection(context.Background(), "nope")
	assert.False(t, res.OK)
	assert.Nil(t, OwnDatabaseGuard(nil))
}

func TestObserveAndErrorReading(t *testing.T) {
	assert.True(t, isAuthFailure(&pgconn.PgError{Code: "28P01"}))
	assert.False(t, isAuthFailure(errors.New("x")))
	assert.False(t, isConnectError(errors.New("x")))
	observe("db", nil)
	observe("db", &pgconn.PgError{Code: "23505"})
	observe("db", errors.New("x"))
	assert.Equal(t, "line one", firstLine("line one\nline two"))
	assert.Equal(t, "boom", scrubDriverError(errors.New("boom")))
	assert.Equal(t, "invalid connection settings: bad", scrubConnString("parse: bad"))
	assert.Equal(t, "invalid connection settings", scrubConnString("bad"))
	se := statementError(&pgconn.PgError{Code: "42P01", Message: "relation does not exist", TableName: "t"})
	assert.Contains(t, se.summary(), "42P01 (undefined_table); table t")
	assert.Nil(t, statementError(errors.New("x")))
}

// atomicMetrics holds the nil recorder a tracer reads.
type atomicMetrics struct {
	p atomic.Pointer[observability.Metrics]
}

func TestToolkit_RegistersNoAgentTool(t *testing.T) {
	tk := NewMulti(MultiConfig{DefaultName: "db"})
	t.Cleanup(func() { _ = tk.Close() })
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	tk.RegisterTools(server)
	assert.Empty(t, tk.Tools(), "an application database is reached from scripts, never by an agent tool")
	assert.NotNil(t, tk.Tools(), "an empty list, never null")
}

func TestTracer_OpensASpanInsideATrace(t *testing.T) {
	var metrics atomicMetrics
	tr := &statementTracer{connection: "db", database: "app", metrics: &metrics.p, cfg: observability.DBConfig{IncludeStatement: true}}
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled})
	ctx := tr.TraceQueryStart(trace.ContextWithSpanContext(context.Background(), sc), nil, pgx.TraceQueryStartData{SQL: "INSERT INTO t VALUES ($1)"})
	st, ok := ctx.Value(traceKey{}).(*traceState)
	require.True(t, ok)
	assert.NotNil(t, st.span)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{Code: "23505"}})
}

// TestJSONValue_IntegersAreInt64 holds the direct path's values to the types
// a script's value conversion takes: int2 and int4 arrive from pgx as int16
// and int32, which it does not (#2428).
func TestJSONValue_IntegersAreInt64(t *testing.T) {
	assert.Equal(t, int64(7), jsonValue(int16(7), "int2"))
	assert.Equal(t, int64(7), jsonValue(int32(7), "int4"))
	assert.Equal(t, []any{int64(1), nil}, jsonValue([]any{int32(1), nil}, "_int4"))
}
