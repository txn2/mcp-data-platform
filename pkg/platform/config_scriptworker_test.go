package platform

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptadmit"
)

// TestScriptsWorker_FromYAML reads the #1843 settings the way an operator
// writes them, inline beside enabled: concurrency as a bare integer or as
// "adaptive", and the run timeout as a duration string.
func TestScriptsWorker_FromYAML(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte(`
scripts:
  worker:
    enabled: true
    concurrency: 4
    run_timeout: 20m
    max_steps: 50000000
    max_query_rows: 40000
`))
	require.NoError(t, err)
	assert.True(t, cfg.Scripts.IsWorkerEnabled())
	adm, err := cfg.Scripts.Worker.Admission()
	require.NoError(t, err)
	assert.Equal(t, 4, adm.Fixed)
	assert.Equal(t, 20*time.Minute, cfg.Scripts.Worker.RunTimeout)
	assert.Equal(t, int64(50_000_000), cfg.Scripts.Worker.MaxSteps)
	assert.Equal(t, 40_000, cfg.Scripts.Worker.MaxQueryRows)

	cfg, err = LoadConfigFromBytes([]byte(`
scripts:
  worker:
    concurrency: adaptive
    max_concurrency: 32
    min_concurrency: 2
    max_memory_percent: 60
    max_cpu_percent: 80
    shed_memory_percent: 85
`))
	require.NoError(t, err)
	adm, err = cfg.Scripts.Worker.Admission()
	require.NoError(t, err)
	assert.Equal(t, scriptadmit.Admission{
		Min: 2, Max: 32, MaxMemoryPercent: 60, MaxCPUPercent: 80, ShedMemoryPercent: 85,
	}, adm)
}

// TestConfigValidate_RefusesAMisspelledConcurrency fails startup rather than
// quietly running adaptive on a value the operator did not mean.
func TestConfigValidate_RefusesAMisspelledConcurrency(t *testing.T) {
	cfg := &Config{Scripts: ScriptsConfig{Worker: ScriptsWorkerConfig{
		Config: scriptadmit.Config{Concurrency: "adpative"},
	}}}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scripts.worker.concurrency")
}
