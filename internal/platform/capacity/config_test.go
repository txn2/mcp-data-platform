package capacity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestConfigFromEnv_Defaults(t *testing.T) {
	for _, env := range []string{EnvTableInterval, EnvScanInterval, EnvOrphanGrace, EnvBudgets, EnvBackend} {
		t.Setenv(env, "")
	}
	cfg, err := ConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, DefaultTableInterval, cfg.TableInterval)
	assert.Equal(t, DefaultScanInterval, cfg.ScanInterval)
	assert.Equal(t, DefaultFlushInterval, cfg.FlushInterval)
	assert.Equal(t, DefaultOrphanGrace, cfg.OrphanGrace)
	assert.Empty(t, cfg.Budgets)
	assert.Empty(t, cfg.Backend)
}

func TestConfigFromEnv_Set(t *testing.T) {
	t.Setenv(EnvTableInterval, "1m")
	t.Setenv(EnvScanInterval, "30s")
	t.Setenv(EnvOrphanGrace, "0s")
	t.Setenv(EnvBudgets, "portal-assets=10GiB, managed-resources=500")
	t.Setenv(EnvBackend, "gcs")
	cfg, err := ConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, time.Minute, cfg.TableInterval)
	assert.Equal(t, 30*time.Second, cfg.ScanInterval)
	assert.Equal(t, time.Duration(0), cfg.OrphanGrace, "a zero grace is kept, not defaulted")
	assert.Equal(t, []observability.BucketBudget{
		{Bucket: "portal-assets", Bytes: 10 << 30}, {Bucket: "managed-resources", Bytes: 500},
	}, cfg.Budgets)
	assert.Equal(t, "gcs", cfg.Backend)
}

// TestConfigFromEnv_KeepsWhatParsed: a malformed interval is named in the
// error and defaulted, and the budgets and backend set beside it are kept.
func TestConfigFromEnv_KeepsWhatParsed(t *testing.T) {
	t.Setenv(EnvTableInterval, "15")
	t.Setenv(EnvScanInterval, "")
	t.Setenv(EnvOrphanGrace, "")
	t.Setenv(EnvBudgets, "portal=500GiB")
	t.Setenv(EnvBackend, "seaweedfs")
	cfg, err := ConfigFromEnv()
	require.ErrorContains(t, err, EnvTableInterval)
	assert.Equal(t, DefaultTableInterval, cfg.TableInterval)
	assert.Equal(t, []observability.BucketBudget{{Bucket: "portal", Bytes: 500 << 30}}, cfg.Budgets)
	assert.Equal(t, "seaweedfs", cfg.Backend)

	t.Setenv(EnvBackend, "ceph")
	cfg, err = ConfigFromEnv()
	require.ErrorContains(t, err, EnvBackend)
	assert.Empty(t, cfg.Backend, "an unknown kind is dropped, and the store is asked instead")
}

func TestConfigFromEnv_Refusals(t *testing.T) {
	for _, tc := range []struct{ env, value, want string }{
		{EnvTableInterval, "soon", EnvTableInterval},
		{EnvScanInterval, "-1h", EnvScanInterval},
		{EnvBudgets, "nobucket", EnvBudgets},
		{EnvBackend, "ceph", EnvBackend},
	} {
		t.Run(tc.env, func(t *testing.T) {
			for _, env := range []string{EnvTableInterval, EnvScanInterval, EnvOrphanGrace, EnvBudgets, EnvBackend} {
				t.Setenv(env, "")
			}
			t.Setenv(tc.env, tc.value)
			_, err := ConfigFromEnv()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestParseBudgets(t *testing.T) {
	got, err := ParseBudgets("a=1KB,b=2MiB,c=3TB,,d=4")
	require.NoError(t, err)
	assert.Equal(t, []observability.BucketBudget{
		{Bucket: "a", Bytes: 1000}, {Bucket: "b", Bytes: 2 << 20}, {Bucket: "c", Bytes: 3e12}, {Bucket: "d", Bytes: 4},
	}, got)
	for _, bad := range []string{"=5", "a=", "a=0", "a=-3", "a=1.5GiB", "a=lots"} {
		_, err := ParseBudgets(bad)
		assert.Error(t, err, bad)
	}
	got, err = ParseBudgets("")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestWithDefaults_NegativeGrace(t *testing.T) {
	assert.Equal(t, DefaultOrphanGrace, Config{OrphanGrace: -time.Second}.withDefaults().OrphanGrace)
}
