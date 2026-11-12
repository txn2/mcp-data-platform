package trino

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	s3kit "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
)

// reserve installs the platform's two buckets for one test.
func reserve(t *testing.T) {
	t.Helper()
	s3kit.SetReservedBuckets(s3kit.PlatformBuckets("portal-assets", "managed-resources"))
	t.Cleanup(func() { s3kit.SetReservedBuckets(nil) })
}

// TestLocationInterceptor is #2616 through Trino: every way a statement points
// a table, a schema or a partition at a platform bucket is refused, a
// location assembled from parts is refused, and the statements a person
// writes against business data are left alone.
func TestLocationInterceptor(t *testing.T) {
	reserve(t)
	refused := map[string]string{
		"an external table":      "CREATE TABLE s.t (line varchar) WITH (external_location = 's3a://managed-resources/resources/user/bob/', format = 'TEXTFILE')",
		"an iceberg table":       "CREATE TABLE s.t (a int) WITH (location = 's3://portal-assets/artifacts/')",
		"a schema":               "CREATE SCHEMA s WITH (location = 's3a://managed-resources/resources/')",
		"CTAS":                   "CREATE TABLE s.t WITH (external_location = 's3a://managed-resources/x/') AS SELECT 1 AS a",
		"a quoted key":           `CREATE TABLE s.t (a int) WITH ("EXTERNAL_LOCATION" = 's3a://managed-resources/x/')`,
		"a user name and a port": "CREATE TABLE s.t (a int) WITH (external_location = 's3a://key@managed-resources:443/x/')",
		"an upper-case bucket":   "CREATE TABLE s.t (a int) WITH (external_location = 's3a://Managed-Resources/x/')",
		"a percent-encoded name": "CREATE TABLE s.t (a int) WITH (external_location = 's3a://managed%2Dresources/x/')",
		"register_partition":     "CALL scratch.system.register_partition('s', 't', ARRAY['dt'], ARRAY['1'], 's3a://managed-resources/resources/user/bob/')",
		"register_table":         "CALL iceberg.system.register_table(schema_name => 's', table_name => 't', table_location => 's3://portal-assets/x')",
		"add_files":              "ALTER TABLE s.t EXECUTE add_files(location => 's3://managed-resources/x/', format => 'ORC')",
		"set properties":         "ALTER TABLE s.t SET PROPERTIES location = 's3://portal-assets/x'",
		"a comment between":      "CREATE TABLE s.t (a int) WITH (external_location /* x */ = 's3a://managed-resources/x/')",
		"execute immediate":      "EXECUTE IMMEDIATE 'CREATE TABLE s.t (a int) WITH (external_location = ''s3a://managed-resources/x/'')'",
		"prepare":                "PREPARE p FROM CALL system.register_partition('s', 't', ARRAY['dt'], ARRAY['1'], 's3a://managed-resources/x/')",
	}
	for what, sql := range refused {
		_, err := LocationInterceptor{}.Intercept(context.Background(), sql, "")
		require.Error(t, err, what)
		assert.Contains(t, err.Error(), "is where the platform keeps", what)
	}

	unreadable := map[string]string{
		"concatenation":     "CREATE TABLE s.t (a int) WITH (external_location = 's3a://managed-' || 'resources/x/')",
		"concat":            "CREATE TABLE s.t (a int) WITH (external_location = concat('s3a://managed', '-resources/'))",
		"a cast":            "CREATE SCHEMA s WITH (location = CAST('s3a://managed-resources/' AS varchar))",
		"a unicode string":  `CREATE TABLE s.t (a int) WITH (external_location = U&'s3a://managed\002dresources/x/')`,
		"an argument built": "CALL system.register_partition('s', 't', ARRAY['dt'], ARRAY['1'], concat('s3a://managed', '-resources/'))",
		"an argument array": "CALL system.register_partition('s', 't', ARRAY['dt'], ARRAY[concat('a', 'b')], 's3a://portal-lake/x/')",
		"copied properties": "CREATE TABLE s.t (LIKE s.registered INCLUDING PROPERTIES)",
		"immediate built":   "EXECUTE IMMEDIATE concat('CREATE', ' TABLE')",
		"unending string":   "CREATE TABLE s.t (a int) WITH (external_location = 's3a://x/)",
		"unending comment":  "SELECT 1 /* never closed",
		"unending name":     `SELECT "never closed`,
	}
	for what, sql := range unreadable {
		_, err := LocationInterceptor{}.Intercept(context.Background(), sql, "")
		assert.Error(t, err, what)
	}

	allowed := []string{
		"SELECT * FROM stores WHERE location = 'NYC' AND region IN ('east', 'west')",
		"WITH recent AS (SELECT * FROM s.t) SELECT * FROM recent",
		"CREATE TABLE s.t (a int) WITH (external_location = 's3a://portal-lake/raw/', format = 'PARQUET')",
		"CREATE SCHEMA s WITH (location = 's3a://portal-lake/')",
		"CALL system.sync_partition_metadata('s', 't', 'ADD', true)",
		"CALL system.register_partition('s', 't', ARRAY['dt', 'h'], ARRAY['2026-11-11', '01'], 's3a://portal-lake/x/')",
		"ALTER TABLE s.t EXECUTE optimize(file_size_threshold => '10MB')",
		"EXECUTE IMMEDIATE 'SELECT 1'",
		"SELECT call, execute FROM t -- execute(1)",
		"SELECT 'portal-assets' AS bucket_name",
		"INSERT INTO s.t SELECT * FROM s.u",
		"CALL system.noop",
		"CALL 'not a name'(1)",
	}
	for _, sql := range allowed {
		got, err := LocationInterceptor{}.Intercept(context.Background(), sql, "")
		require.NoError(t, err, sql)
		assert.Equal(t, sql, got)
	}
}

// TestLocationInterceptor_NoReservedBuckets: with no bucket reserved (a
// deployment with no database) only an unreadable location is refused.
func TestLocationInterceptor_NoReservedBuckets(t *testing.T) {
	s3kit.SetReservedBuckets(nil)
	_, err := LocationInterceptor{}.Intercept(context.Background(),
		"CREATE TABLE s.t (a int) WITH (external_location = 's3a://managed-resources/x/')", "")
	require.NoError(t, err)
}

// TestLocationInterceptor_ImmediateDepth bounds EXECUTE IMMEDIATE nested in
// the strings it runs.
func TestLocationInterceptor_ImmediateDepth(t *testing.T) {
	reserve(t)
	sql := "SELECT 1"
	for range maxImmediateDepth + 1 {
		sql = "EXECUTE IMMEDIATE '" + doubleQuotes(sql) + "'"
	}
	_, err := LocationInterceptor{}.Intercept(context.Background(), sql, "")
	require.ErrorContains(t, err, "nested too deep")
}

func doubleQuotes(s string) string {
	out := make([]byte, 0, len(s)*2)
	for i := range len(s) {
		out = append(out, s[i])
		if s[i] == '\'' {
			out = append(out, '\'')
		}
	}
	return string(out)
}

func TestUnicodeEscapes(t *testing.T) {
	assert.Equal(t, "a-b", unicodeEscapes(`a\002db`))
	assert.Equal(t, "a😀", unicodeEscapes(`a\+01F600`))
	assert.Equal(t, `a\zz`, unicodeEscapes(`a\zz`))
}
